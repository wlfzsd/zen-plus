// Package exemption carries AdGuard exception-modifier effects ($elemhide,
// $generichide, $specifichide, $jsinject and the cosmetic/js components of
// $document) from the network rule engine (NetworkRules.ActiveExceptions) to
// asset injection (Engine.Inject and the per-asset injectors' GetAsset).
//
// 2026-10-08 (B2): introduced by the exception-semantics refactor. The zero
// value means "no exemption" and must preserve pre-B2 behavior byte for byte.
package exemption

import "strings"

// Exemption describes which asset classes are exempted on a document.
type Exemption struct {
	// Elemhide is $elemhide (and the elemhide component of $document):
	// no cosmetic / extended-css / css-rule injection at all.
	Elemhide bool
	// Generichide is $generichide: no generic cosmetic rules (AdGuard
	// "generic rule" definition, docs #exception-modifiers-generic-rules).
	Generichide bool
	// Specifichide is $specifichide: no specific cosmetic rules.
	Specifichide bool
	// Jsinject is $jsinject (and the jsinject component of $document):
	// no scriptlet / jsrule injection.
	Jsinject bool
}

// QueryKey is the asset-URL query parameter that transports the exemption
// from Engine.Inject to the later asset fetch handled by the asset Handler.
const QueryKey = "ex"

// QueryValue encodes the exemption for asset URLs. It returns "" for the
// zero value so non-exempt pages produce byte-identical asset URLs.
func (e Exemption) QueryValue() string {
	var b strings.Builder
	add := func(tok string, on bool) {
		if !on {
			return
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(tok)
	}
	add("eh", e.Elemhide)
	add("gh", e.Generichide)
	add("sh", e.Specifichide)
	add("ji", e.Jsinject)
	return b.String()
}

// FromQueryValue decodes an exemption encoded by QueryValue. Unknown tokens
// are ignored so that newer senders degrade gracefully.
func FromQueryValue(v string) Exemption {
	var e Exemption
	if v == "" {
		return e
	}
	for _, tok := range strings.Split(v, ".") {
		switch tok {
		case "eh":
			e.Elemhide = true
		case "gh":
			e.Generichide = true
		case "sh":
			e.Specifichide = true
		case "ji":
			e.Jsinject = true
		}
	}
	return e
}

// IsDocumentDest reports whether a Sec-Fetch-Dest value denotes a frame
// document load (main frame or iframe). By default, exception rule
// modifiers only take effect for such document requests (AdGuard docs line
// 1119; C5). "frame" is accepted alongside "iframe" because zen's own
// content-type map (rulemodifiers/secFetchDestMap) treats them alike.
func IsDocumentDest(dest string) bool {
	return dest == "document" || dest == "iframe" || dest == "frame"
}
