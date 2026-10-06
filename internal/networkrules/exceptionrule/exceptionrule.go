package exceptionrule

import (
	"net/http"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

type ExceptionRule struct {
	rule.Rule
}

func (er *ExceptionRule) Cancels(r *rule.Rule) bool {
	if r.Important && !er.Important {
		return false
	}

	if er.Document && !r.Document && !r.All {
		return false
	}

	if len(er.AndConditionModifiers()) == 0 && len(er.OrConditionModifiers()) == 0 && len(er.ActionModifiers()) == 0 && len(er.QueryModifiers()) == 0 {
		return true
	}

	for _, exc := range er.AndConditionModifiers() {
		found := false
		for _, basic := range r.AndConditionModifiers() {
			if exc.Cancels(basic) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if or := er.OrConditionModifiers(); len(or) > 0 {
		found := false
		for _, exc := range or {
			for _, basic := range r.OrConditionModifiers() {
				if exc.Cancels(basic) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}

	for _, exc := range er.ActionModifiers() {
		found := false
		for _, basic := range r.ActionModifiers() {
			if exc.Cancels(basic) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	for _, exc := range er.QueryModifiers() {
		found := false
		for _, basic := range r.QueryModifiers() {
			if exc.Cancels(basic) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// ShouldMatchReq returns true if the rule should match the request.
func (er *ExceptionRule) ShouldMatchReq(req *http.Request) bool {
	return er.ModifiersMatchReq(req)
}

// ShouldMatchRes returns true if the rule should match the response.
func (er *ExceptionRule) ShouldMatchRes(res *http.Response) bool {
	return er.ModifiersMatchRes(res)
}
