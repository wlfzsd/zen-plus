package networkrules

// [2026-10-14 全量分类画像探针·无损优化研究] 生产同构装载 13 启用列表后，对最终
// 存储做全量分类普查：存储落点闭合（无静默丢弃断言）、pattern 形态、修饰符族、
// 正则形状族与 fastshape 覆盖、token 反向索引可达性实测（idxMaxRules 旁路查证）、
// 重复度/共享度、covRecord 规模、堆快照；并导出主树 pattern 集 TSV 供
// internal/ruletree/zz_treecensus_test.go 做结构普查。
// env 门控：ZEN_TAXONOMY=1；产物目录 ZEN_TAXONOMY_OUT（默认 %TEMP%\zen_taxonomy）。
// 装载骨架同 zz_fresh_profile_test.go（matchall_bench_test.go 口径）。

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp/syntax"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

// taxFilterURLs 与 matchall_bench_test.go enabledFilterURLs 一致（内测包无法引用
// 外部测试包变量，复制并注明来源；cache 文件名 = MD5(url)）。
var taxFilterURLs = []string{
	"https://cdn.jsdelivr.net/gh/irbis-sh/filter-lists@master/ads/ads.txt",
	"https://easylist.to/easylist/easylist.txt",
	"https://raw.githubusercontent.com/AdguardTeam/FiltersRegistry/master/filters/filter_2_Base/filter.txt",
	"https://cdn.jsdelivr.net/gh/irbis-sh/filter-lists@master/privacy/privacy.txt",
	"https://easylist.to/easylist/easyprivacy.txt",
	"https://raw.githubusercontent.com/AdguardTeam/FiltersRegistry/master/filters/filter_3_Spyware/filter.txt",
	"https://raw.githubusercontent.com/DandelionSprout/adfilt/master/LegitimateURLShortener.txt",
	"https://malware-filter.gitlab.io/malware-filter/urlhaus-filter-online.txt",
	"https://malware-filter.gitlab.io/malware-filter/phishing-filter-hosts.txt",
	"https://someonewhocares.org/hosts/zero/hosts",
	"https://pgl.yoyo.org/adservers/serverlist.php?hostformat=hosts&showintro=1&mimetype=plaintext",
	"https://filters.adtidy.org/extension/ublock/filters/224.txt",
	"https://anti-ad.net/easylist.txt",
}

var (
	taxOnce             sync.Once
	taxErr              error
	taxEngine           *NetworkRules
	taxParsed           int
	taxLines            int
	taxLoadMs           int64
	taxErrTop           map[string]int
	taxInternerDistinct int
	taxCorpus           []string
)

func taxLoad(tb testing.TB) {
	tb.Helper()
	taxOnce.Do(func() {
		filtersDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
		var datas [][]byte
		for _, u := range taxFilterURLs {
			h := fmt.Sprintf("%x", md5.Sum([]byte(u)))
			p := filepath.Join(filtersDir, h+".cache.txt")
			b, err := os.ReadFile(p)
			if err != nil {
				taxErr = fmt.Errorf("read %s: %w", p, err)
				return
			}
			datas = append(datas, b)
		}
		e := New()
		name := "taxonomy"
		start := time.Now()
		taxErrTop = map[string]int{}
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
				taxLines++
				if _, err := e.ParseRule(line, &name); err == nil {
					taxParsed++
				} else {
					k := err.Error()
					if len(k) > 48 {
						k = k[:48]
					}
					taxErrTop[k]++
				}
			}
		}
		taxInternerDistinct = e.modsInterner.size()
		e.Compact()
		taxEngine = e
		taxLoadMs = time.Since(start).Milliseconds()

		raw2, err := os.ReadFile(filepath.Join("..", "ruletree", "testdata", "urls.txt"))
		if err != nil {
			taxErr = fmt.Errorf("read urls.txt: %w", err)
			return
		}
		sc := bufio.NewScanner(bytes.NewReader(raw2))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			u := strings.TrimSpace(sc.Text())
			if u != "" {
				taxCorpus = append(taxCorpus, u)
			}
		}
	})
	if taxErr != nil {
		tb.Skipf("taxonomy data unavailable: %v", taxErr)
	}
}

func taxShapeOf(pat string) string {
	switch {
	case pat == "":
		return "empty"
	case len(pat) > 1 && pat[0] == '/' && pat[len(pat)-1] == '/':
		return "regexp"
	case strings.HasPrefix(pat, "||"):
		rest := pat[2:]
		i := strings.IndexAny(rest, "*^")
		switch {
		case i < 0:
			return "||d(plain)"
		case rest[i] == '^' && i == len(rest)-1:
			return "||d^"
		case rest[i] == '^':
			return "||d^rest"
		case rest[i] == '*':
			return "||d*wild"
		default:
			return "||d-other"
		}
	case strings.HasPrefix(pat, "|"):
		return "|anchor"
	case strings.Contains(pat, "*"):
		return "wildcard*"
	default:
		return "plain-substr"
	}
}

func taxSigOf(re *syntax.Regexp) string {
	var set []string
	has := func(s string) {
		for _, x := range set {
			if x == s {
				return
			}
		}
		set = append(set, s)
	}
	var walk func(r *syntax.Regexp)
	walk = func(r *syntax.Regexp) {
		switch r.Op {
		case syntax.OpLiteral:
			has("lit")
		case syntax.OpCharClass, syntax.OpAnyCharNotNL, syntax.OpAnyChar:
			has("cls")
		case syntax.OpBeginLine, syntax.OpBeginText:
			has("begin")
		case syntax.OpEndLine, syntax.OpEndText:
			has("end")
		case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
			has("quant")
		case syntax.OpAlternate:
			has("alt")
		case syntax.OpWordBoundary, syntax.OpNoWordBoundary:
			has("wb")
		}
		for _, s := range r.Sub {
			walk(s)
		}
	}
	walk(re)
	sort.Strings(set)
	return strings.Join(set, "+")
}

// taxLeadingLiteral 统计正则的前导字面量（syntax.Regexp 无 LiteralPrefix；
// 仅作画像统计，不参与任何过滤判定）。大小写折叠形态不作字面量统计。
func taxLeadingLiteral(re *syntax.Regexp) (string, bool) {
	if re.Flags&syntax.FoldCase != 0 {
		return "", false
	}
	switch re.Op {
	case syntax.OpLiteral:
		return string(re.Rune), true
	case syntax.OpConcat:
		if len(re.Sub) > 0 && re.Sub[0].Op == syntax.OpLiteral {
			return string(re.Sub[0].Rune), false
		}
	}
	return "", false
}

func topMap(m map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	var arr []kv
	for k, v := range m {
		arr = append(arr, kv{k, v})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].v > arr[j].v })
	if len(arr) > n {
		arr = arr[:n]
	}
	var b strings.Builder
	for i, x := range arr {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s=%d", x.k, x.v)
	}
	return b.String()
}

func TestZZTaxonomy(t *testing.T) {
	if os.Getenv("ZEN_TAXONOMY") != "1" {
		t.Skip("ZEN_TAXONOMY not set")
	}
	outDir := os.Getenv("ZEN_TAXONOMY_OUT")
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "zen_taxonomy")
	}
	_ = os.MkdirAll(outDir, 0o755)
	taxLoad(t)
	nr := taxEngine
	ps, es := nr.primaryStore, nr.exceptionStore
	t.Logf("[0]装载: lines=%d parsed=%d loadMs=%d corpus=%d", taxLines, taxParsed, taxLoadMs, len(taxCorpus))

	// ---- 1) 存储落点闭合（无静默丢弃断言）----
	var pTreeVals, eTreeVals int
	ps.tree.Walk(func(r *rule.Rule) { pTreeVals++ })
	es.tree.Walk(func(er *exceptionrule.ExceptionRule) { eTreeVals++ })
	pGen, pReg := len(ps.generic), len(ps.regexp)
	eGen, eReg := len(es.generic), len(es.regexp)
	pFast := 0
	for i := range ps.regexp {
		if ps.regexp[i].fast != nil {
			pFast++
		}
	}
	eFast := 0
	for i := range es.regexp {
		if es.regexp[i].fast != nil {
			eFast++
		}
	}
	acct := pTreeVals + pGen + pReg + nr.actionIdx.total + len(nr.badfilters) + eTreeVals + eGen + eReg
	t.Logf("[1]闭合: parsed=%d accounted=%d 差=%d", taxParsed, acct, taxParsed-acct)
	t.Logf("[1]落点: 主(tree=%d generic=%d regexp=%d fastShape覆盖=%d) actionIdx(total=%d cExact=%d cAny=%d rpExact=%d rpAny=%d) badfilter=%d",
		pTreeVals, pGen, pReg, pFast, nr.actionIdx.total, len(nr.actionIdx.cookieExact), len(nr.actionIdx.cookieAny), len(nr.actionIdx.rpExact), len(nr.actionIdx.rpAny), len(nr.badfilters))
	t.Logf("[1]例外: (tree=%d generic=%d regexp=%d fastShape覆盖=%d) pageScoped=%d frameScoped=%d covP=%d(sz=%dB) covE=%d",
		eTreeVals, eGen, eReg, eFast, nr.pageScopedExceptions.Load(), nr.frameScopedRules.Load(), len(nr.covP), int(unsafe.Sizeof(covRecPrimary{})), len(nr.covE))
	t.Logf("[1]解析错误Top: %s", topMap(taxErrTop, 6))

	// ---- 2) 形态/修饰符/重复/共享 ----
	shape := map[string]int{}
	excShape := map[string]int{}
	modCnt := map[string]int{}
	dup := map[string]int{}
	condPtr := map[uintptr]int{}
	actPtr := map[uintptr]int{}
	withCond, withAct := 0, 0
	flagDoc, flagPopup, flagImp, flagAll, flagFrame := 0, 0, 0, 0, 0
	rawBytes, uniqBytes := 0, 0
	patternSet := map[string]struct{}{}
	var primaryVals int

	collectPrimary := func(r *rule.Rule) {
		primaryVals++
		dup[r.RawRule]++
		rawBytes += len(r.RawRule)
		if r.Document {
			flagDoc++
		}
		if r.Popup {
			flagPopup++
		}
		if r.Important {
			flagImp++
		}
		if r.All {
			flagAll++
		}
		if r.FrameScoped {
			flagFrame++
		}
		rv := reflect.ValueOf(r).Elem()
		if cv := rv.FieldByName("condMods"); cv.Kind() == reflect.Ptr && !cv.IsNil() {
			withCond++
			condPtr[cv.Pointer()]++
		}
		if av := rv.FieldByName("actMods"); av.Kind() == reflect.Ptr && !av.IsNil() {
			withAct++
			actPtr[av.Pointer()]++
		}
		pat, mods := parseRuleParts(r.RawRule)
		shape[taxShapeOf(pat)]++
		patternSet[pat] = struct{}{}
		for _, m := range mods {
			nm := m
			if i := strings.IndexByte(nm, '='); i >= 0 {
				nm = nm[:i]
			}
			nm = strings.TrimPrefix(nm, "~")
			modCnt[nm]++
		}
	}
	ps.tree.Walk(collectPrimary)
	for i := range ps.generic {
		collectPrimary(ps.generic[i])
	}
	for i := range ps.regexp {
		collectPrimary(ps.regexp[i].value)
	}
	for k := range dup {
		uniqBytes += len(k)
	}
	es.tree.Walk(func(er *exceptionrule.ExceptionRule) {
		pat, _ := parseRuleParts(strings.TrimPrefix(er.RawRule, "@@"))
		excShape[taxShapeOf(pat)]++
	})
	excShape["empty"] += len(es.generic)
	t.Logf("[2]主侧形态: %s", topMap(shape, 12))
	t.Logf("[2]例外形态: %s", topMap(excShape, 10))
	t.Logf("[2]修饰符Top16: %s", topMap(modCnt, 16))
	t.Logf("[2]重复: 主侧规则值=%d 文本唯一=%d 文本字节持有=%d 唯一=%d pattern唯一=%d",
		primaryVals, len(dup), rawBytes, uniqBytes, len(patternSet))
	t.Logf("[2]共享: condMods唯一指针=%d(带cond=%d) actMods唯一指针=%d(带act=%d) 装载期interner去重文本=%d",
		len(condPtr), withCond, len(actPtr), withAct, taxInternerDistinct)
	t.Logf("[2]旗标: doc=%d popup=%d imp=%d all=%d frame=%d", flagDoc, flagPopup, flagImp, flagAll, flagFrame)

	// ---- 3) 正则形状族 + fastshape 覆盖 + token 索引可达性 ----
	fam := map[string]int{}
	famSample := map[string]string{}
	prefixNonEmpty, prefixComplete := 0, 0
	processReg := func(body string) {
		re, err := syntax.Parse(body, syntax.Perl)
		if err != nil {
			fam["<parse-err>"]++
			return
		}
		sig := taxSigOf(re)
		fam[sig]++
		if _, ok := famSample[sig]; !ok {
			famSample[sig] = body
		}
		pre, complete := taxLeadingLiteral(re)
		if pre != "" {
			prefixNonEmpty++
		}
		if complete {
			prefixComplete++
		}
	}
	for i := range ps.regexp {
		pat, _ := parseRuleParts(ps.regexp[i].value.RawRule)
		if len(pat) > 1 {
			processReg(pat[1 : len(pat)-1])
		}
	}
	for i := range es.regexp {
		raw := strings.TrimPrefix(es.regexp[i].value.RawRule, "@@")
		pat, _ := parseRuleParts(raw)
		if len(pat) > 1 {
			processReg(pat[1 : len(pat)-1])
		}
	}
	t.Logf("[3]正则形状族Top12: %s", topMap(fam, 12))
	t.Logf("[3]LiteralPrefix: 非空前缀=%d 完全字面=%d", prefixNonEmpty, prefixComplete)

	idx := nr.primaryStore.buildIdx()
	n := len(ps.regexp)
	nonTok := 0
	for i := range idx.tokensOf {
		if len(idx.tokensOf[i]) == 0 {
			nonTok++
		}
	}
	t.Logf("[3]token索引: 主regexp=%d idxMaxRules=%d 旁路=%v 无token规则=%d(必全评); 例外regexp=%d(旁路=%v)",
		n, idxMaxRules, n > idxMaxRules, nonTok, eReg, eReg > idxMaxRules)
	if n > 0 && len(taxCorpus) > 0 {
		var sampled int64
		var candCalls, candRegCalls, noIdxRegCalls int64
		nNonFast := int64(n - pFast)
		for k := 0; k < len(taxCorpus); k += 4 {
			u := taxCorpus[k]
			sampled++
			noIdxRegCalls += nNonFast
			for i := range idx.tokensOf {
				toks := idx.tokensOf[i]
				cand := len(toks) == 0
				if !cand {
					for _, tk := range toks {
						if strings.Contains(u, tk) {
							cand = true
							break
						}
					}
				}
				if cand {
					candCalls++
					if ps.regexp[i].fast == nil {
						candRegCalls++
					}
				}
			}
		}
		totalCand := float64(sampled) * float64(n)
		t.Logf("[3]索引收益(抽样%dURL): 候选调用 %d/%d=%.1f%%(跳过%.1f%%)；正则引擎调用 %d/%d=跳过%.1f%%",
			sampled, candCalls, int64(totalCand), 100*float64(candCalls)/totalCand,
			100-100*float64(candCalls)/totalCand,
			candRegCalls, noIdxRegCalls, 100-100*float64(candRegCalls)/float64(noIdxRegCalls))
	}

	// ---- 4) 堆快照（探针进程含语料，口径≠生产）----
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("[4]heap: HeapAlloc=%dMB HeapObjects=%d", ms.HeapAlloc>>20, ms.HeapObjects)

	// ---- 5) 主树 pattern TSV 导出（供树结构普查）----
	tsv := filepath.Join(outDir, "tree_patterns.tsv")
	f, err := os.Create(tsv)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	nPat := 0
	for pat := range patternSet {
		if pat == "" {
			continue
		}
		if len(pat) > 1 && pat[0] == '/' && pat[len(pat)-1] == '/' {
			continue
		}
		fmt.Fprintln(w, pat)
		nPat++
	}
	w.Flush()
	f.Close()
	t.Logf("[5]TSV: %s patterns=%d", tsv, nPat)
}
