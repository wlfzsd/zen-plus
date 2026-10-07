package proxy

// Inbound protocol-fact capture (2026-10-07 h2-mirror). The ClientHello mirror
// (uptls.go) replays the client's TLS fingerprint; this layer extends the same
// philosophy one layer up: while the MITM serves an HTTP/2 connection, a
// passive tap records the plaintext facts the client itself put on the wire -
// the SETTINGS frame (values AND entry order), the connection-level
// WINDOW_UPDATE, any PRIORITY frames, and the pseudo-header plus header order
// of the first request. The re-origination layer (reoriginate.go) replays
// these facts outbound, so the origin sees the client's own protocol-layer
// fingerprint, not a canned profile. Nothing is invented here either: a
// capture that cannot complete marks the facts failed and the replay falls
// back honestly. HTTP/1.1 inbound connections cannot be tapped (net/http
// type-asserts *tls.Conn before dispatch), keep their pre-mirror behavior,
// and route to the h1 outbound leg unchanged.

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2/hpack"
)

const (
	h2ClientPreface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

	// Failsafe bounds for the passive parser. Real client handshakes fit in
	// the first few KiB; the caps only ever trigger on garbage or hostile
	// peers, and capture then stops (facts stay what was captured so far).
	tapMaxCaptureBytes = 1 << 20
	tapMaxPriorities   = 64
	tapReadyWait       = 2 * time.Second
)

// h2Setting is one SETTINGS entry in wire order.
type h2Setting struct {
	ID  uint16
	Val uint32
}

// h2PriorityFrame is one PRIORITY frame's payload.
type h2PriorityFrame struct {
	StreamID  uint32
	Exclusive bool
	Dep       uint32
	Weight    uint8
}

// connFacts holds the protocol facts captured from one inbound MITM
// connection. ready is closed once the capture is final (or failed); the
// outbound path waits on it for at most tapReadyWait before dialing, because
// the first request of a connection is the one whose fingerprint matters most.
type connFacts struct {
	ready chan struct{}

	// helloRaw is this connection's captured ClientHello record, carried here
	// so per-address outbound transports can re-parse a fresh spec for their
	// own internal dials (no request context is available at that point).
	helloRaw []byte

	inboundH2 bool
	failed    bool // capture aborted; replay falls back to transport defaults

	// capturing reports whether a tap parser is actually running for this
	// connection: h1 connections cannot be tapped (net/http's *tls.Conn
	// dispatch), so their facts stay empty and waitReady must not block.
	capturing atomic.Bool

	settings   []h2Setting // first client SETTINGS frame, wire order
	connWindow uint32      // connection-level WINDOW_UPDATE increment (0 = none seen)
	priorities []h2PriorityFrame
	prioUpdate int // PRIORITY_UPDATE frames seen (replay gap: fhttp cannot emit them)

	// First request's field order. pseudoOrder is the leading ":name" fields;
	// headerOrder is every regular field name in wire order (lowercase, as
	// HTTP/2 sends them). headersPriority is the PRIORITY field attached to
	// the first HEADERS frame, if the client sent one.
	pseudoOrder     []string
	headerOrder     []string
	headersPriority *h2PriorityFrame

	// blockFields collects the HPACK fields of the block currently being
	// decoded; touched only by the parser goroutine.
	blockFields []hpack.HeaderField

	stopOnce sync.Once
	stop     func() // set by the tap; ends byte capture once facts are final
}

func newConnFacts() *connFacts {
	return &connFacts{ready: make(chan struct{})}
}

// waitReady blocks until the capture is final, the context is done, or the
// ready-wait elapses - whichever comes first. The parser only needs bytes the
// HTTP server has already consumed before dispatching the first request, so
// in practice it resolves in microseconds.
func (f *connFacts) waitReady(ctx context.Context) {
	if f == nil || !f.capturing.Load() {
		return
	}
	timer := time.NewTimer(tapReadyWait)
	defer timer.Stop()
	select {
	case <-f.ready:
	case <-ctx.Done():
	case <-timer.C:
		log.Printf("mimic: inbound protocol-fact capture not settled within %s; replay degrades", tapReadyWait)
	}
}

// finish marks the capture complete and stops the tap.
func (f *connFacts) finish() {
	f.stopOnce.Do(func() {
		if f.stop != nil {
			f.stop()
		}
		close(f.ready)
	})
}

// fail marks the capture aborted and stops the tap.
func (f *connFacts) fail(reason string) {
	f.failed = true
	log.Printf("mimic: inbound protocol-fact capture aborted: %s", reason)
	f.finish()
}

// connFactsKey carries *connFacts from the CONNECT handler into the outbound
// transport, next to the raw ClientHello (mimicHelloKey).
type connFactsKey struct{}

func withConnFacts(ctx context.Context, facts *connFacts) context.Context {
	if facts == nil {
		return ctx
	}
	return context.WithValue(ctx, connFactsKey{}, facts)
}

func connFactsFromContext(ctx context.Context) *connFacts {
	f, _ := ctx.Value(connFactsKey{}).(*connFacts)
	return f
}

// tapConn tees every plaintext byte the HTTP server reads from the client
// into the capture parser. It never blocks or errors the data path: when the
// parser falls behind or capture is done, bytes are simply not recorded. The
// ConnectionState passthrough keeps the x/net HTTP/2 server's TLS inspection
// (and Request.TLS) working through the wrapper.
type tapConn struct {
	net.Conn
	facts *connFacts
	ch    chan []byte

	// endRead closes ch exactly once when the underlying stream ends, so a
	// capture that never completes settles instead of leaking the parser.
	// Only the read loop sends on ch (net/http reads a conn serially), so a
	// close after the last send in that same goroutine is race-free.
	endRead sync.Once

	capturedBytes atomic.Int64
	capturedDone  atomic.Bool
}

func newTapConn(c net.Conn, facts *connFacts) *tapConn {
	t := &tapConn{Conn: c, facts: facts, ch: make(chan []byte, 256)}
	facts.stop = func() { t.capturedDone.Store(true) }
	facts.capturing.Store(true)
	go facts.parse(t.ch)
	return t
}

func (t *tapConn) Read(p []byte) (int, error) {
	n, err := t.Conn.Read(p)
	if n > 0 && !t.capturedDone.Load() && t.capturedBytes.Add(int64(n)) <= tapMaxCaptureBytes {
		chunk := make([]byte, n)
		copy(chunk, p[:n])
		select {
		case t.ch <- chunk:
		default:
			t.facts.fail("parser starved by data path")
		}
	}
	if err != nil {
		t.endRead.Do(func() { close(t.ch) })
	}
	return n, err
}

// ConnectionState forwards the inner *tls.Conn state so HTTP/2 serving
// through the wrapper keeps its TLS context (negotiated protocol checks,
// Request.TLS).
func (t *tapConn) ConnectionState() tls.ConnectionState {
	if cs, ok := t.Conn.(interface{ ConnectionState() tls.ConnectionState }); ok {
		return cs.ConnectionState()
	}
	return tls.ConnectionState{}
}

// parse consumes the recorded byte stream and extracts the connection's h2
// facts. The capture ends as soon as the facts are complete - the parser
// never tracks the connection for life.
func (f *connFacts) parse(ch <-chan []byte) {
	p := &tapParser{f: f, hdec: hpack.NewDecoder(65536, f.onHPACKField)}
	defer f.finish()

	for chunk := range ch {
		p.buf = append(p.buf, chunk...)
		if done := p.consume(); done {
			return
		}
	}
	if !f.failed {
		f.fail("connection ended before capture completed")
	}
}

// onHPACKField collects the fields of the HPACK block currently being decoded.
func (f *connFacts) onHPACKField(hf hpack.HeaderField) {
	f.blockFields = append(f.blockFields, hf)
}

type tapParser struct {
	f           *connFacts
	buf         []byte
	hdec        *hpack.Decoder
	prefaceOK   bool
	gotSettings bool
	frag        []byte // accumulating HEADERS/CONTINUATION block fragment
	inHeaders   bool
}

func (p *tapParser) consume() (done bool) {
	if !p.prefaceOK {
		// The tap only attaches inside the h2 dispatch (proxy.go), so the
		// stream must begin with the client preface; anything else is a
		// capture failure, reported honestly.
		if len(p.buf) < len(h2ClientPreface) {
			return false
		}
		if string(p.buf[:len(h2ClientPreface)]) != h2ClientPreface {
			p.f.fail("stream does not start with the h2 client preface")
			return true
		}
		p.buf = p.buf[len(h2ClientPreface):]
		p.prefaceOK = true
		p.f.inboundH2 = true
	}
	return p.consumeH2()
}

// take consumes n bytes from the front of the buffer.
func (p *tapParser) take(n int) []byte {
	b := p.buf[:n]
	p.buf = p.buf[n:]
	return b
}

func (p *tapParser) consumeH2() (done bool) {
	for {
		if len(p.buf) < 9 {
			return false
		}
		length := int(p.buf[0])<<16 | int(p.buf[1])<<8 | int(p.buf[2])
		if length > tapMaxCaptureBytes {
			p.f.fail("h2 frame exceeds capture bound")
			return true
		}
		if len(p.buf) < 9+length {
			return false
		}
		typ, flags := p.buf[3], p.buf[4]
		streamID := binary.BigEndian.Uint32(p.buf[5:9]) & 0x7fffffff
		payload := p.take(9 + length)[9:]

		switch typ {
		case 0x4: // SETTINGS
			if flags&0x1 == 0 && !p.gotSettings {
				p.gotSettings = true
				for i := 0; i+6 <= len(payload); i += 6 {
					p.f.settings = append(p.f.settings, h2Setting{
						ID:  binary.BigEndian.Uint16(payload[i : i+2]),
						Val: binary.BigEndian.Uint32(payload[i+2 : i+6]),
					})
				}
			}
		case 0x8: // WINDOW_UPDATE
			if streamID == 0 && len(payload) == 4 && p.f.connWindow == 0 {
				p.f.connWindow = binary.BigEndian.Uint32(payload) & 0x7fffffff
			}
		case 0x2: // PRIORITY
			if len(payload) >= 5 && len(p.f.priorities) < tapMaxPriorities {
				dep := binary.BigEndian.Uint32(payload[:4])
				p.f.priorities = append(p.f.priorities, h2PriorityFrame{
					StreamID:  streamID,
					Exclusive: payload[0]&0x80 != 0,
					Dep:       dep & 0x7fffffff,
					Weight:    payload[4],
				})
			}
		case 0x10: // PRIORITY_UPDATE
			p.f.prioUpdate++
		case 0x1: // HEADERS
			if p.inHeaders {
				p.f.fail("overlapping HEADERS frames")
				return true
			}
			frag := payload
			if flags&0x8 != 0 { // PADDED
				if len(frag) < 1 {
					p.f.fail("truncated padded HEADERS")
					return true
				}
				pad := int(frag[0])
				frag = frag[1:]
				if pad > len(frag) {
					p.f.fail("HEADERS padding exceeds frame")
					return true
				}
				frag = frag[:len(frag)-pad]
			}
			if flags&0x20 != 0 { // PRIORITY field attached
				if len(frag) < 5 {
					p.f.fail("truncated HEADERS priority field")
					return true
				}
				dep := binary.BigEndian.Uint32(frag[:4])
				p.f.headersPriority = &h2PriorityFrame{
					StreamID:  streamID,
					Exclusive: frag[0]&0x80 != 0,
					Dep:       dep & 0x7fffffff,
					Weight:    frag[4],
				}
				frag = frag[5:]
			}
			p.frag = append(p.frag[:0], frag...)
			p.inHeaders = true
			if flags&0x4 != 0 { // END_HEADERS
				return p.finishH2Block()
			}
		case 0x9: // CONTINUATION
			if !p.inHeaders {
				p.f.fail("CONTINUATION without HEADERS")
				return true
			}
			p.frag = append(p.frag, payload...)
			if flags&0x4 != 0 {
				return p.finishH2Block()
			}
		}
		// DATA, PING, RST_STREAM, GOAWAY, PUSH_PROMISE: skipped by length.
	}
}

// finishH2Block decodes one complete HPACK header block. The first block is
// the connection's fingerprint specimen: its pseudo-header order and header
// order are what the outbound leg replays. Capture ends here - later requests
// reuse the same client-side ordering behavior, and per-request correlation
// would need stream IDs Go's HTTP/2 server does not expose.
func (p *tapParser) finishH2Block() (done bool) {
	p.f.blockFields = p.f.blockFields[:0]
	if _, err := p.hdec.Write(p.frag); err != nil {
		p.f.fail("hpack decode: " + err.Error())
		return true
	}
	for _, hf := range p.f.blockFields {
		if len(hf.Name) > 0 && hf.Name[0] == ':' {
			p.f.pseudoOrder = append(p.f.pseudoOrder, hf.Name)
		} else {
			p.f.headerOrder = append(p.f.headerOrder, hf.Name)
		}
	}
	p.f.blockFields = nil
	p.f.finish()
	return true
}
