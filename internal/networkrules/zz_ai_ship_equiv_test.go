package networkrules

// [交付态全语料等价终检] 交付 exe 的确切开关组合：
//   domainGate=ON（默认）/ hostSplit=OFF（默认）/ v2Root=OFF（默认）
// vs 生产行为（全关）。ModifyReq block/redirect/multiset + ModifyRes multiset。
// 这是部署前最后一道行为门禁。env: ZEN_AI_SHIP_EQ=1
import (
	"net/http"
	"net/url"
	"os"
	"testing"
)

func TestZZAIShipEquivalence(t *testing.T) {
	if os.Getenv("ZEN_AI_SHIP_EQ") != "1" {
		t.Skip("ZEN_AI_SHIP_EQ not set")
	}
	aiLoad(t)
	// 生产行为 = 全部新开关关闭
	eProd := New(WithDomainGate(false), WithHostSplit(false), WithV2Root(false))
	// 交付态 = 生产默认（domainGate 默认 ON，hostSplit/v2Root 默认 OFF）
	eShip := New()
	hmLoadLists(t, eProd)
	hmLoadLists(t, eShip)

	reqOf := func(u string) *http.Request {
		pu, err := url.Parse(u)
		if err != nil || pu.Host == "" {
			return nil
		}
		return &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
	}
	var blockDiff, redirDiff, setDiff int
	for _, u := range aiCorpus {
		r0, r1 := reqOf(u), reqOf(u)
		if r0 == nil {
			continue
		}
		ap0, b0, red0 := eProd.ModifyReq(r0)
		ap1, b1, red1 := eShip.ModifyReq(r1)
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
				t.Errorf("SHIP 多重集差异 url=%.80s\n  prod(n=%d): %v\n  ship(n=%d): %v", u, len(k0), head(k0, 3), len(k1), head(k1, 3))
			}
		}
	}
	t.Logf("SHIP-EQUIV ModifyReq: corpus=%d blockDiff=%d redirectDiff=%d multisetDiff=%d（WS1 保序 → 三项必须全 0）",
		len(aiCorpus), blockDiff, redirDiff, setDiff)
	if blockDiff != 0 || redirDiff != 0 || setDiff != 0 {
		t.Fatalf("交付态与生产行为不一致")
	}

	// ModifyRes
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
		m0, _ := eProd.ModifyRes(r0, mk(r0))
		m1, _ := eShip.ModifyRes(r1, mk(r1))
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
	t.Logf("SHIP-EQUIV ModifyRes: corpus=%d multisetDiff=%d", len(aiCorpus), resDiff)
	if resDiff != 0 {
		t.Fatalf("ModifyRes 交付态不一致")
	}
}
