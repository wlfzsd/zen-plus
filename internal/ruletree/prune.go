package ruletree

// prune.go — 2026-10-09 覆盖去重支撑：按值剪枝。
//
// PruneValues 从树中删除 drop 判定为真的叶值，并自底向上剪掉因此变空的
// 节点（无叶值且无边）。它服务于 networkrules 的装载后覆盖去重（2026-10-09，
// Compact 阶段在 $badfilter 应用之后运行）：被更宽同签名规则覆盖的窄规则
// 在那里被安全判定并从这里物理删除，归还规则对象与独占节点链的内存。
//
// 语义约束：
//   - 幸存值的叶内相对顺序保持不变（原地过滤），Get/GetLP 的
//     确定性首现序列对幸存值保持不变；
//   - 与 Insert/Compact 相同的并发契约：不得与 Insert/Get 并发调用
//     （调用方在 Compact 阶段持有与 applyBadfilters 相同的装载/查询互斥窗口）；
//   - 空 leaf 切片置回 nil，保持 isLeaf() 的"有值"语义；
//   - 三棵根节点永存（父为 nil 不参与摘除），空根与新建树行为一致。

// pruneFrame 是迭代后序遍历的栈帧。parent 指向父节点（根为 nil）；
// edgeIdx 是下一条待进入的边；dead 在本节点出栈时记录其生死，
// 由父帧在子帧出栈的同一时刻摘除对应边（见 PruneValues 尾部说明）。
type pruneFrame[T Data] struct {
	node    *node[T]
	parent  *node[T] // nil = 三根之一，永存
	edgeIdx int
	dead    bool
}

// PruneValues 对三棵根下所有叶值调用 drop，返回删除的值个数。
// drop 返回 true 表示删除该值。随后自底向上剪除既无叶值也无边的节点。
// O(节点数 + 叶值数)，无递归（病态超长 pattern 的树深不受栈深限制）。
func (t *Tree[T]) PruneValues(drop func(T) bool) int {
	t.insertMu.Lock()
	defer t.insertMu.Unlock()

	removed := 0
	stack := make([]pruneFrame[T], 0, 64)

	for _, root := range []*node[T]{t.anchorRoot, t.domainBoundaryRoot, t.root} {
		stack = append(stack[:0], pruneFrame[T]{node: root})
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.edgeIdx < len(f.node.edges) {
				child := f.node.edges[f.edgeIdx].node
				f.edgeIdx++
				stack = append(stack, pruneFrame[T]{node: child, parent: f.node})
				continue
			}
			// 所有子树已出栈：过滤本节点叶值。
			cur := f.node
			if len(cur.leaf) > 0 {
				kept := cur.leaf[:0]
				for _, v := range cur.leaf {
					if drop(v) {
						removed++
						continue
					}
					kept = append(kept, v)
				}
				if len(kept) == 0 {
					cur.leaf = nil
				} else {
					cur.leaf = kept
				}
			}
			f.dead = cur.leaf == nil && len(cur.edges) == 0
			stack = stack[:len(stack)-1]
			// 死节点摘除：本帧是刚弹出的子帧，父帧必在其正下方（LIFO），
			// 且本帧的边恰好是父帧 edgeIdx-1 那条（父逐边推进、立即回手）。
			// 此时摘除不影响父尚未进入的边区间，edgeIdx 同步回退一位。
			if f.dead && f.parent != nil && len(stack) > 0 {
				p := &stack[len(stack)-1]
				if p.edgeIdx > 0 && p.node.edges[p.edgeIdx-1].node == cur {
					removeEdgeAt(p.node, p.edgeIdx-1)
					p.edgeIdx--
				}
			}
		}
	}
	return removed
}

// removeEdgeAt 从排序 edges 切片中删除下标 i 的边（保持有序）。
func removeEdgeAt[T Data](n *node[T], i int) {
	copy(n.edges[i:], n.edges[i+1:])
	n.edges = n.edges[:len(n.edges)-1]
}
