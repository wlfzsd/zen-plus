package rulemodifiers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// permissionsHeaderName is the response header $permissions rules strengthen.
const permissionsHeaderName = "Permissions-Policy"

// PermissionsModifier implements the $permissions action modifier (AdGuard
// semantics, adguard_create-own-filters.md §$permissions, lines 2287-2330).
// A matching rule does not block the request; instead an additional
// Permissions-Policy header equal to the modifier contents is added to the
// response (docs lines 2289, 2297, 2327).
//
// 2026-10-08 (batch B8): initial implementation.
//
// Documented behaviour intentionally NOT implemented (see batch report): the
// "main frame and sub frame requests only" restriction (docs line 2321) —
// evaluating it would need the request at response time, which the existing
// ShouldMatchRes pipeline does not carry, so the header is set for every
// matching response regardless of frame type.
type PermissionsModifier struct {
	// value is the permissions policy text. Empty when cancelAll is set.
	value string
	// cancelAll is set for the bare `$permissions` form, which is used by
	// exception rules (`@@...$permissions`) to disable all $permissions rules
	// whose pattern matches (docs line 2314).
	cancelAll bool
}

var _ ActionModifier = (*PermissionsModifier)(nil)

var ErrInvalidPermissionsModifier = errors.New("invalid permissions modifier")

func (m *PermissionsModifier) Parse(modifier string) error {
	if modifier == "permissions" {
		m.cancelAll = true
		return nil
	}
	if !strings.HasPrefix(modifier, "permissions=") {
		return ErrInvalidPermissionsModifier
	}
	value := strings.TrimPrefix(modifier, "permissions=")
	// The bare exception form is written without `=` (docs line 2314), so an
	// empty value after `=` is invalid for both primary and exception rules.
	if value == "" {
		return fmt.Errorf("empty permissions value")
	}
	// Escaped commas were already unescaped by splitModifiers and `|` is an
	// accepted feature separator (docs lines 2303-2304); the value is passed
	// through verbatim.

	m.value = value
	return nil
}

func (m *PermissionsModifier) ModifyRes(res *http.Response) (bool, error) {
	if m.cancelAll {
		return false, nil
	}
	// Multiple matching rules each add their own Permissions-Policy header
	// (docs line 2327).
	res.Header.Add(permissionsHeaderName, m.value)
	return true, nil
}

func (*PermissionsModifier) ModifyReq(*http.Request) bool {
	return false
}

// Cancels reports whether an exception cancels a $permissions rule. The bare
// exception form cancels every $permissions rule (docs line 2314); otherwise
// the value must match exactly (docs line 2313).
func (m *PermissionsModifier) Cancels(other Modifier) bool {
	o, ok := other.(*PermissionsModifier)
	if !ok {
		return false
	}
	if m.cancelAll {
		return true
	}
	return m.value == o.value
}
