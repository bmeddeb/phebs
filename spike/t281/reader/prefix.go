package main

import "bytes"

// Compressed prefix edges share only admitted derived literal bytes. At most
// two nodes per admitted prefix are reserved at blob admission; no full catalog
// scan or source read occurs for each concrete key.
type prefixNode struct {
	edge         []byte
	children     map[byte]*prefixNode
	declarations []int
}

func newPrefixNode(edge []byte) *prefixNode {
	return &prefixNode{edge: edge, children: map[byte]*prefixNode{}}
}
func (n *prefixNode) insert(key []byte, index int) {
	if len(key) == 0 {
		n.declarations = append(n.declarations, index)
		return
	}
	child := n.children[key[0]]
	if child == nil {
		child = newPrefixNode(key)
		child.declarations = []int{index}
		n.children[key[0]] = child
		return
	}
	common := 0
	for common < len(key) && common < len(child.edge) && key[common] == child.edge[common] {
		common++
	}
	if common == len(child.edge) {
		child.insert(key[common:], index)
		return
	}
	split := newPrefixNode(child.edge[:common])
	n.children[key[0]] = split
	child.edge = child.edge[common:]
	split.children[child.edge[0]] = child
	if common == len(key) {
		split.declarations = append(split.declarations, index)
	} else {
		leaf := newPrefixNode(key[common:])
		leaf.declarations = []int{index}
		split.children[key[common]] = leaf
	}
}
func (n *prefixNode) lookup(key []byte) map[int]bool {
	result := map[int]bool{}
	for n != nil {
		for _, id := range n.declarations {
			result[id] = true
		}
		if len(key) == 0 {
			break
		}
		child := n.children[key[0]]
		if child == nil || !bytes.HasPrefix(key, child.edge) {
			break
		}
		key = key[len(child.edge):]
		n = child
	}
	return result
}
