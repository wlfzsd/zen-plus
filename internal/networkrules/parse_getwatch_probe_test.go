package networkrules

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// 2026-10-08 并发复现：全量语料下单线程 ModifyRes 命中，线上并发下随机丢失。
// 假设：perf 1c8f7eb 的 store 池化（Get/putRes 共享切片）在并发下互踩。
func TestGetWatchJSONPruneConcurrent(t *testing.T) {
	dir := os.Getenv("ZEN_FILTER_DIR")
	if dir == "" {
		t.Skip("ZEN_FILTER_DIR not set")
	}
	bodyPath := os.Getenv("GET_WATCH_BODY")
	if bodyPath == "" {
		t.Skip("GET_WATCH_BODY not set")
	}
	rawBody, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	nr := New()
	myRule := `||youtube.com/youtubei/v1/get_watch$jsonprune=$..[adPlacements\, adSlots\, playerAds]`
	if _, err := nr.ParseRule(myRule, nil); err != nil {
		t.Fatalf("ParseRule(myRule): %v", err)
	}
	count := 0
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".cache.txt") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") {
				continue
			}
			if _, err := nr.ParseRule(line, nil); err == nil {
				count++
			}
		}
		f.Close()
	}
	nr.Compact()
	t.Logf("corpus loaded: %d", count)

	// 并发轰炸：一半打 get_watch，一半打 player/其他 URL 制造真实竞争
	const goroutines = 32
	const iterations = 25
	var hit, miss int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				var reqURL string
				if g%2 == 0 {
					reqURL = "https://www.youtube.com/youtubei/v1/get_watch?prettyPrint=false"
				} else {
					reqURL = "https://www.youtube.com/youtubei/v1/player?prettyPrint=false"
				}
				req, _ := http.NewRequest("POST", reqURL, nil)
				res := &http.Response{
					StatusCode: 200,
					Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
					Body:       io.NopCloser(bytes.NewReader(rawBody)),
				}
				applied, err := nr.ModifyRes(req, res)
				if err != nil {
					t.Errorf("ModifyRes error: %v", err)
					return
				}
				if g%2 == 0 {
					found := false
					for _, r := range applied {
						if strings.Contains(r.RawRule, "get_watch") {
							found = true
							break
						}
					}
					if found {
						atomic.AddInt64(&hit, 1)
					} else {
						atomic.AddInt64(&miss, 1)
					}
				}
				// 消耗 body 防止优化
				_, _ = io.Copy(io.Discard, res.Body)
			}
		}(g)
	}
	wg.Wait()
	hitN, missN := atomic.LoadInt64(&hit), atomic.LoadInt64(&miss)
	t.Logf("get_watch 并发命中=%d 丢失=%d（共 %d）", hitN, missN, hitN+missN)
	if missN > 0 {
		t.Errorf("RACE REPRODUCED: %d/%d 次规则丢失", missN, hitN+missN)
	}
	_ = fmt.Sprint()
}
