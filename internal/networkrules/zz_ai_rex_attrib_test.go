package networkrules

// [2026-10-14 AI 接手审计探针] 正则桶归属与剪枝余量精确测量。
//
// 目的：回答 backlog B1「31.75% 正则 CPU 桶重归因」——
//   1) 31.93% 的 regexp.MatchString 到底由哪些规则、执行多少次贡献；
//   2) token 反向索引（rulestore.go:154-173）实际跳过率是多少；
//   3) 若「分支字面量剪枝」做到完美（全语料位置精确 Contains），上界省多少。
//
// 无损：纯测量，零生产代码改动。env 门控 ZEN_AI_REX_ATTRIB=1。
// 输出目录 ZEN_AI_REX_OUT（默认 %TEMP%\zen_ai_rex_attrib）。
//
// 装载口径与 zz_fresh_profile_test.go / zz_rex_pareto_test.go 完全同构：
// 13 启用列表 cache.txt → ParseRule → Compact；语料 ruletree/testdata/urls.txt。

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"regexp/syntax"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	aiOnce   sync.Once
	aiErr    error
	aiEngine *NetworkRules
	aiCorpus []string
	aiLoadMs int64
	aiParsed int
)

func aiLoad(tb testing.TB) {
	tb.Helper()
	aiOnce.Do(func() {
		filtersDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
		var datas [][]byte
		for _, u := range taxFilterURLs {
			h := fmt.Sprintf("%x", md5.Sum([]byte(u)))
			p := filepath.Join(filtersDir, h+".cache.txt")
			b, err := os.ReadFile(p)
			if err != nil {
				aiErr = fmt.Errorf("read %s: %w", p, err)
				return
			}
			datas = append(datas, b)
		}
		e := New()
		name := "ai-attrib"
		start := time.Now()
		for _, raw := range datas {
			sc := bufio.NewScanner(bytes.NewReader(raw))
			sc.Buffer(nil, 1<<20)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
					continue
				}
				if _, err := e.ParseRule(line, &name); err == nil {
					aiParsed++
				}
			}
		}
		e.Compact()
		aiLoadMs = time.Since(start).Milliseconds()
		aiEngine = e

		raw2, err := os.ReadFile(filepath.Join("..", "ruletree", "testdata", "urls.txt"))
		if err != nil {
			aiErr = fmt.Errorf("read urls.txt: %w", err)
			return
		}
		sc := bufio.NewScanner(bytes.NewReader(raw2))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			u := strings.TrimSpace(sc.Text())
			if u != "" {
				aiCorpus = append(aiCorpus, u)
			}
		}
	})
	if aiErr != nil {
		tb.Skipf("ai attrib data unavailable: %v", aiErr)
	}
}

type aiRow struct {
	i        int
	raw      string
	toks     []string
	fast     bool
	hasToks  bool
	execs    int64
	skips    int64 // times the token gate would have skipped it
	ideal    int64 // times a perfect branch-literal gate would skip it
	nanos    int64
	dfBest   int32
	setMaxDF int32
}

// aiContainsAny 精确判断 url 是否包含任一 token（全位置 Contains）。
func aiContainsAny(url string, toks []string) bool {
	for _, t := range toks {
		if strings.Contains(url, t) {
			return true
		}
	}
	return false
}

func TestZZAIRexAttrib(t *testing.T) {
	if os.Getenv("ZEN_AI_REX_ATTRIB") != "1" {
		t.Skip("ZEN_AI_REX_ATTRIB not set")
	}
	aiLoad(t)
	ps := aiEngine.primaryStore
	outDir := os.Getenv("ZEN_AI_REX_OUT")
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "zen_ai_rex_attrib")
	}
	_ = os.MkdirAll(outDir, 0o755)
	t.Logf("load: parsed=%d loadMs=%d corpus=%d", aiParsed, aiLoadMs, len(aiCorpus))

	// --- primary store ---
	n := len(ps.regexp)
	t.Logf("primary regexp rules=%d", n)

	idx := ps.idx.Load()
	if idx == nil {
		idx = ps.buildIdx()
		ps.idx.Store(idx)
	}
	t.Logf("idx tokensOf=%d", len(idx.tokensOf))

	rows := make([]aiRow, n)
	for i := range ps.regexp {
		rows[i] = aiRow{i: i, raw: ps.regexp[i].value.RawRule, fast: ps.regexp[i].fast != nil}
		if i < len(idx.tokensOf) {
			rows[i].toks = idx.tokensOf[i]
			rows[i].hasToks = len(idx.tokensOf[i]) > 0
		}
	}

	// --- 逐 URL 主循环：完全复刻 rulestore.Get 的 token 门 + match 计时 ---
	var totalEvaluated, totalSkipped, totalIdealSkip int64
	var totalNanos int64
	for k := 0; k < len(aiCorpus); k++ {
		u := aiCorpus[k]
		hasNL := strings.IndexByte(u, '\n') >= 0
		var hit hitBits
		idx.markCandidates(u, &hit)
		for i := range ps.regexp {
			rr := &ps.regexp[i]
			row := &rows[i]
			// (1) 现行 token 门（rulestore.go:154-169 逐行同构）
			gated := false
			if toks := idx.tokensOf[i]; len(toks) > 0 {
				if hit[i/64]&(1<<(i%64)) == 0 {
					gated = true
				} else if !aiContainsAny(u, toks) {
					gated = true
				}
			}
			if gated {
				row.skips++
				totalSkipped++
				continue
			}
			// (2) 完美剪枝模拟：即使 k-gram 命中，也用精确 Contains 复核
			//     （现行实现已经做了这步；此处额外统计「若 branch 集合更优」的差值）
			if len(idx.tokensOf[i]) > 0 && !aiContainsAny(u, idx.tokensOf[i]) {
				row.ideal++
				totalIdealSkip++
			}
			st := time.Now()
			_ = rr.match(u, hasNL)
			d := time.Since(st).Nanoseconds()
			row.nanos += d
			row.execs++
			totalNanos += d
			totalEvaluated++
		}
	}

	sort.Slice(rows, func(a, b int) bool { return rows[a].nanos > rows[b].nanos })
	t.Logf("SUMMARY primary: corpus=%d rules=%d evaluated=%d skipped=%d idealSkip=%d matchTime=%.2fms",
		len(aiCorpus), n, totalEvaluated, totalSkipped, totalIdealSkip, float64(totalNanos)/1e6)

	var sb strings.Builder
	sb.WriteString("i\texecs\tskips\tnanos\tfast\thasToks\tnToks\tdfBest\tsetMaxDF\traw\n")
	for _, r := range rows {
		toks := strings.Join(r.toks, "|")
		if len(toks) > 200 {
			toks = toks[:200] + "..."
		}
		fmt.Fprintf(&sb, "%d\t%d\t%d\t%d\t%v\t%v\t%d\t%d\t%d\t%s\t%s\n",
			r.i, r.execs, r.skips, r.nanos, r.fast, r.hasToks, len(r.toks), r.dfBest, r.setMaxDF, r.raw, toks)
	}
	if err := os.WriteFile(filepath.Join(outDir, "primary_regex_attrib.tsv"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for j := 0; j < 15 && j < len(rows); j++ {
		r := rows[j]
		t.Logf("TOP#%d i=%d nanos=%.2fms execs=%d skips=%d fast=%v nToks=%d raw=%.80s",
			j+1, r.i, float64(r.nanos)/1e6, r.execs, r.skips, r.fast, len(r.toks), r.raw)
	}
}

// TestZZAIRexSelect 静态分析：对每条规则计算「若选 branch 集合 vs 现行选择」
// 的剪枝力差值（用真实语料精确 Contains 计数，不做 k-gram 近似）。
func TestZZAIRexSelect(t *testing.T) {
	if os.Getenv("ZEN_AI_REX_ATTRIB") != "1" {
		t.Skip("ZEN_AI_REX_ATTRIB not set")
	}
	aiLoad(t)
	ps := aiEngine.primaryStore
	outDir := os.Getenv("ZEN_AI_REX_OUT")
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "zen_ai_rex_attrib")
	}
	_ = os.MkdirAll(outDir, 0o755)

	type cand struct {
		i       int
		raw     string
		cur     []string // 现行 tokensOf
		alt     []string // altTokens 集合
		curHits int      // 语料中含 cur 任一的 URL 数
		altHits int      // 语料中含 alt 任一的 URL 数
	}
	var cs []cand
	for i := range ps.regexp {
		pat, _ := parseRuleParts(ps.regexp[i].value.RawRule)
		if len(pat) < 2 {
			continue
		}
		body := pat[1 : len(pat)-1]
		ast, err := syntax.Parse(body, syntax.Perl)
		if err != nil || hasFoldCase(ast) {
			continue
		}
		alt := altTokens(ast)
		var cur []string
		if i < len(ps.idx.Load().tokensOf) {
			cur = ps.idx.Load().tokensOf[i]
		}
		if len(alt) == 0 && len(cur) == 0 {
			continue
		}
		c := cand{i: i, raw: ps.regexp[i].value.RawRule, cur: cur, alt: alt}
		for _, u := range aiCorpus {
			if len(cur) > 0 && aiContainsAny(u, cur) {
				c.curHits++
			}
			if len(alt) > 0 && aiContainsAny(u, alt) {
				c.altHits++
			}
		}
		cs = append(cs, c)
	}
	// 按「alt 剪枝力 - cur 剪枝力」降序（越大越值得改用 alt）
	sort.Slice(cs, func(a, b int) bool {
		da := cs[a].curHits - cs[a].altHits
		db := cs[b].curHits - cs[b].altHits
		return da > db
	})
	var sb strings.Builder
	sb.WriteString("i\tcurHits\taltHits\tdelta\tnCur\tnAlt\traw\tcurToks\taltToks\n")
	for _, c := range cs {
		fmt.Fprintf(&sb, "%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\t%s\n",
			c.i, c.curHits, c.altHits, c.curHits-c.altHits, len(c.cur), len(c.alt),
			c.raw, strings.Join(c.cur, "|"), strings.Join(c.alt, "|"))
	}
	if err := os.WriteFile(filepath.Join(outDir, "select_compare.tsv"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("select compare: rules=%d corpus=%d", len(cs), len(aiCorpus))
	for j := 0; j < 15 && j < len(cs); j++ {
		c := cs[j]
		t.Logf("SEL#%d i=%d curHits=%d altHits=%d delta=%d nCur=%d nAlt=%d raw=%.70s",
			j+1, c.i, c.curHits, c.altHits, c.curHits-c.altHits, len(c.cur), len(c.alt), c.raw)
	}
}
