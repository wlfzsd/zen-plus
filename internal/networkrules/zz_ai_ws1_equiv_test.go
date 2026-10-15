package networkrules

// [2026-10-14 WS1 路径全覆盖等价验证] 域门控开（现行为）vs 关（pre-WS1），
// ModifyReq（block/redirect/命中多重集）与 ModifyRes（mods 多重集/err）逐请求对比。
// 这补上了此前只靠黄金 dump（仅覆盖 ModifyReq）的洞：ModifyRes 路径此前无对照实测。
// env: ZEN_AI_WS1_EQ=1
import (
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"os"
	"testing"
)

func TestZZAIWS1Equiv(t *testing.T) {
	if os.Getenv("ZEN_AI_WS1_EQ") != "1" {
		t.Skip("ZEN_AI_WS1_EQ not set")
	}
	eOff := New(WithDomainGate(false)) // pre-WS1 行为
	eOn := New(WithDomainGate(true))   // WS1 行为（生产默认）
	hmLoadLists(t, eOff)
	hmLoadLists(t, eOn)
	aiLoad(t) // 填充 aiCorpus 语料

	reqOf := func(u string) *http.Request {
		pu, err := url.Parse(u)
		if err != nil || pu.Host == "" {
			return nil
		}
		return &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
	}
	var reqDiff, blockDiff, redirDiff int
	h0, h1 := fnv.New64a(), fnv.New64a()
	for _, u := range aiCorpus {
		r0, r1 := reqOf(u), reqOf(u)
		if r0 == nil {
			continue
		}
		ap0, b0, red0 := eOff.ModifyReq(r0)
		ap1, b1, red1 := eOn.ModifyReq(r1)
		fmt.Fprintf(h0, "D|%v|%s|%d\n", b0, red0, len(ap0))
		fmt.Fprintf(h1, "D|%v|%s|%d\n", b1, red1, len(ap1))
		if b0 != b1 {
			blockDiff++
		}
		if red0 != red1 {
			redirDiff++
		}
		if len(ap0) != len(ap1) {
			reqDiff++
		} else {
			for i := range ap0 {
				if ap0[i].RawRule != ap1[i].RawRule {
					reqDiff++
					break
				}
			}
		}
	}
	t.Logf("WS1-EQUIV ModifyReq: corpus=%d blockDiff=%d redirectDiff=%d appliedSeqDiff=%d",
		len(aiCorpus), blockDiff, redirDiff, reqDiff)
	t.Logf("  序列哈希 off=%016x on=%016x（应相同：WS1 保序）", h0.Sum64(), h1.Sum64())
	if blockDiff != 0 || redirDiff != 0 || reqDiff != 0 {
		t.Fatalf("ModifyReq 存在差异")
	}

	// ModifyRes：无 block/redirect 概念，对比 mods 集合与 err
	var resDiff, resLenDiff, resErrDiff int
	for _, u := range aiCorpus {
		r0, r1 := reqOf(u), reqOf(u)
		if r0 == nil {
			continue
		}
		mk := func(r *http.Request) *http.Response {
			res := &http.Response{StatusCode: 200, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
				Header: http.Header{}, Body: http.NoBody, Request: r}
			res.Header.Set("Content-Type", "text/html; charset=utf-8")
			res.Header.Set("Server", "nginx")
			return res
		}
		m0, err0 := eOff.ModifyRes(r0, mk(r0))
		m1, err1 := eOn.ModifyRes(r1, mk(r1))
		if (err0 != nil) != (err1 != nil) {
			resErrDiff++
			continue
		}
		if len(m0) != len(m1) {
			resLenDiff++
			continue
		}
		for i := range m0 {
			if m0[i].RawRule != m1[i].RawRule {
				resDiff++
				break
			}
		}
	}
	t.Logf("WS1-EQUIV ModifyRes: corpus=%d modsDiff=%d lenDiff=%d errDiff=%d", len(aiCorpus), resDiff, resLenDiff, resErrDiff)
	if resDiff != 0 || resLenDiff != 0 || resErrDiff != 0 {
		t.Fatalf("ModifyRes 存在差异")
	}
}
