package networkrules

// [2026-10-15 通配-only segmatch 定向 spike]（用户裁决解除 v2 禁令后首轮实测）
//
// 假设：通配类（含 * 的根类模式）以 3.2% 模式数占 wildcardTail 26.5% CPU——
// 把"根类通配"（非 | 锚、含 *）出树进 segmatch DAT（语义资产：对拍 1,966 万对=0），
// 枚举桶归零，净 CPU 显著为正；v2 失败根因是把 24 万条 F 类一起搬进去。
//
// 方法（同进程 A/B，无生产改动）：
//   engineB = 原样装载（基线树）
//   engineV = 同源装载 → 根类通配值从两树 prune → segmatch DAT 接管
//   ①差分门：urls.txt 全语料 + 通配自证 URL 全量，双方 GetHosts 结果
//     （RawRule 文本多重集）必须逐 URL 全等——违反=0 才有资格谈收益
//   ②交错计时：GOMAXPROCS(1)，4 轮交替 baseline/variant
//   ③内存：两态 HeapAlloc 差分（含 DAT 成本）
// env 门控：ZEN_WILDSPLIT=1。

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
	"github.com/irbis-sh/zen-desktop/segmatch"
)

type wildSplitSide struct {
	wildPats []string            // 去重后的根类通配 pattern（DAT 输入）
	patVals  map[string][]string // pattern → 各值 RawRule（保插入序）
	dat      *segmatch.DATIndex
	buildMs  int64
}

func collectWildSide(tb testing.TB, e *NetworkRules, side string, which string) *wildSplitSide {
	tb.Helper()
	s := &wildSplitSide{patVals: map[string][]string{}}
	seen := map[string]bool{}
	if which == "P" {
		e.primaryStore.tree.Walk(func(r *rule.Rule) {
			pat, _ := parseRuleParts(r.RawRule)
			if strings.Contains(pat, "*") && !strings.ContainsAny(pat, "|") {
				if !seen[pat] {
					seen[pat] = true
					s.wildPats = append(s.wildPats, pat)
				}
				s.patVals[pat] = append(s.patVals[pat], r.RawRule)
			}
		})
	} else {
		e.exceptionStore.tree.Walk(func(er *exceptionrule.ExceptionRule) {
			pat, _ := parseRuleParts(strings.TrimPrefix(er.RawRule, "@@"))
			if strings.Contains(pat, "*") && !strings.ContainsAny(pat, "|") {
				if !seen[pat] {
					seen[pat] = true
					s.wildPats = append(s.wildPats, pat)
				}
				s.patVals[pat] = append(s.patVals[pat], er.RawRule)
			}
		})
	}
	start := time.Now()
	if len(s.wildPats) > 0 {
		s.dat = segmatch.NewDATIndex(s.wildPats)
	}
	s.buildMs = time.Since(start).Milliseconds()
	tb.Logf("[%s]根类通配 pattern=%d DAT构建=%dms", side, len(s.wildPats), s.buildMs)
	return s
}

// pruneWild 从两树移除根类通配值（DAT 已接管）
func pruneWild(e *NetworkRules) int {
	wild := map[string]bool{}
	count := func(pat string) {
		if strings.Contains(pat, "*") && !strings.ContainsAny(pat, "|") {
			wild[pat] = true
		}
	}
	e.primaryStore.tree.Walk(func(r *rule.Rule) {
		pat, _ := parseRuleParts(r.RawRule)
		count(pat)
	})
	e.exceptionStore.tree.Walk(func(er *exceptionrule.ExceptionRule) {
		pat, _ := parseRuleParts(strings.TrimPrefix(er.RawRule, "@@"))
		count(pat)
	})
	rm := e.primaryStore.pruneValues(func(r *rule.Rule) bool {
		pat, _ := parseRuleParts(r.RawRule)
		return wild[pat]
	})
	rm += e.exceptionStore.pruneValues(func(er *exceptionrule.ExceptionRule) bool {
		pat, _ := parseRuleParts(strings.TrimPrefix(er.RawRule, "@@"))
		return wild[pat]
	})
	return rm
}

func TestZZWildSplit(t *testing.T) {
	if os.Getenv("ZEN_WILDSPLIT") != "1" {
		t.Skip("ZEN_WILDSPLIT not set")
	}

	// ---- engineB（基线）----
	taxLoad(t)
	engB := taxEngine

	// ---- engineV（变体：prune + DAT）----
	filtersDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
	loadEngine := func() *NetworkRules {
		datas := [][]byte{}
		for _, u := range taxFilterURLs {
			h := fmt.Sprintf("%x", md5.Sum([]byte(u)))
			b, err := os.ReadFile(filepath.Join(filtersDir, h+".cache.txt"))
			if err != nil {
				t.Fatalf("read cache: %v", err)
			}
			datas = append(datas, b)
		}
		e := New()
		name := "wildsplit"
		for _, raw := range datas {
			sc := bufio.NewScanner(strings.NewReader(string(raw)))
			sc.Buffer(nil, 1<<20)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
					continue
				}
				_, _ = e.ParseRule(line, &name)
			}
		}
		e.Compact()
		return e
	}
	engV := loadEngine()
	sideP := collectWildSide(t, engV, "P树根类通配", "P")
	sideE := collectWildSide(t, engV, "E树根类通配", "E")
	removed := pruneWild(engV)
	t.Logf("[prune]移除值=%d", removed)

	// ---- 匹配函数（生产口径：primary 带 eff=own host 无 Referer 回退，exception nil）----
	baseGet := func(url string, host string) []string {
		var out []string
		for _, r := range engB.primaryStore.GetHosts(url, nil) {
			out = append(out, r.RawRule)
		}
		for _, er := range engB.exceptionStore.GetHosts(url, nil) {
			out = append(out, er.RawRule)
		}
		sort.Strings(out)
		return out
	}
	varGet := func(url string, host string) []string {
		var out []string
		for _, r := range engV.primaryStore.GetHosts(url, nil) {
			out = append(out, r.RawRule)
		}
		for _, pi := range sideP.dat.MatchAll(url) {
			out = append(out, sideP.patVals[sideP.wildPats[pi]]...)
		}
		for _, er := range engV.exceptionStore.GetHosts(url, nil) {
			out = append(out, er.RawRule)
		}
		for _, pi := range sideE.dat.MatchAll(url) {
			out = append(out, sideE.patVals[sideE.wildPats[pi]]...)
		}
		sort.Strings(out)
		return out
	}

	// ---- ①差分门：urls.txt 全语料 ----
	old := runtime.GOMAXPROCS(0)
	runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	viol := 0
	start := time.Now()
	for _, u := range taxCorpus {
		a := baseGet(u, hostOf(u))
		b := varGet(u, hostOf(u))
		if len(a) != len(b) {
			viol++
			if viol <= 5 {
				onlyA, onlyB := diffLists(a, b)
				t.Logf("[差分]len不等 %s: base=%d var=%d | 仅base=%d条 %v | 仅var=%d条 %v",
					u, len(a), len(b), len(onlyA), headStr(onlyA, 3), len(onlyB), headStr(onlyB, 3))
			}
			continue
		}
		for j := range a {
			if a[j] != b[j] {
				viol++
				if viol <= 5 {
					t.Logf("[差分]元素不等 %s: %q vs %q", u, a[j], b[j])
				}
				break
			}
		}
	}
	t.Logf("[差分门]urls.txt: N=%d 违反=%d 耗时=%.1fs", len(taxCorpus), viol, time.Since(start).Seconds())

	// ---- 差分门②：通配自证 URL 全量 ----
	sc2 := 0
	for pat, vals := range sideP.patVals {
		u, ok := coldSelfCertURL(pat)
		if !ok {
			continue
		}
		sc2++
		a := baseGet(u, hostOf(u))
		b := varGet(u, hostOf(u))
		want := append([]string{}, vals...)
		sort.Strings(want)
		if len(a) != len(b) || !eqStr(a, b) || !containsAll(b, want) {
			viol++
			if viol <= 10 {
				t.Logf("[自证差分]pat=%q base=%d var=%d", pat, len(a), len(b))
			}
		}
	}
	t.Logf("[差分门]自证URL: N=%d 累计违反=%d", sc2, viol)

	// ---- ②分块计时：GC 前置对齐，块内 3 轮，取最小轮（GC 干扰最小）----
	for _, side := range []string{"baseline", "variant"} {
		runtime.GC()
		get := baseGet
		if side == "variant" {
			get = varGet
		}
		var best time.Duration
		for round := 0; round < 3; round++ {
			t0 := time.Now()
			for _, u := range taxCorpus {
				get(u, hostOf(u))
			}
			d := time.Since(t0)
			if round == 0 || d < best {
				best = d
			}
			t.Logf("[A/B %s R%d] %.2fµs/URL", side, round, float64(d.Microseconds())/float64(len(taxCorpus)))
		}
		if side == "baseline" {
			t.Logf("[A/B结论基准] baseline min=%.2fµs/URL", float64(best.Microseconds())/float64(len(taxCorpus)))
		} else {
			t.Logf("[A/B结论] variant min=%.2fµs/URL", float64(best.Microseconds())/float64(len(taxCorpus)))
		}
	}

	// ---- ③内存差分 ----
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("[内存]双引擎共存 HeapAlloc=%dMB（DAT+修剪净差分见装载各自口径）", ms.HeapAlloc/1048576)
}

func diffLists(a, b []string) (onlyA, onlyB []string) {
	setA, setB := map[string]int{}, map[string]int{}
	for _, s := range a {
		setA[s]++
	}
	for _, s := range b {
		setB[s]++
	}
	for _, s := range a {
		if setB[s] > 0 {
			setB[s]--
		} else {
			onlyA = append(onlyA, s)
		}
	}
	for _, s := range b {
		if setA[s] > 0 {
			setA[s]--
		} else {
			onlyB = append(onlyB, s)
		}
	}
	return
}

func headStr(l []string, n int) []string {
	if len(l) > n {
		return l[:n]
	}
	return l
}

func hostOf(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if j := strings.IndexAny(rest, "/?:#"); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	return u
}

func eqStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsAll(hay, want []string) bool {
	set := map[string]int{}
	for _, s := range hay {
		set[s]++
	}
	for _, s := range want {
		if set[s] == 0 {
			return false
		}
		set[s]--
	}
	return true
}
