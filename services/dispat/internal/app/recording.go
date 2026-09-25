// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// The lifetime of the durable records a run writes once its packages have
// published.
//
// A release commit, its tags and the push are how a completed leg commits
// (§17): a package that published and was never recorded is released again by
// the next run. So the recording cannot share the run's own lifetime, which
// ends the moment an operator interrupts it. It cannot be unbounded either,
// because an interrupted run is one somebody wants to end, and a remote that
// hangs would then keep the process and its locks alive for as long as it
// liked. The answer is a lifetime of its own: detached from the run's
// cancellation, and given a fixed grace from the moment the run is cancelled.
// An uninterrupted run is not bounded at all by it, because a large release
// commit or a slow push is still the work the run exists to finish.
//
// The operator's scripts are the other half. A hook observes the release
// rather than being part of it, so it lives as long as the run does: one that
// is running when the interrupt arrives is stopped, and none starts
// afterwards, while the commit, the tags and the push it brackets go on.

import (
	"context"
	"errors"
	"sync"
	"time"
)

// recordingGrace is how long the durable records of a run may still take
// once the run is cancelled. It is a variable so that a test can shrink it.
var recordingGrace = 5 * time.Minute

// errRecordingGraceElapsed is the cause a recording context carries when the
// grace after an interrupt ran out before the records were written.
var errRecordingGraceElapsed = errors.New("recording completed releases timed out after interruption")

// detachRecording returns a context the run's cancellation never cancels and
// that is given recordingGrace from the moment the run is cancelled, so an
// uninterrupted run is not bounded (large finalize batches keep working).
//
// The returned cancel ends the recording: it stops the watch and the grace
// timer and cancels the context with no cause of its own, so a recording that
// finished in time never reads as one that timed out.
func detachRecording(run context.Context) (context.Context, context.CancelFunc) {
	recording, cancel := context.WithCancelCause(context.WithoutCancel(run))
	grace := &recordingDeadline{cancel: cancel}
	stopWatching := context.AfterFunc(run, grace.start)
	return recording, func() {
		stopWatching()
		grace.stop()
		cancel(nil)
	}
}

// recordingDeadline is the grace timer of one recording. It is started by the
// run's cancellation and stopped by the recording's own end, which can race
// each other, so both happen under one mutex and a stopped deadline never
// starts.
type recordingDeadline struct {
	mu        sync.Mutex
	cancel    context.CancelCauseFunc
	timer     *time.Timer
	isStopped bool
}

// start gives the recording its grace, once the run has been cancelled.
func (d *recordingDeadline) start() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.isStopped {
		return
	}
	d.timer = time.AfterFunc(recordingGrace, func() { d.cancel(errRecordingGraceElapsed) })
}

// stop ends the grace, whether or not it was ever started.
func (d *recordingDeadline) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.isStopped = true
	if d.timer != nil {
		d.timer.Stop()
	}
}

// recordHooks carries the live run lifetime into user hooks while native
// release recording proceeds on its separate durable context. Checking the
// observer directly before every hook closes the small scheduling window in
// which context.AfterFunc has observed cancellation but has not run yet.
type recordHooks struct {
	ctx      context.Context
	observer context.Context
}

// newRecordHooks derives the hooks' lifetime from the recording's, ended as
// well when the observed run is cancelled, and returns the function that
// releases the watch. A hook that is running when the run is cancelled is
// stopped by it, while the record it brackets continues on recordCtx.
func newRecordHooks(recordCtx, observerCtx context.Context) (recordHooks, func()) {
	hookCtx, cancelHooks := context.WithCancel(recordCtx)
	stopHooks := context.AfterFunc(observerCtx, cancelHooks)
	return recordHooks{ctx: hookCtx, observer: observerCtx}, func() {
		stopHooks()
		cancelHooks()
	}
}

// run executes one warn-only hook while the observed run is live, and skips
// it with a debug line once the run has been cancelled.
func (h recordHooks) run(hooks *runHooks, name string, refs []string) {
	if h.ctx.Err() != nil || h.observer.Err() != nil {
		if len(refs) > 0 {
			hooks.log.Debug().Str("hook", name).
				Msg("cancelled: skipping record hook while the native record completes")
		}
		return
	}
	hooks.run(h.ctx, name, refs)
}
