package networkrules

// [2026-10-14 方案A 差异定位] 对等价探针报出的样本规则对，逐引擎检查存活状态。
// env: ZEN_AI_HM_DEBUG=1

import (
	"os"
	"testing"

	"github.com/irbis-sh/zen-desktop/internal/networkrules/rule"
)

func TestZZAIHostMapDebug(t *testing.T) {
	if os.Getenv("ZEN_AI_HM_DEBUG") != "1" {
		t.Skip("ZEN_AI_HM_DEBUG not set")
	}
	targets := []string{
		"||browser.events.data.msn.com^",
		"||msn.com/OneCollector/",
		"||conde.digital^",
		"||ads-static.conde.digital^",
	}

	mk := func(split bool) *NetworkRules {
		e := New(WithHostSplit(split))
		hmLoadLists(t, e)
		return e
	}
	e0 := mk(false)
	e1 := mk(true)

	count := func(e *NetworkRules) map[string]int {
		m := map[string]int{}
		e.primaryStore.walkValues(func(r *rule.Rule) {
			for _, tg := range targets {
				if r.RawRule == tg {
					m[tg]++
				}
			}
		})
		return m
	}
	c0 := count(e0)
	c1 := count(e1)
	t.Logf("off 存活: %v", c0)
	t.Logf("on  存活: %v", c1)
	for _, tg := range targets {
		if c0[tg] != c1[tg] {
			t.Errorf("存活数不同 %q: off=%d on=%d", tg, c0[tg], c1[tg])
		}
	}

	// on 引擎：hostRules 里有哪些目标规则 + 禁用态检查
	e1.primaryStore.hostMu.RLock()
	defer e1.primaryStore.hostMu.RUnlock()
	inMap := map[string]int{}
	for _, vals := range e1.primaryStore.hostRules {
		for _, v := range vals {
			for _, tg := range targets {
				if v.RawRule == tg {
					inMap[tg]++
					if v.BadfilterDisableState() != nil {
						t.Logf("  %q 在 hostRules 且带禁用态 %+v", tg, v.BadfilterDisableState())
					}
				}
			}
		}
	}
	t.Logf("on hostRules 中的目标规则: %v", inMap)
}
