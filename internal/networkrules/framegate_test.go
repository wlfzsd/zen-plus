package networkrules

import (
	"net/http"
	"testing"
)

// Regression tests for the $permissions frame restriction (2026-10-08 B9):
// AdGuard docs §$permissions note (adguard_create-own-filters.md line 2321):
// "$permissions rules only take effect for main frame and sub frame
// requests." The live AdGuard Spyware list ships match-all rules of the form
// `/.*/$permissions=...,document`, which before B9 added a Permissions-Policy
// header to every response (images, JSON, 204s — 4255 responses in 31 min of
// decisions.jsonl).

func newTestResponse(status int, contentType string) *http.Response {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
	}
}

func permHeaderPresent(res *http.Response) bool {
	return len(res.Header.Values("Permissions-Policy")) > 0
}

func TestPermissionsFrameRestriction(t *testing.T) {
	t.Parallel()

	nr := New()
	rule := `/.*/$permissions=join-ad-interest-group=(),document`
	if _, err := nr.ParseRule(rule, nil); err != nil {
		t.Fatal(err)
	}
	if nr.frameScopedRules.Load() != 1 {
		t.Fatalf("frameScopedRules = %d, want 1", nr.frameScopedRules.Load())
	}

	const png = "https://cdn.example.com/pixel.png"

	t.Run("https main frame document load gets the header", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, png, http.Header{"Sec-Fetch-Dest": {"document"}})
		res := newTestResponse(http.StatusOK, "text/html; charset=utf-8")
		applied, err := nr.ModifyRes(req, res)
		if err != nil {
			t.Fatal(err)
		}
		if !permHeaderPresent(res) {
			t.Error("Permissions-Policy missing on main frame document load")
		}
		if len(applied) != 1 {
			t.Errorf("applied rules = %d, want 1", len(applied))
		}
	})

	t.Run("https iframe load gets the header", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, png, http.Header{"Sec-Fetch-Dest": {"iframe"}})
		res := newTestResponse(http.StatusOK, "text/html")
		if _, err := nr.ModifyRes(req, res); err != nil {
			t.Fatal(err)
		}
		if !permHeaderPresent(res) {
			t.Error("Permissions-Policy missing on iframe load")
		}
	})

	t.Run("legacy frame dest gets the header", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, png, http.Header{"Sec-Fetch-Dest": {"frame"}})
		res := newTestResponse(http.StatusOK, "text/html")
		if _, err := nr.ModifyRes(req, res); err != nil {
			t.Fatal(err)
		}
		if !permHeaderPresent(res) {
			t.Error("Permissions-Policy missing on legacy frame dest")
		}
	})

	t.Run("image response is not touched", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, png, http.Header{"Sec-Fetch-Dest": {"image"}})
		res := newTestResponse(http.StatusOK, "image/png")
		applied, err := nr.ModifyRes(req, res)
		if err != nil {
			t.Fatal(err)
		}
		if permHeaderPresent(res) {
			t.Error("Permissions-Policy added to an image response")
		}
		if len(applied) != 0 {
			t.Errorf("applied rules = %d, want 0", len(applied))
		}
	})

	t.Run("204 empty response is not touched", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "https://www.example.com/generate_204", http.Header{"Sec-Fetch-Dest": {"empty"}})
		res := newTestResponse(http.StatusNoContent, "")
		if _, err := nr.ModifyRes(req, res); err != nil {
			t.Fatal(err)
		}
		if permHeaderPresent(res) {
			t.Error("Permissions-Policy added to a 204 response")
		}
	})

	t.Run("https request without fetch metadata is not touched", func(t *testing.T) {
		t.Parallel()

		// Non-browser clients do not send fetch metadata; per docs line 2321
		// the effect is frame-scoped and a frame cannot be established.
		req := newTestRequest(t, png, nil)
		res := newTestResponse(http.StatusOK, "application/json")
		if _, err := nr.ModifyRes(req, res); err != nil {
			t.Fatal(err)
		}
		if permHeaderPresent(res) {
			t.Error("Permissions-Policy added to a metadata-less https response")
		}
	})

	t.Run("http request without fetch metadata keeps pre-B9 behavior", func(t *testing.T) {
		t.Parallel()

		// Fetch metadata only exists on https (repo convention, filter
		// isDocumentNavigation): a plain-http load cannot be classified and
		// keeps the pre-B9 (apply) behavior.
		req := newTestRequest(t, "http://www.example.com/", nil)
		res := newTestResponse(http.StatusOK, "text/html")
		if _, err := nr.ModifyRes(req, res); err != nil {
			t.Fatal(err)
		}
		if !permHeaderPresent(res) {
			t.Error("Permissions-Policy missing on http document load")
		}
	})
}

func TestPermissionsFrameRestrictionAppliesToNonMatchAllRules(t *testing.T) {
	t.Parallel()

	// The B9 gate is rule-general: it restricts every $permissions rule,
	// not only the match-all `/.*/` family. (Note: rules additionally
	// qualified by $domain cannot be exercised here — DomainModifier.
	// ShouldMatchRes is false unconditionally, a separate pre-existing
	// engine limitation.)
	nr := New()
	if _, err := nr.ParseRule(`||example.org^$permissions=geolocation=()`, nil); err != nil {
		t.Fatal(err)
	}
	if nr.frameScopedRules.Load() != 1 {
		t.Fatalf("frameScopedRules = %d, want 1", nr.frameScopedRules.Load())
	}

	req := newTestRequest(t, "https://example.org/page", http.Header{"Sec-Fetch-Dest": {"document"}})
	res := newTestResponse(http.StatusOK, "text/html")
	applied, err := nr.ModifyRes(req, res)
	if err != nil {
		t.Fatal(err)
	}
	if !permHeaderPresent(res) {
		t.Error("Permissions-Policy missing on matching frame load")
	}
	if len(applied) != 1 {
		t.Errorf("applied rules = %d, want 1", len(applied))
	}

	req2 := newTestRequest(t, "https://example.org/pixel.png", http.Header{"Sec-Fetch-Dest": {"image"}})
	res2 := newTestResponse(http.StatusOK, "image/png")
	applied2, err := nr.ModifyRes(req2, res2)
	if err != nil {
		t.Fatal(err)
	}
	if permHeaderPresent(res2) {
		t.Error("Permissions-Policy added to a non-frame response")
	}
	if len(applied2) != 0 {
		t.Errorf("applied rules = %d, want 0", len(applied2))
	}
}

func TestCspHasNoFrameRestriction(t *testing.T) {
	t.Parallel()

	// $csp carries no documented frame restriction (docs §$csp, lines
	// 1590-1633), so it must keep applying outside frame loads — pins the
	// boundary of the B9 gate to $permissions only.
	nr := New()
	if _, err := nr.ParseRule(`||example.org^$csp=frame-src 'none'`, nil); err != nil {
		t.Fatal(err)
	}

	req := newTestRequest(t, "https://example.org/script.js", http.Header{"Sec-Fetch-Dest": {"script"}})
	res := newTestResponse(http.StatusOK, "application/javascript")
	applied, err := nr.ModifyRes(req, res)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Header.Values("Content-Security-Policy")) == 0 {
		t.Error("Content-Security-Policy missing for non-frame response")
	}
	if len(applied) != 1 {
		t.Errorf("applied rules = %d, want 1", len(applied))
	}
}

func TestNoFrameScopedRulesSkipsDestCheck(t *testing.T) {
	t.Parallel()

	// Without frame-scoped rules the gate must stay fully inert: no
	// Sec-Fetch-Dest read, no behavioral change (guards the counter fast
	// path).
	nr := New()
	if _, err := nr.ParseRule(`||example.org^$csp=frame-src 'none'`, nil); err != nil {
		t.Fatal(err)
	}
	if nr.frameScopedRules.Load() != 0 {
		t.Fatalf("frameScopedRules = %d, want 0", nr.frameScopedRules.Load())
	}

	req := newTestRequest(t, "https://example.org/script.js", nil)
	res := newTestResponse(http.StatusOK, "application/javascript")
	if _, err := nr.ModifyRes(req, res); err != nil {
		t.Fatal(err)
	}
	if len(res.Header.Values("Content-Security-Policy")) == 0 {
		t.Error("Content-Security-Policy missing")
	}
}
