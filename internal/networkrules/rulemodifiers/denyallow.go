package rulemodifiers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// DenyAllowModifier implements the $denyallow condition modifier
// (AdGuard docs §$denyallow, lines 481-524, 2026-10-08 B5): the rule
// matches only when the request's TARGET hostname is outside the
// modifier's domain list (each entry covers the domain and its
// subdomains). $denyallow matches target domains only, never referrers.
//
// The value accepts plain domains only; negated entries, wildcard TLDs
// and regular expressions make the rule invalid, as does a rule pattern
// starting with "||" (that check lives in networkrules.ParseRule, which
// sees the pattern). Escaping artifacts (backslash, comma) are rejected
// too: they cannot occur in a plain domain and would otherwise yield a
// silently dead entry.
type DenyAllowModifier struct {
	entries []string
}

var _ ConditionModifier = (*DenyAllowModifier)(nil)

func (m *DenyAllowModifier) Parse(modifier string) error {
	eqIndex := strings.IndexByte(modifier, '=')
	if eqIndex == -1 || eqIndex == len(modifier)-1 {
		return errors.New("invalid denyallow modifier")
	}
	value := modifier[eqIndex+1:]

	for _, entry := range strings.Split(value, "|") {
		if entry == "" {
			return errors.New("denyallow entry is empty")
		}
		if entry[0] == '~' {
			return fmt.Errorf("denyallow entry %q cannot be negated", entry)
		}
		if strings.ContainsAny(entry, `*/\,`) {
			return fmt.Errorf("denyallow entry %q cannot be a wildcard, regexp or escaping artifact", entry)
		}
		m.entries = append(m.entries, entry)
	}
	return nil
}

func (m *DenyAllowModifier) ShouldMatchReq(req *http.Request) bool {
	hostname := req.URL.Hostname()
	for _, entry := range m.entries {
		// Domain-and-its-subdomains matching, same shape as $domain
		// regular entries: the rule does not apply when the target
		// hostname is in the list.
		if entry == hostname || strings.HasSuffix(hostname, "."+entry) {
			return false
		}
	}
	return true
}

func (m *DenyAllowModifier) ShouldMatchRes(_ *http.Response) bool {
	return false
}

func (m *DenyAllowModifier) Cancels(modifier Modifier) bool {
	other, ok := modifier.(*DenyAllowModifier)
	if !ok || len(m.entries) != len(other.entries) {
		return false
	}
	for i := range m.entries {
		if m.entries[i] != other.entries[i] {
			return false
		}
	}
	return true
}
