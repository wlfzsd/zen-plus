package proxy

// websocket_prefix_test.go — 回归门禁：WS 后端在 101 之后立刻推送的字节不得丢失。
//
// 2026-11-03 定因：websocketHandshake 曾用 bufio.Reader 读 101 响应后把 reader
// 丢掉，随后隧道直接从裸 targetConn 读——后端与 101 同包到达的首帧（Socket.IO
// 的 "0{...}" connect 包、服务端先发型帧）被读进 bufio 缓冲后永久丢失，客户端
// 等一个永远不会来的帧（表现为 WS 连接建立后"卡住/重连"）。
// 修复：把残留字节用 prefixConn 续在隧道前面（与 CONNECT 隧道的同款处理）。

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestWebsocketBackendPushNotLost(t *testing.T) {
	const push = "FIRSTFRAME-AFTER-101"

	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("backend listen: %v", err)
	}
	defer backend.Close()

	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// 只读到请求头结束
				buf := make([]byte, 0, 2048)
				tmp := make([]byte, 512)
				for !strings.Contains(string(buf), "\r\n\r\n") {
					n, err := c.Read(tmp)
					if err != nil {
						return
					}
					buf = append(buf, tmp[:n]...)
				}
				// 101 与首帧写在同一次 Write 里：保证它们落在同一个 TCP 包，
				// 代理侧的 bufio 会把首帧一起读走。
				_, _ = io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\n"+
					"Upgrade: websocket\r\nConnection: Upgrade\r\n\r\n"+push)
			}(conn)
		}
	}()

	p, err := NewProxy(noopFilter{}, selfSignedCertGenerator{}, 0, nil, "", nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	port, err := p.Start()
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer func() { _ = p.Stop() }()

	conn, err := net.Dial("tcp", "127.0.0.1"+":"+itoa(port))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	req := "GET http://" + backend.Addr().String() + "/ws HTTP/1.1\r\n" +
		"Host: " + backend.Addr().String() + "\r\n" +
		"Connection: Upgrade\r\nUpgrade: websocket\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var got []byte
	tmp := make([]byte, 512)
	for {
		n, rerr := conn.Read(tmp)
		if n > 0 {
			got = append(got, tmp[:n]...)
			if strings.Contains(string(got), push) {
				break
			}
		}
		if rerr != nil {
			if len(got) == 0 {
				t.Fatalf("read from proxy: %v", rerr)
			}
			break
		}
	}
	if !strings.Contains(string(got), "101 Switching Protocols") {
		t.Fatalf("no 101 in response: %q", string(got))
	}
	if !strings.Contains(string(got), push) {
		t.Fatalf("backend push after 101 was lost; got %q", string(got))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
