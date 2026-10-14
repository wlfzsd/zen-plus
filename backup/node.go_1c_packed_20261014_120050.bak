package ruletree

import (
	"sort"
	"strings"
	"unsafe"

	"github.com/irbis-sh/zen-desktop/internal/ruletree/byteset"
)

type litEdge[T Data] struct {
	label token
	node  *node[T]
}

// 2026-10-05: the wildcard/separator/anchor child pointers were merged into
// the sorted edges slice (their token values sort after every literal
// label), and the prefix slice header was replaced by a pointer+length pair,
// shrinking the node from 96 to 64 bytes. Special children are now looked up
// via getEdge.
type node[T Data] struct {
	// leaf stores a possible leaf.
	leaf []T

	// prefixBase and prefixLen hold the node's common prefix. They replace
	// a []token header so that the whole node fits the 64-byte size class.
	prefixBase *token
	prefixLen  int

	// edges stores child edges in sorted label order: literal-character
	// labels (0-255) first, then the special tokens tokenWildcard,
	// tokenSeparator and tokenAnchor.
	edges []litEdge[T]
}

// prefixSlice reconstructs the node's prefix as a slice. It is O(1) and does
// not copy.
func (n *node[T]) prefixSlice() []token {
	return unsafe.Slice(n.prefixBase, n.prefixLen)
}

// prefixBaseOf returns a pointer to the first token of s, or nil when s is
// empty.
func prefixBaseOf(s []token) *token {
	if len(s) == 0 {
		return nil
	}
	return &s[0]
}

func (n *node[T]) isLeaf() bool {
	return n.leaf != nil
}

func (n *node[T]) addEdge(label token, e *node[T]) {
	idx := sort.Search(len(n.edges), func(i int) bool {
		return n.edges[i].label >= label
	})

	n.edges = append(n.edges, litEdge[T]{})
	copy(n.edges[idx+1:], n.edges[idx:])
	n.edges[idx] = litEdge[T]{label, e}
}

func (n *node[T]) updateEdge(label token, node *node[T]) {
	idx := sort.Search(len(n.edges), func(i int) bool {
		return n.edges[i].label >= label
	})
	if idx < len(n.edges) && n.edges[idx].label == label {
		n.edges[idx].node = node
	}
}

// edgeLinearScanMax (2026-10-09 落地建议（eff_probe 沙箱原型验证：黄金对拍 0 差异＋5 轮 A/B）)：扇出不超过该值的节点用线性
// 扫描替代 sort.Search 的闭包二分——CPU 画像显示 sort.Search+闭包占
// ModifyReq 8.3%（getEdge 63.9%/36.1% 分属 primary/exception 树），而树内
// 深层节点扇出普遍很小。语义严格等价：edges 按 label 升序（addEdge 维护），
// 线性扫描命中首个 label==label 或越过即 nil，与 sort.Search 找首个
// label>=label 再判等完全一致。
const edgeLinearScanMax = 24

func (n *node[T]) getEdge(label token) *node[T] {
	if len(n.edges) <= edgeLinearScanMax {
		for i := range n.edges {
			l := n.edges[i].label
			if l == label {
				return n.edges[i].node
			}
			if l > label {
				return nil
			}
		}
		return nil
	}
	idx := sort.Search(len(n.edges), func(i int) bool {
		return n.edges[i].label >= label
	})
	if idx < len(n.edges) && n.edges[idx].label == label {
		return n.edges[idx].node
	}
	return nil
}

// vKey identifies one sub-traversal state: node, how many prefix tokens
// remain, and the base-URL offset the url suffix starts at (2026-10-08 P6
// memo). traversePrefix memoizes on this key, covering every recursion
// state — including the within-node wildcard position enumeration
// (case tokenWildcard default) and separator double-recursion that never
// pass through visit.
type vKey[T Data] struct {
	n    *node[T]
	plen int
	off  int
}

// traverser holds the state for a single traverse() call.
type traverser[T Data] struct {
	data []T
	n    *node[T]

	// dedup (2026-10-08 P6): when non-nil, leaf values already collected in
	// this traversal are skipped, so the accumulator holds each value exactly
	// once, in first-occurrence order. GetLP sets it from a pooled map: a
	// wildcard tail re-visits the same leaf at every URL position (see
	// traverseWildcardTail), which previously appended one copy per position
	// and let a 68 KB URL grow the accumulator to ~180 MB per store
	// (leak_capture heap_2.txt node.go:112 / goroutine_2.txt). nil keeps the
	// plain append semantics used by traverse()/Get.
	dedup map[T]struct{}

	// base (2026-10-08 P6 memo): the full URL this traversal walks. Every
	// url string reaching visit/traversePrefix is a suffix of base, so
	// len(base)-len(url) is the offset the suffix starts at.
	base string

	// visited (2026-10-08 P6 memo): when non-nil, a (node, offset) pair is
	// traversed at most once per call. Combinatorial URLs (dense traversal
	// markers) re-enter the same sub-traversal from hundreds of starting
	// offsets — e.g. a 1017-char alicdn "??,..." combo URL exceeded 15 s in
	// GetLP (normal: ~16 µs) because each of the ~hundreds marker positions
	// re-walked the same wildcard subtrees, exponentially re-visiting
	// (node, offset) pairs. Skipping a REPEAT entry is semantics-neutral:
	// the leaf values a repeat would collect are already in dedup (their
	// first entry inserted them in the same relative order), so the output
	// sequence is bit-identical. nil keeps the un-memoized walk.
	visited map[vKey[T]]struct{}

	// tailMin (2026-10-08 P6 memo): per-call smallest offset each
	// wildcard-tail node (prefix == single wildcard token) has walked. A
	// tail walk at offset m scans and dispatches over EVERY suffix ≥ m, so
	// its collection is a superset of any walk at offset ≥ m at the same
	// node — later entries are accumulator-neutral and skipped. This
	// collapses the per-marker-position root restarts (each re-dispatching
	// the same wildcard tails) from O(positions²) to one walk per tail node.
	tailMin map[*node[T]]int

	// budgetOut (2026-10-08 P6 budget): set when the per-call visited-pair
	// budget (MaxVisitedEntries) is exhausted. Further sub-traversals are
	// skipped for the rest of the call — a deliberate fail-open valve
	// (partial candidate set, request proceeds) mirroring how the old
	// engine behaved on pathological URLs it could not finish. NOT part of
	// the zero-semantics-change memoization; the root-cause fix is the
	// memoization above.
	budgetOut     bool
	budgetCounted bool
}

// appendLeaf adds n's leaf values to the accumulator, honoring t.dedup.
func (t *traverser[T]) appendLeaf() {
	if t.dedup == nil {
		t.data = append(t.data, t.n.leaf...)
		return
	}
	for _, v := range t.n.leaf {
		if _, ok := t.dedup[v]; !ok {
			t.dedup[v] = struct{}{}
			t.data = append(t.data, v)
		}
	}
}

func (n *node[T]) traverse(url string) []T {
	t := traverser[T]{
		n: n,
	}
	t.traversePrefix(n.prefixSlice(), url)

	return t.data
}
// visit (2026-10-06) continues a SHARED traversal accumulator at child
// node n: identical semantics to n.traverse(url) appended into t.data, but
// without allocating a fresh traverser per sub-traversal.
//
// 2026-10-08 P6 memo: visit itself no longer memoizes — the state key moved
// into traversePrefix (covering every recursion state). visit keeps only the
// wildcard-tail subsumption: a tail node walked at the smallest offset
// collects a superset of any larger-offset walk of the same node, so later
// entries skip without even registering a state (traverser.tailMin).
//
// 2026-10-08 P6 budget: once budgetOut is set (MaxVisitedEntries exhausted)
// every new sub-traversal is skipped for the rest of the call (fail-open).
func (t *traverser[T]) visit(n *node[T], url string) {
	if t.visited != nil {
		if t.budgetOut {
			return
		}
		off := len(t.base) - len(url)
		if pl := n.prefixSlice(); len(pl) == 1 && pl[0] == tokenWildcard {
			if mn, ok := t.tailMin[n]; ok && mn <= off {
				return
			}
			t.tailMin[n] = off
		}
	}
	old := t.n
	t.n = n
	t.traversePrefix(n.prefixSlice(), url)
	t.n = old
}


func (t *traverser[T]) traversePrefix(prefix []token, url string) {
	// 2026-10-08 P6 memo: every recursion state (node, remaining prefix
	// length, url offset) is traversed at most once per call. The walk of a
	// state is a pure function of the state, and a repeat's leaf insertions
	// all hit dedup (its values were inserted, in the same relative order,
	// by the state's first walk), so skipping repeats is accumulator- and
	// order-neutral. This covers the within-node enumerations that never
	// pass through visit and turns the backtracking matcher's
	// exponential re-walks into distinct-state work.
	if t.visited != nil {
		if t.budgetOut {
			return
		}
		k := vKey[T]{n: t.n, plen: len(prefix), off: len(t.base) - len(url)}
		if _, seen := t.visited[k]; seen {
			return
		}
		if len(t.visited) >= MaxVisitedEntries {
			t.budgetOut = true
			if !t.budgetCounted {
				t.budgetCounted = true
				budgetExceededCalls.Add(1)
			}
			return
		}
		t.visited[k] = struct{}{}
	}
	// 2026-10-09 落地建议（eff_probe 沙箱原型验证）：连续字面量字节 token 改为循环内联消费。
	// 与原 default 分支逐步递归（prefix[0]==token(url[0]) → 递归
	// (prefix[1:], url[1:])）逐步等价：每轮做同一次比较并同步推进；
	// 碰到特殊 token（>= tokenWildcard）或失配即停，剩余状态交给下方
	// 原有分支。差异仅在于中间递归状态不再登记进 visited memo——少跳过
	// 重复状态只会多做工作（重复走的 leaf 插入全部命中 dedup，累加器
	// 首次出现序不变），不会改变任何输出；预算阀门（budgetOut）语义不变。
	for len(prefix) > 0 && len(url) > 0 && prefix[0] < tokenWildcard {
		if prefix[0] != token(url[0]) {
			return
		}
		prefix = prefix[1:]
		url = url[1:]
	}
	if len(prefix) == 0 {
		if t.n.isLeaf() {
			t.appendLeaf()
		}
		if url == "" {
			if anchor := t.n.getEdge(tokenAnchor); anchor != nil {
				t.visit(anchor, "")
			}
			if wildcard := t.n.getEdge(tokenWildcard); wildcard != nil {
				t.visit(wildcard, "")
			}
			if separator := t.n.getEdge(tokenSeparator); separator != nil {
				t.visit(separator, "")
			}
		} else {
			firstCh := url[0]
			if separator := t.n.getEdge(tokenSeparator); isSeparator(firstCh) && separator != nil {
				t.visit(separator, url)
			}
			if wildcard := t.n.getEdge(tokenWildcard); wildcard != nil {
				t.visit(wildcard, url)
			}
			if ch := t.n.getEdge(token(firstCh)); ch != nil {
				t.visit(ch, url)
			}
		}
		return
	}
	if len(url) == 0 {
		if t.n.isLeaf() && len(prefix) == 1 && (prefix[0] == tokenAnchor || prefix[0] == tokenSeparator || prefix[0] == tokenWildcard) {
			t.appendLeaf()
		}
		return
	}

	switch prefix[0] {
	case tokenWildcard:
		if len(prefix) == 1 {
			t.traverseWildcardTail(url)
		} else {
			switch prefix[1] {
			case tokenAnchor:
				t.traversePrefix(prefix[1:], "")
			case tokenSeparator:
				for i := 0; i < len(url); i++ {
					if isSeparator(url[i]) {
						t.traversePrefix(prefix[1:], url[i:])
					}
				}
			default:
				target := byte(prefix[1]) // #nosec G115 -- literal character tokens are always in ASCII byte range
				off := 0
				for off < len(url) {
					idx := strings.IndexByte(url[off:], target)
					if idx < 0 {
						break
					}
					t.traversePrefix(prefix[1:], url[off+idx:])
					off += idx + 1
				}
			}
		}
	case tokenSeparator:
		if !isSeparator(url[0]) {
			return
		}
		// Scan forward past all separator chars and make
		// a single recursive call for the boundary, plus one
		// for the remaining prefix at the first non-separator position.
		i := 1
		for i < len(url) && isSeparator(url[i]) {
			i++
		}
		t.traversePrefix(prefix[1:], url[1:])
		if i > 1 {
			t.traversePrefix(prefix[1:], url[i:])
		}
	default:
		if prefix[0] == token(url[0]) {
			t.traversePrefix(prefix[1:], url[1:])
		}
	}
}

// traverseWildcardTail handles a wildcard at the end of a node's prefix.
// Instead of calling traversePrefix(nil, url[i:]) for every i, it
// collects the set of characters that can actually start a child edge
// and only dispatches on positions where those characters appear.
func (t *traverser[T]) traverseWildcardTail(url string) {
	n := t.n

	// Wildcard matches the entire remaining URL.
	t.traversePrefix(nil, "")

	// 2026-10-08 P6 memo: a tail node without child edges matches its leaf
	// for ANY suffix — the position scan below has nothing to dispatch and
	// would be pure O(len(url)) waste per (node, offset) entry.
	if len(n.edges) == 0 {
		return
	}

	separator := n.getEdge(tokenSeparator)
	wildcard := n.getEdge(tokenWildcard)

	hasSep := separator != nil
	hasWild := wildcard != nil

	// Build a set of literal first-characters from the node's edges.
	var literalSet byteset.Set
	for _, e := range n.edges {
		if e.label < 256 {
			literalSet.Add(byte(e.label)) // #nosec G115 -- labels < 256 are literal characters
		}
	}

	for i := 0; i < len(url); i++ {
		ch := url[i]

		if hasSep && isSeparator(ch) {
			t.visit(separator, url[i:])
		}
		if hasWild {
			t.visit(wildcard, url[i:])
		}
		if literalSet.Has(ch) {
			if child := n.getEdge(token(ch)); child != nil {
				t.visit(child, url[i:])
			}
		}
	}
}

var separators byteset.Set

func init() {
	const sepChars = "~:/?#[]@!$&'()*+,;="
	for i := range sepChars {
		ch := sepChars[i]
		separators.Add(ch)
	}
}

func isSeparator(char byte) bool {
	return separators.Has(char)
}
