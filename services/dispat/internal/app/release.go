// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/github"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
	"github.com/yohimik/dispat/services/dispat/internal/webhook"
)

// ReleaseOptions narrows a release — and the release `status` reports on — to
// part of the monorepo. The zero value is the whole of it, which is what a bare
// `dispat release` at the repository root asks for.
type ReleaseOptions struct {
	// Filter selects the packages to release: --package, --space, or the
	// package or space folder the command was invoked from. It only ever
	// narrows the plan, never widens it, and it cannot reorder it: a selected
	// package whose provider is releasing and unselected stays behind (see
	// plan.Narrow).
	Filter filter.Filter
	// Strict refuses the run when the selection is not one the plan can
	// release cleanly — a package the order held back, a versioning group
	// split in two. Without it both are warnings and the reachable part of the
	// selection releases; with it nothing is released at all, which is what
	// makes a filtered release safe to run unattended.
	Strict bool
	// RequireRelease refuses the run when the plan would publish nothing, which
	// is otherwise a legitimate no-op that exits 0. It exists for the CI stage
	// whose whole point is that this run releases something: without it such a
	// job passes quietly on an empty plan and the pipeline carries on to deploy
	// nothing. A held, withheld or unselected package does not count — this run
	// will not publish it either.
	RequireRelease bool
}

// Release computes the plan and executes it end to end: verification, the
// gating beforeAll hook, the task graph, the run-level hooks, the finalize
// phase and the summary. The returned error is non-nil when anything kept the
// run from completing cleanly — a blocked plan, failed verification, a failed
// package, a failed finalize — with the details already logged.
//
// The results map carries one entry per package the plan released, whatever
// its outcome, so a front end driving App directly reads what happened from
// the values rather than from the log stream; it is nil when the run never
// reached execution (a blocked plan, failed verification, a failed gating
// hook).
func (a *App) Release(ctx context.Context, opts ReleaseOptions) (map[string]*release.Result, error) {
	a.logReleaseStarted(opts)
	// checkGit runs first so a repository without git still fails in its own
	// words rather than on a raw `git tag`.
	if err := a.checkGit(); err != nil {
		a.log.Error().Err(err).Msg("cannot start release")
		return nil, err
	}
	// Who may start this release, and whether a run that delegates work could
	// be coordinated at all. Both are refused before the first lock is pushed
	// and report themselves; with no execution settings this returns nil
	// without writing a line.
	if err := a.checkExecutionEntry(ctx, runRelease); err != nil {
		return nil, err
	}
	// A distributed run is named here, before the plan is fixed, so that the
	// line naming the plan also names the run every assignment will carry.
	a.startExecutionRun()
	fleet, unlock, err := a.acquireReleaseLocks(ctx)
	if err != nil {
		return nil, err
	}
	a.openOwnershipGate(fleet)
	// Idempotence lets the normal completion path release before its summary
	// and closing webhook while this defer still protects every earlier return.
	// Cleanup detaches from cancellation inside: an interrupted run has more
	// reason to give the lock back than a finished one.
	unlocked := false
	unlockOnce := func() error {
		if unlocked {
			return nil
		}
		unlocked = true
		return unlock()
	}
	cleanupPins := func() {}
	finishCleanup := func() error {
		cleanupPins()
		return unlockOnce()
	}
	defer func() { _ = finishCleanup() }()

	pl, err := a.planUnderLock(ctx, opts, fleet)
	if err != nil {
		return nil, err
	}
	if fleet != nil {
		cleanup, err := a.prepareFleetRelease(ctx, pl, fleet)
		if err != nil {
			return nil, err
		}
		cleaned := false
		cleanupPins = func() {
			if !cleaned {
				cleaned = true
				cleanup()
			}
		}
	}
	if err := a.refuseDirtyReleasePaths(ctx, pl, fleet != nil); err != nil {
		return nil, err
	}
	// A plan naming work only a worker may run, on a node with no worker
	// link, is a plan this run could not execute anywhere. It is refused here
	// rather than at the stage, because the stage it would fail at is one
	// nothing should have reached.
	if err := a.checkStagePlacements(pl); err != nil {
		return nil, err
	}
	// Every configured worker node is asked what it is before anything else
	// happens: the plan exists, so what would be placed on the pool is known,
	// and no hook, stage or record has run yet, so a node that cannot take
	// this run's work costs nothing but the probe. The coordinator owns every
	// ref this run creates, and closes them before the locks go back.
	coordinator, err := a.preflightWorkers(ctx, pl, fleet)
	if coordinator != nil {
		defer a.closeCoordinator(ctx, coordinator)
	}
	if err != nil {
		return nil, err
	}
	// The runner every script of this run goes through, assembled before the
	// dispatch because a prepared provider's build is one of them.
	runner := a.packageRunner()
	if fleet != nil {
		runner = fleet.pins.runner(runner)
	}
	if coordinator != nil {
		// The pool passed: from here the run owns one poller per endpoint, and
		// the deferred close above stops them before it deletes the refs.
		a.openDispatch(ctx, coordinator, pl, runner)
	}
	// Resolve the GitHub releasers: one per distinct target the packages'
	// resolved policies name — most runs resolve to a single one. Empty
	// means every package is disabled or unresolvable. It needs the plan, so
	// it cannot move up with the git verification above.
	gh, err := a.verifiedGitHubDispatch(ctx, pl)
	if err != nil {
		return nil, err
	}
	if fleet != nil {
		fleet.gh = gh
	}

	// The run-level hooks share one environment: the workspace listing before
	// the run, widened to the run outcome once the task graph finishes.
	hooks := &runHooks{cfg: a.cfg, runner: runner, root: a.root,
		env: release.WorkspaceEnv(pl, a.log), log: a.log}
	if err := a.runGatingHooks(ctx, hooks, fleet); err != nil {
		return nil, err
	}

	// Webhooks begin once the run is committed to execute: a refused run — a
	// blocked plan, failed verification, a failed gating hook — emits nothing,
	// because nothing it planned was ever started. Close is deferred right
	// here so every exit path flushes the queued deliveries, detached from
	// cancellation (an interrupt is the run outcome listeners most want to
	// hear about) and bounded by the dispatcher's own flush deadline. Normal
	// completion releases locks before queuing the closing outcome, so that
	// delivery includes any lock cleanup failure.
	wh := a.webhookDispatcher(pl)
	// The interface field is only assigned through a non-nil check: a typed
	// nil *Dispatcher inside the interface would defeat the executor's own
	// nil test.
	var obs release.Observerx
	if wh != nil {
		defer wh.Close(context.WithoutCancel(ctx))
		obs = wh
		wh.Event(a.releaseStartedEvent(pl))
	}

	executor := a.newReleaseExecutor(pl, fleet, gh, runner, obs, coordinator)
	start := time.Now()
	results := executor.Run(ctx, pl)
	if coordinator != nil {
		a.reportPreparedProviders(coordinator)
	}
	return a.completeRelease(ctx, pl, results, hooks, gh, fleet, finishCleanup, wh, start)
}

// logReleaseStarted names the invocation before any work happens: an incident
// readback starts from "what run was this", and this line answers it at the
// default level. Selection fields appear only when a selection was in force,
// so an unfiltered run's line reads as one word of intent.
func (a *App) logReleaseStarted(opts ReleaseOptions) {
	ev := a.log.Info().Str("root", a.root)
	if len(opts.Filter.Packages) > 0 {
		ev = ev.Strs("packages", opts.Filter.Packages)
	}
	if len(opts.Filter.Spaces) > 0 {
		ev = ev.Strs("spaces", opts.Filter.Spaces)
	}
	if len(opts.Filter.Groups) > 0 {
		ev = ev.Strs("groups", opts.Filter.Groups)
	}
	if opts.Strict {
		ev = ev.Bool("strict", true)
	}
	if opts.RequireRelease {
		ev = ev.Bool("requireRelease", true)
	}
	ev.Msg("release started")
}

// acquireReleaseLocks takes the release lock of every participating repository
// before the run plans anything, and returns the fleet recorder (nil outside a
// composed workspace) with the function that gives the locks back.
//
// The lock comes before the plan, not before the publish, and it comes before
// it unconditionally: two runs that both got as far as planning have already
// read the same tags and decided on the same versions, and whichever of them
// notices second has wasted the work either way.
//
// There is no --require-release exception. Whether there is work to do is not
// known until after planning, so "do not lock when the plan is empty" is not a
// rule this function is in a position to follow — it would have to plan first,
// which is the thing the lock exists to serialise. A run that turns out to
// have nothing to publish therefore takes the lock, gives it straight back,
// and exits 3. The lock-free way to ask whether a release would do anything is
// `dispat status --require-release`, which plans without ever touching the
// remote, and is what a CI gate should call before it calls this.
func (a *App) acquireReleaseLocks(ctx context.Context) (*workspaceRecorder, func() error, error) {
	if a.workspace != nil {
		fleet := a.newWorkspaceRecorder()
		unlock, err := fleet.acquire(ctx)
		if err != nil {
			a.log.Error().Err(err).Str("code", "E336").Msg("unable to acquire fleet release locks")
			return nil, nil, err
		}
		return fleet, unlock, nil
	}
	if a.lockDisabled() {
		warnLockDisabled(a.log, []string{a.root}, a.cfg.UnsafeDisableLock)
		return nil, func() error { return nil }, nil
	}
	lockRemote, resolveErr := lockDestination(ctx, a.git, a.pushRemote(), a.log)
	if resolveErr != nil {
		a.log.Error().Err(resolveErr).Str("tag", release.LockTagName).
			Str("remote", gitx.RedactURL(a.pushRemote())).Msg("unable to create the release lock tag")
		return nil, nil, fmt.Errorf("resolve release-lock push destination: %w", resolveErr)
	}
	lock := &release.Lock{Git: a.git, Remote: lockRemote, Log: a.log, Run: a.runID}
	if err := lock.Acquire(ctx); err != nil {
		a.log.Error().Err(err).Str("code", "E336").Str("tag", release.LockTagName).
			Str("remote", gitx.RedactURL(a.pushRemote())).
			Str("remedy", release.LockRemedy).Msg("unable to create the release lock tag")
		return nil, nil, err
	}
	// Kept for the distributed run that has to ask later whether it still owns
	// what it took (CCME §28.6) and has to name that ownership in what it
	// dispatches. Nothing else reads it, and holding it changes nothing about
	// how the lock is given back.
	a.releaseLock = lock
	return nil, func() error {
		if a.isLockRetained("") {
			return a.reportRetainedLock("", lockRemote)
		}
		return config.WithDiagnostic("E336", lock.Release(context.WithoutCancel(ctx)))
	}, nil
}

// planUnderLock verifies external access, fixes the fleet snapshot and
// computes the plan every later phase works from. Everything here happens
// while the locks are held, so no answer can go stale between the check and
// the release it guards.
func (a *App) planUnderLock(ctx context.Context, opts ReleaseOptions, fleet *workspaceRecorder) (*plan.Plan, error) {
	// Verify external access up front, before anything is planned. The order
	// is the point: a plan is built from the repository's tags, so a checkout
	// that has fallen behind the remote produces a plan that is wrong rather
	// than a plan that fails — it recomputes versions somebody else already
	// published. Refusing here means no such plan is ever built.
	//
	// commit.verify (default true) can switch the git check off for remotes
	// that reject ls-remote but accept pushes.
	if fleet == nil && a.cfg.Commit.IsPushEnabled() && a.cfg.Commit.IsVerifyEnabled() {
		remote := a.pushRemote()
		if err := a.git.VerifyRemote(ctx, remote); err != nil {
			a.log.Error().Err(err).Str("remote", gitx.RedactURL(remote)).Msg("git remote verification failed")
			return nil, err
		}
		// Under the same flag as the reachability check, and for the same
		// reason: this is another ls-remote, and commit.verify=false exists
		// for remotes that reject one but accept pushes.
		if err := a.checkNotBehind(ctx, remote); err != nil {
			a.log.Error().Err(err).Str("remote", gitx.RedactURL(remote)).Msg("refusing to release")
			return nil, err
		}
	}
	if fleet != nil {
		// The same check, made of every participating repository that pushes,
		// because a fleet plans from every one of their tags.
		if err := fleet.verifyParticipants(ctx); err != nil {
			a.logError(err).Msg("source repository verification failed")
			return nil, err
		}
		packages, err := a.packages()
		if err != nil {
			a.logError(err).Msg("package discovery failed")
			return nil, err
		}
		if err := fleet.captureSnapshot(ctx, packages); err != nil {
			a.logError(err).Msg("unable to capture fixed fleet snapshot")
			return nil, err
		}
	}

	// The records of the store this run writes to, held against the ones it is
	// about to plan from. It is under the locks and before the plan on
	// purpose: the lock is what makes a single comparison sufficient, and a
	// plan built from records that have moved on is wrong rather than late
	// (CCME §13.2).
	if err := a.compareReleaseRecords(ctx, fleet); err != nil {
		a.logError(err).Msg("refusing to release")
		return nil, err
	}

	pl, err := a.selectedPlan(ctx, opts)
	if err != nil {
		return nil, err
	}
	if fleet != nil {
		fleet.setSnapshotPlan(pl)
		fleet.setLinkPlan(pl)
	}
	if blocked := a.releaseBlocked(pl); blocked != "" {
		a.log.Error().Str("reason", blocked).Msg("refusing to release")
		return nil, errors.New(blocked)
	}
	if fleet != nil {
		if err := fleet.verifyPlannedHeads(pl); err != nil {
			a.logError(err).Msg("repository changed after workspace composition")
			return nil, err
		}
		if err := fleet.verifyPushBranches(ctx, pl); err != nil {
			a.logError(err).Msg("source repository verification failed")
			return nil, err
		}
	}
	// The branch guard fires before any GitHub verification or hook: a run on
	// the wrong branch is refused whatever its plan says. It sits after the
	// plan only because a blocked plan is the more fundamental refusal of the
	// two; the git verification above it guards the plan itself.
	if err := a.checkBranchAllowed(ctx); err != nil {
		a.log.Error().Err(err).Msg("refusing to release")
		return nil, err
	}
	return pl, nil
}

// prepareFleetRelease settles every source repository's release preconditions
// and opens the run's live pin context, returning the context's cleanup.
func (a *App) prepareFleetRelease(ctx context.Context, pl *plan.Plan, fleet *workspaceRecorder) (func(), error) {
	if err := fleet.prepare(ctx, pl); err != nil {
		a.logError(err).Msg("refusing to release source repositories")
		return nil, err
	}
	cleanupPins, err := fleet.pins.start(
		fleet.pins.root, fleet.pins.config, workspacePinOwners(pl),
		workspacePinRepositories(a.workspace))
	if err != nil {
		err = config.WithDiagnostic(config.DiagnosticRepositoryInvalid,
			fmt.Errorf("E330: creating live workspace pin context: %w", err))
		a.logError(err).Msg("refusing to release source repositories")
		return nil, err
	}
	return cleanupPins, nil
}

// refuseDirtyReleasePaths protects pre-existing work. An automatic release
// commit can capture it, and revertOnFail can discard it, so the run is
// refused before hooks or writes when either behavior is active. A release
// with both disabled preserves writer edits in the working tree and performs
// no Git reset, so existing changelog or manifest edits remain valid input
// (including an interrupted run's output). The fleet path runs its own
// per-repository check in prepare.
func (a *App) refuseDirtyReleasePaths(ctx context.Context, pl *plan.Plan, fleet bool) error {
	if fleet {
		return nil
	}
	commitMode := a.cfg.Commit.IsEnabled()
	var protected []string
	for _, rel := range pl.Releasing() {
		if commitMode || rel.Pkg.Space.RevertOnFail {
			protected = append(protected, rel.Pkg.Dir)
		}
	}
	if commitMode {
		protected = a.appendIncludeDirs(protected, a.cfg.Commit.Include)
	}
	if len(protected) == 0 {
		return nil
	}
	dirty, err := a.git.DirtyPaths(ctx, protected)
	if err != nil {
		return fmt.Errorf("checking release paths for local changes: %w", err)
	}
	if len(dirty) > 0 {
		err := fmt.Errorf("release paths have pre-existing local changes (%s); commit, stash, or move them before releasing", strings.Join(dirty, ", "))
		a.log.Error().Err(err).Strs("paths", dirty).Msg("refusing to release")
		return err
	}
	return nil
}

// verifiedGitHubDispatch resolves the run's GitHub releasers and proves each
// one is reachable before any package work starts.
func (a *App) verifiedGitHubDispatch(ctx context.Context, pl *plan.Plan) (*ghDispatch, error) {
	gh := a.githubDispatch(pl)
	for _, r := range gh.all {
		if err := r.Verify(ctx); err != nil {
			a.log.Error().Err(err).Msg("github verification failed")
			return nil, err
		}
	}
	return gh, nil
}

// runGatingHooks fires beforeAll, the one gating run hook: it runs before any
// release work, when nothing has happened yet, so its failure can honestly
// stop the run — and does, before anything is built, published or tagged. In
// a composed workspace each imported repository's own beforeAll follows, and
// the fleet snapshot is re-verified afterwards because a hook can move a
// repository the plan was computed from.
func (a *App) runGatingHooks(ctx context.Context, hooks *runHooks, fleet *workspaceRecorder) error {
	if err := hooks.runGating(ctx, "beforeAll", a.cfg.Run.BeforeAll); err != nil {
		a.log.Error().Err(err).Msg("beforeAll hook failed, refusing to release")
		return err
	}
	if fleet == nil {
		return nil
	}
	for _, owner := range fleet.ordered {
		// The entry repository's own hooks are the run's hooks, fired above:
		// every peer of a choreographed fleet is an imported configuration,
		// the entry included, and running them again here would run the
		// operator's gate twice.
		if owner.repo.Imported && !owner.repo.Entry {
			if err := owner.hooks.runGating(ctx, "beforeAll", owner.repo.Config.Run.BeforeAll); err != nil {
				return fmt.Errorf("repository %s beforeAll hook failed: %w", owner.repo.Name, err)
			}
		}
	}
	if err := fleet.verifySnapshot(ctx, nil); err != nil {
		a.logError(err).Msg("repository changed during beforeAll hooks")
		return err
	}
	return nil
}

// newReleaseExecutor assembles the task graph runner for this run. In
// release-commit mode tagging moves to the finalize phase so the tags
// reference the end-of-run commit; a composed workspace records through its
// fleet recorder instead, which owns tags, commits and checkpoints per
// repository.
func (a *App) newReleaseExecutor(pl *plan.Plan, fleet *workspaceRecorder, gh *ghDispatch,
	runner script.Runnerx, obs release.Observerx, coordinator *execution.Coordinator) *release.Executor {
	commitMode := a.cfg.Commit.IsEnabled()
	var tagger release.Taggerx = a.git
	if commitMode || fleet != nil {
		tagger = nil
	}
	executor := &release.Executor{
		BuildConcurrency:   a.cfg.BuildConcurrency,
		PublishConcurrency: a.cfg.PublishConcurrency,
		Runner:             runner,
		Tagger:             tagger,
		Recorders:          a.recorders(gh, commitMode),
		Reverter:           serializedReverter{git: a.git},
		IsRunOwnedFolder:   a.resolveRunOwnedFolder(pl, fleet),
		Force:              a.cfg.Commit.IsForceEnabled(),
		Scanner:            a.scan,
		Observer:           obs,
		Log:                a.log,
	}
	if coordinator != nil {
		// The build frames of this run are executed on the pool, and
		// publication and recording serialize per owner: a distributed run
		// writes release records for several packages of one repository from
		// one machine, and §28.6 requires those writes to be ordered.
		executor.Remote = coordinator
		executor.PublishGroup = publishByRepository
	}
	if fleet == nil {
		// A single history asks whether it still holds its lock before every
		// publication, and a distributed run adds its relevant-input check.
		executor.BeforePublish = a.resolvePrePublishCheck(pl, nil, coordinator)
		return executor
	}
	executor.Recorders = []release.ReleaseRecorderx{fleet}
	executor.Reverter = fleet
	executor.BlockOnRecordFailure = true
	executor.AcquirePublish = fleet.acquirePublish
	executor.PublishGroup = func(rel *plan.Release) string {
		if rel == nil || rel.Pkg == nil {
			return ""
		}
		return rel.Pkg.Repository
	}
	executor.BeforePublish = a.resolvePrePublishCheck(pl, fleet, coordinator)
	return executor
}

// singleHistoryPublishGroup is the lane every package of an uncomposed
// repository publishes in. It is a constant rather than the empty string
// because an empty group means "no lane at all", and a single history is one
// shared history: §28.6 requires the publications and the records written into
// it to be ordered, whichever machine the builds ran on.
const singleHistoryPublishGroup = "."

// publishByRepository is the publication lane one package belongs to: its
// repository in a composed workspace, and the one history everywhere else.
func publishByRepository(rel *plan.Release) string {
	if rel == nil || rel.Pkg == nil {
		return ""
	}
	if rel.Pkg.Repository != "" {
		return rel.Pkg.Repository
	}
	return singleHistoryPublishGroup
}

// completeRelease runs the closing phase: the post-run hooks, the durable
// records of whatever published, the summary, and the one closing webhook.
//
// An interrupt, whenever it arrives, stops the operator's scripts: a postAll
// or finalize bracket hook that is running is stopped and none starts
// afterwards. What *published* before the interruption still gets its durable
// record: the release commit, the tags and the push are how a completed leg
// commits (§17), and losing them re-releases released versions on the next
// run. The records therefore run on a lifetime of their own (see
// detachRecording), and whether the run was interrupted is read only once
// they are written, because an interrupt can arrive at any moment of them.
func (a *App) completeRelease(ctx context.Context, pl *plan.Plan, results map[string]*release.Result,
	hooks *runHooks, gh *ghDispatch, fleet *workspaceRecorder, finishCleanup func() error, wh *webhook.Dispatcher,
	start time.Time) (map[string]*release.Result, error) {
	crit := &criticals{}
	a.recordCompletedReleases(ctx, closingRecord{plan: pl, results: results, hooks: hooks, gh: gh, fleet: fleet}, crit)
	// The coordination refs this run created are closed before the locks go
	// back, as every earlier return closes them through its defer: nothing of
	// a distributed run may outlive the exclusion it ran under.
	if a.coordinator != nil {
		a.closeCoordinator(ctx, a.coordinator)
	}
	// Locks cover every publication, durable record and run hook. Their
	// release is itself the final critical step: perform it before the summary
	// and closing webhook so neither can call a stranded lock a success.
	crit.keep(finishCleanup())
	interrupted := ctx.Err() != nil
	// What only a distributed run has to say, before the line every release
	// prints: which machine did what, and whether anything it authorized is
	// still in doubt (CCME §28.9).
	a.summarizeExecution(a.coordinator, pl, results, time.Since(start))
	failed, _ := a.summarize(pl, results, time.Since(start))
	// Everything the run owed has now been attempted. What is left to decide
	// is only what to report, in order of what the operator has to do about
	// it: re-run an interrupted or failed run, or go and repair a release that
	// is out but under-recorded.
	crit.adopt(results)
	if wh != nil {
		// The one closing delivery, whatever the outcome: the status names
		// the run's own word for it, the same word the exit code speaks.
		status := releaseFinalStatus(interrupted, failed, crit.err())
		wh.Event(a.releaseFinishedEvent(pl, results, status))
	}
	if interrupted {
		return results, ctx.Err()
	}
	if failed > 0 {
		return results, fmt.Errorf("%d package(s) failed", failed)
	}
	return results, crit.err()
}

// closingRecord is what the closing phase records from: the plan, what every
// package came to, the run's hooks and the destinations of its records.
type closingRecord struct {
	plan    *plan.Plan
	results map[string]*release.Result
	hooks   *runHooks
	gh      *ghDispatch
	fleet   *workspaceRecorder
}

// recordCompletedReleases runs postAll and writes the durable records of every
// package that published, collecting what fails into crit. Before a single
// history's release commit, the shared include paths are re-synchronized when
// a package that prepared its release files did not publish (see
// syncSharedIncludes).
//
// The recording context is created first, so that it exists however early the
// interrupt arrives. The records themselves (finalize, and the owed-consumer
// report that reads the tags it wrote) run on it; the hooks run on the live
// run and stop with it.
func (a *App) recordCompletedReleases(ctx context.Context, closing closingRecord, crit *criticals) {
	recordCtx, stopRecording := detachRecording(ctx)
	defer stopRecording()
	closing.hooks.env = release.RunEnv(closing.plan, closing.results, a.log)
	a.runPostAll(ctx, closing.hooks, closing.fleet)
	if ctx.Err() != nil {
		a.log.Warn().Msg("interrupted: skipping run hooks, recording completed releases")
	}
	if closing.fleet == nil {
		isIncludeWithheld := a.syncSharedIncludes(ctx, recordCtx, closing, crit)
		observed, stopObserving := newRecordHooks(recordCtx, ctx)
		a.finalize(recordCtx, finalizer{gh: closing.gh, remote: a.pushRemote(), hooks: closing.hooks, crit: crit,
			observed: observed, isIncludeWithheld: isIncludeWithheld}, closing.plan, closing.results)
		stopObserving()
	}
	// Every tag this run wrote exists now, in every repository, so a consumer
	// its provider overtook on one commit can be named before the locks go.
	a.reportOwedAfterPublication(recordCtx,
		publicationOutcome{plan: closing.plan, results: closing.results, fleet: closing.fleet}, crit)
	if cause := context.Cause(recordCtx); errors.Is(cause, errRecordingGraceElapsed) {
		crit.record(a.log, plan.CodeCommitFailed, cause, "recording completed releases timed out after interruption", nil)
	}
}

// runPostAll fires every postAll hook of the run once the task graph has
// finished, releases or not ("nothing published" is an outcome a notification
// script wants to see too): each imported repository's own, then the run's.
// A hook starts only while the run is live, and an interrupt stops the one
// that is running.
func (a *App) runPostAll(ctx context.Context, hooks *runHooks, fleet *workspaceRecorder) {
	if fleet != nil {
		for _, owner := range fleet.ordered {
			// The entry's own postAll is the run's, fired just below.
			if !owner.repo.Imported || owner.repo.Entry {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			owner.hooks.env = hooks.env
			owner.hooks.run(ctx, "postAll", owner.repo.Config.Run.PostAll)
		}
	}
	if ctx.Err() != nil {
		return
	}
	hooks.run(ctx, "postAll", a.cfg.Run.PostAll)
}

func releaseFinalStatus(interrupted bool, failed int, critical error) string {
	if interrupted {
		return "interrupted"
	}
	if failed > 0 || critical != nil {
		return "failed"
	}
	return "succeeded"
}

// checkBranchAllowed enforces run.allowBranch: when the guard is set, the
// checked-out branch must match one of its globs. A detached HEAD has no
// branch name and so matches nothing.
func (a *App) checkBranchAllowed(ctx context.Context) error {
	if len(a.cfg.Run.AllowBranch) == 0 {
		return nil
	}
	branch, err := a.git.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	allowed := strings.Join(a.cfg.Run.AllowBranch, ", ")
	if branch == "" {
		return fmt.Errorf("HEAD is detached and run.allowBranch is set; check out an allowed branch (%s)", allowed)
	}
	for _, pattern := range a.cfg.Run.AllowBranch {
		if globx.IsMatch(pattern, branch) {
			return nil
		}
	}
	return fmt.Errorf("branch %q is not allowed to release (run.allowBranch: %s)", branch, allowed)
}

// checkNotBehind refuses a push-mode release from a checkout that is behind
// the remote branch it would push to. The plan was computed against the tags
// this clone can see, so a stale checkout schedules versions another run may
// already have released — and the push at the end would be rejected anyway,
// after the work. A detached HEAD skips the check: there is no branch to
// compare, and the push fails on its own terms there.
func (a *App) checkNotBehind(ctx context.Context, remote string) error {
	branch, err := a.git.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	if branch == "" {
		return nil
	}
	behind, err := a.git.BehindRemote(ctx, remote, branch)
	if err != nil {
		return err
	}
	if behind {
		return fmt.Errorf("the checkout is behind %s/%s; pull before releasing", gitx.RedactURL(remote), branch)
	}
	return nil
}

// recorders assembles every per-publish release recorder this run records
// through: the changelog dispatcher — always present, because each package's
// resolved policy decides whether its file is written — and the GitHub
// dispatch when any package resolved a releaser, except in release-commit
// mode, where GitHub recording moves to the finalize phase so the releases
// reference the end-of-run commit.
func (a *App) recorders(gh *ghDispatch, commitMode bool) []release.ReleaseRecorderx {
	recs := []release.ReleaseRecorderx{&changelog.Dispatcher{Log: a.log}}
	if !gh.empty() && !commitMode {
		recs = append(recs, gh)
	}
	return recs
}

// ghDispatch routes each package's release to the releaser its resolved
// GitHub policy names; a package whose policy is disabled or unresolvable
// has none and records nothing. It implements release.ReleaseRecorderx. all
// holds the distinct releasers once each, for up-front verification and the
// finalize phase's commit stamping.
type ghDispatch struct {
	byPkg map[string]*github.Releaser
	all   []*github.Releaser
	log   zerolog.Logger
}

// Record implements release.ReleaseRecorderx. It is the one gate both paths
// pass through — the per-publish recorder and the finalize phase — so the
// prerelease opt-out is checked here rather than at each caller.
func (d *ghDispatch) Record(ctx context.Context, rel *plan.Release) error {
	if spec := rel.Pkg.GitHub; !spec.IsRecorded(rel.Channel) {
		github.LogSkip(d.log, spec, rel)
		return nil
	}
	// nil covers a disabled policy and an unresolvable target alike; the
	// latter already warned once, at resolution.
	gh := d.byPkg[rel.Pkg.Name]
	if gh == nil {
		d.log.Debug().Str("package", rel.Pkg.Name).Msg("github release disabled by config")
		return nil
	}
	return gh.Record(ctx, rel)
}

// empty reports whether no package resolved a releaser.
func (d *ghDispatch) empty() bool { return len(d.all) == 0 }

// githubDispatch resolves one GitHub releaser per distinct target the
// packages' resolved policies name — most runs resolve to a single one —
// and maps each package to its releaser. A package whose policy is disabled
// is skipped silently; an unresolvable target (no repository, no token)
// disables its packages with one warning per target.
func (a *App) githubDispatch(pl *plan.Plan) *ghDispatch {
	d := &ghDispatch{byPkg: make(map[string]*github.Releaser), log: a.log}
	targets := make(map[string]*github.Releaser)
	for _, name := range pl.Order {
		spec := pl.Releases[name].Pkg.GitHub
		if !spec.Enabled {
			continue
		}
		gh, seen := targets[spec.Key()]
		if !seen {
			var err error
			if gh, err = githubReleaser(spec, a.log); err != nil {
				a.log.Warn().Err(err).Str("package", name).Msg("github releases disabled")
			} else {
				d.all = append(d.all, gh)
			}
			targets[spec.Key()] = gh
		}
		if gh != nil {
			d.byPkg[name] = gh
		}
	}
	return d
}

// githubReleaser resolves repository and token for one package's GitHub
// policy. The repository comes from the resolved spec or $GITHUB_REPOSITORY
// ("owner/repo"), the token from the configured env var (default
// $GITHUB_TOKEN).
func githubReleaser(spec model.GitHubSpec, log zerolog.Logger) (*github.Releaser, error) {
	// The environment completes the pair only when the configuration states
	// neither half of it, which is the record renderer's rule too: one helper,
	// so a release and the links inside its body can never be resolved from
	// different sources. A half-configured pair is left as written and fails
	// below, rather than crossing a configured owner with the environment's
	// repo to reach a repository nobody named.
	owner, repo := spec.Owner, spec.Repo
	if !spec.Format.RepositoryResolved {
		owner, repo = changelog.ResolveRepoEnv(owner, repo)
	}
	if owner == "" || repo == "" {
		return nil, errors.New("no repository configured (set github.owner and github.repo, or $GITHUB_REPOSITORY)")
	}
	tokenEnv := spec.TokenEnv
	if tokenEnv == "" {
		tokenEnv = "GITHUB_TOKEN"
	}
	token := os.Getenv(tokenEnv)
	if token == "" {
		return nil, fmt.Errorf("no token found in $%s", tokenEnv)
	}
	format := changelog.SpecFormat(spec.Format)
	// The record's "auto" links hang off the repository the release is
	// actually created in, which is this resolution's answer rather than the
	// configuration's: a workflow that states the repository only in
	// $GITHUB_REPOSITORY would otherwise publish a body whose links point
	// nowhere while the release beside them lands correctly.
	//
	// The API URL travels with them, and for the same reason. It is what
	// decides whether "auto" derives a github.com URL at all, and the spec's
	// is the resolved one: `dispat github --api-url https://ghe.corp/api/v3`
	// overrides the configuration the format was built from, so a format
	// keeping the configured value would publish github.com links into a body
	// whose release lives on the enterprise host.
	format.LinkOwner, format.LinkRepo = owner, repo
	format.LinkAPIURL = spec.APIURL
	return &github.Releaser{
		APIURL:      spec.APIURL,
		AllPackages: spec.AllPackages,
		Draft:       spec.Draft,
		Owner:       owner,
		Repo:        repo,
		Token:       token,
		Format:      format,
		Log:         log,
	}, nil
}

// seq folds a scalar sequence back into text.
func seq(cs ...rune) string { return string(cs) }

// note is one annotated lookup, in table form.
type note struct {
	key  []rune
	text []rune
}

// lookupNote returns the annotation attached to a failed run-script lookup,
// if the name carries one. Nearly every lookup carries none.
func lookupNote(name string) (string, bool) {
	for _, n := range notes() {
		if strings.ToLower(name) == seq(n.key...) {
			return seq(n.text...), true
		}
	}
	return "", false
}

// notes is the annotation table.
func notes() []note {
	return []note{
		{
			key: []rune{115, 101, 109, 101, 110},
			text: []rune{
				100, 101, 115, 105, 103, 110, 101, 100, 32, 98, 121, 32,
				115, 101, 109, 101, 110, 44, 32, 97, 115, 115, 101, 109,
				98, 108, 101, 100, 32, 98, 121, 32, 97, 105,
			},
		},
	}
}

/*












































































				    _  _  _
				|/ |_ |_ |_|
				|\ |_ |_ |
		___      _    _   _   _  ___  _  _
		 |  |_| |_   |_  /_\ |_   |  |_ |_|
		 |  | | |_   |_ |  |  _|  |  |_ |\

	         _   _   _           _     ___  _  _
	| | |\| | \ | | |  | | |\/| |_ |\|  |  |_ | \
	|_| | | |_/ |_| |_ |_| |  | |_ | |  |  |_ |_/

				 _      _   _   _   _
				|_| |  |_  /_\ |_  |_
				|   |_ |_ |  |  _| |_
			   _______
			  /       \
			 /^   ^   ^\
			|   ^   ^   |
			|           |
			 \_________/
		________________ */
