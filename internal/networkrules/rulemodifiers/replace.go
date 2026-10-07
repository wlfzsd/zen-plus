package rulemodifiers

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"github.com/irbis-sh/zen-desktop/internal/httprewrite"
)

// ReplaceModifier implements the $replace action modifier (AdGuard semantics,
// adguard_create-own-filters.md §$replace, lines 2867-2936). It rewrites the
// body of text responses whose content matches the rule's regexp.
//
// 2026-10-08 (batch B8): initial implementation.
//
// Documented behaviours intentionally NOT implemented (see batch report):
//   - trusted-filter gate (docs line 2932): the zen network layer has no trust
//     gate, so $replace rules from any list are accepted as-is;
//   - alphabetical application order across multiple matching $replace rules
//     (docs line 2885): rules are applied in rule-store order.
type ReplaceModifier struct {
	// cancelAll is set for the bare `$replace` form, which is used by
	// exception rules (`@@...$replace`) to disable all $replace rules whose
	// pattern matches (docs lines 2925-2927).
	cancelAll bool
	// re is the compiled pattern part. nil when cancelAll is set.
	re *regexp.Regexp
	// template is the Go regexp.Expand template translated from the
	// JS/Perl-style replacement string.
	template string
	// value is the raw modifier value, kept for the exact-match Cancels
	// required by docs lines 2920-2923.
	value string
}

var _ ActionModifier = (*ReplaceModifier)(nil)

var ErrInvalidReplaceModifier = errors.New("invalid replace modifier")

// replaceMaxBodySize is the response size above which $replace rules are not
// applied (docs line 2876: "$replace rules do not apply if the size of the
// original response is more than 10 MB").
const replaceMaxBodySize = 10 << 20

func (m *ReplaceModifier) Parse(modifier string) error {
	if modifier == "replace" {
		m.cancelAll = true
		return nil
	}
	if !strings.HasPrefix(modifier, "replace=") {
		return ErrInvalidReplaceModifier
	}
	value := strings.TrimPrefix(modifier, "replace=")
	if value == "" || value[0] != '/' {
		return ErrInvalidReplaceModifier
	}

	pattern, replacement, flags, ok := splitReplaceValue(value)
	if !ok {
		return ErrInvalidReplaceModifier
	}

	prefix, err := replaceFlagsToPrefix(flags)
	if err != nil {
		return err
	}

	re, err := regexp.Compile(prefix + pattern)
	if err != nil {
		// Filter lists author regexps in JS syntax; constructs RE2 does not
		// support (lookahead/lookbehind, backreferences) fail to compile and
		// the rule is dropped, mirroring the AdGuard Go engine.
		return fmt.Errorf("compile replace regexp: %w", err)
	}

	template, err := replaceReplacementToGoTemplate(replacement, re.NumSubexp())
	if err != nil {
		return err
	}

	m.re = re
	m.template = template
	m.value = value
	return nil
}

// replaceFlagsToPrefix converts the documented regexp flags (docs line 2897:
// `i` insensitive, `s` single-line) into a Go regexp flag prefix. `g` is
// accepted and ignored: Go's ReplaceAll is always global.
func replaceFlagsToPrefix(flags string) (string, error) {
	var b strings.Builder
	seen := make(map[rune]bool)
	for _, f := range flags {
		if seen[f] {
			return "", fmt.Errorf("duplicate replace flag %q", f)
		}
		seen[f] = true
		switch f {
		case 'i':
			b.WriteString("(?i)")
		case 's':
			b.WriteString("(?s)")
		case 'm':
			b.WriteString("(?m)")
		case 'g':
			// global is Go ReplaceAll's default behaviour
		default:
			return "", fmt.Errorf("unsupported replace flag %q", f)
		}
	}
	return b.String(), nil
}

// splitReplaceValue splits `/regexp/replacement/flags` on unescaped slashes.
// The leading slash is value[0] (guaranteed by Parse); the next unescaped
// slash ends the regexp and the following one ends the replacement (Perl
// s/// semantics); whatever remains is the flags part, which may be empty
// whether or not the trailing delimiter is present.
func splitReplaceValue(value string) (pattern, replacement, flags string, ok bool) {
	var slashes []int
	escaped := false
	for i := 1; i < len(value); i++ {
		switch value[i] {
		case '\\':
			escaped = !escaped
		case '/':
			if !escaped {
				slashes = append(slashes, i)
			}
			escaped = false
		default:
			escaped = false
		}
	}
	if len(slashes) < 2 {
		return "", "", "", false
	}
	return value[1:slashes[0]], value[slashes[0]+1 : slashes[1]], value[slashes[1]+1:], true
}

// replaceReplacementToGoTemplate translates a JS/Perl-style replacement string
// into a Go regexp.Expand template.
//
// In the rule text the replacement's `$` characters are escaped as `\$`
// (docs line 2899) and splitModifiers leaves those backslashes in place, so a
// backslash directly preceding `$` is dropped here first; `\$1` and `$1` are
// therefore both the first capture group, as the documented VAST example
// (docs lines 2904-2911) requires.
//
// Go and JS agree that `$$` inserts a literal `$`, but disagree on greedy
// digit runs: Go reads `$1x` as a group named "1x" (expands to nothing) where
// JS reads group 1 followed by the literal "x". The translation emits explicit
// `${n}` braces using the JS disambiguation rule (a second digit is part of
// the group number only when that group exists), reproduces JS's literal
// `$n` for out-of-range groups, and maps `$&` (whole match) to `${0}`.
// “ $` “ and `$'` (before/after the match) have no Go template equivalent
// and reject the rule.
func replaceReplacementToGoTemplate(replacement string, numSubexp int) (string, error) {
	var b strings.Builder
	for i := 0; i < len(replacement); i++ {
		c := replacement[i]

		// Rule-level escapes in the replacement: `\$` denotes the literal `$`
		// character (docs line 2899) and `\/` the delimiter slash (Perl s///
		// semantics); drop the backslash and process the escaped character.
		if c == '\\' && i+1 < len(replacement) && (replacement[i+1] == '$' || replacement[i+1] == '/') {
			i++
			c = replacement[i]
		}

		if c != '$' {
			b.WriteByte(c)
			continue
		}

		if i+1 >= len(replacement) {
			b.WriteString("$$") // trailing literal $
			continue
		}
		n := replacement[i+1]
		switch {
		case n == '$':
			b.WriteString("$$")
			i++
		case n >= '0' && n <= '9':
			v1 := int(n - '0')
			if i+2 < len(replacement) && replacement[i+2] >= '0' && replacement[i+2] <= '9' {
				v2 := v1*10 + int(replacement[i+2]-'0')
				switch {
				case v2 >= 1 && v2 <= numSubexp:
					fmt.Fprintf(&b, "${%d}", v2)
				case v1 >= 1 && v1 <= numSubexp:
					fmt.Fprintf(&b, "${%d}", v1)
					b.WriteByte(replacement[i+2])
				default:
					// JS renders `$n` for a nonexistent group literally.
					b.WriteString("$$")
					b.WriteByte(n)
					b.WriteByte(replacement[i+2])
				}
				i += 2
			} else {
				if v1 >= 1 && v1 <= numSubexp {
					fmt.Fprintf(&b, "${%d}", v1)
				} else {
					b.WriteString("$$")
					b.WriteByte(n)
				}
				i++
			}
		case n == '&':
			b.WriteString("${0}") // JS whole match == Go group 0
			i++
		case n == '`' || n == '\'':
			return "", fmt.Errorf("unsupported replacement token $%c", n)
		case n == '<':
			// JS named-group reference $<name>; RE2 declares named groups
			// with a different syntax, so this stays a literal.
			b.WriteString("$$<")
			i++
		default:
			b.WriteString("$$") // literal $; the next char is emitted verbatim
		}
	}
	return b.String(), nil
}

// replaceableContentTypes is the allowlist of textual response types $replace
// applies to (docs line 2875: "any text response, but will not apply to
// binary (media, image, object, etc.)"). Everything under text/ is allowed;
// binary-leaning application/* types are covered explicitly, including the
// HLS/DASH playlist types targeted by corpus rules.
var replaceableContentTypes = map[string]bool{
	"application/javascript":        true,
	"application/x-javascript":      true,
	"application/json":              true,
	"application/xml":               true,
	"application/xhtml+xml":         true,
	"application/rss+xml":           true,
	"application/atom+xml":          true,
	"image/svg+xml":                 true,
	"application/vnd.apple.mpegurl": true,
	"application/x-mpegurl":         true,
	"audio/mpegurl":                 true,
	"audio/x-mpegurl":               true,
	"application/dash+xml":          true,
	"application/manifest+json":     true,
}

// isReplaceableResponse reports whether the response body is textual enough
// for $replace. It runs before any body read so unmatched responses are never
// buffered.
func isReplaceableResponse(res *http.Response) bool {
	contentType := res.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	if replaceableContentTypes[mediaType] {
		return true
	}
	return strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
}

func (m *ReplaceModifier) ModifyRes(res *http.Response) (bool, error) {
	if m.cancelAll || m.re == nil {
		return false, nil
	}
	if !isReplaceableResponse(res) {
		return false, nil
	}

	var touched bool
	_, err := httprewrite.BufferRewriteLimited(res, replaceMaxBodySize, func(src []byte) []byte {
		if !m.re.Match(src) {
			return src
		}
		touched = true
		return m.re.ReplaceAll(src, []byte(m.template))
	})
	if err != nil {
		return false, fmt.Errorf("buffer rewrite: %w", err)
	}

	return touched, nil
}

func (*ReplaceModifier) ModifyReq(*http.Request) bool {
	return false
}

// Cancels reports whether an exception cancels a $replace rule. The bare
// exception form cancels every $replace rule (docs line 2927); otherwise the
// whole value must match exactly (docs lines 2920-2923).
func (m *ReplaceModifier) Cancels(other Modifier) bool {
	o, ok := other.(*ReplaceModifier)
	if !ok {
		return false
	}
	if m.cancelAll {
		return true
	}
	return m.value == o.value
}
