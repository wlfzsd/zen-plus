package asset

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"

	"github.com/irbis-sh/zen-desktop/internal/asset/cosmetic"
	"github.com/irbis-sh/zen-desktop/internal/asset/cssrule"
	"github.com/irbis-sh/zen-desktop/internal/asset/extendedcss"
	"github.com/irbis-sh/zen-desktop/internal/asset/jsrule"
	"github.com/irbis-sh/zen-desktop/internal/asset/scriptlet"
	"github.com/irbis-sh/zen-desktop/internal/csp"
	"github.com/irbis-sh/zen-desktop/internal/exemption"
	"github.com/irbis-sh/zen-desktop/internal/httprewrite"
)

const (
	cosmeticCSSPath = "/cosmetic.css"
	cssRulePath     = "/cssrule.css"
	scriptletsPath  = "/scriptlets.js"
	extendedCSSPath = "/extendedcss.js"
	jsRulePath      = "/jsrule.js"
)

// Engine handles rule ingestion, HTML injection, and asset resolution.
type Engine struct {
	scriptlets  *scriptlet.Injector
	cosmetic    *cosmetic.Injector
	cssRules    *cssrule.Injector
	jsRules     *jsrule.Injector
	extendedCSS *extendedcss.Injector

	scriptletsURL  string
	jsRuleURL      string
	extendedCSSURL string
	cosmeticCSSURL string
	cssRuleCSSURL  string
}

// NewEngine constructs an Engine with default bundles and stores. host is the
// hostname the injected asset URLs point at; the caller must configure the
// proxy to answer for the same name, or every asset load ends in a dial that
// cannot resolve.
func NewEngine(host string) (*Engine, error) {
	if host == "" {
		return nil, errors.New("host must be set")
	}
	scriptlets, err := scriptlet.NewInjectorWithDefaults()
	if err != nil {
		return nil, fmt.Errorf("create scriptlets injector: %w", err)
	}
	extendedCSS, err := extendedcss.NewInjectorWithDefaults()
	if err != nil {
		return nil, fmt.Errorf("create extended css injector: %w", err)
	}
	base := "https://" + host

	return &Engine{
		scriptlets:  scriptlets,
		cosmetic:    cosmetic.NewInjector(),
		cssRules:    cssrule.NewInjector(),
		jsRules:     jsrule.NewInjector(),
		extendedCSS: extendedCSS,

		scriptletsURL:  base + scriptletsPath,
		jsRuleURL:      base + jsRulePath,
		extendedCSSURL: base + extendedCSSPath,
		cosmeticCSSURL: base + cosmeticCSSPath,
		cssRuleCSSURL:  base + cssRulePath,
	}, nil
}

// AddRule attempts to add a non-network rule. Returns handled=true if consumed.
func (e *Engine) AddRule(rule string, filterListTrusted bool) (handled bool, err error) {
	switch {
	case scriptlet.RuleRegex.MatchString(rule):
		if err := e.scriptlets.AddRule(rule, filterListTrusted); err != nil {
			return true, fmt.Errorf("add scriptlet: %w", err)
		}
		return true, nil
	case cosmetic.IsRule(rule):
		if err := e.cosmetic.AddRule(rule); err != nil {
			return true, fmt.Errorf("add cosmetic rule: %w", err)
		}
		return true, nil
	case extendedcss.IsRule(rule):
		if err := e.extendedCSS.AddRule(rule); err != nil {
			return true, fmt.Errorf("add extended css rule: %w", err)
		}
		return true, nil
	case filterListTrusted && cssrule.RuleRegex.MatchString(rule):
		if err := e.cssRules.AddRule(rule); err != nil {
			return true, fmt.Errorf("add css rule: %w", err)
		}
		return true, nil
	case filterListTrusted && jsrule.RuleRegex.MatchString(rule):
		if err := e.jsRules.AddRule(rule); err != nil {
			return true, fmt.Errorf("add js rule: %w", err)
		}
		return true, nil
	default:
		return false, nil
	}
}

// Inject appends asset tags for the matching hostname into HTML responses.
//
// 2026-10-08 (B2): ex optionally carries the exception-modifier exemptions
// resolved for this document by NetworkRules.ActiveExceptions (AdGuard
// $elemhide/$generichide/$specifichide/$jsinject and the cosmetic/js
// components of $document; see internal/exemption). The zero value keeps
// the exact pre-B2 behavior. With an exemption, fully exempted asset tags
// are not injected at all, and the surviving asset URLs carry the exemption
// as the ex query parameter so the later asset fetch serves filtered
// content for the same page (the browser fetches assets after injection).
func (e *Engine) Inject(_ *http.Request, res *http.Response, ex ...exemption.Exemption) error {
	var exv exemption.Exemption
	if len(ex) > 0 {
		exv = ex[0]
	}
	exQuery := exv.QueryValue()

	var operations []csp.PatchOperation
	var injection bytes.Buffer

	if !exv.Jsinject {
		scriptletsNonce := csp.NewNonce()
		jsRuleNonce := csp.NewNonce()
		scriptletsURL := withExemptionQuery(e.scriptletsURL, exQuery)
		jsRuleURL := withExemptionQuery(e.jsRuleURL, exQuery)
		operations = append(operations,
			csp.PatchOperation{Nonce: scriptletsNonce, Kind: csp.Script, ResourceURL: scriptletsURL},
			csp.PatchOperation{Nonce: jsRuleNonce, Kind: csp.Script, ResourceURL: jsRuleURL},
		)
		injection.WriteString(scriptTag(scriptletsURL, scriptletsNonce))
		injection.WriteString(scriptTag(jsRuleURL, jsRuleNonce))
	}
	if !exv.Elemhide {
		extendedCSSNonce := csp.NewNonce()
		cosmeticCSSNonce := csp.NewNonce()
		cssRuleNonce := csp.NewNonce()
		extendedCSSURL := withExemptionQuery(e.extendedCSSURL, exQuery)
		cosmeticCSSURL := withExemptionQuery(e.cosmeticCSSURL, exQuery)
		cssRuleURL := withExemptionQuery(e.cssRuleCSSURL, exQuery)
		operations = append(operations,
			csp.PatchOperation{Nonce: extendedCSSNonce, Kind: csp.Script, ResourceURL: extendedCSSURL},
			csp.PatchOperation{Nonce: cosmeticCSSNonce, Kind: csp.Style, ResourceURL: cosmeticCSSURL},
			csp.PatchOperation{Nonce: cssRuleNonce, Kind: csp.Style, ResourceURL: cssRuleURL},
		)
		injection.WriteString(scriptTag(extendedCSSURL, extendedCSSNonce))
		injection.WriteString(styleTag(cosmeticCSSURL, cosmeticCSSNonce))
		injection.WriteString(styleTag(cssRuleURL, cssRuleNonce))
	}

	if len(operations) == 0 {
		// elemhide+jsinject (e.g. $document/$elemhide,$jsinject): nothing
		// injects at all.
		return nil
	}

	if err := csp.PatchHeadersBatch(res, operations); err != nil {
		return fmt.Errorf("patch CSP headers: %w", err)
	}

	if err := httprewrite.AppendHTMLHeadContents(res, injection.Bytes()); err != nil {
		return fmt.Errorf("append head contents: %w", err)
	}

	return nil
}

// withExemptionQuery appends the exemption query parameter to an asset URL.
// Without an exemption the URL is returned unchanged (byte-identical to
// pre-B2). 2026-10-08 (B2).
func withExemptionQuery(u, exQuery string) string {
	if exQuery == "" {
		return u
	}
	return u + "?" + exemption.QueryKey + "=" + exQuery
}

// assetBytes returns the asset content for a hostname and asset path, with
// the page's exemption (decoded from the asset URL's ex query parameter by
// the asset Handler) applied to rule selection. 2026-10-08 (B2).
func (e *Engine) assetBytes(hostname, path string, ex exemption.Exemption) ([]byte, error) {
	switch path {
	case cosmeticCSSPath:
		return e.cosmetic.GetAsset(hostname, ex), nil
	case cssRulePath:
		return e.cssRules.GetAsset(hostname, ex), nil
	case scriptletsPath:
		body, err := e.scriptlets.GetAsset(hostname, ex)
		if err != nil {
			return nil, fmt.Errorf("scriptlets asset: %w", err)
		}
		return body, nil
	case extendedCSSPath:
		body, err := e.extendedCSS.GetAsset(hostname, ex)
		if err != nil {
			return nil, fmt.Errorf("extended CSS asset: %w", err)
		}
		return body, nil
	case jsRulePath:
		body, err := e.jsRules.GetAsset(hostname, ex)
		if err != nil {
			return nil, fmt.Errorf("js rules: %w", err)
		}
		return body, nil
	default:
		return nil, fmt.Errorf("unknown asset path: %q", path)
	}
}

func scriptTag(src, nonce string) string {
	return fmt.Sprintf(`<script nonce="%s" src="%s"></script>`, nonce, src)
}

func styleTag(href, nonce string) string {
	return fmt.Sprintf(`<link rel="stylesheet" nonce="%s" href="%s">`, nonce, href)
}
