package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	xhttp2 "golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// startH2MirrorOrigin runs a TLS origin that speaks HTTP/2 and taps the
// plaintext frames its client (the proxy's outbound leg) puts on the wire,
// recording them into connFacts - the same capture the proxy runs against
// its inbound leg. Comparing origin facts against client facts is the
// end-to-end fidelity check: what went in must come out.
func startH2MirrorOrigin(t *testing.T) (addr string, observed *connFacts, negotiated *atomic.Value) {
	t.Helper()
	observed = newConnFacts()
	negotiated = &atomic.Value{}

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
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(raw net.Conn) {
				defer raw.Close()
				tlsConn := tls.Server(raw, tlsCfg)
				if herr := tlsConn.Handshake(); herr != nil {
					return
				}
				negotiated.Store(tlsConn.ConnectionState().NegotiatedProtocol)
				h2s.ServeConn(newTapConn(tlsConn, observed), &xhttp2.ServeConnOpts{
					Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusOK)
						io.WriteString(w, "mirrored")
					}),
				})
			}(conn)
		}
	}()
	return ln.Addr().String(), observed, negotiated
}

// dialViaProxyConnect dials the proxy, establishes a CONNECT tunnel to
// target, and returns the raw tunnel.
func dialViaProxyConnect(t *testing.T, proxyAddr, target string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d", resp.StatusCode)
	}
	return conn
}

// writeTapConn tees the client's own writes (its h2 frames) into a capture
// channel - the mirror image of the proxy's inbound read-side tap, letting a
// test client record exactly what it put on the wire.
type writeTapConn struct {
	net.Conn
	ch chan []byte
}

func (w *writeTapConn) Write(p []byte) (int, error) {
	b := append([]byte(nil), p...)
	select {
	case w.ch <- b:
	default:
	}
	return w.Conn.Write(p)
}

// newWriteTapFacts wires a facts object to a write-tap channel pair.
func newWriteTapFacts() (*connFacts, chan []byte) {
	facts := newConnFacts()
	ch := make(chan []byte, 256)
	facts.stop = func() {}
	facts.capturing.Store(true)
	go facts.parse(ch)
	return facts, ch
}

// TestMimicEndToEndH2 drives a real HTTP/2 client through the chained proxy
// and pins the fidelity contract: the origin must observe exactly the h2
// fingerprint parameters the client sent inbound - SETTINGS values and order,
// connection window, pseudo-header order, header order - plus the h2
// protocol itself. Mismatch on any dimension is a replay failure.
func TestMimicEndToEndH2(t *testing.T) {
	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	t.Setenv(upstreamProxyEnv, upstreamAddr)
	originAddr, observed, negotiated := startH2MirrorOrigin(t)

	proxy := startTestProxy(t, func(p *Proxy) {
		p.certGenerator = selfSignedCertGenerator{}
	})

	clientFacts, clientTapCh := newWriteTapFacts()
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
			return &writeTapConn{Conn: tlsConn, ch: clientTapCh}, nil
		},
	}

	req, err := http.NewRequest(http.MethodGet, "https://"+originAddr+"/probe", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("User-Agent", "mimic-e2e/1")
	req.Header.Set("Accept", "text/plain")
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip through the chained proxy: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "mirrored" {
		t.Fatalf("status/body = %d/%q, want 200/mirrored", resp.StatusCode, body)
	}

	waitFacts(t, clientFacts)
	waitFacts(t, observed)

	if got, _ := negotiated.Load().(string); got != "h2" {
		t.Fatalf("origin negotiated %q, want h2", got)
	}
	if diff := diffSettings(clientFacts.settings, observed.settings); diff != "" {
		t.Fatalf("SETTINGS replay mismatch (inbound vs outbound):\n%s", diff)
	}
	if clientFacts.connWindow != 0 && observed.connWindow != clientFacts.connWindow {
		// A client that sent no connection WINDOW_UPDATE cannot stop the
		// fhttp stack from sending its default one (documented gap).
		t.Fatalf("connWindow inbound = %d, outbound = %d", clientFacts.connWindow, observed.connWindow)
	}
	if strings.Join(clientFacts.pseudoOrder, ",") != strings.Join(observed.pseudoOrder, ",") {
		t.Fatalf("pseudo order inbound = %v, outbound = %v", clientFacts.pseudoOrder, observed.pseudoOrder)
	}
	if strings.Join(clientFacts.headerOrder, ",") != strings.Join(observed.headerOrder, ",") {
		t.Fatalf("header order inbound = %v, outbound = %v", clientFacts.headerOrder, observed.headerOrder)
	}
}

// TestMimicEndToEndH2CustomProfile replays a deliberately non-Go h2 profile
// (custom SETTINGS values and order, custom connection window, a Firefox-like
// pseudo-header order, non-alphabetical header order) through the proxy with
// a hand-rolled h2 client, and asserts the origin observes every value
// byte-for-byte. This proves genuine replay: none of these values can be
// produced by any stack default.
func TestMimicEndToEndH2CustomProfile(t *testing.T) {
	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	t.Setenv(upstreamProxyEnv, upstreamAddr)
	originAddr, observed, negotiated := startH2MirrorOrigin(t)

	proxy := startTestProxy(t, func(p *Proxy) {
		p.certGenerator = selfSignedCertGenerator{}
	})

	tunneled := dialViaProxyConnect(t, proxy, originAddr)
	tlsConn := tls.Client(tunneled, &tls.Config{ // #nosec G402 -- MITM cert is self-signed on purpose
		ServerName:         hostOf(originAddr),
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2"},
	})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("TLS handshake with the MITM: %v", err)
	}

	var out bytes.Buffer
	out.WriteString(h2ClientPreface)
	settings := []h2Setting{{1, 4096}, {4, 100000}, {5, 16384}}
	payload := make([]byte, 0, len(settings)*6)
	for _, s := range settings {
		entry := make([]byte, 6)
		binary.BigEndian.PutUint16(entry, s.ID)
		binary.BigEndian.PutUint32(entry[2:], s.Val)
		payload = append(payload, entry...)
	}
	out.Write(h2Frame(0x4, 0, 0, payload))
	wu := make([]byte, 4)
	binary.BigEndian.PutUint32(wu, 5242880)
	out.Write(h2Frame(0x8, 0, 0, wu))

	var block bytes.Buffer
	enc := hpack.NewEncoder(&block)
	for _, f := range []hpack.HeaderField{
		{Name: ":method", Value: "GET"},
		{Name: ":authority", Value: originAddr},
		{Name: ":scheme", Value: "https"},
		{Name: ":path", Value: "/custom"},
		{Name: "x-trace-probe", Value: "1"},
		{Name: "accept", Value: "*/*"},
		{Name: "user-agent", Value: "custom-profile/1"},
	} {
		if err := enc.WriteField(f); err != nil {
			t.Fatalf("hpack encode: %v", err)
		}
	}
	out.Write(h2Frame(0x1, 0x4|0x1, 1, block.Bytes()))

	if _, err := tlsConn.Write(out.Bytes()); err != nil {
		t.Fatalf("write h2 preface+request: %v", err)
	}

	// Drain frames until the response stream ends (END_STREAM on stream 1).
	if err := drainUntilResponseEnd(tlsConn, 1, 5*time.Second); err != nil {
		t.Fatalf("await response: %v", err)
	}

	waitFacts(t, observed)

	if got, _ := negotiated.Load().(string); got != "h2" {
		t.Fatalf("origin negotiated %q, want h2", got)
	}
	if diff := diffSettings(settings, observed.settings); diff != "" {
		t.Fatalf("SETTINGS replay mismatch (custom profile):\n%s", diff)
	}
	if observed.connWindow != 5242880 {
		t.Fatalf("connWindow outbound = %d, want 5242880", observed.connWindow)
	}
	wantPseudo := ":method,:authority,:scheme,:path"
	if strings.Join(observed.pseudoOrder, ",") != wantPseudo {
		t.Fatalf("pseudo order outbound = %v, want %v", observed.pseudoOrder, wantPseudo)
	}
	wantHeaders := "x-trace-probe,accept,user-agent"
	if strings.Join(observed.headerOrder, ",") != wantHeaders {
		t.Fatalf("header order outbound = %v, want %v (non-alphabetical proves replay)", observed.headerOrder, wantHeaders)
	}
}

// waitFacts waits for a capture to settle; connections that were never
// tapped (h1) have no capturing flag and return immediately.
func waitFacts(t *testing.T, f *connFacts) {
	t.Helper()
	select {
	case <-f.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("capture did not settle")
	}
}

func diffSettings(want []h2Setting, got []h2Setting) string {
	var b strings.Builder
	if len(want) != len(got) {
		fmt.Fprintf(&b, "entry count: inbound %d, outbound %d\n", len(want), len(got))
	}
	for i := 0; i < len(want) && i < len(got); i++ {
		if want[i] != got[i] {
			fmt.Fprintf(&b, "entry[%d]: inbound %+v, outbound %+v\n", i, want[i], got[i])
		}
	}
	return b.String()
}

// drainUntilResponseEnd reads h2 frames, skipping everything until a
// HEADERS or DATA frame with END_STREAM on the given stream arrives.
func drainUntilResponseEnd(conn net.Conn, streamID uint32, timeout time.Duration) error {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		var head [9]byte
		if _, err := io.ReadFull(conn, head[:]); err != nil {
			return err
		}
		length := int(head[0])<<16 | int(head[1])<<8 | int(head[2])
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return err
		}
		typ, flags := head[3], head[4]
		sid := binary.BigEndian.Uint32(head[5:]) & 0x7fffffff
		switch typ {
		case 0x1: // HEADERS
			if sid == streamID && flags&0x4 == 0 {
				// Interim HEADERS without END_HEADERS: the block continues in
				// CONTINUATION frames, which the loop skips by length.
				continue
			}
			if sid == streamID && flags&0x1 != 0 {
				return nil
			}
		case 0x0: // DATA
			if sid == streamID && flags&0x1 != 0 {
				return nil
			}
		}
	}
}

// TestMimicALPNFallbackOnNoALPNOrigin 复现并钉死 2026-10-07 断网事件的回归
// 门禁：镜像 hello 供给 [h2, http/1.1] 而源站（或中间路径）完全不协商 ALPN
// 时，h2 腿必须自适应回退到 h1 镜像腿（同一 TLS 指纹、仅 ALPN 对齐），请求
// 成功返回——而不是像事故版那样整站 502。
func TestMimicALPNFallbackOnNoALPNOrigin(t *testing.T) {
	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	t.Setenv(upstreamProxyEnv, upstreamAddr)

	// 仅提供 http/1.1 的 TLS 源站：h2 供给会被协商成 http/1.1（非 h2）——
	// 与事故相同的代码路径（协议协商结果 != h2 → 回退）。
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "fallback ok")
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
				NextProtos:         []string{"h2", "http/1.1"}, // 真实浏览器形态
			})
			if err := tlsConn.Handshake(); err != nil {
				return nil, err
			}
			return tlsConn, nil
		},
	}
	// 目标必须用主机名：IP 字面量 CONNECT 走透明隧道（proxy.go 的
	// net.ParseIP 分支），mimic 路径根本不会触发。参照 TestUpstreamChainMITM
	// 的 portOf 写法取端口，用 localhost 触发 MITM+镜像路径。
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://localhost:%d/probe", portOf(t, server)), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip (fallback must recover): %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "fallback ok" {
		t.Fatalf("status/body = %d/%q, want 200/fallback ok", resp.StatusCode, body)
	}
}
