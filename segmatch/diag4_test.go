package segmatch

import (
	"sort"
	"testing"
)

// [S1 定位/验收器] map 版 Index（差分已证正确）vs DAT 版 ScanEvents 逐事件对拍。
// 事件流规范化：litID → 去重升序 start 集合，首个分歧即 bug 点。
func TestDATScanVsIndexScan(t *testing.T) {
	pats := loadSegPatterns(t)
	di := NewDATIndex(pats)
	url := "https://1cf9ea8ea05d.a.fastly.hls.ttvnw.net/v1/segment/CuwCQgWyhqRpKLpStvmirmUbR2R9iuoknaw"

	mi := NewIndex(pats)
	mh := mi.scan(url)
	dh := map[int][]int32{}
	di.ScanEvents(url, func(id int32, end int32) {
		dh[int(id)] = append(dh[int(id)], end-int32(len(di.lits[id]))+1)
	})

	ids := map[int]bool{}
	for id := range mh {
		ids[id] = true
	}
	for id := range dh {
		ids[id] = true
	}
	var diffIDs []int
	for id := range ids {
		a := uniq32(mh[id])
		b := uniq32(dh[id])
		sort.Slice(a, func(x, y int) bool { return a[x] < a[y] })
		sort.Slice(b, func(x, y int) bool { return b[x] < b[y] })
		if len(a) != len(b) {
			diffIDs = append(diffIDs, id)
			if len(diffIDs) <= 6 {
				t.Errorf("lit id=%d map文本=%q dat文本=%q: map有(%d)=%v dat有(%d)=%v", id, mi.lits[id].s, di.lits[id], len(a), a, len(b), b)
			}
			continue
		}
		for k := range a {
			if a[k] != b[k] {
				diffIDs = append(diffIDs, id)
				if len(diffIDs) <= 6 {
					t.Errorf("lit id=%d (%q/%q) 位次不齐: map=%v dat=%v", id, mi.lits[id].s, di.lits[id], a, b)
				}
				break
			}
		}
	}
	t.Logf("SCAN-VS-SCAN: url_len=%d 命中种数 map=%d dat=%d 差异字面量=%d/%d",
		len(url), len(mh), len(dh), len(diffIDs), len(ids))
	if len(diffIDs) != 0 {
		t.Fatalf("scan 层存在分歧")
	}
	t.Logf("scan 层完全一致")
}

func uniq32(a []int32) []int32 {
	if len(a) == 0 {
		return a
	}
	out := a[:1]
	for _, x := range a[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}
