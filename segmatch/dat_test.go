package segmatch

import (
	"strings"
	"testing"
	"time"
)

// [T2] DAT-AC 差分 vs 甲骨文 ruletree（与 Index/Match 同门禁）。
func TestDATDifferential(t *testing.T) {
	pats := loadSegPatterns(t)
	urls := loadURLs(t)
	di := NewDATIndex(pats)
	_, has := ruletreeNewOracle(t, pats)
	sample := segSample(t, urls, 1606)
	var diffs int
	badPat := map[string]int{}
	badKind := map[string]int{}
	for _, p := range pats {
		pi := patIndex(pats, p)
		if pi < 0 || di.rejected[pi] {
			continue
		}
		for _, u := range sample {
			want := has(u, p)
			got := false
			for _, hit := range di.MatchAll(u) {
				if hit == pi {
					got = true
					break
				}
			}
			if want != got {
				diffs++
				badPat[p]++
				kind := "other"
				switch {
				case !strings.ContainsAny(p, "*^"):
					kind = "pure-literal"
				case !strings.Contains(p, "^"):
					kind = "wild-only"
				case !strings.Contains(p, "*"):
					kind = "sep-only"
				default:
					kind = "wild+sep"
				}
				badKind[kind]++
				if len(badPat) <= 11 {
					t.Errorf("DAT-DIFF pattern=%q url=%.60s tree=%v dat=%v", p, u, want, got)
				}
			}
		}
	}
	t.Logf("DAT-DIFF: patterns=%d sample=%d 对差=%d 唯一失败模式=%d 分类=%v",
		len(pats), len(sample), diffs, len(badPat), badKind)
	for p, c := range badPat {
		t.Logf("  BAD[%d对] %q", c, p)
	}
}

// [T2b] DAT vs Match 互证（更大量）。对拍按模式文本（全量集存在同文本重复记录，
// MatchAll 返回每条记录的下标，必须先映回文本再比——否则误报）。
func TestDATvsMatch(t *testing.T) {
	pats := loadSegPatterns(t)
	urls := loadURLs(t)
	di := NewDATIndex(pats)
	sample := segSample(t, urls, 1611)
	var parsed []Parsed
	for _, p := range pats {
		pp := Parse(p)
		if pp.Rejected {
			continue
		}
		parsed = append(parsed, pp)
	}
	var diffs int
	for i, pp := range parsed {
		for _, u := range sample {
			a := Match(pp, u)
			b := false
			for _, hit := range di.MatchAll(u) {
				if pats[hit] == pats[i] {
					b = true
					break
				}
			}
			if a != b {
				diffs++
				if diffs <= 8 {
					t.Errorf("DM-DIFF pattern=%q url=%.90s match=%v dat=%v", pats[i], u, a, b)
				}
			}
		}
	}
	t.Logf("DAT-vs-MATCH: patterns=%d sample=%d diffs=%d", len(parsed), len(sample), diffs)
	if diffs != 0 {
		t.Fatalf("DAT 与 Match 不一致")
	}
}

// [T3] DAT 速度：MatchAll 每 URL 耗时 + 纯 scan 耗时。
func TestDATSpeed(t *testing.T) {
	pats := loadSegPatterns(t)
	urls := loadURLs(t)
	di := NewDATIndex(pats)
	n := 600
	if len(urls) < n {
		n = len(urls)
	}
	sub := urls[:n]
	t0 := now()
	var total int
	for _, u := range sub {
		total += len(di.MatchAll(u))
	}
	el := since(t0)
	t.Logf("DAT-SPEED: urls=%d hits=%d MatchAll=%v per-url=%v", n, total, el, el/time.Duration(n))
	// 纯 scan
	t0 = now()
	for _, u := range sub {
		di.ScanEvents(u, func(int32, int32) {})
	}
	el2 := since(t0)
	t.Logf("DAT-SCAN: %v per-url=%v", el2, el2/time.Duration(n))
}
