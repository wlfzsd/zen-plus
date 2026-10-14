package ruletree

import (
	"strings"
	"sync"

	"github.com/irbis-sh/zen-desktop/tmp_enginebench/ruletreeopt/byteset"
)

type Data comparable

// rootHint is a precomputed summary of a tree root's children, used to skip
// traverse() calls that cannot possibly collect anything. Rebuilt on Insert/Compact.
type rootHint struct {
	lits      byteset.Set
	hasLeaf   bool
	hasWild   bool
	hasSep    bool
	hasAnchor bool
}

func rebuildRootHint[T Data](h *rootHint, n *node[T]) {
	h.lits = byteset.Set{}
	h.hasLeaf = n.isLeaf()
	h.hasWild = n.wildcard != nil
	h.hasSep = n.separator != nil
	h.hasAnchor = n.anchor != nil
	for _, e := range n.edges {
		h.lits.Add(e.label)
	}
}

// canStart reports whether a traverse rooted at this node can collect anything,
// given the first byte of the suffix. An empty suffix can only match leaf/special children.
func (h *rootHint) canStart(first byte, suffixEmpty bool) bool {
	if suffixEmpty {
		return h.hasLeaf || h.hasWild || h.hasSep || h.hasAnchor
	}
	return h.hasWild || h.hasAnchor || h.hasLeaf || (h.hasSep && isSeparator(first)) || h.lits.Has(first)
}

// canStart2 is canStart for a suffix beginning at s[0] (handles empty s).
func (h *rootHint) canStart2(s string) bool {
	if len(s) == 0 {
		return h.hasLeaf || h.hasWild || h.hasSep || h.hasAnchor
	}
	return h.canStart(s[0], false)
}

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

	rootHint   rootHint
	dbRootHint rootHint
	anchorHint rootHint
}

func New[T Data]() *Tree[T] {
	t := &Tree[T]{
		root:               &node[T]{},
		domainBoundaryRoot: &node[T]{},
		anchorRoot:         &node[T]{},
	}
	t.rebuildHints()
	return t
}

func (t *Tree[T]) rebuildHints() {
	rebuildRootHint(&t.rootHint, t.root)
	rebuildRootHint(&t.dbRootHint, t.domainBoundaryRoot)
	rebuildRootHint(&t.anchorHint, t.anchorRoot)
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
			t.rebuildHints()
			return
		}

		parent = n
		n = n.getEdge(tokens[0])

		if n == nil {
			n := &node[T]{
				prefix: tokens,
				leaf:   []T{v},
			}
			parent.addEdge(tokens[0], n)
			t.rebuildHints()
			return
		}

		commonPrefix := longestPrefix(tokens, n.prefix)
		if commonPrefix == len(n.prefix) {
			tokens = tokens[commonPrefix:]
			continue
		}

		child := &node[T]{
			prefix: tokens[:commonPrefix],
		}
		parent.updateEdge(tokens[0], child)

		child.addEdge(n.prefix[commonPrefix], n)
		n.prefix = n.prefix[commonPrefix:]

		l := []T{v}
		if commonPrefix == len(tokens) {
			child.leaf = l
		} else {
			n := &node[T]{
				leaf:   l,
				prefix: tokens[commonPrefix:],
			}
			child.addEdge(tokens[commonPrefix], n)
		}
		t.rebuildHints()
		return
	}
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
	if t.rootHint.canStart2(url) {
		addUnique(t.root.traverse(url))
	}

	var (
		traverseNext = false

		schemeEnd = strings.Index(url, "://")
		hostStart = schemeEnd + 3
		hostEnd   = strings.IndexAny(url[hostStart:], "/?")
	)
	for i := 1; i < len(url); i++ {
		c := url[i]

		if traverseNext || isTraversalMarker(c) {
			if t.rootHint.canStart(c, false) {
				addUnique(t.root.traverse(url[i:]))
			}
			traverseNext = isTraversalMarker(c)
		}

		if i == hostStart {
			if t.dbRootHint.canStart(c, false) {
				addUnique(t.domainBoundaryRoot.traverse(url[i:]))
			}
		}
		if i > hostStart && (hostEnd == -1 || i < hostStart+hostEnd) {
			if c == '.' {
				if i+1 < len(url) {
					if t.dbRootHint.canStart(url[i+1], false) {
						addUnique(t.domainBoundaryRoot.traverse(url[i+1:]))
					}
				} else if t.dbRootHint.canStart(0, true) {
					addUnique(t.domainBoundaryRoot.traverse(url[i+1:]))
				}
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
