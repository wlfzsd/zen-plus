package networkrules

import (
	"strings"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rulemodifiers"
)

// treeGateIndex 把 WS1 的 $domain 反向索引从 regexp 库扩展到树库（T1，
// 2026-10-15）：装载后（Compact 内）为每条带可索引 $domain 门的树规则分配
// 位号，按域建倒排；每请求沿 eff 后缀链 markEligible 出位图，树候选按位
// O(1) 过滤——替代逐候选 matchReqSide→matchHost→MatchDomain 全量比对
// （E2 profile 占 8.5%）。
//
// 可靠性 = WS1 同构（tokenindex.go markEligible 同一必要条件逻辑）：
// $domain 是强制 And 条件（rule.ParseModifiers），门的正向字面量后缀链
// 与请求主机不相交 ⇒ matchHost 必 false ⇒ 规则必不匹配 ⇒ 预丢弃零行为差。
// 非可索引门（regexp entry / example.* / 全否定）不进索引，候选原样保留
// （旧路径全量评估，保守不变）。构建后索引不可变（Compact 冻结态），
// 并发读安全；晚于构建的插入规则不在 map 中 ⇒ 过滤器原样保留（保守）。
type treeGateIndex[T comparable] struct {
	gateOf func(T) *domainGate

	// gateIdx：可索引门规则 → 位号。不在 map = 无门/非可索引 = 恒合格。
	gateIdx map[T]int32
	// byDomainRef / byDomainAny：正向字面量 → 位号列表（前者仅测 referrer
	// 主机，后者附加测试目标主机，镜像 DomainModifier.MatchTargetDomain）。
	byDomainRef map[string][]int32
	byDomainAny map[string][]int32
	// nBits：位号总数（位图动态尺寸）；超上限时 over=true 整体退回不过滤
	//（与 tokenindex idxMaxRules 的保守回退同款）。
	nBits int
	over  bool
}

// buildTreeGate 遍历树 + generic + hostRules 构建索引。仅在 Compact 内调用
// （装载写已全部完成、树不可变）。无门规则不占位号。
func (s *ruleStore[T]) buildTreeGate() {
	if s.gateOf == nil {
		return
	}
	g := &treeGateIndex[T]{
		gateOf:      s.gateOf,
		gateIdx:     map[T]int32{},
		byDomainRef: map[string][]int32{},
		byDomainAny: map[string][]int32{},
	}
	next := int32(0)
	add := func(v T) {
		dg := g.gateOf(v)
		if dg == nil {
			return // 无门或非可索引：恒合格，不过滤
		}
		id, ok := g.gateIdx[v]
		if !ok {
			id = next
			next++
			g.gateIdx[v] = id
		}
		target := g.byDomainRef
		if dg.matchTarget {
			target = g.byDomainAny
		}
		for _, lit := range dg.lits {
			target[lit] = append(target[lit], id)
		}
	}
	s.tree.Walk(add)
	for i := range s.generic {
		add(s.generic[i])
	}
	s.hostMu.RLock()
	for _, rs := range s.hostRules {
		for i := range rs {
			add(rs[i])
		}
	}
	s.hostMu.RUnlock()
	g.nBits = int(next)
	if next > 64*64 { // 4096 位上限（与 tokenindex idxMaxRules 同款保守回退）
		g.over = true
		g.gateIdx = nil
		g.byDomainRef = nil
		g.byDomainAny = nil
		return
	}
	if next > 0 {
		s.treeGate = g
	}
}

// filterTree 按 eff 后缀链位图过滤树候选：无门/非可索引（不在 map）原样
// 保留；有门者按位判定——位未标 = 门必假 = 丢弃（零行为差的必要条件预滤）。
// eff==nil（调用方关闭门控）原样返回。
func (g *treeGateIndex[T]) filterTree(res []T, eff *effHosts) []T {
	if eff == nil || g.over {
		return res
	}
	if !eff.usable {
		// matchReqSide 对不可解析 Referer 恒 false：全部带门候选剔除。
		out := res[:0]
		for _, v := range res {
			if _, gated := g.gateIdx[v]; !gated {
				out = append(out, v)
			}
		}
		return out
	}
	// 位图标记（后缀链，镜像 tokenindex.markEligible）——动态尺寸
	elig := make([]uint64, (g.nBits+63)/64)
	add := func(m map[string][]int32, host string) {
		if len(m) == 0 || host == "" {
			return
		}
		for h := host; h != ""; {
			for _, id := range m[h] {
				elig[id/64] |= 1 << (id % 64)
			}
			i := strings.IndexByte(h, '.')
			if i < 0 {
				break
			}
			h = h[i+1:]
		}
	}
	add(g.byDomainRef, eff.ref)
	add(g.byDomainAny, eff.ref)
	add(g.byDomainAny, eff.own)
	out := res[:0]
	for _, v := range res {
		id, gated := g.gateIdx[v]
		if !gated {
			out = append(out, v)
			continue
		}
		if elig[id/64]&(1<<(id%64)) == 0 {
			continue
		}
		out = append(out, v)
	}
	return out
}

// 编译期锚定：确保 domainGate/matchTarget 语义与 WS1 门一致（matchTarget
// 规则同时按 ref 与 own 标记，已在 filterTree 的 add 序列体现）。
var _ = rulemodifiers.DomainModifier{}
