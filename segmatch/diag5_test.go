package segmatch

import (
	"testing"
)

// [S1b] root 转移微观 trace：i=5/6/7 三步的状态机与 DAT 槽位核对。
func TestDATRootTransitionTrace(t *testing.T) {
	pats := loadSegPatterns(t)
	di := NewDATIndex(pats)
	d := &di.dat
	url := "https://1cf9ea8ea05d.a.fastly.hls.ttvnw.net/v1/segment/CuwCQgWyhqRpKLpStvmirmUbR2R9iuoknaw"
	t.Logf("size=%d base[0]=%d", len(d.check), d.base[0])
	// root 的 '/' 子槽
	st47 := d.base[0] + 47
	t.Logf("root '/' 槽位=%d check=%d depth=%d outOff=%d outLen=%d",
		st47, d.check[st47], d.depth[st47], d.outOff[st47], d.outLen[st47])
	if d.outLen[st47] > 0 {
		t.Logf("  输出=%v", d.outputs[d.outOff[st47]:d.outOff[st47]+d.outLen[st47]])
	}
	// 手动走 i=5..7
	cur := int32(0)
	for i := 5; i <= 7; i++ {
		c := url[i]
		t2 := d.base[cur] + int32(c)
		inRange := int(t2) < len(d.check)
		ok := inRange && d.check[t2] == cur
		t.Logf("i=%d c=%q cur=%d base=%d t=%d check=%v ok=%v",
			i, c, cur, d.base[cur], t2, inRangeAnd(d.check, int(t2), inRange), ok)
		if ok {
			cur = t2
		} else if cur != 0 {
			cur = d.fail[cur]
			t.Logf("  fail-> %d", cur)
			// fail 后重试一次（简化 trace）
			t3 := d.base[cur] + int32(c)
			if int(t3) < len(d.check) && d.check[t3] == cur {
				cur = t3
				t.Logf("  retry -> %d", cur)
			}
		}
		t.Logf("  i=%d 结束 cur=%d outLen=%d", i, cur, d.outLen[cur])
		if d.outLen[cur] > 0 {
			t.Logf("  EMIT start=%d", i+1-int(d.depth[cur]))
		}
	}
}

func inRangeAnd(arr []int32, idx int, inRange bool) int32 {
	if !inRange {
		return -99
	}
	return arr[idx]
}
