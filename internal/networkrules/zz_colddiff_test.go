package networkrules

// [2026-10-15 回放门差异根因探针] Part2 重建树回放自证 URL 与 Part1 引擎侧
// hot 集存在 7 条差异（仅 Part1 侧）。两个候选根因：
//   R1 推导分叉：引擎 Insert 的 pattern ≠ parseRuleParts(RawRule) 推导值
//   R2 树形依赖：同 pattern 集、不同 Insert 序 ⇒ 匹配结果集不同（真 bug）
// 本探针在引擎进程内逐条核对：
//   a) 引擎树上是否存在该 pattern 的值（RawRule 推导）
//   b) 引擎树 GetLP(selfcert URL) 是否返回它
//   c) 单 pattern 新鲜树 GetLP(selfcert URL) 是否命中（排除 URL 生成问题）
//   d) b 与 c 组合定位 R1/R2
// env：ZEN_COLD_DIFF=1（复用 ZEN_COLD_PROBE 装载与产物目录）。

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
	"github.com/irbis-sh/zen-desktop/internal/ruletree"
)

func TestZZColdDiff(t *testing.T) {
	if os.Getenv("ZEN_COLD_DIFF") != "1" {
		t.Skip("ZEN_COLD_DIFF not set")
	}
	dir := os.Getenv("ZEN_COLD_PROBE_OUT")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "zen_cold_probe")
	}
	taxLoad(t)
	ps := taxEngine.primaryStore

	// 读差异 id
	raw, err := os.ReadFile(filepath.Join(dir, "p_diff_ids.txt"))
	if err != nil {
		t.Fatalf("read diff ids: %v", err)
	}
	var ids []int
	for _, s := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("bad id %q: %v", s, err)
		}
		ids = append(ids, n)
	}
	sort.Ints(ids)

	// 读 pattern TSV（id → pattern）与 URL TSV（id → url）
	pats := map[int]string{}
	urls := map[int]string{}
	readTSV := func(name string, dst map[int]string, second bool) {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 1<<22)
		for sc.Scan() {
			l := sc.Text()
			i := strings.IndexByte(l, '\t')
			if i < 0 {
				continue
			}
			id, err := strconv.Atoi(l[:i])
			if err != nil {
				continue
			}
			if second {
				dst[id] = l[i+1:]
			} else {
				dst[id] = coldUnsanitizeLocal(l[i+1:])
			}
		}
	}
	readTSV("p_tree_patterns.tsv", pats, false)
	readTSV("p_selfcert_urls.tsv", urls, true)

	// 引擎树值 → RawRule 索引（按推导 pattern）
	byPat := map[string][]string{}
	ps.tree.Walk(func(r *rule.Rule) {
		pat, _ := parseRuleParts(r.RawRule)
		byPat[pat] = append(byPat[pat], r.RawRule)
	})

	for _, id := range ids {
		pat := pats[id]
		u := urls[id]
		t.Logf("=== id=%d ===", id)
		t.Logf("  pattern(%d B)=%q", len(pat), trunc(pat, 120))
		t.Logf("  url(%d B)=%q", len(u), trunc(u, 120))
		// a) 引擎树内该 pattern 的值
		raws := byPat[pat]
		if len(raws) == 0 {
			t.Logf("  a)引擎树: 无该 pattern 的值（推导分叉候选）")
		} else {
			t.Logf("  a)引擎树: %d 个值 RawRule=%q", len(raws), trunc(raws[0], 140))
		}
		// b) 引擎树命中？
		var engHit bool
		for _, r := range ps.tree.Get(u) {
			p2, _ := parseRuleParts(r.RawRule)
			if p2 == pat {
				engHit = true
				break
			}
		}
		t.Logf("  b)引擎树 GetLP(selfcert) 命中=%v", engHit)
		// c) solo 树命中？
		solo := ruletree.New[bool]()
		solo.Insert(pat, true)
		soloHits := solo.GetLP(u)
		t.Logf("  c)solo 树命中=%v（insert 后即查）", len(soloHits) > 0 && soloHits[0])
		// d) 判定
		switch {
		case !engHit && len(soloHits) > 0 && soloHits[0]:
			t.Logf("  d)=> 引擎树对自身 pattern 的自证 URL 未命中而 solo 命中 = 树形/状态依赖（R2 真 bug 候选）")
		case engHit && !(len(soloHits) > 0 && soloHits[0]):
			t.Logf("  d)=> solo 未命中引擎命中 = URL 生成与树语义交互（生成器问题）")
		case engHit:
			t.Logf("  d)=> 两树都命中 = Part2 重建树内容差异（R1/TSV 往返）")
		default:
			t.Logf("  d)=> 两树都未命中 = Part1 标记口径问题（co-hit 推导）")
		}
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func coldUnsanitizeLocal(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 't':
				b.WriteByte('\t')
				i++
			case 'n':
				b.WriteByte('\n')
				i++
			case 'r':
				b.WriteByte('\r')
				i++
			case '\\':
				b.WriteByte('\\')
				i++
			default:
				b.WriteByte(s[i])
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
