package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2/hpack"
)

// feedFacts runs a capture over the given byte stream, chunked the way the
// caller says, and waits for the facts to settle.
func feedFacts(t *testing.T, chunks [][]byte) *connFacts {
	t.Helper()
	facts := newConnFacts()
	tap := &tapConn{facts: facts, ch: make(chan []byte, 64)}
	facts.stop = func() { tap.capturedDone.Store(true) }
	facts.capturing.Store(true)
	go facts.parse(tap.ch)
	for _, c := range chunks {
		tap.ch <- c
	}
	select {
	case <-facts.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("capture did not settle")
	}
	return facts
}

func h2Frame(typ byte, flags byte, streamID uint32, payload []byte) []byte {
	out := make([]byte, 9, 9+len(payload))
	out[0] = byte(len(payload) >> 16)
	out[1] = byte(len(payload) >> 8)
	out[2] = byte(len(payload))
	out[3] = typ
	out[4] = flags
	binary.BigEndian.PutUint32(out[5:], streamID)
	return append(out, payload...)
}

func encodeHeaderBlock(t *testing.T, fields ...hpack.HeaderField) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := hpack.NewEncoder(&buf)
	for _, f := range fields {
		if err := enc.WriteField(f); err != nil {
			t.Fatalf("hpack encode: %v", err)
		}
	}
	return buf.Bytes()
}

func TestTapCapturesH2Facts(t *testing.T) {
	settings := []byte{}
	for _, pair := range [][2]uint32{{1, 65536}, {2, 0}, {4, 6291456}, {6, 262144}} {
		entry := make([]byte, 6)
		binary.BigEndian.PutUint16(entry, uint16(pair[0]))
		binary.BigEndian.PutUint32(entry[2:], pair[1])
		settings = append(settings, entry...)
	}
	block := encodeHeaderBlock(t,
		hpack.HeaderField{Name: ":method", Value: "GET"},
		hpack.HeaderField{Name: ":authority", Value: "example.com"},
		hpack.HeaderField{Name: ":scheme", Value: "https"},
		hpack.HeaderField{Name: ":path", Value: "/"},
		hpack.HeaderField{Name: "user-agent", Value: "probe/1"},
		hpack.HeaderField{Name: "accept", Value: "*/*"},
		hpack.HeaderField{Name: "cookie", Value: "a=b"},
	)

	stream := []byte(h2ClientPreface)
	stream = append(stream, h2Frame(0x4, 0, 0, settings)...)
	wu := make([]byte, 4)
	binary.BigEndian.PutUint32(wu, 15663105)
	stream = append(stream, h2Frame(0x8, 0, 0, wu)...)
	stream = append(stream, h2Frame(0x1, 0x4|0x1, 1, block)...)

	// Split the stream into awkward chunk boundaries to exercise incremental
	// parsing.
	var chunks [][]byte
	for len(stream) > 0 {
		n := 7
		if n > len(stream) {
			n = len(stream)
		}
		chunks = append(chunks, stream[:n])
		stream = stream[n:]
	}

	facts := feedFacts(t, chunks)

	if !facts.inboundH2 {
		t.Fatal("capture did not detect h2")
	}
	if facts.failed {
		t.Fatal("capture failed")
	}
	wantSettings := []h2Setting{{1, 65536}, {2, 0}, {4, 6291456}, {6, 262144}}
	if len(facts.settings) != len(wantSettings) {
		t.Fatalf("captured %d settings, want %d (%+v)", len(facts.settings), len(wantSettings), facts.settings)
	}
	for i, s := range facts.settings {
		if s != wantSettings[i] {
			t.Fatalf("settings[%d] = %+v, want %+v (wire order matters)", i, s, wantSettings[i])
		}
	}
	if facts.connWindow != 15663105 {
		t.Fatalf("connWindow = %d, want 15663105", facts.connWindow)
	}
	wantPseudo := []string{":method", ":authority", ":scheme", ":path"}
	if len(facts.pseudoOrder) != len(wantPseudo) {
		t.Fatalf("pseudoOrder = %v", facts.pseudoOrder)
	}
	for i, name := range facts.pseudoOrder {
		if name != wantPseudo[i] {
			t.Fatalf("pseudoOrder[%d] = %q, want %q", i, name, wantPseudo[i])
		}
	}
	wantHeaders := []string{"user-agent", "accept", "cookie"}
	for i, name := range facts.headerOrder {
		if name != wantHeaders[i] {
			t.Fatalf("headerOrder[%d] = %q, want %q", i, name, wantHeaders[i])
		}
	}
	if len(facts.headerOrder) != len(wantHeaders) {
		t.Fatalf("headerOrder = %v", facts.headerOrder)
	}
}

func TestTapCapturesH2ContinuationAndPriority(t *testing.T) {
	block := encodeHeaderBlock(t,
		hpack.HeaderField{Name: ":method", Value: "POST"},
		hpack.HeaderField{Name: ":path", Value: "/x"},
		hpack.HeaderField{Name: "content-type", Value: "application/json"},
	)
	// Split the header block across HEADERS + CONTINUATION.
	stream := []byte(h2ClientPreface)
	stream = append(stream, h2Frame(0x4, 0, 0, nil)...) // empty settings
	// PRIORITY frame for stream 3 (Firefox-style pre-allocation).
	prio := []byte{0x00, 0x00, 0x00, 0x05, 0x28} // dep 5, weight 40
	stream = append(stream, h2Frame(0x2, 0, 3, prio)...)
	// HEADERS with an attached PRIORITY field (5 bytes) and a fragment of the
	// header block, completed by CONTINUATION.
	headersPrio := []byte{0x00, 0x00, 0x00, 0x0d, 0x29} // dep 13, weight 41
	half := len(block) / 2
	stream = append(stream, h2Frame(0x1, 0x20, 1, append(headersPrio, block[:half]...))...)
	stream = append(stream, h2Frame(0x9, 0x4, 1, block[half:])...) // CONTINUATION, END_HEADERS

	facts := feedFacts(t, [][]byte{stream})

	if !facts.inboundH2 || facts.failed {
		t.Fatalf("capture failed: inboundH2=%v failed=%v", facts.inboundH2, facts.failed)
	}
	if len(facts.priorities) != 1 || facts.priorities[0].StreamID != 3 || facts.priorities[0].Dep != 5 || facts.priorities[0].Weight != 40 {
		t.Fatalf("priorities = %+v", facts.priorities)
	}
	if facts.headersPriority == nil || facts.headersPriority.StreamID != 1 {
		t.Fatalf("headersPriority = %+v, want the HEADERS-attached priority", facts.headersPriority)
	}
	if len(facts.pseudoOrder) != 2 || facts.pseudoOrder[0] != ":method" || facts.pseudoOrder[1] != ":path" {
		t.Fatalf("pseudoOrder = %v", facts.pseudoOrder)
	}
	if len(facts.headerOrder) != 1 || facts.headerOrder[0] != "content-type" {
		t.Fatalf("headerOrder = %v", facts.headerOrder)
	}
}

func TestTapRejectsNonH2Stream(t *testing.T) {
	// The tap only attaches to h2-dispatched connections; a stream that does
	// not start with the client preface must be marked failed honestly.
	stream := []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")
	facts := feedFacts(t, [][]byte{stream})

	if facts.inboundH2 {
		t.Fatal("h1 stream detected as h2")
	}
	if !facts.failed {
		t.Fatal("non-h2 stream must mark capture failed")
	}
	if len(facts.settings) != 0 || len(facts.headerOrder) != 0 {
		t.Fatalf("failed capture must leave facts empty, got %+v", facts)
	}
}

func TestTapGarbageFailsGracefully(t *testing.T) {
	facts := newConnFacts()
	tap := &tapConn{facts: facts, ch: make(chan []byte, 8)}
	facts.stop = func() { tap.capturedDone.Store(true) }
	facts.capturing.Store(true)
	go facts.parse(tap.ch)
	tap.ch <- []byte("\x16\x03\x01\x00\x50random bytes that are neither h2 nor http1 and never terminate\x01\x02\x03")
	// The read loop closing on stream end (EOF) is what settles a capture
	// whose head never completes; simulate it by closing the channel.
	close(tap.ch)
	select {
	case <-facts.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("capture did not settle on garbage")
	}
	if !facts.failed {
		t.Fatal("garbage capture must be marked failed")
	}
}

func TestWaitReadyDoesNotBlockWithoutCapture(t *testing.T) {
	facts := newConnFacts() // no tap ever attached (h1 inbound path)
	done := make(chan struct{})
	go func() {
		facts.waitReady(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("waitReady blocked on a connection without an active capture")
	}
}

func TestMergedHeaderOrder(t *testing.T) {
	header := map[string][]string{
		"Accept-Encoding": {"gzip"},
		"User-Agent":      {"probe"},
		"X-New":           {"added-by-filter"},
		"Cookie":          {"a=b"},
	}
	captured := []string{"cookie", "user-agent", "accept-encoding"}
	got := mergedHeaderOrder(header, captured)
	want := []string{"cookie", "user-agent", "accept-encoding", "x-new"}
	if len(got) != len(want) {
		t.Fatalf("mergedHeaderOrder = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mergedHeaderOrder[%d] = %q, want %q (captured wire order first, additions last)", i, got[i], want[i])
		}
	}
}

// make sure the interface still holds for tests that pass conns around.
var _ net.Conn = (*tapConn)(nil)
