package scriptlet_test

// P2+P4 批次专用用例（本文件只新增，不改既有测试）：
//  - P2：真实语料 4+1 条 json-prune 扩展路径规则的注入产物断言（参数转义保真）；
//  - P4：12 个 trusted-* 键在信任清单上的注入产物断言＋非信任清单拒绝；
//  - bundle.js 是 embed 资产，注入内容变化只能在 Injector.GetAsset 产物级断言。
// 注入产物形态（arglist.go GenerateInjection）：
//   try{scriptlet(<JSON 参数逗号连接>)}catch(ex){console.error(ex);}

import (
	"strings"
	"testing"

	scriptlet "github.com/irbis-sh/zen-desktop/internal/asset/scriptlet"
)

func newP24Injector(t *testing.T) *scriptlet.Injector {
	t.Helper()
	inj, err := scriptlet.NewInjectorWithDefaults()
	if err != nil {
		t.Fatalf("NewInjectorWithDefaults: %v", err)
	}
	return inj
}

func mustAddP24(t *testing.T, inj *scriptlet.Injector, rule string, trusted bool) {
	t.Helper()
	if err := inj.AddRule(rule, trusted); err != nil {
		t.Fatalf("AddRule(%q, %v): %v", rule, trusted, err)
	}
}

func assetOfP24(t *testing.T, inj *scriptlet.Injector, host string) string {
	t.Helper()
	out, err := inj.GetAsset(host)
	if err != nil {
		t.Fatalf("GetAsset(%q): %v", host, err)
	}
	return string(out)
}

func wantP24(t *testing.T, asset, substr, note string) {
	t.Helper()
	if !strings.Contains(asset, substr) {
		t.Errorf("[P24注入] %s: 产物缺少 %q", note, substr)
	}
}

// TestP24JsonPruneExtendedInjected P2 门禁③：真实语料 4+1 条惰性规则的注入产物
// 与参数转义（46339/46347/46348/46357＋wykop [=] 值过滤器）。
func TestP24JsonPruneExtendedInjected(t *testing.T) {
	cases := []struct {
		note string
		rule string
		host string
		want string
	}{
		{
			// AdGuard Base 6ead4595 行 46339（Shorts 贴片判定）
			note: "46339 Shorts entries.[-]",
			rule: `youtube.com#%#//scriptlet('json-prune', 'entries.[-].command.reelWatchEndpoint.adClientParams.isAd')`,
			host: "youtube.com",
			want: `scriptlet("json-prune","entries.[-].command.reelWatchEndpoint.adClientParams.isAd")`,
		},
		{
			// 行 46347（首页/浏览页 feed 广告位）
			note: "46347 feed adSlotRenderer",
			rule: `www.youtube.com#%#//scriptlet('json-prune', 'contents.twoColumnBrowseResultsRenderer.tabs.[].tabRenderer.content.richGridRenderer.contents.[-].richItemRenderer.content.adSlotRenderer')`,
			host: "www.youtube.com",
			want: `scriptlet("json-prune","contents.twoColumnBrowseResultsRenderer.tabs.[].tabRenderer.content.richGridRenderer.contents.[-].richItemRenderer.content.adSlotRenderer")`,
		},
		{
			// 行 46348（ytInitialData 前缀变体）
			note: "46348 ytInitialData 前缀",
			rule: `www.youtube.com#%#//scriptlet('json-prune', 'ytInitialData.contents.twoColumnBrowseResultsRenderer.tabs.[].tabRenderer.content.richGridRenderer.contents.[-].richItemRenderer.content.adSlotRenderer')`,
			host: "www.youtube.com",
			want: `scriptlet("json-prune","ytInitialData.contents.twoColumnBrowseResultsRenderer.tabs.[].tabRenderer.content.richGridRenderer.contents.[-].richItemRenderer.content.adSlotRenderer")`,
		},
		{
			// 行 46357（m.youtube get_watch：$.. 递归 descent＋[?()] 谓词，fetch 家族）
			note: "46357 get_watch $..+[?()]",
			rule: `m.youtube.com#%#//scriptlet('json-prune-fetch-response', '$..itemSectionRenderer.contents[?(@.adSlotRenderer)]', '', '/get_watch')`,
			host: "m.youtube.com",
			want: `scriptlet("json-prune-fetch-response","$..itemSectionRenderer.contents[?(@.adSlotRenderer)]","","/get_watch")`,
		},
		{
			// wykop.pl（[=] 值过滤器＋/regex/ 形态；反斜杠按字面保留→JSON 编码翻倍）
			note: "wykop [=] 值过滤器",
			rule: `wykop.pl#%#//scriptlet('json-prune', '*', '*.target.[=]./ad\.doubleclick\.net/')`,
			host: "wykop.pl",
			want: `scriptlet("json-prune","*","*.target.[=]./ad\\.doubleclick\\.net/")`,
		},
	}
	for _, tc := range cases {
		inj := newP24Injector(t)
		mustAddP24(t, inj, tc.rule, false)
		asset := assetOfP24(t, inj, tc.host)
		wantP24(t, asset, tc.want, tc.note)
	}
}

// TestP24TrustedScriptletsInjected P4 门禁③：12 个 trusted-* 键逐个注入产物断言。
func TestP24TrustedScriptletsInjected(t *testing.T) {
	cases := []struct {
		note string
		rule string
		host string
		want string
	}{
		{
			note: "trusted-replace-node-text",
			rule: `example.com#%#//scriptlet('trusted-replace-node-text', 'div', 'some', 'text', 'other text')`,
			host: "example.com",
			want: `scriptlet("trusted-replace-node-text","div","some","text","other text")`,
		},
		{
			// AdGuard Base 6ead4595 行 46309（YouTube 反反拦截中和器，真实语料原行）
			note: "trusted-create-element(46309)",
			rule: `www.youtube.com#%#//scriptlet('trusted-create-element', 'head', 'script', '', '(()=>{try{document.currentScript.remove()}catch{}const t={apply:(t,e,o)=>{const n=o[0];return"function"==typeof n&&n.toString().includes("onAbnormalityDetected")&&(o[0]=function(){}),Reflect.apply(t,e,o)}};window.Promise.prototype.then=new Proxy(window.Promise.prototype.then,t)})();')`,
			host: "www.youtube.com",
			want: `scriptlet("trusted-create-element","head","script","",`,
		},
		{
			note: "trusted-replace-argument",
			rule: `example.com#%#//scriptlet('trusted-replace-argument', 'JSON.parse', '0', 'replace:/ads/no_ads/g', 'ads')`,
			host: "example.com",
			want: `scriptlet("trusted-replace-argument","JSON.parse","0","replace:/ads/no_ads/g","ads")`,
		},
		{
			note: "trusted-click-element",
			rule: `example.com#%#//scriptlet('trusted-click-element', 'button[name="agree"]', 'cookie:cmpconsent', '500')`,
			host: "example.com",
			want: `scriptlet("trusted-click-element","button[name=\"agree\"]","cookie:cmpconsent","500")`,
		},
		{
			note: "trusted-suppress-native-method",
			rule: `example.com#%#//scriptlet('trusted-suppress-native-method', 'localStorage.setItem', '/key/|"value"', 'prevent')`,
			host: "example.com",
			want: `scriptlet("trusted-suppress-native-method","localStorage.setItem","/key/|\"value\"","prevent")`,
		},
		{
			note: "trusted-set-local-storage-item",
			rule: `example.com#%#//scriptlet('trusted-set-local-storage-item', 'COOKIE_CONSENTS', '{"preferences":3,"flag":false}')`,
			host: "example.com",
			want: `scriptlet("trusted-set-local-storage-item","COOKIE_CONSENTS","{\"preferences\":3,\"flag\":false}")`,
		},
		{
			note: "trusted-set-constant",
			rule: `example.com#%#//scriptlet('trusted-set-constant', 'click_r', '"null"')`,
			host: "example.com",
			want: `scriptlet("trusted-set-constant","click_r","\"null\"")`,
		},
		{
			note: "trusted-set-cookie",
			rule: `example.com#%#//scriptlet('trusted-set-cookie', 'cmpconsent', 'accept', '1day', '/', 'example.com')`,
			host: "example.com",
			want: `scriptlet("trusted-set-cookie","cmpconsent","accept","1day","/","example.com")`,
		},
		{
			note: "trusted-prune-inbound-object",
			rule: `example.com#%#//scriptlet('trusted-prune-inbound-object', 'JSON.stringify', 'foo.bar', '', 'test.js')`,
			host: "example.com",
			want: `scriptlet("trusted-prune-inbound-object","JSON.stringify","foo.bar","","test.js")`,
		},
		{
			// nba.com 真实语料形态（HLS 广告段裁剪）
			note: "trusted-replace-xhr-response",
			rule: `nba.com#%#//scriptlet('trusted-replace-xhr-response', '/#EXT-X-DATERANGE:ID="AD-BREAK:[\s\S]*?(\.googlevideo\/|fwmrm\.net)[\s\S]*?#EXT-X-DISCONTINUITY/', '', '/\.akamaized\.net\/(vod|live)-.*\/.*\/hls-.*\/.*\.m3u8/')`,
			host: "nba.com",
			want: `scriptlet("trusted-replace-xhr-response","/#EXT-X-DATERANGE:ID=\"AD-BREAK:[\\s\\S]*?(\\.googlevideo\\/|fwmrm\\.net)[\\s\\S]*?#EXT-X-DISCONTINUITY/","",`,
		},
		{
			note: "trusted-replace-fetch-response",
			rule: `example.com#%#//scriptlet('trusted-replace-fetch-response', '/noAds=false/g', 'noAds=true', 'example.com')`,
			host: "example.com",
			want: `scriptlet("trusted-replace-fetch-response","/noAds=false/g","noAds=true","example.com")`,
		},
		{
			note: "trusted-json-set",
			rule: `example.com#%#//scriptlet('trusted-json-set', 'JSON.parse', 'ads.enabled', 'false', '', 'result', 'adManager')`,
			host: "example.com",
			want: `scriptlet("trusted-json-set","JSON.parse","ads.enabled","false","","result","adManager")`,
		},
	}
	for _, tc := range cases {
		inj := newP24Injector(t)
		mustAddP24(t, inj, tc.rule, true)
		asset := assetOfP24(t, inj, tc.host)
		wantP24(t, asset, tc.want, tc.note)
	}
}

// TestP24TrustedCreateElementArgEscape 46309 真实行参数转义保真抽查：
// 双引号经 JSON 编码转义为 \"，onAbnormalityDetected 标记串完整进入产物。
func TestP24TrustedCreateElementArgEscape(t *testing.T) {
	inj := newP24Injector(t)
	mustAddP24(t, inj, `www.youtube.com#%#//scriptlet('trusted-create-element', 'head', 'script', '', '(()=>{try{document.currentScript.remove()}catch{}const t={apply:(t,e,o)=>{const n=o[0];return"function"==typeof n&&n.toString().includes("onAbnormalityDetected")&&(o[0]=function(){}),Reflect.apply(t,e,o)}};window.Promise.prototype.then=new Proxy(window.Promise.prototype.then,t)})();')`, true)
	asset := assetOfP24(t, inj, "www.youtube.com")
	wantP24(t, asset, `includes(\"onAbnormalityDetected\")`, "46309 双引号转义")
	wantP24(t, asset, `window.Promise.prototype.then=new Proxy(window.Promise.prototype.then,t)})();`, "46309 尾段完整")
}

// TestP24TrustedGatePerName 信任门按名字前缀对新键逐一生效（非信任清单拒绝）。
func TestP24TrustedGatePerName(t *testing.T) {
	names := []string{
		"trusted-replace-node-text", "trusted-create-element", "trusted-replace-argument",
		"trusted-click-element", "trusted-suppress-native-method", "trusted-set-local-storage-item",
		"trusted-set-constant", "trusted-set-cookie", "trusted-prune-inbound-object",
		"trusted-replace-xhr-response", "trusted-replace-fetch-response", "trusted-json-set",
	}
	for _, name := range names {
		inj := newP24Injector(t)
		rule := `example.com#%#//scriptlet('` + name + `', 'a')`
		if err := inj.AddRule(rule, false); err == nil {
			t.Errorf("[P24信任门] %s 在非信任清单应被拒绝，实际通过", name)
		}
	}
}
