package networkrules

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
)

// Tests for the live allowlist path (2026-10-13 batch-2): after Compact the
// main stores are frozen; whitelist-server inserts route into the RWMutex
// live store and must take effect immediately, while writes to the frozen
// stores are rejected instead of racing serving readers.

func TestLiveExceptionAppliesImmediately(t *testing.T) {
	nr := New()
	if _, err := nr.ParseRule("||block.example^", nil); err != nil {
		t.Fatal(err)
	}
	nr.Compact() // freeze

	req := blockedReq("http://block.example/x.js")
	if _, blocked, _ := nr.ModifyReq(req); !blocked {
		t.Fatal("precondition: request must be blocked before allowlist")
	}

	if _, err := nr.ParseRule("@@||block.example^", nil); err != nil {
		t.Fatalf("live exception insert: %v", err)
	}

	req2 := blockedReq("http://block.example/x.js")
	if _, blocked, _ := nr.ModifyReq(req2); blocked {
		t.Fatal("live exception did not cancel the block immediately")
	}
}

func TestFrozenEngineRejectsPrimaryRules(t *testing.T) {
	nr := New()
	nr.Compact()
	if _, err := nr.ParseRule("||example.com^", nil); err == nil {
		t.Fatal("primary rule after freeze must be rejected, got nil error")
	}
	if _, err := nr.ParseRule("0.0.0.0 example.com", nil); err == nil {
		t.Fatal("hosts line after freeze must be rejected, got nil error")
	}
}

func TestFrozenEngineRejectsBadfilterInertly(t *testing.T) {
	// A live $badfilter is accepted (same as the pre-freeze behavior) but
	// inert: applyBadfilters already ran. It must not panic or corrupt.
	nr := New()
	if _, err := nr.ParseRule("||x.example^", nil); err != nil {
		t.Fatal(err)
	}
	nr.Compact()
	if _, err := nr.ParseRule("@@||x.example^$badfilter", nil); err != nil {
		t.Fatalf("live badfilter insert: %v", err)
	}
	req := blockedReq("http://x.example/a.js")
	if _, blocked, _ := nr.ModifyReq(req); !blocked {
		t.Fatal("inert live badfilter must not disable anything")
	}
}

func TestLiveExceptionPageScoped(t *testing.T) {
	nr := New()
	if _, err := nr.ParseRule("||ads.example^$script", nil); err != nil {
		t.Fatal(err)
	}
	nr.Compact()

	req := blockedReq("http://site.example/page")
	req.Header.Set("Referer", "http://site.example/page")
	req.Header.Set("Sec-Fetch-Dest", "document")
	if _, err := nr.ParseRule("@@||ads.example^$document", nil); err != nil {
		t.Fatal(err)
	}
	req2 := blockedReq("http://ads.example/tracker.js")
	req2.Header.Set("Referer", "http://site.example/page")
	req2.Header.Set("Sec-Fetch-Dest", "script")
	if _, blocked, _ := nr.ModifyReq(req2); blocked {
		t.Fatal("live page-scoped exception did not cancel the block")
	}
}

func TestLiveConcurrencyStress(t *testing.T) {
	// Concurrent serving reads vs live inserts: without the live store this
	// races (torn tree edges / map writes). Go's runtime fatals on concurrent
	// map writes; run with -race in toolchained environments.
	nr := New()
	if _, err := nr.ParseRule("||stress.example^", nil); err != nil {
		t.Fatal(err)
	}
	nr.Compact()

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				req := blockedReq(fmt.Sprintf("http://stress.example/%d", i))
				_, _, _ = nr.ModifyReq(req)
			}
		}(g)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				rule := fmt.Sprintf("@@||allow-%d-%d.example^", g, i)
				if _, err := nr.ParseRule(rule, nil); err != nil {
					t.Errorf("live insert %q: %v", rule, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	// Every live rule must be effective afterwards.
	for g := 0; g < 4; g++ {
		for i := 0; i < 50; i++ {
			host := fmt.Sprintf("allow-%d-%d.example", g, i)
			req := blockedReq("http://" + host + "/x")
			if _, blocked, _ := nr.ModifyReq(req); blocked {
				t.Fatalf("live rule for %s not effective", host)
			}
		}
	}
}

func blockedReq(rawURL string) *http.Request {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return &http.Request{
		Method: http.MethodGet,
		URL:    u,
		Header: http.Header{},
	}
}
