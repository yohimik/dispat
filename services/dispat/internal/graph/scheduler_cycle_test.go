package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCycleNodesExcludesDescendantsAndIncludesPassThroughs(t *testing.T) {
	s := NewScheduler[string]()
	s.AddPassThrough("relay")
	s.Add("a")
	s.Add("b")
	s.Add("downstream")
	s.AddEdge("a", "relay")
	s.AddEdge("relay", "b")
	s.AddEdge("b", "a")
	s.AddEdge("b", "downstream")
	assert.Equal(t, []string{"relay", "a", "b"}, s.CycleNodes())
	assert.Equal(t, []string{"relay", "a", "b"}, s.CycleNodes(), "inspection does not consume the graph")
}

func TestCycleNodesFindsSelfLoop(t *testing.T) {
	s := NewScheduler[string]()
	s.AddEdge("a", "a")
	s.AddEdge("a", "b")
	assert.Equal(t, []string{"a"}, s.CycleNodes())
}
