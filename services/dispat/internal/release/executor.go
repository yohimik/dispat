// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

// Package release executes a computed plan: it builds and publishes every
// changed package with bounded parallelism while honouring the dependency
// graph and each space's isBuildWaitingPublish setting.
package release

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/scanner"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// Status is the terminal state of a package release.
type Status uint8

const (
	StatusPending Status = iota
	StatusPublished
	StatusFailed
	StatusSkipped
	// StatusCancelled marks a package the run was interrupted out of: its
	// remaining scripts never ran (or were killed mid-run) because the context
	// was cancelled, not because anything about the package failed. The next
	// run owes it the same release — recovery is just re-running (§17).
	StatusCancelled
)

func (s Status) String() string {
	switch s {
	case StatusPublished:
		return "published"
	case StatusFailed:
		return "failed"
	case StatusSkipped:
		return "skipped"
	case StatusCancelled:
		return "cancelled"
	default:
		return "pending"
	}
}

// Result is the outcome of one released package.
type Result struct {
	Name     string
	From, To ccme.Version
	// Channel is the channel the package is being released on (§11.1).
	Channel string
	Status  Status
	// FailedStage names the stage that failed, under the name it ran as:
	// "sign", "version" or "propagate", "syncLock", "build", "tests",
	// "publish" or a stage the flow defines; empty unless Status is
	// StatusFailed. FailedStageLabel is the label the flow gave that stage,
	// empty where it gave none. Both are informational (shown in the summary).
	FailedStage      string
	FailedStageLabel string
	Err              error
	Duration         time.Duration
	// Blocked reports that the package was planned but never attempted
	// because a dependency failed to publish in this run (W194, §19.3). It is
	// the "absence" half of the non-suppressible diagnostics: a package that
	// was in the plan and did not release needs to be accounted for.
	Blocked bool
	// BlockedBy names the dependency responsible.
	BlockedBy string
	// Critical holds what failed after the package published: a release record
	// that could not be written, a tag that could not be created. The package
	// is published — Status says so — and these are the parts of the record
	// that are missing, which is a thing to go and fix rather than a thing to
	// re-run. They never change Status, and the command they belong to reports
	// them on its way out.
	Critical []error
	// RecordBlocked retains a published outcome while preventing consumers
	// from using a release whose required durable records are incomplete.
	// Skipped dependents carry it forward through the package graph.
	RecordBlocked bool
	// Worker is the node this package's delegated stages were placed on, and
	// is empty for every package a run executed by itself. It belongs to the
	// package rather than to one task because it is what the package's own
	// outcome is attributed to: a receiver asking "where was this version
	// built" is asking about the release, not about the stage it heard last.
	Worker string
	// IsPrepared reports that a task preparing the package's release files
	// started: its sign stage, its version stage or its syncLock stage (see
	// Stage.IsPreparing). A prepared package that did not publish may have
	// left its planned version in files the release commit shares with the
	// packages that did.
	IsPrepared bool
	// IsSyncLockRun reports that the package's syncLock stage ran its
	// commands. The closing phase runs them again for a published package
	// when the files they regenerate are re-synchronized.
	IsSyncLockRun bool
}

// Taggerx creates release tags; *gitx.LocalGitx satisfies it. A nil Taggerx on the
// Executor defers tagging to a later phase (release-commit mode, where tags
// must point at the end-of-run commit). target is the commit the tag points
// at; empty means HEAD.
//
// Besides the write it looks one tag HEAD reaches up by its exact name, which
// is how a refused create-only write is told apart: the flow's own early tag
// (W223), a record at another commit (E221), or a failure of the write itself.
type Taggerx interface {
	CreateTag(ctx context.Context, name, message, target string) error
	FindTag(ctx context.Context, name string) (gitx.Tag, bool, error)
	TagExists(ctx context.Context, name string) (bool, error)
	ResolveCommit(ctx context.Context, rev string) (string, error)
}

// ReleaseRecorderx records a successful release somewhere: a changelog file
// (*changelog.FileWriter), a GitHub release (*github.Releaser), or any other
// destination for the same release data.
type ReleaseRecorderx interface {
	Record(ctx context.Context, rel *plan.Release) error
}

// postPublishRecorder separates the durable record transaction from its
// observer lifetime. The first context survives interruption so a published
// release is still recorded; the second is the live run context, allowing
// recorder-owned hooks to stop with the operator's run. Recorders without
// this extension retain the ReleaseRecorderx contract above.
type postPublishRecorder interface {
	RecordAfterPublish(recordCtx, observerCtx context.Context, rel *plan.Release) error
}

// DurableRecorderx writes a published package's record to the store the next
// run reads after the package publishes: a receipt followed by the release
// tag in tag-only mode, or a receipt until finalization in commit mode
// (§19.4). It runs on the detached recording context.
type DurableRecorderx interface {
	RecordDurably(ctx context.Context, rel *plan.Release) error
}

// PendingRecord is the durable identity and explicit tag target of a prior publication.
type PendingRecord struct {
	Integrity      string
	ExportedCommit string
}

// Reverterx rolls back local changes inside a package folder; *gitx.LocalGitx
// satisfies it. Used for spaces with revertOnFail, and for a skipped package
// whose folder the run owns (see Executor.IsRunOwnedFolder).
type Reverterx interface {
	RevertDir(ctx context.Context, dir string) error
}

// Executor runs the stages of every changed package: the preparing ones
// (sign, version, syncLock), then the stages its flow configures in the order
// the flow gives them, the records, and the stages that observe the release.
//
// Scheduling model: each changed package contributes one task per stage it
// has (see stageplan.go). Packages bumped because of provider updates get a
// version task before everything the flow configures (its job is syncing
// manifests to the new provider versions), and a package whose space
// configures a sign stage gets a sign task before all of them (its job is
// writing the package's own version). The flow's stages run in the order of
// the package's stage graph (CCME §13.11, resolved as the configuration
// loads): by default the build, then the tests and the user stages at once,
// then the publish and the announce. The publish records the release in its
// tail, and a package with no publish records it in a task of its own after
// its last gating stage. What a consumer's stages after the preparing ones
// wait for on each changed provider is the provider space's
// isBuildWaitingPublish relation: nothing at all under
// `none`, the provider's build under `build`, its build and its publish under
// `publish`. A consumer's publish always waits for its providers' publishes
// under all three, since publishing against a not-yet-published provider
// version would be invalid; a provider whose publish failed therefore skips
// its consumers unless they have a release reason of their own, and skips
// them unconditionally when its relation is a blocking one, which `none` and
// `publish` are unless the configuration says otherwise.
//
// Both orders are taken over the whole workspace graph and restricted to the
// packages this run releases afterwards, never over the subgraph the plan
// induces (§19.2, §19.2a), and the skip cascade follows the same closure
// (§19.3): a provider reached only through a package with nothing to release
// still publishes first, and still blocks the consumers behind it.
//
// Every package has a version and a build task; the tests, the publish, the
// announce and the user stages exist where the flow gives them a script or a
// hook, and a package with no publish is recorded by a task of its own.
// Scripts receive DISPAT_* environment
// variables (package, space, versions,
// bump, stage, tag, plus the workspace and updated-provider listings at every
// stage as per-package DISPAT_WORKSPACE_* / DISPAT_UPDATED_* variables).
//
// Build and publish stages have independent parallelism budgets: at most
// BuildConcurrency sign/version/build/tests/user-stage scripts and
// PublishConcurrency publish, record and announce scripts run at any moment.
// A package runs two of its tasks at once only where its graph leaves two
// stages unordered.
type Executor struct {
	BuildConcurrency   int
	PublishConcurrency int
	Runner             script.Runnerx
	Tagger             Taggerx
	Recorders          []ReleaseRecorderx // run in order after each successful publish
	// BeforePublish runs after the package's beforePublish hook and immediately
	// before its publish command. Composed workspaces use it to revalidate the
	// fixed fleet snapshot after arbitrary user hook code has run.
	BeforePublish func(context.Context, *plan.Release) error
	// AcquirePublish serializes publish and recording for packages which share
	// mutable repository state. Its release function is held through the
	// publish tail and called on every success or failure path.
	AcquirePublish func(context.Context, *plan.Release) (func(), error)
	// PublishGroup serializes publish tasks which share mutable repository
	// state without occupying a publish worker while they wait. Empty keeps
	// legacy scheduling. These are ordering edges only: one sibling's failure
	// does not make another sibling a dependency-blocked release.
	PublishGroup func(*plan.Release) string
	// BlockOnRecordFailure requires durable records before consumers proceed.
	// Legacy runs leave this false; composed repositories enable it because
	// a missing source record or control checkpoint invalidates consumption.
	BlockOnRecordFailure bool
	Reverter             Reverterx // rolls back package folders for revertOnFail spaces
	// Durable writes each published package's required remote record before
	// optional effects; in tag-only mode it creates the local tag after the
	// receipt. Nil leaves remote recording to the end of the run or to CI.
	Durable DurableRecorderx
	// PendingRecord reports a package an earlier run published and
	// recorded with a receipt only. Its publish frame does not run; its retry
	// build must reproduce this artefact identity before a new commit records it.
	PendingRecord func(*plan.Release) (PendingRecord, bool)
	// IsRunOwnedFolder reports whether everything in a package's folder at
	// the end of its stages was written by this run, so that restoring the
	// folder can discard nothing else. A package skipped after an earlier
	// stage of it ran has its folder restored when this answers true, whatever
	// revertOnFail says: the folder then holds only edits for a version that
	// will not publish. nil answers false for every package.
	IsRunOwnedFolder func(*plan.Release) bool
	// Force rewrites a tag the repository already carries instead of failing
	// on it (commit.force, default true). The pre-existing-tag rules still
	// come first: a tag already at the release commit is a skip, and one at a
	// different commit is left alone whatever this says.
	Force bool
	// Scanner reads manifests for the autoVersion spaces' native rewriting;
	// nil defaults to the filesystem scanner.
	Scanner scanner.Scannerx
	// Observer receives release-progress events (stage transitions, package
	// outcomes); nil disables observation. It only observes: nothing it does
	// with an event can affect the run. See Observerx.
	Observer Observerx
	// Remote executes the stage frames this run delegates to other execution
	// nodes (CCME §28). nil is local execution, byte for byte as before: the
	// task graph, the budgets and the order are the same either way, and the
	// only thing it decides is where one frame's commands run. See remote.go.
	Remote Remotex
	Log    zerolog.Logger
}

type taskKind uint8

const (
	taskVersion taskKind = iota
	taskBuild
	taskPublish
	// taskSyncLock runs a space's autoVersion.syncLock scripts between the
	// package's version and build stages, under its own run-wide budget
	// (default 1): its job is regenerating shared lock files, which corrupt
	// under parallel writers.
	taskSyncLock
	// taskSign is the optional sign stage, the package's first task when it
	// has one: it writes the package's own version before the version stage
	// propagates its providers' versions. It runs under the build budget, like
	// the version stage, and only for a package whose space configures it
	// (see hasSignTask).
	taskSign
	// taskTests is the optional tests stage, between the package's build and
	// its publish: it checks what the build produced, so its failure fails the
	// package before anything is published. It runs under the build budget
	// and is placed the way the build is, and only for a package whose space
	// configures it (see hasTestsTask).
	taskTests
	// taskAnnounce is the announce stage, which observes a release that is
	// out by default and gates like its neighbours when the flow places it
	// before the publish.
	taskAnnounce
	// taskUser is a stage the flow defines. Its task carries the stage's
	// name, since the kind alone names none.
	taskUser
	// taskBeforeAll runs the package's beforeAll hook, as a task of its own
	// so that several first stages wait for one hook. It reports
	// under the name of the first stage after it, the one a failed beforeAll
	// keeps the package from entering.
	taskBeforeAll
	// taskRecord writes the records of a package that has no publish stage,
	// after its last gating stage, and is the whole release of a package with
	// no stage at all.
	taskRecord
	// Pass-through nodes carry build, publish and failure-outcome waits across
	// packages this run does not release (see stageGaps). Nothing runs them.
	taskPassBuild
	taskPassPublish
	taskPassBlock
)

func (k taskKind) String() string {
	switch k {
	case taskUser:
		return "stage"
	case taskBeforeAll:
		return "beforeAll"
	case taskRecord:
		return "record"
	case taskPassBuild, taskPassPublish, taskPassBlock:
		return "pass"
	}
	return stageOf(k).Name
}

// task is one node of the task graph: a package's stage, named by its kind
// and, for a stage the flow defines, by the stage's name.
type task struct {
	pkg  string
	kind taskKind
	name string
}

// spaceLogin is the once-per-space login gate. sync.Once gives the two
// properties the login needs for free: the script runs exactly once however
// many publishes race to it, and every other publish blocks inside Do until
// the first one finishes. The gate is keyed by space, not by script text, so
// two spaces sharing one login command still log in once each — credentials
// and registries are a property of the space.
type spaceLogin struct {
	once sync.Once
	err  error
	// outputs is what the login script exported through its DISPAT_OUTPUT
	// file. Written only inside once.Do and read only after Do returned, so
	// Do's happens-before is all the synchronization it needs. The exports are
	// space-scoped: each package of the space merges them at its publish (the
	// one stage that gates on the login), so they reach the publish stage and
	// everything after it.
	outputs []plan.Output
}

type spaceLoginKey struct {
	repository string
	name       string
}

// Sequence is one command sequence together with everything it runs under —
// the runner, the working directory, the environment and the failure mode —
// so the run helpers take one value instead of a positional parameter list.
//
// FailFast selects between the two failure semantics scripts have: true is
// the release-gating mode, where the first error stops the sequence and is
// returned, so a stage or hook that exists to gate a release gates it at the
// first refusal. With false every command runs regardless and errors are only
// warned about — the mode of hooks that run after the thing they observe has
// already happened, where stopping the sequence could not un-happen it.
type Sequence struct {
	Runner   script.Runnerx
	Dir      string   // working directory (the package folder, or the repo root)
	Stage    string   // what DISPAT_STAGE carries; also the log label
	Commands []string // the commands, run in order
	Env      []string // the full environment the commands receive
	Log      zerolog.Logger
	FailFast bool
	// OutputPaths translates declared output paths for an isolated local frame.
	OutputPaths []OutputPathMapping
	// Warned is told of each command a warn-only sequence went on past, so a
	// caller that only warns can still answer whether everything succeeded.
	Warned func()
}

// Run executes the sequence's commands in order inside its directory.
//
// A cancelled run stops the sequence where it is. A warn-only sequence does
// not fail on an error, so without this check an interrupted run would go on
// launching every remaining hook command, each one dying at once and each one
// reporting a failure the operator did not cause.
func (s Sequence) Run(ctx context.Context) error {
	for index, command := range s.Commands {
		if err := ctx.Err(); err != nil {
			s.Log.Debug().Int("commandIndex", index+1).Str("stage", s.Stage).
				Msg("run cancelled; remaining commands not started")
			return err
		}
		// Announced before it runs, not only after it finished (the runner's
		// own trace): a script that hangs forever must leave a record of what
		// is hanging. The logger names the package and stage; the index locates
		// the command without exposing literal credentials in its arguments.
		s.Log.Trace().Int("commandIndex", index+1).Msg("script starting")
		err := func() error {
			stdout := newLineWriter(s.Log, zerolog.InfoLevel)
			stderr := newLineWriter(s.Log, zerolog.WarnLevel)
			// Deferred so a panicking runner still logs the partial lines.
			defer stderr.Flush()
			defer stdout.Flush()
			return s.Runner.Run(ctx, s.Dir, command, s.Env, stdout, stderr)
		}()
		if err == nil {
			continue
		}
		if s.FailFast {
			return err
		}
		s.Log.Warn().Err(err).Str("stage", s.Stage).Msg(s.Stage + " script failed (not fatal)")
		if s.Warned != nil {
			s.Warned()
		}
	}
	return nil
}

// Run executes the plan and returns one Result per changed package. Unchanged
// packages are absent from the map. Failures never abort the run: dependent
// packages are skipped (unless they have a release reason of their own) and
// independent packages continue.
func (e *Executor) Run(ctx context.Context, p *plan.Plan) map[string]*Result {
	results, changed := newReleasingResults(p)
	if len(results) == 0 {
		return results
	}

	// The workspace listing depends only on the plan, never on how the run
	// goes, so its variables are built once and shared by every task's
	// environment.
	wsVars := WorkspaceEnv(p, e.Log)
	logins := newSpaceLogins(p)
	plans := newStagePlans(p, changed)
	sched, publishReach := e.buildTaskGraph(p, changed, plans)

	// The tasks run under a context of their own, so that a task that
	// panicked can stop the rest of the graph without cancelling the run that
	// owns it: the run still has its closing phase, its records and its locks
	// to see to (see containPanic).
	tasks, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	r := &run{Executor: e, plan: p, plans: plans, wsVars: wsVars, logins: logins, stop: stop,
		publishReach:     publishReach,
		reachedProviders: make(map[string][]string),
		results:          results, started: make(map[string]time.Time), scan: e.Scanner,
		avChanged:           make(map[string]bool),
		reconciledProviders: make(map[string][]string, len(changed)),
		builtPackages:       make(map[string]bool, len(changed)),
		exports:             make(map[task][]plan.Output),
		failedTasks:         make(map[task]bool),
		inFlight:            make(map[string]int, len(changed)),
		outcomes:            make(map[string]*pendingOutcome)}

	r.prepareNativeWrites(ctx, changed)
	if err := r.drain(tasks, sched, changed); err != nil {
		r.reportStopped(ctx, tasks, err)
	}
	return results
}

// newReleasingResults starts one Result for every package the plan releases
// and returns them with the set of those packages' names.
func newReleasingResults(p *plan.Plan) (map[string]*Result, map[string]bool) {
	results := make(map[string]*Result)
	changed := make(map[string]bool)
	for name, rel := range p.Releases {
		// Releasing(), not Changed(). A package held by `Release-As: none` has
		// a bump and a computed version — both are reported (W154) — but it is
		// excluded from the plan by §13.6a and MUST NOT be built, published or
		// tagged; a package of a versioning-none space is excluded permanently
		// for the same reason. A channel-only release, conversely, has no bump
		// at all and must still be executed: it is a release like any other
		// (§13.9).
		if rel.IsReleasing() {
			changed[name] = true
			results[name] = &Result{
				Name:    name,
				From:    rel.Previous(),
				To:      rel.Next,
				Channel: rel.Channel,
			}
		}
	}
	return results, changed
}

// newSpaceLogins makes one login gate per space that configures a login
// script.
func newSpaceLogins(p *plan.Plan) map[spaceLoginKey]*spaceLogin {
	logins := make(map[spaceLoginKey]*spaceLogin)
	for _, rel := range p.Releases {
		if len(rel.Pkg.Space.LoginScript) > 0 {
			key := spaceLoginKey{rel.Pkg.Space.Repository, rel.Pkg.Space.Name}
			if _, ok := logins[key]; !ok {
				logins[key] = &spaceLogin{}
			}
		}
	}

	return logins
}

// buildTaskGraph states the releasing packages' task graph and returns it with
// the index of what each consumer reaches over the publication order, which
// the skip cascade reads: §19.3 computes its closure over the full workspace
// graph, so the providers that can block a package are the same set that
// orders it.
func (e *Executor) buildTaskGraph(p *plan.Plan, changed map[string]bool,
	plans map[string]*stagePlan) (*graph.Scheduler[task], *stageReach) {
	// Sorted iteration: the scheduler hands nodes out in insertion order, so
	// building the edges in name order is what makes launch order — not just
	// completion semantics — deterministic run to run (§17.2).
	names := slices.Sorted(maps.Keys(changed))
	sched := graph.NewScheduler[task]()
	// A consumer reads a provider through whatever lies between them, and what
	// lies between them need not be releasing, so the build order is taken
	// over the whole dependency graph and then restricted to the packages that
	// build (§19.2a). The publication order is taken over the same whole graph
	// and filtered afterwards, never over the subgraph the plan induces
	// (§19.2): `app` resolves `core` through an unreleasing `ui` at install
	// time, so publishing `app` first would record a range reconciled against
	// a version nobody published.
	buildEdges := newStageEdges(p, changed, plans)
	publishEdges := publicationStageEdges(p, changed, plans)
	blockingEdges := blockingStageEdges(p, changed, plans)
	blockingReach := newPublishReach(blockingEdges)
	buildGaps := newBuildGaps(buildEdges, names)
	publishGaps := newPublishGaps(publishEdges, names)
	var blockingGaps *stageGaps
	if blockingNeedsOutcomeEdges(blockingEdges, publishEdges) {
		blockingGaps = newBlockingGaps(blockingEdges, names)
	}
	buildGaps.addPassThroughs(sched)
	publishGaps.addPassThroughs(sched)
	if blockingGaps != nil {
		blockingGaps.addPassThroughs(sched)
	}
	for _, name := range names {
		sp := plans[name]
		sp.addEdges(sched)
		first := sp.readinessTargets()
		versionTask := task{name, taskVersion, ""}
		if _, hasVersionTask := sp.byTask[versionTask]; hasVersionTask {
			for _, prov := range versionPickupProviders(p.Releases[name], changed) {
				sched.AddEdge(plans[prov].recording, versionTask)
			}
		}
		for _, prov := range p.Providers[name] {
			if !changed[prov] {
				continue
			}
			for _, waited := range resolveFirstTaskWaits(p.Releases[prov].Pkg.Space.ProviderRelation, plans[prov]) {
				addEdges(sched, waited, first)
			}
		}
		for _, prov := range publishEdges.providers[name] {
			if changed[prov] {
				sched.AddEdge(plans[prov].recording, sp.recording)
			}
		}
		if blockingGaps != nil {
			for _, prov := range blockingEdges.providers[name] {
				if changed[prov] {
					sched.AddEdge(plans[prov].recording, sp.recording)
				}
			}
		}
		// A selected cause, version pickup, or blocking provider must have a
		// settled outcome before this package decides what it may publish.
		// Publication kinds may omit those edges, but that cannot turn a
		// still-running provider into a successful one at admission.
		for _, prov := range releaseOutcomeProviders(name, p, changed) {
			sched.AddEdge(plans[prov].recording, sp.recording)
		}
		buildGaps.addConsumerEdges(sched, name, first)
		publishGaps.addConsumerEdges(sched, name, []task{sp.recording})
		if blockingGaps != nil {
			blockingGaps.addConsumerEdges(sched, name, []task{sp.recording})
		}
	}
	buildGaps.addGapEdges(sched)
	publishGaps.addGapEdges(sched)
	if blockingGaps != nil {
		blockingGaps.addGapEdges(sched)
	}
	if e.PublishGroup != nil {
		e.addPublishGroupEdges(sched, p, changed, plans)
	}
	return sched, blockingReach
}

// addEdges states that every one of the after tasks waits for before.
func addEdges(sched *graph.Scheduler[task], before task, after []task) {
	for _, t := range after {
		sched.AddEdge(before, t)
	}
}

// addPublishGroupEdges chains the recording tasks of each publish group in
// plan order.
func (e *Executor) addPublishGroupEdges(sched *graph.Scheduler[task], p *plan.Plan, changed map[string]bool,
	plans map[string]*stagePlan) {
	last := make(map[string]string)
	for _, name := range p.Order {
		if !changed[name] {
			continue
		}
		group := e.PublishGroup(p.Releases[name])
		if group == "" {
			continue
		}
		if previous := last[group]; previous != "" {
			sched.AddEdge(plans[previous].recording, plans[name].recording)
		}
		last[group] = name
	}
}

// drain runs the task graph under the stage budgets until every task ran or
// the tasks' context stopped.
func (r *run) drain(tasks context.Context, sched *graph.Scheduler[task], changed map[string]bool) error {
	// Sign and version tasks share the build budget: they are short local
	// manifest updates leading straight into the build. Tests tasks share it
	// too: they are the build's own continuation, checking what it produced. syncLock has its own
	// budget — almost always 1 — because its whole reason to exist is
	// serialising lock file regeneration. Draining per class keeps the budgets independent, so
	// a stalled stage never blocks another's.
	budgets := map[StageBudget]int{
		BudgetBuild:    r.BuildConcurrency,
		BudgetPublish:  r.PublishConcurrency,
		BudgetSyncLock: syncLockBudget(r.plan, changed),
	}
	return graph.Drain(tasks, sched,
		func(t task) StageBudget { return r.plans[t.pkg].stage(t).Budget },
		func(b StageBudget) int { return budgets[b] },
		// A package's configured weight is how many stage slots its tasks
		// occupy. syncLock keeps the ordinary cost: its budget exists to
		// serialise lock-file writers, not to price packages.
		func(t task) int {
			pkg := r.plan.Releases[t.pkg].Pkg
			switch r.plans[t.pkg].stage(t).Budget {
			case BudgetPublish:
				return pkg.PublishWeight
			case BudgetSyncLock:
				return 1
			default:
				return pkg.BuildWeight
			}
		},
		func(t task) { r.execute(tasks, t) })
}

// reportStopped settles a task graph that did not drain.
func (r *run) reportStopped(ctx, tasks context.Context, err error) {
	// Interrupted, stopped by a task's internal error, or (impossibly
	// after E200) cyclic: tasks that never launched left their packages
	// pending. Cancelled, not failed — nothing about them went wrong, and
	// the next run picks them up unchanged. The names are collected under
	// the lock and announced after it: the observer is called outside the
	// mutex everywhere.
	for _, name := range r.cancelPending() {
		ev := packageEvent(name, r.plan.Releases[name], EventPackageCancelled)
		ev.Status = StatusCancelled.String()
		r.notify(ev)
	}
	switch {
	case errors.Is(context.Cause(tasks), errTaskPanicked) && ctx.Err() == nil:
		r.Log.Error().Msg("run stopped after an internal error: remaining packages cancelled; " +
			"completed releases keep their records")
	case ctx.Err() != nil:
		r.Log.Warn().Msg("run interrupted: remaining packages cancelled; completed releases keep their records")
	default:
		r.Log.Error().Err(err).Msg("task graph stalled")
	}
}

// prepareNativeWrites builds the inputs of the native manifest writes once,
// and only what the releasing packages need: the manifest-name and folder
// indexes of the whole workspace when a releasing package auto-versions, and
// the scanner alone when one only signs, because writing a package's own
// version reads nobody else's manifests.
func (r *run) prepareNativeWrites(ctx context.Context, changed map[string]bool) {
	isSigning := false
	for name := range changed {
		space := r.plan.Releases[name].Pkg.Space
		if space.AutoVersion != nil {
			r.ensureScanner()
			r.avNames, r.avDirs = WorkspaceNames(ctx, r.scan, r.plan, r.Log)
			return
		}
		isSigning = isSigning || space.AutoSign != nil
	}
	if isSigning {
		r.ensureScanner()
	}
}

// ensureScanner defaults the manifest scanner to the filesystem's.
func (r *run) ensureScanner() {
	if r.scan == nil {
		r.scan = scanner.New()
	}
}

// cancelPending marks every package still pending as cancelled and names
// them, in name order, once the graph has stopped early.
func (r *run) cancelPending() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var cancelled []string
	for name, res := range r.results {
		if res.Status == StatusPending {
			res.Status = StatusCancelled
			cancelled = append(cancelled, name)
		}
	}
	slices.Sort(cancelled)
	return cancelled
}

// run is the shared state of one Executor.Run invocation. Every task
// goroutine works against the same value, so the plan, the workspace
// variables, the login gates and the result map with its mutex live here
// instead of travelling through every helper's parameter list. mu guards
// results, started and avChanged; everything else is read-only once Run has
// built it.
type run struct {
	*Executor
	plan *plan.Plan
	// plans are the releasing packages' stage plans, read-only once Run has
	// built them: which stages each runs, in which order, and under which
	// descriptor.
	plans  map[string]*stagePlan
	wsVars []string
	logins map[spaceLoginKey]*spaceLogin
	// stop cancels the tasks' own context with a cause, which is how a task
	// that panicked ends every task in flight and lets Drain start nothing
	// new, while the run's own context stays live for its closing phase.
	stop context.CancelCauseFunc
	// publishReach (guarded by mu) indexes, per changed consumer, the changed
	// providers it reaches only through packages this run does not release
	// (§19.2, §19.3), walked on the first question asked of it.
	publishReach *stageReach
	// reachedProviders (guarded by mu) lists them per consumer once the skip
	// cascade needs them; see resolveReachedProviders.
	reachedProviders map[string][]string
	// isAnyDead (guarded by mu) records that a releasing package failed, was
	// skipped or had its records blocked. Until one has, the cascade has
	// nothing to act on, and an admission asks no closure at all.
	isAnyDead bool
	results   map[string]*Result
	mu        sync.Mutex
	started   map[string]time.Time
	// The native auto-versioning inputs, built once in Run when any releasing
	// package's space enables it: the manifest scanner and the workspace's
	// manifest-name and folder indexes (see workspaceNames).
	scan    scanner.Scannerx
	avNames map[string]string
	avDirs  map[string]string
	// avChanged (guarded by mu) records which packages' version stages
	// actually modified a manifest — natively or through a flow.version
	// script — so a syncLock task with nothing to regenerate can skip its
	// subprocess instead of serialising an empty `npm install` per package.
	avChanged map[string]bool
	// reconciledProviders and builtPackages (guarded by mu) are what §19.5
	// needs to know before a consumer publishes: which providers' planned
	// versions its version stage wrote into its manifests, and whether a build
	// command has since run over them. See recordReconciliation.
	reconciledProviders map[string][]string
	builtPackages       map[string]bool
	// exports (guarded by mu) are what each finished task's scripts could see
	// and exported: its predecessors' exports and its own. A task reads the
	// exports of the tasks that reach it and nothing else (§13.11), so two
	// stages of one package that run at once never write one list.
	exports map[task][]plan.Output
	// failedTasks (guarded by mu) are the tasks that failed without failing
	// their package: the stages that do not precede the record. What waits on
	// one does not run.
	failedTasks map[task]bool
	// inFlight (guarded by mu) counts each package's running tasks, and
	// outcomes holds the revert and the outcome hook a failed or skipped
	// package owes once its last running task has ended, so a folder is never
	// restored under a stage that is still writing it.
	inFlight map[string]int
	outcomes map[string]*pendingOutcome
	// touched (guarded by mu) records the packages a hook, a command or a
	// native step ran for, which is what a skip restores the folder of: a
	// task that ran nothing wrote nothing.
	touched map[string]bool
	// A refused existing publication prevents later publish attempts in this run.
	isPublicationRefused bool
}

// touch records, under mu, that something ran in a package's folder.
func (r *run) touch(pkg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.touched == nil {
		r.touched = make(map[string]bool)
	}
	r.touched[pkg] = true
}

// pendingOutcome is what a package that failed or was skipped still owes once
// nothing of it is running: restoring its folder, and its outcome hook.
type pendingOutcome struct {
	rel           *plan.Release
	log           zerolog.Logger
	isReverted    bool
	isInterrupted bool
	hook          string
	commands      []string
	extra         []string
}

// enter counts one task of a package as running. The maps are made on first
// use as well as in Run, for a run a test assembles by hand.
func (r *run) enter(pkg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inFlight == nil {
		r.inFlight = make(map[string]int)
	}
	if r.exports == nil {
		r.exports = make(map[task][]plan.Output)
	}
	if r.outcomes == nil {
		r.outcomes = make(map[string]*pendingOutcome)
	}
	if r.failedTasks == nil {
		r.failedTasks = make(map[task]bool)
	}
	r.inFlight[pkg]++
}

// finishTask ends one task: it remembers what the task could see and
// exported, and, as the package's last running task, settles the outcome the
// package owes.
func (tc *taskCtx) finishTask(ctx context.Context, res *Result) {
	tc.mu.Lock()
	tc.exports[tc.t] = tc.rel.Outputs
	tc.inFlight[tc.t.pkg]--
	owed := tc.outcomes[tc.t.pkg]
	if tc.inFlight[tc.t.pkg] > 0 || owed == nil {
		tc.mu.Unlock()
		return
	}
	delete(tc.outcomes, tc.t.pkg)
	tc.mu.Unlock()
	tc.run.settleOutcome(ctx, owed)
}

// owe records the outcome a package owes, keeping the first: a package fails
// or is skipped once, however many of its tasks were running.
func (r *run) owe(pkg string, outcome *pendingOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outcomes == nil {
		r.outcomes = make(map[string]*pendingOutcome)
	}
	if _, isOwed := r.outcomes[pkg]; !isOwed {
		r.outcomes[pkg] = outcome
	}
	if r.inFlight[pkg] > 0 {
		return
	}
	// Nothing of the package is running: a task settled outside execute,
	// which a test assembling a run by hand does, so the outcome is owed to
	// nobody and settles now.
	owed := r.outcomes[pkg]
	delete(r.outcomes, pkg)
	r.mu.Unlock()
	r.settleOutcome(context.Background(), owed)
	r.mu.Lock()
}

// settleOutcome restores the folder and runs the outcome hook of a package
// nothing is running for any more. The hook observes an outcome that has
// already settled, so it only warns.
func (r *run) settleOutcome(ctx context.Context, owed *pendingOutcome) {
	if owed.isReverted {
		revertCtx := ctx
		if owed.isInterrupted {
			revertCtx = context.WithoutCancel(ctx)
		}
		r.revert(revertCtx, owed.rel, owed.log)
	}
	if owed.hook == "" || len(owed.commands) == 0 || owed.isInterrupted {
		return
	}
	tc := &taskCtx{run: r, t: task{owed.rel.Pkg.Name, taskBuild, ""}, rel: owed.rel, log: owed.log}
	tc.updates = liveProviderUpdates(owed.rel.Pkg.Name, r.plan, r.results)
	_ = tc.hook(ctx, owed.hook, owed.commands, false, owed.extra...)
}

// visibleRelease is the release as one task sees it: a copy whose outputs are
// the exports of the tasks that reach it, merged in the stage order, so that
// what a stage reads is a function of the graph and never of which of two
// unordered stages finished first.
func (r *run) visibleRelease(t task, rel *plan.Release) *plan.Release {
	visible := *rel
	// What the release carried into the run, an export a caller handed it,
	// is seen by every task; what tasks export is seen along the edges.
	visible.Outputs = append([]plan.Output(nil), rel.Outputs...)
	r.mu.Lock()
	defer r.mu.Unlock()
	sp := r.plans[t.pkg]
	if sp == nil {
		return &visible
	}
	for _, before := range sp.predecessors(t) {
		MergeOutputs(&visible, r.exports[before])
	}
	return &visible
}

// hasFailedPredecessor reports, with mu held, whether a task waits on a task
// that failed without failing the package.
func (r *run) hasFailedPredecessor(t task) (task, bool) {
	sp := r.plans[t.pkg]
	if sp == nil {
		return task{}, false
	}
	for _, before := range sp.predecessors(t) {
		if r.failedTasks[before] {
			return before, true
		}
	}
	return task{}, false
}

// syncLockBudget resolves the run-wide syncLock concurrency: the smallest
// value voted by the releasing autoVersion spaces with syncLock scripts (0
// meaning the default), or 1 — the safe serialisation a shared lock file
// needs — when nobody votes.
func syncLockBudget(p *plan.Plan, changed map[string]bool) int {
	budget := 0
	for name := range changed {
		av := p.Releases[name].Pkg.Space.AutoVersion
		if av == nil || len(av.SyncLock) == 0 {
			continue
		}
		vote := av.SyncLockConcurrency
		if vote == 0 {
			vote = 1
		}
		if budget == 0 || vote < budget {
			budget = vote
		}
	}
	if budget == 0 {
		return 1
	}
	return budget
}

// taskCtx is one task's execution context: the task, the release it belongs
// to, its live provider updates and its logger — the values every hook and
// environment of the task shares.
type taskCtx struct {
	*run
	t   task
	rel *plan.Release
	// stage is the descriptor this task's stage runs and reports under,
	// resolved once when the task starts (see resolveStage).
	stage   Stage
	updates []providerUpdate
	// deadPickups names the providers whose planned version this package's
	// version stage wrote and which have since died. Non-empty only on a
	// publish task admit decided may still proceed, and the whole of what that
	// task's re-reconciliation has to act on (§19.5).
	deadPickups    []string
	log            zerolog.Logger
	publishRelease func()
	// worker is the node this task's frame was executed on, empty for a frame
	// this run executed itself. Owned by the one goroutine that runs the task,
	// written before the frame's outcome is reported and read by the events
	// that report it.
	worker string
	// isPublished records that the publish frame returned success, set before
	// the first recorder runs. From then on the package is published whatever
	// happens, a panic included (see containPanic). Owned by the task's own
	// goroutine.
	isPublished bool
	// requiredRecordFailed blocks effect-consuming successors even when
	// optional changelog or GitHub recording failures do not block them.
	requiredRecordFailed bool
}

// recordPlacement remembers where one task's frame was executed: on the task,
// whose stage events report it, and on the package's result, whose outcome
// events do.
//
// Both are written because they answer different questions. A stage event says
// where that stage ran, so a publish the orchestrator kept names no worker
// even when the build before it was delegated; a package event says where the
// package's work happened, and it outlives the task that did it.
func (tc *taskCtx) recordPlacement(node string) {
	tc.worker = node
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.results[tc.t.pkg].Worker = node
}

func (tc *taskCtx) finishPublishGuard() {
	if tc.publishRelease != nil {
		tc.publishRelease()
		tc.publishRelease = nil
	}
}

// env builds the DISPAT_* environment of the task's scripts and hooks, over
// the exports this task sees; stage is what DISPAT_STAGE carries.
func (tc *taskCtx) env(stage string) []string {
	return releaseEnv(tc.rel, tc.wsVars, tc.updates, stage)
}

// sequence assembles the task's command sequence in its package folder.
func (tc *taskCtx) sequence(ctx context.Context, stage string, commands []string, failFast bool) Sequence {
	dir, runner, env, mappings := ResolveLocalStageExecution(ctx, tc.rel.Pkg.Dir, tc.Runner, tc.env(stage))
	return Sequence{Runner: runner, Dir: dir, Stage: stage,
		Commands: commands, Env: env, Log: tc.log, FailFast: failFast, OutputPaths: mappings}
}

// critical records a failure that happened after this package published: it
// is logged with its diagnostic code and kept on the result, and it changes
// nothing else. The package stays published, its consumers still run, and the
// command reports the collected criticals on its way out.
func (tc *taskCtx) critical(res *Result, code string, err error, msg string) {
	tc.log.Error().Err(err).Str("code", code).Msg(msg)
	tc.mu.Lock()
	defer tc.mu.Unlock()
	res.Critical = append(res.Critical, fmt.Errorf("%s: %s: %w", code, msg, err))
}

// hook runs one per-package hook sequence with the package's full environment
// and DISPAT_STAGE naming the hook. extra entries ("KEY=value") are appended
// on top — the outcome scripts use them for the failure and skip specifics.
func (tc *taskCtx) hook(ctx context.Context, name string, commands []string, failFast bool, extra ...string) error {
	return tc.runHook(ctx, name, commands, failFast, nil, extra...)
}

// runHook is hook with a warned callback for a warn-only sequence, so a
// caller that only warns can still answer whether every command succeeded.
func (tc *taskCtx) runHook(ctx context.Context, name string, commands []string, failFast bool,
	warned func(), extra ...string) error {
	if len(commands) == 0 {
		return nil
	}
	tc.log.Debug().Str("hook", name).Msg("hook started")
	seq := tc.sequence(ctx, name, commands, failFast)
	seq.Env = append(seq.Env, extra...)
	seq.Warned = warned
	return seq.RunMergingOutputs(ctx, tc.rel)
}

// admit is a task's prelude: the questions asked under the run's lock before
// any script exists. It settles whether this task runs at all — an earlier
// stage already failed it, the run was interrupted before it started, or a
// provider's outcome cascades a skip onto it — and, when it does run, resolves
// which provider updates are still live for it.
//
// It reports whether the caller should proceed. Every path that answers false
// has already recorded the outcome and told the observers about it.
func (r *run) admit(ctx context.Context, tc *taskCtx, res *Result) bool {
	verdict := r.settleAdmission(ctx, tc, res)
	switch {
	case verdict.isAdmitted:
		return true
	case verdict.isCancelled:
		ev := packageEvent(tc.t.pkg, tc.rel, EventPackageCancelled)
		ev.Status = StatusCancelled.String()
		r.notify(ev)
	case verdict.blocker != "":
		r.reportBlocked(ctx, tc, verdict)
	}
	return false
}

// admission is what settleAdmission decided about one task: that it runs, that
// it was cancelled before it started, that a provider blocked it (with the
// provider, the sentence and whether an earlier stage already ran), or, with
// every field zero, that an earlier stage already settled the package.
type admission struct {
	isAdmitted  bool
	isCancelled bool
	blocker     string
	reason      string
	isStarted   bool
}

// settleAdmission asks admit's questions under mu and records the answer
// before it lets go, so no other task can read a package whose outcome is
// decided and not yet written. The lock is released through defer: a panic in
// here must not leave it held, or the task's own containment and every other
// task would wait on it for good.
func (r *run) settleAdmission(ctx context.Context, tc *taskCtx, res *Result) admission {
	t := tc.t
	r.mu.Lock()
	defer r.mu.Unlock()
	if tc.stage.IsObserving {
		// A stage that does not precede the record never withholds it: it
		// runs beside or after the package's release and settles nothing.
		if res.Status != StatusPending && res.Status != StatusPublished || ctx.Err() != nil {
			return admission{}
		}
		if res.RecordBlocked {
			r.failedTasks[t] = true
			tc.log.Warn().Msg(tc.stage.Name + " not run: the package's release record is blocked")
			return admission{}
		}
		if failed, isBlocked := r.hasFailedPredecessor(t); isBlocked {
			r.failedTasks[t] = true
			tc.log.Warn().Str("failedStage", r.plans[t.pkg].stage(failed).Name).
				Msg(tc.stage.Name + " not run: a stage it waits on failed")
			return admission{}
		}
		return admission{isAdmitted: true}
	}
	if res.Status != StatusPending { // failed or skipped at an earlier stage
		return admission{}
	}
	if ctx.Err() != nil {
		// Interrupted between scheduling and start: no scripts, no hooks.
		res.Status = StatusCancelled
		return admission{isCancelled: true}
	}
	if t.kind == taskPublish && r.isPublicationRefused {
		res.Status = StatusCancelled
		tc.log.Warn().Str("code", plan.CodeAdoptionRefused).
			Msg("publish not started: this run met an artefact it may not adopt, and starts no further publication")
		return admission{isCancelled: true}
	}
	if skip, blocker := shouldSkip(t.pkg, r.plan, r.results, r.resolveReachedProviders(t.pkg)); skip {
		reason := formatSkipReason(blocker,
			r.plan.Releases[blocker].Pkg.Space.ProviderRelation,
			r.results[blocker].RecordBlocked)
		return r.markBlocked(tc, res, blocker, reason)
	}
	// A consumer proceeding past a provider that died after its version stage
	// publishes what that provider actually published, never the planned
	// version the stage wrote up front (§19.5). Whether that is achievable is
	// decided here, before anything of the publish, or of the records of a
	// package with no publish, runs.
	if t.kind == taskPublish || t.kind == taskRecord {
		if dead := r.resolveDeadPickups(t.pkg); len(dead) > 0 {
			if r.builtPackages[t.pkg] {
				return r.markBlocked(tc, res, dead[0], formatEmbeddedSkipReason(dead[0]))
			}
			tc.deadPickups = dead
		}
	}
	if _, ok := r.started[t.pkg]; !ok {
		r.started[t.pkg] = time.Now()
	}
	if tc.stage.IsPreparing {
		res.IsPrepared = true
	}
	// Resolve which provider updates are still live at this moment: providers
	// that failed or were skipped never got their new version out, so
	// manifests must not be synced to them and scripts must not act on them.
	// Every stage gets the answer — a publish script choosing a dist-tag wants
	// it as much as the version script that synced manifests — and it is
	// per-task on purpose: a provider can fail between this package's build
	// and its publish, and each stage must see the truth of its own moment.
	tc.updates = liveProviderUpdates(t.pkg, r.plan, r.results)
	return admission{isAdmitted: true}
}

// resolveReachedProviders answers, with mu held, the providers shouldSkip
// reads beyond the package's own: the changed providers it reaches through
// packages this run does not release, once any package is dead, and none
// before, because shouldSkip passes over a live provider. Listed for every
// consumer up front, they cost consumers times providers behind one shared
// unreleased package on every run, when only a run with a failure reads them.
// Once one is read it is kept, so every later admission of the package costs
// what an admission over a list made up front did.
func (r *run) resolveReachedProviders(pkg string) []string {
	if !r.isAnyDead {
		return nil
	}
	if reached, isListed := r.reachedProviders[pkg]; isListed {
		return reached
	}
	reached := r.publishReach.Indirect(pkg)
	r.reachedProviders[pkg] = reached
	return reached
}

// markBlocked records, with mu held, a package the run planned and will not
// attempt. It answers what reportBlocked needs to tell every observer of it
// once the lock is released.
//
// One function for both reasons a publish is blocked: the ordinary §19.3
// cascade and the build that already embedded a version nobody published. They
// differ in the sentence they carry and in nothing else, and a second copy of
// the event, the hook and the revert is how the two would drift apart.
func (r *run) markBlocked(tc *taskCtx, res *Result, blocker, reason string) admission {
	res.Status = StatusSkipped
	res.Blocked, res.BlockedBy = true, blocker
	r.isAnyDead = true
	res.RecordBlocked = r.results[blocker].RecordBlocked
	tc.updates = liveProviderUpdates(tc.t.pkg, r.plan, r.results)
	return admission{blocker: blocker, reason: reason, isStarted: r.touched[tc.t.pkg]}
}

// reportBlocked tells every observer of a package markBlocked recorded, outside
// the lock: the W194 line, the event, the revert of an earlier stage's edits
// and the warn-only onSkip hook.
func (r *run) reportBlocked(ctx context.Context, tc *taskCtx, verdict admission) {
	t, rel, log := tc.t, tc.rel, tc.log
	// Planned, but not attempted because a dependency failed to publish.
	// Non-suppressible (§16): a package that was in the plan and produced
	// nothing must be accounted for.
	log.Warn().Str("code", plan.CodeBlocked).Str("reason", verdict.reason).Msg("skipped")
	ev := packageEvent(t.pkg, rel, EventPackageSkipped)
	ev.Status, ev.Code, ev.BlockedBy = StatusSkipped.String(), plan.CodeBlocked, verdict.blocker
	r.notify(ev)
	// The revert and the onSkip hook wait for the package's last running
	// task; onSkip observes a skip that has already settled, so it only
	// warns, and DISPAT_BLOCKED_BY names the provider responsible.
	r.owe(t.pkg, &pendingOutcome{rel: rel, log: log,
		isReverted: verdict.isStarted && r.isRevertedOnSkip(rel),
		hook:       "onSkip", commands: rel.Pkg.Space.OnSkipScript,
		extra: []string{"DISPAT_BLOCKED_BY=" + verdict.blocker}})
}

// resolveDeadPickups lists, with mu held, the providers this package's version
// stage reconciled it to and that have since failed, been skipped or had their
// records blocked, in name order.
//
// It is the question §19.5 asks before a consumer publishes: the version stage
// wrote each provider's PLANNED version because at that moment the provider
// was still going to publish it, and a provider that died afterwards leaves
// the manifests naming a version that will never exist. Empty for a package
// whose version stage reconciled nothing, and empty on the ordinary run where
// every provider it picked up published.
func (r *run) resolveDeadPickups(pkg string) []string {
	var dead []string
	for _, picked := range r.reconciledProviders[pkg] {
		res, ok := r.results[picked]
		if !ok {
			continue // a provider this run does not release cannot have died in it
		}
		if res.Status == StatusFailed || res.Status == StatusSkipped || res.RecordBlocked {
			dead = append(dead, picked)
		}
	}
	slices.Sort(dead)
	return dead
}

// recordReconciliation remembers, under mu, which providers one package's
// version stage actually reconciled it to, whether its build ran a command of
// its own, and whether its syncLock stage ran its commands.
//
// Each is recorded from the stage that did the work rather than derived from
// the plan afterwards, because each question is about a moment: which
// providers were still alive when the manifests were written, whether
// anything has since been built out of them, and whether a lock file was
// regenerated from them. A stage with neither a native reconciliation nor a
// script wrote nothing and is not recorded.
func (tc *taskCtx) recordReconciliation(frame stage) {
	hasWork := frame.native != nil || len(frame.commands) > 0
	tc.mu.Lock()
	defer tc.mu.Unlock()
	switch tc.t.kind {
	case taskVersion:
		if !hasWork {
			return
		}
		picked := make([]string, 0, len(tc.updates))
		for _, u := range tc.updates {
			picked = append(picked, u.Package)
		}
		tc.reconciledProviders[tc.t.pkg] = picked
	case taskBuild:
		if len(frame.commands) > 0 {
			tc.builtPackages[tc.t.pkg] = true
		}
	case taskSyncLock:
		if len(frame.commands) > 0 {
			tc.results[tc.t.pkg].IsSyncLockRun = true
		}
	}
}

// reconcileToPublished re-runs the package's version reconciliation against
// the providers that are still alive, so that what it publishes names each
// provider's version as PUBLISHED rather than as planned (§19.5).
//
// The native reconciliation needs nothing else said to it: it compares every
// declared range against the provider's end-of-run version, and for a provider
// that died that version is its baseline, so the planned version it wrote
// earlier is written back down. A space reconciling through scripts gets the
// same chance, with the dead providers already out of DISPAT_UPDATED_*; what
// its script then does is the script's own business, which is why a package
// whose build has already run is blocked instead of re-reconciled.
//
// The stage's hooks are deliberately not re-run: beforeVersion and postVersion
// bracket the version stage, and firing them again inside a publish would run
// a lock-file regeneration or a commit step a second time. The same reason
// leaves syncLock alone.
func (tc *taskCtx) reconcileToPublished(ctx context.Context) error {
	space := tc.rel.Pkg.Space
	tc.log.Info().Strs("providers", tc.deadPickups).
		Msg("publish: reconciling to the providers that published")
	if space.AutoVersion != nil {
		if err := tc.autoVersion(ctx, space.AutoVersion); err != nil {
			return fmt.Errorf("re-reconciling %s to its published providers: %w", tc.t.pkg, err)
		}
	}
	if len(space.VersionScript) == 0 {
		return nil
	}
	// The live update set, not the null-out stageFor applies to a version
	// stage with nothing left to sync: there a script that would write nothing
	// is not worth a subprocess, while here it is the only thing that can
	// unwrite what the first pass wrote.
	if err := tc.sequence(ctx, space.VersionStage.String(), space.VersionScript, true).
		RunMergingOutputs(ctx, tc.rel); err != nil {
		return fmt.Errorf("re-running %s version scripts against its published providers: %w", tc.t.pkg, err)
	}
	return nil
}

// stageFor is the frame this task runs: the stage's own commands and the
// hooks that bracket them, chosen by the task kind, plus the two cases where
// a stage that exists has nothing to do.
//
// It is a decision rather than an action: nothing here runs, so the whole
// question "what does this task consist of" is answerable in one place, which
// is what the syncLock skip and the dead-provider version skip both turn on.
func (tc *taskCtx) stageFor() stage {
	r, t, rel, log := tc.run, tc.t, tc.rel, tc.log
	space := rel.Pkg.Space
	frame := tc.stage.frame(space)
	switch t.kind {
	case taskVersion:
		if space.AutoVersion != nil {
			frame.native = func(ctx context.Context) error { return tc.autoVersion(ctx, space.AutoVersion) }
		}
	case taskSign:
		if space.AutoSign != nil {
			frame.native = func(ctx context.Context) error { return tc.autoSign(ctx, space.AutoSign) }
		}
	case taskSyncLock:
		// The lock-sync sequence: no hooks of its own, budgeted separately —
		// and skipped outright when this package's version stage changed no
		// file, so a quiet release does not serialise one lock regeneration
		// per package for nothing.
		//
		// A space that configured neither reconciling strategy is the
		// exception: it never produces that signal, so gating on one would
		// mean its scripts never ran at all.
		if !r.isManifestChanged(t.pkg) && space.AutoVersion.IsReconciling() {
			log.Debug().Msg("syncLock: nothing was reconciled, nothing to regenerate")
			frame = stage{}
		}
	}
	if t.kind == taskVersion && len(tc.updates) == 0 && len(rel.Updates) > 0 {
		// Every provider this package picks up a version from failed or was
		// skipped (the package itself proceeds on its own changes): there is
		// nothing to sync manifests to, so the version scripts — hooks
		// included — must not run. Native reconciliation is different: it
		// compares against baselines too (§9.4) and never writes a dead
		// provider's planned version, so it proceeds.
		log.Info().Msg(tc.stage.Name + ": no successfully updated providers, skipping scripts")
		frame.commands, frame.before, frame.after = nil, nil, nil
	}

	return frame
}

// execute runs a single task to completion: a stage of the package, its
// beforeAll hook, or the records of a package with no publish.
func (r *run) execute(ctx context.Context, t task) {
	rel := r.plan.Releases[t.pkg]
	res := r.results[t.pkg]
	descriptor := r.plans[t.pkg].stage(t)
	log := r.Log.With().
		Str("package", t.pkg).
		Str("stage", descriptor.Name).
		Str("version", rel.Next.String()).
		Logger()
	if descriptor.Label != "" {
		log = log.With().Str("label", descriptor.Label).Logger()
	}
	r.enter(t.pkg)
	tc := &taskCtx{run: r, t: t, rel: r.visibleRelease(t, rel), stage: descriptor, log: log}
	// The first defer, so it runs last: once the panic container has settled
	// a task that panicked, the package's last task settles what it owes.
	defer tc.finishTask(ctx, res)
	// After it, so it runs before it: after the publish guard and the
	// snapshot guard have been given back by their own defers, which a
	// panic unwinds through like any other return.
	defer tc.containPanic(ctx, res)

	if !r.admit(ctx, tc, res) {
		return
	}

	switch t.kind {
	case taskBeforeAll:
		// beforeAll runs before the package has a stage at all, so its
		// failure ends the package under the name of the stage it never
		// entered.
		r.touch(t.pkg)
		if err := tc.hook(ctx, "beforeAll", rel.Pkg.Space.BeforeAllScript, true); err != nil {
			tc.failTask(ctx, res, err, "beforeAll hook failed")
		}
		return
	case taskRecord:
		tc.recordWithoutPublish(ctx, res)
		return
	}

	if t.kind == taskPublish && tc.PendingRecord != nil {
		if pending, isPending := tc.PendingRecord(rel); isPending {
			tc.finishPendingRecord(ctx, res, pending)
			return
		}
	}

	frame := tc.stageFor()
	tc.recordReconciliation(frame)
	if frame.native != nil || len(frame.before)+len(frame.commands)+len(frame.after) > 0 {
		r.touch(t.pkg)
	}

	// The event reports the task starting, not a script: a stage with no
	// configured command still runs and still transitions, so it is still
	// observed. It names no worker even for a frame this run delegates,
	// because a frame is placed on a node while it waits for one: at this
	// moment there is no node to name, and the event that follows carries it.
	stageEv := packageEvent(t.pkg, rel, EventStageStarted)
	stageEv.Stage, stageEv.Label = tc.stage.Name, tc.stage.Label
	r.notify(stageEv)

	if tc.stage.IsObserving {
		if !tc.observe(ctx, frame) {
			r.mu.Lock()
			r.failedTasks[t] = true
			r.mu.Unlock()
			return
		}
		stageEv.Name = EventStageSucceeded
		r.notify(stageEv)
		return
	}

	if len(tc.deadPickups) > 0 {
		// Before anything of the publication, and on this machine whatever the
		// run delegates: the reconciliation writes the checkout a placed frame
		// is snapshotted from, exactly as the version stage does.
		if err := tc.reconcileToPublished(ctx); err != nil {
			tc.failTask(ctx, res, err, "reconciling to the providers that published failed")
			return
		}
	}

	if t.kind == taskPublish {
		if err := tc.loginGate(ctx); err != nil {
			tc.failTask(ctx, res, err, "login failed")
			return
		}
		if err := tc.guardPublish(ctx); err != nil {
			tc.failTask(ctx, res, err, "acquiring publish repository guard failed")
			return
		}
		defer tc.finishPublishGuard()
	}

	what, err := tc.runStage(ctx, frame)
	if err != nil && t.kind == taskPublish && what == tc.stage.Name+" script failed" {
		what, err = tc.reconcilePublication(ctx, what, err)
	}
	if err != nil {
		tc.failTask(ctx, res, err, what)
		return
	}
	if (t.kind == taskSign || t.kind == taskVersion) && len(frame.commands) > 0 {
		// A flow.sign or flow.version script may have edited manifests too;
		// the syncLock skip must stay conservative about what it cannot see.
		tc.markManifestsChanged()
	}
	stageEv.Name = EventStageSucceeded
	// The node that ran it, which the starting event could not name: a frame
	// is placed while it waits for a node, so at the moment the stage starts
	// there is no answer yet. The stage's own placement rather than the
	// package's, so a stage this run kept names nobody.
	stageEv.Worker = tc.worker
	r.notify(stageEv)
	if t.kind != taskPublish {
		log.Info().Msg(tc.stage.Name + " succeeded")
		return
	}
	tc.publishTail(ctx, res)
}

// guardPublish takes the repository guard the publish and the records run
// under, when the run has one.
func (tc *taskCtx) guardPublish(ctx context.Context) error {
	if tc.AcquirePublish == nil {
		return nil
	}
	releasePublish, err := tc.AcquirePublish(ctx, tc.rel)
	if err != nil {
		return err
	}
	tc.publishRelease = releasePublish
	return nil
}

// recordWithoutPublish is the record task of a package with no publish stage:
// the records follow its last gating stage under the repository guard, and a
// package with no stage at all is recorded at once.
func (tc *taskCtx) recordWithoutPublish(ctx context.Context, res *Result) {
	if len(tc.deadPickups) > 0 {
		if err := tc.reconcileToPublished(ctx); err != nil {
			tc.failTask(ctx, res, err, "reconciling to the providers that published failed")
			return
		}
	}
	if err := tc.guardPublish(ctx); err != nil {
		tc.failTask(ctx, res, err, "acquiring publish repository guard failed")
		return
	}
	defer tc.finishPublishGuard()
	tc.publishTail(ctx, res)
}

// observe runs the frame of a stage that does not precede the record: the
// hooks and the commands each only warn, and no failure among them stops the
// others, because the stage can never withhold the record and failing the
// package would report an unpublished release for a published one. It answers
// whether every part succeeded; what waits on a failed one does not run.
func (tc *taskCtx) observe(ctx context.Context, s stage) bool {
	label := tc.stage
	isSucceeded := true
	note := func() { isSucceeded = false }
	if tc.runHook(ctx, "before"+label.Title, s.before, false, note) != nil {
		note()
	}
	if len(s.commands) > 0 {
		tc.log.Info().Msg(label.Name + " started")
		seq := tc.sequence(ctx, label.Name, s.commands, false)
		seq.Warned = note
		if seq.RunMergingOutputs(ctx, tc.rel) != nil {
			note()
		}
	}
	if tc.runHook(ctx, "post"+label.Title, s.after, false, note) != nil {
		note()
	}
	if !isSucceeded {
		tc.log.Warn().Msg(label.Name + " failed; the release stands and the stages waiting on it do not run")
	}
	return isSucceeded
}

// failTask settles a task of execute that stopped with err, msg being what
// the log reports as having failed: the status, the event, the revert and
// the onFail hook.
func (tc *taskCtx) failTask(ctx context.Context, res *Result, err error, msg string) {
	// A task dying while the context is cancelled died *of* the
	// cancellation (its script was killed mid-run): that is an
	// interruption, not a package failure, and it must not spawn more
	// scripts — no onFail, no announce. The revert still happens, detached
	// from the cancellation, because a half-modified folder is exactly what
	// revertOnFail promises to clean up.
	interrupted := ctx.Err() != nil
	if !tc.settleFailure(res, err, interrupted) {
		// A sibling stage running at the same time settled the package first.
		tc.log.Error().Err(err).Msg(msg)
		return
	}
	if interrupted {
		ev := packageEvent(tc.t.pkg, tc.rel, EventPackageCancelled)
		ev.Status, ev.Error = StatusCancelled.String(), err.Error()
		tc.notify(ev)
		tc.log.Warn().Err(err).Msg(tc.stage.Name + " interrupted")
		tc.owe(tc.t.pkg, &pendingOutcome{rel: tc.rel, log: tc.log,
			isReverted: tc.rel.Pkg.Space.RevertOnFail, isInterrupted: true})
		return
	}
	ev := packageEvent(tc.t.pkg, tc.rel, EventPackageFailed)
	ev.Status, ev.FailedStage, ev.Label, ev.Error = StatusFailed.String(), tc.stage.Name, tc.stage.Label, err.Error()
	// Where the package's work was placed, which the reader of a failure
	// needs first: a build that failed on one node of several is a
	// failure somebody has to go and look at on that node.
	ev.Worker = res.Worker
	var diagnostic interface{ DiagnosticCode() string }
	if errors.As(err, &diagnostic) {
		ev.Code = diagnostic.DiagnosticCode()
	}
	tc.notify(ev)
	event := tc.log.Error().Err(err)
	if ev.Code != "" {
		event.Str("code", ev.Code)
	}
	event.Msg(msg)
	// The revert and the onFail hook wait for the package's last running
	// task, so a folder is never restored under a stage still writing it.
	// onFail observes a failure that has already settled — it runs after the
	// status and the revert, in the folder's final state, and only warns.
	// It fires once: later tasks of a failed package return before reaching
	// any script.
	tc.owe(tc.t.pkg, &pendingOutcome{rel: tc.rel, log: tc.log,
		isReverted: tc.rel.Pkg.Space.RevertOnFail,
		hook:       "onFail", commands: tc.rel.Pkg.Space.OnFailScript,
		extra: []string{"DISPAT_FAILED_STAGE=" + tc.stage.Name, "DISPAT_ERROR=" + err.Error()}})
}

// settleFailure records, under mu, how a task that stopped with err ended:
// cancelled when the run was interrupted, failed at its stage otherwise. It
// answers false when another task of the package settled it first, which two
// stages that run at once can do.
func (tc *taskCtx) settleFailure(res *Result, err error, isInterrupted bool) bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if res.Status != StatusPending {
		return false
	}
	if isInterrupted {
		res.Status = StatusCancelled
		res.Err = err
	} else {
		res.Status = StatusFailed
		res.FailedStage, res.FailedStageLabel = tc.stage.Name, tc.stage.Label
		res.Err = fmt.Errorf("%s: %w", tc.stage.Name, err)
		tc.isAnyDead = true
	}
	res.Duration = time.Since(tc.started[tc.t.pkg])
	return true
}

// loginGate runs the space's once-per-space login before its first publish;
// every other publish of the space waits inside the gate. A login failure
// fails every publish of the space — none of them could have succeeded
// without it. On success the login's exports are merged onto the release
// (safe: only this package's current task touches rel.Outputs).
func (tc *taskCtx) loginGate(ctx context.Context) error {
	space := tc.rel.Pkg.Space
	sl := tc.logins[spaceLoginKey{space.Repository, space.Name}]
	if sl == nil {
		return nil
	}
	sl.once.Do(func() {
		// Set first and replaced by the outcome: a login that panicked has
		// not logged anybody in, and a Once that already ran would otherwise
		// hand every later publish of the space a nil error.
		sl.err = errLoginIncomplete
		lg := tc.Log.With().Str("space", space.Name).Str("stage", "login").Logger()
		lg.Info().Msg("login started")
		// The login exports like any other script; a malformed export fails
		// the login (it is a gating sequence), and what it did export becomes
		// part of every space package's outputs.
		//
		// It runs in the space's own folder, not in whichever package's
		// publish happened to win the race to the gate: a login script
		// reading a local file must see the same folder on every run. That is
		// why the folder is the space's rather than a member's parent — the
		// two coincide for a space package, but a standalone package is its
		// own space, and its parent is one level above the only folder the
		// login has any business in.
		seq := Sequence{Runner: tc.Runner, Dir: space.Dir, Stage: "login",
			Commands: space.LoginScript, Env: loginEnv(space.Name, space.Env, tc.wsVars),
			Log: lg, FailFast: true}
		outs, seqErr, parseErr := seq.capture(ctx, space.Name+":login")
		sl.outputs = outs
		if sl.err = seqErr; sl.err == nil {
			sl.err = parseErr
		}
	})
	if sl.err != nil {
		return fmt.Errorf("space login: %w", sl.err)
	}
	MergeOutputs(tc.rel, sl.outputs)
	return nil
}

// stage is one task's gating frame: the bracketing hooks, the stage's shell
// commands, and the optional native step dispat runs itself (the sign stage's
// auto-signing, the version stage's auto-versioning). One value instead of
// four same-shaped positional parameters, so a call site cannot quietly
// transpose two of them.
type stage struct {
	before   []string
	native   func(context.Context) error
	commands []string
	after    []string
}

// stageFrame runs the task's gating frame: before hook, the native step when
// the stage has one, stage scripts, after hook — each fail-fast, every
// failure failing the release, because all of them exist to decide whether
// the release happens. what labels the failing piece for the log
// ("beforeBuild hook failed", "build script failed").
func (tc *taskCtx) stageFrame(ctx context.Context, s stage) (what string, err error) {
	kind, label := tc.t.kind, tc.stage
	// One line naming what the frame consists of, before any of it runs: a
	// stage that hangs has to leave a record of what it was about to do.
	tc.log.Trace().Int("before", len(s.before)).Int("commands", len(s.commands)).
		Int("after", len(s.after)).Bool("native", s.native != nil).
		Msg(label.Name + ": stage frame resolved")
	if err := tc.hook(ctx, "before"+label.Title, s.before, true); err != nil {
		return "before" + label.Title + " hook failed", err
	}
	if label.IsAuthorized && tc.BeforePublish != nil {
		if err := tc.BeforePublish(ctx, tc.rel); err != nil {
			return "pre-publish repository validation failed", err
		}
	}
	if s.native != nil {
		if err := s.native(ctx); err != nil {
			return formatNativeFailure(kind), err
		}
		tc.log.Debug().Msg(label.Name + ": native version edit applied")
	}
	if len(s.commands) == 0 {
		// No script configured: the stage completes without running anything.
		tc.log.Debug().Msg(label.Name + ": no script configured, nothing to execute")
	} else {
		tc.log.Info().Msg(label.Name + " started")
		if err := tc.sequence(ctx, label.Name, s.commands, true).RunMergingOutputs(ctx, tc.rel); err != nil {
			return label.Name + " script failed", err
		}
	}
	if err := tc.hook(ctx, "post"+label.Title, s.after, true); err != nil {
		return "post" + label.Title + " hook failed", err
	}
	return "", nil
}

// formatNativeFailure names a failed native step the way the log reports it:
// the sign stage's own-version write, or the version stage's reconciliation.
func formatNativeFailure(kind taskKind) string {
	if kind == taskSign {
		return "auto-signing failed"
	}
	return "auto-versioning failed"
}

// markPublished flips a package's status to published under mu, blocking
// its consumers when required records are missing, and hands the release the
// exports its record saw, which is what the closing phase reads of them.
func (tc *taskCtx) markPublished(res *Result) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.plan.Releases[tc.t.pkg].Outputs = tc.rel.Outputs
	res.Status = StatusPublished
	res.RecordBlocked = tc.requiredRecordFailed || (tc.BlockOnRecordFailure && len(res.Critical) > 0)
	if res.RecordBlocked {
		tc.isAnyDead = true
	}
	res.Duration = time.Since(tc.started[tc.t.pkg])
}

// RecordTimeout bounds the record attempt of one published package, which is
// written on a context detached from the run's cancellation: the tag or
// receipt, optional recorders, a fleet's source commit and checkpoint. It is generous on
// purpose, because it exists to end a record whose remote has stopped
// answering, never to cut short one that is merely slow.
const RecordTimeout = 5 * time.Minute

// publishTail finishes a successful publish, or stands in for one on a
// package with no publish stage: the release recorders (changelog file,
// GitHub release, ...), the tag, the status flip, then the warn-only
// postPublish hook of a publish. The stages that observe the release follow
// as tasks of their own.
//
// It takes no failure callback, and that is the point: past the publish there
// is no failure left that may fail the package. Everything here records a
// release that is already out, so each error becomes a critical and the tail
// keeps going.
func (tc *taskCtx) publishTail(ctx context.Context, res *Result) {
	rel, space := tc.rel, tc.rel.Pkg.Space
	// Before the first recorder: from this line the package is published,
	// and even a panic below is reported as a published package with an
	// incomplete record rather than a failed one.
	tc.isPublished = true
	// The publish succeeded, so from here to the status flip this leg of the
	// transaction is committing: it must durably record its completion (§17).
	// Recording and tagging therefore run detached from cancellation — a
	// Ctrl-C that killed the publish would have been an interruption, but one
	// that loses the tag *after* the publish re-releases a released version on
	// the next run, which is the one thing the model forbids. Detached is not
	// unbounded: RecordTimeout keeps a remote that never answers from holding
	// the run, and with it the release lock, for good.
	recCtx, cancelRecord := context.WithTimeout(context.WithoutCancel(ctx), RecordTimeout)
	defer cancelRecord()
	// The source record must reach the shared store before optional recorders
	// can make another external effect or a dependent can consume publication.
	if tc.Tagger != nil && tc.Durable == nil { // nil: tagging deferred to the release-commit phase
		if err := CreateReleaseTag(recCtx, tc.Tagger, rel, tc.Force, tc.log); err != nil {
			tc.requiredRecordFailed = true
			tc.critical(res, TagFailureCode(err), err, "tagging failed")
		} else {
			tc.log.Debug().Str("tag", rel.TagName()).Msg("release tag written")
		}
	}
	if tc.Durable != nil {
		if err := tc.Durable.RecordDurably(recCtx, rel); err != nil {
			tc.requiredRecordFailed = true
			tc.critical(res, TagFailureCode(err), err, "recording the release on the remote failed")
		}
	}
	// A failed required record leaves the package published but withholds
	// optional effects. The next run settles the durable receipt or verifies
	// the destination before trying to complete the record.
	if !tc.requiredRecordFailed {
		for _, rec := range tc.Recorders {
			var err error
			recorder := fmt.Sprintf("%T", rec)
			if aware, ok := rec.(postPublishRecorder); ok {
				err = aware.RecordAfterPublish(recCtx, ctx, rel)
			} else {
				err = rec.Record(recCtx, rel)
			}
			if err == nil {
				// No tag field: in release-commit mode the tag does not exist
				// yet, and a record line stating one would say it does.
				tc.log.Debug().Str("recorder", recorder).Msg("release recorded")
			}
			if err != nil {
				// The next recorder still runs: a changelog that could not be
				// written is no reason to skip the GitHub release as well.
				code := plan.CodeRecordFailed
				if tc.BlockOnRecordFailure {
					code = "E335"
				}
				tc.critical(res, code, err, "release recording failed")
			}
		}
	}
	tc.markPublished(res)
	ev := packageEvent(tc.t.pkg, rel, EventPackagePublished)
	ev.Status, ev.Tag = StatusPublished.String(), rel.TagName()
	// The node the package's delegated work ran on, so a published version
	// can be traced back to the machine that produced it.
	ev.Worker = res.Worker
	tc.notify(ev)
	if tc.requiredRecordFailed {
		tc.finishPublishGuard()
		return
	}
	// In release-commit mode the tag does not exist yet — finalize creates it
	// — so the line names it as planned rather than stating it as a fact.
	if tc.BlockOnRecordFailure && res.RecordBlocked {
		tc.log.Info().Str("tag", rel.TagName()).Msg("published, required release records incomplete")
	} else if tc.Tagger != nil || tc.BlockOnRecordFailure {
		tc.log.Info().Str("tag", rel.TagName()).Msg("published")
	} else {
		tc.log.Info().Str("plannedTag", rel.TagName()).Msg("published, tag deferred to the release commit")
	}
	// The source record and status are settled. Outcome hooks observe them but
	// do not mutate the release transaction, so packages sharing a repository
	// need not remain serialized while postPublish/announce runs.
	tc.finishPublishGuard()

	if ctx.Err() != nil || tc.t.kind != taskPublish || res.RecordBlocked {
		// Interrupted: the release is out and recorded; observers stay silent.
		// Recorded without a publish: there is no publish for a hook to follow.
		return
	}

	// postPublish observes a release that is already out, which is why it
	// runs after the status settles and only warns: failing the package now
	// would report an unpublished release for a published one.
	_ = tc.hook(ctx, "postPublish", space.PostPublishScript, false)
}

// finishPendingRecord settles the publish task of a package an earlier run
// published and recorded with a receipt only: nothing of the publish frame
// runs, and the tail records the release as this run would have.
func (tc *taskCtx) finishPendingRecord(ctx context.Context, res *Result, pending PendingRecord) {
	current, isExported := tc.rel.Output(IntegrityOutput)
	if !isExported || strings.TrimSpace(current) != pending.Integrity {
		tc.refusePublications()
		tc.isPublished = true
		tc.requiredRecordFailed = true
		tc.critical(res, plan.CodeAdoptionRefused,
			fmt.Errorf("the retry produced an artefact identity different from the publication receipt"),
			"pending publication identity could not be verified")
		tc.markPublished(res)
		ev := packageEvent(tc.t.pkg, tc.rel, EventPackagePublished)
		ev.Status, ev.Code = StatusPublished.String(), plan.CodeAdoptionRefused
		tc.notify(ev)
		return
	}
	if pending.ExportedCommit != "" {
		MergeOutputs(tc.rel, []plan.Output{{Name: plan.PackageCommitExportPrefix + plan.EnvKey(tc.rel.Pkg.Name),
			Value: pending.ExportedCommit, Source: tc.rel.Pkg.Name + ":receipt"}})
	}
	tc.log.Warn().Str("code", plan.CodeRecordPending).Str("tag", tc.rel.TagName()).
		Msg("published by an earlier run that recorded a receipt; finishing its record without publishing again")
	ev := packageEvent(tc.t.pkg, tc.rel, EventStageSucceeded)
	ev.Stage = tc.stage.Name
	tc.notify(ev)
	tc.publishTail(ctx, res)
}

// ErrTagAtOtherCommit reports a release tag that already exists somewhere
// other than this release's target commit.
//
// The tag is left exactly where it is. Moving it would rewrite a record some
// earlier run made, and with force-pushing on, the moved tag would carry that
// over the copy on the remote too — so a local mistake would become everyone's.
// Leaving it alone keeps the damage where it started and leaves the operator a
// repository to reason about.
var ErrTagAtOtherCommit = errors.New("tag already exists at another commit")

// TagFailureCode is the diagnostic code a tagging failure is reported under:
// the pre-existing-tag case is worth telling apart from every other reason
// writing a tag can fail, because it is the one an operator resolves by
// deciding which commit the version really is.
func TagFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrTagAtOtherCommit):
		return plan.CodeTagAtOtherCommit
	case errors.Is(err, ErrRecordNotPushed):
		return plan.CodePushFailed
	}
	return plan.CodeTagFailed
}

// ErrRecordNotPushed reports a release record this run wrote locally and could
// not write to the remote. The record is in this checkout; the remote copy is
// what is missing.
var ErrRecordNotPushed = errors.New("release record not pushed")

// forceTagger is the optional Taggerx extension that can rewrite a tag the
// repository already carries; *gitx.LocalGitx implements it. A Taggerx without it
// simply never forces, which is the right default for a test double or a
// custom tagger written before the option existed.
type forceTagger interface {
	CreateTagForce(ctx context.Context, name, message, target string) error
}

// writeTag creates one tag, forcing when asked and the tagger can.
func writeTag(ctx context.Context, tagger Taggerx, force bool, name, message, target string) error {
	if force {
		if ft, ok := tagger.(forceTagger); ok {
			return ft.CreateTagForce(ctx, name, message, target)
		}
	}
	return tagger.CreateTag(ctx, name, message, target)
}

// CreateReleaseTag creates rel's annotated release tag — the one place the
// tag message is rendered and the PACKAGE_<KEY> export is honoured, shared by
// the in-run tagging above and the finalize phase's deferred tagging: an
// exported commit pins the tag there instead of HEAD (or the release commit).
//
// A tag that already exists at the release's target commit is a skip (W223),
// not an error: the flow tagged early — a `dispat commit --tag` inside a
// stage script — and the durable record the tag exists to be is already
// there. A tag at any other commit stays a hard error, because a wrong tag
// silently accepted would corrupt every future baseline.
func CreateReleaseTag(ctx context.Context, tagger Taggerx, rel *plan.Release, force bool, log zerolog.Logger) error {
	return CreateReleaseTagAs(ctx, tagger, rel, "", force, log)
}

// CreateReleaseTagAs is CreateReleaseTag with the tag name supplied rather
// than computed, and an empty name computes it as usual.
//
// It exists for the nested case. A step command running inside a release stage
// is planning a second time, after earlier packages of the same run have
// already tagged, and a version shared by a fixed versioning group moves under
// it when they do. Naming the tag the outer run decided on is what keeps the
// two agreeing.
func CreateReleaseTagAs(ctx context.Context, tagger Taggerx, rel *plan.Release, name string, force bool, log zerolog.Logger) error {
	tag := rel.TagName()
	if name != "" {
		tag = name
	}
	return createFoundTag(ctx, tagger, rel, tagWrite{name: tag, isForced: force}, log)
}

// createAliasTags writes the extra names a release is published under, after
// its release tag succeeded and at the same commit.
//
// It lives here rather than at each of the three call sites because this is
// the one function every tagging path goes through; adding it anywhere else
// would mean one of them silently not writing aliases.
//
// A failure is warned about and the remaining aliases are still attempted, so
// there is nothing to return: an alias is a convenience ref, not the record of
// the release. The release tag is already written by the time this runs, and
// losing a "v1" is a thing to re-point, not a reason to report a published
// release as broken. It returned an always-nil error until this was written
// down, which read at every call site as a failure somebody had decided to
// pass on.
func createAliasTags(ctx context.Context, tagger Taggerx, rel *plan.Release, log zerolog.Logger) {
	aliases := rel.AliasTags()
	if len(aliases) == 0 {
		return
	}
	_, canForce := tagger.(forceTagger)
	for _, alias := range aliases {
		if alias.Force && !canForce {
			// Without the extension a moving alias cannot move: say so once,
			// rather than letting it fail on "already exists" every release.
			log.Warn().Str("code", plan.CodeAliasTagFailed).Str("tag", alias.Name).
				Msg("alias tag needs to overwrite an existing ref and this tagger cannot force, skipped")
			continue
		}
		if err := writeTag(ctx, tagger, alias.Force, alias.Name, "release "+rel.TagName(), rel.ExportedCommit()); err != nil {
			log.Warn().Err(err).Str("code", plan.CodeAliasTagFailed).Str("tag", alias.Name).
				Msg("alias tag failed")
		}
	}
}

// loginEnv is the space-scoped environment of a login script. Login is a
// space affair — which package's publish happens to trigger it is a scheduling
// accident — so it deliberately carries no package variables: only the space,
// the stage and the workspace listing.
//
// The space's static env does reach it, because a login script is one of the
// scripts a space's env exists for: the registry a package publishes to is
// configuration, and the command authenticating against it needs the same
// value the publish will use. static is the space's resolved pairs.
func loginEnv(space string, static, wsVars []string) []string {
	env := []string{"DISPAT_SPACE=" + space, "DISPAT_STAGE=login"}
	return StaticEnv(static, append(env, wsVars...))
}

// isRevertedOnSkip reports whether a package skipped after an earlier stage
// of it ran has its folder restored: under revertOnFail, and wherever the run
// owns the folder (see IsRunOwnedFolder). A failed package keeps the
// revertOnFail rule alone.
func (e *Executor) isRevertedOnSkip(rel *plan.Release) bool {
	if rel.Pkg.Space.RevertOnFail {
		return true
	}
	return e.IsRunOwnedFolder != nil && e.IsRunOwnedFolder(rel)
}

// revert rolls back all local changes inside the package folder. It runs for
// revertOnFail spaces when a package fails at any stage, and when a package is
// skipped after an earlier stage already ran (the version stage succeeded,
// then a provider's publish failure skipped the package before its build),
// which a folder the run owns reverts without revertOnFail as well.
//
// A run that delegates work snapshots this working tree for its nodes, and a
// restore writes it, so the restore takes the guard the orchestrator's own
// writing stages take: a snapshot never catches a folder half restored.
func (e *Executor) revert(ctx context.Context, rel *plan.Release, log zerolog.Logger) {
	if e.Reverter == nil {
		return
	}
	if e.Remote != nil {
		releaseGuard, err := e.Remote.Guard(ctx, "revert", "")
		if err != nil {
			log.Error().Err(err).Msg("reverting package folder failed: the working tree could not be guarded")
			return
		}
		defer releaseGuard()
	}
	if err := e.Reverter.RevertDir(ctx, rel.Pkg.Dir); err != nil {
		log.Error().Err(err).Msg("reverting package folder failed")
		return
	}
	log.Info().Msg("reverted local changes in package folder")
}

// shouldSkip decides — with mu held — whether a package must be skipped: one
// of its changed providers failed (at any stage) or was skipped, and the
// package has no release reason of its own. It returns the blocking provider.
//
// reached names the changed providers the package does not declare itself and
// reaches through packages this run does not release. They are considered
// exactly as the declared ones are, because §19.3 computes the closure over
// the full workspace graph: a package with no bump this run is still a path
// from a dependent to a failed dependency, and a consumer that publishes
// behind one records a provider movement that never happened. Which relation
// blocks is read from the provider that failed, since a relation is a
// statement about that provider's own consumers wherever they sit.
//
// "No release reason of its own" is the whole of §19.3's rule read exactly:
// EVERY admitted cause of the release comes from a package that failed or was
// blocked. One cause that does not is enough to proceed, and there are three
// kinds: a fresh direct bump, a channel change, and a version this package
// picks up from a provider that is not among the dead. Providers whose outcome
// is still pending count as neither; the check runs again before publish, when
// all provider publishes are final thanks to the task-graph edges.
//
// A provider under a blocking relation outranks every reason of the package's
// own. Under `publish` that is because consumers' builds take the provider's
// *published* release as their input (the dispat images install the binary the
// CLI leg's publish attached), so when that publish never happened the input
// does not exist, and no amount of own work substitutes for it.
// Proceeding would either fail on the missing artifact or, worse, quietly
// build against the provider's previous release and publish it under a version
// that promises the new one. Under `none` it is because the consumer's
// publication is the whole of what follows the provider: a deployment onto
// infrastructure that was never applied is the same mistake one stage later.
// Only `build` leaves the own-reason rule standing by default, which is what
// the key's `false` has always done.
func shouldSkip(pkg string, p *plan.Plan, results map[string]*Result, reached []string) (bool, string) {
	rel := p.Releases[pkg]
	badProvider := ""
	blockingProvider := ""
	// Made on the first dead provider: an admission with none, which is
	// nearly every admission, then allocates nothing.
	var bad map[string]bool
	// Failure blocking is narrower than cause accounting: an optional edge
	// does not block a consumer with its own cause, but a propagated release
	// owed only to that failed provider has no surviving cause to publish.
	markDead := func(prov string) {
		r, ok := results[prov]
		if !ok || (!r.RecordBlocked && r.Status != StatusFailed && r.Status != StatusSkipped) {
			return
		}
		if bad == nil {
			bad = make(map[string]bool)
		}
		bad[prov] = true
		badProvider = prov
	}
	for _, providers := range [...][]string{p.Providers[pkg], reached, rel.DueTo} {
		for _, prov := range providers {
			markDead(prov)
		}
	}
	for _, update := range rel.Updates {
		markDead(update.Name)
	}
	if rel.ChannelFrom != "" {
		markDead(rel.ChannelFrom)
	}
	if badProvider == "" {
		return false, ""
	}
	direct := p.BlockingProviders
	if direct == nil {
		direct = p.Providers
	}
	for _, providers := range [...][]string{direct[pkg], reached} {
		for _, prov := range providers {
			if !bad[prov] {
				continue
			}
			if results[prov].RecordBlocked {
				return true, prov
			}
			pr := p.Releases[prov]
			if pr != nil && pr.Pkg.Space.ProviderRelation.IsBlocking {
				blockingProvider = prov
			}
		}
	}
	if blockingProvider != "" {
		return true, blockingProvider
	}
	if hasCauseOfItsOwn(rel, bad) {
		return false, ""
	}
	return true, badProvider
}

// hasCauseOfItsOwn reports whether any admitted cause of the release comes
// from somewhere other than the packages that failed or were blocked.
//
// The causes are the plan's own: a fresh direct bump, a channel change, and
// every provider version the release picks up. Fresh, not train-wide: own
// work an earlier prerelease already shipped does not explain releasing again,
// and without the failed provider's propagation such a package would not be in
// the plan at all; releasing it would record a provider movement that never
// published.
//
// Updates is the right cause set, and it is wider than "a provider that
// published in this run" in the case that matters: a provider this run does
// not release at all is in Updates when an earlier run published it and this
// one is the catch-up (§13.7a), and picking that version up is as real a
// reason as a publication happening beside it. It is also narrower than the
// declared provider list, deliberately. A provider reached only through a
// package this run does not release hands the consumer no version (§9.4 syncs
// manifests, and the consumer's manifest does not name it), so its publication
// orders the two and never excuses a provider of the consumer's own that
// failed; Updates holds direct providers only, which is exactly that rule.
func hasCauseOfItsOwn(rel *plan.Release, bad map[string]bool) bool {
	if rel.IsFreshOwnBump() || rel.Pinned ||
		(rel.IsChannelChanged() && (rel.ChannelFrom == "" || !bad[rel.ChannelFrom])) {
		return true
	}
	for _, u := range rel.Updates {
		if !bad[u.Name] {
			return true
		}
	}
	return false
}
