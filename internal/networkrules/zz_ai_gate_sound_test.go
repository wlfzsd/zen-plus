package networkrules

// [2026-10-14 域门控必要性验证] 原型（D:\zen-ai-proto）专用。
//
// 性质断言（对应 zz_rex_sound_test.go 的 skip⇒no-match 方法论）：
//
//	gate 排除某规则  ⟹  该规则的 $domain 条件必须为假
//
// 这是域门控剪枝的**充分必要性**直接证据：$domain 属强制 And 修饰符，
// 条件为假 ⇒ ShouldMatchReqNav/ShouldMatchRes 必为假 ⇒ 该规则本就会被丢弃。
// 违反=0 才算通过。
//
// env 门控 ZEN_AI_GATE_SOUND=1。

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rulemodifiers"
)

// aiGateCases 构造三种请求形态，覆盖 matchReqSide 的三条分支。
func aiGateCases(u string) []struct {
	name string
	req  *http.Request
} {
	pu, err := url.Parse(u)
	if err != nil {
		return nil
	}
	mk := func(ref string) *http.Request {
		r := &http.Request{Method: http.MethodGet, URL: pu, Header: http.Header{}}
		if ref != "" {
			r.Header.Set("Referer", ref)
		}
		return r
	}
	return []struct {
		name string
		req  *http.Request
	}{
		{"no-referer", mk("")},
		{"referer-zhihu", mk("https://www.zhihu.com/")},
		{"referer-bad", mk("http://[::1")},
	}
}

func TestZZAIGateSound(t *testing.T) {
	if os.Getenv("ZEN_AI_GATE_SOUND") != "1" {
		t.Skip("ZEN_AI_GATE_SOUND not set")
	}
	aiLoad(t)
	ps := aiEngine.primaryStore
	if ps.idx.Load() == nil {
		ps.idx.Store(ps.buildIdx())
	}
	idx := ps.idx.Load()
	n := len(ps.regexp)

	// 每条规则取它的 $domain 修饰符（若有），用于真实谓词对照。
	domOf := make([]*rulemodifiers.DomainModifier, n)
	for i := range ps.regexp {
		for _, m := range ps.regexp[i].value.AndConditionModifiers() {
			if dm, ok := m.(*rulemodifiers.DomainModifier); ok {
				domOf[i] = dm
				break
			}
		}
	}
	_ = domOf

	var (
		violations        int64
		gatedRules        int
		pairs             int64
		excluded          int64
		domFalseConfirmed int64
	)
	for i := range ps.regexp {
		if ps.gateOf != nil && ps.gateOf(ps.regexp[i].value) != nil {
			gatedRules++
		}
	}

	for _, u := range aiCorpus {
		pu, err := url.Parse(u)
		if err != nil || pu.Hostname() == "" {
			continue
		}
		reqs := aiGateCases(u)
		for _, c := range reqs {
			eff := effHostsOf(c.req)
			var elig hitBits
			idx.markEligible(&eff, &elig)
			for i := range ps.regexp {
				pairs++
				eligible := elig[i/64]&(1<<(i%64)) != 0
				if eligible {
					continue
				}
				excluded++
				// gate 排除了它：$domain 条件必须为假（否则就是错配）。
				if dm := domOf[i]; dm != nil {
					if dm.ShouldMatchReq(c.req) {
						violations++
						if violations <= 5 {
							t.Errorf("VIOLATION: rule %d 被 gate 排除但 $domain 为真 | url=%s case=%s",
								i, u, c.name)
						}
					} else {
						domFalseConfirmed++
					}
				}
				// 无 $domain 的规则绝不应被 gate 排除（gateOf==nil ⇒ alwaysElig）。
				if dm := domOf[i]; dm == nil {
					violations++
					if violations <= 5 {
						t.Errorf("VIOLATION: 无 $domain 规则 %d 被 gate 排除 | url=%s", i, u)
					}
				}
			}
		}
	}

	t.Logf("GATE-SOUND: rules=%d gatedRules=%d pairs=%d excluded=%d domFalseConfirmed=%d violations=%d",
		n, gatedRules, pairs, excluded, domFalseConfirmed, violations)

	outDir := os.Getenv("ZEN_AI_REX_OUT")
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "zen_ai_rex_attrib")
	}
	_ = os.MkdirAll(outDir, 0o755)
	line := fmt.Sprintf("rules=%d gatedRules=%d pairs=%d excluded=%d excludedPct=%.2f violations=%d\n",
		n, gatedRules, pairs, excluded, 100*float64(excluded)/float64(pairs), violations)
	if err := os.WriteFile(filepath.Join(outDir, "gate_sound.txt"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if violations != 0 {
		t.Fatalf("门控必要性断言违反=%d（必须为 0）", violations)
	}
}
