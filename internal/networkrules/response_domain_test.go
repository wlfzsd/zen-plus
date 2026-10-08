package networkrules

// [2026-10-08 B10] 回归测试：$domain 响应路径复活（B-1）、$xmlhttprequest
// 响应路径按 Sec-Fetch-Dest 判定（C-1）、badfilter 响应侧缺口闭合。
// 语义裁决与断言依据见 测试临时\B10修复设计方案.md §2.7/§3.6。

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newB10TestRequest(rawURL string, headers http.Header) *http.Request {
	req := httptest.NewRequest(http.MethodGet, rawURL, nil)
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return req
}

func b10JSONRes(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestResponsePathNzheraldPermissions：Spyware :317585 原文规则——nzherald
// 页面开始收到 geolocation=()，且受 B9 frame 门约束（docs L570 链接目标域 +
// L2321 frame 限定）。
func TestResponsePathNzheraldPermissions(t *testing.T) {
	t.Parallel()

	nr := New()
	if _, err := nr.ParseRule(`$domain=nzherald.co.nz,permissions=geolocation=()`, nil); err != nil {
		t.Fatal(err)
	}

	// dest=image（非框架）：B9 frame 门胜出，不加头。
	req := newB10TestRequest("https://example.com/pixel.png", http.Header{
		"Referer":        {"https://www.nzherald.co.nz/page"},
		"Sec-Fetch-Dest": {"image"},
	})
	res := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}}}
	if _, err := nr.ModifyRes(req, res); err != nil {
		t.Fatal(err)
	}
	if len(res.Header.Values("Permissions-Policy")) != 0 {
		t.Fatal("permissions applied on non-frame response — B9 frame gate must still win")
	}

	// dest=document + Referer=nzherald → 出现（B10 复活）。
	req2 := newB10TestRequest("https://example.com/page", http.Header{
		"Referer":        {"https://www.nzherald.co.nz/page"},
		"Sec-Fetch-Dest": {"document"},
	})
	res2 := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}}
	if _, err := nr.ModifyRes(req2, res2); err != nil {
		t.Fatal(err)
	}
	if len(res2.Header.Values("Permissions-Policy")) == 0 {
		t.Fatal("expected Permissions-Policy on nzherald frame load (B10 domain revival)")
	}

	// dest=document + 其他 Referer → 不出现。
	req3 := newB10TestRequest("https://example.com/page", http.Header{
		"Referer":        {"https://other.example/page"},
		"Sec-Fetch-Dest": {"document"},
	})
	res3 := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}}
	if _, err := nr.ModifyRes(req3, res3); err != nil {
		t.Fatal(err)
	}
	if len(res3.Header.Values("Permissions-Policy")) != 0 {
		t.Fatal("unexpected Permissions-Policy for non-matching referrer")
	}
}

// TestResponsePathDomainCookieApplies：$cookie+$domain 在响应路径按 referrer/
// 目标域链接判定（docs L570/L572）。
func TestResponsePathDomainCookieApplies(t *testing.T) {
	t.Parallel()

	nr := New()
	if _, err := nr.ParseRule(`$cookie=__cfduid,domain=src.example`, nil); err != nil {
		t.Fatal(err)
	}

	// Referer 命中 → Set-Cookie 被过期改写。
	req := newB10TestRequest("https://any.example/a", http.Header{"Referer": {"https://src.example/page"}})
	res := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "Set-Cookie": []string{"__cfduid=abc; Path=/"}},
	}
	if _, err := nr.ModifyRes(req, res); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.Header.Values("Set-Cookie"), "|"); !strings.Contains(got, "Max-Age=0") {
		t.Fatalf("expected cookie expired via referrer match, got %q", got)
	}

	// 目标域命中（$cookie 在 L570 家族内）→ 也改写。
	// 注意：目标是 src.example、referrer 是 other.example。
	res2 := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "Set-Cookie": []string{"__cfduid=abc; Path=/"}},
	}
	req2 := newB10TestRequest("https://src.example/a", http.Header{"Referer": {"https://other.example/page"}})
	if _, err := nr.ModifyRes(req2, res2); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res2.Header.Values("Set-Cookie"), "|"); !strings.Contains(got, "Max-Age=0") {
		t.Fatalf("expected cookie expired via target-domain link (docs L570), got %q", got)
	}

	// 两者皆非 → 不改写。
	res3 := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "Set-Cookie": []string{"__cfduid=abc; Path=/"}},
	}
	req3 := newB10TestRequest("https://any3.example/a", http.Header{"Referer": {"https://other.example/page"}})
	if _, err := nr.ModifyRes(req3, res3); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res3.Header.Values("Set-Cookie"), "|"); got != "__cfduid=abc; Path=/" {
		t.Fatalf("expected untouched cookie when neither referrer nor target matches, got %q", got)
	}
}

// TestResponsePathReplaceReferrerOnly：$replace 不在 docs L570 目标域链接
// 清单内 → 仅 referrer 匹配（L581-582 反证）。
func TestResponsePathReplaceReferrerOnly(t *testing.T) {
	t.Parallel()

	nr := New()
	if _, err := nr.ParseRule(`$replace=/hello/goodbye/,domain=src.example`, nil); err != nil {
		t.Fatal(err)
	}

	// referrer 命中 → 改写。
	req := newB10TestRequest("https://target.example/api", http.Header{"Referer": {"https://src.example/page"}})
	res := b10JSONRes(`{"greeting":"hello world"}`)
	if _, err := nr.ModifyRes(req, res); err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), "goodbye") {
		t.Fatalf("expected referrer-matched $replace to rewrite, got %q", string(b))
	}

	// 目标域命中但 referrer 不命中 → 不改写。
	req2 := newB10TestRequest("https://src.example/api", http.Header{"Referer": {"https://other.example/page"}})
	res2 := b10JSONRes(`{"greeting":"hello world"}`)
	if _, err := nr.ModifyRes(req2, res2); err != nil {
		t.Fatal(err)
	}
	b2, _ := io.ReadAll(res2.Body)
	if !strings.Contains(string(b2), "hello") {
		t.Fatalf("$replace must match referrer only (docs L581-582), got %q", string(b2))
	}
}

// TestBadfilterPartialDisableOnResponsePath：$badfilter 部分禁用（doc
// 1494-1499 交集语义）在响应路径生效——闭合 rule.go 读 res.Request 恒 nil
// 导致"部分禁用在响应路径从不生效"的潜伏缺口。隔离设计：badfilter 的禁用
// 域（other.example）只命中第二个请求的 referrer，而两个请求的 $domain 条件
// 本身都成立（一个经 referrer、一个经 L570 目标域链接）。
func TestBadfilterPartialDisableOnResponsePath(t *testing.T) {
	t.Parallel()

	nr := New()
	if _, err := nr.ParseRule(`$cookie=x,domain=src.example`, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := nr.ParseRule(`$cookie=x,domain=other.example,badfilter`, nil); err != nil {
		t.Fatal(err)
	}
	nr.Compact()

	// referrer=src.example：$domain 条件命中（referrer）；禁用域
	// other.example 不命中 → 规则生效，cookie 被过期改写。
	req1 := newB10TestRequest("https://target.example/a", http.Header{"Referer": {"https://src.example/page"}})
	res1 := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "Set-Cookie": []string{"x=1; Path=/"}},
	}
	if _, err := nr.ModifyRes(req1, res1); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res1.Header.Values("Set-Cookie"), "|"); !strings.Contains(got, "Max-Age=0") {
		t.Fatalf("expected rewrite when disable-domain does not match referrer, got %q", got)
	}

	// referrer=other.example：$domain 条件仍命中（$cookie 在 L570 家族内，
	// 目标域 src.example 链接生效）；但禁用域 other.example 命中 referrer
	// → 部分禁用生效 → cookie 保持原样。B10 前此处会错误改写（缺口）。
	req2 := newB10TestRequest("https://src.example/a", http.Header{"Referer": {"https://other.example/page"}})
	res2 := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "Set-Cookie": []string{"x=1; Path=/"}},
	}
	if _, err := nr.ModifyRes(req2, res2); err != nil {
		t.Fatal(err)
	}
	got2 := strings.Join(res2.Header.Values("Set-Cookie"), "|")
	if !strings.Contains(got2, "x=1") || strings.Contains(got2, "Max-Age=0") {
		t.Fatalf("expected partial badfilter disablement to apply on response path (gap closure), got %q", got2)
	}
}

// TestCancelsResUnchangedBareWhitelist：B2 红线钉子——裸白名单取消语义在
// 响应路径与 B10 前一致。
func TestCancelsResUnchangedBareWhitelist(t *testing.T) {
	t.Parallel()

	nr := New()
	if _, err := nr.ParseRule(`$cookie=x`, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := nr.ParseRule(`@@||target.example^`, nil); err != nil {
		t.Fatal(err)
	}
	nr.Compact()

	req := newB10TestRequest("https://target.example/a", nil)
	res := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "Set-Cookie": []string{"x=1; Path=/"}},
	}
	if _, err := nr.ModifyRes(req, res); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(res.Header.Values("Set-Cookie"), "|")
	if got != "x=1; Path=/" {
		t.Fatalf("bare whitelist must cancel $cookie on response path as before B10, got %q", got)
	}
}

// TestXhrMemberOnResponsePathByDest：$xmlhttprequest 成员在响应路径按请求
// 的 Sec-Fetch-Dest 裁决（docs L1046-1058）。
func TestXhrMemberOnResponsePathByDest(t *testing.T) {
	t.Parallel()

	nr := New()
	if _, err := nr.ParseRule(`$replace=/hello/goodbye/,xmlhttprequest`, nil); err != nil {
		t.Fatal(err)
	}

	run := func(dest string) string {
		req := newB10TestRequest("https://example.com/api", http.Header{"Sec-Fetch-Dest": {dest}})
		res := b10JSONRes(`{"greeting":"hello world"}`)
		if _, err := nr.ModifyRes(req, res); err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}

	if got := run("empty"); !strings.Contains(got, "goodbye") {
		t.Fatalf("dest=empty must apply $replace xhr rule, got %q", got)
	}
	if got := run("document"); !strings.Contains(got, "hello") {
		t.Fatalf("dest=document must not apply $replace xhr rule, got %q", got)
	}
	if got := run("script"); !strings.Contains(got, "hello") {
		t.Fatalf("dest=script must not apply $replace xhr rule, got %q", got)
	}
}

// TestXhrNoMetadataLegacyFallback：req 无 Sec-Fetch-Dest → 回退现行
// Content-Type 逻辑（xhr 成员 false，保持 pre-B10 行为）。
func TestXhrNoMetadataLegacyFallback(t *testing.T) {
	t.Parallel()

	nr := New()
	if _, err := nr.ParseRule(`$replace=/hello/goodbye/,xmlhttprequest`, nil); err != nil {
		t.Fatal(err)
	}

	req := newB10TestRequest("https://example.com/api", nil) // 无 dest
	res := b10JSONRes(`{"greeting":"hello world"}`)
	if _, err := nr.ModifyRes(req, res); err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	if strings.Contains(string(b), "goodbye") {
		t.Fatal("dest-missing fallback must keep pre-B10 behavior (no xhr match)")
	}
}

// TestNotXhrInversionOnResponsePath：~xmlhttprequest 行为修正——dest=empty
// 时 false（B10 前对 unknown CT 恒 true），dest=document 时按 dest 判 true。
func TestNotXhrInversionOnResponsePath(t *testing.T) {
	t.Parallel()

	nr := New()
	if _, err := nr.ParseRule(`$replace=/hello/goodbye/,~xmlhttprequest`, nil); err != nil {
		t.Fatal(err)
	}

	run := func(dest string) string {
		req := newB10TestRequest("https://example.com/api", http.Header{"Sec-Fetch-Dest": {dest}})
		res := b10JSONRes(`{"greeting":"hello world"}`)
		if _, err := nr.ModifyRes(req, res); err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}

	if got := run("empty"); !strings.Contains(got, "hello") {
		t.Fatalf("~xhr with dest=empty must NOT apply, got %q", got)
	}
	if got := run("document"); !strings.Contains(got, "goodbye") {
		t.Fatalf("~xhr with dest=document must apply, got %q", got)
	}
}
