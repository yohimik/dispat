// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// What "once per provider" means when several consumers ask at the same
// moment, and what a consumer that walks away takes with it.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// preparingCoordinator is a coordinator reduced to what a preparation reads:
// the run it belongs to and the two questions it asks the workspace.
//
// Nothing below reaches a pool or a mailbox, because what is being pinned here
// is the sharing rather than the building: the frame a preparation would
// execute is answered by the test, and answering it is where each row decides
// what the preparation does.
func preparingCoordinator(inputs func(string) []InputPackage,
	prepare func(string) (*PreparedProvider, error)) *Coordinator {
	coordinator := &Coordinator{Run: "run-1", Log: zerolog.Nop(),
		preparations: map[string]*preparation{}}
	coordinator.dispatch = Dispatch{Inputs: inputs, Prepare: prepare}
	return coordinator
}

// preparedRequest is one provider's frame as the closure names it, reduced to
// the package the coordinator reads off it.
func preparedRequest(packageName string) release.StageRequest {
	return release.StageRequest{Release: &plan.Release{
		Pkg: &model.Package{Name: packageName, Space: &model.Space{Name: "libs"}}}}
}

// TestOneProviderIsPreparedOnceHoweverManyConsumersAsk: the first caller does
// the work and the others wait on it. The provider's frame is described once,
// which is the only thing a consumer could observe about how many times it was
// built.
func TestOneProviderIsPreparedOnceHoweverManyConsumersAsk(t *testing.T) {
	var described atomic.Int64
	started, carryOn := make(chan struct{}), make(chan struct{})
	var once sync.Once
	refusal := errors.New("the node refused it")
	coordinator := preparingCoordinator(
		func(string) []InputPackage { return nil },
		func(string) (*PreparedProvider, error) {
			described.Add(1)
			once.Do(func() { close(started) })
			<-carryOn
			return nil, refusal
		})

	failures := make([]error, 4)
	var asking sync.WaitGroup
	for index := range failures {
		asking.Add(1)
		go func() {
			defer asking.Done()
			failures[index] = coordinator.ensurePrepared(t.Context(), "assets")
		}()
	}
	<-started
	close(carryOn)
	asking.Wait()

	assert.Equal(t, int64(1), described.Load(), "the provider's frame was resolved once")
	for index, err := range failures {
		require.Errorf(t, err, "consumer %d was told", index)
		assert.ErrorIsf(t, err, refusal, "consumer %d was told the same thing", index)
	}
}

// TestAPreparationIsNotAttemptedTwiceAfterItFailed: a failure is an answer,
// and a consumer arriving after it gets that answer rather than a second
// build of something that has just failed.
func TestAPreparationIsNotAttemptedTwiceAfterItFailed(t *testing.T) {
	var described atomic.Int64
	coordinator := preparingCoordinator(
		func(string) []InputPackage { return nil },
		func(string) (*PreparedProvider, error) {
			described.Add(1)
			return nil, errors.New("no frame")
		})

	first := coordinator.ensurePrepared(t.Context(), "assets")
	second := coordinator.ensurePrepared(t.Context(), "assets")

	require.Error(t, first)
	assert.Equal(t, first, second, "the second consumer was handed the first one's answer")
	assert.Equal(t, int64(1), described.Load(), "and nothing was attempted again")
}

// TestACancelledWaiterLeavesThePreparationRunning: a consumer that gives up
// gives up alone. The build belongs to the run, so the consumer still waiting
// for it is still told what became of it.
func TestACancelledWaiterLeavesThePreparationRunning(t *testing.T) {
	started, carryOn := make(chan struct{}), make(chan struct{})
	var once sync.Once
	coordinator := preparingCoordinator(
		func(string) []InputPackage { return nil },
		func(string) (*PreparedProvider, error) {
			once.Do(func() { close(started) })
			<-carryOn
			return nil, errors.New("the frame could not be described")
		})
	owner := make(chan error, 1)
	go func() { owner <- coordinator.ensurePrepared(t.Context(), "assets") }()
	<-started

	leaving, giveUp := context.WithCancel(t.Context())
	waiting := make(chan error, 1)
	go func() { waiting <- coordinator.ensurePrepared(leaving, "assets") }()
	giveUp()

	require.ErrorIs(t, <-waiting, context.Canceled,
		"the consumer that walked away was told it walked away")
	close(carryOn)
	select {
	case err := <-owner:
		require.Error(t, err, "the preparation ran to its own end")
	case <-time.After(10 * time.Second):
		t.Fatal("the preparation never finished after one of its waiters was cancelled")
	}
}

// TestAConsumerNamesTheProviderItCouldNotBuild: the failure a consumer's build
// is failed with says which provider was missing and carries the integrity
// code, because the operator's next question is which package to go and look
// at.
func TestAConsumerNamesTheProviderItCouldNotBuild(t *testing.T) {
	coordinator := preparingCoordinator(
		func(string) []InputPackage {
			return []InputPackage{
				{Package: "released", Path: "packages/released"},
				{Package: "assets", Path: "packages/assets", IsPrepared: true},
			}
		},
		func(string) (*PreparedProvider, error) { return nil, errors.New("no frame") })

	err := coordinator.prepareProviderOutputs(t.Context(), "ui:build", preparedRequest("ui"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "assets")
	assert.Contains(t, err.Error(), "ui")
	var coded interface{ DiagnosticCode() string }
	require.ErrorAs(t, err, &coded)
	assert.Equal(t, CodeIntegrity, coded.DiagnosticCode())
	assert.Equal(t, CategoryIntegrity, DiagnosticCategory(err))
}

// TestAReleasingProviderIsNeverPrepared: a provider this run releases is built
// by the task graph, so the preparation never looks at it. The closure names
// both kinds and only one of them is asked for.
func TestAReleasingProviderIsNeverPrepared(t *testing.T) {
	asked := map[string]bool{}
	coordinator := preparingCoordinator(
		func(string) []InputPackage {
			return []InputPackage{{Package: "released", Path: "packages/released"}}
		},
		func(name string) (*PreparedProvider, error) {
			asked[name] = true
			return &PreparedProvider{Request: preparedRequest(name)}, nil
		})

	require.NoError(t, coordinator.prepareProviderOutputs(t.Context(), "ui:build", preparedRequest("ui")))
	assert.Empty(t, asked, "nothing was built for a provider the run is releasing")
	assert.Empty(t, coordinator.PreparedRecords())
}

// TestPreparedRecordsSayWhatWasBuiltAndThatNothingWasPublished: the record a
// prepared provider leaves behind is the summary's, and its publication is
// always none.
func TestPreparedRecordsSayWhatWasBuiltAndThatNothingWasPublished(t *testing.T) {
	coordinator := preparingCoordinator(
		func(string) []InputPackage { return nil },
		func(string) (*PreparedProvider, error) { return nil, nil })

	coordinator.rememberPreparation("assets", &preparation{node: "build-a"})
	coordinator.rememberPreparation("vendored", &preparation{err: errors.New("it did not build")})

	assert.Equal(t, []PreparedRecord{
		{Package: "assets", Task: "assets:prepare", Node: "build-a",
			Computation: PreparationCompleted, Outputs: PreparationAdmitted,
			Publication: PreparationNone},
		{Package: "vendored", Task: "vendored:prepare",
			Computation: PreparationFailed, Outputs: PreparationNone,
			Publication: PreparationNone},
	}, coordinator.PreparedRecords())
}

// TestAPreparedFrameRunsUnderTheBuildStagesOwnNames: the scripts of a prepared
// provider read DISPAT_STAGE=build and are bracketed by beforeBuild and
// postBuild, because the frame is the package's build frame however this run
// came to ask for it.
func TestAPreparedFrameRunsUnderTheBuildStagesOwnNames(t *testing.T) {
	for kind, want := range map[string]string{
		KindPrepare: KindBuild,
		KindBuild:   KindBuild,
		KindPublish: KindPublish,
	} {
		t.Run("a "+kind+" assignment", func(t *testing.T) {
			assert.Equal(t, want, resolveFrameStage(kind))
		})
	}
}
