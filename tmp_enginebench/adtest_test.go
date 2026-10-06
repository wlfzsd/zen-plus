// adtest_test.go — 广告拦截"真实性有效性"测试。
//
// 数据集：adtest_urls.txt（619 条探测 URL，覆盖 6 个广告拦截测试页机制，
// 生成器 gen_adtest_urls.py，来源标注见 adtest_urls_meta.txt）。
//
// 判定分两层：
//  1. 一致性：frozen 基线引擎（tmp_enginebench/baselinenr）与生产引擎对每条
//     探测 × 8 种请求头组合跑 ModifyReq，断言 (shouldBlock, redirectURL=="",
//     len(appliedRules)) 完全一致（与任务口径一致，阻塞返回单条不比对
//     RawRule 集——ruletree.Get 的 map 去重使顺序不稳定，见 equiv_test.go 注）。
//  2. 有效性（对"预期答案"）：
//     - d3ward 得分：d3ward 测试页以 adblock_data.json 清单打分，探到即扣分；
//     这里统计两引擎对 d3ward 机制探测行（M1/M2）的 shouldBlock 比例，
//     断言 生产 ≥ 基线（不得出现过滤变少），并输出具体得分。
//     - 通用广告/追踪探测（M3-M6）拦截率两引擎对比。
//     - ModifyRes：20 条 text/html 响应（含 CSP 等典型头），双引擎
//     (err 是否 nil, len(appliedRules)) 一致、0 panic。
package tmp_enginebench

import (
	"bufio"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	prodnr "github.com/irbis-sh/zen-desktop/internal/networkrules"
	basenr "github.com/irbis-sh/zen-desktop/tmp_enginebench/baselinenr/networkrules"
)

// ---------------- 共享引擎（同一测试二进制内只装载一次） ----------------

var (
	adtestEngineOnce   sync.Once
	adtestEngineProd   *prodnr.NetworkRules
	adtestEngineBase   *basenr.NetworkRules
	adtestEngineErrStr string
)

func adtestEngines(t testing.TB) (*prodnr.NetworkRules, *basenr.NetworkRules) {
	t.Helper()
	adtestEngineOnce.Do(func() {
		adtestEngineProd = prodnr.New()
		adtestEngineBase = basenr.New()
		name := "adtest"
		for _, l := range loadRealLines(t) {
			if _, err := adtestEngineProd.ParseRule(l, &name); err != nil {
				_ = err
			}
			if _, err := adtestEngineBase.ParseRule(l, &name); err != nil {
				_ = err
			}
		}
	})
	if adtestEngineProd == nil || adtestEngineBase == nil {
		t.Fatal("engine build failed: " + adtestEngineErrStr)
	}
	return adtestEngineProd, adtestEngineBase
}

// adtestRow 是 adtest_urls.txt 的一行。
type adtestRow struct {
	URL     string
	Site    string // Sec-Fetch-Site
	Dest    string // Sec-Fetch-Dest
	Referer string
	Mech    string // adtest_urls_meta.txt 的机制标注（同行序）
}

// loadAdTestRows 读探测数据集（URL/site/dest/referer 四列，制表符分隔），
// 机制标注从 adtest_urls_meta.txt 按行序对齐。
func loadAdTestRows(t testing.TB) []adtestRow {
	t.Helper()
	read := func(name string, ncols int) []string {
		f, err := os.Open(name)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer f.Close()
		var lines []string
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		if err := sc.Err(); err != nil {
			t.Fatalf("scan %s: %v", name, err)
		}
		return lines
	}
	dataLines := read("adtest_urls.txt", 4)
	metaLines := read("adtest_urls_meta.txt", 2)
	if len(dataLines) != len(metaLines) {
		t.Fatalf("adtest_urls.txt 行数(%d) 与 adtest_urls_meta.txt 行数(%d) 不一致", len(dataLines), len(metaLines))
	}
	rows := make([]adtestRow, 0, len(dataLines))
	for i, line := range dataLines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			t.Fatalf("adtest_urls.txt 第 %d 行列数=%d（要求 4）", i+1, len(parts))
		}
		if _, err := url.Parse(parts[0]); err != nil {
			t.Fatalf("adtest_urls.txt 第 %d 行 URL 不可解析: %v", i+1, err)
		}
		mparts := strings.Split(metaLines[i], "\t")
		if len(mparts) != 2 {
			t.Fatalf("adtest_urls_meta.txt 第 %d 行列数=%d", i+1, len(mparts))
		}
		rows = append(rows, adtestRow{URL: parts[0], Site: parts[1], Dest: parts[2], Referer: parts[3], Mech: mparts[0]})
	}
	if len(rows) < 300 {
		t.Fatalf("数据集不足 300 条: %d", len(rows))
	}
	return rows
}

func adtestBuildReq(u, site, dest, referer, userNav string) *http.Request {
	parsed, err := url.Parse(u)
	if err != nil {
		return nil
	}
	req := &http.Request{Method: http.MethodGet, URL: parsed, Header: http.Header{}}
	req.Header.Set("Sec-Fetch-Site", site)
	req.Header.Set("Sec-Fetch-Dest", dest)
	if userNav != "" {
		req.Header.Set("Sec-Fetch-User", userNav)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	return req
}

// ---------------- 1) 双引擎一致性：全数据集 × 8 头组合 ----------------

func TestAdTestDualEngineConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("全量语料装载耗时长，-short 跳过")
	}
	prod, base := adtestEngines(t)
	rows := loadAdTestRows(t)

	type outcome struct {
		block      bool
		noRedirect bool // redirectURL == ""
		appliedN   int
		panicMsg   string
	}
	runPair := func(r adtestRow, combo goldenCombo, referer string) (op, ob outcome) {
		up := func() outcome {
			defer func() {
				if v := recover(); v != nil {
					op.panicMsg = fmt.Sprint(v)
				}
			}()
			req := adtestBuildReq(r.URL, combo.site, combo.dest, referer, map[bool]string{true: "?1", false: ""}[combo.user])
			if req == nil {
				op.panicMsg = "url parse failed"
				return op
			}
			applied, block, redirect := prod.ModifyReq(req)
			op.block, op.noRedirect, op.appliedN = block, redirect == "", len(applied)
			return op
		}
		ub := func() outcome {
			defer func() {
				if v := recover(); v != nil {
					ob.panicMsg = fmt.Sprint(v)
				}
			}()
			req := adtestBuildReq(r.URL, combo.site, combo.dest, referer, map[bool]string{true: "?1", false: ""}[combo.user])
			if req == nil {
				ob.panicMsg = "url parse failed"
				return ob
			}
			applied, block, redirect := base.ModifyReq(req)
			ob.block, ob.noRedirect, ob.appliedN = block, redirect == "", len(applied)
			return ob
		}
		op, ob = up(), ub()
		return
	}

	mismatch := 0
	pairs := 0
	for _, r := range rows {
		for _, combo := range goldenCombos { // 8 种头组合（golden_test.go 定义）
			referer := r.Referer
			if referer == "" {
				referer = combo.referer
			}
			op, ob := runPair(r, combo, referer)
			pairs++
			if op.panicMsg != "" || ob.panicMsg != "" {
				mismatch++
				t.Errorf("panic 差异 %s [%s]: prod=%q base=%q", r.URL, combo.name, op.panicMsg, ob.panicMsg)
				continue
			}
			if op.block != ob.block || op.noRedirect != ob.noRedirect || op.appliedN != ob.appliedN {
				mismatch++
				t.Errorf("三元组不一致 %s [%s]: prod={block=%v noRedir=%v applied=%d} base={block=%v noRedir=%v applied=%d}",
					r.URL, combo.name, op.block, op.noRedirect, op.appliedN, ob.block, ob.noRedirect, ob.appliedN)
			}
		}
	}
	if mismatch > 0 {
		t.Fatalf("一致性失败: %d/%d 对不一致", mismatch, pairs)
	}
	t.Logf("一致性通过: %d 探测 × %d 组合 = %d 对，两引擎 (shouldBlock, redirectURL==\"\", len(appliedRules)) 全一致", len(rows), len(goldenCombos), pairs)
}

// ---------------- 2) d3ward 得分（有效性：对测试页预期答案） ----------------

// d3ward 测试页打分机制：对 adblock_data.json 清单逐 host 发起探测，取回
// 成功即扣分。此处同构判定：引擎对探测行 shouldBlock（严格口径）或
// shouldBlock||redirectURL!=""（宽口径：重定向同样使原始资源取回失败）。
func TestAdTestD3wardScore(t *testing.T) {
	if testing.Short() {
		t.Skip("全量语料装载耗时长，-short 跳过")
	}
	prod, base := adtestEngines(t)
	rows := loadAdTestRows(t)

	// d3ward 机制行 = M1-d3ward-data / M2-d3host
	var d3rows []adtestRow
	for _, r := range rows {
		if strings.HasPrefix(r.Mech, "M1-") || strings.HasPrefix(r.Mech, "M2-") {
			d3rows = append(d3rows, r)
		}
	}
	if len(d3rows) < 200 {
		t.Fatalf("d3ward 机制探测行不足 200: %d", len(d3rows))
	}

	var prodBlock, baseBlock, prodInter, baseInter int
	var prodMisses, baseMisses []string
	for _, r := range d3rows {
		// 用与 d3ward 探测最贴近的组合：跨站资源抓取
		reqP := adtestBuildReq(r.URL, "cross-site", r.Dest, r.Referer, "")
		reqB := adtestBuildReq(r.URL, "cross-site", r.Dest, r.Referer, "")
		if reqP == nil || reqB == nil {
			continue
		}
		_, blockP, redirP := prod.ModifyReq(reqP)
		_, blockB, redirB := base.ModifyReq(reqB)
		if blockP {
			prodBlock++
		} else if len(prodMisses) < 20 {
			prodMisses = append(prodMisses, r.URL)
		}
		if blockB {
			baseBlock++
		} else if len(baseMisses) < 20 {
			baseMisses = append(baseMisses, r.URL)
		}
		if blockP || redirP != "" {
			prodInter++
		}
		if blockB || redirB != "" {
			baseInter++
		}
	}
	for _, u := range prodMisses {
		t.Logf("d3ward 未拦截样本（生产）: %s", u)
	}
	for _, u := range baseMisses {
		t.Logf("d3ward 未拦截样本（基线）: %s", u)
	}
	total := len(d3rows)
	pct := func(n int) string { return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(total)) }
	t.Logf("d3ward 机制得分（严格口径 shouldBlock）: 生产 %d/%d=%s, 基线 %d/%d=%s",
		prodBlock, total, pct(prodBlock), baseBlock, total, pct(baseBlock))
	t.Logf("d3ward 机制得分（宽口径 block||redirect）: 生产 %d/%d=%s, 基线 %d/%d=%s",
		prodInter, total, pct(prodInter), baseInter, total, pct(baseInter))
	if prodBlock < baseBlock {
		t.Errorf("过滤有效性回退：d3ward 严格得分 生产(%d) < 基线(%d)", prodBlock, baseBlock)
	}
	if prodInter < baseInter {
		t.Errorf("过滤有效性回退：d3ward 宽口径得分 生产(%d) < 基线(%d)", prodInter, baseInter)
	}
}

// ---------------- 3) 通用广告/追踪探测拦截率（M3-M6） ----------------

func TestAdTestGeneralAdDomains(t *testing.T) {
	if testing.Short() {
		t.Skip("全量语料装载耗时长，-short 跳过")
	}
	prod, base := adtestEngines(t)
	rows := loadAdTestRows(t)

	mechTotals := map[string]int{}
	prodHits := map[string]int{}
	baseHits := map[string]int{}
	for _, r := range rows {
		if strings.HasPrefix(r.Mech, "M1-") || strings.HasPrefix(r.Mech, "M2-") {
			continue
		}
		reqP := adtestBuildReq(r.URL, "cross-site", r.Dest, r.Referer, "")
		reqB := adtestBuildReq(r.URL, "cross-site", r.Dest, r.Referer, "")
		if reqP == nil || reqB == nil {
			continue
		}
		_, blockP, _ := prod.ModifyReq(reqP)
		_, blockB, _ := base.ModifyReq(reqB)
		mechTotals[r.Mech]++
		if blockP {
			prodHits[r.Mech]++
		}
		if blockB {
			baseHits[r.Mech]++
		}
	}
	if len(mechTotals) == 0 {
		t.Fatal("通用探测行为空")
	}
	var totP, totB, totN int
	for mech, n := range mechTotals {
		t.Logf("%-18s: 生产 %2d/%2d 拦截, 基线 %2d/%2d 拦截", mech, prodHits[mech], n, baseHits[mech], n)
		totP += prodHits[mech]
		totB += baseHits[mech]
		totN += n
	}
	t.Logf("通用探测合计: 生产 %d/%d=%.1f%%, 基线 %d/%d=%.1f%%",
		totP, totN, 100*float64(totP)/float64(totN), totB, totN, 100*float64(totB)/float64(totN))
}

// ---------------- 4) ModifyRes 一致性（20 条 text/html 响应） ----------------

func TestAdTestModifyResConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("全量语料装载耗时长，-short 跳过")
	}
	prod, base := adtestEngines(t)
	rows := loadAdTestRows(t)
	if len(rows) < 20 {
		t.Fatal("数据集不足 20 条")
	}

	// 20 条典型 text/html 文档响应头形态（含 CSP / 文档头 / 缓存 / Cookie）
	resHeads := []map[string]string{
		{"Content-Type": "text/html; charset=utf-8"},
		{"Content-Type": "text/html; charset=utf-8", "Content-Security-Policy": "default-src 'self'; script-src 'self' https://pagead2.googlesyndication.com"},
		{"Content-Type": "text/html; charset=iso-8859-1", "X-Content-Type-Options": "nosniff"},
		{"Content-Type": "text/html", "Content-Security-Policy": "frame-ancestors 'none'"},
		{"Content-Type": "TEXT/HTML; charset=UTF-8", "Cache-Control": "no-store"},
		{"Content-Type": "text/html;charset=utf-8", "Set-Cookie": "sid=abc; Path=/; HttpOnly"},
		{"Content-Type": "text/html; charset=utf-8", "Server": "nginx", "X-Frame-Options": "SAMEORIGIN"},
		{"Content-Type": "text/html; charset=utf-8", "Content-Encoding": "gzip", "Vary": "Accept-Encoding"},
		{"Content-Type": "text/html; charset=utf-8", "Strict-Transport-Security": "max-age=31536000"},
		{"Content-Type": "text/html; charset=utf-8", "Referrer-Policy": "strict-origin-when-cross-origin"},
		{"Content-Type": "text/html; charset=utf-8", "Content-Security-Policy-Report-Only": "default-src 'none'"},
		{"Content-Type": "text/html; charset=utf-8", "Permissions-Policy": "interest-cohort=()"},
		{"Content-Type": "text/html; charset=utf-8", "Cross-Origin-Opener-Policy": "same-origin"},
		{"Content-Type": "text/html; charset=utf-8", "ETag": "\"abc123\"", "Last-Modified": "Mon, 05 Oct 2026 00:00:00 GMT"},
		{"Content-Type": "text/html; charset=utf-8", "Location": "https://www.example.com/", "X-Debug": "no"},
		{"Content-Type": "text/html; charset=utf-8", "Set-Cookie": "a=1; Path=/", "Set-Cookie2": "b=2; Path=/"},
		{"Content-Type": "text/html; charset=utf-8", "Content-Length": "1024", "Date": "Mon, 05 Oct 2026 00:00:00 GMT"},
		{"Content-Type": "text/html; charset=utf-8", "X-Powered-By": "PHP/8.3", "X-Request-Id": "req-1"},
		{"Content-Type": "text/html; charset=utf-8", "Cross-Origin-Resource-Policy": "cross-origin"},
		{"Content-Type": "text/html; charset=utf-8", "Content-Security-Policy": "script-src 'nonce-abc'; report-uri /csp"},
	}
	if len(resHeads) != 20 {
		t.Fatalf("响应头形态应为 20 条: %d", len(resHeads))
	}

	callRes := func(isProd bool, u string, heads map[string]string) (errNil bool, appliedN int, panicMsg string) {
		defer func() {
			if v := recover(); v != nil {
				panicMsg = fmt.Sprint(v)
			}
		}()
		req := adtestBuildReq(u, "cross-site", "document", rows[0].Referer, "")
		if req == nil {
			panicMsg = "url parse failed"
			return
		}
		h := http.Header{}
		for k, v := range heads {
			h.Set(k, v)
		}
		res := &http.Response{
			StatusCode: 200,
			Status:     "200 OK",
			Header:     h,
			Body:       http.NoBody,
			Request:    req,
		}
		if isProd {
			ap, err := prod.ModifyRes(req, res)
			errNil = err == nil
			appliedN = len(ap)
		} else {
			ap, err := base.ModifyRes(req, res)
			errNil = err == nil
			appliedN = len(ap)
		}
		return
	}

	mismatch := 0
	for i := 0; i < 20; i++ {
		u := rows[i%len(rows)].URL
		errP, nP, panP := callRes(true, u, resHeads[i])
		errB, nB, panB := callRes(false, u, resHeads[i])
		if panP != "" || panB != "" {
			mismatch++
			t.Errorf("ModifyRes panic #%d %s: prod=%q base=%q", i, u, panP, panB)
			continue
		}
		if errP != errB || nP != nB {
			mismatch++
			t.Errorf("ModifyRes 不一致 #%d %s: prod={errNil=%v applied=%d} base={errNil=%v applied=%d}", i, u, errP, nP, errB, nB)
		}
	}
	if mismatch > 0 {
		t.Fatalf("ModifyRes 一致性失败: %d 处", mismatch)
	}
	t.Logf("ModifyRes 一致性通过: 20 条 text/html 响应（含 CSP/文档头），0 panic，(err, len(appliedRules)) 两引擎一致")
}
