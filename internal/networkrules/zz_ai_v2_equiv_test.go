package networkrules

// [v2-S6 全语料对拍] 段自动机接管 root 类（ZEN_V2_ROOT=1）vs v1（关）。
// 门禁：blockDiff=0 / redirectDiff=0 / appliedMultisetDiff=0（行为三重等价），
// 序列哈希预期不同（v2 候选顺序=插入序，非树 DFS 序）——设计稿 §4 已声明。
// 附 ModifyRes 与耗时对比。env: ZEN_AI_V2_EQ=1
import (
	"net/http"
	"net/url"
	"os"
	"testing"
)

func TestZZAIV2RootEquiv(t *testing.T) {
	if os.Getenv("ZEN_AI_V2_EQ") != "1" {
		t.Skip("ZEN_AI_V2_EQ not set")
	}
	aiLoad(t)
	e0 := New(WithV2Root(false), WithDomainGate(true))
	e1 := New(WithV2Root(true), WithDomainGate(true))
	hmLoadLists(t, e0)
	hmLoadLists(t, e1)

	reqOf := func(u string) *http.Request {
		pu, err := url.Parse(u)
		if err != nil || pu.Host == "" {
			return nil
		}
		return &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
	}
	var blockDiff, redirDiff, setDiff, violations int
	for _, u := range aiCorpus {
		r0, r1 := reqOf(u), reqOf(u)
		if r0 == nil {
			continue
		}
		ap0, b0, red0 := e0.ModifyReq(r0)
		ap1, b1, red1 := e1.ModifyReq(r1)
		k0 := make([]string, len(ap0))
		for i := range ap0 {
			k0[i] = ap0[i].RawRule
		}
		k1 := make([]string, len(ap1))
		for i := range ap1 {
			k1[i] = ap1[i].RawRule
		}
		if b0 != b1 {
			blockDiff++
		}
		if red0 != red1 {
			redirDiff++
		}
		if !sameMultiset(k0, k1) {
			setDiff++
			if setDiff <= 5 {
				t.Errorf("V2 多重集差异 url=%.80s\n  v1(n=%d): %v\n  v2(n=%d): %v", u, len(k0), head(k0, 3), len(k1), head(k1, 3))
			}
		}
		if b0 != b1 || red0 != red1 || !sameMultiset(k0, k1) {
			violations++
		}
	}
	t.Logf("V2-EQUIV ModifyReq: corpus=%d blockDiff=%d redirectDiff=%d multisetDiff=%d violations=%d",
		len(aiCorpus), blockDiff, redirDiff, setDiff, violations)

	var resDiff int
	for _, u := range aiCorpus {
		r0, r1 := reqOf(u), reqOf(u)
		if r0 == nil {
			continue
		}
		mk := func(r *http.Request) *http.Response {
			res := &http.Response{StatusCode: 200, Proto: "HTTP/1.1", ProtoMajor: 1,
				Header: http.Header{}, Body: http.NoBody, Request: r}
			res.Header.Set("Content-Type", "text/html; charset=utf-8")
			return res
		}
		m0, _ := e0.ModifyRes(r0, mk(r0))
		m1, _ := e1.ModifyRes(r1, mk(r1))
		k0 := make([]string, len(m0))
		for i := range m0 {
			k0[i] = m0[i].RawRule
		}
		k1 := make([]string, len(m1))
		for i := range m1 {
			k1[i] = m1[i].RawRule
		}
		if !sameMultiset(k0, k1) {
			resDiff++
		}
	}
	t.Logf("V2-EQUIV ModifyRes: corpus=%d multisetDiff=%d", len(aiCorpus), resDiff)
	if blockDiff != 0 || redirDiff != 0 {
		t.Fatalf("可观测决策差异 blockDiff=%d redirectDiff=%d（必须 0）", blockDiff, redirDiff)
	}
	if setDiff != 0 || resDiff != 0 {
		t.Logf("⚠️ 多重集差异存在（applied 集合变化）——需逐例归因")
	}
}
