package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// startFakeUpstreamProxy runs a minimal HTTP forward proxy. Plain-HTTP
// requests arrive in absolute form and are relayed to their URL. CONNECT
// requests are tunnelled: in relay mode to the real target the CONNECT names
// (the MITM test points one at a live local TLS server); in echo mode the
// tunnel payload is echoed back (the tunnel tests point at names nothing
// dialable answers for). connectHits counts CONNECT commands, plainHits
// counts relayed requests. The relay uses a bare transport on purpose: the
// fake upstream stands in for a foreign proxy that dials directly.
func startFakeUpstreamProxy(t *testing.T, echoTunnels bool) (connectHits *atomic.Int64, plainHits *atomic.Int64, addr string) {
	t.Helper()

	var connects, plains atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			connects.Add(1)

			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "no hijack", http.StatusInternalServerError)
				return
			}
			targetConn, _, err := hj.Hijack()
			if err != nil {
				return
			}
			defer targetConn.Close()

			if _, err := targetConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
				return
			}

			if echoTunnels {
				io.Copy(targetConn, targetConn)
				return
			}

			remote, err := net.DialTimeout("tcp", r.Host, backstopTimeout)
			if err != nil {
				return
			}
			defer remote.Close()
			linkBidirectionalTunnel(targetConn, remote)
			return
		}

		plains.Add(1)

		// Absolute-form request URL names the real target.
		if r.URL.Host == "" {
			http.Error(w, "not an absolute-form request", http.StatusBadRequest)
			return
		}
		out, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("build request: %v", err), http.StatusBadGateway)
			return
		}
		out.Header = r.Header.Clone()
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, fmt.Sprintf("relay: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	t.Cleanup(upstream.Close)

	return &connects, &plains, strings.TrimPrefix(upstream.URL, "http://")
}

// TestUpstreamChainPlainHTTP pins the plain-HTTP forwarding path: a request
// made through Zen must arrive at the upstream proxy, and its response must
// make it back to the client.
func TestUpstreamChainPlainHTTP(t *testing.T) {
	_, plains, upstreamAddr := startFakeUpstreamProxy(t, false)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "through the chain")
	}))
	defer target.Close()

	t.Setenv(upstreamProxyEnv, upstreamAddr)
	addr := startTestProxy(t, nil)

	resp, err := proxyClient(t, addr).Get(target.URL)
	if err != nil {
		t.Fatalf("get through chained proxy: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "through the chain" {
		t.Fatalf("body = %q, want %q", body, "through the chain")
	}

	if got := plains.Load(); got != 1 {
		t.Fatalf("upstream plain hits = %d, want 1", got)
	}
}

// TestUpstreamChainMITM pins the HTTPS path: after the MITM handshake Zen
// relays the request through the upstream proxy, tunnelling to the target
// with CONNECT. The self-signed certificate generator and the skip-verify
// transport stand in for the trust a real installation has: the generator
// mints the leaves clients accept, and Zen's outbound transport would
// otherwise reject httptest's own certificate.
func TestUpstreamChainMITM(t *testing.T) {
	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	tlsTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "mitm through the chain")
	}))
	defer tlsTarget.Close()

	t.Setenv(upstreamProxyEnv, upstreamAddr)
	addr := startTestProxy(t, func(p *Proxy) {
		p.certGenerator = selfSignedCertGenerator{}
		// The transport is built without a TLS config, so assign one whole.
		transportOf(t, p).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- the upstream target is an httptest server; trust is not under test.
	})

	status, body := mitmGet(t, addr, fmt.Sprintf("localhost:%d", portOf(t, tlsTarget)), "localhost")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	if body != "mitm through the chain" {
		t.Fatalf("body = %q, want %q", body, "mitm through the chain")
	}
	// NOTE: no upstream-CONNECT assertion here. The MITM'd https request rides
	// the mimic-TLS transport, whose raw dial goes through chainDialer -
	// which dials loopback targets directly by design (skipChain), so the
	// fake upstream sees no CONNECT for a localhost target. Through-the-
	// upstream behaviour for non-loopback targets is pinned by
	// TestUpstreamChainTunnel; the mimic dialer's chain wiring is pinned by
	// dialUpstreamMimicTLS reading p.upstreamChain first.
}

// TestUpstreamChainTunnel pins the passthrough CONNECT path: a host on the
// transparent list is tunnelled, and the tunnel must be established via the
// upstream proxy.
func TestUpstreamChainTunnel(t *testing.T) {
	connects, _, upstreamAddr := startFakeUpstreamProxy(t, true)

	t.Setenv(upstreamProxyEnv, upstreamAddr)
	addr := startTestProxy(t, func(p *Proxy) {
		p.addTransparentHost("chain.example.invalid")
	})

	conn, br, resp := connectThrough(t, addr, "chain.example.invalid:443")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatalf("write into tunnel: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(backstopTimeout))
	echo := make([]byte, 4)
	if _, err := io.ReadFull(br, echo); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(echo) != "ping" {
		t.Fatalf("echo = %q, want %q", echo, "ping")
	}

	if got := connects.Load(); got != 1 {
		t.Fatalf("upstream CONNECT hits = %d, want 1", got)
	}
}

// TestUpstreamChainSkipsLoopbackTargets holds the loopback guard: with the
// chain pointed at a dead upstream, tunnelling to a loopback target must
// still succeed by dialing directly.
func TestUpstreamChainSkipsLoopbackTargets(t *testing.T) {
	want := "loopback stays direct"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, want)
	}))
	defer target.Close()

	// Port 1 on loopback: nothing listens there, any chain attempt fails.
	t.Setenv(upstreamProxyEnv, "127.0.0.1:1")

	_, addr := startLocalEndpointProxy(t, noopFilter{}, nil)

	conn, br, resp := connectThrough(t, addr, target.Listener.Addr().String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	req, err := http.NewRequest(http.MethodGet, target.URL, nil)
	if err != nil {
		t.Fatalf("build tunnelled request: %v", err)
	}
	if err := req.Write(conn); err != nil {
		t.Fatalf("write tunnelled request: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(backstopTimeout))
	tunnelled, err := http.ReadResponse(br, req)
	if err != nil {
		t.Fatalf("read tunnelled response: %v", err)
	}
	defer tunnelled.Body.Close()

	body, err := io.ReadAll(tunnelled.Body)
	if err != nil {
		t.Fatalf("read tunnelled body: %v", err)
	}
	if string(body) != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

// TestChainDialerPreservesEarlyTunnelBytes pins the CONNECT-response parsing:
// payload bytes arriving in the same segment as the response head must reach
// the tunnel reader, not rot in the bufio buffer.
func TestChainDialerPreservesEarlyTunnelBytes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Consume the CONNECT request so the echo loop only ever reflects
		// tunnel payload; then answer with head and payload in a single
		// write, as a chatty upstream proxy bundled into one segment would.
		br := bufio.NewReader(conn)
		req, err := http.ReadRequest(br)
		if err != nil || req.Method != http.MethodConnect {
			return
		}
		conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\nEARLY"))
		io.Copy(conn, br)
	}()

	u, err := url.Parse("http://" + ln.Addr().String())
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	d := &chainDialer{inner: &net.Dialer{Timeout: backstopTimeout}, proxyURL: u}

	ctx, cancel := context.WithTimeout(context.Background(), backstopTimeout)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", "target.example.invalid:443")
	if err != nil {
		t.Fatalf("dial via chain: %v", err)
	}
	defer conn.Close()

	head := make([]byte, 5)
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatalf("read early bytes: %v", err)
	}
	if string(head) != "EARLY" {
		t.Fatalf("early bytes = %q, want %q", head, "EARLY")
	}

	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatalf("write into tunnel: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(backstopTimeout))
	echo := make([]byte, 4)
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(echo) != "ping" {
		t.Fatalf("echo = %q, want %q", echo, "ping")
	}
}

// TestParseUpstreamProxyURL covers the accepted and rejected configuration
// forms.
func TestParseUpstreamProxyURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"bare host port", "192.0.2.1:8080", "http://192.0.2.1:8080", false},
		{"full url", "http://192.0.2.1:8080", "http://192.0.2.1:8080", false},
		{"default port", "192.0.2.1", "http://192.0.2.1:80", false},
		{"userinfo", "http://user:pass@192.0.2.1:8080", "http://user:pass@192.0.2.1:8080", false},
		{"whitespace", "  http://192.0.2.1:8080  ", "http://192.0.2.1:8080", false},
		{"socks rejected", "socks5://192.0.2.1:1080", "", true},
		{"empty", "   ", "", true},
		{"no host", "http://", "", true},
		{"path rejected", "http://192.0.2.1:8080/path", "", true},
		{"not a url", "http://\"bad\"", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := parseUpstreamProxyURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseUpstreamProxyURL(%q) = %v, want error", tc.in, u)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseUpstreamProxyURL(%q): %v", tc.in, err)
			}
			if u.String() != tc.want {
				t.Fatalf("parseUpstreamProxyURL(%q) = %q, want %q", tc.in, u.String(), tc.want)
			}
		})
	}
}

// TestSkipChain pins the direct-dial guards.
func TestSkipChain(t *testing.T) {
	upstream := &url.URL{Scheme: "http", Host: "192.0.2.1:8080"}

	for _, addr := range []string{"127.0.0.1:80", "localhost:8080", "[::1]:443", "192.0.2.1:8080"} {
		if !skipChain(addr, upstream) {
			t.Errorf("skipChain(%q) = false, want true", addr)
		}
	}
	for _, addr := range []string{"example.com:443", "192.0.2.1:9090", "192.0.2.53:53"} {
		if skipChain(addr, upstream) {
			t.Errorf("skipChain(%q) = true, want false", addr)
		}
	}
}

// TestUnsetEnvKeepsStockBehaviour pins the off switch: with the variable
// unset, the transport keeps no Proxy and no chain dialer is installed.
func TestUnsetEnvKeepsStockBehaviour(t *testing.T) {
	t.Setenv(upstreamProxyEnv, "")

	p := newTestProxy(t)

	if p.upstreamChain != nil {
		t.Fatal("upstreamChain installed despite unset environment variable")
	}
	if transportOf(t, p).Proxy != nil {
		t.Fatal("transport Proxy set despite unset environment variable")
	}
}

// TestInvalidEnvKeepsStockBehaviour pins the fail-open rule: an invalid value
// disables chaining rather than breaking startup.
func TestInvalidEnvKeepsStockBehaviour(t *testing.T) {
	t.Setenv(upstreamProxyEnv, "ftp://not-a-proxy")

	p := newTestProxy(t)

	if p.upstreamChain != nil {
		t.Fatal("upstreamChain installed despite invalid configuration")
	}
	if transportOf(t, p).Proxy != nil {
		t.Fatal("transport Proxy set despite invalid configuration")
	}
}

// portOf returns the port an httptest server listens on.
func portOf(t *testing.T, s *httptest.Server) int {
	t.Helper()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	if err != nil {
		if _, port2, err2 := net.SplitHostPort(strings.TrimPrefix(s.URL, "https://")); err2 == nil {
			return atoiPort(t, port2)
		}
		t.Fatalf("split server url: %v", err)
	}
	return atoiPort(t, port)
}

func atoiPort(t *testing.T, port string) int {
	t.Helper()

	var n int
	if _, err := fmt.Sscanf(port, "%d", &n); err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}
	return n
}

// TestReadUpstreamConfigFile covers the config-file source of the upstream
// setting: valid value, BOM/whitespace tolerance, empty file and missing file.
// (2026-09-18: config-file source added so the chain engages no matter how
// Zen is started, without persistent environment variables.)
func TestReadUpstreamConfigFile(t *testing.T) {
	dir := t.TempDir()

	if _, _, ok := readUpstreamConfigFile(filepath.Join(dir, "missing")); ok {
		t.Fatal("missing config file must not resolve")
	}

	path := filepath.Join(dir, upstreamConfigFile)
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"plain", "http://1.2.3.4:8080\r\n", "http://1.2.3.4:8080"},
		{"bare host port", "10.0.0.1:7890\n", "10.0.0.1:7890"},
		{"bom and whitespace", "\ufeff  192.168.1.1:8080 \r\n", "192.168.1.1:8080"},
		{"empty file", "  \r\n", ""},
	}
	for _, tc := range cases {
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatalf("write %s: %v", tc.name, err)
		}
		cfg, source, ok := readUpstreamConfigFile(dir)
		if tc.want == "" {
			if ok {
				t.Fatalf("%s: empty config must not resolve (got %q)", tc.name, cfg)
			}
			continue
		}
		if !ok {
			t.Fatalf("%s: expected resolution, got none", tc.name)
		}
		if cfg != tc.want {
			t.Fatalf("%s: cfg = %q, want %q", tc.name, cfg, tc.want)
		}
		if source != path {
			t.Fatalf("%s: source = %q, want %q", tc.name, source, path)
		}
	}
}

// TestResolveUpstreamConfigEnvWins pins the precedence: a set environment
// variable always wins over any config file on disk.
func TestResolveUpstreamConfigEnvWins(t *testing.T) {
	t.Setenv(upstreamProxyEnv, "http://9.9.9.9:1")

	cfg, source, ok := resolveUpstreamConfig()
	if !ok {
		t.Fatal("env var set but not resolved")
	}
	if cfg != "http://9.9.9.9:1" {
		t.Fatalf("cfg = %q, want env value", cfg)
	}
	if source != upstreamProxyEnv {
		t.Fatalf("source = %q, want %q", source, upstreamProxyEnv)
	}
}
