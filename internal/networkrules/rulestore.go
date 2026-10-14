package networkrules

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/irbis-sh/zen-desktop/internal/ruletree"
	"github.com/irbis-sh/zen-desktop/internal/trimslice"
)

// ruleStore matches URLs against ad-block style patterns with associated data.
type ruleStore[T comparable] struct {
	tree *ruletree.Tree[T]
	// loadMu guards the generic/regexp appends: list loading runs on
	// parallel goroutines (internal/app/filter.go populateFilter), and only
	// the tree branch serializes itself via ruletree's insertMu. Serving
	// starts after loading completes, so reads need no lock.
	loadMu  sync.Mutex
	generic []T
	regexp  []regexpRule[T]

	// idx is the lazily built rarest-token reverse index over regexp (see
	// tokenindex.go). Insert invalidates it; the next Get rebuilds it.
	idx atomic.Pointer[reIdx]

	// gateOf derives a rule's $domain eligibility gate (nil = no gate).
	// The owner (networkrules.New) installs the concrete per-type function;
	// ruleStore itself stays generic over T. (2026-10-14)
	gateOf func(T) *domainGate

	// hostSplit enables the pure-hostname index (2026-10-14 方案A): patterns
	// of the exact shape ||host^ (no wildcard/anchor/separator/path inside)
	// are held in hostRules instead of the pattern tree, and matched per
	// request by host-segment suffix lookups (see GetHosts). Default OFF —
	// the flag is the rollback switch; production enablement is a separate
	// decision after set-equality verification.
	hostSplit bool
	// hostRules maps the pattern's host text to its rules in insertion
	// order. Writes serialize on hostMu (list loading runs on parallel
	// goroutines, and the live exception store inserts while serving);
	// reads take hostMu.RLock.
	hostMu    sync.RWMutex
	hostRules map[string][]T

	// resPool backs Get's result slices; the caller must putRes each result
	// exactly once (2026-10-06).
	resOnce sync.Once
	resPool sync.Pool
}

// pureHostPattern reports whether pattern is exactly ||host^ — a domain
// boundary, a host with no wildcard/anchor/extra separator/path segment, and
// a trailing separator token. For such patterns the tree semantics reduce
// exactly to "the request URL's host segment equals host or ends with
// "."+host" (see GetHosts for the visit-point equivalence argument), which
// the hostname index can evaluate without the trie.
func pureHostPattern(pattern string) (host string, ok bool) {
	if len(pattern) < 4 || pattern[0] != '|' || pattern[1] != '|' || pattern[len(pattern)-1] != '^' {
		return "", false
	}
	h := pattern[2 : len(pattern)-1]
	if h == "" {
		return "", false
	}
	for i := 0; i < len(h); i++ {
		switch h[i] {
		case '*', '^', '|', '/', '$':
			return "", false
		}
	}
	return h, true
}

// urlSepByte mirrors the tree's tokenSeparator semantics (tokenize.go:10-12):
// "^" matches any character except a letter, digit, or _-.%, and matches the
// end of the address.
func urlSepByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return false
	case c == '_', c == '-', c == '.', c == '%':
		return false
	}
	return true
}


// domainGate is one rule's $domain condition in the form the per-request
// eligibility pass can evaluate by host-suffix lookup (2026-10-14). It is
// built once per rule in buildIdx from the rule's MANDATORY (And) condition
// modifiers: $domain is appended to the And set (rule.ParseModifiers), so a
// rule whose gate cannot be satisfied by the request host can never match.
type domainGate struct {
	// lits are the modifier's non-negated literal entries
	// (rulemodifiers.DomainModifier.IndexableLiterals).
	lits []string
	// matchTarget mirrors DomainModifier.MatchTargetDomain: the condition
	// also tests the request's own hostname, not only the referrer's.
	matchTarget bool
}

// effHosts is the per-request effective-host pair that the $domain condition
// tests (rulemodifiers.DomainModifier.matchReqSide): the referrer hostname,
// falling back to the target hostname when no Referer header is present.
// usable=false means an unparseable Referer, for which matchReqSide returns
// false for every rule, so no gated rule is eligible.
type effHosts struct {
	ref, own string
	usable   bool
}

type regexpRule[T comparable] struct {
	regexp *regexp.Regexp
	// fast is an exact-equivalence matcher for supported pattern shapes
	// (see fastshape.go); nil means the pattern is outside the family and
	// the regexp is always used.
	fast  *fastShape
	value T
}

// maxPooledResEntries drops oversized result slices from the pool so a
// pathological URL cannot make the pool hoard huge backings.
const maxPooledResEntries = 8192

// putRes returns a result slice's backing to the store pool. Callers must
// invoke it exactly once per Get result and must not retain the backing.
func (s *ruleStore[T]) putRes(res []T) {
	if res == nil || cap(res) > maxPooledResEntries {
		return
	}
	s.resOnce.Do(func() {
		s.resPool.New = func() any { return new([]T) }
	})
	b := res[:0]
	s.resPool.Put(&b)
}

// match reports whether url matches this rule. hasNL tells whether url
// contains '\n': an unflagged "." in the pattern cannot match '\n' while
// fastShape's gaps are position-free, so fastShape is only used for
// '\n'-free URLs (see fastshape.go for the exactness argument).
func (r *regexpRule[T]) match(url string, hasNL bool) bool {
	if r.fast != nil && !hasNL {
		return r.fast.matchFast(url)
	}
	return r.regexp.MatchString(url)
}

func newRuleStore[T comparable]() *ruleStore[T] {
	return &ruleStore[T]{
		tree: ruletree.New[T](),
	}
}

func (s *ruleStore[T]) Insert(pattern string, v T) error {
	// The pattern is either generic (empty), regexp, or regular.
	switch {
	case pattern == "":
		s.loadMu.Lock()
		s.generic = append(s.generic, v)
		s.loadMu.Unlock()
	case len(pattern) > 1 && pattern[0] == '/' && pattern[len(pattern)-1] == '/':
		body := pattern[1 : len(pattern)-1]
		if body == "" {
			return fmt.Errorf("empty regexp rule")
		}
		re, err := regexp.Compile(body)
		if err != nil {
			return fmt.Errorf("compile regexp rule: %w", err)
		}
		// parseFastShape stays outside the lock: it is pure and potentially
		// costly, and parallel loaders should not serialize on it.
		fast := parseFastShape(body, re)
		s.loadMu.Lock()
		s.regexp = append(s.regexp, regexpRule[T]{
			regexp: re,
			fast:   fast,
			value:  v,
		})
		// A previously built index indexes the OLD regexp slice; drop it so
		// the next Get rebuilds against the new rules (2026-10-06).
		s.idx.Store(nil)
		s.loadMu.Unlock()
	default:
		// 方案A (2026-10-14): pure ||host^ patterns go to the hostname
		// index instead of the trie when hostSplit is on. The host text is
		// kept byte-exact — the tree stores the same bytes, so case and
		// IDN handling stay identical.
		if s.hostSplit {
			if host, ok := pureHostPattern(pattern); ok {
				s.hostMu.Lock()
				if s.hostRules == nil {
					s.hostRules = make(map[string][]T)
				}
				s.hostRules[host] = append(s.hostRules[host], v)
				s.hostMu.Unlock()
				return nil
			}
		}
		s.tree.Insert(pattern, v)
	}

	return nil
}

// Get returns the candidate values for url: generic rules, tree matches and
// matching regexp rules, in insertion order per bucket. The result slice's
// backing comes from a pool: the caller must call putRes exactly once and
// must not retain the backing (values must be copied out first).
//
// Get is the ungated form (no $domain pre-filter); it is equivalent to
// GetHosts(url, nil) and is kept for callers without a request context
// (tests, tools).
func (s *ruleStore[T]) Get(url string) []T {
	return s.GetHosts(url, nil)
}

// GetHosts is Get with the per-request $domain eligibility context
// (2026-10-14). eff==nil disables the pre-filter, which reproduces the
// pre-change behaviour exactly.
//
// The pre-filter is semantics-preserving for every caller that afterwards
// drops non-matching rules through the rule's condition modifiers
// (NetworkRules.ModifyReq's ShouldMatchReqNav filter and ModifyRes's
// ShouldMatchRes filter): $domain is a mandatory (And) condition, so a rule
// whose gate cannot be satisfied by eff is discarded either way. Candidate
// ORDER is unchanged — the loop still walks the regexp slice in insertion
// order and only skips rules that could not have survived.
func (s *ruleStore[T]) GetHosts(url string, eff *effHosts) []T {
	matches := s.tree.GetLP(url)

	s.resOnce.Do(func() {
		s.resPool.New = func() any { return new([]T) }
	})
	var res []T
	if b := s.resPool.Get(); b != nil {
		res = (*b.(*[]T))[:0]
	}
	if res == nil {
		res = make([]T, 0, len(s.generic)+len(matches))
	}
	res = append(res, s.generic...)
	res = append(res, matches...)
	res = s.appendHostMatches(res, url)

	if len(s.regexp) > 0 {
		// fastShape gaps are position-free while an unflagged "." cannot
		// match '\n', so '\n'-bearing URLs use the regexp for every rule
		// (one IndexByte per Get; see fastshape.go).
		hasNL := strings.IndexByte(url, '\n') >= 0
		idx := s.idx.Load()
		if idx == nil {
			n := s.buildIdx()
			if s.idx.CompareAndSwap(nil, n) {
				idx = n
			} else {
				idx = s.idx.Load()
			}
		}
		if len(s.regexp) <= idxMaxRules {
			// Rarest-token reverse index: a rule whose provably-required
			// literal cannot be present in url is skipped without running
			// its regexp (zero false negatives; see tokenindex.go).
			// The $domain eligibility pass runs first: a rule whose
			// mandatory $domain condition cannot hold for this request is
			// skipped before its regexp too (2026-10-14).
			var hit hitBits
			idx.markCandidates(url, &hit)
			var elig hitBits
			idx.markEligible(eff, &elig)
			for i := range s.regexp {
				if elig[i/64]&(1<<(i%64)) == 0 {
					continue
				}
				if toks := idx.tokensOf[i]; len(toks) > 0 {
					if hit[i/64]&(1<<(i%64)) == 0 {
						continue
					}
					present := false
					for _, tk := range toks {
						if strings.Contains(url, tk) {
							present = true
							break
						}
					}
					if !present {
						continue
					}
				}
				if s.regexp[i].match(url, hasNL) {
					res = append(res, s.regexp[i].value)
				}
			}
			return res
		}
		for _, rule := range s.regexp {
			if rule.match(url, hasNL) {
				res = append(res, rule.value)
			}
		}
	}
	return res
}

func (s *ruleStore[T]) Compact() {
	s.generic = trimslice.TrimSlice(s.generic)
	s.regexp = trimslice.TrimSlice(s.regexp)
	s.tree.Compact()
}

// pruneValues removes every value for which drop reports true from the whole
// store — tree leaves, hostname-index rules and (for completeness) the
// generic/regexp buckets' shared-slice membership is handled by their owners.
// It returns the number of removed values. Post-load pruning passes
// ($badfilter wiring, coverage dedup) must go through this method instead of
// tree.PruneValues directly, or they would silently miss 方案A-indexed rules
// (found by the 2026-10-14 equivalence probe: coverage-dedup pruned the tree
// copy but left the hostname-index copy alive, flipping which of a
// parent/child rule pair survived).
func (s *ruleStore[T]) pruneValues(drop func(T) bool) int {
	removed := s.tree.PruneValues(drop)
	if !s.hostSplit || len(s.hostRules) == 0 {
		return removed
	}
	s.hostMu.Lock()
	defer s.hostMu.Unlock()
	for host, vals := range s.hostRules {
		kept := vals[:0]
		for _, v := range vals {
			if drop(v) {
				removed++
				continue
			}
			kept = append(kept, v)
		}
		if len(kept) == 0 {
			delete(s.hostRules, host)
		} else {
			s.hostRules[host] = kept
		}
	}
	return removed
}

// appendHostMatches appends the pure-hostname rules (方案A) whose pattern
// matches url, in the exact order the tree traversal would have produced
// them for these rules.
//
// Equivalence to the tree (ruletree.go:394-413): domain-boundary patterns
// are only ever visited at (a) the host start and (b) positions right after
// a dot that lies inside the host span — the same two visit families, in
// the same order, are enumerated here. At a visit offset v the tree walks
// the pattern bytes against url[v:]; a pure ||host^ pattern can only match
// when host equals url[v:w] where w is the FIRST separator-or-end position
// at/after v (the pattern text holds no separator bytes, and the trailing
// "^" must consume exactly the byte at w), so each visit point yields at
// most one candidate host string — a single map lookup. The hostSpan bound
// and the hostStart/hostEnd formulas mirror GetLP verbatim.
func (s *ruleStore[T]) appendHostMatches(res []T, url string) []T {
	if !s.hostSplit || len(s.hostRules) == 0 {
		return res
	}
	schemeEnd := strings.Index(url, "://")
	hostStart := schemeEnd + 3
	if hostStart < 0 || hostStart >= len(url) {
		return res
	}
	hostEnd := strings.IndexAny(url[hostStart:], "/?")

	s.hostMu.RLock()
	defer s.hostMu.RUnlock()

	var hostHits []T
	addVisit := func(v int) {
		if v <= 0 || v >= len(url) {
			return
		}
		w := v
		for w < len(url) && !urlSepByte(url[w]) {
			w++
		}
		if w == v {
			return // separator at v: no host text can start here
		}
		for _, val := range s.hostRules[url[v:w]] {
			dup := false
			for _, prev := range hostHits {
				if prev == val {
					dup = true
					break
				}
			}
			if !dup { // same first-occurrence dedup as the tree traversal
				hostHits = append(hostHits, val)
			}
		}
	}
	// (a) host start — mirrors GetLP's `if i == hostStart` visit.
	addVisit(hostStart)
	// (b) after each dot inside the host span — mirrors GetLP's dot visit,
	// bounded by hostEnd exactly as there (IndexAny over "/?").
	for i := hostStart + 1; i < len(url); i++ {
		if hostEnd != -1 && i >= hostStart+hostEnd {
			break
		}
		if url[i] == '.' {
			addVisit(i + 1)
		}
	}
	if len(hostHits) == 0 {
		return res
	}
	return append(res, hostHits...)
}


// walkValues calls fn for every value stored in the store — the generic
// slice, the regexp slice and the whole pattern tree — in no particular
// order. Used once post-load by $badfilter application (networkrules.Compact,
// 2026-10-08 B6); the same Insert/Compact exclusion as Get applies.
func (s *ruleStore[T]) walkValues(fn func(T)) {
	for i := range s.generic {
		fn(s.generic[i])
	}
	for i := range s.regexp {
		fn(s.regexp[i].value)
	}
	if s.hostSplit {
		// 方案A: hostname-indexed rules are values like any other — $badfilter
		// disablement must reach them exactly as it reaches tree leaves.
		s.hostMu.RLock()
		for _, vals := range s.hostRules {
			for _, v := range vals {
				fn(v)
			}
		}
		s.hostMu.RUnlock()
	}
	s.tree.Walk(fn)
}
