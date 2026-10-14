package networkrules

// [2026-10-08 B10-A] 动作索引回归测试：分类路由、顺序保持、双引擎等价、
// badfilter 接线、空索引惰性。断言依据 测试临时\B10修复设计方案.md §1.6。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

func b10Req(rawURL string, headers http.Header) *http.Request {
	req := httptest.NewRequest(http.MethodGet, rawURL, nil)
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return req
}

// TestActionIndexClassification：规则按动作形态进入正确桶。
func TestActionIndexClassification(t *testing.T) {
	t.Parallel()

	nr := New()
	rules := map[string]string{
		"$cookie=__cfduid":         "exact",
		"$cookie=/__utm[a-z]/":     "any",
		"$cookie":                  "any",
		"$removeparam=utm_source":  "rp-exact",
		"$removeparam=/^utm_/":     "rp-any",
		"$removeparam=~x":          "rp-any",
		"||host.example^$cookie=y": "store",
		"$csp=frame-src 'none'":    "store",
		"$csp=x,cookie=y":          "store",
	}
	for r := range rules {
		if _, err := nr.ParseRule(r, nil); err != nil {
			t.Fatalf("ParseRule(%q): %v", r, err)
		}
	}

	if nr.actionIdx.total != 6 {
		t.Fatalf("routed rules = %d, want 6 (mixed/domain/host rules stay in store)", nr.actionIdx.total)
	}
	if len(nr.actionIdx.cookieExact) != 1 {
		t.Fatalf("cookieExact buckets = %d, want 1", len(nr.actionIdx.cookieExact))
	}
	if len(nr.actionIdx.cookieAny) != 2 {
		t.Fatalf("cookieAny entries = %d, want 2", len(nr.actionIdx.cookieAny))
	}
	if len(nr.actionIdx.rpExact) != 1 {
		t.Fatalf("rpExact buckets = %d, want 1", len(nr.actionIdx.rpExact))
	}
	if len(nr.actionIdx.rpAny) != 2 {
		t.Fatalf("rpAny entries = %d, want 2", len(nr.actionIdx.rpAny))
	}

	// 混排规则（$csp+$cookie）必须留在 store。
	found := false
	nr.primaryStore.walkValues(func(r *rule.Rule) {
		if r.RawRule == "$csp=x,cookie=y" {
			found = true
		}
	})
	if !found {
		t.Fatal("mixed-action rule must stay in the store")
	}
}

// TestActionIndexCookieOrderPreserved：同族双规则改写同一 Set-Cookie 时，
// 装载序决定终态（后写者胜），索引 seq 必须恢复它。
func TestActionIndexCookieOrderPreserved(t *testing.T) {
	t.Parallel()

	run := func(order []string) string {
		nr := New()
		for _, r := range order {
			if _, err := nr.ParseRule(r, nil); err != nil {
				t.Fatal(err)
			}
		}
		nr.Compact()
		req := b10Req("https://example.com/a", nil)
		res := &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "Set-Cookie": []string{"x=1; Path=/"}},
		}
		if _, err := nr.ModifyRes(req, res); err != nil {
			t.Fatal(err)
		}
		return strings.Join(res.Header.Values("Set-Cookie"), "|")
	}

	// bare 先（过期），具名 maxAge 后（续期 3600）→ 终态续期。
	got1 := run([]string{`$cookie`, `$cookie=x;maxAge=3600`})
	if !strings.Contains(got1, "Max-Age=3600") {
		t.Fatalf("load order [bare, named maxAge]: want renewed cookie, got %q", got1)
	}
	// 调换装载序 → 终态过期。
	got2 := run([]string{`$cookie=x;maxAge=3600`, `$cookie`})
	if !strings.Contains(got2, "Max-Age=0") {
		t.Fatalf("load order [named maxAge, bare]: want expired cookie, got %q", got2)
	}
}

// TestActionIndexDualFamilyDedup：同规则带 $cookie+$removeparam 双族时，
// 请求路径候选中只评估一次。
func TestActionIndexDualFamilyDedup(t *testing.T) {
	t.Parallel()

	nr := New()
	if _, err := nr.ParseRule(`$cookie=a,removeparam=b`, nil); err != nil {
		t.Fatal(err)
	}
	nr.Compact()

	if nr.actionIdx.total != 1 {
		t.Fatalf("routed = %d, want 1", nr.actionIdx.total)
	}

	req := b10Req("https://example.com/p?b=1", http.Header{"Cookie": {"a=1"}})
	storeRules := nr.primaryStore.Get("https://example.com/p?b=1")
	nr.primaryStore.putRes(storeRules)
	merged, scratch := nr.prependIndexedReq(storeRules, req)
	if scratch != nil {
		defer nr.putReqScratch(scratch)
	}

	count := 0
	for _, r := range merged {
		if r.RawRule == `$cookie=a,removeparam=b` {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("dual-family rule candidate count = %d, want 1", count)
	}
}

// TestActionIndexEquivalence：双引擎（默认开索引 vs WithActionIndex(false)）
// 对同一组规则与同一组请求/响应，终态逐字节相等。
func TestActionIndexEquivalence(t *testing.T) {
	t.Parallel()

	rules := []string{
		`$cookie=__cfduid`,
		`$cookie=/^_/`,
		`$cookie`,
		`$cookie=keep;maxAge=3600`,
		`$removeparam=utm_source`,
		`$removeparam=/^utm_/`,
		`$removeparam=gclid`,
		`||ads.example^$cookie=adnxs`,
	}

	type outcome struct {
		setCookie string
		rawQuery  string
		applied   []string
	}

	run := func(useIndex bool) map[string]outcome {
		var opts []NetworkRulesOption
		if !useIndex {
			opts = append(opts, WithActionIndex(false))
		}
		nr := New(opts...)
		for _, r := range rules {
			if _, err := nr.ParseRule(r, nil); err != nil {
				t.Fatal(err)
			}
		}
		nr.Compact()

		out := map[string]outcome{}
		responses := []struct {
			url       string
			setCookie string
		}{
			{"https://a.example/x", "__cfduid=abc; Path=/"},
			{"https://a.example/y", "_ga=GA1.2.1; Path=/"},
			{"https://a.example/z", "keep=1; Path=/"},
			{"https://ads.example/x", "adnxs=1; Path=/"},
			{"https://b.example/x", "other=2; Path=/"},
		}
		for i, tc := range responses {
			req := b10Req(tc.url, nil)
			res := &http.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
					"Set-Cookie":   []string{tc.setCookie},
				},
			}
			applied, err := nr.ModifyRes(req, res)
			if err != nil {
				t.Fatal(err)
			}
			var raws []string
			for _, r := range applied {
				raws = append(raws, r.RawRule)
			}
			key := "res" + string(rune('A'+i))
			out[key] = outcome{setCookie: strings.Join(res.Header.Values("Set-Cookie"), "|"), applied: raws}
		}

		requests := []string{
			"https://a.example/p?utm_source=x&id=1",
			"https://a.example/q?gclid=abc&keepme=2",
			"https://a.example/r?utm_campaign=a&utm_term=b",
			"https://a.example/s?a=1&&b=2",
			"https://a.example/t",
			"https://a.example/u?_ga=x",
		}
		for i, u := range requests {
			req := b10Req(u, http.Header{"Cookie": {"__cfduid=abc; _ga=x; keep=1"}})
			_, _, redirect := nr.ModifyReq(req)
			key := "req" + string(rune('A'+i))
			out[key] = outcome{rawQuery: redirect, applied: nil}
			// redirect 仅在 URL 改写时非空——removeparam 不产生 redirect，
			// 但 finalURL 语义经 ModifyReq 返回值不可见；直接读改写后的 query。
			out[key] = outcome{rawQuery: req.URL.RawQuery}
		}
		return out
	}

	withIdx := run(true)
	withoutIdx := run(false)

	for k, want := range withoutIdx {
		got := withIdx[k]
		if got.setCookie != want.setCookie || got.rawQuery != want.rawQuery {
			t.Fatalf("outcome %q differs:\n  with index:    %+v\n  without index: %+v", k, got, want)
		}
		if len(got.applied) != len(want.applied) {
			t.Fatalf("applied count %q differs: %v vs %v", k, got.applied, want.applied)
		}
		for i := range got.applied {
			if got.applied[i] != want.applied[i] {
				t.Fatalf("applied order %q differs at %d", k, i)
			}
		}
	}
}

// TestBadfilterDisablesIndexedRules：$badfilter 对被路由规则仍然生效
// （badfilter.go walk 接线）。
func TestBadfilterDisablesIndexedRules(t *testing.T) {
	t.Parallel()

	nr := New()
	if _, err := nr.ParseRule(`$cookie=x`, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := nr.ParseRule(`$cookie=x,badfilter`, nil); err != nil {
		t.Fatal(err)
	}
	nr.Compact()

	audit := nr.BadfilterAudit()
	if len(audit.FullDisabled) == 0 || audit.FullDisabled[0] != "$cookie=x" {
		t.Fatalf("indexed rule must appear in FullDisabled, got %+v", audit)
	}

	req := b10Req("https://example.com/a", nil)
	res := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "Set-Cookie": []string{"x=1; Path=/"}},
	}
	if _, err := nr.ModifyRes(req, res); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.Header.Values("Set-Cookie"), "|"); !strings.Contains(got, "x=1") {
		t.Fatalf("badfilter must disable indexed rule, got %q", got)
	}
}

// TestActionIndexInertWhenEmpty：无索引规则的引擎与 disabled 引擎行为一致
// （total==0 门短路，prepend 零工作）。
func TestActionIndexInertWhenEmpty(t *testing.T) {
	t.Parallel()

	nr := New()
	if _, err := nr.ParseRule(`||example.org^$script`, nil); err != nil {
		t.Fatal(err)
	}
	if !nr.actionIdx.empty() {
		t.Fatal("index must be empty")
	}

	storeRules := nr.primaryStore.Get("https://example.org/x.js")
	nr.primaryStore.putRes(storeRules)
	merged, scratch := nr.prependIndexedReq(storeRules, b10Req("https://example.org/x.js?utm_source=1", http.Header{"Cookie": {"a=1"}}))
	if scratch != nil || len(merged) != len(storeRules) {
		t.Fatal("empty index must return store candidates untouched")
	}
}
