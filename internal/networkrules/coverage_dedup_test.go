package networkrules

// coverage_dedup_test.go — 2026-10-09 覆盖去重陷阱矩阵测试。
//
// 每个用例钉住一个语义前提（见 coveragededup.go 头注与《规则语义去重
// 评估报告.md》）：签名隔离、覆盖方向、badfilter 交互、例外侧、hosts、
// 活插入豁免、幂等、重建恢复。决策级断言走 ModifyReq（含用户导航
// 变体——普通规则不拦导航、$document 规则拦导航的分界在此复验）。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/exceptionrule"
	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

// buildFromLines 用与装载一致的入口（ParseRule + Compact）构建引擎。
func buildFromLines(t *testing.T, opts []NetworkRulesOption, lines []string) *NetworkRules {
	t.Helper()
	nr := New(opts...)
	name := "test"
	for _, ln := range lines {
		if _, err := nr.ParseRule(ln, &name); err != nil {
			t.Fatalf("ParseRule(%q): %v", ln, err)
		}
	}
	nr.Compact()
	return nr
}

func countStored(t *testing.T, nr *NetworkRules) (primary, exception int) {
	t.Helper()
	nr.primaryStore.walkValues(func(*rule.Rule) { primary++ })
	nr.exceptionStore.walkValues(func(*exceptionrule.ExceptionRule) { exception++ })
	return primary, exception
}

func decision(t *testing.T, nr *NetworkRules, rawURL string, userNav bool) (block bool, redirect string, nRules int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, rawURL, nil)
	if userNav {
		req.Header.Set("Sec-Fetch-User", "?1")
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
	}
	applied, shouldBlock, redirectURL := nr.ModifyReq(req)
	return shouldBlock, redirectURL, len(applied)
}

// ── 基本覆盖与签名隔离 ──────────────────────────────────────────────

// TestCoverageDedupSubdomainCovered：||sub.d^ 被 ||d^ 覆盖并删除；
// 决策不变。
func TestCoverageDedupSubdomainCovered(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||example.com^",
		"||www.example.com^",
		"||mail.example.com^",
	})
	p, e := countStored(t, nr)
	if p != 1 || e != 0 {
		t.Fatalf("stored primary=%d exception=%d, want 1/0 (both subdomain rules covered by ||example.com^)", p, e)
	}
	for _, u := range []string{"https://www.example.com/x", "https://mail.example.com/y", "https://example.com/z"} {
		if block, _, _ := decision(t, nr, u, false); !block {
			t.Fatalf("%s should still block", u)
		}
	}
}

// TestCoverageDedupSignatureIsolation：$document（hosts 派生形态）与普通
// 规则互不覆盖——普通规则不拦用户导航（rule.go:496-497），删除会改行为。
func TestCoverageDedupSignatureIsolation(t *testing.T) {
	// hosts 行派生 ||host^$document；与同域普通 ||host^ 共存。
	nr := buildFromLines(t, nil, []string{
		"||ads.example.com^",      // 普通（列表行）
		"0.0.0.0 ads.example.com", // hosts → ||ads.example.com^$document
		"||plain.other.com^",      // 普通规则域
	})
	p, _ := countStored(t, nr)
	if p != 3 {
		t.Fatalf("hosts-derived and plain rules must NOT merge: primary=%d, want 3", p)
	}
	// 用户导航：hosts 派生规则拦截，普通规则放行（上游 #257 语义）。
	if block, _, _ := decision(t, nr, "https://ads.example.com/x", true); !block {
		t.Fatal("user-nav to ads.example.com must be blocked by the $document hosts rule")
	}
	if block, _, _ := decision(t, nr, "https://plain.other.com/x", true); block {
		t.Fatal("user-nav to plain.other.com must NOT be blocked (plain rule, no $document)")
	}
	// 子资源：两类都拦。
	if block, _, _ := decision(t, nr, "https://ads.example.com/x", false); !block {
		t.Fatal("subresource must be blocked")
	}
}

// TestCoverageDedupImportantAndAll 验证 $important/$all/$popup 各自独立签名。
func TestCoverageDedupImportantAndAll(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||imp.test^",
		"||imp.test^$important",
		"||all.test^",
		"||all.test^$all",
		"||pop.test^",
		"||pop.test^$popup",
	})
	p, _ := countStored(t, nr)
	if p != 6 {
		t.Fatalf("different signatures must not merge: primary=%d, want 6", p)
	}
}

// TestCoverageDedupCondsAndActionsExcluded：带条件/动作修饰符的规则不参与
// 覆盖（$script 与全类型不互并；$csp 动作规则保留）。
func TestCoverageDedupCondsAndActionsExcluded(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||cond.test^",
		"||cond.test^$script",
		"||cond.test^$image",
		"||csp.test^",
		"||csp.test^$csp=frame-ancestors 'none'",
		"||rp.test^$removeparam=utm_source",
		"||rp.test^",
	})
	p, _ := countStored(t, nr)
	if p != 7 {
		t.Fatalf("conds/actions-bearing rules must be excluded from coverage: primary=%d, want 7", p)
	}
}

// TestCoverageDedupDomainConditionExcluded：$domain 条件规则不参与。
func TestCoverageDedupDomainConditionExcluded(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||dom.test^",
		"||dom.test^$domain=example.org",
	})
	p, _ := countStored(t, nr)
	if p != 2 {
		t.Fatalf("primary=%d, want 2", p)
	}
}

// ── 覆盖方向 ────────────────────────────────────────────────────────

// TestCoverageDedupDirectionNoCaret：||d 无^ 是 ||a^/||a.b 的超集方向
// 关系——||a.b（无^）不被 ||b^ 覆盖（^祖先非其超集），但 ||a.b^ 被覆盖。
func TestCoverageDedupDirectionNoCaret(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||b.test^",   // 覆盖者
		"||a.b.test^", // 被覆盖
		"||c.b.test",  // 无^：不被 ||b.test^ 覆盖（||c.b.test 匹配 c.b.testX）
	})
	p, _ := countStored(t, nr)
	if p != 2 {
		t.Fatalf("primary=%d, want 2 (||c.b.test must survive)", p)
	}
	// ||c.b.test 匹配 c.b.testX（无^不要求分隔符）；||b.test^ 不匹配它
	// （b.test 后是 '.'，'.' 不在分隔符集）。若误删会漏拦 c.b.testX。
	if block, _, _ := decision(t, nr, "https://c.b.test.evil.io/payload", false); !block {
		t.Fatal("||c.b.test (no caret) must still block c.b.testX hosts")
	}
}

// TestCoverageDedupRestPaths：||d/… 与 ||d^… 形态被 ||d^ 覆盖。
func TestCoverageDedupRestPaths(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||rest.test^",
		"||rest.test/ads/",
		"||rest.test^/track",
		"||rest.test/img.png",
	})
	p, _ := countStored(t, nr)
	if p != 1 {
		t.Fatalf("primary=%d, want 1 (three rest rules covered by ||rest.test^)", p)
	}
	if block, _, _ := decision(t, nr, "https://rest.test/ads/banner.js", false); !block {
		t.Fatal("rest.test/ads must still block via the coverer")
	}
}

// TestCoverageDedupSameShapeDup：同形态同签名的重复（不同行文本）只留一条。
func TestCoverageDedupSameShapeDup(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"0.0.0.0 dup.hosts.test",      // → ||dup.hosts.test^$document
		"127.0.0.1 dup.hosts.test #x", // → 同合成规则，不同行文本（#804 不去重）
		"||dup.hosts.test^$document",  // 显式行，同语义
	})
	p, _ := countStored(t, nr)
	if p != 1 {
		t.Fatalf("primary=%d, want 1 (three same-semantics rules collapse to one)", p)
	}
}

// ── badfilter 交互 ──────────────────────────────────────────────────

// TestCoverageDedupBadfilterOnCoverer：badfilter 瞄准覆盖者 ⇒ 整链保留
// （窄规则必须活下来，否则 badfilter 生效时该域完全不拦）。
func TestCoverageDedupBadfilterOnCoverer(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||bf.test^",     // 覆盖者，被 badfilter 禁用
		"||www.bf.test^", // 被覆盖，但覆盖者死后它必须独活
		"||bf.test^$badfilter",
	})
	p, _ := countStored(t, nr)
	if p != 2 {
		t.Fatalf("primary=%d, want 2 (chain kept: survivor is badfilter-targeted)", p)
	}
	if block, _, _ := decision(t, nr, "https://www.bf.test/x", false); !block {
		t.Fatal("www.bf.test must still block via the surviving narrow rule")
	}
	if block, _, _ := decision(t, nr, "https://other.bf.test/x", false); block {
		t.Fatal("other.bf.test must be unblocked (coverer disabled by badfilter)")
	}
}

// TestCoverageDedupBadfilterOnCovered：badfilter 瞄准被覆盖规则本身 ⇒
// 该规则仍可安全删除（它本来就被禁用；幸存者覆盖同样的 URL 面）。
func TestCoverageDedupBadfilterOnCovered(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||cov.test^",
		"||www.cov.test^$badfilter",
		"||www.cov.test^",
	})
	p, _ := countStored(t, nr)
	if p != 1 {
		t.Fatalf("primary=%d, want 1 (covered rule dropped regardless of being targeted)", p)
	}
	if block, _, _ := decision(t, nr, "https://www.cov.test/x", false); !block {
		t.Fatal("www.cov.test must block via the coverer")
	}
}

// TestCoverageDedupBadfilterPartialOnCoverer：域级 badfilter（$domain+部分
// 禁用）瞄准幸存者 ⇒ 保守保留整链（部分禁用使覆盖者在特定域放行，窄规则
// 是那些域上的唯一拦截者时不可删——保守处理）。
func TestCoverageDedupBadfilterPartialOnCoverer(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||partial.test^",
		"||www.partial.test^",
		"||partial.test^$domain=downdetector.example,badfilter",
	})
	p, _ := countStored(t, nr)
	if p != 2 {
		t.Fatalf("primary=%d, want 2 (partial badfilter on survivor keeps the chain)", p)
	}
}

// ── 例外侧 ──────────────────────────────────────────────────────────

// TestCoverageDedupExceptions：例外规则同签名覆盖；例外取消语义不变。
func TestCoverageDedupExceptions(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||block.test^",
		"@@||block.test^",
		"@@||www.block.test^",
	})
	_, e := countStored(t, nr)
	if e != 1 {
		t.Fatalf("exceptions=%d, want 1 (narrow exception covered by broad one)", e)
	}
	if block, _, _ := decision(t, nr, "https://www.block.test/x", false); block {
		t.Fatal("must stay unblocked: the surviving broad exception still cancels")
	}
}

// TestCoverageDedupExceptionSignature：例外签名隔离——$document 例外与裸
// 例外不互并（IsBare 语义影响 wNormAllBare 分类）。
func TestCoverageDedupExceptionSignature(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||exc.test^",
		"@@||exc.test^",
		"@@||exc.test^$document",
		"@@||sub.exc.test^",
	})
	_, e := countStored(t, nr)
	if e != 2 {
		t.Fatalf("exceptions=%d, want 2 (bare survives; narrow bare dropped; $document exception kept)", e)
	}
}

// ── 活插入豁免与幂等 ────────────────────────────────────────────────

// TestCoverageDedupLiveInsertExempt：Compact（=Finalize）之后白名单服务器
// 插入的 @@ 规则永不参与、永不被删。
func TestCoverageDedupLiveInsertExempt(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||live.test^",
	})
	allow := "Allowlist"
	if _, err := nr.ParseRule("@@||live.test^", &allow); err != nil {
		t.Fatalf("live allowlist insert: %v", err)
	}
	// 再跑一次 Compact（幂等）：活插入的例外必须原样保留。
	nr.Compact()
	_, e := countStored(t, nr)
	if e != 1 {
		t.Fatalf("exceptions=%d, want 1 (live-inserted rule exempt)", e)
	}
	if block, _, _ := decision(t, nr, "https://live.test/x", false); block {
		t.Fatal("live allowlist must keep unblocking")
	}
}

// TestCoverageDedupIdempotent：重复 Compact 不再变化。
func TestCoverageDedupIdempotent(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||idem.test^",
		"||www.idem.test^",
		"||a.www.idem.test^",
	})
	p1, _ := countStored(t, nr)
	nr.Compact()
	p2, _ := countStored(t, nr)
	if p1 != p2 {
		t.Fatalf("second Compact changed store: %d -> %d", p1, p2)
	}
}

// ── 重建恢复（前端设置实时生效的关键） ────────────────────────────

// TestCoverageDedupRebuildRestores：模拟"禁用覆盖者所在列表"→ 全新构建
// （生产 buildFilter 语义）→ 被覆盖规则恢复、行为与从未去重一致。
func TestCoverageDedupRebuildRestores(t *testing.T) {
	listA := []string{"||kept.test^"} // 独立规则
	listB := []string{                // 将被"禁用"的列表
		"||reb.test^",
		"||sub.reb.test^", // 只被 listB 的宽规则覆盖
	}
	build := func(lines ...[]string) *NetworkRules {
		var all []string
		for _, l := range lines {
			all = append(all, l...)
		}
		return buildFromLines(t, nil, all)
	}
	both := build(listA, listB)
	p, _ := countStored(t, both)
	if p != 2 {
		t.Fatalf("A+B: primary=%d, want 2 (sub.reb covered by reb)", p)
	}

	onlyA := build(listA)
	// 禁用 B 后重建：A 的规则全部在位。
	pA, _ := countStored(t, onlyA)
	if pA != 1 {
		t.Fatalf("A only: primary=%d, want 1", pA)
	}
	// 且与"从未去重"的 A-only 引擎行为逐请求一致。
	onlyANoDedup := buildFromLines(t, []NetworkRulesOption{WithCoverageDedup(false)}, listA)
	for _, u := range []string{"https://kept.test/x", "https://sub.reb.test/y", "https://reb.test/z"} {
		b1, r1, n1 := decision(t, onlyA, u, false)
		b2, r2, n2 := decision(t, onlyANoDedup, u, false)
		if b1 != b2 || r1 != r2 || n1 != n2 {
			t.Fatalf("rebuild mismatch on %s: dedup(%v,%q,%d) vs nodedup(%v,%q,%d)", u, b1, r1, n1, b2, r2, n2)
		}
	}
}

// TestCoverageDedupDisabled：开关关闭时零删除（回退开关）。
func TestCoverageDedupDisabled(t *testing.T) {
	nr := buildFromLines(t, []NetworkRulesOption{WithCoverageDedup(false)}, []string{
		"||off.test^",
		"||www.off.test^",
		"||a.www.off.test^",
	})
	p, _ := countStored(t, nr)
	if p != 3 {
		t.Fatalf("primary=%d, want 3 (dedup disabled)", p)
	}
}

// ── 分类器 ──────────────────────────────────────────────────────────

// TestClassifyCovDomain：形态分类与评估探针口径一致。
func TestClassifyCovDomain(t *testing.T) {
	cases := []struct {
		pattern string
		domain  string
		kind    covKind
	}{
		{"||example.com^", "example.com", covPureCaret},
		{"||example.com", "example.com", covPureBare},
		{"||example.com/ads/", "example.com", covDomainRest},
		{"||example.com^/track", "example.com", covDomainRest},
		{"||example.com:8080/x", "", covNone}, // ':' 不在域名字符集
		{"||exa*mple.com^", "", covNone},      // 通配符不参与
		{"example.com^", "", covNone},         // 无 || 锚点不参与
		{"/regex/", "", covNone},
		{"||x", "x", covPureBare},
		{"||", "", covNone},
	}
	for _, c := range cases {
		d, k := classifyCovDomain(c.pattern)
		if d != c.domain || k != c.kind {
			t.Errorf("classify(%q) = (%q,%d), want (%q,%d)", c.pattern, d, k, c.domain, c.kind)
		}
	}
}

// ── 决策级：用户导航分界 ────────────────────────────────────────────

// TestCoverageDedupUserNavBoundary：覆盖去重后，"普通规则不拦用户导航"
// 的分界保持——被删的只能是普通规则，导航豁免不受影响。
func TestCoverageDedupUserNavBoundary(t *testing.T) {
	nr := buildFromLines(t, nil, []string{
		"||nav.test^",
		"||www.nav.test^",
		"||www.nav.test^$document", // hosts 派生同款语义
	})
	p, _ := countStored(t, nr)
	if p != 2 {
		t.Fatalf("primary=%d, want 2 (plain dup dropped; $document kept)", p)
	}
	// 用户导航：仅 $document 规则拦截。
	if block, _, _ := decision(t, nr, "https://www.nav.test/x", true); !block {
		t.Fatal("user-nav must be blocked by the $document rule")
	}
	// 子资源：普通覆盖者拦截。
	if block, _, _ := decision(t, nr, "https://www.nav.test/x", false); !block {
		t.Fatal("subresource must be blocked")
	}
}

// silence unused warning for strings import used in helpers below
var _ = strings.TrimSpace
