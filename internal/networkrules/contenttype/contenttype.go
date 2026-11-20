// Package contenttype classifies an HTTP request or response as one of the
// content types AdGuard's $script/$image/... modifiers name.
//
// 2026-11-20 (learn-from-upstream b09b676, #847): a rule's content types used to
// be one condition modifier each, all ORed together, with negated types included
// in the same OR group — so `$~stylesheet,~xmlhttprequest` meant "not a
// stylesheet OR not an XHR", which every request carrying Sec-Fetch-Dest
// satisfies. Content types are now one bit set per rule (see rule.Rule.ctTypes):
// positive types set bits, negated types clear them, and a rule whose content
// types are all negated starts from every type.
//
// The type is derived once per request (networkrules.ModifyReq/ModifyRes) and
// passed down, so a request is classified once instead of once per rule.
package contenttype

import (
	"net/http"
	"strings"
)

// Type is a single content type. Unclassifiable traffic is Other rather than
// a zero "unknown": a rule that only negates types ($~stylesheet) starts from
// All and must keep matching traffic Zen cannot classify (native apps, old
// browsers send no Fetch Metadata). Unknown is the zero value and is never
// produced for a real request or response.
type Type uint16

// Content types, one bit each (12 bits, matching AdGuard's modifier set).
const (
	Unknown Type = 0

	Document       Type = 1 << 0
	Subdocument    Type = 1 << 1
	Script         Type = 1 << 2
	Stylesheet     Type = 1 << 3
	Image          Type = 1 << 4
	Media          Type = 1 << 5
	Font           Type = 1 << 6
	Object         Type = 1 << 7
	XMLHTTPRequest Type = 1 << 8
	Websocket      Type = 1 << 9
	Ping           Type = 1 << 10
	Other          Type = 1 << 11
)

// Mask is a set of Types.
type Mask uint16

// All contains every content type. Unknown has no bit and is deliberately not
// part of it; unclassifiable traffic is Other, which is.
const All = Mask(Document|Subdocument|Script|Stylesheet|Image|Media|Font|Object|
	XMLHTTPRequest|Websocket|Ping|Other)

// Matches reports whether the mask contains t. A zero mask matches everything
// (the rule names no content type, so the condition is vacuous) — including
// Unknown.
func (m Mask) Matches(t Type) bool {
	return m == 0 || uint16(m)&uint16(t) != 0
}

// ByName returns the Type a content-type modifier names. ok is false for a name
// that is not a content type.
func ByName(name string) (Type, bool) {
	switch normaliseName(name) {
	case "document", "doc":
		return Document, true
	case "subdocument":
		return Subdocument, true
	case "script":
		return Script, true
	case "stylesheet", "css":
		return Stylesheet, true
	case "image":
		return Image, true
	case "media":
		return Media, true
	case "font":
		return Font, true
	case "object":
		return Object, true
	case "xmlhttprequest", "xhr":
		return XMLHTTPRequest, true
	case "websocket":
		return Websocket, true
	case "ping":
		return Ping, true
	case "other":
		return Other, true
	}
	return Unknown, false
}

func normaliseName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	return strings.TrimPrefix(name, "~")
}

// secFetchDestTypes maps Sec-Fetch-Dest values to their content type.
var secFetchDestTypes = map[string]Type{
	"document":  Document,
	"iframe":    Subdocument,
	"frame":     Subdocument,
	"script":    Script,
	"style":     Stylesheet,
	"image":     Image,
	"font":      Font,
	"object":    Object,
	"embed":     Object,
	"audio":     Media,
	"video":     Media,
	"track":     Media,
	"websocket": Websocket,
	"empty":     XMLHTTPRequest,
}

// FromRequest classifies a request from its Fetch Metadata headers.
func FromRequest(req *http.Request) Type {
	if req == nil {
		return Other
	}

	dest := strings.ToLower(strings.TrimSpace(req.Header.Get("Sec-Fetch-Dest")))
	if dest != "" {
		if t, ok := secFetchDestTypes[dest]; ok {
			return t
		}
		return Other
	}

	if isWebsocketUpgrade(req.Header) {
		return Websocket
	}
	if isPing(req.Header) {
		return Ping
	}
	// No Fetch Metadata: Other, so negated-only type sets still match.
	return Other
}

// FromResponse classifies a response.
//
// 2026-11-20 decision (differs from upstream b09b676 on purpose): upstream falls
// back to the response Content-Type when the request carries no Fetch Metadata.
// That fallback is NOT adopted here — local behaviour since 2026-10-08 (B10) is
// that traffic Zen cannot classify never matches a typed rule (see
// internal/networkrules/response_domain_test.go TestXhrNoMetadataLegacyFallback).
// Rationale: with the fallback, every JSON reply to a native app or an old
// browser would start matching $xmlhttprequest rules, i.e. response rewriting
// could reach payloads the browser never announced. The upstream fix we do take
// is the set semantics of `$~a,~b`; the fallback is a separate traffic-scope
// change that needs its own evidence.
func FromResponse(req *http.Request, res *http.Response) Type {
	_ = res
	return FromRequest(req)
}

func isWebsocketUpgrade(h http.Header) bool {
	return headerContains(h, "Connection", "upgrade") && headerContains(h, "Upgrade", "websocket")
}

func isPing(h http.Header) bool {
	return h.Get("Ping-To") != "" || h.Get("Ping-From") != ""
}

func headerContains(h http.Header, name, value string) bool {
	for _, v := range h[name] {
		for _, s := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(s), value) {
				return true
			}
		}
	}
	return false
}
