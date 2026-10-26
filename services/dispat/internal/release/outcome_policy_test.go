// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

type parallelFailureRunner struct {
	*fakeRunner
	holdStarted     chan struct{}
	releaseHold     chan struct{}
	buildFailed     chan struct{}
	appBuildStarted chan struct{}
}

func (r *parallelFailureRunner) Run(ctx context.Context, dir, command string, env []string,
	stdout, stderr io.Writer) error {
	if dir == "core" && command == "hold" {
		close(r.holdStarted)
		select {
		case <-r.releaseHold:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if dir == "core" && command == "build" {
		<-r.holdStarted
		close(r.buildFailed)
		return errors.New("core build failed")
	}
	if dir == "app" && command == "build" {
		close(r.appBuildStarted)
	}
	return r.fakeRunner.Run(ctx, dir, command, env, stdout, stderr)
}

func TestOutcomeEvaluatorRechecksAReachedProviderAfterEarlyAdmission(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	p.Releases["app"].OwnBump = ccme.BumpNone
	p.Releases["app"].Units, p.Releases["app"].FreshUnits = nil, nil
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	evaluator := compiled.NewOutcomeEvaluator()

	skip, _, err := evaluator.ShouldSkip("app")
	require.NoError(t, err)
	require.False(t, skip, "a live provider does not block an early stage")
	require.NoError(t, evaluator.Apply("core", OutcomeFailed))
	skip, blocker, err := evaluator.ShouldSkip("app")
	require.NoError(t, err)
	require.True(t, skip, "the failed provider reached through unreleasing ui removes the only cause")
	require.Equal(t, "core", blocker)
}

func TestOutcomeEvaluatorPreservesOptionalOwnCauseAndRequiredRecord(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	evaluator := compiled.NewOutcomeEvaluator()
	require.NoError(t, evaluator.Apply("core", OutcomeFailed))
	skip, _, err := evaluator.ShouldSkip("app")
	require.NoError(t, err)
	require.False(t, skip, "an independent own change survives an optional failed provider")

	// A failed required record overrides both the optional relation and the
	// consumer's independent change. The transition is monotone in one run.
	require.Error(t, evaluator.Apply("core", OutcomePublished),
		"a failed release cannot later be reclassified as published")
	fresh := compiled.NewOutcomeEvaluator()
	require.Error(t, fresh.ValidateOutcome("core", "invented"))
	require.NoError(t, fresh.ValidateOutcome("core", string(OutcomePublished)))
	require.Empty(t, fresh.outcomes, "pre-persist validation cannot mutate durable policy state")
	require.NoError(t, fresh.Apply("core", OutcomePublished))
	require.Error(t, fresh.ValidateOutcome("core", string(OutcomeFailed)))
	require.NoError(t, fresh.Apply("core", OutcomeRecordBlocked))
	skip, blocker, err := fresh.ShouldSkip("app")
	require.NoError(t, err)
	require.True(t, skip)
	require.Equal(t, "core", blocker)
}

func TestExecutorEmitsOnlySettledPackageOutcomesBeforeDependentPublication(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	p.Releases["app"].OwnBump = ccme.BumpNone
	p.Releases["app"].Units, p.Releases["app"].FreshUnits = nil, nil
	runner := &fakeRunner{fail: map[string]bool{"publish core": true}}
	e := newExecutor(execSpec{Runner: runner, Build: 2, Publish: 2})
	var mu sync.Mutex
	var settled []struct {
		pkg     string
		outcome PackageOutcome
	}
	e.PackageOutcomeSettled = func(_ context.Context, pkg, taskID string, outcome PackageOutcome) error {
		mu.Lock()
		defer mu.Unlock()
		settled = append(settled, struct {
			pkg     string
			outcome PackageOutcome
		}{pkg, outcome})
		return nil
	}
	compiled, err := e.CompileTaskGraph(p)
	require.NoError(t, err)
	results, err := e.RunCompiled(context.Background(), p, compiled)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, results["core"].Status)
	require.Equal(t, StatusSkipped, results["app"].Status)
	require.Equal(t, -1, runner.indexOf("publish app"))
	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, settled, struct {
		pkg     string
		outcome PackageOutcome
	}{"core", OutcomeFailed})
	require.Contains(t, settled, struct {
		pkg     string
		outcome PackageOutcome
	}{"app", OutcomeSkipped})
}

func TestExecutorRefusesDispatchAfterOutcomePersistenceFails(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	runner := &fakeRunner{}
	e := newExecutor(execSpec{Runner: runner, Build: 2, Publish: 2})
	want := errors.New("ledger unavailable")
	e.PackageOutcomeSettled = func(_ context.Context, pkg, taskID string, outcome PackageOutcome) error {
		if pkg == "core" && outcome == OutcomePublished {
			return want
		}
		return nil
	}
	compiled, err := e.CompileTaskGraph(p)
	require.NoError(t, err)
	_, err = e.RunCompiled(context.Background(), p, compiled)
	require.ErrorIs(t, err, want)
	require.Equal(t, -1, runner.indexOf("publish app"),
		"a consumer cannot publish past an outcome that was not persisted")
}

func TestExecutorContainsOutcomePersistencePanicBeforeConsumerDispatch(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	runner := &fakeRunner{}
	e := newExecutor(execSpec{Runner: runner, Build: 2, Publish: 2})
	e.PackageOutcomeSettled = func(_ context.Context, pkg, taskID string, outcome PackageOutcome) error {
		if pkg == "core" && outcome == OutcomePublished {
			panic("ledger writer stopped")
		}
		return nil
	}
	compiled, err := e.CompileTaskGraph(p)
	require.NoError(t, err)
	_, err = e.RunCompiled(context.Background(), p, compiled)
	require.ErrorContains(t, err, "package outcome persistence panicked")
	require.Equal(t, -1, runner.indexOf("publish app"))
}

func TestParallelFailedStageWaitsForSiblingBeforeConsumerAdmission(t *testing.T) {
	for _, ownCause := range []bool{false, true} {
		name := "propagated cause"
		if ownCause {
			name = "independent own cause"
		}
		t.Run(name, func(t *testing.T) {
			p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
			core := p.Releases["core"].Pkg.Space
			core.UserStages = []model.UserStage{{Name: "hold", Commands: []string{"hold"}}}
			core.StageGraph = graphOf(t,
				spec("version"), spec("build", after("version")),
				spec("hold", after("version"), parallel("build")),
				spec("publish", after("build", "hold")))
			if !ownCause {
				app := p.Releases["app"]
				app.OwnBump, app.Units, app.FreshUnits = ccme.BumpNone, nil, nil
			}
			runner := &parallelFailureRunner{fakeRunner: &fakeRunner{},
				holdStarted: make(chan struct{}), releaseHold: make(chan struct{}),
				buildFailed: make(chan struct{}), appBuildStarted: make(chan struct{})}
			e := newExecutor(execSpec{Runner: runner.fakeRunner, Build: 3, Publish: 2})
			e.Runner = runner
			var mu sync.Mutex
			coreSettled := false
			coreCause := ""
			taskSettled := make(map[string]PackageOutcome)
			e.PackageOutcomeSettled = func(_ context.Context, pkg, taskID string, outcome PackageOutcome) error {
				if pkg == "core" && outcome == OutcomeFailed {
					mu.Lock()
					coreSettled = true
					coreCause = taskID
					mu.Unlock()
				}
				return nil
			}
			e.TaskOutcomeSettled = func(_ context.Context, taskID string, outcome PackageOutcome) error {
				mu.Lock()
				taskSettled[taskID] = outcome
				mu.Unlock()
				return nil
			}
			compiled, err := e.CompileTaskGraph(p)
			require.NoError(t, err)
			type result struct {
				packages map[string]*Result
				err      error
			}
			done := make(chan result, 1)
			go func() {
				packages, err := e.RunCompiled(context.Background(), p, compiled)
				done <- result{packages, err}
			}()
			select {
			case <-runner.buildFailed:
			case <-time.After(5 * time.Second):
				close(runner.releaseHold)
				t.Fatal("parallel build did not fail while its sibling was active")
			}
			premature := false
			select {
			case <-runner.appBuildStarted:
				premature = true
			case <-time.After(100 * time.Millisecond):
			}
			mu.Lock()
			settledBeforeRelease := coreSettled
			_, buildTerminalBeforeRelease := taskSettled[taskGraphID(task{pkg: "core", kind: taskBuild})]
			mu.Unlock()
			close(runner.releaseHold)
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("release did not finish after the sibling was released")
			}
			require.False(t, premature, "a failed build edge cannot release the consumer before sibling cleanup")
			require.False(t, settledBeforeRelease, "package outcome is not final while a sibling owns the checkout")
			require.False(t, buildTerminalBeforeRelease,
				"a failed task cannot terminal before its parallel sibling has stopped")
			require.NoError(t, got.err)
			require.Equal(t, StatusFailed, got.packages["core"].Status)
			mu.Lock()
			cause := coreCause
			_, buildTerminal := taskSettled[taskGraphID(task{pkg: "core", kind: taskBuild})]
			buildOutcome := taskSettled[taskGraphID(task{pkg: "core", kind: taskBuild})]
			holdOutcome := taskSettled[taskGraphID(task{pkg: "core", kind: taskUser, name: "hold"})]
			var failedTerminals int
			for taskID, outcome := range taskSettled {
				if strings.HasPrefix(taskID, "core:") && outcome == OutcomeFailed {
					failedTerminals++
				}
			}
			mu.Unlock()
			require.Equal(t, taskGraphID(task{pkg: "core", kind: taskBuild}), cause,
				"the failed build, not the last parallel sibling, settles the package")
			require.True(t, buildTerminal)
			require.Equal(t, OutcomeFailed, buildOutcome,
				"the causal failed task carries the settled package result")
			require.Empty(t, holdOutcome, "a successful last sibling does not carry another task's failure")
			require.Equal(t, 1, failedTerminals, "one task carries the actual package settlement")
			if ownCause {
				require.Equal(t, StatusPublished, got.packages["app"].Status)
				require.NotEqual(t, -1, runner.indexOf("build app"))
			} else {
				require.Equal(t, StatusSkipped, got.packages["app"].Status)
				require.Equal(t, -1, runner.indexOf("build app"))
			}
		})
	}
}
