package ruletree

// prune_values_test.go — 2026-10-09 PruneValues 单元测试：
// 剪枝正确性（删值/空节点摘除/幸存值顺序/根永存）、与 Get/GetLP/Compact
// 的互操作、以及去重后的匹配语义不变。

import (
	"fmt"
	"testing"
)

func mustInsertAll[T Data](t *testing.T, tr *Tree[T], pairs map[string]T) {
	t.Helper()
	for pattern, v := range pairs {
		tr.Insert(pattern, v)
	}
}

func setOf[T comparable](vals []T) map[T]struct{} {
	m := make(map[T]struct{}, len(vals))
	for _, v := range vals {
		m[v] = struct{}{}
	}
	return m
}

// TestPruneValuesKeepAll 验证 drop 全假时树不变。
func TestPruneValuesKeepAll(t *testing.T) {
	tr := New[int]()
	mustInsertAll(t, tr, map[string]int{
		"||example.com^":     1,
		"||www.example.com^": 2,
		"example.org":        3,
		"||example.com/ads":  4,
	})
	if n := tr.PruneValues(func(int) bool { return false }); n != 0 {
		t.Fatalf("PruneValues removed %d, want 0", n)
	}
	for _, url := range []string{
		"https://www.example.com/x", "https://example.com/x", "https://ads.example.org/y",
		"https://example.com/ads/c",
	} {
		got := tr.GetLP(url)
		if len(got) == 0 {
			t.Fatalf("url %q: no candidates after keep-all prune", url)
		}
	}
}

// TestPruneValuesCoveredSubdomain 验证剪掉被覆盖的子域规则后：
// 幸存规则照常命中、被删规则不再出现、空节点链被摘除（Get 结果恒等）。
func TestPruneValuesCoveredSubdomain(t *testing.T) {
	tr := New[int]()
	tr.Insert("||example.com^", 1)      // 幸存（覆盖者）
	tr.Insert("||www.example.com^", 2)  // 被覆盖 → 删
	tr.Insert("||mail.example.com^", 3) // 独立规则，保留

	if n := tr.PruneValues(func(v int) bool { return v == 2 }); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	if got := tr.GetLP("https://www.example.com/a"); len(got) != 1 || got[0] != 1 {
		t.Fatalf("www.example.com after prune = %v, want [1]", got)
	}
	if got := tr.GetLP("https://mail.example.com/a"); len(got) != 2 || got[0] != 3 || got[1] != 1 {
		t.Fatalf("mail.example.com after prune = %v, want [3 1] (mail 规则在前＋覆盖者 example.com)", got)
	}
	if got := tr.GetLP("https://example.com/a"); len(got) != 1 || got[0] != 1 {
		t.Fatalf("example.com after prune = %v, want [1]", got)
	}
}

// TestPruneValuesEmptyLeafAndRoot 验证：叶值删空后 isLeaf 语义复位；
// 三根即使剪空也存活（Get 返回空而非 panic）。
func TestPruneValuesEmptyLeafAndRoot(t *testing.T) {
	tr := New[string]()
	tr.Insert("||solo.example^", "a")
	if n := tr.PruneValues(func(string) bool { return true }); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	if got := tr.GetLP("https://solo.example/x"); len(got) != 0 {
		t.Fatalf("expected no candidates, got %v", got)
	}
	// 剪空后树仍可用：可继续 Insert。
	tr.Insert("||solo.example^", "b")
	if got := tr.GetLP("https://solo.example/x"); len(got) != 1 || got[0] != "b" {
		t.Fatalf("after re-insert got %v, want [b]", got)
	}
}

// TestPruneValuesOrderStable 验证幸存值的叶内相对顺序保持。
func TestPruneValuesOrderStable(t *testing.T) {
	tr := New[int]()
	tr.Insert("||order.test^", 10)
	tr.Insert("||order.test^", 20)
	tr.Insert("||order.test^", 30)
	// 删除中间值 20，幸存顺序应保持 10, 30。
	if n := tr.PruneValues(func(v int) bool { return v == 20 }); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	got := tr.GetLP("https://order.test/x")
	if len(got) != 2 || got[0] != 10 || got[1] != 30 {
		t.Fatalf("order after prune = %v, want [10 30]", got)
	}
}

// TestPruneValuesWithCompact 验证剪枝后 Compact 正常且行为不变。
func TestPruneValuesWithCompact(t *testing.T) {
	tr := New[int]()
	for i := 1; i <= 5; i++ {
		tr.Insert(fmt.Sprintf("||sub%d.deep.example.com^", i), i)
	}
	tr.Insert("||example.com^", 100)
	if n := tr.PruneValues(func(v int) bool { return v >= 1 && v <= 5 }); n != 5 {
		t.Fatalf("removed %d, want 5", n)
	}
	tr.Compact()
	for i := 1; i <= 5; i++ {
		url := fmt.Sprintf("https://sub%d.deep.example.com/x", i)
		if got := tr.GetLP(url); len(got) != 1 || got[0] != 100 {
			t.Fatalf("url %q after prune+compact = %v, want [100]", url, got)
		}
	}
	if got := tr.GetLP("https://other.org/"); len(got) != 0 {
		t.Fatalf("other.org = %v, want empty", got)
	}
}

// TestPruneValuesDeterministicSequences 验证剪枝不破坏 Get/GetLP 的
// 确定性首现序列（P6 语义）：多候选、通配符邻居共存时幸存值序列稳定。
func TestPruneValuesDeterministicSequences(t *testing.T) {
	tr := New[int]()
	tr.Insert("||det.test^", 1)
	tr.Insert("||*.det.test^", 2)
	tr.Insert("||det.test/*/x", 3)
	if n := tr.PruneValues(func(v int) bool { return v == 3 }); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	want := []int{2, 1} // 通配符邻居命中在前（锚根/根遍历序），叶序稳定
	for i := 0; i < 3; i++ {
		got := tr.GetLP("https://a.det.test/y")
		if len(got) != len(want) {
			t.Fatalf("run %d: got %v, want %v", i, got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("run %d: got %v, want %v", i, got, want)
			}
		}
	}
}
