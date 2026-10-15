package networkrules

// [2026-10-14 正则帕累托探针·方向②] 定位"少数未索引正则烧 1/3 CPU"的贡献者：
// 1) 逐条正则执行次数×耗时（帕累托前 12）；
// 2) 前导字面量预筛（^锚定场景下 strings.HasPrefix / Index）可跳过率实测。
// 无损：纯测量，不改引擎。env 门控 ZEN_REX_PARETO=1。
import (
	"bufio"
	"bytes"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"regexp/syntax"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	rpxOnce   sync.Once
	rpxErr    error
	rpxEngine *NetworkRules
	rpxCorpus []string
)

func rpxLoad(tb testing.TB) {
	tb.Helper()
	rpxOnce.Do(func() {
		filtersDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
		var datas [][]byte
		for _, u := range taxFilterURLs {
			h := fmt.Sprintf("%x", md5.Sum([]byte(u)))
			p := filepath.Join(filtersDir, h+".cache.txt")
			b, err := os.ReadFile(p)
			if err != nil {
				rpxErr = err
				return
			}
			datas = append(datas, b)
		}
		e := New()
		name := "rexpareto"
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
		rpxEngine = e
		raw2, err := os.ReadFile(filepath.Join("..", "ruletree", "testdata", "urls.txt"))
		if err != nil {
			rpxErr = err
			return
		}
		sc := bufio.NewScanner(bytes.NewReader(raw2))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			u := strings.TrimSpace(sc.Text())
			if u != "" {
				rpxCorpus = append(rpxCorpus, u)
			}
		}
	})
	if rpxErr != nil {
		tb.Skipf("rexpareto data unavailable: %v", rpxErr)
	}
}

func TestZZRexPareto(t *testing.T) {
	if os.Getenv("ZEN_REX_PARETO") != "1" {
		t.Skip("ZEN_REX_PARETO not set")
	}
	rpxLoad(t)
	nr := rpxEngine
	ps := nr.primaryStore
	n := len(ps.regexp)
	if n == 0 {
		t.Fatal("no regexp rules")
	}
	// --- 1) 逐条执行计数与耗时（复刻 Get 的循环，计入 match()） ---
	cnt := make([]int64, n)
	tim := make([]int64, n)
	for k := 0; k < len(rpxCorpus); k++ {
		u := rpxCorpus[k]
		idx := ps.idx.Load()
		if idx == nil {
			idx = ps.buildIdx()
			if !ps.idx.CompareAndSwap(nil, idx) {
				idx = ps.idx.Load()
			}
		}
		var hit []bool
		if n <= idxMaxRules {
			hit = make([]bool, n)
			var hb hitBits
			idx.markCandidates(u, &hb)
			for i := range idx.tokensOf {
				toks := idx.tokensOf[i]
				if len(toks) == 0 {
					hit[i] = true
					continue
				}
				for _, tk := range toks {
					if strings.Contains(u, tk) {
						hit[i] = true
						break
					}
				}
			}
		} else {
			for i := range hit {
				hit[i] = true
			}
		}
		for i := range ps.regexp {
			if n <= idxMaxRules && !hit[i] {
				continue
			}
			rr := &ps.regexp[i]
			st := time.Now()
			_ = rr.match(u, false)
			tim[i] += time.Since(st).Nanoseconds()
			cnt[i]++
		}
	}
	type row struct {
		i    int
		c    int64
		t    int64
		raw  string
		fast bool
	}
	var rows []row
	var totT int64
	var totC int64
	for i := range cnt {
		totT += tim[i]
		totC += cnt[i]
		rows = append(rows, row{i, cnt[i], tim[i], ps.regexp[i].value.RawRule, ps.regexp[i].fast != nil})
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].t > rows[b].t })
	t.Logf("total: regexp=%d corpus=%d 执行总次数=%d 总耗时=%.2fms", n, len(rpxCorpus), totC, float64(totT)/1e6)
	for j := 0; j < 12 && j < len(rows); j++ {
		r := rows[j]
		t.Logf("Pareto#%d: cumT=%.2fms(%.1f%%) execs=%d raw=%.60s fast=%v",
			j+1, float64(r.t)/1e6, 100*float64(r.t)/float64(totT), r.c, r.raw, r.fast)
	}

	// --- 2) 前导字面量预筛可跳过率（^锚定正则：URL 须以字面量开头或含之） ---
	var nPrefix int
	skipURLs, totalPairs := 0, 0
	for i := range ps.regexp {
		pat, _ := parseRuleParts(ps.regexp[i].value.RawRule)
		if len(pat) < 2 {
			continue
		}
		body := pat[1 : len(pat)-1]
		re, err := syntax.Parse(body, syntax.Perl)
		if err != nil {
			continue
		}
		pre, _ := taxLeadingLiteral(re)
		if pre == "" {
			continue
		}
		nPrefix++
		for k := 0; k < len(rpxCorpus); k += 4 {
			u := rpxCorpus[k]
			totalPairs++
			// ^ 锚定语义下：如果正则要求 ^紧跟字面量（无前缀通配），URL 前缀不匹配即可跳过
			if re.Op == syntax.OpConcat && len(re.Sub) > 0 && re.Sub[0].Op == syntax.OpLiteral &&
				len(re.Sub) > 1 && (re.Sub[1].Op == syntax.OpBeginText || re.Sub[1].Op == syntax.OpBeginLine) {
				// ^ 在字面量之后 = 实际无锚定意义，退化为 Contains
				if !strings.Contains(u, pre) {
					skipURLs++
				}
			} else {
				if !strings.HasPrefix(u, pre) {
					skipURLs++
				}
			}
		}
	}
	t.Logf("leading-literal 预筛: 有前导字面量正则=%d 抽样对=%d 可跳过=%d(%.1f%%)",
		nPrefix, totalPairs, skipURLs, 100*float64(skipURLs)/float64(totalPairs))
}
