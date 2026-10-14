package exceptionrule

import (
	"net/http"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

// testReq builds a minimal request for Cancels' per-request judgment
// (2026-10-08 B2 signature: Cancels(rule, req, pageSurface)). URL matching
// happens before Cancels, so the request URL only feeds condition modifiers,
// which these test rules do not carry.
func testReq(rawURL string) *http.Request {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		panic(err)
	}
	return req
}

func TestExceptionRule(t *testing.T) {
	t.Parallel()

	t.Run("'@@||page' should cancel '||page$document'", func(t *testing.T) {
		t.Parallel()

		filterName := "test"

		er := &ExceptionRule{
			Rule: rule.Rule{
				RawRule:    "||example.com",
				FilterName: &filterName,
			},
		}
		r := &rule.Rule{
			RawRule:    "||example.com$document",
			FilterName: &filterName,
		}
		r.ParseModifiers([]string{"document"})

		want := true
		if got := er.Cancels(r, testReq("https://example.com/page"), false); got != want {
			t.Errorf("'%s'.Cancels('%s') = %t, want %t", er.RawRule, r.RawRule, got, want)
		}
	})

	t.Run("'@@||page$document' should cancel '||page$document'", func(t *testing.T) {
		t.Parallel()

		filterName := "test"

		er := &ExceptionRule{
			Rule: rule.Rule{
				RawRule:    "||example.com$document",
				FilterName: &filterName,
			},
		}
		r := &rule.Rule{
			RawRule:    "||example.com$document",
			FilterName: &filterName,
		}
		r.ParseModifiers([]string{"document"})
		er.ParseModifiers([]string{"document"})

		want := true
		if got := er.Cancels(r, testReq("https://example.com/page"), false); got != want {
			t.Errorf("'%s'.Cancels('%s') = %t, want %t", er.RawRule, r.RawRule, got, want)
		}
	})

	// 2026-10-08 (B2): @@$document cancels everything on the matched request
	// (AdGuard docs 919-923; sandbox B2M5/"pure @@$document still full-stop"),
	// so it DOES cancel a plain block rule. The pre-B2 assertion expected the
	// old structural matching, where the exception only cancelled rules
	// carrying the same modifier.
	t.Run("'@@||page$document' should cancel '||page'", func(t *testing.T) {
		t.Parallel()

		filterName := "test"

		er := &ExceptionRule{
			Rule: rule.Rule{
				RawRule:    "||example.com^$document",
				FilterName: &filterName,
			},
		}
		r := &rule.Rule{
			RawRule:    "||example.com",
			FilterName: &filterName,
		}
		er.ParseModifiers([]string{"document"})

		want := true
		if got := er.Cancels(r, testReq("https://example.com/page"), false); got != want {
			t.Errorf("'%s'.Cancels('%s') = %t, want %t", er.RawRule, r.RawRule, got, want)
		}
	})

	t.Run("'@@||page$important' should cancel '||page$important'", func(t *testing.T) {
		t.Parallel()

		filterName := "test"

		er := &ExceptionRule{
			Rule: rule.Rule{
				RawRule:    "@@||example.com$important",
				FilterName: &filterName,
			},
		}
		r := &rule.Rule{
			RawRule:    "||example.com$important",
			FilterName: &filterName,
		}
		r.ParseModifiers([]string{"important"})
		er.ParseModifiers([]string{"important"})

		want := true
		if got := er.Cancels(r, testReq("https://example.com/page"), false); got != want {
			t.Errorf("'%s'.Cancels('%s') = %t, want %t", er.RawRule, r.RawRule, got, want)
		}
	})

	t.Run("'@@||page' should not cancel '||page$important'", func(t *testing.T) {
		t.Parallel()

		filterName := "test"

		er := &ExceptionRule{
			Rule: rule.Rule{
				RawRule:    "@@||example.com",
				FilterName: &filterName,
			},
		}
		r := &rule.Rule{
			RawRule:    "||example.com$important",
			FilterName: &filterName,
		}
		r.ParseModifiers([]string{"important"})

		want := false
		if got := er.Cancels(r, testReq("https://example.com/page"), false); got != want {
			t.Errorf("'%s'.Cancels('%s') = %t, want %t", er.RawRule, r.RawRule, got, want)
		}
	})

	t.Run("'@@||page$important' should cancel '||page'", func(t *testing.T) {
		t.Parallel()

		filterName := "test"

		er := &ExceptionRule{
			Rule: rule.Rule{
				RawRule:    "@@||example.com$important",
				FilterName: &filterName,
			},
		}
		r := &rule.Rule{
			RawRule:    "||example.com",
			FilterName: &filterName,
		}
		er.ParseModifiers([]string{"important"})

		want := true
		if got := er.Cancels(r, testReq("https://example.com/page"), false); got != want {
			t.Errorf("'%s'.Cancels('%s') = %t, want %t", er.RawRule, r.RawRule, got, want)
		}
	})
}
