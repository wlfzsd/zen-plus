package tmp_enginebench

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules"
)

// TestTokenIdxInvalidationOnLateInsert proves the lazy token index is
// invalidated when regexp rules are inserted AFTER a Get has built it
// (2026-10-06 main-thread hardening beyond the V3 probe, which never
// exercised insert-after-get).
func TestTokenIdxInvalidationOnLateInsert(t *testing.T) {
	nr := networkrules.New()
	name := "t"
	if _, err := nr.ParseRule("/earlyad/", &name); err != nil {
		t.Fatal(err)
	}
	if _, err := nr.ParseRule("@@||example.com^$important", &name); err != nil {
		t.Fatal(err)
	}

	mkReq := func(raw string) *http.Request {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{Method: http.MethodGet, URL: u, Header: http.Header{}}
	}

	// First Get builds the index over the early rule set.
	if got := countBlock(t, nr, mkReq("https://tracker.example/earlyad/pixel.js")); got != 1 {
		t.Fatalf("early rule should block once, got %d", got)
	}

	// Late regexp insert must invalidate the index; the new rule must take
	// effect on the next Get (no stale-index miss, no panic).
	if _, err := nr.ParseRule("/latebanner[0-9]+/", &name); err != nil {
		t.Fatal(err)
	}
	if got := countBlock(t, nr, mkReq("https://cdn.example/latebanner123.js")); got != 1 {
		t.Fatalf("late rule should block once after index rebuild, got %d", got)
	}
	if got := countBlock(t, nr, mkReq("https://cdn.example/latebannerX.js")); got != 0 {
		t.Fatalf("non-matching URL must not block, got %d", got)
	}
}

func countBlock(t *testing.T, nr *networkrules.NetworkRules, req *http.Request) int {
	t.Helper()
	_, block, _ := nr.ModifyReq(req)
	if block {
		return 1
	}
	return 0
}
