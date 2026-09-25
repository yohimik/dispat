// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// A task that panics.
//
// Every task of a release runs in a goroutine of its own, and a panic that
// leaves a goroutine ends the whole process: whatever else was in flight, the
// records of what already published and the release lock the run holds all go
// with it, and the next run meets a lock nobody will ever give back. So a
// panic is contained where the task starts, turned into that task's outcome,
// and the run is stopped the way an interrupt stops it, with one difference:
// the run itself is not interrupted. Its closing phase, its records and its
// unlock still run, and it ends as a failed run.
//
// A published package is never reported failed, a panic included (§17): once
// the publish frame returned success, the package is published and what the
// panic cost is its record, which is reported as a critical and blocks its
// consumers until it is repaired.

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// errTaskPanicked is the cause the tasks' context carries once a task has
// panicked: every task in flight ends cancelled and no new one starts.
var errTaskPanicked = errors.New("release: a task stopped with an internal error")

// errLoginIncomplete is what a space login that never returned leaves behind
// for the publishes waiting on it.
var errLoginIncomplete = errors.New("release: the space login did not complete")

// panicRevertTimeout bounds the revert of a package whose task panicked. The
// revert runs detached from the tasks' cancellation, which the panic itself
// caused, so it needs a bound of its own.
const panicRevertTimeout = time.Minute

// panicOutcome is what a contained panic made of its package.
type panicOutcome uint8

const (
	// panicSettled is a panic in a package whose outcome was already
	// decided: it keeps that outcome and gains a critical.
	panicSettled panicOutcome = iota
	// panicFailed is a panic before the publish frame returned success: the
	// package failed at the stage the task was running.
	panicFailed
	// panicPublished is a panic after the publish frame returned success and
	// before the package's published status was recorded.
	panicPublished
)

// containPanic turns a panic in the task into the task's outcome. It is the
// first deferred call of execute, so it runs after every other defer of the
// task has given back what it held.
//
// It runs none of the operator's outcome scripts (no onFail, no onSkip): the
// state a panic leaves is not one a script was written for. It reverts only
// what the ordinary rules revert, on a bounded context detached from the
// cancellation it causes, and it stops the rest of the graph.
func (tc *taskCtx) containPanic(ctx context.Context, res *Result) {
	value := recover()
	if value == nil {
		return
	}
	err := fmt.Errorf("internal error: %v", value)
	tc.log.Error().Err(err).Msg(tc.t.kind.String() + " stopped by an internal error")
	tc.log.Debug().Str("stack", string(debug.Stack())).Msg("internal error stack")
	tc.stop(errTaskPanicked)
	switch tc.settlePanic(res, err) {
	case panicPublished:
		ev := packageEvent(tc.t.pkg, tc.rel, EventPackagePublished)
		ev.Status, ev.Tag, ev.Worker = StatusPublished.String(), tc.rel.TagName(), res.Worker
		tc.notify(ev)
	case panicFailed:
		ev := packageEvent(tc.t.pkg, tc.rel, EventPackageFailed)
		ev.Status, ev.FailedStage, ev.Error = StatusFailed.String(), tc.t.kind.String(), err.Error()
		ev.Worker = res.Worker
		tc.notify(ev)
		if tc.rel.Pkg.Space.RevertOnFail {
			revertCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), panicRevertTimeout)
			defer cancel()
			tc.revert(revertCtx, tc.rel, tc.log)
		}
	}
}

// settlePanic records, under mu, what a panic made of the package.
func (tc *taskCtx) settlePanic(res *Result, err error) panicOutcome {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if started, ok := tc.started[tc.t.pkg]; ok {
		res.Duration = time.Since(started)
	}
	if tc.isPublished {
		code := plan.CodeRecordFailed
		if tc.BlockOnRecordFailure {
			code = "E335"
		}
		res.Critical = append(res.Critical, fmt.Errorf("%s: recording stopped by an internal error: %w", code, err))
		res.RecordBlocked = true
		if res.Status == StatusPublished {
			return panicSettled
		}
		res.Status = StatusPublished
		return panicPublished
	}
	if res.Status != StatusPending {
		res.Critical = append(res.Critical, fmt.Errorf("%s stopped by an internal error: %w", tc.t.kind, err))
		return panicSettled
	}
	res.Status = StatusFailed
	res.FailedStage = tc.t.kind.String()
	res.Err = err
	return panicFailed
}
