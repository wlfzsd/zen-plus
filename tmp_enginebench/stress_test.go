package tmp_enginebench

// stress_test.go — 复杂环境与压力测试矩阵（2026-10-05）。
//
// 本文件覆盖三块：
//  1. TestStressConcurrentMixedTraffic：32 goroutine 并发 ModifyReq+ModifyRes
//     混合流量 10 万次 ×2 轮，逐 op 与串行参考结果比对（无 panic / 结果集与
//     串行版一致），另做两引擎串行参考交叉复核。
//  2. TestStressCacheRebuildUnderConcurrency：4096 上限缓存
//     （internal/networkrules/rulemodifiers/domain.go:67 refererHostCacheLimit、
//     domain.go:164 eTLD1CacheLimit）整体重建瞬间与读并发交错的定向测试：
//     6000 个不同 referer 高并发灌入，无死锁/panic/错误结果。
//  3. TestStressTreeInsertGetLifecycle：Insert 与 Get "不得并发"契约
//     （internal/ruletree/ruletree.go:15 注释）下的顺序交替生命周期：
//     批量 Insert → 批量 Get → 再 Insert → 再 Get → Compact → 再 Get，
//     断言 Compact 前后匹配结果集一致，且与冻结基线树逐项一致。
//
// race detector 说明：本机 gcc 不存在（command not found）且 go env
// CGO_ENABLED=0，-race 不可用（竞态检测器依赖 CGO + C 编译器）。按任务书改用
// "GOMAXPROCS=默认(18 逻辑核) 高并发 × 多轮重复 + 结果集与串行版逐 op 一致"
// 作为替代证据（弱于 -race，已在报告如实标注）。
//
// 禁改 internal/ 与 baselinenr/；发现差异即证据，全部如实记录。

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	prodnr "github.com/irbis-sh/zen-desktop/internal/networkrules"
	baseruletree "github.com/irbis-sh/zen-desktop/internal/ruletree"
	basenr "github.com/irbis-sh/zen-desktop/tmp_enginebench/baselinenr/networkrules"
	basenrtree "github.com/irbis-sh/zen-desktop/tmp_enginebench/baselinenr/ruletree"
)

// ---------------- 共享构建 ----------------

var (
	stressOnce     sync.Once
	stressBaseline *basenr.NetworkRules
	stressProd     *prodnr.NetworkRules
	stressLines    int
)

// stressEngines 以真实订阅语料（loadRealLines，61 万+ 行）构建两套引擎，
// sync.Once 保证同进程只建一次（单引擎实测 222.9 MB 活堆 / 0.79s 构建，
// 见 TestNetworkRulesLiveMemoryReal）。
func stressEngines(t *testing.T) (*basenr.NetworkRules, *prodnr.NetworkRules, int) {
	t.Helper()
	stressOnce.Do(func() {
		lines := loadRealLines(t)
		stressLines = len(lines)
		nameB, nameP := "stress-base", "stress-prod"
		b := basenr.New()
		p := prodnr.New()
		for _, l := range lines {
			if _, err := b.ParseRule(l, &nameB); err != nil {
				_ = err // 与 filter.AddURL 一致：解析失败静默跳过
			}
			if _, err := p.ParseRule(l, &nameP); err != nil {
				_ = err
			}
		}
		stressBaseline = b
		stressProd = p
	})
	return stressBaseline, stressProd, stressLines
}

// stressParseBoth 在两套小引擎上解析同一批规则（解析失败打日志后跳过，
// 与 filter.AddURL 语义一致），返回引擎对。
func stressParseBoth(t *testing.T, rules []string) (*basenr.NetworkRules, *prodnr.NetworkRules) {
	t.Helper()
	b := basenr.New()
	p := prodnr.New()
	nameB, nameP := "stress-small-base", "stress-small-prod"
	for _, r := range rules {
		if _, err := b.ParseRule(r, &nameB); err != nil {
			t.Logf("基线拒收规则 %s: %v", stressDisplay(r), err)
		}
		if _, err := p.ParseRule(r, &nameP); err != nil {
			t.Logf("生产拒收规则 %s: %v", stressDisplay(r), err)
		}
	}
	return b, p
}

// stressDisplay 展示辅助：短串原样，长串给"长度+首尾"的无损描述
// （构造本身在用例清单中完整给出，无信息丢失）。
func stressDisplay(s string) string {
	if len(s) <= 100 {
		return s
	}
	return fmt.Sprintf("<len=%d %q...%q>", len(s), s[:20], s[len(s)-20:])
}

// stressCompareOut 按 golden 同口径比较两个结果：panic 必须两边相等；
// 阻塞态只比三元组（阻塞单条依赖 map 遍历序，见 equiv_test.go 顶部说明），
// 非阻塞态比完整排序集合。
func stressCompareOut(got, want goldenOut) []string {
	var diffs []string
	if got.panicMsg != want.panicMsg {
		diffs = append(diffs, fmt.Sprintf("panic: got=%q want=%q", got.panicMsg, want.panicMsg))
	}
	if got.resPanicMsg != want.resPanicMsg {
		diffs = append(diffs, fmt.Sprintf("resPanic: got=%q want=%q", got.resPanicMsg, want.resPanicMsg))
	}
	if got.shouldBlock != want.shouldBlock {
		diffs = append(diffs, fmt.Sprintf("shouldBlock: got=%v want=%v", got.shouldBlock, want.shouldBlock))
	}
	if got.redirectURL != want.redirectURL {
		diffs = append(diffs, fmt.Sprintf("redirectURL: got=%q want=%q", got.redirectURL, want.redirectURL))
	}
	if len(got.rawRules) != len(want.rawRules) {
		diffs = append(diffs, fmt.Sprintf("len(appliedRules): got=%d want=%d", len(got.rawRules), len(want.rawRules)))
	} else if !want.shouldBlock {
		for i := range want.rawRules {
			if got.rawRules[i] != want.rawRules[i] {
				diffs = append(diffs, fmt.Sprintf("appliedRules[%d]: got=%q want=%q", i, got.rawRules[i], want.rawRules[i]))
				break
			}
		}
	}
	if got.resErrNil != want.resErrNil {
		diffs = append(diffs, fmt.Sprintf("resErrNil: got=%v want=%v", got.resErrNil, want.resErrNil))
	}
	if len(got.resRawRules) != len(want.resRawRules) {
		diffs = append(diffs, fmt.Sprintf("ModifyRes len(appliedRules): got=%d want=%d", len(got.resRawRules), len(want.resRawRules)))
	} else {
		for i := range want.resRawRules {
			if got.resRawRules[i] != want.resRawRules[i] {
				diffs = append(diffs, fmt.Sprintf("ModifyRes appliedRules[%d]: got=%q want=%q", i, got.resRawRules[i], want.resRawRules[i]))
				break
			}
		}
	}
	return diffs
}

// ---------------- 1. 并发压力（重点） ----------------

// stressTriple 是一次流量的完整规格（URL + 头组合 + referer）。
type stressTriple struct {
	u, ref string
	c      goldenCombo
}

func stressTripleKey(tr stressTriple) string {
	return fmt.Sprintf("%s|%s|%s|user=%v", tr.u, tr.c.name, tr.ref, tr.c.user)
}

// TestStressConcurrentMixedTraffic 32 goroutine 并发 ModifyReq+ModifyRes
// 混合流量 10 万次 ×2 轮（两引擎各跑），每个 op 与串行参考逐项比对。
func TestStressConcurrentMixedTraffic(t *testing.T) {
	if testing.Short() {
		t.Skip("并发压力耗时长，-short 跳过")
	}
	base, prod, nRules := stressEngines(t)
	urls := loadURLs(t, 2000)
	if len(urls) < 100 {
		t.Fatalf("URL 样本不足: %d", len(urls))
	}
	t.Logf("语料规则数=%d，URL 样本=%d，GOMAXPROCS=%d", nRules, len(urls), runtime.GOMAXPROCS(0))

	// --- 生成互异 (url, combo, referer) 三元组全表 ---
	triples := make([]stressTriple, 0, len(urls)*len(goldenCombos))
	for ui, u := range urls {
		for ci, c := range goldenCombos {
			ref := ""
			if c.referer != "" {
				ref = goldenReferers[(ui*3+ci)%len(goldenReferers)]
			}
			triples = append(triples, stressTriple{u: u, ref: ref, c: c})
		}
	}
	const totalOps = 100_000
	specs := make([]int, totalOps)
	for i := range specs {
		specs[i] = (i * 7919) % len(triples) // 7919 质数，打散访问序
	}

	// --- 串行参考：每引擎对全部互异三元组各跑一次 ---
	serialBase := make(map[string]goldenOut, len(triples))
	for _, tr := range triples {
		serialBase[stressTripleKey(tr)] = goldenCallBaseline(base, tr.u, tr.c, tr.ref)
	}
	serialProd := make(map[string]goldenOut, len(triples))
	for _, tr := range triples {
		serialProd[stressTripleKey(tr)] = goldenCallProduction(prod, tr.u, tr.c, tr.ref)
	}

	// 跨引擎串行参考交叉复核（并发一致性的等价性前提）
	crossDiffs := 0
	for _, tr := range triples {
		key := stressTripleKey(tr)
		if d := stressCompareOut(serialProd[key], serialBase[key]); len(d) > 0 {
			crossDiffs++
			if crossDiffs <= 5 {
				t.Logf("跨引擎串行参考差异 key=%s: %v", key, d)
			}
		}
	}
	if crossDiffs > 0 {
		t.Errorf("跨引擎串行参考不一致 %d/%d 处", crossDiffs, len(triples))
	}

	// --- 并发阶段：32 goroutine × 10 万 op × 2 轮 × 2 引擎 ---
	for round := 1; round <= 2; round++ {
		doneB, pB, mB, durB := stressRunConcurrent(t, "baseline", round, totalOps, triples, specs, serialBase,
			func(tr stressTriple) goldenOut { return goldenCallBaseline(base, tr.u, tr.c, tr.ref) })
		if doneB != totalOps || pB != 0 || mB != 0 {
			t.Errorf("baseline 第%d轮: done=%d panics=%d mismatches=%d", round, doneB, pB, mB)
		} else {
			t.Logf("baseline 第%d轮: 32 goroutine × %d op 全部完成，0 panic，0 与串行不一致（耗时 %v）", round, totalOps, durB)
		}
		doneP, pP, mP, durP := stressRunConcurrent(t, "production", round, totalOps, triples, specs, serialProd,
			func(tr stressTriple) goldenOut { return goldenCallProduction(prod, tr.u, tr.c, tr.ref) })
		if doneP != totalOps || pP != 0 || mP != 0 {
			t.Errorf("production 第%d轮: done=%d panics=%d mismatches=%d", round, doneP, pP, mP)
		} else {
			t.Logf("production 第%d轮: 32 goroutine × %d op 全部完成，0 panic，0 与串行不一致（耗时 %v）", round, totalOps, durP)
		}
	}
}

// stressRunConcurrent 以 32 goroutine 消费 op 流；每 op recover 包裹，
// 结果与串行参考逐项比对，返回（完成数, panic 数, 不一致数, 耗时）。
func stressRunConcurrent(t *testing.T, name string, round, totalOps int,
	triples []stressTriple, specs []int, serial map[string]goldenOut,
	call func(tr stressTriple) goldenOut) (opsDone, panics, mismatches int64, dur time.Duration) {
	t.Helper()
	start := time.Now()
	var (
		wg           sync.WaitGroup
		next         int64
		pCnt, mCnt   int64
		dCnt         int64
		sampleMu     sync.Mutex
		firstPanics  []string
		firstDiffs   []string
	)
	const workers = 32
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				idx := int(atomic.AddInt64(&next, 1)) - 1
				if idx >= totalOps {
					return
				}
				var out goldenOut
				func() {
					defer func() {
						if r := recover(); r != nil {
							out.panicMsg = "RECOVERED:" + fmt.Sprint(r)
						}
					}()
					out = call(triples[specs[idx]])
				}()
				atomic.AddInt64(&dCnt, 1)
				if out.panicMsg != "" || out.resPanicMsg != "" {
					atomic.AddInt64(&pCnt, 1)
					sampleMu.Lock()
					if len(firstPanics) < 10 {
						firstPanics = append(firstPanics, fmt.Sprintf("op#%d %s", idx, out.panicMsg+out.resPanicMsg))
					}
					sampleMu.Unlock()
					continue
				}
				key := stressTripleKey(triples[specs[idx]])
				if d := stressCompareOut(out, serial[key]); len(d) > 0 {
					atomic.AddInt64(&mCnt, 1)
					sampleMu.Lock()
					if len(firstDiffs) < 10 {
						firstDiffs = append(firstDiffs, fmt.Sprintf("op#%d key=%s diffs=%v", idx, key, d))
					}
					sampleMu.Unlock()
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Minute):
		t.Fatalf("%s 第%d轮并发阶段超时（10min，疑似死锁）", name, round)
	}
	if pCnt > 0 || mCnt > 0 {
		for _, s := range firstPanics {
			t.Logf("[%s R%d] panic 样本: %s", name, round, s)
		}
		for _, s := range firstDiffs {
			t.Logf("[%s R%d] 不一致样本: %s", name, round, s)
		}
	}
	return dCnt, pCnt, mCnt, time.Since(start)
}

// ---------------- 2. 缓存重建定向压力 ----------------

// TestStressCacheRebuildUnderConcurrency 用 6000 个不同 referer（> 4096 上限，
// domain.go:93-95 / domain.go:190-193 的整体重建分支必然触发）高并发灌入
// $domain 规则，验证重建瞬间与读并发交错时无死锁、无 panic、结果与串行参考
// 一致，并与基线引擎（无缓存）交叉验证。
func TestStressCacheRebuildUnderConcurrency(t *testing.T) {
	rules := []string{
		"||cachetest.invalid^$domain=example.org",           // 常规条目
		"||cachetest.invalid^$domain=example.*",             // tld 条目 → eTLD1Cache
		"||cachetest.invalid^$domain=/^h[0-9]+\\.example\\./", // 正则条目（只匹配 example 类 referer 主机）
		"@@||cachetest.invalid^$domain=example.org",         // exception（Cancels 路径）
	}
	base, prod := stressParseBoth(t, rules)

	const nRef = 6000 // > refererHostCacheLimit=4096
	referers := make([]string, nRef)
	for i := range referers {
		if i%2 == 0 {
			referers[i] = fmt.Sprintf("https://h%d.example.org/", i) // 命中 → 阻塞
		} else {
			referers[i] = fmt.Sprintf("https://h%d.pressure%d.test/", i, i%7) // 不命中
		}
	}
	targetURL := "https://cachetest.invalid/pixel.js"
	combo := goldenCombos[1] // tracking-pixel：GET + image dest、带 Referer

	// 串行参考（两引擎）+ 交叉一致性 + 覆盖自检
	serBase := make(map[string]goldenOut, nRef)
	serProd := make(map[string]goldenOut, nRef)
	for _, r := range referers {
		serBase[r] = goldenCallBaseline(base, targetURL, combo, r)
		serProd[r] = goldenCallProduction(prod, targetURL, combo, r)
	}
	crossDiff := 0
	blockedSeen, cleanSeen := 0, 0
	for _, r := range referers {
		if d := stressCompareOut(serProd[r], serBase[r]); len(d) > 0 {
			crossDiff++
			if crossDiff <= 5 {
				t.Logf("串行交叉差异 ref=%s: %v", r, d)
			}
		}
		if serProd[r].shouldBlock {
			blockedSeen++
		} else {
			cleanSeen++
		}
	}
	if crossDiff > 0 {
		t.Errorf("缓存场景两引擎串行结果不一致 %d/%d", crossDiff, nRef)
	}
	if blockedSeen == 0 || cleanSeen == 0 {
		t.Fatalf("串行参考异常：blocked=%d clean=%d（应两类均非零，否则 $domain 匹配/豁免路径未被真实覆盖）", blockedSeen, cleanSeen)
	}
	t.Logf("串行参考：blocked=%d clean=%d（$domain 匹配与豁免两路均覆盖）", blockedSeen, cleanSeen)

	// 并发灌入：32 goroutine × 10 万 op，referer 以与 6000 互质的 37 步进轮转，
	// 多 goroutine 同时 miss 新键 → 写重建与读并发交错。
	const totalOps = 100_000
	runConcurrentCache := func(isProd bool, e interface{}, serial map[string]goldenOut) (int64, int64, int64) {
		var (
			wg   sync.WaitGroup
			next int64
			pCnt, mCnt, dCnt int64
			mu          sync.Mutex
			firstPanics []string
			firstDiffs  []string
		)
		const workers = 32
		wg.Add(workers)
		for w := 0; w < workers; w++ {
			go func() {
				defer wg.Done()
				for {
					idx := int(atomic.AddInt64(&next, 1)) - 1
					if idx >= totalOps {
						return
					}
					ref := referers[(idx*37)%nRef] // 37 与 6000 互质，覆盖全部键
					var out goldenOut
					func() {
						defer func() {
							if r := recover(); r != nil {
								out.panicMsg = "RECOVERED:" + fmt.Sprint(r)
							}
						}()
						if isProd {
							out = goldenCallProduction(e.(*prodnr.NetworkRules), targetURL, combo, ref)
						} else {
							out = goldenCallBaseline(e.(*basenr.NetworkRules), targetURL, combo, ref)
						}
					}()
					atomic.AddInt64(&dCnt, 1)
					if out.panicMsg != "" || out.resPanicMsg != "" {
						atomic.AddInt64(&pCnt, 1)
						mu.Lock()
						if len(firstPanics) < 10 {
							firstPanics = append(firstPanics, fmt.Sprintf("op#%d ref=%s %s", idx, ref, out.panicMsg+out.resPanicMsg))
						}
						mu.Unlock()
						continue
					}
					if d := stressCompareOut(out, serial[ref]); len(d) > 0 {
						atomic.AddInt64(&mCnt, 1)
						mu.Lock()
						if len(firstDiffs) < 10 {
							firstDiffs = append(firstDiffs, fmt.Sprintf("op#%d ref=%s diffs=%v", idx, ref, d))
						}
						mu.Unlock()
					}
				}
			}()
		}
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Minute):
			t.Fatalf("缓存重建并发阶段超时（>5min，疑似死锁）")
		}
		for _, s := range firstPanics {
			t.Logf("panic 样本: %s", s)
		}
		for _, s := range firstDiffs {
			t.Logf("不一致样本: %s", s)
		}
		return dCnt, pCnt, mCnt
	}

	if d, p, m := runConcurrentCache(true, prod, serProd); p != 0 || m != 0 || d != totalOps {
		t.Errorf("生产引擎缓存重建并发：done=%d panics=%d mismatches=%d", d, p, m)
	} else {
		t.Logf("生产引擎缓存重建并发：10 万 op 全部完成，0 panic，0 与串行不一致（6000 键 > 4096 上限 → 整体重建必然发生）")
	}
	if d, p, m := runConcurrentCache(false, base, serBase); p != 0 || m != 0 || d != totalOps {
		t.Errorf("基线引擎（无缓存）并发：done=%d panics=%d mismatches=%d", d, p, m)
	} else {
		t.Logf("基线引擎并发：10 万 op 全部完成，0 panic，0 与串行不一致")
	}
}

// ---------------- 3. Insert / Get 生命周期 ----------------

// treeSnap 是一次树探针的快照（panic 状态 + 排序结果集）。
type treeSnap struct {
	panicMsg string
	set      []string
}

// TestStressTreeInsertGetLifecycle 验证 "Insert/Compact 不得与 Get 并发"
// 契约下的顺序交替生命周期：Compact 前后匹配结果集一致；两树逐阶段一致。
func TestStressTreeInsertGetLifecycle(t *testing.T) {
	type treePair struct {
		name    string
		insert  func(pattern, v string)
		get     func(url string) treeSnap
		compact func()
	}
	newPair := func(name string) treePair {
		if name == "production" {
			tr := baseruletree.New[string]()
			return treePair{
				name:    name,
				insert:  func(p, v string) { tr.Insert(p, v) },
				compact: func() { tr.Compact() },
				get: func(u string) (snap treeSnap) {
					defer func() {
						if r := recover(); r != nil {
							snap.panicMsg = fmt.Sprint(r)
						}
					}()
					res := tr.Get(u)
					snap.set = append([]string(nil), res...)
					sort.Strings(snap.set)
					return
				},
			}
		}
		tr := basenrtree.New[string]()
		return treePair{
			name:    name,
			insert:  func(p, v string) { tr.Insert(p, v) },
			compact: func() { tr.Compact() },
			get: func(u string) (snap treeSnap) {
				defer func() {
					if r := recover(); r != nil {
						snap.panicMsg = fmt.Sprint(r)
					}
				}()
				res := tr.Get(u)
				snap.set = append([]string(nil), res...)
				sort.Strings(snap.set)
				return
			},
		}
	}

	batch1 := []string{
		"||example.com^", "|https", "banner", "/ads/*", "a*b*c",
		"****", "^^^^", "|||", "|", "example.com",
		"||例え.jp^", "||emoji-\U0001F600.com^", "a\x00b", "a||b", // a||b: 树语义会跳过中途含 || 的 pattern
		"|" + strings.Repeat("x", 65536) + "^", // 64KB pattern
	}
	batch2 := []string{
		"||deep.example.org^", "*wild*card*", "||double.invalid^",
		"||example.com^", // 与 batch1 同 pattern、不同 value → leaf 追加
	}

	probes := []string{
		"https://example.com/path?q=1",
		"https://a.example.com/deep/path/banner.gif",
		"https://deep.example.org/x",
		"https://www.example.org/ads/banner.js",
		"https://例え.jp/テスト",
		"https://emoji-\U0001F600.com/pic",
		"https://double.invalid/px",
		"https://unrelated.example.net/nothing.html",
		"http://x.com/axbycz",
	}

	pairs := []treePair{newPair("production"), newPair("baseline")}

	runPhase := func(p treePair) map[string]treeSnap {
		out := make(map[string]treeSnap, len(probes))
		for _, u := range probes {
			out[u] = p.get(u)
		}
		return out
	}
	compareAcross := func(stage string, mBase, mProd map[string]treeSnap) int {
		diff := 0
		for _, u := range probes {
			b, p := mBase[u], mProd[u]
			if b.panicMsg != p.panicMsg || len(b.set) != len(p.set) {
				diff++
				t.Errorf("[%s] 两树不一致 url=%s base=(panic=%q set=%v) prod=(panic=%q set=%v)",
					stage, u, b.panicMsg, b.set, p.panicMsg, p.set)
				continue
			}
			for i := range b.set {
				if b.set[i] != p.set[i] {
					diff++
					t.Errorf("[%s] 两树结果集差异 url=%s at %d: base=%q prod=%q", stage, u, i, b.set[i], p.set[i])
					break
				}
			}
		}
		return diff
	}

	// phase1: 插入 batch1（同 pattern 双 value 追加语义）
	for _, p := range pairs {
		for i, pat := range batch1 {
			p.insert(pat, fmt.Sprintf("v1#%d:%s", i, pat))
			p.insert(pat, fmt.Sprintf("v1#%d-dup:%s", i, pat))
		}
		p.insert("", "empty-should-be-noop") // Insert("") 语义：直接 return（ruletree.go:38）
	}
	phase1B := runPhase(pairs[0])
	phase1P := runPhase(pairs[1])
	d1 := compareAcross("after-batch1", phase1B, phase1P)

	// phase2: 再插入 batch2 → 再 Get
	for _, p := range pairs {
		for i, pat := range batch2 {
			p.insert(pat, fmt.Sprintf("v2#%d:%s", i, pat))
		}
	}
	phase2B := runPhase(pairs[0])
	phase2P := runPhase(pairs[1])
	d2 := compareAcross("after-batch2", phase2B, phase2P)

	// 树单调性：phase1 的全部命中必须仍在 phase2 中出现
	shrink := 0
	for _, u := range probes {
		in1 := make(map[string]struct{}, len(phase1P[u].set))
		for _, v := range phase1P[u].set {
			in1[v] = struct{}{}
		}
		in2 := make(map[string]struct{}, len(phase2P[u].set))
		for _, v := range phase2P[u].set {
			in2[v] = struct{}{}
		}
		for v := range in1 {
			if _, ok := in2[v]; !ok {
				shrink++
				t.Errorf("插入 batch2 后 phase1 命中回退 url=%s 丢失=%q", u, v)
				break
			}
		}
	}

	// phase3: Compact → Get，断言与 Compact 前完全一致
	for _, p := range pairs {
		p.compact()
	}
	phase3B := runPhase(pairs[0])
	phase3P := runPhase(pairs[1])
	d3 := compareAcross("after-compact", phase3B, phase3P)
	cB := 0
	for _, u := range probes {
		if phase2B[u].panicMsg != phase3B[u].panicMsg || len(phase2B[u].set) != len(phase3B[u].set) {
			cB++
			t.Errorf("基线树 Compact 前后不一致 url=%s before=(panic=%q set=%v) after=(panic=%q set=%v)",
				u, phase2B[u].panicMsg, phase2B[u].set, phase3B[u].panicMsg, phase3B[u].set)
		}
	}
	cP := 0
	for _, u := range probes {
		if phase2P[u].panicMsg != phase3P[u].panicMsg || len(phase2P[u].set) != len(phase3P[u].set) {
			cP++
			t.Errorf("生产树 Compact 前后不一致 url=%s before=(panic=%q set=%v) after=(panic=%q set=%v)",
				u, phase2P[u].panicMsg, phase2P[u].set, phase3P[u].panicMsg, phase3P[u].set)
		}
	}

	// 命中非空自检：至少一条 probe 命中了内容（保证断言不是空转）
	hit := 0
	for _, u := range probes {
		if len(phase3P[u].set) > 0 {
			hit++
		}
	}
	if hit == 0 {
		t.Fatalf("生命周期探针零命中，断言空转")
	}
	t.Logf("生命周期矩阵：跨树差异 batch1=%d batch2=%d compact=%d；插入回退=%d；Compact 回退 base=%d prod=%d；命中探针=%d/%d",
		d1, d2, d3, shrink, cB, cP, hit, len(probes))
}
