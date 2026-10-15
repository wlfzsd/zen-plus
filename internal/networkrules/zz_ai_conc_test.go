package networkrules

// [2026-10-14 WS1 并发压力探针] 本机无 gcc → -race 不可用（CGO 缺失）。
// 替代：高并发混合压测——读路径（ModifyReq/ModifyRes）+ 运行时写路径
// （liveExc.insert，白名单服务器语义），抓 panic 与结果漂移。
// env: ZEN_AI_CONC=1
import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"testing"
)

func TestZZAIConcStress(t *testing.T) {
	if os.Getenv("ZEN_AI_CONC") != "1" {
		t.Skip("ZEN_AI_CONC not set")
	}
	aiLoad(t)
	e := New(WithDomainGate(true)) // 生产默认形态
	hmLoadLists(t, e)

	reqOf := func(u string) *http.Request {
		pu, err := url.Parse(u)
		if err != nil || pu.Host == "" {
			return nil
		}
		return &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
	}
	// 基准答案：单线程算一遍
	type ans struct {
		b bool
		r string
		n int
	}
	gold := make([]ans, len(aiCorpus))
	for i, u := range aiCorpus {
		if r := reqOf(u); r != nil {
			ap, b, red := e.ModifyReq(r)
			gold[i] = ans{b, red, len(ap)}
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan string, 64)
	const W = 32
	for w := 0; w < W; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k, u := range aiCorpus {
				r := reqOf(u)
				if r == nil {
					continue
				}
				ap, b, red := e.ModifyReq(r)
				g := gold[k]
				if b != g.b || red != g.r || len(ap) != g.n {
					select {
					case errCh <- u:
					default:
					}
					return
				}
				// 混入响应路径读（同协程交错）
				if k%97 == id%97 {
					res := &http.Response{StatusCode: 200, Proto: "HTTP/1.1", ProtoMajor: 1,
						Header: http.Header{}, Body: http.NoBody, Request: r}
					_, _ = e.ModifyRes(r, res)
				}
				// 运行时插入（白名单服务器语义）：liveExc 并发写与读交错——
				// 专打 WS1 触碰的并发面（exceptionStore/liveExc 的 Get 路径）
				if k%503 == id%503 {
					name := "conc-ins"
					_, _ = e.ParseRule("@@||stress-"+string(rune('a'+id%26))+fmt.Sprintf("%d", k)+".example^", &name)
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	n := 0
	for e := range errCh {
		n++
		if n <= 3 {
			t.Errorf("并发结果漂移: %s", e)
		}
	}
	t.Logf("CONC-STRESS: workers=%d corpus=%d 漂移=%d（含 liveExc 运行时插入交错）", W, len(aiCorpus), n)
	if n != 0 {
		t.Fatalf("并发压力发现不一致")
	}
}
