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
	entries  []domainModifierEntry
	inverted bool
}

var _ ConditionModifier = (*DomainModifier)(nil)

func (m *DomainModifier) Parse(modifier string) error {
	eqIndex := strings.IndexByte(modifier, '=')
	if eqIndex == -1 || eqIndex == len(modifier)-1 {
		return errors.New("invalid domain modifier")
	}
	value := modifier[eqIndex+1:]

	m.inverted = strings.HasPrefix(value, "~")
	matches := domainModifierRegex.FindAllString(value, -1)
	m.entries = make([]domainModifierEntry, len(matches))
	for i, entry := range matches {
		inverted := len(entry) > 0 && entry[0] == '~'
		if inverted != m.inverted {
			return errors.New("cannot mix inverted and non-inverted method modifiers")
		}
		if inverted {
			entry = entry[1:]
		}

		m.entries[i] = domainModifierEntry{}
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
	var hostname string
	referer := req.Header.Get("Referer")
	// Allow empty "Referer" header to make inverted rules work.
	if referer != "" {
		host, ok := refererHostname(referer)
		if !ok {
			return false
		}
		hostname = host
	} else {
		hostname = req.URL.Hostname()
	}

	matches := false
	for _, entry := range m.entries {
		if entry.MatchDomain(hostname) {
			matches = true
			break
		}
	}
	if m.inverted {
		return !matches
	}
	return matches
}

func (m *DomainModifier) ShouldMatchRes(_ *http.Response) bool {
	return false
}

type domainModifierEntry struct {
	regular string
	tld     string
	regexp  *regexp.Regexp
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
	if !ok || len(m.entries) != len(other.entries) || m.inverted != other.inverted {
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
	if a.regular != b.regular || a.tld != b.tld {
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
