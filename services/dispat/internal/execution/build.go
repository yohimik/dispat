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

// Publish runs one package's publish frame on a node under an explicit
// authorization. Publication stays on the orchestrator in this build, so
// nothing calls it: the method is here because Remotex is the whole seam and a
// half-declared seam is one a later gate would have to reshape.
func (c *Coordinator) Publish(context.Context, release.StageRequest, func(context.Context) error) (release.StageOutcome, error) {
	return release.StageOutcome{}, NewIdentifiedDiagnostic(Identity{Run: c.Run},
		CodeConfiguration, CategoryConfiguration,
		"this build does not delegate publication: the publish stage runs on the orchestrator")
}

// Build prepares one package's input state, places its build frame on a node
// and waits for that node to report.
func (c *Coordinator) Build(ctx context.Context, request release.StageRequest) (release.StageOutcome, error) {
	task := request.Release.Pkg.Name + ":" + request.Stage
	sources := c.dispatch.Sources(request.Release.Pkg.Name)
	dir, err := resolvePackageDir(sources, request)
	if err != nil {
		return release.StageOutcome{}, c.refuseTask(task, "", err)
	}
	commits, err := c.captureInputs(ctx, sources)
	if err != nil {
		return release.StageOutcome{}, c.refuseTask(task, "", err)
	}
	lease, err := c.Pool.Acquire(ctx, request.Release.Pkg.Space.BuildPlatforms)
	if err != nil {
		return release.StageOutcome{}, c.refuseTask(task, "", err)
	}
	repositories, err := c.offerInputs(ctx, lease.Node, sources, commits)
	if err != nil {
		lease.Release()
		return release.StageOutcome{}, c.refuseTask(task, lease.Node, err)
	}
	return c.runTask(ctx, lease, task, dir, repositories, request)
}

// runTask offers one assignment and waits for its terminal result, settling
// the node slot exactly once whichever way the attempt ends.
func (c *Coordinator) runTask(ctx context.Context, lease *Lease, task, dir string,
	repositories []AssignmentRepository, request release.StageRequest) (release.StageOutcome, error) {
	outcome := release.StageOutcome{Node: lease.Node}
	assignment := c.formatAssignment(lease.Node, task, dir, repositories, request)
	observer := c.watchers[lease.Node]
	replies := observer.watch(assignment.Branch)
	offered, err := c.mailboxes[lease.Node].Assign(ctx, assignment)
	if err != nil {
		observer.forget(assignment.Branch)
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, err)
	}
	c.recordOwnedRef(lease.Node, assignment.Branch, offered)
	observer.bind(assignment.Branch, offered, assignment)
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
		Str("branch", assignment.Branch).Str("commit", offered).Int("attempt", assignment.Attempt).
		Msg("task assigned")
	defer observer.forget(assignment.Branch)
	return c.awaitResult(ctx, lease, task, outcome, replies)
}

// awaitResult waits for the node to report, for the task deadline, or for the
// run to be interrupted, and settles the slot according to which of the three
// happened.
func (c *Coordinator) awaitResult(ctx context.Context, lease *Lease, task string,
	outcome release.StageOutcome, replies <-chan Result) (release.StageOutcome, error) {
	deadline := time.NewTimer(c.Timeouts.Task)
	defer deadline.Stop()
	select {
	case result := <-replies:
		lease.Release()
		return c.readTaskOutcome(task, lease.Node, outcome, result)
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
func (c *Coordinator) readTaskOutcome(task, node string, outcome release.StageOutcome,
	result Result) (release.StageOutcome, error) {
	outcome.Exports = formatOutputs(result.Exports)
	outcome.FailedPart = result.FailedPart
	if result.StrayWrites > 0 {
		c.Log.Warn().Str("run", c.Run).Str("task", task).Str("worker", node).
			Int("files", result.StrayWrites).Str("code", CodeTransportRetained).
			Str("category", CategoryTransportCleanup).
			Msg("the task wrote tracked files outside what it declared, and they are not admitted")
	}
	if result.Status != StatusSucceeded {
		return outcome, c.refuseTask(task, node, fmt.Errorf(
			"the node reported the %s frame as %s (exit %d)", result.Kind, result.Status, result.Exit))
	}
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("worker", node).
		Str("status", result.Status).Int("exports", len(outcome.Exports)).
		Str("os", result.Platform.OS).Str("arch", result.Platform.Arch).Msg("task finished")
	return outcome, nil
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
func (c *Coordinator) formatAssignment(node, task, dir string,
	repositories []AssignmentRepository, request release.StageRequest) *Assignment {
	branch := FormatBranch(node, KindBuild, time.Now())
	return &Assignment{
		Header: Header{
			Protocol: ProtocolVersion, Kind: KindBuild, Run: c.Run,
			PlanDigest: c.PlanDigest, Task: task, Attempt: 1,
			Generation: c.Generation, Node: node, Branch: branch,
			IssuedAt: time.Now().UTC().Format(time.RFC3339),
		},
		Repositories: repositories,
		Package: &AssignmentPackage{
			Name:       request.Release.Pkg.Name,
			Repository: request.Release.Pkg.Repository,
			Dir:        dir,
		},
		Frame: &AssignmentFrame{
			Before:   request.Frame.Before,
			Commands: request.Frame.Commands,
			After:    request.Frame.After,
		},
		Env:       request.Env,
		StaticEnv: request.StaticEnv,
		Exports:   formatExports(request.Release.Outputs),
		Shell:     c.dispatch.Shell(request.Dir),
		Platforms: request.Release.Pkg.Space.BuildPlatforms,
		Limits:    c.Limits,
	}
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
	replies    chan Result
}

// watch registers one branch before it is created, so that a node quick
// enough to answer between the push and the registration is still heard.
func (w *watcher) watch(branch string) <-chan Result {
	w.mu.Lock()
	defer w.mu.Unlock()
	waiting := &attempt{replies: make(chan Result, 1)}
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
		if ctx.Err() == nil {
			w.coordinator.Log.Warn().Err(err).Str("worker", w.link.Name).Str("branch", head.Name).
				Msg("a coordination branch could not be read")
		}
		return false
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
		w.coordinator.Log.Warn().Str("worker", w.link.Name).Str("branch", tip.Branch).
			Str("commit", tip.OID).Str("reason", string(reason)).Str("code", CodeAuthority).
			Str("category", CategoryAuthority).Msg("result rejected")
		return false
	}
	waiting.replies <- result
	w.forget(head.Name)
	return true
}

// readResult verifies one reply and answers the result it carries or the
// reason it is not this attempt's.
func (w *watcher) readResult(ctx context.Context, tip ChainTip, waiting *attempt) (Result, RejectReason) {
	document, err := w.mailbox.Read(ctx, tip, w.coordinator.Limits.MaxManifestBytes)
	if err != nil {
		if reason := RejectionReason(err); reason != "" {
			return Result{}, reason
		}
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
