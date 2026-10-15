package networkrules

// [2026-10-15 A2 冷份额探针·batch4 方法重建（post-hostSplit 口径）]
//
// 目的：为 M1 冷层扁平化立项提供实测数据。三个层次：
//   A) 树候选级：自证 URL（每条树 pattern 构造一条必然匹配它的 URL）+ urls.txt
//      真实语料，分别标记主/例外树里"至少被一次 Get 返回过"的叶子。
//      自证口径 = 原理可匹配冷份额下界；urls.txt 口径 = 真实语料冷份额上界。
//   B) 规则级：自证 URL × 16 dest×referer 变体（batch4 的 16 变体矩阵方法），
//      走完整 ModifyReq，取 appliedRules——规则级活跃/冷份额（可比 batch4 的 60.4% 先验）。
//   C) 导出 Part 2 文件：树 pattern TSV + 各口径 hot id 集，供
//      internal/ruletree/zz_coldshare2_test.go 做节点级冷份额普查（M1 定量）。
//
// 无损性：纯 _test 探针，env 门控（ZEN_COLD_PROBE=1），常规套件零影响；
// 生产引擎零改动。装载复用 zz_taxonomy_test.go 的 taxLoad（生产同构：13 列表
// 缓存 + Compact（badfilter/覆盖去重/hostSplit/T1 全部生效））。
//
// 产物目录：ZEN_COLD_PROBE_OUT（默认 %TEMP%\zen_cold_probe）。

import (
	"bufio"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

// coldSelfCertURL 为一条树 pattern 构造"必然匹配"的 URL。
// 依据：树匹配为大小写敏感的逐字节匹配（tokenize.go 无折叠），因此
// 原 pattern 文本嵌入 URL 后逐字节一致 ⇒ 必然命中（带 scheme，避开
// ruletree.Get 对无 scheme 超短 URL 的存量 panic，经验记录 L40）。
// 返回 ok=false 表示该 pattern 无法安全自证（进 unmatchable 桶计数）。
func coldSelfCertURL(pat string) (string, bool) {
	transform := func(s string) string {
		s = strings.ReplaceAll(s, "*", "a")
		s = strings.ReplaceAll(s, "^", "/")
		s = strings.ReplaceAll(s, "|", "")
		return s
	}
	switch {
	case pat == "":
		return "", false
	case pat == "||":
		return "", false
	case strings.HasPrefix(pat, "||"):
		rest := pat[2:]
		i := strings.IndexAny(rest, "/*^|")
		var host, tail string
		if i < 0 {
			host, tail = rest, ""
		} else {
			host, tail = rest[:i], rest[i:]
		}
		if host == "" {
			return "", false
		}
		return "http://" + host + transform(tail), true
	case strings.HasPrefix(pat, "|"):
		rest := transform(pat[1:])
		if !strings.Contains(rest, "://") {
			return "", false
		}
		return rest, true
	default:
		t := transform(pat)
		if strings.Contains(t, "://") {
			return t, true
		}
		if !strings.HasPrefix(t, "/") {
			t = "/" + t
		}
		return "http://c.io" + t, true
	}
}

// coldRuleHasConds 反射读 rule.Rule.condMods（rule 包私有字段，探针专用；
// 先例：zz_taxonomy_test.go:307-311 同法）。nil = 无条件修饰符，
// 判定与请求变体无关，16 变体矩阵可降为 1 变体。
func coldRuleHasConds(r *rule.Rule) bool {
	rv := reflect.ValueOf(r).Elem()
	cv := rv.FieldByName("condMods")
	return cv.Kind() == reflect.Ptr && !cv.IsNil()
}

func coldTSVSanitize(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\t", "\\t")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

func coldMaxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// coldPatEntry 一条树 pattern 的聚合信息（side：'P' 主侧 / 'E' 例外侧）。
type coldPatEntry struct {
	side     byte
	hasConds bool
	values   int
}

func TestZZColdProbe(t *testing.T) {
	if os.Getenv("ZEN_COLD_PROBE") != "1" {
		t.Skip("ZEN_COLD_PROBE not set")
	}
	outDir := os.Getenv("ZEN_COLD_PROBE_OUT")
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "zen_cold_probe")
	}
	_ = os.MkdirAll(outDir, 0o755)
	taxLoad(t)
	nr := taxEngine
	ps, es := nr.primaryStore, nr.exceptionStore

	var say []string
	log := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		say = append(say, line)
		t.Logf("%s", line)
	}

	// ---- 1) 枚举存活树值 → pattern（parseRuleParts 与生产 Insert 同源）----
	patMap := map[string]*coldPatEntry{}
	var pTreeVals, eTreeVals, pureHostInTree int
	ps.tree.Walk(func(r *rule.Rule) {
		pTreeVals++
		pat, _ := parseRuleParts(r.RawRule)
		if _, ok := pureHostPattern(pat); ok {
			pureHostInTree++ // hostSplit 开启时主树不应有纯主机 pattern——防御断言
		}
		e := patMap[pat]
		if e == nil {
			e = &coldPatEntry{side: 'P'}
			patMap[pat] = e
		}
		if coldRuleHasConds(r) {
			e.hasConds = true
		}
		e.values++
	})
	es.tree.Walk(func(er *exceptionrule.ExceptionRule) {
		eTreeVals++
		pat, _ := parseRuleParts(strings.TrimPrefix(er.RawRule, "@@"))
		e := patMap[pat]
		if e == nil {
			e = &coldPatEntry{side: 'E'}
			patMap[pat] = e
		}
		e.values++
	})
	log("[1]树值: primary=%d exception=%d 纯主机混入(应为0)=%d 唯一pattern=%d",
		pTreeVals, eTreeVals, pureHostInTree, len(patMap))

	// pattern 密集 id（P/E 各自编号）
	type idSet struct {
		pats []string
		ids  map[string]int
	}
	mk := func(side byte) *idSet {
		s := &idSet{ids: map[string]int{}}
		for p, e := range patMap {
			if e.side == side {
				s.ids[p] = len(s.pats)
				s.pats = append(s.pats, p)
			}
		}
		return s
	}
	pIds, eIds := mk('P'), mk('E')
	log("[1]id 分配: P=%d E=%d", len(pIds.pats), len(eIds.pats))

	// ---- 2) 自证 URL 生成 ----
	var pURLs, eURLs, unmatchedP, unmatchedE []string
	urlOf := map[string]string{}
	for p, e := range patMap {
		u, ok := coldSelfCertURL(p)
		if !ok {
			if e.side == 'P' {
				unmatchedP = append(unmatchedP, p)
			} else {
				unmatchedE = append(unmatchedE, p)
			}
			continue
		}
		urlOf[p] = u
		if e.side == 'P' {
			pURLs = append(pURLs, u)
		} else {
			eURLs = append(eURLs, u)
		}
	}
	log("[2]自证URL: P=%d E=%d 不可自证: P=%d E=%d", len(pURLs), len(eURLs), len(unmatchedP), len(unmatchedE))

	// 并行树标记：worker 本地 hot 集，最后合并
	markParallel := func(urls []string, apply func(u string, emit func(pat string))) map[string]struct{} {
		hot := map[string]struct{}{}
		var mu sync.Mutex
		workers := runtime.GOMAXPROCS(0)
		if workers > 12 {
			workers = 12
		}
		chunk := (len(urls) + workers - 1) / coldMaxI(workers, 1)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				lo, hi := w*chunk, (w+1)*chunk
				if lo >= len(urls) {
					return
				}
				if hi > len(urls) {
					hi = len(urls)
				}
				local := map[string]struct{}{}
				emit := func(pat string) { local[pat] = struct{}{} }
				for _, u := range urls[lo:hi] {
					apply(u, emit)
				}
				mu.Lock()
				for k := range local {
					hot[k] = struct{}{}
				}
				mu.Unlock()
			}(w)
		}
		wg.Wait()
		return hot
	}

	// P 树标记
	pMarkApply := func(u string, emit func(string)) {
		for _, r := range ps.tree.Get(u) {
			pat, _ := parseRuleParts(r.RawRule)
			emit(pat)
		}
	}
	pHotSelf := markParallel(pURLs, pMarkApply)
	log("[3]P树自证 hot pattern=%d / %d（冷=%d）", len(pHotSelf), len(pIds.pats), len(pIds.pats)-len(pHotSelf))

	pHotURLs := markParallel(taxCorpus, pMarkApply)
	log("[3]P树urls.txt hot pattern=%d（冷=%d）", countSide(pHotURLs, patMap, 'P'), len(pIds.pats)-countSide(pHotURLs, patMap, 'P'))

	eMarkApply := func(u string, emit func(string)) {
		for _, er := range es.tree.Get(u) {
			pat, _ := parseRuleParts(strings.TrimPrefix(er.RawRule, "@@"))
			emit(pat)
		}
	}
	eHotSelf := markParallel(eURLs, eMarkApply)
	eHotURLs := markParallel(taxCorpus, eMarkApply)
	log("[3]E树: 自证 hot=%d urls hot=%d / %d", len(eHotSelf), len(eHotURLs), len(eIds.pats))

	// ---- 3) 导出 Part 2 文件 ----
	writeLines := func(name string, lines []string) error {
		f, err := os.Create(filepath.Join(outDir, name))
		if err != nil {
			return err
		}
		defer f.Close()
		bw := bufio.NewWriterSize(f, 1<<20)
		for _, l := range lines {
			if _, err := bw.WriteString(l + "\n"); err != nil {
				return err
			}
		}
		return bw.Flush()
	}
	idsOf := func(s *idSet, hot map[string]struct{}) []string {
		var out []string
		for p, id := range s.ids {
			if _, ok := hot[p]; ok {
				out = append(out, fmt.Sprintf("%d", id))
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	patLines := func(s *idSet) []string {
		out := make([]string, len(s.pats))
		for id, p := range s.pats {
			out[id] = fmt.Sprintf("%d\t%s", id, coldTSVSanitize(p))
		}
		return out
	}
	mustWrite := func(name string, lines []string) {
		if err := writeLines(name, lines); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mustWrite("p_tree_patterns.tsv", patLines(pIds))
	mustWrite("e_tree_patterns.tsv", patLines(eIds))
	mustWrite("p_hot_selfcert.ids", idsOf(pIds, pHotSelf))
	mustWrite("p_hot_urls.ids", idsOf(pIds, pHotURLs))
	mustWrite("e_hot_selfcert.ids", idsOf(eIds, eHotSelf))
	mustWrite("e_hot_urls.ids", idsOf(eIds, eHotURLs))
	var selfURLLines []string
	for p, id := range pIds.ids {
		if u, ok := urlOf[p]; ok {
			selfURLLines = append(selfURLLines, fmt.Sprintf("%d\t%s", id, u))
		}
	}
	sort.Slice(selfURLLines, func(i, j int) bool { return selfURLLines[i] < selfURLLines[j] })
	mustWrite("p_selfcert_urls.tsv", selfURLLines)
	log("[3]Part2 文件已导出 -> %s", outDir)

	// ---- 4) 规则级 16 变体矩阵（batch4 方法，完整 ModifyReq，appliedRules 活跃集）----
	type variant struct {
		dest, site, mode, user, referer string
	}
	dests := []variant{
		{"document", "none", "navigate", "?1", ""},
		{"document", "same-origin", "navigate", "", ""},
		{"empty", "cross-site", "cors", "", ""},
		{"empty", "same-origin", "cors", "", ""},
		{"image", "cross-site", "cors", "", ""},
		{"script", "cross-site", "cors", "", ""},
		{"style", "cross-site", "cors", "", ""},
		{"media", "cross-site", "cors", "", ""},
	}
	var variants []variant
	for _, d := range dests {
		variants = append(variants, d) // referer = 无（eff.ref 回退 own host）
		v2 := d
		v2.referer = "https://www.google.com/"
		variants = append(variants, v2)
	}
	log("[4]规则级变体=%d", len(variants))

	activeRules := map[string]struct{}{}
	var activeMu sync.Mutex
	var patDone, reqDone atomic.Int64
	workerN := runtime.GOMAXPROCS(0)
	if workerN > 12 {
		workerN = 12
	}
	type workItem struct {
		pat      string
		hasConds bool
	}
	work := make([]workItem, 0, len(patMap))
	for p, e := range patMap {
		if e.side == 'P' {
			work = append(work, workItem{p, e.hasConds})
		}
	}
	chunk2 := (len(work) + workerN - 1) / coldMaxI(workerN, 1)
	var wg2 sync.WaitGroup
	for w := 0; w < workerN; w++ {
		wg2.Add(1)
		go func(w int) {
			defer wg2.Done()
			lo, hi := w*chunk2, (w+1)*chunk2
			if lo >= len(work) {
				return
			}
			if hi > len(work) {
				hi = len(work)
			}
			local := map[string]struct{}{}
			for _, it := range work[lo:hi] {
				u, ok := urlOf[it.pat]
				if !ok {
					continue
				}
				pu, err := url.Parse(u)
				if err != nil || pu.Host == "" {
					continue
				}
				vs := variants
				if !it.hasConds {
					vs = variants[:1]
				}
				for _, v := range vs {
					req := &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
					if v.dest != "" {
						req.Header.Set("Sec-Fetch-Dest", v.dest)
					}
					if v.site != "" {
						req.Header.Set("Sec-Fetch-Site", v.site)
					}
					if v.mode != "" {
						req.Header.Set("Sec-Fetch-Mode", v.mode)
					}
					if v.user != "" {
						req.Header.Set("Sec-Fetch-User", v.user)
					}
					if v.referer != "" {
						req.Header.Set("Referer", v.referer)
					}
					applied, _, _ := nr.ModifyReq(req)
					reqDone.Add(1)
					for i := range applied {
						local[applied[i].RawRule] = struct{}{}
					}
				}
				if n := patDone.Add(1); n%20000 == 0 {
					t.Logf("[4]进度 pattern=%d req=%d", n, reqDone.Load())
				}
			}
			activeMu.Lock()
			for k := range local {
				activeRules[k] = struct{}{}
			}
			activeMu.Unlock()
		}(w)
	}
	wg2.Wait()
	log("[4]规则级: patterns=%d 请求=%d applied唯一规则文本=%d", len(work), reqDone.Load(), len(activeRules))
	var activeLines []string
	for r := range activeRules {
		activeLines = append(activeLines, coldTSVSanitize(r))
	}
	sort.Strings(activeLines)
	mustWrite("p_active_rules.txt", activeLines)

	// ---- 5) intern 候选普查（跨清单文本重复）----
	dup := map[string]int{}
	var rawBytes int
	internWalk := func(r *rule.Rule) {
		dup[r.RawRule]++
		rawBytes += len(r.RawRule)
	}
	ps.tree.Walk(internWalk)
	for i := range ps.generic {
		internWalk(ps.generic[i])
	}
	for i := range ps.regexp {
		internWalk(ps.regexp[i].value)
	}
	ps.hostMu.RLock()
	for _, rs := range ps.hostRules {
		for i := range rs {
			internWalk(rs[i])
		}
	}
	ps.hostMu.RUnlock()
	var uniqBytes int
	for k := range dup {
		uniqBytes += len(k)
	}
	saved := rawBytes - uniqBytes
	pct := 0.0
	if rawBytes > 0 {
		pct = 100 * float64(saved) / float64(rawBytes)
	}
	log("[5]intern 普查: 主侧规则值=%d 文本唯一=%d 持有字节=%d 唯一字节=%d 可省=%d (%.1f%%)",
		len(dup), len(dup), rawBytes, uniqBytes, saved, pct)

	if err := writeLines("summary.txt", say); err != nil {
		t.Fatalf("write summary: %v", err)
	}
}

func countSide(hot map[string]struct{}, patMap map[string]*coldPatEntry, side byte) int {
	n := 0
	for p := range hot {
		if e := patMap[p]; e != nil && e.side == side {
			n++
		}
	}
	return n
}
