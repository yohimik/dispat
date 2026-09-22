// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The orchestrator's side of one dispatched build.
//
// A build stage leaves this process as three things and comes back as one. It
// leaves as a prepared input state, a placement and a signed assignment; it
// comes back as a result the run believes because it is bound to the
// assignment this run offered, on the branch this run created, signed with
// this run's secret. Everything between the two is somebody else's machine.
//
// Waiting is one goroutine per endpoint rather than one per task. A run of
// twenty packages on two nodes polls twice per tick, not twenty times: the
// poll already lists every branch addressed to a node, so the only thing a
// waiting task adds is a channel to be told on.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	// Store is the object store a reported result and its outputs are read
	// from, and the one a relay is pushed out of. It is the repository the run
	// was started in, which is where every mailbox of the run fetches.
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
func (c *Coordinator) Start(ctx context.Context, dispatch Dispatch) {
	watched, stop := context.WithCancel(ctx)
	c.stopWatching = stop
	c.dispatch = dispatch
	c.snapshots = newSnapshots()
	c.outputs = newOutputRegistry()
	c.preparations = map[string]*preparation{}
	c.offered = map[string]offeredState{}
	c.local = make(chan struct{}, max(dispatch.Concurrency, 1))
	c.watchers = make(map[string]*watcher, len(c.Links))
	for _, link := range c.Links {
		observer := &watcher{coordinator: c, link: link, mailbox: c.mailboxes[link.Name],
			attempts: map[string]*attempt{}}
		c.watchers[link.Name] = observer
		c.watching.Add(1)
		go func() {
			defer c.watching.Done()
			observer.serve(watched)
		}()
	}
	c.Log.Debug().Int("nodes", len(c.Links)).Int("concurrency", max(dispatch.Concurrency, 1)).
		Str("run", c.Run).Msg("dispatch opened")
}

// Guard brackets a frame this node keeps for itself.
//
// Two things are taken and both are given back together. The shared side of
// the snapshot guard, so that a dispatch cannot capture a working tree while a
// version or syncLock frame is writing into it; and one of this node's own
// slots, so that the frames the orchestrator still runs are bounded by the
// capacity its configuration states rather than by the size of the plan.
func (c *Coordinator) Guard(ctx context.Context, stage string) (func(), error) {
	select {
	case c.local <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a local slot to run the %s stage: %w", stage, ctx.Err())
	}
	c.guard.RLock()
	c.Log.Trace().Str("stage", stage).Str("run", c.Run).Msg("stage holds the shared snapshot guard")
	return func() {
		c.guard.RUnlock()
		<-c.local
	}, nil
}

// Build places one package's build frame and runs it where it was placed.
//
// The providers this run does not release come first, before a node slot is
// taken: a preparation needs a slot of its own, and a consumer holding one
// while it waits for a preparation would be a consumer waiting for capacity it
// is itself occupying. What it does hold meanwhile is the run-wide build slot
// the task graph gave it, which is what keeps the preparations inside the
// build budget rather than beside it.
//
// The placement comes next because it decides what the rest of the work is.
// A frame placed on this machine needs no input state prepared, nothing
// offered to a mailbox and nothing fetched back: it runs against the checkout
// the run was started in. A frame placed on a node needs all three.
func (c *Coordinator) Build(ctx context.Context, request release.StageRequest,
	here release.LocalFrame) (release.StageOutcome, error) {
	task := request.Release.Pkg.Name + ":" + request.Stage
	if err := c.prepareProviderOutputs(ctx, task, request); err != nil {
		return release.StageOutcome{FailedPart: release.PartInputs}, err
	}
	space := request.Release.Pkg.Space
	placement := ResolveStagePlacement(request.Stage,
		space.RunOnly.ResolveBuild(), len(space.LoginScript) > 0)
	lease, err := c.Pool.Acquire(ctx, space.BuildPlatforms, placement)
	if err != nil {
		return release.StageOutcome{}, c.refuseTask(task, "", err)
	}
	if lease.IsLocal {
		return c.buildHere(ctx, lease, task, request, here)
	}
	return c.dispatchBuild(ctx, lease, KindBuild, task, request)
}

// dispatchBuild prepares one package's input state, offers its build frame to
// the node the pool chose and waits for that node to report.
//
// The kind is the assignment's rather than the frame's: a build this run
// releases and a build of a provider it does not are the same frame executed
// under the same rules, and the word is what tells a reader of the mailbox,
// of the branch names and of the log which of the two it is looking at.
func (c *Coordinator) dispatchBuild(ctx context.Context, lease *Lease, kind, task string,
	request release.StageRequest) (release.StageOutcome, error) {
	sources := c.dispatch.Sources(request.Release.Pkg.Name)
	dir, err := resolvePackageDir(sources, request)
	if err != nil {
		lease.Release()
		return release.StageOutcome{}, c.refuseTask(task, lease.Node, err)
	}
	commits, err := c.captureInputs(ctx, sources)
	if err != nil {
		lease.Release()
		return release.StageOutcome{}, c.refuseTask(task, lease.Node, err)
	}
	c.rememberConsumedSnapshot(request.Release.Pkg.Name, request.Release.Pkg.Repository, sources, commits)
	repositories, err := c.offerInputs(ctx, lease.Node, sources, commits)
	if err != nil {
		lease.Release()
		return release.StageOutcome{}, c.refuseTask(task, lease.Node, err)
	}
	inputs, err := c.resolveTaskInputs(ctx, lease.Node, request.Release.Pkg.Name)
	if err != nil {
		lease.Release()
		return release.StageOutcome{}, c.refuseTask(task, lease.Node, err)
	}
	return c.runTask(ctx, lease, kind, task, dir, repositories, inputs, request)
}

// runTask offers one assignment and waits for its terminal result, settling
// the node slot exactly once whichever way the attempt ends.
func (c *Coordinator) runTask(ctx context.Context, lease *Lease, kind, task, dir string,
	repositories []AssignmentRepository, inputs []AssignmentInput,
	request release.StageRequest) (release.StageOutcome, error) {
	outcome := release.StageOutcome{Node: lease.Node}
	offer, err := c.offerTask(ctx, lease, kind, task, dir, repositories, inputs, request)
	if err != nil {
		return outcome, err
	}
	defer offer.observer.forget(offer.branch)
	return c.awaitResult(ctx, lease, task, outcome, offer.replies, offer.branch, request)
}

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
}

// offerTask writes one assignment onto its node's mailbox and registers the
// attempt with the poller before the push, so that a node quick enough to
// answer between the two is still heard.
//
// A failed offer settles the lease here: nothing was placed anywhere, so the
// slot is free rather than held by an attempt that never existed.
func (c *Coordinator) offerTask(ctx context.Context, lease *Lease, kind, task, dir string,
	repositories []AssignmentRepository, inputs []AssignmentInput,
	request release.StageRequest) (taskOffer, error) {
	assignment := c.formatAssignment(lease.Node, kind, task, dir, repositories, inputs, request)
	observer := c.watchers[lease.Node]
	replies := observer.watch(assignment.Branch)
	offered, err := c.mailboxes[lease.Node].Assign(ctx, assignment)
	if err != nil {
		observer.forget(assignment.Branch)
		lease.Release()
		return taskOffer{}, c.refuseTask(task, lease.Node, err)
	}
	c.recordOwnedRef(lease.Node, assignment.Branch, offered)
	observer.bind(assignment.Branch, offered, assignment)
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
		Str("branch", assignment.Branch).Str("commit", offered).Int("attempt", assignment.Attempt).
		Str("kind", kind).Msg("task assigned")
	return taskOffer{observer: observer, replies: replies, branch: assignment.Branch}, nil
}

// awaitResult waits for the node to report, for the task deadline, or for the
// run to be interrupted, and settles the slot according to which of the three
// happened.
func (c *Coordinator) awaitResult(ctx context.Context, lease *Lease, task string,
	outcome release.StageOutcome, replies <-chan taskReply, branch string,
	request release.StageRequest) (release.StageOutcome, error) {
	deadline := time.NewTimer(c.Timeouts.Task)
	defer deadline.Stop()
	select {
	case reply := <-replies:
		lease.Release()
		return c.readTaskOutcome(ctx, task, outcome, reply.result, reply.commit, branch, request)
	case <-deadline.C:
		// The work may still be running on that machine, so the slot stays
		// where it is and the node leaves the pool.
		lease.Leak()
		return outcome, c.refuseTask(task, lease.Node, fmt.Errorf(
			"the node did not report within %s: the attempt is abandoned and the node is not used again by this run",
			c.Timeouts.Task))
	case <-ctx.Done():
		lease.Leak()
		return outcome, fmt.Errorf("waiting for %s on %s: %w", task, lease.Node, ctx.Err())
	}
}

// readTaskOutcome turns one accepted result into what the executor does with
// it: the exports to merge, the stray writes to report, and the failure to
// fail the package with.
func (c *Coordinator) readTaskOutcome(ctx context.Context, task string, outcome release.StageOutcome,
	result Result, commit, branch string, request release.StageRequest) (release.StageOutcome, error) {
	outcome, err := c.readReportedOutcome(task, outcome, result)
	if err != nil {
		return outcome, err
	}
	if err := c.admitOutputs(ctx, task, producedOutputs{
		node: result.Node, store: c.dispatch.Store, endpoint: c.endpointOf(result.Node),
		branch: branch, commit: commit, manifest: result.Outputs,
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
func (c *Coordinator) readReportedOutcome(task string, outcome release.StageOutcome,
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
		return outcome, c.refuseTask(task, result.Node, fmt.Errorf(
			"the node reported the %s frame as %s%s (exit %d)",
			result.Kind, result.Status, formatFailedPart(result), result.Exit))
	}
	return outcome, nil
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
func (c *Coordinator) refuseTask(task, node string, err error) error {
	return NewIdentifiedDiagnostic(Identity{Run: c.Run, Worker: node, Task: task, Attempt: 1},
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

// offerInputs puts every prepared state on a branch of the node's endpoint and
// answers the checkouts the assignment names.
//
// A state that is already on that endpoint under this run's own branch is not
// pushed again: a run of twenty packages whose working tree does not change
// between dispatches offers one source branch, not twenty.
func (c *Coordinator) offerInputs(ctx context.Context, node string, sources []Source, commits []string) ([]AssignmentRepository, error) {
	offered := make([]AssignmentRepository, 0, len(sources))
	for index, source := range sources {
		branch, err := c.offerInput(ctx, node, source, commits[index])
		if err != nil {
			return nil, err
		}
		offered = append(offered, AssignmentRepository{
			Name: source.Name, Path: source.Path, Snapshot: commits[index], Branch: branch,
		})
	}
	return offered, nil
}

// offerInput pushes one prepared state to one node, create-only, and answers
// the immutable branch it now sits on.
//
// The push comes out of the repository the state was captured in rather than
// out of the release's own store, because in a composed workspace those are
// different object stores and only the first one holds the objects.
func (c *Coordinator) offerInput(ctx context.Context, node string, source Source, commit string) (string, error) {
	c.offers.Lock()
	defer c.offers.Unlock()
	key := node + "\x00" + source.Dir
	if state, isOffered := c.offered[key]; isOffered && state.commit == commit {
		return state.branch, nil
	}
	branch := FormatBranch(node, KindSnapshot, time.Now())
	if err := c.dispatch.OpenRepository(source.Dir).PushCreate(ctx, c.endpointOf(node), commit, branch); err != nil {
		return "", fmt.Errorf("offering the input state of %s: %w", source.Dir, err)
	}
	c.recordOwnedRef(node, branch, commit)
	c.offered[key] = offeredState{commit: commit, branch: branch}
	c.Log.Debug().Str("worker", node).Str("repository", source.Name).Str("branch", branch).
		Str("commit", commit).Str("run", c.Run).Msg("input state pushed")
	return branch, nil
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
func (c *Coordinator) formatAssignment(node, kind, task, dir string,
	repositories []AssignmentRepository, inputs []AssignmentInput,
	request release.StageRequest) *Assignment {
	branch := FormatBranch(node, kind, time.Now())
	return &Assignment{
		Header: Header{
			Protocol: ProtocolVersion, Kind: kind, Run: c.Run,
			PlanDigest: c.PlanDigest, Task: task, Attempt: 1,
			Generation: c.Generation, Node: node, Branch: branch,
			IssuedAt: time.Now().UTC().Format(time.RFC3339),
		},
		Repositories: repositories,
		Package: &AssignmentPackage{
			Name:       request.Release.Pkg.Name,
			Version:    request.Release.Next.String(),
			Repository: request.Release.Pkg.Repository,
			Dir:        dir,
		},
		Frame: &AssignmentFrame{
			Before:   request.Frame.Before,
			Commands: request.Frame.Commands,
			After:    request.Frame.After,
		},
		Env:             request.Env,
		StaticEnv:       request.StaticEnv,
		Exports:         formatExports(request.Release.Outputs),
		Shell:           c.dispatch.Shell(request.Dir),
		Platforms:       request.Release.Pkg.Space.BuildPlatforms,
		Inputs:          inputs,
		Outputs:         resolveDeclaredOutputs(kind, request),
		Permits:         AssignmentPermits{Publish: kind == KindPublish},
		DeadlineSeconds: resolveTaskDeadlineSeconds(kind, c.Timeouts.Task),
		Limits:          c.Limits,
	}
}

// resolveDeclaredOutputs is what a task is expected to produce: a build's
// declared roots, and nothing at all for a publication, which consumes the
// bytes a build already produced and captures none of its own. A publisher
// asked for them would be describing a second, later version of an output set
// this run has already admitted.
func resolveDeclaredOutputs(kind string, request release.StageRequest) []string {
	if kind == KindPublish {
		return nil
	}
	return request.Release.Pkg.Space.BuildOutputs
}

// resolveTaskDeadlineSeconds is the bound a node holds one attempt to.
//
// Only a publication carries one in this build. A build that outlives the
// run's own wait is abandoned and costs a machine some work; a publisher that
// outlived it would be a publisher acting on an authorization the run has
// already written off, which is the one thing §28.6 asks a node to prevent by
// itself. Gate 12 extends this to every kind.
func resolveTaskDeadlineSeconds(kind string, wait time.Duration) int {
	if kind != KindPublish {
		return 0
	}
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

	// mu guards attempts alone: the loop reads it, and the tasks that come and
	// go write it.
	mu       sync.Mutex
	attempts map[string]*attempt
}

// attempt is one dispatched task waiting for its node to report.
type attempt struct {
	offered    string
	assignment *Assignment
	replies    chan taskReply
	// isAuthorized marks a publication this run has already answered, and is
	// what makes the authorization single use: it is set before the message is
	// written, so a second ready on the same attempt is refused whatever
	// became of the first push. Written and read by the task that owns the
	// attempt, which is the only party that authorizes anything.
	isAuthorized bool
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
// The kind is carried because two messages answer a publication and they mean
// opposite things: a ready is the node asking to be let through, and a result
// is the node saying it is over.
type taskReply struct {
	kind   MessageKind
	result Result
	ready  Ready
	tip    ChainTip
	commit string
}

// watch registers one branch before it is created, so that a node quick
// enough to answer between the push and the registration is still heard.
//
// Two slots, because a publication answers twice: the ready that waits for an
// authorization and the result that ends the attempt. The poller must never
// block on handing one over, since the goroutine that would be blocked is the
// one every other attempt on that endpoint is waiting for.
func (w *watcher) watch(branch string) <-chan taskReply {
	w.mu.Lock()
	defer w.mu.Unlock()
	waiting := &attempt{replies: make(chan taskReply, 2)}
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
func (w *watcher) find(branch string) *attempt {
	w.mu.Lock()
	defer w.mu.Unlock()
	waiting := w.attempts[branch]
	if waiting == nil || waiting.offered == "" {
		return nil
	}
	return waiting
}

// isIdle reports whether this endpoint has nothing in flight, which is what
// keeps a run that delegates nothing from polling anything.
func (w *watcher) isIdle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.attempts) == 0
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
	heads, err := w.mailbox.Observe(ctx, FormatBranchPattern(w.link.Name))
	if err != nil {
		if ctx.Err() == nil {
			w.coordinator.Log.Warn().Err(err).Str("worker", w.link.Name).
				Msg("the mailbox could not be polled")
		}
		return false
	}
	isProgress := false
	for _, head := range heads {
		if w.inspect(ctx, head) {
			isProgress = true
		}
	}
	return isProgress
}

// inspect reads one moved branch and reports whether it answered an attempt.
func (w *watcher) inspect(ctx context.Context, head gitx.RemoteHead) bool {
	waiting := w.find(head.Name)
	if waiting == nil {
		return false
	}
	w.coordinator.recordOwnedRef(w.link.Name, head.Name, head.OID)
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
	if tip.Kind == MessageReady {
		return w.offerReady(ctx, tip, waiting)
	}
	if tip.Kind != MessageResult {
		w.coordinator.Log.Debug().Str("worker", w.link.Name).Str("branch", tip.Branch).
			Str("message", string(tip.Kind)).Msg("the attempt moved on")
		return false
	}
	result, reason := w.readResult(ctx, tip, waiting)
	if reason != "" {
		// A reply nobody signed, one bound to another attempt and a node that
		// has not answered yet are one situation from here, and the task
		// deadline is what decides how long that is tolerated.
		w.reportRejectedReply(tip, reason)
		return false
	}
	waiting.replies <- taskReply{kind: MessageResult, result: result, tip: tip, commit: tip.OID}
	w.forget(head.Name)
	return true
}

// offerReady hands one publisher's ready message to the task waiting to
// authorize it, and leaves the attempt registered: the branch has one more
// message to come, and the result of it is what ends the attempt.
func (w *watcher) offerReady(ctx context.Context, tip ChainTip, waiting *attempt) bool {
	ready, reason := w.readReady(ctx, tip, waiting)
	if reason != "" {
		w.reportRejectedReply(tip, reason)
		return false
	}
	waiting.replies <- taskReply{kind: MessageReady, ready: ready, tip: tip, commit: tip.OID}
	return true
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
func (w *watcher) readResult(ctx context.Context, tip ChainTip, waiting *attempt) (Result, RejectReason) {
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
func (w *watcher) readReady(ctx context.Context, tip ChainTip, waiting *attempt) (Ready, RejectReason) {
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
func checkTaskResult(result Result, node string, tip ChainTip, waiting *attempt) RejectReason {
	if reason := CheckHeader(result.Header, Binding{Node: node, Branch: tip.Branch}, time.Now()); reason != "" {
		return reason
	}
	if !IsTransitionLegal(tip.Previous, MessageResult, PartyWorker) {
		return ReasonChain
	}
	offered := waiting.assignment.Header
	if result.Assignment != waiting.offered || result.Run != offered.Run ||
		result.Task != offered.Task || result.Attempt != offered.Attempt ||
		result.Generation != offered.Generation || result.PlanDigest != offered.PlanDigest {
		return ReasonReplay
	}
	return ""
}
