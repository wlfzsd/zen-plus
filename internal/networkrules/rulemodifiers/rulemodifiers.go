package rulemodifiers

import (
	"net/http"
	"strings"
	"sync"
)

// Modifier is a Modifier of a rule.
type Modifier interface {
	Parse(string) error
	Cancels(Modifier) bool
}

// ConditionModifier restrict when a rule applies based on request/response metadata.
type ConditionModifier interface {
	Modifier
	ShouldMatchReq(*http.Request) bool
	ShouldMatchRes(*http.Response) bool
}

// ActionModifier modifies requests and responses.
type ActionModifier interface {
	Modifier
	ModifyReq(*http.Request) bool
	ModifyRes(*http.Response) (bool, error)
}

// FrameScopedAction is an ActionModifier whose documented effect is limited
// to main frame and sub frame requests. AdGuard docs §$permissions note
// (adguard_create-own-filters.md line 2321): "$permissions rules only take
// effect for main frame and sub frame requests. This means they are applied
// when a page is loaded or when an iframe is loaded."
//
// networkrules.ModifyRes consults the flag so frame-scoped rules are skipped
// for every other response (images, XHR, 204s, ...) instead of applying an
// out-of-scope rewrite (2026-10-08, B9).
type FrameScopedAction interface {
	ActionModifier
	IsFrameScoped() bool
}

// QueryModifier modifies request query parameters.
// According to terminology, they are also "action modifiers", but are implemented separately for performance reasons.
//
// 2026-10-08 (B7): ModifyQuery receives the whole request instead of a
// decoded url.Values map. The request provides the method context needed by
// the AdGuard $removeparam method whitelist (doc 2634: GET/HEAD/OPTIONS and
// bodyless POST only), and lets the modifier rewrite req.URL.RawQuery
// directly in encoded form (doc 2650-2656), preserving the order and
// encoding of untouched pairs.
//
// 2026-10-08 (perf audit ①): ModifyQuery additionally receives a per-request
// QueryState holding the query split into "&" segments once per ModifyReq
// pass, instead of re-splitting req.URL.RawQuery for every candidate rule.
// qs is nil when the request has no query or its method is outside the
// $removeparam whitelist; implementations must return false in that case.
type QueryModifier interface {
	Modifier
	ModifyQuery(req *http.Request, qs *QueryState) bool
}

// maxPooledQuerySegs drops oversized segment slices from the pool so a
// pathological query cannot make the pool hoard huge backings.
const maxPooledQuerySegs = 4096

// QueryState carries the per-request query segments shared by all query
// modifiers of one ModifyReq pass (2026-10-08 perf audit ①). The raw query
// is split once and every rule filters the shared segment slice in place;
// the joined RawQuery is written back once after the rule loop.
//
// The visible semantics are unchanged relative to the per-rule
// split/join round-trip: segments never contain '&', so split∘join is an
// identity and each rule sees exactly the segments the previous per-rule
// rewrite would have produced. The one degenerate case — a filtering result
// whose join is "" (e.g. the only kept segment is the empty segment) — is
// normalized by the empty flag, reproducing the old behavior where the
// joined RawQuery became "" and every later rule's `RawQuery != ""` gate
// skipped it.
type QueryState struct {
	segs  []string
	dirty bool
	empty bool
}

var qsPool sync.Pool

// AcquireQueryState splits req.URL.RawQuery into segments once for the whole
// ModifyReq pass. It returns nil when the method is outside the $removeparam
// whitelist (doc 2634) — hoisting the per-request method check out of the
// per-rule loop preserves outcomes, since every ModifyQuery call would have
// returned false anyway. The caller must ReleaseQueryState the result.
func AcquireQueryState(req *http.Request) *QueryState {
	if req.URL.RawQuery == "" {
		return nil
	}
	switch strings.ToUpper(req.Method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		// Always eligible.
	case http.MethodPost:
		// Bodyless POST only (doc 2634 "sometimes POST").
		if req.Body != nil && req.ContentLength != 0 {
			return nil
		}
	default:
		return nil
	}
	qs, _ := qsPool.Get().(*QueryState)
	if qs == nil {
		qs = new(QueryState)
	}
	qs.reset(req.URL.RawQuery)
	return qs
}

// ReleaseQueryState returns the state (and its segment backing) to the pool.
func ReleaseQueryState(qs *QueryState) {
	if qs == nil || cap(qs.segs) > maxPooledQuerySegs {
		return
	}
	qs.segs = qs.segs[:0]
	qs.dirty = false
	qs.empty = false
	qsPool.Put(qs)
}

// reset splits raw on '&' into the reused backing. Produces exactly
// strings.Split(raw, "&") for every input (a trailing '&' yields a trailing
// empty segment; the empty string yields one empty segment).
func (qs *QueryState) reset(raw string) {
	qs.segs = qs.segs[:0]
	qs.dirty = false
	qs.empty = false
	for {
		i := strings.IndexByte(raw, '&')
		if i < 0 {
			qs.segs = append(qs.segs, raw)
			return
		}
		qs.segs = append(qs.segs, raw[:i])
		raw = raw[i+1:]
	}
}

// Segments returns the current segment slice for in-place filtering. The
// slice must be installed back with SetSegments when it was filtered.
func (qs *QueryState) Segments() []string { return qs.segs }

// SetSegments installs the filtered segment slice (it must share the backing
// of the slice previously read from Segments) and normalizes the degenerate
// "joins to empty" case into the empty flag.
func (qs *QueryState) SetSegments(kept []string) {
	qs.segs = kept
	qs.dirty = true
	if len(kept) == 0 || (len(kept) == 1 && kept[0] == "") {
		qs.empty = true
	}
}

// Empty reports whether the joined query would be empty: later rules must
// then behave exactly as the old per-rule `req.URL.RawQuery != ""` gate did.
func (qs *QueryState) Empty() bool { return qs.empty }

// SetEmpty clears the query (naked $removeparam): the joined form becomes ""
// and later rules see the old RawQuery=="" behavior.
func (qs *QueryState) SetEmpty() {
	qs.segs = qs.segs[:0]
	qs.dirty = true
	qs.empty = true
}

// Finalize returns the joined RawQuery and whether any rule modified the
// segments. Callers write the raw form back to req.URL.RawQuery only when
// changed is true — mirroring the old per-rule in-place rewrites.
func (qs *QueryState) Finalize() (string, bool) {
	if !qs.dirty {
		return "", false
	}
	return strings.Join(qs.segs, "&"), true
}
