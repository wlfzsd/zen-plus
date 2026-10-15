package segmatch

// [引擎 v2 · S1 性能化] AC 自动机索引：全部模式的全部字面量段建一台自动机，
// 每 URL 单遍 O(len) 得到全部字面量出现位；匹配走「首字面量出现位 ∩ 对齐位」
// + 有序归并（与 Match 的 greedy 语义严格同构，同一差分门禁验收）。
import (
	"sort"
	"strings"
)

type acNode struct {
	children map[byte]*acNode
	fail     *acNode
	outs     []int // 以此节点结尾的字面量 id（可能有多个：不同 id 同串不可能——id 按 string 去重；同节点多 id 仅当不同串共享后缀节点？AC 中 outputs 挂在终止节点，同串唯一 id）
}

type acLit struct {
	s string
}

// Index 构建后不可变，可并发使用；每次匹配的临时态在调用栈内。
type Index struct {
	root  *acNode
	lits  []acLit
	litID map[string]int
	pats  []Parsed
	// tokLit[pi][partIdx][tokIdx] = 字面量 id 或 -1（'^'/空）
	tokLit [][][]int
	// firstLit[pi] = 模式首段首个字面量的 id（-1 = 非字面量首段）
	firstLit []int
	// litFirstPats[id] = 以字面量 id 为首段字面量的模式列表（倒排）
	litFirstPats [][]int32
	// nonLitFirst = 非字面量首段模式（^开头/空段）——始终参与匹配
	nonLitFirst []int32
}

// NewIndex 构建索引。
func NewIndex(patterns []string) *Index {
	ix := &Index{
		root:   &acNode{children: map[byte]*acNode{}},
		litID:  map[string]int{},
		pats:   make([]Parsed, len(patterns)),
		tokLit: make([][][]int, len(patterns)),
	}
	for pi, pat := range patterns {
		pp := Parse(pat)
		ix.pats[pi] = pp
		parts := make([][]int, len(pp.Parts))
		for ki := range pp.Parts {
			part := &pp.Parts[ki]
			ids := make([]int, len(part.Lits))
			for ti, lit := range part.Lits {
				if part.Seps[ti] || lit == "" {
					ids[ti] = -1
					continue
				}
				ids[ti] = ix.intern(lit)
			}
			parts[ki] = ids
		}
		ix.tokLit[pi] = parts
	}
	// 倒排：首段字面量 → 模式列表
	ix.firstLit = make([]int, len(ix.pats))
	firstOwners := map[int][]int32{}
	var nonLitFirst []int32 // 非字面量首段模式（^开头/空段）——始终参与匹配
	for pi := range ix.pats {
		fl := -1
		if len(ix.tokLit[pi]) > 0 && len(ix.tokLit[pi][0]) > 0 && ix.tokLit[pi][0][0] >= 0 {
			fl = ix.tokLit[pi][0][0]
			firstOwners[fl] = append(firstOwners[fl], int32(pi))
		} else if !ix.pats[pi].Rejected {
			nonLitFirst = append(nonLitFirst, int32(pi))
		}
		ix.firstLit[pi] = fl
	}
	ix.litFirstPats = make([][]int32, len(ix.lits))
	for id, ps := range firstOwners {
		ix.litFirstPats[id] = ps
	}
	ix.nonLitFirst = nonLitFirst
	ix.buildFail()
	return ix
}

func (ix *Index) intern(s string) int {
	if id, ok := ix.litID[s]; ok {
		return id
	}
	id := len(ix.lits)
	ix.lits = append(ix.lits, acLit{s: s})
	ix.litID[s] = id
	n := ix.root
	for i := 0; i < len(s); i++ {
		c := s[i]
		ch, ok := n.children[c]
		if !ok {
			ch = &acNode{children: map[byte]*acNode{}}
			n.children[c] = ch
		}
		n = ch
	}
	n.outs = append(n.outs, id)
	return id
}

func (ix *Index) buildFail() {
	q := []*acNode{}
	for _, c := range ix.root.children {
		c.fail = ix.root
		q = append(q, c)
	}
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		for b, ch := range n.children {
			f := n.fail
			for {
				if fc, ok := f.children[b]; ok && fc != ch {
					ch.fail = fc
					break
				}
				if f == ix.root {
					ch.fail = ix.root
					break
				}
				f = f.fail
			}
			q = append(q, ch)
		}
	}
}

// scan 单遍扫描，返回命中字面量的位置表：id → 升序位置。
// 只为命中的 id 分配（map 规模 = 命中数，典型几十）。
func (ix *Index) scan(url string) map[int][]int32 {
	hits := map[int][]int32{}
	n := ix.root
	for i := 0; i < len(url); i++ {
		c := url[i]
		for {
			if ch, ok := n.children[c]; ok {
				n = ch
				break
			}
			if n == ix.root {
				break
			}
			n = n.fail
		}
		for m := n; m != ix.root; m = m.fail {
			for _, id := range m.outs {
				hits[id] = append(hits[id], int32(i-len(ix.lits[id].s)+1))
			}
		}
	}
	return hits
}

// MatchIndex 判定 patterns[pi] 是否匹配 url。语义 = Match(Parse(pattern), url)。
func (ix *Index) MatchIndex(pi int, url string) bool {
	pp := ix.pats[pi]
	if pp.Rejected {
		return false
	}
	hits := ix.scan(url)
	al := markerAligned(url)
	tokL := ix.tokLit[pi]

	// 首段首 token 为字面量：起点候选 = 该字面量出现位 ∩ 对齐位（严格必要条件）
	if len(pp.Parts) > 0 && len(pp.Parts[0].Lits) > 0 && !pp.Parts[0].Seps[0] {
		id := tokL[0][0]
		for _, p32 := range hits[id] {
			v := int(p32)
			if v <= len(url) && al[v] && ix.anchoredMatchIdx(pi, url, v, hits) {
				return true
			}
		}
		return false
	}
	for v := 0; v <= len(url); v++ {
		if !al[v] {
			continue
		}
		if ix.anchoredMatchIdx(pi, url, v, hits) {
			return true
		}
	}
	return false
}

// anchoredMatchIdx 是 anchoredMatch 的索引版：段间跳转用 AC 出现位表（二分推进），
// 段内 token 仍前缀直查。语义与 anchoredMatch 一致。
func (ix *Index) anchoredMatchIdx(pi int, url string, v int, hits map[int][]int32) bool {
	cur := v
	pp := ix.pats[pi]
	tokL := ix.tokLit[pi]
	for ki := 0; ki < len(pp.Parts); ki++ {
		part := &pp.Parts[ki]
		if ki > 0 {
			if len(part.Lits) == 0 {
				// 空段零宽
			} else if part.Seps[0] {
				found := -1
				for i := cur; i <= len(url); i++ {
					if i == len(url) {
						found = i
						break
					}
					if isSep(url[i]) {
						found = i
						break
					}
				}
				if found < 0 {
					return false
				}
				cur = found
			} else if lit := part.Lits[0]; lit != "" {
				id := tokL[ki][0]
				nxt := firstGe32(hits[id], cur)
				if nxt < 0 {
					return false
				}
				cur = nxt
			} else {
				// 空 token：cur 不动
			}
		}
		for ti := range part.Lits {
			lit := part.Lits[ti]
			if lit != "" {
				if !strings.HasPrefix(url[cur:], lit) {
					return false
				}
				cur += len(lit)
			}
			if part.Seps[ti] {
				if cur < len(url) {
					if !isSep(url[cur]) {
						return false
					}
					cur++
				}
			}
		}
	}
	return true
}

func firstGe32(arr []int32, v int) int {
	i := sort.Search(len(arr), func(j int) bool { return int(arr[j]) >= v })
	if i >= len(arr) {
		return -1
	}
	return int(arr[i])
}

// MatchAll 批量判定：一次 scan 供全部模式共享（引擎每 URL 调一次）。
// 返回命中模式下标（升序）。语义 = 逐个 MatchIndex。
func (ix *Index) MatchAll(url string) []int {
	hits := ix.scan(url)
	al := markerAligned(url)
	var out []int
	// 倒排预筛：仅「首段字面量在 URL 有出现」的模式进入归并；
	// 非字面量首段（^开头/空段）模式始终参与（数量极少）。
	var cands []int32
	seen := map[int]bool{}
	for id := range hits {
		for _, p32 := range ix.litFirstPats[id] {
			pi := int(p32)
			if !seen[pi] {
				seen[pi] = true
				cands = append(cands, p32)
			}
		}
	}
	cands = append(cands, ix.nonLitFirst...)
	sort.Slice(cands, func(a, b int) bool { return cands[a] < cands[b] })
	for _, p32 := range cands {
		pi := int(p32)
		pp := &ix.pats[pi]
		if pp.Rejected {
			continue
		}
		tokL := ix.tokLit[pi]
		matched := false
		if len(pp.Parts) > 0 && len(pp.Parts[0].Lits) > 0 && !pp.Parts[0].Seps[0] {
			id := tokL[0][0]
			for _, p32b := range hits[id] {
				v := int(p32b)
				if v <= len(url) && al[v] && ix.anchoredMatchIdx(pi, url, v, hits) {
					matched = true
					break
				}
			}
		} else {
			for v := 0; v <= len(url); v++ {
				if !al[v] {
					continue
				}
				if ix.anchoredMatchIdx(pi, url, v, hits) {
					matched = true
					break
				}
			}
		}
		if matched {
			out = append(out, pi)
		}
	}
	return out
}
