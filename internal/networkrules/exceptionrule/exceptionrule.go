package exceptionrule

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/irbis-sh/zen-desktop/internal/exemption"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

// ExceptionRule is a @@-prefixed rule.
//
// 2026-10-08 (B2): AdGuard's exception-scoped modifiers now parse into the
// flag fields below (they were unknown-modifier parse errors before). They
// scope WHAT the exception disables; none of them widens the bare
// user-whitelist path:
//   - $elemhide/$generichide/$specifichide/$jsinject feed the asset-layer
//     exemption query (NetworkRules.ActiveExceptions) and never cancel
//     network rules on their own;
//   - $extension/$stealth are accepted as no-ops (zen has no matching module);
//   - $urlblock/$genericblock cancel (generic) blocking for requests sent
//     from the matching page (Referer surface) and for the page document
//     itself (docs #urlblock-modifier, lines 1292-1298);
//   - $content cancels response rewrites ($jsonprune/$removeheader/
//     $remove-js-constant/$scramblejs; docs #content-modifier, lines 1132-1134);
//   - $document ≡ $elemhide+$content+$urlblock+$jsinject+$extension
//     (docs line 919-923): cancels everything on the matched request —
//     popup rules included (2026-10-08, B1 absorb: popup rules carry the
//     document content type) — unblocks its page, and exempts assets;
//   - $popup (2026-10-08, B1 absorb) only cancels popup rules; it must not
//     lift the blocking of other rule kinds;
//   - exceptions combining $document/$all with action/query modifiers
//     ($removeparam=/$jsonprune=/...) are action/query-scoped: the
//     $document/$all flags do not add cancel-all effects (docs 1119;
//     2026-10-08, B2 — fixed the @@$doc,removeparam global wipe).
type ExceptionRule struct {
	rule.Rule

	Elemhide     bool
	Generichide  bool
	Specifichide bool
	Genericblock bool
	URLBlock     bool
	Jsinject     bool
	Content      bool
	Extension    bool // no-op: zen has no extension host
	Stealth      bool // no-op: zen has no stealth module
}

// exceptionScopedModifiers are AdGuard exception-only modifiers. They stay
// rejected on primary rules so primary parsing is unchanged (the expected
// B2 net_err drop comes from @@ lines only).
var exceptionScopedModifiers = map[string]struct{}{
	"elemhide": {}, "generichide": {}, "specifichide": {}, "genericblock": {},
	"urlblock": {}, "jsinject": {}, "content": {}, "extension": {}, "stealth": {},
}

// ParseModifiers parses the exception-scoped modifiers into flags and
// delegates the rest to Rule.ParseModifiers. ParseRule holds the rule as
// *ExceptionRule, so this override is the one invoked for @@ rules.
func (er *ExceptionRule) ParseModifiers(modifiers []string) error {
	rest := make([]string, 0, len(modifiers))
	for _, m := range modifiers {
		name, hasValue := cutModifierName(m)
		if _, ok := exceptionScopedModifiers[name]; !ok {
			rest = append(rest, m)
			continue
		}
		if len(m) > 0 && m[0] == '~' {
			return fmt.Errorf("modifier %q cannot be negated", name)
		}
		// $stealth and $extension take option values; the rest are bare flags.
		if hasValue && name != "stealth" && name != "extension" {
			return fmt.Errorf("modifier %q does not take a value", name)
		}
		switch name {
		case "elemhide":
			er.Elemhide = true
		case "generichide":
			er.Generichide = true
		case "specifichide":
			er.Specifichide = true
		case "genericblock":
			er.Genericblock = true
		case "urlblock":
			er.URLBlock = true
		case "jsinject":
			er.Jsinject = true
		case "content":
			er.Content = true
		case "extension":
			er.Extension = true
		case "stealth":
			er.Stealth = true
		}
	}
	return er.Rule.ParseModifiers(rest)
}

func cutModifierName(modifier string) (name string, hasValue bool) {
	if len(modifier) > 0 && modifier[0] == '~' {
		modifier = modifier[1:]
	}
	name, _, hasValue = strings.Cut(modifier, "=")
	return name, hasValue
}

// IsBare reports whether the exception carries no modifiers at all — the
// user-whitelist form @@||host^. Its cancel-everything behavior is
// load-bearing and frozen (B2 red line), as is its exclusion from the asset
// exemption query.
func (er *ExceptionRule) IsBare() bool {
	return !er.Document && !er.All && !er.Popup &&
		!er.Elemhide && !er.Generichide && !er.Specifichide && !er.Genericblock &&
		!er.URLBlock && !er.Jsinject && !er.Content && !er.Extension && !er.Stealth &&
		len(er.AndConditionModifiers()) == 0 && len(er.OrConditionModifiers()) == 0 &&
		len(er.ActionModifiers()) == 0 && len(er.QueryModifiers()) == 0
}

// hasScopedFlags reports whether any AdGuard exception-scoped modifier is
// present; scoped exceptions never cancel network rules wholesale.
func (er *ExceptionRule) hasScopedFlags() bool {
	return er.Elemhide || er.Generichide || er.Specifichide || er.Genericblock ||
		er.URLBlock || er.Jsinject || er.Content || er.Extension || er.Stealth ||
		er.Popup
}

// HasPageScope reports whether the exception carries effects that reach the
// whole page on the request path ($urlblock/$genericblock and the urlblock
// component of $document). Action/query-scoped exceptions (see
// hasActionQueryMods) are excluded: their $document flag does not carry the
// page-level urlblock component.
func (er *ExceptionRule) HasPageScope() bool {
	return (er.Document && !er.hasActionQueryMods()) || er.URLBlock || er.Genericblock
}

// HasResPageScope reports whether the exception carries effects that reach
// the whole page on the response path ($content and the content component
// of $document). Action/query-scoped exceptions are excluded.
func (er *ExceptionRule) HasResPageScope() bool {
	return (er.Document || er.Content) && !er.hasActionQueryMods()
}

// hasActionQueryMods reports whether the exception carries action or query
// modifiers ($jsonprune=/$removeparam=/$removeheader=/$scramblejs/
// $remove-js-constant). 2026-10-08 (B2): such exceptions are action/query-
// SCOPED — per docs line 1119, additional modifiers narrow the exception's
// effects — so their $document/$all flags do not add cancel-all effects and
// only the structural action/query cancellation applies. Real-world case
// (9 lines in the live lists): @@$doc,removeparam=/^.*_dest_url=.*$/ means
// "keep _dest_url params", not "disable all filtering".
func (er *ExceptionRule) hasActionQueryMods() bool {
	return len(er.ActionModifiers()) > 0 || len(er.QueryModifiers()) > 0
}

// Cancels reports whether this exception — already matched to the request by
// URL pattern and condition modifiers (ShouldMatchReq) — cancels applying
// rule r to that request.
//
// 2026-10-08 (B2/H3): replaces the previous structural modifier matching
// (which failed whenever the rule carried fewer/different conditions, e.g.
// @@||ads.com^$domain=example.org could not cancel ||ads.com^) with
// per-request judgment: the caller matches the exception against the request
// first and then asks which rules on that request are cancelled.
//
// pageSurface marks exceptions matched via the page (Referer) URL instead of
// the request URL. Only page-scoped blocking effects apply there; bare user
// whitelists and @@$all are never page-surface candidates.
func (er *ExceptionRule) Cancels(r *rule.Rule, req *http.Request, pageSurface bool) bool {
	// $important outranks exceptions without it (docs 670-679; unchanged).
	if r.Important && !er.Important {
		return false
	}

	if er.All && !er.hasActionQueryMods() {
		// Unchanged: @@$all cancels everything it matches (kept as-is for
		// batch B1's tightening). Combined with action/query modifiers it
		// is action/query-scoped instead (docs 1119).
		return true
	}

	if er.IsBare() {
		// Unchanged: the bare user whitelist cancels everything it matches
		// (B2 red line), with one carve-out:
		// 2026-10-08 (B7 absorb, verify08-D5 / AdGuard doc 2737): basic
		// exceptions without any modifier do NOT disable $removeparam rules.
		// Exceptions carrying the important flag keep the pre-B7 behavior;
		// $document/$urlblock exceptions DO disable $removeparam (doc 2737)
		// — they take the cancel-all paths below.
		if len(r.QueryModifiers()) > 0 && !er.Important {
			return false
		}
		return true
	}

	if pageSurface {
		// $urlblock/$genericblock and the urlblock component of $document:
		// unblock (generic) requests sent from the matching page
		// (docs 1292-1298, 919-923).
		if !r.ShouldBlockReq(req) {
			return false
		}
		if er.Document || er.URLBlock {
			return true
		}
		return r.IsGeneric() // $genericblock: generic rules only
	}

	if er.Document && !er.hasActionQueryMods() {
		// $document exception cancels everything on the matched request
		// (docs 919-923; probe M5 expects allow). Combined with action/query
		// modifiers it is action/query-scoped instead (docs 1119).
		return true
	}

	if len(er.ActionModifiers()) == 0 && len(er.QueryModifiers()) == 0 {
		if er.hasScopedFlags() {
			// Scoped exception modifiers never cancel network rules
			// wholesale.
			// 2026-10-08 (B1 absorb): @@$popup only cancels popup rules
			// (AdGuard: it must not lift the blocking of other rule kinds).
			if er.Popup && !r.Popup {
				return false
			}
			// $urlblock/$genericblock additionally unblock the page document
			// request itself: for that request the page is the request
			// (docs 1292-1298).
			if (er.URLBlock || er.Genericblock) &&
				exemption.IsDocumentDest(req.Header.Get("Sec-Fetch-Dest")) &&
				r.ShouldBlockReq(req) {
				if er.URLBlock {
					return true
				}
				return r.IsGeneric()
			}
			// A satisfied @@$popup (its rule is a popup rule) is cancelled;
			// every other scoped modifier leaves network rules untouched.
			return er.Popup
		}
		// Condition-only exception (@@...$domain=, $script, $third-party,
		// ...): a per-request whitelist — it cancels every rule that also
		// matched this request (H3; probe M18/M19 expect allow).
		return true
	}

	// Action/query-scoped exceptions ($jsonprune=, $removeparam=, ...) stay
	// scoped: structural matching against rules carrying the same modifier
	// kind (previous semantics for this subset).
	return er.cancelsActionQueryStructural(r)
}

// CancelsRes reports whether the exception cancels r's response-side
// application ($content family: $jsonprune/$removeheader/$remove-js-constant/
// $scramblejs; docs 1132-1134, 919-923). pageSurface marks exceptions matched
// via the page (Referer) URL.
func (er *ExceptionRule) CancelsRes(r *rule.Rule, pageSurface bool) bool {
	if r.Important && !er.Important {
		return false
	}

	if er.All {
		// Unchanged: @@$all cancels everything it matches.
		return true
	}

	if er.IsBare() {
		// 2026-10-08 (B7 absorb): mirror Cancels — basic exceptions without
		// modifiers do not disable $removeparam rules (doc 2737).
		if len(r.QueryModifiers()) > 0 && !er.Important {
			return false
		}
		return true
	}

	if (er.Document || er.Content) && !er.hasActionQueryMods() {
		// $document / $content cancel response rewrites (docs 919-923,
		// 1132-1134). Combined with action/query modifiers the exception is
		// action/query-scoped instead (docs 1119).
		return true
	}

	if pageSurface {
		// Page-surface candidates only carry document/content effects here.
		return false
	}

	if len(er.ActionModifiers()) == 0 && len(er.QueryModifiers()) == 0 {
		if er.hasScopedFlags() {
			// $urlblock/$genericblock/$elemhide/... do not touch response
			// rewrites.
			return false
		}
		// Condition-only exception: per-request whitelist (H3).
		return true
	}

	return er.cancelsActionQueryStructural(r)
}

// cancelsActionQueryStructural mirrors the pre-B2 structural matching for
// action/query-scoped exceptions ($jsonprune=, $removeparam=, ...), which
// stay limited to rules carrying the same kind of modifier.
func (er *ExceptionRule) cancelsActionQueryStructural(r *rule.Rule) bool {
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

// CancelsNothingOnReq reports whether the exception — already matched to the
// request by URL pattern and condition modifiers — can never cancel any
// network rule on the request path, i.e. Cancels(r, req, false) ≡ false for
// every rule r. True exactly when the exception's only scoped effects belong
// to the asset-exemption family ($elemhide/$generichide/$specifichide/
// $jsinject/$content/$extension/$stealth) with no $document/$all/$popup/
// $urlblock/$genericblock flag, no action/query modifiers, and it is not a
// bare whitelist: in Cancels such an exception always reaches the scoped
// branch and returns er.Popup (false). Read-only hot-path classifier
// (2026-10-08 perf audit ②); semantics must stay in lockstep with Cancels.
func (er *ExceptionRule) CancelsNothingOnReq() bool {
	if er.Document || er.All || er.Popup || er.URLBlock || er.Genericblock || er.IsBare() {
		return false
	}
	if len(er.ActionModifiers()) > 0 || len(er.QueryModifiers()) > 0 {
		return false
	}
	return er.Elemhide || er.Generichide || er.Specifichide || er.Jsinject ||
		er.Content || er.Extension || er.Stealth
}

// CancelsEverythingOnReq reports whether the exception — already matched to
// the request — cancels every candidate rule except for the two documented
// vetoes that the caller must still evaluate per rule: (1) $important rules
// survive a non-$important exception (docs 670-679), and (2) a bare
// whitelist without $important does not disable rules carrying query
// modifiers (B7 carve-out, doc 2737). True exactly for @@$all without
// action/query mods (docs 1119 exception), @@$document without action/query
// mods (docs 919-923), bare whitelists, and pure condition-only exceptions
// (H3 per-request whitelist). Read-only hot-path classifier (2026-10-08
// perf audit ②); semantics must stay in lockstep with Cancels.
func (er *ExceptionRule) CancelsEverythingOnReq() bool {
	if len(er.ActionModifiers()) > 0 || len(er.QueryModifiers()) > 0 {
		return false
	}
	if er.All || er.Document || er.IsBare() {
		return true
	}
	// Pure condition-only exception (@@...$third-party, $script, ...):
	// cancels every matched rule (H3).
	return !er.Popup && !er.URLBlock && !er.Genericblock &&
		!er.Elemhide && !er.Generichide && !er.Specifichide &&
		!er.Jsinject && !er.Content && !er.Extension && !er.Stealth
}
