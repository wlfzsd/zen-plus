package networkrules

// [2026-10-14 方案A 等价性验证] 主机名索引（WithHostSplit）vs 基线的
// 逐请求判定面对比：block / redirect / 命中规则多重集。
//
// 顺序变化声明：A1 规则从树中抽出后候选序列会变，黄金 dump 哈希预期改变；
// 本探针用比序列哈希更强的判据——每个请求的 block、redirect 与命中规则
// 多重集必须完全一致（多重集对称差=0），这直接证明可观测行为不变。
//
// env 门控 ZEN_AI_HOSTMAP_EQ=1 才运行。

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// hmLoadLists 按生产同构骨架把 13 个启用列表装载进 e。
func hmLoadLists(t *testing.T, e *NetworkRules) {
	t.Helper()
	filtersDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Zen", "filters")
	for _, u := range taxFilterURLs {
		p := filepath.Join(filtersDir, fmt.Sprintf("%x", md5.Sum([]byte(u)))+".cache.txt")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(nil, 1<<20)
		name := "hostmap-equiv"
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
				continue
			}
			_, _ = e.ParseRule(line, &name)
		}
	}
	e.Compact()
}

func buildReq(u string) (*http.Request, error) {
	pu, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	return &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}, nil
}

// dumpReq 把一个请求的命中规则序列写入哈希并返回原始规则切片。
func dumpReq(h *fmt.Stringer, rules []struct{}, block bool, redirect string) []string {
	return nil
}

func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func head(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func TestZZAIHostMapEquiv(t *testing.T) {
	if os.Getenv("ZEN_AI_HOSTMAP_EQ") != "1" {
		t.Skip("ZEN_AI_HOSTMAP_EQ not set")
	}
	aiLoad(t) // 只为预热语料 aiCorpus（沙箱骨架已缓存列表内容）

	e0 := New(WithHostSplit(false))
	e1 := New(WithHostSplit(true))
	hmLoadLists(t, e0)
	hmLoadLists(t, e1)

	var violations, blockDiff, redirDiff, setDiff int
	h0, h1 := fnv.New64a(), fnv.New64a()
	reqOf := func(u string) *http.Request {
		pu, err := url.Parse(u)
		if err != nil || pu.Host == "" {
			return nil
		}
		return &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
	}

	for _, u := range aiCorpus {
		r0 := reqOf(u)
		r1 := reqOf(u)
		if r0 == nil {
			continue
		}
		ap0, b0, red0 := e0.ModifyReq(r0)
		ap1, b1, red1 := e1.ModifyReq(r1)

		keys0 := make([]string, len(ap0))
		for i := range ap0 {
			keys0[i] = ap0[i].RawRule
			fmt.Fprintf(h0, "R|%s\n", ap0[i].RawRule)
		}
		keys1 := make([]string, len(ap1))
		for i := range ap1 {
			keys1[i] = ap1[i].RawRule
			fmt.Fprintf(h1, "R|%s\n", ap1[i].RawRule)
		}
		fmt.Fprintf(h0, "D|%v|%s\n", b0, red0)
		fmt.Fprintf(h1, "D|%v|%s\n", b1, red1)

		if b0 != b1 {
			blockDiff++
		}
		if red0 != red1 {
			redirDiff++
		}
		if !sameMultiset(keys0, keys1) {
			setDiff++
		}
		if b0 != b1 || red0 != red1 || !sameMultiset(keys0, keys1) {
			violations++
			if violations <= 5 {
				t.Errorf("判定面差异 url=%s\n  off: block=%v redirect=%q n=%d %v\n  on : block=%v redirect=%q n=%d %v",
					u, b0, red0, len(keys0), head(keys0, 3), b1, red1, len(keys1), head(keys1, 3))
			}
		}
	}
	t.Logf("HOSTMAP-EQUIP: corpus=%d violations=%d | blockDiff=%d redirectDiff=%d appliedMultisetDiff=%d",
		len(aiCorpus), violations, blockDiff, redirDiff, setDiff)
	t.Logf("序列哈希: off=%016x on=%016x (on≠off=顺序变化属预期; 行为等价由 blockDiff=redirectDiff=0 证明)", h0.Sum64(), h1.Sum64())
	if blockDiff != 0 || redirDiff != 0 {
		t.Fatalf("可观测决策差异: blockDiff=%d redirectDiff=%d（必须为 0）", blockDiff, redirDiff)
	}

	// ===== ModifyRes 路径等价（WS1 同样改了 ModifyRes 的 Get 调用）=====
	resViolations, resDiff := 0, 0
	for _, u := range aiCorpus {
		r0 := reqOf(u)
		r1 := reqOf(u)
		if r0 == nil {
			continue
		}
		mkRes := func(req *http.Request) *http.Response {
			return &http.Response{StatusCode: 200, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
				Header: http.Header{}, Body: http.NoBody, Request: req}
		}
		res0 := mkRes(r0)
		res0.Header.Set("Content-Type", "text/html; charset=utf-8")
		res0.Header.Set("Server", "nginx")
		res1 := mkRes(r1)
		res1.Header.Set("Content-Type", "text/html; charset=utf-8")
		res1.Header.Set("Server", "nginx")
		m0, err0 := e0.ModifyRes(r0, res0)
		m1, err1 := e1.ModifyRes(r1, res1)
		k0 := make([]string, len(m0))
		for i := range m0 {
			k0[i] = m0[i].RawRule
		}
		k1 := make([]string, len(m1))
		for i := range m1 {
			k1[i] = m1[i].RawRule
		}
		if (err0 != nil) != (err1 != nil) || !sameMultiset(k0, k1) {
			resDiff++
		}
		if len(k0) != len(k1) {
			resViolations++
		}
	}
	t.Logf("ModifyRes 等价: corpus=%d multisetDiff=%d lenDiff=%d（必须均为 0）", len(aiCorpus), resDiff, resViolations)
	if resDiff != 0 || resViolations != 0 {
		t.Fatalf("ModifyRes 路径存在差异: multisetDiff=%d lenDiff=%d", resDiff, resViolations)
	}

	// 抽出的主机名规则规模
	e1s := e1.primaryStore
	e1s.hostMu.RLock()
	nRules, nKeys := 0, len(e1s.hostRules)
	for _, v := range e1s.hostRules {
		nRules += len(v)
	}
	e1s.hostMu.RUnlock()
	t.Logf("主机名索引: rules=%d keys=%d", nRules, nKeys)
}
