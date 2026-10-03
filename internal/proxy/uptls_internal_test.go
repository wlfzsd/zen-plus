package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

// captureMirroredSpec simulates the inbound leg: a real client handshake is
// terminated while peekClientHello captures the raw record, which is then
// turned into the mirrored spec exactly like production does. The inbound
// server's GetConfigForClient records the client's original cipher list for
// fidelity comparison.
func captureMirroredSpec(t *testing.T, clientProtos []string) (*utls.ClientHelloSpec, []uint16) {
	t.Helper()

	inner, inbound := net.Pipe()
	var rawHello []byte
	var inboundCiphers []uint16
	done := make(chan struct{})
	go func() {
		defer close(done)
		raw, replayed, err := peekClientHello(inbound)
		if err != nil {
			t.Errorf("peekClientHello: %v", err)
			return
		}
		rawHello = raw
		server := tls.Server(replayed, &tls.Config{ // #nosec G402 -- test pipe
			Certificates: []tls.Certificate{mustCert(t)},
			GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
				inboundCiphers = chi.CipherSuites
				return nil, nil
			},
		})
		if err := server.Handshake(); err != nil {
			t.Errorf("inbound handshake: %v", err)
			return
		}
	}()

	client := tls.Client(inner, &tls.Config{ // #nosec G402 -- test pipe, trust is not under test
		InsecureSkipVerify: true,
		NextProtos:         clientProtos,
	})
	if err := client.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	<-done

	if len(rawHello) == 0 {
		t.Fatal("no ClientHello captured")
	}
	spec := extractMimicSpec(rawHello)
	if spec == nil {
		t.Fatal("extractMimicSpec returned nil for a plain Go hello")
	}
	return spec, inboundCiphers
}

// captureHelloBytes captures a real resumption hello through the production
// inbound path (peekClientHello) using two stdlib handshakes, so the second
// hello carries PSK/session-ticket extensions.
func captureHelloBytes(t *testing.T) []byte {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var rawHello []byte
	go func() {
		for i := 0; i < 2; i++ {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			raw, replayed, perr := peekClientHello(conn)
			if perr == nil {
				rawHello = raw
				server := tls.Server(replayed, &tls.Config{ // #nosec G402 -- test listener
					Certificates: []tls.Certificate{mustCert(t)},
				})
				_ = server.Handshake()
				io.Copy(io.Discard, server)
			}
			conn.Close()
		}
	}()

	cache := tls.NewLRUClientSessionCache(4)
	for i := 0; i < 2; i++ {
		client, cerr := tls.DialWithDialer(
			&net.Dialer{Timeout: 5 * time.Second},
			"tcp",
			ln.Addr().String(),
			&tls.Config{ // #nosec G402 -- test listener, trust is not under test
				InsecureSkipVerify: true,
				NextProtos:         []string{"h2", "http/1.1"},
				ClientSessionCache: cache,
				ServerName:         "probe.local",
			})
		if cerr != nil {
			t.Fatalf("client handshake %d: %v", i, cerr)
		}
		client.Close()
	}
	time.Sleep(100 * time.Millisecond)
	if len(rawHello) == 0 {
		t.Fatal("no ClientHello captured")
	}
	return rawHello
}

func mustCert(t *testing.T) tls.Certificate {
	t.Helper()
	cert, err := selfSignedCertGenerator{}.GetCertificate("127.0.0.1")
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	return *cert
}

// TestMimicMirrorPreservesClientHello pins the mirror contract end to end:
// the upstream hello the mirror dials with carries the client's own
// fingerprint - identical cipher list, in order - with only h2 trimmed from
// ALPN and unusable groups dropped, so the re-originated connection keeps
// the client's TLS fingerprint. No canned profile is involved.
func TestMimicMirrorPreservesClientHello(t *testing.T) {
	t.Parallel()

	spec, inboundCiphers := captureMirroredSpec(t, []string{"h2", "http/1.1"})

	// Fidelity: the mirrored spec's cipher list equals the inbound
	// handshake's, in the client's order.
	if len(spec.CipherSuites) != len(inboundCiphers) {
		t.Fatalf("cipher count = %d, inbound = %d", len(spec.CipherSuites), len(inboundCiphers))
	}
	for i, c := range spec.CipherSuites {
		if uint16(c) != inboundCiphers[i] {
			t.Fatalf("cipher[%d] = %#x, inbound = %#x", i, uint16(c), inboundCiphers[i])
		}
	}

	// Only groups the Go TLS stack can negotiate survive sanitization.
	for _, ext := range spec.Extensions {
		if c, ok := ext.(*utls.SupportedCurvesExtension); ok {
			if len(c.Curves) == 0 {
				t.Fatal("mirrored spec lost the curve list")
			}
			for _, g := range c.Curves {
				if g != utls.X25519 && g != utls.CurveP256 && g != utls.CurveP384 && g != utls.CurveP521 && !isGREASE(uint16(g)) {
					t.Fatalf("unsupported group %#x survived sanitization", uint16(g))
				}
			}
		}
	}

	// ALPN trim: h2 removed, http/1.1 kept in order.
	alpnFound := false
	for _, ext := range spec.Extensions {
		if a, ok := ext.(*utls.ALPNExtension); ok {
			alpnFound = true
			if len(a.AlpnProtocols) != 1 || a.AlpnProtocols[0] != "http/1.1" {
				t.Fatalf("mirrored ALPN = %v, want [http/1.1]", a.AlpnProtocols)
			}
		}
	}
	if !alpnFound {
		t.Fatal("mirrored spec lost the ALPN extension")
	}
}

// TestDialUpstreamMimicTLSNoH2 drives the mirror dialer against a h2-capable
// origin: even though the client offered h2, the outbound handshake must
// negotiate http/1.1 (the ALPN trim) and complete.
func TestDialUpstreamMimicTLSNoH2(t *testing.T) {
	t.Parallel()

	helloRaw := captureHelloBytes(t) // client offered h2 + http/1.1

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{ // #nosec G402 -- test server
		Certificates: []tls.Certificate{mustCert(t)},
		NextProtos:   []string{"h2", "http/1.1"},
	}
	server.StartTLS()
	defer server.Close()

	p, err := NewProxy(noopFilter{}, unusedCertGenerator{}, 0, nil, "", nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	ctx := newMimicContext(context.Background(), helloRaw)
	conn, err := p.dialUpstreamMimicTLS(ctx, "tcp", server.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true}) // #nosec G402 -- httptest target, trust is not under test
	if err != nil {
		t.Fatalf("dialUpstreamMimicTLS: %v", err)
	}
	defer conn.Close()

	uc, ok := conn.(*utls.UConn)
	if !ok {
		t.Fatalf("conn is %T, want *utls.UConn", conn)
	}
	if !uc.ConnectionState().HandshakeComplete {
		t.Fatal("handshake not complete")
	}
	if got := uc.ConnectionState().NegotiatedProtocol; got != "http/1.1" {
		t.Fatalf("negotiated protocol = %q, want http/1.1 (h2 must never be negotiated by the mimic path)", got)
	}
}

// TestMimicFreshSpecPerDial pins the root-cause fix for the multi-dial
// corruption: ApplyPreset fills key-share public keys into the spec it is
// given, so reusing one parsed spec across upstream dials made the second
// dial skip key generation and fail with an internal error. The context now
// carries the raw hello and every dial parses a fresh spec; two consecutive
// dials on one connection context must both succeed and produce usable TLS.
// The mirrored spec must also carry no session credentials (PSK identities /
// ECH ciphertext are invalid when replayed from a relay).
func TestMimicFreshSpecPerDial(t *testing.T) {
	t.Parallel()

	helloRaw := captureHelloBytes(t)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p, err := NewProxy(noopFilter{}, unusedCertGenerator{}, 0, nil, "", nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	ctx := newMimicContext(context.Background(), helloRaw)
	addr := server.Listener.Addr().String()
	stock := &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- httptest target, trust is not under test

	for dial := 1; dial <= 2; dial++ {
		conn, derr := p.dialUpstreamMimicTLS(ctx, "tcp", addr, stock)
		if derr != nil {
			t.Fatalf("dial %d: %v", dial, derr)
		}
		uc, ok := conn.(*utls.UConn)
		if !ok {
			t.Fatalf("dial %d: conn is %T", dial, conn)
		}
		if uc.ConnectionState().PeerCertificates == nil {
			t.Fatalf("dial %d: handshake produced no certificates", dial)
		}
		conn.Close()
	}

	spec := extractMimicSpec(helloRaw)
	if spec == nil {
		t.Fatal("spec extraction failed")
	}
	pskModesFound := false
	for _, ext := range spec.Extensions {
		if _, bad := ext.(utls.PreSharedKeyExtension); bad {
			t.Fatal("mirrored spec still carries a pre-shared key identity")
		}
		if _, ok := ext.(*utls.PSKKeyExchangeModesExtension); ok {
			pskModesFound = true
		}
	}
	if !pskModesFound {
		t.Fatal("mirrored spec lost PSK key-exchange modes (capability declaration must stay for fingerprint fidelity)")
	}
}

// TestDialUpstreamMimicTLSRefusesWithoutHello pins the no-downgrade rule:
// without a captured hello there is NO stock-Go-TLS fallback - the dialer
// refuses (both attempts), because an unmirrored handshake would surface the
// Go fingerprint to the origin.
func TestDialUpstreamMimicTLSRefusesWithoutHello(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := NewProxy(noopFilter{}, unusedCertGenerator{}, 0, nil, "", nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	_, err = p.dialUpstreamMimicTLS(context.Background(), "tcp", srv.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true}) // #nosec G402 -- httptest target, trust is not under test
	if err == nil {
		t.Fatal("dial without a captured hello must fail, not fall back to Go TLS")
	}
	if !strings.Contains(err.Error(), "no mirrored hello") {
		t.Fatalf("error = %v, want the no-mirrored-hello refusal", err)
	}
}

// TestDialUpstreamMimicTLSRetriesMirrorOnRejection pins the retry semantics:
// when the origin rejects the first mirrored handshake, the retry is ANOTHER
// mirror dial (client's own fingerprint again), never a Go-TLS downgrade.
// The test server refuses the first ALPN-capable hello and accepts the next.
func TestDialUpstreamMimicTLSRetriesMirrorOnRejection(t *testing.T) {
	t.Parallel()

	helloRaw := captureHelloBytes(t) // mirrored hello offers ALPN http/1.1

	var refusals int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{ // #nosec G402 -- test server
		Certificates: []tls.Certificate{mustCert(t)},
		GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			if atomic.AddInt32(&refusals, 1) == 1 && len(chi.SupportedProtos) > 0 {
				// Refuse only the first mirror attempt.
				return nil, errors.New("first mirror dial refused")
			}
			return nil, nil
		},
	}
	server.StartTLS()
	defer server.Close()

	p, err := NewProxy(noopFilter{}, unusedCertGenerator{}, 0, nil, "", nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	ctx := newMimicContext(context.Background(), helloRaw)
	conn, derr := p.dialUpstreamMimicTLS(ctx, "tcp", server.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true}) // #nosec G402 -- httptest target, trust is not under test
	if derr != nil {
		t.Fatalf("retry after rejection must succeed via the mirror: %v", derr)
	}
	defer conn.Close()

	uc, ok := conn.(*utls.UConn)
	if !ok {
		t.Fatalf("conn is %T, want *utls.UConn", conn)
	}
	// The successful connection must be the mirror (ALPN http/1.1 offered),
	// not a Go-TLS downgrade (which would negotiate no ALPN at all).
	if got := uc.ConnectionState().NegotiatedProtocol; got != "http/1.1" {
		t.Fatalf("negotiated protocol = %q, want http/1.1 (the retried dial must be a mirror)", got)
	}
}

// TestPeekFragmentedClientHello pins the fragmented-hello reassembly: a
// ClientHello split across two TLS records is reassembled by peekClientHello
// and yields a usable mirror spec, instead of degrading to a non-mirrored
// handshake.
func TestPeekFragmentedClientHello(t *testing.T) {
	t.Parallel()

	// Build a real ClientHello record stream with stdlib, then split its
	// handshake body across two records to emulate a fragmented hello.
	full := captureHelloBytes(t)
	if len(full) < 9 {
		t.Fatalf("captured hello too short to fragment: %d", len(full))
	}
	head, body := full[:5], full[5:]
	if body[0] != 0x01 {
		t.Fatalf("unexpected first handshake message type %d", body[0])
	}
	split := 4 + len(body)/2 // handshake header plus half the body in record 1
	rec1Len := split
	rec2Len := len(body) - split
	rec1 := append(append([]byte{}, head[0:3]...), byte(rec1Len>>8), byte(rec1Len&0xff))
	rec1 = append(rec1, body[:split]...)
	rec2 := append([]byte{head[0], head[1], head[2], byte(rec2Len >> 8), byte(rec2Len & 0xff)}, body[split:]...)

	ln, lerr := net.Listen("tcp", "127.0.0.1:0")
	if lerr != nil {
		t.Fatalf("listen: %v", lerr)
	}
	defer ln.Close()

	type result struct {
		raw []byte
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			resCh <- result{err: aerr}
			return
		}
		defer conn.Close()
		raw, _, perr := peekClientHello(conn)
		resCh <- result{raw: raw, err: perr}
	}()

	client, cerr := net.Dial("tcp", ln.Addr().String())
	if cerr != nil {
		t.Fatalf("dial: %v", cerr)
	}
	defer client.Close()
	if _, werr := client.Write(rec1); werr != nil {
		t.Fatalf("write record 1: %v", werr)
	}
	time.Sleep(30 * time.Millisecond) // let the server block on the partial hello
	if _, werr := client.Write(rec2); werr != nil {
		t.Fatalf("write record 2: %v", werr)
	}

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("peekClientHello on a fragmented hello: %v", res.err)
		}
		if len(res.raw) == 0 {
			t.Fatal("no hello bytes reassembled")
		}
		if spec := extractMimicSpec(res.raw); spec == nil {
			t.Fatal("fragmented hello did not yield a mirror spec")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("peekClientHello did not finish")
	}
}
