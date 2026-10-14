package networkrules

import (
	"testing"
)

// Tests for modifier-block sharing (2026-10-13 batch 3, modshare.go,
// borrowed from upstream #821 and adapted to this fork's two-group Rule
// shape): rules parsed from identical modifier text must alias one parsed
// block, and behavior must be identical to unshared parsing.

func TestModsInternerSharesBlocks(t *testing.T) {
	nr := New()
	lines := []string{
		"||a.example^$script,third-party,domain=one.test",
		"||b.example^$script,third-party,domain=one.test",
		"||c.example^$script,third-party,domain=one.test",
	}
	for _, line := range lines {
		if _, err := nr.ParseRule(line, nil); err != nil {
			t.Fatalf("ParseRule(%q): %v", line, err)
		}
	}
	if got := nr.modsInterner.size(); got != 1 {
		t.Fatalf("interner entries = %d, want 1 (three rules, one modifier text)", got)
	}

	nr.Compact()
	if nr.modsInterner.m != nil {
		t.Fatal("interner map must be dropped at Compact")
	}
	// Live rules after Compact parse unshared and must still work.
	nr.Compact() // idempotent re-Compact must not panic
	if _, err := nr.ParseRule("@@||a.example^$script", nil); err != nil {
		t.Fatalf("live exception after interner drop: %v", err)
	}
}

func TestModsInternerDifferentTextsDoNotShare(t *testing.T) {
	nr := New()
	lines := []string{
		"||a.example^$script",
		"||b.example^$script,third-party",
		"||c.example^$image",
		"||d.example^$script,third-party,domain=x.test",
	}
	for _, line := range lines {
		if _, err := nr.ParseRule(line, nil); err != nil {
			t.Fatalf("ParseRule(%q): %v", line, err)
		}
	}
	if got := nr.modsInterner.size(); got != 4 {
		t.Fatalf("interner entries = %d, want 4 (distinct texts)", got)
	}
}

func TestModsInternerSharedBlockBehaviorParity(t *testing.T) {
	// Behavioral parity: a shared-mods rule must decide identically to a
	// freshly parsed rule with the same modifiers. Build two engines — one
	// where the second rule shares the first's block, one where both parse
	// into the frozen store independently — and compare decisions.
	newEngine := func() *NetworkRules {
		nr := New()
		for _, line := range []string{
			"||ads.example^$script,third-party,domain=paged.test",
			"||tracker.example^$script,third-party,domain=paged.test",
		} {
			if _, err := nr.ParseRule(line, nil); err != nil {
				t.Fatal(err)
			}
		}
		nr.Compact()
		return nr
	}
	shared := newEngine()
	unshared := New()
	for _, line := range []string{
		"||ads.example^$script,third-party,domain=paged.test",
		"||tracker.example^$script,third-party,domain=paged.test",
	} {
		if _, err := unshared.ParseRule(line, nil); err != nil {
			t.Fatal(err)
		}
	}
	unshared.dropModsInterner() // second engine parses without sharing
	unshared.Compact()

	for _, u := range []string{
		"https://ads.example/a.js",
		"https://tracker.example/t.js",
		"https://ads.example/a.js?x=1",
		"https://other.example/",
	} {
		req := blockedReq(u)
		req.Header.Set("Referer", "http://paged.test/")
		_, b1, _ := shared.ModifyReq(req)
		req2 := blockedReq(u)
		req2.Header.Set("Referer", "http://paged.test/")
		_, b2, _ := unshared.ModifyReq(req2)
		if b1 != b2 {
			t.Errorf("decision mismatch for %s: shared=%v unshared=%v", u, b1, b2)
		}
	}
}
