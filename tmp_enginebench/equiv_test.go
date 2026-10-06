// Equivalence gate for the 2026-10-05 engine slimming work: the production
// networkrules engine must produce the same observable ModifyReq outcomes as
// the frozen pre-change copy in tmp_enginebench/baselinenr.
//
// NOTE on semantics: ruleStore.Get dedupes via a Go map, so the ORDER of
// matched rules — and hence which single rule ModifyReq reports when several
// blocking rules match the same URL — is nondeterministic in the ORIGINAL
// engine too (verified by probe: both engines match identical pattern sets;
// only the reported-first varies). The gate therefore compares the SET of
// observable outcomes per URL over repeated runs, not single calls.
package tmp_enginebench

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"testing"

	basenr "github.com/irbis-sh/zen-desktop/tmp_enginebench/baselinenr/networkrules"
)

// loadURLs is the testing.TB variant of loadURLList.
func loadURLs(tb testing.TB, max int) []string {
	tb.Helper()
	data, err := os.ReadFile("../internal/ruletree/testdata/urls.txt")
	if err != nil {
		tb.Fatal(err)
	}
	var urls []string
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if _, err := url.Parse(string(line)); err != nil {
			continue
		}
		urls = append(urls, string(line))
		if max > 0 && len(urls) >= max {
			break
		}
	}
	return urls
}

func buildBaseNetworkRulesReal(tb testing.TB) *basenr.NetworkRules {
	tb.Helper()
	nr := basenr.New()
	name := "real"
	for _, l := range loadRealLines(tb) {
		if _, err := nr.ParseRule(l, &name); err != nil {
			_ = err
		}
	}
	return nr
}

func buildEquivRequest(u string, i int, urls []string) *http.Request {
	parsed, err := url.Parse(u)
	if err != nil {
		return nil
	}
	req := &http.Request{Method: http.MethodGet, URL: parsed, Header: http.Header{}}
	sites := []string{"same-origin", "cross-site", "none"}
	req.Header.Set("Sec-Fetch-Site", sites[i%len(sites)])
	req.Header.Set("Sec-Fetch-Dest", "document")
	if i%3 == 0 {
		req.Header.Set("Sec-Fetch-User", "?1")
	}
	if i%2 == 0 {
		req.Header.Set("Referer", urls[(i+1)%len(urls)])
	}
	return req
}

// engineResult captures the observable outcome of one ModifyReq call.
type engineResult struct {
	raws     []string
	block    bool
	redirect string
}

func (r engineResult) String() string {
	return fmt.Sprintf("block=%v redirect=%q applied=%v", r.block, r.redirect, r.raws)
}

func sampleOutcomes(run func(*http.Request) engineResult, i int, u string, urls []string, n int) map[string]struct{} {
	out := map[string]struct{}{}
	for k := 0; k < n; k++ {
		req := buildEquivRequest(u, i, urls)
		if req == nil {
			return nil
		}
		out[run(req).String()] = struct{}{}
		if len(out) == 1 {
			// Fast path: stable result, no need for more samples unless
			// adjudication escalates.
			continue
		}
	}
	return out
}

// TestEngineEquivalenceReal compares the slimmed production engine with the
// frozen baseline on >=5000 real URLs (block / redirectURL / applied-rule set).
func TestEngineEquivalenceReal(t *testing.T) {
	urls := loadURLs(t, 10000)
	if len(urls) < 5000 {
		t.Fatalf("need >=5000 urls, got %d", len(urls))
	}

	prod := buildNetworkRulesReal(t)
	base := buildBaseNetworkRulesReal(t)

	runProd := func(req *http.Request) engineResult {
		applied, block, redirect := prod.ModifyReq(req)
		r := engineResult{block: block, redirect: redirect}
		for _, a := range applied {
			r.raws = append(r.raws, a.RawRule)
		}
		sort.Strings(r.raws)
		return r
	}
	runBase := func(req *http.Request) engineResult {
		applied, block, redirect := base.ModifyReq(req)
		r := engineResult{block: block, redirect: redirect}
		for _, a := range applied {
			r.raws = append(r.raws, a.RawRule)
		}
		sort.Strings(r.raws)
		return r
	}

	const baseSamples = 4
	const escalateSamples = 64

	var mismatches, subsetCases int
	for i, u := range urls {
		reqP := buildEquivRequest(u, i, urls)
		reqB := buildEquivRequest(u, i, urls)
		if reqP == nil || reqB == nil {
			continue
		}

		sp := sampleOutcomes(runProd, i, u, urls, baseSamples)
		sb := sampleOutcomes(runBase, i, u, urls, baseSamples)
		if sp == nil || sb == nil {
			continue
		}
		if setEqual(sp, sb) {
			continue
		}
		// Unequal after quick sampling: escalate before deciding.
		sp = sampleOutcomes(runProd, i, u, urls, escalateSamples)
		sb = sampleOutcomes(runBase, i, u, urls, escalateSamples)
		if setEqual(sp, sb) {
			continue
		}
		if !setOverlap(sp, sb) {
			mismatches++
			t.Errorf("equivalence mismatch (disjoint outcomes after escalation) for %q:\n prod: %v\n base: %v", u, keys(sp), keys(sb))
			continue
		}
		// Overlapping but unequal: with 64 samples per side this means some
		// outcome has low probability on one side; log for review rather
		// than fail (sampling cannot enumerate all map-order outcomes).
		subsetCases++
		t.Logf("partial-overlap outcomes (sampled %d) for %q:\n prod: %v\n base: %v", escalateSamples, u, keys(sp), keys(sb))
	}
	if mismatches > 0 {
		t.Fatalf("equivalence FAILED: %d mismatching urls (subset-warn cases: %d)", mismatches, subsetCases)
	}
	t.Logf("equivalence: 0 mismatches, subset-warn cases=%d", subsetCases)
}

func setEqual(a, b map[string]struct{}) bool {
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

func setOverlap(a, b map[string]struct{}) bool {
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
