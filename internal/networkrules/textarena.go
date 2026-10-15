package networkrules

import (
	"unsafe"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

// textArena 是装载完成后的 RawRule 文本紧凑字节区（R5 碎片化探针，
// 2026-10-15）：装载期每条规则一个独立小字符串对象（56.6 万个），与
// 装载期垃圾交错散布在堆 span 里——E1 实测 live 130.7MB 而 heapSys
// 303.8MB（173MB span 碎片保留，FreeOSMemory 实测无效）。本结构把全部
// RawRule 搬进大块字节区并重指，量化 span 返还边界。
//
// 安全性：RawRule 的全部生产消费者为只读（badfilter.go 精确文本比对、
// filter.go:625 dump、response.go:47 blockpage、frontend_events JSON、
// networkrules.go:477 错误消息——2026-10-15 消费者审计）；Go 字符串不可
// 变且按内容比较，搬移后语义零变化。chunk 字节经字符串指针钉住。
type textArena struct {
	chunks [][]byte
	cur    []byte
}

const textArenaChunkBytes = 1 << 20 // 1 MiB

func (a *textArena) put(s string) string {
	n := len(s)
	if n == 0 {
		return ""
	}
	if cap(a.cur)-len(a.cur) < n {
		sz := textArenaChunkBytes
		if n > sz {
			sz = n
		}
		a.cur = make([]byte, 0, sz)
		a.chunks = append(a.chunks, a.cur[:cap(a.cur)])
	}
	// 头部 bump：从 chunk 头分配，cur 只增长不缩短（&s[off] 恒在界内）
	off := len(a.cur)
	a.cur = a.cur[:off+n]
	copy(a.cur[off:], s)
	return unsafe.String(&a.cur[off], n)
}

// CompactTextArena 把全部已装载规则的 RawRule 文本搬进紧凑字节区并重指。
// 必须在 Compact 之后、开始服务之前调用（装载写已全部完成）。
// 返回覆盖的规则文本数与字节区字节数（含 chunk 圆整）。
func (nr *NetworkRules) CompactTextArena() (covered int, arenaBytes int) {
	var ta textArena
	saw := func(p *string) {
		if p == nil || *p == "" {
			return
		}
		*p = ta.put(*p)
		covered++
	}
	// primary 树 + generic + regexp + hostRules
	nr.primaryStore.tree.Walk(func(r *rule.Rule) { saw(&r.RawRule) })
	for i := range nr.primaryStore.generic {
		saw(&nr.primaryStore.generic[i].RawRule)
	}
	for i := range nr.primaryStore.regexp {
		saw(&nr.primaryStore.regexp[i].value.RawRule)
	}
	nr.primaryStore.hostMu.RLock()
	for _, rs := range nr.primaryStore.hostRules {
		for i := range rs {
			saw(&rs[i].RawRule)
		}
	}
	nr.primaryStore.hostMu.RUnlock()
	// exception 树 + 同构桶（ExceptionRule 内嵌 rule.Rule，RawRule 为提升字段）
	nr.exceptionStore.tree.Walk(func(er *exceptionrule.ExceptionRule) { saw(&er.RawRule) })
	for i := range nr.exceptionStore.generic {
		saw(&nr.exceptionStore.generic[i].RawRule)
	}
	for i := range nr.exceptionStore.regexp {
		saw(&nr.exceptionStore.regexp[i].value.RawRule)
	}
	nr.exceptionStore.hostMu.RLock()
	for _, rs := range nr.exceptionStore.hostRules {
		for i := range rs {
			saw(&rs[i].RawRule)
		}
	}
	nr.exceptionStore.hostMu.RUnlock()
	for _, ch := range ta.chunks {
		arenaBytes += len(ch)
	}
	// 钉住 chunks：chunks 切片本身被本函数的 ta 丢弃后，chunk 字节由
	// 各字符串的指针引用，不会回收——无需额外字段。
	return covered, arenaBytes
}
