package rulemodifiers

import (
	"net/http"
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

// QueryModifier modifies request query parameters.
// According to terminology, they are also "action modifiers", but are implemented separately for performance reasons.
//
// 2026-10-08 (B7): ModifyQuery receives the whole request instead of a
// decoded url.Values map. The request provides the method context needed by
// the AdGuard $removeparam method whitelist (doc 2634: GET/HEAD/OPTIONS and
// bodyless POST only), and lets the modifier rewrite req.URL.RawQuery
// directly in encoded form (doc 2650-2656), preserving the order and
// encoding of untouched pairs.
type QueryModifier interface {
	Modifier
	ModifyQuery(req *http.Request) bool
}
