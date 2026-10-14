package networkrules

// coverage_measure_test.go — 2026-10-09 多轮内存/效率 A/B 测量（env 门控）。
//
// 测量协议（对齐 memharness 方法论）：
//   - 每轮：构建引擎并**持有引用** → runtime.GC()×2 → HeapAlloc（live 口径）
//   - A/B：dedup ON vs OFF，各 5 轮
//   - 输出：live 堆、装载耗时、去重阶段耗时（covTimeNanos 插桩）、
//     ModifyReq 单请求均值（33k 语料抽样 5000 条）
//
// 触发：ZEN_FILTER_CACHE_DIR 在位 + ZEN_COV_MEASURE=1；
// 结果落盘 ZEN_COV_OUT/measure_ab.txt（缺省不落盘）。

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
	"time"

	"github.com/irbis-sh/zen-desktop/internal/asset"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

type measureResult struct {
	liveHeapMB    float64
	loadMs        float64
	dedupMs       float64
	reqMicroPerOp float64
	storedP       int
	storedE       int
}

func TestCoverageMeasureAB(t *testing.T) {
	if os.Getenv("ZEN_COV_MEASURE") != "1" {
		t.Skip("ZEN_COV_MEASURE=1 才运行（多轮 A/B 测量，高内存）")
	}
	cacheDir := os.Getenv("ZEN_FILTER_CACHE_DIR")
	if cacheDir == "" {
		cacheDir = filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
	}
	if st, err := os.Stat(cacheDir); err != nil || !st.IsDir() {
		t.Skipf("real filter cache not present: %s", cacheDir)
	}

	const rounds = 5
	var resOn, resOff []measureResult
	for i := 0; i < rounds; i++ {
		resOn = append(resOn, measureOne(t, cacheDir, true))
		resOff = append(resOff, measureOne(t, cacheDir, false))
		runtime.GC()
	}

	stat := func(rs []measureResult) (avg, lo, hi float64) {
		lo, hi = rs[0].liveHeapMB, rs[0].liveHeapMB
		for _, r := range rs {
			if r.liveHeapMB < lo {
				lo = r.liveHeapMB
			}
			if r.liveHeapMB > hi {
				hi = r.liveHeapMB
			}
			avg += r.liveHeapMB
		}
		return avg / float64(len(rs)), lo, hi
	}

	onAvg, onLo, onHi := stat(resOn)
	offAvg, offLo, offHi := stat(resOff)

	var b strings.Builder
	fmt.Fprintf(&b, "=== 覆盖去重 A/B 测量（%d 轮 × ON/OFF，生产同构装载）===\n", rounds)
	for i := range resOn {
		fmt.Fprintf(&b, "round %d ON : live=%.1fMB load=%.0fms dedup=%.0fms req=%.2fµs stored=%d+%d\n",
			i, resOn[i].liveHeapMB, resOn[i].loadMs, resOn[i].dedupMs, resOn[i].reqMicroPerOp, resOn[i].storedP, resOn[i].storedE)
	}
	for i := range resOff {
		fmt.Fprintf(&b, "round %d OFF: live=%.1fMB load=%.0fms req=%.2fµs stored=%d+%d\n",
			i, resOff[i].liveHeapMB, resOff[i].loadMs, resOff[i].reqMicroPerOp, resOff[i].storedP, resOff[i].storedE)
	}
	fmt.Fprintf(&b, "live heap : ON %.1fMB(%.1f~%.1f) | OFF %.1fMB(%.1f~%.1f) | 节省 %.1fMB (%.1f%%)\n",
		onAvg, onLo, onHi, offAvg, offLo, offHi, offAvg-onAvg, (offAvg-onAvg)/offAvg*100)
	out := b.String()
	t.Log(out)
	if d := os.Getenv("ZEN_COV_OUT"); d != "" {
		_ = os.WriteFile(filepath.Join(d, "measure_ab.txt"), []byte(out), 0o644)
	}
}

func measureOne(t *testing.T, cacheDir string, dedup bool) measureResult {
	t.Helper()
	var res measureResult

	start := time.Now()
	nr := New(WithCoverageDedup(dedup))
	eng, err := asset.NewEngine("assets.zen.local")
	if err != nil {
		t.Fatalf("asset.NewEngine: %v", err)
	}
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
	loadDone := time.Now()
	nr.Compact()

	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	res.liveHeapMB = float64(m.HeapAlloc) / 1024 / 1024
	res.loadMs = float64(loadDone.Sub(start).Milliseconds())
	res.dedupMs = float64(nr.covTimeNanos.Load()) / 1e6

	// ModifyReq 均值：语料抽样 5000 条。
	urlsPath := filepath.Join("..", "ruletree", "testdata", "urls.txt")
	if f, err := os.Open(urlsPath); err == nil {
		var urls []string
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			u := strings.TrimSpace(sc.Text())
			if strings.Contains(u, "://") {
				urls = append(urls, u)
			}
		}
		f.Close()
		if len(urls) > 5000 {
			urls = urls[:5000]
		}
		t0 := time.Now()
		n := 0
		for _, u := range urls {
			req, err := http.NewRequest(http.MethodGet, u, nil)
			if err != nil {
				continue
			}
			_, _, _ = nr.ModifyReq(req)
			n++
		}
		if n > 0 {
			res.reqMicroPerOp = float64(time.Since(t0).Microseconds()) / float64(n)
		}
	}

	nr.primaryStore.walkValues(func(*rule.Rule) { res.storedP++ })
	nr.exceptionStore.walkValues(func(*exceptionrule.ExceptionRule) { res.storedE++ })
	return res
}
