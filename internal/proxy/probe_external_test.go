package proxy

// 外部实弹探针（2026-10-07 h2-mirror）。默认跳过，设 ZEN_PROBE_EXTERNAL=1 运行：
//
//	ZEN_PROBE_EXTERNAL=1 go test ./internal/proxy/ -run TestProbeExternal -v
//
// 用两个真实第三方 h2 栈（进程内 x/net Go 客户端 + 外部 Node/nghttp2 进程，
// 后者带自定义 SETTINGS）各跑 直连 vs 经 Zen 两轮。源站侧用生产 tap 逐连接
// 捕获实际帧，取"最近一连接"快照做直连 vs 经代逐字段对比（SETTINGS 值与
// 顺序、连接窗口、伪头顺序、header 顺序）。判据：逐字段一致（错配=0）。

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	xhttp2 "golang.org/x/net/http2"
)

const nodeProbeScript = `
const net = require('net');
const tls = require('tls');
const http2 = require('http2');
const [proxyHost, proxyPort, originHost, originPort, viaProxy] = process.argv.slice(1);
const originAddr = originHost + ':' + originPort;
// 拿到裸 TCP（直连或经 CONNECT 隧道），自行 TLS 包装后交给 http2
// （http2.connect 的 createConnection 拿到什么用什么，不会代做 TLS）。
function rawSock(cb) {
  if (viaProxy !== '1') {
    const s = net.connect(Number(originPort), originHost);
    s.on('connect', () => cb(s));
    s.on('error', (e) => { console.error('sockerr', e.message); process.exit(2); });
    return;
  }
  const s = net.connect(Number(proxyPort), proxyHost);
  s.on('connect', () => {
    s.write('CONNECT ' + originAddr + ' HTTP/1.1\r\nHost: ' + originAddr + '\r\n\r\n');
  });
  let buf = '';
  s.on('data', function onData(d) {
    buf += d.toString('latin1');
    if (!buf.includes('\r\n\r\n')) return;
    s.removeListener('data', onData);
    cb(s);
  });
  s.on('error', (e) => { console.error('sockerr', e.message); process.exit(2); });
}
rawSock((raw) => {
  const tlsOpts = { socket: raw, rejectUnauthorized: false };
  if (!net.isIP(originHost)) tlsOpts.servername = originHost; // IP 目标不发 SNI（与浏览器一致）
  const tlsSock = tls.connect(tlsOpts, () => {
    const h2 = http2.connect('https://' + originAddr, {
      createConnection: () => tlsSock,
      settings: { headerTableSize: 8192, initialWindowSize: 1048576, maxFrameSize: 16384 },
    });
    h2.on('error', (e) => { console.error('h2err', e.message); process.exit(3); });
    const req = h2.request({ ':path': '/node', 'user-agent': 'nodeprobe/1', 'x-node-probe': 'yes' });
    req.end();
    req.on('response', (headers) => {
      let body = '';
      req.on('data', (c) => body += c);
      req.on('end', () => {
        console.log(JSON.stringify({ status: headers[':status'], body: body }));
        process.exit(0);
      });
    });
    req.on('error', (e) => { console.error('reqerr', e.message); process.exit(4); });
  });
  tlsSock.on('error', (e) => { console.error('tlserr', e.message); process.exit(5); });
});
`

// observedSnapshot 是生产 connFacts 已捕获事实的纯数据快照。
type observedSnapshot struct {
	Settings    []h2Setting
	ConnWindow  uint32
	PseudoOrder []string
	HeaderOrder []string
}

func snapshotOf(f *connFacts) observedSnapshot {
	return observedSnapshot{
		Settings:    append([]h2Setting(nil), f.settings...),
		ConnWindow:  f.connWindow,
		PseudoOrder: append([]string(nil), f.pseudoOrder...),
		HeaderOrder: append([]string(nil), f.headerOrder...),
	}
}

func snapshotsEqual(a, b observedSnapshot) []string {
	var diffs []string
	if strings.Join(a.PseudoOrder, ",") != strings.Join(b.PseudoOrder, ",") {
		diffs = append(diffs, fmt.Sprintf("pseudo: %v vs %v", a.PseudoOrder, b.PseudoOrder))
	}
	if strings.Join(a.HeaderOrder, ",") != strings.Join(b.HeaderOrder, ",") {
		diffs = append(diffs, fmt.Sprintf("headers: %v vs %v", a.HeaderOrder, b.HeaderOrder))
	}
	if len(a.Settings) != len(b.Settings) {
		diffs = append(diffs, fmt.Sprintf("settings count: %d vs %d", len(a.Settings), len(b.Settings)))
	}
	for i := 0; i < len(a.Settings) && i < len(b.Settings); i++ {
		if a.Settings[i] != b.Settings[i] {
			diffs = append(diffs, fmt.Sprintf("settings[%d]: %+v vs %+v", i, a.Settings[i], b.Settings[i]))
		}
	}
	if a.ConnWindow != 0 && b.ConnWindow != 0 && a.ConnWindow != b.ConnWindow {
		// 客户端未发连接级 WINDOW_UPDATE 时，fhttp 必发其默认值（已知残余）。
		diffs = append(diffs, fmt.Sprintf("connWindow: %d vs %d", a.ConnWindow, b.ConnWindow))
	}
	return diffs
}

// startProbeOrigin 起一个 h2 源站；每条连接用全新 connFacts 捕获（各轮互不
// 污染），latest() 返回最近一条连接的捕获结果。
func startProbeOrigin(t *testing.T) (addr string, latest func() *connFacts, negotiated *atomic.Value) {
	t.Helper()
	var cur atomic.Pointer[connFacts]
	proto := &atomic.Value{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("origin listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	tlsCfg := &tls.Config{ // #nosec G402 -- test origin
		Certificates: []tls.Certificate{mustCert(t)},
		NextProtos:   []string{"h2", "http/1.1"},
	}
	var h2s xhttp2.Server
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(raw net.Conn) {
				defer raw.Close()
				tlsConn := tls.Server(raw, tlsCfg)
				if herr := tlsConn.Handshake(); herr != nil {
					return
				}
				proto.Store(tlsConn.ConnectionState().NegotiatedProtocol)
				f := newConnFacts()
				cur.Store(f)
				h2s.ServeConn(newTapConn(tlsConn, f), &xhttp2.ServeConnOpts{
					Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusOK)
						io.WriteString(w, "mirrored")
					}),
				})
			}(conn)
		}
	}()
	return ln.Addr().String(), func() *connFacts { return cur.Load() }, proto
}

func TestProbeExternal(t *testing.T) {
	if os.Getenv("ZEN_PROBE_EXTERNAL") == "" {
		t.Skip("外部实弹探针：设 ZEN_PROBE_EXTERNAL=1 启用（需要 node 在 PATH）")
	}
	nodeExe, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node 不在 PATH: %v", err)
	}

	_, _, upstreamAddr := startFakeUpstreamProxy(t, false)
	t.Setenv(upstreamProxyEnv, upstreamAddr)
	originAddr, latest, negotiated := startProbeOrigin(t)
	proxyAddr := startTestProxy(t, func(p *Proxy) {
		p.certGenerator = selfSignedCertGenerator{}
	})

	goRound := func(viaProxy bool) error {
		tr := &xhttp2.Transport{
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				var raw net.Conn
				var derr error
				if viaProxy {
					raw, derr = net.Dial("tcp", proxyAddr)
					if derr != nil {
						return nil, derr
					}
					fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, addr)
					br := bufio.NewReader(raw)
					resp, rerr := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
					if rerr != nil {
						return nil, rerr
					}
					if resp.StatusCode != http.StatusOK {
						return nil, fmt.Errorf("connect %d", resp.StatusCode)
					}
				} else {
					raw, derr = net.Dial("tcp", addr)
					if derr != nil {
						return nil, derr
					}
				}
				host, _, _ := net.SplitHostPort(addr)
				tlsConn := tls.Client(raw, &tls.Config{ // #nosec G402 -- probe
					ServerName: host, InsecureSkipVerify: true, NextProtos: []string{"h2"},
				})
				if herr := tlsConn.Handshake(); herr != nil {
					return nil, herr
				}
				return tlsConn, nil
			},
		}
		req, _ := http.NewRequest(http.MethodGet, "https://"+originAddr+"/go", nil)
		req.Header.Set("User-Agent", "goprobe/1")
		req.Header.Set("Accept", "*/*")
		resp, err := tr.RoundTrip(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "mirrored") {
			return fmt.Errorf("status=%d body=%q", resp.StatusCode, body)
		}
		return nil
	}

	nodeRound := func(viaProxy bool) error {
		ph, pp, _ := net.SplitHostPort(proxyAddr)
		oh, op, _ := net.SplitHostPort(originAddr)
		vp := "0"
		if viaProxy {
			vp = "1"
		}
		cmd := exec.Command(nodeExe, "-e", nodeProbeScript, ph, pp, oh, op, vp)
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		cmd.Env = append(os.Environ(), "NODE_TLS_REJECT_UNAUTHORIZED=0")
		if rerr := cmd.Run(); rerr != nil {
			return fmt.Errorf("node: %v stderr=%s", rerr, errb.String())
		}
		if !strings.Contains(out.String(), "mirrored") {
			return fmt.Errorf("node round bad output: out=%q stderr=%q", out.String(), errb.String())
		}
		return nil
	}

	record := map[string]observedSnapshot{}
	type roundDef struct {
		name string
		run  func(viaProxy bool) error
	}
	for _, r := range []roundDef{{"go", goRound}, {"node", nodeRound}} {
		for _, via := range []struct {
			name string
			on   bool
		}{{"DIRECT", false}, {"VIAZEN", true}} {
			if err := r.run(via.on); err != nil {
				t.Fatalf("%s/%s: %v", r.name, via.name, err)
			}
			f := latest()
			if f == nil {
				t.Fatalf("%s/%s: 源站未捕获到任何连接", r.name, via.name)
			}
			waitFacts(t, f)
			record[r.name+"/"+via.name] = snapshotOf(f)
			t.Logf("%s/%s: %+v", r.name, via.name, record[r.name+"/"+via.name])
		}
	}

	if got, _ := negotiated.Load().(string); got != "" && got != "h2" {
		t.Fatalf("origin negotiated %q, want h2", got)
	}
	for _, client := range []string{"go", "node"} {
		diffs := snapshotsEqual(record[client+"/DIRECT"], record[client+"/VIAZEN"])
		if len(diffs) != 0 {
			t.Fatalf("%s 客户端 经Zen 与 直连 不一致（错配≠0）:\n  %s", client, strings.Join(diffs, "\n  "))
		}
		t.Logf("%s: 直连≈经Zen 逐字段一致（错配=0）", client)
	}
}
