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
	// generic is rules with an empty pattern, which match for every URL.
	generic []T
	regexp  []regexpRule[T]

	// idx is the lazily built rarest-token reverse index over regexp (see
	// tokenindex.go). Insert invalidates it; the next Get rebuilds it.
	idx atomic.Pointer[reIdx]

	// resPool backs Get's result slices; the caller must putRes each result
	// exactly once (2026-10-06).
	resOnce sync.Once
	resPool sync.Pool
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
		s.generic = append(s.generic, v)
	case len(pattern) > 1 && pattern[0] == '/' && pattern[len(pattern)-1] == '/':
		body := pattern[1 : len(pattern)-1]
		if body == "" {
			return fmt.Errorf("empty regexp rule")
		}
		re, err := regexp.Compile(body)
		if err != nil {
			return fmt.Errorf("compile regexp rule: %w", err)
		}
		s.regexp = append(s.regexp, regexpRule[T]{
			regexp: re,
			fast:   parseFastShape(body, re),
			value:  v,
		})
		// A previously built index indexes the OLD regexp slice; drop it so
		// the next Get rebuilds against the new rules (2026-10-06).
		s.idx.Store(nil)
	default:
		s.tree.Insert(pattern, v)
	}

	return nil
}

// Get returns the candidate values for url: generic rules, tree matches and
// matching regexp rules, in insertion order per bucket. The result slice's
// backing comes from a pool: the caller must call putRes exactly once and
// must not retain the backing (values must be copied out first).
func (s *ruleStore[T]) Get(url string) []T {
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
			var hit hitBits
			idx.markCandidates(url, &hit)
			for i := range s.regexp {
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
	s.tree.Walk(fn)
}
