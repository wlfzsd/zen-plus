package networkrules

// [2026-10-14 方案B 收益上界探针] 先测后装：F 类（无 *^| 的纯字面量 contains，
// 约 24 万条）不入树时的 ModifyReq 计时对比。
//
//   off = 正常引擎（基线）
//   on  = F 类模式被跳过 Insert（故意有损！仅测走位/候选成本上界，不作匹配）
//
// 差值 = 方案B（AC 自动机接管 F 类 + 取消 marker 位位置枚举）可省的硬上界。
// env: ZEN_AI_NOF_PROBE=1
import (
	"bufio"
	"bytes"
	"crypto/md5"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nofCachePath(u string) string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters",
		fmt.Sprintf("%x", md5.Sum([]byte(u)))+".cache.txt")
}

func nofLines(t *testing.T, u string) []string {
	t.Helper()
	b, err := os.ReadFile(nofCachePath(u))
	if err != nil {
		t.Fatalf("read %s: %v", u, err)
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 1<<20)
	var out []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func nofIsF(line string) bool {
	pat := line
	if i := strings.IndexByte(pat, '$'); i >= 0 {
		pat = pat[:i]
	}
	if strings.HasPrefix(pat, "@@") {
		pat = pat[2:]
	}
	return pat != "" && !strings.ContainsAny(pat, "*^|")
}

func nofReq(u string) *http.Request {
	pu, err := url.Parse(u)
	if err != nil || pu.Host == "" {
		return nil
	}
	return &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
}

func TestZZAINoFUpperBound(t *testing.T) {
	if os.Getenv("ZEN_AI_NOF_PROBE") != "1" {
		t.Skip("ZEN_AI_NOF_PROBE not set")
	}
	aiLoad(t) // 复用语料 aiCorpus

	// 每引擎独立装载；on 版直接跳过 F 类的 ParseRule（故意有损，仅计时）
	run := func(skipF bool) (ms int64, skipped, kept int) {
		e := New(WithHostSplit(false))
		name := "nof-probe"
		for _, u := range taxFilterURLs {
			for _, line := range nofLines(t, u) {
				if skipF && nofIsF(line) {
					skipped++
					continue
				}
				if _, err := e.ParseRule(line, &name); err == nil {
					kept++
				}
			}
		}
		e.Compact()
		t0 := time.Now()
		for _, u := range aiCorpus {
			if r := nofReq(u); r != nil {
				_, _, _ = e.ModifyReq(r)
			}
		}
		return time.Since(t0).Milliseconds(), skipped, kept
	}

	offMs, _, keptOff := run(false)
	onMs, nF, keptOn := run(true)
	t.Logf("NOF-PROBE: off=%dms(kept=%d)  on=%dms(kept=%d, 跳过F类=%d)  方案B节省上界=%.1f%%",
		offMs, keptOff, onMs, keptOn, nF, 100*float64(offMs-onMs)/float64(offMs))
}
