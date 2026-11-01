package proxy

// 诊断用：模拟"拖动进度条 = 并发取流后立刻取消"的取消风暴，观察同一目标随后
// 的正常请求是否被拖慢/挂住。这是用户报障的触发动作（seek 后新分片长时间
// "等待中"）在进程内的最小复现形状。

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xhttp2 "golang.org/x/net/http2"
)

// startBigH2Origin 起一个正常应答的 h2 源站：/big 回 4MB（可被中途取消），
// /small 回 "ok"。
func startBigH2Origin(t *testing.T) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("origin listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	tlsCfg := &tls.Config{ // #nosec G402 -- test origin
		Certificates: []tls.Certificate{mustCert(t)},
		NextProtos:   []string{"h2", "http/1.1"},
	}
	big := strings.Repeat("x", 1<<20) // 1MB per write chunk
	var h2s xhttp2.Server
	go func() {
		for {
			raw, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(raw net.Conn) {
				defer raw.Close()
				tlsConn := tls.Server(raw, tlsCfg)
				if herr := tlsConn.Handshake(); herr != nil {
					return
				}
				if tlsConn.ConnectionState().NegotiatedProtocol != "h2" {
					srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/big" {
							for i := 0; i < 4; i++ {
								if _, err := io.WriteString(w, big); err != nil {
									return
								}
							}
							return
						}
						io.WriteString(w, "ok")
					})}
					_ = srv.Serve(newSingleConnListener(tlsConn))
					return
				}
				h2s.ServeConn(tlsConn, &xhttp2.ServeConnOpts{
					Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/big" {
							for i := 0; i < 4; i++ {
								if _, err := io.WriteString(w, big); err != nil {
									return
								}
							}
							return
						}
						w.WriteHeader(http.StatusOK)
						io.WriteString(w, "ok")
					}),
				})
			}(raw)
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = host
	return net.JoinHostPort("localhost", port)
}

// TestH2CancelStormThenRequest 用取消风暴复现"seek 之后新请求卡住"。
func TestH2CancelStormThenRequest(t *testing.T) {
	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	t.Setenv(upstreamProxyEnv, upstreamAddr)

	originAddr := startBigH2Origin(t)
	proxyAddr := startTestProxy(t, func(p *Proxy) {
		p.certGenerator = selfSignedCertGenerator{}
		transportOf(t, p).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- test target
	})

	tr := newH2ClientThroughProxy(t, proxyAddr)
	defer tr.CloseIdleConnections()

	do := func(ctx context.Context, path string, readAll bool) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+originAddr+path, nil)
		if err != nil {
			return 0, err
		}
		resp, rerr := tr.RoundTrip(req)
		if rerr != nil {
			return 0, rerr
		}
		defer resp.Body.Close()
		if readAll {
			_, _ = io.Copy(io.Discard, resp.Body)
		}
		return resp.StatusCode, nil
	}

	// 预热：一条正常请求
	if code, err := do(context.Background(), "/small", true); err != nil || code != 200 {
		t.Fatalf("预热失败: code=%d err=%v", code, err)
	}

	// 取消风暴：4 轮 × 6 并发，取 /big 后 200ms 取消
	var wg sync.WaitGroup
	var canceled atomic.Int64
	for round := 0; round < 4; round++ {
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				defer cancel()
				_, _ = do(ctx, "/big", false)
				canceled.Add(1)
			}()
		}
		wg.Wait()
	}
	t.Logf("取消风暴完成：%d 条，耗时结束", canceled.Load())

	// 风暴之后的正常请求
	start := time.Now()
	code, err := do(context.Background(), "/small", true)
	d := time.Since(start)
	if err != nil || code != 200 {
		t.Fatalf("风暴后请求失败: code=%d err=%v 耗时=%v", code, err, d.Round(time.Millisecond))
	}
	t.Logf("风暴后正常请求: code=%d 耗时=%v", code, d.Round(time.Millisecond))
	if d > 3*time.Second {
		t.Fatalf("风暴后请求被拖慢：%v（应 < 3s）", d.Round(time.Millisecond))
	}
}
