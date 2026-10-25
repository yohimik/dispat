// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The orchestrator's side of one dispatched build.
//
// A build leaves as a signed assignment carrying exact source and predecessor
// commits, then returns a signed result bound to that assignment. V2 task
// branches are node-neutral. One watcher per endpoint polls only active task
// refs, while workers poll their own fixed dispatch slots.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// Dispatch is what a coordinator needs of the run before it places any work:
// how much this node keeps for itself, which repositories a package's input
// closure consists of, which shell its commands run through, and how one of
// those repositories is opened.
//
// They are functions rather than values because each of them is a question
// about one package or one folder, and the answers belong to the code that
// composed the workspace. Handing the coordinator the workspace itself would
// be handing the transport a planner.
type Dispatch struct {
	// Concurrency bounds the frames this node keeps for itself: the version
	// and syncLock stages the guard brackets.
	Concurrency int
	// Sources answers the repositories one package's input closure needs, the
	// root first, each with the planned head its snapshot descends from.
	Sources func(packageName string) []Source
	// InitialSources and OutputRoots freeze source entries beneath declared
	// output roots before any stage can write or admit an output set.
	InitialSources      []Source
	OutputRoots         map[string][]string
	PackageOutputRoots  map[string][]string
	PackageRepositories map[string]string
	// Shell is the interpreter the commands of a folder run through, which in
	// a composed workspace is the configuration that owns that folder.
	Shell func(dir string) []string
	// OpenRepository opens the plumbing of one repository's working tree, so
	// that a snapshot is written into the object store it came from.
	OpenRepository func(dir string) *gitx.LocalGitx
	// Inputs answers the provider output sets one package's build consumes:
	// its transitive provider closure over every dependency kind, in
	// dependency order, restricted to the packages that declare build outputs.
	// A build-only edge outside the propagation kinds still supplies bytes, so
	// the closure is the graph's rather than the release rules'.
	Inputs func(packageName string) []InputPackage
	// Prepare describes the build frame of one provider this run does not
	// release, so that its declared outputs can be produced without a release
	// being invented for it (§28.5). It answers the frame as a node receives
	// it and the same frame as this process runs it, because where that frame
	// is placed is the pool's decision and not the caller's.
	Prepare func(packageName string) (*PreparedProvider, error)
	// Store is the object store of assignments and worker results. Source and
	// local result commits are imported here before their carrier is signed.
	Store *gitx.LocalGitx
}

// InputPackage is one provider a consumer's build may need bytes from: which
// package it is, and where its folder sits in the checkout a node
// materializes, relative to the run's own anchor.
type InputPackage struct {
	Package string
	Path    string
	// IsPrepared says this run releases no version of the provider, so nothing
	// in the task graph builds it: its declared outputs exist only because the
	// run builds it on purpose before the first consumer that reads them
	// (§28.5).
	IsPrepared bool
}

// Start gives the coordinator what it needs to place work and begins watching
// the endpoints for what comes back.
//
// It is separate from Preflight because the two answer different questions: a
// pool that cannot be assembled is a refusal before anything happens, and a
// run that has passed that check is one that now owns goroutines. Close stops
// them.
func (c *Coordinator) Start(ctx context.Context, dispatch Dispatch) error {
	c.dispatch = dispatch
	c.snapshots = newSnapshots()
	if len(dispatch.InitialSources) > 0 {
		if err := c.snapshots.captureBaselines(ctx, baselineCapture{sources: dispatch.InitialSources,
			roots: dispatch.OutputRoots, open: dispatch.OpenRepository, log: c.Log}); err != nil {
			c.snapshots.closeBaselines()
			return NewDiagnostic(CodeIntegrity, CategoryIntegrity,
				"execution input baseline could not be captured: %w", err)
		}
	}
	watched, stop := context.WithCancel(ctx)
	c.stopWatching = stop
	c.outputs = newOutputRegistry()
	c.sweepOutputs = newSweepOutputs()
	c.preparations = map[string]*preparation{}
	c.local = make(chan struct{}, max(dispatch.Concurrency, 1))
	c.watchers = make(map[string]*watcher, len(c.Links))
	for _, link := range c.Links {
		observer := &watcher{coordinator: c, link: link, mailbox: c.mailboxes[link.Name],
			wake: make(chan struct{}, 1), attempts: map[string]*attemptState{}}
		c.watchers[link.Name] = observer
		c.watching.Add(1)
		go func() {
			defer c.watching.Done()
			observer.serve(watched)
		}()
	}
	c.Log.Debug().Int("nodes", len(c.Links)).Int("concurrency", max(dispatch.Concurrency, 1)).
		Str("run", c.Run).Msg("dispatch opened")
	return nil
}

// Guard brackets a frame this node keeps for itself.
//
// Before the frame runs, its package's declared roots are restored once from
// the pinned source while holding the exclusive snapshot guard. Then the
// shared side prevents a dispatch from capturing a partial preparation write.
// A local slot also bounds the orchestrator's simultaneous preparing frames.
func (c *Coordinator) Guard(ctx context.Context, stage, packageName string) (func(), error) {
	select {
	case c.local <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a local slot to run the %s stage: %w", stage, ctx.Err())
	}
	if packageName != "" && stage != "revert" {
		c.guard.Lock()
		err := c.cleanPreparationRoots(ctx, packageName)
		c.guard.Unlock()
		if err != nil {
			<-c.local
			return nil, err
		}
	}
	c.guard.RLock()
	c.Log.Trace().Str("stage", stage).Str("run", c.Run).Msg("stage holds the shared snapshot guard")
	return func() {
		c.guard.RUnlock()
		<-c.local
	}, nil
}

func (c *Coordinator) cleanPreparationRoots(ctx context.Context, packageName string) error {
	roots := c.dispatch.PackageOutputRoots[packageName]
	if len(roots) == 0 {
		return nil
	}
	for _, source := range c.dispatch.Sources(packageName) {
		if source.Name == c.dispatch.PackageRepositories[packageName] {
			return c.snapshots.cleanPackageRoots(ctx, packageRootState{name: packageName,
				source: source, roots: roots, git: c.dispatch.OpenRepository(source.Dir), log: c.Log})
		}
	}
	return fmt.Errorf("execution: source repository of %s is absent from its input closure", packageName)
}

// Stage places one package's frame of a delegable stage and runs it where it
// was placed. The request's descriptor decides what differs between stages:
// the runOnly value that places the frame, what a node is given first, whether
// its outputs are admitted, and which local authority runs its commands.
//
// A stage reading its providers' outputs has the providers this run does not
// release prepared before a node slot is taken: a consumer holding a slot
// while it waits for a preparation would wait for capacity it occupies. It
// holds the run-wide slot the task graph gave it meanwhile, which keeps the
// preparations inside the build budget. A frame placed here then runs against
// this checkout; one placed on a node needs its input state prepared, offered
// and fetched back. A stage reading the package's own outputs prefers the node
// that produced them, whose cache already holds the objects.
func (c *Coordinator) Stage(ctx context.Context, request release.StageRequest,
	here release.LocalFrame, _ func(context.Context) error) (release.StageOutcome, error) {
	descriptor := request.Descriptor
	task := request.Release.Pkg.Name + ":" + request.Stage
	if !descriptor.IsPreparing {
		if err := c.refreshPreparedOutputRoots(ctx, request); err != nil {
			return release.StageOutcome{FailedPart: release.PartInputs}, err
		}
	}
	if descriptor.Inputs == release.InputsProviderOutputs {
		if err := c.prepareProviderOutputs(ctx, task, request); err != nil {
			return release.StageOutcome{FailedPart: release.PartInputs}, err
		}
	}
	space := request.Release.Pkg.Space
	placement := resolveDescriptorPlacement(descriptor, space)
	if descriptor.Placement == release.PlacementByPublish || request.Stage == StagePublish {
		if len(space.BuildOutputs) > 0 {
			if err := c.prepareLocalPublishInputs(ctx, request); err != nil {
				return release.StageOutcome{FailedPart: release.PartInputs}, err
			}
		}
		return c.publishHere(ctx, here)
	}
	preferred := ""
	if descriptor.Inputs == release.InputsOwnOutputs {
		preferred = c.producerOf(request)
	}
	return c.placeTask(ctx, task, placement, space.BuildPlatforms, preferred,
		func(ctx context.Context, lease *Lease, attempt int) (release.StageOutcome, error) {
			if lease.IsLocal {
				if len(space.BuildOutputs) > 0 {
					return c.runIsolatedHere(ctx, localIsolatedFrame{
						lease: lease, task: task, attempt: attempt, request: request, here: here})
				}
				return c.runHere(ctx, lease, task, request, here)
			}
			return c.dispatchStage(ctx, lease, resolveStageKind(descriptor), task, attempt, request)
		})
}

func (c *Coordinator) refreshPreparedOutputRoots(ctx context.Context, request release.StageRequest) error {
	name := request.Release.Pkg.Name
	roots := c.dispatch.PackageOutputRoots[name]
	if len(roots) == 0 {
		return nil
	}
	c.guard.Lock()
	defer c.guard.Unlock()
	for _, source := range c.dispatch.Sources(name) {
		if source.Name == request.Release.Pkg.Repository {
			request := packageRootState{name: name, source: source, roots: roots,
				git: c.dispatch.OpenRepository(source.Dir), log: c.Log}
			if err := c.snapshots.cleanPackageRoots(ctx, request); err != nil {
				return err
			}
			return c.snapshots.refreshPackageRoots(ctx, request)
		}
	}
	return fmt.Errorf("execution: source repository of %s is absent from its input closure", name)
}

// resolveStageKind is the assignment kind a stage's frame travels under. The
// build and the publish keep the kinds every worker has always served; any
// other stage travels under the generic kind, which names the stage in the
// assignment itself.
func resolveStageKind(descriptor release.Stage) string {
	switch descriptor.Name {
	case StageBuild:
		return KindBuild
	case StagePublish:
		return KindPublish
	default:
		return KindStage
	}
}

// maxPlacementAttempts bounds how often one task is offered again after an
// assignment nobody claimed was revoked.
//
// It is small and it exists so that a pool with no capacity fails a task
// instead of offering it for ever: three attempts is enough for a node that
// was busy with another run's work to become free, and a fourth would be this
// run deciding that waiting is always better than saying so.
const maxPlacementAttempts = 3

// placeTask takes a node slot, runs one attempt on it and offers the task
// again when the assignment was revoked before anybody claimed it.
//
// The loop is what separates queue time from run time. An assignment that sat
// in a mailbox behind another run's work performed nothing and holds nothing,
// so revoking it costs the run a branch and nothing else; placing the task
// again is then an ordinary placement, on whichever compatible node is free by
// then, including the one that was busy. A task that runs out of attempts
// fails saying what was actually wrong, which is that the pool had no capacity
// for it rather than that anything about the package is broken.
func (c *Coordinator) placeTask(ctx context.Context, task string, placement Placement,
	platforms []string, preferred string,
	attemptOnce func(context.Context, *Lease, int) (release.StageOutcome, error)) (release.StageOutcome, error) {
	// Every attempt of this task runs under a context a lost lock can end, so
	// that a loss noticed by any other task of the run reaches this one too.
	ctx, settled := c.watchOwnership(ctx)
	defer settled()
	for attempt := 1; ; attempt++ {
		lease, err := c.Pool.AcquireNear(ctx, platforms, placement, preferred)
		if err != nil {
			return release.StageOutcome{}, c.refuseTask(task, "", attempt, err)
		}
		outcome, err := attemptOnce(ctx, lease, attempt)
		if !isPlacedAgain(err) {
			c.rememberPlacedTask(task, lease, attempt, placedOutcome{exports: len(outcome.Exports), err: err})
			return outcome, err
		}
		if attempt >= maxPlacementAttempts {
			return outcome, c.refuseTask(task, "", attempt, formatPlacementExhausted(err, c.Timeouts.Task, attempt))
		}
		c.Log.Warn().Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
			Int("attempt", attempt).Str("code", CodeIntegrity).Str("category", CategoryIntegrity).
			Msg("the queued assignment was revoked and the task is placed again")
	}
}

// isPlacedAgain reports whether an attempt ended without placing anything, so
// that the task is offered again: an assignment nobody claimed and that was
// revoked, or one the mailbox never took.
func isPlacedAgain(err error) bool {
	return errors.Is(err, errQueueExpired) || errors.Is(err, errOfferNotLanded)
}

// formatPlacementExhausted is why a task that was offered again as often as it
// may be is refused: a pool with no capacity for it, or a mailbox that would
// not take its assignment, which are different things for an operator to go
// and look at.
func formatPlacementExhausted(last error, wait time.Duration, attempts int) error {
	if errors.Is(last, errOfferNotLanded) {
		return fmt.Errorf("the mailbox did not take the assignment on any of %d attempts: %w", attempts, last)
	}
	return fmt.Errorf("no node claimed this task within %s on any of %d attempts: the pool had no capacity for it",
		wait, attempts)
}

// placedOutcome is what one placed attempt ended with, as the summary records
// it: how many values its scripts exported, and the failure it reported.
type placedOutcome struct {
	exports int
	err     error
}

// rememberPlacedTask records what one placed task came to, for the summary
// §28.9 requires: the work, where it ran, and the four outcomes told apart.
func (c *Coordinator) rememberPlacedTask(task string, lease *Lease, attempt int, placed placedOutcome) {
	packageName, stage := splitTaskName(task)
	computation, publication := c.resolveTaskOutcome(task, stage, placed.err)
	c.rememberTaskOutcome(TaskRecord{
		Package: packageName, Stage: stage, Task: task, Node: lease.Node, Attempt: attempt,
		Computation: computation, Outputs: OutputsNone, Publication: publication,
		Recording: RecordingNone, Exports: placed.exports,
	})
}

// splitTaskName is the package and the stage a task name is made of. A name
// with no separator is its own package and no stage, which nothing this run
// dispatches produces and which is still not worth a panic.
func splitTaskName(task string) (packageName, stage string) {
	for index := len(task) - 1; index >= 0; index-- {
		if task[index] == ':' {
			return task[:index], task[index+1:]
		}
	}
	return task, ""
}

// dispatchStage prepares one package's input state, offers its frame to the
// node the pool chose and waits for that node to report.
//
// The kind is the assignment's rather than the frame's: a build this run
// releases and a build of a provider it does not are the same frame executed
// under the same rules, and the word is what tells a reader of the mailbox,
// of the branch names and of the log which of the two it is looking at.
func (c *Coordinator) dispatchStage(ctx context.Context, lease *Lease, kind, task string,
	attempt int, request release.StageRequest) (release.StageOutcome, error) {
	if kind == KindPublish || request.Descriptor.IsAuthorized {
		lease.Release()
		return release.StageOutcome{}, NewDiagnostic(CodeAuthority, CategoryAuthority,
			"publication cannot be delegated to a worker")
	}
	frame, err := c.prepareFrame(ctx, lease.Node, request)
	if err != nil {
		lease.Release()
		return release.StageOutcome{}, c.refuseTask(task, lease.Node, attempt, err)
	}
	return c.runTask(ctx, lease, kind, task, attempt, frame.dir, frame.repositories, frame.inputs, request)
}

// preparedFrame is what a node needs before one frame can be offered to it:
// the package's folder in its checkout, the prepared input state of every
// repository it reads, and the output sets to install.
type preparedFrame struct {
	dir          string
	repositories []AssignmentRepository
	inputs       []AssignmentInput
}

// prepareFrame captures the package's input state, offers it to the node's
// mailbox and resolves the output sets the stage reads. The input state of a
// frame that produces outputs is remembered as the package's consumed
// snapshot, which is what a publication is revalidated against.
func (c *Coordinator) prepareFrame(ctx context.Context, node string,
	request release.StageRequest) (preparedFrame, error) {
	frame := preparedFrame{}
	sources := c.dispatch.Sources(request.Release.Pkg.Name)
	dir, err := resolvePackageDir(sources, request)
	if err != nil {
		return frame, err
	}
	frame.dir = dir
	commits, err := c.captureInputs(ctx, sources)
	if err != nil {
		return frame, err
	}
	if request.Descriptor.IsProducingOutputs {
		c.rememberConsumedSnapshot(request.Release.Pkg.Name, request.Release.Pkg.Repository, sources, commits)
	}
	if frame.repositories, err = c.carrySourceInputs(ctx, sources, commits); err != nil {
		return frame, err
	}
	switch request.Descriptor.Inputs {
	case release.InputsOwnOutputs:
		frame.inputs, err = c.resolvePublishInputs(ctx, node, request, sources, dir)
	case release.InputsProviderOutputs:
		frame.inputs, err = c.resolveTaskInputs(ctx, node, request.Release.Pkg.Name)
	}
	return frame, err
}

// carrySourceInputs imports exact source snapshots into the mailbox store.
// The Assignment commit transports them as direct secondary parents; no
// snapshot branch is created after task initialization.
func (c *Coordinator) carrySourceInputs(ctx context.Context,
	sources []Source, commits []string) ([]AssignmentRepository, error) {
	repositories := make([]AssignmentRepository, 0, len(sources))
	for index, source := range sources {
		if err := c.dispatch.Store.ImportCommitFrom(ctx, source.Dir, commits[index]); err != nil {
			return nil, fmt.Errorf("execution: importing the input state of %s: %w", source.Name, err)
		}
		repositories = append(repositories, AssignmentRepository{
			Name: source.Name, Path: source.Path, Snapshot: commits[index],
		})
	}
	return repositories, nil
}

// runTask offers one assignment and waits for its terminal result, settling
// the node slot exactly once whichever way the attempt ends.
func (c *Coordinator) runTask(ctx context.Context, lease *Lease, kind, task string, attempt int,
	dir string, repositories []AssignmentRepository, inputs []AssignmentInput,
	request release.StageRequest) (release.StageOutcome, error) {
	outcome := release.StageOutcome{Node: lease.Node}
	offer, err := c.offerAssignment(ctx, lease, task,
		c.formatAssignment(lease.Node, kind, task, attempt, dir, repositories, inputs, request))
	if err != nil {
		return outcome, err
	}
	defer offer.observer.forget(offer.branch)
	return c.awaitResult(ctx, lease, task, attempt, kind, outcome, offer,
		func(ctx context.Context, outcome release.StageOutcome, reply taskReply, branch string) (release.StageOutcome, error) {
			return c.readTaskOutcome(ctx, task, attempt, outcome, reply.result, reply.commit, branch, request)
		})
}

// resultReader turns one accepted terminal result into what the attempt
// comes to. It is the one step a kind of task decides for itself: a build
// admits its outputs as a package's build outputs and a sweep task merges
// its own, while the wait for the result, the deadline and the withdrawal
// are one protocol whatever the kind.
type resultReader func(ctx context.Context, outcome release.StageOutcome, reply taskReply,
	branch string) (release.StageOutcome, error)

// taskOffer is one assignment already on its node's mailbox: the branch it
// went out on, and the channel the messages that answer it arrive through.
//
// It is a value rather than three results because both waits use all three and
// neither may hold a different combination of them: a task registered for
// replies on one branch and waiting on another would be a task that never
// hears anything.
type taskOffer struct {
	observer *watcher
	replies  <-chan taskReply
	branch   string
	kind     string
	// offered is the assignment's own object id, which every message of the
	// attempt names and which every message this party writes afterwards has
	// to echo back.
	offered string
}

// offerAssignment writes one assignment onto its node's mailbox, making the
// attempt this run's own before anybody can see it: the branch is registered
// with the poller, bound to the assignment and recorded for cleanup before the
// push, so that a node quick enough to answer at once is still heard and a
// push with no known outcome still leaves a ref this run closes.
//
// A failed offer settles the lease here. A provably refused activation frees
// the slot; an ambiguous activation retains the node's capacity until the
// signed attempt can be settled.
func (c *Coordinator) offerAssignment(ctx context.Context, lease *Lease, task string,
	assignment *Assignment) (taskOffer, error) {
	// No new effect after lock loss (§28.6). An assignment is the first thing
	// of an attempt that exists anywhere but in this process, so this is the
	// moment the question has to be asked again.
	if err := c.checkOwnership(ctx); err != nil {
		lease.Release()
		return taskOffer{}, err
	}
	attempt, kind := assignment.Attempt, assignment.Kind
	mailbox := c.mailboxes[lease.Node]
	claimed, err := c.claimReservedAttempt(reservationClaim{
		lease: lease, task: task, kind: kind, attempt: attempt,
	})
	if err != nil {
		lease.Release()
		return taskOffer{}, c.refuseTask(task, lease.Node, attempt, err)
	}
	assignment.Branch = claimed.attempt.branch
	offered, err := mailbox.PrepareAssignmentOnReservation(ctx, assignment, claimed.attempt.oid)
	if err != nil {
		lease.Release()
		return taskOffer{}, c.refuseTask(task, lease.Node, attempt, err)
	}
	notice := &SlotNotice{Header: c.formatOrchestratorHeader(KindSlot, task, attempt,
		lease.Node, claimed.slot.branch), Sequence: claimed.next,
		TaskBranch: claimed.attempt.branch, AssignmentOID: offered}
	slotOID, err := mailbox.PrepareSlotNotice(ctx, notice, claimed.prior)
	if err != nil {
		lease.Release()
		return taskOffer{}, c.refuseTask(task, lease.Node, attempt, err)
	}
	observer := c.watchers[lease.Node]
	offer := taskOffer{observer: observer, replies: observer.watch(assignment.Branch),
		branch: assignment.Branch, kind: kind, offered: offered}
	observer.bind(assignment.Branch, offered, assignment)
	err = mailbox.ActivateReserved(ctx, Activation{
		TaskBranch: assignment.Branch, ReservationOID: claimed.attempt.oid, AssignmentOID: offered,
		SlotBranch: claimed.slot.branch, SlotOldOID: claimed.prior, SlotNewOID: slotOID,
	})
	if err != nil {
		observer.forget(assignment.Branch)
		if errors.Is(err, gitx.ErrPushUnknown) {
			lease.Leak(LeakTransport)
			return taskOffer{}, c.refuseTask(task, lease.Node, attempt,
				fmt.Errorf("the atomic task activation has no known outcome: %w", err))
		}
		lease.Release()
		return taskOffer{}, fmt.Errorf("%w: %w", errOfferNotLanded, err)
	}
	c.recordOwnedRef(ctx, ownedRefStep{node: lease.Node, branch: assignment.Branch,
		oid: offered, parent: claimed.attempt.oid})
	c.commitSlotNotice(ctx, lease.Node, claimed, slotOID)
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
		Str("branch", assignment.Branch).Str("commit", offered).Int("attempt", assignment.Attempt).
		Str("kind", kind).Msg("task assigned")
	return offer, nil
}

// errOfferNotLanded is an assignment the mailbox provably did not take: a
// lease or a server rule refused the create, or it had no known outcome and
// the revocation found no branch. Nothing was placed, so the caller offers
// the task again under a new attempt, exactly as it does after errQueueExpired.
var errOfferNotLanded = errors.New("execution: the assignment never reached the mailbox")

// awaitResult waits for the node to report, for the task deadline, or for the
// run to be interrupted, and settles the slot according to which of the three
// happened.
func (c *Coordinator) awaitResult(ctx context.Context, lease *Lease, task string, attempt int,
	kind string, outcome release.StageOutcome, offer taskOffer,
	read resultReader) (release.StageOutcome, error) {
	deadline := time.NewTimer(c.Timeouts.Task)
	defer deadline.Stop()
	tip := offer.offered
	isQueueSettled := false
	offeredAt, claimedAt := time.Now(), time.Time{}
	for {
		select {
		case reply := <-offer.replies:
			if reply.kind == MessageClaim {
				// The work has started, so the run-time clock starts with it.
				isQueueSettled, tip, claimedAt = true, reply.commit, time.Now()
				deadline.Reset(c.Timeouts.Task)
				continue
			}
			c.rememberTiming(task, resolveQueueTime(offeredAt, claimedAt),
				resolveRunTime(offeredAt, claimedAt))
			lease.Release()
			return read(ctx, outcome, reply, offer.branch)
		case <-deadline.C:
			if !isQueueSettled {
				boundary, err := c.settleQueuedAttempt(ctx, lease, task, attempt, offer)
				if err != nil {
					return outcome, err
				}
				// The branch moved while revocation was attempted. Allow
				// one bounded wait for the watcher to authenticate its reply;
				// an observed tip is not yet a trusted cancellation boundary.
				isQueueSettled, tip = true, boundary
				deadline.Reset(c.Timeouts.Task)
				continue
			}
			return outcome, c.settleAbandonedAttempt(ctx, lease, task, attempt, offer, tip)
		case <-ctx.Done():
			return outcome, c.settleInterruptedAttempt(ctx, lease, task, attempt, kind, offer, tip)
		}
	}
}

// settleQueuedAttempt revokes an assignment nobody claimed. When a changed
// tip wins the lease race, it keeps the assignment as the trusted boundary
// until the watcher authenticates the changed tip.
//
// The error it answers is the sentinel that makes the caller place the task
// again: nothing was executed anywhere, so this is not a failure of the task
// and must not be reported as one.
func (c *Coordinator) settleQueuedAttempt(ctx context.Context, lease *Lease, task string,
	attempt int, offer taskOffer) (string, error) {
	if c.revokeAttempt(ctx, lease.Node, task, attempt, offer.branch, offer.offered) {
		// Provably nothing ran: the ref is gone at the object this run put
		// there, so the node could not have claimed it, and the slot is free.
		lease.Release()
		return "", errQueueExpired
	}
	head, err := c.mailboxes[lease.Node].Reread(ctx, offer.branch)
	if err != nil || head.OID == "" {
		lease.Leak(LeakTransport)
		return "", c.refuseTask(task, lease.Node, attempt, fmt.Errorf(
			"no node claimed this task within %s and the assignment could not be revoked: %w",
			c.Timeouts.Task, err))
	}
	// Reread fetched the moved tip and memoized it. Give it back to the
	// watcher immediately for validation: otherwise Observe skips that
	// unchanged tip and accepted work can time out unheard.
	offer.observer.reconsider(head.Name)
	return offer.offered, nil
}

// readTaskOutcome turns one accepted result into what the executor does with
// it: the exports to merge, the stray writes to report, and the failure to
// fail the package with.
func (c *Coordinator) readTaskOutcome(ctx context.Context, task string, attempt int,
	outcome release.StageOutcome, result Result, commit, branch string,
	request release.StageRequest) (release.StageOutcome, error) {
	outcome, err := c.readReportedOutcome(task, attempt, outcome, result)
	if err != nil {
		return outcome, err
	}
	if !request.Descriptor.IsProducingOutputs {
		c.reportTaskFinished(task, result, outcome)
		return outcome, nil
	}
	if err := c.admitOutputs(ctx, task, producedOutputs{
		node: result.Node, store: c.dispatch.Store,
		commit: commit, manifest: result.Outputs, attempt: attempt,
	}, request); err != nil {
		// A build whose outputs cannot be used is a build that did not
		// satisfy its consumers, so the package fails here rather than
		// somewhere downstream with a puzzle about missing files.
		outcome.FailedPart = release.PartOutputs
		return outcome, err
	}
	c.reportTaskFinished(task, result, outcome)
	return outcome, nil
}

// readReportedOutcome is what every accepted result says about itself,
// whatever kind of work it answered: the exports it produced, the tracked
// files it wrote where nobody expected them, and the failure it reports.
//
// It is separate from the admission because only one kind of task produces an
// output set. A publication consumes the bytes a build already produced and
// describes none of its own, so running it through the admission would be
// asking a publisher for a second, later version of a set this run has already
// admitted, and refusing the publication for not having one.
func (c *Coordinator) readReportedOutcome(task string, attempt int, outcome release.StageOutcome,
	result Result) (release.StageOutcome, error) {
	outcome.Exports = formatOutputs(result.Exports)
	outcome.FailedPart = result.FailedPart
	if result.StrayWrites > 0 {
		c.Log.Warn().Str("run", c.Run).Str("task", task).Str("worker", result.Node).
			Int("files", result.StrayWrites).Str("code", CodeTransportRetained).
			Str("category", CategoryTransportCleanup).
			Msg("the task wrote tracked files outside what it declared, and they are not admitted")
	}
	if result.Status != StatusSucceeded {
		return outcome, c.refuseTask(task, result.Node, attempt, fmt.Errorf(
			"the node reported the %s frame as %s%s%s",
			result.Kind, result.Status, formatFailedPart(result), formatExitStatus(result.Exit)))
	}
	return outcome, nil
}

// formatExitStatus names the exit status a node reported for the command that
// failed its frame, and says nothing for a failure that was not a command's,
// which a node reports as 0.
func formatExitStatus(exit int) string {
	if exit == 0 {
		return ""
	}
	return fmt.Sprintf(" (exit %d)", exit)
}

// reportTaskFinished is the one line a finished task produces: what it was,
// where it ran and what it produced.
func (c *Coordinator) reportTaskFinished(task string, result Result, outcome release.StageOutcome) {
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("worker", result.Node).
		Str("status", result.Status).Int("exports", len(outcome.Exports)).
		Str("os", result.Platform.OS).Str("arch", result.Platform.Arch).Msg("task finished")
}

// formatFailedPart names the rule a task broke when it failed at something
// that was not a command: the word the node reported, and nothing it read.
func formatFailedPart(result Result) string {
	if result.Reason == "" {
		return ""
	}
	return " (" + result.Reason + ")"
}

// refuseTask is the failure one dispatched task reports, with the work it is
// about already named on it.
func (c *Coordinator) refuseTask(task, node string, attempt int, err error) error {
	return NewIdentifiedDiagnostic(Identity{Run: c.Run, Worker: node, Task: task, Attempt: attempt},
		CodeIntegrity, CategoryIntegrity, "%s could not be executed: %w", task, err)
}

// captureInputs takes one prepared state per repository of the package's input
// closure, under the exclusive side of the guard.
//
// The guard is exclusive here and shared by the frames that write: no version
// or syncLock frame may be halfway through a manifest or a lock file while a
// tree is being hashed, or the node would build from a file nobody ever had.
// Nothing is pushed under it, because a guard held across a network call would
// serialise the whole run on the slowest remote.
func (c *Coordinator) captureInputs(ctx context.Context, sources []Source) ([]string, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("the package's input closure names no repository, so there is nothing to build it from")
	}
	c.guard.Lock()
	defer c.guard.Unlock()
	captured := make([]string, 0, len(sources))
	for _, source := range sources {
		commit, err := c.snapshots.capture(ctx, c.dispatch.OpenRepository(source.Dir), source, c.Log)
		if err != nil {
			return nil, err
		}
		captured = append(captured, commit)
	}
	return captured, nil
}

// endpointOf is where one node reads its work, as the configuration named it.
func (c *Coordinator) endpointOf(node string) string {
	for _, link := range c.Links {
		if link.Name == node {
			return link.Endpoint
		}
	}
	return ""
}

// resolvePackageDir is where the package's commands run inside the checkout a
// node materializes: its folder relative to the repository that owns it.
func resolvePackageDir(sources []Source, request release.StageRequest) (string, error) {
	for _, source := range sources {
		if source.Name != request.Release.Pkg.Repository {
			continue
		}
		relative, err := filepath.Rel(source.Dir, request.Dir)
		if err != nil {
			return "", fmt.Errorf("the package folder %s is not inside its repository %s: %w",
				request.Dir, source.Dir, err)
		}
		return filepath.ToSlash(relative), nil
	}
	return "", fmt.Errorf("the package's input closure does not name the repository %q it belongs to",
		request.Release.Pkg.Repository)
}

// formatAssignment is the document one build task travels as.
func (c *Coordinator) formatAssignment(node, kind, task string, attempt int, dir string,
	repositories []AssignmentRepository, inputs []AssignmentInput,
	request release.StageRequest) *Assignment {
	branch := FormatBranch(node, kind, time.Now())
	return &Assignment{
		Header:       c.formatOrchestratorHeader(kind, task, attempt, node, branch),
		Repositories: repositories,
		Package: &AssignmentPackage{
			Name:        request.Release.Pkg.Name,
			Version:     request.Release.Next.String(),
			Repository:  request.Release.Pkg.Repository,
			Dir:         dir,
			SourceDir:   canonicalPackageDir(request.Release.Pkg.Dir),
			OutputRoots: request.Release.Pkg.Space.BuildOutputs,
		},
		Frame: &AssignmentFrame{
			Before:   request.Frame.Before,
			Commands: request.Frame.Commands,
			After:    request.Frame.After,
		},
		Env:             request.Env,
		StaticEnv:       request.StaticEnv,
		Stage:           resolveAssignmentStage(kind, request),
		Exports:         formatExports(request.Release.Outputs),
		Shell:           c.dispatch.Shell(request.Dir),
		Platforms:       request.Release.Pkg.Space.BuildPlatforms,
		Inputs:          inputs,
		Outputs:         resolveDeclaredOutputs(kind, request),
		Permits:         AssignmentPermits{},
		DeadlineSeconds: resolveTaskDeadlineSeconds(c.Timeouts.Task),
		Limits:          c.Limits,
	}
}

func canonicalPackageDir(dir string) string {
	resolved, err := filepath.EvalSymlinks(dir)
	if err == nil {
		return resolved
	}
	return filepath.Clean(dir)
}

// resolveAssignmentStage is the stage an assignment of the generic kind names,
// and nothing for every other kind, whose kind already says what it runs.
func resolveAssignmentStage(kind string, request release.StageRequest) string {
	if kind != KindStage {
		return ""
	}
	return request.Stage
}

// resolveDeclaredOutputs is what a task is expected to produce: a build's
// declared roots, and nothing at all for a publication or a stage that
// produces no output set, which consume the bytes a build already produced
// and capture none of their own. A node asked for them would be describing a
// second, later version of an output set this run has already admitted.
func resolveDeclaredOutputs(kind string, request release.StageRequest) []string {
	if kind == KindPublish || (kind == KindStage && !request.Descriptor.IsProducingOutputs) {
		return nil
	}
	return request.Release.Pkg.Space.BuildOutputs
}

// resolveQueueTime is how long an assignment waited for a node to claim it,
// and zero for one whose claim this run never observed.
func resolveQueueTime(offeredAt, claimedAt time.Time) time.Duration {
	if claimedAt.IsZero() {
		return 0
	}
	return claimedAt.Sub(offeredAt)
}

// resolveRunTime is how long the work took once a node had claimed it, and the
// whole wait for an attempt whose claim was never observed: a duration nobody
// measured is worse than one that is charged to the wrong half.
func resolveRunTime(offeredAt, claimedAt time.Time) time.Duration {
	if claimedAt.IsZero() {
		return time.Since(offeredAt)
	}
	return time.Since(claimedAt)
}

// resolveTaskDeadlineSeconds is the bound every node holds one attempt to,
// stated in the assignment so that the two parties hold the same number.
//
// Every kind carries it, and that is the point. The orchestrator's wait
// decides when it stops expecting an answer; this decides when the work
// actually stops. A node that kept running past the run's own wait would be a
// machine holding a folder, a registry session and a capacity slot for a run
// that has already written the attempt off, and a run whose network went away
// cannot end anything at all: the only party that can is the one the work is
// on (§28.6).
func resolveTaskDeadlineSeconds(wait time.Duration) int {
	return int(wait / time.Second)
}

// watcher polls one endpoint for every attempt this run has in flight there,
// and hands each terminal result to the task waiting for it.
//
// One goroutine owns the mailbox, its memo and its object store, which is what
// lets several tasks share one endpoint without sharing anything mutable.
type watcher struct {
	coordinator *Coordinator
	link        Link
	mailbox     *GitMailbox
	wake        chan struct{}

	// mu guards attempts alone: the loop reads it, and the tasks that come and
	// go write it.
	mu       sync.Mutex
	attempts map[string]*attemptState
}

// attemptState is one dispatched task waiting for its node to report, as the
// poller holds it.
type attemptState struct {
	offered    string
	assignment *Assignment
	replies    chan taskReply
	// isTipForeign marks an attempt whose branch tip was not written by either
	// party, so the line saying so is written once rather than once per poll.
	isTipForeign bool
	// isClaimAccepted marks an attempt whose node has been seen to take the
	// work on, so that reading the chain under a moved tip does not start the
	// run-time clock a second time.
	isClaimAccepted bool
}

// taskReply is one accepted message of an attempt together with the object it
// was read at.
//
// The object id travels beside the document rather than inside it because it
// is not the node's to state: what a result is read at is what the
// orchestrator fetched and proved to be on the branch, and a set of outputs
// admitted at an object a node named would be a set admitted at whatever that
// node pointed to.
//
// The kind distinguishes a claim, which starts the task clock, from its result.
type taskReply struct {
	kind   MessageKind
	result Result
	tip    ChainTip
	commit string
}

// watch registers one branch before it is created, so that a node quick
// enough to answer between the push and the registration is still heard.
//
// The poller must never block on a claim or result while another task on the
// same endpoint waits for its own reply.
func (w *watcher) watch(branch string) <-chan taskReply {
	w.mu.Lock()
	defer w.mu.Unlock()
	waiting := &attemptState{replies: make(chan taskReply, 2)}
	w.attempts[branch] = waiting
	return waiting.replies
}

// bind completes a registration once the assignment is on the branch: until
// the offered object is known, no reply could be accepted anyway.
func (w *watcher) bind(branch, offered string, assignment *Assignment) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if waiting, isWatched := w.attempts[branch]; isWatched {
		waiting.offered, waiting.assignment = offered, assignment
	}
}

// forget drops one branch's registration, which every waiting task does on its
// way out however it ended.
func (w *watcher) forget(branch string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.attempts, branch)
}

// find answers the attempt waiting on one branch, and nil for a branch this
// run has nothing to do with.
func (w *watcher) find(branch string) *attemptState {
	w.mu.Lock()
	defer w.mu.Unlock()
	waiting := w.attempts[branch]
	if waiting == nil || waiting.offered == "" {
		return nil
	}
	return waiting
}

// isWatched reports whether a branch is one this run is waiting on, bound or
// not: a registration made before its push is wanted from the moment it
// exists.
func (w *watcher) isWatched(branch string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, isWatched := w.attempts[branch]
	return isWatched
}

// watchedBranches snapshots only active attempts. Polling exact refs keeps
// inert reservations out of the steady-state poll cost.
func (w *watcher) watchedBranches() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	branches := make([]string, 0, len(w.attempts))
	for branch := range w.attempts {
		branches = append(branches, branch)
	}
	sort.Strings(branches)
	return branches
}

// isIdle reports whether this endpoint has nothing in flight, which is what
// keeps a run that delegates nothing from polling anything.
func (w *watcher) isIdle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.attempts) == 0
}

// reconsider makes a tip read by the task's own deadline path visible to the
// watcher again. The watcher remains the sole goroutine that validates and
// delivers replies, while the wake avoids waiting for its idle poll backoff.
func (w *watcher) reconsider(branch string) {
	w.mailbox.Reconsider(branch)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// serve is the poll loop: one tick, then a wait whose length is what the tick
// found.
func (w *watcher) serve(ctx context.Context) {
	interval := minimumPollInterval
	next := time.NewTimer(interval)
	defer next.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-next.C:
		case <-w.wake:
			next.Stop()
		}
		interval = resolvePollInterval(interval, w.tick(ctx))
		next.Reset(interval)
	}
}

// tick is one poll of the endpoint, and reports whether anything was answered.
//
// A failed poll is logged and swallowed: a mailbox briefly out of reach is the
// ordinary condition of a machine on a network, and the task deadline is what
// decides how long a node that never answers is tolerated.
func (w *watcher) tick(ctx context.Context) bool {
	if w.isIdle() {
		return false
	}
	isProgress := false
	branches := w.watchedBranches()
	for start := 0; start < len(branches); start += gitx.MaxTransportBatch {
		chunk := branches[start:min(start+gitx.MaxTransportBatch, len(branches))]
		heads, err := w.mailbox.ObserveManyExact(ctx, chunk)
		if err != nil {
			if ctx.Err() == nil {
				w.coordinator.Log.Warn().Err(err).Str("worker", w.link.Name).
					Int("branches", len(chunk)).Msg("the mailbox could not be polled")
			}
			continue
		}
		for _, head := range heads {
			if w.inspect(ctx, head) {
				isProgress = true
			}
		}
	}
	return isProgress
}

// inspect reads one moved branch and reports whether it answered an attempt.
//
// A panic while reading it is contained to the branch: what a branch carries
// is written by other machines, so the branch is quarantined, said once, and
// left until it moves, and every other attempt on the endpoint goes on. A
// recovery around the whole poll would fire again on the same branch every
// tick.
//
// The tip is tried first because that is where an attempt's answer is in every
// ordinary run. When the tip carries nothing this attempt can act on, the
// chain below it is read: a mailbox is writable by whoever can push to it, so
// an authentic result can be sitting under a commit somebody else put on top,
// and waiting the deadline out over that would turn one push into a lost
// attempt and a node taken out of the pool.
func (w *watcher) inspect(ctx context.Context, head gitx.RemoteHead) (isAnswered bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			w.mailbox.quarantineBranch(head, fmt.Errorf("reading the branch failed unexpectedly: %v", recovered))
			isAnswered = false
		}
	}()
	waiting := w.find(head.Name)
	if waiting == nil {
		return false
	}
	tip, err := w.mailbox.Inspect(ctx, head)
	if err != nil {
		// The objects are here and this process could not make anything of
		// them, so the branch has not been consumed. The poll's memo is what
		// keeps an unchanged branch from being read twice, and a tip that
		// never moves again would never be offered a second time: one local
		// failure would cost the whole task deadline instead of one tick.
		w.mailbox.Reconsider(head.Name)
		if ctx.Err() == nil {
			w.coordinator.Log.Warn().Err(err).Str("worker", w.link.Name).Str("branch", head.Name).
				Msg("a coordination branch could not be read")
		}
		return false
	}
	if w.acceptReply(ctx, tip, waiting) {
		return true
	}
	return w.searchChainForReply(ctx, head, waiting)
}

// acceptReply hands one step of a chain to the task waiting for it, and
// reports whether it did.
//
// A step this party wrote, a step of another attempt and a step nobody signed
// are one situation from here, which is "no answer yet", and the task deadline
// is what decides how long that is tolerated.
func (w *watcher) acceptReply(ctx context.Context, tip ChainTip, waiting *attemptState) bool {
	if tip.Kind == MessageClaim {
		if waiting.isClaimAccepted {
			return false
		}
		return w.offerClaim(ctx, tip, waiting)
	}
	if tip.Kind != MessageResult {
		w.coordinator.Log.Debug().Str("worker", w.link.Name).Str("branch", tip.Branch).
			Str("message", string(tip.Kind)).Msg("the attempt moved on")
		return false
	}
	result, reason := w.readResult(ctx, tip, waiting)
	if reason != "" {
		w.reportRejectedReply(tip, reason)
		return false
	}
	w.coordinator.recordOwnedRef(ctx, ownedRefStep{node: w.link.Name, branch: tip.Branch,
		oid: tip.OID, parent: tip.PreviousOID})
	waiting.replies <- taskReply{kind: MessageResult, result: result, tip: tip, commit: tip.OID}
	w.forget(tip.Branch)
	return true
}

// searchChainForReply looks under a tip that answered nothing for the step
// that does, and says once per branch that it had to.
//
// The warning is once because the condition is one push and the consequence is
// one line: an operator reading it learns that somebody with write access to
// the mailbox put a commit on an attempt's branch, which is worth knowing and
// is not worth repeating every poll.
func (w *watcher) searchChainForReply(ctx context.Context, head gitx.RemoteHead, waiting *attemptState) bool {
	ancestors, err := w.mailbox.InspectChain(ctx, head)
	if err != nil {
		if ctx.Err() == nil {
			w.coordinator.Log.Debug().Err(err).Str("worker", w.link.Name).Str("branch", head.Name).
				Msg("the chain under the branch tip could not be read")
		}
		return false
	}
	for _, ancestor := range ancestors {
		if !w.acceptReply(ctx, ancestor, waiting) {
			continue
		}
		if !waiting.isTipForeign {
			waiting.isTipForeign = true
			w.coordinator.Log.Warn().Str("worker", w.link.Name).Str("branch", head.Name).
				Str("commit", head.OID).Str("reason", string(ReasonChain)).
				Str("code", CodeAuthority).Str("category", CategoryAuthority).
				Msg("the attempt was answered below a branch tip this run did not write")
		}
		return true
	}
	return false
}

// offerClaim tells the task waiting on a branch that its node has taken the
// work on, and leaves the attempt registered: the claim is the beginning of
// the attempt rather than the end of it.
//
// It matters for one reason, and it is the reason §28.2 states separately from
// everything else about capacity: the run's wait for an attempt has to measure
// the work rather than the queue. An assignment sitting in a mailbox behind
// another run's task has not started, so a wait that began when this run
// started waiting would abandon a task that never ran, hold a slot nobody was
// using and take a perfectly healthy node out of the pool.
func (w *watcher) offerClaim(ctx context.Context, tip ChainTip, waiting *attemptState) bool {
	if reason := w.readClaim(ctx, tip, waiting); reason != "" {
		w.reportRejectedReply(tip, reason)
		return false
	}
	waiting.isClaimAccepted = true
	w.coordinator.recordOwnedRef(ctx, ownedRefStep{node: w.link.Name, branch: tip.Branch,
		oid: tip.OID, parent: tip.PreviousOID})
	waiting.replies <- taskReply{kind: MessageClaim, tip: tip, commit: tip.OID}
	w.coordinator.Log.Debug().Str("worker", w.link.Name).Str("branch", tip.Branch).
		Str("commit", tip.OID).Msg("the node claimed the work")
	return true
}

// readClaim verifies one claim and answers the reason it is not this
// attempt's. Nothing of its contents is used: what the claim says is that the
// work has started, and the object it was read at is what the run remembers.
func (w *watcher) readClaim(ctx context.Context, tip ChainTip, waiting *attemptState) RejectReason {
	document, err := w.mailbox.Read(ctx, tip, w.coordinator.Limits.MaxManifestBytes)
	if err != nil {
		if reason := RejectionReason(err); reason != "" {
			return reason
		}
		return ReasonUnreadable
	}
	var claim Claim
	if err := json.Unmarshal(document, &claim); err != nil {
		return ReasonUnreadable
	}
	if reason := CheckHeader(claim.Header,
		Binding{Node: w.link.Name, Branch: tip.Branch}, time.Now()); reason != "" {
		return reason
	}
	offered := waiting.assignment.Header
	if !IsTransitionLegal(tip.Previous, MessageClaim, PartyWorker) ||
		claim.Assignment != waiting.offered || tip.PreviousOID != waiting.offered ||
		claim.Kind != offered.Kind ||
		claim.Run != offered.Run || claim.Task != offered.Task ||
		claim.Attempt != offered.Attempt || claim.Generation != offered.Generation ||
		claim.PlanDigest != offered.PlanDigest {
		return ReasonReplay
	}
	return ""
}

// reportRejectedReply writes the one line a refused reply produces: the branch,
// the object and the reason, and never what the message said.
func (w *watcher) reportRejectedReply(tip ChainTip, reason RejectReason) {
	w.coordinator.Log.Warn().Str("worker", w.link.Name).Str("branch", tip.Branch).
		Str("commit", tip.OID).Str("reason", string(reason)).Str("code", CodeAuthority).
		Str("category", CategoryAuthority).Msg("result rejected")
}

// readResult verifies one reply and answers the result it carries or the
// reason it is not this attempt's.
func (w *watcher) readResult(ctx context.Context, tip ChainTip, waiting *attemptState) (Result, RejectReason) {
	document, err := w.mailbox.Read(ctx, tip, w.coordinator.Limits.MaxManifestBytes)
	if err != nil {
		if reason := RejectionReason(err); reason != "" {
			// A message this node really wrote and this protocol refuses is a
			// decision rather than a mishap: reading it again would reach the
			// same one every tick until the deadline.
			return Result{}, reason
		}
		w.mailbox.Reconsider(tip.Branch)
		return Result{}, ReasonUnreadable
	}
	var result Result
	if err := json.Unmarshal(document, &result); err != nil {
		return Result{}, ReasonUnreadable
	}
	if reason := checkTaskResult(result, w.link.Name, tip, waiting); reason != "" {
		return Result{}, reason
	}
	return result, ""
}

// readReady verifies one publisher's ready message and answers it, or the
// reason it is not this attempt's.
//
// It is held to the rules a result is held to plus the chain's own: a ready
// may only follow the claim of this very attempt, so the object it names as
// its claim has to be the object it was written on top of. That is what keeps
// an authentic ready of one attempt from being replayed onto another.
func (w *watcher) readReady(ctx context.Context, tip ChainTip, waiting *attemptState) (Ready, RejectReason) {
	document, err := w.mailbox.Read(ctx, tip, w.coordinator.Limits.MaxManifestBytes)
	if err != nil {
		if reason := RejectionReason(err); reason != "" {
			return Ready{}, reason
		}
		return Ready{}, ReasonUnreadable
	}
	var ready Ready
	if err := json.Unmarshal(document, &ready); err != nil {
		return Ready{}, ReasonUnreadable
	}
	if reason := CheckHeader(ready.Header, Binding{Node: w.link.Name, Branch: tip.Branch}, time.Now()); reason != "" {
		return Ready{}, reason
	}
	if !IsTransitionLegal(tip.Previous, MessageReady, PartyWorker) {
		return Ready{}, ReasonChain
	}
	offered := waiting.assignment.Header
	if ready.Assignment != waiting.offered || ready.Claim != tip.PreviousOID ||
		ready.Kind != offered.Kind ||
		ready.Run != offered.Run || ready.Task != offered.Task ||
		ready.Attempt != offered.Attempt || ready.Generation != offered.Generation ||
		ready.PlanDigest != offered.PlanDigest {
		return Ready{}, ReasonReplay
	}
	return ready, ""
}

// checkTaskResult holds a node's reply to the rules every message is held to,
// plus the ones only the orchestrator can ask: that the reply answers the
// assignment this run offered, on the chain a worker could have written it on,
// and under the ownership this run holds.
func checkTaskResult(result Result, node string, tip ChainTip, waiting *attemptState) RejectReason {
	if reason := CheckHeader(result.Header, Binding{Node: node, Branch: tip.Branch}, time.Now()); reason != "" {
		return reason
	}
	if !IsTransitionLegal(tip.Previous, MessageResult, PartyWorker) {
		return ReasonChain
	}
	offered := waiting.assignment.Header
	if result.Assignment != waiting.offered || result.Kind != offered.Kind || result.Run != offered.Run ||
		result.Task != offered.Task || result.Attempt != offered.Attempt ||
		result.Generation != offered.Generation || result.PlanDigest != offered.PlanDigest {
		return ReasonReplay
	}
	return ""
}
