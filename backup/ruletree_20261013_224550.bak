package ruletree

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/irbis-sh/zen-desktop/internal/ruletree/byteset"
)

type Data comparable

// Tree is a prefix tree for storing and retrieving data
// associated with adblock-style patterns.
//
// Insert and Compact must not run concurrently with Get.
type Tree[T Data] struct {
	// insertMu protects the tree during inserts.
	insertMu sync.Mutex

	root *node[T]
	// domainBoundaryRoot stores patterns beginning with tokenDomainBoundary (||).
	domainBoundaryRoot *node[T]
	// anchorRoot stores pattern beginning with tokenAnchor (|).
	anchorRoot *node[T]

	// lpOnce lazily initializes the GetLP pools (2026-10-06): generic New
	// closures cannot be static package variables.
	lpOnce  sync.Once
	accPool sync.Pool
	mapPool sync.Pool
	// visPool (2026-10-08 P6 memo) backs the per-call (node, offset)
	// memoization maps used for combinatorial URLs; see maxMemoFreeURLLen.
	visPool sync.Pool
	// tailPool (2026-10-08 P6 memo) backs the per-call wildcard-tail
	// subsumption maps (tail node → smallest walked offset).
	tailPool sync.Pool
}

// MaxVisitedEntries (2026-10-08 P6 budget) caps the per-call (node, offset)
// memoization pairs. It is a tunable safety valve, not part of the
// memoization semantics: memoized traversal of realistic URLs stays orders
// of magnitude below it (a 1017-char alicdn combo URL uses ~10⁴ pairs), so
// hitting it means an unknown pathological pattern/URL combination. A call
// that exhausts the budget abandons further sub-traversals (fail-open:
// partial candidate set, request proceeds — the same net behavior the old
// engine had when a pathological URL hung past its deadline) and bumps the
// atomic counter exposed by TraversalBudgetExceeded.
var MaxVisitedEntries = 1_000_000

// budgetExceededCalls counts GetLP calls that exhausted MaxVisitedEntries.
var budgetExceededCalls atomic.Uint64

// TraversalBudgetExceeded reports how many GetLP calls exhausted the
// per-call traversal budget (MaxVisitedEntries) since process start.
func TraversalBudgetExceeded() uint64 { return budgetExceededCalls.Load() }

// maxMemoFreeURLLen (2026-10-08 P6 memo): URLs at or below this length are
// traversed WITHOUT the (node, offset) memoization map — the common short
// request pays zero extra cost per visit. Longer URLs enable memoization
// from the first visit. Enabling or disabling memoization never changes
// results: a repeat (node, offset) entry collects only values already in
// the dedup map (inserted, in the same relative order, by the pair's first
// entry), so any subset of repeat-skips preserves the output sequence
// bit-for-bit — the gate is a pure performance valve, not a semantic switch.
const maxMemoFreeURLLen = 256

func New[T Data]() *Tree[T] {
	return &Tree[T]{
		root:               &node[T]{},
		domainBoundaryRoot: &node[T]{},
		anchorRoot:         &node[T]{},
	}
}

// Insert adds a pattern with associated data to the tree.
func (t *Tree[T]) Insert(pattern string, v T) {
	if pattern == "" {
		return
	}

	var parent *node[T]
	var n *node[T]

	tokens := tokenize(pattern)

	for i := 1; i < len(tokens); i++ {
		if tokens[i] == tokenDomainBoundary {
			return
		}
	}

	t.insertMu.Lock()
	defer t.insertMu.Unlock()

	switch tokens[0] {
	case tokenDomainBoundary:
		n, tokens = t.domainBoundaryRoot, tokens[1:]
	case tokenAnchor:
		n, tokens = t.anchorRoot, tokens[1:]
	default:
		n = t.root
	}

	for {
		if len(tokens) == 0 {
			if n.isLeaf() {
				n.leaf = append(n.leaf, v)
			} else {
				n.leaf = []T{v}
			}
			return
		}

		parent = n
		n = n.getEdge(tokens[0])

		if n == nil {
			n := &node[T]{
				prefixBase: prefixBaseOf(tokens),
				prefixLen:  len(tokens),
				leaf:       []T{v},
			}
			parent.addEdge(tokens[0], n)
			return
		}

		commonPrefix := longestPrefix(tokens, n.prefixSlice())
		if commonPrefix == n.prefixLen {
			tokens = tokens[commonPrefix:]
			continue
		}

		child := &node[T]{
			prefixBase: prefixBaseOf(tokens[:commonPrefix]),
			prefixLen:  commonPrefix,
		}
		parent.updateEdge(tokens[0], child)

		child.addEdge(n.prefixSlice()[commonPrefix], n)
		n.advancePrefix(commonPrefix)

		l := []T{v}
		if commonPrefix == len(tokens) {
			child.leaf = l
		} else {
			n := &node[T]{
				leaf:       l,
				prefixBase: prefixBaseOf(tokens[commonPrefix:]),
				prefixLen:  len(tokens) - commonPrefix,
			}
			child.addEdge(tokens[commonPrefix], n)
		}
		return
	}
}

// advancePrefix drops the first n tokens from the node's prefix.
func (n *node[T]) advancePrefix(k int) {
	n.prefixBase = prefixBaseOf(n.prefixSlice()[k:])
	n.prefixLen -= k
}

// Get retrieves data matching the given URL.
//
// Results are deduplicated in deterministic first-occurrence order
// (2026-10-08 P6): the traversal itself is ordered (sorted edges,
// insertion-ordered leaves), so the only nondeterminism was the former
// map-based dedup, whose map iteration order made the result sequence —
// and with it the identity of the winning rule — vary between runs of the
// same binary. The set of returned values is unchanged.
//
// The URL is expected to be a valid URL with scheme and host.
func (t *Tree[T]) Get(url string) []T {
	// 2026-10-08 P6 memo: Get delegates to GetLP. Both produce the same
	// deterministic first-occurrence sequence (same traversal order, same
	// dedup semantics — asserted by TestP6RuleTreeDeterminism's Get≡GetLP
	// check), and delegation gives Get the (node, offset) memoization that
	// keeps combinatorial URLs bounded.
	return t.GetLP(url)
}

// getLegacy is the pre-delegation body of Get (map-per-sub-traversal merge),
// retained unused as the reference implementation for audit comparison;
// Get now delegates to GetLP (identical first-occurrence sequence).
func (t *Tree[T]) getLegacy(url string) []T {
	seen := make(map[T]struct{})
	result := make([]T, 0)

	addUnique := func(items []T) {
		for _, item := range items {
			if _, exists := seen[item]; !exists {
				seen[item] = struct{}{}
				result = append(result, item)
			}
		}
	}

	addUnique(t.anchorRoot.traverse(url))
	addUnique(t.root.traverse(url))

	var (
		traverseNext = false

		schemeEnd = strings.Index(url, "://")
		hostStart = schemeEnd + 3
		hostEnd   = strings.IndexAny(url[hostStart:], "/?")
	)
	for i := 1; i < len(url); i++ {
		c := url[i]

		if traverseNext {
			addUnique(t.root.traverse(url[i:]))
			traverseNext = isTraversalMarker(c)
		} else if isTraversalMarker(c) {
			addUnique(t.root.traverse(url[i:]))
			traverseNext = true
		}

		if i == hostStart {
			addUnique(t.domainBoundaryRoot.traverse(url[i:]))
		}
		if i > hostStart && (hostEnd == -1 || i < hostStart+hostEnd) {
			if c == '.' {
				addUnique(t.domainBoundaryRoot.traverse(url[i+1:]))
			}
		}
	}

	return result
}

// maxPooledAccEntries drops oversized pooled containers so a pathological
// URL cannot make the pools hoard huge backings (defense in depth on top of
// the in-traversal dedup; mirrors rulestore.maxPooledResEntries).
const maxPooledAccEntries = 8192

// GetLP is the low-allocation variant of Get (2026-10-06). Since P6
// (2026-10-08) results are deduplicated in deterministic first-occurrence
// order: identical value set, a stable identity sequence across runs of the
// same binary, and identical sequences in Get and GetLP. It keeps
//   - one shared accumulator for the whole traversal (node.visit), with the
//     dedup applied DURING accumulation (traverser.dedup): a wildcard tail
//     re-visits the same leaf at every URL position, which previously
//     appended one copy per position and let a 68 KB URL grow the
//     accumulator to ~180 MB per store (leak_capture heap_2.txt /
//     goroutine_2.txt; CPU 3.9 cores + 2.1 GB resident), and
//   - the accumulator and the dedup map served from per-tree sync.Pools, and
//   - (2026-10-08 P6 memo) for URLs longer than maxMemoFreeURLLen, a pooled
//     (node, offset) visited map so each sub-traversal runs at most once —
//     combinatorial URLs (dense traversal markers × wildcard subtrees, e.g.
//     a 1017-char alicdn "??,..." combo URL) previously re-walked the same
//     subtrees from hundreds of starting offsets and exceeded 15 s; the
//     memoization skips only accumulator-neutral repeats (semantic no-op).
//
// The returned slice is freshly allocated, exactly like Get. GetLP may run
// concurrently with itself and with Get; the same Insert/Compact exclusion
// as Get applies.
func (t *Tree[T]) GetLP(url string) []T {
	t.lpOnce.Do(func() {
		t.accPool.New = func() any { return new([]T) }
		t.mapPool.New = func() any { return make(map[T]struct{}, 64) }
		t.visPool.New = func() any { return make(map[vKey[T]]struct{}) }
		t.tailPool.New = func() any { return make(map[*node[T]]int) }
	})

	accp := t.accPool.Get().(*[]T)
	acc := (*accp)[:0]

	m := t.mapPool.Get().(map[T]struct{})
	clear(m)

	// (node, offset) memoization (2026-10-08 P6): enabled only above
	// maxMemoFreeURLLen so the common short request pays nothing. Pure
	// performance valve — repeat entries are accumulator-neutral, see the
	// traverser.visited and maxMemoFreeURLLen comments.
	memoOn := len(url) > maxMemoFreeURLLen
	var vis map[vKey[T]]struct{}
	var tailMin map[*node[T]]int
	if memoOn {
		vis = t.visPool.Get().(map[vKey[T]]struct{})
		clear(vis)
		tailMin = t.tailPool.Get().(map[*node[T]]int)
		clear(tailMin)
	}

	tr := traverser[T]{data: acc, dedup: m, base: url, visited: vis, tailMin: tailMin}
	tr.visit(t.anchorRoot, url)
	tr.visit(t.root, url)

	var (
		traverseNext = false

		schemeEnd = strings.Index(url, "://")
		hostStart = schemeEnd + 3
		hostEnd   = strings.IndexAny(url[hostStart:], "/?")
	)
	for i := 1; i < len(url); i++ {
		c := url[i]

		if traverseNext {
			tr.visit(t.root, url[i:])
			traverseNext = isTraversalMarker(c)
		} else if isTraversalMarker(c) {
			tr.visit(t.root, url[i:])
			traverseNext = true
		}

		if i == hostStart {
			tr.visit(t.domainBoundaryRoot, url[i:])
		}
		if i > hostStart && (hostEnd == -1 || i < hostStart+hostEnd) {
			if c == '.' {
				tr.visit(t.domainBoundaryRoot, url[i+1:])
			}
		}
	}

	acc = tr.data

	result := make([]T, len(acc))
	copy(result, acc)

	// Defense in depth (P6): never pool oversized containers. The
	// in-traversal dedup already bounds acc by the number of distinct
	// matched values; this keeps a pathological tree from pinning memory.
	if cap(acc) <= maxPooledAccEntries {
		*accp = acc[:0]
		t.accPool.Put(accp)
	}
	if len(m) <= maxPooledAccEntries {
		t.mapPool.Put(m)
	}
	if memoOn && len(vis) <= maxPooledAccEntries {
		t.visPool.Put(vis)
	}
	if memoOn && len(tailMin) <= maxPooledAccEntries {
		t.tailPool.Put(tailMin)
	}
	return result
}

// Walk calls fn for every value stored in the tree — under all three roots,
// in no particular order (2026-10-08 B6). The same Insert/Compact exclusion
// applies: Walk must not run concurrently with Insert.
func (t *Tree[T]) Walk(fn func(T)) {
	t.insertMu.Lock()
	defer t.insertMu.Unlock()

	var rec func(*node[T])
	rec = func(n *node[T]) {
		for _, v := range n.leaf {
			fn(v)
		}
		for _, e := range n.edges {
			rec(e.node)
		}
	}
	rec(t.root)
	rec(t.domainBoundaryRoot)
	rec(t.anchorRoot)
}

func longestPrefix(a, b []token) int {
	maxLen := len(a)
	if l := len(b); l < maxLen {
		maxLen = l
	}
	for i := range maxLen {
		if a[i] != b[i] {
			return i
		}
	}
	return maxLen
}

// traversalMarkers indicate traversal starting points in a URL.
var traversalMarkers byteset.Set

func init() {
	const markerChars = "-._~:/?#[]@!$&'()*+,;%="
	for i := range markerChars {
		ch := markerChars[i]
		traversalMarkers.Add(ch)
	}
}

func isTraversalMarker(char byte) bool {
	return traversalMarkers.Has(char)
}
