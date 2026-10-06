package networkrules

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
}

func New() *NetworkRules {
	return &NetworkRules{
		primaryStore:   newRuleStore[*rule.Rule](),
		exceptionStore: newRuleStore[*exceptionrule.ExceptionRule](),
	}
}

func (nr *NetworkRules) ModifyReq(req *http.Request) (appliedRules []rule.Rule, shouldBlock bool, redirectURL string) {
	reqURL := renderURLWithoutPort(req.URL)

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

	exceptions := nr.exceptionStore.Get(reqURL)
	defer nr.exceptionStore.putRes(exceptions)
	exceptions = filterInPlace(exceptions, func(er *exceptionrule.ExceptionRule) bool {
		return er.ShouldMatchReq(req)
	})

	initialURL := req.URL.String()

	var query url.Values
	if req.URL.RawQuery != "" {
		query = req.URL.Query()
	}

	var queryModified bool
outer:
	for _, r := range primaryRules {
		for _, ex := range exceptions {
			if ex.Cancels(r) {
				continue outer
			}
		}
		if r.ShouldBlockReq(req) {
			return []rule.Rule{*r}, true, ""
		}

		modified := r.ModifyReq(req)
		if query != nil {
			if r.ModifyReqQuery(query) {
				queryModified = true
				modified = true
			}
		}

		if modified {
			appliedRules = append(appliedRules, *r)
		}
	}

	if queryModified {
		// Re-encoding the same query params may cause subtle normalization changes
		// (e.g. parameter reordering), so only do it if they were actually modified.
		req.URL.RawQuery = query.Encode()
	}

	finalURL := req.URL.String()
	if initialURL != finalURL {
		return appliedRules, false, finalURL
	}

	return appliedRules, false, ""
}

func (nr *NetworkRules) ModifyRes(req *http.Request, res *http.Response) ([]rule.Rule, error) {
	url := renderURLWithoutPort(req.URL)

	primaryRules := nr.primaryStore.Get(url)
	defer nr.primaryStore.putRes(primaryRules)
	primaryRules = filterInPlace(primaryRules, func(r *rule.Rule) bool {
		return r.ShouldMatchRes(res)
	})
	if len(primaryRules) == 0 {
		return nil, nil
	}

	exceptions := nr.exceptionStore.Get(url)
	defer nr.exceptionStore.putRes(exceptions)
	exceptions = filterInPlace(exceptions, func(er *exceptionrule.ExceptionRule) bool {
		return er.ShouldMatchRes(res)
	})

	var appliedRules []rule.Rule
outer:
	for _, r := range primaryRules {
		for _, ex := range exceptions {
			if ex.Cancels(r) {
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
