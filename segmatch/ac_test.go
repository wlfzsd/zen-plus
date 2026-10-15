package segmatch

import (
	"testing"
	"time"
)

// 差分 2：MatchIndex（AC 索引版）vs 甲骨文 ruletree，同口径全对拍。
func TestSegMatchACDifferential(t *testing.T) {
	pats := loadSegPatterns(t)
	urls := loadURLs(t)
	ix := NewIndex(pats)
	tree, has := ruletreeNewOracle(t, pats)
	_ = tree

	sample := segSample(t, urls, 1606)
	var diffs int
	for _, p := range pats {
		pi := patIndex(pats, p)
		parsed := Parse(p)
		if parsed.Rejected {
			continue
		}
		for _, u := range sample {
			treeHit := has(u, p)
			mine := ix.MatchIndex(pi, u)
			if treeHit != mine {
				diffs++
				if diffs <= 8 {
					t.Errorf("AC-DIFF pattern=%q url=%.90s tree=%v ac=%v", p, u, treeHit, mine)
				}
			}
		}
	}
	t.Logf("AC-DIFF: patterns=%d sample=%d diffs=%d", len(pats), len(sample), diffs)
	if diffs != 0 {
		t.Fatalf("AC 语义差分未通过: diffs=%d", diffs)
	}
}

// 差分 3：MatchIndex vs Match（两实现互证，比树快，可全对拍）
func TestSegMatchACvsMatch(t *testing.T) {
	pats := loadSegPatterns(t)
	urls := loadURLs(t)
	ix := NewIndex(pats)
	var parsed []Parsed
	idx := map[string]int{}
	for i, p := range pats {
		pp := Parse(p)
		if pp.Rejected {
			continue
		}
		parsed = append(parsed, pp)
		idx[p] = i
	}
	sample := segSample(t, urls, 1606)
	var diffs int
	for i, pp := range parsed {
		for _, u := range sample {
			a := Match(pp, u)
			b := ix.MatchIndex(idx[pats[i]], u)
			if a != b {
				diffs++
				if diffs <= 8 {
					t.Errorf("MM-DIFF pattern=%q url=%.90s match=%v ac=%v", pats[i], u, a, b)
				}
			}
		}
	}
	t.Logf("AC-vs-MATCH: patterns=%d sample=%d diffs=%d", len(parsed), len(sample), diffs)
	if diffs != 0 {
		t.Fatalf("AC 与 Match 语义不一致")
	}
}

// 速度：索引版每对耗时
func TestSegMatchACSpeed(t *testing.T) {
	pats := loadSegPatterns(t)
	urls := loadURLs(t)
	ix := NewIndex(pats)
	var pis []int
	for i := range pats {
		pis = append(pis, i)
	}
	n := 600
	if len(urls) < n {
		n = len(urls)
	}
	sub := urls[:n]
	t0 := now()
	hits := 0
	for _, pi := range pis {
		for _, u := range sub {
			if ix.MatchIndex(pi, u) {
				hits++
			}
		}
	}
	el := since(t0)
	pairs := len(pis) * len(sub)
	t.Logf("AC-SPEED: pairs=%d hits=%d elapsed=%v per-pair=%v", pairs, hits, el, el/time.Duration(pairs))
}

// 差分 4：MatchAll 批量 vs 甲骨文（引擎实际调用形态）
func TestSegMatchAllDifferential(t *testing.T) {
	pats := loadSegPatterns(t)
	urls := loadURLs(t)
	ix := NewIndex(pats)
	_, has := ruletreeNewOracle(t, pats)
	sample := segSample(t, urls, 1606)
	var diffs int
	for _, u := range sample {
		got := map[string]bool{}
		for _, pi := range ix.MatchAll(u) {
			got[pats[pi]] = true
		}
		for _, p := range pats {
			want := has(u, p)
			if got[p] != want {
				diffs++
				if diffs <= 8 {
					t.Errorf("ALL-DIFF pattern=%q url=%.90s tree=%v all=%v", p, u, want, got[p])
				}
			}
		}
	}
	t.Logf("ALL-DIFF: sample=%d diffs=%d", len(sample), diffs)
	if diffs != 0 {
		t.Fatalf("MatchAll 语义差分未通过")
	}
}

// 速度：MatchAll 每 URL 耗时（引擎口径）
func TestSegMatchAllSpeed(t *testing.T) {
	pats := loadSegPatterns(t)
	urls := loadURLs(t)
	ix := NewIndex(pats)
	n := 600
	if len(urls) < n {
		n = len(urls)
	}
	sub := urls[:n]
	t0 := now()
	var total int
	for _, u := range sub {
		total += len(ix.MatchAll(u))
	}
	el := since(t0)
	t.Logf("ALL-SPEED: urls=%d hits=%d elapsed=%v per-url=%v", n, total, el, el/time.Duration(n))
}

// 拆账：scan 与归并各占多少
func TestSegScanVsMerge(t *testing.T) {
	pats := loadSegPatterns(t)
	urls := loadURLs(t)
	ix := NewIndex(pats)
	n := 600
	if len(urls) < n {
		n = len(urls)
	}
	sub := urls[:n]
	t0 := now()
	for _, u := range sub {
		ix.scan(u)
	}
	scanEl := since(t0)
	t0 = now()
	var total int
	for _, u := range sub {
		total += len(ix.MatchAll(u))
	}
	allEl := since(t0)
	t.Logf("SCAN-VS-MERGE: scan=%v (%v/url)  matchall=%v (%v/url)  归并占=%.0f%%", scanEl, scanEl/time.Duration(n), allEl, allEl/time.Duration(n), 100*float64(allEl-scanEl)/float64(allEl))
}
