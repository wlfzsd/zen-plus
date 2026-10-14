// Package replacee2e 是一次性 scratch 实测探针：用真实抓取的
// https://www.baidu.com/ 响应过引擎响应路径，验证用户给的 $replace 规则
// `||baidu.com^$replace=/hao123/测试456/` 的端到端改写效果。
//
// 放在模块内的原因：Go internal/ 包只能被本模块代码引用，外部模块
// （测试临时\）无法 import internal/networkrules；与 tmp_enginebench\nrprobe
// 先例一致。用完可整目录删除。
package replacee2e

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/irbis-sh/zen-desktop/internal/networkrules"
)

const baiduRule = `||baidu.com^$replace=/hao123/测试456/`

// TestBaiduHao123ReplaceLive：真实抓取百度首页 → 引擎 ModifyRes → 断言
// hao123 全部替换为 测试456。响应体全文读取（长度以实际 len 为准，读取
// 上限 20MB 仅为防失控；引擎 $replace 自身上限 10MB，replace.go:49）。
func TestBaiduHao123ReplaceLive(t *testing.T) {
	nr := networkrules.New()
	if _, err := nr.ParseRule(baiduRule, nil); err != nil {
		t.Fatalf("ParseRule(%q): %v", baiduRule, err)
	}

	req, err := http.NewRequest(http.MethodGet, "https://www.baidu.com/", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("fetch https://www.baidu.com/: %v", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 20<<20))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	before := bytes.Count(body, []byte("hao123"))
	t.Logf("final URL: %s", res.Request.URL.String())
	t.Logf("status: %d, content-type: %q, body bytes: %d", res.StatusCode, res.Header.Get("Content-Type"), len(body))
	t.Logf(`occurrences of "hao123" before rewrite: %d`, before)

	// 复刻真实响应头（client.Do 已透明解压，体为明文，故删 Content-Encoding
	// 使头与体一致；Content-Length 由引擎改写后自行重设，rawbody.go:118）。
	resForEngine := &http.Response{
		StatusCode: res.StatusCode,
		Header:     res.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	resForEngine.Header.Del("Content-Encoding")
	resForEngine.Header.Del("Content-Length")

	appliedRules, err := nr.ModifyRes(res.Request, resForEngine)
	if err != nil {
		t.Fatalf("ModifyRes: %v", err)
	}
	applied := len(appliedRules) > 0

	after, err := io.ReadAll(resForEngine.Body)
	if err != nil {
		t.Fatalf("read rewritten body: %v", err)
	}

	afterHao := bytes.Count(after, []byte("hao123"))
	afterNew := bytes.Count(after, []byte("测试456"))
	t.Logf("applied: %v, rewritten body bytes: %d", applied, len(after))
	t.Logf(`occurrences of "hao123" after rewrite: %d`, afterHao)
	t.Logf(`occurrences of "测试456" after rewrite: %d`, afterNew)

	// 产物落盘（完整无损）：改写前/后各一份，供人工开浏览器核对。
	if outDir := os.Getenv("BAIDU_REPLACE_OUT"); outDir != "" {
		if mkErr := os.MkdirAll(outDir, 0o755); mkErr != nil {
			t.Logf("mkdir out dir: %v", mkErr)
		} else {
			orig := filepath.Join(outDir, "baidu_before.html")
			rewr := filepath.Join(outDir, "baidu_after.html")
			if wErr := os.WriteFile(orig, body, 0o644); wErr != nil {
				t.Logf("write %s: %v", orig, wErr)
			} else {
				t.Logf("original body written: %s (%d bytes)", orig, len(body))
			}
			if wErr := os.WriteFile(rewr, after, 0o644); wErr != nil {
				t.Logf("write %s: %v", rewr, wErr)
			} else {
				t.Logf("rewritten body written: %s (%d bytes)", rewr, len(after))
			}
		}
	}

	// 首个替换点上下文窗口（前后各 40 字节，窗口 ≤ 40+9+40 字节，行内已标注）。
	if idx := bytes.Index(after, []byte("测试456")); idx >= 0 {
		lo := idx - 40
		if lo < 0 {
			lo = 0
		}
		hi := idx + len("测试456") + 40
		if hi > len(after) {
			hi = len(after)
		}
		t.Logf("context window after[%d:%d] (%dB): %q", lo, hi, hi-lo, after[lo:hi])
	}

	if !applied {
		t.Fatalf("$replace rule reported not applied (applied=false)")
	}
	if before == 0 {
		t.Fatalf(`real response contained no "hao123" — nothing to replace; inspect page structure`)
	}
	if afterHao != 0 {
		t.Fatalf(`"hao123" still present after rewrite: %d occurrences`, afterHao)
	}
	if afterNew != before {
		t.Fatalf("replacement count mismatch: before=%d after=%d", before, afterNew)
	}
}
