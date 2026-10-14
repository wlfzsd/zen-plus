package proxy

// 链式出站两腿的卫生门禁（2026-10-13 上游不稳期"视频全挂、重启才恢复"事故沉淀）：
//
//   1. TestH1LegFreshDialALPNAlignedToH1 —— h1 镜像腿新拨号的 ALPN 必须对齐
//      http/1.1（说话的腿），不得跟随入站客户端的 h2。此前 freshSpecFrom 读
//      connFacts.inboundH2：h2 入站浏览器令出站 hello 原样供给 [h2, http/1.1]，
//      对端按 ALPN 协商 h2 后收到的却是 h1 请求，回 h2 SETTINGS 帧 →
//      "malformed HTTP response" 502（application.log 实证 180 条，聚簇在
//      2026/10/11-12 上游不稳窗口）。本用例用"边缘骰子"源站复现生产时序：
//      首个请求命中 http/1.1 边缘（触发 alpnFallback → markH1Only → handoff
//      成功），源站关闭连接后第二个请求必须新拨号，命中 h2 边缘——
//      修复前该请求 502，修复后 200。
//   2. TestMimicH2TransportLivenessKnobs —— h2 腿必须打开 fhttp2 库自带的
//      ping 健康检查（ReadIdleTimeout>0；=0 时库完全不健康检查，
//      transport.go:2135）。每目标全部流多路复用在一条骑上游隧道的 h2 连接
//      上，上游不稳把隧道黑洞后，挂着卡死流的活跃连接 canTakeNewRequest 仍为
//      true（client_conn_pool.go:91），该主机全部新请求无限等待——重启前不解。

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	xhttp2 "golang.org/x/net/http2"
)

// diceOrigin 起一个"边缘骰子"TLS 靶：在它经手第一条 h1 请求（即 ALPN 回退
// 的 handoff 请求）之前，ALPN 一律应答 [http/1.1]（复现视频 CDN 的 h1 边缘，
// 触发 markH1Only）；之后新连接一律应答 [h2, http/1.1]（h2 能力边缘）。
// 协商出 h2 时先写一段真实的 h2 服务端 SETTINGS 帧——h1 客户端读它就是
// 生产日志里 "malformed HTTP response" 的原文形态。h1 路径回 200 并
// Connection: close，逼出下一次请求的新拨号。
func diceOrigin(t *testing.T) (hostPort string, served *atomic.Int32) {
	t.Helper()
	var servedCnt atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(r net.Conn) {
				defer r.Close()
				// 服务过请求后 = "换到了 h2 能力边缘"。
				next := []string{"http/1.1"}
				if servedCnt.Load() > 0 {
					next = []string{"h2", "http/1.1"}
				}
				tc := tls.Server(r, &tls.Config{ // #nosec G402 -- test origin
					Certificates: []tls.Certificate{mustCert(t)},
					NextProtos:   next,
				})
				if herr := tc.Handshake(); herr != nil {
					return
				}
				if tc.ConnectionState().NegotiatedProtocol == "h2" {
					// 真实 h2 服务端的第一帧；h1 客户端读它 = malformed。
					io.WriteString(tc, "\x00\x00\x12\x04\x00\x00\x00\x00\x00\x00\x03\x00\x00\x00\x80\x00\x04\x00\x01\x00\x00\x00")
					tc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
					io.Copy(io.Discard, tc) // 排空对端在写的请求，避免 RST 吞掉已写帧
					return
				}
				// h1：读请求头（+按 CL 读 body），回 200 并关闭逼新拨号。
				cl := 0
				head := make([]byte, 0, 4096)
				buf := make([]byte, 4096)
				for {
					n, rerr := tc.Read(buf)
					head = append(head, buf[:n]...)
					if i := strings.Index(string(head), "\r\n\r\n"); i >= 0 {
						for _, hln := range strings.Split(string(head[:i]), "\r\n") {
							if strings.HasPrefix(strings.ToLower(hln), "content-length:") {
								fmt.Sscanf(strings.SplitN(hln, ":", 2)[1], "%d", &cl)
							}
						}
						if len(head)-i-4 >= cl {
							break
						}
					}
					if rerr != nil {
						return
					}
				}
				servedCnt.Add(1)
				io.WriteString(tc, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
			}(c)
		}
	}()
	return strings.TrimPrefix(ln.Addr().String(), "127.0.0.1:"), &servedCnt
}

// h2InboundClient 模拟现代浏览器：经 CONNECT 进 Zen，隧道内以 h2 与 Zen 的
// MITM 握手（facts.inboundH2=true），请求全部走 h2 腿路由。
func h2InboundClient(t *testing.T, proxyAddr string) *xhttp2.Transport {
	t.Helper()
	return &xhttp2.Transport{
		DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
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

func TestH1LegFreshDialALPNAlignedToH1(t *testing.T) {
	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	t.Setenv(upstreamProxyEnv, upstreamAddr)

	hostPort, served := diceOrigin(t)

	proxyAddr := startTestProxy(t, func(p *Proxy) {
		p.certGenerator = selfSignedCertGenerator{}
		transportOf(t, p).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- test target
	})

	client := h2InboundClient(t, proxyAddr)

	// 请求一：命中 [http/1.1] 边缘 → h2 腿 alpnFallback → markH1Only →
	// handoff（h1 对齐拨号）成功。这是既有回退路径的基线。
	req1, err := http.NewRequest(http.MethodGet, "https://localhost:"+hostPort+"/first", nil)
	if err != nil {
		t.Fatalf("build req1: %v", err)
	}
	resp1, err := client.RoundTrip(req1)
	if err != nil {
		t.Fatalf("请求一（回退基线）必须成功: %v", err)
	}
	io.Copy(io.Discard, resp1.Body)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("请求一 status = %d, want 200", resp1.StatusCode)
	}
	if got := served.Load(); got != 1 {
		t.Fatalf("源站服务请求数 = %d, want 1（回退 handoff 未达源站）", got)
	}

	// 请求二/三：连接已被源站关闭 → h1 腿必须新拨号；此时源站在 [h2,
	// http/1.1] 边缘上。修复前：拨号 ALPN 跟随入站 h2 → 对端协商 h2 →
	// h1 请求读进 h2 SETTINGS → 502。修复后：ALPN 对齐 http/1.1 → 200。
	for i := 2; i <= 3; i++ {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://localhost:%s/fresh-%d", hostPort, i), nil)
		if err != nil {
			t.Fatalf("build req%d: %v", i, err)
		}
		resp, err := client.RoundTrip(req)
		if err != nil {
			t.Fatalf("请求%d（h1 腿新拨号）: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("请求%d status = %d body=%q, want 200（h1 腿新拨号 ALPN 必须对齐 http/1.1，不得让对端协商出 h2）", i, resp.StatusCode, string(body))
		}
		if string(body) != "ok" {
			t.Fatalf("请求%d body = %q, want \"ok\"", i, string(body))
		}
	}
}

func TestMimicH2TransportLivenessKnobs(t *testing.T) {
	m := newMimicRoundTripper(&Proxy{}, func() *tls.Config { return nil })
	facts := &connFacts{inboundH2: true}
	t2 := m.h2TransportFor("example.com:443", facts)
	if t2.ReadIdleTimeout <= 0 {
		t.Fatalf("h2 腿未启用活性检测: ReadIdleTimeout=%v（=0 时 fhttp2 完全不做健康检查，黑洞连接永久吞新请求）", t2.ReadIdleTimeout)
	}
	if t2.PingTimeout <= 0 {
		t.Fatalf("h2 腿未设置 PingTimeout=%v（PONG 等待无界）", t2.PingTimeout)
	}
	if again := m.h2TransportFor("example.com:443", facts); again != t2 {
		t.Fatal("同地址 transport 实例必须复用（池语义未保持）")
	}
}
