package networkrules

// [2026-10-14 AI 接手审计探针 2] 两条具体改进路径的量化诊断：
//   A) altTokens 失败原因（分支字面量 < idxMinTokenLen 拖垮整个 any-of 集合）；
//   B) $domain 门控正则规则的「域资格率」——若接近 0，则按域建索引可省掉其 regex 执行。
//
// 无损：纯测量。env 门控 ZEN_AI_ALT_DIAG=1。

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp/syntax"
	"sort"
	"strings"
	"testing"
)

func TestZZAIAltDiag(t *testing.T) {
	if os.Getenv("ZEN_AI_ALT_DIAG") != "1" {
		t.Skip("ZEN_AI_ALT_DIAG not set")
	}
	aiLoad(t)
	ps := aiEngine.primaryStore
	if ps.idx.Load() == nil {
		ps.idx.Store(ps.buildIdx())
	}
	idx := ps.idx.Load()

	type diag struct {
		i        int
		raw      string
		segs     []string
		locks    []bool
		altN     int
		altErr   string
		tokN     int
		chosen   string
		fast     bool
		domHits  int
		domTotal int
		hasDom   bool
		domKind  string
	}
	var out []diag
	for i := range ps.regexp {
		d := diag{i: i, raw: ps.regexp[i].value.RawRule, fast: ps.regexp[i].fast != nil}
		if i < len(idx.tokensOf) {
			d.tokN = len(idx.tokensOf[i])
			d.chosen = strings.Join(idx.tokensOf[i], "|")
		}
		pat, _ := parseRuleParts(ps.regexp[i].value.RawRule)
		if len(pat) >= 2 {
			body := pat[1 : len(pat)-1]
			if ast, err := syntax.Parse(body, syntax.Perl); err == nil && !hasFoldCase(ast) {
				d.segs, d.locks = segmentsOfNode(ast)
				set := altTokens(ast)
				d.altN = len(set)
				if set == nil {
					d.altErr = aiAltFailReason(ast)
				}
			} else if err != nil {
				d.altErr = "parse:" + err.Error()
			} else {
				d.altErr = "foldcase"
			}
		}
		// --- 域资格率（$domain 门控） ---
		rule := ps.regexp[i].value
		for _, m := range rule.AndConditionModifiers() {
			dm, ok := m.(interface {
				MatchHost(string) bool
				HasPermittedDomains() bool
				Inverted() bool
			})
			if !ok {
				continue
			}
			d.hasDom = true
			if !dm.HasPermittedDomains() {
				d.domKind = "negonly"
			} else if dm.Inverted() {
				d.domKind = "mixed"
			} else {
				d.domKind = "positive"
			}
			for _, u := range aiCorpus {
				pu, err := url.Parse(u)
				if err != nil || pu.Hostname() == "" {
					continue
				}
				d.domTotal++
				if dm.MatchHost(pu.Hostname()) {
					d.domHits++
				}
			}
			break
		}
		out = append(out, d)
	}

	outDir := os.Getenv("ZEN_AI_REX_OUT")
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "zen_ai_rex_attrib")
	}
	_ = os.MkdirAll(outDir, 0o755)
	var sb strings.Builder
	sb.WriteString("i\tfast\tsegs\taltN\taltErr\ttokN\tchosen\tdomKind\tdomHits\tdomTotal\traw\n")
	for _, d := range out {
		chosen := d.chosen
		if len(chosen) > 160 {
			chosen = chosen[:160] + "..."
		}
		fmt.Fprintf(&sb, "%d\t%v\t%s\t%d\t%s\t%d\t%s\t%s\t%d\t%d\t%s\n",
			d.i, d.fast, strings.Join(d.segs, ";"), d.altN, d.altErr, d.tokN, chosen,
			d.domKind, d.domHits, d.domTotal, d.raw)
	}
	if err := os.WriteFile(filepath.Join(outDir, "alt_diag.tsv"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// 汇总：altTokens 失败的规则
	var fails []diag
	for _, d := range out {
		if d.altN == 0 && d.altErr != "" {
			fails = append(fails, d)
		}
	}
	sort.Slice(fails, func(a, b int) bool { return fails[a].i < fails[b].i })
	t.Logf("altTokens 返回 nil 的规则数=%d / %d", len(fails), len(out))
	reasonCount := map[string]int{}
	for _, d := range fails {
		reasonCount[d.altErr]++
	}
	for r, c := range reasonCount {
		t.Logf("  reason=%s count=%d", r, c)
	}
	for j := 0; j < 10 && j < len(fails); j++ {
		t.Logf("  FAIL#%d i=%d reason=%s segs=%v raw=%.75s", j+1, fails[j].i, fails[j].altErr, fails[j].segs, fails[j].raw)
	}

	// 汇总：域资格率
	var gated []diag
	for _, d := range out {
		if d.hasDom && d.domTotal > 0 {
			gated = append(gated, d)
		}
	}
	zero := 0
	for _, d := range gated {
		if d.domHits == 0 {
			zero++
		}
	}
	t.Logf("域门控正则规则=%d，其中语料内 0 命中的=%d", len(gated), zero)
}

// aiAltFailReason 复刻 altTokens 的判定，返回失败原因（诊断用）。
func aiAltFailReason(ast *syntax.Regexp) string {
	var group *syntax.Regexp
	switch ast.Op {
	case syntax.OpAlternate:
		group = ast
	case syntax.OpConcat:
		for i, c := range ast.Sub {
			if !isMandatory(c) {
				continue
			}
			if g := altGroupOf(c); g != nil {
				group = g
				_ = i
				break
			}
		}
		if group == nil {
			return "no-mandatory-alt-group"
		}
	default:
		return "root-not-concat/alt"
	}
	for bi, b := range group.Sub {
		ts, ok := branchOneOf(b)
		if !ok {
			return fmt.Sprintf("branch#%d-no-token(op=%s)", bi, b.Op)
		}
		if len(ts) == 0 {
			return fmt.Sprintf("branch#%d-empty", bi)
		}
	}
	return "other"
}
