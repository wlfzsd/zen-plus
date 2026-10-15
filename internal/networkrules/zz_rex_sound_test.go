package networkrules

// [2026-10-14 token索引健全性性质测试·红队] 对全部正则规则×抽样 URL 直接断言：
// 索引跳过 ⇒ 该正则对该 URL 必不匹配（零假阴性）。任何违反=真 bug。
// 同时输出 never-skip 规则数（供 altfix 前后 A/B）。env 门控 ZEN_REX_SOUND=1。
import (
	"bufio"
	"bytes"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	sdOnce   sync.Once
	sdErr    error
	sdEngine *NetworkRules
	sdCorpus []string
)

func sdLoad(tb testing.TB) {
	tb.Helper()
	sdOnce.Do(func() {
		filtersDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
		var datas [][]byte
		for _, u := range taxFilterURLs {
			h := fmt.Sprintf("%x", md5.Sum([]byte(u)))
			p := filepath.Join(filtersDir, h+".cache.txt")
			b, err := os.ReadFile(p)
			if err != nil {
				sdErr = err
				return
			}
			datas = append(datas, b)
		}
		e := New()
		name := "rexsound"
		for _, raw := range datas {
			sc := bufio.NewScanner(bytes.NewReader(raw))
			sc.Buffer(nil, 1<<20)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
					continue
				}
				e.ParseRule(line, &name)
			}
		}
		e.Compact()
		sdEngine = e
		raw2, err := os.ReadFile(filepath.Join("..", "ruletree", "testdata", "urls.txt"))
		if err != nil {
			sdErr = err
			return
		}
		sc := bufio.NewScanner(bytes.NewReader(raw2))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			u := strings.TrimSpace(sc.Text())
			if u != "" {
				sdCorpus = append(sdCorpus, u)
			}
		}
	})
	if sdErr != nil {
		tb.Skipf("rexsound data unavailable: %v", sdErr)
	}
}

func checkSound[T comparable](tb *testing.T, storeName string, rules []regexpRule[T], idx *reIdx, corpus []string) {
	tb.Helper()
	neverSkip := 0
	var violations int
	var checkedSkips int64
	for i := range rules {
		toks := idx.tokensOf[i]
		if len(toks) == 0 {
			neverSkip++
			continue
		}
		re := rules[i].regexp
		for k := 0; k < len(corpus); k += 2 {
			u := corpus[k]
			present := false
			for _, tk := range toks {
				if strings.Contains(u, tk) {
					present = true
					break
				}
			}
			if present {
				continue
			}
			checkedSkips++
			if re.MatchString(u) {
				violations++
				if violations <= 3 {
					tb.Errorf("[%s] 健全性违反 rule#%d toks=%q url=%.80q", storeName, i, toks, u)
				}
			}
		}
	}
	tb.Logf("[%s] rules=%d neverSkip=%d 抽样跳过断言=%d 违反=%d", storeName, len(rules), neverSkip, checkedSkips, violations)
}

func TestZZRexSound(t *testing.T) {
	if os.Getenv("ZEN_REX_SOUND") != "1" {
		t.Skip("ZEN_REX_SOUND not set")
	}
	sdLoad(t)
	nr := sdEngine
	pi := nr.primaryStore.buildIdx()
	checkSound(t, "primary", nr.primaryStore.regexp, pi, sdCorpus)
	ei := nr.exceptionStore.buildIdx()
	checkSound(t, "exception", nr.exceptionStore.regexp, ei, sdCorpus)
}
