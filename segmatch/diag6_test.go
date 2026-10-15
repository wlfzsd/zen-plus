package segmatch

import (
	"sort"
	"testing"
)

// [S1 终检] DAT 转移表 vs map 版 trie 转移表：对每个状态（按可达字节路径枚举）
// 比较两版自动机在同一字符串前缀下的「状态输出集合」。走字符串枚举（BFS 全路径，
// 深度 ≤ 12，限制规模），比较：给定前缀 s，map 版走到的节点 outs 集合 vs
// DAT 扫描 s 后的事件 litID 集合（按 end=len(s)-1 过滤，即 start=len(s)-len）。
func TestDATTransitionParity(t *testing.T) {
	pats := loadSegPatterns(t)
	di := NewDATIndex(pats)
	mi := NewIndex(pats)

	// BFS 枚举前缀串（ASCII 可见子集），深度限 10
	chars := []byte("ab/.comhttpseek:1^x*-")
	type item struct {
		s string
	}
	frontier := []item{{""}}
	seen := map[string]bool{"": true}
	var mism int
	for depth := 0; depth < 10; depth++ {
		var next []item
		for _, it := range frontier {
			for _, c := range chars {
				s := it.s + string(c)
				if seen[s] {
					continue
				}
				seen[s] = true
				next = append(next, item{s})
				// map 版：s 结束时的输出集合
				mn := miWalk(mi, s)
				mset := map[int32]bool{}
				for _, id := range mn {
					mset[id] = true
				}
				// DAT 版：扫描 s，取 end=len(s)-1 的事件
				dset := map[int32]bool{}
				di.ScanEvents(s, func(id int32, start int32) {
					// start+len-1 == len(s)-1 ⇒ end 对齐
					if int(start)+len(di.lits[id])-1 == len(s)-1 {
						dset[id] = true
					}
				})
				if len(mset) != len(dset) {
					mism++
					if mism <= 6 {
						t.Errorf("前缀=%q: map outs=%d dat outs=%d (map独=%v dat独=%v)",
							s, len(mset), len(dset), diffIDs2(mset, dset, di.lits), diffIDs2(dset, mset, di.lits))
					}
					continue
				}
				for id := range mset {
					if !dset[id] {
						mism++
						if mism <= 6 {
							t.Errorf("前缀=%q: map 有 lit %q dat 无", s, di.lits[id])
						}
						break
					}
				}
			}
		}
		frontier = next
	}
	t.Logf("TRANSITION-PARITY: 检查前缀数=%d 差异=%d", len(seen), mism)
	if mism != 0 {
		t.Fatalf("DAT 转移表与 map 版不一致")
	}
}

// miWalk：map 版扫描 s，收集 end==len(s)-1 的事件（与 DAT 语义对齐）。
func miWalk(mi *Index, s string) []int32 {
	var out []int32
	m := mi.scan(s)
	for id, ends := range m {
		for _, e := range ends {
			if int(e) == len(s)-1 {
				out = append(out, int32(id))
			}
		}
	}
	return out
}

func diffIDs2(a, b map[int32]bool, lits []string) []string {
	var d []string
	for id := range a {
		if !b[id] {
			d = append(d, lits[id])
		}
	}
	sort.Strings(d)
	return d
}
