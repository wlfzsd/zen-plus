package rulemodifiers

import (
	"fmt"
	"net/http"
	"strings"
)

// StrictPartyModifier implements the $strict-first-party and
// $strict-third-party modifiers (AdGuard doc #strict-first-party-modifier,
// #strict-third-party-modifier, lines 759-801; 2026-10-08 B6).
//
// They work like $~third-party / $third-party but decide party-ness by the
// EXACT hostname equality of the referrer and the request target instead of
// the Sec-Fetch-Site header alone: a request from sub.domain.com to
// domain.com is strict-third-party even though it is same-site. Requests
// without a referrer are treated as first-party (doc 763), so
// $strict-first-party matches them and $strict-third-party does not.
type StrictPartyModifier struct {
	// firstParty is true for $strict-first-party / $strict1p and false for
	// $strict-third-party / $strict3p.
	firstParty bool
}

var _ ConditionModifier = (*StrictPartyModifier)(nil)

func (m *StrictPartyModifier) Parse(modifier string) error {
	switch modifier {
	case "strict-first-party", "strict1p":
		m.firstParty = true
	case "strict-third-party", "strict3p":
		m.firstParty = false
	default:
		return fmt.Errorf("unexpected strict-party modifier %q", modifier)
	}
	return nil
}

func (m *StrictPartyModifier) ShouldMatchReq(req *http.Request) bool {
	referer := req.Header.Get("Referer")
	if referer == "" {
		// Requests without a referrer are treated as first-party
		// requests (doc 763).
		return m.firstParty
	}
	host, ok := RefererHostname(referer)
	if !ok {
		// Unparseable referer: treat like no referrer (first-party).
		return m.firstParty
	}

	same := strings.EqualFold(host, req.URL.Hostname())
	if m.firstParty {
		return same
	}
	return !same
}

func (m *StrictPartyModifier) ShouldMatchRes(_ *http.Response) bool {
	// Same response-phase behavior as ThirdPartyModifier.
	return false
}

func (m *StrictPartyModifier) Cancels(modifier Modifier) bool {
	other, ok := modifier.(*StrictPartyModifier)
	if !ok {
		return false
	}
	return m.firstParty == other.firstParty
}
