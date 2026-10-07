package tmp_enginebench

// golden_test.go — 黄金对拍（golden master）测试。
//
// 基准侧：tmp_enginebench/baselinenr/networkrules —— 改前引擎的冻结副本。
// 生产侧：internal/networkrules —— 允许被并行优化修改（本测试不改 internal/）。
//
// 断言（每对调用）：两引擎 ModifyReq 的 (shouldBlock, redirectURL,
// len(appliedRules)) 三元组完全一致；非阻塞情形下 appliedRules 的 RawRule
// 集合加严比较（阻塞情形下单条返回依赖 ruletree.Get 的 map 遍历序，逐次运行
// 不稳定，故阻塞时只比三元组——与任务口径一致）；ModifyRes 无 panic 且
// (err 是否为 nil, len(appliedRules), RawRule 集合) 一致。
//
// 对抗性门禁 TestGoldenAdversarialRules：50+ 条畸形/极端规则，
// 断言不 panic、两引擎"都接受或都拒绝"、ModifyReq 全程不 panic。
// 发现差异如实记录，禁止为通过测试修改 internal/ 或 baselinenr 的代码。
//
// 语料来源：
//   - 规则行：本机 Zen filters 缓存（loadRealLines，17 个 *.cache.txt）
//   - urls.txt：internal/ruletree/testdata/urls.txt 的副本（33,303 行）
//   - 广告测试 URL：d3ward/toolz d3Host 清单（CC BY-NC-SA，仅本地测试用）
//     d3host_raw.txt + adblock_data.json（d3ward adblock 测试页所用域名），
//     另加主流广告/追踪域名与路径形态的构造清单。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	prodnr "github.com/irbis-sh/zen-desktop/internal/networkrules"
	baselinenr "github.com/irbis-sh/zen-desktop/tmp_enginebench/baselinenr/networkrules"
)

// ---------------- 引擎构建 ----------------

func goldenBuildBaseline(t testing.TB) *baselinenr.NetworkRules {
	t.Helper()
	nr := baselinenr.New()
	name := "golden-baseline"
	for _, l := range loadRealLines(t) {
		if _, err := nr.ParseRule(l, &name); err != nil {
			_ = err // 与生产 filter.AddURL 语义一致：解析失败静默跳过
		}
	}
	return nr
}

func goldenBuildProduction(t testing.TB) *prodnr.NetworkRules {
	t.Helper()
	nr := prodnr.New()
	name := "golden-prod"
	for _, l := range loadRealLines(t) {
		if _, err := nr.ParseRule(l, &name); err != nil {
			_ = err
		}
	}
	return nr
}

// ---------------- URL 集 ----------------

// goldenLoadURLs 返回 urls.txt 全部行（已按 url.Parse 过滤）。
func goldenLoadURLs(t testing.TB) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "internal", "ruletree", "testdata", "urls.txt"))
	if err != nil {
		t.Fatalf("读 urls.txt: %v", err)
	}
	var urls []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := url.Parse(line); err != nil {
			continue
		}
		urls = append(urls, line)
	}
	return urls
}

// goldenAdHosts 从 d3ward d3host_raw.txt 与 adblock_data.json 抽取广告/追踪域名。
func goldenAdHosts(t testing.TB) []string {
	t.Helper()
	seen := map[string]struct{}{}
	var hosts []string
	add := func(h string) {
		h = strings.TrimSpace(h)
		if h == "" || strings.ContainsAny(h, " #") {
			return
		}
		if _, ok := seen[h]; ok {
			return
		}
		seen[h] = struct{}{}
		hosts = append(hosts, h)
	}
	// 1) d3host.txt 形如 "0.0.0.0 host"
	if data, err := os.ReadFile("d3host_raw.txt"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 2 {
				add(fields[1])
			}
		}
	} else {
		t.Logf("提示: d3host_raw.txt 不可读(%v)，仅用 adblock_data.json", err)
	}
	// 2) adblock_data.json: {"分类": {"厂商": [host...]}}
	if data, err := os.ReadFile("adblock_data.json"); err == nil {
		var m map[string]map[string][]string
		if err := json.Unmarshal(data, &m); err == nil {
			for _, vendors := range m {
				for _, hs := range vendors {
					for _, h := range hs {
						add(h)
					}
				}
			}
		} else {
			t.Logf("提示: adblock_data.json 解析失败: %v", err)
		}
	} else {
		t.Logf("提示: adblock_data.json 不可读(%v)", err)
	}
	return hosts
}

// goldenAdPaths 是常见广告/追踪资源的路径形态（轮转分配给各 host）。
var goldenAdPaths = []string{
	"/pagead/js/adsbygoogle.js",
	"/gpt/pubads_impl_2026100501.js",
	"/gtag/js?id=G-XXXXXXX&cx=c",
	"/collect?v=2&tid=UA-12345-6&cid=%d&t=pageview&dp=/news/article.html",
	"/j/collect?t=a&v=2&_v=j101&cid=%d",
	"/analytics.js",
	"/pixel.gif?uid=%d&ts=1696500000",
	"/beacon.gif?account_id=100&url=/news/article.html",
	"/tr?id=%d&ev=PageView&dl=https://www.example.com/news/article.html",
	"/tr/?id=%d&ev=PageView&noscript=1",
	"/en_US/fbevents.js",
	"/insight.min.js",
	"/li.lms-analytics/insight.min.js",
	"/collect/?pid=1001&url=/news/article.html",
	"/i/adsct?txn_id=abc&pid=%d",
	"/action/0?ti=5500&Ver=2&mid=%d",
	"/tag/xyz123",
	"/dis/rtb/pv/%d?adomain=example.com",
	"/cdb?profileId=712&av=35&wid=%d",
	"/pxj?bidder=305&action=setuid&uid=%d",
	"/ut/v3/prebid",
	"/openrtb2/auction",
	"/prebid/hb_multi_%d.json",
	"/header-bidding/bid?slot=1&host=%d",
	"/api/2/eventlog?i=%d",
	"/v1/track?t=en&e=pageview&url=/news",
	"/v3/pixel.gif?e=pv&i=%d",
	"/event?v=1&cat=pageview&url=%d",
	"/quant.js",
	"/matomo.php?idsite=1&rec=1&r=%d",
	"/sync?dsp_id=42&user_id=%d",
	"/cm?ci=100&st=1000&si=%d",
	"/activityi;src=100;type=inv;qty=1;ord=%d",
	"/pg?pid=100&uid=%d",
	"/s/track?e=%d",
	"/setuid?bidder=pubmatic&uid=%d",
	"/match?google_gid=%d",
}

// goldenAdURLs 构造广告测试网站封锁目标 URL 集（≥200 条）：
// d3ward 官方测试域名 × 轮转路径（每域名 2 变体）+ 主流厂商构造清单。
func goldenAdURLs(t testing.TB) []string {
	t.Helper()
	seen := map[string]struct{}{}
	var urls []string
	add := func(u string) {
		if _, ok := seen[u]; ok {
			return
		}
		seen[u] = struct{}{}
		urls = append(urls, u)
	}
	hosts := goldenAdHosts(t)
	if len(hosts) == 0 {
		t.Fatal("广告域名清单为空：d3host_raw.txt / adblock_data.json 均不可用")
	}
	for i, h := range hosts {
		for v := 0; v < 2; v++ {
			p := goldenAdPaths[(i*2+v)%len(goldenAdPaths)]
			var sb strings.Builder
			sb.WriteString("https://")
			sb.WriteString(h)
			if strings.ContainsRune(p, '%') {
				fmt.Fprintf(&sb, p, i*7+v*3)
			} else {
				sb.WriteString(p)
				if v == 1 {
					fmt.Fprintf(&sb, "?cb=%d&ref=news", i)
				}
			}
			add(sb.String())
		}
	}
	// 主流厂商/路径形态构造清单（对 d3ward 清单的补充）。
	static := []string{
		"https://securepubads.g.doubleclick.net/gpt/pubads_impl_300.js",
		"https://tpc.googlesyndication.com/pagead/js/adsbygoogle.js",
		"https://www.googletagservices.com/tag/js/gpt.js",
		"https://ad.doubleclick.net/ddm/adj/N1234.5678/AB1234;sz=300x250",
		"https://cm.g.doubleclick.net/push?partner=pubmatic",
		"https://www.google-analytics.com/analytics.js",
		"https://ssl.google-analytics.com/ga.js",
		"https://www.googletagmanager.com/gtm.js?id=GTM-ABC123",
		"https://www.googleadservices.com/pagead/conversion_async.js",
		"https://connect.facebook.net/en_US/fbevents.js",
		"https://www.facebook.com/tr?id=1001&ev=PageView",
		"https://graph.facebook.com/v18.0/1001/activities",
		"https://an.facebook.com/v2.100/prebid/static/get_config.html",
		"https://pixel.facebook.com/si/kappa/?Ko=a",
		"https://static.ads-twitter.com/uwt.js",
		"https://analytics.twitter.com/i/adsct?p_id=Twitter&p_user_id=0",
		"https://t.co/i/adsct?txn_id=o87gd&events=%5B%5D",
		"https://ib.adnxs.com/pxj?bidder=140&seg=742948&action=setuid",
		"https://ib.adnxs.com/ut/v3/prebid",
		"https://secure.adnxs.com/seg?add=1001&t=2",
		"https://bidder.criteo.com/cdb?profileId=712&av=35&wsc=us",
		"https://sslwidget.criteo.com/event?a=1001&v=5",
		"https://dis.criteo.com/dis/rtb/appnexus/cookiematch.aspx",
		"https://static.criteo.net/js/ld/publishertag.js",
		"https://trc.taboola.com/example-tsv2/log/3/unmatched?rid=abc",
		"https://cdn.taboola.com/lib/trc.js",
		"https://widgets.outbrain.com/outbrain.js",
		"https://logs.outbrain.com/log?e=PVi4Z",
		"https://sb.scorecardresearch.com/beacon.gif?c1=7&c2=1001",
		"https://pixel.quantserve.com/pixel;r=123;a=p-abc",
		"https://secure.quantserve.com/quant.js",
		"https://c.amazon-adsystem.com/aax2/apstag.js",
		"https://aax.amazon-adsystem.com/e/dtb/bid?src=3001&u=/news",
		"https://s.amazon-adsystem.com/iu3?cm3ppd=1&dme=1",
		"https://ads.pubmatic.com/AdServer/js/pwt/1001/5/pwt.js",
		"https://image6.pubmatic.com/ads?gdid=100&pmpOffer=1",
		"https://s2s.pubmatic.com/other/s2s/cookie_sync",
		"https://ads.rubiconproject.com/prebid/1001_PBNV.json",
		"https://token.rubiconproject.com/khaos.json?gdpr=0",
		"https://optimatic.rubiconproject.com/opt?accountId=100",
		"https://us.openx.net/w/1.0/pj?callback=pbjs",
		"https://delivery.g.switchadhub.com/adserver/prebid/v1",
		"https://htlb.casalemedia.com/cygnus?v=1&hn=htlb",
		"https://js.adscale.de/getads.js",
		"https://prg.smartadserver.com/prebid/v1",
		"https://sync.smartadserver.com/api/cidm/100/1001/sync",
		"https://ads.adfox.ru/1001/getCode?pp=abc",
		"https://adform.net/b/show.asp?mid=1001",
		"https://cm.adform.net/cookie?redirect_url=x",
		"https://tags.crwdcntrl.net/lt/c/1001/lt.js",
		"https://bcp.crwdcntrl.net/map?c=1001",
		"https://simage2.pubmatic.com/AdServer/Pug?v2",
		"https://moatads.com/tracker/json?m=1001",
		"https://px.moatads.com/pixel.gif?x=1",
		"https://ad.doubleclick.net/ddm/clk/1001;abc",
		"https://bat.bing.com/action/0?ti=55001&Ver=2",
		"https://www.clarity.ms/tag/xq7k1",
		"https://q.clarity.ms/collect",
		"https://cdn.mxpnl.com/libs/mixpanel-2-latest.min.js",
		"https://api.mixpanel.com/track/?data=abc",
		"https://cdn.segment.com/analytics.js/v1/abc/analytics.min.js",
		"https://api.segment.io/v1/p",
		"https://cdn.amplitude.com/libs/analytics-7.35.0-min.js.gz",
		"https://api2.amplitude.com/2/httpapi",
		"https://static.chartbeat.com/js/chartbeat.js",
		"https://ping.chartbeat.net/ping?h=example.com&p=/news",
		"https://js-agent.newrelic.com/nr-spa-1200.min.js",
		"https://nr-data.net/1/abc?a=100",
		"https://static.hotjar.com/c/hotjar-100.js?sv=6",
		"https://events.hotjar.io/api/v2/client/log",
		"https://script.crazyegg.com/pages/scripts/0001/1001.js",
		"https://snap.licdn.com/li.lms-analytics/insight.min.js",
		"https://px.ads.linkedin.com/collect/?pid=100&fmt=gif",
		"https://ads.linkedin.com/collect?pid=100",
		"https://analytics.pinterest.com/v3/api/event",
		"https://ct.pinterest.com/ct.html",
		"https://ads.reddit.com/api/v2.0/pixel",
		"https://alb.reddit.com/snoo.gif?q=CaAwAB",
		"https://events.redditmedia.com/v2j?cid=100",
		"https://sc-static.net/scevent.min.js",
		"https://tr.snapchat.com/cm/i?pid=100",
		"https://analytics.tiktok.com/i18n/pixel/events.js?sdkid=abc",
		"https://business-api.tiktok.com/open_api/v1.3/pixel/track/",
		"https://analytics.tiktok.com/api/v2/pixel",
		"https://cdn-magiclinks.adcolony.com/ads/config.js",
		"https://events3alt.adcolony.com/track?ad_type=video",
		"https://googleads.g.doubleclick.net/pagead/id",
		"https://pagead2.googlesyndication.com/getconfig/sodar?sv=200",
		"https://fundingchoicesmessages.google.com/i/1001?ers=1",
		"https://partner.googleadservices.com/gpt/pubads_impl.js",
		"https://adx.g.doubleclick.net/dcm/adx?ex=1",
		"https://a.teads.tv/analytics/tag.js",
		"https://sync.teads.tv/track?pid=100",
		"https://js.teads.tv/teads-player.js",
		"https://adservice.google.com/adsid/integrator.js?domain=example.com",
		"https://gum.criteo.com/sync?c=1&r=1",
		"https://gumgum.com/hb/1001/getads",
		"https://everesttech.net/etap?sysid=1",
		"https://tags.bluekai.com/site/1001?ret=html",
		"https://st.beacon.qq.com/collect?sid=100",
		"https://hmma.baidu.com/mm.gif?si=100",
		"https://pos.baidu.com/s?hei=250&wid=300",
		"https://cdn.example-nonad.test/should-not-match.js",
		"https://www.example.com/news/article.html",
	}
	for _, u := range static {
		add(u)
	}
	if len(urls) < 200 {
		t.Fatalf("广告 URL 集不足 200 条：实际 %d", len(urls))
	}
	return urls
}

// ---------------- 请求头组合 ----------------

type goldenCombo struct {
	name    string
	method  string
	site    string // Sec-Fetch-Site
	dest    string // Sec-Fetch-Dest
	user    bool   // Sec-Fetch-User: ?1
	referer string // "" 表示不带 Referer
}

var goldenCombos = []goldenCombo{
	{"third-party-script", http.MethodGet, "cross-site", "script", false, "https://www.example.com/news/article.html"},
	{"tracking-pixel", http.MethodGet, "cross-site", "image", false, "https://www.example.com/news/article.html"},
	{"xhr-fetch", http.MethodPost, "cross-site", "empty", false, "https://www.example.com/news/article.html"},
	{"same-origin-script", http.MethodGet, "same-origin", "script", false, "https://www.example.com/news/article.html"},
	{"user-navigation", http.MethodGet, "none", "document", true, ""},
	{"iframe-embed", http.MethodGet, "cross-site", "iframe", false, "https://www.example.com/news/article.html"},
	{"no-referer-pixel", http.MethodGet, "cross-site", "image", false, ""},
	{"same-site-fetch", http.MethodGet, "same-site", "empty", false, "https://cdn.example.com/page.html"},
}

var goldenReferers = []string{
	"https://www.example.com/news/article.html",
	"https://www.nytimes.com/arts/2026/10/05/review.html",
	"https://www.spiegel.de/panorama/a-123456.html",
	"https://en.wikipedia.org/wiki/Ad_blocking",
}

func goldenBuildReq(u string, c goldenCombo, referer string) *http.Request {
	parsed, err := url.Parse(u)
	if err != nil {
		return nil
	}
	req := &http.Request{
		Method: c.method,
		URL:    parsed,
		Header: http.Header{},
	}
	req.Header.Set("Sec-Fetch-Site", c.site)
	req.Header.Set("Sec-Fetch-Dest", c.dest)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "GoldenTest/1.0")
	if c.user {
		req.Header.Set("Sec-Fetch-User", "?1")
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	return req
}

func goldenBuildRes(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body:       http.NoBody,
		Request:    req,
	}
}

// goldenOut 统一两引擎结果形态（一次 = ModifyReq + ModifyRes 各一次）。
type goldenOut struct {
	shouldBlock bool
	redirectURL string
	rawRules    []string // sorted RawRule（ModifyReq 的 appliedRules）
	panicMsg    string
	resRawRules []string // sorted RawRule（ModifyRes 的 appliedRules）
	resErrNil   bool
	resPanicMsg string
}

func goldenSortRules(in []string) []string {
	sort.Strings(in)
	return in
}

// 每引擎各自的调用实现（两包类型不同，无法共用一个泛型函数），
// 各自用 defer/recover 兜住 panic —— panic 本身也是要断言的差异点。

func goldenCallBaseline(e *baselinenr.NetworkRules, u string, c goldenCombo, referer string) (out goldenOut) {
	defer func() {
		if r := recover(); r != nil {
			out.panicMsg = fmt.Sprint(r)
		}
	}()
	reqB := goldenBuildReq(u, c, referer)
	reqB2 := goldenBuildReq(u, c, referer)
	if reqB == nil || reqB2 == nil {
		out.panicMsg = "url parse failed"
		return
	}
	applied, shouldBlock, redirect := e.ModifyReq(reqB)
	out.shouldBlock = shouldBlock
	out.redirectURL = redirect
	raws := make([]string, 0, len(applied))
	for _, r := range applied {
		raws = append(raws, r.RawRule)
	}
	out.rawRules = goldenSortRules(raws)

	defer func() {
		if r := recover(); r != nil {
			out.resPanicMsg = fmt.Sprint(r)
		}
	}()
	resB := goldenBuildRes(reqB2)
	resApplied, err := e.ModifyRes(reqB2, resB)
	out.resErrNil = err == nil
	resRaws := make([]string, 0, len(resApplied))
	for _, r := range resApplied {
		resRaws = append(resRaws, r.RawRule)
	}
	out.resRawRules = goldenSortRules(resRaws)
	return
}

func goldenCallProduction(e *prodnr.NetworkRules, u string, c goldenCombo, referer string) (out goldenOut) {
	defer func() {
		if r := recover(); r != nil {
			out.panicMsg = fmt.Sprint(r)
		}
	}()
	reqP := goldenBuildReq(u, c, referer)
	reqP2 := goldenBuildReq(u, c, referer)
	if reqP == nil || reqP2 == nil {
		out.panicMsg = "url parse failed"
		return
	}
	applied, shouldBlock, redirect := e.ModifyReq(reqP)
	out.shouldBlock = shouldBlock
	out.redirectURL = redirect
	raws := make([]string, 0, len(applied))
	for _, r := range applied {
		raws = append(raws, r.RawRule)
	}
	out.rawRules = goldenSortRules(raws)

	defer func() {
		if r := recover(); r != nil {
			out.resPanicMsg = fmt.Sprint(r)
		}
	}()
	resP := goldenBuildRes(reqP2)
	resApplied, err := e.ModifyRes(reqP2, resP)
	out.resErrNil = err == nil
	resRaws := make([]string, 0, len(resApplied))
	for _, r := range resApplied {
		resRaws = append(resRaws, r.RawRule)
	}
	out.resRawRules = goldenSortRules(resRaws)
	return
}

// ---------------- 主对拍测试 ----------------

func TestGoldenCompare(t *testing.T) {
	// 2026-10-08: AdGuard 兼容性批次（B1-B8）有意变更生产引擎判定语义
	// （$popup 复活、例外逐请求判定、$denyallow 等），与冻结基线
	// baselinenr 的黄金对拍默认跳过；设 ZEN_ENGINEDIFF=1 复跑对拍。
	if os.Getenv("ZEN_ENGINEDIFF") == "" {
		t.Skip("golden-master 前提已被 AdGuard 兼容性批次有意变更——设 ZEN_ENGINEDIFF=1 复跑对拍")
	}
	if testing.Short() {
		t.Skip("golden 对拍耗时长，-short 跳过")
	}
	baseline := goldenBuildBaseline(t)
	prod := goldenBuildProduction(t)

	urls := goldenLoadURLs(t)
	t.Logf("urls.txt 全量：%d 条", len(urls))
	adURLs := goldenAdURLs(t)
	t.Logf("广告测试 URL 集：%d 条", len(adURLs))

	var (
		totalPairs, identicalPairs int
		blockHits, redirectHits    int
		varDiffs                   []string // 前 20 条差异样本
	)
	recordDiff := func(format string, args ...interface{}) {
		if len(varDiffs) < 20 {
			varDiffs = append(varDiffs, fmt.Sprintf(format, args...))
		}
	}

	sweep := func(set []string, combosFor func(i int) []int) {
		for i, u := range set {
			for _, ci := range combosFor(i) {
				c := goldenCombos[ci]
				ref := ""
				if c.referer != "" {
					ref = goldenReferers[(i+ci)%len(goldenReferers)]
				}
				base := goldenCallBaseline(baseline, u, c, ref)
				prod := goldenCallProduction(prod, u, c, ref)
				totalPairs++

				if base.shouldBlock || prod.shouldBlock {
					blockHits++
				}
				if base.redirectURL != "" || prod.redirectURL != "" {
					redirectHits++
				}

				ok := true
				if base.shouldBlock != prod.shouldBlock {
					recordDiff("shouldBlock 不一致 url=%s combo=%s base=%v prod=%v", u, c.name, base.shouldBlock, prod.shouldBlock)
					ok = false
				}
				if base.redirectURL != prod.redirectURL {
					recordDiff("redirectURL 不一致 url=%s combo=%s base=%q prod=%q", u, c.name, base.redirectURL, prod.redirectURL)
					ok = false
				}
				// 三元组第三元：len(appliedRules)。
				if len(base.rawRules) != len(prod.rawRules) {
					recordDiff("len(appliedRules) 不一致 url=%s combo=%s base=%d(%v) prod=%d(%v)", u, c.name, len(base.rawRules), base.rawRules, len(prod.rawRules), prod.rawRules)
					ok = false
				}
				// 非阻塞情形 appliedRules 集合跨运行稳定，加严比较。
				if !base.shouldBlock && !prod.shouldBlock && len(base.rawRules) == len(prod.rawRules) {
					for k := range base.rawRules {
						if base.rawRules[k] != prod.rawRules[k] {
							recordDiff("appliedRules 集合不一致 url=%s combo=%s base=%v prod=%v", u, c.name, base.rawRules, prod.rawRules)
							ok = false
							break
						}
					}
				}
				if base.panicMsg != "" || prod.panicMsg != "" {
					recordDiff("ModifyReq panic url=%s combo=%s base=%q prod=%q", u, c.name, base.panicMsg, prod.panicMsg)
					ok = false
				}
				// ModifyRes。
				if base.resPanicMsg != "" || prod.resPanicMsg != "" {
					recordDiff("ModifyRes panic url=%s combo=%s base=%q prod=%q", u, c.name, base.resPanicMsg, prod.resPanicMsg)
					ok = false
				}
				if base.resErrNil != prod.resErrNil {
					recordDiff("ModifyRes err 不一致 url=%s combo=%s baseErrNil=%v prodErrNil=%v", u, c.name, base.resErrNil, prod.resErrNil)
					ok = false
				}
				if len(base.resRawRules) != len(prod.resRawRules) {
					recordDiff("ModifyRes len(appliedRules) 不一致 url=%s combo=%s base=%v prod=%v", u, c.name, base.resRawRules, prod.resRawRules)
					ok = false
				} else {
					for k := range base.resRawRules {
						if base.resRawRules[k] != prod.resRawRules[k] {
							recordDiff("ModifyRes appliedRules 不一致 url=%s combo=%s base=%v prod=%v", u, c.name, base.resRawRules, prod.resRawRules)
							ok = false
							break
						}
					}
				}
				if ok {
					identicalPairs++
				}
			}
		}
	}

	// urls.txt 全量：每条 URL 一个头组合（按 index 轮转 8 组合，保证组合覆盖）。
	sweep(urls, func(i int) []int { return []int{i % len(goldenCombos)} })
	// 广告 URL 集：每条 URL 全部 8 个头组合。
	sweep(adURLs, func(i int) []int {
		all := make([]int, len(goldenCombos))
		for j := range all {
			all[j] = j
		}
		return all
	})

	t.Logf("=== 对拍矩阵 ===")
	t.Logf("URL 总数：%d（urls.txt %d + 广告集 %d）", len(urls)+len(adURLs), len(urls), len(adURLs))
	t.Logf("对拍对：每引擎 ModifyReq %d 次 + ModifyRes %d 次（urls.txt 每条 1 组合，广告集每条 8 组合）", totalPairs, totalPairs)
	t.Logf("三元组完全一致：%d/%d", identicalPairs, totalPairs)
	t.Logf("阻塞命中对：%d，重定向命中对：%d（非零 = 对拍真实覆盖了封锁/重定向路径）", blockHits, redirectHits)

	if len(varDiffs) > 0 {
		for i, d := range varDiffs {
			t.Errorf("差异样本 %d/%d：%s", i+1, len(varDiffs), d)
		}
		t.Fatalf("对拍发现差异（上限样本 20 条，实际差异至少 %d 处）", len(varDiffs))
	}
}

// ---------------- 对抗性规则注入门禁 ----------------

// goldenAdversarialRules 构造畸形/极端规则清单（每条附意图）。
func goldenAdversarialRules(t testing.TB) []struct {
	Rule string
	Why  string
} {
	t.Helper()
	mb := strings.Repeat("a", 1<<20-16)                    // ~1MB 字面量
	mbRegExp := "/" + strings.Repeat("ab?", 350000) + "c/" // ~1MB 正则体
	deepDomains := "||example.com^$domain=" + strings.Repeat("((((", 250)
	var manyHosts strings.Builder
	manyHosts.WriteString("||example.com^$domain=")
	for i := 0; i < 1000; i++ {
		if i > 0 {
			manyHosts.WriteByte('|')
		}
		fmt.Fprintf(&manyHosts, "h%d.bench.invalid", i)
	}
	var hugeHosts strings.Builder
	hugeHosts.WriteString("0.0.0.0 ")
	for i := 0; i < 50000; i++ {
		fmt.Fprintf(&hugeHosts, "h%d.bench.invalid ", i)
	}
	deepExceptions := "@@$domain=x.invalid"
	return []struct {
		Rule string
		Why  string
	}{
		{"$$", "纯修饰符"},
		{"$", "单美元"},
		{"", "空行"},
		{"||", "只含 ||"},
		{"|||^$all", "连续 || + $all"},
		{"$document", "空 pattern + modifier"},
		{"$script,image,document,all,important", "空 pattern 多 modifier"},
		{"@@$all,important,document,script", "空 pattern exception"},
		{"||example.com^$domain=(((", "嵌套未闭合 domain"},
		{"||example.com^$domain=(((((((((((((a)))))))))))))))", "嵌套已配对 domain"},
		{deepDomains, "1000 层嵌套 domain"},
		{"||example.com^$domain=", "空 domain 值"},
		{"||example.com^$domain=~", "仅反选符 domain"},
		{"||example.com^$domain=,", "仅逗号 domain"},
		{"||example.com^$domain=a.invalid|~b.invalid|c.invalid|~d.invalid", "混合正反 domain"},
		{manyHosts.String(), "1000 个 domain 项"},
		{"||example.com^$domain=/.*/", "domain 为正则"},
		{"/(a+)+b/$script", "灾难性回溯形态 1（RE2 应线性）"},
		{"/(x+x+)+y/$image", "灾难性回溯形态 2"},
		{"/(a|a)*?b/", "灾难性回溯形态 3"},
		{"/(.*a){20}/$xhr", "重复量词回溯形态"},
		{"||" + mb + "^$script", "~1MB 字面量 pattern"},
		{mbRegExp, "~1MB 正则体"},
		{"@@" + mb, "~1MB exception"},
		{"/$script", "空正则体 + modifier"},
		{"//", "空正则体"},
		{"///$script", "斜杠正则歧义"},
		{"||中文域名.example.中国^$script", "unicode pattern"},
		{"||例え.jp/テスト/^", "unicode 路径"},
		{"||emoji-\U0001F600.com^", "emoji pattern"},
		{"||a\x00b^", "NUL 字节"},
		{"||\xff\xfe.invalid^", "非法 UTF-8 字节"},
		{"||example.com^$", "尾随空 modifier"},
		{"||example.com^$_", "noop 修饰符"},
		{"||example.com^$____,script,____", "noop 混排"},
		{"||example.com^$~third-party", "反转 flag"},
		{"||example.com^$third-party,~third-party", "自相矛盾 third-party"},
		{"||example.com^$method=GET", "method 单值"},
		{"||example.com^$method=GET|POST|DELETE", "method 多值"},
		{"||example.com^$method=", "空 method"},
		{"||example.com^$method=【", "method 非法字符"},
		{"||example.com^$removeparam=utm_source", "removeparam 字面量"},
		{"||example.com^$removeparam=/^utm_/", "removeparam 正则"},
		{"||example.com^$removeparam=(((", "removeparam 畸形正则"},
		{"||example.com^$header=(", "header 畸形"},
		{"||example.com^$jsonprune=$..a[0]", "jsonprune 表达式"},
		{"||example.com^$jsonprune=not json", "jsonprune 畸形"},
		{"||example.com^$remove-js-constant=window.__data", "remove-js-constant"},
		{"||example.com^$scramblejs", "scramblejs"},
		{"||example.com^$csp=script-src 'none'", "未支持的 csp modifier"},
		{"0.0.0.0 example.com", "hosts 行"},
		{hugeHosts.String(), "~50000 主机 hosts 行"},
		{deepExceptions, "exception 带 domain"},
		{"||example.com^$ping,image,websocket,other,xhr,font,media,stylesheet,object,subdocument", "多 content-type Or"},
		{"!foo", "注释样规则直入 ParseRule"},
		{"#%#ad-title { display: none }", "cosmetic 样规则直入 ParseRule"},
		{"##.banner", "元素隐藏样规则直入 ParseRule"},
		{"#@#.banner", "元素隐藏 exception 直入 ParseRule"},
		{"#$#body { background: red }", "css 注入样规则直入 ParseRule"},
	}
}

func TestGoldenAdversarialRules(t *testing.T) {
	// 2026-10-08: AdGuard 兼容性批次（B1-B8）有意变更了生产引擎的规则
	// 接受/拒绝面（如 <4 字符规则拒收、@@$all 拒收）与判定语义，与冻结
	// 基线 baselinenr 的对抗性对拍默认跳过；设 ZEN_ENGINEDIFF=1 复跑对拍。
	if os.Getenv("ZEN_ENGINEDIFF") == "" {
		t.Skip("golden-master 前提已被 AdGuard 兼容性批次有意变更——设 ZEN_ENGINEDIFF=1 复跑对拍")
	}
	rules := goldenAdversarialRules(t)
	if len(rules) < 50 {
		t.Fatalf("对抗性规则不足 50 条：实际 %d", len(rules))
	}
	t.Logf("对抗性规则：%d 条", len(rules))

	baseline := baselinenr.New()
	prod := prodnr.New()
	nameB, nameP := "adv-base", "adv-prod"

	probeURLs := []string{
		"https://example.com/path?q=1&utm_source=x",
		"https://a.example.com/deep/path/pixel.gif",
		"https://中文.com/路径",
		"https://example.com/",
	}

	var (
		acceptedByBoth, rejectedByBoth, parityDiffs int
		panicCount                                  int
		samples                                     []string
	)
	record := func(format string, args ...interface{}) {
		if len(samples) < 20 {
			samples = append(samples, fmt.Sprintf(format, args...))
		}
	}

	for idx, tc := range rules {
		_, errB := baseline.ParseRule(tc.Rule, &nameB)
		_, errP := prod.ParseRule(tc.Rule, &nameP)

		if (errB == nil) != (errP == nil) {
			parityDiffs++
			record("规则 #%d [%s] 接受性不一致: baseErr=%v prodErr=%v", idx, tc.Why, errB, errP)
			continue
		}
		if errB != nil {
			rejectedByBoth++
			continue
		}
		acceptedByBoth++

		// 两引擎都接受 → 用多个探针请求过 ModifyReq，断言全程不 panic 且行为一致。
		for _, u := range probeURLs {
			for _, c := range goldenCombos {
				ref := c.referer
				if ref == "" {
					ref = goldenReferers[idx%len(goldenReferers)]
				} else {
					ref = goldenReferers[(idx+len(ref))%len(goldenReferers)]
				}
				base := goldenCallBaseline(baseline, u, c, ref)
				prd := goldenCallProduction(prod, u, c, ref)
				if base.panicMsg != "" || prd.panicMsg != "" || base.resPanicMsg != "" || prd.resPanicMsg != "" {
					panicCount++
					record("规则 #%d [%s] ModifyReq/ModifyRes panic on %s: base=%q prod=%q", idx, tc.Why, u, base.panicMsg, prd.panicMsg)
				}
				if base.shouldBlock != prd.shouldBlock || base.redirectURL != prd.redirectURL || len(base.rawRules) != len(prd.rawRules) {
					parityDiffs++
					record("规则 #%d [%s] 行为不一致 on %s/%s: base=(%v,%q,%d) prod=(%v,%q,%d)",
						idx, tc.Why, u, c.name, base.shouldBlock, base.redirectURL, len(base.rawRules),
						prd.shouldBlock, prd.redirectURL, len(prd.rawRules))
				}
			}
		}
	}

	t.Logf("对抗性门禁矩阵：接受(两引擎一致)=%d，拒绝(两引擎一致)=%d，行为/接受性差异=%d，panic=%d",
		acceptedByBoth, rejectedByBoth, parityDiffs, panicCount)

	if len(samples) > 0 {
		for i, s := range samples {
			t.Errorf("对抗差异样本 %d/%d：%s", i+1, len(samples), s)
		}
		t.Fatalf("对抗性门禁失败：差异=%d panic=%d", parityDiffs, panicCount)
	}
}
