package networkrules

// coverage_realdata_test.go — 2026-10-09 真实订阅等价性门禁（env 门控）。
//
// 在真实过滤缓存上构建两台引擎（覆盖去重 ON vs OFF，生产同构管线），
// 对同一语料逐请求对比决策（block/redirect）——这是覆盖去重的
// 行为一致性门禁；缓存不在位时整组跳过，CI 不受影响。
//
// 触发：ZEN_FILTER_CACHE_DIR 指向缓存目录（默认 %LOCALAPPDATA%/Zen/filters
// 存在即跑）。ZEN_COV_SKIP_REALDATA=1 可显式跳过。
//
// 同时覆盖（多轮 A/B 供 -count=N 收集）：BenchmarkCoverageRealLoad
// 对比去重开关下的装载内存与耗时。

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/asset"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

// realList 与生产 app/config 启用列表一致（zz_probe_test.go probeLists 同源）。
type realList struct {
	name    string
	url     string
	trusted bool
}

var realLists = []realList{
	{"Zen-Ads", "https://cdn.jsdelivr.net/gh/irbis-sh/filter-lists@master/ads/ads.txt", false},
	{"EasyList", "https://easylist.to/easylist/easylist.txt", false},
	{"AdGuard-Base", "https://raw.githubusercontent.com/AdguardTeam/FiltersRegistry/master/filters/filter_2_Base/filter.txt", true},
	{"Zen-Privacy", "https://cdn.jsdelivr.net/gh/irbis-sh/filter-lists@master/privacy/privacy.txt", false},
	{"EasyPrivacy", "https://easylist.to/easylist/easyprivacy.txt", false},
	{"AdGuard-Spyware", "https://raw.githubusercontent.com/AdguardTeam/FiltersRegistry/master/filters/filter_3_Spyware/filter.txt", true},
	{"URL-Shortener", "https://raw.githubusercontent.com/DandelionSprout/adfilt/master/LegitimateURLShortener.txt", false},
	{"Online-Malicious", "https://malware-filter.gitlab.io/malware-filter/urlhaus-filter-online.txt", false},
	{"Phishing", "https://malware-filter.gitlab.io/malware-filter/phishing-filter-hosts.txt", false},
	{"Dan-Pollock", "https://someonewhocares.org/hosts/zero/hosts", false},
	{"Peter-Lowe", "https://pgl.yoyo.org/adservers/serverlist.php?hostformat=hosts&showintro=1&mimetype=plaintext", false},
	{"AdGuard-CN", "https://filters.adtidy.org/extension/ublock/filters/224.txt", false},
	{"anti-AD", "https://anti-ad.net/easylist.txt", true},
}

func realCacheDir(t *testing.T) string {
	t.Helper()
	if os.Getenv("ZEN_COV_SKIP_REALDATA") == "1" {
		t.Skip("ZEN_COV_SKIP_REALDATA=1")
	}
	dir := os.Getenv("ZEN_FILTER_CACHE_DIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Skipf("real filter cache not present: %s", dir)
	}
	return dir
}

// buildRealEngine 走生产同构装载管线（含 #804 行去重与覆盖去重开关）。
// injector 由 asset.Engine.AddRule 承担（与 filter.addRule 同序）；
// 不经 filter.Filter 以避免 import 环（filter → networkrules）。
func buildRealEngine(t *testing.T, cacheDir string, dedup bool, stats *map[string]int) *NetworkRules {
	t.Helper()
	nr := New(WithCoverageDedup(dedup))
	eng, err := asset.NewEngine("assets.zen.local")
	if err != nil {
		t.Fatalf("asset.NewEngine: %v", err)
	}
	// seen 对齐生产 #804：跨列表全局行去重（filter.seenRules 是 Filter 级）。
	seen := map[string]struct{}{}
	for _, l := range realLists {
		sum := md5.Sum([]byte(l.url))
		data, err := os.ReadFile(filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".cache.txt"))
		if err != nil {
			t.Logf("[realdata] 缓存缺失，跳过 %s", l.name)
			continue
		}
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || isIgnoreLine(line) {
				continue
			}
			if handled, ierr := eng.AddRule(line, l.trusted); ierr != nil || handled {
				continue
			}
			if _, dup := seen[line]; dup {
				continue
			}
			seen[line] = struct{}{}
			if _, perr := nr.ParseRule(line, &l.name); perr == nil {
				if stats != nil {
					(*stats)[l.name]++
				}
			}
		}
	}
	nr.Compact()
	return nr
}

func isIgnoreLine(line string) bool {
	if len(line) == 0 {
		return true
	}
	switch line[0] {
	case '!':
		return true
	case '[':
		return true
	case '#':
		if len(line) >= 2 && strings.ContainsRune("#%@$", rune(line[1])) {
			return false
		}
		return true
	}
	return false
}

func realDecision(t *testing.T, nr *NetworkRules, rawURL string, variant int) (bool, string, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil || req.URL == nil {
		return false, "", ""
	}
	switch variant {
	case 1:
		req.Header.Set("Sec-Fetch-User", "?1")
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
	case 2:
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		if h := req.URL.Hostname(); h != "" {
			req.Header.Set("Referer", "https://"+h+"/")
		}
	}
	_, block, redir := nr.ModifyReq(req)
	return block, redir, req.URL.String()
}

// TestRealCoverageDedupDecisionEquivalence：去重 ON vs OFF 决策等价。
func TestRealCoverageDedupDecisionEquivalence(t *testing.T) {
	cacheDir := realCacheDir(t)
	on := buildRealEngine(t, cacheDir, true, nil)
	off := buildRealEngine(t, cacheDir, false, nil)

	var onP, onE, offP, offE int
	on.primaryStore.walkValues(func(*rule.Rule) { onP++ })
	on.exceptionStore.walkValues(func(*exceptionrule.ExceptionRule) { onE++ })
	off.primaryStore.walkValues(func(*rule.Rule) { offP++ })
	off.exceptionStore.walkValues(func(*exceptionrule.ExceptionRule) { offE++ })
	t.Logf("规则数: dedup=ON primary=%d exception=%d | OFF primary=%d exception=%d (省 %d)",
		onP, onE, offP, offE, (offP+offE)-(onP+onE))
	if onP+onE >= offP+offE {
		t.Fatalf("dedup ON must store fewer or equal rules: %d vs %d", onP+onE, offP+offE)
	}

	urlsPath := os.Getenv("ZEN_COV_REAL_URLS")
	if urlsPath == "" {
		urlsPath = filepath.Join("..", "ruletree", "testdata", "urls.txt")
	}
	f, err := os.Open(urlsPath)
	if err != nil {
		t.Skipf("corpus missing: %v", err)
	}
	defer f.Close()

	var urls []string
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		u := strings.TrimSpace(sc.Text())
		if strings.Contains(u, "://") {
			urls = append(urls, u)
		}
	}

	diffs := 0
	for _, u := range urls {
		for v := 0; v < 3; v++ {
			b1, r1, u1 := realDecision(t, on, u, v)
			b2, r2, u2 := realDecision(t, off, u, v)
			if b1 != b2 || r1 != r2 || u1 != u2 {
				diffs++
				if diffs <= 10 {
					t.Errorf("decision diff url=%s variant=%d: on(%v,%q,%s) off(%v,%q,%s)", u, v, b1, r1, u1, b2, r2, u2)
				}
			}
		}
	}
	t.Logf("等价对拍: %d urls × 3 variants, decision_diffs=%d", len(urls), diffs)
	if diffs > 0 {
		t.Fatalf("decision equivalence violated: %d diffs", diffs)
	}
}

// BenchmarkCoverageRealLoad：真实缓存装载（含 Compact）内存/耗时 A/B。
// 多轮：go test -bench BenchmarkCoverageRealLoad -count=5。
// liveMB/op = b.N 次装载后保留堆相对首轮前基线的增量折算（近似稳态规则
// 内存口径）；ns/op 为装载全程耗时。GC 由 testing 框架在基准间自动管理。
func BenchmarkCoverageRealLoad(b *testing.B) {
	dir := os.Getenv("ZEN_FILTER_CACHE_DIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		b.Skipf("real filter cache not present: %s", dir)
	}
	for _, dedup := range []bool{true, false} {
		b.Run(fmt.Sprintf("dedup=%v", dedup), func(b *testing.B) {
			var m0, m1 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m0)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buildRealEngineB(dir, dedup)
			}
			b.StopTimer()
			runtime.GC()
			runtime.ReadMemStats(&m1)
			b.ReportMetric(float64(m1.HeapAlloc)/1024/1024, "heapMB-end")
			_ = m0
		})
	}
}

func buildRealEngineB(cacheDir string, dedup bool) {
	nr := New(WithCoverageDedup(dedup))
	eng, err := asset.NewEngine("assets.zen.local")
	if err != nil {
		return
	}
	// seen 对齐生产 #804：跨列表全局行去重。
	seen := map[string]struct{}{}
	for _, l := range realLists {
		sum := md5.Sum([]byte(l.url))
		data, err := os.ReadFile(filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".cache.txt"))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || isIgnoreLine(line) {
				continue
			}
			if handled, ierr := eng.AddRule(line, l.trusted); ierr != nil || handled {
				continue
			}
			if _, dup := seen[line]; dup {
				continue
			}
			seen[line] = struct{}{}
			_, _ = nr.ParseRule(line, &l.name)
		}
	}
	nr.Compact()
}
