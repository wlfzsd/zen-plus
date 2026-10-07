package rulemodifiers

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/net/publicsuffix"
)

var (
	// domainModifierRegex matches domain modifier entries.
	//
	// The need for this regex comes from the fact that domain modifiers can contain regular expressions,
	// which can contain the separator character (|). This makes it impossible to just split the modifier by the separator.
	domainModifierRegex = regexp.MustCompile(`~?((/.*/)|[^|]+)+`)
)

type DomainModifier struct {
	entries []domainModifierEntry

	// MatchTargetDomain enables the AdGuard "$domain modifier matching
	// target domain" behavior (docs §$domain, lines 566-585): when the
	// host rule carries $cookie, $csp, $permissions or $removeparam, the
	// modifier also matches the request's target hostname instead of only
	// the referrer. rule.ParseModifiers sets it after the whole rule is
	// parsed, because $domain may precede the linking modifier in the
	// rule text.
	//
	// It is deliberately NOT compared by Cancels: the flag derives from
	// the rule's other modifiers, and exception-vs-rule cancellation is
	// already gated per request by ShouldMatchReq, so comparing it would
	// needlessly break subset exceptions. (2026-10-08, B5)
	MatchTargetDomain bool
}

var _ ConditionModifier = (*DomainModifier)(nil)

// Inverted reports whether the modifier's value consists of negated domain
// entries (2026-10-08 B6). Used by $badfilter partial disablement, which is
// only allowed for non-negated $domain values (doc 1487-1488, 1499).
// 2026-10-08 (merge): under B5's per-entry negation a value may mix
// positive and negated entries; any negated entry makes the value negative
// in the doc-1499 sense, so it opts out of partial disablement — the same
// outcome B6's sandbox produced, where a mixed value failed to parse.
func (m *DomainModifier) Inverted() bool {
	for i := range m.entries {
		if m.entries[i].inverted {
			return true
		}
	}
	return false
}

// MatchHost reports whether any stored domain entry matches host (2026-10-08
// B6). It reuses the exact entry matching of ShouldMatchReq — regular
// entries match the host itself or any of its subdomains, "example.*"
// entries match by effective TLD, regexp entries by regexp — without the
// referer fallback. Used by $badfilter partial disablement.
func (m *DomainModifier) MatchHost(host string) bool {
	for _, entry := range m.entries {
		if entry.MatchDomain(host) {
			return true
		}
	}
	return false
}

// RefererHostname returns the hostname of the referer URL (cached). ok is
// false when url.Parse rejected the string. Exported for the $badfilter
// partial-disablement check, which selects the effective domain the same
// way DomainModifier.ShouldMatchReq does (2026-10-08 B6).
func RefererHostname(referer string) (host string, ok bool) {
	return refererHostname(referer)
}

// HasPermittedDomains reports whether the modifier positively permits any
// domain. Under the AdGuard generic-rule definition (docs lines 1316-1339)
// a rule limited only by negated domains ($domain=~example.com) is still
// generic. 2026-10-08 (B2). 2026-10-08 (merge): expressed over B5's
// per-entry negation — at least one non-negated entry.
func (m *DomainModifier) HasPermittedDomains() bool {
	for i := range m.entries {
		if !m.entries[i].inverted {
			return true
		}
	}
	return false
}

func (m *DomainModifier) Parse(modifier string) error {
	eqIndex := strings.IndexByte(modifier, '=')
	if eqIndex == -1 || eqIndex == len(modifier)-1 {
		return errors.New("invalid domain modifier")
	}
	value := modifier[eqIndex+1:]

	// Every entry carries its own negation (AdGuard docs §$domain syntax
	// and "and negation ~" examples, lines 533-564): e.g.
	// $domain=example.org|~foo.example.org or $domain=~a.com|~b.com|~/re/.
	// (2026-10-08, B5)
	matches := domainModifierRegex.FindAllString(value, -1)
	m.entries = make([]domainModifierEntry, len(matches))
	for i, entry := range matches {
		inverted := len(entry) > 0 && entry[0] == '~'
		if inverted {
			entry = entry[1:]
		}

		m.entries[i] = domainModifierEntry{inverted: inverted}
		if err := m.entries[i].Parse(entry); err != nil {
			return fmt.Errorf("parse entry %q: %w", entry, err)
		}
	}
	return nil
}

// refererHostEntry is one cached url.Parse result. ok=false caches the
// negative outcome of a failed parse, preserving ShouldMatchReq's semantics
// of not matching unparseable referers.
type refererHostEntry struct {
	host string
	ok   bool
}

// refererHostCacheLimit bounds the cache; on overflow the whole map is
// dropped and rebuilt on demand (2026-10-05).
const refererHostCacheLimit = 4096

// refererHostCache maps a raw Referer header value to the hostname that
// url.Parse(referer).Hostname() returns for it. The mapping is a pure
// function of the string, so entries never go stale and no invalidation is
// needed.
var (
	refererHostCacheMu sync.RWMutex
	refererHostCache   = make(map[string]refererHostEntry)
)

// refererHostname returns the hostname of the referer URL. ok is false when
// url.Parse rejected the string; the caller must then not match.
func refererHostname(referer string) (host string, ok bool) {
	refererHostCacheMu.RLock()
	e, hit := refererHostCache[referer]
	refererHostCacheMu.RUnlock()
	if hit {
		return e.host, e.ok
	}

	u, err := url.Parse(referer)
	if err == nil {
		e = refererHostEntry{host: u.Hostname(), ok: true}
	}
	refererHostCacheMu.Lock()
	if len(refererHostCache) >= refererHostCacheLimit {
		refererHostCache = make(map[string]refererHostEntry)
	}
	refererHostCache[referer] = e
	refererHostCacheMu.Unlock()
	return e.host, e.ok
}

func (m *DomainModifier) ShouldMatchReq(req *http.Request) bool {
	referer := req.Header.Get("Referer")
	var refHostname string
	// Allow empty "Referer" header to make inverted rules work.
	if referer != "" {
		host, ok := refererHostname(referer)
		if !ok {
			return false
		}
		refHostname = host
	} else {
		refHostname = req.URL.Hostname()
	}

	if m.MatchTargetDomain {
		// The rule carries $cookie/$csp/$permissions/$removeparam: the
		// modifier also matches the target hostname, but a referrer that
		// is explicitly excluded by a negated entry vetoes the rule
		// (AdGuard docs §$domain, lines 570-585). (2026-10-08, B5)
		if m.matchRestricted(refHostname) {
			return false
		}
		return m.matchHost(refHostname) || m.matchHost(req.URL.Hostname())
	}

	return m.matchHost(refHostname)
}

// matchHost reports whether hostname satisfies the modifier's entry list:
// no negated entry may hit, and at least one non-negated entry must hit —
// vacuously true when the list holds only negated entries ("any domain
// except ..."). This is AdGuard's per-entry negation semantics; negated
// entries always win, so a positive hit does not end the scan — later
// negated entries can still veto. (2026-10-08, B5)
func (m *DomainModifier) matchHost(hostname string) bool {
	hasPermitted := false
	permitted := false
	for i := range m.entries {
		e := &m.entries[i]
		if e.inverted {
			if e.MatchDomain(hostname) {
				return false
			}
			continue
		}
		hasPermitted = true
		if !permitted && e.MatchDomain(hostname) {
			permitted = true
		}
	}
	return permitted || !hasPermitted
}

// matchRestricted reports whether any negated entry hits hostname, i.e.
// the hostname is explicitly excluded by the modifier. (2026-10-08, B5)
func (m *DomainModifier) matchRestricted(hostname string) bool {
	for i := range m.entries {
		if m.entries[i].inverted && m.entries[i].MatchDomain(hostname) {
			return true
		}
	}
	return false
}

func (m *DomainModifier) ShouldMatchRes(_ *http.Response) bool {
	return false
}

type domainModifierEntry struct {
	regular string
	tld     string
	regexp  *regexp.Regexp
	// inverted marks a negated ("~"-prefixed) entry. (2026-10-08, B5)
	inverted bool
}

func (m *domainModifierEntry) Parse(entry string) error {
	if len(entry) == 0 {
		return errors.New("entry is empty")
	}

	var err error
	if m.regexp, err = parseRegexp(entry); err != nil {
		return fmt.Errorf("parse regexp: %w", err)
	} else if m.regexp != nil {
		return nil
	}

	if strings.HasSuffix(entry, ".*") {
		m.tld = strings.TrimSuffix(entry, ".*")
		if len(m.tld) == 0 {
			return errors.New("tld is empty")
		}
		return nil
	}

	m.regular = entry
	return nil
}

// eTLD1CacheLimit bounds the effective-TLD-plus-one memo; on overflow the
// whole map is dropped and rebuilt on demand (2026-10-05).
const eTLD1CacheLimit = 4096

// eTLD1Cache maps a hostname to its effective TLD+1. The mapping is a pure
// function of the hostname, so entries never go stale; ok=false memoizes the
// negative outcome of EffectiveTLDPlusOne failing for that host.
var (
	eTLD1CacheMu sync.RWMutex
	eTLD1Cache   = make(map[string]struct {
		eTLD1 string
		ok    bool
	})
)

func effectiveTLDPlusOneCached(domain string) (string, bool) {
	eTLD1CacheMu.RLock()
	e, hit := eTLD1Cache[domain]
	eTLD1CacheMu.RUnlock()
	if hit {
		return e.eTLD1, e.ok
	}

	eTLD1, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err == nil {
		e.eTLD1, e.ok = eTLD1, true
	}
	eTLD1CacheMu.Lock()
	if len(eTLD1Cache) >= eTLD1CacheLimit {
		eTLD1Cache = make(map[string]struct {
			eTLD1 string
			ok    bool
		})
	}
	eTLD1Cache[domain] = e
	eTLD1CacheMu.Unlock()
	return e.eTLD1, e.ok
}

func (m *domainModifierEntry) MatchDomain(domain string) bool {
	switch {
	case m.regular != "":
		return m.regular == domain || strings.HasSuffix(domain, "."+m.regular)
	case m.tld != "":
		eTLD1, ok := effectiveTLDPlusOneCached(domain)
		if !ok {
			return false
		}

		dotIndex := strings.Index(eTLD1, ".")
		if dotIndex == -1 {
			return false
		}

		return eTLD1[:dotIndex] == m.tld
	case m.regexp != nil:
		return m.regexp.MatchString(domain)
	default:
		return false
	}
}

func (m *DomainModifier) Cancels(modifier Modifier) bool {
	other, ok := modifier.(*DomainModifier)
	// Entry-level negation is compared inside entryEqual, replacing the
	// former modifier-level m.inverted check. (2026-10-08, B5)
	if !ok || len(m.entries) != len(other.entries) {
		return false
	}

	used := make(map[int]struct{}, len(other.entries))

	for _, entry := range m.entries {
		matchFound := false
		for i, otherEntry := range other.entries {
			if _, alreadyUsed := used[i]; alreadyUsed {
				continue
			}
			if entryEqual(entry, otherEntry) {
				used[i] = struct{}{}
				matchFound = true
				break
			}
		}
		if !matchFound {
			return false
		}
	}

	return true
}

func entryEqual(a, b domainModifierEntry) bool {
	if a.inverted != b.inverted || a.regular != b.regular || a.tld != b.tld {
		return false
	}

	if a.regexp == nil && b.regexp == nil {
		return true
	}
	if a.regexp == nil || b.regexp == nil {
		return false
	}
	return a.regexp.String() == b.regexp.String()
}
