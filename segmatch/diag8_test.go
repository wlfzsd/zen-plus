package segmatch

import (
	"strings"
	"testing"
)

// [S1f 终局] 失败转移 's'@785521 的槽位归属 + "https" 前缀实走状态。
func TestDATSlotOwnership(t *testing.T) {
	pats := loadSegPatterns(t)
	di := NewDATIndex(pats)
	d := &di.dat
	url := "https://1cf9ea8ea05d.a.fastly.hls.ttvnw.net/v1/segment/CuwCQgWyhqRpKLpStvmirmUbR2R9iuoknaw"
	// 走到 "http" 节点
	cur := int32(0)
	for i := 0; i < 4; i++ {
		cur = d.base[cur] + int32(url[i])
	}
	t.Logf("http 节点=%d base=%d depth=%d", cur, d.base[cur], d.depth[cur])
	sSlot := d.base[cur] + int32('s')
	t.Logf("'s' 槽位=%d check=%d（期望 %d） depth=%d outLen=%d", sSlot, d.check[sSlot], cur, d.depth[sSlot], d.outLen[sSlot])
	if int(sSlot) < len(d.check) && d.check[sSlot] != -1 && d.check[sSlot] != cur {
		// 谁占了：枚举所有状态 X 使 base[X]+'s' == sSlot 且 check==X
		owner := int32(-1)
		for x := int32(0); x < int32(len(d.base)); x++ {
			if d.base[x]+int32('s') == sSlot && d.check[sSlot] == x {
				owner = x
				break
			}
		}
		t.Logf("槽位被状态 %d 占用（其 depth=%d outLen=%d）", owner, d.depth[owner], d.outLen[owner])
	}
	// "https" 前缀实走
	cur2 := int32(0)
	valid := true
	for i := 0; i < 5; i++ {
		t2 := d.base[cur2] + int32(url[i])
		if int(t2) < len(d.check) && d.check[t2] == cur2 {
			cur2 = t2
		} else {
			t.Logf("前缀断在 i=%d (%q)", i, url[i])
			valid = false
			break
		}
	}
	if valid {
		t.Logf("https 前缀走到=%d depth=%d outLen=%d", cur2, d.depth[cur2], d.outLen[cur2])
	}
	// 有多少模式含 "https" 前缀（字面量）
	n := 0
	for _, lit := range di.lits {
		if strings.HasPrefix(lit, "https") {
			n++
		}
	}
	t.Logf("以 https 开头的字面量数=%d", n)
	// "http" 节点本身存在吗（字面量恰好 "http"）？
	for _, lit := range di.lits {
		if lit == "http" {
			t.Logf("存在恰好=http 的字面量")
		}
	}
}
