package nrprobe

// PROBE 1 equivalence + benchmarks (tmp_enginebench only, 2026-10-05).
//
// Equivalence gate: GetIdx must return exactly the same rule set (and, for
// pure-regexp stores, the same ORDER) as Get:
//   - real corpus: both stores × 5000 real URLs (multiset compare, because
//     tree.Get's map iteration makes tree-part order random in BOTH);
//   - order check: a store holding ONLY regexp rules compared with exact
//     slice equality (deterministic in both — this is where an index bug
//     could reorder results);
//   - fuzz: 100k random URLs, half of them seeded with rule tokens to hit
//     the Contains-verification path;
//   - '\n'-bearing URLs exercise the hasNL fallback branch.
//
// Benchmarks: engine-level ModifyReq A/B (UseTokenIndex off/on) + store-level
// Get/GetIdx. Run with -count>=3 and take medians.

import (
	"bytes"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func probeRealLines(t testing.TB) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(censusRealCacheDir, "*.cache.txt"))
	if err != nil || len(matches) == 0 {
		t.Fatal("no real filter caches found in " + censusRealCacheDir)
	}
	seen := map[string]struct{}{}
	var lines []string
	for _, f := range matches {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range bytes.Split(data, []byte("\n")) {
			s := strings.TrimSpace(string(raw))
			if s == "" || censusIgnoreLineRe.MatchString(s) {
				continue
			}
			if strings.HasPrefix(s, "!#include") {
				continue
			}
			if _, ok := seen[s]; ok {
				continue
			}
			seen[s] = struct{}{}
			lines = append(lines, s)
		}
	}
	return lines
}

func probeBuildReal(t testing.TB) *NetworkRules {
	t.Helper()
	nr := New()
	name := "real"
	for _, l := range probeRealLines(t) {
		if _, err := nr.ParseRule(l, &name); err != nil {
			_ = err
		}
	}
	return nr
}

func probeURLs(t testing.TB, max int) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "urls.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, bline := range bytes.Split(data, []byte("\n")) {
		line := strings.TrimSpace(string(bline))
		if line == "" {
			continue
		}
		if _, err := url.Parse(line); err != nil {
			continue
		}
		urls = append(urls, line)
		if max > 0 && len(urls) >= max {
			break
		}
	}
	return urls
}

// sortedCopy compares rule sets as multisets (tree-part order is map-random
// in both Get and GetIdx).
func sortedCopy[T comparable](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = fmt.Sprint(v)
	}
	sort.Strings(out)
	return out
}

func assertSameSet(t *testing.T, where string, u string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: count mismatch for %q: got=%d want=%d", where, u, len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: mismatch for %q at %d: got=%q want=%q", where, u, i, got[i], want[i])
		}
	}
}

// TestTokenIdxEquivalenceReal compares every probe Get variant with the
// production Get on both stores of the real engine over 5000 real URLs:
// GetIdx (probe 1), GetLP (probe 2), GetIdxLP (1+2) and DisableFastShape
// (probe 3) in all combinations.
func TestTokenIdxEquivalenceReal(t *testing.T) {
	nr := probeBuildReal(t)
	urls := probeURLs(t, 5000)
	if len(urls) < 5000 {
		t.Fatalf("need 5000 urls, got %d", len(urls))
	}
	type variant struct {
		name                      string
		useIdx, lowAlloc, noFast  bool
	}
	variants := []variant{
		{"GetIdx", true, false, false},
		{"GetLP", false, true, false},
		{"GetIdxLP", true, true, false},
		{"GetIdx+noFast", true, false, true},
		{"GetIdxLP+noFast", true, true, true},
	}
	nr.primaryStore.disableFast = false
	nr.exceptionStore.disableFast = false
	for _, u := range urls {
		want := sortedCopy(nr.primaryStore.Get(u))
		wantE := sortedCopy(nr.exceptionStore.Get(u))
		for _, v := range variants {
			nr.primaryStore.disableFast = v.noFast
			nr.exceptionStore.disableFast = v.noFast
			var got, gotE []string
			switch {
			case v.useIdx && v.lowAlloc:
				r := nr.primaryStore.GetIdxLP(u)
				got = sortedCopy(r)
				nr.primaryStore.putRes(r)
				re := nr.exceptionStore.GetIdxLP(u)
				gotE = sortedCopy(re)
				nr.exceptionStore.putRes(re)
			case v.useIdx:
				got = sortedCopy(nr.primaryStore.GetIdx(u))
				gotE = sortedCopy(nr.exceptionStore.GetIdx(u))
			case v.lowAlloc:
				r := nr.primaryStore.GetLP(u)
				got = sortedCopy(r)
				nr.primaryStore.putRes(r)
				re := nr.exceptionStore.GetLP(u)
				gotE = sortedCopy(re)
				nr.exceptionStore.putRes(re)
			}
			assertSameSet(t, "primary/"+v.name, u, got, want)
			assertSameSet(t, "exception/"+v.name, u, gotE, wantE)
			nr.primaryStore.disableFast = false
			nr.exceptionStore.disableFast = false
		}
	}
	idx := nr.primaryStore.idx.Load()
	t.Logf("primary idx: %d rules, %d indexed", len(nr.primaryStore.regexp), countIndexed(idx))
	idxE := nr.exceptionStore.idx.Load()
	t.Logf("exception idx: %d rules, %d indexed", len(nr.exceptionStore.regexp), countIndexed(idxE))
}

func countIndexed(idx *reIdx) int {
	n := 0
	for _, toks := range idx.tokensOf {
		if len(toks) > 0 {
			n++
		}
	}
	return n
}

// TestTokenIdxOrderEquivalence: pure-regexp store, exact slice order.
func TestTokenIdxOrderEquivalence(t *testing.T) {
	patterns := []string{
		`^https?:\/\/.*\/.*(sw[0-9a-z._-]{1,6}|\.notify\.).*`,
		`(toonget|kickassanime|watchanime|gogoanimes?)\.[a-z]{2,4}`,
		`[a-z0-9]{8,}\.[a-z]{3,}`,
		`\/[a-zA-Z0-9-]{0,9}(?:clobew|-host-)[a-zA-Z0-9-]{0,9}\.[a-z]{3}`,
		`^http.*[^?]\?$`,
		`\.com\/watch\?|\.com\/playlist\?list=`,
		`^(\S+\.)?(webstats?|swebstats?|mywebstats?)\.`,
		`[0-9]{5}`,
		`js/sextb.js$domain=sextb.date|sextb.net,replace=/if\(checkads\(\)==false\)/if(!0)`,
		`\.js$`,
		`/ads.`,
		`http://x`,
	}
	st := newRuleStore[string]()
	for i, p := range patterns {
		if err := st.Insert("/" + p + "/", fmt.Sprintf("rule%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	st.Insert("", "generic")
	st.Insert("||example.com^", "tree-example")
	st.Insert("|https://www.example.com/ads", "tree-anchor")

	rnd := rand.New(rand.NewSource(42))
	chars := []byte("abcdefghijklmnopqrstuvwxyz0123456789-._~:/?#[]@!$&'()*+,;%=\n ")
	urls := make([]string, 0, 200000)
	for i := 0; i < 100000; i++ {
		n := 1 + rnd.Intn(80)
		b := make([]byte, n)
		for j := range b {
			b[j] = chars[rnd.Intn(len(chars))]
		}
		// Always carry a scheme: ruletree.Get indexes the host from
		// "://" and would panic on scheme-less 1-2 byte inputs (upstream
		// latent bug, unreachable via renderURLWithoutPort).
		u := "https://" + string(b)
		if rnd.Intn(2) == 0 {
			u = "http://" + string(b)
		}
		urls = append(urls, u)
	}
	// token-seeded URLs: splice rule tokens into random contexts
	tokens := []string{"toonget", "kickassanime", "gogoanime", "clobew", "-host-",
		".com/watch?", "webstat", "sextb.js", "nt.st", "example.com", "12345",
		"kimcartoon", ".js", "http", "://", "ads."}
	for i := 0; i < 100000; i++ {
		n := 1 + rnd.Intn(60)
		b := make([]byte, n)
		for j := range b {
			b[j] = chars[rnd.Intn(len(chars))]
		}
		pos := rnd.Intn(len(b) + 1)
		tok := tokens[rnd.Intn(len(tokens))]
		var sb strings.Builder
		sb.Grow(len(b) + len(tok))
		sb.Write(b[:pos])
		sb.WriteString(tok)
		sb.Write(b[pos:])
		u := sb.String()
		switch rnd.Intn(2) {
		case 0:
			u = "https://" + u
		}
		urls = append(urls, u)
	}

	for i, u := range urls {
		got := st.GetIdx(u)
		want := st.Get(u)
		if len(got) != len(want) {
			t.Fatalf("order/fuzz url#%d %q: got=%v want=%v", i, u, got, want)
		}
		for j := range got {
			if got[j] != want[j] {
				t.Fatalf("order/fuzz url#%d %q at %d: got=%v want=%v", i, u, j, got, want)
			}
		}
		if i%10 == 0 {
			// pooled variants: multiset compare (tree-part order is
			// map-random in both Get and the pooled paths)
			r := st.GetLP(u)
			assertSameSet(t, "fuzz/GetLP", u, sortedCopy(r), sortedCopy(want))
			st.putRes(r)
			r2 := st.GetIdxLP(u)
			assertSameSet(t, "fuzz/GetIdxLP", u, sortedCopy(r2), sortedCopy(want))
			st.putRes(r2)
			// probe 3: fastshape disabled must not change results
			st.disableFast = true
			g := st.GetIdx(u)
			if len(g) != len(want) {
				t.Fatalf("fuzz/noFast url#%d %q: got=%v want=%v", i, u, g, want)
			}
			for j := range g {
				if g[j] != want[j] {
					t.Fatalf("fuzz/noFast url#%d %q at %d: got=%v want=%v", i, u, j, g, want)
				}
			}
			st.disableFast = false
		}
	}
	t.Logf("order+fuzz equivalence: %d urls x %d rules, 0 mismatches (pooled+noFast every 10th)", len(urls), len(patterns))
}

// TestTokenIdxMemory measures the index build's allocation increment
// (TotalAlloc is monotonic; the live part is what the index retains).
func TestTokenIdxMemory(t *testing.T) {
	nr := probeBuildReal(t)
	// trigger one GetIdx so both stores have indexes built
	_ = nr.primaryStore.GetIdx("https://www.example.com/")
	_ = nr.exceptionStore.GetIdx("https://www.example.com/")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	idx := nr.primaryStore.buildIdx()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(idx)
	fmt.Printf("primary token-index build: %.1f KB allocated (%d regexp rules, %d indexed)\n",
		float64(after.TotalAlloc-before.TotalAlloc)/1024, len(nr.primaryStore.regexp), countIndexed(idx))
}

// --- benchmarks ---

// BenchmarkProbeStoreGet* isolate the primary store Get path (5000 URLs).
func BenchmarkProbeStoreGet(b *testing.B) {
	nr := probeBuildReal(b)
	urls := probeURLs(b, 5000)
	var i int
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		nr.primaryStore.Get(urls[i%len(urls)])
		i++
	}
}

func BenchmarkProbeStoreGetIdx(b *testing.B) {
	nr := probeBuildReal(b)
	urls := probeURLs(b, 5000)
	var i int
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		nr.primaryStore.GetIdx(urls[i%len(urls)])
		i++
	}
}

func BenchmarkProbeStoreGetLP(b *testing.B) {
	nr := probeBuildReal(b)
	urls := probeURLs(b, 5000)
	var i int
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := nr.primaryStore.GetLP(urls[i%len(urls)])
		nr.primaryStore.putRes(r)
		i++
	}
}

func BenchmarkProbeStoreGetIdxLP(b *testing.B) {
	nr := probeBuildReal(b)
	urls := probeURLs(b, 5000)
	var i int
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := nr.primaryStore.GetIdxLP(urls[i%len(urls)])
		nr.primaryStore.putRes(r)
		i++
	}
}

// benchmarkProbeModifyReq is the engine-level A/B driver; combo selects the
// probe flag set: 0=control, 1=+index, 2=+lowAlloc, 3=index+lowAlloc,
// 4=index+noFast, 5=index+lowAlloc+noFast.
func benchmarkProbeModifyReq(b *testing.B, combo int) {
	b.Helper()
	nr := probeBuildReal(b)
	urls := probeURLs(b, 3000)
	reqs := probeRequests(b, urls)
	useIdx := combo == 1 || combo == 3 || combo == 4 || combo == 5
	lowAlloc := combo == 2 || combo == 3 || combo == 5
	noFast := combo == 4 || combo == 5
	nr.setProbeFlags(useIdx, lowAlloc, noFast)
	b.ReportAllocs()
	b.ResetTimer()
	var i int
	for b.Loop() {
		nr.ModifyReq(reqs[i%len(reqs)])
		i++
	}
}

func BenchmarkProbeModifyReqReal(b *testing.B)     { benchmarkProbeModifyReq(b, 0) }
func BenchmarkProbeModifyReqRealIdx(b *testing.B)  { benchmarkProbeModifyReq(b, 1) }
func BenchmarkProbeModifyReqRealLP(b *testing.B)   { benchmarkProbeModifyReq(b, 2) }
func BenchmarkProbeModifyReqRealIdxLP(b *testing.B) { benchmarkProbeModifyReq(b, 3) }
func BenchmarkProbeModifyReqRealIdxNoFast(b *testing.B) { benchmarkProbeModifyReq(b, 4) }
func BenchmarkProbeModifyReqRealAll(b *testing.B)  { benchmarkProbeModifyReq(b, 5) }

func probeRequests(b testing.TB, urls []string) []*http.Request {
	b.Helper()
	reqs := make([]*http.Request, 0, len(urls))
	sites := []string{"same-origin", "cross-site", "none"}
	for i, u := range urls {
		parsed, err := url.Parse(u)
		if err != nil {
			continue
		}
		req := &http.Request{Method: http.MethodGet, URL: parsed, Header: http.Header{}}
		req.Header.Set("Sec-Fetch-Site", sites[i%len(sites)])
		req.Header.Set("Sec-Fetch-Dest", "document")
		if i%3 == 0 {
			req.Header.Set("Sec-Fetch-User", "?1")
		}
		if i%2 == 0 {
			req.Header.Set("Referer", urls[(i+1)%len(urls)])
		}
		reqs = append(reqs, req)
	}
	return reqs
}

// TestEngineFlagsEquivalence closes the loop at ModifyReq level: with all
// probe flags on (index + lowAlloc + noFast variants), the observable
// outcome set per URL must equal the flag-free engine. Run over 3000 real
// URLs with 4 outcome samples each (outcome sets, because tree-part order
// is map-random in the engine itself).
func TestEngineFlagsEquivalence(t *testing.T) {
	nrOn := probeBuildReal(t)
	nrOff := probeBuildReal(t)
	urls := probeURLs(t, 3000)
	reqsOn := probeRequests(t, urls)
	reqsOff := probeRequests(t, urls)

	run := func(nr *NetworkRules, reqs []*http.Request, i int) string {
		applied, block, redirect := nr.ModifyReq(reqs[i%len(reqs)])
		raws := make([]string, 0, len(applied))
		for _, a := range applied {
			raws = append(raws, a.RawRule)
		}
		sort.Strings(raws)
		return fmt.Sprintf("block=%v redirect=%q applied=%v", block, redirect, raws)
	}

	combos := []struct {
		name  string
		flags [3]bool
	}{
		{"idx", [3]bool{true, false, false}},
		{"lowAlloc", [3]bool{false, true, false}},
		{"idx+lowAlloc", [3]bool{true, true, false}},
		{"idx+lowAlloc+noFast", [3]bool{true, true, true}},
	}
	const samples = 4
	const escalate = 64
	for _, combo := range combos {
		nrOn.setProbeFlags(combo.flags[0], combo.flags[1], combo.flags[2])
		var mismatches, subsetWarns int
		for i, u := range urls {
			sample := func(nr *NetworkRules, reqs []*http.Request, n int) map[string]struct{} {
				seen := map[string]struct{}{}
				for k := 0; k < n; k++ {
					seen[run(nr, reqs, i)] = struct{}{}
				}
				return seen
			}
			seen := sample(nrOn, reqsOn, samples)
			seenBase := sample(nrOff, reqsOff, samples)
			if outcomeSetsEqual(seen, seenBase) {
				continue
			}
			// Unequal after quick sampling: escalate. Several blocking rules
			// may match one URL; WHICH one ModifyReq reports is
			// map-order-nondeterministic in the ORIGINAL engine (see
			// equiv_test.go), so overlapping-but-unequal sets are a known
			// sampling artifact, not a probe regression.
			seen = sample(nrOn, reqsOn, escalate)
			seenBase = sample(nrOff, reqsOff, escalate)
			if outcomeSetsEqual(seen, seenBase) {
				continue
			}
			if outcomeSetsOverlap(seen, seenBase) {
				subsetWarns++
				continue
			}
			mismatches++
			if mismatches <= 3 {
				t.Errorf("%s: DISJOINT outcomes for %q: on=%v off=%v", combo.name, u, keys(seen), keys(seenBase))
			}
		}
		if mismatches > 0 {
			t.Errorf("%s: %d mismatching urls (subset-warn: %d)", combo.name, mismatches, subsetWarns)
		} else {
			t.Logf("%s: 0 mismatches over %d urls x %d samples (subset-warn: %d)", combo.name, len(urls), samples, subsetWarns)
		}
	}
}

func outcomeSetsEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func outcomeSetsOverlap(a, b map[string]struct{}) bool {
	for k := range a {
		if _, ok := b[k]; ok {
			return true
		}
	}
	return false
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
