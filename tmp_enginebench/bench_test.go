// Package tmp_enginebench hosts temporary A/B benchmarks comparing the
// production ruletree (internal/ruletree) with a patched prototype
// (tmp_enginebench/ruletreeopt). Scratch code for research only.
package tmp_enginebench

import (
	"bufio"
	"bytes"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	baseruletree "github.com/irbis-sh/zen-desktop/internal/ruletree"
	"github.com/irbis-sh/zen-desktop/internal/networkrules"
	baselinenr "github.com/irbis-sh/zen-desktop/tmp_enginebench/baselinenr/networkrules"
	optruletree "github.com/irbis-sh/zen-desktop/tmp_enginebench/ruletreeopt"
)

func loadLines(b *testing.B) [][]byte {
	b.Helper()
	var lines [][]byte
	for _, f := range []string{"../internal/ruletree/testdata/easylist.txt", "../internal/ruletree/testdata/easyprivacy.txt"} {
		data, err := os.ReadFile(f)
		if err != nil {
			b.Fatal(err)
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func loadURLList(b *testing.B, max int) []string {
	b.Helper()
	data, err := os.ReadFile("../internal/ruletree/testdata/urls.txt")
	if err != nil {
		b.Fatal(err)
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

func buildBaseTree(b *testing.B) *baseruletree.Tree[string] {
	b.Helper()
	t := baseruletree.New[string]()
	for _, line := range loadLines(b) {
		t.Insert(string(line), string(line))
	}
	return t
}

func buildOptTree(b *testing.B) *optruletree.Tree[string] {
	b.Helper()
	t := optruletree.New[string]()
	for _, line := range loadLines(b) {
		t.Insert(string(line), string(line))
	}
	return t
}

// TestOptTreeEquivalence verifies the patched tree returns identical match sets.
func TestOptTreeEquivalence(t *testing.T) {
	base := buildBaseTree(&testing.B{})
	opt := buildOptTree(&testing.B{})

	urls := loadURLList(&testing.B{}, 20000)
	for _, u := range urls {
		bg := base.Get(u)
		og := opt.Get(u)
		if len(bg) != len(og) {
			t.Fatalf("count mismatch for %q: base=%d opt=%d", u, len(bg), len(og))
		}
		sort.Strings(bg)
		sort.Strings(og)
		for i := range bg {
			if bg[i] != og[i] {
				t.Fatalf("match mismatch for %q at %d: base=%q opt=%q", u, i, bg[i], og[i])
			}
		}
	}
}

func BenchmarkTreeGetBase(b *testing.B) {
	tree := buildBaseTree(b)
	urls := loadURLList(b, 5000)
	var i int
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		tree.Get(urls[i%len(urls)])
		i++
	}
}

func BenchmarkTreeGetOpt(b *testing.B) {
	tree := buildOptTree(b)
	urls := loadURLList(b, 5000)
	var i int
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		tree.Get(urls[i%len(urls)])
		i++
	}
}

func BenchmarkTreeGetBaseParallel(b *testing.B) {
	tree := buildBaseTree(b)
	urls := loadURLList(b, 5000)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var i int
		for pb.Next() {
			tree.Get(urls[i%len(urls)])
			i++
		}
	})
}

func BenchmarkTreeGetOptParallel(b *testing.B) {
	tree := buildOptTree(b)
	urls := loadURLList(b, 5000)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var i int
		for pb.Next() {
			tree.Get(urls[i%len(urls)])
			i++
		}
	})
}

// TestTreeLiveMemory measures live heap of the loaded tree.
func TestTreeLiveMemory(t *testing.T) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	base := buildBaseTree(&testing.B{})
	fmt.Printf("  [dbg] base tree built\n")

	runtime.GC()
	var mid runtime.MemStats
	runtime.ReadMemStats(&mid)
	runtime.KeepAlive(base)

	opt := buildOptTree(&testing.B{})

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(opt)

	lines := len(loadLines(&testing.B{}))
	fmt.Printf("  [dbg] lines=%d\n", lines)
	fmt.Printf("tree live heap: base=%.1f MB (%.0f B/line), opt=%.1f MB, lines=%d\n",
		float64(mid.HeapInuse-before.HeapInuse)/(1<<20),
		float64(mid.HeapInuse-before.HeapInuse)/float64(lines),
		float64(after.HeapInuse-mid.HeapInuse)/(1<<20), lines)
}

// --- end-to-end networkrules benchmark ---

var lastParsedCount int

func buildNetworkRules(b *testing.B) *networkrules.NetworkRules {
	b.Helper()
	nr := networkrules.New()
	name := "bench"
	list := loadLines(b)
	n := 0
	for _, line := range list {
		l := string(line)
		if l[0] == '!' || l[0] == '[' {
			continue
		}
		if _, err := nr.ParseRule(l, &name); err != nil {
			// real engine skips unparsable lines silently in AddURL; ignore
			_ = err
		} else {
			n++
		}
	}
	lastParsedCount = n
	return nr
}

func buildRequests(b *testing.B, n int) []*http.Request {
	b.Helper()
	urls := loadURLList(b, n)
	reqs := make([]*http.Request, 0, len(urls))
	sites := []string{"same-origin", "cross-site", "none"}
	for i, u := range urls {
		parsed, err := url.Parse(u)
		if err != nil {
			continue
		}
		req := &http.Request{
			Method: http.MethodGet,
			URL:    parsed,
			Header: http.Header{},
		}
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

func BenchmarkModifyReq(b *testing.B) {
	nr := buildNetworkRules(b)
	reqs := buildRequests(b, 3000)
	b.ReportAllocs()
	b.ResetTimer()
	var i int
	for b.Loop() {
		r := reqs[i%len(reqs)]
		nr.ModifyReq(r)
		i++
	}
}

// --- component micro-costs for ModifyReq-level optimization estimates ---

func BenchmarkMicroURLString(b *testing.B) {
	u, _ := url.Parse("https://www.example.com/some/path/index.html?query=1&other=2")
	b.ReportAllocs()
	for b.Loop() {
		_ = u.String()
	}
}

func BenchmarkMicroRenderURLWithoutPort(b *testing.B) {
	u, _ := url.Parse("https://www.example.com:443/some/path/index.html?query=1&other=2")
	b.ReportAllocs()
	for b.Loop() {
		stripped := url.URL{
			Scheme:   u.Scheme,
			Host:     u.Hostname(),
			Path:     u.Path,
			RawQuery: u.RawQuery,
		}
		_ = stripped.String()
	}
}

func BenchmarkMicroURLParseReferer(b *testing.B) {
	ref := "https://www.example.com/some/path/index.html?query=1&other=2"
	b.ReportAllocs()
	for b.Loop() {
		_, err := url.Parse(ref)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMicroHeaderGet(b *testing.B) {
	h := http.Header{}
	h.Set("Referer", "https://www.example.com/")
	h.Set("Sec-Fetch-Site", "same-origin")
	h.Set("Sec-Fetch-Dest", "document")
	b.ReportAllocs()
	for b.Loop() {
		_ = h.Get("Referer")
		_ = h.Get("Sec-Fetch-Site")
		_ = h.Get("Sec-Fetch-Dest")
	}
}

// --- memory of full network rules (rule.Rule objects, not just tree) ---

func TestNetworkRulesLiveMemory(t *testing.T) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	nr := buildNetworkRules(&testing.B{})

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(nr)

	live := after.HeapInuse - before.HeapInuse
	lines := lastParsedCount
	fmt.Printf("networkrules live heap: %.1f MB, rules=%d, %.0f B/rule\n",
		float64(live)/(1<<20), lines, float64(live)/float64(lines))
}

// --- scanner overhead of list loading (filter.AddURL path) ---

func BenchmarkLineScanOnly(b *testing.B) {
	lines := loadLines(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var n int
		for _, l := range lines {
			s := string(l)
			if len(s) == 0 {
				continue
			}
			n += len(s)
		}
		_ = n
	}
}

var _ = bufio.NewScanner(nil)

// BenchmarkModifyReqRealBaseline runs the FROZEN pre-optimization engine
// (tmp_enginebench/baselinenr) on the identical corpus and request set.
func BenchmarkModifyReqRealBaseline(b *testing.B) {
	nr := buildNetworkRulesRealFrozen(b)
	urls := loadURLList(b, 3000)
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
	b.ReportAllocs()
	b.ResetTimer()
	var i int
	for b.Loop() {
		r := reqs[i%len(reqs)]
		nr.ModifyReq(r)
		i++
	}
}

func buildNetworkRulesRealFrozen(b testing.TB) *baselinenr.NetworkRules {
	b.Helper()
	nr := baselinenr.New()
	name := "real"
	for _, l := range loadRealLines(b) {
		if _, err := nr.ParseRule(l, &name); err != nil {
			_ = err
		}
	}
	return nr
}


// --- real production corpus (17 subscription caches) ---

// realCacheDir defaults to the local Zen install's filter cache; override
// with ZEN_FILTER_DIR when running on another machine.
func realCacheDir() string {
	if d := os.Getenv("ZEN_FILTER_DIR"); d != "" {
		return d
	}
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
}

func realFilterLists(tb testing.TB) []string {
	matches, err := filepath.Glob(filepath.Join(realCacheDir(), "*.cache.txt"))
	if err != nil || len(matches) == 0 {
		tb.Skipf("no real filter caches found in %s (set ZEN_FILTER_DIR to enable real-corpus benchmarks)", realCacheDir())
	}
	return matches
}

var ignoreLineRe = regexp.MustCompile(`^(?:!|\[|#[^#%@$])`)

// loadRealLines replicates filter.AddURL line semantics: TrimSpace, comment
// skip (ignoreLineRegex), !#include skip, and exact-line dedupe (markSeen).
func loadRealLines(tb testing.TB) []string {
	tb.Helper()
	lists := realFilterLists(tb)
	seen := map[string]struct{}{}
	var lines []string
	for _, f := range lists {
		data, err := os.ReadFile(f)
		if err != nil {
			tb.Fatal(err)
		}
		for _, raw := range bytes.Split(data, []byte("\n")) {
			line := strings.TrimSpace(string(raw))
			if line == "" || ignoreLineRe.MatchString(line) {
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

func buildNetworkRulesReal(b testing.TB) *networkrules.NetworkRules {
	b.Helper()
	nr := networkrules.New()
	name := "real"
	for _, l := range loadRealLines(b) {
		if _, err := nr.ParseRule(l, &name); err != nil {
			_ = err
		}
	}
	return nr
}

func BenchmarkModifyReqReal(b *testing.B) {
	nr := buildNetworkRulesReal(b)
	urls := loadURLList(b, 3000)
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
	b.ReportAllocs()
	b.ResetTimer()
	var i int
	for b.Loop() {
		r := reqs[i%len(reqs)]
		nr.ModifyReq(r)
		i++
	}
}

func TestNetworkRulesLiveMemoryReal(t *testing.T) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	lines := loadRealLines(t)
	nr := buildNetworkRulesReal(t)

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(nr)

	live := after.HeapInuse - before.HeapInuse
	fmt.Printf("REAL corpus: rules=%d, live=%.1f MB, %.0f B/rule\n", len(lines), float64(live)/(1<<20), float64(live)/float64(len(lines)))
}



// TestFastStoreFuzz checks shape equivalence on randomized printable URLs.
func TestFastStoreFuzz(t *testing.T) {
	base := buildReStore(&testing.B{}, false)
	opt := buildFastStore(&testing.B{})
	rnd := rand.New(rand.NewSource(7))
	chars := []byte("abcdefghijklmnopqrstuvwxyz0123456789-._~:/?#[]@!$&'()*+,;%=")
	for iter := 0; iter < 200000; iter++ {
		n := 1 + rnd.Intn(80)
		b := make([]byte, n)
		for i := range b {
			b[i] = chars[rnd.Intn(len(chars))]
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
		if base.matchAll(u) != opt.matchAll(u) {
			t.Fatalf("fuzz mismatch for %q: base=%d opt=%d", u, base.matchAll(u), opt.matchAll(u))
		}
	}
}


func TestDebugAST(t *testing.T) {
	for _, pat := range []string{
		`^https?:\/\/.*\/.*(sw[0-9a-z._-]{1,6}|\.notify\.).*`,
		`^https?:\/\/.*\/.*sw[0-9._].*`,
		`^https?:\/\/.*bit(ly)?\.(com|ly)\/`,
	} {
		re, err := syntax.Parse(pat, syntax.Perl)
		if err != nil {
			fmt.Printf("parse err %v\n", err)
			continue
		}
		fmt.Printf("PAT %s\n%s\n---\n", pat, re.String())
		sh := parseFastShape(pat)
		fmt.Printf("  shape=%v\n", sh != nil)
	}
	st := buildReStore(&testing.B{}, false)
	for _, idx := range []int{12, 16} {
		pat := st.plain[idx].re.String()
		fmt.Printf("PAT#%d %s\n", idx, pat)
		re, err := syntax.Parse(pat, syntax.Perl)
		if err != nil {
			continue
		}
		dumpOp(re, 1)
	}
}

func dumpOp(r *syntax.Regexp, depth int) {
	ind := strings.Repeat("  ", depth)
	fmt.Printf("%sop=%v flags=%d runes=%q sub=%d\n", ind, r.Op, r.Flags, string(r.Rune), len(r.Sub))
	for _, s := range r.Sub {
		dumpOp(s, depth+1)
	}
}


// --- specialized fast matcher for ^https?:// .* <literal-ish> .* shapes ---

// fastShape is an exact equivalent of a restricted regexp shape:
//
//	^https?:\/\/ <.* and literals and (lit)? groups> <mandatory alternation or class> .*
//
// The matcher walks the AST keeping a cross-product of pending branch
// literals; any pattern outside the supported shapes falls back to regexp.
type fastShape struct {
	branches []fastBranch
	needPath bool // branch content must appear after the first "/"
}

type fastBranch struct {
	lit    []byte
	cls    *bytesetSet // class that must follow lit (nil = none)
	clsMin int         // min class repetitions (default 1)
	clsMax int         // max class repetitions (only used when cls != nil)
}

type bytesetSet [4]uint64

func (s *bytesetSet) add(b byte)      { s[b/64] |= 1 << (b % 64) }
func (s *bytesetSet) has(b byte) bool { return s[b/64]&(1<<(b%64)) != 0 }

func classSetOf(r *syntax.Regexp) *bytesetSet {
	if r.Op != syntax.OpCharClass {
		return nil
	}
	cls := &bytesetSet{}
	for i := 0; i+1 < len(r.Rune); i += 2 {
		for c := r.Rune[i]; c <= r.Rune[i+1]; c++ {
			if c < 256 {
				cls.add(byte(c))
			}
		}
	}
	return cls
}

// parseFastShape attempts to compile the regexp AST into a fastShape.
// Returns nil when the pattern does not fit the supported shapes.
func parseFastShape(pattern string) *fastShape {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil
	}
	if re.Op != syntax.OpConcat || len(re.Sub) < 2 {
		return nil
	}
	subs := re.Sub
	if subs[0].Op != syntax.OpBeginText {
		return nil
	}
	if len(subs) < 3 || subs[1].Op != syntax.OpLiteral || string(subs[1].Rune) != "http" {
		return nil
	}
	i := 2
	if subs[i].Op == syntax.OpQuest && len(subs[i].Sub) == 1 && subs[i].Sub[0].Op == syntax.OpLiteral && string(subs[i].Sub[0].Rune) == "s" {
		i++
	}
	if subs[i].Op != syntax.OpLiteral || string(subs[i].Rune) != "://" {
		return nil
	}
	i++
	sh := &fastShape{}
	pend := [][]byte{{}} // cross product of pending branch literals
	anyBranch := false
	flush := func() {
		for _, p := range pend {
			if len(p) > 0 {
				sh.branches = append(sh.branches, fastBranch{lit: p})
				anyBranch = true
			}
		}
		pend = [][]byte{{}}
	}
	isDot := func(r *syntax.Regexp) bool {
		return r.Op == syntax.OpStar && len(r.Sub) == 1 && (r.Sub[0].Op == syntax.OpAnyCharNotNL || r.Sub[0].Op == syntax.OpAnyChar)
	}
	for ; i < len(subs); i++ {
		r := subs[i]
		switch r.Op {
		case syntax.OpStar:
			if !isDot(r) {
				return nil
			}
			// .*\/ => branch content must sit after a slash
			if i+1 < len(subs) && subs[i+1].Op == syntax.OpLiteral && string(subs[i+1].Rune) == "/" {
				need := false
				for _, p := range pend {
					if len(p) > 0 {
						need = true
					}
				}
				if need {
					return nil // unsupported: literal before the \/ fork point
				}
				sh.needPath = true
				i++
				continue
			}
			// Trailing .* is a pure tail: the forked prefixes are complete
			// branches. A middle .* between required parts would need AND
			// semantics we do not support.
			if len(pend) > 1 && i != len(subs)-1 {
				return nil
			}
			flush()
		case syntax.OpLiteral:
			for j := range pend {
				pend[j] = append(pend[j], string(r.Rune)...)
			}
		case syntax.OpCharClass:
			cls := classSetOf(r)
			if cls == nil {
				return nil
			}
			for _, p := range pend {
				if len(p) == 0 {
					return nil // bare class with no literal: unsupported
				}
				sh.branches = append(sh.branches, fastBranch{lit: p, cls: cls, clsMin: 1, clsMax: 1})
				anyBranch = true
			}
			pend = [][]byte{{}}
		case syntax.OpRepeat:
			// literal + [class]{min,max} at top level (e.g. \?{50,} tail)
			cls := classSetOf(r.Sub[0])
			if cls == nil || r.Min < 1 {
				return nil
			}
			cmax := r.Max
			if cmax < 0 {
				cmax = 1 << 30
			}
			for _, p := range pend {
				if len(p) == 0 {
					return nil
				}
				sh.branches = append(sh.branches, fastBranch{lit: p, cls: cls, clsMax: cmax, clsMin: r.Min})
				anyBranch = true
			}
			pend = [][]byte{{}}
		case syntax.OpCapture:
			if len(r.Sub) != 1 || r.Sub[0].Op != syntax.OpAlternate {
				return nil
			}
			var newPend [][]byte
			for _, alt := range r.Sub[0].Sub {
				switch {
				case alt.Op == syntax.OpLiteral:
					for _, p := range pend {
						newPend = append(newPend, append(append([]byte{}, p...), string(alt.Rune)...))
					}
				case alt.Op == syntax.OpConcat && len(alt.Sub) == 2 &&
					alt.Sub[0].Op == syntax.OpLiteral && alt.Sub[1].Op == syntax.OpRepeat &&
					alt.Sub[1].Min >= 1:
					rep := alt.Sub[1]
					cls := classSetOf(rep.Sub[0])
					if cls == nil {
						return nil
					}
					cmax := rep.Max
					if cmax < 0 {
						cmax = 1 << 30
					}
					for _, p := range pend {
						lit := append(append([]byte{}, p...), string(alt.Sub[0].Rune)...)
						sh.branches = append(sh.branches, fastBranch{lit: lit, cls: cls, clsMin: rep.Min, clsMax: cmax})
						anyBranch = true
					}
				case alt.Op == syntax.OpConcat && len(alt.Sub) == 2 &&
					alt.Sub[0].Op == syntax.OpLiteral && alt.Sub[1].Op == syntax.OpCharClass:
					// prefix-factored alternation, e.g. bid|biz => bi[dz]
					cls := classSetOf(alt.Sub[1])
					if cls == nil {
						return nil
					}
					for _, p := range pend {
						lit := append(append([]byte{}, p...), string(alt.Sub[0].Rune)...)
						sh.branches = append(sh.branches, fastBranch{lit: lit, cls: cls, clsMin: 1, clsMax: 1})
						anyBranch = true
					}
				default:
					return nil
				}
			}
			pend = newPend
		case syntax.OpQuest:
			// (ly)? style optional group: fork pending prefixes
			if len(r.Sub) != 1 || r.Sub[0].Op != syntax.OpCapture {
				return nil
			}
			cap := r.Sub[0]
			var alts []*syntax.Regexp
			switch {
			case len(cap.Sub) == 1 && cap.Sub[0].Op == syntax.OpAlternate:
				alts = cap.Sub[0].Sub
			case len(cap.Sub) == 1 && cap.Sub[0].Op == syntax.OpLiteral:
				alts = cap.Sub
			default:
				return nil
			}
			var newPend [][]byte
			newPend = append(newPend, pend...)
			for _, alt := range alts {
				if alt.Op != syntax.OpLiteral {
					return nil
				}
				for _, p := range pend {
					newPend = append(newPend, append(append([]byte{}, p...), string(alt.Rune)...))
				}
			}
			pend = newPend
		default:
			return nil
		}
	}
	flush()
	if !anyBranch {
		return nil
	}
	return sh
}

// matchFast is the exact equivalent of the original regexp for accepted shapes.
func (sh *fastShape) matchFast(url string) bool {
	var scope string
	switch {
	case strings.HasPrefix(url, "https://"):
		scope = url[8:]
	case strings.HasPrefix(url, "http://"):
		scope = url[7:]
	default:
		return false
	}
	if sh.needPath {
		i := strings.IndexByte(scope, '/')
		if i < 0 {
			return false
		}
		scope = scope[i:]
	}
	for _, br := range sh.branches {
		from := 0
		for {
			j := strings.Index(scope[from:], string(br.lit))
			if j < 0 {
				break
			}
			if br.cls == nil {
				return true
			}
			p := from + j + len(br.lit)
			q := p
			max := p + br.clsMax
			if max > len(scope) {
				max = len(scope)
			}
			k := 0
			for q < max && br.cls.has(scope[q]) {
				q++
				k++
			}
			if k >= br.clsMin {
				return true
			}
			from = from + j + 1
		}
	}
	return false
}

// specialized reStore: fast shapes replace regexp; others fall back.
type fastStore struct {
	shapes []*fastShape
	fallback []*regexp.Regexp
	count  int
}

func buildFastStore(b *testing.B) *fastStore {
	b.Helper()
	fs := &fastStore{}
	for _, line := range loadLines(b) {
		l := string(line)
		if l[0] == '!' || l[0] == '[' {
			continue
		}
		if len(l) > 1 && l[0] == '/' {
			end := strings.LastIndexByte(l, '/')
			if end > 1 && (end == len(l)-1 || l[end+1] == '$') {
				body := l[1:end]
				re, err := regexp.Compile(body)
				if err != nil {
					continue
				}
				fs.count++
				sh := parseFastShape(body)
				if sh != nil {
					fs.shapes = append(fs.shapes, sh)
				} else {
					fs.fallback = append(fs.fallback, re)
				}
			}
		}
	}
	return fs
}

func (fs *fastStore) matchAll(url string) int {
	hits := 0
	for _, sh := range fs.shapes {
		if sh.matchFast(url) {
			hits++
		}
	}
	for _, re := range fs.fallback {
		if re.MatchString(url) {
			hits++
		}
	}
	return hits
}

func TestFastStoreEquivalence(t *testing.T) {
	base := buildReStore(&testing.B{}, false)
	opt := buildFastStore(&testing.B{})
	fmt.Printf("fast store: shapes=%d fallback=%d of %d regexps\n", len(opt.shapes), len(opt.fallback), opt.count)
	urls := loadURLList(&testing.B{}, 20000)
	for _, u := range urls {
		if base.matchAll(u) != opt.matchAll(u) {
			t.Fatalf("mismatch for %q: base=%d opt=%d", u, base.matchAll(u), opt.matchAll(u))
		}
	}
}

func BenchmarkReStoreFast(b *testing.B) {
	st := buildFastStore(b)
	urls := loadURLList(b, 5000)
	var i int
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		st.matchAll(urls[i%len(urls)])
		i++
	}
}


func TestRegexpCostDistribution(t *testing.T) {
	st := buildReStore(&testing.B{}, false)
	urls := loadURLList(&testing.B{}, 1000)
	type cost struct {
		idx     int
		pat     string
		req     string
		totalNs float64
		hits    int
	}
	costs := make([]cost, len(st.plain))
	for i := range st.plain {
		costs[i] = cost{idx: i, pat: st.plain[i].re.String(), req: st.plain[i].required}
	}
	for _, u := range urls {
		for i := range st.plain {
			start := time.Now()
			if st.plain[i].re.MatchString(u) {
				costs[i].hits++
			}
			costs[i].totalNs += float64(time.Since(start).Nanoseconds())
		}
	}
	sort.Slice(costs, func(a, b int) bool { return costs[a].totalNs > costs[b].totalNs })
	var sum float64
	for _, c := range costs {
		sum += c.totalNs
	}
	var cum float64
	fmt.Printf("regexp count=%d, total=%.1fms over %d urls\n", len(costs), sum/1e6, len(urls))
	for i, c := range costs[:25] {
		cum += c.totalNs
		fmt.Printf("#%02d idx=%d hits=%d avg=%.0fns share=%.1f%% cum=%.1f%% req=%q pat=%.80s\n",
			i, c.idx, c.hits, c.totalNs/float64(len(urls)), 100*c.totalNs/sum, 100*cum/sum, c.req, c.pat)
	}
}


// --- regexp store prefilter prototype ---

type reEntry struct {
	re       *regexp.Regexp
	required string
}

type reStore struct {
	plain   []reEntry
	tri     map[uint32][]int32
	noLit   []int32
	enabled bool
}

func trigram(s string) uint32 {
	if len(s) < 3 {
		return 0xFFFFFFFF
	}
	return uint32(s[0])<<16 | uint32(s[1])<<8 | uint32(s[2])
}

// requiredLiteral extracts a literal substring that must appear in any match,
// via regexp/syntax AST. Returns "" when none can be derived.
func requiredLiteral(pattern string) string {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return ""
	}
	best := ""
	var walk func(r *syntax.Regexp)
	walk = func(r *syntax.Regexp) {
		switch r.Op {
		case syntax.OpLiteral:
			if len(r.Rune) > len(best) {
				best = string(r.Rune)
			}
		case syntax.OpConcat:
			for _, c := range r.Sub {
				walk(c)
			}
		case syntax.OpCapture, syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
			// literals inside a repeated group are not guaranteed to appear;
			// only descend into capture groups (always participate in match)
			if r.Op == syntax.OpCapture {
				for _, c := range r.Sub {
					walk(c)
				}
			}
		}
	}
	walk(re)
	if len(best) < 3 {
		return ""
	}
	return best
}

func buildReStore(b *testing.B, prefilter bool) *reStore {
	b.Helper()
	st := &reStore{tri: make(map[uint32][]int32), enabled: prefilter}
	for _, line := range loadLines(b) {
		l := string(line)
		if l[0] == '!' || l[0] == '[' {
			continue
		}
		if len(l) > 1 && l[0] == '/' {
			end := strings.LastIndexByte(l, '/')
			if end > 1 && (end == len(l)-1 || l[end+1] == '$') {
				body := l[1:end]
				re, err := regexp.Compile(body)
				if err != nil {
					continue
				}
				idx := len(st.plain)
				req := requiredLiteral(body)
				st.plain = append(st.plain, reEntry{re: re, required: req})
				if prefilter {
					if req == "" {
						st.noLit = append(st.noLit, int32(idx))
					} else {
						t := trigram(req)
						st.tri[t] = append(st.tri[t], int32(idx))
					}
				}
			}
		}
	}
	return st
}

func (st *reStore) matchAll(url string) int {
	hits := 0
	if !st.enabled {
		for i := range st.plain {
			if st.plain[i].re.MatchString(url) {
				hits++
			}
		}
		return hits
	}
	var cand [64]int32
	nCand := 0
	var seen [2048]bool
	for i := 0; i+3 <= len(url); i++ {
		for _, idx := range st.tri[trigram(url[i:i+3])] {
			if !seen[idx] {
				seen[idx] = true
				if nCand < len(cand) {
					cand[nCand] = idx
					nCand++
				}
			}
		}
	}
	for _, idx := range st.noLit {
		if !seen[idx] {
			seen[idx] = true
			if nCand < len(cand) {
				cand[nCand] = idx
				nCand++
			}
		}
	}
	for j := 0; j < nCand; j++ {
		idx := cand[j]
		e := st.plain[idx]
		if e.required != "" && !strings.Contains(url, e.required) {
			continue
		}
		if e.re.MatchString(url) {
			hits++
		}
	}
	return hits
}

func TestReStoreEquivalence(t *testing.T) {
	base := buildReStore(&testing.B{}, false)
	opt := buildReStore(&testing.B{}, true)
	urls := loadURLList(&testing.B{}, 20000)
	for _, u := range urls {
		if base.matchAll(u) != opt.matchAll(u) {
			t.Fatalf("mismatch for %q: base=%d opt=%d", u, base.matchAll(u), opt.matchAll(u))
		}
	}
}

func BenchmarkReStorePlain(b *testing.B) {
	st := buildReStore(b, false)
	urls := loadURLList(b, 5000)
	var i int
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		st.matchAll(urls[i%len(urls)])
		i++
	}
}

func BenchmarkReStorePrefiltered(b *testing.B) {
	st := buildReStore(b, true)
	urls := loadURLList(b, 5000)
	var i int
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		st.matchAll(urls[i%len(urls)])
		i++
	}
}
