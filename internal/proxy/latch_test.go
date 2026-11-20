package proxy

import (
	"testing"
	"time"
)

func TestLatchExpiresAndIsRetried(t *testing.T) {
	p := &Proxy{}

	if d := p.addTransparentHost("pinned.example"); d != transparentHostRetryBase {
		t.Fatalf("first retry delay = %s, want %s", d, transparentHostRetryBase)
	}
	if p.shouldMITM("pinned.example") {
		t.Fatalf("host must be latched right after the failure")
	}
	if p.shouldMITM("sub.pinned.example") {
		t.Fatalf("subdomains must be latched too")
	}

	// Wind the deadline back: the latch must expire instead of lasting for the
	// life of the proxy.
	p.transparentHostsMu.Lock()
	p.transparentLatches["pinned.example"].retryAt = time.Now().Add(-time.Second)
	p.transparentHostsMu.Unlock()

	if !p.shouldMITM("pinned.example") {
		t.Fatalf("expired latch must re-arm MITM")
	}
}

func TestLatchBacksOffAndCaps(t *testing.T) {
	p := &Proxy{}

	want := []time.Duration{
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		transparentHostRetryMax,
		transparentHostRetryMax,
	}
	for i, expected := range want {
		if got := p.addTransparentHost("flaky.example"); got != expected {
			t.Fatalf("retry delay #%d = %s, want %s", i+1, got, expected)
		}
	}
}

func TestClearLatchReArmsMITM(t *testing.T) {
	p := &Proxy{}
	p.addTransparentHost("healed.example")
	p.addTransparentHost("healed.example")
	if p.shouldMITM("healed.example") {
		t.Fatalf("host must be latched")
	}

	if !p.clearLatch("healed.example", "test") {
		t.Fatalf("clearLatch reported nothing to clear")
	}
	if !p.shouldMITM("healed.example") {
		t.Fatalf("cleared latch must re-arm MITM")
	}
	if p.transparentLatchCount.Load() != 0 {
		t.Fatalf("latch count = %d, want 0", p.transparentLatchCount.Load())
	}
	if len(p.transparentHosts) != 0 || len(p.transparentHostsSet) != 0 {
		t.Fatalf("latch containers not emptied: %v / %v", p.transparentHosts, p.transparentHostsSet)
	}
	if p.clearLatch("healed.example", "test") {
		t.Fatalf("clearLatch must report nothing to clear the second time")
	}
	// The next failure after a heal must start from the base delay, not inherit
	// the inflated backoff.
	if got := p.addTransparentHost("healed.example"); got != transparentHostRetryBase {
		t.Fatalf("retry delay after heal = %s, want %s", got, transparentHostRetryBase)
	}
}

func TestForgetHealedLatchesDropsStaleEntries(t *testing.T) {
	p := &Proxy{}
	p.addTransparentHost("stale.example")
	p.addTransparentHost("fresh.example")

	p.transparentHostsMu.Lock()
	p.transparentLatches["stale.example"].retryAt = time.Now().Add(-2 * transparentHostForgetAfter)
	p.transparentHostsMu.Unlock()

	p.addTransparentHost("other.example")

	p.transparentHostsMu.RLock()
	defer p.transparentHostsMu.RUnlock()
	for _, host := range p.transparentHosts {
		if host == "stale.example" {
			t.Fatalf("stale latch not forgotten: %v", p.transparentHosts)
		}
	}
	if _, ok := p.transparentLatches["stale.example"]; ok {
		t.Fatalf("stale latch state not forgotten")
	}
	if _, ok := p.transparentHostsSet["stale.example"]; ok {
		t.Fatalf("stale latch set entry not forgotten")
	}
	if p.transparentLatchCount.Load() != 2 {
		t.Fatalf("latch count = %d, want 2", p.transparentLatchCount.Load())
	}
}

func TestHostFingerprintIsStableAndCaseInsensitive(t *testing.T) {
	first := hostFingerprint("www.youtube.com")
	if first != hostFingerprint("WWW.YouTube.com") {
		t.Fatalf("fingerprint must be case-insensitive")
	}
	if first == hostFingerprint("i.ytimg.com") {
		t.Fatalf("different hosts must not share a fingerprint")
	}
	if len(first) != 16 {
		t.Fatalf("fingerprint length = %d, want 16 hex chars", len(first))
	}
}
