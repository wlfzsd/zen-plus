package proxy

// sing-box 真实上游探针（2026-10-07 断网事件定位）。默认跳过，设
// ZEN_PROBE_SINGBOX=1 运行（需要本机 sing-box 在 127.0.0.1:20122）：
//
//	ZEN_PROBE_SINGBOX=1 go test ./internal/proxy/ -run TestProbeSingBoxALPN -v
//
// 用生产镜像路径（captureHelloBytes→extractMimicSpec→dialWithHello）经真实
// 上游对真实源站做两种 ALPN 的握手，打印各自协商结果：
//   - ALPN [h2, http/1.1]（h2-mirror 语义）
//   - ALPN [http/1.1]（旧版语义）
// 判定上游是否存在"对 h2 供给不回 ALPN"的行为（断网事件根因假设）。

import (
	"context"
	"crypto/tls"
	"os"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

func TestProbeSingBoxALPN(t *testing.T) {
	if os.Getenv("ZEN_PROBE_SINGBOX") == "" {
		t.Skip("sing-box 真实上游探针：设 ZEN_PROBE_SINGBOX=1 启用（需 sing-box 在 127.0.0.1:20122）")
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

	helloRaw := captureHelloBytes(t) // 客户端供给 h2 + http/1.1

	stock := &tls.Config{} // 生产默认：系统根证书校验
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, tc := range []struct {
		name string
		h2   bool
	}{{"ALPN-h2+h1(mirror 语义)", true}, {"ALPN-h1-only(旧版语义)", false}} {
		spec := extractMimicSpec(helloRaw, tc.h2)
		if spec == nil {
			t.Fatalf("%s: spec 提取失败", tc.name)
		}
		conn, derr := p.dialWithHello(ctx, "tcp", origin, stock, spec)
		if derr != nil {
			t.Logf("%s → 握手失败: %v", tc.name, derr)
			continue
		}
		uc := conn.(*utls.UConn)
		t.Logf("%s → 经 sing-box 协商协议 = %q", tc.name, uc.ConnectionState().NegotiatedProtocol)
		conn.Close()
	}
}
