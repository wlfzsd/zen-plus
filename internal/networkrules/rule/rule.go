package rule

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rulemodifiers"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rulemodifiers/removejsconstant"
)

// Rule represents modifiers of a rule.
//
// 2026-10-05: the four modifier slices were folded into two lazily
// allocated groups (ruleCondMods/ruleActMods) so that rules without
// modifiers (the majority) keep this struct small.
type Rule struct {
	// string representation
	RawRule string
	// FilterName is the name of the filter that the rule belongs to.
	FilterName *string

	// condMods holds the rule's condition modifiers. It is nil for rules
	// without any condition modifier and allocated on first use by
	// ParseModifiers.
	condMods *ruleCondMods
	// actMods holds the rule's action and query modifiers, allocated
	// lazily the same way.
	actMods *ruleActMods

	// Document shows if rule has Document modifier.
	Document bool
	// Popup shows if rule has Popup modifier ($popup). Popup rules carry the
	// document content type with a special flag (AdGuard CoreLibs semantics,
	// doc "popup modifier limitations"): they match top-level document
	// navigations and hand the flag to the blocking page, which then tries
	// window.close().
	// 2026-10-08 (B1): implemented, was silently dropped before.
	Popup bool
	// Important shows if rule has Important modifier.
	Important bool
	// All shows if rule has All modifier.
	// On primary rules, it implies Document. Since 2026-10-08 (B1) exceptions
	// with $all are rejected at parse time (AdGuard restriction), so All is
	// only ever set on primary rules.
	All bool
	// Badfilter marks a rule carrying the $badfilter modifier (AdGuard doc
	// #badfilter-modifier, 2026-10-08 B6). Such rules are never inserted
	// into a rule store: networkrules collects them at parse time and
	// applies them once in Compact, after every filter list was fully
	// loaded (禁逐条即时禁用).
	Badfilter bool
	// badfilterDisable holds post-load $badfilter disablement state; nil
	// unless a $badfilter rule targeted this rule (set in
	// networkrules.Compact via applyBadfilters).
	badfilterDisable *BadfilterDisable
	// noConds marks rules parsed without any condition modifier (2026-10-08
	// perf audit ③). ParseModifiers is the only writer of condMods, so a
	// rule whose ParseModifiers left condMods nil has an empty And/Or set
	// and ModifiersMatchReq reduces to the $badfilter check — the flag lets
	// the request hot path skip the empty-slice evaluation entirely.
	// Conservative by construction: rules that bypass ParseModifiers
	// (hosts-derived rules) keep false and just take the regular path.
	noConds bool
}

// BadfilterDisable describes how $badfilter rules disabled a rule after the
// filter lists were fully loaded (networkrules.Compact, 2026-10-08 B6).
type BadfilterDisable struct {
	// Full disables the rule outright (exact text match, doc 1476-1483).
	Full bool
	// Domains holds the $domain sets of partially disabling $badfilter
	// rules: the rule must not apply to requests whose effective domain
	// matches any entry (doc 1485-1499, intersection semantics).
	Domains []*rulemodifiers.DomainModifier
}

// TODO: The split between And and Or modifiers is somewhat convoluted and exists only to support ContentType.
// Remove it by grouping multiple ContentTypes into a single modifier and evaluating all modifiers with AND logic.

// ruleCondMods groups the condition modifiers of a rule. It only exists for
// rules that actually carry condition modifiers; see Rule.condMods.
type ruleCondMods struct {
	// and are modifiers that must all match for the rule to apply.
	and []rulemodifiers.ConditionModifier
	// or are modifiers where at least one must match for the rule to apply.
	or []rulemodifiers.ConditionModifier
}

// ruleActMods groups the action and query modifiers of a rule. It only
// exists for rules that actually carry them; see Rule.actMods.
type ruleActMods struct {
	action []rulemodifiers.ActionModifier
	query  []rulemodifiers.QueryModifier
}

// ensureCondMods lazily allocates Rule.condMods.
func (rm *Rule) ensureCondMods() *ruleCondMods {
	if rm.condMods == nil {
		rm.condMods = &ruleCondMods{}
	}
	return rm.condMods
}

// ensureActMods lazily allocates Rule.actMods.
func (rm *Rule) ensureActMods() *ruleActMods {
	if rm.actMods == nil {
		rm.actMods = &ruleActMods{}
	}
	return rm.actMods
}

// AndConditionModifiers returns the modifiers that must all match for the rule to apply.
func (rm *Rule) AndConditionModifiers() []rulemodifiers.ConditionModifier {
	if rm.condMods == nil {
		return nil
	}
	return rm.condMods.and
}

// OrConditionModifiers returns the modifiers where at least one must match for the rule to apply.
func (rm *Rule) OrConditionModifiers() []rulemodifiers.ConditionModifier {
	if rm.condMods == nil {
		return nil
	}
	return rm.condMods.or
}

// ActionModifiers returns the rule's action modifiers.
func (rm *Rule) ActionModifiers() []rulemodifiers.ActionModifier {
	if rm.actMods == nil {
		return nil
	}
	return rm.actMods.action
}

// QueryModifiers returns the rule's query modifiers.
func (rm *Rule) QueryModifiers() []rulemodifiers.QueryModifier {
	if rm.actMods == nil {
		return nil
	}
	return rm.actMods.query
}

func (rm *Rule) ParseModifiers(modifiers []string) error {
	// linkDomainToTarget tracks whether the rule carries any of the
	// modifiers that make its $domain modifier also match the target
	// hostname (see the post-loop application below). (2026-10-08, B5)
	linkDomainToTarget := false
	for _, m := range modifiers {
		if len(m) == 0 {
			return errors.New("empty modifier")
		}

		// Noop modifier is ignored
		if isNoopModifier(m) {
			continue
		}

		name, hasValue := cutModifierName(m)
		// origTilde remembers whether the modifier was written with a
		// leading '~' (2026-10-08 B6): cutModifierName strips it silently,
		// and AdGuard allows '~' only on content types, $domain entries and
		// third-party, so every other flag modifier must reject it here.
		origTilde := m[0] == '~'
		// parseArg is the modifier string handed to modifier.Parse.
		// $queryprune is a deprecated alias of $removeparam (AdGuard doc
		// #removeparam-modifier): normalize it so the parametrised branch
		// and RemoveParamModifier.Parse (which matches the literal name)
		// accept both spellings.
		parseArg := m
		if name == "queryprune" {
			parseArg = "removeparam" + m[len("queryprune"):]
			name = "removeparam"
		}
		// $cookie/$csp/$permissions/$removeparam make the rule's $domain
		// modifier also match the request's target hostname (AdGuard docs
		// §$domain, lines 566-585). (2026-10-08, B5) — evaluated after the
		// $queryprune alias normalization so the alias links like
		// $removeparam (2026-10-08 merge).
		switch name {
		case "cookie", "csp", "permissions", "removeparam":
			linkDomainToTarget = true
		}

		var modifier rulemodifiers.Modifier
		var isOr bool // true if the modifier belongs to ruleCondMods.or.

		if !hasValue {
			// Flag modifiers.
			switch name {
			case "document", "doc":
				if origTilde {
					return tildeError(m)
				}
				rm.Document = true
				continue
			case "popup":
				// 2026-10-08 (B1): $popup flag; see Rule.Popup.
				rm.Popup = true
				continue
			case "important":
				if origTilde {
					return tildeError(m)
				}
				rm.Important = true
				continue
			case "xmlhttprequest",
				"xhr",
				"font",
				"subdocument",
				"image",
				"object",
				"script",
				"stylesheet",
				"media",
				"websocket",
				"ping",
				"other":
				// Content types: negation is legal and is detected inside
				// ContentTypeModifier.Parse, so no tilde check here.
				modifier = &rulemodifiers.ContentTypeModifier{}
				isOr = true
			case "third-party":
				// '~third-party' is legal and is detected inside
				// ThirdPartyModifier.Parse, so no tilde check here.
				modifier = &rulemodifiers.ThirdPartyModifier{}
			case "removeparam":
				if origTilde {
					return tildeError(m)
				}
				modifier = &rulemodifiers.RemoveParamModifier{}
			case "cookie": // 2026-10-08 batch B3: $cookie action modifier.
				modifier = &rulemodifiers.CookieModifier{}
			case "csp": // 2026-10-08 batch B8: $csp action modifier.
				modifier = &rulemodifiers.CspModifier{}
			case "replace": // 2026-10-08 batch B8: $replace action modifier.
				modifier = &rulemodifiers.ReplaceModifier{}
			case "permissions": // 2026-10-08 batch B8: $permissions action modifier.
				modifier = &rulemodifiers.PermissionsModifier{}
			case "match-case":
				// Accepted as a no-op flag (2026-10-08 B6): zen's matching
				// layer is always case-sensitive, which is exactly the
				// semantics $match-case demands (AdGuard doc
				// #match-case-modifier: default matching is insensitive,
				// match-case makes it sensitive). Linked batch: the future
				// case-insensitive matching batch (B11) must turn this
				// into a real condition modifier.
				if origTilde {
					return tildeError(m)
				}
				continue
			case "strict-first-party", "strict1p", "strict-third-party", "strict3p":
				if origTilde {
					return tildeError(m)
				}
				modifier = &rulemodifiers.StrictPartyModifier{}
			case "all":
				if origTilde {
					return tildeError(m)
				}
				// $all is made of all content-type modifiers plus $popup
				// (AdGuard doc "all modifier"). The All flag keeps the
				// existing full matching face; Popup adds the blockpage
				// tab-close flag. 2026-10-08 (B1).
				rm.All = true
				rm.Popup = true
				continue
			case "badfilter":
				// Marks the rule as a $badfilter rule (2026-10-08 B6);
				// networkrules.ParseRule collects it instead of inserting
				// it, and networkrules.Compact applies it post-load.
				if origTilde {
					return tildeError(m)
				}
				rm.Badfilter = true
				continue
			default:
				return fmt.Errorf("unknown modifier %q", m)
			}
		} else {
			// Parametrised modifiers.
			switch name {
			case "domain", "from":
				// "from" is an alias of "domain" (AdGuard docs §$domain
				// compatibility note, line 604). (2026-10-08, B5)
				modifier = &rulemodifiers.DomainModifier{}
			case "denyallow":
				// $denyallow only restricts the rule by the request's
				// target domain (AdGuard docs §$denyallow, lines 481-524).
				// (2026-10-08, B5)
				modifier = &rulemodifiers.DenyAllowModifier{}
			case "method":
				modifier = &rulemodifiers.MethodModifier{}
			case "removeparam":
				modifier = &rulemodifiers.RemoveParamModifier{}
			case "cookie": // 2026-10-08 batch B3: $cookie action modifier.
				modifier = &rulemodifiers.CookieModifier{}
			case "csp": // 2026-10-08 batch B8: $csp action modifier.
				modifier = &rulemodifiers.CspModifier{}
			case "replace": // 2026-10-08 batch B8: $replace action modifier.
				modifier = &rulemodifiers.ReplaceModifier{}
			case "permissions": // 2026-10-08 batch B8: $permissions action modifier.
				modifier = &rulemodifiers.PermissionsModifier{}
			case "header":
				modifier = &rulemodifiers.HeaderModifier{}
			case "removeheader":
				modifier = &rulemodifiers.RemoveHeaderModifier{}
			case "remove-js-constant":
				modifier = &removejsconstant.Modifier{}
			case "scramblejs":
				modifier = &rulemodifiers.ScrambleJSModifier{}
			case "jsonprune":
				modifier = &rulemodifiers.JSONPruneModifier{}
			default:
				return fmt.Errorf("unknown modifier %q", m)
			}
		}

		if err := modifier.Parse(parseArg); err != nil {
			return err
		}

		switch typed := modifier.(type) {
		case rulemodifiers.ConditionModifier:
			mods := rm.ensureCondMods()
			if isOr {
				mods.or = append(mods.or, typed)
			} else {
				mods.and = append(mods.and, typed)
			}
		case rulemodifiers.ActionModifier:
			mods := rm.ensureActMods()
			mods.action = append(mods.action, typed)
		case rulemodifiers.QueryModifier:
			mods := rm.ensureActMods()
			mods.query = append(mods.query, typed)
		default:
			log.Fatalf("got unknown modifier type %T for modifier %s", modifier, m)
		}
	}

	// Now that every modifier is known, mark the rule's $domain modifiers
	// for target-domain matching: $domain may precede the linking modifier
	// in the rule text (e.g. "...$domain=x.com,removeparam=p").
	// With the merged batches cookie/csp/permissions now parse on their
	// own, so the link is observable through them as well as through
	// $removeparam. (2026-10-08, B5)
	if linkDomainToTarget && rm.condMods != nil {
		for _, m := range rm.condMods.and {
			if dm, ok := m.(*rulemodifiers.DomainModifier); ok {
				dm.MatchTargetDomain = true
			}
		}
	}

	// Load-time noConditions flag (2026-10-08 perf audit ③): condMods is
	// nil exactly when no condition modifier was parsed.
	rm.noConds = rm.condMods == nil

	return nil
}

// isNoopModifier returns true if modifier is one or more underscores.
func isNoopModifier(modifier string) bool {
	for i := 0; i < len(modifier); i++ {
		if modifier[i] != '_' {
			return false
		}
	}
	return true
}

func cutModifierName(modifier string) (name string, hasValue bool) {
	if len(modifier) > 0 && modifier[0] == '~' {
		modifier = modifier[1:]
	}
	name, _, hasValue = strings.Cut(modifier, "=")
	return name, hasValue
}

// tildeError builds the error for a leading '~' on a flag modifier that
// does not support negation (2026-10-08 B6). AdGuard allows '~' only on
// content types, $domain entries and third-party.
func tildeError(m string) error {
	return fmt.Errorf("invalid modifier %q: '~' is not allowed on this modifier", m)
}

// ApplyBadfilterDisable records one $badfilter hit against this rule
// (2026-10-08 B6). full means the rule is disabled outright; otherwise dm
// is the parsed $domain set of a partially disabling $badfilter rule.
// Exported for networkrules.applyBadfilters, which walks the stores once
// post-load and marks targeted rules.
func (rm *Rule) ApplyBadfilterDisable(full bool, dm *rulemodifiers.DomainModifier) {
	if rm.badfilterDisable == nil {
		rm.badfilterDisable = &BadfilterDisable{}
	}
	if full {
		rm.badfilterDisable.Full = true
		return
	}
	if rm.badfilterDisable.Full {
		return // already disabled outright; partial marks are moot
	}
	rm.badfilterDisable.Domains = append(rm.badfilterDisable.Domains, dm)
}

// BadfilterDisableState returns the rule's post-load $badfilter disablement
// state, or nil when no $badfilter rule targeted it. Read-only diagnostic
// for filter audits (networkrules.BadfilterAudit, 2026-10-08 B6).
func (rm *Rule) BadfilterDisableState() *BadfilterDisable {
	return rm.badfilterDisable
}

// badfilterDisabledFor reports whether $badfilter disablement bars this rule
// for req (2026-10-08 B6). Full disablement always bars; partial
// disablement bars when the request's effective domain matches a disabling
// $domain entry. The effective domain mirrors DomainModifier.ShouldMatchReq:
// the referrer host when present, otherwise the request's own host.
func (rm *Rule) badfilterDisabledFor(req *http.Request) bool {
	bd := rm.badfilterDisable
	if bd == nil {
		return false
	}
	if bd.Full {
		return true
	}
	if len(bd.Domains) == 0 || req == nil {
		return false
	}

	var host string
	if referer := req.Header.Get("Referer"); referer != "" {
		h, ok := rulemodifiers.RefererHostname(referer)
		if !ok {
			// Unparseable referer: like DomainModifier, never attribute
			// the request to a disabled domain.
			return false
		}
		host = h
	} else {
		host = req.URL.Hostname()
	}

	for _, dm := range bd.Domains {
		if dm.MatchHost(host) {
			return true
		}
	}
	return false
}

// ShouldMatchReq returns true if the rule should match the request.
func (rm *Rule) ShouldMatchReq(req *http.Request) bool {
	return rm.ShouldMatchReqNav(req, req.Header.Get("Sec-Fetch-User") == "?1" && req.Header.Get("Sec-Fetch-Dest") == "document")
}

// ShouldMatchReqNav is ShouldMatchReq with the user-navigation guard
// (Sec-Fetch-User=?1 + Sec-Fetch-Dest=document, upstream #257 semantics)
// precomputed by the caller, so evaluating many candidate rules against one
// request does not repeat the two header reads per rule.
//
// 2026-10-08 (B7): the guard only spares the user navigation from BLOCKING
// (upstream #257: the main frame must not be blocked by non-$document/$all
// rules). It no longer hides rules that cannot block anyway: rewrite-type
// rules (action/query modifiers, e.g. $removeparam) are still evaluated on
// user navigations, matching AdGuard doc 2713 ("$removeparam ... removes
// ... from URL queries of any request", main navigations included).
// Blocking outcomes are bit-for-bit unchanged: a rule with action/query
// modifiers always has ShouldBlockReq == false, so admitting it here can
// never introduce a block; plain blocking rules keep returning false and
// $document/$all rules keep matching.
//
// 2026-10-08 (B1 merge): the user-navigation guard also admits $popup
// rules, so popup rules participate in user-navigation/document matching
// exactly like $document rules. The popup admission and the B7
// rewrite-type exemption compose: a rule carrying both ($popup plus
// action/query modifiers) is admitted through either path.
func (rm *Rule) ShouldMatchReqNav(req *http.Request, isUserNav bool) bool {
	if isUserNav && !rm.Document && !rm.All && !rm.Popup {
		// 2026-10-08 (perf audit ③): condition-free rewrite rules — the
		// generic $removeparam family, 1763 of the 2310 generic rules in
		// the live main6 lists — reduce to hasRewriteAction plus the
		// $badfilter check; skip the (empty) modifier evaluation.
		if rm.noConds {
			return rm.hasRewriteAction() && !rm.badfilterDisabledFor(req)
		}
		return rm.hasRewriteAction() && rm.ModifiersMatchReq(req)
	}

	// $popup applies the document content type with a special flag (AdGuard
	// CoreLibs semantics): popup-only rules match top-level document
	// navigations, never subresources. Rules that also carry $document or
	// $all keep their existing, broader matching face. 2026-10-08 (B1).
	if rm.Popup && !rm.Document && !rm.All &&
		req.Header.Get("Sec-Fetch-Dest") != "document" {
		return false
	}

	return rm.ModifiersMatchReq(req)
}

// hasRewriteAction reports whether the rule carries action or query
// modifiers, i.e. it rewrites requests instead of blocking them
// (rule.ShouldBlockReq is false exactly when this returns true).
func (rm *Rule) hasRewriteAction() bool {
	return len(rm.ActionModifiers()) > 0 || len(rm.QueryModifiers()) > 0
}

// ModifiersMatchReq returns true if the rule's matching modifiers match the request.
func (rm *Rule) ModifiersMatchReq(req *http.Request) bool {
	// $badfilter disablement (2026-10-08 B6): a rule disabled post-load
	// must not apply. ExceptionRule embeds Rule and evaluates exceptions
	// through the same ShouldMatchReq → ModifiersMatchReq entry, so
	// disablement covers exceptions as well (2026-10-08 merge).
	if rm.badfilterDisabledFor(req) {
		return false
	}

	// Condition-free rules (perf audit ③): the And/Or evaluation is
	// vacuously true.
	if rm.noConds {
		return true
	}

	// AndModifiers: All must match.
	for _, m := range rm.AndConditionModifiers() {
		if !m.ShouldMatchReq(req) {
			return false
		}
	}

	// OrModifiers: At least one must match.
	if or := rm.OrConditionModifiers(); len(or) > 0 {
		for _, m := range or {
			if m.ShouldMatchReq(req) {
				return true
			}
		}
		return false
	}

	return true
}

// ShouldMatchRes returns true if the rule should match the response.
func (rm *Rule) ShouldMatchRes(res *http.Response) bool {
	return rm.ModifiersMatchRes(res)
}

// ModifiersMatchRes returns true if the rule's matching modifiers match the response.
func (rm *Rule) ModifiersMatchRes(res *http.Response) bool {
	// $badfilter disablement (2026-10-08 B6), evaluated against the request
	// that produced the response when it is available. ExceptionRule
	// embeds Rule and evaluates exceptions through the same
	// ShouldMatchRes → ModifiersMatchRes entry, so disablement covers
	// exceptions as well (2026-10-08 merge).
	if rm.badfilterDisable != nil {
		if rm.badfilterDisabledFor(res.Request) {
			return false
		}
	}

	// Condition-free rules (perf audit ③): the And/Or evaluation is
	// vacuously true.
	if rm.noConds {
		return true
	}

	for _, m := range rm.AndConditionModifiers() {
		if !m.ShouldMatchRes(res) {
			return false
		}
	}

	if or := rm.OrConditionModifiers(); len(or) > 0 {
		for _, m := range or {
			if m.ShouldMatchRes(res) {
				return true
			}
		}
		return false
	}

	return true
}

// IsGeneric reports whether the rule is "generic" in the AdGuard sense
// (docs #exception-modifiers-generic-rules, lines 1316-1339): it is not
// limited to specific domains. The URL pattern itself does not matter
// (||domain.com^ is generic); only a $domain modifier with positively
// permitted domains makes the rule specific. 2026-10-08 (B2): used by the
// $genericblock exception semantics.
func (rm *Rule) IsGeneric() bool {
	for _, m := range rm.AndConditionModifiers() {
		if dm, ok := m.(*rulemodifiers.DomainModifier); ok && dm.HasPermittedDomains() {
			return false
		}
	}
	for _, m := range rm.OrConditionModifiers() {
		if dm, ok := m.(*rulemodifiers.DomainModifier); ok && dm.HasPermittedDomains() {
			return false
		}
	}
	return true
}

// ShouldBlockReq returns true if the request should be blocked.
func (rm *Rule) ShouldBlockReq(*http.Request) bool {
	return len(rm.ActionModifiers()) == 0 && len(rm.QueryModifiers()) == 0
}

// ModifyReq modifies a request. Returns true if the request was modified.
func (rm *Rule) ModifyReq(req *http.Request) (modified bool) {
	for _, modifier := range rm.ActionModifiers() {
		if modifier.ModifyReq(req) {
			modified = true
		}
	}

	return modified
}

// ModifyReqQuery modifies the request query. Returns true if the query was modified.
//
// 2026-10-08 (B7): the whole request is passed so that query modifiers get
// the method context (AdGuard doc 2634) and rewrite req.URL.RawQuery
// directly in encoded form; the decoded url.Values round-trip is gone.
//
// 2026-10-08 (perf audit ①): qs carries the request query pre-split into
// segments once per ModifyReq pass; qs is nil when there is no query or the
// method is not eligible, and every query modifier then returns false.
func (rm *Rule) ModifyReqQuery(req *http.Request, qs *rulemodifiers.QueryState) (modified bool) {
	for _, qm := range rm.QueryModifiers() {
		if qm.ModifyQuery(req, qs) {
			modified = true
		}
	}

	return modified
}

// ModifyRes modifies a response. Returns true if the response was modified.
func (rm *Rule) ModifyRes(res *http.Response) (modified bool, err error) {
	for _, modifier := range rm.ActionModifiers() {
		m, err := modifier.ModifyRes(res)
		if err != nil {
			return false, fmt.Errorf("modify response: %w", err)
		}
		if m {
			modified = true
		}
	}

	return modified, nil
}
