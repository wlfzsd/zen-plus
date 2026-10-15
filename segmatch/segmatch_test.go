package segmatch

import (
	"strings"
	"testing"
	"time"
)

// [P2 核心件 · 差分验证 1] 段匹配器 Match vs ruletree 甲骨文。
func TestSegMatchDifferential(t *testing.T) {
	pats := loadSegPatterns(t)
	urls := loadURLs(t)
	t.Logf("patterns=%d urls=%d", len(pats), len(urls))

	tree, has := ruletreeNewOracle(t, pats)
	_ = tree

	sample := segSample(t, urls, 1606)
	var diffs int
	for _, p := range pats {
		parsed := Parse(p)
		if parsed.Rejected {
			continue
		}
		for _, u := range sample {
			treeHit := has(u, p)
			mine := Match(parsed, u)
			if treeHit != mine {
				diffs++
				if diffs <= 8 {
					t.Errorf("DIFF pattern=%q url=%.90s tree=%v seg=%v", p, u, treeHit, mine)
				}
			}
		}
	}
	t.Logf("SEG-DIFF: patterns=%d sample=%d diffs=%d", len(pats), len(sample), diffs)
	if diffs != 0 {
		t.Fatalf("语义差分未通过: diffs=%d", diffs)
	}
}

// [P2 核心件 · 速度基线] Match 逐对耗时（性能化前对照）。
func TestSegMatchSpeed(t *testing.T) {
	pats := loadSegPatterns(t)
	urls := loadURLs(t)
	var parsed []Parsed
	for _, p := range pats {
		pp := Parse(p)
		if !pp.Rejected {
			parsed = append(parsed, pp)
		}
	}
	n := 600
	if len(urls) < n {
		n = len(urls)
	}
	sub := urls[:n]
	t0 := now()
	hits := 0
	for _, pp := range parsed {
		for _, u := range sub {
			if Match(pp, u) {
				hits++
			}
		}
	}
	el := since(t0)
	pairs := len(parsed) * len(sub)
	t.Logf("SPEED: pairs=%d hits=%d elapsed=%v per-pair=%v", pairs, hits, el, el/time.Duration(pairs))
	_ = strings.Contains
}
