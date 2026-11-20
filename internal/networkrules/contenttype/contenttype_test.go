package contenttype

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func reqWith(dest string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	if dest != "" {
		r.Header.Set("Sec-Fetch-Dest", dest)
	}
	return r
}

func TestMaskMatches(t *testing.T) {
	t.Parallel()

	t.Run("zero mask matches everything", func(t *testing.T) {
		t.Parallel()

		var m Mask
		for _, ct := range []Type{Document, Stylesheet, Other} {
			if !m.Matches(ct) {
				t.Fatalf("zero Mask.Matches(%v) = false, want true", ct)
			}
		}
	})

	t.Run("single type matches only itself", func(t *testing.T) {
		t.Parallel()

		m := Mask(Document)
		if !m.Matches(Document) {
			t.Fatalf("Mask(Document).Matches(Document) = false, want true")
		}
		if m.Matches(Stylesheet) || m.Matches(Other) {
			t.Fatalf("Mask(Document) matched a type it does not contain")
		}
	})
}

func TestFromRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dest string
		want Type
	}{
		{name: "document", dest: "document", want: Document},
		{name: "iframe is a subdocument", dest: "iframe", want: Subdocument},
		{name: "style is a stylesheet", dest: "style", want: Stylesheet},
		{name: "empty dest is an xhr", dest: "empty", want: XMLHTTPRequest},
		{name: "unknown dest is other", dest: "manifest", want: Other},
		{name: "missing dest is other", dest: "", want: Other},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := FromRequest(reqWith(tt.dest)); got != tt.want {
				t.Fatalf("FromRequest(dest=%q) = %v, want %v", tt.dest, got, tt.want)
			}
		})
	}

	t.Run("websocket upgrade", func(t *testing.T) {
		t.Parallel()

		r := reqWith("")
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		if got := FromRequest(r); got != Websocket {
			t.Fatalf("FromRequest(websocket) = %v, want %v", got, Websocket)
		}
	})
}

func TestFromResponse(t *testing.T) {
	t.Parallel()

	t.Run("request destination wins over content type", func(t *testing.T) {
		t.Parallel()

		res := &http.Response{Header: http.Header{"Content-Type": {"text/css"}}}
		if got := FromResponse(reqWith("script"), res); got != Script {
			t.Fatalf("FromResponse(script request, text/css response) = %v, want %v", got, Script)
		}
	})

	// 2026-11-20 decision: no Content-Type fallback (upstream has one). A
	// request without Fetch Metadata stays Other, so typed rules keep the
	// pre-B10 behaviour of not touching traffic Zen cannot classify.
	t.Run("no request metadata never falls back to content type", func(t *testing.T) {
		t.Parallel()

		cases := []string{"text/html; charset=utf-8", "application/json", "image/webp"}
		for _, ct := range cases {
			res := &http.Response{Header: http.Header{"Content-Type": {ct}}}
			if got := FromResponse(reqWith(""), res); got != Other {
				t.Fatalf("FromResponse(no dest, %q) = %v, want %v", ct, got, Other)
			}
		}
	})

	t.Run("no content type is other", func(t *testing.T) {
		t.Parallel()

		res := &http.Response{Header: http.Header{}}
		if got := FromResponse(reqWith(""), res); got != Other {
			t.Fatalf("FromResponse(no content type) = %v, want %v", got, Other)
		}
	})
}
func TestByName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want Type
		ok   bool
	}{
		{name: "stylesheet", want: Stylesheet, ok: true},
		{name: "css", want: Stylesheet, ok: true},
		{name: "~image", want: Image, ok: true},
		{name: "XMLHTTPRequest", want: XMLHTTPRequest, ok: true},
		{name: "xhr", want: XMLHTTPRequest, ok: true},
		{name: "domain", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := ByName(tt.name)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ByName(%q) = (%v, %v), want (%v, %v)", tt.name, got, ok, tt.want, tt.ok)
			}
		})
	}
}
