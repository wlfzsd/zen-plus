package nrprobe

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

type NetworkRules struct {
	primaryStore   *ruleStore[*rule.Rule]
	exceptionStore *ruleStore[*exceptionrule.ExceptionRule]

	// PROBE flags (tmp_enginebench only, for A/B benchmarking only):
	//   UseTokenIndex   — probe 1: rarest-token reverse index pruning
	//   LowAlloc        — probe 2: tree.GetLP + pooled result slices +
	//                     in-place filter compaction (caller releases via
	//                     putRes defers)
	//   DisableFastShape— probe 3: ignore fastshape matchers (all regexp
	//                     rules go through the regexp engine)
	// Results are identical for every combination; equivalence is guarded
	// by the probe tests.
	UseTokenIndex    bool
	LowAlloc         bool
	DisableFastShape bool
}

func New() *NetworkRules {
	return &NetworkRules{
		primaryStore:   newRuleStore[*rule.Rule](),
		exceptionStore: newRuleStore[*exceptionrule.ExceptionRule](),
	}
}

func (nr *NetworkRules) setProbeFlags(useIdx, lowAlloc, disableFast bool) {
	nr.UseTokenIndex = useIdx
	nr.LowAlloc = lowAlloc
	nr.DisableFastShape = disableFast
	nr.primaryStore.disableFast = disableFast
	nr.exceptionStore.disableFast = disableFast
}

// getDispatch is the A/B switch shared by ModifyReq/ModifyRes.
func (nr *NetworkRules) getDispatch(url string) []*rule.Rule {
	switch {
	case nr.UseTokenIndex && nr.LowAlloc:
		return nr.primaryStore.GetIdxLP(url)
	case nr.UseTokenIndex:
		return nr.primaryStore.GetIdx(url)
	case nr.LowAlloc:
		return nr.primaryStore.GetLP(url)
	default:
		return nr.primaryStore.Get(url)
	}
}

func (nr *NetworkRules) getDispatchExceptions(url string) []*exceptionrule.ExceptionRule {
	switch {
	case nr.UseTokenIndex && nr.LowAlloc:
		return nr.exceptionStore.GetIdxLP(url)
	case nr.UseTokenIndex:
		return nr.exceptionStore.GetIdx(url)
	case nr.LowAlloc:
		return nr.exceptionStore.GetLP(url)
	default:
		return nr.exceptionStore.Get(url)
	}
}

func (nr *NetworkRules) ModifyReq(req *http.Request) (appliedRules []rule.Rule, shouldBlock bool, redirectURL string) {
	reqURL := renderURLWithoutPort(req.URL)

	// The user-navigation guard is a per-request property (upstream #257
	// semantics); hoisted the two header reads out of the per-rule evaluation.
	isUserNav := req.Header.Get("Sec-Fetch-User") == "?1" && req.Header.Get("Sec-Fetch-Dest") == "document"

	primaryRules := nr.getDispatch(reqURL)
	if nr.LowAlloc {
		defer nr.primaryStore.putRes(primaryRules)
		primaryRules = filterInPlace(primaryRules, func(r *rule.Rule) bool {
			return r.ShouldMatchReqNav(req, isUserNav)
		})
	} else {
		primaryRules = filter(primaryRules, func(r *rule.Rule) bool {
			return r.ShouldMatchReqNav(req, isUserNav)
		})
	}
	if len(primaryRules) == 0 {
		return nil, false, ""
	}

	exceptions := nr.getDispatchExceptions(reqURL)
	if nr.LowAlloc {
		defer nr.exceptionStore.putRes(exceptions)
		exceptions = filterInPlace(exceptions, func(er *exceptionrule.ExceptionRule) bool {
			return er.ShouldMatchReq(req)
		})
	} else {
		exceptions = filter(exceptions, func(er *exceptionrule.ExceptionRule) bool {
			return er.ShouldMatchReq(req)
		})
	}


	initialURL := req.URL.String()

outer:
	for _, r := range primaryRules {
		for _, ex := range exceptions {
			// 2026-10-08 (port): adapted to the B2 Cancels signature — the
			// probe's exceptions are request-matched (ShouldMatchReq above),
			// so pageSurface is false. Call-shape-only change.
			if ex.Cancels(r, req, false) {
				continue outer
			}
		}
		if r.ShouldBlockReq(req) {
			return []rule.Rule{*r}, true, ""
		}

		modified := r.ModifyReq(req)
		// 2026-10-08 (port): adapted to the B7 ModifyReqQuery signature —
		// the whole request is passed and query modifiers rewrite
		// req.URL.RawQuery in place; the old decoded url.Values round-trip
		// (parse once, Encode back) is gone with it.
		if req.URL.RawQuery != "" {
			if r.ModifyReqQuery(req) {
				modified = true
			}
		}

		if modified {
			appliedRules = append(appliedRules, *r)
		}
	}

	finalURL := req.URL.String()
	if initialURL != finalURL {
		return appliedRules, false, finalURL
	}

	return appliedRules, false, ""
}

func (nr *NetworkRules) ModifyRes(req *http.Request, res *http.Response) ([]rule.Rule, error) {
	url := renderURLWithoutPort(req.URL)

	primaryRules := nr.getDispatch(url)
	if nr.LowAlloc {
		defer nr.primaryStore.putRes(primaryRules)
		primaryRules = filterInPlace(primaryRules, func(r *rule.Rule) bool {
			return r.ShouldMatchRes(res)
		})
	} else {
		primaryRules = filter(primaryRules, func(r *rule.Rule) bool {
			return r.ShouldMatchRes(res)
		})
	}
	if len(primaryRules) == 0 {
		return nil, nil
	}

	exceptions := nr.getDispatchExceptions(url)
	if nr.LowAlloc {
		defer nr.exceptionStore.putRes(exceptions)
		exceptions = filterInPlace(exceptions, func(er *exceptionrule.ExceptionRule) bool {
			return er.ShouldMatchRes(res)
		})
	} else {
		exceptions = filter(exceptions, func(er *exceptionrule.ExceptionRule) bool {
			return er.ShouldMatchRes(res)
		})
	}

	var appliedRules []rule.Rule
outer:
	for _, r := range primaryRules {
		for _, ex := range exceptions {
			// 2026-10-08 (port): adapted to the B2 Cancels signature
			// (call-shape-only change; the probe's exception filtering by
			// ShouldMatchRes is kept as before).
			if ex.Cancels(r, req, false) {
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

func (nr *NetworkRules) Compact() {
	nr.primaryStore.Compact()
	nr.exceptionStore.Compact()
}

// filterInPlace compacts arr to the elements satisfying the predicate and
// returns the resliced result (probe 2): identical output to filter, without
// the fresh allocation. Safe because arr is freshly returned by the store
// and the caller owns it exclusively.
func filterInPlace[T any](arr []T, predicate func(T) bool) []T {
	n := 0
	for _, el := range arr {
		if predicate(el) {
			arr[n] = el
			n++
		}
	}
	return arr[:n]
}

// filter returns a new slice containing only the elements of arr
// that satisfy the predicate.
func filter[T any](arr []T, predicate func(T) bool) []T {
	var res []T
	for _, el := range arr {
		if predicate(el) {
			res = append(res, el)
		}
	}
	return res
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
