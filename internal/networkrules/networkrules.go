package networkrules

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/irbis-sh/zen-desktop/internal/exemption"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rulemodifiers"
)

type NetworkRules struct {
	primaryStore   *ruleStore[*rule.Rule]
	exceptionStore *ruleStore[*exceptionrule.ExceptionRule]

	// pageScopedExceptions counts parsed exceptions whose effects reach
	// beyond the request whose URL they match ($document/$urlblock/
	// $genericblock/$content). While it is zero, ModifyReq/ModifyRes skip
	// the Referer (page) lookup entirely (2026-10-08 B2).
	pageScopedExceptions atomic.Int64

	// badfilters holds $badfilter rules collected at parse time (see
	// badfilter.go, 2026-10-08 B6). They are applied once in Compact, after
	// every filter list was fully loaded, and never inserted into a store.
	badfilters []badfilterRule
}

func New() *NetworkRules {
	return &NetworkRules{
		primaryStore:   newRuleStore[*rule.Rule](),
		exceptionStore: newRuleStore[*exceptionrule.ExceptionRule](),
	}
}

// maxMatchURLLen (2026-10-08 P6): AdGuard truncates URLs to 4096 characters
// before matching — docs "Basic rules → Basic rule syntax", pattern bullet:
// "AdGuard truncates URLs to a length of 4096 characters in order to speed
// up matching and avoid issues with ridiculously long URLs"
// (adguard.com/kb/general/ad-filtering/create-own-filters/). Beyond
// alignment with AdGuard, this bounds ruletree traversal input: a 68 KB URL
// previously drove wildcard position-enumeration into minutes of CPU and a
// ~180 MB accumulator per store (leak_capture heap_2.txt / goroutine_2.txt).
func matchURL(u *url.URL) string {
	s := renderURLWithoutPort(u)
	if len(s) > maxMatchURLLen {
		s = s[:maxMatchURLLen]
	}
	return s
}

// maxMatchURLLen is the matching-URL cap (AdGuard semantics, see matchURL).
const maxMatchURLLen = 4096

func (nr *NetworkRules) ModifyReq(req *http.Request) (appliedRules []rule.Rule, shouldBlock bool, redirectURL string) {
	reqURL := matchURL(req.URL)

	// The user-navigation guard is a per-request property (upstream #257
	// semantics); hoist the two header reads out of the per-rule evaluation.
	isUserNav := req.Header.Get("Sec-Fetch-User") == "?1" && req.Header.Get("Sec-Fetch-Dest") == "document"

	primaryRules := nr.primaryStore.Get(reqURL)
	defer nr.primaryStore.putRes(primaryRules)
	primaryRules = filterInPlace(primaryRules, func(r *rule.Rule) bool {
		return r.ShouldMatchReqNav(req, isUserNav)
	})
	if len(primaryRules) == 0 {
		return nil, false, ""
	}

	// Exceptions matching the request URL are judged per request (2026-10-08
	// B2/H3): URL pattern hit plus the exception's own condition modifiers.
	exceptions := nr.exceptionStore.Get(reqURL)
	defer nr.exceptionStore.putRes(exceptions)
	exceptions = filterInPlace(exceptions, func(er *exceptionrule.ExceptionRule) bool {
		return er.ShouldMatchReq(req)
	})

	// 2026-10-08 (perf audit ②): classify the matched exceptions once
	// instead of re-deriving the same answers per rule×exception pair.
	//   - Exceptions that can never cancel a network rule on the request
	//     path (the asset-exemption scoped family: $generichide & co) are
	//     dropped from the sweep — Cancels(r, req, false) is false for every
	//     rule, so dropping them is semantics-preserving.
	//   - "Cancels-everything" exceptions ($all/$document/bare/pure
	//     condition-only) are summarized into three flags so the rule loop
	//     can skip wholesale, exactly reproducing which rules their
	//     Cancels would have cancelled (see the per-rule predicate below).
	//     The two remaining per-rule vetoes are $important rules (docs
	//     670-679) and — for bare whitelists without $important — rules
	//     carrying query modifiers (B7 carve-out, doc 2737).
	var (
		// wImp: a $important wholesale exception matched — cancels every
		// candidate rule outright.
		wImp bool
		// wNorm: a non-$important wholesale exception matched — cancels
		// every candidate rule except $important ones.
		wNorm bool
		// wNormAllBare: every non-$important wholesale exception is bare,
		// so rules carrying query modifiers survive them (at least one
		// non-bare wholesale exception would cancel those too).
		wNormAllBare = true
	)
	keep := exceptions[:0]
	for _, er := range exceptions {
		if !er.CancelsNothingOnReq() {
			keep = append(keep, er)
		}
		if er.CancelsEverythingOnReq() {
			if er.Important {
				wImp = true
			} else {
				wNorm = true
				if !er.IsBare() {
					wNormAllBare = false
				}
			}
		}
	}
	exceptions = keep

	// Exceptions matching the page (Referer) URL carry page-scoped effects
	// only: $urlblock/$genericblock and the urlblock component of $document
	// (docs 1292-1298, 919-923). zen is a system proxy without a frame tree,
	// so the page is approximated by the Referer.
	pageExceptions := nr.pageExceptions(req, (*exceptionrule.ExceptionRule).HasPageScope)
	defer nr.exceptionStore.putRes(pageExceptions)

	initialURL := req.URL.String()

	// 2026-10-08 (perf audit ①): split the query once per request instead
	// of once per candidate rule. qs is nil when there is no query or the
	// method is outside the $removeparam whitelist — every query modifier
	// would have returned false for those, so skipping the calls preserves
	// outcomes. Released on every return path below.
	var qs *rulemodifiers.QueryState
	if req.URL.RawQuery != "" {
		qs = rulemodifiers.AcquireQueryState(req)
	}

outer:
	for _, r := range primaryRules {
		// Wholesale cancellation (perf audit ②): mirrors "some matched
		// exception's Cancels(r, req, false) is true for this rule" without
		// the per-pair call. wImp covers every rule; a non-$important
		// wholesale exception spares only $important rules, and when every
		// such exception is bare, rules carrying query modifiers
		// additionally survive them (B7 carve-out, doc 2737) — a single
		// non-bare one ($all/$document/condition-only) cancels those too.
		if wImp || (wNorm && !r.Important && (!wNormAllBare || len(r.QueryModifiers()) == 0)) {
			continue outer
		}
		for _, ex := range exceptions {
			// 2026-10-08 (perf audit ②): an action/query-scoped exception
			// reaches only cancelsActionQueryStructural, which cannot cancel
			// a rule carrying no modifiers of the same kind — skip the pair
			// without the call. Exact: every earlier Cancels branch either
			// already returned or falls through to the structural check.
			if (len(ex.ActionModifiers()) > 0 && len(r.ActionModifiers()) == 0) ||
				(len(ex.QueryModifiers()) > 0 && len(r.QueryModifiers()) == 0) {
				continue
			}
			if ex.Cancels(r, req, false) {
				continue outer
			}
		}
		for _, ex := range pageExceptions {
			if ex.Cancels(r, req, true) {
				continue outer
			}
		}
		if r.ShouldBlockReq(req) {
			if qs != nil {
				rulemodifiers.ReleaseQueryState(qs)
			}
			return []rule.Rule{*r}, true, ""
		}

		modified := r.ModifyReq(req)
		if qs != nil {
			if r.ModifyReqQuery(req, qs) {
				modified = true
			}
		}

		if modified {
			appliedRules = append(appliedRules, *r)
		}
	}

	if qs != nil {
		// 2026-10-08 (perf audit ①): write the joined segments back once.
		// B7's in-place encoded-form rewrite semantics (doc 2650-2656) are
		// unchanged — each rule observed exactly the segments the previous
		// per-rule split/join round-trip would have produced.
		if raw, changed := qs.Finalize(); changed {
			req.URL.RawQuery = raw
		}
		rulemodifiers.ReleaseQueryState(qs)
	}

	// 2026-10-08 (B7): query modifiers rewrite req.URL.RawQuery in place in
	// encoded form (AdGuard doc 2650-2656), so the old url.Values
	// re-serialization step — req.URL.RawQuery = query.Encode(), which
	// re-sorted and re-escaped the remaining parameters — is no longer
	// needed here.

	finalURL := req.URL.String()
	if initialURL != finalURL {
		return appliedRules, false, finalURL
	}

	return appliedRules, false, ""
}

func (nr *NetworkRules) ModifyRes(req *http.Request, res *http.Response) ([]rule.Rule, error) {
	url := matchURL(req.URL)

	primaryRules := nr.primaryStore.Get(url)
	defer nr.primaryStore.putRes(primaryRules)
	primaryRules = filterInPlace(primaryRules, func(r *rule.Rule) bool {
		return r.ShouldMatchRes(res)
	})
	if len(primaryRules) == 0 {
		return nil, nil
	}

	// Exceptions are judged per request on the response path too (2026-10-08
	// B2/H3): URL pattern on the request URL, condition modifiers against
	// the request (ModifyReq/ModifyRes each pass their own req).
	exceptions := nr.exceptionStore.Get(url)
	defer nr.exceptionStore.putRes(exceptions)
	exceptions = filterInPlace(exceptions, func(er *exceptionrule.ExceptionRule) bool {
		return er.ShouldMatchReq(req)
	})

	// Page (Referer) surface for $content and the content component of
	// $document (docs 1132-1134: "on the pages that match the rule").
	pageExceptions := nr.pageExceptions(req, (*exceptionrule.ExceptionRule).HasResPageScope)
	defer nr.exceptionStore.putRes(pageExceptions)

	var appliedRules []rule.Rule
outer:
	for _, r := range primaryRules {
		for _, ex := range exceptions {
			if ex.CancelsRes(r, false) {
				continue outer
			}
		}
		for _, ex := range pageExceptions {
			if ex.CancelsRes(r, true) {
				continue outer
			}
		}

		m, err := r.ModifyRes(res)
		if err != nil {
			return nil, fmt.Errorf("apply %q: %v", r.RawRule, err)
		}
		if m {
			appliedRules = append(appliedRules, *r)
		}
	}

	return appliedRules, nil
}

// ActiveExceptions resolves the exception-modifier exemptions that apply to
// a document request: the $elemhide/$generichide/$specifichide/$jsinject
// family plus the cosmetic/js components of $document (docs 919-923). The
// caller passes the result to asset.Engine.Inject before HTML injection.
//
// Per C5 (docs line 1119) exception modifiers only take effect for frame
// document loads; subresource requests never trigger exemptions. Bare user
// whitelists (@@||x^ without modifiers) are deliberately NOT included —
// their behavior is frozen (B2 red line) — and @@$all is left to batch B1.
func (nr *NetworkRules) ActiveExceptions(req *http.Request) exemption.Exemption {
	if !exemption.IsDocumentDest(req.Header.Get("Sec-Fetch-Dest")) {
		return exemption.Exemption{}
	}

	reqURL := matchURL(req.URL)
	exceptions := nr.exceptionStore.Get(reqURL)
	defer nr.exceptionStore.putRes(exceptions)

	var ex exemption.Exemption
	for _, er := range exceptions {
		if er.IsBare() || er.All {
			continue
		}
		if !er.ShouldMatchReq(req) {
			continue
		}
		if er.Elemhide || er.Document {
			ex.Elemhide = true
		}
		if er.Jsinject || er.Document {
			ex.Jsinject = true
		}
		if er.Generichide {
			ex.Generichide = true
		}
		if er.Specifichide {
			ex.Specifichide = true
		}
	}
	return ex
}

// refererRenderCache maps a raw Referer header value to the rendered URL
// (renderURLWithoutPort of its parse) used for page-surface exception
// lookups; "" memoizes "no usable page URL". The mapping is a pure function
// of the string, so entries never go stale (2026-10-08 B2).
var (
	refererRenderCacheMu    sync.RWMutex
	refererRenderCache      = make(map[string]string)
	refererRenderCacheLimit = 4096
)

func refererRendered(referer string) string {
	refererRenderCacheMu.RLock()
	rendered, hit := refererRenderCache[referer]
	refererRenderCacheMu.RUnlock()
	if hit {
		return rendered
	}

	u, err := url.Parse(referer)
	if err == nil && u.Hostname() != "" {
		rendered = matchURL(u)
	}
	refererRenderCacheMu.Lock()
	if len(refererRenderCache) >= refererRenderCacheLimit {
		refererRenderCache = make(map[string]string)
	}
	refererRenderCache[referer] = rendered
	refererRenderCacheMu.Unlock()
	return rendered
}

// pageExceptions returns exceptions whose URL pattern matches the request's
// page (Referer) and that carry the page-scoped effects selected by scope.
// Returns nil when there is nothing to look up. The caller must putRes a
// non-nil result exactly once (putRes accepts nil, so an unconditional
// defer is fine).
func (nr *NetworkRules) pageExceptions(req *http.Request, scope func(*exceptionrule.ExceptionRule) bool) []*exceptionrule.ExceptionRule {
	if nr.pageScopedExceptions.Load() == 0 {
		return nil
	}
	referer := req.Header.Get("Referer")
	if referer == "" {
		return nil
	}
	pageURL := refererRendered(referer)
	if pageURL == "" {
		return nil
	}
	excs := nr.exceptionStore.Get(pageURL)
	return filterInPlace(excs, func(er *exceptionrule.ExceptionRule) bool {
		return scope(er) && er.ShouldMatchReq(req)
	})
}

func (nr *NetworkRules) Compact() {
	// $badfilter disablement must run after every filter list was loaded
	// and before the stores are used for matching (2026-10-08 B6).
	nr.applyBadfilters()
	nr.primaryStore.Compact()
	nr.exceptionStore.Compact()
}

// filterInPlace compacts arr in place, keeping only the elements that
// satisfy the predicate, and returns the shortened slice (same backing).
// The caller must not retain elements beyond the returned length. Used with
// ruleStore.Get results whose backing is pool-recycled via putRes (2026-10-06).
func filterInPlace[T any](arr []T, predicate func(T) bool) []T {
	out := arr[:0]
	for _, el := range arr {
		if predicate(el) {
			out = append(out, el)
		}
	}
	return out
}

func renderURLWithoutPort(u *url.URL) string {
	stripped := url.URL{
		Scheme:   u.Scheme,
		Host:     u.Hostname(),
		Path:     u.Path,
		RawQuery: u.RawQuery,
	}

	return stripped.String()
}
