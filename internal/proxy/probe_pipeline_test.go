package proxy

// 完整生产管线复现探针（2026-10-07 断网事件）。默认跳过：
//
//	ZEN_PROBE_PIPELINE=1 go test ./internal/proxy/ -run TestProbePipeline -v
//
// 链 sing-box 真实上游 + 真实 Edge headless 作为入站客户端 + 真实源站，
// 完整复现 MITM→tap→facts→fhttp 重放管线，验证断网根因。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProbePipeline(t *testing.T) {
	if os.Getenv("ZEN_PROBE_PIPELINE") == "" {
		t.Skip("完整管线复现探针：设 ZEN_PROBE_PIPELINE=1 启用（需 sing-box 20122 + Edge）")
	}
	edge := findEdgeOrChrome(t)

	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	_ = upstreamAddr
	t.Setenv(upstreamProxyEnv, "127.0.0.1:20122") // 真实 sing-box 上游
	proxyAddr := startTestProxy(t, func(p *Proxy) {
		p.certGenerator = selfSignedCertGenerator{}
	})

	target := os.Getenv("ZEN_PROBE_PIPELINE_URL")
	if target == "" {
		target = "https://www.cloudflare.com"
	}

	out := filepath.Join(os.TempDir(), "zen-pipeline-dom.html")
	_ = os.Remove(out)
	cmd := exec.Command(edge,
		"--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
		"--user-data-dir="+filepath.Join(os.TempDir(), "zen-pipeline-profile"),
		"--proxy-server=http://"+proxyAddr,
		"--dump-dom", target)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("edge exit: %v stderr=%s", err, stderr.String())
		}
	case <-time.After(45 * time.Second):
		_ = cmd.Process.Kill()
		t.Log("edge 超时（45s）——疑似复现卡死/失败")
	}
	dom, _ := os.ReadFile(out)
	t.Logf("dump-dom 长度 = %d（0 或很小=页面加载失败）", len(dom))
	if len(stdout.String()) > 0 {
		t.Logf("edge stdout: %s", truncateForLog(stdout.String(), 400))
	}
	if len(stderr.String()) > 0 {
		t.Logf("edge stderr: %s", truncateForLog(stderr.String(), 400))
	}
}

func truncateForLog(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " | ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

