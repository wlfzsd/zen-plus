package cssrule

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
	RuleRegex          = regexp.MustCompile(`.*#@?\$#.+`)
	primaryRuleRegex   = regexp.MustCompile(`(.*?)#\$#(.*)`)
	exceptionRuleRegex = regexp.MustCompile(`(.*?)#@\$#(.+)`)
)

type store interface {
	AddPrimaryRule(hostnamePatterns string, css string) error
	AddExceptionRule(hostnamePatterns string, css string) error
	Get(hostname string) []string
}

type Injector struct {
	store store

	// classification (2026-10-08 B2): see cosmetic.Injector.
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
		inj.classify(match[1], match[2])
		if err := inj.store.AddPrimaryRule(match[1], match[2]); err != nil {
			return fmt.Errorf("add primary rule: %w", err)
		}
		return nil
	}

	if match := exceptionRuleRegex.FindStringSubmatch(rule); match != nil {
		// #@$# exceptions only remove css rules; no classification needed.
		if err := inj.store.AddExceptionRule(match[1], match[2]); err != nil {
			return fmt.Errorf("add exception rule: %w", err)
		}
		return nil
	}

	return errors.New("unsupported syntax")
}

// classify records whether a css rule instance is generic or specific
// (2026-10-08 B2). An instance can legitimately appear in both classes.
func (inj *Injector) classify(hostnamePatterns, css string) {
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
		inj.generic[css] = struct{}{}
	} else {
		inj.specific[css] = struct{}{}
	}
}

// GetAsset returns the CSS asset for the given hostname.
//
// 2026-10-08 (B2): ex carries the $elemhide/$generichide/$specifichide
// exemptions (see cosmetic.Injector for the filtering semantics).
func (inj *Injector) GetAsset(hostname string, ex ...exemption.Exemption) []byte {
	if len(ex) > 0 && ex[0].Elemhide {
		return nil
	}
	cssRules := inj.store.Get(hostname)
	if len(ex) > 0 && (ex[0].Generichide || ex[0].Specifichide) {
		cssRules = inj.filterHide(cssRules, ex[0])
	}
	log.Printf("got %d css rules for %q", len(cssRules), redacted.Redacted(hostname))
	if len(cssRules) == 0 {
		return nil
	}

	stylesheet := strings.Join(cssRules, "")
	return []byte(stylesheet)
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
