package rulemodifiers

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestRemoveParamModifier(t *testing.T) {
	t.Parallel()

	t.Run("ModifyReq", func(t *testing.T) {
		t.Parallel()

		mustParse := func(modifier string) RemoveParamModifier {
			t.Helper()

			var rm RemoveParamModifier
			if err := rm.Parse(modifier); err != nil {
				t.Fatalf("Failed to parse modifier: %v", err)
			}
			return rm
		}

		tests := []struct {
			name     string
			modifier RemoveParamModifier
			url      string
			want     string
			modified bool
		}{
			{
				name:     "generic removes all params",
				modifier: mustParse("removeparam"),
				url:      "https://example.com/?id=123&name=test",
				want:     "https://example.com/",
				modified: true,
			},
			{
				name:     "exact removes the specified param",
				modifier: mustParse("removeparam=id"),
				url:      "https://example.com/?id=123&known=1&name=test",
				want:     "https://example.com/?known=1&name=test",
				modified: true,
			},
			{
				name:     "exact leaves the URL unchanged if the param is not found",
				modifier: mustParse("removeparam=unknown"),
				url:      "https://example.com/?id=123&name=test&known=1",
				want:     "https://example.com/?id=123&name=test&known=1",
				modified: false,
			},
			{
				name:     "exact leaves the URL unchanged if the URL has no query params",
				modifier: mustParse("removeparam=id"),
				url:      "https://example.com:443/",
				want:     "https://example.com:443/",
				modified: false,
			},
			{
				name:     "exact inverse removes all params except the specified one",
				modifier: mustParse("removeparam=~id"),
				url:      "https://example.com/?id=123&name=test&known=1",
				want:     "https://example.com/?id=123",
				modified: true,
			},
			{
				name:     "regexp removes matching params",
				modifier: mustParse("removeparam=/id=\\d+/"),
				url:      "https://example.com/?id=123&name=test",
				want:     "https://example.com/?name=test",
				modified: true,
			},
			{
				name:     "regexp leaves the URL unchanged if the param is not found",
				modifier: mustParse("removeparam=/id=[a-z]+/"),
				url:      "https://example.com/?id=123&name=test",
				want:     "https://example.com/?id=123&name=test",
				modified: false,
			},
			{
				name:     "regexp only removes matching params for values with the same key",
				modifier: mustParse("removeparam=/id=\\d+/"),
				url:      "https://example.com/?id=test0&id=1&id=2&id=3&id=test1&id=test2",
				want:     "https://example.com/?id=test0&id=test1&id=test2",
				modified: true,
			},
			{
				name:     "inverse regexp removes non-matching params",
				modifier: mustParse("removeparam=~/id=\\d+/"),
				url:      "https://example.com/?id=123&name=test&known=1",
				want:     "https://example.com/?id=123",
				modified: true,
			},
		}

		// 2026-10-08 (B7): ModifyQuery takes the whole request, matches the
		// encoded RawQuery text in place and applies the method whitelist
		// (GET/HEAD/OPTIONS + bodyless POST). For unreserved-ASCII queries
		// the expected URLs equal the pre-B7 decoded-mode results, so the
		// table below keeps the original expectations.
		// 2026-10-08 (perf audit ①): the query is pre-split into a
		// per-request QueryState (AcquireQueryState) and the joined form is
		// written back once via Finalize, mirroring the production wiring in
		// networkrules.ModifyReq.
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				req, err := http.NewRequest(http.MethodGet, tt.url, nil)
				if err != nil {
					t.Fatalf("build request for %q: %v", tt.url, err)
				}

				qs := AcquireQueryState(req)
				modified := tt.modifier.ModifyQuery(req, qs)
				if qs != nil {
					if raw, changed := qs.Finalize(); changed {
						req.URL.RawQuery = raw
					}
					ReleaseQueryState(qs)
				}
				if modified != tt.modified {
					t.Errorf("ModifyQuery() modified = %v, want %v", modified, tt.modified)
				}

				got := req.URL.String()
				if got != tt.want {
					t.Errorf("ModifyQuery() got URL = %v, want %v", got, tt.want)
				}
			})
		}
	})

	// 2026-10-08 (B7): the method whitelist (AdGuard doc 2634) — $removeparam
	// applies to GET/HEAD/OPTIONS and bodyless POST only.
	t.Run("method whitelist", func(t *testing.T) {
		t.Parallel()

		var rm RemoveParamModifier
		if err := rm.Parse("removeparam=id"); err != nil {
			t.Fatalf("parse modifier: %v", err)
		}

		mustReq := func(method, rawURL string, body string) *http.Request {
			t.Helper()
			req, err := http.NewRequest(method, rawURL, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if body != "" {
				req.Body = io.NopCloser(strings.NewReader(body))
				req.ContentLength = int64(len(body))
			}
			return req
		}

		put := mustReq(http.MethodPut, "https://example.com/?id=1", "")
		bodylessPost := mustReq(http.MethodPost, "https://example.com/?id=1", "")
		postWithBody := mustReq(http.MethodPost, "https://example.com/?id=1", "body")

		// 2026-10-08 (perf audit ①): method eligibility is hoisted into
		// AcquireQueryState (nil for ineligible requests); the helper mirrors
		// the production call pattern (networkrules.ModifyReq).
		call := func(req *http.Request) bool {
			qs := AcquireQueryState(req)
			modified := rm.ModifyQuery(req, qs)
			if qs != nil {
				if raw, changed := qs.Finalize(); changed {
					req.URL.RawQuery = raw
				}
				ReleaseQueryState(qs)
			}
			return modified
		}

		if call(put) {
			t.Error("PUT request must not be modified (doc 2634)")
		}

		if !call(bodylessPost) {
			t.Error("bodyless POST request should be modified (doc 2634 sometimes-POST)")
		}

		if call(postWithBody) {
			t.Error("POST with a body must not be modified (doc 2634)")
		}
	})

	t.Run("cancels", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			a        RemoveParamModifier
			b        RemoveParamModifier
			expected bool
		}{
			{
				"identical modifiers - should cancel",
				RemoveParamModifier{
					kind:   removeparamKindRegexp,
					param:  "id",
					regexp: regexp.MustCompile(`^\d+$`),
				},
				RemoveParamModifier{
					kind:   removeparamKindRegexp,
					param:  "id",
					regexp: regexp.MustCompile(`^\d+$`),
				},
				true,
			},
			{
				"empty modifiers - should cancel",
				RemoveParamModifier{},
				RemoveParamModifier{},
				true,
			},
			{
				"modifiers with different \"param\" - should not cancel",
				RemoveParamModifier{
					kind:   removeparamKindRegexp,
					param:  "id",
					regexp: regexp.MustCompile(`^\d+$`),
				},
				RemoveParamModifier{
					kind:   removeparamKindRegexp,
					param:  "user",
					regexp: regexp.MustCompile(`^\d+$`),
				},
				false,
			},
			{
				"modifiers with different \"kind\" - should not cancel",
				RemoveParamModifier{
					kind:   removeparamKindRegexp,
					param:  "id",
					regexp: regexp.MustCompile(`^\d+$`),
				},
				RemoveParamModifier{
					kind:   removeparamKindExact,
					param:  "id",
					regexp: regexp.MustCompile(`^\d+$`),
				},
				false,
			},
			{
				"modifiers with different \"regexp\" - should not cancel",
				RemoveParamModifier{
					kind:   removeparamKindRegexp,
					param:  "id",
					regexp: regexp.MustCompile(`^\d+$`),
				},
				RemoveParamModifier{
					kind:   removeparamKindRegexp,
					param:  "id",
					regexp: regexp.MustCompile("^[a-zA-Z]+$"),
				},
				false,
			},
			{
				"modifiers with nil regexes - should cancel",
				RemoveParamModifier{
					kind:   removeparamKindRegexp,
					param:  "id",
					regexp: nil,
				},
				RemoveParamModifier{
					kind:   removeparamKindRegexp,
					param:  "id",
					regexp: nil,
				},
				true,
			},
			{
				"modifier with nil regex should not cancel with non-nil regex",
				RemoveParamModifier{
					kind:   removeparamKindRegexp,
					param:  "id",
					regexp: nil,
				},
				RemoveParamModifier{
					kind:   removeparamKindRegexp,
					param:  "id",
					regexp: regexp.MustCompile(`^\d+$`),
				},
				false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				result := tt.a.Cancels(&tt.b)
				if result != tt.expected {
					t.Errorf("RemoveParamModifier.Cancels() = %t, want %t", result, tt.expected)
				}
			})
		}
	})
}
