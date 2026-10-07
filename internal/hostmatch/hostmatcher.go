package hostmatch

import (
	"errors"
	"strings"
	"sync"
)

var (
	errNoEmptyPattern = errors.New("empty patterns are not allowed")
)

// IsGenericPatternSet reports whether a cosmetic-rule hostname-pattern list
// makes the rule "generic" in the AdGuard sense (docs
// #exception-modifiers-generic-rules, lines 1316-1339): the rule is not
// limited to specific domains. Generic are: no patterns at all, only the
// wildcard "*", or only negated patterns. Any positive pattern other than
// the bare "*" limits the rule (this includes "*.example.com" and
// "example.*"). 2026-10-08 (B2): used by $generichide/$specifichide.
func IsGenericPatternSet(hostnamePatterns string) bool {
	if len(hostnamePatterns) == 0 {
		return true
	}
	for _, pattern := range strings.Split(hostnamePatterns, ",") {
		pattern = strings.TrimSpace(pattern)
		if len(pattern) == 0 {
			continue
		}
		if pattern[0] == '~' {
			continue
		}
		if pattern != "*" {
			return false
		}
	}
	return true
}

// [B4 2026-10-08] ExclusionMatcher reports whether an exception value
// excludes a rule value. When a HostMatcher is created with
// NewHostMatcherWithExclusionMatcher, exception filtering consults it
// instead of plain equality, so an exception can suppress a superset of
// values (e.g. a name-only scriptlet exception suppressing every
// same-name scriptlet regardless of arguments).
type ExclusionMatcher[T any] func(ex, item T) bool

type hostnameStore[T any] interface {
	Add(hostnamePattern string, data T)
	Get(hostname string) []T
}

// HostMatcher matches rules against hostnames, supporting wildcards and exceptions.
// It is safe for concurrent use.
type HostMatcher[T comparable] struct {
	// mu protects generic and genericExceptions slices.
	mu                sync.RWMutex
	generic           []T
	genericExceptions []T
	primaryStore      hostnameStore[T]
	exceptionStore    hostnameStore[T]
	// exclusionMatcher is nil for matchers created with NewHostMatcher, in
	// which case exceptions exclude by plain equality as before.
	exclusionMatcher ExclusionMatcher[T]
}

func NewHostMatcher[T comparable]() *HostMatcher[T] {
	return &HostMatcher[T]{
		primaryStore:   newTrieStore[T](),
		exceptionStore: newTrieStore[T](),
	}
}

// NewHostMatcherWithExclusionMatcher behaves like NewHostMatcher, but
// exception filtering consults match instead of plain equality.
func NewHostMatcherWithExclusionMatcher[T comparable](match ExclusionMatcher[T]) *HostMatcher[T] {
	return &HostMatcher[T]{
		primaryStore:     newTrieStore[T](),
		exceptionStore:   newTrieStore[T](),
		exclusionMatcher: match,
	}
}

func (hm *HostMatcher[T]) AddPrimaryRule(hostnamePatterns string, data T) error {
	if len(hostnamePatterns) == 0 {
		hm.mu.Lock()
		hm.generic = append(hm.generic, data)
		hm.mu.Unlock()
		return nil
	}

	patterns := strings.Split(hostnamePatterns, ",")
	for _, pattern := range patterns {
		if len(pattern) == 0 {
			return errNoEmptyPattern
		}
	}
	for _, pattern := range patterns {
		if pattern[0] == '~' {
			pattern = pattern[1:]
			hm.exceptionStore.Add(pattern, data)
			if !strings.HasPrefix(pattern, "*.") {
				hm.exceptionStore.Add("*."+pattern, data)
			}
			continue
		}

		hm.primaryStore.Add(pattern, data)
		if !strings.HasPrefix(pattern, "*.") {
			hm.primaryStore.Add("*."+pattern, data)
		}
	}

	return nil
}

func (hm *HostMatcher[T]) AddExceptionRule(hostnamePatterns string, data T) error {
	if len(hostnamePatterns) == 0 {
		hm.mu.Lock()
		hm.genericExceptions = append(hm.genericExceptions, data)
		hm.mu.Unlock()
		return nil
	}

	patterns := strings.Split(hostnamePatterns, ",")
	for _, pattern := range patterns {
		if len(pattern) == 0 {
			return errNoEmptyPattern
		}

		hm.exceptionStore.Add(pattern, data)
		if !strings.HasPrefix(pattern, "*.") {
			hm.exceptionStore.Add("*."+pattern, data)
		}
	}

	return nil
}

func (hm *HostMatcher[T]) Get(hostname string) []T {
	primary := hm.primaryStore.Get(hostname)
	exceptions := hm.exceptionStore.Get(hostname)

	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if len(hm.genericExceptions) == 0 && len(exceptions) == 0 {
		// Optimize the most common case.
		res := make([]T, len(hm.generic)+len(primary))
		copy(res, hm.generic)
		copy(res[len(hm.generic):], primary)
		return res
	}

	// [B4 2026-10-08] Custom exception matching, e.g. scriptlet argument-list
	// prefix matching; equality-based matching below is unchanged.
	if hm.exclusionMatcher != nil {
		var res []T
		for _, r := range hm.generic {
			if !hm.isExcludedWithMatcher(r, exceptions) {
				res = append(res, r)
			}
		}
		for _, r := range primary {
			if !hm.isExcludedWithMatcher(r, exceptions) {
				res = append(res, r)
			}
		}
		return res
	}

	exceptionMap := make(map[T]struct{}, len(hm.genericExceptions)+len(exceptions))
	for _, ex := range hm.genericExceptions {
		exceptionMap[ex] = struct{}{}
	}
	for _, ex := range exceptions {
		exceptionMap[ex] = struct{}{}
	}

	var res []T
	for _, r := range hm.generic {
		if _, excluded := exceptionMap[r]; !excluded {
			res = append(res, r)
		}
	}
	for _, r := range primary {
		if _, excluded := exceptionMap[r]; !excluded {
			res = append(res, r)
		}
	}

	return res
}

// isExcludedWithMatcher reports whether item is excluded by any exception,
// consulting the custom matcher. hm.mu must be held.
func (hm *HostMatcher[T]) isExcludedWithMatcher(item T, exceptions []T) bool {
	for _, ex := range hm.genericExceptions {
		if hm.exclusionMatcher(ex, item) {
			return true
		}
	}
	for _, ex := range exceptions {
		if hm.exclusionMatcher(ex, item) {
			return true
		}
	}
	return false
}
