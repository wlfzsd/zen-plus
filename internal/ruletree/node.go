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

func (n *node[T]) getEdge(label token) *node[T] {
	idx := sort.Search(len(n.edges), func(i int) bool {
		return n.edges[i].label >= label
	})
	if idx < len(n.edges) && n.edges[idx].label == label {
		return n.edges[idx].node
	}
	return nil
}

// traverser holds the state for a single traverse() call.
type traverser[T Data] struct {
	data []T
	n    *node[T]
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
func (t *traverser[T]) visit(n *node[T], url string) {
	old := t.n
	t.n = n
	t.traversePrefix(n.prefixSlice(), url)
	t.n = old
}


func (t *traverser[T]) traversePrefix(prefix []token, url string) {
	if len(prefix) == 0 {
		if t.n.isLeaf() {
			t.data = append(t.data, t.n.leaf...)
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
			t.data = append(t.data, t.n.leaf...)
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
