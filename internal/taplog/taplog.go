// Package taplog is the opt-in decision tap used for reproduction
// diagnostics (2026-10-08). It records filter decisions, connection modes
// and TLS-error latches to an append-only JSONL file so that intermittent
// behaviour (e.g. YouTube ads appearing in bursts) can be correlated with
// exactly what the proxy saw and when. Enabled only when ZEN_DECISION_LOG=1;
// when disabled every call reduces to a boolean check and nothing is written.
//
// Unlike application.log, entries here are NOT redacted: the tap exists to
// answer "which URL/host slipped through at which second", which redaction
// defeats. It is therefore off by default and enabled explicitly for a
// diagnostic session via the launcher, never by default in production.
package taplog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	once    sync.Once
	enabled bool
	file    *os.File
	writeMu sync.Mutex

	extraHosts []string
)

// watchSuffixes are the host suffixes whose ALLOW decisions are worth
// recording (block/redirect/modify/latch decisions are recorded regardless
// of host): YouTube and its ad/CDN ecosystem, plus anything supplied via
// ZEN_DECISION_HOSTS (comma-separated suffixes).
var watchSuffixes = []string{
	"youtube.com", "youtu.be", "youtube-nocookie.com", "googlevideo.com",
	"ytimg.com", "ggpht.com", "doubleclick.net", "googlesyndication.com",
	"googleadservices.com", "gvt1.com", "wide-youtube.l.google.com",
}

func setup() {
	if os.Getenv("ZEN_DECISION_LOG") != "1" {
		return
	}
	dir := os.Getenv("ZEN_DECISION_LOG_DIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "Logs")
	}
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	// O_TRUNC: each diagnostic session starts a fresh file; the analyzer
	// archives sessions under 测试临时 when needed.
	f, err := os.OpenFile(filepath.Join(dir, "decisions.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	file = f
	enabled = true
	if hosts := os.Getenv("ZEN_DECISION_HOSTS"); hosts != "" {
		for _, h := range strings.Split(hosts, ",") {
			if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
				extraHosts = append(extraHosts, h)
			}
		}
	}
}

// Enabled reports whether the tap is active.
func Enabled() bool {
	once.Do(setup)
	return enabled
}

// WatchedHost reports whether per-host decisions (conn mode, request allow)
// should be recorded for host.
func WatchedHost(host string) bool {
	if !Enabled() {
		return false
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, s := range watchSuffixes {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	for _, s := range extraHosts {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	return false
}

// Log appends one JSON line. Failures are swallowed on purpose: the tap
// observes the traffic path and must never break it.
func Log(fields map[string]any) {
	if !Enabled() {
		return
	}
	fields["ts"] = time.Now().Format("2006-01-02T15:04:05.000")
	b, err := json.Marshal(fields)
	if err != nil {
		return
	}
	b = append(b, '\n')
	writeMu.Lock()
	defer writeMu.Unlock()
	_, _ = file.Write(b)
}

// Facts are per-request connection facts carried in the request context.
type Facts struct {
	InboundH2 bool
}

type factsKey struct{}

// WithFacts returns a context carrying f.
func WithFacts(ctx context.Context, f Facts) context.Context {
	return context.WithValue(ctx, factsKey{}, f)
}

// FactsFromContext returns the facts stored by WithFacts, if any.
func FactsFromContext(ctx context.Context) (Facts, bool) {
	f, ok := ctx.Value(factsKey{}).(Facts)
	return f, ok
}

// ── [2026-10-08 采集 v3] player 响应体转储（jsonprune 覆盖缺口取证）──────────
// 仅在 ZEN_DECISION_LOG=1 时工作：把 /youtubei/v1/ 前缀响应的 body 前
// bodyDumpLimit 字节落到 bodies 子目录，供离线分析"广告配置藏在哪个结构里"。
// 每会话上限 bodyDumpMaxFiles 个文件，超限静默跳过。
const (
	bodyDumpLimit    = 1 << 20 // 1 MiB per response
	bodyDumpMaxFiles = 80
)

var bodyDumpSeq int64

func DumpPlayerBody(url string, body []byte) {
	if !Enabled() {
		return
	}
	if bodyDumpSeq >= bodyDumpMaxFiles {
		return
	}
	dir := filepath.Join(filepath.Dir(file.Name()), "bodies")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	n := atomic.AddInt64(&bodyDumpSeq, 1)
	name := filepath.Join(dir, fmt.Sprintf("%03d.json", n))
	meta := map[string]any{"url": url, "len": len(body)}
	mb, _ := json.Marshal(meta)
	header := append(mb, '\n')
	_ = os.WriteFile(name, append(header, body...), 0o644)
}
