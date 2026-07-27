// Package graph provides a small directed graph with Kahn topological sorting
// in O((V+E) log V) and cycle detection.
package graph

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"
)

// Graph is a directed graph of named nodes. An edge provider -> consumer means
// the consumer depends on the provider.
type Graph struct {
	idx   map[string]int
	names []string
	out   [][]int
}

// New returns an empty graph.
func New() *Graph { return &Graph{idx: make(map[string]int)} }

// AddNode registers a node; adding the same name twice is a no-op.
func (g *Graph) AddNode(name string) {
	if _, ok := g.idx[name]; ok {
		return
	}
	g.idx[name] = len(g.names)
	g.names = append(g.names, name)
	g.out = append(g.out, nil)
}

// AddEdge records that consumer depends on provider. Both nodes must exist.
func (g *Graph) AddEdge(provider, consumer string) error {
	p, ok := g.idx[provider]
	if !ok {
		return fmt.Errorf("graph: unknown node %q", provider)
	}
	c, ok := g.idx[consumer]
	if !ok {
		return fmt.Errorf("graph: unknown node %q", consumer)
	}
	g.out[p] = append(g.out[p], c)
	return nil
}

// TopoSort returns all nodes in dependency order (providers before consumers)
// using Kahn's algorithm. Ties are broken alphabetically so the result is
// deterministic. If the graph contains a cycle an error naming the involved
// nodes is returned.
func (g *Graph) TopoSort() ([]string, error) {
	n := len(g.names)
	indeg := make([]int, n)
	for _, outs := range g.out {
		for _, c := range outs {
			indeg[c]++
		}
	}

	h := &nameHeap{names: g.names}
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			h.items = append(h.items, i)
		}
	}
	heap.Init(h)

	order := make([]string, 0, n)
	for h.Len() > 0 {
		v := heap.Pop(h).(int)
		order = append(order, g.names[v])
		for _, c := range g.out[v] {
			indeg[c]--
			if indeg[c] == 0 {
				heap.Push(h, c)
			}
		}
	}
	if len(order) != n {
		var cyc []string
		for i, d := range indeg {
			if d > 0 {
				cyc = append(cyc, g.names[i])
			}
		}
		sort.Strings(cyc)
		return nil, fmt.Errorf("graph: dependency cycle involving: %s", strings.Join(cyc, ", "))
	}
	return order, nil
}

// nameHeap is a min-heap of node indices ordered by node name.
type nameHeap struct {
	items []int
	names []string
}

func (h *nameHeap) Len() int           { return len(h.items) }
func (h *nameHeap) Less(i, j int) bool { return h.names[h.items[i]] < h.names[h.items[j]] }
func (h *nameHeap) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *nameHeap) Push(x interface{}) { h.items = append(h.items, x.(int)) }
func (h *nameHeap) Pop() interface{} {
	n := len(h.items) - 1
	v := h.items[n]
	h.items = h.items[:n]
	return v
}
