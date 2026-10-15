package segmatch

import (
	"testing"
)

// [S1c 最小复现] "beta"/"bet" 前缀链污染最小集。
func TestDATBetaMinimal(t *testing.T) {
	pats := []string{"/beta", "/bet"}
	di := NewDATIndex(pats)
	url := "https://x/betaa"
	got := di.MatchAll(url)
	t.Logf("MatchAll=%v（期望空或仅 /bet@...）", got)
	// 直接扫
	di.ScanEvents(url, func(id int32, start int32) {
		t.Logf("event lit=%q start=%d", di.lits[id], start)
	})
	// 前缀 "beta" 的语义检查：len=4 的串 "x/beta"——"bet" 在 2..5 结束？
	// url="https://x/betaa"：'/b' 在 10..11，"bet"=11..13，"beta"=11..14
	// i=14（'a'）时状态=beta 节点：merged 应含 "beta" 不含 "bet"
	d := &di.dat
	// 找 beta 节点：沿 /b e t a 走
	cur := int32(0)
	walk := func(s string) int32 {
		c := cur
		for i := 0; i < len(s); i++ {
			t2 := d.base[c] + int32(s[i])
			if int(t2) < len(d.check) && d.check[t2] == c {
				c = t2
			}
		}
		return c
	}
	bn := walk("/beta")
	t.Logf("beta 节点=%d outOff=%d outLen=%d outs=%v", bn, d.outOff[bn], d.outLen[bn],
		d.outputs[d.outOff[bn]:d.outOff[bn]+d.outLen[bn]])
	tn := walk("/bet")
	t.Logf("bet  节点=%d outOff=%d outLen=%d outs=%v", tn, d.outOff[tn], d.outLen[tn],
		d.outputs[d.outOff[tn]:d.outOff[tn]+d.outLen[tn]])
	t.Logf("fail(beta)=%d fail(bet)=%d", d.fail[bn], d.fail[tn])
}
