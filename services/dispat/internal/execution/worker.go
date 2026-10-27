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
// guards its own memo and never holds it across a transfer. What it does with the frame it was given is in
// task.go.
//
// A kind this build does not execute is left exactly where the orchestrator
// put it: a branch this node does not claim stays queued for a node that can
// do the work, which is what lets an older node and a newer orchestrator share
// a mailbox without either of them losing anything.

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	public "github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// mailboxx is the read-only Git view a serving node needs: see what moved,
// inspect a tip and fetch exact carried inputs. Replies use the authenticated
// first-node return channel, never a peer Git push.
//
// Declared at the consumer, so that a fake in a test answers the four
// operations a worker actually makes rather than the whole protocol, and so
// that the loop below can be read without knowing how a branch is fetched.
type mailboxx interface {
	Observe(ctx context.Context, pattern string, isWanted func(string) bool) ([]gitx.RemoteHead, error)
	ObserveManyExact(ctx context.Context, branches []string) ([]gitx.RemoteHead, error)
	Inspect(ctx context.Context, head gitx.RemoteHead) (ChainTip, error)
	Read(ctx context.Context, tip ChainTip, maxBytes int64) ([]byte, error)
	Reread(ctx context.Context, branch string) (gitx.RemoteHead, error)
	Fetch(ctx context.Context, branches []string) error
	Reconsider(branch string)
	Forget()
}

type slotMailboxx interface {
	InspectChain(context.Context, gitx.RemoteHead) ([]ChainTip, error)
	VerifyAssignmentCarriers(context.Context, string, int64) (*Assignment, error)
	OpenCarriedAssignment(context.Context, string, int64) (*VerifiedCarriers, error)
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
	// StopDisowned is another process's id found in this node's worker.lock
	// before a claim: the folder is no longer this process's to serve from.
	StopDisowned = "disowned"
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
	// TransferTimeout bounds the streamed result pack sent to the first node,
	// the configured `transfer.timeout`. A result carrying no output uses the
	// shorter taskReportTimeout. It also bounds each Git input poll,
	// which fetches a task's inputs (see observeMailbox).
	TransferTimeout time.Duration
	// Mailbox is the endpoint this node serves.
	Mailbox mailboxx
	// ReturnSigner authenticates the direct result channel announced in the
	// signed per-run probe. The app passes the same secret as the mailbox.
	ReturnSigner *Signer
	// JoinIdentityKey proves a preauthorized late join's pinned node key.
	JoinIdentityKey ed25519.PrivateKey
	// NodeSlots is the stable physical-node capacity authority shared with
	// first-node work and other serving processes for this logical node.
	NodeSlots *NodeSlotAuthority
	// Cache is the object store the task checkouts are materialized from: the
	// same store the mailbox fetches into, because a worktree can only be made
	// of objects the repository holds.
	Cache *gitx.LocalGitx
	// Seen is the record of the work this node has already answered.
	Seen *SeenSet
	// VerifyOwner reports another process's claim on this node's state folder,
	// and is asked before every assignment is claimed. Nil asks nothing.
	VerifyOwner func() error
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
	// isCacheCleared becomes true once the transport refs an earlier process
	// left in the cache have been dropped, which happens once, before the
	// first poll.
	isCacheCleared bool
	// lastMaintained is when the poll goroutine last compacted the cache, the
	// only goroutine that reads or writes it.
	lastMaintained time.Time
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
	claimed map[workerClaimIdentity]*claimedTask
	// A signed probe fixes one return channel per run. Task goroutines read
	// this map after the poll goroutine has answered preflight.
	returnMu       sync.RWMutex
	returnChannels map[string]*workerReturnChannel
	// refusal is what stopped this node when neither a signal nor idleness
	// did: the refusal VerifyOwner answered. Only the poll goroutine writes
	// it, and Err reads it once Serve has returned.
	refusal       error
	taskReturnMu  sync.Mutex
	taskReturnErr error
	// An unanswered return claim retains its local slot and exact proposal.
	// A missing receipt at one read cannot prove that an in-flight claim will
	// not be accepted later by the first node.
	pendingReturn map[string]*pendingReturnClaim
	parkedReturn  map[string]*parkedReturnRun
	// The poll sees fixed slot refs, not every inert task ref. Keep an exact
	// verified offer's slot only while a proposal is pending or parked, so a
	// signed wake can make that unchanged slot visible again.
	slotForTask  map[string]string
	compiledRuns map[string]*workerCompiledRun
	// compiledUnavailable retains an exact drain proposal when this admitted
	// node cannot read the complete signed graph before any task claim.
	compiledUnavailable map[string]*workerCompiledUnavailable
	// Set by the app only when it can independently reconstruct pinned plans.
	// A verified bootstrap mode, never local config, selects this callback.
	PrepareInterpretedRun PrepareInterpretedRun
}

type workerReturnChannel struct {
	binding      ChoreographyRunBinding
	endpoint     string
	certSHA256   string
	bootstrapOID string
	bootstrap    *RunBootstrap
	client       workerReturnx
}

// workerReturnx is the signed first-node return path consumed by a serving node.
type workerReturnx interface {
	SubmitProbe(context.Context, ChoreographyProbeReply) (ChoreographyReceipt, error)
	SubmitClaim(context.Context, ChoreographyClaimProposal) (ChoreographyReceipt, error)
	SubmitResult(context.Context, ChoreographyResultProposal, io.ReadSeeker) (ChoreographyReceipt, error)
	SubmitAck(context.Context, ChoreographyAckProposal) (ChoreographyReceipt, error)
	LookupReceipt(context.Context, ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error)
	PollEvents(context.Context, ChoreographyEventRequest) (ChoreographyEventPage, error)
	Close()
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
	w.slots = make(chan struct{}, max(w.Report.Capacity, workerRuntimeMinimumSlotCapacity))
	cleanTaskDirectory := w.NodeSlots == nil
	if w.NodeSlots != nil {
		recoverCtx, stopRecover := context.WithTimeout(ctx, w.resolvePollTimeout())
		if err := w.RecoverHeldNodeSlots(recoverCtx); err != nil {
			w.Log.Warn().Err(err).Msg(workerRuntimeRecoverSlotsMessage)
		} else if held, err := w.NodeSlots.Held(recoverCtx); err != nil {
			w.Log.Warn().Err(err).Msg(workerRuntimeRecoverSlotsMessage)
		} else {
			cleanTaskDirectory = len(held) == workerRuntimeNoActiveWork
		}
		stopRecover()
	}
	// A held lease can still own a live child or an unresolved external effect.
	// Preserve its checkout until every durable slot has settled; a failed
	// recovery/read proves no cleanup is safe either.
	if cleanTaskDirectory {
		w.clearTaskDir(filepath.Join(w.StateDir, taskWorkDir), w.Log)
	}
	w.Log.Info().Str("endpoint", gitx.RedactURL(w.Endpoint)).
		Int("concurrency", w.Report.Capacity).Str("stateDir", w.StateDir).
		Msg("worker started")
	reason := w.poll(ctx)
	if len(w.compiledRuns) != workerRuntimeNoActiveWork {
		drainCtx, stopDrain := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
		w.refreshSealedCompiledRuns(drainCtx)
		if err := w.drainCompiledRuns(drainCtx); err != nil {
			w.refusal = err
			w.Log.Error().Err(err).Msg(workerRuntimeDrainErrorMessage)
		}
		stopDrain()
	}
	// A node that stopped claiming still owes a report for everything it took
	// on: the commands are already dead when the stop reached them, and the
	// run that dispatched them is waiting to hear so.
	w.running.Wait()
	if w.NodeSlots != nil {
		queryCtx, stopQuery := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
		held, heldErr := w.NodeSlots.Held(queryCtx)
		stopQuery()
		if heldErr != nil {
			w.refusal = errors.Join(w.refusal, heldErr)
		} else {
			for _, run := range w.compiledRuns {
				if run.interpreted == nil {
					continue
				}
				occupied := false
				for _, lease := range held {
					if lease.Intent.Run == run.binding.Run {
						occupied = true
						break
					}
				}
				if !occupied {
					w.refusal = errors.Join(w.refusal, run.interpreted.Close())
					run.interpreted = nil
				}
			}
		}
	}
	w.closeReturnChannels()
	w.Log.Info().Str("reason", reason).Msg("worker stopped")
	return reason
}

// Err is the refusal that stopped this node, and nil for a node that stopped
// because it was signalled or went idle. It is read once Serve has returned.
func (w *Worker) Err() error {
	w.taskReturnMu.Lock()
	defer w.taskReturnMu.Unlock()
	return errors.Join(w.refusal, w.taskReturnErr)
}

func (w *Worker) rememberTaskReturnFailure(err error) {
	w.taskReturnMu.Lock()
	defer w.taskReturnMu.Unlock()
	if w.taskReturnErr == nil {
		w.taskReturnErr = err
	}
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
		if w.refusal != nil {
			return StopDisowned
		}
		w.maintainIdleCache(ctx, time.Now())
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
	if w.inFlight > workerRuntimeNoActiveWork || len(w.pendingReturn) > workerRuntimeNoActiveWork ||
		len(w.parkedReturn) > workerRuntimeNoActiveWork {
		return w.IdleTimeout
	}
	return w.IdleTimeout - time.Since(w.lastActive)
}

// The cache compaction rhythm. A node compacts its cache once per idle
// stretch: after a minute with nothing claimed and nothing in flight, which is
// twelve polls at the longest interval and long enough that the gaps between
// the tasks of a busy run do not reach it. The bound keeps a compaction that
// has run into a slow disk from holding up the poll for longer than that.
const (
	cacheMaintenanceIdle    = time.Minute
	cacheMaintenanceTimeout = 10 * time.Minute
)

// maintainIdleCache compacts the cache when the node has been idle long
// enough, and at most once per idle stretch.
//
// Every message a node writes and every branch it fetches leaves objects in
// the cache that nothing reaches once the run closes its branches, and git's
// own maintenance is turned off there, because it would otherwise start inside
// a poll's fetch. So the node collects the garbage itself, on the poll
// goroutine and with no task in flight: nothing else writes the cache then,
// which is what makes deleting every unreachable object at once safe. A
// failure costs disk and nothing else, so it is a debug line, and the next
// idle stretch tries again.
func (w *Worker) maintainIdleCache(ctx context.Context, now time.Time) {
	if !w.isCacheMaintenanceDue(now) {
		return
	}
	w.lastMaintained = now
	bounded, done := context.WithTimeout(ctx, cacheMaintenanceTimeout)
	defer done()
	started := time.Now()
	if err := w.Cache.CollectGarbage(bounded); err != nil {
		w.Log.Debug().Err(err).Msg("the idle node's cache was not compacted")
		return
	}
	w.Log.Debug().Dur("took", time.Since(started)).Msg("the idle node's cache was compacted")
}

// isCacheMaintenanceDue reports whether the cache is open, no task is in
// flight, something happened since the last compaction, and nothing has
// happened for cacheMaintenanceIdle.
func (w *Worker) isCacheMaintenanceDue(now time.Time) bool {
	if w.Cache == nil || !w.isStorePrepared {
		return false
	}
	w.activity.Lock()
	defer w.activity.Unlock()
	return w.inFlight == 0 && w.lastActive.After(w.lastMaintained) &&
		now.Sub(w.lastActive) >= cacheMaintenanceIdle
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
	settled := w.settlePendingReturnClaims(ctx)
	settled = w.pollParkedReturnClaims(ctx) || settled
	isProgress, err := w.inspectMailbox(ctx)
	isProgress = isProgress || settled
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
	if errors.Is(err, errPollExpired) {
		// Nothing is wrong with the store: the memo still says what was last
		// read, so the next poll asks for the same branches again.
		w.Log.Warn().Err(err).Msg("the poll of the mailbox ran out of time; the next poll tries again")
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
		if !w.isCacheCleared {
			w.isCacheCleared = true
			w.clearLeftoverRefs(ctx)
		}
	}
	heads, err := w.observeMailbox(ctx)
	if err != nil {
		return false, err
	}
	isProgress := false
	for _, head := range heads {
		var answered bool
		if strings.HasPrefix(head.Name, FormatSlotPrefix(w.Node)) {
			answered, err = w.handleSlot(ctx, head)
		} else {
			answered, err = w.handle(ctx, head)
		}
		isProgress = isProgress || answered
		if err != nil {
			return isProgress, err
		}
		if w.refusal != nil {
			// The rest of the mailbox belongs to the process that owns the
			// state folder now.
			return isProgress, nil
		}
	}
	compiledProgress, compiledErr := w.pollCompiledRuns(ctx)
	isProgress = isProgress || compiledProgress
	if compiledErr != nil {
		return isProgress, compiledErr
	}
	return isProgress, nil
}

// errPollExpired is a poll that reached its own deadline before the mailbox
// answered.
var errPollExpired = errors.New("execution: the poll of the mailbox ran out of time")

// observeMailbox is one poll of this node's branches under a deadline of its
// own.
//
// The poll runs on the goroutine that claims work, so a listing or a fetch
// that hangs on a dead connection would otherwise stop the node for as long as
// the process lives. The bound is the configured transfer window rather than
// something tied to the poll interval, because a poll fetches whatever moved
// on this node's branches, and that includes a task's inputs: an input state
// that carries a repository's whole source, and the output sets a build
// consumes. The transfer window is what the operator allowed for moving
// exactly those, and a shorter bound would cut a large first fetch at the same
// point on every poll, since git keeps nothing of a fetch it did not finish.
// No byte budget is applied here: git's fetch has no ceiling on what it
// receives, and the ceilings on what a task consumes are enforced when its
// inputs are read.
func (w *Worker) observeMailbox(ctx context.Context) ([]gitx.RemoteHead, error) {
	bound := w.resolvePollTimeout()
	bounded, done := context.WithTimeout(ctx, bound)
	defer done()
	probes, err := w.Mailbox.Observe(bounded, FormatBranchPattern(w.Node), func(branch string) bool {
		return ResolveBranchKindHint(branch) == KindProbe
	})
	heads := probes
	if err == nil {
		var slots []gitx.RemoteHead
		slots, err = w.Mailbox.Observe(bounded, FormatSlotPattern(w.Node), nil)
		heads = append(heads, slots...)
	}
	if err == nil {
		branches := w.activeTaskBranches()
		for start := 0; start < len(branches); start += gitx.MaxTransportBatch {
			end := min(start+gitx.MaxTransportBatch, len(branches))
			var active []gitx.RemoteHead
			active, err = w.Mailbox.ObserveManyExact(bounded, branches[start:end])
			heads = append(heads, active...)
			if err != nil {
				break
			}
		}
	}
	if err == nil || ctx.Err() != nil || bounded.Err() == nil {
		return heads, err
	}
	// A poll cut off between two fetches has already remembered the branches
	// it fetched as seen. They are handed to the next poll rather than
	// dropped, since nothing is going to move them again for this node.
	for _, head := range heads {
		w.Mailbox.Reconsider(head.Name)
	}
	return nil, fmt.Errorf("%w after %s: %w", errPollExpired, bound, err)
}

// activeTaskBranches keeps cancellation and uncertain claims visible after a
// slot notice stops moving. Running claims may finish while the poll is in
// flight; pending return proposals belong to this same poll goroutine.
func (w *Worker) activeTaskBranches() []string {
	w.claims.Lock()
	branches := make([]string, workerRuntimeNoActiveWork, len(w.claimed)+len(w.pendingReturn))
	for identity := range w.claimed {
		branches = append(branches, identity.Branch)
	}
	w.claims.Unlock()
	for branch := range w.pendingReturn {
		branches = append(branches, branch)
	}
	slices.Sort(branches)
	return slices.Compact(branches)
}

// reconsiderOfferedTask rearms the fixed slot that authenticated this task.
// A task ref alone is not part of the idle poll, so reconsidering only that
// ref would leave an unchanged signed offer invisible indefinitely.
func (w *Worker) reconsiderOfferedTask(branch string) {
	w.Mailbox.Reconsider(branch)
	if slot := w.slotForTask[branch]; slot != workerRuntimeNoIdentity {
		w.Mailbox.Reconsider(slot)
	}
}

// handleSlot follows one signed fixed-size index entry to its exact task
// branch. An inert entry cannot start work, and an assignment on a different
// branch or for another node cannot be substituted by moving a ref.
func (w *Worker) handleSlot(ctx context.Context, head gitx.RemoteHead) (bool, error) {
	carrier, ok := w.Mailbox.(slotMailboxx)
	if !ok {
		return false, fmt.Errorf("execution: this mailbox cannot verify dispatch carriers")
	}
	tip, err := w.Mailbox.Inspect(ctx, head)
	if err != nil {
		w.Mailbox.Reconsider(head.Name)
		return false, err
	}
	if tip.Kind != MessageSlot {
		return false, nil
	}
	document, err := w.Mailbox.Read(ctx, tip, w.Report.Limits.MaxManifestBytes)
	if err != nil {
		w.Mailbox.Reconsider(head.Name)
		return false, err
	}
	var notice SlotNotice
	if err := json.Unmarshal(document, &notice); err != nil {
		return false, err
	}
	if notice.GraphRootOID != workerRuntimeNoIdentity && notice.AssignmentOID == workerRuntimeNoIdentity {
		return w.handleCompiledNotice(ctx, head, notice)
	}
	if notice.AssignmentOID == workerRuntimeNoIdentity {
		return false, nil
	}
	if reason := CheckHeaderVersion(HeaderVersionCheck{Header: notice.Header,
		Binding: Binding{Node: w.Node, Branch: head.Name}, Now: time.Now(), Version: notice.Protocol}); reason != workerRuntimeNoIdentity ||
		notice.Kind != KindSlot || notice.Sequence == 0 || notice.TaskBranch == "" {
		return false, NewDiagnostic(CodeAuthority, CategoryAuthority,
			"a dispatch slot did not authenticate its task binding")
	}
	branches, err := w.Mailbox.Observe(ctx, "refs/heads/"+notice.TaskBranch, nil)
	if err != nil || len(branches) == 0 {
		// An exact task listing can disappear while this already observed slot
		// still points at it. Keep the slot visible for the next bounded poll.
		w.Mailbox.Reconsider(head.Name)
		return false, err
	}
	assignment, err := carrier.VerifyAssignmentCarriers(ctx, notice.AssignmentOID,
		w.Report.Limits.MaxManifestBytes)
	if err != nil {
		return false, err
	}
	if assignment.Protocol != notice.Protocol || assignment.Branch != notice.TaskBranch || assignment.Node != w.Node ||
		assignment.Run != notice.Run || assignment.Task != notice.Task ||
		assignment.Attempt != notice.Attempt || assignment.PlanDigest != notice.PlanDigest ||
		assignment.Generation != notice.Generation {
		return false, NewDiagnostic(CodeAuthority, CategoryAuthority,
			"a dispatch slot named a different assignment")
	}
	for _, branch := range branches {
		chain, err := carrier.InspectChain(ctx, branch)
		if err != nil {
			return false, err
		}
		found := branch.OID == notice.AssignmentOID
		for _, step := range chain {
			found = found || step.OID == notice.AssignmentOID
		}
		if !found {
			return false, NewDiagnostic(CodeAuthority, CategoryAuthority,
				"a dispatch slot's assignment is absent from its branch")
		}
		wasFull := cap(w.slots) > 0 && len(w.slots) == cap(w.slots)
		if w.slotForTask == nil {
			w.slotForTask = make(map[string]string)
		}
		w.slotForTask[branch.Name] = head.Name
		answered, err := w.handle(ctx, branch)
		if err != nil {
			// A validated offer whose local read, capacity epoch, or return
			// failed still names work. Both memo entries must move together.
			w.reconsiderOfferedTask(branch.Name)
		}
		if w.pendingReturn[branch.Name] == nil {
			run := w.parkedReturn[assignment.Run]
			if run == nil || run.entries[branch.Name] == nil {
				delete(w.slotForTask, branch.Name)
			}
		}
		run := w.parkedReturn[assignment.Run]
		parked := run != nil && run.entries[branch.Name] != nil
		if wasFull && !answered && err == nil && !parked && w.pendingReturn[branch.Name] == nil {
			// A queued task reconsidered only its task ref. The unchanged slot
			// would otherwise hide that ref forever after capacity returned.
			w.Mailbox.Reconsider(head.Name)
		}
		return answered, err
	}
	return false, nil
}

// resolvePollTimeout is the bound on one poll: the configured transfer window,
// and its default for a node assembled without one.
func (w *Worker) resolvePollTimeout() time.Duration {
	if w.TransferTimeout > 0 {
		return w.TransferTimeout
	}
	return time.Duration(public.DefaultExecutionTransferTimeout) * time.Second
}

// clearLeftoverRefs drops every coordination ref the cache holds, once, the
// first time the store is open.
//
// The refs a process fetched are the ones it removes again, and it knows them
// only while it runs: a node that was killed, or restarted after a crash,
// leaves refs nobody will ever remove, and each keeps a task's inputs or an
// output set reachable. The cache is dispensable and this process is its only
// user (worker.lock), and before the first poll nothing of this process's is
// in it, so the whole namespace goes. It happens here rather than when Serve
// starts because the store may not be open yet, and it happens only once
// because a store reopened later may already hold the inputs of a task in
// flight. A failure costs disk and nothing else, so it is a debug line.
func (w *Worker) clearLeftoverRefs(ctx context.Context) {
	if w.Cache == nil {
		return
	}
	cleared, err := w.Cache.ClearTransportRefs(ctx)
	if err != nil {
		w.Log.Debug().Err(err).Msg("the coordination refs an earlier process fetched were not removed")
		return
	}
	w.Log.Debug().Int("refs", cleared).Msg("the coordination refs an earlier process fetched were removed")
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
	if (assignment.Kind == KindProbe || isFrameKind(assignment.Kind)) && !w.isStateOwned() {
		// Left exactly where the orchestrator put it, for the node that owns
		// the state folder now.
		return false, nil
	}
	if assignment.Kind == KindProbe {
		return true, w.answerProbe(ctx, tip, assignment)
	}
	// A preparation is a build frame and is executed as one: the kind says why
	// the run asked for it, not what the node does with it. A sweep task uses
	// the same machinery with nothing around its commands.
	if isFrameKind(assignment.Kind) {
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

// isStateOwned asks, before a claim, whether this process still owns its
// state folder, and records the refusal that stops the node when another
// process has written its id there. Claims are compare-and-swap pushes, so
// the two processes can never both claim one assignment; asking before every
// claim bounds how long they serve side by side to the claims already in
// flight.
func (w *Worker) isStateOwned() bool {
	if w.refusal != nil {
		return false
	}
	if w.VerifyOwner == nil {
		return true
	}
	err := w.VerifyOwner()
	if err == nil {
		return true
	}
	w.refusal = err
	w.Log.Error().Err(err).Str("code", CodeConfiguration).Str("category", CategoryConfiguration).
		Msg("the worker state folder is claimed by another process; this node stops claiming work")
	return false
}

// isFrameKind reports whether an assignment of this kind is a frame this node
// runs: a build, a preparation, a configured stage or a sweep task.
func isFrameKind(kind string) bool {
	switch kind {
	case KindBuild, KindPrepare, KindStage, KindRun:
		return true
	default:
		return false
	}
}

// takeTask proposes one assignment to the first node and starts it only
// after an exact signed receipt. A full node leaves the assignment queued;
// an unanswered proposal keeps its slot and body until the first node settles
// it, so a transient response failure cannot start duplicate work.
func (w *Worker) takeTask(ctx context.Context, tip ChainTip, assignment Assignment) (bool, error) {
	if assignment.GraphRootOID != workerRuntimeNoIdentity {
		return workerRuntimeTaskNotClaimed, fmt.Errorf(workerRuntimeCompiledClaimPathError)
	}
	if w.pendingReturn[tip.Branch] != nil {
		return workerRuntimeTaskNotClaimed, nil
	}
	if run := w.parkedReturn[assignment.Run]; run != nil && run.entries[tip.Branch] != nil {
		return workerRuntimeTaskNotClaimed, nil
	}
	select {
	case w.slots <- struct{}{}:
	default:
		// Left exactly where the orchestrator put it, and forgotten by the
		// memo, so that the next poll offers it to this node again: nobody
		// else is going to move the branch on this node's behalf.
		w.Mailbox.Reconsider(tip.Branch)
		w.Log.Debug().Str("branch", tip.Branch).Str("run", assignment.Run).
			Str("task", assignment.Task).Msg("assignment left queued: this node is full")
		return workerRuntimeTaskNotClaimed, nil
	}
	channel, err := w.returnChannelForTask(ctx, tip, assignment)
	if err != nil {
		<-w.slots
		return workerRuntimeTaskNotClaimed, fmt.Errorf(workerRuntimeReturnErrorFormat, err)
	}
	if w.ReturnSigner == nil {
		<-w.slots
		return workerRuntimeTaskNotClaimed, fmt.Errorf(workerRuntimeReturnSignerError)
	}
	claimCtx, stopClaim := context.WithTimeout(ctx, taskReportTimeout)
	defer stopClaim()
	page, err := channel.client.PollEvents(claimCtx, ChoreographyEventRequest{
		ChoreographyRunBinding: channel.binding, Node: w.Node, MaxEvents: workerRuntimeProbeEventPageSize})
	if err != nil {
		<-w.slots
		return workerRuntimeTaskNotClaimed, fmt.Errorf(workerRuntimeCapacityErrorFormat, err)
	}
	if page.Head.OID == workerRuntimeNoIdentity {
		<-w.slots
		return workerRuntimeTaskNotClaimed, fmt.Errorf(workerRuntimeEmptyCapacityError)
	}
	proposal := w.formatReturnClaim(returnClaimRequest{Binding: channel.binding,
		LedgerOID: page.Head.OID, Tip: tip, Assignment: assignment})
	if err := w.Seen.Persist(); err != nil {
		<-w.slots
		w.reconsiderOfferedTask(tip.Branch)
		return workerRuntimeTaskNotClaimed, err
	}
	lease, available, err := w.reserveNodeSlot(claimCtx, assignment, channel, proposal)
	if err != nil {
		<-w.slots
		return workerRuntimeTaskNotClaimed, err
	}
	if !available {
		<-w.slots
		w.reconsiderOfferedTask(tip.Branch)
		return workerRuntimeTaskNotClaimed, nil
	}
	receipt, settled, err := w.submitReturnClaim(claimCtx, channel, proposal)
	if settled {
		if receipt.Refused {
			settleCtx, done := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
			defer done()
			if err := w.releaseNodeSlot(settleCtx, lease, receipt.FenceOID); err != nil {
				return workerRuntimeTaskNotClaimed, err
			}
			<-w.slots
			w.parkRefusedClaim(parkedClaimRequest{Channel: channel, Branch: tip.Branch,
				Assignment: assignment, Receipt: receipt})
			return workerRuntimeTaskNotClaimed, nil
		}
		var done bool
		var progress bool
		progress, done, err = w.startReturnedClaim(returnedClaimStart{
			LookupContext: claimCtx, TaskContext: ctx, Tip: tip,
			Assignment: assignment, ClaimOID: receipt.OID, Held: []NodeSlotLease{lease},
		})
		if done {
			return progress, err
		}
	}
	if w.pendingReturn == nil {
		w.pendingReturn = make(map[string]*pendingReturnClaim)
	}
	w.pendingReturn[tip.Branch] = &pendingReturnClaim{
		tip: tip, assignment: assignment, proposal: proposal, lease: lease,
	}
	w.Log.Warn().Err(err).Str(workerRuntimeBranchLogField, tip.Branch).
		Msg(workerRuntimeClaimUnsettledMessage)
	return workerRuntimeTaskNotClaimed, nil
}

// startClaimedTask records a claim this node holds and runs its task, in a
// slot the caller already took.
func (w *Worker) startClaimedTask(ctx context.Context, tip ChainTip, assignment Assignment,
	claimed string, held ...NodeSlotLease) (bool, error) {
	w.Log.Info().Str("branch", tip.Branch).Str("commit", claimed).
		Str("run", assignment.Run).Str("task", assignment.Task).Int("attempt", assignment.Attempt).
		Str("kind", assignment.Kind).Msg("task claimed")
	// Remembered before the work starts: an attempt this node took on and then
	// failed to report must not be taken on again.
	if err := w.Seen.Record(assignment.Run, assignment.Task, assignment.Attempt, time.Now()); err != nil {
		// The accepted claim remains pending with its exact proposal and slot.
		// A later poll may repair the record, but no command may start yet.
		return false, err
	}
	// The task's own context is created here rather than inside the goroutine
	// because the party that cancels it is this one: a withdrawal arrives on
	// the poll, and the cancellation has to be reachable from it.
	bounded, stop := resolveTaskDeadline(ctx, assignment.DeadlineSeconds)
	task := &claimedTask{assignment: assignment, tip: tip, claimed: claimed,
		stop: stop, expectedTip: claimed, finalityCtx: ctx}
	if len(held) != workerRuntimeNoActiveWork {
		task.slotLease = held[workerRuntimeFirstHeldLeaseIndex]
	}
	forget := w.registerClaim(task)
	w.running.Add(1)
	w.beginTask()
	go func() {
		defer w.running.Done()
		defer w.endTask()
		defer func() { <-w.slots }()
		defer forget()
		defer stop()
		defer w.recoverTask(ctx, task)
		w.answerTask(ctx, bounded, task)
	}()
	return true, nil
}

// recoverTask contains a panic of one task to that task.
//
// The node carries on serving its other tasks. What the run hears depends on
// how far the task got: a task whose command never started reports a failure,
// or acknowledges a withdrawal it was sent, because nothing irreversible
// happened; a task whose command started reports nothing, because only the
// orchestrator's own unknown-outcome path may decide what a started
// publication came to.
func (w *Worker) recoverTask(ctx context.Context, task *claimedTask) {
	recovered := recover()
	if recovered == nil {
		return
	}
	assignment := task.assignment
	log := w.Log.With().Str("run", assignment.Run).Str("task", assignment.Task).
		Int("attempt", assignment.Attempt).Str("branch", task.tip.Branch).Logger()
	log.Error().Str("panic", fmt.Sprint(recovered)).Str("code", CodeIntegrity).
		Str("category", CategoryIntegrity).Msg("the task failed unexpectedly")
	cancel, _, isCommandStarted := task.readProgress()
	if isCommandStarted {
		return
	}
	if cancel != "" {
		w.acknowledgeCancellation(ctx, task, log)
		return
	}
	reportCtx, done := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
	defer done()
	_, err := w.returnTaskResult(reportCtx, task, Result{
		Header: w.formatReplyHeader(assignment.Header), Assignment: task.claimed,
		Status: StatusFailed, FailedPart: resolveCancelledPhase(task.readPhase()),
		Platform: Platform{OS: w.Report.OS, Arch: w.Report.Arch, Dispat: w.Report.Dispat},
	}, nil)
	if err != nil {
		w.rememberTaskReturnFailure(err)
		log.Error().Err(err).Str("code", CodeTransport).Str("category", CategoryTransportCleanup).
			Msg("the failed task could not be reported")
	}
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
	if outcome.unsettledEffects {
		w.rememberTaskReturnFailure(script.ErrProcessGroupUnsettled)
		log.Error().Err(script.ErrProcessGroupUnsettled).
			Msg(workerRuntimeProcessGroupUnsettledMessage)
		return
	}
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
	exports := outcome.transportExports
	if exports == nil {
		exports = formatExports(outcome.exports)
	}
	report := Result{
		Header:      w.formatReplyHeader(assignment.Header),
		Assignment:  task.claimed,
		Status:      outcome.status,
		FailedPart:  outcome.failedPart,
		Exit:        outcome.exit,
		Reason:      string(outcome.reason),
		Platform:    Platform{OS: w.Report.OS, Arch: w.Report.Arch, Dispat: w.Report.Dispat},
		Exports:     exports,
		Outputs:     outcome.outputs,
		StrayWrites: outcome.strayWrites,
	}
	carried := carriedOutputs(outcome.outputs)
	reportCtx, done := context.WithTimeout(context.WithoutCancel(ctx),
		w.resolveReportTimeout(len(carried) > 0))
	defer done()
	reported, err := w.returnTaskResult(reportCtx, task, report, carried)
	if errors.Is(err, errResultWithdrawn) {
		return
	}
	if err != nil && w.acknowledgeResultWithdrawal(reportCtx, task, task.claimed, log) {
		return
	}
	if err != nil {
		w.rememberTaskReturnFailure(err)
		log.Error().Err(err).Str("code", CodeTransport).Str("category", CategoryTransportCleanup).
			Msg("the task result could not be reported")
		return
	}
	log.Info().Str("commit", reported).Str("status", outcome.status).
		Int("strayWrites", outcome.strayWrites).Msg("task finished")
}

// acknowledgeResultWithdrawal recognizes an exact Cancel that arrived after
// the task's last progress check. The command processes have ended, so the
// signed Cancel can be acknowledged. A transport error alone proves no such
// cancellation and never authorizes a different result body.
func (w *Worker) acknowledgeResultWithdrawal(ctx context.Context, task *claimedTask,
	resultLease string, log zerolog.Logger) bool {
	if w == nil || w.Mailbox == nil || task == nil {
		return workerRuntimeNoWithdrawal
	}
	head, err := w.Mailbox.Reread(ctx, task.tip.Branch)
	var answer ChainTip
	if err == nil && head.OID != workerRuntimeNoIdentity {
		answer, err = w.Mailbox.Inspect(ctx, head)
	}
	if err != nil || answer.Kind != MessageCancel || answer.PreviousOID != resultLease {
		answer, err = w.readRetainedPickupCancellation(ctx, task)
	}
	if err != nil || answer.Kind != MessageCancel || answer.PreviousOID != resultLease {
		return workerRuntimeNoWithdrawal
	}
	if reason := w.checkWithdrawal(ctx, answer, task.tip, resultLease, task.assignment); reason != "" {
		w.reportRejection(answer, reason)
		return false
	}
	if !task.withdraw(answer.OID) {
		withdrawn, _, _ := task.readProgress()
		if withdrawn != answer.OID {
			return false
		}
	}
	log.Debug().Str("commit", answer.OID).
		Msg("the result lease lost to this attempt's withdrawal; acknowledging completed work")
	w.writeCancellationAcknowledgement(ctx, task, log)
	return true
}

// resolveReportTimeout bounds a finished task's return. An output pack can
// take the configured transfer window; a result without outputs gets the
// shorter bound.
func (w *Worker) resolveReportTimeout(isCarryingOutputs bool) time.Duration {
	if isCarryingOutputs && w.TransferTimeout > taskReportTimeout {
		return w.TransferTimeout
	}
	return taskReportTimeout
}

// checkAssignment applies the acceptance rules to one assignment: the ones
// every message is held to, and the one that is this node's own memory.
func (w *Worker) checkAssignment(assignment Assignment, tip ChainTip) RejectReason {
	if reason := CheckHeaderVersion(HeaderVersionCheck{Header: assignment.Header,
		Binding: Binding{Node: w.Node, Branch: tip.Branch}, Now: time.Now(), Version: assignment.Protocol}); reason != workerRuntimeNoIdentity {
		return reason
	}
	if w.Seen.IsSeen(assignment.Run, assignment.Task, assignment.Attempt) {
		return ReasonReplay
	}
	if assignment.Kind == KindPublish || assignment.Permits.Publish || assignment.Permits.NativeRefs ||
		(assignment.Kind == KindStage && assignment.Stage == StagePublish) {
		return ReasonPermit
	}
	if assignment.Kind == KindStage && !isStageName(assignment.Stage) {
		// A frame whose stage has no name would run its hooks as `before` and
		// `post`, which no configuration ever wrote.
		return ReasonUnreadable
	}
	return ""
}

// answerProbe claims the branch and reports what this node is.
//
// A probe runs no command, so it is answered inline and takes no capacity
// slot: it is a question about the node rather than work for it, and a node
// whose capacity was full would otherwise be unable to say so.
func (w *Worker) answerProbe(ctx context.Context, tip ChainTip, assignment Assignment) error {
	if w.ReturnSigner == nil {
		return fmt.Errorf(workerRuntimeProbeSignerError)
	}
	return w.answerProbeByReturn(ctx, tip, assignment)
}

func (w *Worker) answerProbeByReturn(ctx context.Context, tip ChainTip, assignment Assignment) error {
	var bootstrap *RunBootstrap
	if assignment.Protocol == ChoreographyProtocolVersion {
		verified, pinned, err := w.verifyPinnedProbe(ctx, tip.OID)
		if err != nil {
			return err
		}
		if pinned.CoordinationEndpoint != w.Endpoint {
			return errors.New(workerProbeMailboxEndpointMismatchError)
		}
		assignment, bootstrap = *verified, &pinned
	}
	if assignment.ReturnEndpoint == workerRuntimeNoIdentity || assignment.ReturnCertSHA256 == workerRuntimeNoIdentity ||
		assignment.GraphDigest == workerRuntimeNoIdentity {
		return fmt.Errorf(workerRuntimeProbeIdentityError)
	}
	binding := ChoreographyRunBinding{Run: assignment.Run, Generation: assignment.Generation,
		PlanDigest: assignment.PlanDigest, GraphDigest: assignment.GraphDigest}
	channel, err := w.prepareReturnChannel(binding, assignment.ReturnEndpoint, assignment.ReturnCertSHA256)
	if err != nil {
		return err
	}
	if bootstrap != nil {
		if channel.bootstrapOID != workerRuntimeNoIdentity && channel.bootstrapOID != assignment.BootstrapOID {
			w.closeUnrememberedReturnChannel(channel)
			return fmt.Errorf(workerRuntimeProbeBootstrapError)
		}
		channel.bootstrapOID, channel.bootstrap = assignment.BootstrapOID, bootstrap
	}
	probeCtx, stopProbe := context.WithTimeout(ctx, taskReportTimeout)
	defer stopProbe()
	if err := w.Seen.Persist(); err != nil {
		w.closeUnrememberedReturnChannel(channel)
		w.Mailbox.Reconsider(tip.Branch)
		return err
	}
	report := w.Report
	report.Protocol = assignment.Protocol
	receipt, err := channel.client.SubmitProbe(probeCtx, ChoreographyProbeReply{
		ChoreographyRunBinding: binding, Node: w.Node, Branch: tip.Branch,
		AssignmentOID: tip.OID, Report: report,
	})
	if err != nil {
		w.closeUnrememberedReturnChannel(channel)
		w.Mailbox.Reconsider(tip.Branch)
		return fmt.Errorf(workerRuntimeProbeReturnErrorFormat, err)
	}
	if !receipt.Accepted || receipt.OID == workerRuntimeNoIdentity {
		w.closeUnrememberedReturnChannel(channel)
		return fmt.Errorf(workerRuntimeProbeReceiptError)
	}
	// The accepted roster entry survives a later replay-record failure. Keep
	// its authenticated channel so a repaired record can resume this run;
	// the claim gate still requires persistence before any new work.
	w.rememberReturnChannel(channel)
	if err := w.Seen.Record(assignment.Run, assignment.Task, assignment.Attempt, time.Now()); err != nil {
		return err
	}
	w.Log.Info().Str(workerRuntimeBranchLogField, tip.Branch).Str(workerRuntimeCommitLogField, receipt.OID).
		Str("run", assignment.Run).Str("task", assignment.Task).Int("attempt", assignment.Attempt).
		Str(workerRuntimeStatusLogField, StatusSucceeded).Msg(workerRuntimePreflightDoneMessage)
	return nil
}

const workerProbeMailboxEndpointMismatchError = "execution: signed preflight coordination endpoint differs from this node's configured mailbox endpoint"

type pinnedProbeVerifier interface {
	ReadVerifiedPinnedProbe(context.Context, string, int64, int64) (*Assignment, RunBootstrap, error)
}

func (w *Worker) verifyPinnedProbe(ctx context.Context, oid string) (*Assignment, RunBootstrap, error) {
	verifier, ok := w.Mailbox.(pinnedProbeVerifier)
	if !ok || w.Report.Limits.MaxManifestBytes <= workerRuntimeNoActiveWork ||
		w.Report.Limits.MaxPackBytes <= workerRuntimeNoActiveWork {
		return nil, RunBootstrap{}, fmt.Errorf(workerRuntimePinnedProbeError)
	}
	assignment, bootstrap, err := verifier.ReadVerifiedPinnedProbe(ctx, oid,
		w.Report.Limits.MaxManifestBytes, w.Report.Limits.MaxPackBytes)
	if err != nil {
		return nil, RunBootstrap{}, fmt.Errorf(workerRuntimeBootstrapErrorFormat, err)
	}
	return assignment, bootstrap, nil
}

func (w *Worker) prepareReturnChannel(binding ChoreographyRunBinding, endpoint, certSHA256 string) (*workerReturnChannel, error) {
	w.returnMu.RLock()
	prior := w.returnChannels[binding.Run]
	w.returnMu.RUnlock()
	if prior != nil && (prior.binding != binding || prior.endpoint != endpoint || prior.certSHA256 != certSHA256) {
		return nil, fmt.Errorf(workerRuntimeReturnIdentityError)
	}
	if prior != nil {
		return prior, nil
	}
	client, err := NewChoreographyReturnClient(ReturnClientOptions{BaseURL: endpoint,
		PinnedCertSHA256: certSHA256, Signer: w.ReturnSigner, Binding: binding,
		MaxResultBytes: w.Report.Limits.MaxPackBytes})
	if err != nil {
		return nil, err
	}
	return &workerReturnChannel{binding: binding, endpoint: endpoint, certSHA256: certSHA256, client: client}, nil
}

func (w *Worker) rememberReturnChannel(channel *workerReturnChannel) {
	w.returnMu.Lock()
	defer w.returnMu.Unlock()
	if w.returnChannels == nil {
		w.returnChannels = make(map[string]*workerReturnChannel)
	}
	if prior := w.returnChannels[channel.binding.Run]; prior != nil {
		if prior == channel {
			return
		}
		prior.client.Close()
	}
	w.returnChannels[channel.binding.Run] = channel
}

func (w *Worker) closeUnrememberedReturnChannel(channel *workerReturnChannel) {
	w.returnMu.RLock()
	remembered := w.returnChannels[channel.binding.Run] == channel
	w.returnMu.RUnlock()
	if !remembered {
		channel.client.Close()
	}
}

// returnChannelForTask restores a v3 channel after a serving-node restart.
// The probe may already be in Seen, or its ref may have been retired; neither
// is authority for an ambient URL. The offered task's signed ancestry keeps
// the exact bootstrap, endpoint and TLS pin reachable without answering the
// probe twice or trusting a moving configuration value.
func (w *Worker) returnChannelForTask(ctx context.Context, tip ChainTip,
	assignment Assignment) (*workerReturnChannel, error) {
	w.returnMu.RLock()
	prior := w.returnChannels[assignment.Run]
	w.returnMu.RUnlock()
	if prior != nil || assignment.Protocol != ChoreographyProtocolVersion {
		return w.returnChannelFor(assignment)
	}
	if w.ReturnSigner == nil || w.Report.Limits.MaxManifestBytes <= workerRuntimeNoActiveWork ||
		w.Report.Limits.MaxPackBytes <= workerRuntimeNoActiveWork || assignment.BootstrapOID == workerRuntimeNoIdentity ||
		assignment.GraphDigest == workerRuntimeNoIdentity || assignment.Branch != tip.Branch || tip.OID == workerRuntimeNoIdentity {
		return nil, fmt.Errorf(workerRuntimeTaskAuthorityError)
	}
	mailbox, ok := w.Mailbox.(*GitMailbox)
	if !ok {
		return nil, fmt.Errorf(workerRuntimeTaskReaderError)
	}
	view, err := mailbox.OpenCarriedAssignment(ctx, tip.OID, w.Report.Limits.MaxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf(workerRuntimeTaskCarrierErrorFormat, err)
	}
	if view == nil || !reflect.DeepEqual(view.Assignment, assignment) {
		return nil, fmt.Errorf(workerRuntimeTaskCarrierError)
	}
	bootstrap, err := mailbox.ReadChoreographyBootstrap(ctx, assignment.BootstrapOID,
		w.Report.Limits.MaxManifestBytes, w.Report.Limits.MaxPackBytes)
	if err != nil {
		return nil, fmt.Errorf(workerRuntimeTaskBootstrapFormat, err)
	}
	binding := ChoreographyRunBinding{Run: assignment.Run, Generation: assignment.Generation,
		PlanDigest: assignment.PlanDigest, GraphDigest: assignment.GraphDigest}
	if !validChoreographyMode(bootstrap.Mode) || bootstrap.ChoreographyRunBinding != binding ||
		bootstrap.CoordinationEndpoint != w.Endpoint ||
		bootstrap.ReturnEndpoint == workerRuntimeNoIdentity || bootstrap.ReturnCertSHA256 == workerRuntimeNoIdentity {
		return nil, fmt.Errorf(workerRuntimeTaskReturnIdentity)
	}
	channel, err := w.prepareReturnChannel(binding, bootstrap.ReturnEndpoint, bootstrap.ReturnCertSHA256)
	if err != nil {
		return nil, err
	}
	channel.bootstrapOID, channel.bootstrap = assignment.BootstrapOID, &bootstrap
	w.rememberReturnChannel(channel)
	return channel, nil
}

func (w *Worker) returnChannelFor(assignment Assignment) (*workerReturnChannel, error) {
	w.returnMu.RLock()
	channel := w.returnChannels[assignment.Run]
	w.returnMu.RUnlock()
	if channel == nil || channel.binding.Generation != assignment.Generation ||
		channel.binding.PlanDigest != assignment.PlanDigest ||
		assignment.Protocol == ChoreographyProtocolVersion &&
			(channel.bootstrap == nil || channel.bootstrap.Protocol != ChoreographyProtocolVersion ||
				!validChoreographyMode(channel.bootstrap.Mode) ||
				channel.bootstrapOID != assignment.BootstrapOID ||
				assignment.GraphDigest != channel.binding.GraphDigest ||
				channel.bootstrap.ChoreographyRunBinding != channel.binding ||
				channel.bootstrap.CoordinationEndpoint != w.Endpoint) ||
		assignment.Protocol == ProtocolVersion && channel.bootstrap != nil {
		return nil, fmt.Errorf(workerRuntimeTaskPreflightError)
	}
	return channel, nil
}

func (w *Worker) closeReturnChannels() {
	w.returnMu.Lock()
	defer w.returnMu.Unlock()
	for _, channel := range w.returnChannels {
		channel.client.Close()
	}
	w.returnChannels = nil
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
		Protocol:   assignment.Protocol,
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
