package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// BenchmarkProxyForward 量测代理转发路径的端到端吞吐（plain HTTP：每请求都
// 走 FindByRequest 进程识别 → shouldProxy → 过滤器 → 转发），作为代理链路
// 效率优化的 A/B 基准（2026-10-07 效率审计立项）。
func BenchmarkProxyForward(b *testing.B) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer target.Close()

	p, err := NewProxy(noopFilter{}, unusedCertGenerator{}, 0, nil, "", nil)
	if err != nil {
		b.Fatalf("NewProxy: %v", err)
	}
	port, err := p.Start()
	if err != nil {
		b.Fatalf("start proxy: %v", err)
	}
	b.Cleanup(func() { _ = p.Stop() })

	proxyURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		b.Fatalf("parse proxy url: %v", err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			MaxIdleConnsPerHost: 64,
		},
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := client.Get(target.URL)
			if err != nil {
				b.Fatalf("forward: %v", err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}
