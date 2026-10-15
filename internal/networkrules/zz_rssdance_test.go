package networkrules

// [2026-10-15 RSS dance 微尖峰 v2·D7 前提验证（指标修正版）]
//
// v1 教训（本轮自纠）：
//   ① e1 在首次 GC 前无读取点 → Go 按 liveness 判死，引擎被回收（live=0.8MB 伪影）
//     → 修复：runtime.KeepAlive 钉活到测量点之后。
//   ② HeapSys 是"从不缩水"的保留地址空间高水位，不能衡量"还给 OS"——
//     R4/R5 以 heapSys 不变为"span 零返还"结论的口径需要复核。
//     正确指标：HeapIdle（空闲未用 span）与 HeapReleased（已归还 OS 的字节）。
//
// 四阶段（全部报 live/Inuse/Idle/Released/工作集）：
//   1) 装载 engine1 → KeepAlive → GC×2 → FreeOSMemory —— 碎片基线
//   2) 丢弃 engine1 → GC×2 → FreeOSMemory —— 死 span 是否真被归还（前提验证）
//   3) 重载 engine2 —— 全新顺序装载的密度（Idle−Released 余量）
//   4) 再丢弃 → 还原 —— 终态
// env 门控：ZEN_RSS_DANCE=1。零生产影响。

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func danceLoad(tb testing.TB) *NetworkRules {
	tb.Helper()
	filtersDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
	var datas [][]byte
	for _, u := range taxFilterURLs {
		h := fmt.Sprintf("%x", md5.Sum([]byte(u)))
		p := filepath.Join(filtersDir, h+".cache.txt")
		b, err := os.ReadFile(p)
		if err != nil {
			tb.Fatalf("read %s: %v", p, err)
		}
		datas = append(datas, b)
	}
	e := New()
	name := "rss-dance"
	start := time.Now()
	parsed := 0
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
				parsed++
			}
		}
	}
	e.Compact()
	tb.Logf("dance load: parsed=%d ms=%d", parsed, time.Since(start).Milliseconds())
	return e
}

func danceStats(tb testing.TB, tag string) {
	tb.Helper()
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	tb.Logf("[%s] live=%.1f Inuse=%.1f Idle=%.1f Released=%.1f HeapSys=%.1f",
		tag,
		float64(ms.HeapAlloc)/1048576,
		float64(ms.HeapInuse)/1048576,
		float64(ms.HeapIdle)/1048576,
		float64(ms.HeapReleased)/1048576,
		float64(ms.HeapSys)/1048576)
}

func TestZZRSSDance(t *testing.T) {
	if os.Getenv("ZEN_RSS_DANCE") != "1" {
		t.Skip("ZEN_RSS_DANCE not set")
	}

	// 阶段 1：装载基线（KeepAlive 钉活到测量后）
	e1 := danceLoad(t)
	runtime.KeepAlive(e1)
	danceStats(t, "1装后")
	debug.FreeOSMemory()
	danceStats(t, "1FreeOS后")

	// 阶段 2：整体丢弃 → 死 span 是否真归还 OS（D7 前提）
	runtime.KeepAlive(e1) // 读引用保证上面的统计期间 e1 存活
	e1 = nil
	runtime.GC()
	runtime.GC()
	debug.FreeOSMemory()
	danceStats(t, "2弃后(关键)")

	// 阶段 3：全新顺序装载（密度对比）
	e2 := danceLoad(t)
	runtime.KeepAlive(e2)
	danceStats(t, "3重载后")
	debug.FreeOSMemory()
	danceStats(t, "3FreeOS后")

	// 阶段 4：终态还原
	runtime.KeepAlive(e2)
	e2 = nil
	runtime.GC()
	runtime.GC()
	debug.FreeOSMemory()
	danceStats(t, "4终态")
}
