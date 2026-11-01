package proxy

// 复现「池内 h2 连接被对端静默丢弃后，新请求长时间无响应头」的现场形态
// [2026-11-01 HLS 分片"等待中"排查]。对端在一条已建立的 h2 连接上不再回应
// 后续流请求（不回 HEADERS、不回 RST、不断 TCP），正是边缘/NAT 静默丢状态的
// 形态：连接层探活看不出来，Zen 只能等 chainedH2TTFBTimeout 触发后改走 h1
// 镜像腿。本测试量出这段等待的真实长度，并在修复后回测其收敛值。

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	xhttp2 "golang.org/x/net/http2"
)

// startStaleH2Origin 起一个"第一条流正常、后续流静默不答"的 h2 源站。
// 每条连接独立计数：新连接照常服务（h2 与 h1 都服务），只有同一条 h2 连接上
// 的第二个及以后的请求会被挂起不回——模拟该连接在边缘侧已经失效。
func startStaleH2Origin(t *testing.T) (addr string, served *atomic.Int64) {
	t.Helper()
	served = &atomic.Int64{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("origin listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	tlsCfg := &tls.Config{ // #nosec G402 -- test origin
		Certificates: []tls.Certificate{mustCert(t)},
		NextProtos:   []string{"h2", "http/1.1"},
	}
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
					// h1 腿（Zen 的 TTFB 兜底会走到这里）：照常服务
					srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						served.Add(1)
						w.WriteHeader(http.StatusOK)
						io.WriteString(w, "ok")
					})}
					_ = srv.Serve(newSingleConnListener(tlsConn))
					return
				}
				var onConn atomic.Int64
				h2s.ServeConn(tlsConn, &xhttp2.ServeConnOpts{
					Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if onConn.Add(1) > 1 {
							// 静默黑洞：收下流、不回头部、不重置、不断连
							<-r.Context().Done()
							return
						}
						served.Add(1)
						w.WriteHeader(http.StatusOK)
						io.WriteString(w, "ok")
					}),
				})
			}(raw)
		}
	}()

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = host
	// 用主机名而不是 IP 字面量：Zen 对 IP 字面量目标只走透明隧道（proxy.go:450），
	// 不会 MITM，也就碰不到 h2 镜像腿。
	return net.JoinHostPort("localhost", port), served
}

// newH2ClientThroughProxy returns an HTTP/2 client whose connection is a
// CONNECT tunnel to the proxy, then TLS to the (MITM'd) origin - the same
// shape a browser produces.
func newH2ClientThroughProxy(t *testing.T, proxyAddr string) *xhttp2.Transport {
	t.Helper()
	return &xhttp2.Transport{
		DialTLS: func(_ string, addr string, _ *tls.Config) (net.Conn, error) {
			tunneled := dialViaProxyConnect(t, proxyAddr, addr)
			tlsConn := tls.Client(tunneled, &tls.Config{ // #nosec G402 -- MITM cert is self-signed on purpose
				ServerName:         hostOf(addr),
				InsecureSkipVerify: true,
				NextProtos:         []string{"h2"},
			})
			if err := tlsConn.Handshake(); err != nil {
				return nil, err
			}
			return tlsConn, nil
		},
	}
}

// TestH2StaleConnStall measures how long a request waits when the pooled h2
// connection to a target has been silently dropped by the peer.
//
// 修复前：第二条请求要等满 chainedH2TTFBTimeout（30s）才由 h1 兜底完成。
// 修复后：应在秒级内发现连接已死并改用新连接。
func TestH2StaleConnStall(t *testing.T) {
	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	t.Setenv(upstreamProxyEnv, upstreamAddr)

	originAddr, served := startStaleH2Origin(t)
	proxyAddr := startTestProxy(t, func(p *Proxy) {
		p.certGenerator = selfSignedCertGenerator{}
		transportOf(t, p).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- test target, trust is not under test
	})

	tr := newH2ClientThroughProxy(t, proxyAddr)
	defer tr.CloseIdleConnections()

	get := func(path string) (int, time.Duration, error) {
		req, err := http.NewRequest(http.MethodGet, "https://"+originAddr+path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("User-Agent", "stale-probe/1")
		start := time.Now()
		resp, rerr := tr.RoundTrip(req)
		if rerr != nil {
			return 0, time.Since(start), rerr
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, time.Since(start), nil
	}

	// 第一条：新鲜连接，正常
	code1, d1, err1 := get("/first")
	if err1 != nil || code1 != http.StatusOK {
		t.Fatalf("first request: code=%d err=%v", code1, err1)
	}
	t.Logf("第一条（新鲜连接）: %d，耗时 %v，源站已服务 %d 次", code1, d1.Round(time.Millisecond), served.Load())

	// 第二条：复用同一条 h2 连接 —— 对端静默不答
	code2, d2, err2 := get("/second")
	if err2 != nil {
		t.Logf("第二条返回错误（%v），耗时 %v", err2, d2.Round(time.Millisecond))
	} else {
		t.Logf("第二条（复用已死连接）: %d，耗时 %v", code2, d2.Round(time.Millisecond))
	}

	if code2 != http.StatusOK || err2 != nil {
		t.Fatalf("第二条最终应被 h1 兜底完成: code=%d err=%v", code2, err2)
	}
	// 回归判据 [2026-11-01]：池内连接被对端静默丢弃时，用户最多等
	// chainedH2TTFBTimeout 才轮到 h1 兜底。修复前常量 30s（实测 30.007s），
	// 收紧后判据锁在 12s——留 2s 余量，同时挡住任何把窗口改回两位数的回归。
	if d2 > 12*time.Second {
		t.Fatalf("第二条耗时 %v，超过 12s 判据：静默丢连接的等待窗口回归了", d2.Round(time.Millisecond))
	}
	if d1 > 2*time.Second {
		t.Fatalf("第一条（健康路径）耗时 %v，超过 2s：镜像腿本身变慢了", d1.Round(time.Millisecond))
	}
	t.Logf("STALE-STALL: first=%v second=%v", d1.Round(time.Millisecond), d2.Round(time.Millisecond))
	_ = fmt.Sprintf("%v", d2)
}
