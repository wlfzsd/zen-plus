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
	"strings"
	"sync"
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
		{"socks5 url", "socks5://192.0.2.1:1080", "socks5://192.0.2.1:1080", false},
		{"socks5 default port", "socks5://192.0.2.1", "socks5://192.0.2.1:1080", false},
		{"socks5h alias", "socks5h://192.0.2.1:1080", "socks5://192.0.2.1:1080", false},
		{"socks5 userinfo", "socks5://user:pass@192.0.2.1:1080", "socks5://user:pass@192.0.2.1:1080", false},
		{"https url", "https://192.0.2.1:8443", "https://192.0.2.1:8443", false},
		{"https default port", "https://192.0.2.1", "https://192.0.2.1:443", false},
		{"empty", "   ", "", true},
		{"no host", "http://", "", true},
		{"no host socks5", "socks5://", "", true},
		{"path rejected", "http://192.0.2.1:8080/path", "", true},
		{"path rejected socks5", "socks5://192.0.2.1:1080/path", "", true},
		{"ftp rejected", "ftp://192.0.2.1:21", "", true},
		{"port zero rejected", "http://192.0.2.1:0", "", true},
		{"port out of range rejected", "http://192.0.2.1:70000", "", true},
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

// fakeSOCKS5Upstream is a minimal SOCKS5 server (RFC 1928): method
// negotiation, optional username/password subnegotiation (RFC 1929), CONNECT
// only, then an echo tunnel. It records what it saw so tests can pin the
// bytes the chain dialer puts on the wire.
type fakeSOCKS5Upstream struct {
	mu         sync.Mutex
	handshakes int
	authMode   int // 0 = none, 2 = username/password
	authUser   string
	authPass   string
	target     string
}

func (f *fakeSOCKS5Upstream) snapshot() (handshakes, authMode int, authUser, authPass, target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handshakes, f.authMode, f.authUser, f.authPass, f.target
}

// startFakeSOCKS5Upstream runs the server. When requireAuth is set the
// username/password method is selected and the offered credentials must
// match, else the handshake is dropped.
func startFakeSOCKS5Upstream(t *testing.T, requireAuth bool, user, pass string) (*fakeSOCKS5Upstream, string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSOCKS5Upstream{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn, requireAuth, user, pass)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f, ln.Addr().String()
}

func (f *fakeSOCKS5Upstream) serve(conn net.Conn, requireAuth bool, user, pass string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(backstopTimeout))
	defer func() {
		// A late echo copy must not outlive the deadline; nothing to do, the
		// deferred Close ends both directions.
	}()

	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	if head[0] != 5 {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	hasNoAuth, hasUserPass := false, false
	for _, m := range methods {
		switch m {
		case 0:
			hasNoAuth = true
		case 2:
			hasUserPass = true
		}
	}
	selected := byte(0xFF)
	switch {
	case requireAuth && hasUserPass:
		selected = 2
	case !requireAuth && hasNoAuth:
		selected = 0
	}
	if _, err := conn.Write([]byte{5, selected}); err != nil {
		return
	}
	if selected == 0xFF {
		return
	}

	authMode := 0
	if selected == 2 {
		authMode = 2
		ver := make([]byte, 1)
		if _, err := io.ReadFull(conn, ver); err != nil || ver[0] != 1 {
			return
		}
		nlen := make([]byte, 1)
		if _, err := io.ReadFull(conn, nlen); err != nil {
			return
		}
		uname := make([]byte, nlen[0])
		if _, err := io.ReadFull(conn, uname); err != nil {
			return
		}
		plen := make([]byte, 1)
		if _, err := io.ReadFull(conn, plen); err != nil {
			return
		}
		passwd := make([]byte, plen[0])
		if _, err := io.ReadFull(conn, passwd); err != nil {
			return
		}
		if string(uname) != user || string(passwd) != pass {
			conn.Write([]byte{1, 1}) // auth failure
			return
		}
		if _, err := conn.Write([]byte{1, 0}); err != nil {
			return
		}
	}

	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return
	}
	if hdr[0] != 5 || hdr[1] != 1 { // CONNECT only
		return
	}
	target, err := readSOCKS5Address(conn, hdr[3])
	if err != nil {
		return
	}

	f.mu.Lock()
	f.handshakes++
	f.authMode = authMode
	f.authUser = user
	f.authPass = pass
	f.target = target
	f.mu.Unlock()

	// Succeeded, BND.ADDR 0.0.0.0, BND.PORT 0; then echo the tunnel.
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	io.Copy(conn, conn)
}

// readSOCKS5Address reads the target address following an ATYP byte.
func readSOCKS5Address(conn net.Conn, atyp byte) (string, error) {
	switch atyp {
	case 1: // IPv4
		raw := make([]byte, 6)
		if _, err := io.ReadFull(conn, raw); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s:%d", net.IP(raw[:4]), int(raw[4])<<8|int(raw[5])), nil
	case 3: // domain
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return "", err
		}
		raw := make([]byte, int(l[0])+2)
		if _, err := io.ReadFull(conn, raw); err != nil {
			return "", err
		}
		port := int(raw[len(raw)-2])<<8 | int(raw[len(raw)-1])
		return fmt.Sprintf("%s:%d", string(raw[:l[0]]), port), nil
	case 4: // IPv6
		raw := make([]byte, 18)
		if _, err := io.ReadFull(conn, raw); err != nil {
			return "", err
		}
		return fmt.Sprintf("[%s]:%d", net.IP(raw[:16]), int(raw[16])<<8|int(raw[17])), nil
	default:
		return "", fmt.Errorf("unknown ATYP %d", atyp)
	}
}

// TestChainDialerSOCKS5 pins the SOCKS5 handshake the dialer speaks: method
// selection, RFC 1929 credentials when configured, the CONNECT target in
// domain form, and a working echo tunnel afterwards.
func TestChainDialerSOCKS5(t *testing.T) {
	t.Run("no auth", func(t *testing.T) {
		fake, addr := startFakeSOCKS5Upstream(t, false, "", "")
		d := newSOCKS5ChainDialerForTest(t, "socks5://"+addr)
		ctx, cancel := context.WithTimeout(context.Background(), backstopTimeout)
		defer cancel()
		conn, err := d.DialContext(ctx, "tcp", "target.example.invalid:443")
		if err != nil {
			t.Fatalf("dial via socks5: %v", err)
		}
		defer conn.Close()
		echoRoundTrip(t, conn)

		handshakes, authMode, _, _, target := fake.snapshot()
		if handshakes != 1 {
			t.Fatalf("handshakes = %d, want 1", handshakes)
		}
		if authMode != 0 {
			t.Fatalf("authMode = %d, want 0 (none)", authMode)
		}
		if target != "target.example.invalid:443" {
			t.Fatalf("target = %q, want domain-form CONNECT target", target)
		}
	})

	t.Run("username/password auth", func(t *testing.T) {
		fake, addr := startFakeSOCKS5Upstream(t, true, "alice", "s3cret:pass")
		d := newSOCKS5ChainDialerForTest(t, "socks5://alice:s3cret:pass@"+addr)
		ctx, cancel := context.WithTimeout(context.Background(), backstopTimeout)
		defer cancel()
		conn, err := d.DialContext(ctx, "tcp", "target.example.invalid:443")
		if err != nil {
			t.Fatalf("dial via socks5 with auth: %v", err)
		}
		defer conn.Close()
		echoRoundTrip(t, conn)

		handshakes, authMode, authUser, authPass, _ := fake.snapshot()
		if handshakes != 1 {
			t.Fatalf("handshakes = %d, want 1", handshakes)
		}
		if authMode != 2 {
			t.Fatalf("authMode = %d, want 2 (username/password)", authMode)
		}
		if authUser != "alice" || authPass != "s3cret:pass" {
			t.Fatalf("credentials = %q/%q, want alice/s3cret:pass", authUser, authPass)
		}
	})

	t.Run("wrong credentials fail", func(t *testing.T) {
		fake, addr := startFakeSOCKS5Upstream(t, true, "alice", "right")
		d := newSOCKS5ChainDialerForTest(t, "socks5://alice:wrong@"+addr)
		ctx, cancel := context.WithTimeout(context.Background(), backstopTimeout)
		defer cancel()
		if conn, err := d.DialContext(ctx, "tcp", "target.example.invalid:443"); err == nil {
			conn.Close()
			t.Fatal("dial with wrong credentials succeeded, want failure")
		}
		if handshakes, _, _, _, _ := fake.snapshot(); handshakes != 0 {
			t.Fatalf("handshakes = %d, want 0 (server drops before CONNECT)", handshakes)
		}
	})
}

// newSOCKS5ChainDialerForTest builds a chainDialer wired for a SOCKS5
// upstream, the same way applyUpstreamChain wires it in production.
func newSOCKS5ChainDialerForTest(t *testing.T, rawURL string) *chainDialer {
	t.Helper()
	inner := &net.Dialer{Timeout: backstopTimeout}
	u := parseUpstreamURLForTest(t, rawURL)
	cd, err := newSOCKS5Dialer(u, inner)
	if err != nil {
		t.Fatalf("build socks5 dialer: %v", err)
	}
	return &chainDialer{inner: inner, proxyURL: u, socksDialer: cd}
}

// TestUpstreamChainSOCKS5Tunnel pins the passthrough CONNECT path through a
// SOCKS5 upstream: the tunnel is established with the RFC 1928 handshake and
// carries the client's bytes.
func TestUpstreamChainSOCKS5Tunnel(t *testing.T) {
	fake, upstreamAddr := startFakeSOCKS5Upstream(t, false, "", "")

	t.Setenv(upstreamProxyEnv, "socks5://"+upstreamAddr)
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

	handshakes, _, _, _, target := fake.snapshot()
	if handshakes != 1 {
		t.Fatalf("upstream socks5 handshakes = %d, want 1", handshakes)
	}
	if target != "chain.example.invalid:443" {
		t.Fatalf("upstream CONNECT target = %q, want chain.example.invalid:443", target)
	}
}

// TestChainDialerHTTPSUpstream pins the https-upstream path: the connection
// to the proxy is TLS-wrapped (proxy certificate verified per the injected
// config) before the CONNECT handshake runs inside it.
func TestChainDialerHTTPSUpstream(t *testing.T) {
	var connects atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "not a CONNECT", http.StatusBadRequest)
			return
		}
		connects.Add(1)
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			return
		}
		io.Copy(conn, conn)
	}))
	defer upstream.Close()

	d := &chainDialer{
		inner:          &net.Dialer{Timeout: backstopTimeout},
		proxyURL:       parseUpstreamURLForTest(t, "https://"+strings.TrimPrefix(upstream.URL, "https://")),
		proxyTLSConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- the upstream is an httptest server; trust is not under test.
	}
	ctx, cancel := context.WithTimeout(context.Background(), backstopTimeout)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", "target.example.invalid:443")
	if err != nil {
		t.Fatalf("dial via https upstream: %v", err)
	}
	defer conn.Close()
	echoRoundTrip(t, conn)

	if got := connects.Load(); got != 1 {
		t.Fatalf("upstream CONNECT hits = %d, want 1", got)
	}
}

// parseUpstreamURLForTest parses a proxy URL, failing the test on error.
func parseUpstreamURLForTest(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

// echoRoundTrip writes "ping" into the tunnel and expects it echoed back.
func echoRoundTrip(t *testing.T, conn net.Conn) {
	t.Helper()
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

// setUpstreamProviderForTest installs fn as the app-settings provider for the
// duration of the test, restoring the previous one afterwards.
func setUpstreamProviderForTest(t *testing.T, fn func() (string, string, bool)) {
	t.Helper()
	prev := upstreamProvider
	upstreamProvider = fn
	t.Cleanup(func() { upstreamProvider = prev })
}

// TestResolveUpstreamConfigProvider pins the app-settings source: with the
// environment variable unset, the provider's value is used; a provider
// reporting not-configured means stock behaviour.
func TestResolveUpstreamConfigProvider(t *testing.T) {
	t.Setenv(upstreamProxyEnv, "")

	setUpstreamProviderForTest(t, func() (string, string, bool) {
		return "socks5://192.0.2.1:1080", "app settings", true
	})
	cfg, source, ok := resolveUpstreamConfig()
	if !ok {
		t.Fatal("provider reports configured but nothing resolved")
	}
	if cfg != "socks5://192.0.2.1:1080" {
		t.Fatalf("cfg = %q, want provider value", cfg)
	}
	if source != "app settings" {
		t.Fatalf("source = %q, want %q", source, "app settings")
	}

	setUpstreamProviderForTest(t, func() (string, string, bool) { return "", "app settings", false })
	if _, _, ok := resolveUpstreamConfig(); ok {
		t.Fatal("provider reports not configured but something resolved")
	}
}

// TestResolveUpstreamConfigEnvWins pins the precedence: a set environment
// variable always wins over the app-settings provider.
func TestResolveUpstreamConfigEnvWins(t *testing.T) {
	t.Setenv(upstreamProxyEnv, "http://9.9.9.9:1")
	setUpstreamProviderForTest(t, func() (string, string, bool) {
		return "socks5://8.8.8.8:1", "app settings", true
	})

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

// TestSyncUpstreamEnvMirrors pins the environment mirroring for
// DefaultTransport-based clients: configured upstream sets the standard
// variables, a disabled one clears them again - but only when this package
// set them before.
func TestSyncUpstreamEnvMirrors(t *testing.T) {
	t.Setenv(upstreamProxyEnv, "")
	// The mirror writes the standard proxy variables; restore whatever the
	// test process started with so no other test sees leaked state.
	proxyEnvNames := []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"}
	prevEnv := make(map[string]string, len(proxyEnvNames))
	for _, name := range proxyEnvNames {
		prev, had := os.LookupEnv(name)
		prevEnv[name] = prev
		if had {
			t.Cleanup(func() { os.Setenv(name, prevEnv[name]) })
		} else {
			t.Cleanup(func() { os.Unsetenv(name) })
		}
	}
	setUpstreamProviderForTest(t, func() (string, string, bool) {
		return "http://192.0.2.1:8080", "app settings", true
	})
	// User's own HTTP_PROXY must survive when we never owned the variables.
	t.Setenv("HTTP_PROXY", "http://user-owns-me:1")

	SyncUpstreamEnv()
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		if got := os.Getenv(name); got != "http://192.0.2.1:8080" {
			t.Fatalf("%s = %q, want mirrored upstream", name, got)
		}
	}

	// Disable the upstream: our variables go away...
	setUpstreamProviderForTest(t, func() (string, string, bool) { return "", "app settings", false })
	SyncUpstreamEnv()
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		if got := os.Getenv(name); got != "" {
			t.Fatalf("%s = %q, want cleared", name, got)
		}
	}

	// ...and a user-owned HTTP_PROXY (set after ownership lapsed) stays.
	t.Setenv("HTTP_PROXY", "http://user-owns-me:1")
	SyncUpstreamEnv()
	if got := os.Getenv("HTTP_PROXY"); got != "http://user-owns-me:1" {
		t.Fatalf("user-owned HTTP_PROXY = %q, want untouched", got)
	}
}
