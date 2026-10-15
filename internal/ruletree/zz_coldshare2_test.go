package ruletree

// [2026-10-15 A2 Part2·节点级冷份额普查（M1 定量）]
//
// 输入：internal/networkrules/zz_coldprobe_test.go（ZEN_COLD_PROBE=1）导出的
//   - p_tree_patterns.tsv / e_tree_patterns.tsv：Compact 后存活树 pattern（id\tpattern）
//   - p_hot_selfcert.ids / p_hot_urls.ids / e_hot_*.ids：各口径 hot pattern id
//   - p_selfcert_urls.tsv：自证 URL（id\turl，回放等价门用）
//
// 方法：同 pattern 集 + 同 Insert 代码 ⇒ 结构同构树（Insert 确定性，
// ruletree.go:166-252）。对重建树做 DFS：节点冷 = 自身叶与全部后代叶均未命中。
// 输出各口径的 节点/叶/分支 冷份额与 M1 节省推算（口径与假设全部行内标注）。
//
// 回放等价门（规则10 反向预测）：对重建树重放自证 URL，Get 返回的 id 集
// 必须与 Part1 引擎侧标记的 hot id 集**完全一致**——不一致=管线有 bug，
// 数据不可信，先修再谈结论。
//
// env：ZEN_COLD_PROBE2=1；输入目录 ZEN_COLD_PROBE_OUT（与 Part1 相同）。

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func coldUnsanitize(s string) string {
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

func coldReadLines(tb testing.TB, dir, name string) []string {
	tb.Helper()
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		tb.Fatalf("read %s: %v", name, err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<22)
	for sc.Scan() {
		if l := sc.Text(); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func coldReadIDSet(tb testing.TB, dir, name string) map[int64]struct{} {
	tb.Helper()
	lines := coldReadLines(tb, dir, name)
	set := make(map[int64]struct{}, len(lines))
	for _, l := range lines {
		id, err := strconv.ParseInt(strings.TrimSpace(l), 10, 64)
		if err != nil {
			tb.Fatalf("%s: bad id %q: %v", name, l, err)
		}
		set[id] = struct{}{}
	}
	return set
}

type coldNodeFrame struct {
	n      *node[int64]
	parent int32
	depth  int32
}

// coldCensus 对重建树做全结构普查 + 指定 hot id 集的冷/热标记。
// roots 参与遍历但不计入统计（真实节点从第二层起）。
func coldCensus(tr *Tree[int64], hotSet map[int64]struct{}) (
	totalNodes, leafNodes, branchNodes, hotNodes, coldLeaves, coldBranches, singleLeaves, multiLeaves int,
	hotPrefixBytes, coldPrefixBytes int,
) {
	order := make([]coldNodeFrame, 0, 1<<21)
	type se struct {
		n      *node[int64]
		parent int32
		depth  int32
	}
	stack := []se{{tr.root, -1, 0}, {tr.domainBoundaryRoot, -1, 0}, {tr.anchorRoot, -1, 0}}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		idx := int32(len(order))
		order = append(order, coldNodeFrame{s.n, s.parent, s.depth})
		for _, e := range s.n.edges {
			stack = append(stack, se{e.node, idx, s.depth + 1})
		}
	}
	hot := make([]bool, len(order))
	rootCount := 0
	for i := len(order) - 1; i >= 0; i-- {
		f := order[i]
		isRoot := f.parent == -1
		if isRoot {
			rootCount++
			continue
		}
		if f.n.leaf != nil {
			for _, v := range f.n.leaf {
				if _, ok := hotSet[v]; ok {
					hot[i] = true
					break
				}
			}
		}
		if hot[i] && f.parent >= 0 {
			hot[f.parent] = true
		}
	}
	_ = rootCount
	for i, f := range order {
		if f.parent == -1 {
			continue
		}
		totalNodes++
		pl := len(f.n.prefixSlice())
		if hot[i] {
			hotNodes++
			hotPrefixBytes += pl
		} else {
			coldPrefixBytes += pl
		}
		if f.n.leaf != nil {
			leafNodes++
			if len(f.n.leaf) == 1 {
				singleLeaves++
			} else {
				multiLeaves++
			}
			if !hot[i] {
				coldLeaves++
			}
		} else {
			branchNodes++
			if !hot[i] {
				coldBranches++
			}
		}
	}
	return
}

func TestZZColdShare2(t *testing.T) {
	if os.Getenv("ZEN_COLD_PROBE2") != "1" {
		t.Skip("ZEN_COLD_PROBE2 not set")
	}
	dir := os.Getenv("ZEN_COLD_PROBE_OUT")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "zen_cold_probe")
	}

	run := func(side string, patternsFile string) {
		lines := coldReadLines(t, dir, patternsFile)
		pats := make([]string, len(lines))
		tr := New[int64]()
		for _, l := range lines {
			i := strings.IndexByte(l, '\t')
			if i < 0 {
				t.Fatalf("%s: bad line %q", patternsFile, l)
			}
			id, err := strconv.ParseInt(l[:i], 10, 64)
			if err != nil {
				t.Fatalf("%s: bad id: %v", patternsFile, err)
			}
			pats[id] = coldUnsanitize(l[i+1:])
		}
		missing := 0
		for id, p := range pats {
			if p == "" {
				missing++
				continue
			}
			tr.Insert(p, int64(id)) // id 唯一 ⇒ 叶值即 pattern id（冷标记按值命中）
		}
		tr.Compact()
		t.Logf("[%s]patterns=%d 空行=%d", side, len(pats), missing)

		// 回放等价门：重放自证 URL，id 集必须与 Part1 hot 集（按 pattern 记）一致
		selfIDs := coldReadIDSet(t, dir, side+"_hot_selfcert.ids")
		urlPath := filepath.Join(dir, side+"_selfcert_urls.tsv")
		if _, err := os.Stat(urlPath); err == nil {
			urlLines := coldReadLines(t, dir, side+"_selfcert_urls.tsv")
			replay := map[int64]struct{}{}
			for _, l := range urlLines {
				i := strings.IndexByte(l, '\t')
				if _, err := strconv.ParseInt(l[:i], 10, 64); err != nil {
					t.Fatalf("bad selfcert id: %v", err)
				}
				u := coldUnsanitize(l[i+1:])
				for _, v := range tr.GetLP(u) {
					replay[v] = struct{}{}
				}
			}
			onlyReplay, onlyPart1 := 0, 0
			var diffSamples []string
			for id := range replay {
				if _, ok := selfIDs[id]; !ok {
					onlyReplay++
					if len(diffSamples) < 20 && int(id) < len(pats) {
						diffSamples = append(diffSamples, fmt.Sprintf("id=%d len=%d pat=%.80s", id, len(pats[id]), pats[id]))
					}
				}
			}
			for id := range selfIDs {
				if _, ok := replay[id]; !ok {
					onlyPart1++
					if len(diffSamples) < 20 && int(id) < len(pats) {
						diffSamples = append(diffSamples, fmt.Sprintf("id=%d(len=%d,只P1) pat=%.80s", id, len(pats[id]), pats[id]))
					}
				}
			}
			t.Logf("[%s]回放等价门: replay=%d part1=%d 仅replay=%d 仅part1=%d => %s",
				side, len(replay), len(selfIDs), onlyReplay, onlyPart1,
				map[bool]string{true: "PASS", false: "FAIL"}[onlyReplay == 0 && onlyPart1 == 0])
			if onlyReplay > 0 || onlyPart1 > 0 {
				t.Logf("[%s]差异样本: %s", side, strings.Join(diffSamples, " | "))
				t.Logf("[%s]遍历预算截断计数(本进程 replay 期间)=%d", side, TraversalBudgetExceeded())
				var diffIDs []string
				for id := range selfIDs {
					if _, ok := replay[id]; !ok {
						diffIDs = append(diffIDs, strconv.FormatInt(id, 10))
					}
				}
				sort.Strings(diffIDs)
				if err := os.WriteFile(filepath.Join(dir, side+"_diff_ids.txt"), []byte(strings.Join(diffIDs, "\n")), 0o644); err != nil {
					t.Fatalf("write diff ids: %v", err)
				}
				// 决定性诊断：差异 id 的 URL 在重建树内单条重放——
				// 仍未命中=重建树内容/结构问题（与顺序无关）；命中=回放循环状态问题
				for _, l := range urlLines {
					i := strings.IndexByte(l, '\t')
					id, err := strconv.ParseInt(l[:i], 10, 64)
					if err != nil {
						continue
					}
					found := false
					for _, s := range diffIDs {
						if s == strconv.FormatInt(id, 10) {
							found = true
							break
						}
					}
					if !found {
						continue
					}
					u := coldUnsanitize(l[i+1:])
					var hitID []int64
					for _, v := range tr.GetLP(u) {
						hitID = append(hitID, v)
					}
					t.Logf("[%s]单条重放 id=%d urlLen=%d 命中值=%v", side, id, len(u), hitID)
				}
			}
		} else {
			t.Logf("[%s]selfcert URL 文件缺失，跳过回放门（%v）", side, err)
		}

		hotURLs := coldReadIDSet(t, dir, side+"_hot_urls.ids")
		for _, tc := range []struct {
			name string
			hot  map[int64]struct{}
		}{
			{"selfcert", selfIDs},
			{"urls", hotURLs},
		} {
			total, leafN, branchN, hotN, coldL, coldB, s1, mL, hpb, cpb := coldCensus(tr, tc.hot)
			sav1 := coldL * 48
			sav2 := sav1 + coldB*24
			t.Logf("[%s][%s]节点: total=%d leaf=%d(单值=%d 多值=%d) branch=%d hot=%d | 冷: leaf=%d branch=%d | arena字节: hot=%d cold=%d",
				side, tc.name, total, leafN, s1, mL, branchN, hotN, coldL, coldB, hpb, cpb)
			t.Logf("[%s][%s]M1 推算: S1(仅冷叶扁平化)=%.1fMB  S2(冷叶+冷分支)=%.1fMB  （口径：冷叶省48B/个、冷分支省24B/个；64B size class）",
				side, tc.name, float64(sav1)/1048576, float64(sav2)/1048576)
		}
	}

	run("p", "p_tree_patterns.tsv")
	run("e", "e_tree_patterns.tsv")
}
