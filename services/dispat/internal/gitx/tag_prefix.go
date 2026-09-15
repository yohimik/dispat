// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

// tagPrefixNode dispatches tag names to candidate matchers by their literal
// prefix. Each node has at most the fixed byte alphabet of children.
type tagPrefixNode[T any] struct {
	children []tagPrefixEdge[T]
	matchers []T
}

type tagPrefixEdge[T any] struct {
	byteValue byte
	child     *tagPrefixNode[T]
}

func (n *tagPrefixNode[T]) add(prefix string, matcher T) {
	for i := 0; i < len(prefix); i++ {
		var child *tagPrefixNode[T]
		for edge := range n.children {
			if n.children[edge].byteValue == prefix[i] {
				child = n.children[edge].child
				break
			}
		}
		if child == nil {
			child = &tagPrefixNode[T]{}
			n.children = append(n.children, tagPrefixEdge[T]{byteValue: prefix[i], child: child})
		}
		n = child
	}
	n.matchers = append(n.matchers, matcher)
}

func (n *tagPrefixNode[T]) child(value byte) *tagPrefixNode[T] {
	for _, edge := range n.children {
		if edge.byteValue == value {
			return edge.child
		}
	}
	return nil
}
