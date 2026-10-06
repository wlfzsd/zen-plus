package rule

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
	// Important shows if rule has Important modifier.
	Important bool
	// All shows if rule has All modifier.
	// On primary rules, it implies Document. It is kept separate so that "@@...$all" exceptions,
	// which share this struct, still cancel non-document rules.
	All bool
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
	for _, m := range modifiers {
		if len(m) == 0 {
			return errors.New("empty modifier")
		}

		// Noop modifier is ignored
		if isNoopModifier(m) {
			continue
		}

		name, hasValue := cutModifierName(m)

		var modifier rulemodifiers.Modifier
		var isOr bool // true if the modifier belongs to ruleCondMods.or.

		if !hasValue {
			// Flag modifiers.
			switch name {
			case "document", "doc":
				rm.Document = true
				continue
			case "important":
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
				modifier = &rulemodifiers.ContentTypeModifier{}
				isOr = true
			case "third-party":
				modifier = &rulemodifiers.ThirdPartyModifier{}
			case "removeparam":
				modifier = &rulemodifiers.RemoveParamModifier{}
			case "all":
				// TODO: should also act as "popup" modifier once it gets implemented
				rm.All = true
				continue
			default:
				return fmt.Errorf("unknown modifier %q", m)
			}
		} else {
			// Parametrised modifiers.
			switch name {
			case "domain":
				modifier = &rulemodifiers.DomainModifier{}
			case "method":
				modifier = &rulemodifiers.MethodModifier{}
			case "removeparam":
				modifier = &rulemodifiers.RemoveParamModifier{}
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

		if err := modifier.Parse(m); err != nil {
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

// ShouldMatchReq returns true if the rule should match the request.
func (rm *Rule) ShouldMatchReq(req *http.Request) bool {
	return rm.ShouldMatchReqNav(req, req.Header.Get("Sec-Fetch-User") == "?1" && req.Header.Get("Sec-Fetch-Dest") == "document")
}

// ShouldMatchReqNav is ShouldMatchReq with the user-navigation guard
// (Sec-Fetch-User=?1 + Sec-Fetch-Dest=document, upstream #257 semantics)
// precomputed by the caller, so evaluating many candidate rules against one
// request does not repeat the two header reads per rule.
func (rm *Rule) ShouldMatchReqNav(req *http.Request, isUserNav bool) bool {
	if isUserNav && !rm.Document && !rm.All {
		return false
	}

	return rm.ModifiersMatchReq(req)
}

// ModifiersMatchReq returns true if the rule's matching modifiers match the request.
func (rm *Rule) ModifiersMatchReq(req *http.Request) bool {
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

// ModifyReqQuery modifies a request query. Returns true if the query was modified.
func (rm *Rule) ModifyReqQuery(query url.Values) (modified bool) {
	for _, qm := range rm.QueryModifiers() {
		if qm.ModifyQuery(query) {
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
