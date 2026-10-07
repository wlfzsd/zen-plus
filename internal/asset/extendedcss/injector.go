package extendedcss

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"

	"github.com/irbis-sh/zen-desktop/internal/exemption"
	"github.com/irbis-sh/zen-desktop/internal/hostmatch"
	"github.com/irbis-sh/zen-desktop/internal/redacted"
)

var (
	primaryRuleRegex   = regexp.MustCompile(`(.+?)#\??#(.+)`)
	exceptionRuleRegex = regexp.MustCompile(`(.+?)#@\??#(.+)`)

	//go:embed bundle.js
	defaultExtendedCSSBundle []byte
)

type store interface {
	AddPrimaryRule(hostnamePatterns string, body string) error
	AddExceptionRule(hostnamePatterns string, body string) error
	Get(hostname string) []string
}

// Injector injects extended CSS rules into HTML HTTP responses.
type Injector struct {
	// bundle contains the extended CSS JS bundle.
	bundle []byte
	// store stores and retrieves extended CSS rules by hostname.
	store store

	// classification (2026-10-08 B2): see cosmetic.Injector.
	mu       sync.RWMutex
	generic  map[string]struct{}
	specific map[string]struct{}
}

func NewInjectorWithDefaults() (*Injector, error) {
	store := hostmatch.NewHostMatcher[string]()
	return newInjector(defaultExtendedCSSBundle, store)
}

func newInjector(bundleData []byte, store store) (*Injector, error) {
	if bundleData == nil {
		return nil, errors.New("bundleData is nil")
	}
	if store == nil {
		return nil, errors.New("store is nil")
	}

	return &Injector{
		bundle: bundleData,
		store:  store,
	}, nil
}

// AddRule adds an extended CSS rule to the injector.
func (inj *Injector) AddRule(rule string) error {
	if match := primaryRuleRegex.FindStringSubmatch(rule); match != nil {
		hostnamePatters := match[1]
		selector := match[2]
		inj.classify(hostnamePatters, selector)
		if err := inj.store.AddPrimaryRule(hostnamePatters, selector); err != nil {
			return fmt.Errorf("add primary rule: %v", err)
		}
		return nil
	} else if match := exceptionRuleRegex.FindStringSubmatch(rule); match != nil {
		hostnamePatterns := match[1]
		selector := match[2]
		// #@?# exceptions only remove selectors; no classification needed.
		if err := inj.store.AddExceptionRule(hostnamePatterns, selector); err != nil {
			return fmt.Errorf("add exception rule: %v", err)
		}
		return nil
	}
	return errors.New("unknown rule format")
}

// classify records whether an extended-css selector instance is generic or
// specific (2026-10-08 B2). An instance can appear in both classes.
func (inj *Injector) classify(hostnamePatterns, selector string) {
	generic := hostmatch.IsGenericPatternSet(hostnamePatterns)
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if inj.generic == nil {
		inj.generic = make(map[string]struct{})
	}
	if inj.specific == nil {
		inj.specific = make(map[string]struct{})
	}
	if generic {
		inj.generic[selector] = struct{}{}
	} else {
		inj.specific[selector] = struct{}{}
	}
}

// GetAsset returns the JS asset for the given hostname.
//
// 2026-10-08 (B2): ex carries the $elemhide/$generichide/$specifichide
// exemptions (extended CSS is part of the elemhide/generichide/specifichide
// surface; see cosmetic.Injector for the filtering semantics).
func (inj *Injector) GetAsset(hostname string, ex ...exemption.Exemption) ([]byte, error) {
	if len(ex) > 0 && ex[0].Elemhide {
		return nil, nil
	}
	rules := inj.store.Get(hostname)
	if len(ex) > 0 && (ex[0].Generichide || ex[0].Specifichide) {
		rules = inj.filterHide(rules, ex[0])
	}
	log.Printf("got %d extended-css rules for %q", len(rules), redacted.Redacted(hostname))
	if len(rules) == 0 {
		return nil, nil
	}

	joined := strings.Join(rules, "\n")
	encodedRules, err := json.Marshal(joined)
	if err != nil {
		return nil, fmt.Errorf("encode rules: %v", err)
	}

	var injection bytes.Buffer
	// The wrapping IIFE keeps the bundle's top-level var from becoming a window
	// property that page scripts could probe for.
	injection.WriteString("(()=>{")
	injection.Write(inj.bundle)
	injection.WriteString("\nextendedCSS(")
	injection.Write(encodedRules)
	injection.WriteString(")})();")

	return injection.Bytes(), nil
}

// filterHide applies the $generichide / $specifichide exemptions
// (2026-10-08 B2).
func (inj *Injector) filterHide(items []string, ex exemption.Exemption) []string {
	inj.mu.RLock()
	defer inj.mu.RUnlock()
	out := items[:0]
	for _, s := range items {
		_, g := inj.generic[s]
		_, sp := inj.specific[s]
		if ex.Generichide && g && !sp {
			continue
		}
		if ex.Specifichide && sp && !g {
			continue
		}
		out = append(out, s)
	}
	return out
}
