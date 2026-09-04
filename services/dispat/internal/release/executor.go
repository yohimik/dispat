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
	// FailedStage names the stage that failed ("version", "build" or
	// "publish"); empty unless Status is StatusFailed. Informational (shown
	// in the summary).
	FailedStage string
	Err         error
	Duration    time.Duration
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
}

// Tagger creates release tags; *gitx.CLI satisfies it. A nil Tagger on the
// Executor defers tagging to a later phase (release-commit mode, where tags
// must point at the end-of-run commit). target is the commit the tag points
// at; empty means HEAD.
type Tagger interface {
	CreateTag(ctx context.Context, name, message, target string) error
}

// ReleaseRecorder records a successful release somewhere: a changelog file
// (*changelog.FileWriter), a GitHub release (*github.Releaser), or any other
// destination for the same release data.
type ReleaseRecorder interface {
	Record(ctx context.Context, rel *plan.Release) error
}

// Reverter rolls back local changes inside a package folder; *gitx.CLI
// satisfies it. Used for spaces with revertOnFail.
type Reverter interface {
	RevertDir(ctx context.Context, dir string) error
}

// Executor runs the version, build and publish stages of every changed
// package.
//
// Scheduling model: each changed package contributes a build and a publish
// task; packages bumped because of provider updates additionally get a
// version task that runs exactly before their build (its job is syncing
// manifests to the new provider versions). Publish always depends on the
// package's own build. A consumer's first task (version when present,
// otherwise build) depends on each changed provider's build — and on the
// provider's publish when the provider's space sets isBuildWaitingPublish. A
// consumer's publish always waits for its providers' publishes regardless of
// the flag, since publishing against a not-yet-published provider version
// would be invalid; a provider whose publish failed therefore skips its
// consumers unless they have a release reason of their own — and skips them
// unconditionally when its space sets isBuildWaitingPublish, because that
// flag declares their builds consume the publish that never happened.
//
// A stage with no configured script still runs — orderings, statuses,
// changelogs and tags are preserved — it just executes no shell command.
// Scripts receive DISPAT_* environment variables (package, space, versions,
// bump, stage, tag, plus the workspace and updated-provider listings at every
// stage as per-package DISPAT_WORKSPACE_* / DISPAT_UPDATED_* variables).
//
// Build and publish stages have independent parallelism budgets: at most
// BuildConcurrency build/version scripts and PublishConcurrency publish
// scripts run at any moment. A package never runs two of its tasks
// concurrently.
type Executor struct {
	BuildConcurrency   int
	PublishConcurrency int
	Runner             script.Runner
	Tagger             Tagger
	Recorders          []ReleaseRecorder // run in order after each successful publish
	Reverter           Reverter          // rolls back package folders for revertOnFail spaces
	// Force rewrites a tag the repository already carries instead of failing
	// on it (commit.force, default true). The pre-existing-tag rules still
	// come first: a tag already at the release commit is a skip, and one at a
	// different commit is left alone whatever this says.
	Force bool
	// Scanner reads manifests for the autoVersion spaces' native rewriting;
	// nil defaults to the filesystem scanner.
	Scanner scanner.Scanner
	// Observer receives release-progress events (stage transitions, package
	// outcomes); nil disables observation. It only observes: nothing it does
	// with an event can affect the run. See Observer.
	Observer Observer
	Log      zerolog.Logger
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
)

// stageNames maps each task kind onto its lowercase stage name and the
// TitleCase fragment hook names embed ("beforeBuild", "postVersion") — one
// table, so the two spellings cannot drift apart.
var stageNames = [...]struct{ name, title string }{
	taskVersion:  {"version", "Version"},
	taskBuild:    {"build", "Build"},
	taskPublish:  {"publish", "Publish"},
	taskSyncLock: {"syncLock", "SyncLock"},
}

func (k taskKind) String() string { return stageNames[k].name }

// stageTitle renders the stage name as it appears inside a hook name:
// "beforeBuild", "postVersion".
func stageTitle(k taskKind) string { return stageNames[k].title }

type task struct {
	pkg  string
	kind taskKind
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
	Runner   script.Runner
	Dir      string   // working directory (the package folder, or the repo root)
	Stage    string   // what DISPAT_STAGE carries; also the log label
	Commands []string // the commands, run in order
	Env      []string // the full environment the commands receive
	Log      zerolog.Logger
	FailFast bool
}

// Run executes the sequence's commands in order inside its directory.
func (s Sequence) Run(ctx context.Context) error {
	for index, command := range s.Commands {
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
	}
	return nil
}

// Run executes the plan and returns one Result per changed package. Unchanged
// packages are absent from the map. Failures never abort the run: dependent
// packages are skipped (unless they have a release reason of their own) and
// independent packages continue.
func (e *Executor) Run(ctx context.Context, p *plan.Plan) map[string]*Result {
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
		if rel.Releasing() {
			changed[name] = true
			results[name] = &Result{
				Name:    name,
				From:    rel.Previous(),
				To:      rel.Next,
				Channel: rel.Channel,
			}
		}
	}
	if len(results) == 0 {
		return results
	}

	// The workspace listing depends only on the plan, never on how the run
	// goes, so its variables are built once and shared by every task's
	// environment.
	wsVars := WorkspaceEnv(p, e.Log)

	// One login gate per space that configures a login script.
	logins := make(map[string]*spaceLogin)
	for _, rel := range p.Releases {
		if len(rel.Pkg.Space.LoginScript) > 0 {
			if _, ok := logins[rel.Pkg.Space.Name]; !ok {
				logins[rel.Pkg.Space.Name] = &spaceLogin{}
			}
		}
	}

	// Build the task graph. The scheduler owns the dependency bookkeeping —
	// registration, in-degrees, the became-ready cascade — and this loop only
	// states the edges; AddEdge registers its nodes as a side effect.
	// Sorted iteration: the scheduler hands nodes out in insertion order, so
	// building the edges in name order is what makes launch order — not just
	// completion semantics — deterministic run to run (§17.2).
	sched := graph.NewScheduler[task]()
	for _, name := range slices.Sorted(maps.Keys(changed)) {
		b, pub := task{name, taskBuild}, task{name, taskPublish}
		sched.AddEdge(b, pub)
		// Packages bumped because of provider updates run a version task
		// right before their build, and so does every releasing package of an
		// autoVersion space — §9.4 reconciles against every workspace
		// dependency, including providers released by an earlier run, so the
		// stage cannot be conditional on this run's updates. Provider
		// dependencies attach to it. A space with syncLock scripts inserts a
		// syncLock task between the version and the build.
		first := b
		if hasVersionTask(p.Releases[name]) {
			ver := task{name, taskVersion}
			pre := b
			if av := p.Releases[name].Pkg.Space.AutoVersion; av != nil && len(av.SyncLock) > 0 {
				syncTask := task{name, taskSyncLock}
				sched.AddEdge(syncTask, b)
				pre = syncTask
			}
			sched.AddEdge(ver, pre)
			first = ver
		}
		for _, prov := range p.Providers[name] {
			if !changed[prov] {
				continue
			}
			sched.AddEdge(task{prov, taskBuild}, first)
			if p.Releases[prov].Pkg.Space.BuildWaitsPublish {
				sched.AddEdge(task{prov, taskPublish}, first)
			}
			sched.AddEdge(task{prov, taskPublish}, pub)
		}
	}

	r := &run{Executor: e, plan: p, wsVars: wsVars, logins: logins,
		results: results, started: make(map[string]time.Time), scan: e.Scanner,
		avChanged: make(map[string]bool)}

	// The native rewriting inputs — the manifest-name and folder indexes of
	// the whole workspace — are built once, and only when a releasing package
	// actually auto-versions.
	for name := range changed {
		if p.Releases[name].Pkg.Space.AutoVersion != nil {
			if r.scan == nil {
				r.scan = scanner.New()
			}
			r.avNames, r.avDirs = WorkspaceNames(ctx, r.scan, p, e.Log)
			break
		}
	}

	// Version tasks share the build budget: they are short local manifest
	// updates leading straight into the build. syncLock has its own budget —
	// almost always 1 — because its whole reason to exist is serialising lock
	// file regeneration. Draining per class keeps the budgets independent, so
	// a stalled stage never blocks another's.
	budgets := map[taskKind]int{
		taskBuild:    e.BuildConcurrency,
		taskPublish:  e.PublishConcurrency,
		taskSyncLock: syncLockBudget(p, changed),
	}
	err := graph.Drain(ctx, sched,
		func(t task) taskKind {
			switch t.kind {
			case taskPublish, taskSyncLock:
				return t.kind
			default:
				return taskBuild
			}
		},
		func(k taskKind) int { return budgets[k] },
		// A package's configured weight is how many stage slots its tasks
		// occupy. syncLock keeps the ordinary cost: its budget exists to
		// serialise lock-file writers, not to price packages.
		func(t task) int {
			pkg := p.Releases[t.pkg].Pkg
			switch t.kind {
			case taskPublish:
				return pkg.PublishWeight
			case taskSyncLock:
				return 1
			default:
				return pkg.BuildWeight
			}
		},
		func(t task) { r.execute(ctx, t) })
	if err != nil {
		// Interrupted (or, impossibly after E200, cyclic): tasks that never
		// launched left their packages pending. Cancelled, not failed — nothing
		// about them went wrong, and the next run picks them up unchanged.
		// The names are collected under the lock and announced after it: the
		// observer is called outside the mutex everywhere.
		var cancelled []string
		r.mu.Lock()
		for name, res := range results {
			if res.Status == StatusPending {
				res.Status = StatusCancelled
				cancelled = append(cancelled, name)
			}
		}
		r.mu.Unlock()
		for _, name := range slices.Sorted(slices.Values(cancelled)) {
			ev := packageEvent(name, p.Releases[name], EventPackageCancelled)
			ev.Status = StatusCancelled.String()
			e.notify(ev)
		}
		if ctx.Err() != nil {
			e.Log.Warn().Msg("run interrupted: remaining packages cancelled; completed releases keep their records")
		} else {
			e.Log.Error().Err(err).Msg("task graph stalled")
		}
	}
	return results
}

// run is the shared state of one Executor.Run invocation. Every task
// goroutine works against the same value, so the plan, the workspace
// variables, the login gates and the result map with its mutex live here
// instead of travelling through every helper's parameter list. mu guards
// results, started and avChanged; everything else is read-only once Run has
// built it.
type run struct {
	*Executor
	plan    *plan.Plan
	wsVars  []string
	logins  map[string]*spaceLogin
	results map[string]*Result
	mu      sync.Mutex
	started map[string]time.Time
	// The native auto-versioning inputs, built once in Run when any releasing
	// package's space enables it: the manifest scanner and the workspace's
	// manifest-name and folder indexes (see workspaceNames).
	scan    scanner.Scanner
	avNames map[string]string
	avDirs  map[string]string
	// avChanged (guarded by mu) records which packages' version stages
	// actually modified a manifest — natively or through a flow.version
	// script — so a syncLock task with nothing to regenerate can skip its
	// subprocess instead of serialising an empty `npm install` per package.
	avChanged map[string]bool
}

// hasVersionTask reports whether the package's release runs a version task:
// any provider of it moved, or its space auto-versions (whose reconciliation
// is unconditional per §9.4).
//
// Updates rather than DueTo, so that a hand-written flow.version script and a
// native autoVersion block answer the same question. Propagation depth is 0 by
// default, so gating on DueTo meant a consumer whose provider released beside
// it — without a caret between them — got no version stage at all, while the
// same space under autoVersion reconciled normally.
func hasVersionTask(rel *plan.Release) bool {
	return len(rel.Updates) > 0 || rel.Pkg.Space.AutoVersion != nil
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
	t       task
	rel     *plan.Release
	updates []providerUpdate
	log     zerolog.Logger
}

// env builds the DISPAT_* environment of the task's scripts and hooks; stage
// is what DISPAT_STAGE carries.
func (tc *taskCtx) env(stage string) []string {
	return packageEnv(tc.plan, tc.t.pkg, tc.wsVars, tc.updates, stage)
}

// sequence assembles the task's command sequence in its package folder.
func (tc *taskCtx) sequence(stage string, commands []string, failFast bool) Sequence {
	return Sequence{Runner: tc.Runner, Dir: tc.rel.Pkg.Dir, Stage: stage,
		Commands: commands, Env: tc.env(stage), Log: tc.log, FailFast: failFast}
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
	if len(commands) == 0 {
		return nil
	}
	tc.log.Debug().Str("hook", name).Msg("hook started")
	seq := tc.sequence(name, commands, failFast)
	seq.Env = append(seq.Env, extra...)
	return seq.RunMergingOutputs(ctx, tc.rel)
}

// execute runs a single version, build or publish task to completion.
func (r *run) execute(ctx context.Context, t task) {
	rel := r.plan.Releases[t.pkg]
	res := r.results[t.pkg]
	log := r.Log.With().
		Str("package", t.pkg).
		Str("stage", t.kind.String()).
		Str("version", rel.Next.String()).
		Logger()
	tc := &taskCtx{run: r, t: t, rel: rel, log: log}

	r.mu.Lock()
	if res.Status != StatusPending { // failed or skipped at an earlier stage
		r.mu.Unlock()
		return
	}
	if ctx.Err() != nil {
		// Interrupted between scheduling and start: no scripts, no hooks.
		res.Status = StatusCancelled
		r.mu.Unlock()
		ev := packageEvent(t.pkg, rel, EventPackageCancelled)
		ev.Status = StatusCancelled.String()
		r.notify(ev)
		return
	}
	if skip, blocker := shouldSkip(t.pkg, r.plan, r.results); skip {
		res.Status = StatusSkipped
		res.Blocked, res.BlockedBy = true, blocker
		reason := "provider " + blocker + " failed or was skipped, and the package has no changes of its own"
		if pr := r.plan.Releases[blocker]; pr != nil && pr.Pkg.Space.BuildWaitsPublish {
			reason = "provider " + blocker + " failed or was skipped, and this package's build takes its publish as input"
		}
		_, ran := r.started[t.pkg] // earlier stages already modified the folder?
		tc.updates = liveProviderUpdates(t.pkg, r.plan, r.results)
		r.mu.Unlock()
		// Planned, but not attempted because a dependency failed to publish.
		// Non-suppressible (§16) — a package that was in the plan and produced
		// nothing must be accounted for.
		log.Warn().Str("code", plan.CodeBlocked).Str("reason", reason).Msg("skipped")
		ev := packageEvent(t.pkg, rel, EventPackageSkipped)
		ev.Status, ev.Code, ev.BlockedBy = StatusSkipped.String(), plan.CodeBlocked, blocker
		r.notify(ev)
		if ran && rel.Pkg.Space.RevertOnFail {
			r.revert(ctx, rel, log)
		}
		// onSkip observes a skip that has already settled, so it only warns;
		// DISPAT_BLOCKED_BY names the provider responsible.
		_ = tc.hook(ctx, "onSkip", rel.Pkg.Space.OnSkipScript, false,
			"DISPAT_BLOCKED_BY="+blocker)
		return
	}
	if _, ok := r.started[t.pkg]; !ok {
		r.started[t.pkg] = time.Now()
	}
	// Resolve which provider updates are still live at this moment: providers
	// that failed or were skipped never got their new version out, so
	// manifests must not be synced to them and scripts must not act on them.
	// Every stage gets the answer — a publish script choosing a dist-tag wants
	// it as much as the version script that synced manifests — and it is
	// per-task on purpose: a provider can fail between this package's build
	// and its publish, and each stage must see the truth of its own moment.
	tc.updates = liveProviderUpdates(t.pkg, r.plan, r.results)
	r.mu.Unlock()

	fail := func(err error, msg string) {
		// A task dying while the context is cancelled died *of* the
		// cancellation (its script was killed mid-run): that is an
		// interruption, not a package failure, and it must not spawn more
		// scripts — no onFail, no announce. The revert still happens, detached
		// from the cancellation, because a half-modified folder is exactly what
		// revertOnFail promises to clean up.
		interrupted := ctx.Err() != nil
		r.mu.Lock()
		if interrupted {
			res.Status = StatusCancelled
			res.Err = err
		} else {
			res.Status = StatusFailed
			res.FailedStage = t.kind.String()
			res.Err = fmt.Errorf("%s: %w", t.kind, err)
		}
		res.Duration = time.Since(r.started[t.pkg])
		r.mu.Unlock()
		if interrupted {
			ev := packageEvent(t.pkg, rel, EventPackageCancelled)
			ev.Status, ev.Error = StatusCancelled.String(), err.Error()
			r.notify(ev)
			log.Warn().Err(err).Msg(t.kind.String() + " interrupted")
			if rel.Pkg.Space.RevertOnFail {
				r.revert(context.WithoutCancel(ctx), rel, log)
			}
			return
		}
		ev := packageEvent(t.pkg, rel, EventPackageFailed)
		ev.Status, ev.FailedStage, ev.Error = StatusFailed.String(), t.kind.String(), err.Error()
		r.notify(ev)
		log.Error().Err(err).Msg(msg)
		if rel.Pkg.Space.RevertOnFail {
			r.revert(ctx, rel, log)
		}
		// onFail observes a failure that has already settled — it runs after
		// the status and the revert, in the folder's final state, and only
		// warns. It fires once: later tasks of a failed package return before
		// reaching any script.
		_ = tc.hook(ctx, "onFail", rel.Pkg.Space.OnFailScript, false,
			"DISPAT_FAILED_STAGE="+t.kind.String(), "DISPAT_ERROR="+err.Error())
	}

	// beforeAll runs at the package's first task: version when the package
	// has one, build otherwise.
	first := t.kind == taskVersion || (t.kind == taskBuild && !hasVersionTask(rel))
	if first {
		if err := tc.hook(ctx, "beforeAll", rel.Pkg.Space.BeforeAllScript, true); err != nil {
			fail(err, "beforeAll hook failed")
			return
		}
	}

	space := rel.Pkg.Space
	var frame stage
	switch t.kind {
	case taskVersion:
		frame = stage{commands: space.VersionScript, before: space.BeforeVersionScript, after: space.PostVersionScript}
		if space.AutoVersion != nil {
			frame.native = func(ctx context.Context) error { return tc.autoVersion(ctx, space.AutoVersion) }
		}
	case taskBuild:
		frame = stage{commands: space.BuildScript, before: space.BeforeBuildScript, after: space.PostBuildScript}
	case taskSyncLock:
		// The lock-sync sequence: no hooks of its own, budgeted separately —
		// and skipped outright when this package's version stage changed no
		// file, so a quiet release does not serialise one lock regeneration
		// per package for nothing.
		//
		// A space that configured neither reconciling strategy is the
		// exception: it never produces that signal, so gating on one would
		// mean its scripts never ran at all.
		r.mu.Lock()
		filesChanged := r.avChanged[t.pkg]
		r.mu.Unlock()
		if filesChanged || !space.AutoVersion.Reconciles() {
			frame = stage{commands: space.AutoVersion.SyncLock}
		} else {
			log.Debug().Msg("syncLock: nothing was reconciled, nothing to regenerate")
		}
	default:
		// postPublish is not part of this frame: it only runs after the
		// package is fully published, further down.
		frame = stage{commands: space.PublishScript, before: space.BeforePublishScript}
	}
	if t.kind == taskVersion && len(tc.updates) == 0 && len(rel.Updates) > 0 {
		// Every provider this package picks up a version from failed or was
		// skipped (the package itself proceeds on its own changes): there is
		// nothing to sync manifests to, so the version scripts — hooks
		// included — must not run. Native reconciliation is different: it
		// compares against baselines too (§9.4) and never writes a dead
		// provider's planned version, so it proceeds.
		log.Info().Msg("version: no successfully updated providers, skipping scripts")
		frame.commands, frame.before, frame.after = nil, nil, nil
	}

	// The event reports the task starting, not a script: a stage with no
	// configured command still runs and still transitions, so it is still
	// observed.
	stageEv := packageEvent(t.pkg, rel, EventStageStarted)
	stageEv.Stage = t.kind.String()
	r.notify(stageEv)

	if t.kind == taskPublish {
		if err := tc.loginGate(ctx); err != nil {
			fail(err, "login failed")
			return
		}
	}

	if what, err := tc.stageFrame(ctx, frame); err != nil {
		fail(err, what)
		return
	}
	if t.kind == taskVersion && len(frame.commands) > 0 {
		// A flow.version script may have edited manifests too; the syncLock
		// skip must stay conservative about what it cannot see.
		tc.markManifestsChanged()
	}
	stageEv.Name = EventStageSucceeded
	r.notify(stageEv)
	if t.kind != taskPublish {
		log.Info().Msg(t.kind.String() + " succeeded")
		return
	}
	tc.publishTail(ctx, res)
}

// loginGate runs the space's once-per-space login before its first publish;
// every other publish of the space waits inside the gate. A login failure
// fails every publish of the space — none of them could have succeeded
// without it. On success the login's exports are merged onto the release
// (safe: only this package's current task touches rel.Outputs).
func (tc *taskCtx) loginGate(ctx context.Context) error {
	space := tc.rel.Pkg.Space
	sl := tc.logins[space.Name]
	if sl == nil {
		return nil
	}
	sl.once.Do(func() {
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
// commands, and the optional native step dispat runs itself (the version
// stage's auto-versioning). One value instead of four same-shaped positional
// parameters, so a call site cannot quietly transpose two of them.
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
	kind := tc.t.kind
	if err := tc.hook(ctx, "before"+stageTitle(kind), s.before, true); err != nil {
		return "before" + stageTitle(kind) + " hook failed", err
	}
	if s.native != nil {
		if err := s.native(ctx); err != nil {
			return "auto-versioning failed", err
		}
	}
	if len(s.commands) == 0 {
		// No script configured: the stage completes without running anything.
		tc.log.Debug().Msg(kind.String() + ": no script configured, nothing to execute")
	} else {
		tc.log.Info().Msg(kind.String() + " started")
		if err := tc.sequence(kind.String(), s.commands, true).RunMergingOutputs(ctx, tc.rel); err != nil {
			return kind.String() + " script failed", err
		}
	}
	if err := tc.hook(ctx, "post"+stageTitle(kind), s.after, true); err != nil {
		return "post" + stageTitle(kind) + " hook failed", err
	}
	return "", nil
}

// publishTail finishes a successful publish: the release recorders (changelog
// file, GitHub release, ...), the tag, the status flip, then the warn-only
// postPublish hook and the announce frame.
//
// It takes no failure callback, and that is the point: past the publish there
// is no failure left that may fail the package. Everything here records a
// release that is already out, so each error becomes a critical and the tail
// keeps going.
func (tc *taskCtx) publishTail(ctx context.Context, res *Result) {
	rel, space := tc.rel, tc.rel.Pkg.Space
	// The publish succeeded, so from here to the status flip this leg of the
	// transaction is committing: it must durably record its completion (§17).
	// Recording and tagging therefore run detached from cancellation — a
	// Ctrl-C that killed the publish would have been an interruption, but one
	// that loses the tag *after* the publish re-releases a released version on
	// the next run, which is the one thing the model forbids.
	recCtx := context.WithoutCancel(ctx)
	// Neither the recorders nor the tag may fail the package now. The artefact
	// is on its registry: reporting the package as failed would revert its
	// folder, run its onFail script and skip every consumer, none of which
	// un-publishes anything. Each failure is recorded as a critical instead,
	// the rest of the tail still runs, and the run exits non-zero at the end.
	for _, rec := range tc.Recorders {
		if err := rec.Record(recCtx, rel); err != nil {
			// The next recorder still runs: a changelog that could not be
			// written is no reason to skip the GitHub release as well.
			tc.critical(res, plan.CodeRecordFailed, err, "release recording failed")
		}
	}
	if tc.Tagger != nil { // nil: tagging deferred to the release-commit phase
		if err := CreateReleaseTag(recCtx, tc.Tagger, rel, tc.Force, tc.log); err != nil {
			tc.critical(res, TagFailureCode(err), err, "tagging failed")
		}
	}
	tc.mu.Lock()
	res.Status = StatusPublished
	res.Duration = time.Since(tc.started[tc.t.pkg])
	tc.mu.Unlock()
	ev := packageEvent(tc.t.pkg, rel, EventPackagePublished)
	ev.Status, ev.Tag = StatusPublished.String(), rel.TagName()
	tc.notify(ev)
	// In release-commit mode the tag does not exist yet — finalize creates it
	// — so the line names it as planned rather than stating it as a fact.
	if tc.Tagger != nil {
		tc.log.Info().Str("tag", rel.TagName()).Msg("published")
	} else {
		tc.log.Info().Str("plannedTag", rel.TagName()).Msg("published, tag deferred to the release commit")
	}

	if ctx.Err() != nil {
		// Interrupted: the release is out and recorded; observers stay silent.
		return
	}

	// postPublish observes a release that is already out, which is why it
	// runs after the status settles and only warns: failing the package now
	// would report an unpublished release for a published one.
	_ = tc.hook(ctx, "postPublish", space.PostPublishScript, false)

	// The announce frame: a fourth stage after the publish, for pushing the
	// release out to update channels (a Slack message, a webhook, a docs
	// feed). It has the gating stages' hook structure but none of their
	// authority — the release is out, so the stage and both hooks only warn,
	// and no failure among them stops the others from running.
	_ = tc.hook(ctx, "beforeAnnounce", space.BeforeAnnounceScript, false)
	if len(space.AnnounceScript) > 0 {
		// Unlike the graph stages, announce is observed only when a script is
		// configured: it is a tail of the publish rather than a task of its
		// own, and an empty announce transitions nothing worth reporting.
		annEv := packageEvent(tc.t.pkg, rel, EventStageStarted)
		annEv.Stage = "announce"
		tc.notify(annEv)
		tc.log.Info().Msg("announce started")
		_ = tc.sequence("announce", space.AnnounceScript, false).RunMergingOutputs(ctx, rel)
		annEv.Name = EventStageSucceeded
		tc.notify(annEv)
	}
	_ = tc.hook(ctx, "postAnnounce", space.PostAnnounceScript, false)
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
	if errors.Is(err, ErrTagAtOtherCommit) {
		return plan.CodeTagAtOtherCommit
	}
	return plan.CodeTagFailed
}

// tagInspector is the optional Tagger extension the same-commit tag skip
// needs; *gitx.CLI implements it. A Tagger without it keeps the strict
// pre-existing-tag-is-an-error behaviour, which is the right default for test
// doubles and custom taggers.
type tagInspector interface {
	Tags(ctx context.Context, pkg string, format gitx.TagFormat) (gitx.Tags, error)
	ResolveCommit(ctx context.Context, rev string) (string, error)
}

// forceTagger is the optional Tagger extension that can rewrite a tag the
// repository already carries; *gitx.CLI implements it. A Tagger without it
// simply never forces, which is the right default for a test double or a
// custom tagger written before the option existed.
type forceTagger interface {
	CreateTagForce(ctx context.Context, name, message, target string) error
}

// writeTag creates one tag, forcing when asked and the tagger can.
func writeTag(ctx context.Context, tagger Tagger, force bool, name, message, target string) error {
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
func CreateReleaseTag(ctx context.Context, tagger Tagger, rel *plan.Release, force bool, log zerolog.Logger) error {
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
func CreateReleaseTagAs(ctx context.Context, tagger Tagger, rel *plan.Release, name string, force bool, log zerolog.Logger) error {
	tag := rel.TagName()
	if name != "" {
		tag = name
	}
	if insp, ok := tagger.(tagInspector); ok {
		if tags, err := insp.Tags(ctx, rel.Pkg.Name, rel.TagFormat()); err == nil {
			for _, t := range tags {
				if t.Name != tag {
					continue
				}
				target := rel.ExportedCommit()
				if target == "" {
					target = "HEAD"
				}
				sha, err := insp.ResolveCommit(ctx, target)
				if err != nil {
					return fmt.Errorf("tag %s already exists and the release target %q cannot be resolved: %w", tag, target, err)
				}
				if sha == t.Commit {
					log.Warn().Str("code", plan.CodeTagExists).Str("tag", tag).
						Msg("tag already exists at the release commit, skipped")
					return nil
				}
				return fmt.Errorf("%w: %s is at %s, not at the release commit %s",
					ErrTagAtOtherCommit, tag, t.Commit, sha)
			}
		}
	}
	if err := writeTag(ctx, tagger, force, tag, "release "+tag, rel.ExportedCommit()); err != nil {
		return err
	}
	return createAliasTags(ctx, tagger, rel, log)
}

// createAliasTags writes the extra names a release is published under, after
// its release tag succeeded and at the same commit.
//
// It lives here rather than at each of the three call sites because this is
// the one function every tagging path goes through; adding it anywhere else
// would mean one of them silently not writing aliases.
//
// A failure is warned about and the remaining aliases are still attempted. An
// alias is a convenience ref, not the record of the release: the release tag
// is already written by the time this runs, and losing a "v1" is a thing to
// re-point, not a reason to report a published release as broken.
func createAliasTags(ctx context.Context, tagger Tagger, rel *plan.Release, log zerolog.Logger) error {
	aliases := rel.AliasTags()
	if len(aliases) == 0 {
		return nil
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
	return nil
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

// revert rolls back all local changes inside the package folder. Used for
// revertOnFail spaces when a package fails at any stage — or is skipped after
// an earlier stage already ran (e.g. version succeeded, then a provider's
// publish failure skipped the package before its build).
func (e *Executor) revert(ctx context.Context, rel *plan.Release, log zerolog.Logger) {
	if e.Reverter == nil {
		return
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
// A "reason of its own" is a *fresh* direct bump or a channel change: a
// package moving between channels is being released for something a failed
// provider cannot invalidate, so it proceeds. Fresh, not train-wide — own
// work an earlier prerelease already shipped does not explain releasing
// again, and without the failed provider's propagation such a package would
// not be in the plan at all; releasing it would record a provider movement
// that never published. Providers whose outcome is still pending count as
// neither; the check runs again before publish, when all provider publishes
// are final thanks to the task-graph edges.
//
// A provider whose space sets isBuildWaitingPublish outranks every reason of
// the package's own. The flag declares that consumers' builds take the
// provider's *published* release as their input — the dispat images install
// the binary the CLI leg's publish attached — so when that publish never
// happened the input does not exist, and no amount of own work substitutes
// for it. Proceeding would either fail on the missing artifact or, worse,
// quietly build against the provider's previous release and publish it under
// a version that promises the new one.
func shouldSkip(pkg string, p *plan.Plan, results map[string]*Result) (bool, string) {
	rel := p.Releases[pkg]
	badProvider := ""
	waitedProvider := ""
	anyPublished := false
	for _, prov := range p.Providers[pkg] {
		r, ok := results[prov]
		if !ok { // unchanged provider
			continue
		}
		switch r.Status {
		case StatusFailed, StatusSkipped:
			badProvider = prov
			if pr := p.Releases[prov]; pr != nil && pr.Pkg.Space.BuildWaitsPublish {
				waitedProvider = prov
			}
		case StatusPublished:
			anyPublished = true
		}
	}
	if badProvider == "" {
		return false, ""
	}
	if waitedProvider != "" {
		return true, waitedProvider
	}
	if rel.FreshOwnBump() || rel.ChannelChanged() || anyPublished {
		return false, ""
	}
	return true, badProvider
}
