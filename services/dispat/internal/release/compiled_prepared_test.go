// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

func TestPreparedProviderIsAnEffectiveGraphPredecessorBeforeConsumerCapacity(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	remote := &fakeRemote{}
	executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	executor.Remote = remote
	compiled, err := executor.CompileTaskGraph(p)
	require.NoError(t, err)
	baseDigest := compiled.Digest()
	consumer := taskGraphID(task{pkg: "app", kind: taskBuild})
	prepared := PreparedTaskSpec{TaskID: "ui:prepare", Package: "ui", Release: p.Releases["ui"],
		ConsumerTaskIDs: []string{consumer}, Weight: 1, Placement: PlacementByBuild}
	require.NoError(t, compiled.IncludePreparedTasks([]PreparedTaskSpec{prepared}))
	require.NotEqual(t, baseDigest, compiled.Digest())
	var found bool
	for _, shape := range compiled.TaskShapes() {
		if shape.ID == consumer {
			require.Contains(t, shape.Predecessors, prepared.TaskID)
			found = true
		}
	}
	require.True(t, found)
	started := make(chan struct{})
	release := make(chan struct{})
	executor.PreparedTask = func(ctx context.Context, got PreparedTaskSpec) error {
		require.Equal(t, prepared, got)
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := executor.RunCompiled(context.Background(), p, compiled)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("prepared provider did not start")
	}
	for _, request := range remote.requests() {
		require.False(t, request.Release.Pkg.Name == "app" && request.Stage == "build",
			"consumer cannot run while its prepared predecessor holds the sole build slot")
	}
	close(release)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("effective graph did not drain")
	}
	var appRan bool
	for _, request := range remote.requests() {
		appRan = appRan || request.Release.Pkg.Name == "app"
	}
	require.True(t, appRan)
}

func TestPreparedProviderRefusesUnknownConsumerWithoutChangingCompiledGraph(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	digest := compiled.Digest()
	err = compiled.IncludePreparedTasks([]PreparedTaskSpec{{TaskID: "ui:prepare", Package: "ui",
		Release:         p.Releases["ui"],
		ConsumerTaskIDs: []string{"missing"}, Weight: 1, Placement: PlacementByBuild}})
	require.ErrorContains(t, err, "unknown consumer")
	require.Equal(t, digest, compiled.Digest())
}

func TestPreparedProviderAugmentationIsOrderIndependentAndRefusesCycle(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	other := *p.Releases["ui"]
	pkg := *other.Pkg
	pkg.Name = "other"
	other.Pkg = &pkg
	p.Releases["other"] = &other
	p.Order = append(p.Order, "other")
	executor := newExecutor(execSpec{})
	first, err := executor.CompileTaskGraph(p)
	require.NoError(t, err)
	second, err := executor.CompileTaskGraph(p)
	require.NoError(t, err)
	consumer := taskGraphID(task{pkg: "app", kind: taskBuild})
	a := PreparedTaskSpec{TaskID: "ui:prepare", Package: "ui", Release: p.Releases["ui"],
		ConsumerTaskIDs: []string{consumer}, Weight: 1, Placement: PlacementByBuild}
	b := PreparedTaskSpec{TaskID: "other:prepare", Package: "other", Release: p.Releases["other"],
		ConsumerTaskIDs: []string{consumer}, Weight: 1, Placement: PlacementOrchestratorOnly}
	require.NoError(t, first.IncludePreparedTasks([]PreparedTaskSpec{a, b}))
	require.NoError(t, second.IncludePreparedTasks([]PreparedTaskSpec{b, a}))
	require.Equal(t, first.Digest(), second.Digest())
	require.Equal(t, first.TaskShapes(), second.TaskShapes())
	require.Equal(t, first.PreparedTaskSpecs(), second.PreparedTaskSpecs())
	returned := first.PreparedTaskSpecs()
	returned[0].ConsumerTaskIDs[0] = "mutated"
	require.NotEqual(t, returned[0].ConsumerTaskIDs, first.PreparedTaskSpecs()[0].ConsumerTaskIDs)
	firstReservations, secondReservations := first.ReservationStages(), second.ReservationStages()
	require.Len(t, firstReservations, len(secondReservations))
	for i := range firstReservations {
		require.Equal(t, firstReservations[i].TaskID, secondReservations[i].TaskID)
		require.Same(t, firstReservations[i].Release, secondReservations[i].Release)
		require.Equal(t, firstReservations[i].Descriptor.Placement, secondReservations[i].Descriptor.Placement)
	}

	cyclic, err := executor.CompileTaskGraph(p)
	require.NoError(t, err)
	baseDigest := cyclic.Digest()
	a.ConsumerTaskIDs = []string{b.TaskID}
	b.ConsumerTaskIDs = []string{a.TaskID}
	err = cyclic.IncludePreparedTasks([]PreparedTaskSpec{a, b})
	require.ErrorAs(t, err, new(*ExecutionCycleError))
	require.Equal(t, baseDigest, cyclic.Digest())
}

func TestPreparedProviderMayBeOutsideReleasingPlan(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	external := *p.Releases["ui"]
	pkg := *external.Pkg
	pkg.Name = "external"
	external.Pkg = &pkg
	require.NotContains(t, p.Releases, "external")
	consumer := taskGraphID(task{pkg: "app", kind: taskBuild})
	require.NoError(t, compiled.IncludePreparedTasks([]PreparedTaskSpec{{
		TaskID: "external:prepare", Package: "external", Release: &external,
		ConsumerTaskIDs: []string{consumer}, Weight: 1, Placement: PlacementByBuild,
	}}))
	var found bool
	for _, stage := range compiled.ReservationStages() {
		if stage.TaskID == "external:prepare" {
			require.Same(t, &external, stage.Release)
			found = true
		}
	}
	require.True(t, found)
}

func TestFailedPreparedProviderDoesNotStopUnrelatedRelease(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	remote := &fakeRemote{failBuild: "app"}
	executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	executor.Remote = remote
	compiled, err := executor.CompileTaskGraph(p)
	require.NoError(t, err)
	require.NoError(t, compiled.IncludePreparedTasks([]PreparedTaskSpec{{
		TaskID: "ui:prepare", Package: "ui", Release: p.Releases["ui"],
		ConsumerTaskIDs: []string{taskGraphID(task{pkg: "app", kind: taskBuild})},
		Weight:          1, Placement: PlacementByBuild,
	}}))
	executor.PreparedTask = func(context.Context, PreparedTaskSpec) error { return errors.New("provider failed") }
	results, err := executor.RunCompiled(context.Background(), p, compiled)
	require.NoError(t, err, "a settled provider failure does not stop independent work")
	require.Equal(t, StatusPublished, results["core"].Status)
	require.Equal(t, StatusFailed, results["app"].Status)
}

func TestPreparedCallbackPanicIsContained(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	compiled, err := executor.CompileTaskGraph(p)
	require.NoError(t, err)
	require.NoError(t, compiled.IncludePreparedTasks([]PreparedTaskSpec{{
		TaskID: "ui:prepare", Package: "ui", Release: p.Releases["ui"],
		ConsumerTaskIDs: []string{taskGraphID(task{pkg: "app", kind: taskBuild})},
		Weight:          1, Placement: PlacementByBuild,
	}}))
	executor.PreparedTask = func(context.Context, PreparedTaskSpec) error { panic("prepared boom") }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = executor.RunCompiled(ctx, p, compiled)
	require.ErrorContains(t, err, "prepared boom")
}
