package networkrules

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

var (
	reHosts       = regexp.MustCompile(`^(?:0\.0\.0\.0|127\.0\.0\.1)\s(.+)`)
	reHostsIgnore = regexp.MustCompile(`^(?:0\.0\.0\.0|broadcasthost|local|localhost(?:\.localdomain)?|ip6-\w+)$`)
)

func (nr *NetworkRules) ParseRule(rawRule string, filterName *string) (isException bool, err error) {
	if matches := reHosts.FindStringSubmatch(rawRule); matches != nil {
		hostsField := matches[1]
		if commentIndex := strings.IndexByte(hostsField, '#'); commentIndex != -1 {
			hostsField = hostsField[:commentIndex]
		}

		// An IP address may be followed by multiple hostnames.
		//
		// As stated in https://man.freebsd.org/cgi/man.cgi?hosts(5):
		// "Items are separated by any number of blanks and/or tab characters."
		hosts := strings.Fields(hostsField)

		for _, host := range hosts {
			if reHostsIgnore.MatchString(host) {
				continue
			}

			pattern := fmt.Sprintf("||%s^", host)
			if err := nr.primaryStore.Insert(pattern, &rule.Rule{
				RawRule:    pattern + "$document",
				FilterName: filterName,
				Document:   true,
			}); err != nil {
				return false, fmt.Errorf("insert hosts rule: %w", err)
			}
		}

		return false, nil
	}

	if strings.HasPrefix(rawRule, "@@") {
		r := &exceptionrule.ExceptionRule{
			Rule: rule.Rule{
				RawRule:    rawRule,
				FilterName: filterName,
			},
		}

		pattern, modifiers := parseRuleParts(rawRule[2:])
		if err := validateDenyAllowPattern(pattern, modifiers); err != nil {
			return false, err
		}
		if modifiers != nil {
			// 2026-10-08 (B1): AdGuard restriction on the $all modifier:
			// "This modifier cannot be used as an exception with the @@ mark"
			// (create-own-filters doc, $all Restrictions). Previously @@$all
			// was accepted as a full-cancel exception.
			if err := checkExceptionModifiers(modifiers); err != nil {
				return false, fmt.Errorf("parse modifiers: %v", err)
			}
			if err := r.ParseModifiers(modifiers); err != nil {
				return false, fmt.Errorf("parse modifiers: %v", err)
			}
		}
		// $badfilter exception rules are collected instead of inserted
		// (2026-10-08 B6); they disable exception rules by exact text
		// match in Compact, after every list was loaded (doc 1482:
		// "@@||example.com$badfilter disables @@||example.com").
		// Parity check for exception path: RawRule keeps the '@@' prefix
		// while parseRuleParts strips it (it operates on rawRule[2:]), so
		// the collected text must re-attach '@@' — the full-text
		// comparison runs against stored RawRules.
		if r.Badfilter {
			nr.addBadfilter("@@"+pattern, modifiers)
			return true, nil
		}
		if err := nr.exceptionStore.Insert(pattern, r); err != nil {
			return false, fmt.Errorf("insert exception rule: %w", err)
		}

		// 2026-10-08 (B2): track exceptions whose effects reach the whole
		// page so ModifyReq/ModifyRes can skip the Referer lookup when no
		// such exception exists.
		if r.Document || r.URLBlock || r.Genericblock || r.Content {
			nr.pageScopedExceptions.Add(1)
		}

		return true, nil
	}

	// AdGuard: "Rules shorter than 4 characters are considered incorrect
	// and will be ignored" (doc lines 285-289; 2026-10-08 B6). Hosts lines
	// and exception rules are handled above and keep their behavior; the
	// boundary is exact (4 characters are accepted, fewer are not).
	if utf8.RuneCountInString(rawRule) < 4 {
		return false, fmt.Errorf("rule shorter than 4 characters: %q", rawRule)
	}

	r := &rule.Rule{
		RawRule:    rawRule,
		FilterName: filterName,
	}

	pattern, modifiers := parseRuleParts(rawRule)
	if err := validateDenyAllowPattern(pattern, modifiers); err != nil {
		return false, err
	}
	if modifiers != nil {
		if err := r.ParseModifiers(modifiers); err != nil {
			return false, fmt.Errorf("parse modifiers: %v", err)
		}
	}
	// $badfilter rules are collected instead of inserted (2026-10-08 B6):
	// they never block by themselves and are applied once, post-load, in
	// Compact (a badfilter rule may precede its target in load order).
	if r.Badfilter {
		nr.addBadfilter(pattern, modifiers)
		return false, nil
	}
	// Frame-scoped action rules ($permissions, docs 2321) enable the
	// ModifyRes Sec-Fetch-Dest gate; counting at insert keeps the gate
	// free while no such rule is loaded (2026-10-08 B9).
	if r.FrameScoped {
		nr.frameScopedRules.Add(1)
	}
	// 2026-10-08 (B10-A): empty-pattern rules whose effects are entirely
	// $cookie/$removeparam route out of the generic bucket into the
	// action index, so the hot paths evaluate only the applicable ones
	// (actionindex.go). The route point sits after $badfilter collection
	// and before Insert; mixed-action rules stay in the store unchanged.
	if nr.actionIdxOn && pattern == "" && routeableMatchAllAction(r) {
		nr.actionIdx.addRule(r)
		return false, nil
	}
	if err := nr.primaryStore.Insert(pattern, r); err != nil {
		return false, fmt.Errorf("insert rule: %w", err)
	}

	return false, nil
}

// checkExceptionModifiers rejects modifiers that AdGuard forbids in
// exception rules: "$all cannot be used as an exception with the @@ mark"
// (create-own-filters doc, $all modifier Restrictions). 2026-10-08 (B1).
func checkExceptionModifiers(modifiers []string) error {
	for _, m := range modifiers {
		if m == "all" {
			return errors.New(`$all cannot be used as an exception`)
		}
	}
	return nil
}

// validateDenyAllowPattern enforces the $denyallow restriction that the
// rule's matching pattern cannot target specific domains, e.g. it cannot
// start with "||" (AdGuard docs §$denyallow, lines 509-515; rules
// violating it are considered invalid). (2026-10-08, B5)
func validateDenyAllowPattern(pattern string, modifiers []string) error {
	if !strings.HasPrefix(pattern, "||") {
		return nil
	}
	for _, m := range modifiers {
		name, _, _ := strings.Cut(m, "=")
		name = strings.TrimPrefix(name, "~")
		if name == "denyallow" {
			return errors.New("denyallow modifier cannot be used in a pattern-targeted rule")
		}
	}
	return nil
}

// parseRuleParts splits rawRule into its pattern and modifier list.
func parseRuleParts(rawRule string) (pattern string, modifiers []string) {
	if pattern, modifiers, ok := parseRegexpRuleParts(rawRule); ok {
		return pattern, modifiers
	}

	pattern, rawModifiers, found := strings.Cut(rawRule, "$")
	if found {
		modifiers = splitModifiers(rawModifiers)
	}
	return pattern, modifiers
}

// parseRegexpRuleParts splits a slash-delimited regexp rule into its pattern and modifier list.
// Reports ok only if the rule looks like a regexp rule.
func parseRegexpRuleParts(rawRule string) (pattern string, modifiers []string, ok bool) {
	if len(rawRule) < 2 || rawRule[0] != '/' {
		return "", nil, false
	}

	end := regexpPatternEnd(rawRule)
	if end == -1 {
		return "", nil, false
	}

	pattern = rawRule[:end+1]
	if end+1 < len(rawRule) {
		modifiers = splitModifiers(rawRule[end+2:])
	}
	return pattern, modifiers, true
}

func regexpPatternEnd(s string) int {
	escaped := false
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			escaped = !escaped
		case '/':
			if escaped {
				escaped = false
				continue
			}
			if i+1 == len(s) || s[i+1] == '$' { // End-of-string or modifier delimiter.
				return i
			}
		default:
			escaped = false
		}
	}
	return -1
}

// splitModifiers splits by unescaped commas.
// Empty fields are preserved (like strings.Split).
func splitModifiers(s string) []string {
	var res []string
	var b strings.Builder
	escaped := false

	for _, r := range s {
		switch r {
		case '\\':
			if escaped {
				b.WriteRune('\\')
			}
			escaped = !escaped
		case ',':
			if escaped {
				b.WriteRune(',')
				escaped = false
			} else {
				res = append(res, b.String())
				b.Reset()
			}
		default:
			if escaped {
				b.WriteRune('\\')
				escaped = false
			}
			b.WriteRune(r)
		}
	}

	if escaped {
		b.WriteRune('\\')
	}
	res = append(res, b.String())
	return res
}
