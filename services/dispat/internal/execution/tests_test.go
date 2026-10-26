// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// A stage that is neither the build nor the publish, the test stage here, as
// the generic stage path places and runs it: where it may run, what its
// assignment asks for back, what a node calls it, where the summary sorts it,
// and what a frame placed on this machine does.

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	public "github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// TestATestsFrameIsPlacedLikeTheBuild: the frame reads the build's runOnly
// value with the build's meaning, and a space that logs in does not pin it,
// because a login is the publication's affair.
func TestATestsFrameIsPlacedLikeTheBuild(t *testing.T) {
	for _, value := range []string{public.RunOnlyBoth, public.RunOnlyWorker, public.RunOnlyOrchestrator} {
		for _, login := range [][]string{nil, {"npm login"}} {
			space := &model.Space{RunOnly: &public.RunOnly{Build: value, Publish: public.RunOnlyOrchestrator},
				LoginScript: login}
			assert.Equal(t, ResolveStagePlacement(StageBuild, value, false),
				resolveDescriptorPlacement(testsStage(), space), "%s, login %v", value, login)
		}
	}
}

// testsStage is the test stage's descriptor, as the executor hands it over.
func testsStage() release.Stage {
	return release.Stage{Name: "test", Title: "Test", Placement: release.PlacementByBuild,
		Inputs: release.InputsOwnOutputs}
}

// TestATestsFrameCarriesNoDeclaredOutputs: the frame checks the bytes the
// build produced and describes none of its own, so its assignment asks for
// nothing back, and the admitted set stays the build's.
func TestATestsFrameCarriesNoDeclaredOutputs(t *testing.T) {
	request := release.StageRequest{Descriptor: testsStage(), Release: &plan.Release{
		Pkg: &model.Package{Name: "core", Space: &model.Space{BuildOutputs: []string{"dist"}}},
	}}
	assert.Equal(t, KindStage, resolveStageKind(request.Descriptor))
	assert.Nil(t, resolveDeclaredOutputs(request))
	assert.Equal(t, KindBuild, resolveStageKind(release.BuildStage()), "the build keeps its own kind")
}

// TestANodeRunsATestsFrameUnderItsOwnName: a node serves the generic kind, its
// scripts read the stage the assignment names, DISPAT_STAGE=test, its hooks
// are beforeTest and postTest, an assignment naming no stage is refused, and
// the summary sorts the frame between the build and the publication of its
// package.
func TestANodeRunsATestsFrameUnderItsOwnName(t *testing.T) {
	assignment := Assignment{Header: Header{Kind: KindStage}, Stage: "test"}
	assert.True(t, isFrameKind(KindStage))
	assert.Equal(t, "test", resolveFrameStage(assignment))
	assert.Equal(t, "Test", formatStageTitle(resolveFrameStage(assignment)))
	assert.True(t, isStageName("test"))
	for _, name := range []string{"", "run:test", "../tests", "test stage"} {
		assert.False(t, isStageName(name), "%q", name)
	}
	assert.Less(t, resolveStageRank(KindBuild), resolveStageRank("test"))
	assert.Less(t, resolveStageRank("test"), resolveStageRank(KindPublish))
	assert.Equal(t, "test", resolveAssignmentStage(KindStage, release.StageRequest{Stage: "test"}))
	assert.Empty(t, resolveAssignmentStage(KindBuild, release.StageRequest{Stage: "build"}),
		"a build assignment reads exactly as it always has")
}

// localTestsCoordinator is a run with no worker at all, so every frame it
// places runs on this machine.
func localTestsCoordinator() *Coordinator {
	return &Coordinator{Run: "run-1", Log: zerolog.Nop(), outputs: newOutputRegistry(),
		Pool: NewPool(nil, nil, LocalNode{Name: "here", Capacity: 1}, zerolog.Nop())}
}

// testsRequest is the test frame of one package pinned to this machine.
func testsRequest() release.StageRequest {
	space := &model.Space{RunOnly: &public.RunOnly{Build: public.RunOnlyOrchestrator, Publish: public.RunOnlyOrchestrator}}
	return release.StageRequest{Stage: "test", Descriptor: testsStage(),
		Release: &plan.Release{Pkg: &model.Package{Name: "core", Space: space}}}
}

// TestATestsFramePlacedHereRunsTheLocalSequence: the frame placed on this
// machine runs the executor's own sequence and names no node, its failure is
// reported in the executor's own words, and the slot it held is given back on
// both paths.
func TestATestsFramePlacedHereRunsTheLocalSequence(t *testing.T) {
	coordinator := localTestsCoordinator()
	ran := 0
	outcome, err := coordinator.Stage(t.Context(), testsRequest(), func(context.Context) (string, error) {
		ran++
		return "", nil
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, ran)
	assert.Empty(t, outcome.Node, "a frame this run kept names no worker")

	failure := errors.New("exit 1")
	outcome, err = coordinator.Stage(t.Context(), testsRequest(), func(context.Context) (string, error) {
		return "tests script failed", failure
	}, nil)
	require.ErrorIs(t, err, failure)
	assert.Equal(t, "tests script failed", outcome.LocalFailure)

	_, err = coordinator.Stage(t.Context(), testsRequest(), func(context.Context) (string, error) {
		return "", nil
	}, nil)
	require.NoError(t, err, "the slot of a failed frame was given back")
}
