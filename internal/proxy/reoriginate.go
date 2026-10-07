package proxy

// Outbound protocol replay (2026-10-07 h2-mirror). The chained HTTPS leg no
// longer speaks HTTP/1.1 unconditionally: the inbound connection's negotiated
// protocol decides the outbound one. An HTTP/2 client - every modern browser
// - is re-originated over HTTP/2 whose fingerprint parameters are the ones
// the client itself put on the wire and the tap captured (httptap.go):
// SETTINGS values and entry order, the connection-level WINDOW_UPDATE,
// PRIORITY frames, the pseudo-header order and the header order. An HTTP/1.1
// client stays on HTTP/1.1. The TLS layer below remains the ClientHello
// mirror (uptls.go), whose ALPN now passes through verbatim.
//
// Wire format by bogdanfinn/fhttp's http2 transport, the same stack tls-client
// uses for browser-grade h2 fingerprints; every knob it exposes is driven
// from captured facts, never from a canned profile. Known replay gaps (honest
// baseline): fhttp cannot emit PRIORITY_UPDATE frames (RFC 9218) or control
// HPACK index representations, mid-connection WINDOW_UPDATEs are flow-control
// driven, and a client that never sends a connection-level WINDOW_UPDATE gets
// fhttp's default one.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"

	fhttp "github.com/bogdanfinn/fhttp"
	fhttp2 "github.com/bogdanfinn/fhttp/http2"
	bfutls "github.com/bogdanfinn/utls"
	utls "github.com/refraction-networking/utls"

	"github.com/irbis-sh/zen-desktop/internal/redacted"
)

// mimicRoundTripper is the chained-HTTPS RoundTripper. It rides the mirror
// TLS dialer (dialUpstreamMimicTLS / dialWithHello) and routes by protocol.
type mimicRoundTripper struct {
	proxy *Proxy
	// stockTLSCfg mirrors the stock transport's live TLSClientConfig so
	// verification customisations made after wiring are honoured, matching
	// the previous transport's behavior.
	stockTLSCfg func() *tls.Config

	mu     sync.Mutex
	h2t    map[string]*fhttp2.Transport // per-target h2 transports (knobs baked at first dial)
	h1Only map[string]bool              // targets whose wire path cannot honor h2
	h1t    *fhttp.Transport             // shared h1-only transport
}

func newMimicRoundTripper(p *Proxy, stockTLSCfg func() *tls.Config) *mimicRoundTripper {
	m := &mimicRoundTripper{
		proxy:       p,
		stockTLSCfg: stockTLSCfg,
		h2t:         make(map[string]*fhttp2.Transport),
		h1Only:      make(map[string]bool),
	}
	// TLSNextProto emptied on purpose: the h1 leg must never upgrade to Go's
	// own h2 - h2 belongs to the fhttp transport with the replayed fingerprint.
	// DisableCompression on purpose: the captured request is authoritative -
	// the proxy must never inject an Accept-Encoding the client didn't send
	// (and therefore never transparently decompress a response the client
	// asked for in encoded form). Note fhttp's TLS surface is built on
	// bogdanfinn/utls, hence the types.
	m.h1t = &fhttp.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// The h2 leg's ALPN fallback hands its pre-dialed h1-aligned
			// mirror connection over through the request context.
			if h := dialHandoffFromContext(ctx); h != nil && h.conn != nil {
				c := h.conn
				h.conn = nil
				return c, nil
			}
			return p.dialUpstreamMimicTLS(ctx, network, addr, stockTLSCfg())
		},
		Proxy:                 nil,
		TLSNextProto:          map[string]func(string, *bfutls.Conn) fhttp.RoundTripper{},
		DisableCompression:    true,
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       chainedIdleConnTimeout,
		ResponseHeaderTimeout: chainedResponseHeaderTimeout,
	}
	return m
}

func (m *mimicRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return nil, fmt.Errorf("mimic round tripper used for %q URL", redacted.Redacted(req.URL))
	}
	facts := connFactsFromContext(req.Context())
	if facts != nil {
		facts.waitReady(req.Context())
	}
	if facts != nil && facts.inboundH2 {
		return m.roundTripH2(req, facts)
	}
	return m.roundTripH1(req)
}

// roundTripH2 re-originates an h2 client's request on the fhttp http2
// transport whose fingerprint knobs were baked from this target's first
// captured connection.
func (m *mimicRoundTripper) roundTripH2(req *http.Request, facts *connFacts) (*http.Response, error) {
	addr := canonicalH2Addr(req.URL.Host)
	if m.isH1Only(addr) {
		return m.roundTripH1(req)
	}
	t2 := m.h2TransportFor(addr, facts)
	freq := toFhttpRequest(req, facts)
	resp, err := t2.RoundTrip(freq)
	if err == nil {
		return fromFhttpResponse(resp, req), nil
	}
	var alpnErr *alpnFallbackError
	// 握手告警形态：对端 ALPN 配置与供给零交集时直接告警断连（连"无 ALPN 完成"
	// 都没有）——同样按可回退处理（2026-10-07）。两种文案都要认：
	//   复数 "application protocols"：Go 系服务端视角（handshake_server.go:362）
	//   单数 "no application protocol"：utls 客户端收到对端 alert 120 后的本地文案
	//   （refraction utls alert.go:97）——2026-10-07 审计实锤：只匹配复数时，
	//   mimic 的 utls 腿收到告警不回退，502 且 host 被误入透明名单。
	// 会话告警形态（2026-10-07 晚，YouTube 实测）：镜像 hello 握手成功、h2 会话建立后
	// google 前端在 TLS 层回 unexpected_message（对端 alert 10）——重放 hello+重放 h2
	// 指纹的会话特征被其 BoringSSL 栈拒收。h1 镜像腿（同指纹仅 ALPN 对齐+原生 h1 会话）
	// 对同一站全程无拒（旧版为证），故同样按可回退处理，而非 502+永久黑名单。
	if !isAlpnFallbackShape(err, &alpnErr) {
		return nil, err
	}
	if alpnErr == nil {
		// 诊断：会话期被拒的 host 定性（低频路径，打明文 host）
		log.Printf("mimic: session-level TLS alert on %s; falling back to the h1 mirror leg", redacted.Redacted(addr))
	}
	// ALPN-adaptive fallback (2026-10-07 outage fix): redial once with the
	// h1-aligned mirror spec and hand the connection to the h1 transport.
	// The TLS fingerprint stays the client's own; only the protocol follows
	// what the wire path can serve. Remember the target so later requests
	// skip the doomed h2 handshake.
	if alpnErr != nil {
		log.Printf("%s", alpnErr.Error())
	} else {
		log.Printf("mimic: %s rejected the h2 offer at handshake; falling back to the h1 mirror leg", redacted.Redacted(addr))
	}
	m.markH1Only(addr)
	spec := extractMimicSpec(facts.helloRaw, false)
	if spec == nil {
		return nil, fmt.Errorf("mimic: no h1-aligned mirrored hello for %s after ALPN fallback", redacted.Redacted(addr))
	}
	conn, derr := m.proxy.dialWithHello(req.Context(), "tcp", addr, m.stockTLSCfg(), spec)
	if derr != nil {
		return nil, derr
	}
	handoffCtx := context.WithValue(req.Context(), dialHandoffKey{}, &dialHandoff{conn: conn})
	freq1 := toFhttpRequest(req.WithContext(handoffCtx), facts)
	resp1, err1 := m.h1t.RoundTrip(freq1)
	if err1 != nil {
		return nil, err1
	}
	return fromFhttpResponse(resp1, req), nil
}

func (m *mimicRoundTripper) roundTripH1(req *http.Request) (*http.Response, error) {
	facts := connFactsFromContext(req.Context())
	freq := toFhttpRequest(req, facts)
	resp, err := m.h1t.RoundTrip(freq)
	if err != nil {
		return nil, err
	}
	return fromFhttpResponse(resp, req), nil
}

// h2TransportFor returns the per-target http2 transport, building it on first
// use with the fingerprint knobs captured from the triggering connection.
// Per-address baking matches the ClientHello mirror's pooling semantics: a
// pooled connection keeps the fingerprint of the dial that created it, and
// one browser produces identical facts on every connection, so reuse is
// consistent. The map is capped to bound memory on hostile host variety.
func (m *mimicRoundTripper) h2TransportFor(addr string, facts *connFacts) *fhttp2.Transport {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.h2t[addr]; ok {
		return t
	}
	if len(m.h2t) >= 1024 {
		m.h2t = make(map[string]*fhttp2.Transport)
	}

	helloRaw := append([]byte(nil), facts.helloRaw...)
	inboundH2 := facts.inboundH2
	// fhttp's TLS surface is bogdanfinn/utls; mirror the caller's
	// verification settings into it (used for server-push authorization
	// checks - the dial itself verifies in dialWithHello).
	tlsCfg := &bfutls.Config{}
	if stock := m.stockTLSCfg(); stock != nil {
		tlsCfg.InsecureSkipVerify = stock.InsecureSkipVerify //nolint:gosec -- mirrors the caller's transport config
		tlsCfg.RootCAs = stock.RootCAs
	}
	t2 := &fhttp2.Transport{
		TLSClientConfig: tlsCfg,
		// Same rule as the h1 leg: never inject Accept-Encoding the client
		// didn't send (replay fidelity), so never transparently decompress.
		DisableCompression: true,
		DialTLS: func(network, dialAddr string, _ *bfutls.Config) (net.Conn, error) {
			// No request context reaches transport-internal dials; the
			// dial timeout bounds them and the mirror retry inside
			// dialUpstreamMimicTLS is replayed manually via dialWithHello.
			spec := extractMimicSpec(helloRaw, inboundH2)
			if spec == nil {
				return nil, fmt.Errorf("mimic: no usable mirrored hello for %s", redacted.Redacted(dialAddr))
			}
			conn, err := m.proxy.dialWithHello(context.Background(), network, dialAddr, m.stockTLSCfg(), spec)
			if err != nil {
				return nil, err
			}
			// The mirror offers the client's verbatim ALPN. An origin that
			// negotiated anything but h2 cannot carry the h2 preface - report
			// it as an ALPN fallback signal (roundTripH2 redials on the h1
			// mirror leg) instead of speaking the wrong protocol into the
			// connection.
			proto := negotiatedProto(conn)
			if proto != "h2" {
				conn.Close()
				return nil, &alpnFallbackError{addr: dialAddr, proto: proto, spec: spec, helloLen: len(helloRaw)}
			}
			return conn, nil
		},
		IdleConnTimeout: chainedIdleConnTimeout,
		PushHandler:     &fhttp2.DefaultPushHandler{},
	}
	t2.Settings, t2.SettingsOrder = settingsFromFacts(facts)
	t2.PseudoHeaderOrder = append([]string(nil), facts.pseudoOrder...)
	t2.ConnectionFlow = facts.connWindow
	t2.Priorities = prioritiesFromFacts(facts)
	t2.HeaderPriority = headerPriorityFromFacts(facts)
	m.h2t[addr] = t2
	return t2
}

func alpnOf(spec *utls.ClientHelloSpec) string {
	for _, ext := range spec.Extensions {
		if a, ok := ext.(*utls.ALPNExtension); ok {
			return fmt.Sprintf("%v", a.AlpnProtocols)
		}
	}
	return "<ALPN-LOST>"
}

func settingsFromFacts(facts *connFacts) (map[fhttp2.SettingID]uint32, []fhttp2.SettingID) {
	if len(facts.settings) == 0 {
		return nil, nil
	}
	settings := make(map[fhttp2.SettingID]uint32, len(facts.settings))
	order := make([]fhttp2.SettingID, 0, len(facts.settings))
	for _, s := range facts.settings {
		settings[fhttp2.SettingID(s.ID)] = s.Val
		order = append(order, fhttp2.SettingID(s.ID))
	}
	return settings, order
}

func prioritiesFromFacts(facts *connFacts) []fhttp2.Priority {
	if len(facts.priorities) == 0 {
		return nil
	}
	priorities := make([]fhttp2.Priority, 0, len(facts.priorities))
	for _, pf := range facts.priorities {
		priorities = append(priorities, fhttp2.Priority{
			StreamID: pf.StreamID,
			PriorityParam: fhttp2.PriorityParam{
				Exclusive: pf.Exclusive,
				StreamDep: pf.Dep,
				Weight:    pf.Weight,
			},
		})
	}
	return priorities
}

// headerPriorityFromFacts reproduces the PRIORITY field the client attached
// to its first HEADERS frame; a client that sent none gets the zero param,
// which fhttp's IsZero check turns into a plain HEADERS frame (the browser
// shape) instead of the stack's default exclusive dependency.
func headerPriorityFromFacts(facts *connFacts) *fhttp2.PriorityParam {
	if facts.headersPriority != nil {
		return &fhttp2.PriorityParam{
			Exclusive: facts.headersPriority.Exclusive,
			StreamDep: facts.headersPriority.Dep,
			Weight:    facts.headersPriority.Weight,
		}
	}
	return &fhttp2.PriorityParam{}
}

// isAlpnFallbackShape reports whether err is a shape the h2 leg should treat as
// "fall back to the h1 mirror leg": protocol negotiation mismatch (alpnFallbackError),
// handshake-level ALPN alerts from the origin, or session-level TLS alerts that
// strict frontends (google) raise against the replayed-hello h2 session.
func isAlpnFallbackShape(err error, alpnErr **alpnFallbackError) bool {
	if errors.As(err, alpnErr) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "application protocols") ||
		strings.Contains(msg, "no application protocol") ||
		strings.Contains(msg, "unexpected message")
}

// alpnFallbackError reports that the origin could not honor the offered h2
// ALPN ("" = no ALPN at all, "http/1.1" = h1 only). roundTripH2 turns it into
// a redial on the h1 mirror leg - same TLS fingerprint, ALPN aligned to what
// the peer can serve (2026-10-07 outage fix: the hard refusal 502'd every
// HTTPS request when the wire path answered without ALPN).
type alpnFallbackError struct {
	addr     string
	proto    string
	spec     *utls.ClientHelloSpec
	helloLen int
}

func (e *alpnFallbackError) Error() string {
	return fmt.Sprintf("mimic: origin %s negotiated %q with the h2 offer (hello %d bytes, spec ALPN %s); needs the h1 mirror leg", redacted.Redacted(e.addr), e.proto, e.helloLen, alpnOf(e.spec))
}

// dialHandoff carries a pre-dialed, ALPN-h1-aligned mirror connection from
// the h2 leg's fallback redial into the h1 transport's DialTLSContext.
type dialHandoff struct {
	conn net.Conn
}

type dialHandoffKey struct{}

func dialHandoffFromContext(ctx context.Context) *dialHandoff {
	h, _ := ctx.Value(dialHandoffKey{}).(*dialHandoff)
	return h
}

// h1OnlyCache records targets whose wire path could not honor h2, so later
// requests skip the doomed h2 handshake and go straight to the h1 mirror leg.
func (m *mimicRoundTripper) markH1Only(addr string) {
	m.mu.Lock()
	m.h1Only[addr] = true
	m.mu.Unlock()
}

func (m *mimicRoundTripper) isH1Only(addr string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.h1Only[addr]
}

func negotiatedProto(conn net.Conn) string {
	if cs, ok := conn.(interface{ ConnectionState() tls.ConnectionState }); ok {
		return cs.ConnectionState().NegotiatedProtocol
	}
	// 2026-10-07 修复：镜像拨号（dialWithHello）返回的是 refraction-networking
	// *utls.UConn，其 ConnectionState()（经内嵌 *Conn 提升，conn.go:1607）返回
	// utls 自己的 ConnectionState 类型（common.go:241，独立 struct，非 std 别
	// 名）——上面的 std 签名断言对它必然失败，把每次成功的握手都读成 ""。
	// 这正是断网事故 "negotiated \"\"" 的真因：h2 腿被伪影误杀，而不是对端
	// 不回 ALPN。缺了这个分支，所有流量都会被赶进 h1 回退腿，h2 镜像整体失效。
	if uc, ok := conn.(interface{ ConnectionState() utls.ConnectionState }); ok {
		return uc.ConnectionState().NegotiatedProtocol
	}
	return ""
}

// CloseIdleConnections drains both legs' idle pools at Stop time.
func (m *mimicRoundTripper) CloseIdleConnections() {
	m.h1t.CloseIdleConnections()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t2 := range m.h2t {
		t2.CloseIdleConnections()
	}
}

// canonicalH2Addr normalizes an authority to host:port with the https default,
// matching fhttp's internal authorityAddr so transport caching keys line up.
func canonicalH2Addr(host string) string {
	if _, _, err := net.SplitHostPort(host); err != nil {
		return net.JoinHostPort(host, "443")
	}
	return host
}

// toFhttpRequest converts the pipeline's stdlib request into the fhttp shape,
// attaching the captured wire-order keys. The original request's Header map
// is not touched: the order keys are replay plumbing, not request data.
func toFhttpRequest(req *http.Request, facts *connFacts) *fhttp.Request {
	freq := &fhttp.Request{
		Method:        req.Method,
		URL:           req.URL,
		Proto:         req.Proto,
		ProtoMajor:    req.ProtoMajor,
		ProtoMinor:    req.ProtoMinor,
		Body:          req.Body,
		ContentLength: req.ContentLength,
		GetBody:       req.GetBody,
		Host:          req.Host,
	}
	// Carry the request's context: the dialers read the captured hello and
	// facts from it, and cancellation must propagate.
	freq = freq.WithContext(req.Context())
	freq.Header = make(fhttp.Header, len(req.Header)+2)
	for k, vv := range req.Header {
		freq.Header[k] = vv
	}
	// 2026-10-07 POST 400 修复：入站 server 把 "Content-Length" 同时留在 Header 和
	// ContentLength 字段里。fhttp 的 h1 transferWriter 按字段值再写一次 CL —— 线材上
	// 出现两个 Content-Length（实测抓包实锤），严格源站（AWS ELB / 1688 等）按请求
	// 走私防护直接回 400。GET 无 CL 不受影响，这就是"POST 全 400、GET 200"的机制。
	// 值本身由 ContentLength 字段承载（h1 腿写一次；fhttp2 transport.go:1817 在
	// Header 无 content-length 时也按字段补写一次），删头即两侧各恰好一次。
	delete(freq.Header, "Content-Length")
	delete(freq.Header, "Content-Length ")
	if facts != nil && !facts.failed {
		if len(facts.headerOrder) > 0 {
			freq.Header[fhttp.HeaderOrderKey] = mergedHeaderOrder(freq.Header, facts.headerOrder)
		}
		if len(facts.pseudoOrder) > 0 {
			freq.Header[fhttp.PHeaderOrderKey] = append([]string(nil), facts.pseudoOrder...)
		}
	}
	return freq
}

// mergedHeaderOrder maps the captured wire order onto the headers that
// survived filtering: captured names come first in wire order, names the
// filter added (or that the stacks injected) follow alphabetically. The
// captured list is authoritative for everything it names - that is the
// replay; the tail is deterministic rather than invented.
func mergedHeaderOrder(header fhttp.Header, captured []string) []string {
	present := make(map[string]bool, len(header))
	for k := range header {
		if strings.HasPrefix(k, ":") || k == fhttp.HeaderOrderKey || k == fhttp.PHeaderOrderKey {
			continue
		}
		present[strings.ToLower(k)] = true
	}
	order := make([]string, 0, len(captured)+len(present))
	seen := make(map[string]bool, len(captured))
	for _, name := range captured {
		if strings.HasPrefix(name, ":") || seen[name] || !present[name] {
			continue
		}
		order = append(order, name)
		seen[name] = true
	}
	rest := make([]string, 0, len(present))
	for name := range present {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(order, rest...)
}

// fromFhttpResponse converts the fhttp response back to the stdlib shape the
// filter pipeline and writeResp consume. Header and Trailer maps are shared,
// not copied: the fhttp response is owned by this conversion and nobody
// touches it afterwards.
func fromFhttpResponse(fr *fhttp.Response, req *http.Request) *http.Response {
	return &http.Response{
		Status:           fr.Status,
		StatusCode:       fr.StatusCode,
		Proto:            fr.Proto,
		ProtoMajor:       fr.ProtoMajor,
		ProtoMinor:       fr.ProtoMinor,
		Header:           http.Header(fr.Header),
		Body:             fr.Body,
		ContentLength:    fr.ContentLength,
		Trailer:          http.Header(fr.Trailer),
		TransferEncoding: fr.TransferEncoding,
		Uncompressed:     fr.Uncompressed,
		Close:            fr.Close,
		Request:          req,
	}
}
