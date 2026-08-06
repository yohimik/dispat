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

func TestSchedulerReadyAndDoneCascade(t *testing.T) {
	// A diamond: a -> b, a -> c, b -> d, c -> d. Only a starts ready; d
	// becomes ready when its LAST dependency completes, not its first.
	s := NewScheduler[string]()
	s.AddEdge("a", "b")
	s.AddEdge("a", "c")
	s.AddEdge("b", "d")
	s.AddEdge("c", "d")

	assert.Equal(t, 4, s.Len())
	assert.Equal(t, []string{"a"}, s.Ready())
	assert.Empty(t, s.Ready(), "the frontier is handed out once")

	assert.Equal(t, []string{"b", "c"}, s.Done("a"), "edge insertion order")
	assert.Empty(t, s.Done("b"), "d still waits for c")
	assert.Equal(t, []string{"d"}, s.Done("c"))
	assert.Empty(t, s.Blocked(), "everything was reachable")
}

func TestSchedulerIsolatedAndImplicitNodes(t *testing.T) {
	s := NewScheduler[string]()
	s.Add("lone")
	s.Add("lone")       // idempotent
	s.AddEdge("x", "y") // registers both implicitly

	assert.Equal(t, 3, s.Len())
	assert.Equal(t, []string{"lone", "x"}, s.Ready(), "node insertion order")
}

func TestSchedulerBlockedNamesCycleMembers(t *testing.T) {
	s := NewScheduler[string]()
	s.AddEdge("root", "a")
	s.AddEdge("a", "b")
	s.AddEdge("b", "a") // cycle a <-> b

	for _, n := range s.Ready() {
		s.Done(n)
	}
	assert.Equal(t, []string{"a", "b"}, s.Blocked(),
		"after draining the reachable frontier, what remains is the cycle")
}

func TestSchedulerGenericNodeType(t *testing.T) {
	// The executor's use: struct-typed task nodes.
	type node struct {
		pkg, stage string
	}
	s := NewScheduler[node]()
	build, publish := node{"core", "build"}, node{"core", "publish"}
	s.AddEdge(build, publish)

	assert.Equal(t, []node{build}, s.Ready())
	assert.Equal(t, []node{publish}, s.Done(build))
}
