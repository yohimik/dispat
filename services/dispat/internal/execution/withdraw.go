// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// A serving node's half of cancellation (CCME §28.6).
//
// The rule the whole file exists for is one sentence of §28.2: a timeout alone
// cannot free capacity for a possibly overlapping attempt. So a node does not
// simply stop when it is told to. It ends the work, waits for the processes it
// started to be gone, and only then writes the acknowledgement that says what
// the attempt had got to. The run reads that sentence and nothing else: a slot
// comes back on a result or on an acknowledgement, and on nothing that merely
// elapsed.
//
// The other half is what the acknowledgement has to carry. For a build it is a
// courtesy; for a publication it is the only evidence anybody has about an
// effect that was authorized. A node that was withdrawn before its publish
// command began leaves an outcome the run knows, and one that was withdrawn in
// the middle of it leaves an outcome nobody can establish from here, so the
// two are told apart by the node, where the difference is a fact rather than
// an inference.

import (
	"context"
	"sync"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// claimedTask is one task this node has taken on, as the poll goroutine can
// reach it.
//
// It exists because cancellation crosses the one boundary this node otherwise
// keeps: the poll goroutine owns the mailbox and the task goroutine owns the
// work, and a withdrawal arrives on the first and has to stop the second. What
// crosses is deliberately small, one cancellation and three facts about
// progress, and every one of them is behind the mutex below.
type claimedTask struct {
	assignment Assignment
	// tip is the assignment's own commit, which every reply of the attempt
	// names, and claimed is the commit this node wrote to take the work.
	tip     ChainTip
	claimed string
	// stop ends the task's context, which kills the process group the runner
	// started exactly as an interrupt does.
	stop context.CancelFunc

	// mu guards everything below it: the poll goroutine reads the progress
	// when a withdrawal arrives, and the task goroutine writes it as it goes.
	mu sync.Mutex
	// phase is the part of the frame the work is in, and expectedTip is the
	// object the attempt's next message is written on top of: the claim for a
	// task that moved its branch no further, the authorization for a
	// publication that was let through.
	phase            string
	expectedTip      string
	isCommandStarted bool
	// withdrawn is the cancel commit the run wrote, empty while none has
	// arrived. It is what the acknowledgement answers and what it is leased
	// against.
	withdrawn string
}

// registerClaim remembers one claimed task so that a withdrawal arriving on
// the poll can reach it, and answers the removal of it.
func (w *Worker) registerClaim(task *claimedTask) func() {
	w.claims.Lock()
	defer w.claims.Unlock()
	if w.claimed == nil {
		w.claimed = map[string]*claimedTask{}
	}
	w.claimed[task.tip.Branch] = task
	return func() {
		w.claims.Lock()
		defer w.claims.Unlock()
		delete(w.claimed, task.tip.Branch)
	}
}

// findClaim answers the task claimed on one branch, and nil for a branch this
// node has nothing running for.
func (w *Worker) findClaim(branch string) *claimedTask {
	w.claims.Lock()
	defer w.claims.Unlock()
	return w.claimed[branch]
}

// reportPhase records the part of the frame the work has reached, and whether
// the stage's own command sequence has begun.
//
// The command flag is sticky: once a publish command has started, the outcome
// of the publication is unknown whatever the node does afterwards, and a later
// phase must not be able to say otherwise.
func (t *claimedTask) reportPhase(phase string, isCommandStarted bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phase = phase
	t.isCommandStarted = t.isCommandStarted || isCommandStarted
}

// advanceTip records the object the attempt's next message is written on top
// of, which the publication handshake moves twice before any command runs.
func (t *claimedTask) advanceTip(oid string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expectedTip = oid
}

// withdraw records the cancellation and answers whether this is the first one:
// a second withdrawal of the same attempt is the run asking again, and the
// work is already ending.
func (t *claimedTask) withdraw(cancel string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.withdrawn != "" {
		return false
	}
	t.withdrawn = cancel
	return true
}

// readProgress answers what the acknowledgement has to state: the
// cancellation it answers, the phase the work was in, and whether the stage's
// own command had begun.
func (t *claimedTask) readProgress() (cancel, phase string, isCommandStarted bool) {
	if t == nil {
		return "", "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.withdrawn, t.phase, t.isCommandStarted
}

// readPhase answers the part of the frame the work has reached.
func (t *claimedTask) readPhase() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.phase
}

// readExpectedTip answers the object the attempt's next message follows.
func (t *claimedTask) readExpectedTip() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.expectedTip
}

// observeWithdrawal is what the poll does with a cancellation it finds.
//
// The work is ended and nothing is acknowledged here: the acknowledgement is
// owed only once the processes are gone, so it is written by the goroutine
// that was running them. A withdrawal of an assignment this node never claimed
// needs no answer at all, since the attempt is closed and this node has
// nothing running under it.
func (w *Worker) observeWithdrawal(ctx context.Context, tip ChainTip) bool {
	task := w.findClaim(tip.Branch)
	if task == nil {
		w.Log.Debug().Str("branch", tip.Branch).Str("commit", tip.OID).
			Msg("withdrawn work this node is not running")
		return false
	}
	if reason := w.checkWithdrawal(ctx, tip, task.tip, task.readExpectedTip(), task.assignment); reason != "" {
		w.reportRejection(tip, reason)
		return false
	}
	if !task.withdraw(tip.OID) {
		return false
	}
	w.Log.Info().Str("branch", tip.Branch).Str("commit", tip.OID).
		Str("run", task.assignment.Run).Str("task", task.assignment.Task).
		Int("attempt", task.assignment.Attempt).Msg("task withdrawn")
	// The runner kills the process group on cancellation and waits for it,
	// which is what makes the acknowledgement that follows a statement about a
	// machine with nothing of this attempt running on it.
	task.stop()
	return true
}

// acknowledgeCancellation writes the attempt's terminal acknowledgement, on a
// context of its own.
//
// It is detached and bounded for the reason every closing write of this engine
// is: the cancellation that ended the work may have been an interrupt, and the
// one thing the node still owes is the sentence saying what the work had got
// to. A node that let the same signal cancel that write would leave the run
// with no evidence at all, which for a publication is the difference between a
// lock released and a lock retained.
func (w *Worker) acknowledgeCancellation(ctx context.Context, task *claimedTask,
	log zerolog.Logger) {
	acked, done := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
	defer done()
	w.writeCancellationAcknowledgement(acked, task, log)
}

// writeCancellationAcknowledgement uses the caller's already bounded report
// context. A Result that lost to Cancel must settle inside its original report
// deadline rather than starting a second one.
func (w *Worker) writeCancellationAcknowledgement(ctx context.Context, task *claimedTask,
	log zerolog.Logger) {
	cancel, reached, isCommandStarted := task.readProgress()
	phase := resolveCancelledPhase(reached)
	written, err := w.advance(ctx, task.tip, cancel, MessageAck, Ack{
		Header: w.formatReplyHeader(task.assignment.Header), Assignment: task.tip.OID,
		Cancel: cancel, Phase: phase, CommandStarted: isCommandStarted,
	}, nil)
	if err != nil {
		log.Error().Err(err).Str("code", CodeTransport).Str("category", CategoryTransportCleanup).
			Msg("the cancelled task could not be acknowledged")
		return
	}
	log.Info().Str("commit", written).Str("phase", phase).
		Bool("commandStarted", isCommandStarted).Msg("cancellation acknowledged")
}

// resolveCancelledPhase is the phase a withdrawn task reports when it never
// reached one: a node cancelled while it was still assembling the checkout
// has run nothing of the frame at all.
func resolveCancelledPhase(phase string) string {
	if phase == "" {
		return release.PartInputs
	}
	return phase
}
