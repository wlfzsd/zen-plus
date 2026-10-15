package networkrules_test

// [2026-10-13 黄金对拍] Arena 无损性验证：同语料全量决策 FNV 哈希。
// 每请求哈希：命中规则序列（RawRule＋FilterName 内容身份）＋shouldBlock＋redirectURL。
// ZEN_GOLDEN_DUMP=1 才运行。新旧实现各跑一次，哈希相等＝判定面零变化。
import (
	"fmt"
	"hash/fnv"
	"os"
	"testing"
)

func TestZZGoldenDump(t *testing.T) {
	if os.Getenv("ZEN_GOLDEN_DUMP") != "1" {
		t.Skip("ZEN_GOLDEN_DUMP not set")
	}
	freshLoad(t)
	h := fnv.New64a()
	n := 0
	blocked := 0
	redirs := 0
	for _, req := range freshCorpus {
		r2 := req.Clone(req.Context())
		rules, block, redirect := freshEngine.ModifyReq(r2)
		for _, rr := range rules {
			fn := ""
			if rr.FilterName != nil {
				fn = *rr.FilterName
			}
			fmt.Fprintf(h, "R|%s|%s\n", rr.RawRule, fn)
			n++
		}
		if block {
			blocked++
		}
		if redirect != "" {
			redirs++
		}
		fmt.Fprintf(h, "D|%v|%s\n", block, redirect)
	}
	t.Logf("GOLDEN rules=%d blocked=%d redirects=%d hash=%016x", n, blocked, redirs, h.Sum64())
}
