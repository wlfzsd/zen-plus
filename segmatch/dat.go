package segmatch

// [引擎 v2 · T1] 双数组 AC 自动机（DAT-AC）。
//   base/check 扁平 int32 数组，转移 = base[cur]+c 的 O(1) 算术寻址；
//   fail 独立 int32 数组（构建期 BFS 保证 fail 目标先就位）；
//   输出扁平化（构建期把 fail 链输出合并进各状态区间）→ 扫描零 map 零分配。
//
// 语义 = Index/Match（同一差分门禁：segmatch 甲骨文对拍 diffs=0 才算数）。
import (
	"log"
	"sort"
	"strings"
	"sync"
)

type datBuildNode struct {
	children map[byte]int32 // byte -> build 节点下标
	fail     int32
	outs     []int32 // 自身终止的字面量 id
	merged   []int32 // 自身 + fail 链输出（构建期合并）
	depth    int32
}

// DAT 冻结后的双数组自动机。
type DAT struct {
	base    []int32
	check   []int32
	fail    []int32
	depth   []int32 // 状态深度（根=0）——fail 链输出的实际起始位 = i+1-depth[cur]
	outOff  []int32
	outLen  []int32
	outputs []int32
}

// DATIndex = 两大类专用结构之 F 类终态：DAT-AC + 紧凑编码段表。
// 2026-10-15 T4a：pats/tokLit 结构化缓存（实测 ~150MB/24 万模式）替换为
// 扁平 int32 编码（enc/patEncOff）——每模式 [nParts, (nTok, code...)...]，
// code>=0 = 字面量 id，code==-1 = '^' 分隔符。nParts==0 = rejected 占位。
type DATIndex struct {
	dat          DAT
	lits         []string
	patEncOff    []int32 // 每模式在 enc 中的起点（len = npats+1）
	enc          []int32
	rejected     []bool
	firstLit     []int
	litFirstPats [][]int32
	nonLitFirst  []int32
}

// NewDATIndex：patterns = 全部 root 类模式（与 NewIndex 同入参）。
func NewDATIndex(patterns []string) *DATIndex {
	// ---- 构建期：map 版 trie ----
	nodes := []datBuildNode{{children: map[byte]int32{}}}
	litID := map[string]int{}
	var lits []string
	intern := func(s string) int32 {
		if id, ok := litID[s]; ok {
			return int32(id)
		}
		id := int32(len(lits))
		lits = append(lits, s)
		litID[s] = int(id)
		return id
	}
	di := &DATIndex{patEncOff: make([]int32, len(patterns)+1), rejected: make([]bool, len(patterns))}
	firstOwners := map[int][]int32{}
	di.firstLit = make([]int, len(patterns))
	var nonLit []int32
	var enc []int32
	for pi, pat := range patterns {
		pp := Parse(pat)
		di.rejected[pi] = pp.Rejected
		di.patEncOff[pi] = int32(len(enc))
		if pp.Rejected {
			// rejected 占位：nParts=0，匹配恒 false
			enc = append(enc, 0)
			continue
		}
		enc = append(enc, int32(len(pp.Parts)))
		parts := make([][]int, len(pp.Parts))
		// 每段从 root 独立插入（2026-10-15 修复）：段间若沿用上一段终点节点，
		// 会把「前段尾+后段头」拼成 trie 假边（如 "://…" 的 ":/" 出现在
		// 非 ":/" 开头模式），AC 沿假边产生幽灵事件——map 版无此边，
		// 519 个差分差异全部源于此。
		for ki := range pp.Parts {
			part := &pp.Parts[ki]
			ids := make([]int, len(part.Lits))
			for ti, lit := range part.Lits {
				if part.Seps[ti] || lit == "" {
					ids[ti] = -1
					continue
				}
				ids[ti] = int(intern(lit))
				// 该字面量从 root 独立串入 trie
				n := int32(0)
				for x := 0; x < len(lit); x++ {
					c := lit[x]
					ch, ok := nodes[n].children[c]
					if !ok {
						nodes = append(nodes, datBuildNode{children: map[byte]int32{}, depth: nodes[n].depth + 1})
						ch = int32(len(nodes) - 1)
						nodes[n].children[c] = ch
					}
					n = ch
				}
				nodes[n].outs = append(nodes[n].outs, int32(ids[ti]))
			}
			parts[ki] = ids
			// T4a 紧凑编码：本段 = [nTok, code...]（nTok 头在 token 前）
			enc = append(enc, int32(len(ids)))
			for _, id := range ids {
				enc = append(enc, int32(id))
			}
		}
		fl := -1
		if len(parts) > 0 && len(parts[0]) > 0 && parts[0][0] >= 0 {
			fl = parts[0][0]
			firstOwners[fl] = append(firstOwners[fl], int32(pi))
		} else if !pp.Rejected {
			nonLit = append(nonLit, int32(pi))
		}
		di.firstLit[pi] = fl
	}
	di.lits = lits
	di.enc = enc
	di.patEncOff[len(patterns)] = int32(len(enc))
	// T4a：结构化缓存使命完成（enc 已序列化），释放引用供 GC 回收
	di.litFirstPats = make([][]int32, len(lits))
	for id, ps := range firstOwners {
		di.litFirstPats[id] = ps
	}
	di.nonLitFirst = nonLit

	// ---- fail 计算（BFS，map 版）；bfsOrder = 处理序（fail 目标必先出现）----
	root := int32(0)
	nodes[root].fail = root
	queue := make([]int32, 0, 64)
	bfsOrder := []int32{root}
	for _, ch := range nodes[root].children {
		nodes[ch].fail = root
		queue = append(queue, ch)
		bfsOrder = append(bfsOrder, ch)
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for c, ch := range nodes[n].children {
			f := nodes[n].fail
			for {
				if fc, ok := nodes[f].children[c]; ok && fc != ch {
					nodes[ch].fail = fc
					break
				}
				if f == root {
					nodes[ch].fail = root
					break
				}
				f = nodes[f].fail
			}
			queue = append(queue, ch)
			bfsOrder = append(bfsOrder, ch)
		}
	}
	// 输出合并：merged[ch] = outs[ch] + merged[fail[ch]]（后缀链！）。
	// 2026-10-15 修复：此前误用 merged[父]（前缀链）——":/" 态因此不含
	// fail 目标 "/" 节点的输出，AC 事件缺报。BFS 序下 fail 深度更小、
	// 已先算好，直接按 fail 依赖合并。
	for _, ni := range bfsOrder {
		if ni == root {
			nodes[ni].merged = append([]int32{}, nodes[ni].outs...)
			continue
		}
		nodes[ni].merged = append(append([]int32{}, nodes[ni].outs...), nodes[nodes[ni].fail].merged...)
	}

	// ---- DAT 排布（空槽双链表密集版，2026-10-15）----
	// 修复：旧实现 nextFree 每状态 +256 单调推进且无空槽回收 → 35 万状态
	// 膨胀到 1.6 亿槽（live 20.2GB）。改为空槽双链表：占用槽摘链、空闲槽
	// 沿链 first-fit，排布密度 ≈1.2× 状态数。
	d := DAT{}
	cap0 := int32(len(nodes))*2 + 1024
	d.base = make([]int32, cap0)
	d.check = make([]int32, cap0)
	d.fail = make([]int32, cap0)
	d.depth = make([]int32, cap0)
	d.outOff = make([]int32, cap0)
	d.outLen = make([]int32, cap0)
	freeNext := make([]int32, cap0)
	freePrev := make([]int32, cap0)
	for i := int32(0); i < cap0; i++ {
		freeNext[i] = i + 1
		if i > 0 {
			freePrev[i] = i - 1
		} else {
			freePrev[i] = -1
		}
	}
	freeNext[cap0-1] = -1
	freeHead := int32(0)
	// root 占槽 0（check=-2 占用标记，区别于空位 -1）
	freeHead = 1
	if cap0 > 1 {
		freePrev[1] = -1
	}
	d.check[0] = -2

	datState := make([]int32, len(nodes)) // build idx -> dat state
	datState[root] = root

	growTo := func(sz int32) {
		for int32(len(d.check)) <= sz {
			d.base = append(d.base, 0)
			d.check = append(d.check, -1)
			d.fail = append(d.fail, 0)
			d.depth = append(d.depth, 0)
			d.outOff = append(d.outOff, -1)
			d.outLen = append(d.outLen, 0)
			t := int32(len(d.check) - 1)
			// 空槽链同步扩容
			for int32(len(freeNext)) <= t {
				freeNext = append(freeNext, -1)
				freePrev = append(freePrev, -1)
			}
			freeNext[t] = freeHead
			freePrev[t] = -1
			if freeHead >= 0 {
				freePrev[freeHead] = t
			}
			freeHead = t
		}
	}
	removeSlot := func(t int32) {
		p, n := freePrev[t], freeNext[t]
		if p >= 0 {
			freeNext[p] = n
		} else {
			freeHead = n
		}
		if n >= 0 {
			freePrev[n] = p
		}
		freePrev[t] = -1
		freeNext[t] = -1
	}
	setOut := func(state int32, outs []int32) {
		if len(outs) == 0 {
			return
		}
		d.outOff[state] = int32(len(d.outputs))
		d.outLen[state] = int32(len(outs))
		d.outputs = append(d.outputs, outs...)
	}
	setOut(root, nodes[root].merged)
	// BFS 队列：build 节点 idx
	bq := []int32{root}
	for len(bq) > 0 {
		ni := bq[0]
		bq = bq[1:]
		st := datState[ni]
		nd := &nodes[ni]
		if len(nd.children) == 0 {
			continue
		}
		keys := make([]int, 0, len(nd.children))
		for c := range nd.children {
			keys = append(keys, int(c))
		}
		sort.Ints(keys)
		minC := int32(keys[0])
		maxC := int32(keys[len(keys)-1])
		// 沿空槽链 first-fit：base = 空槽 - minC，验证全部 key 槽空
		placed := false
		for slot := freeHead; slot != -1; {
			base := slot - minC
			if base < 0 {
				slot = freeNext[slot]
				continue
			}
			growTo(base + maxC)
			ok := true
			for _, kc := range keys {
				if d.check[base+int32(kc)] != -1 {
					ok = false
					break
				}
			}
			if !ok {
				slot = freeNext[slot]
				continue
			}
			d.base[st] = base
			for _, kc := range keys {
				c := byte(kc)
				t := base + int32(kc)
				removeSlot(t)
				d.check[t] = st
				ci := nd.children[c]
				datState[ci] = t
				d.depth[t] = nodes[ci].depth
				// fail：沿父 fail 链找 c 的转移
				f := nodes[ni].fail
				ft := int32(-1)
				for {
					if f == root {
						// root 有 c 子边则指过去，否则留 root
						if rc, ok2 := nodes[root].children[c]; ok2 && rc != ci {
							ft = datState[rc]
						} else {
							ft = root
						}
						break
					}
					if fc, ok2 := nodes[f].children[c]; ok2 {
						ft = datState[fc]
						break
					}
					f = nodes[f].fail
				}
				d.fail[t] = ft
				setOut(t, nodes[ci].merged)
				bq = append(bq, ci)
			}
			placed = true
			break
		}
		if !placed {
			// 空槽链耗尽：扩容重试（growTo 预挂新空槽到链头）
			growTo(int32(len(d.check)) + maxC + 1024)
			bq = append([]int32{ni}, bq...) // 本节点重排
		}
	}
	di.dat = d
	return di
}

// ScanEvents：单遍扫描，输出事件 (litID, endPos)——endPos 升序。
// 零分配（emit 回调直接消费）。
func (di *DATIndex) ScanEvents(url string, emit func(litID int32, endPos int32)) {
	d := &di.dat
	cur := int32(0)
	for i := 0; i < len(url); i++ {
		c := url[i]
		for {
			t := d.base[cur] + int32(c)
			if int(t) < len(d.check) && d.check[t] == cur {
				cur = t
				break
			}
			if cur == 0 {
				break
			}
			cur = d.fail[cur]
		}
		if d.outLen[cur] > 0 {
			off := d.outOff[cur]
			for k := int32(0); k < d.outLen[cur]; k++ {
				id := d.outputs[off+k]
				// [诊断] datEnv=1 时打印 http 字面量的触发点（对照 Match 差异）
				if datTraceLit >= 0 && id == datTraceLit {
					log.Printf("[DAT-EMIT] lit=%q i=%d depth=%d", di.lits[id], i, d.depth[cur])
				}
				emit(id, int32(i))
			}
		}
	}
}

// datTraceLit 诊断开关：≥0 时 ScanEvents 打印该字面量 id 的全部触发点。
var datTraceLit = int32(-1)

// datScratch 是 MatchAll 的可复用扫描缓冲（零每 URL 分配目标）。
// hits 按 litID 平铺索引：litHits[id] = 升序 start 列表；touched 记录
// 本轮命中的 id（扫后按 touched 重置，避免全量清零）。
type datScratch struct {
	litHits [][]int32
	touched []int32
	cands   []int32
	seen    []int32 // 命中过的候选模式（线性查重，候选量小）
	out     []int
}

var datScratchPool = sync.Pool{New: func() any { return new(datScratch) }}

// reset 清空本轮使用痕迹（保留容量）。
func (sc *datScratch) reset() {
	for _, id := range sc.touched {
		sc.litHits[id] = sc.litHits[id][:0]
	}
	sc.touched = sc.touched[:0]
	sc.cands = sc.cands[:0]
	sc.seen = sc.seen[:0]
	sc.out = sc.out[:0]
}

// MatchAllDAT：DAT 版 MatchAll（语义 = Index.MatchAll）。
// 零分配热路径：扫描缓冲经 sync.Pool 复用，扫描写平铺数组。
func (di *DATIndex) MatchAll(url string) []int {
	sc := datScratchPool.Get().(*datScratch)
	if len(sc.litHits) < len(di.lits) {
		sc.litHits = make([][]int32, len(di.lits))
	}
	defer func() {
		sc.reset()
		datScratchPool.Put(sc)
	}()
	hits := sc.litHits
	d := &di.dat
	cur := int32(0)
	for i := 0; i < len(url); i++ {
		c := url[i]
		for {
			t := d.base[cur] + int32(c)
			if int(t) < len(d.check) && d.check[t] == cur {
				cur = t
				break
			}
			if cur == 0 {
				break
			}
			cur = d.fail[cur]
		}
		if d.outLen[cur] > 0 {
			off := d.outOff[cur]
			for k := int32(0); k < d.outLen[cur]; k++ {
				id := d.outputs[off+k]
				if len(hits[id]) == 0 {
					sc.touched = append(sc.touched, id)
				}
				hits[id] = append(hits[id], int32(i)-int32(len(di.lits[id]))+1)
			}
		}
	}
	al := markerAligned(url)
	// 倒排预筛：仅首段字面量有命中的模式参与归并（+非字面量首段模式）
	var cands []int32
	for _, id := range sc.touched {
		cands = append(cands, di.litFirstPats[id]...)
	}
	cands = append(cands, di.nonLitFirst...)
	// 去重+排序（cands 规模=命中模式数，典型几十；线性查重即可）
	sort.Slice(cands, func(a, b int) bool { return cands[a] < cands[b] })
	var out []int
	prev := int32(-1)
	for _, p32 := range cands {
		if p32 == prev {
			continue
		}
		prev = p32
		pi := int(p32)
		if di.rejected[pi] {
			continue
		}
		// 快速路径：首 token 为字面量 → 只在其出现位 ∩ 对齐位尝试
		fc := di.firstTokCode(pi)
		if fc >= 0 {
			matched := false
			for _, v32 := range hits[fc] {
				v := int(v32)
				if v <= len(url) && al[v] && di.matchOneEnc(pi, url, v, hits, al) {
					matched = true
					break
				}
			}
			if matched {
				out = append(out, pi)
			}
			continue
		}
		// 首段空/^：全对齐位尝试
		for v := 0; v <= len(url); v++ {
			if !al[v] {
				continue
			}
			if di.matchOneEnc(pi, url, v, hits, al) {
				out = append(out, pi)
				break
			}
		}
	}
	sc.out = append(sc.out[:0], out...)
	return sc.out
}

// MatchAll 保持旧签名兼容（内部走零分配路径，返回拷贝语义不变——
// 注意：返回值引用池化缓冲，调用方不得长期持有；引擎内每次匹配
// 消费后即弃，与 rulestore 的 putRes 纪律一致）。
func (di *DATIndex) MatchAllLegacy(url string) []int {
	res := di.MatchAll(url)
	cp := make([]int, len(res))
	copy(cp, res)
	return cp
}

// matchOneEnc：按紧凑编码归并（语义 = 旧 matchOne/anchored，差分同门禁）。
// 2026-10-15 修复：v 参数必须透传——T4a 重构时丢失导致从 cur=0 硬编码
// 起配（所有模式试图从 url 开头匹配），14 条失败模式全部源于此。
func (di *DATIndex) matchOneEnc(pi int, url string, v int, hits [][]int32, al []bool) bool {
	off := di.patEncOff[pi]
	nParts := int(di.enc[off])
	off++
	if nParts == 0 {
		return false // rejected 占位
	}
	cur := v
	for ki := 0; ki < nParts; ki++ {
		nTok := int(di.enc[off])
		off++
		if ki > 0 {
			if nTok == 0 {
				continue // 空段零宽
			}
			code := di.enc[off]
			if code == -1 {
				// '^' 开头：下一分隔符位（或末尾零宽）
				found := -1
				for p := cur; p <= len(url); p++ {
					if p == len(url) {
						found = p
						break
					}
					if isSep(url[p]) {
						found = p
						break
					}
				}
				if found < 0 {
					return false
				}
				cur = found
			} else {
				nxt := firstGe32(hits[code], cur)
				if nxt < 0 {
					return false
				}
				cur = nxt
			}
		}
		for ti := 0; ti < nTok; ti++ {
			code := di.enc[off]
			off++
			if code >= 0 {
				lit := di.lits[code]
				if !strings.HasPrefix(url[cur:], lit) {
					return false
				}
				cur += len(lit)
			} else {
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

// firstTokCode 返回模式首 token 的 code（-2 = rejected，-1 = '^'，≥0 = 字面量 id）。
func (di *DATIndex) firstTokCode(pi int) int32 {
	off := di.patEncOff[pi]
	if di.enc[off] == 0 {
		return -2 // rejected
	}
	nTok := di.enc[off+1]
	if nTok == 0 {
		return -2 // 空首段
	}
	return di.enc[off+2]
}
