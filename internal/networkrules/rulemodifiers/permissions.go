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
// semantics, adguard_create-own-filters.md §$permissions, lines 2287-2330). A
// matching rule does not block the request; instead an additional
// Permissions-Policy header equal to the modifier contents is added to the
// response (docs lines 2289, 2297, 2327).
//
// 2026-10-08 (batch B8): initial implementation.
//
// 2026-10-08 (B9): the documented frame restriction (docs line 2321) is now
// enforced on the response path — IsFrameScoped marks the modifier as
// frame-scoped and networkrules.ModifyRes drops frame-scoped rules for
// non-frame responses via IsFrameLoad (previously the header was set for
// every matching response regardless of frame type, polluting images, JSON
// API replies and 204s, ~4255 responses per 31 min in the 2026-10-08
// decisions.jsonl capture).
type PermissionsModifier struct {
	// value is the permissions policy text. Empty when cancelAll is set.
	value string
	// cancelAll is set for the bare `$permissions` form, which is used by
	// exception rules (`@@...$permissions`) to disable all $permissions rules
	// whose pattern matches (docs line 2314).
	cancelAll bool
}

var _ ActionModifier = (*PermissionsModifier)(nil)
var _ FrameScopedAction = (*PermissionsModifier)(nil)

// IsFrameScoped reports that $permissions effects apply to main frame and sub
// frame requests only (docs line 2321). Enforced by networkrules.ModifyRes.
func (*PermissionsModifier) IsFrameScoped() bool { return true }

// IsFrameLoad reports whether req is a main frame or sub frame load.
//
// Frame identity comes from the Sec-Fetch-Dest request header; the accepted
// values mirror exemption.IsDocumentDest ("document", "iframe" and the
// legacy "frame" spelling). Fetch metadata is only meaningful on https
// requests (repo convention: internal/filter isDocumentNavigation), so
// non-https requests cannot be classified and keep the pre-B9 behavior
// (fail-open).
func IsFrameLoad(req *http.Request) bool {
	if req == nil || req.URL == nil || req.URL.Scheme != "https" {
		return true
	}
	dest := req.Header.Get("Sec-Fetch-Dest")
	return dest == "document" || dest == "iframe" || dest == "frame"
}

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
