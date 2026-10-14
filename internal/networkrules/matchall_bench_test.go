// Benchmark: marginal cost of "match-all + action-modifier" generic rules.
//
// Corpus and engines mirror the 2026-10-08 investigation of the network
// slowdown caused by the AdGuard Spyware list's match-all $permissions rules
// (pattern: slash, dot, star, slash — with $permissions=...,document
// modifiers): the rule files are the live Zen filter caches, and the
// URL/response corpus comes from the live decisions.jsonl tap.
//
// Engines:
//   - "with"    — every rule line of the enabled filter lists (production).
//   - "without" — the same lines minus every match-all action rule (empty
//     pattern, "*", or the match-everything slash-dot-star regexp, carrying
//     at least one of: $permissions/$csp/$removeheader/$replace/$jsonprune/
//     $remove-js-constant/$scramblejs/$removeparam/$cookie).
//
// Run (adjust -benchtime as needed):
//
//	go test -run '^$' -bench MatchAllBench -benchtime 1s ./internal/networkrules
//
// Data sources (both optional — the benchmark skips when absent):
//   - filter caches: %LOCALAPPDATA%\Zen\filters\<md5(url)>.cache.txt
//   - decision tap:  %LOCALAPPDATA%\Zen\Logs\decisions.jsonl (ZEN_DECISION_LOG=1)
//
// Response corpus realism notes: decisions.jsonl records neither Set-Cookie
// headers nor bodies, so every synthetic response carries one representative
// Set-Cookie header (keeps the $cookie rule scans honest — 427 match-all
// ones in the "with" engine) and a ~4 KiB body (host-scoped $replace rules
// read it). Body-size-bound $replace regex cost is therefore a lower bound.
// Sec-Fetch-Dest is supplied per sub-benchmark.
package networkrules_test

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules"
)

// enabledFilterURLs mirrors Zen Config\config.json filterLists enabled=true
// entries (2026-10-08). The cache file name is the MD5 of the URL.
var enabledFilterURLs = []string{
	"https://cdn.jsdelivr.net/gh/irbis-sh/filter-lists@master/ads/ads.txt",
	"https://easylist.to/easylist/easylist.txt",
	"https://raw.githubusercontent.com/AdguardTeam/FiltersRegistry/master/filters/filter_2_Base/filter.txt",
	"https://cdn.jsdelivr.net/gh/irbis-sh/filter-lists@master/privacy/privacy.txt",
	"https://easylist.to/easylist/easyprivacy.txt",
	"https://raw.githubusercontent.com/AdguardTeam/FiltersRegistry/master/filters/filter_3_Spyware/filter.txt",
	"https://raw.githubusercontent.com/DandelionSprout/adfilt/master/LegitimateURLShortener.txt",
	"https://malware-filter.gitlab.io/malware-filter/urlhaus-filter-online.txt",
	"https://malware-filter.gitlab.io/malware-filter/phishing-filter-hosts.txt",
	"https://someonewhocares.org/hosts/zero/hosts",
	"https://pgl.yoyo.org/adservers/serverlist.php?hostformat=hosts&showintro=1&mimetype=plaintext",
	"https://filters.adtidy.org/extension/ublock/filters/224.txt",
	"https://anti-ad.net/easylist.txt",
}

var (
	enginesOnce   sync.Once
	engineWith    *networkrules.NetworkRules
	engineWithout *networkrules.NetworkRules
	enginesErr    error
	reqCorpus     []*http.Request
	resCorpus     []*http.Response
	resReqs       []*http.Request
)

// benchBody is the synthetic response body (decisions.jsonl does not record
// bodies; production responses always carry one and host-scoped $replace
// rules legitimately read it). ~4 KiB of plausible HTML.
var benchBody = []byte(`<!doctype html><html><head><title>bench</title></head><body><div id=a>content</div><script>var x=1;</script></body></html>` +
	strings.Repeat(`<!-- pad --><p>Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore.</p>`, 26))

type benchDecision struct {
	Kind     string `json:"kind"`
	Decision string `json:"decision"`
	Method   string `json:"method"`
	URL      string `json:"url"`
	Referer  string `json:"referer"`
	Status   int    `json:"status"`
	Ctype    string `json:"ctype"`
}

// actionModifierNames is the rule.Rule ParseModifiers set of request/response
// rewriting actions (internal/networkrules/rule/rule.go).
var actionModifierNames = []string{
	"permissions", "csp", "removeheader", "replace", "jsonprune",
	"remove-js-constant", "scramblejs", "removeparam", "cookie",
}

var ignoreLineRe = regexp.MustCompile(`^(?:!|\[|#[^#%@$])`)

// splitModifiers mirrors networkrules.splitModifiers (unescaped commas).
func benchSplitModifiers(s string) []string {
	var res []string
	var b strings.Builder
	escaped := false
	for _, r := range s {
		switch {
		case r == '\\':
			if escaped {
				b.WriteRune('\\')
			}
			escaped = !escaped
		case r == ',':
			if escaped {
				b.WriteRune(',')
				escaped = false
			} else {
				res = append(res, b.String())
				b.Reset()
			}
		default:
			if escaped {
				b.WriteRune('\\')
				escaped = false
			}
			b.WriteRune(r)
		}
	}
	res = append(res, b.String())
	return res
}

// benchRuleParts mirrors networkrules.parseRuleParts: slash-delimited regexp
// rule first, then the first unescaped "$".
func benchRuleParts(raw string) (pattern, mods string) {
	if len(raw) >= 2 && raw[0] == '/' {
		escaped := false
		for i := 1; i < len(raw); i++ {
			c := raw[i]
			if c == '\\' {
				escaped = !escaped
				continue
			}
			if c == '/' && !escaped {
				if i == len(raw)-1 {
					return raw[:i+1], ""
				}
				if raw[i+1] == '$' {
					return raw[:i+1], raw[i+2:]
				}
			}
			escaped = false
		}
	}
	idx := strings.IndexByte(raw, '$')
	if idx >= 0 {
		return raw[:idx], raw[idx+1:]
	}
	return raw, ""
}

// isMatchAllActionRule reports whether the rule line carries a match-all
// pattern (empty, "*", or "/.*/") plus at least one action modifier — the
// inventory from the 2026-10-08 scan of the enabled lists (~1938 rules).
func isMatchAllActionRule(line string) bool {
	pattern, modsText := benchRuleParts(line)
	matchAll := pattern == "" || pattern == "*" || pattern == "/.*/"
	if !matchAll {
		return false
	}
	for _, m := range benchSplitModifiers(modsText) {
		for _, name := range actionModifierNames {
			if m == name || strings.HasPrefix(m, name+"=") {
				return true
			}
		}
	}
	return false
}

func loadEngines(tb testing.TB) {
	tb.Helper()
	enginesOnce.Do(func() {
		filtersDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
		type listFile struct{ name, path string }
		var files []listFile
		for _, u := range enabledFilterURLs {
			h := fmt.Sprintf("%x", md5.Sum([]byte(u)))
			files = append(files, listFile{
				name: u,
				path: filepath.Join(filtersDir, h+".cache.txt"),
			})
		}

		var data [][]byte
		for _, f := range files {
			b, err := os.ReadFile(f.path)
			if err != nil {
				enginesErr = fmt.Errorf("read %s: %w", f.path, err)
				return
			}
			data = append(data, b)
		}

		engineWith = networkrules.New()
		engineWithout = networkrules.New()
		name := "bench-list"
		for _, raw := range data {
			sc := bufio.NewScanner(bytes.NewReader(raw))
			sc.Buffer(nil, 1<<20)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || ignoreLineRe.MatchString(line) {
					continue
				}
				// Production semantics (filter.AddReader): parse failures
				// are silently skipped; the injector handles cosmetic lines
				// before network rules ever see them.
				_, _ = engineWith.ParseRule(line, &name)
				if !isMatchAllActionRule(line) {
					_, _ = engineWithout.ParseRule(line, &name)
				}
			}
		}
		engineWith.Compact()
		engineWithout.Compact()

		logPath := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "Logs", "decisions.jsonl")
		f, err := os.Open(logPath)
		if err != nil {
			enginesErr = fmt.Errorf("open decisions log: %w", err)
			return
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			var d benchDecision
			if err := json.Unmarshal(sc.Bytes(), &d); err != nil {
				continue
			}
			u, err := url.Parse(d.URL)
			if err != nil || u.Host == "" {
				continue
			}
			method := d.Method
			if method == "" {
				method = http.MethodGet
			}
			h := http.Header{}
			if d.Referer != "" {
				h.Set("Referer", d.Referer)
			}
			req := &http.Request{Method: method, URL: u, Header: h}
			switch d.Kind {
			case "req":
				reqCorpus = append(reqCorpus, req)
			case "res":
				rh := http.Header{}
				rh.Set("Content-Type", d.Ctype)
				rh.Set("Set-Cookie", "_ga=GA1.1.123456789.1760000000; Path=/; Expires=Wed, 21 Oct 2026 07:28:00 GMT; Secure; HttpOnly")
				res := &http.Response{
					StatusCode:    d.Status,
					Header:        rh,
					Body:          io.NopCloser(bytes.NewReader(benchBody)),
					ContentLength: int64(len(benchBody)),
				}
				resCorpus = append(resCorpus, res)
				resReqs = append(resReqs, req)
			}
		}
		if len(reqCorpus) == 0 || len(resCorpus) == 0 {
			enginesErr = fmt.Errorf("empty corpus from %s", logPath)
		}
	})
	if enginesErr != nil {
		tb.Skipf("benchmark data unavailable: %v", enginesErr)
	}
}

func BenchmarkMatchAllBenchModifyReq_WithGenericActions(b *testing.B) {
	loadEngines(b)
	var i int
	for b.Loop() {
		engineWith.ModifyReq(reqCorpus[i%len(reqCorpus)])
		i++
	}
	b.ReportAllocs()
}

func BenchmarkMatchAllBenchModifyReq_WithoutGenericActions(b *testing.B) {
	loadEngines(b)
	var i int
	for b.Loop() {
		engineWithout.ModifyReq(reqCorpus[i%len(reqCorpus)])
		i++
	}
	b.ReportAllocs()
}

func resWithDest(req *http.Request, dest string) *http.Request {
	r := req.Clone(req.Context())
	if dest != "" {
		r.Header = r.Header.Clone()
		r.Header.Set("Sec-Fetch-Dest", dest)
	}
	return r
}

func BenchmarkMatchAllBenchModifyRes_WithGenericActions(b *testing.B) {
	loadEngines(b)
	// Realistic desktop browsing: every https request carries fetch metadata.
	reqs := make([]*http.Request, len(resReqs))
	for i, r := range resReqs {
		reqs[i] = resWithDest(r, "image")
	}
	var i int
	for b.Loop() {
		engineWith.ModifyRes(reqs[i%len(reqs)], resCorpus[i%len(resCorpus)])
		i++
	}
	b.ReportAllocs()
}

func BenchmarkMatchAllBenchModifyRes_WithoutGenericActions(b *testing.B) {
	loadEngines(b)
	reqs := make([]*http.Request, len(resReqs))
	for i, r := range resReqs {
		reqs[i] = resWithDest(r, "image")
	}
	var i int
	for b.Loop() {
		engineWithout.ModifyRes(reqs[i%len(reqs)], resCorpus[i%len(resCorpus)])
		i++
	}
	b.ReportAllocs()
}

func BenchmarkMatchAllBenchModifyRes_WithGenericActions_Document(b *testing.B) {
	loadEngines(b)
	// Frame loads keep the $permissions behavior after the fix (the headers
	// are legitimately applied).
	reqs := make([]*http.Request, len(resReqs))
	for i, r := range resReqs {
		reqs[i] = resWithDest(r, "document")
	}
	var i int
	for b.Loop() {
		engineWith.ModifyRes(reqs[i%len(reqs)], resCorpus[i%len(resCorpus)])
		i++
	}
	b.ReportAllocs()
}
