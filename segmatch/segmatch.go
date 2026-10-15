package segmatch

// [引擎 v2 · P2 核心件 1] 段匹配器：含 * / ^ 模式的精确语义实现。
//
// 语义目标 = ruletree 对 root 子树模式的判定：
//   match ⟺ ∃ v ∈ {0} ∪ markerPositions(url): url[v:] 以该模式为前缀（glob 式）
//   其中 '*' = 任意串（含空），'^' = 恰一个分隔符字符，或地址末尾的零宽匹配。
//   分隔符字符 = 非字母/数字/_-.%（tokenize.go:10-12 注释口径）。
//
// 本文件先作为独立可差分验证的纯函数：以 ruletree 为甲骨文逐对对拍，
// 语义逐位对齐后才会并入引擎候选层。

import "strings"

// Part 是按 '*' 切开的连续段；段内是字面量与 '^' 的交替序列。
type Part struct {
	Lits []string // 字面量片段（与 Seps 交错；Lits[i] 后跟 Seps[i]）
	Seps []bool   // true=该位置是 '^'
}

// Parsed 是一条解析好的段模式。
type Parsed struct {
	Parts    []Part
	Rejected bool // 与 ruletree.Insert 的拒绝条件一致（位置>0 出现 "||"）
}

// Parse 把模式解析为段结构。与 tokenize.go 的 token 语义对齐：
// '*' → 通配段界，'^' → 分隔符标记，'|' 锚定（本类模式不应含 '|'，出现即按树口径处理）。
func Parse(pattern string) Parsed {
	var p Parsed
	// ruletree.Insert 拒绝条件：token[0] 之后还存在 "||"（tokenDomainBoundary）
	if strings.Contains(pattern[mini(1, len(pattern)):], "||") {
		p.Rejected = true
		return p
	}
	cur := Part{}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			cur.Lits = append(cur.Lits, lit.String())
			cur.Seps = append(cur.Seps, false)
			lit.Reset()
		}
	}
	endPart := func() {
		flush()
		p.Parts = append(p.Parts, cur)
		cur = Part{}
	}
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			endPart()
		case '^':
			flush()
			cur.Lits = append(cur.Lits, "")
			cur.Seps = append(cur.Seps, true)
		default:
			lit.WriteByte(pattern[i])
		}
	}
	endPart()
	return p
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// isSep 与 ruletree tokenSeparator 口径一致：非字母/数字/_-.% 即分隔符。
func isSep(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return false
	case c == '_', c == '-', c == '.', c == '%':
		return false
	}
	return true
}

// isMarker 与 ruletree 的 traversalMarkers 逐字对齐（ruletree.go:528）。
// 注意：这与 tokenSeparator 口径不同（isSep 排除 ._-%，marker 却包含它们）——
// 两套字符集在树里各司其职（marker=visit 起点，sep='^' 的匹配字符），
// 差分测试证实不可混用。
func isMarker(c byte) bool {
	return strings.IndexByte("-._~:/?#[]@!$&'()*+,;%=", c) >= 0
}

// markerAligned 预计算 url 的合法起点位图。
// GetLP 的 visit 序列（ruletree.go:394-403）：{0} ∪ {i: url[i] 是 marker}
// ∪ {i+1: url[i] 是 marker（traverseNext 补一位）}。
func markerAligned(url string) []bool {
	m := make([]bool, len(url)+1)
	m[0] = true
	for i := 1; i < len(url); i++ {
		if isMarker(url[i]) {
			m[i] = true
			if i+1 <= len(url) {
				m[i+1] = true
			}
		}
	}
	return m
}

// Match 判定模式是否匹配 url（root 子树语义：标记对齐起点 + 前缀式 glob）。
// 性能化：首段字面量模式只在「首字面量出现位 ∩ 对齐位」尝试（严格必要条件，
// 不漏——任何成功匹配的 v 必须 url[v:] 以首字面量开头）；其余模式回退全对齐位。
func Match(p Parsed, url string) bool {
	if p.Rejected {
		return false
	}
	al := markerAligned(url)
	if len(p.Parts) > 0 && len(p.Parts[0].Lits) > 0 && !p.Parts[0].Seps[0] {
		lit := p.Parts[0].Lits[0]
		for i := 0; i+len(lit) <= len(url); {
			j := strings.Index(url[i:], lit)
			if j < 0 {
				break
			}
			v := i + j
			if v > len(url) {
				break
			}
			if al[v] && anchoredMatch(p, url, v) {
				return true
			}
			i = v + 1
		}
		return false
	}
	for v := 0; v <= len(url); v++ {
		if !al[v] {
			continue
		}
		if anchoredMatch(p, url, v) {
			return true
		}
	}
	return false
}

// anchoredMatch 判定 url[v:] 是否以模式为前缀（* = 任意间隙，^ = 单分隔符/末尾零宽）。
func anchoredMatch(p Parsed, url string, v int) bool {
	cur := v
	for pi := 0; pi < len(p.Parts); pi++ {
		part := &p.Parts[pi]
		if pi > 0 {
			// '*' 间隙：贪婪找下一段的最早可行起点。
			// 下一 token：字面量 → Index；'^' → 下一个分隔符位。
			nxt := firstTokenStart(part, url, cur)
			if nxt < 0 {
				return false
			}
			cur = nxt
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
				// '^'：恰一个分隔符字符，或地址末尾零宽。
				if cur < len(url) {
					if !isSep(url[cur]) {
						return false
					}
					cur++
				}
				// cur == len(url)：零宽成立，继续
			}
		}
	}
	return true
}

// firstTokenStart 计算段 part 的首个 token 从 url 的哪个 ≥from 位置开始匹配。
// 空段（纯 '*' 边界产物）零宽成立。
func firstTokenStart(part *Part, url string, from int) int {
	if len(part.Lits) == 0 {
		return from
	}
	if part.Seps[0] {
		// '^' 开头：下一个分隔符位（或末尾零宽——末尾也可，但后续 token 须为空）
		for i := from; i <= len(url); i++ {
			if i == len(url) {
				return i
			}
			if isSep(url[i]) {
				return i
			}
		}
		return -1
	}
	lit := part.Lits[0]
	if lit == "" {
		return from
	}
	i := strings.Index(url[from:], lit)
	if i < 0 {
		return -1
	}
	return from + i
}

// SegSafe 报告模式是否适合进入段自动机（引擎分流用，2026-10-15）。
// 判定：无 ||/| 锚定，且首段（第一个 '*' 或 '^' 之前）含至少一个字面量字符
// ——前导通配/空首段模式的段编码语义有偏差（差分 14 条实锤），回退树匹配。
func SegSafe(pattern string) bool {
	if strings.ContainsAny(pattern, "|") {
		return false
	}
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			return false // 首段内出现通配 → 首段无字面量前缀
		case '^':
			return false // 首段内出现分隔符 → 同上
		}
	}
	return true
}
