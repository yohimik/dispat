// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// panickingRunner is fakeRunner with one command that panics instead of
// running, which is what a bug inside a stage looks like from the executor.
type panickingRunner struct {
	fakeRunner
	panicOn string
}

func (r *panickingRunner) Run(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) error {
	if command+" "+dir == r.panicOn {
		panic("stage bug")
	}
	return r.fakeRunner.Run(ctx, dir, command, env, stdout, stderr)
}

// runWithin runs the executor and fails the test instead of hanging when the
// run never returns, which is what a panic that left a lock held looks like.
func runWithin(t *testing.T, e *Executor, p *plan.Plan) map[string]*Result {
	t.Helper()
	done := make(chan map[string]*Result, 1)
	go func() { done <- e.Run(context.Background(), p) }()
	select {
	case results := <-done:
		return results
	case <-time.After(30 * time.Second):
		t.Fatal("the run never finished after a task panicked")
		return nil
	}
}

// TestRunContainsAPanicInABuildFrame: a panic inside one package's build is
// that package's failure at its build, with the internal error as its reason,
// and it stops the rest of the run the way an interrupt does: work that had
// not started is cancelled rather than failed or skipped. The process and the
// run survive to report it.
func TestRunContainsAPanicInABuildFrame(t *testing.T) {
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	var logs bytes.Buffer
	runner := &panickingRunner{panicOn: "build a"}
	e := newExecutor(execSpec{Runner: &runner.fakeRunner, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	e.Runner = runner
	e.Log = zerolog.New(&logs)

	results := runWithin(t, e, p)

	require.Equal(t, StatusFailed, results["a"].Status)
	assert.Equal(t, "build", results["a"].FailedStage)
	assert.ErrorContains(t, results["a"].Err, "internal error: stage bug")
	assert.Equal(t, StatusCancelled, results["b"].Status, "work behind the panic is cancelled, not failed")
	assert.Contains(t, logs.String(), "run stopped after an internal error")
	assert.Equal(t, -1, runner.indexOf("publish a"), "nothing of the panicked package publishes")
}

// TestRunKeepsAPackagePublishedWhenARecorderPanics: a panic after the publish
// frame returned success cannot unpublish the package. It stays published,
// the panic is a critical of its record, and its consumers are blocked as
// they are behind any incomplete record.
func TestRunKeepsAPackagePublishedWhenARecorderPanics(t *testing.T) {
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	runner := &fakeRunner{}
	e := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	e.Recorders = []ReleaseRecorderx{recorderFunc(func(_ context.Context, rel *plan.Release) error {
		if rel.Pkg.Name == "a" {
			panic("recorder bug")
		}
		return nil
	})}

	results := runWithin(t, e, p)

	require.Equal(t, StatusPublished, results["a"].Status, "a published package is never reported failed")
	assert.True(t, results["a"].RecordBlocked, "its consumers wait for a repaired record")
	require.NotEmpty(t, results["a"].Critical)
	assert.ErrorContains(t, results["a"].Critical[0], "internal error: recorder bug")
	assert.NotEqual(t, StatusPublished, results["b"].Status, "the consumer does not publish behind it")
	assert.Equal(t, -1, runner.indexOf("publish b"))
}

// TestExecuteContainsAPanicWhileAdmitHoldsTheLock: a panic inside admit, while
// the run's mutex is held, must not leave the mutex held, or the containment
// and every other task would wait on it for good. The inconsistent plan below
// names a failed provider the plan has no release for, which is what makes
// admit dereference nothing.
func TestExecuteContainsAPanicWhileAdmitHoldsTheLock(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Providers["a"] = []string{"ghost"}
	var stopped error
	r := &run{
		Executor: newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1}),
		plan:     p,
		results: map[string]*Result{
			"a":     {Name: "a"},
			"ghost": {Name: "ghost", Status: StatusFailed},
		},
		started:             map[string]time.Time{},
		reachedProviders:    map[string][]string{},
		avChanged:           map[string]bool{},
		reconciledProviders: map[string][]string{},
		builtPackages:       map[string]bool{},
		stop:                func(cause error) { stopped = cause },
	}

	done := make(chan struct{})
	go func() {
		r.execute(context.Background(), task{"a", taskBuild})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the task never finished: the panic left the run's lock held")
	}

	require.True(t, r.mu.TryLock(), "the run's lock was given back")
	r.mu.Unlock()
	assert.Equal(t, StatusFailed, r.results["a"].Status)
	assert.Equal(t, "build", r.results["a"].FailedStage)
	assert.ErrorContains(t, r.results["a"].Err, "internal error")
	assert.ErrorIs(t, stopped, errTaskPanicked, "the rest of the graph is stopped")
}
