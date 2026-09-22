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
// A claimed task is the one thing that leaves that goroutine. It runs under a
// WaitGroup, holds one of this node's capacity slots until it has reported,
// and shares the mailbox rather than owning one, which is why the mailbox
// carries a lock of its own. What it does with the frame it was given is in
// task.go.
//
// A kind this build does not execute is left exactly where the orchestrator
// put it: a branch this node does not claim stays queued for a node that can
// do the work, which is what lets an older node and a newer orchestrator share
// a mailbox without either of them losing anything.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
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
	Advance(ctx context.Context, branch, expectedOld string, kind MessageKind, document []byte,
		carried []gitx.TreeEntry) (string, error)
	Reread(ctx context.Context, branch string) (gitx.RemoteHead, error)
	Fetch(ctx context.Context, branches []string) error
	Reconsider(branch string)
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
	// TransferTimeout bounds the one push that carries a task's build outputs
	// to the mailbox, the configured `transfer.timeout`. A result that carries
	// nothing is bounded by taskReportTimeout instead, because writing one
	// small document should never take that long.
	TransferTimeout time.Duration
	// Mailbox is the endpoint this node serves.
	Mailbox mailboxx
	// Cache is the object store the task checkouts are materialized from: the
	// same store the mailbox fetches into, because a worktree can only be made
	// of objects the repository holds.
	Cache *gitx.LocalGitx
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
	// slots is this node's capacity, one entry per task it may run at once.
	// It is filled lazily by the poll goroutine, which is the only one that
	// takes a slot; the task goroutines give theirs back.
	slots chan struct{}
	// running is the tasks in flight, so that a node asked to stop stops when
	// they have reported rather than while they are running.
	running sync.WaitGroup
	// activity guards the two fields below it: when this node last had
	// something to do, and how much of it is still going on. Both are written
	// by the poll goroutine and by every task goroutine as it ends, which is
	// why they are behind a mutex rather than on the loop's own stack.
	activity   sync.Mutex
	lastActive time.Time
	inFlight   int
	// claims are the tasks in flight by coordination branch, so that a
	// withdrawal arriving on the poll can reach the work it is about. One
	// owner for the map, and one mutex for the small amount of progress each
	// entry carries across that boundary.
	claims  sync.Mutex
	claimed map[string]*claimedTask
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
	w.slots = make(chan struct{}, max(w.Report.Capacity, 1))
	// Whatever a killed process left behind: an attempt's checkout, the
	// folder a transfer was being assembled in, the copy of a root that was
	// moved aside. Every one of them lives inside a folder this node owns and
	// nothing of this process's is in it yet, so the sweep is the removal of
	// that folder.
	w.clearTaskDir(filepath.Join(w.StateDir, taskWorkDir), w.Log)
	w.Log.Info().Str("endpoint", gitx.RedactURL(w.Endpoint)).
		Int("concurrency", w.Report.Capacity).Str("stateDir", w.StateDir).
		Msg("worker started")
	reason := w.poll(ctx)
	// A node that stopped claiming still owes a report for everything it took
	// on: the commands are already dead when the stop reached them, and the
	// run that dispatched them is waiting to hear so.
	w.running.Wait()
	w.Log.Info().Str("reason", reason).Msg("worker stopped")
	return reason
}

// poll is the loop itself: one tick, then a wait whose length is what the
// tick found.
//
// Both timers are created once and stopped on every exit path. The idle one
// fires on schedule and is then asked whether the node is actually idle, which
// is what makes the word mean what it says. Three things are not idleness and
// each of them used to look like it: a node that has just started, a node with
// a task in flight, and a node whose task outlived the timeout and has only
// just ended. The clock therefore runs from the last moment this node had
// something to do (its own start, a claim, a task ending, a probe answered)
// rather than from whenever a timer was last reset, and a node with work in
// flight is never idle at all.
func (w *Worker) poll(ctx context.Context) string {
	interval := minimumPollInterval
	next := time.NewTimer(interval)
	defer next.Stop()
	// The clock starts here: a node that has just started has had nothing to
	// do for no time at all.
	w.markActive()
	idle := time.NewTimer(w.IdleTimeout)
	defer idle.Stop()
	idleC := idle.C
	if w.IdleTimeout == 0 {
		// A node with no idle timeout waits on a channel nothing ever sends
		// on, which is how the same select serves both shapes.
		idleC = nil
	}
	for {
		interval = resolvePollInterval(interval, w.tick(ctx))
		next.Reset(interval)
		for isWaiting := true; isWaiting; {
			select {
			case <-ctx.Done():
				return StopSignal
			case <-idleC:
				remaining := w.resolveIdleRemainder()
				if remaining <= 0 {
					return StopIdle
				}
				// Something happened after the timer was armed, so the node is
				// not idle yet and the wait continues for what is left of it.
				idle.Reset(remaining)
			case <-next.C:
				w.Log.Trace().Dur("interval", interval).Msg("polling the mailbox")
				isWaiting = false
			}
		}
	}
}

// markActive records that this node had something to do just now, which is
// what the idle clock counts from.
func (w *Worker) markActive() {
	w.activity.Lock()
	defer w.activity.Unlock()
	w.lastActive = time.Now()
}

// beginTask and endTask keep the count of the work in flight, and mark the
// moments a task starts and stops as activity.
//
// The end is marked as well as the start because a task that outlived the idle
// timeout leaves a node that has just finished something, not one that has
// been doing nothing: a node that stopped five seconds after a long build
// reported would be a node that cannot be given the next task of the same run.
func (w *Worker) beginTask() {
	w.activity.Lock()
	defer w.activity.Unlock()
	w.inFlight++
	w.lastActive = time.Now()
}

func (w *Worker) endTask() {
	w.activity.Lock()
	defer w.activity.Unlock()
	w.inFlight--
	w.lastActive = time.Now()
}

// resolveIdleRemainder is how much longer this node has to have nothing to do
// before it may stop, and zero or less for a node that may stop now.
//
// A node with a task in flight answers the whole timeout rather than zero: it
// is not idle, and the next answer will be computed from the moment that task
// ends.
func (w *Worker) resolveIdleRemainder() time.Duration {
	w.activity.Lock()
	defer w.activity.Unlock()
	if w.inFlight > 0 {
		return w.IdleTimeout
	}
	return w.IdleTimeout - time.Since(w.lastActive)
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
	if isProgress {
		// Anything answered is this node having had something to do, which is
		// what the idle clock counts from. A probe reaches this and nothing
		// else, since a claimed task marks its own start and end.
		w.markActive()
	}
	if err == nil {
		return isProgress
	}
	if ctx.Err() != nil {
		// The process is stopping; the failed call is the cancellation
		// reaching git, not a fault of the node.
		return isProgress
	}
	w.isStorePrepared = false
	w.Log.Error().Err(err).Msg("the mailbox could not be served")
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
	w.Log.Trace().Str("branch", head.Name).Str("commit", head.OID).
		Msg("coordination branch inspected")
	if !IsBranchCarryingWork(head.Name) {
		// A prepared input state and a relayed result both appear in the
		// namespace addressed to this node, and neither is work: the first is a
		// repository tree and the second is another attempt's report. Reading
		// them would find no message of this protocol and report a branch this
		// run created as an assignment nobody could read, which is a warning an
		// operator cannot act on.
		w.Log.Trace().Str("branch", head.Name).Str("commit", head.OID).
			Str("kind", ResolveBranchKindHint(head.Name)).Msg("transport branch skipped")
		return false, nil
	}
	tip, err := w.Mailbox.Inspect(ctx, head)
	if err != nil {
		// The branch's objects are here and this process could not make
		// anything of them, so the work on it is still work: the memo would
		// otherwise make one local failure mean "already dealt with" for a
		// tip that never moves again, and the run would wait out the task
		// deadline for an assignment sitting in the mailbox.
		w.Mailbox.Reconsider(head.Name)
		return false, err
	}
	if tip.Kind == MessageCancel {
		// A withdrawal is the one message of another party this node acts on
		// outside a task's own wait: the work it names is running in a
		// goroutine of this process, and the poll is where the sentence
		// arrives.
		return w.observeWithdrawal(ctx, tip), nil
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
	if assignment.Kind == KindProbe {
		return true, w.answerProbe(ctx, tip, assignment)
	}
	// A preparation is a build frame and is executed as one: the kind says why
	// the run asked for it, not what the node does with it. A publication is
	// the same frame machinery with one step inserted in the middle, which is
	// where its own file picks it up.
	if assignment.Kind == KindBuild || assignment.Kind == KindPrepare || assignment.Kind == KindPublish {
		return w.takeTask(ctx, tip, assignment)
	}
	// The kinds that are not executed by this build belong to the gate that
	// executes them. Leaving the branch untouched is what keeps the work
	// queued for a node that can do it rather than consuming it here.
	w.Log.Debug().Str("branch", tip.Branch).Str("kind", assignment.Kind).
		Str("run", assignment.Run).Str("task", assignment.Task).Int("attempt", assignment.Attempt).
		Msg("assignment inspected")
	return false, nil
}

// takeTask claims one assignment and starts it, and leaves it alone when this
// node has no free slot for it.
//
// The claim is what says the work is this node's, so it is pushed before the
// task starts and by the goroutine that owns the poll: an assignment nobody
// claimed is an assignment another node may still take, which is what makes a
// full node queue work rather than lose it.
func (w *Worker) takeTask(ctx context.Context, tip ChainTip, assignment Assignment) (bool, error) {
	select {
	case w.slots <- struct{}{}:
	default:
		// Left exactly where the orchestrator put it, and forgotten by the
		// memo, so that the next poll offers it to this node again: nobody
		// else is going to move the branch on this node's behalf.
		w.Mailbox.Reconsider(tip.Branch)
		w.Log.Debug().Str("branch", tip.Branch).Str("run", assignment.Run).
			Str("task", assignment.Task).Msg("assignment left queued: this node is full")
		return false, nil
	}
	claimed, err := w.advance(ctx, tip, tip.OID, MessageClaim, Claim{
		Header: w.formatReplyHeader(assignment.Header), Assignment: tip.OID,
	}, nil)
	if err != nil {
		<-w.slots
		return false, err
	}
	w.Log.Info().Str("branch", tip.Branch).Str("commit", claimed).
		Str("run", assignment.Run).Str("task", assignment.Task).Int("attempt", assignment.Attempt).
		Str("kind", assignment.Kind).Msg("task claimed")
	// Remembered before the work starts: an attempt this node took on and then
	// failed to report must not be taken on again.
	if err := w.Seen.Record(assignment.Run, assignment.Task, assignment.Attempt, time.Now()); err != nil {
		<-w.slots
		return false, err
	}
	// The task's own context is created here rather than inside the goroutine
	// because the party that cancels it is this one: a withdrawal arrives on
	// the poll, and the cancellation has to be reachable from it.
	bounded, stop := resolveTaskDeadline(ctx, assignment.DeadlineSeconds)
	task := &claimedTask{assignment: assignment, tip: tip, claimed: claimed,
		stop: stop, expectedTip: claimed}
	forget := w.registerClaim(task)
	w.running.Add(1)
	w.beginTask()
	go func() {
		defer w.running.Done()
		defer w.endTask()
		defer func() { <-w.slots }()
		defer forget()
		defer stop()
		w.answerTask(ctx, bounded, task)
	}()
	return true, nil
}

// answerTask runs one claimed assignment and reports what became of it.
//
// The report goes out on a context detached from the run's, bounded by its own
// deadline: a node asked to stop has already had its commands killed by that
// same cancellation, and the one thing it still owes is the sentence saying so.
func (w *Worker) answerTask(ctx context.Context, bounded context.Context, task *claimedTask) {
	assignment, tip := task.assignment, task.tip
	log := w.Log.With().Str("run", assignment.Run).
		Str("task", assignment.Task).Int("attempt", assignment.Attempt).
		Str("branch", tip.Branch).Logger()
	outcome := w.runTask(bounded, task, log)
	if cancel, _, _ := task.readProgress(); cancel != "" {
		// The run withdrew the attempt and is waiting to hear that nothing of
		// it is running any more. The commands are gone by now, because the
		// runner kills the process group and waits for it, so the
		// acknowledgement is a statement about this machine rather than a
		// promise: it is the attempt's terminal message, and no result follows
		// it.
		w.acknowledgeCancellation(ctx, task, log)
		return
	}
	if outcome.isAnswered {
		// The attempt is already terminal on the branch: a withdrawal this node
		// acknowledged is the answer, and a result written on top of it would be
		// a second answer to a question both parties have settled.
		log.Info().Str("status", outcome.status).Msg("task finished")
		return
	}
	if ctx.Err() != nil && outcome.status != StatusSucceeded {
		// The commands died of the stop rather than of anything about the
		// package, which is a different thing for the run to hear.
		outcome.status, outcome.failedPart = StatusCancelled, ""
	} else if bounded.Err() != nil && outcome.status != StatusSucceeded {
		// The assignment's own deadline, enforced by this node on its own
		// clock: the work stopped because of the bound the run stated and not
		// because of anything about the package (§28.6).
		outcome.failedPart = release.PartDeadline
		log.Warn().Str("code", CodeIntegrity).Str("category", CategoryIntegrity).
			Msg("the task reached the deadline its assignment stated and was ended here")
	}
	report := Result{
		Header:      w.formatReplyHeader(assignment.Header),
		Assignment:  tip.OID,
		Status:      outcome.status,
		FailedPart:  outcome.failedPart,
		Reason:      string(outcome.reason),
		Platform:    Platform{OS: w.Report.OS, Arch: w.Report.Arch, Dispat: w.Report.Dispat},
		Exports:     formatExports(outcome.exports),
		Outputs:     outcome.outputs,
		StrayWrites: outcome.strayWrites,
	}
	carried := carriedOutputs(outcome.outputs)
	reportCtx, done := context.WithTimeout(context.WithoutCancel(ctx),
		w.resolveReportTimeout(len(carried) > 0))
	defer done()
	reported, err := w.advance(reportCtx, tip, resolveResultLease(task.claimed, outcome),
		MessageResult, report, carried)
	if err != nil {
		log.Error().Err(err).Str("code", CodeTransport).Str("category", CategoryTransportCleanup).
			Msg("the task result could not be reported")
		return
	}
	log.Info().Str("commit", reported).Str("status", outcome.status).
		Int("strayWrites", outcome.strayWrites).Msg("task finished")
}

// resolveReportTimeout is how long a finished task may take to report. A
// result that carries build outputs is a push of those outputs, which is as
// large as the build made it and travels under the transfer timeout the
// operator configured for exactly that; a result carrying nothing is one
// document and gets the short bound, so a node asked to stop is not held for
// the transfer window by a report that has nothing to transfer.
func (w *Worker) resolveReportTimeout(isCarryingOutputs bool) time.Duration {
	if isCarryingOutputs && w.TransferTimeout > taskReportTimeout {
		return w.TransferTimeout
	}
	return taskReportTimeout
}

// resolveResultLease is the object a result is written on top of: the claim
// for a task that moved its branch no further, and whatever the publication
// handshake left there for one that did. A lease over the wrong object is a
// push the remote refuses, which would turn a finished task into one that
// never reported.
func resolveResultLease(claimed string, outcome taskOutcome) string {
	if outcome.expectedTip == "" {
		return claimed
	}
	return outcome.expectedTip
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
	if assignment.Kind == KindPublish && !assignment.Permits.Publish {
		return ReasonPermit
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
	}, nil)
	if err != nil {
		return err
	}
	w.Log.Info().Str("branch", tip.Branch).Str("commit", claimed).
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
	}, nil)
	if err != nil {
		return err
	}
	w.Log.Info().Str("branch", tip.Branch).Str("commit", reported).
		Str("run", assignment.Run).Str("task", assignment.Task).Int("attempt", assignment.Attempt).
		Str("status", StatusSucceeded).Msg("task finished")
	return nil
}

// advance writes one reply onto the branch, under a lease over the value this
// node read.
func (w *Worker) advance(ctx context.Context, tip ChainTip, expectedOld string, kind MessageKind,
	message any, carried []gitx.TreeEntry) (string, error) {
	document, err := json.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("execution: writing the %s document: %w", kind, err)
	}
	return w.Mailbox.Advance(ctx, tip.Branch, expectedOld, kind, document, carried)
}

// carriedOutputs is what a result carries beside itself: the tree of the
// files it describes, so that the one push that reports the task also sends
// the bytes of it. A task that produced nothing carries nothing.
func carriedOutputs(manifest *OutputManifest) []gitx.TreeEntry {
	if manifest == nil {
		return nil
	}
	return []gitx.TreeEntry{
		{Mode: gitx.TreeModeDir, Type: "tree", OID: manifest.OutputTree, Name: outputsDir},
	}
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
		// Not a message this protocol refuses but a read that failed, so the
		// branch is still unread and the next poll has to offer it again.
		w.Mailbox.Reconsider(tip.Branch)
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
	if tip.Kind == "" && !tip.isProtocol {
		// A commit carrying nothing of this protocol at all is a prepared
		// input state, which is what the source of every dispatched task sits
		// on and is an ordinary thing to find in a mailbox.
		w.Log.Debug().Str("branch", tip.Branch).Str("commit", tip.OID).
			Msg("input state inspected")
		return
	}
	if tip.Kind == "" {
		w.reportRejection(tip, ReasonUnreadable)
		return
	}
	if tip.Kind == MessageAssignment {
		w.reportRejection(tip, ReasonChain)
		return
	}
	w.Log.Debug().Str("branch", tip.Branch).Str("commit", tip.OID).
		Str("message", string(tip.Kind)).Msg("assignment inspected")
}

// reportRejection writes the one line a refused message produces: which
// branch, which commit, and why. Never what it said.
func (w *Worker) reportRejection(tip ChainTip, reason RejectReason) {
	w.Log.Warn().Str("branch", tip.Branch).Str("commit", tip.OID).
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
