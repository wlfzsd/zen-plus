package rulemodifiers

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

type removeparamKind int8

const (
	removeparamKindGeneric removeparamKind = iota
	removeparamKindRegexp
	removeparamKindRegexpInverse
	removeparamKindExact
	removeparamKindExactInverse
)

type RemoveParamModifier struct {
	kind   removeparamKind
	param  string
	regexp *regexp.Regexp
}

var _ QueryModifier = (*RemoveParamModifier)(nil)

func (rm *RemoveParamModifier) Parse(modifier string) error {
	if modifier == "removeparam" {
		rm.kind = removeparamKindGeneric
		return nil
	}

	eqIndex := strings.IndexByte(modifier, '=')
	if eqIndex == -1 || eqIndex == len(modifier)-1 {
		return errors.New("invalid syntax")
	}
	value := modifier[eqIndex+1:]

	var inverse bool
	if value[0] == '~' {
		inverse = true
		value = value[1:]
	}

	regexp, err := parseRegexp(value)
	if err != nil {
		return fmt.Errorf("parse regexp: %w", err)
	}
	if regexp != nil {
		if inverse {
			rm.kind = removeparamKindRegexpInverse
		} else {
			rm.kind = removeparamKindRegexp
		}
		rm.regexp = regexp
		return nil
	}

	// 2026-10-08 (B7, verify08-D7): a value that starts with "/" announces
	// regexp syntax (AdGuard doc 2646). If it did not parse as one above
	// (e.g. "/re" without the closing slash, or "/re/x" with an unsupported
	// trailing option), the rule is malformed and must be rejected instead
	// of silently falling back to an Exact match on the literal text.
	if strings.HasPrefix(value, "/") {
		return fmt.Errorf("invalid removeparam regexp syntax: %s", value)
	}

	if inverse {
		rm.kind = removeparamKindExactInverse
		rm.param = value
	} else {
		rm.kind = removeparamKindExact
		rm.param = value
	}
	return nil
}

// ModifyQuery removes matching query parameters from the request URL.
//
// 2026-10-08 (B7): two semantics fixes against the AdGuard specification
// (测试临时\adguard_create-own-filters.md, §$removeparam 2626-2738):
//   - Method whitelist (doc 2634, verify08-D1): rules apply only to GET,
//     HEAD, OPTIONS and bodyless POST requests ("sometimes POST": nil body
//     or Content-Length == 0). Requests with any other method, and POSTs
//     carrying a body, are left untouched.
//   - Matching happens on the encoded RawQuery text (doc 2650-2656,
//     verify08-D3): the rule value and each raw "name=value" pair are
//     compared in encoded form, instead of decoding the query into a
//     url.Values map. Untouched pairs keep their original order and
//     encoding — the old query.Encode() re-serialization (which sorted and
//     re-escaped the remaining pairs) is gone.
//
// For names/values made only of unreserved ASCII characters the two
// matching modes are equivalent (AdGuard docs 2626-2738; batch B7 verified
// the equivalence against a verbatim reference implementation of the pre-B7
// decoded-mode behavior).
func (rm *RemoveParamModifier) ModifyQuery(req *http.Request) bool {
	switch strings.ToUpper(req.Method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		// Always eligible.
	case http.MethodPost:
		// Bodyless POST only (doc 2634 "sometimes POST").
		if req.Body != nil && req.ContentLength != 0 {
			return false
		}
	default:
		return false
	}

	raw := req.URL.RawQuery
	if raw == "" {
		return false
	}

	if rm.kind == removeparamKindGeneric {
		// Naked $removeparam removes all query parameters (doc 2670).
		// Same end state as the pre-B7 decoded-mode path (clear + Encode).
		req.URL.RawQuery = ""
		return true
	}

	// Filter the raw "&"-separated pairs in encoded form.
	segs := strings.Split(raw, "&")
	kept := segs[:0]
	var modified bool
	for _, seg := range segs {
		if seg == "" {
			// Preserve empty segments verbatim ("a=1&&b=2"): they are not
			// parameters (url.ParseQuery ignores them the same way).
			kept = append(kept, seg)
			continue
		}
		name, _, hasValue := strings.Cut(seg, "=")
		// A pair without "=" is matched against the normalized "name="
		// text, mirroring the pre-B7 decoded-mode behavior where a
		// valueless parameter matched as param + "=" + "".
		matchText := seg
		if !hasValue {
			matchText = name + "="
		}

		var drop bool
		switch rm.kind {
		case removeparamKindExact:
			drop = name == rm.param
		case removeparamKindExactInverse:
			drop = name != rm.param
		case removeparamKindRegexp:
			// Regexp rules match the entire normalized name=value pair,
			// not just the name (doc 2646).
			drop = rm.regexp.MatchString(matchText)
		case removeparamKindRegexpInverse:
			drop = !rm.regexp.MatchString(matchText)
		}

		if drop {
			modified = true
			continue
		}
		kept = append(kept, seg)
	}

	if !modified {
		return false
	}
	req.URL.RawQuery = strings.Join(kept, "&")
	return true
}

func (rm *RemoveParamModifier) Cancels(modifier Modifier) bool {
	other, ok := modifier.(*RemoveParamModifier)
	if !ok {
		return false
	}

	// 2026-10-08 (B7, verify08-D4 / AdGuard doc 2693): a naked
	// $removeparam exception negates ALL $removeparam rules. Cancels is
	// only ever invoked exception-rule → primary-rule (exceptionrule.go),
	// so rm here is the exception's own modifier.
	if rm.kind == removeparamKindGeneric {
		return true
	}

	if other.kind != rm.kind || other.param != rm.param {
		return false
	}

	if rm.regexp == nil && other.regexp == nil {
		return true
	}
	if rm.regexp == nil || other.regexp == nil {
		return false
	}
	return rm.regexp.String() == other.regexp.String()
}
