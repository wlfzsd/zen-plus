package process

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestFindPIDByIP(t *testing.T) {
	t.Parallel()

	t.Run("finds owning process for active connection", func(t *testing.T) {
		t.Parallel()

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		serverConn := make(chan net.Conn, 1)
		go func() {
			c, _ := ln.Accept()
			serverConn <- c
		}()

		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		var sConn net.Conn
		if sConn = <-serverConn; sConn != nil {
			defer sConn.Close()
		}

		srcAddr := conn.LocalAddr().(*net.TCPAddr)
		dstAddr := conn.RemoteAddr().(*net.TCPAddr)

		srcPort := tcpPort(t, srcAddr.Port)
		dstPort := tcpPort(t, dstAddr.Port)
		srcIP := srcAddr.IP
		dstIP := dstAddr.IP

		pid, err := findPIDByIP(srcPort, dstPort, srcIP, dstIP)
		if err != nil {
			t.Fatalf("findPIDByIP failed: %v", err)
		}

		if int(pid) != os.Getpid() {
			t.Errorf("PID = %d, want %d", pid, os.Getpid())
		}
	})

	t.Run("returns error for unbound port or non-existent connection", func(t *testing.T) {
		t.Parallel()
		l1, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srcPort := tcpPort(t, l1.Addr().(*net.TCPAddr).Port)
		_ = l1.Close()

		l2, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		dstPort := tcpPort(t, l2.Addr().(*net.TCPAddr).Port)
		_ = l2.Close()

		ip := net.ParseIP("127.0.0.1")
		_, err = findPIDByIP(srcPort, dstPort, ip, ip)

		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("srcPort=0 returns early", func(t *testing.T) {
		t.Parallel()

		dummyIP := net.ParseIP("127.0.0.1")
		pid, err := findPIDByIP(0, 0, dummyIP, dummyIP)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want %v", err, ErrNotFound)
		}
		if pid != 0 {
			t.Errorf("pid = %d, want 0", pid)
		}
	})
}

func TestFindByRequest(t *testing.T) {
	t.Parallel()

	t.Run("finds owning process for local HTTP request", func(t *testing.T) {
		t.Parallel()

		type result struct {
			info Info
			err  error
		}

		results := make(chan result, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			info, err := FindByRequest(r)
			results <- result{info: info, err: err}
		}))
		defer srv.Close()

		resp, err := srv.Client().Get(srv.URL)
		if err != nil {
			t.Fatalf("GET %s: %v", srv.URL, err)
		}
		defer resp.Body.Close()

		got := <-results
		if got.err != nil {
			t.Fatalf("FindByRequest: %v", got.err)
		}
		if int(got.info.PID) != os.Getpid() {
			t.Errorf("PID = %d, want %d", got.info.PID, os.Getpid())
		}
		if got.info.ExecutablePath == "" {
			t.Errorf("ExecutablePath = %q, want non-empty path", got.info.ExecutablePath)
		}
	})

	t.Run("returns error for malformed RemoteAddr", func(t *testing.T) {
		t.Parallel()

		_, err := FindByRequest(&http.Request{RemoteAddr: "127.0.0.1"})
		if err == nil {
			t.Fatal("err = nil")
		}
	})

	t.Run("returns error for invalid source port", func(t *testing.T) {
		t.Parallel()

		_, err := FindByRequest(&http.Request{RemoteAddr: "127.0.0.1:meow"})
		if err == nil {
			t.Fatal("err = nil")
		}
	})
}

func tcpPort(tb testing.TB, port int) uint16 {
	tb.Helper()

	if port < 0 || port > 65535 {
		tb.Fatalf("port out of range: %d", port)
	}

	return uint16(port) // #nosec G115 -- port was validated to fit in uint16 above.
}
