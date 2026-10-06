// fullmatrix_bench_test.go — 全维度新旧引擎对比矩阵（基线 vs 生产）。
//
// 维度清单（与 任务书 对应）：
//  1. 加载耗时        BenchmarkFMLoadReal{Baseline,Prod}（-benchtime=1x，ns/op=全量装载）
//     + TestFMLoadTimeReal（time.Now 差直测）
//  2. 规则树装载吞吐  BenchmarkFMTreeInsert{Baseline,Prod}（easylist+easyprivacy，SetBytes→MB/s）
//  3. ModifyReqReal   已有 BenchmarkModifyReqReal{,Baseline}（bench_test.go，不重复定义）
//  4. ModifyResReal   BenchmarkFMModifyResReal{,Baseline}（新增基准）
//  5. HandleRequest   BenchmarkFMHandleRequestProd（真 filter.Filter+stub）
//     基线侧：*basenr.NetworkRules 因 rule.Rule 类型不同无法实现
//     filter.networkRules 接口（internal/networkrules/rule/rule.go
//     的 Rule 含未导出字段，无法从外部构造），改用与
//     filter.HandleRequest（internal/filter/filter.go:368-401）逐行
//     等价的镜像包装 —— 已如实记录，差异=接口动态分发（可忽略）。
//  6. live 内存       TestFMLiveMemoryRealBaseline（生产版已有
//     TestNetworkRulesLiveMemoryReal；两者须各自独立进程运行）
//  7. GC 指标         TestFMGCStatsModifyReq（MemStats PauseTotalNs/NumGC 差）
//  8. 规则文本兼容性  TestFMParseRuleCompatReal（61.1 万行流式逐行对拍，
//     不同时物化两份行清单）
package tmp_enginebench

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/irbis-sh/zen-desktop/internal/filter"
	"github.com/irbis-sh/zen-desktop/internal/filterliststore"
	prodnr "github.com/irbis-sh/zen-desktop/internal/networkrules"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
	"github.com/irbis-sh/zen-desktop/internal/process"
	prodtree "github.com/irbis-sh/zen-desktop/internal/ruletree"
	basenr "github.com/irbis-sh/zen-desktop/tmp_enginebench/baselinenr/networkrules"
	basetree "github.com/irbis-sh/zen-desktop/tmp_enginebench/baselinenr/ruletree"
)

// ---------------- 1) 全量装载耗时（启动成本） ----------------

func BenchmarkFMLoadRealBaseline(b *testing.B) {
	for b.Loop() {
		_ = buildBaseNetworkRulesReal(b)
	}
}

func BenchmarkFMLoadRealProd(b *testing.B) {
	for b.Loop() {
		_ = buildNetworkRulesReal(b)
	}
}

// TestFMLoadTimeReal 直接以 time.Now 差报告全量装载耗时。
// 注意：装载含读文件+行过滤（loadRealLines），与生产 AddURL 读文件语义一致；
// lastParsedCount 只由 buildNetworkRules（easylist 版）维护，这里改报
// loadRealLines 的行数（含未解析行，规则条数见 TestFMParseRuleCompatReal）。
func TestFMLoadTimeReal(t *testing.T) {
	lineN := len(loadRealLines(t))
	t0 := time.Now()
	nrP := buildNetworkRulesReal(t)
	dP := time.Since(t0)

	t1 := time.Now()
	nrB := buildBaseNetworkRulesReal(t)
	dB := time.Since(t1)

	fmt.Printf("FULLMATRIX load time: prod=%v baseline=%v (corpus lines=%d)\n", dP, dB, lineN)
	runtime.KeepAlive(nrP)
	runtime.KeepAlive(nrB)
}

// ---------------- 2) 规则树装载吞吐（MB/s） ----------------

func fmInsertBytes(lines [][]byte) int64 {
	var total int64
	for _, l := range lines {
		total += int64(len(l))
	}
	return total
}

func BenchmarkFMTreeInsertBaseline(b *testing.B) {
	lines := loadLines(b)
	b.SetBytes(fmInsertBytes(lines))
	b.ReportAllocs()
	for b.Loop() {
		t := basetree.New[string]()
		for _, line := range lines {
			t.Insert(string(line), string(line))
		}
		runtime.KeepAlive(t)
	}
}

func BenchmarkFMTreeInsertProd(b *testing.B) {
	lines := loadLines(b)
	b.SetBytes(fmInsertBytes(lines))
	b.ReportAllocs()
	for b.Loop() {
		t := prodtree.New[string]()
		for _, line := range lines {
			t.Insert(string(line), string(line))
		}
		runtime.KeepAlive(t)
	}
}

// ---------------- 4) ModifyResReal（同请求集构造 http.Response） ----------------

func fmBuildReqResPairs(b *testing.B, n int) ([]*http.Request, []*http.Response) {
	b.Helper()
	urls := loadURLList(b, n)
	reqs := make([]*http.Request, 0, len(urls))
	ress := make([]*http.Response, 0, len(urls))
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
		res := &http.Response{
			StatusCode: 200,
			Status:     "200 OK",
			Header: http.Header{
				"Content-Type":  {"text/html; charset=utf-8"},
				"Cache-Control": {"no-cache"},
			},
			Body:    http.NoBody,
			Request: req,
		}
		reqs = append(reqs, req)
		ress = append(ress, res)
	}
	return reqs, ress
}

func BenchmarkFMModifyResReal(b *testing.B) {
	nr := buildNetworkRulesReal(b)
	reqs, ress := fmBuildReqResPairs(b, 3000)
	b.ReportAllocs()
	b.ResetTimer()
	var i int
	for b.Loop() {
		nr.ModifyRes(reqs[i%len(reqs)], ress[i%len(ress)])
		i++
	}
}

func BenchmarkFMModifyResRealBaseline(b *testing.B) {
	nr := buildBaseNetworkRulesReal(b)
	reqs, ress := fmBuildReqResPairs(b, 3000)
	b.ReportAllocs()
	b.ResetTimer()
	var i int
	for b.Loop() {
		nr.ModifyRes(reqs[i%len(reqs)], ress[i%len(ress)])
		i++
	}
}

// ---------------- 5) HandleRequest 级 ----------------

// --- 最小 stub（internal/filter/filter.go 的接口，全部最小实现） ---

type fmStubInjector struct{}

func (fmStubInjector) AddRule(string, bool) (bool, error)         { return false, nil }
func (fmStubInjector) Inject(*http.Request, *http.Response) error { return nil }

type fmStubListStore struct{}

func (fmStubListStore) Get(context.Context, string, filterliststore.FetchMode) (io.ReadCloser, filterliststore.Source, error) {
	return nil, 0, errors.New("fm stub: no list store")
}

type fmStubObserver struct{}

func (fmStubObserver) OnFilterBlock(string, string, string, []rule.Rule, process.Info)            {}
func (fmStubObserver) OnFilterRedirect(string, string, string, string, []rule.Rule, process.Info) {}
func (fmStubObserver) OnFilterModify(string, string, string, []rule.Rule, process.Info)           {}

type fmStubWhitelist struct{}

func (fmStubWhitelist) GetPort() int { return 0 } // 用户导航拦截走简单 block response

// fmBuildHandleReqs 为 HandleRequest 基准预构建请求集（与 ModifyReqReal 同源同形）。
func fmBuildHandleReqs(b *testing.B, n int) []*http.Request {
	b.Helper()
	urls := loadURLList(b, n)
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

// BenchmarkFMHandleRequestProd 生产引擎经真 filter.Filter（stub 依赖）。
// 引擎先 Compact()，模拟生产 Finalize（filter.go:409-415）后的服务态。
func BenchmarkFMHandleRequestProd(b *testing.B) {
	nr := buildNetworkRulesReal(b)
	nr.Compact()
	f, err := filter.NewFilter(nr, fmStubInjector{}, fmStubListStore{}, fmStubObserver{}, fmStubWhitelist{})
	if err != nil {
		b.Fatal(err)
	}
	reqs := fmBuildHandleReqs(b, 3000)
	b.ReportAllocs()
	b.ResetTimer()
	var i int
	for b.Loop() {
		_, _ = f.HandleRequest(reqs[i%len(reqs)], process.Info{})
		i++
	}
}

// fmHandleRequestBaseline 是 filter.HandleRequest（filter.go:368-401）对基线
// 引擎的镜像包装：基线 rule.Rule 类型不同（未导出字段，见文件头注释），
// 无法注入 filter.networkRules 接口；此处逐行复刻同一处理链。
func fmHandleRequestBaseline(nr *basenr.NetworkRules, req *http.Request) (*http.Response, error) {
	_, shouldBlock, redirectURL := nr.ModifyReq(req)
	if shouldBlock {
		if req.Header.Get("Sec-Fetch-User") == "?1" && req.Header.Get("Sec-Fetch-Dest") == "document" {
			port := fmStubWhitelist{}.GetPort()
			if port <= 0 {
				return nr.CreateBlockResponse(req), nil
			}
			// port 恒为 0，block page 分支在生产 stub 下不可达，与生产一致
		}
		return nr.CreateBlockResponse(req), nil
	}
	if redirectURL != "" {
		return nr.CreateRedirectResponse(req, redirectURL), nil
	}
	return nil, nil
}

func BenchmarkFMHandleRequestBaseline(b *testing.B) {
	nr := buildBaseNetworkRulesReal(b)
	nr.Compact()
	reqs := fmBuildHandleReqs(b, 3000)
	b.ReportAllocs()
	b.ResetTimer()
	var i int
	for b.Loop() {
		_, _ = fmHandleRequestBaseline(nr, reqs[i%len(reqs)])
		i++
	}
}

// ---------------- 6) 引擎 live 内存（基线版；各自独立进程运行） ----------------

func TestFMLiveMemoryRealBaseline(t *testing.T) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	lines := loadRealLines(t)
	nr := buildBaseNetworkRulesReal(t)

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(nr)
	runtime.KeepAlive(lines)

	live := after.HeapInuse - before.HeapInuse
	fmt.Printf("FULLMATRIX baseline corpus: rules=%d, live=%.1f MB, %.0f B/rule\n",
		len(lines), float64(live)/(1<<20), float64(live)/float64(len(lines)))
}

// ---------------- 7) GC 指标（ModifyReqReal 工作负载窗口内） ----------------

// TestFMGCStatsModifyReq 在固定工作负载窗口读 MemStats 差值。
// 两引擎常驻同进程同堆条件下先后测量，公平 A/B。
func TestFMGCStatsModifyReq(t *testing.T) {
	nrP := buildNetworkRulesReal(t)
	nrB := buildBaseNetworkRulesReal(t)

	measure := func(name string, mod func(*http.Request)) {
		all := goldenLoadURLs(t)
		if len(all) > 1000 {
			all = all[:1000]
		}
		urls := all
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
		runtime.GC()
		var m0 runtime.MemStats
		runtime.ReadMemStats(&m0)

		const passes = 3
		for p := 0; p < passes; p++ {
			for _, r := range reqs {
				mod(r)
			}
		}

		var m1 runtime.MemStats
		runtime.ReadMemStats(&m1)
		fmt.Printf("FULLMATRIX GC %s: NumGC=%d PauseTotal=%.2fms Mallocs=%d HeapAlloc delta=%.1f MB (workload=%d reqs)\n",
			name,
			m1.NumGC-m0.NumGC,
			float64(m1.PauseTotalNs-m0.PauseTotalNs)/1e6,
			m1.Mallocs-m0.Mallocs,
			float64(int64(m1.HeapAlloc)-int64(m0.HeapAlloc))/(1<<20),
			passes*len(reqs))
	}

	fmt.Println("GCTRACE-MARK prod-workload-start")
	measure("prod", func(r *http.Request) { nrP.ModifyReq(r) })
	fmt.Println("GCTRACE-MARK prod-workload-end")
	fmt.Println("GCTRACE-MARK baseline-workload-start")
	measure("baseline", func(r *http.Request) { nrB.ModifyReq(r) })
	fmt.Println("GCTRACE-MARK baseline-workload-end")
}

// ---------------- 8) 规则文本兼容性（611k 行流式逐行对拍） ----------------

// TestFMParseRuleCompatReal 逐行读 17 个真实订阅缓存，双引擎各 ParseRule，
// 每行比对 (isException, err==nil)。流式处理：不物化全量行切片、不建行去重
// 表（去重不影响逐行 接受/拒绝 判定——两引擎收到同一行序列）。
func TestFMParseRuleCompatReal(t *testing.T) {
	nrP := prodnr.New()
	nrB := basenr.New()
	name := "compat"

	files := realFilterLists(t)
	var total, skipped, mismatch, acceptP, acceptB, exceptP, exceptB int
	var samples []string

	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatalf("open %s: %v", filepath.Base(f), err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || ignoreLineRe.MatchString(line) {
				skipped++
				continue
			}
			if strings.HasPrefix(line, "!#include") {
				skipped++
				continue
			}
			total++
			isExP, errP := nrP.ParseRule(line, &name)
			isExB, errB := nrB.ParseRule(line, &name)

			okP := errP == nil
			okB := errB == nil
			if okP {
				acceptP++
			}
			if okB {
				acceptB++
			}
			if okP && isExP {
				exceptP++
			}
			if okB && isExB {
				exceptB++
			}
			if okP != okB || isExP != isExB {
				mismatch++
				if len(samples) < 20 {
					samples = append(samples, fmt.Sprintf("line %d of %s: %q prod={ok=%v ex=%v err=%v} base={ok=%v ex=%v err=%v}",
						total, filepath.Base(f), line, okP, isExP, errP, okB, isExB, errB))
				}
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatalf("scan %s: %v", filepath.Base(f), err)
		}
		fh.Close()
	}

	for _, s := range samples {
		t.Logf("兼容性差异样本: %s", s)
	}
	t.Logf("规则文本兼容性: 总行=%d 跳过(注释/空)=%d 生产接受=%d(异常=%d) 基线接受=%d(异常=%d) 逐行不一致=%d",
		total, skipped, acceptP, exceptP, acceptB, exceptB, mismatch)
	if mismatch > 0 {
		t.Fatalf("规则文本兼容性失败: %d/%d 行不一致", mismatch, total)
	}
	runtime.KeepAlive(nrP)
	runtime.KeepAlive(nrB)
}
