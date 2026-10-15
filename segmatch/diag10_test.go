package segmatch

import (
	"testing"
)

// [S2 定位] /banner/* 单模式 vs 失败 URL——区分排布 bug 与模式间交互。
func TestDATBannerSingle(t *testing.T) {
	di := NewDATIndex([]string{"/banner/*"})
	url := "https://api-v2.soundcloud.com/smartnudges/banner/0?session_id=abc"
	t.Logf("enc=%v patEncOff=%v", di.enc, di.patEncOff)
	hits := map[int][]int32{}
	di.ScanEvents(url, func(id int32, end int32) {
		hits[int(id)] = append(hits[int(id)], end-int32(len(di.lits[id]))+1)
	})
	for id, v := range hits {
		t.Logf("lit=%q starts=%v", di.lits[id], v)
	}
	got := di.MatchAll(url)
	t.Logf("单模式 MatchAll=%v（期望 [0]）", got)
	if len(got) != 1 {
		t.Errorf("单模式即失败——排布/编码 bug")
	}
}
