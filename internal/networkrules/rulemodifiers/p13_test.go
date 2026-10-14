package rulemodifiers_test

// [P3 2026-10-08] P1+P3 批次端到端用例（comp_audit TOP-1/TOP-5）：
//   - SA-B 5 条真实语料表达式（AdGuard Base 6ead4595 行 46278/46280/46281/46282
//     的未引号联合 ×4 ＋行 46283 的 has 过滤器 ×1；矩阵 yt_compensation_matrix.txt
//     行 1936/1940/1944/1948/1952）全规则文本经引擎 ParseRule 验收；
//   - 语料表达式的响应改写端到端（mock youtubei 响应 → 断言广告键被删）；
//   - 方言归一化与原生形态解析等价（行为级幂等验证）；
//   - jsonprune apply 失败告警（按规则文本去重）；
//   - scriptlet 未知名/trusted-* 告警（去重＋单独措辞）。
// 字符串级归一化用例在 internal/networkrules/rulemodifiers/p13_test.go；
// 44 名单一致性用例在 internal/asset/scriptlet/p13_test.go。

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/asset/scriptlet"
	"github.com/irbis-sh/zen-desktop/internal/networkrules"
	rulemodifiers "github.com/irbis-sh/zen-desktop/internal/networkrules/rulemodifiers"
)

// p13CorpusRules 语料 5 条真实规则全文（SA-B，逐字取自缓存过滤文件行 46278-46283）。
var p13CorpusRules = []string{
	// 46278
	`.com/watch?$xmlhttprequest,jsonprune=\$..[adPlacements\, adSlots\, playerAds],domain=youtubekids.com|youtube-nocookie.com|youtube.com`,
	// 46280
	`/youtubei/v*/get_watch?$jsonprune=\$..[adPlacements\, adSlots\, playerAds],domain=m.youtube.com|youtubekids.com|youtube-nocookie.com`,
	// 46281
	`/youtubei/v*/player?$jsonprune=\$..[adPlacements\, adSlots\, playerAds],domain=youtubekids.com|youtube-nocookie.com|youtube.com`,
	// 46282
	`.com/playlist?list=$jsonprune=\$..[adPlacements\, adSlots\, playerAds],domain=youtubekids.com|youtube-nocookie.com|youtube.com`,
	// 46283
	`/youtubei/v*/reel/reel_watch$jsonprune=\$.entries[?(has "\$..command.reelWatchEndpoint.adClientParams.isAd")],domain=youtube.com`,
}

// TestP13CorpusRuleAcceptance 5 条语料规则必须被引擎接受（P1 前 4+1 条均在
// parse 期被拒：parse JSONPath: wrong symbol '\' at 0）。
func TestP13CorpusRuleAcceptance(t *testing.T) {
	for i, raw := range p13CorpusRules {
		nr := networkrules.New()
		if _, err := nr.ParseRule(raw, nil); err != nil {
			t.Errorf("corpus rule #%d rejected: %v\nrule: %s", i+1, err, raw)
		}
	}
}

func p13JSONResponse(t *testing.T, body string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func p13Get(t *testing.T, url string) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodGet, url, nil)
}

func p13ReadAll(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// TestP13CorpusUnionPruneEndToEnd 未引号联合规则对 mock player 响应的递归删键。
func TestP13CorpusUnionPruneEndToEnd(t *testing.T) {
	nr := networkrules.New()
	rule := `||youtube.com/youtubei/v1/player$jsonprune=\$..[adPlacements\, adSlots\, playerAds]`
	if _, err := nr.ParseRule(rule, nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	body := `{"adPlacements":[{"adSlotRenderer":{"k":"v"}}],"adSlots":[{"k":"v"}],"playerAds":[{"k":"v"}],"playerResponse":{"adPlacements":[{"deep":"yes"}]},"streamingData":{"url":"keep"}}`
	res := p13JSONResponse(t, body)
	req := p13Get(t, "https://www.youtube.com/youtubei/v1/player?key=x")
	applied, err := nr.ModifyRes(req, res)
	if err != nil {
		t.Fatalf("ModifyRes: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied=%d, want 1", len(applied))
	}
	got := p13ReadAll(t, res)
	for _, banned := range []string{"adPlacements", "adSlots", "playerAds"} {
		if strings.Contains(got, banned) {
			t.Errorf("ad key %q still present after prune: %s", banned, got)
		}
	}
	if !strings.Contains(got, "streamingData") {
		t.Errorf("non-ad key streamingData lost: %s", got)
	}
}

// TestP13CorpusHasFilterEndToEnd has 过滤器规则：只删命中条目（isAd:true 的
// entry 整体删除，isAd:false 与无关键保留——探针实测的预期行为）。
func TestP13CorpusHasFilterEndToEnd(t *testing.T) {
	nr := networkrules.New()
	rule := `||youtube.com/youtubei/v1/reel/reel_watch$jsonprune=\$.entries[?(has "\$..command.reelWatchEndpoint.adClientParams.isAd")]`
	if _, err := nr.ParseRule(rule, nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	body := `{"entries":[{"command":{"reelWatchEndpoint":{"adClientParams":{"isAd":true}}}},{"command":{"reelWatchEndpoint":{"adClientParams":{"isAd":false}}}}],"tracking":"keep"}`
	res := p13JSONResponse(t, body)
	req := p13Get(t, "https://www.youtube.com/youtubei/v1/reel/reel_watch")
	applied, err := nr.ModifyRes(req, res)
	if err != nil {
		t.Fatalf("ModifyRes: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied=%d, want 1", len(applied))
	}
	got := p13ReadAll(t, res)
	if strings.Contains(got, `"isAd":true`) {
		t.Errorf("ad entry survived: %s", got)
	}
	if !strings.Contains(got, `"isAd":false`) {
		t.Errorf("non-ad entry wrongly deleted: %s", got)
	}
	if !strings.Contains(got, "tracking") {
		t.Errorf("non-ad key tracking lost: %s", got)
	}
}

// TestP13DialectParseEquivalence 方言形态与归一化后原生形态解析等价
// （Cancels 基于 commands 全等：等价成立即归一化单趟正确；原生形态侧
// 由 rulemodifiers 内的逐字节用例保证归一化层不动它，两者合起来构成
// 行为级幂等验证）。
func TestP13DialectParseEquivalence(t *testing.T) {
	cases := []struct{ dialect, native string }{
		{
			`jsonprune=\$..[adPlacements, adSlots, playerAds]`,
			`jsonprune=$..['adPlacements','adSlots','playerAds']`,
		},
		{
			`jsonprune=\$..['adPlacements','adSlots','playerAds']`,
			`jsonprune=$..['adPlacements','adSlots','playerAds']`,
		},
		{
			`jsonprune=$.entries[?(has "\$..command.reelWatchEndpoint.adClientParams.isAd")]`,
			`jsonprune=$.entries[?(@..command.reelWatchEndpoint.adClientParams.isAd)]`,
		},
		{
			`jsonprune=$.state_machine.tracks[?(key-eq 'content_type' 'AD')]`,
			`jsonprune=$.state_machine.tracks[?(@.content_type == 'AD')]`,
		},
		{
			`jsonprune=$.data.x[?(key-substr 'entryId' 'promoted-tweet-')]`,
			`jsonprune=$.data.x[?(@.entryId =~ ".*promoted-tweet-.*")]`,
		},
		{
			`jsonprune=$.results[?(has adTitle)]`,
			`jsonprune=$.results[?(@.adTitle)]`,
		},
	}
	for i, tc := range cases {
		md := &rulemodifiers.JSONPruneModifier{}
		if err := md.Parse(tc.dialect); err != nil {
			t.Fatalf("#%d dialect parse: %v", i, err)
		}
		mn := &rulemodifiers.JSONPruneModifier{}
		if err := mn.Parse(tc.native); err != nil {
			t.Fatalf("#%d native parse: %v", i, err)
		}
		if !md.Cancels(mn) {
			t.Errorf("#%d dialect commands != native commands\ndialect=%s\nnative =%s", i, tc.dialect, tc.native)
		}
	}
}

// TestP13KeySubstrLiteralEscaping key-substr 值的正则元字符必须按字面量匹配
// （双层转义：正则层 QuoteMeta＋ajson 字面量层 \ 双写）——若转义链断裂，
// apply 会整段报错（一个都不删）或把 . 当通配符（axb 也被删），两种都过不了。
func TestP13KeySubstrLiteralEscaping(t *testing.T) {
	nr := networkrules.New()
	rule := `||example.test/data$jsonprune=$.items[?(key-substr 'tag' 'a.b')]`
	if _, err := nr.ParseRule(rule, nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	body := `{"items":[{"tag":"x.a.by","del":1},{"tag":"axby","keep":2},{"tag":"aby","keep":3}]}`
	res := p13JSONResponse(t, body)
	req := p13Get(t, "https://example.test/data")
	applied, err := nr.ModifyRes(req, res)
	if err != nil {
		t.Fatalf("ModifyRes: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied=%d, want 1", len(applied))
	}
	got := p13ReadAll(t, res)
	// 命中（tag 含字面量 a.b）的条目被删；含元字符误匹配的条目必须保留。
	if strings.Contains(got, `"x.a.by"`) || strings.Contains(got, `"del"`) {
		t.Errorf("literal a.b was not matched as substring: %s", got)
	}
	if !strings.Contains(got, `"axby"`) || !strings.Contains(got, `"aby"`) {
		t.Errorf("regex metachar was not literalized (axb/ab wrongly deleted): %s", got)
	}
}

// TestP13JSONPruneApplyFailureLoggedOnce apply 失败必须记日志且按规则文本去重。
func TestP13JSONPruneApplyFailureLoggedOnce(t *testing.T) {
	md := &rulemodifiers.JSONPruneModifier{}
	// 可 parse 但 apply 期报 wrong request 的形态（裸词非常量）；文本唯一，
	// 防止与其他用例/门禁的去重键串扰。
	expr := `jsonprune=$..[?(@.p13applyfail 'x')]`
	if err := md.Parse(expr); err != nil {
		t.Fatalf("parse: %v", err)
	}

	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	res := p13JSONResponse(t, `{"a":1}`)
	modified, err := md.ModifyRes(res)
	if err != nil {
		t.Fatalf("ModifyRes: %v", err)
	}
	if modified {
		t.Fatalf("nothing should be pruned when apply fails")
	}
	want := `jsonprune "$..[?(@.p13applyfail 'x')]": apply failed`
	if n := strings.Count(buf.String(), want); n != 1 {
		t.Fatalf("apply-failure log count=%d, want 1\ngot: %s", n, buf.String())
	}

	// 同一规则第二次 apply 失败：不再告警。
	buf.Reset()
	res2 := p13JSONResponse(t, `{"a":1}`)
	if _, err := md.ModifyRes(res2); err != nil {
		t.Fatalf("ModifyRes #2: %v", err)
	}
	if got := buf.String(); strings.Contains(got, want) {
		t.Fatalf("apply-failure log not deduplicated: %s", got)
	}
}

// TestP13ScriptletUnknownNameWarnings 未知名首次告警＋去重；trusted-* 单独措辞；
// 已知名不告警。
func TestP13ScriptletUnknownNameWarnings(t *testing.T) {
	inj, err := scriptlet.NewInjectorWithDefaults()
	if err != nil {
		t.Fatalf("NewInjectorWithDefaults: %v", err)
	}

	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	if err := inj.AddRule(`a.example#%#//scriptlet('p13-unknown-probe-name')`, false); err != nil {
		t.Fatalf("AddRule unknown: %v", err)
	}
	want := `scriptlet "p13-unknown-probe-name" has no implementation (rule inert)`
	if n := strings.Count(buf.String(), want); n != 1 {
		t.Fatalf("unknown-name warning count=%d, want 1\ngot: %s", n, buf.String())
	}

	// 同名第二次：不重复告警。
	if err := inj.AddRule(`b.example#%#//scriptlet('p13-unknown-probe-name')`, false); err != nil {
		t.Fatalf("AddRule unknown again: %v", err)
	}
	if n := strings.Count(buf.String(), want); n != 1 {
		t.Fatalf("unknown-name warning not deduplicated: %s", buf.String())
	}

	// trusted-* 前缀（受信列表才入库）：单独措辞。
	if err := inj.AddRule(`a.example#%#//scriptlet('trusted-p13-probe-name','v')`, true); err != nil {
		t.Fatalf("AddRule trusted: %v", err)
	}
	wantTrusted := `trusted scriptlet not yet implemented`
	if n := strings.Count(buf.String(), wantTrusted); n != 1 {
		t.Fatalf("trusted warning count=%d, want 1\ngot: %s", n, buf.String())
	}

	// 已知名：零告警。
	before := buf.Len()
	if err := inj.AddRule(`a.example#%#//scriptlet('set-constant','x','1')`, false); err != nil {
		t.Fatalf("AddRule known: %v", err)
	}
	if buf.Len() != before {
		t.Fatalf("known name warned unexpectedly: %s", buf.String()[before:])
	}
}
