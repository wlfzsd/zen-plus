package rulemodifiers

import (
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/irbis-sh/zen-desktop/internal/httprewrite"
	"github.com/spyzhov/ajson"
)

type JSONPruneModifier struct {
	// commands is a parsed sequence representing the JSONPath expression.
	commands []string
	// [P1 2026-10-08] raw is the normalized expression text, kept for the
	// apply-failure log (deduplicated per rule text).
	raw string
}

var _ ActionModifier = (*JSONPruneModifier)(nil)

var ErrInvalidJSONPruneModifier = errors.New("invalid jsonprune modifier")

func (m *JSONPruneModifier) Parse(modifier string) error {
	if !strings.HasPrefix(modifier, "jsonprune=") {
		return ErrInvalidJSONPruneModifier
	}
	raw := strings.TrimPrefix(modifier, "jsonprune=")
	raw = strings.TrimSpace(raw)

	if raw == "" {
		return ErrInvalidJSONPruneModifier
	}

	// [P1 2026-10-08] AdGuard 方言归一化：把 ajson 不认/误判的 AdGuard
	// jsonprune 形态改写为原生等价形态（幂等，处理不了的保持原样）。
	raw = normalizeAdGuardJSONPath(raw)

	commands, err := ajson.ParseJSONPath(raw)
	if err != nil {
		return fmt.Errorf("parse JSONPath: %w", err)
	}

	m.commands = commands
	m.raw = raw
	return nil
}

// ── [P1 2026-10-08] AdGuard jsonprune 方言归一化（comp_audit TOP-1）──────────
//
// AdGuard 过滤语法允许若干 jsonprune 表达式形态，而 ajson
// (github.com/spyzhov/ajson v0.9.6) 要么在 ParseJSONPath 直接拒绝，要么在
// ApplyJSONPath 误判（bracket 裸词经 tokenize 被 strings.ToLower，camelCase
// 键永不命中 → 未引号联合 hits=0）。此处做字符串级预处理，把「AdGuard 认、
// ajson 不认」的形态改写为 ajson 原生等价形态（映射均经探针逐形态实测）：
//
//	前导 \$（splitModifiers 保留的根转义）→ 剥为 $
//	$..[a, b]            → $..['a','b']        未引号联合加引号
//	?(has "P")/(has 'P') → ?(@P)               P 重锚当前节点：$..→@..、$.→@.、
//	?(has P)（裸词）      → ?(@.P)              @ 前缀路径已相对则原样
//	?(key-eq 'k' 'v')    → ?(@.k == 'v')
//	?(key-substr 'k' 'v')→ ?(@.k =~ ".*v.*")   v 按字面量 QuoteMeta
//
// 性质：①幂等——归一化输出再过一遍不变（引号/@ 前缀形态不会被再匹配）；
// ②保守——任何无法确信改写的形态原样返回，在下游以与改动前完全相同的方式失败。
func normalizeAdGuardJSONPath(raw string) string {
	// splitModifiers 只反转义 "\,"（parse.go），值内的 "\$" 原样保留；
	// JSONPath 里 "\" 非法，前导 "\$" 只能是根符号的规则级转义。
	if len(raw) >= 2 && raw[0] == '\\' && raw[1] == '$' {
		raw = raw[1:]
	}
	out := bareUnionBracketRe.ReplaceAllStringFunc(raw, quoteUnionElements)
	out = hasFilterRe.ReplaceAllStringFunc(out, rewriteHasFilter)
	out = keyFilterRe.ReplaceAllStringFunc(out, rewriteKeyFilter)
	return out
}

var (
	// bareUnionBracketRe 匹配内容不含括号/过滤器/引号/反斜杠的 [...] 组——
	// 未引号联合候选。已引号形态（含 ' 或 "）、过滤器 [?(...)]（含 ?）、
	// 切片转义（含 \）天然不匹配，保证幂等与零误伤。
	bareUnionBracketRe = regexp.MustCompile(`\[([^[\]?"'\\]*)\]`)

	// bareUnionKeyRe 校验单个联合元素是「对象键」而非索引/切片/通配：
	// 必须以字母或下划线开头（纯数字加引号会把数组索引变成键，禁止）。
	bareUnionKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

	// hasFilterRe 匹配 AdGuard has 存在性过滤器，路径三种写法：
	// 双引号 / 单引号 / 裸标识符（语料实测三种都存在）。
	hasFilterRe = regexp.MustCompile(`\?\(\s*has\s+(?:"([^"]*)"|'([^']*)'|([A-Za-z_][A-Za-z0-9_.-]*))\s*\)`)

	// keyFilterRe 匹配 key-eq / key-substr 过滤器，键值均为引号字符串
	//（AdGuard 文档形态；裸值如 key-eq "k" true 保守跳过，保持原样）。
	keyFilterRe = regexp.MustCompile(`\?\(\s*(key-eq|key-substr)\s+(?:"([^"]*)"|'([^']*)')\s+(?:"([^"]*)"|'([^']*)')\s*\)`)
)

// quoteUnionElements 把未引号联合的元素加引号：[a, b] → ['a','b']。
// 任一元素不是普通对象键（数字索引/切片/空/含杂字符）则整组原样返回。
func quoteUnionElements(match string) string {
	inner := match[1 : len(match)-1]
	parts := strings.Split(inner, ",")
	quoted := make([]string, 0, len(parts))
	for _, p := range parts {
		key := strings.TrimSpace(p)
		if !bareUnionKeyRe.MatchString(key) {
			return match
		}
		quoted = append(quoted, "'"+key+"'")
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

// rewriteHasFilter 把 ?(has "P") 改写为 ajson 原生存在性过滤器 ?(<路径>)。
// AdGuard 的 has 路径相对当前节点求值：$ 前缀重锚为 @（$..→@..、$.→@.），
// 裸路径补 @.，已是 @ 前缀的原样保留。
func rewriteHasFilter(match string) string {
	sm := hasFilterRe.FindStringSubmatch(match)
	if sm == nil {
		return match
	}
	path := sm[1]
	if path == "" {
		path = sm[2]
	}
	if path == "" {
		path = sm[3]
	}
	if path == "" {
		return match
	}
	// splitModifiers 保留值内 "\$"（语料 has 路径写作 "\$..x.y"）。
	path = strings.ReplaceAll(path, `\$`, `$`)
	switch {
	case strings.HasPrefix(path, "@"):
		// 已是相对当前节点的写法，原样。
	case strings.HasPrefix(path, "$"):
		path = "@" + path[1:]
	default:
		path = "@." + path
	}
	return "?(" + path + ")"
}

// rewriteKeyFilter 把 key-eq / key-substr 改写为 ajson 原生 == / =~ 过滤器。
// 键必须是普通标识符路径；值含会产生非法转义/截断字面量的字符时整段跳过
// （处理不了的保持原样，行为与改动前一致）。
func rewriteKeyFilter(match string) string {
	sm := keyFilterRe.FindStringSubmatch(match)
	if sm == nil {
		return match
	}
	key := sm[2]
	if key == "" {
		key = sm[3]
	}
	val := sm[4]
	if val == "" {
		val = sm[5]
	}
	if !bareUnionKeyRe.MatchString(key) {
		return match
	}
	switch sm[1] {
	case "key-eq":
		// ' 会截断单引号字面量 → 跳过。值内 \ 先做 ajson 字符串字面量层
		// 转义（\ → \\），unquote 后仍得原字符，比较语义保持字面量。
		if strings.Contains(val, `'`) {
			return match
		}
		return "?(@." + key + " == '" + escapeAJSONStringLiteral(val) + "')"
	case "key-substr":
		// =~ 正则字面量用双引号承载，转义要过两层：正则层（QuoteMeta）＋
		// ajson 字符串字面量层（unquote 只认 \\"\\/'\b\f\n\r\t\u，不认
		// \. \( 等正则转义，必须把 QuoteMeta 产生的 \ 再双写为 \\）。
		// v 含 " 会截断双引号字面量 → 跳过。
		if strings.Contains(val, `"`) {
			return match
		}
		return `?(@.` + key + ` =~ ".*` + escapeAJSONStringLiteral(regexp.QuoteMeta(val)) + `.*")`
	}
	return match
}

// escapeAJSONStringLiteral 把文本转成 ajson 字符串字面量内容（双/单引号
// 通用）：仅反斜杠需要双写（其他字面量合法字符原样保留）。
func escapeAJSONStringLiteral(s string) string {
	return strings.ReplaceAll(s, `\`, `\\`)
}

// jsonPruneApplyWarned 记录已告警过的规则文本，同一规则只在进程内首次
// apply 失败时记日志，防止热规则刷屏（comp_audit TOP-5）。
var jsonPruneApplyWarned sync.Map

func logJSONPruneApplyFailure(raw string, err error) {
	if raw == "" {
		return
	}
	if _, loaded := jsonPruneApplyWarned.LoadOrStore(raw, struct{}{}); loaded {
		return
	}
	log.Printf("jsonprune %q: apply failed, response body left unchanged: %v", raw, err)
}

// [bound_audit 修复批 2026-10-08，发现①] jsonPruneMaxDepth 是 jsonprune 应用
// 的 JSON 嵌套深度上限。口径对齐 Go 标准库 encoding/json 的
// maxNestingDepth=10,000（go/src/encoding/json/scanner.go）：超出视为病态结构，
// 拒绝应用。原因（bound_audit 发现①）：spyzhov/ajson 的 $.. 递归下降
//（jsonpath.go recursiveChildren）与 Marshal（encode.go）都是无界递归，
// 深嵌套 body 会触发 fatal error: stack overflow——不可 recover，代理进程
// 整体死亡。深度门把恶意结构挡在一切 ajson 调用之前。
const jsonPruneMaxDepth = 10_000

// [bound_audit 修复批 2026-10-08，发现③] jsonPruneMaxBodySize 是 jsonprune
// 缓冲改写的 body 上限，口径对齐 $replace 的 replaceMaxBodySize=10MB
//（AdGuard 文档 2876）。超限体不经缓冲、原样流式透传。
const jsonPruneMaxBodySize = 10 << 20

// jsonPruneDepthExceeded 字节级扫描 src 的 {/[ 嵌套深度（跳过字符串字面量，
// 转义感知；UTF-8 多字节均 ≥0x80，不会与 ASCII 定界符混淆），超过
// jsonPruneMaxDepth 立即返回 true。单趟线性、O(1) 空间、超限提前退出。
func jsonPruneDepthExceeded(src []byte) bool {
	depth := 0
	inStr, esc := false, false
	for _, c := range src {
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
			if depth > jsonPruneMaxDepth {
				return true
			}
		case '}', ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return false
}

func (m *JSONPruneModifier) ModifyRes(res *http.Response) (modified bool, err error) {
	if !isJSONResponse(res) {
		return false, nil
	}

	var touched bool
	// [bound_audit 修复批 2026-10-08，发现③] BufferRewrite →
	// BufferRewriteLimited：与 $replace 同口径的 10MB 上限，超限体原样
	// 流式透传（over-limit 时体未被改写，modified=false）。
	_, err = httprewrite.BufferRewriteLimited(res, jsonPruneMaxBodySize, func(src []byte) []byte {
		// [bound_audit 修复批 2026-10-08，发现①] 深度门：在任何 ajson
		// 调用（含迭代安全的 Unmarshal）之前拒掉深嵌套结构。
		if jsonPruneDepthExceeded(src) {
			logJSONPruneApplyFailure(m.raw, fmt.Errorf("body nesting depth exceeds %d, response body left unchanged", jsonPruneMaxDepth))
			return src
		}

		root, err := ajson.Unmarshal(src)
		if err != nil {
			// [bound_audit 修复批 2026-10-08，发现①] 解析失败纳入 P3
			// 去重告警（原实现静默吞掉）。
			logJSONPruneApplyFailure(m.raw, fmt.Errorf("unmarshal: %w", err))
			return src
		}

		nodes, err := ajson.ApplyJSONPath(root, m.commands)
		if err != nil {
			// [P1 2026-10-08] 失效可观测性（comp_audit TOP-5）：apply 失败
			// 不再静默吞掉，按规则文本去重记 warn 级日志。
			logJSONPruneApplyFailure(m.raw, err)
			return src
		}

		for _, node := range nodes {
			if err := node.Delete(); err == nil {
				touched = true
			}
		}

		if !touched {
			return src
		}

		newBody, err := ajson.Marshal(root)
		if err != nil {
			return src
		}
		return newBody
	})

	if err != nil {
		return false, fmt.Errorf("buffer rewrite: %w", err)
	}

	return touched, nil
}

func (m *JSONPruneModifier) ModifyReq(*http.Request) bool {
	return false
}

func (m *JSONPruneModifier) Cancels(other Modifier) bool {
	o, ok := other.(*JSONPruneModifier)
	if !ok {
		return false
	}

	if len(m.commands) != len(o.commands) {
		return false
	}

	for i := range m.commands {
		if m.commands[i] != o.commands[i] {
			return false
		}
	}

	return true
}

func isJSONResponse(res *http.Response) bool {
	contentType := res.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}

	return mediaType == "application/json"
}
