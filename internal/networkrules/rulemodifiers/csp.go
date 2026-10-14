package rulemodifiers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// cspHeaderName is the response header $csp rules strengthen.
const cspHeaderName = "Content-Security-Policy"

// CspModifier implements the $csp action modifier (AdGuard semantics,
// adguard_create-own-filters.md §$csp, lines 1590-1633). A matching rule does
// not block the request; instead its policy is added to the response's
// Content-Security-Policy (docs line 1592).
//
// 2026-10-08 (batch B8): initial implementation.
//
// Documented restrictions intentionally NOT enforced (see batch report):
//   - the "only $domain/$important/$subdocument companions" whitelist
//     (docs line 1623): unknown companion modifiers still fail ParseModifiers
//     naturally, but recognised condition modifiers beyond the three are not
//     rejected;
//   - directive-level conflict resolution for multiple matching rules: each
//     rule's policy is appended as its own header line and the browser
//     enforces the intersection (docs lines 1602-1604 "apply each of them").
type CspModifier struct {
	// value is the CSP policy text. Empty when cancelAll is set.
	value string
	// cancelAll is set for the bare `$csp` form, which is used by exception
	// rules (`@@...$csp`) to disable all $csp rules whose pattern matches
	// (docs line 1616).
	cancelAll bool
}

var _ ActionModifier = (*CspModifier)(nil)

var ErrInvalidCspModifier = errors.New("invalid csp modifier")

func (m *CspModifier) Parse(modifier string) error {
	if modifier == "csp" {
		m.cancelAll = true
		return nil
	}
	if !strings.HasPrefix(modifier, "csp=") {
		return ErrInvalidCspModifier
	}
	value := strings.TrimPrefix(modifier, "csp=")
	// The bare exception form is written without `=` (docs line 1616), so an
	// empty value after `=` is invalid for both primary and exception rules.
	if value == "" {
		return fmt.Errorf("empty csp value")
	}
	// Forbidden characters (docs line 1622): `,` and `$`.
	if strings.ContainsAny(value, ",$") {
		return fmt.Errorf("forbidden character in csp value")
	}
	// Rules with report-* directives are invalid (docs line 1624).
	for _, rawDir := range strings.Split(value, ";") {
		name := rawDir
		if i := strings.IndexAny(name, " \t"); i >= 0 {
			name = name[:i]
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if strings.HasPrefix(name, "report-") {
			return fmt.Errorf("report-* directives are not allowed in csp value")
		}
	}

	m.value = value
	return nil
}

func (m *CspModifier) ModifyRes(res *http.Response) (bool, error) {
	if m.cancelAll {
		return false, nil
	}
	// "Apply each of them" (docs line 1604): every matching rule appends its
	// own policy line; browsers enforce all CSP headers (intersection), which
	// strengthens the site's policy without rewriting it.
	res.Header.Add(cspHeaderName, m.value)
	return true, nil
}

func (*CspModifier) ModifyReq(*http.Request) bool {
	return false
}

// Cancels reports whether an exception cancels a $csp rule. The bare
// exception form cancels every $csp rule (docs line 1616); otherwise the
// value must match exactly (docs line 1615).
func (m *CspModifier) Cancels(other Modifier) bool {
	o, ok := other.(*CspModifier)
	if !ok {
		return false
	}
	if m.cancelAll {
		return true
	}
	return m.value == o.value
}
