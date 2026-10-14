package networkrules

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"strings"
	"sync"
	"testing"
)

// --- unit-level equivalence table -------------------------------------------

// TestParseFastShapeAccepted checks patterns inside the supported family:
// the fast matcher must exist and agree with the regexp on every URL,
// including the cases the matcher is most likely to get wrong (ordering,
// anchoring, needPath windows, end anchors).
func TestParseFastShapeAccepted(t *testing.T) {
	tests := []struct {
		pattern string
		urls    []string
	}{
		{
			// canonical corpus shape: single fork after needPath
			pattern: `^https?:\/\/.*\/.*(sw[0-9a-z._-]{1,6}|\.notify\.).*`,
			urls: []string{
				"https://example.com/sw.js",
				"https://example.com/a/sw9z-x",
				"https://example.com/a/b.notify.c",
				"https://x.sw/a", // "sw" before the first slash must NOT match
				"http://h/p/sw",
				"https://example.com/sw", // class run missing -> no match
				"https://example.com/swj",
			},
		},
		{
			pattern: `^https?:\/\/.*\/.*sw[0-9._].*`,
			urls: []string{
				"https://a/b/sw1",
				"https://a/sw.x/c",
				"https://a/swc", // class char missing
				"https://sw1/a", // before first slash
			},
		},
		{
			pattern: `^https?:\/\/.*bit(ly)?\.(com|ly)\/`,
			urls: []string{
				"https://x.bit.com/",
				"https://x.bit.ly/a",
				"https://x.bitly.com/",
				"https://x.bitly.ly/",
				"https://bitly.com/", // no "bit" after scheme? "bit" occurs -> match
				"https://x.bitcom/",
			},
		},
		{
			// anchored first unit (with optional group) — must match at the
			// scope start, not anywhere.
			pattern: `^https?:\/\/(www\.)?example\.com\/`,
			urls: []string{
				"https://example.com/x",
				"https://www.example.com/x",
				"http://example.com/",
				"https://xexample.com/",     // must NOT match
				"https://xwww.example.com/", // must NOT match
			},
		},
		{
			// merged prefix literal: ^https?:\/\/foo\/bar
			pattern: `^https?:\/\/foo\.bar\.com\/baz`,
			urls: []string{
				"https://foo.bar.com/baz",
				"http://foo.bar.com/bazqux",
				"https://xfoo.bar.com/baz", // must NOT match
			},
		},
		{
			// two required units in order (AND, not OR)
			pattern: `^https?:\/\/.*foo.*bar.*`,
			urls: []string{
				"https://foo-x-bar",
				"https://x/foobar/1",
				"https://bar-x-foo", // wrong order: must NOT match
				"https://foox",      // bar missing
				"https://barx",      // foo missing
			},
		},
		{
			// merged needPath literal (".*\/foo" == contains "/foo")
			pattern: `^https?:\/\/.*\/foo`,
			urls: []string{
				"https://x/foo",
				"https://foo",  // no slash before
				"https://xfoo", // no slash before "foo"
			},
		},
		{
			// needPath: content after the first slash
			pattern: `^https?:\/\/.*\/.*foo`,
			urls: []string{
				"https://a/b/foo",
				"https://a/bfoo",
				"https://foo/a", // foo before first slash: must NOT match
			},
		},
		{
			// "$" directly after content
			pattern: `^https?:\/\/.*foo$`,
			urls: []string{
				"https://x/foo",
				"https://xfoo",
				"https://foo/x", // must NOT match
			},
		},
		{
			// "$" after a free gap: no constraint beyond the units
			pattern: `^https?:\/\/.*foo.*$`,
			urls: []string{
				"https://foo/x",
				"https://x/foo/y",
			},
		},
		{
			// end-anchored fork
			pattern: `^https?:\/\/.*(jpg|png)$`,
			urls: []string{
				"https://x/a.jpg",
				"https://x/a.png",
				"https://a.jpg/x", // must NOT match
				"https://x/a.jpgq",
			},
		},
		{
			// bare class unit with repetition, top level (anchored: no gap
			// between the prefix and the class)
			pattern: `^https?:\/\/\?{4,}.*`,
			urls: []string{
				"https://????x",
				"https://x????", // run not at scope start: must NOT match
				"https://??x",
			},
		},
		{
			// class run between gaps: "?" then "=" afterwards
			pattern: `^https?:\/\/.*\?.*=.*`,
			urls: []string{
				"https://x?a=b",
				"https://x=1?a", // "=" before "?": must NOT match
			},
		},
		{
			// literal + unbounded class run
			pattern: `^https?:\/\/.*ad[0-9]{2,}.*`,
			urls: []string{
				"https://x/ad123",
				"https://x/ad1", // run too short
				"https://xaadd", // "ad" not followed by 2 digits
			},
		},
		{
			// non-capturing group as a bare alternation item; also a fork
			// with different literal lengths, anchored
			pattern: `^https?:\/\/(?:a|bc)\/.*`,
			urls: []string{
				"https://a/x",
				"https://bc/x",
				"https://x/a/",
			},
		},
		{
			// double slash as a merged literal unit
			pattern: `^https?:\/\/.*\/\/.*ads.*`,
			urls: []string{
				"https://x//ads",
				"https://x/a/ds",
			},
		},
		{
			// prefix-only pattern
			pattern: `^https?:\/\/.*`,
			urls: []string{
				"https://anything",
				"http://x",
			},
		},
		{
			// grouped alternation anchored at both ends
			pattern: `^https?:\/\/(foo|bar)$`,
			urls: []string{
				"https://foo",
				"https://bar",
				"https://foox",
				"https://xfoo",
			},
		},
		{
			// optional group that can match empty, then "$"
			pattern: `^https?:\/\/x(track)?$`,
			urls: []string{
				"https://x",
				"https://xtrack",
				"https://xy",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			re := regexp.MustCompile(tt.pattern)
			sh := parseFastShape(tt.pattern, re)
			if sh == nil {
				t.Fatalf("pattern unexpectedly rejected: %s", tt.pattern)
			}
			for _, u := range tt.urls {
				want := re.MatchString(u)
				got := sh.matchFast(u)
				if got != want {
					t.Errorf("mismatch for %q: fastShape=%v regexp=%v", u, got, want)
				}
			}
		})
	}
}

// TestParseFastShapeRejected checks patterns outside the supported family:
// they must fall back (parseFastShape == nil), never approximate.
func TestParseFastShapeRejected(t *testing.T) {
	patterns := []string{
		`(?i)^https?:\/\/.*foo`,               // case folding
		`^https?:\/\/.+foo`,                   // ".+" is not a free gap
		`^https?:\/\/.*foo[0-9]{2}bar.*`,      // class between literals
		`^https?:\/\/.*li[0-9]?t.*`,           // optional class between literals
		`^https?:\/\/.*foo.bar.*`,             // bare "." between literals
		`^https?:\/\/.*\/$`,                   // "$" directly after needPath
		`^https?:\/\/.*\/.*\/.*foo`,           // second ".*/" (two slashes)
		`^https?:\/\/.*\/(foo|bar)`,           // group right after ".*\/"
		`^https?:\/\/.*foo.*\/.*bar`,          // flushed unit before needPath
		`^https?:\/\/.*[^a-z]foo.*`,           // negated class (non-ASCII range)
		`^https?:\/\/.*[\x80-\xff]foo.*`,      // high-byte class
		`^https?:\/\/.*ünïcode.*`,             // non-ASCII literal
		`^https?:\/\/.*\bfoo.*`,               // word boundary
		`^https?:\/\/.*(?:foo|bar)+.*`,        // plus of a group (not a single literal)
		`^https?:\/\/.*foo|bar.*`,             // top-level alternation (root)
		`^ftp:\/\/.*foo`,                      // unsupported scheme family
		`^https?:\/\/.*(a[0-9]b|c).*`,         // class run between literals in a branch
		`^https?:\/\/.*a$.*b`,                 // "$" not at the end
		`^https?:\/\/.*[a-z]+[0-9].*`,         // class run then class
		`^https?:\/\/api\.x\.com\/v[0-9]\/.*`, // class run then literal ("/")
		`^https?:\/\/.*(\d{2,4}|track)\.js.*`, // bare class-repeat alternate + literal after
	}
	for _, pattern := range patterns {
		t.Run(pattern, func(t *testing.T) {
			re, err := regexp.Compile(pattern)
			if err != nil {
				// The engine never inserts such rules (Insert fails), but
				// parseFastShape must still reject the pattern itself.
				re = regexp.MustCompile("")
			}
			if sh := parseFastShape(pattern, re); sh != nil {
				t.Fatalf("pattern unexpectedly accepted: %s", pattern)
			}
		})
	}
}

// TestFastShapeNewlineFallback pins the '\n' semantics: unflagged "." cannot
// match '\n', and the fast matcher must defer to the regexp on such URLs in
// both directions (including URLs that DO match despite containing '\n').
func TestFastShapeNewlineFallback(t *testing.T) {
	patterns := []string{
		`^https?:\/\/.*foo`,
		`^https?:\/\/.*\/.*foo.*`,
		`^https?:\/\/.*b`,
	}
	urls := []string{
		"https://a\nfoo", // no match for either
		"https://ab\nb",  // matches: "." stops at '\n', "b" follows
		"https://a\nbfoo",
		"https://x/foo\ny",
		"https://plain",
	}
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		sh := parseFastShape(pattern, re)
		if sh == nil {
			t.Fatalf("pattern unexpectedly rejected: %s", pattern)
		}
		if !sh.nlSensitive {
			t.Errorf("pattern %s should be nlSensitive", pattern)
		}
		for _, u := range urls {
			if got, want := sh.matchFast(u), re.MatchString(u); got != want {
				t.Errorf("mismatch for %q / %s: fastShape=%v regexp=%v", u, pattern, got, want)
			}
		}
	}
}

// TestFastShapeSchemeMask checks single-scheme prefixes: "^https://" must
// not match http URLs and vice versa.
func TestFastShapeSchemeMask(t *testing.T) {
	tests := []struct {
		pattern string
		url     string
		want    bool
	}{
		{`^https?:\/\/.*foo`, "https://foo", true},
		{`^https?:\/\/.*foo`, "http://foo", true},
		{`^https:\/\/.*foo`, "https://foo", true},
		{`^https:\/\/.*foo`, "http://foo", false},
		{`^http:\/\/.*foo`, "http://foo", true},
		{`^http:\/\/.*foo`, "https://foo", false},
	}
	for _, tt := range tests {
		re := regexp.MustCompile(tt.pattern)
		sh := parseFastShape(tt.pattern, re)
		if sh == nil {
			t.Fatalf("pattern unexpectedly rejected: %s", tt.pattern)
		}
		if got := sh.matchFast(tt.url); got != tt.want {
			t.Errorf("%s / %s: got %v want %v", tt.pattern, tt.url, got, tt.want)
		}
	}
}

// TestFastShapeNoAlloc pins the zero-allocation fast path.
func TestFastShapeNoAlloc(t *testing.T) {
	pattern := `^https?:\/\/.*\/.*(sw[0-9a-z._-]{1,6}|\.notify\.).*`
	re := regexp.MustCompile(pattern)
	sh := parseFastShape(pattern, re)
	if sh == nil {
		t.Fatal("canonical pattern rejected")
	}
	url := "https://example.com/some/path/sw123.js"
	if n := testing.AllocsPerRun(200, func() { sh.matchFast(url) }); n != 0 {
		t.Errorf("matchFast allocs = %v, want 0", n)
	}
}

// TestFastShapeConcurrent exercises concurrent matchFast on one immutable
// shape (run with -race).
func TestFastShapeConcurrent(t *testing.T) {
	pattern := `^https?:\/\/.*\/.*(sw[0-9a-z._-]{1,6}|\.notify\.).*`
	re := regexp.MustCompile(pattern)
	sh := parseFastShape(pattern, re)
	if sh == nil {
		t.Fatal("canonical pattern rejected")
	}
	urls := []string{
		"https://example.com/sw.js",
		"https://example.com/a.notify.b",
		"http://x/y",
		"https://plain",
		"https://a\nfoo",
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				u := urls[(i+offset)%len(urls)]
				if got, want := sh.matchFast(u), re.MatchString(u); got != want {
					t.Errorf("mismatch for %q: fastShape=%v regexp=%v", u, got, want)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// --- real corpus equivalence -------------------------------------------------

var fastIgnoreLineRe = regexp.MustCompile(`^(?:!|\[|#[^#%@$])`)

type fastCorpusRule struct {
	raw  string // full pattern as handed to ruleStore.Insert ("/body/")
	body string
	re   *regexp.Regexp
}

// fastCorpusRules loads every regexp rule from the real subscription caches,
// replicating filter.AddURL line selection and ruleStore.Insert's compile
// step. Skips the suite when the cache directory is absent (CI).
func fastCorpusRules(tb testing.TB) []fastCorpusRule {
	tb.Helper()
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
	matches, err := filepath.Glob(filepath.Join(dir, "*.cache.txt"))
	if err != nil || len(matches) == 0 {
		tb.Skipf("real filter caches not found in %s", dir)
	}
	seen := map[string]struct{}{}
	var rules []fastCorpusRule
	for _, f := range matches {
		data, err := os.ReadFile(f)
		if err != nil {
			tb.Fatal(err)
		}
		for _, raw := range strings.Split(string(data), "\n") {
			line := strings.TrimSpace(raw)
			if line == "" || fastIgnoreLineRe.MatchString(line) || strings.HasPrefix(line, "!#include") {
				continue
			}
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}

			pattern, _, ok := parseRegexpRuleParts(strings.TrimPrefix(line, "@@"))
			if !ok {
				continue
			}
			body := pattern[1 : len(pattern)-1]
			if body == "" {
				continue // ruleStore.Insert rejects these
			}
			re, err := regexp.Compile(body)
			if err != nil {
				continue // ruleStore.Insert rejects these too
			}
			rules = append(rules, fastCorpusRule{raw: pattern, body: body, re: re})
		}
	}
	return rules
}

const fastRandChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~:/?#[]@!$&'()*+,;%="

func fastRandomURL(rnd *rand.Rand) string {
	n := 1 + rnd.Intn(90)
	b := make([]byte, n)
	for i := range b {
		b[i] = fastRandChars[rnd.Intn(len(fastRandChars))]
	}
	u := string(b)
	switch rnd.Intn(4) {
	case 0:
		u = "http://" + u
	case 1:
		u = "https://" + u
	case 2:
		u = "https://www." + u
	}
	return u
}

// fastShapeLiterals extracts literal substrings from the pattern AST to build
// directed URLs that contain them.
func fastShapeLiterals(tb testing.TB, pattern string) []string {
	tb.Helper()
	ast, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil
	}
	var lits []string
	var walk func(r *syntax.Regexp)
	walk = func(r *syntax.Regexp) {
		if r.Op == syntax.OpLiteral && len(r.Rune) > 0 {
			lits = append(lits, string(r.Rune))
		}
		for _, sub := range r.Sub {
			walk(sub)
		}
	}
	walk(ast)
	return lits
}

func fastDirectedURL(rnd *rand.Rand, lit string) string {
	fill := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = fastRandChars[rnd.Intn(len(fastRandChars))]
		}
		return string(b)
	}
	scheme := "https://"
	if rnd.Intn(3) == 0 {
		scheme = "http://"
	}
	switch rnd.Intn(6) {
	case 0: // right after the scheme (anchored shapes)
		return scheme + lit + fill(rnd.Intn(20))
	case 1: // right after the first slash (needPath shapes)
		return scheme + fill(4) + "/" + lit + fill(rnd.Intn(20))
	case 2: // at the very end ("$" shapes)
		return scheme + fill(4) + "/" + fill(4) + lit
	case 3: // repeated literal
		return scheme + fill(4) + "/" + lit + fill(3) + lit
	case 4: // embedded mid-path
		return scheme + fill(6) + "/" + fill(3) + lit + fill(5) + "/" + fill(3)
	default:
		return scheme + fill(8) + lit + fill(8)
	}
}

// TestFastShapeCorpusEquivalence runs every regexp rule of the real
// subscription caches against random and directed URLs: hit shapes must
// agree with the regexp everywhere, missed shapes must have fallen back.
func TestFastShapeCorpusEquivalence(t *testing.T) {
	rules := fastCorpusRules(t)
	rnd := rand.New(rand.NewSource(42))
	total, hit, fallback := 0, 0, 0
	for i, rule := range rules {
		total++
		sh := parseFastShape(rule.body, rule.re)
		if sh == nil {
			fallback++ // ruleStore.Get uses the regexp for this rule
			continue
		}
		hit++
		urls := make([]string, 0, 250)
		for k := 0; k < 200; k++ {
			urls = append(urls, fastRandomURL(rnd))
		}
		lits := fastShapeLiterals(t, rule.body)
		for k := 0; k < 50; k++ {
			if len(lits) == 0 {
				urls = append(urls, fastRandomURL(rnd))
				continue
			}
			urls = append(urls, fastDirectedURL(rnd, lits[k%len(lits)]))
		}
		for _, u := range urls {
			if got, want := sh.matchFast(u), rule.re.MatchString(u); got != want {
				t.Fatalf("rule #%d %q mismatch for %q: fastShape=%v regexp=%v", i, rule.raw, u, got, want)
			}
		}
	}
	fmt.Printf("fastshape corpus: total=%d hitShapes=%d fallback=%d (%.1f%% specialized)\n",
		total, hit, fallback, 100*float64(hit)/float64(max(total, 1)))
}

// TestFastShapeFuzz is the ported store-level fuzz: for 200k random URLs the
// fast+fallback store must produce the same match count as the pure regexp
// store over the real corpus rules.
func TestFastShapeFuzz(t *testing.T) {
	rules := fastCorpusRules(t)
	type entry struct {
		re   *regexp.Regexp
		fast *fastShape
	}
	ents := make([]entry, len(rules))
	nFast := 0
	for i, r := range rules {
		ents[i] = entry{re: r.re, fast: parseFastShape(r.body, r.re)}
		if ents[i].fast != nil {
			nFast++
		}
	}
	fmt.Printf("fastshape fuzz: %d regexp rules, %d specialized\n", len(ents), nFast)

	rnd := rand.New(rand.NewSource(7))
	for iter := 0; iter < 200000; iter++ {
		u := fastRandomURL(rnd)
		hasNL := strings.IndexByte(u, '\n') >= 0
		base, opt := 0, 0
		for _, e := range ents {
			if e.re.MatchString(u) {
				base++
			}
			if e.fast != nil && !hasNL {
				if e.fast.matchFast(u) {
					opt++
				}
			} else if e.re.MatchString(u) {
				opt++
			}
		}
		if base != opt {
			t.Fatalf("fuzz mismatch for %q: base=%d opt=%d", u, base, opt)
		}
	}
}

// --- ruleStore-layer A/B benchmarks over the real corpus ---------------------

func buildCorpusRuleStore(b *testing.B, useFast bool) *ruleStore[string] {
	b.Helper()
	rules := fastCorpusRules(b)
	s := newRuleStore[string]()
	for _, r := range rules {
		if err := s.Insert(r.raw, r.raw); err != nil {
			b.Fatalf("insert %q: %v", r.raw, err)
		}
	}
	if !useFast {
		for i := range s.regexp {
			s.regexp[i].fast = nil // force the pure-regexp path
		}
	}
	return s
}

func benchmarkCorpusRuleStoreGet(b *testing.B, useFast bool) {
	s := buildCorpusRuleStore(b, useFast)
	rnd := rand.New(rand.NewSource(11))
	urls := make([]string, 3000)
	for i := range urls {
		// Always include a scheme: ruletree.Get (upstream) slices naively
		// and panics on 1-2 character inputs, which real URLs never are.
		urls[i] = "https://" + fastRandomURL(rnd)
	}
	b.ReportAllocs()
	b.ResetTimer()
	var idx, hits int
	for b.Loop() {
		hits += len(s.Get(urls[idx%len(urls)]))
		idx++
	}
	_ = hits
}

func BenchmarkRuleStoreGetFast(b *testing.B)   { benchmarkCorpusRuleStoreGet(b, true) }
func BenchmarkRuleStoreGetRegexp(b *testing.B) { benchmarkCorpusRuleStoreGet(b, false) }
