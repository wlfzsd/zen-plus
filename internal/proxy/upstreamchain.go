package proxy

// Upstream proxy chaining, enabled through the ZEN_UPSTREAM_PROXY environment
// variable. When the variable holds a valid HTTP proxy address, every outbound
// path of the proxy is chained through it:
//
//   - forwarded plain-HTTP requests and MITM'd requests travel through
//     requestTransport, which gets a Proxy function pointing at the upstream;
//   - CONNECT tunnels and WebSocket dials bypass the transport and dial with
//     netDialer; those call sites go through a chainDialer that first
//     establishes a CONNECT tunnel via the upstream.
//
// The feature keys off the environment variable alone: unset (or invalid,
// with a logged warning) means stock behaviour. Loopback destinations and the
// upstream's own address are always dialed directly - a local service would
// become unreachable through a remote upstream, and CONNECTing to the
// upstream through itself would recurse.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/irbis-sh/zen-desktop/internal/redacted"
)

// upstreamProxyEnv names the environment variable holding the upstream proxy.
// Accepted forms: "host:port", "http://host:port", "http://user:pass@host:port".
const upstreamProxyEnv = "ZEN_UPSTREAM_PROXY"

const (
	// connectHandshakeTimeout bounds the CONNECT handshake with the upstream
	// when the caller's context carries no deadline. A local upstream answers
	// within milliseconds; anything past this means the upstream is wedged.
	connectHandshakeTimeout = 30 * time.Second

	// Chained-mode transport hygiene (2026-09-18): stock Zen dials targets
	// directly, where a wedged connection costs at most one target. Through
	// an upstream, the transport's pooled connections die with every upstream
	// hiccup, and the stock values turn that into minutes-long stalls:
	//   - ResponseHeaderTimeout 3min: a stale pooled connection hangs the
	//     request for the full window instead of failing fast and retrying;
	//   - ForceAttemptHTTP2: every request to a host shares one multiplexed
	//     connection, so one dead tunnel stalls all of them (head-of-line);
	//   - IdleConnTimeout 90s: keeps corpses around through upstream
	//     restarts, and piles up connections on the upstream.
	// HTTP/1.1 per-request connections isolate failures and make the
	// maxIdleConnsPerHost=16 pool meaningful again.
	chainedResponseHeaderTimeout = 30 * time.Second
	chainedIdleConnTimeout       = 30 * time.Second
	// chainedForceHTTP1 flips the upstream TLS ALPN to HTTP/1.1 only.
	chainedForceHTTP1 = false
)

// upstreamConfigFile names the optional file sitting next to the executable
// holding the upstream proxy address ("http://host:port" or bare "host:port").
// It lets the chain engage no matter how Zen is started - Start Menu, autostart
// or double-click - without touching any persistent environment variable.
const upstreamConfigFile = "upstream-proxy.txt"

// resolveUpstreamConfig locates the upstream proxy setting. The environment
// variable wins when set; otherwise a upstream-proxy.txt next to the
// executable is read. Returns the raw value, a human-readable source for log
// lines, and whether anything was found.
func resolveUpstreamConfig() (cfg, source string, ok bool) {
	if v := os.Getenv(upstreamProxyEnv); strings.TrimSpace(v) != "" {
		return v, upstreamProxyEnv, true
	}
	exe, err := os.Executable()
	if err != nil {
		return "", "", false
	}
	return readUpstreamConfigFile(filepath.Dir(exe))
}

// readUpstreamConfigFile reads upstream-proxy.txt from dir, tolerating a BOM
// and surrounding whitespace. A missing file is silently ignored; anything
// else that goes wrong is logged and treated as "not configured" - a broken
// config file must never take the proxy down.
func readUpstreamConfigFile(dir string) (cfg, source string, ok bool) {
	path := filepath.Join(dir, upstreamConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("upstream proxy: reading %s: %v", path, err)
		}
		return "", "", false
	}
	// Tolerate editors that save a BOM or surrounding whitespace.
	v := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if v == "" {
		return "", "", false
	}
	return v, path, true
}

// UpstreamConfigured reports whether an upstream proxy is currently configured
// (environment variable or upstream-proxy.txt config file). The app consults
// it when rendering the system PAC file: with chaining active the PAC must not
// send the embedded sensitive-host exclusions DIRECT - on censored networks
// those hosts are exactly the ones only reachable through the chain, and a
// DIRECT answer makes the browser bypass Zen entirely.
func UpstreamConfigured() bool {
	_, _, ok := resolveUpstreamConfig()
	return ok
}

// applyUpstreamChain wires the upstream proxy into p, if configured. It is
// called once from NewProxy, before the proxy starts serving.
func applyUpstreamChain(p *Proxy) {
	cfg, source, ok := resolveUpstreamConfig()
	if !ok {
		return
	}

	u, err := parseUpstreamProxyURL(cfg)
	if err != nil {
		log.Printf("upstream proxy: ignoring invalid %s=%q from %s: %v", upstreamProxyEnv, cfg, source, err)
		return
	}

	transport, ok := p.requestTransport.(*http.Transport)
	if !ok {
		log.Printf("upstream proxy: requestTransport is %T, not *http.Transport; chaining disabled", p.requestTransport)
		return
	}

	p.upstreamChain = &chainDialer{inner: p.netDialer, proxyURL: u}
	// Plain-HTTP forwarding and MITM'd requests speak through
	// requestTransport: pointing its Proxy at the upstream chains them. Its
	// DialContext stays the plain netDialer - the transport dials the
	// upstream address itself and speaks the proxy protocol on top.
	transport.Proxy = http.ProxyURL(u)
	// Time the transport's own dials to the upstream: the MITM/plain path is
	// the dominant one for browsers, and its CONNECT stage is Go-internal -
	// without this there is zero observability when it goes bad.
	innerDial := p.netDialer.DialContext
	proxyHost := u.Host
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		start := time.Now()
		conn, err := innerDial(ctx, network, addr)
		if addr == proxyHost {
			if err != nil {
				logUpstreamEvent(addr, "transport-dial", time.Since(start), err)
			} else if elapsed := time.Since(start); elapsed > slowTunnelLogThreshold {
				logUpstreamEvent(addr, "transport-dial", elapsed, nil)
			}
		}
		return conn, err
	}
	applyChainedTransportHygiene(transport)

	// HTTPS re-originated by the MITM gets a dedicated transport: the chain
	// tunnel is established here and the TLS handshake replays the client's
	// captured ClientHello (uTLS), so the request keeps its original TLS
	// fingerprint end to end. Proxy is nil on purpose - the tunnel is
	// established inside DialTLSContext, and TLSNextProto is emptied to keep
	// Go's own h2 out of the re-originated leg. The dialer reads the stock
	// transport's TLSClientConfig live, so verification customisations made
	// after the chain is built are still honoured.
	httpsTransport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			start := time.Now()
			conn, err := p.dialUpstreamMimicTLS(ctx, network, addr, transport.TLSClientConfig)
			if err != nil {
				logUpstreamEvent(addr, "mimic-tls", time.Since(start), err)
			} else if elapsed := time.Since(start); elapsed > slowTunnelLogThreshold {
				logUpstreamEvent(addr, "mimic-tls", elapsed, nil)
			}
			return conn, err
		},
		Proxy:        nil,
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	applyChainedTransportHygiene(httpsTransport)
	p.requestTransportHTTPS = httpsTransport

	log.Printf("upstream proxy: chaining outbound traffic through %s (source: %s)", u.Host, source)
}

// applyChainedTransportHygiene retunes the transport for upstream-chained
// operation: fail fast on stale pooled connections, prune idle ones before
// they rot, and (optionally) drop HTTP/2 multiplexing so one dead tunnel
// cannot stall every stream multiplexed onto it. Stock (non-chained) behaviour
// is untouched - this only runs when an upstream is configured.
func applyChainedTransportHygiene(t *http.Transport) {
	t.ResponseHeaderTimeout = chainedResponseHeaderTimeout
	t.IdleConnTimeout = chainedIdleConnTimeout
	if chainedForceHTTP1 {
		t.ForceAttemptHTTP2 = false
		next := t.TLSClientConfig.Clone()
		if next == nil {
			next = &tls.Config{}
		}
		next.NextProtos = []string{"http/1.1"}
		t.TLSClientConfig = next
	}
}

// tunnelDialContext dials addr for a CONNECT tunnel, chaining through the
// upstream proxy when configured.
func (p *Proxy) tunnelDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if p.upstreamChain != nil {
		return p.upstreamChain.DialContext(ctx, network, addr)
	}
	return p.netDialer.DialContext(ctx, network, addr)
}

// tunnelDial is tunnelDialContext for callers holding no request context.
func (p *Proxy) tunnelDial() func(network, addr string) (net.Conn, error) {
	if p.upstreamChain != nil {
		return p.upstreamChain.Dial
	}
	return p.netDialer.Dial
}

// chainTLSDial returns a dial func that chains through the upstream proxy and
// completes the TLS handshake to the target, matching tls.Dialer semantics
// for hijacked WebSocket upgrades.
func (p *Proxy) chainTLSDial(ctx context.Context) func(network, addr string) (net.Conn, error) {
	return func(network, addr string) (net.Conn, error) {
		conn, err := p.upstreamChain.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tlsConn := tls.Client(conn, &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: hostOnly(addr),
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("TLS handshake with %s via upstream: %w", redacted.Redacted(addr), err)
		}
		return tlsConn, nil
	}
}

// chainDialer dials TCP addresses through an upstream HTTP proxy, tunneling
// each target with CONNECT. It offers the DialContext/Dial surface the
// netDialer call sites expect.
type chainDialer struct {
	inner    *net.Dialer
	proxyURL *url.URL
}

// slowTunnelLogThreshold bounds the "healthy but slow" noise floor: only
// tunnel attempts slower than this get a success line. Failures always log.
const slowTunnelLogThreshold = time.Second

// logUpstreamEvent records a chained-tunnel attempt with its stage and
// duration. Added 2026-09-18 after a silent failure mode (every target dying
// at ~2s with zero log lines, healed only by restarting Zen) cost a full
// diagnosis cycle: the next occurrence must document itself.
func logUpstreamEvent(addr, stage string, elapsed time.Duration, err error) {
	if err != nil {
		log.Printf("upstream proxy: %s to %s failed after %s: %v", stage, redacted.Redacted(addr), elapsed.Round(time.Millisecond), err)
		return
	}
	log.Printf("upstream proxy: %s to %s slow: %s", stage, redacted.Redacted(addr), elapsed.Round(time.Millisecond))
}

// DialContext implements the net.Dialer.DialContext shape.
func (d *chainDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if skipChain(addr, d.proxyURL) {
		return d.inner.DialContext(ctx, network, addr)
	}

	if network != "tcp" {
		return nil, fmt.Errorf("upstream proxy: unsupported network %q for CONNECT", network)
	}

	start := time.Now()
	conn, err := d.inner.DialContext(ctx, "tcp", d.proxyURL.Host)
	if err != nil {
		logUpstreamEvent(addr, "dial-proxy", time.Since(start), err)
		return nil, fmt.Errorf("dialing upstream proxy %s: %w", d.proxyURL.Host, err)
	}

	tunneled, err := tunnelThroughUpstream(ctx, conn, d.proxyURL, addr)
	if err != nil {
		conn.Close()
		logUpstreamEvent(addr, "connect", time.Since(start), err)
		return nil, err
	}
	if elapsed := time.Since(start); elapsed > slowTunnelLogThreshold {
		logUpstreamEvent(addr, "connect", elapsed, nil)
	}
	return tunneled, nil
}

// Dial is the context-free shape used by the hijacked WebSocket dials. It
// applies the stock dial timeout: with no context there is nothing to honour
// ctx cancellation by, and a wedged upstream must not block forever.
func (d *chainDialer) Dial(network, addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	return d.DialContext(ctx, network, addr)
}

// skipChain reports whether addr must bypass the upstream and dial directly:
// loopback targets would become unreachable through a remote upstream, and
// the upstream's own address CONNECTed through itself would recurse.
func skipChain(addr string, proxyURL *url.URL) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	if host == proxyURL.Hostname() && (proxyURL.Port() == "" || port == proxyURL.Port()) {
		return true
	}
	return false
}

// tunnelThroughUpstream turns conn into a tunnel to target by issuing CONNECT
// via proxyURL. On success the returned conn speaks directly to target; any
// bytes the upstream sent past the response head are preserved.
//
// The handshake honours ctx cancellation even when ctx carries no deadline:
// a caller walking away (browser abort, shutdown) must not leave this
// goroutine blocked forever on the upstream read, leaking the connection.
func tunnelThroughUpstream(ctx context.Context, conn net.Conn, proxyURL *url.URL, target string) (net.Conn, error) {
	// Watchdog: bound the handshake by the ctx deadline when present, by
	// connectHandshakeTimeout otherwise, and break out early on cancellation.
	deadline := time.Now().Add(connectHandshakeTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("upstream proxy: setting CONNECT deadline: %w", err)
	}
	watchDone := make(chan struct{})
	defer func() {
		_ = conn.SetDeadline(time.Time{})
	}()
	// Registered after the deadline-clear so it runs FIRST on return (LIFO):
	// the watchdog must be stopped before the deadline is cleared, or a
	// late cancellation could poison the just-returned tunnel connection.
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now()) // force pending I/O to fail now
		case <-watchDone:
		}
	}()

	var b strings.Builder
	fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if auth := proxyAuthorization(proxyURL); auth != "" {
		fmt.Fprintf(&b, "Proxy-Authorization: %s\r\n", auth)
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(conn, b.String()); err != nil {
		return nil, fmt.Errorf("upstream proxy: sending CONNECT %s: %w", redacted.Redacted(target), err)
	}

	br := bufio.NewReader(conn)
	req := &http.Request{
		Method: http.MethodConnect,
		// Opaque rather than Path: Request.Write only emits the authority
		// form CONNECT requires, and ReadResponse needs a matching request.
		URL:  &url.URL{Opaque: target},
		Host: target,
	}
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, fmt.Errorf("upstream proxy: reading CONNECT response for %s: %w", redacted.Redacted(target), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("upstream proxy: CONNECT %s returned %s", redacted.Redacted(target), resp.Status)
	}

	// Bytes buffered past the response head already belong to the tunnel;
	// returning the raw conn alone would strand them in the bufio buffer.
	if n := br.Buffered(); n > 0 {
		prefix, err := br.Peek(n)
		if err != nil {
			return nil, fmt.Errorf("upstream proxy: reading tunnel bytes for %s: %w", redacted.Redacted(target), err)
		}
		buffered := make([]byte, n)
		copy(buffered, prefix)
		return &prefixConn{Conn: conn, r: io.MultiReader(bytes.NewReader(buffered), conn)}, nil
	}
	return conn, nil
}

// prefixConn serves the bytes already read past the CONNECT response before
// the underlying connection, making the bufio buffering invisible to the
// tunnelled traffic.
type prefixConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}

// proxyAuthorization builds a Basic Proxy-Authorization header value from the
// URL's user info, or "" when none is configured.
func proxyAuthorization(u *url.URL) string {
	if u.User == nil {
		return ""
	}
	password, _ := u.User.Password()
	token := base64.StdEncoding.EncodeToString([]byte(u.User.Username() + ":" + password))
	return "Basic " + token
}

// parseUpstreamProxyURL normalises a ZEN_UPSTREAM_PROXY value into an HTTP
// proxy URL. Bare "host:port" is accepted and read as http; the port defaults
// to 80 when omitted. Any other scheme is rejected.
func parseUpstreamProxyURL(cfg string) (*url.URL, error) {
	cfg = strings.TrimSpace(cfg)
	if cfg == "" {
		return nil, errors.New("empty value")
	}
	if !strings.Contains(cfg, "://") {
		cfg = "http://" + cfg
	}
	u, err := url.Parse(cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing URL: %w", err)
	}
	if u.Scheme != "http" {
		return nil, fmt.Errorf("unsupported scheme %q: only http upstream proxies are supported", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, errors.New("missing host")
	}
	if !validProxyHost(u.Hostname()) {
		return nil, fmt.Errorf("invalid host %q", u.Hostname())
	}
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), "80")
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("unexpected path or query in proxy URL")
	}
	return u, nil
}

// validProxyHost reports whether host is a plausible proxy address: a hostname
// or IPv4 literal, or a bracketed IPv6 literal. url.Parse is deliberately
// lenient, so a bare character-set check rejects garbage before it reaches
// the dialer.
func validProxyHost(host string) bool {
	if strings.HasSuffix(host, "]") && strings.HasPrefix(host, "[") {
		// Bracketed IPv6 literal; the digits, colons and dots are checked by
		// the dialer when the address is used.
		for _, r := range host[1 : len(host)-1] {
			if !strings.ContainsRune("0123456789abcdefABCDEF:.", r) {
				return false
			}
		}
		return true
	}
	if host == "" {
		return false
	}
	for _, r := range host {
		if !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// hostOnly strips the port from a host:port pair, leaving IPv6 literals
// bracketed form aside as plain addresses.
func hostOnly(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// Filter-list downloads and update checks use DefaultTransport-based clients,
// whose proxy lookup reads the standard environment variables lazily on first
// use - long after package init. Mirroring the upstream setting into those
// variables chains them too; loopback targets stay excluded by net/http.
func init() {
	cfg, source, ok := resolveUpstreamConfig()
	if !ok {
		return
	}
	u, err := parseUpstreamProxyURL(cfg)
	if err != nil {
		log.Printf("upstream proxy: ignoring invalid %s=%q from %s: %v", upstreamProxyEnv, cfg, source, err)
		return
	}
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		if err := os.Setenv(name, u.String()); err != nil {
			log.Printf("upstream proxy: setting %s: %v", name, err)
		}
	}
}
