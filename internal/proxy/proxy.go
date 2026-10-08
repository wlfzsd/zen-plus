package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"

	"github.com/irbis-sh/zen-desktop/internal/process"
	"github.com/irbis-sh/zen-desktop/internal/redacted"
	"github.com/irbis-sh/zen-desktop/internal/taplog"
)

const (
	dialTimeout           = 60 * time.Second
	dialKeepAlive         = 30 * time.Second
	tlsHandshakeTimeout   = 20 * time.Second
	responseHeaderTimeout = 3 * time.Minute
	idleConnTimeout       = 90 * time.Second

	maxIdleConns        = 512
	maxIdleConnsPerHost = 16
)

// certGenerator is an interface capable of generating certificates for the proxy.
type certGenerator interface {
	GetCertificate(host string) (*tls.Certificate, error)
}

// filter is an interface capable of filtering HTTP requests.
type filter interface {
	HandleRequest(*http.Request, process.Info) (*http.Response, error)
	HandleResponse(*http.Request, *http.Response, process.Info) error
}

// ShouldProxyFunc should report whether requests from processPath should be handled by the proxy.
// Returning false makes the proxy tunnel/forward traffic without filtering or MITM.
type ShouldProxyFunc func(processPath string) bool

// Proxy is a forward HTTP/HTTPS proxy that can filter requests.
type Proxy struct {
	filter                filter
	certGenerator         certGenerator
	port                  int
	server                *http.Server
	requestTransport      http.RoundTripper
	requestTransportHTTPS http.RoundTripper
	requestClient         *http.Client
	netDialer             *net.Dialer
	upstreamChain         *chainDialer
	shouldProxy           ShouldProxyFunc
	localHost             string
	localHandler          http.Handler
	transparentHosts      []string
	transparentHostsSet   map[string]struct{}
	transparentHostsMu    sync.RWMutex
	// h2Server serves the inbound MITM leg's HTTP/2 connections (2026-10-07
	// h2-mirror). Registering TLSNextProto["h2"] on the per-connection inner
	// server routes h2 conns here instead of net/http's bundled setup, which
	// is required to tap the plaintext stream: conn.serve type-asserts
	// *tls.Conn before dispatching, so the tap can only live inside this
	// callback, wrapped around the conn handed to ServeConn.
	h2Server http2.Server

	// stopped flips before the proxy starts tearing itself down, so a
	// connection hijacked mid-handshake does not begin serving past Stop.
	stopped atomic.Bool
	// innerServers tracks the per-connection http.Servers created by
	// proxyConnect (one per MITM'd CONNECT). Each of their handlers closes
	// over the Proxy - and through it the whole filter with the rule trees -
	// so they must be reaped on Stop or the retired filter stays reachable
	// for as long as any tunnel survives (2026-10-04 toggle-leak fix).
	innerServersMu sync.Mutex
	innerServers   map[*http.Server]struct{}
	// tunnels tracks every live bidirectional tunnel (transparent CONNECT,
	// websockets). Their goroutines block for the tunnel's lifetime and the
	// frames above them hold the Proxy - so Stop must close the sockets or
	// the retired filter stays reachable even after the inner servers are
	// reaped (2026-10-04 toggle-leak fix, part 2).
	tunnelsMu sync.Mutex
	tunnels   map[*tunnelPair]struct{}
}

// tunnelPair holds both ends of a proxied tunnel for Stop-time reaping.
type tunnelPair struct {
	client net.Conn
	remote net.Conn
}

// NewProxy creates a proxy. localHost and localHandler, when set, name a host
// the proxy answers for itself: requests to it are served by localHandler
// instead of being forwarded upstream. Both must be set together or both left
// empty.
func NewProxy(filter filter, certGenerator certGenerator, port int, shouldProxy ShouldProxyFunc, localHost string, localHandler http.Handler) (*Proxy, error) {
	if filter == nil {
		return nil, errors.New("filter is nil")
	}
	if certGenerator == nil {
		return nil, errors.New("certGenerator is nil")
	}
	if (localHost == "") != (localHandler == nil) {
		return nil, errors.New("localHost and localHandler must be set together")
	}

	p := &Proxy{
		filter:        filter,
		certGenerator: certGenerator,
		port:          port,
		shouldProxy:   shouldProxy,
		localHost:     localHost,
		localHandler:  localHandler,

		transparentHostsSet: make(map[string]struct{}),

		innerServers: make(map[*http.Server]struct{}),
		tunnels:      make(map[*tunnelPair]struct{}),
	}

	p.netDialer = &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: dialKeepAlive,
	}
	p.requestTransport = &http.Transport{
		DialContext:           p.netDialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       idleConnTimeout,
	}
	p.requestClient = &http.Client{
		// Timeout is deliberately unset: it covers the response body read, which would make
		// it exactly the total budget the timeouts above rule out. Ending a transfer is the
		// client's call.
		Transport: p.requestTransport,
		// Let the client handle any redirects.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	applyUpstreamChain(p)

	return p, nil
}

// Start starts the proxy on the given address.
//
// If Proxy was configured with a port of 0, the actual port will be returned.
func (p *Proxy) Start() (int, error) {
	p.server = &http.Server{
		Handler: p,
		// WriteTimeout is deliberately unset: it caps the whole handler, response write
		// included, and would truncate large downloads and long-lived streams.
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", "127.0.0.1", p.port))
	if err != nil {
		return 0, fmt.Errorf("listen: %v", err)
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	log.Printf("proxy listening on port %d", actualPort)

	go func() {
		if err := p.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("serve: %v", err)
		}
	}()

	return actualPort, nil
}

// Stop stops the proxy.
func (p *Proxy) Stop() error {
	// Flip first: a connection hijacked mid-handshake must not begin serving
	// its inner server after the reap below has already run.
	p.stopped.Store(true)

	err := p.shutdownServer()

	// Reap the per-connection inner servers (2026-10-04, toggle-leak fix):
	// Shutdown only closes inbound connections and deliberately ignores
	// hijacked ones - and every MITM'd CONNECT is hijacked, with its inner
	// Serve loop pinning the handler closure and, through the Proxy, the whole
	// retired filter (rule trees). Left alone, a filter-list toggle kept the
	// previous generation's filter alive for as long as any tunnel survived -
	// memory doubled on every disable/enable. Clients reconnect through the
	// fresh proxy immediately, so force-closing the tunnels is correct here.
	p.closeInnerServers()
	// The bidirectional tunnels (transparent CONNECT, websockets) are the
	// second retention path: their blocked caller frames hold the Proxy, and
	// Shutdown ignores their hijacked connections just the same. Close both
	// ends of every live tunnel so all frames unwind (2026-10-04, part 2).
	p.closeTunnels()

	// Shutdown only closes inbound connections, and runs first so that requests still in
	// flight cannot return an upstream connection to the pool after it has been drained.
	// Left alone, those connections and their read and write goroutines outlive the proxy
	// until idleConnTimeout.
	p.requestClient.CloseIdleConnections()
	// The chained mimic transport is not behind requestClient; its idle pool
	// must be drained explicitly.
	if t, ok := p.requestTransport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
	if c, ok := p.requestTransportHTTPS.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}

	if err != nil {
		return fmt.Errorf("shut down server: %v", err)
	}

	return nil
}

// trackInnerServer registers a per-connection server for reaping on Stop.
func (p *Proxy) trackInnerServer(srv *http.Server) {
	p.innerServersMu.Lock()
	defer p.innerServersMu.Unlock()
	p.innerServers[srv] = struct{}{}
}

// untrackInnerServer removes a per-connection server once its Serve loop ended.
func (p *Proxy) untrackInnerServer(srv *http.Server) {
	p.innerServersMu.Lock()
	defer p.innerServersMu.Unlock()
	delete(p.innerServers, srv)
}

// closeInnerServers force-closes every per-connection server still tracked.
func (p *Proxy) closeInnerServers() {
	p.innerServersMu.Lock()
	servers := make([]*http.Server, 0, len(p.innerServers))
	for srv := range p.innerServers {
		servers = append(servers, srv)
	}
	p.innerServersMu.Unlock()
	for _, srv := range servers {
		_ = srv.Close()
	}
}

// trackTunnel registers a live tunnel for reaping on Stop.
func (p *Proxy) trackTunnel(tp *tunnelPair) {
	p.tunnelsMu.Lock()
	defer p.tunnelsMu.Unlock()
	p.tunnels[tp] = struct{}{}
}

// untrackTunnel removes a tunnel once both of its directions finished.
func (p *Proxy) untrackTunnel(tp *tunnelPair) {
	p.tunnelsMu.Lock()
	defer p.tunnelsMu.Unlock()
	delete(p.tunnels, tp)
}

// closeTunnels force-closes both ends of every tracked tunnel, so their
// blocked caller frames (which hold the Proxy) unwind during Stop.
func (p *Proxy) closeTunnels() {
	p.tunnelsMu.Lock()
	pairs := make([]*tunnelPair, 0, len(p.tunnels))
	for tp := range p.tunnels {
		pairs = append(pairs, tp)
	}
	p.tunnelsMu.Unlock()
	for _, tp := range pairs {
		_ = tp.client.Close()
		_ = tp.remote.Close()
	}
}

func (p *Proxy) shutdownServer() error {
	if p.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := p.server.Shutdown(ctx); err != nil {
		// As per documentation:
		// Shutdown does not attempt to close nor wait for hijacked connections such as WebSockets. The caller of Shutdown should separately notify such long-lived connections of shutdown and wait for them to close, if desired. See RegisterOnShutdown for a way to register shutdown notification functions.
		// TODO: implement websocket shutdown
		return fmt.Errorf("server shutdown: %w", err)
	}

	return nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	processInfo, err := process.FindByRequest(r)
	if err != nil {
		log.Printf("error finding request process: %v", err)
	}

	shouldProxy := true
	if p.shouldProxy != nil && processInfo.PID != 0 {
		shouldProxy = p.shouldProxy(processInfo.ExecutablePath)
	}

	if r.Method == http.MethodConnect {
		p.proxyConnect(w, r, processInfo, shouldProxy)
	} else {
		p.proxyHTTP(w, r, processInfo, shouldProxy)
	}
}

// proxyHTTP proxies the HTTP request to the remote server.
func (p *Proxy) proxyHTTP(w http.ResponseWriter, r *http.Request, processInfo process.Info, shouldProxy bool) {
	if p.isLocalEndpoint(r.URL.Hostname()) {
		// Nothing references the local endpoint over plain HTTP; served anyway
		// so the host behaves the same on both schemes.
		p.localHandler.ServeHTTP(w, r)
		return
	}

	if shouldProxy {
		filterResp, err := p.filter.HandleRequest(r, processInfo)
		if err != nil {
			log.Printf("error handling request for %q: %v", redacted.Redacted(r.URL), err)
		}

		if filterResp != nil {
			filterResp.Write(w)
			return
		}
	}

	if _, ok := r.Header["User-Agent"]; !ok {
		// If the outbound request doesn't have a User-Agent header set,
		// don't send the default Go HTTP client User-Agent.
		r.Header.Set("User-Agent", "")
	}

	if isWS(r) {
		p.proxyWebsocket(w, r)
		return
	}

	r.RequestURI = ""
	r.Close = false

	// Check before removeHopHeaders strips Te.
	teTrailers := headerContains(r.Header, "Te", "trailers")

	removeHopHeaders(r.Header)

	if teTrailers {
		r.Header.Set("Te", "trailers")
	}

	var (
		roundTripMutex sync.Mutex
		roundTripDone  bool
	)
	trace := &httptrace.ClientTrace{
		Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
			roundTripMutex.Lock()
			defer roundTripMutex.Unlock()
			if roundTripDone {
				return nil
			}
			h := w.Header()
			for k, vv := range header {
				for _, v := range vv {
					h.Add(k, v)
				}
			}
			w.WriteHeader(code)
			clear(h)
			return nil
		},
	}
	r = r.WithContext(httptrace.WithClientTrace(r.Context(), trace))

	resp, err := p.requestClient.Do(r) // #nosec G704 -- this is a proxy; forwarding requests is its purpose
	roundTripMutex.Lock()
	roundTripDone = true
	roundTripMutex.Unlock()
	if err != nil {
		log.Printf("error making request: %v", redacted.Redacted(err)) // The error might contain information about the hostname we are connecting to.
		writeUpstreamError(w, r, err)
		return
	}
	defer resp.Body.Close()

	removeHopHeaders(resp.Header)

	if shouldProxy {
		if err := p.filter.HandleResponse(r, resp, processInfo); err != nil {
			log.Printf("error handling response by filter: %v", err)
			writeFilterError(w, r, err)
			return
		}
	}

	writeResp(w, resp)
}

// proxyConnect proxies the initial CONNECT and subsequent data between the
// client and the remote server.
func (p *Proxy) proxyConnect(w http.ResponseWriter, connReq *http.Request, processInfo process.Info, shouldProxy bool) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		log.Fatal("http server does not support hijacking")
	}

	clientConn, _, err := hj.Hijack()
	if err != nil {
		log.Printf("hijacking connection(%s): %v", redacted.Redacted(connReq.Host), err)
		return
	}
	defer clientConn.Close()

	host, _, err := net.SplitHostPort(connReq.Host)
	if err != nil {
		log.Printf("splitting host and port(%s): %v", redacted.Redacted(connReq.Host), err)
		return
	}

	// The local-endpoint check runs before every path that could tunnel:
	// a tunnel would dial the hostname for real, and nothing out there
	// answers for it. Checking first also keeps a TLS failure that lands the
	// host on transparentHosts from silently breaking the endpoint until
	// restart.
	isLocal := p.isLocalEndpoint(host)

	if !isLocal && !shouldProxy {
		taplogConn(host, "tunnel", "routing-not-selected")
		p.tunnel(clientConn, connReq)
		return
	}

	if !isLocal && (!p.shouldMITM(host) || net.ParseIP(host) != nil) {
		// TODO: implement upstream certificate sniffing
		// https://docs.mitmproxy.org/stable/concepts-howmitmproxyworks/#complication-1-whats-the-remote-hostname
		taplogConn(host, "tunnel", "latched-or-ip-literal")
		p.tunnel(clientConn, connReq)
		return
	}

	tlsCert, err := p.certGenerator.GetCertificate(host)
	if err != nil {
		log.Printf("getting certificate(%s): %v", redacted.Redacted(connReq.Host), err)
		return
	}

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n")); err != nil {
		log.Printf("writing 200 OK to client(%s): %v", redacted.Redacted(connReq.Host), err)
		return
	}

	// Capture the client's ClientHello before TLS termination: it is replayed
	// verbatim on the upstream leg (mimic), so the browser keeps its own TLS
	// fingerprint end to end. Failure falls back to stock Go TLS - never to a
	// canned profile.
	helloRaw, clientConn, err := peekClientHello(clientConn)
	if err != nil {
		log.Printf("peeking ClientHello(%s): %v", redacted.Redacted(connReq.Host), err)
		helloRaw = nil
	}
	// The raw hello - not a parsed spec - is carried downstream: every upstream
	// dial parses a fresh spec (ApplyPreset mutates specs in place, so a parsed
	// spec must never be reused across handshakes).

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*tlsCert},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS12,
	}

	tlsConn := tls.Server(clientConn, tlsConfig)
	defer tlsConn.Close()

	// Perform the TLS handshake manually so we can capture TLS errors
	// and add the host to transparentHosts before entering the server loop.
	// The local endpoint is exempt: an entry for it is dead weight (isLocal
	// overrides transparentHosts), and because the endpoint never stops being
	// MITM'd, repeated failures would keep appending it without bound.
	if err := tlsConn.HandshakeContext(context.Background()); err != nil {
		if !isLocal && isTLSError(err) {
			log.Printf("adding %s to ignored hosts", redacted.Redacted(host))
			taplog.Log(map[string]any{"kind": "latch", "host": host, "leg": "inbound", "err": err.Error()})
			p.addTransparentHost(host)
		}
		log.Printf("TLS handshake(%s): %v", redacted.Redacted(connReq.Host), err)
		return
	}
	taplogConn(host, "mitm", "")

	ln := newSingleConnListener(tlsConn)

	// Protocol-fact capture (2026-10-07 h2-mirror): every MITM'd connection
	// carries a facts object; HTTP/2 connections additionally get a plaintext
	// tap inside the h2 dispatch below (an h1 conn cannot be tapped without
	// breaking net/http's *tls.Conn dispatch, so h1 keeps its pre-mirror
	// behavior). helloRaw rides on the facts for the per-address outbound
	// transports.
	facts := newConnFacts()
	facts.helloRaw = helloRaw

	var inner http.Handler
	if isLocal {
		// The local endpoint is served directly, not round-tripped: its
		// requests deliberately skip the filter, so no filter-list rule can
		// block Zen's own assets.
		inner = p.localHandler
	} else {
		inner = p.connectHandler(connReq, host, ln, processInfo, helloRaw, facts)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		inner.ServeHTTP(w, req.WithContext(withConnFacts(req.Context(), facts)))
	})

	srv := &http.Server{
		Handler:   handler,
		TLSConfig: tlsConfig,
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateClosed {
				ln.Close()
			}
		},
		ReadHeaderTimeout: 20 * time.Second,
	}
	// Registering the h2 dispatch takes it over from net/http's bundled
	// setup (a non-nil TLSNextProto disables that): the tap can only wrap
	// the conn inside this callback, because conn.serve type-asserts
	// *tls.Conn before dispatching.
	srv.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){
		"h2": func(s *http.Server, c *tls.Conn, _ http.Handler) {
			p.h2Server.ServeConn(newTapConn(c, facts), &http2.ServeConnOpts{
				BaseConfig: s,
				Handler:    handler,
			})
		},
	}

	// Track for the Stop reap (2026-10-04): http.Server.Shutdown ignores
	// hijacked connections, so without this every open tunnel pinned the
	// handler closure - and through it the whole retired filter - past Stop.
	p.trackInnerServer(srv)
	defer p.untrackInnerServer(srv)
	if p.stopped.Load() {
		ln.Close()
		return
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		log.Printf("serving connection(%s): %v", redacted.Redacted(connReq.Host), err)
	}
}

// connectHandler returns an http.Handler that processes requests on a CONNECT-tunnelled TLS connection.
func (p *Proxy) connectHandler(connReq *http.Request, host string, ln *singleConnListener, processInfo process.Info, helloRaw []byte, facts *connFacts) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.URL.Host = connReq.Host
		req.URL.Scheme = "https"
		req.RequestURI = ""
		req.Close = false
		req = req.WithContext(taplog.WithFacts(req.Context(), taplog.Facts{InboundH2: facts.inboundH2}))

		// Filter request, before upgrading to websockets, to match/block wss:// handshake
		filterResp, err := p.filter.HandleRequest(req, processInfo)
		if err != nil {
			log.Printf("handling request for %q: %v", redacted.Redacted(req.URL), err)
		}
		if filterResp != nil {
			writeResp(w, filterResp)
			if filterResp.Body != nil {
				filterResp.Body.Close()
			}
			return
		}

		if _, ok := req.Header["User-Agent"]; !ok {
			// If the outbound request doesn't have a User-Agent header set,
			// don't send the default Go HTTP client User-Agent.
			req.Header.Set("User-Agent", "")
		}

		// WebSocket upgrade is only done over HTTP/1.1.
		if isWS(req) && req.ProtoMajor == 1 {
			p.proxyWebsocketTLS(w, req)
			ln.Close()
			return
		}

		// Check before removeHopHeaders strips Te.
		teTrailers := headerContains(req.Header, "Te", "trailers")

		removeHopHeaders(req.Header)

		if teTrailers {
			req.Header.Set("Te", "trailers")
		}

		// Go's HTTP server always sets a non-nil value for req.Body.
		// RoundTrip interprets a non-nil Body as chunked, which causes strict servers to reject the request.
		if req.ContentLength == 0 {
			req.Body = nil
		}

		var (
			roundTripMutex sync.Mutex
			roundTripDone  bool
		)
		trace := &httptrace.ClientTrace{
			Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
				roundTripMutex.Lock()
				defer roundTripMutex.Unlock()
				if roundTripDone {
					return nil
				}
				h := w.Header()
				for k, vv := range header {
					for _, v := range vv {
						h.Add(k, v)
					}
				}
				w.WriteHeader(code)
				// Clear headers, which is not done automatically by ResponseWriter.WriteHeader() for 1xx responses.
				clear(h)
				return nil
			},
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

		// HTTPS over an active upstream chain rides the mimic round tripper,
		// which replays this connection's captured ClientHello and - for h2
		// clients - the captured h2 fingerprint parameters; everything else -
		// plain http, and https without a chain - uses the stock transport.
		req = req.WithContext(newMimicContext(req.Context(), helloRaw, facts))
		rt := p.requestTransport
		if req.URL.Scheme == "https" && p.requestTransportHTTPS != nil {
			rt = p.requestTransportHTTPS
		}
		resp, err := rt.RoundTrip(req)
		roundTripMutex.Lock()
		roundTripDone = true
		roundTripMutex.Unlock()
		if err != nil {
			if taplog.WatchedHost(host) {
				taplog.Log(map[string]any{"kind": "rt_error", "host": host, "err": err.Error()})
			}
			if isTLSError(err) {
				log.Printf("adding %s to ignored hosts", redacted.Redacted(host))
				taplog.Log(map[string]any{"kind": "latch", "host": host, "leg": "outbound", "err": err.Error()})
				p.addTransparentHost(host)
			}
			log.Printf("roundtrip(%s): %v", redacted.Redacted(connReq.Host), err)
			writeUpstreamError(w, req, err)
			return
		}
		defer resp.Body.Close()

		removeHopHeaders(resp.Header)

		if err := p.filter.HandleResponse(req, resp, processInfo); err != nil {
			log.Printf("error handling response by filter for %q: %v", redacted.Redacted(req.URL), err)
			writeFilterError(w, req, err)
			return
		}

		writeResp(w, resp)
	})
}

// isLocalEndpoint reports whether host names the proxy's own local endpoint.
func (p *Proxy) isLocalEndpoint(host string) bool {
	return p.localHandler != nil && strings.EqualFold(host, p.localHost)
}

// shouldMITM returns true if the host should be MITM'd.
func (p *Proxy) shouldMITM(host string) bool {
	p.transparentHostsMu.RLock()
	defer p.transparentHostsMu.RUnlock()

	for _, transparentHost := range p.transparentHosts {
		if host == transparentHost || strings.HasSuffix(host, "."+transparentHost) {
			return false
		}
	}

	return true
}

// addTransparentHost adds a host to the list of hosts that should be MITM'd.
// Duplicate entries are skipped: TLS-failing hosts (cert-pinned apps retrying
// over days) used to append a fresh copy of the same host on every failure,
// growing the slice without bound and stretching the linear scan in
// shouldMITM (2026-10-03 memory audit: the only unbounded container in the
// codebase).
func (p *Proxy) addTransparentHost(host string) {
	p.transparentHostsMu.Lock()
	defer p.transparentHostsMu.Unlock()

	if p.transparentHostsSet == nil {
		p.transparentHostsSet = make(map[string]struct{})
	}
	if _, ok := p.transparentHostsSet[host]; ok {
		return
	}
	p.transparentHostsSet[host] = struct{}{}
	p.transparentHosts = append(p.transparentHosts, host)
}

// tunnel tunnels the connection between the client and the remote server
// without inspecting the traffic.
func (p *Proxy) tunnel(w net.Conn, r *http.Request) {
	remoteConn, err := p.tunnelDialContext(r.Context(), "tcp", r.Host) // #nosec G704 -- this is a proxy; forwarding connections is its purpose
	if err != nil {
		log.Printf("dialing remote(%s): %v", redacted.Redacted(r.Host), err)
		w.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer remoteConn.Close()

	// A dial that lands after Stop began must not open a tunnel past it.
	if p.stopped.Load() {
		return
	}

	if _, err := w.Write([]byte("HTTP/1.1 200 OK\r\n\r\n")); err != nil {
		log.Printf("writing 200 OK to client(%s): %v", redacted.Redacted(r.Host), err)
		return
	}

	// Track for the Stop reap (2026-10-04): the blocked caller frames of this
	// tunnel hold the Proxy, and Shutdown ignores hijacked connections.
	tp := &tunnelPair{client: w, remote: remoteConn}
	p.trackTunnel(tp)
	defer p.untrackTunnel(tp)

	linkBidirectionalTunnel(w, remoteConn)
}

// writeResp writes the response (status code, headers, and body) to the ResponseWriter.
//
// writeResp closes resp.Body to populate trailers for HTTP/1.1 chunked responses.
// The caller's deferred Body.Close is still safe (double close is benign for HTTP response bodies).
func writeResp(w http.ResponseWriter, resp *http.Response) {
	// Announce trailers before writing the status line so net/http can
	// emit a proper Trailer header in the chunked response.
	announcedTrailers := len(resp.Trailer)
	if announcedTrailers > 0 {
		trailerKeys := make([]string, 0, len(resp.Trailer))
		for k := range resp.Trailer {
			trailerKeys = append(trailerKeys, k)
		}
		w.Header().Add("Trailer", strings.Join(trailerKeys, ", "))
	}

	for h, v := range resp.Header {
		for _, vv := range v {
			w.Header().Add(h, vv)
		}
	}

	w.WriteHeader(resp.StatusCode)

	if resp.Body != nil {
		var dst io.Writer = w
		if isStreamingResponse(resp) {
			rc := http.NewResponseController(w)
			dst = &flushWriter{w: w, flush: rc.Flush}
		}
		_, err := io.Copy(dst, resp.Body)
		// Close the body before reading trailers;
		// resp.Trailer is only populated after the body is fully consumed and closed.
		resp.Body.Close()
		if err != nil {
			panic(http.ErrAbortHandler)
		}
	}

	if len(resp.Trailer) > 0 {
		// Force chunking if we saw a response trailer.
		// This prevents net/http from calculating the length for short
		// bodies and adding a Content-Length.
		http.NewResponseController(w).Flush()
	}

	if len(resp.Trailer) == announcedTrailers {
		for h, v := range resp.Trailer {
			for _, vv := range v {
				w.Header().Add(h, vv)
			}
		}
		return
	}
	for h, v := range resp.Trailer {
		for _, vv := range v {
			w.Header().Add(http.TrailerPrefix+h, vv)
		}
	}
}

func linkBidirectionalTunnel(src, dst io.ReadWriter) {
	doneC := make(chan struct{}, 2)
	go tunnelConn(src, dst, doneC)
	go tunnelConn(dst, src, doneC)
	<-doneC
	<-doneC
}

// tunnelConn tunnels the data between src and dst.
func tunnelConn(dst io.Writer, src io.Reader, done chan<- struct{}) {
	if _, err := io.Copy(dst, src); err != nil && !isCloseable(err) {
		log.Printf("copying: %v", err)
	}
	done <- struct{}{}
}

// headerContains returns true if the named header contains the given value
// as a comma-separated token (case-insensitive).
func headerContains(h http.Header, name, value string) bool {
	for _, v := range h[name] {
		for _, s := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(s), value) {
				return true
			}
		}
	}
	return false
}

// isTLSError reports whether err is a TLS protocol error. The proxy treats these
// as a signal that a host cannot be MITM'd and should be tunnelled transparently.
func isTLSError(err error) bool {
	return strings.Contains(err.Error(), "tls: ")
}

// taplogConn emits one connection-mode fact for host to the decision tap
// (2026-10-08 reproduction diagnostics; no-op unless ZEN_DECISION_LOG=1).
// Recorded for watched hosts only: the tunnel modes are exactly the states
// in which nothing is filtered, and latch events are logged separately with
// no host filter.
func taplogConn(host, mode, reason string) {
	if taplog.WatchedHost(host) {
		taplog.Log(map[string]any{"kind": "conn", "host": host, "mode": mode, "reason": reason})
	}
}

// isCloseable returns true if the error is one that indicates the connection
// can be closed.
func isCloseable(err error) (ok bool) {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}

	switch err {
	case io.EOF, io.ErrClosedPipe, io.ErrUnexpectedEOF:
		return true
	default:
		return false
	}
}

// isStreamingResponse reports whether the response should be flushed
// to the client immediately (e.g. Server-Sent Events, chunked streams).
func isStreamingResponse(resp *http.Response) bool {
	if ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); ct == "text/event-stream" {
		return true
	}
	return resp.ContentLength == -1
}

// flushWriter wraps an io.Writer and calls flush after every Write
// to ensure streaming data reaches the client without buffering delay.
type flushWriter struct {
	w     io.Writer
	flush func() error
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if n > 0 {
		f.flush()
	}
	return n, err
}

// Hop-by-hop headers. These are removed when sent to the backend.
// As of RFC 7230, hop-by-hop headers are required to appear in the
// Connection header field. These are the headers defined by the
// obsoleted RFC 2616 (section 13.5.1) and are used for backward
// compatibility.
var hopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",      // canonicalized version of "TE"
	"Trailer", // spelling per https://www.rfc-editor.org/errata_search.php?eid=4522
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopHeaders(header http.Header) {
	// RFC 7230, section 6.1: Remove headers listed in the "Connection" header.
	for _, f := range header["Connection"] {
		for _, sf := range strings.Split(f, ",") {
			if sf = strings.TrimSpace(sf); sf != "" {
				header.Del(sf)
			}
		}
	}
	// RFC 2616, section 13.5.1: Remove a set of known hop-by-hop headers.
	// This behavior is superseded by the RFC 7230 Connection header, but
	// preserve it for backwards compatibility.
	for _, h := range hopHeaders {
		header.Del(h)
	}
}
