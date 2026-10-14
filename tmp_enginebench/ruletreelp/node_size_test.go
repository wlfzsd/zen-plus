package ruletreelp

import (
	"fmt"
	"testing"
	"unsafe"
)

// TestNodeSizeClass documents the node size class (probe 4 re-check).
func TestNodeSizeClass(t *testing.T) {
	type payload struct{ p *int }
	fmt.Printf("node[*int]      = %d B\n", unsafe.Sizeof(node[payload]{}))
	fmt.Printf("node[*struct{}] = %d B\n", unsafe.Sizeof(node[struct{}]{})&0xffff)
	fmt.Printf("litEdge[*int]   = %d B\n", unsafe.Sizeof(litEdge[payload]{}))
}
