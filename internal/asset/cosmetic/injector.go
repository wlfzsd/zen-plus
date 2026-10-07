package cosmetic

import (
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
	primaryRuleRegex   = regexp.MustCompile(`(.*?)##(.*)`)
	exceptionRuleRegex = regexp.MustCompile(`(.*?)#@#(.+)`)
)

type store interface {
	AddPrimaryRule(hostnamePatterns string, selector string) error
	AddExceptionRule(hostnamePatterns string, selector string) error
	Get(hostname string) []string
}

type Injector struct {
	store store

	// classification (2026-10-08 B2): selectors seen in generic vs specific
	// rules, used by the $generichide/$specifichide exemptions. Written at
	// rule-load time and read at serve time, guarded like the store.
	mu       sync.RWMutex
	generic  map[string]struct{}
	specific map[string]struct{}
}

func NewInjector() *Injector {
	return &Injector{
		store: hostmatch.NewHostMatcher[string](),
	}
}

func (inj *Injector) AddRule(rule string) error {
	if match := primaryRuleRegex.FindStringSubmatch(rule); match != nil {
		css, err := sanitizeCSSSelector(match[2])
		if err != nil {
			return fmt.Errorf("sanitize css selector: %w", err)
		}
		inj.classify(match[1], css)
		if err := inj.store.AddPrimaryRule(match[1], css); err != nil {
			return fmt.Errorf("add primary rule: %w", err)
		}
		return nil
	}

	if match := exceptionRuleRegex.FindStringSubmatch(rule); match != nil {
		// #@# exceptions only remove selectors; no classification needed.
		if err := inj.store.AddExceptionRule(match[1], match[2]); err != nil {
			return fmt.Errorf("add exception rule: %w", err)
		}
		return nil
	}

	return errors.New("unsupported syntax")
}

// classify records whether a selector instance is generic or specific
// (2026-10-08 B2). A selector can legitimately appear in both classes.
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

// GetAsset returns the CSS asset for the given hostname.
//
// 2026-10-08 (B2): ex carries the $elemhide/$generichide/$specifichide
// exemptions. Elemhide clears everything; generichide keeps only specific
// selectors; specifichide keeps only generic ones. A selector present in
// both classes survives both filters (the other instance still applies).
func (inj *Injector) GetAsset(hostname string, ex ...exemption.Exemption) []byte {
	if len(ex) > 0 && ex[0].Elemhide {
		return nil
	}
	selectors := inj.store.Get(hostname)
	if len(ex) > 0 && (ex[0].Generichide || ex[0].Specifichide) {
		selectors = inj.filterHide(selectors, ex[0])
	}
	log.Printf("got %d cosmetic rules for %q", len(selectors), redacted.Redacted(hostname))
	if len(selectors) == 0 {
		return nil
	}

	stylesheet := generateBatchedCSS(selectors)
	return []byte(stylesheet)
}

// filterHide applies the $generichide (drop generic) / $specifichide (drop
// specific) exemptions (2026-10-08 B2, docs 1316-1339).
func (inj *Injector) filterHide(selectors []string, ex exemption.Exemption) []string {
	inj.mu.RLock()
	defer inj.mu.RUnlock()
	out := selectors[:0]
	for _, s := range selectors {
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

func generateBatchedCSS(selectors []string) string {
	const batchSize = 100

	var builder strings.Builder
	for i := 0; i < len(selectors); i += batchSize {
		end := i + batchSize
		if end > len(selectors) {
			end = len(selectors)
		}
		batch := selectors[i:end]

		joinedSelectors := strings.Join(batch, ",")
		fmt.Fprintf(&builder, "%s{display:none!important;}", joinedSelectors)
	}

	return builder.String()
}
