package proxy

// 真实浏览器 hello 复现探针（2026-10-07 断网事件根因定位）。默认跳过：
//
//	ZEN_PROBE_REALHELLO=1 go test ./internal/proxy/ -run TestProbeRealHello -v
//
// 流程：本机起捕获型 CONNECT 代理 → 用系统 Edge headless 过它访问真实站点
// （抓到真实浏览器 ClientHello 存文件）→ 把该 hello 走生产镜像路径
// （extractMimicSpec→dialWithHello，经 sing-box 上游）握手 → 打印协商协议与
// 镜像 spec 的 ALPN 内容，判定"真实 hello 重生成后 ALPN 是否损坏"。

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

// TestProbeCaptureBrowserHello 抓真实浏览器 hello 并存盘（供第二步喂镜像）。
func TestProbeCaptureBrowserHello(t *testing.T) {
	if os.Getenv("ZEN_PROBE_REALHELLO") == "" {
		t.Skip("真实浏览器 hello 探针：设 ZEN_PROBE_REALHELLO=1 启用")
	}
	edge := findEdgeOrChrome(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	helloCh := make(chan []byte, 4)
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, rerr := http.ReadRequest(br)
				if rerr != nil || req.Method != http.MethodConnect {
					return
				}
				c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				raw, _, perr := peekClientHello(c)
				if perr == nil && len(raw) > 0 {
					select {
					case helloCh <- raw:
					default:
					}
				}
			}(conn)
		}
	}()

	out := filepath.Join(os.TempDir(), "zen-realhello.bin")
	_ = os.Remove(out)
	targets := os.Getenv("ZEN_PROBE_REALHELLO_URLS")
	if targets == "" {
		targets = "https://www.cloudflare.com https://www.baidu.com"
	}
	cmd := exec.Command(edge,
		"--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
		"--user-data-dir="+filepath.Join(os.TempDir(), "zen-probe-profile"),
		"--proxy-server=http://"+ln.Addr().String(),
		"--dump-dom", targets)
	_ = cmd.Run()

	deadline := time.Now().Add(5 * time.Second)
	var hello []byte
	for time.Now().Before(deadline) && hello == nil {
		select {
		case hello = <-helloCh:
		case <-time.After(500 * time.Millisecond):
		}
	}
	if hello == nil {
		t.Fatal("未捕获到浏览器 hello（浏览器可能没起或没走代理）")
	}
	if werr := os.WriteFile(out, hello, 0o600); werr != nil {
		t.Fatalf("写 hello: %v", werr)
	}
	t.Logf("真实浏览器 hello 已捕获：%s（%d 字节）", out, len(hello))
}

// TestProbeRealHelloMirror 把已捕获的真实 hello 喂生产镜像路径，经 sing-box
// 握手，检查协商协议与 spec 的 ALPN。
func TestProbeRealHelloMirror(t *testing.T) {
	if os.Getenv("ZEN_PROBE_REALHELLO") == "" {
		t.Skip("真实浏览器 hello 探针：设 ZEN_PROBE_REALHELLO=1 启用")
	}
	helloPath := os.Getenv("ZEN_PROBE_REALHELLO_FILE")
	if helloPath == "" {
		helloPath = filepath.Join(os.TempDir(), "zen-realhello.bin")
	}
	helloRaw, err := os.ReadFile(helloPath)
	if err != nil || len(helloRaw) == 0 {
		t.Fatalf("读 hello（先跑 TestProbeCaptureBrowserHello）: %v", err)
	}

	upstream := os.Getenv("ZEN_PROBE_SINGBOX_UPSTREAM")
	if upstream == "" {
		upstream = "127.0.0.1:20122"
	}
	origin := os.Getenv("ZEN_PROBE_SINGBOX_ORIGIN")
	if origin == "" {
		origin = "www.cloudflare.com:443"
	}

	t.Setenv(upstreamProxyEnv, upstream)
	p, err := NewProxy(noopFilter{}, unusedCertGenerator{}, 0, nil, "", nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	spec := extractMimicSpec(helloRaw, true)
	if spec == nil {
		t.Fatal("真实 hello 的 spec 提取失败（本身即异常）")
	}
	alpnSeen := ""
	for _, ext := range spec.Extensions {
		if a, ok := ext.(*utls.ALPNExtension); ok {
			alpnSeen = fmt.Sprintf("%v", a.AlpnProtocols)
		}
	}
	t.Logf("镜像 spec 的 ALPN = %q（空字符串=ALPN 扩展丢失，即根因）", alpnSeen)
	t.Logf("spec 扩展数 = %d", len(spec.Extensions))
	if alpnSeen == "" {
		t.Logf("spec 全部扩展类型：")
		for _, ext := range spec.Extensions {
			t.Logf("  %T", ext)
		}
	}

	stock := &tls.Config{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, derr := p.dialWithHello(ctx, "tcp", origin, stock, spec)
	if derr != nil {
		t.Fatalf("经 sing-box 镜像握手失败: %v", derr)
	}
	defer conn.Close()
	uc := conn.(*utls.UConn)
	t.Logf("真实 hello 镜像 → 经 sing-box 协商协议 = %q（\"\"=复现断网根因）", uc.ConnectionState().NegotiatedProtocol)
	t.Logf("hello 原始字节（前 64）：\n%s", hex.Dump(helloRaw[:min(64, len(helloRaw))]))
}

func findEdgeOrChrome(t *testing.T) string {
	t.Helper()
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	t.Skip("本机未找到 Edge/Chrome")
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
