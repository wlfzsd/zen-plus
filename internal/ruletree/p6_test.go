package ruletree_test

// P6 批（遍历确定性）专用用例。禁改 diffharness_test.go / probe_test.go；
// 本文件新增：
//  1. TestP6RuleTreeDeterminism  —— 同 URL 1000 次 Get/GetLP，胜出候选序列必须完全一致
//     （SA-A 审计：map 去重导致同二进制两跑胜出规则身份漂移）。
//  2. TestP6RuleTreeConcurrent   —— 并发 GetLP 下结果仍逐次一致（池化安全复核，-race 下更有价值）。
//  3. TestP6EngineDecisionStability —— 引擎级 ModifyReq 1000 次，判定三元组+appliedRules 身份序列一致。
//  4. TestP6YouTubeGolden        —— ytprobe3 26 URL×5 dest×3 site×3 referer 对拍形态固化为 golden 电池
//     （基线 testdata/yt_golden.txt，P6_GEN_GOLDEN=1 重新生成）。
//  5. BenchmarkMatch             —— ruletree 微基准（Get/GetLP），P6 前后对比门禁用。

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irbis-sh/zen-desktop/internal/asset"
	"github.com/irbis-sh/zen-desktop/internal/networkrules"
	"github.com/irbis-sh/zen-desktop/internal/ruletree"
)

// ---------- 公共小件 ----------

const p6SeqSep = "\x1f"

func p6JoinSeq(s []string) string { return strings.Join(s, p6SeqSep) }

func p6SameSeq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// p6TreePatterns 刻意多规则重叠同一 URL（含通配、重复插入），保证候选集 ≥5，
// 使“序确定性”断言有意义。切片序 = 插入序（确定性来源）。
var p6TreePatterns = []string{
	"||ads.example.com^",
	"||example.com^banner",
	"/banner/*",
	"*ad-300x250*",
	"-ad-300x250.gif",
	"ad_type=image",
	"&ad_type=",
	"click=",
	"utm_source=",
	"utm_medium=",
	"tracker/pixel",
	"*pixel*",
	"||cdn.example.net^",
	"https://cdn.example.net/*",
	"/tracker/",
	"watch?v=abc",
	"?v=abc&",
	"&pbj=1",
	"adformat=",
	"https://www.site.org/watch*",
	"*site.org/watch*",
	"example.com/banner/",
	".gif?",
	"=image",
	"300x250",
	"||site.org^",
	"/ad/*",
	"ad",
	// 刻意重复插入：同 pattern 两个身份，leaf 顺序 = 插入序，必须保持
	"||ads.example.com^",
	"/banner/*",
}

var p6TreeURLs = []string{
	"https://ads.example.com/banner/ad-300x250.gif?click=https%3A%2F%2Fx.com%2F&ad_type=image",
	"https://cdn.example.net/tracker/pixel?id=abc&utm_source=y&utm_medium=z",
	"https://www.site.org/watch?v=abc&pbj=1&adformat=1",
	"https://ads.example.com/ad/banner/?utm_source=q",
	"https://plain.example.com/nothing/here?q=1", // 零命中对照
}

// ---------- 1. ruletree 确定性（验收①） ----------

func TestP6RuleTreeDeterminism(t *testing.T) {
	tree := ruletree.New[string]()
	for i, pat := range p6TreePatterns {
		tree.Insert(pat, fmt.Sprintf("R%03d#%s", i, pat))
	}

	for _, u := range p6TreeURLs {
		refLP := tree.GetLP(u)
		refGet := tree.Get(u)

		if !p6SameSeq(refLP, refGet) {
			t.Fatalf("Get 与 GetLP 序列不一致 url=%s\n GetLP=%v\n Get  =%v", u, refLP, refGet)
		}

		// 无重复项（去重语义保持）
		seen := make(map[string]struct{}, len(refLP))
		for _, v := range refLP {
			if _, dup := seen[v]; dup {
				t.Fatalf("结果含重复项 url=%s v=%q", u, v)
			}
			seen[v] = struct{}{}
		}

		if strings.Contains(u, "nothing/here") {
			if len(refLP) != 0 {
				t.Fatalf("零命中 URL 却返回 %d 候选", len(refLP))
			}
			continue
		}
		// 意义性守卫：多候选（否则确定性断言空洞）
		if len(refLP) < 5 {
			t.Fatalf("守卫失败：url=%s 仅 %d 候选（设计要求≥5）", u, len(refLP))
		}

		refKey := p6JoinSeq(refLP)
		for k := 0; k < 1000; k++ {
			if got := tree.GetLP(u); !p6SameSeq(got, refLP) {
				t.Fatalf("GetLP 第 %d 次与基准序列漂移 url=%s\n ref =%s\n got =%s", k, u, refKey, p6JoinSeq(got))
			}
			if got := tree.Get(u); !p6SameSeq(got, refGet) {
				t.Fatalf("Get 第 %d 次与基准序列漂移 url=%s", k, u)
			}
		}
		t.Logf("url=%s 候选=%d 1000 次序列全一致", u, len(refLP))
	}
}

// ---------- 2. 并发 GetLP 稳定（池化安全复核） ----------

func TestP6RuleTreeConcurrent(t *testing.T) {
	tree := ruletree.New[string]()
	for i, pat := range p6TreePatterns {
		tree.Insert(pat, fmt.Sprintf("R%03d", i))
	}
	refs := make(map[string]string, len(p6TreeURLs))
	for _, u := range p6TreeURLs {
		refs[u] = p6JoinSeq(tree.GetLP(u))
	}

	const workers = 8
	const rounds = 250
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for k := 0; k < rounds; k++ {
				for _, u := range p6TreeURLs {
					got := tree.GetLP(u)
					if p6JoinSeq(got) != refs[u] {
						errCh <- fmt.Errorf("worker%d round%d url=%s 序列漂移", w, k, u)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

// ---------- 3. 引擎级决策稳定性 ----------

func TestP6EngineDecisionStability(t *testing.T) {
	nr := networkrules.New()
	rules := []string{
		"||ads.example.com^",
		"||ads.example.com^$script",
		"/ad-300x250/",
		"*banner*",
		"$removeparam=utm_source",
		"$removeparam=/^utm_/",
		"$removeparam=fbclid",
		"@@||ads.example.com^$script",
		"@@||cdn.example.net^$domain=ref.example",
		"||tracker.example^$domain=site.example",
		"||blocker.example^$important",
	}
	for _, r := range rules {
		if _, err := nr.ParseRule(r, nil); err != nil {
			t.Fatalf("ParseRule(%q): %v", r, err)
		}
	}
	nr.Compact()

	type spec struct {
		method, url, dest, site, user, referer string
	}
	specs := []spec{
		{"GET", "https://ads.example.com/banner/ad-300x250.gif?utm_source=1&utm_medium=2&fbclid=3&keep=k", "script", "cross-site", "", "https://ref.example/"},
		{"GET", "https://cdn.example.net/tracker/pixel?utm_source=a&fbclid=b", "script", "cross-site", "", "https://ref.example/"},
		{"GET", "https://cdn.example.net/tracker/pixel?utm_source=a", "script", "same-origin", "", "https://cdn.example.net/x"},
		{"GET", "https://tracker.example/p?x=1", "xmlhttprequest", "cross-site", "", "https://site.example/"},
		{"GET", "https://blocker.example/x", "script", "cross-site", "", ""},
		{"GET", "https://www.site.org/watch?v=abc&pbj=1", "document", "none", "?1", ""},
		{"POST", "https://ads.example.com/x?utm_source=1", "empty", "cross-site", "", ""},
		{"GET", "https://clean.example/ok?q=1", "script", "cross-site", "", ""},
	}

	fingerprint := func() string {
		var b strings.Builder
		for _, sp := range specs {
			req := httptest.NewRequest(sp.method, sp.url, nil)
			if sp.dest != "" {
				req.Header.Set("Sec-Fetch-Dest", sp.dest)
			}
			if sp.site != "" {
				req.Header.Set("Sec-Fetch-Site", sp.site)
			}
			if sp.user != "" {
				req.Header.Set("Sec-Fetch-User", sp.user)
			}
			if sp.referer != "" {
				req.Header.Set("Referer", sp.referer)
			}
			applied, block, redirect := nr.ModifyReq(req)
			b.WriteString(sp.url)
			b.WriteByte('|')
			b.WriteString(map[bool]string{true: "B", false: "-"}[block])
			b.WriteByte('|')
			b.WriteString(redirect)
			for _, r := range applied {
				b.WriteByte('#')
				b.WriteString(r.RawRule)
			}
			b.WriteByte('\n')
		}
		return b.String()
	}

	ref := fingerprint()
	sawIdentity := false
	for _, line := range strings.Split(strings.TrimSpace(ref), "\n") {
		if strings.Contains(line, "#") || strings.HasSuffix(line, "|B") {
			sawIdentity = true
		}
	}
	if !sawIdentity {
		t.Fatalf("守卫失败：指纹中无任何阻断/改写身份，用例失去意义\n%s", ref)
	}
	for k := 1; k < 1000; k++ {
		if got := fingerprint(); got != ref {
			t.Fatalf("第 %d 轮指纹漂移\nref:\n%s\ngot:\n%s", k, ref, got)
		}
	}
	t.Logf("8 个请求形态 × 1000 轮：判定三元组+appliedRules 身份序列全一致")
}

// ---------- 4. YouTube golden 电池（固化 ytprobe3 对拍形态） ----------

// p6YTLists 与 ytprobe3\main.go loadLists 同一清单（6 列表），但改为固定顺序载入：
// ytprobe3 用 map 迭代（序随机），golden 固化要求确定性。
var p6YTLists = []struct {
	name    string
	file    string
	trusted bool
}{
	{"AdGuardBase", "6ead4595bd0a66101e518ae10b365969.cache.txt", true},
	{"AdGuardSpyware", "d72a4d2b09eb3ae58eb3ccc4e7037062.cache.txt", true},
	{"EasyList", "17ba74de8f13543dbff29e37b3ce125d.cache.txt", false},
	{"EasyPrivacy", "3017d28ef6b8789fc7064dbd848b8a7d.cache.txt", false},
	{"Zen-Ads", "118a386ed2095c6564f1c1e61a28c788.cache.txt", false},
	{"URLShortener", "bab7e3d637e8e60a89dd8f26d4e1423e.cache.txt", false},
}

var p6YTURLs = []string{
	"https://www.youtube.com/watch?v=abc",
	"https://www.youtube.com/",
	"https://www.youtube.com/youtubei/v1/player?key=x",
	"https://www.youtube.com/youtubei/v1/next?key=x",
	"https://www.youtube.com/pagead/adview?ai=x",
	"https://www.youtube.com/pagead/interaction?ai=x",
	"https://www.youtube.com/api/stats/ads?at=x",
	"https://www.youtube.com/api/stats/qoe?x",
	"https://www.youtube.com/get_video_info?x",
	"https://www.youtube.com/my_video_ad?x",
	"https://www.youtube.com/ptracking?x",
	"https://www.youtube.com/api/timedtext?x",
	"https://redirector.googlevideo.com/videoplayback?ctier=W&x=1",
	"https://redirector.googlevideo.com/videoplayback?ctier=L&x=%2Cxpc%2Cctier%2C",
	"https://r1---sn-x.googlevideo.com/videoplayback?ctier=L&x=%2Cxpc%2Cctier%2C&id=abc",
	"https://r1---sn-x.googlevideo.com/videoplayback?x=1",
	"https://googleads.g.doubleclick.net/pagead/id?x",
	"https://googleads.g.doubleclick.net/pagead/ads?x",
	"https://pubads.g.doubleclick.net/gampad/ads?ad_rule=0&d_imp=1&gdfp_req=1&iu=%2F6762%2Fmkt.ythome_1x1",
	"https://pubads.g.doubleclick.net/gampad/ads?ad_rule=1&iu=%2F6762%2Fmkt.ythome_1x1",
	"https://static.doubleclick.net/instream/ad_status.js",
	"https://securepubads.g.doubleclick.net/gampad/ads?x",
	"https://s.youtube.com/api/stats/playback?x",
	"https://www.youtube.com/generate_204?x",
	"https://i.ytimg.com/vi/abc/hqdefault.jpg",
	"https://www.youtube.com/error_204?x",
}

var (
	p6YTOnce sync.Once
	p6YTNr   *networkrules.NetworkRules
	p6YTErr  error
)

// p6YTLoader 镜像 ytprobe3 loadLists：eng.AddRule 优先，余下进 nr.ParseRule；
// 跳过规则同 ytprobe3（空行/!/[），仅列表载入序固定。
func p6YTLoader(t *testing.T) (*networkrules.NetworkRules, error) {
	p6YTOnce.Do(func() {
		dir := `C:\Users\Administrator\AppData\Local\Zen\filters`
		nr := networkrules.New()
		eng, err := asset.NewEngine("assets.zen.local")
		if err != nil {
			p6YTErr = fmt.Errorf("NewEngine: %w", err)
			return
		}
		for _, li := range p6YTLists {
			data, err := os.ReadFile(filepath.Join(dir, li.file))
			if err != nil {
				p6YTErr = fmt.Errorf("read %s: %w", li.file, err)
				return
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || line[0] == '!' || line[0] == '[' {
					continue
				}
				if h, err := eng.AddRule(line, li.trusted); err == nil && h {
					continue
				}
				if _, err := nr.ParseRule(line, &li.name); err != nil {
					// ytprobe3 同款容忍：解析失败行静默跳过（不计入判定面）
					_ = err
				}
			}
		}
		nr.Compact()
		p6YTNr = nr
	})
	return p6YTNr, p6YTErr
}

func p6YTReq(url, dest, site, referer string) *http.Request {
	r := httptest.NewRequest("GET", url, nil)
	r.Header.Set("Sec-Fetch-Dest", dest)
	if site != "" {
		r.Header.Set("Sec-Fetch-Site", site)
	}
	if referer != "" {
		r.Header.Set("Referer", referer)
	}
	return r
}

func p6YTBatteryLines(nr *networkrules.NetworkRules) []string {
	dests := []string{"document", "xmlhttprequest", "script", "media", "subdocument"}
	sites := []string{"same-origin", "cross-site", "none"}
	refs := []string{"", "https://www.youtube.com/watch?v=abc", "https://www.youtube.com/"}
	var out []string
	for _, u := range p6YTURLs {
		for _, d := range dests {
			for _, s := range sites {
				for _, r := range refs {
					_, block, redirect := nr.ModifyReq(p6YTReq(u, d, s, r))
					outcome := "allow"
					if block {
						outcome = "BLOCK"
					} else if redirect != "" {
						outcome = "redirect"
					}
					out = append(out, fmt.Sprintf("%s|%s|%s|%s|%s", u, d, s, r, outcome))
				}
			}
		}
	}
	return out
}

func TestP6YouTubeGolden(t *testing.T) {
	nr, err := p6YTLoader(t)
	if err != nil {
		t.Skipf("列表不可载入：%v", err)
	}
	lines := p6YTBatteryLines(nr)
	if want := len(p6YTURLs) * 5 * 3 * 3; len(lines) != want {
		t.Fatalf("组合数=%d 期望=%d", len(lines), want)
	}
	blocks := 0
	redirects := 0
	for _, l := range lines {
		switch {
		case strings.HasSuffix(l, "|BLOCK"):
			blocks++
		case strings.HasSuffix(l, "|redirect"):
			redirects++
		}
	}
	if blocks == 0 {
		t.Fatalf("守卫失败：golden 电池无任何 BLOCK（引擎或列表载入异常）")
	}

	goldenPath := filepath.Join("testdata", "yt_golden.txt")
	if os.Getenv("P6_GEN_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		content := strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(goldenPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden 已重生成：%s（%d 行，BLOCK=%d redirect=%d）——判定集若变更须显式更新基线并说明",
			goldenPath, len(lines), blocks, redirects)
		return
	}

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("读 golden（先生成：P6_GEN_GOLDEN=1 go test -run TestP6YouTubeGolden .）: %v", err)
	}
	want := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(want) != len(lines) {
		t.Fatalf("golden 行数=%d 实测=%d", len(want), len(lines))
	}
	diffs := 0
	for i := range want {
		if want[i] != lines[i] {
			diffs++
			if diffs <= 20 {
				t.Errorf("第 %d 行漂移\n golden: %s\n 实测  : %s", i, want[i], lines[i])
			}
		}
	}
	if diffs > 0 {
		t.Fatalf("共 %d 行与基线漂移（判定集变更须显式更新基线并说明）", diffs)
	}
	t.Logf("golden 一致：%d 行（BLOCK=%d redirect=%d allow=%d）", len(lines), blocks, redirects, len(lines)-blocks-redirects)
}

// ---------- 4b. 超长 URL 4096 截断（AdGuard 语义，父代理追加） ----------

// TestP6LongURLTruncation 验证 matchURL 截断语义：
//   - 4096 内命中 → 超长 URL 与其截断形态判定等价；
//   - 仅 4096 外才有锚点 → 超长 URL 不命中（AdGuard 同款截断匹配），短 URL 命中；
//   - 改写（$removeparam）在规则入选后作用于完整 URL；
//   - 68KB URL 全入口不 panic。
func TestP6LongURLTruncation(t *testing.T) {
	nr := networkrules.New()
	rules := []string{
		"||deep-path.example^",
		"/early-marker/",
		"/only-beyond-4096-marker-xyzzy/",
		"$removeparam=keepme",
	}
	for _, r := range rules {
		if _, err := nr.ParseRule(r, nil); err != nil {
			t.Fatalf("ParseRule(%q): %v", r, err)
		}
	}
	nr.Compact()

	// outCome 比较判定等价性：redirect 字符串本身因输入 URL 不同而不同，
	// 只比较（block, 是否改写, appliedRules 身份序列）。
	outcome := func(method, url, dest string) string {
		req := httptest.NewRequest(method, url, nil)
		req.Header.Set("Sec-Fetch-Dest", dest)
		applied, block, redirect := nr.ModifyReq(req)
		ids := make([]string, 0, len(applied))
		for _, r := range applied {
			ids = append(ids, r.RawRule)
		}
		return fmt.Sprintf("block=%v rewritten=%v applied=%s", block, redirect != "", strings.Join(ids, ","))
	}

	// Case A：4096 内有命中 → 超长与截断形态等价（阻断）。
	longA := "https://deep-path.example/early-marker/" + strings.Repeat("a", 60000) + "?keepme=1"
	if len(longA) <= 4096 {
		t.Fatalf("构造失败：Case A 长度 %d", len(longA))
	}
	gotA := outcome("GET", longA, "script")
	wantA := outcome("GET", longA[:4096], "script")
	if gotA != wantA {
		t.Fatalf("Case A 判定与截断形态不等价\n超长: %s\n截断: %s", gotA, wantA)
	}
	if !strings.Contains(gotA, "block=true") {
		t.Fatalf("Case A 预期阻断，实测 %s", gotA)
	}

	// Case B：锚点只在 4096 外 → 超长不命中；短 URL（4096 内）命中。
	longB := "https://plain.example/" + strings.Repeat("b", 48000) + "/only-beyond-4096-marker-xyzzy/"
	shortB := "https://plain.example/" + strings.Repeat("b", 3000) + "/only-beyond-4096-marker-xyzzy/"
	if len(longB) <= 4096 || len(shortB) > 4096 {
		t.Fatalf("构造失败：Case B 长度 long=%d short=%d", len(longB), len(shortB))
	}
	if got := outcome("GET", longB, "script"); !strings.Contains(got, "block=false") {
		t.Fatalf("Case B 预期超长 URL 不命中（截断语义），实测 %s", got)
	}
	if got := outcome("GET", shortB, "script"); !strings.Contains(got, "block=true") {
		t.Fatalf("Case B 预期短 URL 命中同一规则，实测 %s", got)
	}

	// Case C：generic $removeparam 在规则入选后作用于完整 URL（参数在 48000 字符处）。
	// 主机选 plain.example：无阻断规则命中（阻断早退会吞掉改写返回值，
	// 见 networkrules.go ModifyReq 的 return []rule.Rule{*r}, true, ""）。
	longC := "https://plain.example/x?" + strings.Repeat("pad=1&", 8000) + "keepme=1"
	reqC := httptest.NewRequest("GET", longC, nil)
	reqC.Header.Set("Sec-Fetch-Dest", "script")
	_, blockC, redirectC := nr.ModifyReq(reqC)
	if blockC {
		t.Fatalf("Case C 预期非阻断（plain.example 无阻断规则）")
	}
	if redirectC == "" {
		t.Fatalf("Case C 预期触发 removeparam 改写")
	}
	tailC := redirectC
	if len(tailC) > 120 {
		tailC = tailC[:120]
	}
	if strings.Contains(redirectC, "keepme=1") {
		t.Fatalf("Case C 预期完整 URL 上的 keepme 被移除，redirect 头 120 字节=%q", tailC)
	}

	// Case D：68071 字符（生产实测形态）全入口不 panic。
	longD := "https://www.youtube.com/watch?v=" + strings.Repeat("x&y=/z.", 9724)[:68039]
	if len(longD) != 68071 {
		t.Logf("Case D 长度=%d（目标 68071）", len(longD))
	}
	reqD := httptest.NewRequest("GET", longD, nil)
	reqD.Header.Set("Sec-Fetch-Dest", "document")
	resD := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: http.NoBody}
	_, _, _ = nr.ModifyReq(reqD)
	_, _ = nr.ModifyRes(reqD, resD)
	_ = nr.ActiveExceptions(reqD)
	t.Logf("Case D：%d 字符 URL 三入口（ModifyReq/ModifyRes/ActiveExceptions）无 panic", len(longD))
}

// ---------- 4c. 68KB 对抗 URL 有界成本门禁（父代理追加） ----------

// p6AdversarialURL 构造对抗 URL：分隔符密布（最大化遍历标记位）＋每 2000 字节
// 一个独占标记（q000…q059，保证有命中/有 append）＋4096 内的 adtag=1（截断
// 形态同样命中 removeparam）。target 控制总长：引擎级用 68KB（生产实测形态），
// ruletree 层用 8192（>4096 上限；裸 API 的 CPU 随长度平方增长，68KB 全尺寸
// 实测 3.54s/次——预先存在的枚举形态，引擎 4096 截断是 CPU 防线，见报告）。
func p6AdversarialURL(target int) string {
	filler := "xyz0123456789.&/_~:?=-"
	var b strings.Builder
	b.WriteString("https://longtail.example/watch?q=&adtag=1")
	marker := 0
	for b.Len() < target-len(filler) {
		b.WriteString(filler)
		if b.Len()%2000 < len(filler) && marker < 60 {
			b.WriteString(fmt.Sprintf("q%03d", marker))
			marker++
		}
	}
	b.WriteString("&end=1")
	return b.String()
}

// TestP6RuleTreeLongURLBoundedMemory：ruletree 层——通配符重压树下，超上限
// URL 的单次 GetLP 内存增量有界（去重前置：acc 只容纳去重后候选，与 URL
// 长度无关）。68KB 全尺寸实测（2026-10-08 门禁首轮）：1243 B/op ✅。
func TestP6RuleTreeLongURLBoundedMemory(t *testing.T) {
	tree := ruletree.New[string]()
	for i := 0; i < 60; i++ {
		tree.Insert(fmt.Sprintf("*q%03d*", i), fmt.Sprintf("R%03d", i))
	}
	for i := 0; i < 40; i++ {
		tree.Insert(fmt.Sprintf("*/seg%03d/*", i), fmt.Sprintf("S%03d", i)) // 不命中，但逐位枚举成本在
	}
	tree.Insert("*", "WILD")

	u := p6AdversarialURL(8192)
	if len(u) <= 4096 {
		t.Fatalf("对抗 URL 未超 4096 上限：len=%d", len(u))
	}
	// 意义性守卫：确实有候选（append 路径真实发生）
	if got := tree.GetLP(u); len(got) == 0 {
		t.Fatalf("守卫失败：对抗 URL 零命中，append 路径未触发")
	} else {
		t.Logf("对抗 URL 命中 %d 候选", len(got))
	}

	for i := 0; i < 3; i++ { // 预热（池填充）
		tree.GetLP(u)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	const n = 5
	for i := 0; i < n; i++ {
		tree.GetLP(u)
	}
	runtime.ReadMemStats(&after)
	perCall := (after.TotalAlloc - before.TotalAlloc) / n
	t.Logf("GetLP 单次分配增量：%d B/op（上限 1MB）", perCall)
	if perCall > 1<<20 {
		t.Fatalf("内存增量超界：%d B/op > 1MB", perCall)
	}

	start := time.Now()
	for i := 0; i < n; i++ {
		tree.GetLP(u)
	}
	elapsed := time.Since(start) / n
	t.Logf("GetLP 单次耗时：%v（宽松上限 2s，防挂死）", elapsed)
	if elapsed > 2*time.Second {
		t.Fatalf("GetLP 耗时超界：%v", elapsed)
	}
}

// TestP6EngineLongURLBoundedCost：引擎级真实复现路径——68KB 级 URL 经
// ModifyReq（4096 截断）后耗时与内存增量有界，且判定与截断形态一致。
func TestP6EngineLongURLBoundedCost(t *testing.T) {
	nr := networkrules.New()
	for i := 0; i < 60; i++ {
		if _, err := nr.ParseRule(fmt.Sprintf("*q%03d*", i), nil); err != nil {
			t.Fatalf("ParseRule(marker %d): %v", i, err)
		}
	}
	if _, err := nr.ParseRule("$removeparam=adtag", nil); err != nil {
		t.Fatalf("ParseRule(removeparam): %v", err)
	}
	nr.Compact()

	long := p6AdversarialURL(68071)
	if len(long) < 68000 {
		t.Fatalf("对抗 URL 构造不足：len=%d", len(long))
	}
	run := func(url string) (string, time.Duration) {
		req := httptest.NewRequest("GET", url, nil)
		req.Header.Set("Sec-Fetch-Dest", "script")
		start := time.Now()
		_, block, redirect := nr.ModifyReq(req)
		return fmt.Sprintf("block=%v rewritten=%v", block, redirect != ""), time.Since(start)
	}

	gotLong, dLong := run(long)
	gotShort, dShort := run(long[:4096])
	if gotLong != gotShort {
		t.Fatalf("对抗 URL 与截断形态判定不一致\n超长: %s\n截断: %s", gotLong, gotShort)
	}
	// q000/q001 落在 4096 内，普通阻断规则在首个候选即早退（networkrules.go
	// ModifyReq return ... true, ""），改写路径由 TestP6LongURLTruncation
	// Case C 单独覆盖；此处守卫阻断路径真实触发。
	if !strings.Contains(gotLong, "block=true") {
		t.Fatalf("守卫失败：4096 内标记规则应阻断，实测 %s", gotLong)
	}
	t.Logf("ModifyReq 68KB 级 URL：耗时 %v（截断形态 %v，宽松上限 200ms），判定=%s", dLong, dShort, gotLong)
	if dLong > 200*time.Millisecond {
		t.Fatalf("ModifyReq 耗时超界：%v", dLong)
	}

	// 内存增量（含请求构造）有界
	for i := 0; i < 3; i++ {
		run(long)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	const n = 5
	for i := 0; i < n; i++ {
		run(long)
	}
	runtime.ReadMemStats(&after)
	perCall := (after.TotalAlloc - before.TotalAlloc) / n
	t.Logf("ModifyReq 单次分配增量（含请求构造）：%d B/op（上限 1MB）", perCall)
	if perCall > 1<<20 {
		t.Fatalf("内存增量超界：%d B/op > 1MB", perCall)
	}
}

// ---------- 5. 基准（P6 前后对比门禁） ----------

func BenchmarkP6TreeMatch(b *testing.B) {
	tree := ruletree.New[string]()
	for i := 0; i < 300; i++ {
		pats := []string{
			fmt.Sprintf("||ads%03d.example.com^", i),
			fmt.Sprintf("||cdn%03d.net/banner/*.gif", i),
			fmt.Sprintf("/adframe%03d.", i),
			fmt.Sprintf("*tracker%03d*", i),
			fmt.Sprintf("https://pixel%03d.io/*", i),
		}
		for _, p := range pats {
			tree.Insert(p, p)
		}
	}
	urls := []string{
		"https://ads050.example.com/banner/creative.gif?utm=x&tracker042=y",
		"https://cdn123.net/banner/ad_728x90.gif",
		"https://www.example.com/page/adframe42./content",
		"https://pixel007.io/collect?id=x&tracker042=y",
		"https://tracker042.example.com/a/b/c?x=1&y=2&z=3",
		"https://plain.example.com/nothing/here?q=1",
	}
	b.Run("GetLP", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = tree.GetLP(urls[i%len(urls)])
		}
	})
	b.Run("Get", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = tree.Get(urls[i%len(urls)])
		}
	})
}

// ---------- 4d. 组合 URL 对抗门禁（记忆化＋预算，回归任务追加） ----------

// p6AlicdnURL：真实复现形态（alicdnprobe 同源），1017 字符，`??`+逗号密集分隔。
const p6AlicdnURL = `https://astyle.alicdn.com/??fdevlib/js/lofty/util/webp/1.0/webp.js,fdevlib/js/lofty/util/template/2.0/tplhandler.js,fdevlib/js/lofty/util/misc/2.0/misc.js,fdevlib/js/lofty/util/misc/1.0/misc.js,fdevlib/js/lofty/util/json/1.0/json.js,fdevlib/js/lofty/util/exposure/1.0/exposure.js,fdevlib/js/lofty/util/datalazyload/2.0/datalazyload.js,fdevlib/js/lofty/util/cookie/1.0/cookie.js,fdevlib/js/lofty/util/cms-vm/1.0/cms-vm-jsonp.js,fdevlib/js/lofty/ui/widget/1.0/widget.js,fdevlib/js/lofty/ui/tabs/2.0/tabs.js,fdevlib/js/lofty/alicn/subcookie/1.0/subcookie.js,fdevlib/js/lofty/alicn/everlog/1.0/everlog.js,fdevlib/js/lofty/alicn/aliuser/1.0/aliuser.js,fdevlib/js/lofty/alicn/alitalk/1.0/alitalk-shunt.js,pkg/@alife/lofty-xdutil/1.0.0/index.js,pkg/@alife/lofty-xdutil/1.0.0/crossdomain.js,pkg/@alife/lofty-json/1.0.0/index.js,fdevlib/js/app/link/plugin/i18n/1.0/i18n.js,pkg/@alife/art-mould/1.1.x/fmd.js,pkg/@alife/refly-wk-store/0.0.7/index.js,pkg/@alife/refly-request/0.2.x/index.js?_v=442e2630fdf2640b79da6d61abb1a111.js`

// TestP6AlicdnComboURLBoundedTime：组合 URL（1017 字符，真实复现形态）在通配符
// 重压树上 GetLP 与引擎 ModifyReq 均 <50ms（修复前 15s 无法完成）＋内存增量有界。
func TestP6AlicdnComboURLBoundedTime(t *testing.T) {
	tree := ruletree.New[string]()
	for i := 0; i < 60; i++ {
		tree.Insert(fmt.Sprintf("*q%03d*", i), fmt.Sprintf("R%03d", i))
	}
	tree.Insert("*a*b*", "AB")
	tree.Insert("*util*", "UTIL")
	tree.Insert("*js*", "JS")

	u := p6AlicdnURL
	if len(u) != 1017 {
		t.Fatalf("URL 长度漂移: %d", len(u))
	}
	for i := 0; i < 3; i++ { // 预热
		tree.GetLP(u)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	got := tree.GetLP(u)
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	perCall := after.TotalAlloc - before.TotalAlloc
	t.Logf("ruletree GetLP(alicdn): %v, 候选 %d, 单次分配 %d B", elapsed, len(got), perCall)
	if elapsed > 50*time.Millisecond {
		t.Fatalf("GetLP 耗时超界: %v > 50ms", elapsed)
	}
	if perCall > 4<<20 {
		t.Fatalf("内存增量超界: %d B > 4MB", perCall)
	}

	// 引擎级真实复现路径（youtube 6 列表引擎）
	nr, err := p6YTLoader(t)
	if err != nil {
		t.Skipf("列表不可载入：%v", err)
	}
	r := httptest.NewRequest("GET", u, nil)
	r.Header.Set("Sec-Fetch-Dest", "script")
	r.Header.Set("Referer", "https://www.taobao.com/")
	start = time.Now()
	_, block, redirect := nr.ModifyReq(r)
	engElapsed := time.Since(start)
	outcome := "allow"
	if block {
		outcome = "BLOCK"
	} else if redirect != "" {
		outcome = "redirect"
	}
	t.Logf("引擎 ModifyReq(alicdn): %v → %s", engElapsed, outcome)
	if engElapsed > 50*time.Millisecond {
		t.Fatalf("引擎耗时超界: %v > 50ms", engElapsed)
	}
}

// TestP6DenseSeparator4000：自造 4000 字符分隔符密集 URL，断言 <50ms 与内存有界。
func TestP6DenseSeparator4000(t *testing.T) {
	tree := ruletree.New[string]()
	for i := 0; i < 60; i++ {
		tree.Insert(fmt.Sprintf("*q%03d*", i), fmt.Sprintf("R%03d", i))
	}
	tree.Insert("*a*b*", "AB")
	tree.Insert("*js*", "JS")
	u := p6AdversarialURL(4000)
	for i := 0; i < 3; i++ {
		tree.GetLP(u)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	tree.GetLP(u)
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	perCall := after.TotalAlloc - before.TotalAlloc
	t.Logf("GetLP(4000 密集): %v, 单次分配 %d B", elapsed, perCall)
	if elapsed > 50*time.Millisecond {
		t.Fatalf("耗时超界: %v > 50ms", elapsed)
	}
	if perCall > 4<<20 {
		t.Fatalf("内存增量超界: %d B > 4MB", perCall)
	}
}

// TestP6TraversalBudgetFailOpen：把 MaxVisitedEntries 调到 500，构造能超预算
// 的组合 URL，断言：完成＋fail-open（结果为合法子集判定）＋原子计数递增。
func TestP6TraversalBudgetFailOpen(t *testing.T) {
	old := ruletree.MaxVisitedEntries
	ruletree.MaxVisitedEntries = 500
	defer func() { ruletree.MaxVisitedEntries = old }()

	tree := ruletree.New[string]()
	for i := 0; i < 60; i++ {
		tree.Insert(fmt.Sprintf("*q%03d*", i), fmt.Sprintf("R%03d", i))
	}
	tree.Insert("*a*b*", "AB")
	tree.Insert("*js*", "JS")
	u := p6AdversarialURL(4000)

	before := ruletree.TraversalBudgetExceeded()
	start := time.Now()
	got := tree.GetLP(u)
	elapsed := time.Since(start)
	after := ruletree.TraversalBudgetExceeded()
	t.Logf("预算触发测试: %v, 候选 %d（预算前计数 %d → 后 %d）", elapsed, len(got), before, after)
	if elapsed > 50*time.Millisecond {
		t.Fatalf("预算兜底后仍超时: %v", elapsed)
	}
	if after <= before {
		t.Fatalf("预算未触发：计数未递增（%d → %d）", before, after)
	}
	// fail-open 语义：结果仍合法（无重复、无 panic）
	seen := map[string]struct{}{}
	for _, v := range got {
		if _, dup := seen[v]; dup {
			t.Fatalf("fail-open 结果含重复项 %q", v)
		}
		seen[v] = struct{}{}
	}
	ruletree.MaxVisitedEntries = 1_000_000 // 恢复后验证预算未污染正常路径
	full := tree.GetLP(u)
	if len(full) < len(got) {
		t.Fatalf("预算内结果不应多于完整结果")
	}
}
