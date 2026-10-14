// Package taplog is the opt-in decision tap used for reproduction
// diagnostics (2026-10-08). It records filter decisions, connection modes,
// TLS-error latches and (v4) raw response bodies to support offline analysis
// of ad-delivery coverage gaps. Enabled only when ZEN_DECISION_LOG=1; when
// disabled every call reduces to a boolean check and nothing is written.
//
// Entries are NOT redacted: the tap exists to answer "which URL slipped
// through at which second". It is off by default and enabled explicitly for
// diagnostic sessions.
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

// watchSuffixes are the host suffixes whose per-host facts (connection mode,
// request allow) are recorded alongside the always-recorded decisions.
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

// WatchedHost reports whether per-host facts should be recorded for host.
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

// Log appends one JSON line. Failures are swallowed: the tap observes the
// traffic path and must never break it.
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

// ── [v4] 响应体转储：环形命名，API 与 youtube HTML 共享容量 ─────────────────
// 赞助商内容有两个载体：/youtubei/v1/ API 响应，以及 youtube HTML 文档内嵌的
// ytInitialData。v3 的 80 文件硬上限在关键时刻静默停采（实证），v4 改为环形
// 覆盖最旧，采集永不停止。

const (
	bodyDumpLimit    = 1 << 20 // 1 MiB per response
	bodyDumpMaxFiles = 400     // 环形容量
)

var bodyDumpSeq int64

func dumpBody(tag, url string, body []byte) {
	if !Enabled() || len(body) == 0 {
		return
	}
	dir := filepath.Join(filepath.Dir(file.Name()), "bodies")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	n := atomic.AddInt64(&bodyDumpSeq, 1)
	name := filepath.Join(dir, fmt.Sprintf("%s-%04d.bin", tag, n%int64(bodyDumpMaxFiles)))
	meta, _ := json.Marshal(map[string]any{"url": url, "len": len(body), "tag": tag})
	header := append(meta, '\n')
	_ = os.WriteFile(name, append(header, body...), 0o644)
}

// DumpAPIBody dumps a /youtubei/v1/ API response body (raw wire bytes).
func DumpAPIBody(url string, body []byte) { dumpBody("api", url, body) }

// DumpHTMLBody dumps a youtube HTML document (ytInitialData carrier).
func DumpHTMLBody(url string, body []byte) { dumpBody("html", url, body) }

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
