package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func indexOf(order []string, name string) int {
	for i, n := range order {
		if n == name {
			return i
		}
	}
	return -1
}

func TestTopoSortOrder(t *testing.T) {
	g := New()
	for _, n := range []string{"app", "core", "utils", "cli"} {
		g.AddNode(n)
	}
	// app depends on core and utils; cli depends on core.
	require.NoError(t, g.AddEdge("core", "app"))
	require.NoError(t, g.AddEdge("utils", "app"))
	require.NoError(t, g.AddEdge("core", "cli"))

	order, err := g.TopoSort()
	require.NoError(t, err)
	require.Len(t, order, 4)
	assert.Less(t, indexOf(order, "core"), indexOf(order, "app"), "core before app")
	assert.Less(t, indexOf(order, "utils"), indexOf(order, "app"), "utils before app")
	assert.Less(t, indexOf(order, "core"), indexOf(order, "cli"), "core before cli")
}

func TestTopoSortDeterministic(t *testing.T) {
	g := New()
	for _, n := range []string{"c", "a", "b"} {
		g.AddNode(n)
	}
	order, err := g.TopoSort()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, order, "independent nodes sort alphabetically")
}

func TestTopoSortCycle(t *testing.T) {
	g := New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddNode("c")
	require.NoError(t, g.AddEdge("a", "b"))
	require.NoError(t, g.AddEdge("b", "c"))
	require.NoError(t, g.AddEdge("c", "a"))

	_, err := g.TopoSort()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestAddEdgeUnknownNode(t *testing.T) {
	g := New()
	g.AddNode("a")
	assert.Error(t, g.AddEdge("a", "ghost"))
}

func TestAddNodeIdempotent(t *testing.T) {
	g := New()
	g.AddNode("a")
	g.AddNode("a")
	order, err := g.TopoSort()
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, order)
}
