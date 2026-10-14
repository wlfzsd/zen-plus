package asset

import "testing"

// Test for the load fast path (2026-10-13): every asset family's pattern
// contains a literal '#', so a line without one can decline the whole engine
// without running any family regex. The vast majority of lines in every
// subscription are network rules without '#'.
func TestAddRuleDeclinesHashlessLines(t *testing.T) {
	e, err := NewEngine("http://127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	hashless := []string{
		"||example.com^",
		"||ads.example.com^$script",
		"/ad\\.js$/",
		"@@||example.com^$document",
		"example.com$domain=foo.test,denyallow=bar.test",
		"",
	}
	for _, line := range hashless {
		if handled, err := e.AddRule(line, false); handled || err != nil {
			t.Errorf("AddRule(%q) = (%v, %v), want (false, nil)", line, handled, err)
		}
	}

	// Sanity: family lines (which all contain '#') are still handled.
	if handled, err := e.AddRule("example.com#@#.banner", false); err != nil || !handled {
		t.Errorf("AddRule(cosmetic) = (%v, %v), want (true, nil)", handled, err)
	}
	// CSS rules (`#$#`) only apply from trusted lists.
	if handled, err := e.AddRule("example.com#$#body { display: none; }", true); err != nil || !handled {
		t.Errorf("AddRule(cssrule) = (%v, %v), want (true, nil)", handled, err)
	}
}
