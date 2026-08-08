// Package graph provides directed-graph machinery shared by planning and
// execution: a generic dependency Scheduler (the incremental core of Kahn's
// algorithm) and a small named Graph with deterministic topological sorting in
// O((V+E) log V) and cycle detection built on top of it.
package graph

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"
)

// Graph is a directed graph of named nodes. An edge provider -> consumer means
// the consumer depends on the provider.
//
// It differs from using a Scheduler directly in the two guarantees a *plan*
// needs and a task runner does not: edges naming unknown nodes are errors
// rather than implicit registrations (a dependency on a package that does not
// exist is a configuration mistake), and TopoSort is pure — it builds a fresh
// Scheduler per call, so sorting never consumes the graph.
type Graph struct {
	known map[string]bool
	nodes []string
	edges [][2]string
}

// New returns an empty graph.
func New() *Graph { return &Graph{known: make(map[string]bool)} }

// AddNode registers a node; adding the same name twice is a no-op.
func (g *Graph) AddNode(name string) {
	if g.known[name] {
		return
	}
	g.known[name] = true
	g.nodes = append(g.nodes, name)
}

// AddEdge records that consumer depends on provider. Both nodes must exist.
func (g *Graph) AddEdge(provider, consumer string) error {
	if !g.known[provider] {
		return fmt.Errorf("graph: unknown node %q", provider)
	}
	if !g.known[consumer] {
		return fmt.Errorf("graph: unknown node %q", consumer)
	}
	g.edges = append(g.edges, [2]string{provider, consumer})
	return nil
}

// TopoSort returns all nodes in dependency order (providers before consumers)
// using Kahn's algorithm — a Scheduler drained through a min-heap, so ties
// break alphabetically and the result is deterministic. If the graph contains
// a cycle an error naming the involved nodes is returned.
func (g *Graph) TopoSort() ([]string, error) {
	s := NewScheduler[string]()
	for _, n := range g.nodes {
		s.Add(n)
	}
	for _, e := range g.edges {
		s.AddEdge(e[0], e[1])
	}

	h := &stringHeap{}
	for _, n := range s.Ready() {
		heap.Push(h, n)
	}
	order := make([]string, 0, s.Len())
	for h.Len() > 0 {
		n := heap.Pop(h).(string)
		order = append(order, n)
		for _, next := range s.Done(n) {
			heap.Push(h, next)
		}
	}
	if len(order) != s.Len() {
		cyc := s.Blocked()
		sort.Strings(cyc)
		return nil, &CycleError{Nodes: cyc}
	}
	return order, nil
}

// CycleError reports that the graph has no topological order; Nodes are the
// blocked nodes. Typed so a caller can enrich its diagnostic with what it
// knows about the edges among them (the planner names each edge's manifest
// field, as §13.1 requires).
type CycleError struct{ Nodes []string }

func (e *CycleError) Error() string {
	return "graph: dependency cycle involving: " + strings.Join(e.Nodes, ", ")
}

// stringHeap is a min-heap of node names.
type stringHeap []string

func (h stringHeap) Len() int           { return len(h) }
func (h stringHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h stringHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *stringHeap) Push(x any)        { *h = append(*h, x.(string)) }
func (h *stringHeap) Pop() any {
	n := len(*h) - 1
	v := (*h)[n]
	*h = (*h)[:n]
	return v
}
