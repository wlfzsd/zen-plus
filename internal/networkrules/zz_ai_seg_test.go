package networkrules

// [2026-10-14 结构重设计前置：树遍历逐段成本实测]
// 把 GetLP 的三个子树分开计价——跳过整类模式入树，测 ModifyReq 差值：
//   off     = 全量引擎
//   noDom   = 跳过 domainBoundary 类（pattern 以 || 开头：A1/A2/A3/A4 ≈26.6万）
//   noRoot  = 跳过 root 子树类（无锚定字面量 F + 含通配 D + 含^ E ≈24.3万）
// 各段差值 = 该子树走位+候选的真实成本 → 决定结构重设计打哪。
// 有损探针（跳过的类不匹配），仅计时，不作判定。env: ZEN_AI_SEG=1
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

func segCachePath(u string) string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters",
		fmt.Sprintf("%x", md5.Sum([]byte(u)))+".cache.txt")
}

func segLines(t *testing.T) [][]string {
	t.Helper()
	var all [][]string
	for _, u := range taxFilterURLs {
		b, err := os.ReadFile(segCachePath(u))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(nil, 1<<20)
		var ls []string
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
				continue
			}
			ls = append(ls, line)
		}
		all = append(all, ls)
	}
	return all
}

// segClass: 0=全收 1=跳 domainBoundary(||) 2=跳 root 子树(无||、无|锚定的其余)
func segKeep(line string, class int) bool {
	pat := line
	if i := strings.IndexByte(pat, '$'); i >= 0 {
		pat = pat[:i]
	}
	if strings.HasPrefix(pat, "@@") {
		pat = pat[2:]
	}
	isDom := strings.HasPrefix(pat, "||")
	switch class {
	case 1:
		return !isDom
	case 2:
		if pat == "" {
			return false
		}
		// root 子树 = 无 || 且无 | 锚定（|http 锚定在 anchorRoot，量极小忽略归属误差）
		return isDom || pat[0] == '|'
	case 3:
		// 归因验证：仅跳过含通配 * 或含 ^ 的无 || 模式（D+E ≈2,274 条）
		return isDom || (!strings.Contains(pat, "*") && !strings.Contains(pat, "^"))
	}
	return true
}

func segBuild(t *testing.T, class int) *NetworkRules {
	e := New(WithDomainGate(true)) // 生产口径（WS1 开），账目与交付态一致
	name := "seg"
	nKept := 0
	for _, ls := range segLines(t) {
		for _, line := range ls {
			if !segKeep(line, class) {
				continue
			}
			if _, err := e.ParseRule(line, &name); err == nil {
				nKept++
			}
		}
	}
	e.Compact()
	t.Logf("  class=%d kept=%d", class, nKept)
	return e
}

func segRun(t *testing.T, e *NetworkRules) time.Duration {
	aiLoad(t)
	t0 := time.Now()
	for _, u := range aiCorpus {
		pu, err := url.Parse(u)
		if err != nil || pu.Host == "" {
			continue
		}
		r := &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
		_, _, _ = e.ModifyReq(r)
	}
	return time.Since(t0)
}

func TestZZAISegmentCost(t *testing.T) {
	if os.Getenv("ZEN_AI_SEG") != "1" {
		t.Skip("ZEN_AI_SEG not set")
	}
	aiLoad(t)
	off := segRun(t, segBuild(t, 0))
	noDom := segRun(t, segBuild(t, 1))
	noRoot := segRun(t, segBuild(t, 2))
	noWE := segRun(t, segBuild(t, 3))
	t.Logf("SEG-COST: 全量=%dms  跳domainBoundary=%dms（A类段=%.1f%%）  跳root=%dms（root段=%.1f%%）  仅跳通配/*^类=%dms（该类=%.1f%%）",
		off.Milliseconds(), noDom.Milliseconds(), 100*float64(off-noDom)/float64(off),
		noRoot.Milliseconds(), 100*float64(off-noRoot)/float64(off),
		noWE.Milliseconds(), 100*float64(off-noWE)/float64(off))
}
