// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// A node serving the work addressed to it.
//
// One goroutine owns everything that is decided here: the poll, the memo of
// what each branch was last seen at, the record of what has already been
// answered, and the object store the messages are read from. That is the
// whole concurrency design of this file, and it is deliberate: the state a
// serving node keeps is small and entirely about what it has already done, so
// the cheapest way to keep two answers from disagreeing is for one goroutine
// to give both.
//
// What the loop does with a claimed assignment is not here. This build
// answers probes, which run no command and consume no capacity, and leaves
// every kind of work that does for the gate that executes it: a branch it
// does not claim is a branch that stays exactly where the orchestrator put
// it, which is what lets an older node and a newer orchestrator share a
// mailbox without either of them losing work.

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// mailboxx is what a serving node needs of its mailbox: see what has moved,
// find out what a tip carries, read it, and write the next step.
//
// Declared at the consumer, so that a fake in a test answers the four
// operations a worker actually makes rather than the whole protocol, and so
// that the loop below can be read without knowing how a branch is fetched.
type mailboxx interface {
	Observe(ctx context.Context, pattern string) ([]gitx.RemoteHead, error)
	Inspect(ctx context.Context, head gitx.RemoteHead) (ChainTip, error)
	Read(ctx context.Context, tip ChainTip, maxBytes int64) ([]byte, error)
	Advance(ctx context.Context, branch, expectedOld string, kind MessageKind, document []byte) (string, error)
	Forget()
}

// The poll rhythm. A node with nothing to do costs one ls-remote every five
// seconds; a node that just answered something looks again almost at once,
// because work arrives in bursts and the next branch of a run is usually
// already there.
const (
	minimumPollInterval = 250 * time.Millisecond
	maximumPollInterval = 5 * time.Second
)

// The reasons a worker stops, as the stopped line reports them.
const (
	// StopSignal is an operator or an orchestrating system asking the process
	// to end.
	StopSignal = "signal"
	// StopIdle is --idle-timeout elapsing with nothing claimed and nothing in
	// flight, which is how a node started for one release ends by itself.
	StopIdle = "idle"
)

// Worker is one serving node: which node it is, what it can take on, and the
// mailbox it reads its work from.
type Worker struct {
	// Node is this node's name, compared byte for byte with what an
	// assignment says it is addressed to.
	Node string
	// Endpoint is the mailbox address, carried only so that the started line
	// can name it, redacted.
	Endpoint string
	// StateDir is the folder this process owns, named in the started line so
	// that an operator can find the cache and the answered-work record.
	StateDir string
	// Report is this node's description of itself, assembled once at startup
	// and answered to every probe.
	Report NodeReport
	// IdleTimeout ends the process after this long with nothing to do. Zero
	// serves until the process is signalled, which is what a long-running
	// node does.
	IdleTimeout time.Duration
	// Mailbox is the endpoint this node serves.
	Mailbox mailboxx
	// Seen is the record of the work this node has already answered.
	Seen *SeenSet
	// PrepareStore opens the object store behind the mailbox, and is called
	// again after any failure: the store is a cache, so recovering from a
	// deleted or broken one is the same operation as creating it.
	PrepareStore func(ctx context.Context) error
	// Log is the run logger.
	Log zerolog.Logger

	// isStorePrepared is false until the store has been opened, and becomes
	// false again after any failure, which is what makes the cache
	// dispensable rather than a prerequisite.
	isStorePrepared bool
}

// Serve polls until the process is signalled or goes idle, and answers the
// reason it stopped.
//
// It answers a reason rather than an error because a serving node has no
// failure of its own to report: a poll that failed is reported and retried,
// since a mailbox briefly out of reach is the ordinary condition of a machine
// on a network, and a node that exited on it would have to be restarted by
// somebody.
func (w *Worker) Serve(ctx context.Context) string {
	w.Log.Info().Str("node", w.Node).Str("endpoint", gitx.RedactURL(w.Endpoint)).
		Int("concurrency", w.Report.Capacity).Str("stateDir", w.StateDir).
		Msg("worker started")
	reason := w.poll(ctx)
	w.Log.Info().Str("node", w.Node).Str("reason", reason).Msg("worker stopped")
	return reason
}

// poll is the loop itself: one tick, then a wait whose length is what the
// tick found.
//
// Both timers are created once and stopped on every exit path, and the idle
// one is restarted whenever anything was answered, so "idle" means what it
// says: nothing arrived for the whole timeout, rather than nothing arrived
// since the process started.
func (w *Worker) poll(ctx context.Context) string {
	interval := minimumPollInterval
	next := time.NewTimer(interval)
	defer next.Stop()
	idle := time.NewTimer(w.IdleTimeout)
	defer idle.Stop()
	idleC := idle.C
	if w.IdleTimeout == 0 {
		// A node with no idle timeout waits on a channel nothing ever sends
		// on, which is how the same select serves both shapes.
		idleC = nil
	}
	for {
		isProgress := w.tick(ctx)
		interval = resolvePollInterval(interval, isProgress)
		if isProgress && idleC != nil {
			idle.Reset(w.IdleTimeout)
		}
		next.Reset(interval)
		select {
		case <-ctx.Done():
			return StopSignal
		case <-idleC:
			return StopIdle
		case <-next.C:
			w.Log.Trace().Str("node", w.Node).Dur("interval", interval).Msg("polling the mailbox")
		}
	}
}

// resolvePollInterval is the back-off: straight back to the shortest wait
// after anything was answered, and doubling up to the longest otherwise.
func resolvePollInterval(current time.Duration, isProgress bool) time.Duration {
	if isProgress {
		return minimumPollInterval
	}
	return min(current*2, maximumPollInterval)
}

// tick is one poll of the mailbox, and reports whether anything was answered.
//
// A failure is logged and swallowed here on purpose: the store is marked
// unprepared, so the next tick rebuilds it and forgets what the memo
// remembered, and a node whose cache was deleted or whose git failed once
// carries on serving instead of exiting.
func (w *Worker) tick(ctx context.Context) bool {
	isProgress, err := w.inspectMailbox(ctx)
	if err == nil {
		return isProgress
	}
	if ctx.Err() != nil {
		// The process is stopping; the failed call is the cancellation
		// reaching git, not a fault of the node.
		return isProgress
	}
	w.isStorePrepared = false
	w.Log.Error().Err(err).Str("node", w.Node).Msg("the mailbox could not be served")
	return isProgress
}

// inspectMailbox opens the store when it has to, polls once, and handles
// every branch whose tip moved.
func (w *Worker) inspectMailbox(ctx context.Context) (bool, error) {
	if !w.isStorePrepared {
		if err := w.PrepareStore(ctx); err != nil {
			return false, err
		}
		// The memo describes objects the previous store held, and a rebuilt
		// store holds none of them.
		w.Mailbox.Forget()
		w.isStorePrepared = true
	}
	heads, err := w.Mailbox.Observe(ctx, FormatBranchPattern(w.Node))
	if err != nil {
		return false, err
	}
	isProgress := false
	for _, head := range heads {
		answered, err := w.handle(ctx, head)
		isProgress = isProgress || answered
		if err != nil {
			return isProgress, err
		}
	}
	return isProgress, nil
}

// handle decides what one moved branch is, and answers it when it is work
// this node may take.
//
// The order is the order of cost and of trust: the chain shape is free, the
// document is read under a ceiling and verified before it is parsed, and only
// then is anything it says believed. Every refusal is one warning naming the
// reason and never the contents, because a rejected message is exactly the
// input nobody has authenticated.
func (w *Worker) handle(ctx context.Context, head gitx.RemoteHead) (bool, error) {
	w.Log.Trace().Str("node", w.Node).Str("branch", head.Name).Str("commit", head.OID).
		Msg("coordination branch inspected")
	tip, err := w.Mailbox.Inspect(ctx, head)
	if err != nil {
		return false, err
	}
	if ResolveWorkerAction(tip) != ActionClaim {
		w.reportIgnoredTip(tip)
		return false, nil
	}
	document, err := w.Mailbox.Read(ctx, tip, w.Report.Limits.MaxManifestBytes)
	if err != nil {
		return false, w.reportUnusable(tip, err)
	}
	var assignment Assignment
	if err := json.Unmarshal(document, &assignment); err != nil {
		w.reportRejection(tip, ReasonUnreadable)
		return false, nil
	}
	if reason := w.checkAssignment(assignment, tip); reason != "" {
		w.reportRejection(tip, reason)
		return false, nil
	}
	if assignment.Kind != KindProbe {
		// The kinds that run commands belong to the gate that executes them.
		// Leaving the branch untouched is what keeps the work queued for a
		// node that can do it rather than consuming it here.
		w.Log.Debug().Str("node", w.Node).Str("branch", tip.Branch).Str("kind", assignment.Kind).
			Str("run", assignment.Run).Str("task", assignment.Task).Int("attempt", assignment.Attempt).
			Msg("assignment inspected")
		return false, nil
	}
	return true, w.answerProbe(ctx, tip, assignment)
}

// checkAssignment applies the acceptance rules to one assignment: the ones
// every message is held to, and the one that is this node's own memory.
func (w *Worker) checkAssignment(assignment Assignment, tip ChainTip) RejectReason {
	if reason := CheckHeader(assignment.Header,
		Binding{Node: w.Node, Branch: tip.Branch}, time.Now()); reason != "" {
		return reason
	}
	if w.Seen.IsSeen(assignment.Run, assignment.Task, assignment.Attempt) {
		return ReasonReplay
	}
	return ""
}

// answerProbe claims the branch and reports what this node is.
//
// A probe runs no command, so it is answered inline and takes no capacity
// slot: it is a question about the node rather than work for it, and a node
// whose capacity was full would otherwise be unable to say so.
func (w *Worker) answerProbe(ctx context.Context, tip ChainTip, assignment Assignment) error {
	claimed, err := w.advance(ctx, tip, tip.OID, MessageClaim, Claim{
		Header: w.formatReplyHeader(assignment.Header), Assignment: tip.OID,
	})
	if err != nil {
		return err
	}
	w.Log.Info().Str("node", w.Node).Str("branch", tip.Branch).Str("commit", claimed).
		Str("run", assignment.Run).Str("task", assignment.Task).Int("attempt", assignment.Attempt).
		Str("kind", assignment.Kind).Msg("task claimed")
	// Remembered before the answer is written: the record says what this node
	// has taken on, so an attempt that was claimed and then failed to report
	// must not be taken on again.
	if err := w.Seen.Record(assignment.Run, assignment.Task, assignment.Attempt, time.Now()); err != nil {
		return err
	}
	report := w.Report
	reported, err := w.advance(ctx, tip, claimed, MessageResult, Result{
		Header:     w.formatReplyHeader(assignment.Header),
		Assignment: tip.OID,
		Status:     StatusSucceeded,
		Platform:   Platform{OS: report.OS, Arch: report.Arch, Dispat: report.Dispat},
		Report:     &report,
	})
	if err != nil {
		return err
	}
	w.Log.Info().Str("node", w.Node).Str("branch", tip.Branch).Str("commit", reported).
		Str("run", assignment.Run).Str("task", assignment.Task).Int("attempt", assignment.Attempt).
		Str("status", StatusSucceeded).Msg("task finished")
	return nil
}

// advance writes one reply onto the branch, under a lease over the value this
// node read.
func (w *Worker) advance(ctx context.Context, tip ChainTip, expectedOld string, kind MessageKind, message any) (string, error) {
	document, err := json.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("execution: writing the %s document: %w", kind, err)
	}
	return w.Mailbox.Advance(ctx, tip.Branch, expectedOld, kind, document)
}

// formatReplyHeader is the header of everything this node writes back: the
// work's own identity as the assignment stated it, this node's protocol
// version, and this moment.
func (w *Worker) formatReplyHeader(assignment Header) Header {
	return Header{
		Protocol:   ProtocolVersion,
		Kind:       assignment.Kind,
		Run:        assignment.Run,
		PlanDigest: assignment.PlanDigest,
		Task:       assignment.Task,
		Attempt:    assignment.Attempt,
		Generation: assignment.Generation,
		Node:       w.Node,
		Branch:     assignment.Branch,
		IssuedAt:   time.Now().UTC().Format(time.RFC3339),
	}
}

// reportUnusable turns a failed read into either a refusal this node reports
// and carries on from, or a transport failure the loop recovers from.
func (w *Worker) reportUnusable(tip ChainTip, err error) error {
	reason := RejectionReason(err)
	if reason == "" {
		return err
	}
	w.reportRejection(tip, reason)
	return nil
}

// reportIgnoredTip says why a branch was left alone.
//
// Three cases, and they are told apart because they mean three different
// things to whoever is reading the log. A tip carrying no message at all, and
// an assignment written onto a chain no legal sequence of pushes could have
// produced, are both somebody writing into this node's mailbox and are
// warnings. Everything else is the protocol working: a branch this node
// already answered, or a step it does not write.
func (w *Worker) reportIgnoredTip(tip ChainTip) {
	if tip.Kind == "" {
		w.reportRejection(tip, ReasonUnreadable)
		return
	}
	if tip.Kind == MessageAssignment {
		w.reportRejection(tip, ReasonChain)
		return
	}
	w.Log.Debug().Str("node", w.Node).Str("branch", tip.Branch).Str("commit", tip.OID).
		Str("message", string(tip.Kind)).Msg("assignment inspected")
}

// reportRejection writes the one line a refused message produces: which
// branch, which commit, and why. Never what it said.
func (w *Worker) reportRejection(tip ChainTip, reason RejectReason) {
	w.Log.Warn().Str("node", w.Node).Str("branch", tip.Branch).Str("commit", tip.OID).
		Str("reason", string(reason)).Str("code", CodeAuthority).Str("category", CategoryAuthority).
		Msg("assignment rejected")
}

// FormatNodeReport is a node's description of itself, assembled once at
// startup from the process rather than from the configuration alone: the
// platform is what this binary runs on, and the capacity and the ceilings are
// what its configuration allows.
func FormatNodeReport(version, gitVersion string, capacity int, limits TransferLimits) NodeReport {
	return NodeReport{
		Protocol:   ProtocolVersion,
		Dispat:     version,
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		Capacity:   capacity,
		GitVersion: gitVersion,
		Limits:     limits,
	}
}
