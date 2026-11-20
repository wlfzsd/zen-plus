package rule

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/contenttype"
)

func ctReq() *http.Request {
	return httptest.NewRequest(http.MethodGet, "https://example.com/ad.js", nil)
}

func mustParseModifiers(t *testing.T, mods ...string) *Rule {
	t.Helper()

	var r Rule
	if err := r.ParseModifiers(mods); err != nil {
		t.Fatalf("ParseModifiers(%q) = %v, want nil", mods, err)
	}
	return &r
}

// TestContentTypeSetSemantics pins the 2026-11-20 fix (learn-from-upstream
// b09b676): content types are one bit set per rule, so `$~a,~b` means "neither
// a nor b". Before the fix both negations joined the same OR group and the
// rule matched every request that carried Sec-Fetch-Dest at all.
func TestContentTypeSetSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mods []string
		ct   contenttype.Type
		want bool
	}{
		{name: "positive matches its own type", mods: []string{"stylesheet"}, ct: contenttype.Stylesheet, want: true},
		{name: "positive rejects another type", mods: []string{"stylesheet"}, ct: contenttype.Document, want: false},
		{name: "negated pair rejects first type", mods: []string{"~stylesheet", "~xmlhttprequest"}, ct: contenttype.Stylesheet, want: false},
		{name: "negated pair rejects second type", mods: []string{"~stylesheet", "~xmlhttprequest"}, ct: contenttype.XMLHTTPRequest, want: false},
		{name: "negated pair keeps document", mods: []string{"~stylesheet", "~xmlhttprequest"}, ct: contenttype.Document, want: true},
		{name: "negated pair keeps unclassifiable traffic", mods: []string{"~stylesheet", "~xmlhttprequest"}, ct: contenttype.Other, want: true},
		{name: "two positives match either", mods: []string{"image", "stylesheet"}, ct: contenttype.Stylesheet, want: true},
		{name: "two positives still reject others", mods: []string{"image", "stylesheet"}, ct: contenttype.Script, want: false},
		{name: "mixed uses included minus excluded", mods: []string{"image", "stylesheet", "~image"}, ct: contenttype.Image, want: false},
		{name: "mixed keeps the included type left over", mods: []string{"image", "stylesheet", "~image"}, ct: contenttype.Stylesheet, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := mustParseModifiers(t, tt.mods...)
			if got := r.ModifiersMatchReqT(ctReq(), tt.ct); got != tt.want {
				t.Fatalf("ModifiersMatchReqT(mods=%q, ct=%v) = %v, want %v", tt.mods, tt.ct, got, tt.want)
			}
		})
	}
}

func TestContentTypeSetDoesNotMakeRuleConditionFree(t *testing.T) {
	t.Parallel()

	r := mustParseModifiers(t, "stylesheet")
	if !r.HasContentTypeCondition() {
		t.Fatalf("HasContentTypeCondition() = false after $stylesheet, want true")
	}
	if r.noConds {
		t.Fatalf("noConds = true after $stylesheet, want false: a content type is a condition")
	}

	plain := mustParseModifiers(t)
	if plain.HasContentTypeCondition() {
		t.Fatalf("HasContentTypeCondition() = true without a content type, want false")
	}
	if !plain.noConds {
		t.Fatalf("noConds = false without modifiers, want true")
	}
}

func TestNegatedOnlyContentTypeStartsFromAll(t *testing.T) {
	t.Parallel()

	r := mustParseModifiers(t, "~stylesheet")
	if r.ctTypes != contenttype.All&^contenttype.Mask(contenttype.Stylesheet) {
		t.Fatalf("ctTypes = %b, want every type but stylesheet (%b)", r.ctTypes, contenttype.All)
	}
}

func TestNegatedContentTypesThatExcludeEverythingAreRejected(t *testing.T) {
	t.Parallel()

	var r Rule
	err := r.ParseModifiers([]string{"~stylesheet", "~xmlhttprequest", "~document", "~subdocument",
		"~script", "~image", "~media", "~font", "~object", "~websocket", "~ping", "~other"})
	if err == nil {
		t.Fatalf("ParseModifiers of every negated content type = nil error, want a parse error")
	}
}

func TestNegatedAllContentTypeIsRejected(t *testing.T) {
	t.Parallel()

	var r Rule
	if err := r.ParseModifiers([]string{"~all"}); err == nil {
		t.Fatalf("ParseModifiers($~all) = nil error, want a parse error")
	}
}
