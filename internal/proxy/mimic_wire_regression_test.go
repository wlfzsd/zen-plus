package proxy

// mimic 出站线材回归门禁（2026-10-07 审计沉淀，规则 B8：漏检必须固化为永久门禁）：
//
//   1. TestALPNFallbackAlertSingular —— 源站对零交集 ALPN 供给回 alert 120 时，
//      utls 客户端的错误文案是单数 "no application protocol"（refraction utls
//      alert.go:97），回退判定必须认得它；只认 Go 服务端视角的复数文案
//      （handshake_server.go:362 "application protocols"）会让 mimic 腿 502 且
//      把 host 误关进透明名单。
//   2. TestALPNFallbackH1Wire —— 回退 h1 腿（h2 入站 → 源站 h1-only）的线材门禁：
//      请求行必须 HTTP/1.1（ProtoMajor=2 不得泄漏）、Content-Length 恰好一个
//      （双 CL 会被严格源站按走私防护 400）、body 完整。
//   3. TestMimicH1PostWireSingleCL —— 主线 h1 镜像腿（h1 入站 POST，2026-10-07
//      双 CL 事故的原生场景）的线材门禁：单 CL、body 完整。双 CL 事故的原始
//      探针跑完即删，此用例把该缺陷永久钉死。
//
// 全部用例驱动真实客户端经完整 MITM+镜像链路，非单测桩。

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	xhttp2 "golang.org/x/net/http2"
)

// rawH1EchoOrigin 起一个 h1-only TLS 靶：不解析，直接记录解密后的原始线材字节，
// 回复 200。解析器会把要抓的伪影（重复 CL 等）折叠掉，raw 才见真相。
func rawH1EchoOrigin(t *testing.T) (hostPort string, mu *sync.Mutex, raw *string) {
	mu = &sync.Mutex{}
	s := ""
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	tlsCfg := &tls.Config{ // #nosec G402 -- test origin
		Certificates: []tls.Certificate{mustCert(t)},
		NextProtos:   []string{"http/1.1"},
	}
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(r net.Conn) {
				defer r.Close()
				tc := tls.Server(r, tlsCfg)
				if herr := tc.Handshake(); herr != nil {
					return
				}
				var all []byte
				buf := make([]byte, 4096)
				for {
					n, rerr := tc.Read(buf)
					if n > 0 {
						all = append(all, buf[:n]...)
					}
					if rerr != nil {
						break
					}
					if i := strings.Index(string(all), "\r\n\r\n"); i >= 0 {
						head := string(all[:i])
						cl := 0
						for _, hln := range strings.Split(head, "\r\n") {
							if strings.HasPrefix(strings.ToLower(hln), "content-length:") {
								fmt.Sscanf(strings.SplitN(hln, ":", 2)[1], "%d", &cl)
							}
						}
						if len(all)-i-4 >= cl {
							break
						}
					}
				}
				mu.Lock()
				s = string(all)
				mu.Unlock()
				io.WriteString(tc, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
			}(c)
		}
	}()
	return strings.TrimPrefix(ln.Addr().String(), "127.0.0.1:"), mu, &s
}

func TestALPNFallbackAlertSingular(t *testing.T) {
	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	t.Setenv(upstreamProxyEnv, upstreamAddr)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "alert-fallback ok")
	}))
	server.TLS = &tls.Config{ // #nosec G402 -- test origin
		Certificates: []tls.Certificate{mustCert(t)},
		NextProtos:   []string{"http/1.1"},
	}
	server.StartTLS()
	defer server.Close()

	proxy := startTestProxy(t, func(p *Proxy) {
		p.certGenerator = selfSignedCertGenerator{}
		transportOf(t, p).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- test target
	})

	tr := &xhttp2.Transport{
		DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
			tunneled := dialViaProxyConnect(t, proxy, addr)
			tlsConn := tls.Client(tunneled, &tls.Config{ // #nosec G402 -- MITM cert is self-signed on purpose
				ServerName:         hostOf(addr),
				InsecureSkipVerify: true,
				NextProtos:         []string{"h2"}, // h2-only 供给：与源站 [http/1.1] 零交集 → alert 120
			})
			if err := tlsConn.Handshake(); err != nil {
				return nil, err
			}
			return tlsConn, nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://localhost:%d/probe", portOf(t, server)), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("告警型零交集未回退（utls 单数文案没被认出）: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "alert-fallback ok" {
		t.Fatalf("status/body = %d/%q, want 200/alert-fallback ok", resp.StatusCode, body)
	}
}

func TestALPNFallbackH1Wire(t *testing.T) {
	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	t.Setenv(upstreamProxyEnv, upstreamAddr)

	hostPort, mu, raw := rawH1EchoOrigin(t)

	proxy := startTestProxy(t, func(p *Proxy) {
		p.certGenerator = selfSignedCertGenerator{}
		transportOf(t, p).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- test target
	})

	tr := &xhttp2.Transport{
		DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
			tunneled := dialViaProxyConnect(t, proxy, addr)
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
	body := strings.Repeat("y", 2048)
	req, err := http.NewRequest(http.MethodPost, "https://localhost:"+hostPort+"/submit", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip (fallback must recover): %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()

	lower := strings.ToLower(*raw)
	if clCount := strings.Count(lower, "content-length:"); clCount != 1 {
		t.Fatalf("回退腿线材 CL 头数量=%d, want 1（双 CL 会被严格源站 400）:\n%s", clCount, *raw)
	}
	if strings.Contains(*raw, "HTTP/2.0") {
		t.Fatalf("回退腿请求行泄漏 HTTP/2.0:\n%s", *raw)
	}
	if !strings.Contains(*raw, "HTTP/1.1") {
		t.Fatalf("回退腿请求行不是 HTTP/1.1:\n%s", *raw)
	}
	if !strings.Contains(*raw, body) {
		t.Fatalf("回退腿 body 不完整:\n%s", *raw)
	}
}

// TestMimicH1PostWireSingleCL 钉死 2026-10-07 双 CL 事故的主线场景：
// h1 入站 POST（Go server 会把 CL 同时留在 Header 与字段）经 h1 镜像腿时，
// 线材上 Content-Length 必须恰好出现一次。
func TestMimicH1PostWireSingleCL(t *testing.T) {
	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	t.Setenv(upstreamProxyEnv, upstreamAddr)

	hostPort, mu, raw := rawH1EchoOrigin(t)

	proxy := startTestProxy(t, func(p *Proxy) {
		p.certGenerator = selfSignedCertGenerator{}
		transportOf(t, p).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- test target
	})

	// h1 入站：显式 CONNECT + TLS + 手写请求（带显式 CL，与浏览器/.NET 同型）
	tun := dialViaProxyConnect(t, proxy, "localhost:"+hostPort)
	tlsConn := tls.Client(tun, &tls.Config{ // #nosec G402 -- MITM cert is self-signed on purpose
		ServerName:         "localhost",
		InsecureSkipVerify: true,
		NextProtos:         []string{"http/1.1"},
	})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("inbound TLS: %v", err)
	}
	body := strings.Repeat("x", 1024)
	hreq := "POST /submit HTTP/1.1\r\n" +
		"Host: localhost:" + hostPort + "\r\n" +
		"Content-Length: " + fmt.Sprint(len(body)) + "\r\n" +
		"Content-Type: application/json\r\n" +
		"\r\n" + body
	if _, err := tlsConn.Write([]byte(hreq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(tlsConn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read resp: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()

	lower := strings.ToLower(*raw)
	if clCount := strings.Count(lower, "content-length:"); clCount != 1 {
		t.Fatalf("主线 h1 腿线材 CL 头数量=%d, want 1（双 CL = POST 400 根因复发）:\n%s", clCount, *raw)
	}
	if !strings.Contains(*raw, body) {
		t.Fatalf("主线 h1 腿 body 不完整:\n%s", *raw)
	}
}
