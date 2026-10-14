package process

import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func BenchmarkFindPIDByIP(b *testing.B) {
	conn := newBenchmarkTCPConnection(b)

	b.ResetTimer()

	for b.Loop() {
		_, err := findPIDByIP(conn.srcPort, conn.dstPort, conn.srcIP, conn.dstIP)
		if err != nil {
			b.Fatalf("findPIDByIP: %v", err)
		}
	}
}

func BenchmarkFindByRequest(b *testing.B) {
	conn := newBenchmarkTCPConnection(b)
	req := &http.Request{
		RemoteAddr: conn.remoteAddr,
	}
	req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, conn.localAddr))

	b.ResetTimer()

	for b.Loop() {
		_, err := FindByRequest(req)
		if err != nil {
			b.Fatalf("FindByRequest: %v", err)
		}
	}
}

func BenchmarkFindByRequestParallel(b *testing.B) {
	conn := newBenchmarkTCPConnection(b)

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		req := &http.Request{
			RemoteAddr: conn.remoteAddr,
		}
		req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, conn.localAddr))

		for pb.Next() {
			_, err := FindByRequest(req)
			if err != nil {
				b.Errorf("FindByRequest: %v", err)
				return
			}
		}
	})
}

func BenchmarkPIDExecutablePath(b *testing.B) {
	pid := PID(os.Getpid())

	b.ResetTimer()

	for b.Loop() {
		_, err := pidExecutablePath(pid)
		if err != nil {
			b.Fatalf("pidExecutablePath(%d): %v", pid, err)
		}
	}
}

func BenchmarkInfoNameWithExecutablePath(b *testing.B) {
	pid := PID(os.Getpid())
	path, err := pidExecutablePath(pid)
	if err != nil {
		b.Fatalf("pidExecutablePath(%d): %v", pid, err)
	}
	info := Info{PID: pid, ExecutablePath: path}

	b.ResetTimer()

	for b.Loop() {
		_, err := info.Name()
		if err != nil {
			b.Fatalf("Name: %v", err)
		}
	}
}

func BenchmarkInfoNameWithoutExecutablePath(b *testing.B) {
	info := Info{PID: PID(os.Getpid())}

	b.ResetTimer()

	for b.Loop() {
		_, err := info.Name()
		if err != nil {
			b.Fatalf("Name: %v", err)
		}
	}
}

type benchmarkTCPConnection struct {
	remoteAddr string
	localAddr  net.Addr
	srcPort    uint16
	dstPort    uint16
	srcIP      net.IP
	dstIP      net.IP
}

func newBenchmarkTCPConnection(tb testing.TB) *benchmarkTCPConnection {
	tb.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen: %v", err)
	}

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	clientConn, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		ln.Close()
		tb.Fatalf("dial: %v", err)
	}

	var serverConn net.Conn
	select {
	case serverConn = <-accepted:
	case err := <-acceptErr:
		clientConn.Close()
		ln.Close()
		tb.Fatalf("accept: %v", err)
	case <-time.After(5 * time.Second):
		clientConn.Close()
		ln.Close()
		tb.Fatal("accept: timed out")
	}

	remoteAddr := clientConn.LocalAddr().String()

	srcAddr, ok1 := clientConn.LocalAddr().(*net.TCPAddr)
	dstAddr, ok2 := clientConn.RemoteAddr().(*net.TCPAddr)

	if !ok1 || !ok2 {
		clientConn.Close()
		serverConn.Close()
		ln.Close()
		tb.Fatalf("failed to cast network addresses to *net.TCPAddr")
	}

	conn := &benchmarkTCPConnection{
		remoteAddr: remoteAddr,
		localAddr:  serverConn.LocalAddr(),
		srcPort:    tcpPort(tb, srcAddr.Port),
		dstPort:    tcpPort(tb, dstAddr.Port),
		srcIP:      srcAddr.IP,
		dstIP:      dstAddr.IP,
	}

	tb.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
		ln.Close()
	})

	return conn
}
