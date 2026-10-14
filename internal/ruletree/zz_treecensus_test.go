package ruletree

// [2026-10-14 树结构普查·无损优化研究] 读 zz_taxonomy_test.go 导出的主树 pattern
// 集 TSV，按生产同构（Insert+Compact）重建树后做全结构普查：节点/边/扇出/深度/
// 前缀长度/叶子值分布/arena 利用率/内存构成估算。
// env 门控：ZEN_TREE_CENSUS=1；TSV 路径 ZEN_TREE_CENSUS_TSV
// （默认 %TEMP%\zen_taxonomy\tree_patterns.tsv）。

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZZTreeCensus(t *testing.T) {
	if os.Getenv("ZEN_TREE_CENSUS") != "1" {
		t.Skip("ZEN_TREE_CENSUS not set")
	}
	tsv := os.Getenv("ZEN_TREE_CENSUS_TSV")
	if tsv == "" {
		tsv = filepath.Join(os.TempDir(), "zen_taxonomy", "tree_patterns.tsv")
	}
	f, err := os.Open(tsv)
	if err != nil {
		t.Skipf("tsv unavailable: %v", err)
	}
	defer f.Close()
	tr := New[int64]()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	n := 0
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		if p == "" {
			continue
		}
		tr.Insert(p, 1)
		n++
	}
	tr.Compact()
	t.Logf("patterns=%d", n)

	var walk func(root *node[int64], name string)
	walk = func(root *node[int64], name string) {
		var nodes, edges, leafNodes, leafVals, purePath, prefixTokens int64
		var fanout, depth, prefix, leafDist [12]int64
		type se struct {
			n *node[int64]
			d int
		}
		stack := []se{{root, 0}}
		for len(stack) > 0 {
			s := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			nodes++
			if s.d < 11 {
				depth[s.d]++
			} else {
				depth[11]++
			}
			fo := len(s.n.edges)
			edges += int64(fo)
			switch {
			case fo == 0:
				fanout[0]++
			case fo == 1:
				fanout[1]++
			case fo == 2:
				fanout[2]++
			case fo <= 4:
				fanout[3]++
			case fo <= 8:
				fanout[4]++
			case fo <= 16:
				fanout[5]++
			case fo <= 24:
				fanout[6]++
			case fo <= 64:
				fanout[7]++
			case fo <= 256:
				fanout[8]++
			default:
				fanout[9]++
			}
			if fo == 1 && s.n.leaf == nil {
				purePath++
			}
			if s.n.leaf != nil {
				leafNodes++
				lv := int64(len(s.n.leaf))
				leafVals += lv
				switch {
				case lv == 1:
					leafDist[1]++
				case lv == 2:
					leafDist[2]++
				case lv <= 4:
					leafDist[3]++
				case lv <= 8:
					leafDist[4]++
				default:
					leafDist[5]++
				}
			}
			pl := len(s.n.prefixSlice())
			prefixTokens += int64(pl)
			switch {
			case pl == 0:
				prefix[0]++
			case pl == 1:
				prefix[1]++
			case pl == 2:
				prefix[2]++
			case pl <= 4:
				prefix[3]++
			case pl <= 8:
				prefix[4]++
			case pl <= 16:
				prefix[5]++
			default:
				prefix[6]++
			}
			for _, e := range s.n.edges {
				stack = append(stack, se{e.node, s.d + 1})
			}
		}
		estN := nodes * 64
		estE := edges * 16
		estL := leafVals * 8
		est := estN + estE + estL
		t.Logf("[%s] nodes=%d edges=%d 纯过路(扇出1无叶)=%d leafNodes=%d leafVals=%d 前缀tokens=%d 估算=%.1fMB(节点%.1f+边%.1f+叶值%.1f)",
			name, nodes, edges, purePath, leafNodes, leafVals, prefixTokens,
			float64(est)/1048576, float64(estN)/1048576, float64(estE)/1048576, float64(estL)/1048576)
		t.Logf("[%s] fanout桶[0,1,2,<=4,<=8,<=16,<=24,<=64,<=256,>256]=%v", name, fanout[:10])
		t.Logf("[%s] depth桶[0..10,>=11]=%v", name, depth)
		t.Logf("[%s] prefix桶[0,1,2,<=4,<=8,<=16,>16]=%v", name, prefix[:7])
		t.Logf("[%s] leaf值分布[1,2,<=4,<=8,>8]=%v", name, leafDist[:6])
	}
	walk(tr.root, "plain")
	walk(tr.domainBoundaryRoot, "||dom")
	walk(tr.anchorRoot, "|anch")

	alloc := 0
	for _, ch := range tr.arena.chunks {
		alloc += len(ch)
	}
	used := alloc - len(tr.arena.cur)
	var wastePct float64
	if alloc > 0 {
		wastePct = 100 * float64(alloc-used) / float64(alloc)
	}
	t.Logf("[arena] chunks=%d tokens分配=%d 实占=%d 浪费=%.1f%% | 2B/token 实占=%.1fMB → 1B打包可省≈%.1fMB",
		len(tr.arena.chunks), alloc, used, wastePct,
		float64(used*2)/1048576, float64(used)/1048576)
}
