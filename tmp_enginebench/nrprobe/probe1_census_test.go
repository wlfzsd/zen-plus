package nrprobe

// PROBE 1 premise census (tmp_enginebench only, 2026-10-05).
// Question 1 (task-mandated premise): of the real-corpus regexp rules, how
// many have a PROVABLY required literal token, and how discriminating are
// the df-selected tokens on real URLs?
// Question 2: how much regexp time would the token index actually skip
// (cost model over 5000 real URLs, per-rule measured)?
//
// Run: go test ./tmp_enginebench/nrprobe/ -run TestCensusTokenPremise -v

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- local corpus loaders (same semantics as tmp_enginebench.bench_test) ---

var censusRealCacheDir = filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")

var censusIgnoreLineRe = regexp.MustCompile(`^(?:!|\[|#[^#%@$])`)

func censusRealLines(t testing.TB) []string {
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
			line := strings.TrimSpace(string(raw))
			if line == "" || censusIgnoreLineRe.MatchString(line) {
				continue
			}
			if strings.HasPrefix(line, "!#include") {
				continue
			}
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			lines = append(lines, line)
		}
	}
	return lines
}

func censusURLs(t testing.TB, max int) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "urls.txt"))
	if err == nil {
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
	t.Fatal("urls.txt not found")
	return nil
}

type censusRule struct {
	store   string // primary | exception
	body    string
	re      *regexp.Regexp
	fast    bool
	segs    []string
	locked  []bool
	tokens  []string // df-selected any-of set (len 0/1 = single required token)
	altCase bool     // set via the mandatory-alternation (case B) path
	costNs  float64  // avg regexp match ns over census URLs
	sel     float64  // fraction of URLs where some token is present
}

func TestCensusTokenPremise(t *testing.T) {
	lines := censusRealLines(t)
	var hostsFormat int

	urls := censusURLs(t, 5000)
	t.Logf("urls: %d", len(urls))

	// Ground truth: enumerate the regexp rules of a REAL engine build, so
	// hosts routing, modifier validation and dedupe match ParseRule exactly.
	nr := probeBuildReal(t)

	var rules []censusRule
	var hostRules, exceptionRules, otherRules int
	for _, line := range lines {
		raw := line
		if reHosts.MatchString(raw) {
			hostsFormat++
			continue
		}
		if strings.HasPrefix(raw, "@@") {
			exceptionRules++
			continue
		}
		pattern, _ := parseRuleParts(raw)
		if pattern == "" {
			continue
		}
		if len(pattern) > 1 && pattern[0] == '/' && pattern[len(pattern)-1] == '/' {
			continue // counted via the stores below
		}
		if strings.HasPrefix(pattern, "||") {
			hostRules++
		} else {
			otherRules++
		}
	}
	for _, r := range nr.primaryStore.regexp {
		rules = append(rules, censusRule{store: "primary", body: r.regexp.String(), re: r.regexp, fast: r.fast != nil})
	}
	for _, r := range nr.exceptionStore.regexp {
		rules = append(rules, censusRule{store: "exception", body: r.regexp.String(), re: r.regexp, fast: r.fast != nil})
	}
	t.Logf("real lines: %d (hosts-format: %d); tree-style: ||host^ %d + plain %d + exception %d",
		len(lines), hostsFormat, hostRules, otherRules, exceptionRules)
	t.Logf("STORE regexp rules: primary=%d exception=%d total=%d",
		len(nr.primaryStore.regexp), len(nr.exceptionStore.regexp), len(rules))

	for i := range rules {
		r := &rules[i]
		if !r.fast {
			r.segs, r.locked = requiredSegments(r.body)
		}
	}

	var fastPrimary, fastException, fbPrimary, fbException int
	var segHist [8]int // longest-seg length buckets: 0,1,2,3,4-7,8-15,16-31,32+
	for i := range rules {
		r := &rules[i]
		if r.fast {
			if r.store == "primary" {
				fastPrimary++
			} else {
				fastException++
			}
			continue
		}
		if r.store == "primary" {
			fbPrimary++
		} else {
			fbException++
		}
		longest := 0
		for _, sg := range r.segs {
			if len(sg) > longest {
				longest = len(sg)
			}
		}
		switch {
		case longest == 0:
			segHist[0]++
		case longest == 1:
			segHist[1]++
		case longest == 2:
			segHist[2]++
		case longest == 3:
			segHist[3]++
		case longest <= 7:
			segHist[4]++
		case longest <= 15:
			segHist[5]++
		case longest <= 31:
			segHist[6]++
		default:
			segHist[7]++
		}
	}
	t.Logf("regexp rules: primary=%d (fast=%d fallback=%d) exception=%d (fast=%d fallback=%d) total=%d",
		fbPrimary+fastPrimary, fastPrimary, fbPrimary, fbException+fastException, fastException, fbException, len(rules))
	t.Logf("fallback longest-required-segment histogram [0,1,2,3,4-7,8-15,16-31,32+]: %v", segHist)

	// df selection (same algorithm as buildIdx).
	distinctSet := map[string]struct{}{}
	var distinct []string
	for i := range rules {
		for _, sg := range rules[i].segs {
			if len(sg) >= idxMinTokenLen && len(sg) <= idxMaxTokenLen {
				if _, ok := distinctSet[sg]; !ok {
					distinctSet[sg] = struct{}{}
					distinct = append(distinct, sg)
				}
			}
		}
	}
	df := make(map[string]int32, len(distinct))
	for i := range rules {
		if rules[i].fast || len(rules[i].segs) == 0 {
			continue
		}
		hit := map[string]struct{}{}
		for _, sg := range rules[i].segs {
			for _, tok := range distinct {
				if _, done := hit[tok]; done {
					continue
				}
				if len(tok) <= len(sg) && strings.Contains(sg, tok) {
					df[tok]++
					hit[tok] = struct{}{}
				}
			}
		}
	}
	var indexed, always, viaAlt int
	schemePrefixed := 0
	for i := range rules {
		if rules[i].fast {
			continue
		}
		best, bestLocked := "", false
		bestDF := int32(-1)
		for j, sg := range rules[i].segs {
			if len(sg) < idxMinTokenLen || len(sg) > idxMaxTokenLen {
				continue
			}
			d := df[sg]
			lk := rules[i].locked[j]
			if bestDF < 0 ||
				(bestLocked && !lk) ||
				(lk == bestLocked && (d < bestDF || (d == bestDF && len(sg) > len(best)))) {
				best, bestLocked, bestDF = sg, lk, d
			}
		}
		// case B set (computed for the locked-caseA fallback rule too)
		var caseB []string
		if ast, err := syntax.Parse(rules[i].body, syntax.Perl); err == nil && !hasFoldCase(ast) {
			if set := altTokens(ast); len(set) > 0 {
				caseB = set
			}
		}
		if best != "" && !(bestLocked && len(caseB) > 0) {
			rules[i].tokens = []string{best}
			indexed++
			if strings.HasPrefix(best, "http") {
				schemePrefixed++
			}
			continue
		}
		if len(caseB) > 0 {
			rules[i].tokens = caseB
			rules[i].altCase = true
			indexed++
			viaAlt++
			continue
		}
		always++
	}
	t.Logf("df-selected tokens: indexed=%d (caseA single=%d, caseB alternation=%d) always-evaluate=%d (http-prefixed tokens: %d)",
		indexed, indexed-viaAlt, viaAlt, always, schemePrefixed)

	// per-rule regexp cost + token selectivity on real URLs
	var totalCost, skippedCost float64
	var totalRulesCosting float64
	type costRow struct {
		idx    int
		cost   float64
		sel    float64
		tokens []string
		alt    bool
		body   string
	}
	costs := make([]costRow, 0, len(rules))
	for i := range rules {
		r := &rules[i]
		if r.fast {
			continue
		}
		var hitURLs int
		var ns time.Duration
		start := time.Now()
		for _, u := range urls {
			if r.re.MatchString(u) {
				hitURLs++
			}
		}
		ns = time.Since(start)
		r.costNs = float64(ns) / float64(len(urls))
		if len(r.tokens) > 0 {
			hits := 0
			for _, u := range urls {
				for _, tk := range r.tokens {
					if strings.Contains(u, tk) {
						hits++
						break
					}
				}
			}
			r.sel = float64(hits) / float64(len(urls))
		} else {
			r.sel = 1 // always evaluated
		}
		totalCost += r.costNs
		skippedCost += r.costNs * (1 - r.sel)
		totalRulesCosting++
		costs = append(costs, costRow{idx: i, cost: r.costNs, sel: r.sel, tokens: r.tokens, alt: r.altCase, body: r.body})
	}
	sort.Slice(costs, func(a, b int) bool { return costs[a].cost > costs[b].cost })
	t.Logf("per-request regexp cost model over %d urls (amortized timer): total=%.1fus, skippable=%.1fus (%.1f%% of regexp cost)",
		len(urls), totalCost/1000, skippedCost/1000, 100*skippedCost/totalCost)

	var cum float64
	for i, c := range costs {
		if i >= 20 {
			break
		}
		cum += c.cost
		kind := "A"
		if c.alt {
			kind = "B"
		}
		t.Logf("cost#%02d avg=%7.0fns sel=%5.1f%% case=%s tokens=%q pat=%.60s",
			i, c.cost, 100*c.sel, kind, c.tokens, c.body)
	}
	t.Logf("top20 share=%.1f%%", 100*cum/totalCost)

	// Full dump of the most expensive always-evaluate patterns: these are
	// where neither the token index nor fastshape helps; diagnose why.
	dumped := 0
	for _, c := range costs {
		if len(c.tokens) != 0 || dumped >= 6 {
			continue
		}
		t.Logf("ALWAYSEVAL cost=%.0fns pat(%d bytes)=%s", c.cost, len(c.body), c.body)
		dumped++
	}
	// Debug: why does the sextb replace-rule yield no token?
	for _, c := range costs {
		if strings.Contains(c.body, "sextb.js") {
			segs, lks := requiredSegments(c.body)
			t.Logf("DEBUG sextb body=%q\n  segs=%v locked=%v", c.body, segs, lks)
		}
	}

	// candidate-set sizes after marking (upper bound of regexp runs per url)
	// simulated via the same selection on 500 urls
	sample := urls
	if len(sample) > 500 {
		sample = sample[:500]
	}
	var sumCand, maxCand, nCand float64
	for _, u := range sample {
		var c int
		for i := range rules {
			if rules[i].fast {
				continue // fast rules run matchFast, not regexp
			}
			if len(rules[i].tokens) == 0 {
				c++ // always evaluated
				continue
			}
			for _, tk := range rules[i].tokens {
				if strings.Contains(u, tk) {
					c++
					break
				}
			}
		}
		sumCand += float64(c)
		if float64(c) > maxCand {
			maxCand = float64(c)
		}
		nCand++
	}
	fmt.Printf("census: avg candidates/url=%.1f max=%d (of %d fallback)\n",
		sumCand/nCand, int(maxCand), fbPrimary+fbException)
}
