// Package abbench — zen-plus v3.1.0 与上游官方 v0.26.0 的规则引擎 A/B 对拍基准。
// 同一份代码分别放进 wt_plus（v3.1.0）与 wt_026（v0.26.0）两个 worktree 运行，
// 只用两版共有的公共 API（networkrules.New/ParseRule/ModifyReq/ModifyRes/Compact、
// asset.NewEngine/AddRule），保证测量语义完全一致。
//
// 装载路径 = 生产同构：与 internal/networkrules/coverage_measure_test.go measureOne
// （v3.1.0 覆盖去重 A/B 测量器）逐行同构 —— 13 个启用列表 cache.txt → asset.AddRule
// 分流（scriptlet/cosmetic/jsrule 等）→ 其余行跨列表去重 → nr.ParseRule → Compact。
// 列表定义与用户 config.json filter.filterLists 的 13 个 enabled=true 条目一一对应。
//
// 环境变量：
//   ZEN_FILTER_CACHE_DIR  过滤器缓存目录（默认 %LOCALAPPDATA%\Zen\filters）
//   ZEN_AB_CORPUS         语料目录（urls.txt / obfusgated_probes.tsv / adtest_urls.txt）
//   ZEN_AB_OUT            输出目录（memory/loadstats/parity TSV）
//   ZEN_AB_TAG            版本标签（写入所有输出文件首列，防混淆）
//
// 创建：2026-10-10（引擎对比测试任务）
package abbench

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/irbis-sh/zen-desktop/internal/asset"
	"github.com/irbis-sh/zen-desktop/internal/networkrules"
)

// ---- 生产同构列表定义（v3.1.0 internal/networkrules/coverage_realdata_test.go:39-53 逐字） ----

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

func cacheDir() string {
	if d := os.Getenv("ZEN_FILTER_CACHE_DIR"); d != "" {
		return d
	}
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
}

// isIgnoreLine 与 v3.1.0 internal/networkrules/coverage_realdata_test.go:114-131 逐字同构。
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

// ---- 装载 ----

type loadStats struct {
	lines        int         // 扫过的有效行
	parseOK      int         // ParseRule 成功（进入网络规则引擎）
	parseErr     int         // ParseRule 失败（被静默丢弃）
	assetHandled int         // asset 引擎接管（scriptlet/cosmetic 等，含 asset 层报错）
	dupSkipped   int         // 跨列表重复行跳过（与生产一致）
	perList      map[string]*listStat
}

type listStat struct {
	lines int
	errs  int
}

// loadAll 生产同构装载。返回引擎与统计。
// ZEN_AB_SKIP_ADGUARD=1 时跳过三个 AdGuard 品牌列表（Base/Spyware/CN），
// 用于"同规则集纯引擎内存对比"（消除 plus 解析器多救回规则的变量）。
func loadAll(tb testing.TB) (*networkrules.NetworkRules, *loadStats) {
	tb.Helper()
	nr := networkrules.New()
	eng, err := asset.NewEngine("assets.zen.local")
	if err != nil {
		tb.Fatalf("asset.NewEngine: %v", err)
	}
	st := &loadStats{perList: map[string]*listStat{}}
	seen := map[string]struct{}{}
	skipAdGuard := os.Getenv("ZEN_AB_SKIP_ADGUARD") == "1"
	for _, l := range realLists {
		if skipAdGuard && strings.HasPrefix(l.name, "AdGuard") {
			continue
		}
		sum := md5.Sum([]byte(l.url))
		data, err := os.ReadFile(filepath.Join(cacheDir(), hex.EncodeToString(sum[:])+".cache.txt"))
		if err != nil {
			tb.Logf("list %s: cache missing (%v)", l.name, err)
			continue
		}
		ls := &listStat{}
		st.perList[l.name] = ls
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if isIgnoreLine(line) {
				continue
			}
			st.lines++
			ls.lines++
			handled, ierr := eng.AddRule(line, l.trusted)
			if ierr != nil || handled {
				st.assetHandled++
				continue
			}
			if _, dup := seen[line]; dup {
				st.dupSkipped++
				continue
			}
			seen[line] = struct{}{}
			if _, err := nr.ParseRule(line, &l.name); err != nil {
				st.parseErr++
				ls.errs++
			} else {
				st.parseOK++
			}
		}
	}
	nr.Compact()
	return nr, st
}

// ---- 语料 ----

type probe struct {
	rawURL  string
	site    string
	dest    string
	referer string
	label   string
}

func corpusDir() string {
	if d := os.Getenv("ZEN_AB_CORPUS"); d != "" {
		return d
	}
	return "corpus"
}

// readBaseURLs 读 urls.txt（每行一个 URL）。
func readBaseURLs(tb testing.TB) []string {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join(corpusDir(), "urls.txt"))
	if err != nil {
		tb.Fatalf("read urls.txt: %v", err)
	}
	var urls []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := url.Parse(line); err != nil {
			continue
		}
		urls = append(urls, line)
	}
	return urls
}

// readTSVProbes 读 4-5 列 TSV 探针（url, site, dest, referer[, label]）。
func readTSVProbes(tb testing.TB, name string) []probe {
	tb.Helper()
	f, err := os.Open(filepath.Join(corpusDir(), name))
	if err != nil {
		tb.Fatalf("read %s: %v", name, err)
	}
	defer f.Close()
	var ps []probe
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 4 {
			continue
		}
		p := probe{rawURL: cols[0], site: cols[1], dest: cols[2], referer: cols[3]}
		if len(cols) >= 5 {
			p.label = cols[4]
		}
		ps = append(ps, p)
	}
	return ps
}

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func fetchMode(dest string) string {
	switch dest {
	case "document":
		return "navigate"
	case "":
		return ""
	default:
		return "no-cors"
	}
}

// buildBaseRequests 把探针转成预构建的基础请求（计时区外完成）。
// 返回与 accepted 探针一一对应的请求切片（NewRequest 失败的探针被剔除，防错位）。
func buildBaseRequests(tb testing.TB, ps []probe) ([]probe, []*http.Request) {
	tb.Helper()
	var reqs []*http.Request
	var accepted []probe
	for _, p := range ps {
		r, err := http.NewRequest("GET", p.rawURL, nil)
		if err != nil {
			continue
		}
		h := r.Header
		h.Set("User-Agent", userAgent)
		h.Set("Accept-Language", "zh-CN,zh;q=0.9")
		if p.site != "" {
			h.Set("Sec-Fetch-Site", p.site)
		}
		if p.dest != "" {
			h.Set("Sec-Fetch-Dest", p.dest)
			if m := fetchMode(p.dest); m != "" {
				h.Set("Sec-Fetch-Mode", m)
			}
		}
		if p.referer != "" {
			h.Set("Referer", p.referer)
		}
		accepted = append(accepted, p)
		reqs = append(reqs, r)
	}
	return accepted, reqs
}

// probeSet 汇总全部探针：P1（导航/通用，无 fetch 元数据头）+ P2（XHR/fetch 型）+ 额外探针。
func probeSet(tb testing.TB) (ps []probe) {
	tb.Helper()
	for _, u := range readBaseURLs(tb) {
		ps = append(ps, probe{rawURL: u}) // P1：无 Sec-Fetch 头（引擎回退路径）
	}
	for _, u := range readBaseURLs(tb) {
		var ref string
		ps = append(ps, probe{rawURL: u, site: "cross-site", dest: "empty", referer: ref}) // P2
	}
	// P2 的 referer 按 index 交替（zhihu/baidu 真实发布页，混合一方/三方判定）
	for i := range ps {
		if ps[i].site == "cross-site" {
			if i%2 == 0 {
				ps[i].referer = "https://www.zhihu.com/"
			} else {
				ps[i].referer = "https://www.baidu.com/"
			}
		}
	}
	ps = append(ps, readTSVProbes(tb, "obfusgated_probes.tsv")...)
	ps = append(ps, readTSVProbes(tb, "adtest_urls.txt")...)
	return ps
}

// ---- 输出工具 ----

func outDir() string {
	if d := os.Getenv("ZEN_AB_OUT"); d != "" {
		return d
	}
	return "out"
}

func tag() string {
	if t := os.Getenv("ZEN_AB_TAG"); t != "" {
		return t
	}
	return "untagged"
}

func writeOut(tb testing.TB, name, content string) {
	tb.Helper()
	p := filepath.Join(outDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		tb.Fatalf("write %s: %v", p, err)
	}
	tb.Logf("written: %s", p)
}

func statsLine(st *loadStats) string {
	return fmt.Sprintf("tag=%s lines=%d parseOK=%d parseErr=%d assetHandled=%d dupSkipped=%d",
		tag(), st.lines, st.parseOK, st.parseErr, st.assetHandled, st.dupSkipped)
}

// ---- 测试：装载内存 ----

// TestAbMemory 生产同构装载一次，测装载耗时、装载峰值堆、GC 后驻留堆、runtime Sys。
func TestAbMemory(t *testing.T) {
	if os.Getenv("ZEN_AB_RUN") != "1" {
		t.Skip("ZEN_AB_RUN=1 才运行（真实语料，高内存）")
	}
	var peak uint64
	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for !stop.Load() {
			runtime.ReadMemStats(&m)
			for {
				cur := atomic.LoadUint64(&peak)
				if m.HeapAlloc <= cur || atomic.CompareAndSwapUint64(&peak, cur, m.HeapAlloc) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	t0 := time.Now()
	nr, st := loadAll(t)
	loadMs := time.Since(t0).Milliseconds()
	stop.Store(true)
	<-done
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	runtime.KeepAlive(nr) // 防止引擎在 GC 测量前被判死回收（实测曾致 liveHeap=0.6MB 假象）
	memFile := "memory.txt"
	if os.Getenv("ZEN_AB_SKIP_ADGUARD") == "1" {
		memFile = "memory_noadguard.txt"
	}
	line := fmt.Sprintf("tag=%s loadMs=%d liveHeapMB=%.1f peakHeapMB=%.1f heapSysMB=%.1f sysMB=%.1f rules=%d parseErr=%d\n",
		tag(), loadMs, float64(m.HeapAlloc)/1048576, float64(peak)/1048576,
		float64(m.HeapSys)/1048576, float64(m.Sys)/1048576, st.parseOK, st.parseErr)
	writeOut(t, memFile, line)
	t.Log(strings.TrimSpace(line))
}

// ---- 基准：装载速度 ----

// BenchmarkAbLoad 完整生产同构装载（13 列表）的耗时。-benchtime=3x -count=3 运行。
func BenchmarkAbLoad(b *testing.B) {
	if os.Getenv("ZEN_AB_RUN") != "1" {
		b.Skip("ZEN_AB_RUN=1 才运行")
	}
	for i := 0; i < b.N; i++ {
		nr, st := loadAll(b)
		b.ReportMetric(float64(st.parseOK), "rules/op")
		_ = nr
	}
}

// ---- 基准：请求侧匹配 ----

func benchModifyReq(b *testing.B, ps []probe) {
	if os.Getenv("ZEN_AB_RUN") != "1" {
		b.Skip("ZEN_AB_RUN=1 才运行")
	}
	nr, st := loadAll(b)
	_, bases := buildBaseRequests(b, ps)
	b.ResetTimer()
	b.ReportAllocs()
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		for _, base := range bases {
			r := base.Clone(ctx)
			nr.ModifyReq(r)
		}
	}
	b.ReportMetric(float64(st.parseOK), "rules")
	b.ReportMetric(float64(len(bases)), "probes")
}

// BenchmarkAbModifyReq P1：全部语料无 Sec-Fetch 元数据头（导航/通用回退路径）。
func BenchmarkAbModifyReq(b *testing.B) {
	benchModifyReq(b, navProbes(b))
}

// BenchmarkAbModifyReqXHR P2：全部语料 dest=empty + cross-site + 交替真实 referer（fetch/XHR 路径）。
func BenchmarkAbModifyReqXHR(b *testing.B) {
	benchModifyReq(b, xhrProbes(b))
}

// navProbes/xhrProbes 构造对应 profile 的探针（含额外探针文件）。
func navProbes(tb testing.TB) []probe {
	var ps []probe
	for _, u := range readBaseURLs(tb) {
		ps = append(ps, probe{rawURL: u})
	}
	ps = append(ps, readTSVProbes(tb, "obfusgated_probes.tsv")...)
	ps = append(ps, readTSVProbes(tb, "adtest_urls.txt")...)
	return ps
}

func xhrProbes(tb testing.TB) []probe {
	var ps []probe
	for i, u := range readBaseURLs(tb) {
		ref := "https://www.zhihu.com/"
		if i%2 == 1 {
			ref = "https://www.baidu.com/"
		}
		ps = append(ps, probe{rawURL: u, site: "cross-site", dest: "empty", referer: ref})
	}
	ps = append(ps, readTSVProbes(tb, "obfusgated_probes.tsv")...)
	ps = append(ps, readTSVProbes(tb, "adtest_urls.txt")...)
	return ps
}

// ---- 基准：响应侧匹配 ----

func benchModifyRes(b *testing.B, ps []probe) {
	if os.Getenv("ZEN_AB_RUN") != "1" {
		b.Skip("ZEN_AB_RUN=1 才运行")
	}
	nr, _ := loadAll(b)
	_, bases := buildBaseRequests(b, ps)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, base := range bases {
			r := base.Clone(context.Background())
			res := &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
				Header:  http.Header{},
				Body:    http.NoBody,
				Request: r,
			}
			ct := "text/html; charset=utf-8"
			if d := base.Header.Get("Sec-Fetch-Dest"); d == "image" {
				ct = "image/avif"
			} else if d == "empty" {
				ct = "application/json"
			}
			res.Header.Set("Content-Type", ct)
			res.Header.Set("Server", "nginx")
			_, _ = nr.ModifyRes(r, res)
		}
	}
}

// BenchmarkAbModifyRes 响应侧：dest=document(text/html) 与 dest=empty(application/json)。
func BenchmarkAbModifyRes(b *testing.B) {
	ps := navProbes(b)
	benchModifyRes(b, ps)
}

// BenchmarkAbModifyResXHR 响应侧：dest=empty + cross-site（fetch 响应路径）。
func BenchmarkAbModifyResXHR(b *testing.B) {
	ps := xhrProbes(b)
	benchModifyRes(b, ps)
}

// ---- 测试：决策全量对拍导出 ----

// TestAbParity 全量语料过 ModifyReq + ModifyRes，输出逐条决策 TSV（两树跑完后外部 diff）。
func TestAbParity(t *testing.T) {
	if os.Getenv("ZEN_AB_RUN") != "1" {
		t.Skip("ZEN_AB_RUN=1 才运行")
	}
	nr, st := loadAll(t)
	ps := probeSet(t)
	ps, reqs := buildBaseRequests(t, ps)
	ctx := context.Background()

	var reqSB, resSB, obfSB strings.Builder
	var blockedCnt, redirectCnt int
	for i, base := range reqs {
		r := base.Clone(ctx)
		applied, block, redirect := nr.ModifyReq(r)
		raw1 := ""
		if len(applied) > 0 {
			raw1 = applied[0].RawRule
		}
		if block {
			blockedCnt++
		}
		if redirect != "" {
			redirectCnt++
		}
		// TSV 转义：raw 规则里的 tab/换行替换为空格
		raw1 = strings.ReplaceAll(raw1, "\t", " ")
		raw1 = strings.ReplaceAll(raw1, "\n", " ")
		fmt.Fprintf(&reqSB, "%s\t%d\t%s\t%s\t%v\t%s\t%d\t%s\n",
			tag(), i, ps[i].rawURL, ps[i].dest, block, redirect, len(applied), raw1)
		if block || redirect != "" || len(applied) > 0 {
			// 命中明细单独收（obfusgated 域名报告用）
			lbl := ps[i].label
			if lbl != "" || strings.Contains(ps[i].rawURL, "cb=1770000000000") {
				fmt.Fprintf(&obfSB, "%s\t%s\t%s\t%s\t%v\t%s\t%d\t%s\n",
					lbl, hostOf(ps[i].rawURL), ps[i].rawURL, ps[i].dest, block, redirect, len(applied), raw1)
			}
		}

		// 响应侧
		r2 := base.Clone(ctx)
		res := &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header:  http.Header{},
			Body:    http.NoBody,
			Request: r2,
		}
		ct := "text/html; charset=utf-8"
		switch ps[i].dest {
		case "image":
			ct = "image/avif"
		case "empty":
			ct = "application/json"
		}
		res.Header.Set("Content-Type", ct)
		res.Header.Set("Server", "nginx")
		res.Header.Set("Content-Length", "1234")
		mods, rerr := nr.ModifyRes(r2, res)
		fmt.Fprintf(&resSB, "%s\t%d\t%s\t%s\t%d\t%v\n", tag(), i, ps[i].rawURL, ps[i].dest, len(mods), rerr != nil)
	}

	var lsSB strings.Builder
	fmt.Fprintf(&lsSB, "%s\t%d\t%d\t%d\t%d\t%d\n", tag(), st.lines, st.parseOK, st.parseErr, st.assetHandled, st.dupSkipped)
	lsSB.WriteString("list\tlines\terrs\n")
	for _, l := range realLists {
		if ls, ok := st.perList[l.name]; ok {
			fmt.Fprintf(&lsSB, "%s\t%d\t%d\n", l.name, ls.lines, ls.errs)
		}
	}

	writeOut(t, "parity_req.tsv", reqSB.String())
	writeOut(t, "parity_res.tsv", resSB.String())
	writeOut(t, "obf_hits.tsv", obfSB.String())
	writeOut(t, "loadstats.tsv", lsSB.String())
	t.Logf("parity done: probes=%d blocked=%d redirect=%d | %s", len(ps), blockedCnt, redirectCnt, statsLine(st))
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Hostname()
	}
	return ""
}

// 编译期引用防呆（strconv 备用）
var _ = strconv.Itoa
