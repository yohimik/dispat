// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestPool is a pool of the given reports under the names a-node, b-node
// and so on, in the shape preflight hands one over.
func newTestPool(nodes ...*NodeReport) *Pool {
	links := make([]Link, 0, len(nodes))
	for index := range nodes {
		links = append(links, Link{Name: string(rune('a'+index)) + "-node", Endpoint: "file:///dev/null"})
	}
	return NewPool(links, nodes, LocalNode{Name: "here", Capacity: 1}, zerolog.Nop())
}

// linuxNode is a healthy node of the ordinary platform with the given
// capacity.
func linuxNode(capacity int) *NodeReport {
	return &NodeReport{Protocol: ProtocolVersion, OS: "linux", Arch: "amd64", Capacity: capacity}
}

// TestPoolPlacement: which node a task is given, in the situations placement
// actually has to decide something.
func TestPoolPlacement(t *testing.T) {
	for name, tc := range map[string]struct {
		nodes     []*NodeReport
		platforms []string
		takeFirst int
		want      string
	}{
		"the first node by name when nothing is in flight": {
			nodes: []*NodeReport{linuxNode(2), linuxNode(2)}, want: "a-node"},
		"the least loaded node once one is busy": {
			nodes: []*NodeReport{linuxNode(2), linuxNode(2)}, takeFirst: 1, want: "b-node"},
		"the only node whose platform satisfies the package": {
			nodes: []*NodeReport{
				{Protocol: ProtocolVersion, OS: "linux", Arch: "amd64", Capacity: 1},
				{Protocol: ProtocolVersion, OS: "darwin", Arch: "arm64", Capacity: 1},
			},
			platforms: []string{"darwin/arm64"}, want: "b-node"},
		"any node when the package declares no platform": {
			nodes: []*NodeReport{{Protocol: ProtocolVersion, OS: "plan9", Arch: "mips", Capacity: 1}},
			want:  "a-node"},
	} {
		t.Run(name, func(t *testing.T) {
			pool := newTestPool(tc.nodes...)
			for range tc.takeFirst {
				taken, err := pool.Acquire(t.Context(), nil, PlacementAnyNode)
				require.NoError(t, err)
				assert.Equal(t, "a-node", taken.Node)
			}

			lease, err := pool.Acquire(t.Context(), tc.platforms, PlacementAnyNode)

			require.NoError(t, err)
			assert.Equal(t, tc.want, lease.Node)
		})
	}
}

// TestPoolWaitsForACapacitySlot: a task asking for a node every one of which
// is full waits, and is given the slot the next attempt gives back.
func TestPoolWaitsForACapacitySlot(t *testing.T) {
	pool := newTestPool(linuxNode(1))
	held, err := pool.Acquire(t.Context(), nil, PlacementWorker)
	require.NoError(t, err)

	waited := make(chan *Lease, 1)
	go func() {
		lease, err := pool.Acquire(t.Context(), nil, PlacementWorker)
		require.NoError(t, err)
		waited <- lease
	}()
	select {
	case <-waited:
		t.Fatal("a full node handed out a second slot")
	case <-time.After(50 * time.Millisecond):
	}
	held.Release()

	select {
	case lease := <-waited:
		assert.Equal(t, "a-node", lease.Node)
	case <-time.After(5 * time.Second):
		t.Fatal("the returned slot never reached the waiting task")
	}
}

// TestPoolLeakedSlotIsNeverReturned: an attempt whose node stopped answering
// keeps its slot and takes the node out of the pool, so nothing else is
// placed beside a process nobody can account for.
func TestPoolLeakedSlotIsNeverReturned(t *testing.T) {
	pool := newTestPool(linuxNode(2), linuxNode(1))
	lost, err := pool.Acquire(t.Context(), nil, PlacementAnyNode)
	require.NoError(t, err)
	require.Equal(t, "a-node", lost.Node)

	lost.Leak(LeakTaskDeadline)
	lost.Release() // a lease settles once, whatever the caller does afterwards

	next, err := pool.Acquire(t.Context(), nil, PlacementAnyNode)
	require.NoError(t, err)
	assert.Equal(t, "b-node", next.Node, "the unhealthy node is skipped although it had a free slot")
}

// TestPoolWithoutAHealthyNodeFailsAtOnce: a task that could only have run on
// nodes that are all gone is failed immediately rather than waiting for a
// machine that will not come back, and the failure names what it needed.
func TestPoolWithoutAHealthyNodeFailsAtOnce(t *testing.T) {
	pool := newTestPool(linuxNode(1))
	lease, err := pool.Acquire(t.Context(), nil, PlacementAnyNode)
	require.NoError(t, err)
	lease.Leak(LeakTaskDeadline)

	_, err = pool.Acquire(t.Context(), []string{"linux/amd64"}, PlacementAnyNode)

	require.Error(t, err)
	assert.Equal(t, CodeIntegrity, diagnosticCodeOf(err))
	assert.Equal(t, CategoryIntegrity, DiagnosticCategory(err))
	assert.Contains(t, err.Error(), "linux/amd64")
	assert.Contains(t, err.Error(), "a-node")
}

// TestPoolWithoutACompatiblePlatformFailsAtOnce: waiting cannot help a task
// no node in the pool could ever run, so it does not wait.
func TestPoolWithoutACompatiblePlatformFailsAtOnce(t *testing.T) {
	pool := newTestPool(linuxNode(1))

	_, err := pool.Acquire(t.Context(), []string{"plan9/mips"}, PlacementAnyNode)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan9/mips")
}

// TestPoolWaitEndsWithTheRun: a task waiting for a slot when the run is
// interrupted stops waiting, rather than holding the run open on a node that
// is still busy.
func TestPoolWaitEndsWithTheRun(t *testing.T) {
	pool := newTestPool(linuxNode(1))
	held, err := pool.Acquire(t.Context(), nil, PlacementWorker)
	require.NoError(t, err)
	defer held.Release()
	ctx, cancel := context.WithCancel(t.Context())

	go cancel()
	_, err = pool.Acquire(ctx, nil, PlacementWorker)

	require.ErrorIs(t, err, context.Canceled)
}

// diagnosticCodeOf is the numbered code an error carries, for the assertions
// that are about which code a refusal took.
func diagnosticCodeOf(err error) string {
	var carrier interface{ DiagnosticCode() string }
	if !errors.As(err, &carrier) {
		return ""
	}
	return carrier.DiagnosticCode()
}

// TestALocalFrameNeverLeaksItsSlot: this machine is the one node whose
// capacity cannot be lost. A frame placed here runs in this process, so a
// frame that ended is a frame whose goroutine returned and there is no unknown
// process left holding anything. A run that helped its own pool used to lose
// the slot it lent itself and take the orchestrator out of the pool with it,
// which also fails every frame only the orchestrator may run.
func TestALocalFrameNeverLeaksItsSlot(t *testing.T) {
	pool := newTestPool(linuxNode(1))

	local, err := pool.Acquire(t.Context(), nil, PlacementOrchestrator)
	require.NoError(t, err)
	require.True(t, local.IsLocal)
	local.Leak(LeakTaskDeadline)

	again, err := pool.Acquire(t.Context(), nil, PlacementOrchestrator)
	require.NoError(t, err, "the slot came back and the node is still in the pool")
	assert.True(t, again.IsLocal)
	again.Release()

	// A worker's slot is the opposite statement and stays where it is.
	worker, err := pool.Acquire(t.Context(), nil, PlacementWorker)
	require.NoError(t, err)
	worker.Leak(LeakTaskDeadline)
	_, err = pool.Acquire(t.Context(), nil, PlacementWorker)
	require.Error(t, err, "the node that stopped answering is out of the pool")
}
