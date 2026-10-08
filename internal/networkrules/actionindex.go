package networkrules

// Match-all action-rule index (2026-10-08, B10-A).
//
// Empty-pattern rules whose actions are entirely $cookie (or whose queries
// are entirely $removeparam) match every URL, so they live in ruleStore's
// generic bucket and are evaluated on every request/response. The enabled
// lists ship thousands of them (504 $cookie + 1763 $removeparam as of
// 2026-10-09), and the per-candidate evaluation dominated the hot path:
// +180 µs/request and +56 µs/response in the MatchAllBench baseline.
//
// Exact-name forms dominate (463/504 cookie, 1605/1763 removeparam), so at
// load time these rules are routed out of the store into this index: exact
// forms into name-keyed maps, the remaining bare/regexp/inverted forms into
// small linear fallbacks. The hot path collects the applicable candidates
// from request/response facts (cookie names, query parameter names) and
// prepends them to the store candidates; every evaluation function
// (ShouldMatch*, Modify*, exception pairing) is unchanged, and the collected
// candidates are seq-ordered and pointer-deduplicated to reproduce the
// original load-time evaluation order (设计文档 §1.6).
//
// Semantics-preservation notes:
//   - exact keys are the full matching predicate: a rule whose name is not
//     present cannot modify anything, so omitting it is outcome-neutral;
//   - order within the index reproduces load order (a bare $cookie and a
//     named $cookie=maxAge rewriting the same Set-Cookie must apply in load
//     order — last writer wins);
//   - index candidates enter the same exception pairing as store candidates,
//     so @@…$cookie / @@…$removeparam exceptions keep working;
//   - $badfilter disablement reaches indexed rules via the walk in
//     badfilter.go (pointer flags on the same *rule.Rule).

import (
	"net/http"
	"slices"
	"strings"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rulemodifiers"
)

// maxIndexedScratchEntries drops oversized candidate slices from the pools so
// a pathological candidate count cannot hoard huge backings (mirrors
// rulestore.maxPooledResEntries).
const maxIndexedScratchEntries = 8192

// indexedRule is one routed rule with its load order.
type indexedRule struct {
	seq uint32
	r   *rule.Rule
}

// matchAllActionIndex holds the routed empty-pattern $cookie/$removeparam
// rules. Writes happen only during loading (ParseRule, single-threaded);
// reads happen after Compact during serving, unlocked — the same
// "write at load, read at serve" shape as frameScopedRules.
type matchAllActionIndex struct {
	total       int
	nextSeq     uint32
	cookieExact map[string][]indexedRule // $cookie=NAME (byte-exact key)
	cookieAny   []indexedRule            // bare $cookie and /regexp/ forms
	rpExact     map[string][]indexedRule // $removeparam=NAME (raw encoded key)
	rpAny       []indexedRule            // naked, /regexp/, ~exact, ~regexp forms
}

func (idx *matchAllActionIndex) empty() bool { return idx == nil || idx.total == 0 }

// hasCookieRules reports whether any cookie rule is indexed (res-path gate).
func (idx *matchAllActionIndex) hasCookieRules() bool {
	return idx != nil && (len(idx.cookieExact) > 0 || len(idx.cookieAny) > 0)
}

// addRule routes one empty-pattern rule into the index buckets. The caller
// (NetworkRules.routeableMatchAllAction) has already verified the rule's
// actions are all *CookieModifier and/or its queries all
// *RemoveParamModifier.
func (idx *matchAllActionIndex) addRule(r *rule.Rule) {
	seq := idx.nextSeq
	idx.nextSeq++

	routed := false
	for _, m := range r.ActionModifiers() {
		cm, ok := m.(*rulemodifiers.CookieModifier)
		if !ok {
			continue
		}
		routed = true
		e := indexedRule{seq: seq, r: r}
		switch cm.MatchKind() {
		case rulemodifiers.CookieMatchExact:
			if idx.cookieExact == nil {
				idx.cookieExact = make(map[string][]indexedRule)
			}
			name := cm.ExactName()
			idx.cookieExact[name] = append(idx.cookieExact[name], e)
		default:
			idx.cookieAny = append(idx.cookieAny, e)
		}
	}
	for _, m := range r.QueryModifiers() {
		rm, ok := m.(*rulemodifiers.RemoveParamModifier)
		if !ok {
			continue
		}
		routed = true
		e := indexedRule{seq: seq, r: r}
		switch rm.MatchKind() {
		case rulemodifiers.RemoveParamMatchExact:
			if idx.rpExact == nil {
				idx.rpExact = make(map[string][]indexedRule)
			}
			name := rm.ExactParam()
			idx.rpExact[name] = append(idx.rpExact[name], e)
		default:
			idx.rpAny = append(idx.rpAny, e)
		}
	}
	if routed {
		idx.total++
	}
}

// walk visits every indexed rule ($badfilter application and audit). The
// same *rule.Rule pointers as evaluation receive the disablement flags, so
// badfilter semantics over indexed rules are identical to store rules.
func (idx *matchAllActionIndex) walk(fn func(*rule.Rule)) {
	if idx.empty() {
		return
	}
	for _, e := range idx.cookieAny {
		fn(e.r)
	}
	for _, entries := range idx.cookieExact {
		for _, e := range entries {
			fn(e.r)
		}
	}
	for _, e := range idx.rpAny {
		fn(e.r)
	}
	for _, entries := range idx.rpExact {
		for _, e := range entries {
			fn(e.r)
		}
	}
}

// collectCookieCandidates APPENDS the indexed rules that can match a
// request/response carrying the given cookie names: the any bucket (bare and
// regexp forms evaluate themselves) plus exact-name hits. The caller resets
// and dedups the working slice.
func (idx *matchAllActionIndex) collectCookieCandidates(names []string, cands []indexedRule) []indexedRule {
	for _, e := range idx.cookieAny {
		cands = append(cands, e)
	}
	for _, name := range names {
		for _, e := range idx.cookieExact[name] {
			cands = append(cands, e)
		}
	}
	return cands
}

// collectRemoveparamCandidates APPENDS the indexed rules that can match a
// request carrying the given parameter names (RemoveParamEligible already
// verified by the caller): the any bucket plus exact-name hits.
func (idx *matchAllActionIndex) collectRemoveparamCandidates(names []string, cands []indexedRule) []indexedRule {
	for _, e := range idx.rpAny {
		cands = append(cands, e)
	}
	for _, name := range names {
		for _, e := range idx.rpExact[name] {
			cands = append(cands, e)
		}
	}
	return cands
}

// dedupBySeq sorts by seq (load order) and drops later duplicates of the
// same rule pointer (a rule with both a $cookie and a $removeparam modifier
// is registered in two buckets but must be evaluated once).
func dedupBySeq(cands []indexedRule) []indexedRule {
	if len(cands) < 2 {
		return cands
	}
	slices.SortStableFunc(cands, func(a, b indexedRule) int {
		if a.seq < b.seq {
			return -1
		}
		if a.seq > b.seq {
			return 1
		}
		return 0
	})
	out := cands[:1]
	last := cands[0].r
	for _, e := range cands[1:] {
		if e.r == last {
			continue
		}
		out = append(out, e)
		last = e.r
	}
	return out
}

// routeableMatchAllAction reports whether the empty-pattern rule's effects
// are entirely within the index's scope: every action modifier is a
// $cookie and/or every query modifier is a $removeparam, with at least one
// of them present. Rules mixing other actions ($csp/$replace/…) stay in the
// store (conservative: their evaluation is unchanged).
func routeableMatchAllAction(r *rule.Rule) bool {
	actions := r.ActionModifiers()
	queries := r.QueryModifiers()
	if len(actions) == 0 && len(queries) == 0 {
		return false
	}
	for _, m := range actions {
		if _, ok := m.(*rulemodifiers.CookieModifier); !ok {
			return false
		}
	}
	for _, m := range queries {
		if _, ok := m.(*rulemodifiers.RemoveParamModifier); !ok {
			return false
		}
	}
	return true
}

// requestCookieNames extracts the cookie names of the request Cookie header,
// mirroring CookieModifier.ModifyReq's own parsing (cookie line → ";" split
// → name Cut → TrimSpace). The mirrored extraction keeps the index
// candidates a byte-for-byte superset of what ModifyReq can match.
func requestCookieNames(req *http.Request, names []string) []string {
	for _, line := range req.Header["Cookie"] {
		for _, p := range strings.Split(line, ";") {
			name, _, _ := strings.Cut(p, "=")
			names = append(names, strings.TrimSpace(name))
		}
	}
	return names
}

// responseCookieNames extracts the Set-Cookie cookie names of the response
// via the shared SetCookieName parser (single-parser principle with the
// evaluation path).
func responseCookieNames(res *http.Response, names []string) []string {
	for _, sc := range res.Header["Set-Cookie"] {
		if name, ok := rulemodifiers.SetCookieName(sc); ok {
			names = append(names, name)
		}
	}
	return names
}

// requestParamNames extracts the raw encoded parameter names of the request
// query, mirroring RemoveParamModifier.ModifyQuery's own segment parsing
// (removeparam.go: segments → name Cut). Empty segments are skipped: no
// exact rule can match them (Parse rejects an empty name), so omitting them
// is outcome-neutral.
func requestParamNames(req *http.Request, names []string) []string {
	for _, seg := range strings.Split(req.URL.RawQuery, "&") {
		if seg == "" {
			continue
		}
		name, _, _ := strings.Cut(seg, "=")
		names = append(names, name)
	}
	return names
}

// indexScratch is the pooled working set of one indexed prepend pass: the
// extracted names, the collected candidates and the merged candidate list.
// Pooling the whole struct keeps the hot path allocation-free in steady
// state (2026-10-08, B10-A; an unpooled intermediate measured +27 allocs and
// +13 KB/op in the MatchAllBench).
type indexScratch struct {
	names  []string
	cands  []indexedRule
	merged []*rule.Rule
}

// prependIndexedReq prepends the request-path index candidates (cookie +
// removeparam rules applicable to req) ahead of the store candidates. It
// returns store untouched when the index is empty or nothing is applicable;
// otherwise it builds the merged candidate slice in a pooled scratch (the
// caller must putReqScratch it after the pass).
func (nr *NetworkRules) prependIndexedReq(store []*rule.Rule, req *http.Request) (merged []*rule.Rule, scratch *indexScratch) {
	idx := &nr.actionIdx
	if idx.empty() || !nr.actionIdxOn {
		return store, nil
	}

	hasCookies := len(req.Header["Cookie"]) > 0
	rpEligible := rulemodifiers.RemoveParamEligible(req)
	if !hasCookies && !rpEligible {
		return store, nil
	}

	scratch = nr.getReqScratch()
	s := scratch
	s.cands = s.cands[:0]
	if hasCookies && idx.hasCookieRules() {
		s.names = requestCookieNames(req, s.names[:0])
		s.cands = idx.collectCookieCandidates(s.names, s.cands)
	}
	if rpEligible && (len(idx.rpExact) > 0 || len(idx.rpAny) > 0) {
		s.names = requestParamNames(req, s.names[:0])
		s.cands = idx.collectRemoveparamCandidates(s.names, s.cands)
	}
	s.cands = dedupBySeq(s.cands)
	if len(s.cands) == 0 {
		nr.putReqScratch(scratch)
		return store, nil
	}

	s.merged = s.merged[:0]
	for _, c := range s.cands {
		s.merged = append(s.merged, c.r)
	}
	s.merged = append(s.merged, store...)
	return s.merged, scratch
}

func (nr *NetworkRules) getReqScratch() *indexScratch {
	b, _ := nr.candScratchReq.Get().(*indexScratch)
	if b == nil {
		b = new(indexScratch)
	}
	return b
}

// putReqScratch returns the request candidate scratch to its pool.
func (nr *NetworkRules) putReqScratch(scratch *indexScratch) {
	if scratch == nil || cap(scratch.merged) > maxIndexedScratchEntries {
		return
	}
	scratch.names = scratch.names[:0]
	scratch.cands = scratch.cands[:0]
	scratch.merged = scratch.merged[:0]
	nr.candScratchReq.Put(scratch)
}

// prependIndexedRes prepends the response-path index candidates ($cookie
// rules applicable to the response's Set-Cookie headers) ahead of the store
// candidates. Returns store untouched when the index is empty or the
// response carries no Set-Cookie headers.
func (nr *NetworkRules) prependIndexedRes(store []*rule.Rule, res *http.Response) (merged []*rule.Rule, scratch *indexScratch) {
	idx := &nr.actionIdx
	if idx.empty() || !nr.actionIdxOn || !idx.hasCookieRules() {
		return store, nil
	}
	scs := res.Header["Set-Cookie"]
	if len(scs) == 0 {
		return store, nil
	}

	scratch = nr.getResScratch()
	s := scratch
	s.names = responseCookieNames(res, s.names[:0])
	if len(s.names) == 0 {
		nr.putResScratch(scratch)
		return store, nil
	}

	s.cands = append(s.cands[:0], idx.cookieAny...)
	for _, name := range s.names {
		s.cands = append(s.cands, idx.cookieExact[name]...)
	}
	s.cands = dedupBySeq(s.cands)
	if len(s.cands) == 0 {
		nr.putResScratch(scratch)
		return store, nil
	}

	s.merged = s.merged[:0]
	for _, c := range s.cands {
		s.merged = append(s.merged, c.r)
	}
	s.merged = append(s.merged, store...)
	return s.merged, scratch
}

func (nr *NetworkRules) getResScratch() *indexScratch {
	b, _ := nr.candScratchRes.Get().(*indexScratch)
	if b == nil {
		b = new(indexScratch)
	}
	return b
}

// putResScratch returns the response candidate scratch to its pool.
func (nr *NetworkRules) putResScratch(scratch *indexScratch) {
	if scratch == nil || cap(scratch.merged) > maxIndexedScratchEntries {
		return
	}
	scratch.names = scratch.names[:0]
	scratch.cands = scratch.cands[:0]
	scratch.merged = scratch.merged[:0]
	nr.candScratchRes.Put(scratch)
}
