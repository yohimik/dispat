// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Where a frame is allowed to run, and which node the pool then gives it.
//
// The two are tested apart because they answer different questions. The
// decision reads a configuration and says what the pool is asked for; the
// pool reads what it has and says which machine. A run that placed signing
// work on a worker would have got one of the two wrong, and only one of them
// needs a pool to be wrong in.

import (
	"runtime"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	public "github.com/yohimik/dispat/pkg/models"
)

// TestResolveStagePlacement: every combination of a stated value, a stage and
// a space that logs in.
func TestResolveStagePlacement(t *testing.T) {
	for name, row := range map[string]struct {
		stage            string
		value            string
		isSpaceLoggingIn bool
		want             Placement
	}{
		"a build nobody placed": {
			stage: StageBuild, value: public.RunOnlyBoth, want: PlacementAnyNode},
		"a build pinned to a worker": {
			stage: StageBuild, value: public.RunOnlyWorker, want: PlacementWorker},
		"a build pinned to this machine": {
			stage: StageBuild, value: public.RunOnlyOrchestrator, want: PlacementOrchestrator},
		"a build of a space that logs in": {
			stage: StageBuild, value: public.RunOnlyBoth, isSpaceLoggingIn: true, want: PlacementAnyNode},
		"a publish nobody placed": {
			stage: StagePublish, value: public.RunOnlyBoth, want: PlacementOrchestrator},
		"a publish pinned to a worker": {
			stage: StagePublish, value: public.RunOnlyWorker, want: PlacementWorker},
		"a publish pinned to this machine": {
			stage: StagePublish, value: public.RunOnlyOrchestrator, want: PlacementOrchestrator},
		"a publish of a space that logs in": {
			stage: StagePublish, value: public.RunOnlyBoth, isSpaceLoggingIn: true, want: PlacementOrchestrator},
		"a publish of a space that logs in and is pinned here": {
			stage: StagePublish, value: public.RunOnlyOrchestrator, isSpaceLoggingIn: true,
			want: PlacementOrchestrator},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, row.want, ResolveStagePlacement(row.stage, row.value, row.isSpaceLoggingIn))
		})
	}
}

// TestPlacementIsDelegable: the one question a caller with no pool asks, so
// that a release naming work only a worker may run is refused before it
// starts rather than running that work in the one place it was told not to.
func TestPlacementIsDelegable(t *testing.T) {
	assert.True(t, PlacementWorker.IsDelegable())
	assert.False(t, PlacementAnyNode.IsDelegable())
	assert.False(t, PlacementOrchestrator.IsDelegable())
}

// nativeNode is a report describing this machine, so that a pool test can say
// something about the platform filter without knowing which machine it is on.
func nativeNode(capacity int) *NodeReport {
	return &NodeReport{Protocol: ProtocolVersion, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Capacity: capacity}
}

// TestPoolKeepsThisMachineForLast: the orchestrator is one more node of the
// pool, and it is the node of last resort: it takes a frame only when no
// worker has room for it, because it is also the one machine that owns the
// version stages, the publications and the records.
func TestPoolKeepsThisMachineForLast(t *testing.T) {
	pool := NewPool([]Link{{Name: "a-node", Endpoint: "file:///dev/null"}},
		[]*NodeReport{nativeNode(1)}, LocalNode{Name: "here", Capacity: 2}, zerolog.Nop())

	first, err := pool.AcquireNear(t.Context(), nil, PlacementAnyNode, "")
	require.NoError(t, err)
	assert.Equal(t, "a-node", first.Node)
	assert.False(t, first.IsLocal, "a free worker is preferred to this machine")

	second, err := pool.AcquireNear(t.Context(), nil, PlacementAnyNode, "")
	require.NoError(t, err)
	assert.Equal(t, "here", second.Node)
	assert.True(t, second.IsLocal, "with the worker full the frame is taken here rather than queued")

	third, err := pool.AcquireNear(t.Context(), nil, PlacementAnyNode, "")
	require.NoError(t, err)
	assert.True(t, third.IsLocal, "this machine's own capacity is what bounds it")

	first.Release()
	fourth, err := pool.AcquireNear(t.Context(), nil, PlacementAnyNode, "")
	require.NoError(t, err)
	assert.Equal(t, "a-node", fourth.Node, "a returned worker slot is preferred again")
}

// TestPoolHonoursAPinnedPlacement: a pinned frame is given the kind of node
// it names and never the other kind, whatever is free.
func TestPoolHonoursAPinnedPlacement(t *testing.T) {
	pool := NewPool([]Link{{Name: "a-node", Endpoint: "file:///dev/null"}},
		[]*NodeReport{nativeNode(2)}, LocalNode{Name: "here", Capacity: 2}, zerolog.Nop())

	pinnedHere, err := pool.AcquireNear(t.Context(), nil, PlacementOrchestrator, "")
	require.NoError(t, err)
	assert.True(t, pinnedHere.IsLocal, "a frame pinned here is placed here although a worker is free")

	pinnedAway, err := pool.AcquireNear(t.Context(), nil, PlacementWorker, "")
	require.NoError(t, err)
	assert.Equal(t, "a-node", pinnedAway.Node)
	assert.False(t, pinnedAway.IsLocal, "a frame pinned to a worker never falls back to this machine")
}

// TestPoolRefusesAPinnedPlacementNothingSatisfies: waiting cannot help a
// frame pinned to a kind of node the pool has none of, so it does not wait.
func TestPoolRefusesAPinnedPlacementNothingSatisfies(t *testing.T) {
	pool := NewPool(nil, nil, LocalNode{Name: "here", Capacity: 1}, zerolog.Nop())

	_, err := pool.AcquireNear(t.Context(), nil, PlacementWorker, "")

	require.Error(t, err)
	assert.Equal(t, CodeIntegrity, diagnosticCodeOf(err))
	assert.Contains(t, err.Error(), string(PlacementWorker))
}

// TestPoolFiltersThisMachineByPlatformToo: the orchestrator is held to the
// package's declared platforms exactly as a worker is, so a package that
// builds only somewhere else is never quietly built here.
func TestPoolFiltersThisMachineByPlatformToo(t *testing.T) {
	pool := NewPool([]Link{{Name: "a-node", Endpoint: "file:///dev/null"}},
		[]*NodeReport{{Protocol: ProtocolVersion, OS: "plan9", Arch: "mips", Capacity: 1}},
		LocalNode{Name: "here", Capacity: 1}, zerolog.Nop())

	lease, err := pool.AcquireNear(t.Context(), []string{"plan9/mips"}, PlacementAnyNode, "")
	require.NoError(t, err)
	assert.Equal(t, "a-node", lease.Node)

	_, err = pool.AcquireNear(t.Context(), []string{"plan9/mips"}, PlacementOrchestrator, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan9/mips")
}
