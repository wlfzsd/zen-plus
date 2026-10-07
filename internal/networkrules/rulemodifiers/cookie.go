package rulemodifiers

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type cookieKind int8

const (
	// cookieKindGeneric matches all cookies (bare $cookie).
	cookieKindGeneric cookieKind = iota
	// cookieKindExact matches a cookie by exact name.
	cookieKindExact
	// cookieKindRegexp matches a cookie name by a regular expression.
	cookieKindRegexp
)

// CookieModifier implements the $cookie modifier
// (https://adguard.com/kb/general/ad-filtering/create-own-filters/#cookie-modifier).
// It completely changes rule behavior: instead of blocking a matching
// request, the rule suppresses or rewrites the Cookie and Set-Cookie
// headers of matched requests/responses.
//
// 2026-10-08 (batch B3): implemented per AdGuard docs "create-own-filters"
// $cookie section (lines 1517-1589).
type CookieModifier struct {
	kind   cookieKind
	name   string
	regexp *regexp.Regexp

	// maxAge is the number of seconds to offset the cookie expiration date.
	// Zero (including a bare $cookie) means matched cookies are expired
	// (delete-style), which also suppresses them in requests.
	maxAge int32
	// sameSite is the SameSite strategy to set on a matched Set-Cookie
	// header. Empty means SameSite is left untouched.
	sameSite string
}

var _ ActionModifier = (*CookieModifier)(nil)

// Parse parses a $cookie modifier, e.g. one of:
//
//	cookie
//	cookie=NAME
//	cookie=/regexp/
//	cookie=NAME;maxAge=3600;sameSite=lax
//
// Semicolon-separated attribute parts are part of the modifier value
// itself: splitModifiers only splits on unescaped commas, so the maxAge and
// sameSite suffixes are split here.
func (cm *CookieModifier) Parse(modifier string) error {
	value, ok := strings.CutPrefix(modifier, "cookie")
	if !ok {
		return errors.New("invalid cookie modifier")
	}
	if value == "" {
		// Bare $cookie: all cookies with maxAge=0.
		cm.kind = cookieKindGeneric
		return nil
	}
	if value[0] != '=' {
		return errors.New("invalid cookie modifier")
	}
	value = value[1:]
	if value == "" {
		return errors.New("empty cookie name")
	}

	parts := strings.Split(value, ";")
	nameSpec := parts[0]
	for _, attr := range parts[1:] {
		attr = strings.TrimSpace(attr)
		attrName, attrValue, hasValue := strings.Cut(attr, "=")
		switch strings.ToLower(strings.TrimSpace(attrName)) {
		case "maxage":
			seconds, err := strconv.ParseInt(strings.TrimSpace(attrValue), 10, 32)
			if !hasValue || err != nil || seconds < 0 {
				return fmt.Errorf("invalid maxAge value in %q", modifier)
			}
			cm.maxAge = int32(seconds)
		case "samesite":
			attrValue = strings.TrimSpace(attrValue)
			if !hasValue || attrValue == "" {
				return fmt.Errorf("invalid sameSite value in %q", modifier)
			}
			cm.sameSite = attrValue
		default:
			return fmt.Errorf("unknown cookie attribute %q in %q", attr, modifier)
		}
	}

	if nameSpec == "" {
		return errors.New("empty cookie name")
	}

	re, err := parseRegexp(nameSpec)
	if err != nil {
		return fmt.Errorf("parse regexp: %w", err)
	}
	if re != nil {
		cm.kind = cookieKindRegexp
		cm.regexp = re
		return nil
	}

	cm.kind = cookieKindExact
	cm.name = nameSpec
	return nil
}

// ModifyReq removes cookies matched by this rule from the request Cookie
// header. Only blocking cookie rules (maxAge == 0) touch request cookies: a
// maxAge > 0 rule extends a cookie's lifetime instead of suppressing it,
// and a request header carries no expiration to rewrite.
func (cm *CookieModifier) ModifyReq(req *http.Request) (modified bool) {
	if cm.maxAge != 0 {
		return false
	}
	lines := req.Header["Cookie"]
	if len(lines) == 0 {
		return false
	}

	out := make([]string, 0, len(lines))
	for _, line := range lines {
		parts := strings.Split(line, ";")
		kept := parts[:0]
		for _, p := range parts {
			name, _, _ := strings.Cut(p, "=")
			if cm.matchesName(strings.TrimSpace(name)) {
				continue
			}
			kept = append(kept, p)
		}

		switch len(kept) {
		case len(parts):
			out = append(out, line) // untouched, keep the line verbatim
		case 0:
			// Every cookie of this line matched: the line is dropped.
		default:
			// Rebuild the line with the canonical "; " separator.
			rebuilt := make([]string, 0, len(kept))
			for _, p := range kept {
				if p = strings.TrimSpace(p); p != "" {
					rebuilt = append(rebuilt, p)
				}
			}
			out = append(out, strings.Join(rebuilt, "; "))
		}
		if len(kept) != len(parts) {
			modified = true
		}
	}

	if !modified {
		return false
	}
	if len(out) == 0 {
		req.Header.Del("Cookie")
	} else {
		req.Header["Cookie"] = out
	}
	return true
}

// ModifyRes rewrites matched Set-Cookie headers: maxAge=0 (including a bare
// $cookie) expires the cookie delete-style so the browser drops it; a
// positive maxAge rewrites the expiration date; sameSite rewrites the
// SameSite attribute. Unmatched cookies are left verbatim.
func (cm *CookieModifier) ModifyRes(res *http.Response) (modified bool, err error) {
	values := res.Header["Set-Cookie"]
	if len(values) == 0 {
		return false, nil
	}

	for i, sc := range values {
		name, ok := setCookieName(sc)
		if !ok || !cm.matchesName(name) {
			continue
		}
		values[i] = cm.rewriteSetCookie(sc)
		modified = true
	}

	return modified, nil
}

// Cancels reports whether this (exception) modifier suppresses the effect
// of a basic rule's $cookie modifier:
//   - a bare @@…$cookie cancels every $cookie rule (all cookies);
//   - @@…$cookie=NAME cancels a rule matching the same exact name;
//   - @@…$cookie=/re/ cancels a rule with the same regular expression.
func (cm *CookieModifier) Cancels(other Modifier) bool {
	om, ok := other.(*CookieModifier)
	if !ok {
		return false
	}

	switch cm.kind {
	case cookieKindGeneric:
		return true
	case cookieKindExact:
		return om.kind == cookieKindExact && om.name == cm.name
	default:
		return om.kind == cookieKindRegexp &&
			cm.regexp != nil && om.regexp != nil &&
			cm.regexp.String() == om.regexp.String()
	}
}

// matchesName reports whether a cookie name is matched by this rule.
func (cm *CookieModifier) matchesName(name string) bool {
	switch cm.kind {
	case cookieKindGeneric:
		return true
	case cookieKindExact:
		return name == cm.name
	default:
		return cm.regexp.MatchString(name)
	}
}

// rewriteSetCookie rewrites one Set-Cookie header value: the matched
// cookie's expiration attributes are replaced with this rule's values and
// the SameSite attribute is replaced when the rule specifies one. All other
// attributes (Path, Domain, Secure, HttpOnly, ...) are preserved verbatim.
func (cm *CookieModifier) rewriteSetCookie(sc string) string {
	parts := strings.Split(sc, ";")
	kept := make([]string, 0, len(parts)) // attributes after name=value
	for _, p := range parts[1:] {
		attrName := p
		if idx := strings.IndexByte(attrName, '='); idx != -1 {
			attrName = attrName[:idx]
		}
		switch strings.ToLower(strings.TrimSpace(attrName)) {
		case "max-age", "expires":
			// Rewritten below.
		case "samesite":
			if cm.sameSite != "" {
				continue // Rewritten below.
			}
			kept = append(kept, strings.TrimSpace(p))
		default:
			kept = append(kept, strings.TrimSpace(p))
		}
	}

	if cm.maxAge == 0 {
		kept = append(kept, "Max-Age=0", "Expires=Thu, 01 Jan 1970 00:00:00 GMT")
	} else {
		expires := time.Now().Add(time.Duration(cm.maxAge) * time.Second).UTC().Format(http.TimeFormat)
		kept = append(kept,
			"Max-Age="+strconv.FormatInt(int64(cm.maxAge), 10),
			"Expires="+expires)
	}
	if cm.sameSite != "" {
		kept = append(kept, "SameSite="+cm.sameSite)
	}

	// The name=value pair is kept verbatim; attributes are rebuilt with the
	// canonical "; " separator.
	return parts[0] + "; " + strings.Join(kept, "; ")
}

// setCookieName extracts the cookie name of a Set-Cookie header value.
// Reports false if the value does not carry a name=value pair.
func setCookieName(sc string) (name string, ok bool) {
	first := sc
	if idx := strings.IndexByte(first, ';'); idx != -1 {
		first = first[:idx]
	}
	name, _, found := strings.Cut(first, "=")
	if !found {
		return "", false
	}
	return strings.TrimSpace(name), true
}
