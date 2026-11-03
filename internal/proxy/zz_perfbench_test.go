package proxy

// zz_perfbench_test.go — 代理链路性能/稳定性测量台（2026-11-03 深度优化立项）。
//
// 目的是给「请求速度 / 下载速度 / 网络稳定性」三轴提供可复现、低方差的 A/B 测量
// 装置，而不是再猜。装置由三部分组成：
//
//	origin  —— 真实 TLS 源站（httptest + EnableHTTP2），提供 /small /big /
//	           /slow /stall 四个端点，/stall 永不响应（模拟挂死的边缘）。
//	chain   —— 本地 HTTP CONNECT 代理，扮演用户的 sing-box/上游代理：可注入
//	           单向延迟（模拟 RTT）、可在 CONNECT 阶段挂死、可在数据阶段冻结。
//	zen     —— 生产 NewProxy（含上游链 = mimic h2/h1 腿、过滤 stub、MITM）。
//
// 三个对照臂：chain-only（客户端直接经 chain 到 origin，地板）、zen-h2
// （入站 h2）、zen-h1（入站 h1）。所有臂走同一条 chain，因此差异只来自 Zen。
//
// 门禁：ZEN_PERF=1 才运行（默认跳过，不影响 go test ./internal/proxy 的常规回归）。
//
//	go test ./internal/proxy -run 'TestPerf' -v            （跳过）
//	$env:ZEN_PERF="1"; go test ./internal/proxy -run 'TestPerf' -v -timeout 30m
//
// 测量纪律（沿用 经验记录 2026-11-03 的教训）：同轮交替、多轮取中位、单轮不下结论。

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2/hpack"
)

const perfHost = "origin.perf.test"

// perfRTT is the simulated one-way delay injected by the chain (每方向每次传输
// 睡眠 d)。100ms/方向 ≈ 200ms RTT，接近国内经境外节点的真实形态。
// ZEN_PERF_RTT 可覆盖（如 ZEN_PERF_RTT=0 测纯 CPU 开销，=200ms 测斜率）。
var perfRTT = perfRTTFromEnv()

func perfRTTFromEnv() time.Duration {
	if v := os.Getenv("ZEN_PERF_RTT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 100 * time.Millisecond
}

func perfGate(t *testing.T) {
	t.Helper()
	if os.Getenv("ZEN_PERF") != "1" {
		t.Skip("ZEN_PERF=1 才运行（性能测量台）")
	}
}

// ---------- chain：扮演上游代理 ----------

type perfChain struct {
	ln             net.Listener
	target         string // 非空时所有 CONNECT 都拨它（模拟"上游解析远端域名"）
	delay          time.Duration
	flightGap      time.Duration // 同一"飞行"内（距上次写 < gap）不再加延迟：模拟链路把背靠背写合并进同一 RTT
	stallHandshake atomic.Bool   // CONNECT 收下但永不回 200
	accepted       atomic.Int64
	delays         atomic.Int64
	reqDelays      atomic.Int64 // 客户端→上游方向（请求）的单向传输次数
	respDelays     atomic.Int64 // 上游→客户端方向（响应）的单向传输次数
	reqBytes       atomic.Int64
	respBytes      atomic.Int64
	lastReq        atomic.Int64 // 该方向最近一次写的 unix nano（0=从未）
	lastResp       atomic.Int64
	mu             sync.Mutex
	conns          map[net.Conn]struct{}
}

// connCount 返回累计接受的 CONNECT 连接数（用于判断"每次请求是否新开隧道"）。
func (c *perfChain) connCount() int64 { return c.accepted.Load() }

// delayCount 返回累计注入的单向延迟次数（= 经过本跳的单向传输次数）。
func (c *perfChain) delayCount() int64 { return c.delays.Load() }

func newPerfChain(t *testing.T, target string) *perfChain {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("chain listen: %v", err)
	}
	c := &perfChain{ln: ln, target: target, conns: make(map[net.Conn]struct{}), flightGap: 5 * time.Millisecond}
	go c.serve()
	t.Cleanup(func() {
		_ = ln.Close()
		c.mu.Lock()
		for conn := range c.conns {
			_ = conn.Close()
		}
		c.mu.Unlock()
	})
	return c
}

func (c *perfChain) addr() string { return c.ln.Addr().String() }

func (c *perfChain) url() *url.URL {
	return &url.URL{Scheme: "http", Host: c.addr()}
}

func (c *perfChain) serve() {
	for {
		conn, err := c.ln.Accept()
		if err != nil {
			return
		}
		c.accepted.Add(1)
		c.mu.Lock()
		c.conns[conn] = struct{}{}
		c.mu.Unlock()
		go c.handle(conn)
	}
}

func (c *perfChain) handle(conn net.Conn) {
	defer func() {
		_ = conn.Close()
		c.mu.Lock()
		delete(c.conns, conn)
		c.mu.Unlock()
	}()

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method != http.MethodConnect {
		_, _ = io.WriteString(conn, "HTTP/1.1 405 Method Not Allowed\r\n\r\n")
		return
	}

	if c.stallHandshake.Load() {
		// 收下 CONNECT、永不回 200：模拟上游节点拥塞/挂死时 sing-box 的表象。
		<-time.After(5 * time.Minute)
		return
	}

	target := req.Host
	if c.target != "" {
		target = c.target
	}
	up, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer up.Close()
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if n := br.Buffered(); n > 0 {
		head, err := br.Peek(n)
		if err != nil {
			return
		}
		if _, err := up.Write(head); err != nil {
			return
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		c.copy(up, conn, &c.reqDelays, &c.reqBytes)
	}()
	go func() {
		defer wg.Done()
		c.copy(conn, up, &c.respDelays, &c.respBytes)
	}()
	wg.Wait()
}

// copy 是带单向延迟的隧道拷贝。延迟模型刻意模拟"飞行（flight）"而不是
// "每块都睡"：真实链路把背靠背发出的字节合并进同一个 RTT（TCP_NODELAY 下
// 两段紧邻发出、同一飞行到达）。因此这里先在一个 2ms 窗口内把已到达的字节
// 收成一个 burst，再睡一次 delay 转发；只有真正相隔一个 RTT 的下一批字节
// 才会再付一次延迟。每睡一次记一次数 = 该方向消耗了几个单向飞行。
func (c *perfChain) copy(dst io.Writer, src net.Conn, delays, bytes *atomic.Int64) {
	if c.delay <= 0 {
		_, _ = io.Copy(dst, src)
		return
	}
	buf := make([]byte, 64<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			total := n
			// 收集同一飞行里紧随其后的字节（最多 2ms 或缓冲满）。
			for total < len(buf) {
				if derr := src.SetReadDeadline(time.Now().Add(2 * time.Millisecond)); derr != nil {
					break
				}
				m, rerr := src.Read(buf[total:])
				if m > 0 {
					total += m
					continue
				}
				_ = rerr
				break
			}
			_ = src.SetReadDeadline(time.Time{})

			delays.Add(1)
			c.delays.Add(1)
			bytes.Add(int64(total))
			time.Sleep(c.delay)
			if _, werr := dst.Write(buf[:total]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// ---------- origin：真实 TLS 源站 ----------

type perfOrigin struct {
	srv     *httptest.Server
	addr    string
	bigBody []byte
}

func newPerfOrigin(t *testing.T) *perfOrigin {
	t.Helper()
	o := &perfOrigin{bigBody: makePerfBody(16 << 20)}

	mux := http.NewServeMux()
	mux.HandleFunc("/small", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{"ok":true,"pad":"` + strings.Repeat("x", 900) + `"}`))
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprint(len(o.bigBody)))
		_, _ = w.Write(o.bigBody)
	})
	mux.HandleFunc("/stall", func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	o.srv = srv
	o.addr = srv.Listener.Addr().String()
	t.Cleanup(srv.Close)
	return o
}

func makePerfBody(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

// ---------- zen：生产代理 + 上游链 ----------

type perfZen struct {
	p     *Proxy
	addr  string
	chain *perfChain
}

func newPerfZen(t *testing.T, chain *perfChain) *perfZen {
	t.Helper()
	return newPerfZenWithUpstream(t, chain.url().String())
}

// newPerfZenWithUpstream 用任意上游代理 URL（真实链或测试链）启动一个进程内
// 生产代理（无 Wails、无 sysproxy），供测量台使用。
func newPerfZenWithUpstream(t *testing.T, upstreamURL string) *perfZen {
	t.Helper()

	prev := upstreamProvider
	chainURL := upstreamURL
	SetUpstreamProvider(func() (string, string, bool) { return chainURL, "perfbench", true })
	t.Cleanup(func() {
		upstreamProvider = prev
		SyncUpstreamEnv() // 清掉本测试镜像进进程环境的 HTTP(S)_PROXY
	})

	p, err := NewProxy(noopFilter{}, selfSignedCertGenerator{}, 0, nil, "", nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	transportOf(t, p).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- test origin is self-signed
	port, err := p.Start()
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	return &perfZen{p: p, addr: fmt.Sprintf("127.0.0.1:%d", port)}
}

// dropOutboundIdle 清空 Zen 出站连接池，强制下一次请求支付完整
// CONNECT+mimic 握手（用于量化"冷启动"开销）。
func (z *perfZen) dropOutboundIdle() {
	if c, ok := z.p.requestTransport.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
	if c, ok := z.p.requestTransportHTTPS.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// ---------- 客户端 ----------

func perfProxyClient(proxyAddr string, h2 bool) *http.Client {
	t := &http.Transport{
		Proxy:               http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr}),
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- MITM cert is self-signed on purpose
		MaxIdleConnsPerHost: 64,
		ForceAttemptHTTP2:   h2,
	}
	if !h2 {
		// 关掉 h2 upgrade：入站腿走 h1（对照镜像腿 h1 路径）
		t.TLSClientConfig.NextProtos = []string{"http/1.1"}
		t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	}
	return &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// perfURL 构造指向 origin 的 URL：host 用 perfHost，由 chain 映射到真实源站。
func perfURL(originAddr, path string) string {
	_, port, _ := net.SplitHostPort(originAddr)
	return "https://" + perfHost + ":" + port + path
}

// ---------- 测量原语 ----------

type perfSample struct {
	ttfb   time.Duration
	total  time.Duration
	nbytes int64
	err    error
}

func perfDo(ctx context.Context, client *http.Client, rawURL string, discard bool) perfSample {
	var ttfb time.Duration
	start := time.Now()
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { ttfb = time.Since(start) }}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, rawURL, nil)
	if err != nil {
		return perfSample{err: err}
	}
	resp, err := client.Do(req)
	if err != nil {
		return perfSample{err: err}
	}
	defer resp.Body.Close()
	if ttfb == 0 {
		ttfb = time.Since(start)
	}
	var n int64
	if discard {
		n, err = io.Copy(io.Discard, resp.Body)
	} else {
		n, err = io.Copy(io.Discard, resp.Body)
	}
	return perfSample{ttfb: ttfb, total: time.Since(start), nbytes: n, err: err}
}

func perfPercentiles(xs []time.Duration) (p50, p90, p99, min, max, mean time.Duration) {
	if len(xs) == 0 {
		return
	}
	s := append([]time.Duration(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	at := func(q float64) time.Duration {
		i := int(float64(len(s)-1) * q)
		return s[i]
	}
	var sum time.Duration
	for _, d := range s {
		sum += d
	}
	return at(0.50), at(0.90), at(0.99), s[0], s[len(s)-1], sum / time.Duration(len(s))
}

func perfReport(t *testing.T, label string, xs []time.Duration, fails int) {
	t.Helper()
	if len(xs) == 0 {
		t.Logf("%-18s 无有效样本（失败 %d）", label, fails)
		return
	}
	p50, p90, p99, lo, hi, mean := perfPercentiles(xs)
	t.Logf("%-18s n=%-3d fail=%-2d p50=%-8s p90=%-8s p99=%-8s min=%-8s max=%-8s mean=%s",
		label, len(xs), fails,
		p50.Round(100*time.Microsecond), p90.Round(100*time.Microsecond), p99.Round(100*time.Microsecond),
		lo.Round(100*time.Microsecond), hi.Round(100*time.Microsecond), mean.Round(100*time.Microsecond))
}

// ---------- 测量 1：热连接 TTFB（每请求开销） ----------

func TestPerfHotTTFB(t *testing.T) {
	perfGate(t)

	origin := newPerfOrigin(t)
	chain := newPerfChain(t, origin.addr)
	chain.delay = perfRTT
	zen := newPerfZen(t, chain)

	arms := []struct {
		name   string
		client *http.Client
	}{
		{"chain-only", perfProxyClient(chain.addr(), true)},

		{"zen-h2", perfProxyClient(zen.addr, true)},
		{"zen-h1", perfProxyClient(zen.addr, false)},
	}
	const rounds, perRound = 3, 60
	ctx := context.Background()
	target := perfURL(origin.addr, "/small")

	for _, arm := range arms {
		// 预热：建立连接、完成握手，避免第一轮夹带冷启动
		for i := 0; i < 5; i++ {
			perfDo(ctx, arm.client, target, true)
		}
	}

	results := make(map[string][]time.Duration, len(arms))
	fails := make(map[string]int, len(arms))
	// 同轮交替：每轮三个臂各跑 perRound 次，消除时间漂移。
	for r := 0; r < rounds; r++ {
		for _, arm := range arms {
			for i := 0; i < perRound; i++ {
				s := perfDo(ctx, arm.client, target, true)
				if s.err != nil {
					fails[arm.name]++
					continue
				}
				results[arm.name] = append(results[arm.name], s.ttfb)
			}
		}
	}
	for _, arm := range arms {
		perfReport(t, arm.name+" /small", results[arm.name], fails[arm.name])
	}
}

// ---------- 测量 2：冷出站握手开销（量化连接复用价值） ----------

func TestPerfColdOutbound(t *testing.T) {
	perfGate(t)

	origin := newPerfOrigin(t)
	chain := newPerfChain(t, origin.addr)
	chain.delay = perfRTT
	zen := newPerfZen(t, chain)

	client := perfProxyClient(zen.addr, true)
	direct := perfProxyClient(chain.addr(), true)
	ctx := context.Background()
	target := perfURL(origin.addr, "/small")

	const rounds = 12
	var warm, cold, base []time.Duration
	var warmF, coldF, baseF int

	for i := 0; i < 3; i++ {
		perfDo(ctx, client, target, true)
		perfDo(ctx, direct, target, true)
	}

	for i := 0; i < rounds; i++ {
		s := perfDo(ctx, direct, target, true)
		if s.err != nil {
			baseF++
		} else {
			base = append(base, s.ttfb)
		}

		s = perfDo(ctx, client, target, true)
		if s.err != nil {
			warmF++
		} else {
			warm = append(warm, s.ttfb)
		}

		zen.dropOutboundIdle()
		s = perfDo(ctx, client, target, true)
		if s.err != nil {
			coldF++
		} else {
			cold = append(cold, s.ttfb)
		}
	}
	perfReport(t, "chain-only 热", base, baseF)
	perfReport(t, "zen 热", warm, warmF)
	perfReport(t, "zen 出站冷", cold, coldF)
}

// ---------- 测量 3：大对象下载吞吐 ----------

func TestPerfThroughput(t *testing.T) {
	perfGate(t)

	origin := newPerfOrigin(t)
	chain := newPerfChain(t, origin.addr) // 吞吐测试不加延迟：只测 Zen 数据面本身
	zen := newPerfZen(t, chain)

	arms := []struct {
		name   string
		client *http.Client
		path   string
	}{
		{"chain-only", perfProxyClient(chain.addr(), true), "/big"},
		{"zen-h2", perfProxyClient(zen.addr, true), "/big"},
		{"zen-h1", perfProxyClient(zen.addr, false), "/big"},
	}
	const rounds = 5
	ctx := context.Background()
	out := make(map[string][]float64, len(arms))
	for _, arm := range arms {
		for i := 0; i < 2; i++ { // 预热
			perfDo(ctx, arm.client, perfURL(origin.addr, arm.path), true)
		}
	}
	for r := 0; r < rounds; r++ {
		for _, arm := range arms {
			s := perfDo(ctx, arm.client, perfURL(origin.addr, arm.path), true)
			if s.err != nil || s.total <= 0 {
				continue
			}
			mbps := float64(s.nbytes) / (1 << 20) / s.total.Seconds()
			out[arm.name] = append(out[arm.name], mbps)
			t.Logf("%-12s round=%d bytes=%d ttfb=%s total=%s → %.1f MB/s",
				arm.name, r, s.nbytes, s.ttfb.Round(time.Millisecond), s.total.Round(time.Millisecond), mbps)
		}
	}
	for _, arm := range arms {
		xs := out[arm.name]
		if len(xs) == 0 {
			t.Logf("%-12s 无样本", arm.name)
			continue
		}
		sort.Float64s(xs)
		t.Logf("%-12s MB/s p50=%.1f min=%.1f max=%.1f", arm.name, xs[len(xs)/2], xs[0], xs[len(xs)-1])
	}
}

// ---------- 测量 4：空闲后重建代价（chainedIdleConnTimeout 的价值量化） ----------

func TestPerfIdleReuse(t *testing.T) {
	perfGate(t)

	origin := newPerfOrigin(t)
	chain := newPerfChain(t, origin.addr)
	chain.delay = perfRTT
	zen := newPerfZen(t, chain)

	client := perfProxyClient(zen.addr, true)
	ctx := context.Background()
	target := perfURL(origin.addr, "/small")

	// 热态基线
	for i := 0; i < 3; i++ {
		perfDo(ctx, client, target, true)
	}
	var hot []time.Duration
	for i := 0; i < 10; i++ {
		if s := perfDo(ctx, client, target, true); s.err == nil {
			hot = append(hot, s.ttfb)
		}
	}
	perfReport(t, "空闲前 热", hot, 0)

	// 空闲 35s（> chainedIdleConnTimeout 30s）：Chrome 侧入站连接仍在，
	// 但 Zen 出站池已过期 → 下一请求要重建 CONNECT+mimic 握手。
	t.Logf("空闲 35s（> chainedIdleConnTimeout=30s）后复测…")
	time.Sleep(35 * time.Second)
	var after []time.Duration
	for i := 0; i < 3; i++ {
		if s := perfDo(ctx, client, target, true); s.err == nil {
			after = append(after, s.ttfb)
		}
		time.Sleep(200 * time.Millisecond)
	}
	perfReport(t, "空闲后 首请求", after, 0)
}

// ---------- 测量 5：入站 h2「客户端不读」的写阻塞回收 ----------

// TestPerfSlowClientH2 验证入站 h2 连接在客户端停止读取后的行为：
// 默认 http2.Server.WriteByteTimeout=0 → 写阻塞无界，连接永不被 Zen 回收；
// 设置 WriteByteTimeout 后，连接应在该窗口内被关闭（Chrome 侧随即重连自愈）。
func TestPerfSlowClientH2(t *testing.T) {
	perfGate(t)

	cases := []struct {
		name     string
		timeout  time.Duration
		wantReap bool
	}{
		{"default(无界)", 0, false},
		{"writeByteTimeout=2s", 2 * time.Second, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origin := newPerfOrigin(t)
			chain := newPerfChain(t, origin.addr)
			zen := newPerfZen(t, chain)
			if tc.timeout > 0 {
				zen.p.h2Server.WriteByteTimeout = tc.timeout
			}

			_, port, _ := net.SplitHostPort(origin.addr)
			conn, err := net.Dial("tcp", zen.addr)
			if err != nil {
				t.Fatalf("dial zen: %v", err)
			}
			defer conn.Close()

			// CONNECT 隧道
			connect := fmt.Sprintf("CONNECT %s:%s HTTP/1.1\r\nHost: %s:%s\r\n\r\n", perfHost, port, perfHost, port)
			if _, err := io.WriteString(conn, connect); err != nil {
				t.Fatalf("write connect: %v", err)
			}
			br := bufio.NewReader(conn)
			resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
			if err != nil {
				t.Fatalf("read connect response: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("connect status = %d", resp.StatusCode)
			}

			tlsConn := tls.Client(conn, &tls.Config{
				ServerName:         perfHost,
				InsecureSkipVerify: true, // #nosec G402 -- MITM cert
				NextProtos:         []string{"h2"},
			})
			if err := tlsConn.Handshake(); err != nil {
				t.Fatalf("tls handshake: %v", err)
			}

			// h2 preface + 空 SETTINGS
			if _, err := io.WriteString(tlsConn, h2ClientPreface); err != nil {
				t.Fatalf("preface: %v", err)
			}
			settings := []byte{0, 0, 0, 0x4, 0, 0, 0, 0, 0}
			if _, err := tlsConn.Write(settings); err != nil {
				t.Fatalf("settings: %v", err)
			}
			// 读服务端 SETTINGS（否则客户端不 ack 也无妨，这里只是让流建起来）
			_ = tlsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
			head := make([]byte, 9)
			if _, err := io.ReadFull(tlsConn, head); err != nil {
				t.Fatalf("read server settings: %v", err)
			}
			bodyLen := binary.BigEndian.Uint32(append([]byte{0}, head[0:3]...))
			if bodyLen > 0 {
				if _, err := io.CopyN(io.Discard, tlsConn, int64(bodyLen)); err != nil {
					t.Fatalf("read settings body: %v", err)
				}
			}

			// HEADERS: GET /big
			var hbuf []byte
			enc := hpack.NewEncoder(&fakeWriter{&hbuf})
			for _, f := range []hpack.HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: "/big"},
				{Name: ":authority", Value: perfHost + ":" + port},
			} {
				if err := enc.WriteField(f); err != nil {
					t.Fatalf("hpack: %v", err)
				}
			}
			frame := make([]byte, 9+len(hbuf))
			frame[0], frame[1], frame[2] = byte(len(hbuf)>>16), byte(len(hbuf)>>8), byte(len(hbuf))
			frame[3] = 0x1 // HEADERS
			frame[4] = 0x5 // END_STREAM | END_HEADERS
			frame[8] = 0x1 // stream 1
			copy(frame[9:], hbuf)
			if _, err := tlsConn.Write(frame); err != nil {
				t.Fatalf("headers: %v", err)
			}

			// 从这里开始不再读取：让服务端 DATA 写阻塞。
			// 等 writeByteTimeout 窗口的两倍，再看连接是否被 Zen 关闭。
			wait := 6 * time.Second
			if tc.timeout > 0 {
				wait = tc.timeout*2 + 2*time.Second
			}
			time.Sleep(wait)

			_ = tlsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, err = tlsConn.Read(make([]byte, 1))
			reaped := err != nil
			t.Logf("timeout=%v 等 %s 后连接状态: err=%v reaped=%v", tc.timeout, wait, err, reaped)
			if reaped != tc.wantReap {
				t.Errorf("reaped=%v, want %v", reaped, tc.wantReap)
			}
		})
	}
}

// fakeWriter 让 hpack.Encoder 写进 []byte。
type fakeWriter struct{ b *[]byte }

func (w *fakeWriter) Write(p []byte) (int, error) {
	*w.b = append(*w.b, p...)
	return len(p), nil
}

// ---------- 测量 6：单向传输次数记账（"是否多走跳/RTT"的硬判据） ----------

// TestPerfRTTAccounting 统计每个臂完成一次热请求时 chain 睡了多少次单向延迟。
// 地板臂（chain-only）的期望值 = 2（请求一次 + 响应一次）。若 Zen 臂显著高于
// 地板，说明 Zen 在热路径上多引入了串行的单向传输（多一次 RTT 级等待）。
func TestPerfRTTAccounting(t *testing.T) {
	perfGate(t)

	origin := newPerfOrigin(t)
	chain := newPerfChain(t, origin.addr)
	chain.delay = perfRTT
	zen := newPerfZen(t, chain)
	ctx := context.Background()
	target := perfURL(origin.addr, "/small")

	arms := []struct {
		name   string
		client *http.Client
	}{
		{"chain-only", perfProxyClient(chain.addr(), true)},
		{"zen-h2", perfProxyClient(zen.addr, true)},
		{"zen-h1", perfProxyClient(zen.addr, false)},
	}
	for _, arm := range arms {
		for i := 0; i < 5; i++ { // 预热（含握手，握手不进记账窗口）
			perfDo(ctx, arm.client, target, true)
		}
		conns0 := chain.connCount()
		reqD0, respD0 := chain.reqDelays.Load(), chain.respDelays.Load()
		reqB0, respB0 := chain.reqBytes.Load(), chain.respBytes.Load()
		const n = 5
		var ttfbSum time.Duration
		var ok int
		for i := 0; i < n; i++ {
			s := perfDo(ctx, arm.client, target, true)
			if s.err == nil {
				ttfbSum += s.ttfb
				ok++
			}
		}
		if ok == 0 {
			t.Logf("%-12s 全部失败", arm.name)
			continue
		}
		t.Logf("%-12s 每请求: 新连接=%.2f 请求向传输=%.2f次/%dB 响应向传输=%.2f次/%dB 平均TTFB=%s",
			arm.name,
			float64(chain.connCount()-conns0)/float64(n),
			float64(chain.reqDelays.Load()-reqD0)/float64(n), (chain.reqBytes.Load()-reqB0)/int64(n),
			float64(chain.respDelays.Load()-respD0)/float64(n), (chain.respBytes.Load()-respB0)/int64(n),
			(ttfbSum / time.Duration(ok)).Round(100*time.Microsecond))
	}
}

// ---------- 测量 7：并发下的每请求开销（锁竞争 + 分配） ----------

// TestPerfConcurrent 在无链路延迟的环回上用 32 并发打满，测每请求的 CPU/分配
// 开销与尾延迟。目的：判断 Zen 的每请求路径（互斥锁、header 重建、拷贝缓冲）
// 在并发下是否成为瓶颈——这是"请求速度"里唯一还能被 Zen 自己改变的部分。
func TestPerfConcurrent(t *testing.T) {
	perfGate(t)

	origin := newPerfOrigin(t)
	chain := newPerfChain(t, origin.addr) // 不加延迟：把网络因素清零，只看 Zen 自身
	zen := newPerfZen(t, chain)
	ctx := context.Background()

	arms := []struct {
		name   string
		client *http.Client
	}{
		{"chain-only", perfProxyClient(chain.addr(), true)},
		{"zen-h2", perfProxyClient(zen.addr, true)},
		{"zen-h1", perfProxyClient(zen.addr, false)},
	}
	const workers, perWorker = 32, 40
	target := perfURL(origin.addr, "/small")

	for _, arm := range arms {
		for i := 0; i < 20; i++ { // 预热连接池
			perfDo(ctx, arm.client, target, true)
		}
		var mu sync.Mutex
		var ttfbs []time.Duration
		start := time.Now()
		var wg sync.WaitGroup
		var fails atomic.Int64
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < perWorker; i++ {
					s := perfDo(ctx, arm.client, target, true)
					if s.err != nil {
						fails.Add(1)
						continue
					}
					mu.Lock()
					ttfbs = append(ttfbs, s.ttfb)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		elapsed := time.Since(start)
		p50, p90, p99, _, mx, _ := perfPercentiles(ttfbs)
		t.Logf("%-12s n=%-4d fail=%-2d 耗时=%-7s rps=%-7.0f p50=%-7s p90=%-7s p99=%-7s max=%s",
			arm.name, len(ttfbs), fails.Load(), elapsed.Round(time.Millisecond),
			float64(len(ttfbs))/elapsed.Seconds(),
			p50.Round(10*time.Microsecond), p90.Round(10*time.Microsecond),
			p99.Round(10*time.Microsecond), mx.Round(10*time.Microsecond))
	}
}

// ---------- 测量 8：下载路径的每字节分配（GC 压力） ----------

// TestPerfAllocPerByte 量测经 Zen 下载 16MB 时进程堆分配增量，判断"每个响应
// 现分配 32KB 拷贝缓冲"是否值得池化。分配/字节 > 1 说明数据被额外复制进堆。
func TestPerfAllocPerByte(t *testing.T) {
	perfGate(t)

	origin := newPerfOrigin(t)
	chain := newPerfChain(t, origin.addr)
	zen := newPerfZen(t, chain)
	ctx := context.Background()

	arms := []struct {
		name   string
		client *http.Client
	}{
		{"chain-only", perfProxyClient(chain.addr(), true)},
		{"zen-h2", perfProxyClient(zen.addr, true)},
		{"zen-h1", perfProxyClient(zen.addr, false)},
	}
	target := perfURL(origin.addr, "/big")

	for _, arm := range arms {
		perfDo(ctx, arm.client, target, true) // 预热
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		s := perfDo(ctx, arm.client, target, true)
		runtime.ReadMemStats(&after)
		if s.err != nil {
			t.Logf("%-12s 失败: %v", arm.name, s.err)
			continue
		}
		alloc := after.TotalAlloc - before.TotalAlloc
		t.Logf("%-12s 传输 %dB 期间堆分配 %dB → 倍率 %.2fx（%.1f MB/s）",
			arm.name, s.nbytes, alloc,
			float64(alloc)/float64(s.nbytes),
			float64(s.nbytes)/(1<<20)/s.total.Seconds())
	}
}

// ---------- 测量 9：真实网络（真实上游链）多轮交替 A/B ----------

// TestPerfRealNetwork 用**真实上游代理**（默认 127.0.0.1:20122，即生产配置里的
// sing-box）做多轮交替对比：chain-only（客户端直接经上游，地板）vs zen-h2
// （同一条上游，中间加 Zen）。只量传输面（过滤器用 noop）——本测量的目的是回答
// "Zen 自己在真实链路上多加了多少"，不是规则引擎开销。
//
// 纪律：同一 URL 相邻两次请求间隔 ≥1.5s（规则24）、两臂逐轮交替、多轮取中位。
// 运行：$env:ZEN_PERF="1"; $env:ZEN_PERF_REAL="1"; go test ./internal/proxy -run TestPerfRealNetwork -v -timeout 30m
// 可用 ZEN_REAL_UPSTREAM / ZEN_PERF_URLS 覆盖上游与 URL 列表。
func TestPerfRealNetwork(t *testing.T) {
	perfGate(t)
	if os.Getenv("ZEN_PERF_REAL") != "1" {
		t.Skip("ZEN_PERF_REAL=1 才运行（真实网络测量）")
	}

	upstream := os.Getenv("ZEN_REAL_UPSTREAM")
	if upstream == "" {
		upstream = "http://127.0.0.1:20122"
	}
	upURL, err := url.Parse(upstream)
	if err != nil {
		t.Fatalf("bad upstream %q: %v", upstream, err)
	}
	zen := newPerfZenWithUpstream(t, upstream)

	// 断言上游端口真的在监听，避免把"上游没开"测成"Zen 慢"。
	if conn, derr := net.DialTimeout("tcp", upURL.Host, 3*time.Second); derr != nil {
		t.Fatalf("上游 %s 不可达: %v", upURL.Host, derr)
	} else {
		_ = conn.Close()
	}

	urls := []string{
		"https://www.baidu.com/",
		"https://www.qq.com/",
		"https://cdn.jsdelivr.net/npm/jquery@3.7.1/dist/jquery.min.js",
	}
	if v := os.Getenv("ZEN_PERF_URLS"); v != "" {
		urls = strings.Split(v, ",")
	}
	bigURL := os.Getenv("ZEN_PERF_BIG_URL")
	if bigURL == "" {
		bigURL = "https://cdn.jsdelivr.net/npm/typescript@5.4.5/lib/typescript.js"
	}

	direct := perfProxyClientURL(upURL.String(), true)
	viaZen := perfProxyClient(zen.addr, true)
	ctx := context.Background()

	const rounds = 3
	for _, u := range urls {
		var baseT, zenT []time.Duration
		var baseB, zenB []int64
		var baseF, zenF int
		for r := 0; r < rounds; r++ {
			order := []struct {
				name   string
				client *http.Client
			}{{"chain", direct}, {"zen", viaZen}}
			if r%2 == 1 { // 逐轮换序，抵消时间漂移
				order[0], order[1] = order[1], order[0]
			}
			for _, arm := range order {
				time.Sleep(1500 * time.Millisecond) // 规则24：同 URL 相邻发起 ≥1.5s
				s := perfDo(ctx, arm.client, u, true)
				if s.err != nil {
					if arm.name == "chain" {
						baseF++
					} else {
						zenF++
					}
					t.Logf("%s round=%d %-5s 失败: %v", shortURL(u), r, arm.name, s.err)
					continue
				}
				if arm.name == "chain" {
					baseT = append(baseT, s.ttfb)
					baseB = append(baseB, s.nbytes)
				} else {
					zenT = append(zenT, s.ttfb)
					zenB = append(zenB, s.nbytes)
				}
			}
		}
		perfReport(t, shortURL(u)+" chain", baseT, baseF)
		perfReport(t, shortURL(u)+" zen", zenT, zenF)
		if len(baseB) > 0 && len(zenB) > 0 && baseB[0] != zenB[0] {
			t.Logf("%s 字节数不一致: chain=%d zen=%d（下面吞吐对比无效）", shortURL(u), baseB[0], zenB[0])
		}
	}

	// 吞吐：大对象各测 2 次，交替
	for r := 0; r < 2; r++ {
		for _, arm := range []struct {
			name   string
			client *http.Client
		}{{"chain", direct}, {"zen", viaZen}} {
			time.Sleep(1500 * time.Millisecond)
			s := perfDo(ctx, arm.client, bigURL, true)
			if s.err != nil {
				t.Logf("big %-5s 失败: %v", arm.name, s.err)
				continue
			}
			t.Logf("big %-5s bytes=%d ttfb=%s total=%s → %.2f MB/s", arm.name, s.nbytes,
				s.ttfb.Round(time.Millisecond), s.total.Round(time.Millisecond),
				float64(s.nbytes)/(1<<20)/s.total.Seconds())
		}
	}
}

func shortURL(u string) string {
	if len(u) > 48 {
		return u[:48] + "…"
	}
	return u
}

// perfProxyClientURL 与 perfProxyClient 相同，但接受任意代理 URL 字符串。
func perfProxyClientURL(proxyURL string, h2 bool) *http.Client {
	u, err := url.Parse(proxyURL)
	if err != nil {
		panic(err)
	}
	t := &http.Transport{
		Proxy:               http.ProxyURL(u),
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- 只测传输，不验证书本
		MaxIdleConnsPerHost: 64,
		ForceAttemptHTTP2:   h2,
	}
	if !h2 {
		t.TLSClientConfig.NextProtos = []string{"http/1.1"}
		t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	}
	return &http.Client{Transport: t}
}

// TestPerfRealBig 定位"真实链路上 Zen 的下载反而更快"是否测量假象：
// 打印协议/状态码/字节数，并把 h1 臂拆出来对照。
func TestPerfRealBig(t *testing.T) {
	perfGate(t)
	if os.Getenv("ZEN_PERF_REAL") != "1" {
		t.Skip("ZEN_PERF_REAL=1 才运行")
	}
	upstream := os.Getenv("ZEN_REAL_UPSTREAM")
	if upstream == "" {
		upstream = "http://127.0.0.1:20122"
	}
	zen := newPerfZenWithUpstream(t, upstream)
	bigURL := os.Getenv("ZEN_PERF_BIG_URL")
	if bigURL == "" {
		bigURL = "https://cdn.jsdelivr.net/npm/typescript@5.4.5/lib/typescript.js"
	}
	ctx := context.Background()
	_ = ctx
	arms := []struct {
		name   string
		client *http.Client
	}{
		{"chain-h2", perfProxyClientURL(upstream, true)},
		{"chain-h1", perfProxyClientURL(upstream, false)},
		{"zen-h2", perfProxyClient(zen.addr, true)},
		{"zen-h1", perfProxyClient(zen.addr, false)},
	}
	for r := 0; r < 2; r++ {
		for _, arm := range arms {
			time.Sleep(1500 * time.Millisecond)
			start := time.Now()
			resp, err := arm.client.Get(bigURL)
			if err != nil {
				t.Logf("%-9s 失败: %v", arm.name, err)
				continue
			}
			var ttfb time.Duration
			_ = ttfb
			n, rerr := io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			el := time.Since(start)
			t.Logf("%-9s round=%d proto=%-8s status=%d bytes=%d total=%s → %.2f MB/s err=%v",
				arm.name, r, resp.Proto, resp.StatusCode, n, el.Round(time.Millisecond),
				float64(n)/(1<<20)/el.Seconds(), rerr)
		}
	}
}

// TestPerfRealViaLocalRelay 用**本进程的直通中继**（无人工延迟，真实 WAN）替代
// sing-box，把"Zen 的处理"与"上游代理本身"两个变量解耦：
//
//	arm1 = 客户端 → 中继 → 源站（地板，等价于一个纯 TCP 代理）
//	arm2 = 客户端 → Zen → 中继 → 源站
//
// 两侧走同一条 WAN 路径与同一个中继实现，差异只可能来自 Zen 自己。
func TestPerfRealViaLocalRelay(t *testing.T) {
	perfGate(t)

	relay := newPerfChain(t, "") // target 为空 = 拨 CONNECT 请求里的真实主机
	zen := newPerfZen(t, relay)

	bigURL := os.Getenv("ZEN_PERF_BIG_URL")
	if bigURL == "" {
		bigURL = "https://cdn.jsdelivr.net/npm/typescript@5.4.5/lib/typescript.js"
	}
	ctx := context.Background()
	arms := []struct {
		name   string
		client *http.Client
	}{
		{"relay-only", perfProxyClient(relay.addr(), true)},
		{"zen-h2", perfProxyClient(zen.addr, true)},
	}
	for r := 0; r < 3; r++ {
		for _, arm := range arms {
			time.Sleep(1500 * time.Millisecond)
			s := perfDo(ctx, arm.client, bigURL, true)
			if s.err != nil {
				t.Logf("%-11s round=%d 失败: %v", arm.name, r, s.err)
				continue
			}
			t.Logf("%-11s round=%d bytes=%d ttfb=%s total=%s → %.2f MB/s",
				arm.name, r, s.nbytes, s.ttfb.Round(time.Millisecond),
				s.total.Round(time.Millisecond),
				float64(s.nbytes)/(1<<20)/s.total.Seconds())
		}
	}
}

// TestPerfIdleFirstRequest 只测一件事：空闲 35s 之后"第一个请求"的 TTFB。
// 用 ZEN_CHAIN_IDLE_TIMEOUT 在同一份二进制里切换 30s/180s 两种策略，供外部脚本
// 交替多轮调用，形成同构建多轮 A/B（消除"改前改后不是同一次网络条件"的质疑）。
func TestPerfIdleFirstRequest(t *testing.T) {
	perfGate(t)

	origin := newPerfOrigin(t)
	chain := newPerfChain(t, origin.addr)
	chain.delay = perfRTT
	zen := newPerfZen(t, chain)
	client := perfProxyClient(zen.addr, true)
	ctx := context.Background()
	target := perfURL(origin.addr, "/small")

	for i := 0; i < 3; i++ { // 暖连接 + 完成握手
		perfDo(ctx, client, target, true)
	}
	var hot []time.Duration
	for i := 0; i < 5; i++ {
		if s := perfDo(ctx, client, target, true); s.err == nil {
			hot = append(hot, s.ttfb)
		}
	}
	// 空闲 35s：> 旧窗口 30s，< 新窗口 180s
	time.Sleep(35 * time.Second)
	s := perfDo(ctx, client, target, true)
	if s.err != nil {
		t.Fatalf("post-idle request failed: %v", s.err)
	}
	p50, _, _, _, _, _ := perfPercentiles(hot)
	t.Logf("IDLE_AB idle_timeout=%s hot_p50=%s post_idle_first=%s",
		chainedIdleConnTimeout, p50.Round(100*time.Microsecond), s.ttfb.Round(100*time.Microsecond))
}
