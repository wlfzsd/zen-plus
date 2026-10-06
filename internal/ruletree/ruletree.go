package ruletree

import (
	"strings"
	"sync"

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
}

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
// The URL is expected to be a valid URL with scheme and host.
func (t *Tree[T]) Get(url string) []T {
	data := make(map[T]struct{})

	addUnique := func(items []T) {
		for _, item := range items {
			if _, exists := data[item]; !exists {
				data[item] = struct{}{}
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

	result := make([]T, len(data))
	var i int
	for d := range data {
		result[i] = d
		i++
	}
	return result
}

// GetLP is the low-allocation variant of Get (2026-10-06): identical result
// SET (dedup makes order map-random in both), with
//   - one shared accumulator for the whole traversal (node.visit), and
//   - the accumulator and the dedup map served from per-tree sync.Pools.
//
// The returned slice is freshly allocated, exactly like Get. GetLP may run
// concurrently with itself and with Get; the same Insert/Compact exclusion
// as Get applies.
func (t *Tree[T]) GetLP(url string) []T {
	t.lpOnce.Do(func() {
		t.accPool.New = func() any { return new([]T) }
		t.mapPool.New = func() any { return make(map[T]struct{}, 64) }
	})

	accp := t.accPool.Get().(*[]T)
	acc := (*accp)[:0]

	tr := traverser[T]{data: acc}
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

	m := t.mapPool.Get().(map[T]struct{})
	clear(m)
	for _, item := range acc {
		m[item] = struct{}{}
	}
	result := make([]T, len(m))
	var i int
	for d := range m {
		result[i] = d
		i++
	}
	*accp = acc[:0]
	t.accPool.Put(accp)
	t.mapPool.Put(m)
	return result
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
