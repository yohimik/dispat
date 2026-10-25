package graph

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchedulerPassThroughIsNeverHandedOut: a pass-through node orders its
// dependents behind its dependencies and is itself never handed out, counted
// or reported blocked.
func TestSchedulerPassThroughIsNeverHandedOut(t *testing.T) {
	s := NewScheduler[string]()
	s.AddPassThrough("gap")
	s.AddEdge("a", "gap")
	s.AddEdge("b", "gap")
	s.AddEdge("gap", "c")
	s.AddEdge("gap", "d")

	assert.Equal(t, 4, s.Len())
	assert.Equal(t, []string{"a", "b"}, s.Ready())
	assert.Equal(t, []string{"c", "d"}, s.Blocked())
	assert.Nil(t, s.Done("a"), "the gap still waits for b")
	assert.Equal(t, []string{"c", "d"}, s.Done("b"))
	assert.Empty(t, s.Blocked())
}

// TestSchedulerPassThroughHandsOutAtTheReadyingEdge: what a pass-through
// node readies is handed out at the place of the edge that readied it, which
// is where the edge from the completed node would have stood had the ordering
// been stated directly.
func TestSchedulerPassThroughHandsOutAtTheReadyingEdge(t *testing.T) {
	s := NewScheduler[string]()
	s.AddPassThrough("gap")
	s.AddEdge("gap", "early") // stated before b's own edge
	s.AddEdge("b", "middle")
	s.AddEdge("b", "gap")
	s.AddEdge("gap", "late") // stated after it

	assert.Equal(t, []string{"b"}, s.Ready())
	assert.Equal(t, []string{"early", "middle", "late"}, s.Done("b"))
}

// TestSchedulerPassThroughOnTheFrontier: a pass-through node with nothing to
// wait for releases its dependents into the initial frontier, each at its own
// insertion place.
func TestSchedulerPassThroughOnTheFrontier(t *testing.T) {
	s := NewScheduler[string]()
	s.AddPassThrough("outer")
	s.AddPassThrough("inner")
	s.Add("first")
	s.AddEdge("outer", "inner")
	s.AddEdge("inner", "second")
	s.Add("third")

	assert.Equal(t, []string{"first", "second", "third"}, s.Ready())
}

// TestSchedulerPassThroughBeforeEdges: the edge numbering Done's order needs
// starts with the first pass-through node, so one added later is refused.
func TestSchedulerPassThroughBeforeEdges(t *testing.T) {
	s := NewScheduler[string]()
	s.AddEdge("a", "b")
	assert.Panics(t, func() { s.AddPassThrough("gap") })
}

// TestDrainRunsAroundPassThroughNodes: Drain runs every real node once,
// edges through pass-through nodes respected, and never runs a pass-through
// node.
func TestDrainRunsAroundPassThroughNodes(t *testing.T) {
	s := NewScheduler[string]()
	s.AddPassThrough("gap")
	s.AddEdge("a", "gap")
	s.AddEdge("gap", "b")
	var mu sync.Mutex
	var ran []string
	err := Drain(context.Background(), s,
		func(string) int { return 0 },
		func(int) int { return 4 },
		func(string) int { return 1 },
		func(n string) {
			mu.Lock()
			defer mu.Unlock()
			ran = append(ran, n)
		})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, ran)
}
