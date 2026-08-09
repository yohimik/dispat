package graph

// Drain's weighted launch accounting. The tests observe concurrency through
// a monitor goroutine that tracks the running set, because Drain's only
// contract is "never more than the budget's slots occupied" — the schedule
// itself is free to vary.

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// weighted drains independent nodes with the given weights under one class
// and budget, returning the maximum number of slots ever occupied at once
// and the peak number of simultaneously running nodes. Each node dwells long
// enough that an over-admitting launch loop — which starts every admitted
// goroutine before awaiting any completion — would be observed as overlap,
// so the "never" claims are not satisfied vacuously by fast bodies.
func weighted(t *testing.T, budget int, weights map[string]int) (peakSlots, peakNodes int) {
	t.Helper()
	s := NewScheduler[string]()
	for name := range weights {
		s.Add(name)
	}
	var mu sync.Mutex
	slots, running := 0, 0
	cost := func(n string) int { return min(max(1, weights[n]), max(1, budget)) }
	err := Drain(context.Background(), s,
		func(string) int { return 0 },
		func(int) int { return budget },
		func(n string) int { return weights[n] },
		func(n string) {
			mu.Lock()
			slots += cost(n)
			running++
			peakSlots = max(peakSlots, slots)
			peakNodes = max(peakNodes, running)
			mu.Unlock()
			time.Sleep(25 * time.Millisecond)
			mu.Lock()
			slots -= cost(n)
			running--
			mu.Unlock()
		})
	require.NoError(t, err)
	return peakSlots, peakNodes
}

func TestDrainWeightedBudget(t *testing.T) {
	// Four weight-2 nodes in a budget of 3: only one fits at a time, so the
	// occupied slots never exceed the budget and the nodes serialise.
	peakSlots, peakNodes := weighted(t, 3, map[string]int{"a": 2, "b": 2, "c": 2, "d": 2})
	assert.LessOrEqual(t, peakSlots, 3, "occupied slots never exceed the budget")
	assert.Equal(t, 1, peakNodes, "two weight-2 nodes cannot share a budget of 3")
}

func TestDrainWeightClampedToBudget(t *testing.T) {
	// A weight past the budget is clamped, so the node still launches (alone)
	// instead of waiting forever, and everything completes.
	peakSlots, _ := weighted(t, 2, map[string]int{"heavy": 99, "light": 1})
	assert.LessOrEqual(t, peakSlots, 2)
}

func TestDrainZeroWeightCostsOne(t *testing.T) {
	// Weights below 1 (a hand-built package's zero value) cost the ordinary
	// one slot; with budget 2, two of them run — accounting returns to zero
	// or the drain would stall before the third.
	peakSlots, peakNodes := weighted(t, 2, map[string]int{"a": 0, "b": 0, "c": 0})
	assert.LessOrEqual(t, peakSlots, 2)
	assert.LessOrEqual(t, peakNodes, 2)
}

func TestDrainFIFOWithinClass(t *testing.T) {
	// Under a serialising budget, independent nodes run in the order they
	// became ready — the queue is a queue, not a stack.
	s := NewScheduler[string]()
	for _, n := range []string{"a", "b", "c"} {
		s.Add(n)
	}
	var order []string
	var mu sync.Mutex
	err := Drain(context.Background(), s,
		func(string) int { return 0 },
		func(int) int { return 1 },
		func(string) int { return 1 },
		func(n string) {
			mu.Lock()
			order = append(order, n)
			mu.Unlock()
		})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, order)
}

func TestDrainHeavyHeadNotOvertaken(t *testing.T) {
	// The head-of-line discipline: a heavy node waiting for slots is not
	// overtaken by a lighter node that became ready after it. With p1 (weight
	// 1) holding a slot of the budget of 2, q's completion readies heavy
	// (weight 2, does not fit) and then l1 (weight 1, would fit); l1 must
	// still wait its turn behind heavy. The assertion is structural — in no
	// interleaving does FIFO start l1 first, whereas a stack pops l1 first
	// whenever p1 is still running.
	s := NewScheduler[string]()
	s.Add("p1")
	s.Add("q")
	s.AddEdge("q", "heavy")
	s.AddEdge("q", "l1")
	weights := map[string]int{"p1": 1, "q": 1, "heavy": 2, "l1": 1}
	dwell := map[string]time.Duration{"p1": 150 * time.Millisecond, "q": 10 * time.Millisecond}
	var order []string
	var mu sync.Mutex
	err := Drain(context.Background(), s,
		func(string) int { return 0 },
		func(int) int { return 2 },
		func(n string) int { return weights[n] },
		func(n string) {
			mu.Lock()
			order = append(order, n)
			mu.Unlock()
			time.Sleep(dwell[n])
		})
	require.NoError(t, err)
	iHeavy := slices.Index(order, "heavy")
	iLight := slices.Index(order, "l1")
	require.GreaterOrEqual(t, iHeavy, 0)
	require.GreaterOrEqual(t, iLight, 0)
	assert.Less(t, iHeavy, iLight, "heavy waits at the head; l1 does not overtake it")
}

func TestDrainWeightedRespectsEdges(t *testing.T) {
	// A weighted node behind an edge still waits for its provider: weights
	// change slot accounting, never ordering.
	s := NewScheduler[string]()
	s.Add("provider")
	s.Add("consumer")
	s.AddEdge("provider", "consumer")
	var order []string
	var mu sync.Mutex
	err := Drain(context.Background(), s,
		func(string) int { return 0 },
		func(int) int { return 4 },
		func(n string) int { return 3 },
		func(n string) {
			mu.Lock()
			order = append(order, n)
			mu.Unlock()
		})
	require.NoError(t, err)
	assert.Equal(t, []string{"provider", "consumer"}, order)
}
