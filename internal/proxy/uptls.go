package proxy

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"

	utls "github.com/refraction-networking/utls"

	"github.com/irbis-sh/zen-desktop/internal/redacted"
)

// HTTPS re-originated by the MITM forwards the client's own ClientHello
// instead of a canned fingerprint profile: the raw record is captured before
// TLS termination and replayed upstream through uTLS's RawClientHello/
// ApplyPreset, so whatever browser or tool made the request keeps its exact
// TLS fingerprint end to end. No profile is invented or selected here.

// mimicHelloKey carries the per-connection raw ClientHello record from the
// connect handler into the transport's DialTLSContext. The raw bytes - not a
// parsed spec - are carried because ApplyPreset mutates the spec it is given
// (it fills key-share public keys in place), so a parsed spec must never be
// reused across handshakes: every upstream dial parses a fresh spec from the
// raw hello instead.
type mimicHelloKey struct{}

// peekClientHello reads the first TLS record(s) off conn before termination
// and returns them verbatim (record headers included, the format
// RawClientHello expects) plus a connection that replays the peeked bytes.
// A ClientHello fragmented across several TLS records is reassembled by
// reading further records - real clients normally send it in one record, but
// a fragmented one must not be silently downgraded to a non-mirrored
// handshake. Non-handshake first records (plain traffic into a TLS tunnel)
// fail, replaying what was read.
func peekClientHello(conn net.Conn) (raw []byte, peeked net.Conn, err error) {
	const (
		maxRecords     = 8
		maxHelloBodies = 1 << 16 // 64 KiB, far above any real hello
	)

	var stream []byte // raw record stream, replayed verbatim on the inbound leg
	var bodies []byte // concatenated record payloads, for handshake accounting

	for records := 0; records < maxRecords; records++ {
		var head [5]byte
		if _, err := io.ReadFull(conn, head[:]); err != nil {
			return nil, &peekedConn{Conn: conn, buf: stream}, err
		}
		if head[0] != 0x16 { // not a handshake record
			stream = append(stream, head[:]...)
			return nil, &peekedConn{Conn: conn, buf: stream},
				fmt.Errorf("first record is not a handshake record (type %d)", head[0])
		}
		body := make([]byte, binary.BigEndian.Uint16(head[3:5]))
		if _, err := io.ReadFull(conn, body); err != nil {
			return nil, &peekedConn{Conn: conn, buf: stream}, err
		}
		stream = append(stream, head[:]...)
		stream = append(stream, body...)
		bodies = append(bodies, body...)

		// A handshake message is a 1-byte type, a 3-byte big-endian length,
		// then the body. The ClientHello is the first message a client sends.
		if len(bodies) < 4 {
			continue
		}
		if bodies[0] != 0x01 { // not a client hello
			return nil, &peekedConn{Conn: conn, buf: stream},
				fmt.Errorf("first handshake message is not a client hello (type %d)", bodies[0])
		}
		helloLen := int(bodies[1])<<16 | int(bodies[2])<<8 | int(bodies[3])
		if len(bodies)-4 >= helloLen {
			// Canonicalize: hand the hello downstream as a single record
			// (RawClientHello parses one record), no matter how the client
			// fragmented it. Record boundaries are not a fingerprint
			// dimension - the handshake message content is unchanged.
			msg := bodies[:4+helloLen]
			canonical := make([]byte, 0, 5+len(msg))
			canonical = append(canonical, head[0], head[1], head[2])
			canonical = append(canonical, byte(len(msg)>>8), byte(len(msg)&0xff))
			canonical = append(canonical, msg...)
			return canonical, &peekedConn{Conn: conn, buf: stream}, nil
		}
		if len(bodies) > int(maxHelloBodies) {
			return nil, &peekedConn{Conn: conn, buf: stream},
				fmt.Errorf("client hello exceeds %d bytes", int(maxHelloBodies))
		}
	}
	return nil, &peekedConn{Conn: conn, buf: stream},
		fmt.Errorf("client hello not complete after %d records", maxRecords)
}

// peekedConn replays the peeked bytes before passing reads through.
type peekedConn struct {
	net.Conn
	buf []byte
	off int
}

func (c *peekedConn) Read(p []byte) (int, error) {
	if c.off < len(c.buf) {
		n := copy(p, c.buf[c.off:])
		c.off += n
		return n, nil
	}
	return c.Conn.Read(p)
}

// extractMimicSpec turns the captured record into a mirrored ClientHelloSpec.
// Unknown extensions are passed through (AllowBluntMimicry) so nothing the
// client sent is dropped, except session credentials that cannot survive a
// MITM relay (see dropInvalidCredentials) and the ALPN alignment below. A
// failure yields nil, and the upstream handshake falls back to refusing the
// dial rather than to any invented fingerprint.
func extractMimicSpec(raw []byte, inboundH2 bool) *utls.ClientHelloSpec {
	if len(raw) == 0 {
		return nil
	}
	f := utls.Fingerprinter{AllowBluntMimicry: true}
	spec, err := f.RawClientHello(raw)
	if err != nil {
		log.Printf("mimic: extracting ClientHello spec: %v; falling back", err)
		return nil
	}
	alignSpecALPN(spec, inboundH2)
	// [2026-10-08 诊断] ZEN_FORCE_H1_OUT=1: 出站 ALPN 强制只报 http/1.1（复刻
	// h2-mirror 之前的行为），用于 A/B 验证 YouTube 对 h1 客户端的广告投放。
	// 仅诊断会话启用，默认关闭。
	if os.Getenv("ZEN_FORCE_H1_OUT") == "1" {
		alignSpecALPN(spec, false)
		log.Printf("mimic: ZEN_FORCE_H1_OUT=1, outbound ALPN forced to http/1.1")
	}
	dropInvalidCredentials(spec)
	if !sanitizeSpecForGo(spec) {
		log.Printf("mimic: mirrored hello unusable by the Go TLS stack; refusing to dial")
		return nil
	}
	return spec
}

// dropInvalidCredentials removes session-credential extensions from the
// mirrored spec. Their payloads are bound to the original TLS endpoint - the
// browser's past sessions with the origin - and are meaningless, invalid, or
// actively harmful when replayed from a MITM: resumption identities the
// origin cannot decrypt (strict servers answer with illegal_parameter), ECH
// ciphertext encrypted under the origin's keys we do not hold, and an
// early-data flag with no usable pre-shared key behind it. Dropping them
// makes the relayed handshake an honest fresh full handshake that keeps the
// client's structural fingerprint (extensions, curves, ciphers, ALPN).
// The session-ticket support declaration (0x23) is kept: it advertises a
// capability, it carries no credential; early_data (0x2a) without a pre-shared
// key is a pure capability declaration servers ignore (RFC 8446), and
// browsers/curl send it unconditionally - so it stays to keep the fingerprint
// verbatim.
func dropInvalidCredentials(spec *utls.ClientHelloSpec) {
	const (
		extECH  = 0xfe0d
		extALPS = 0x44cd // application_settings (RFC 8879 / ALPS)
	)
	kept := make([]utls.TLSExtension, 0, len(spec.Extensions))
	for _, ext := range spec.Extensions {
		switch e := ext.(type) {
		case utls.PreSharedKeyExtension: // interface: resumption identity + binders
			_ = e
			continue
		case *utls.GenericExtension:
			if e.Id == extECH {
				continue
			}
			if e.Id == extALPS {
				// 2026-10-07 深挖实锤：ALPS 声明"客户端栈会处理服务器 Encrypted-
				// Extensions 里的应用设置子消息并按其调整 h2 行为"。中转栈
				// （utls+x/net 或 fhttp2）不履行该声明：重放 0x44CD 后 google
				// 系前端（BoringSSL）按 ALPS 分支走 TLS 状态机，与我们错位，
				// 回 unexpected_message（对端 alert 10）——h2 会话建立即被拒。
				// 逐扩展二分实锤：仅剥 0x44CD，youtube 全域 h2 会话 200；
				// 其余指纹（ciphers/组/ALPN/顺序/keyshare/ECH 外的扩展）原样。
				// 这是对"无法履行的能力声明"的剥除（与 ECH 同型），非针对特定站。
				continue
			}
		}
		kept = append(kept, ext)
	}
	spec.Extensions = kept
}

// sanitizeSpecForGo drops key-exchange groups the Go TLS stack cannot
// negotiate on the client's behalf - notably the post-quantum hybrids
// (X25519MLKEM768 et al.) browsers ship but Go cannot generate key shares
// for. Everything else keeps the client's order verbatim. This is a
// transport-capability trim: the server simply picks from the remaining
// groups. Returns false when nothing usable remains.
func sanitizeSpecForGo(spec *utls.ClientHelloSpec) bool {
	usable := func(c utls.CurveID) bool {
		switch c {
		case utls.X25519, utls.CurveP256, utls.CurveP384, utls.CurveP521:
			return true
		}
		return isGREASE(uint16(c))
	}

	for _, ext := range spec.Extensions {
		switch e := ext.(type) {
		case *utls.SupportedCurvesExtension:
			kept := e.Curves[:0]
			for _, c := range e.Curves {
				if usable(c) {
					kept = append(kept, c)
				}
			}
			e.Curves = kept
		case *utls.KeyShareExtension:
			kept := e.KeyShares[:0]
			for _, k := range e.KeyShares {
				if usable(k.Group) {
					kept = append(kept, k)
				}
			}
			e.KeyShares = kept
		}
	}

	for _, ext := range spec.Extensions {
		if curves, ok := ext.(*utls.SupportedCurvesExtension); ok && len(curves.Curves) == 0 {
			return false
		}
	}
	return true
}

// isGREASE reports whether the value is a GREASE placeholder pattern
// (0x?a?a per RFC 8701). uTLS regenerates real GREASE values from these.
func isGREASE(v uint16) bool {
	return (v & 0x0f0f) == 0x0a0a
}

// alignSpecALPN aligns the mirrored ALPN with the inbound connection's
// negotiated protocol, so the origin sees the same protocol the client used
// end to end (2026-10-07 h2-mirror). An h2 client keeps its verbatim ALPN -
// the h2 leg (reoriginate.go) speaks HTTP/2 with the client's own fingerprint
// parameters. An HTTP/1.1 client keeps only http/1.1, mirroring the protocol
// it actually used rather than the capabilities it advertised. This is a
// transport-capability alignment, not a fingerprint edit: a hello advertising
// only h2 loses its ALPN extension rather than being rewritten.
func alignSpecALPN(spec *utls.ClientHelloSpec, inboundH2 bool) {
	if inboundH2 {
		return
	}
	for i, ext := range spec.Extensions {
		alpn, ok := ext.(*utls.ALPNExtension)
		if !ok {
			continue
		}
		kept := make([]string, 0, len(alpn.AlpnProtocols))
		for _, proto := range alpn.AlpnProtocols {
			if proto != "h2" {
				kept = append(kept, proto)
			}
		}
		if len(kept) == 0 {
			spec.Extensions = append(spec.Extensions[:i], spec.Extensions[i+1:]...)
		} else {
			alpn.AlpnProtocols = kept
		}
		break
	}
}

// dialUpstreamMimicTLS dials addr through the upstream-chain tunnel (or the
// plain dialer without a chain) and completes the TLS handshake with the
// client's mirrored ClientHello. There is deliberately no downgrade path to
// stock Go TLS: a handshake that cannot be carried by the mirror must fail
// loudly (per-connection 502, visible in the log) instead of silently
// surfacing the Go fingerprint to the origin. After the root-cause fixes
// (fresh spec per dial, no cross-endpoint credentials) the origin-rejection
// path is expected to be empty; a single fresh-spec mirror retry absorbs
// transient network noise without ever leaving the mirror.
func (p *Proxy) dialUpstreamMimicTLS(ctx context.Context, network, addr string, stockCfg *tls.Config) (net.Conn, error) {
	conn, err := p.dialMirrorOnce(ctx, network, addr, stockCfg)
	if err == nil {
		return conn, nil
	}
	// One fresh-spec mirror retry: transient upstream noise is absorbed
	// while the fingerprint stays the client's own, byte for byte.
	log.Printf("mimic: first mirror dial failed, retrying with a fresh mirror: %v", err)
	return p.dialMirrorOnce(ctx, network, addr, stockCfg)
}

// dialMirrorOnce performs one full mirror dial: a fresh spec parsed from the
// captured hello, a fresh raw connection, the mirrored handshake.
func (p *Proxy) dialMirrorOnce(ctx context.Context, network, addr string, stockCfg *tls.Config) (net.Conn, error) {
	spec := p.freshSpecFrom(ctx)
	if spec == nil {
		return nil, fmt.Errorf("no mirrored hello captured; refusing to dial %s with a non-mirrored fingerprint", redacted.Redacted(hostOf(addr)))
	}
	return p.dialWithHello(ctx, network, addr, stockCfg, spec)
}

// dialWithHello completes the upstream handshake with the given mirrored spec.
func (p *Proxy) dialWithHello(ctx context.Context, network, addr string, stockCfg *tls.Config, spec *utls.ClientHelloSpec) (net.Conn, error) {
	raw, err := p.dialUpstreamRaw(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	conn := utls.UClient(raw, mirrorTLSConfig(addr, stockCfg), utls.HelloCustom)
	if err := conn.ApplyPreset(spec); err != nil {
		raw.Close()
		return nil, fmt.Errorf("mimic TLS preset(%s): %w", redacted.Redacted(hostOf(addr)), err)
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, fmt.Errorf("mimic TLS handshake(%s): %w", redacted.Redacted(hostOf(addr)), err)
	}
	// ZEN_DEBUG_MIMIC=1：每个镜像出站连接的协商结果取证（h2 转发保真验证用）
	if os.Getenv("ZEN_DEBUG_MIMIC") == "1" {
		log.Printf("mimic: dial %s negotiated %q", hostOf(addr), conn.ConnectionState().NegotiatedProtocol)
	}
	return conn, nil
}

// dialUpstreamRaw opens the raw connection to addr: through the upstream-chain
// tunnel when a chain is configured, plain otherwise.
func (p *Proxy) dialUpstreamRaw(ctx context.Context, network, addr string) (net.Conn, error) {
	if p.upstreamChain != nil {
		return p.upstreamChain.DialContext(ctx, network, addr)
	}
	return p.netDialer.DialContext(ctx, network, addr)
}

// mirrorTLSConfig builds the uTLS config for an upstream handshake: SNI from
// the target, verification mode and root pool mirrored from the caller's
// live TLSClientConfig (nil means system defaults).
func mirrorTLSConfig(addr string, stockCfg *tls.Config) *utls.Config {
	cfg := &utls.Config{ServerName: hostOf(addr)}
	if stockCfg != nil {
		cfg.InsecureSkipVerify = stockCfg.InsecureSkipVerify //nolint:gosec -- mirrors the caller's transport config
		cfg.RootCAs = stockCfg.RootCAs
	}
	return cfg
}

func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// freshSpecFrom parses a fresh mirrored spec from the raw hello carried in
// the context. Parsing per dial - instead of reusing a parsed spec - is
// required because ApplyPreset fills key-share public keys into the spec it
// is given; a second dial on a mutated spec would skip key generation and
// fail with an internal error. Returns nil when no hello was captured.
func (p *Proxy) freshSpecFrom(ctx context.Context) *utls.ClientHelloSpec {
	raw, ok := ctx.Value(mimicHelloKey{}).([]byte)
	if !ok || len(raw) == 0 {
		return nil
	}
	inboundH2 := false
	if facts := connFactsFromContext(ctx); facts != nil {
		inboundH2 = facts.inboundH2
	}
	return extractMimicSpec(raw, inboundH2)
}

// newMimicContext attaches the raw captured hello and the connection's
// protocol facts to a request context.
func newMimicContext(ctx context.Context, helloRaw []byte, facts *connFacts) context.Context {
	if len(helloRaw) > 0 {
		ctx = context.WithValue(ctx, mimicHelloKey{}, helloRaw)
	}
	if facts != nil {
		ctx = context.WithValue(ctx, connFactsKey{}, facts)
	}
	return ctx
}
