package networkrules

// [2026-10-14 候选过滤探针·方向②补完] 定谳 ShouldMatchReqNav 18.44% 桶：
// 1) 每 URL 树候选数分布（树遍历输出多少候选）；
// 2) ShouldMatchReqNav 淘汰率（多少候选被 domain/dest 过滤掉）；
// 3) $domain 规则的域条目数分布（matchHost 13% 的线性扫描宽度）。
// 无损：纯测量。env 门控 ZEN_CAND_PROBE=1。
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
	"sync"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

var (
	cpdOnce   sync.Once
	cpdErr    error
	cpdEngine *NetworkRules
	cpdCorpus []*http.Request
)

func cpdLoad(tb testing.TB) {
	tb.Helper()
	cpdOnce.Do(func() {
		filtersDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
		var datas [][]byte
		for _, u := range taxFilterURLs {
			h := fmt.Sprintf("%x", md5.Sum([]byte(u)))
			p := filepath.Join(filtersDir, h+".cache.txt")
			b, err := os.ReadFile(p)
			if err != nil {
				cpdErr = err
				return
			}
			datas = append(datas, b)
		}
		e := New()
		name := "candprobe"
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
		cpdEngine = e
		raw2, err := os.ReadFile(filepath.Join("..", "ruletree", "testdata", "urls.txt"))
		if err != nil {
			cpdErr = err
			return
		}
		sc := bufio.NewScanner(bytes.NewReader(raw2))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			u := strings.TrimSpace(sc.Text())
			if u == "" {
				continue
			}
			pu, err := url.Parse(u)
			if err != nil || pu.Host == "" {
				continue
			}
			req := &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
			req.Header.Set("Sec-Fetch-Site", "none")
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			req.Header.Set("Sec-Fetch-Dest", "document")
			req.Header.Set("Sec-Fetch-User", "?1")
			cpdCorpus = append(cpdCorpus, req)
		}
	})
	if cpdErr != nil {
		tb.Skipf("candprobe data unavailable: %v", cpdErr)
	}
}

func TestZZCandProbe(t *testing.T) {
	if os.Getenv("ZEN_CAND_PROBE") != "1" {
		t.Skip("ZEN_CAND_PROBE not set")
	}
	cpdLoad(t)
	nr := cpdEngine
	ps := nr.primaryStore
	var totalCand, navKept int64
	maxCand := 0
	buckets := map[string]int{"1-10": 0, "11-50": 0, "51-200": 0, "201-1000": 0, ">1000": 0}
	for _, req := range cpdCorpus {
		r2 := req.Clone(req.Context())
		reqURL := matchURL(r2.URL)
		storeRules := ps.Get(reqURL)
		// 树+generic+regexp 原始候选（未过 nav 过滤）
		cands, _ := nr.prependIndexedReq(storeRules, r2)
		totalCand += int64(len(cands))
		if len(cands) > maxCand {
			maxCand = len(cands)
		}
		switch {
		case len(cands) <= 10:
			buckets["1-10"]++
		case len(cands) <= 50:
			buckets["11-50"]++
		case len(cands) <= 200:
			buckets["51-200"]++
		case len(cands) <= 1000:
			buckets["201-1000"]++
		default:
			buckets[">1000"]++
		}
		isUserNav := r2.Header.Get("Sec-Fetch-User") == "?1" && r2.Header.Get("Sec-Fetch-Dest") == "document"
		kept := filterInPlace(cands, func(r *rule.Rule) bool {
			return r.ShouldMatchReqNav(r2, isUserNav)
		})
		navKept += int64(len(kept))
		ps.putRes(storeRules)
	}
	n := int64(len(cpdCorpus))
	t.Logf("URLs=%d 树候选均值=%.1f 中位见分桶 峰值=%d", n, float64(totalCand)/float64(n), maxCand)
	t.Logf("候选分桶: %v", buckets)
	t.Logf("ShouldMatchReqNav 淘汰率: 进=%d 留=%d (淘汰 %.2f%%)", totalCand, navKept, 100*(1-float64(navKept)/float64(totalCand)))
}
