package networkrules_test

// [2026-10-13 重画像探针] 当前基线（v3.1.4-batch4 源码＋#818 后新缓存列表）的
// CPU 热点与堆构成画像。env 门控：ZEN_FRESH_PROFILE=1 才运行，常规套件不受影响。
// 产物目录：ZEN_FRESH_PROFILE_OUT（默认 %TEMP%\zen_fresh_profile）。
// 方法同 测试临时\eff_probe\REPORT.md（2026-10-09），装载骨架同 matchall_bench_test.go。

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irbis-sh/zen-desktop/internal/networkrules"
)

var (
	freshOnce   sync.Once
	freshEngine *networkrules.NetworkRules
	freshErr    error
	freshCorpus []*http.Request
	freshParsed int
	freshLoadMs int64
)

func freshLoad(tb testing.TB) {
	tb.Helper()
	freshOnce.Do(func() {
		filtersDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
		var datas [][]byte
		for _, u := range enabledFilterURLs {
			h := fmt.Sprintf("%x", md5.Sum([]byte(u)))
			p := filepath.Join(filtersDir, h+".cache.txt")
			b, err := os.ReadFile(p)
			if err != nil {
				freshErr = fmt.Errorf("read %s: %w", p, err)
				return
			}
			datas = append(datas, b)
		}
		e := networkrules.New()
		name := "fresh-profile"
		start := time.Now()
		for _, raw := range datas {
			sc := bufio.NewScanner(bytes.NewReader(raw))
			sc.Buffer(nil, 1<<20)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" {
					continue
				}
				if strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
					continue
				}
				if _, err := e.ParseRule(line, &name); err == nil {
					freshParsed++
				}
			}
		}
		e.Compact()
		freshEngine = e
		freshLoadMs = time.Since(start).Milliseconds()

		raw2, err := os.ReadFile(filepath.Join("..", "ruletree", "testdata", "urls.txt"))
		if err != nil {
			freshErr = fmt.Errorf("read urls.txt: %w", err)
			return
		}
		sc := bufio.NewScanner(bytes.NewReader(raw2))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			u := strings.TrimSpace(sc.Text())
			if u == "" {
				continue
			}
			for v := 0; v < 4; v++ {
				pu, err := url.Parse(u)
				if err != nil || pu.Host == "" {
					continue
				}
				req := &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
				switch v {
				case 1:
					req.Header.Set("Sec-Fetch-Site", "none")
					req.Header.Set("Sec-Fetch-Mode", "navigate")
					req.Header.Set("Sec-Fetch-Dest", "document")
					req.Header.Set("Sec-Fetch-User", "?1")
				case 2:
					req.Header.Set("Sec-Fetch-Site", "cross-site")
					req.Header.Set("Sec-Fetch-Mode", "cors")
					req.Header.Set("Sec-Fetch-Dest", "empty")
					req.Header.Set("Referer", "https://www.google.com/")
				case 3:
					req.Header.Set("Sec-Fetch-Site", "same-site")
					req.Header.Set("Sec-Fetch-Mode", "cors")
					req.Header.Set("Sec-Fetch-Dest", "empty")
					req.Header.Set("Cookie", "_ga=GA1.1.123456789.1760000000; Path=/")
				}
				freshCorpus = append(freshCorpus, req)
			}
		}
	})
	if freshErr != nil {
		tb.Skipf("fresh profile data unavailable: %v", freshErr)
	}
}

func TestZZFreshProfile(t *testing.T) {
	if os.Getenv("ZEN_FRESH_PROFILE") != "1" {
		t.Skip("ZEN_FRESH_PROFILE not set")
	}
	outDir := os.Getenv("ZEN_FRESH_PROFILE_OUT")
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "zen_fresh_profile")
	}
	_ = os.MkdirAll(outDir, 0o755)
	freshLoad(t)
	t.Logf("load: parsed=%d loadMs=%d corpus=%d", freshParsed, freshLoadMs, len(freshCorpus))

	runtime.GC()
	runtime.GC()
	hf, err := os.Create(filepath.Join(outDir, "heap_live.pprof"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.WriteHeapProfile(hf); err != nil {
		hf.Close()
		t.Fatal(err)
	}
	hf.Close()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("heap live: HeapAlloc=%dMB HeapSys=%dMB HeapObjects=%d", ms.HeapAlloc/1024/1024, ms.HeapSys/1024/1024, ms.HeapObjects)

	for _, req := range freshCorpus {
		r2 := req.Clone(req.Context())
		_, _, _ = freshEngine.ModifyReq(r2)
	}
	cf, err := os.Create(filepath.Join(outDir, "cpu_modifyreq.pprof"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.StartCPUProfile(cf); err != nil {
		cf.Close()
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		for _, req := range freshCorpus {
			r2 := req.Clone(req.Context())
			_, _, _ = freshEngine.ModifyReq(r2)
		}
	}
	pprof.StopCPUProfile()
	cf.Close()
	t.Logf("cpu profile written: corpus=%d x2 rounds", len(freshCorpus))
}
