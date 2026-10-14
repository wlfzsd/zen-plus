package ruletree

import "github.com/irbis-sh/zen-desktop/internal/trimslice"

// Compact shrinks internal slice capacities to reduce memory usage.
func (t *Tree[T]) Compact() {
	t.insertMu.Lock()
	defer t.insertMu.Unlock()

	stack := []*node[T]{
		t.anchorRoot,
		t.domainBoundaryRoot,
		t.root,
	}

	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if n.isLeaf() {
			n.leaf = trimslice.TrimSlice(n.leaf)
		}

		n.edges = trimslice.TrimSlice(n.edges)
		if n.prefixLen > 0 {
			p := trimslice.TrimSlice(n.prefixSlice())
			n.prefixBase = prefixBaseOf(p)
			n.prefixLen = len(p)
		}

		for _, e := range n.edges {
			stack = append(stack, e.node)
		}
	}
}
