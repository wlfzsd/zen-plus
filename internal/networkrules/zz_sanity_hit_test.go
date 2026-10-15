package networkrules

// [冷份额 sanity 探针] 前 10 个语料 URL：候选数、ShouldMatchReqNav 命中数、
// 未命中候选的规则原文样本（看哪一环断了）。
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
)

var (
	snOnce   sync.Once
	snErr    error
	snEngine *NetworkRules
	snCorpus []string
)

func snLoad(tb testing.TB) {
	tb.Helper()
	snOnce.Do(func() {
		filtersDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
		var datas [][]byte
		for _, u := range taxFilterURLs {
			h := fmt.Sprintf("%x", md5.Sum([]byte(u)))
			p := filepath.Join(filtersDir, h+".cache.txt")
			b, err := os.ReadFile(p)
			if err != nil {
				snErr = err
				return
			}
			datas = append(datas, b)
		}
		e := New()
		name := "sanity"
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
		snEngine = e
		raw2, err := os.ReadFile(filepath.Join("..", "ruletree", "testdata", "urls.txt"))
		if err != nil {
			snErr = err
			return
		}
		sc := bufio.NewScanner(bytes.NewReader(raw2))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			u := strings.TrimSpace(sc.Text())
			if u != "" {
				snCorpus = append(snCorpus, u)
			}
		}
	})
	if snErr != nil {
		tb.Skipf("sanity data unavailable: %v", snErr)
	}
}

func TestZZSanityHit(t *testing.T) {
	if os.Getenv("ZEN_SANITY_HIT") != "1" {
		t.Skip("ZEN_SANITY_HIT not set")
	}
	snLoad(t)
	nr := snEngine
	ps := nr.primaryStore
	for k := 0; k < 10 && k < len(snCorpus); k++ {
		u := snCorpus[k]
		pu, err := url.Parse(u)
		if err != nil || pu.Host == "" {
			continue
		}
		req := &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
		req.Header.Set("Sec-Fetch-Site", "none")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-User", "?1")
		reqURL := matchURL(req.URL)
		storeRules := ps.Get(reqURL)
		isUserNav := true
		cand, hit := 0, 0
		var missSample []string
		for _, r := range storeRules {
			cand++
			if r.ShouldMatchReqNav(req, isUserNav) {
				hit++
			} else if len(missSample) < 3 {
				missSample = append(missSample, r.RawRule)
			}
		}
		t.Logf("URL[%d]=%.70s 候选=%d 命中=%d 未命中样本=%v", k, u, cand, hit, missSample)
		ps.putRes(storeRules)
	}
}
