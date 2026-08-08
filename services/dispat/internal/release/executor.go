// Package release executes a computed plan: it builds and publishes every
// changed package with bounded parallelism while honouring the dependency
// graph and each space's isBuildWaitingPublish setting.
package release

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/pkg/ccme"

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
// consumers unless they have a release reason of their own.
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
	Log                zerolog.Logger
}

type taskKind uint8

const (
	taskVersion taskKind = iota
	taskBuild
	taskPublish
)

// stageNames maps each task kind onto its lowercase stage name and the
// TitleCase fragment hook names embed ("beforeBuild", "postVersion") — one
// table, so the two spellings cannot drift apart.
var stageNames = [...]struct{ name, title string }{
	taskVersion: {"version", "Version"},
	taskBuild:   {"build", "Build"},
	taskPublish: {"publish", "Publish"},
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
	for _, command := range s.Commands {
		stdout := newLineWriter(s.Log, zerolog.InfoLevel)
		stderr := newLineWriter(s.Log, zerolog.WarnLevel)
		err := s.Runner.Run(ctx, s.Dir, command, s.Env, stdout, stderr)
		stdout.Flush()
		stderr.Flush()
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
		// tagged. A channel-only release, conversely, has no bump at all and
		// must still be executed: it is a release like any other (§13.9).
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
	sched := graph.NewScheduler[task]()
	for name := range changed {
		b, pub := task{name, taskBuild}, task{name, taskPublish}
		sched.AddEdge(b, pub)
		// Packages bumped because of provider updates run a version task
		// right before their build; provider dependencies attach to it.
		first := b
		if len(p.Releases[name].DueTo) > 0 {
			ver := task{name, taskVersion}
			sched.AddEdge(ver, b)
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
		results: results, started: make(map[string]time.Time)}

	// Version tasks share the build budget: they are short local manifest
	// updates leading straight into the build. Draining per class keeps the
	// two budgets independent, so a stalled stage never blocks the other's.
	budgets := map[taskKind]int{taskBuild: e.BuildConcurrency, taskPublish: e.PublishConcurrency}
	err := graph.Drain(ctx, sched,
		func(t task) taskKind {
			if t.kind == taskPublish {
				return taskPublish
			}
			return taskBuild
		},
		func(k taskKind) int { return budgets[k] },
		func(t task) { r.execute(ctx, t) })
	if err != nil {
		// Interrupted (or, impossibly after E200, cyclic): tasks that never
		// launched left their packages pending. Cancelled, not failed — nothing
		// about them went wrong, and the next run picks them up unchanged.
		r.mu.Lock()
		for _, res := range results {
			if res.Status == StatusPending {
				res.Status = StatusCancelled
			}
		}
		r.mu.Unlock()
		if ctx.Err() != nil {
			e.Log.Warn().Msg("run interrupted: remaining packages cancelled; completed releases keep their records")
		} else {
			e.Log.Error().Err(err).Msg("task graph stalled")
		}
	}
	return results
}

// run is the shared state of one Executor.Run invocation. Every task
// goroutine works against the same value, so what used to travel through
// every helper's parameter list — the plan, the workspace variables, the
// login gates, the result map with its mutex — lives here instead. mu guards
// results and started; everything else is read-only once Run has built it.
type run struct {
	*Executor
	plan    *plan.Plan
	wsVars  []string
	logins  map[string]*spaceLogin
	results map[string]*Result
	mu      sync.Mutex
	started map[string]time.Time
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
		return
	}
	if skip, blocker := shouldSkip(t.pkg, r.plan, r.results); skip {
		res.Status = StatusSkipped
		res.Blocked, res.BlockedBy = true, blocker
		reason := "provider " + blocker + " failed or was skipped, and the package has no changes of its own"
		_, ran := r.started[t.pkg] // earlier stages already modified the folder?
		tc.updates = liveProviderUpdates(t.pkg, r.plan, r.results)
		r.mu.Unlock()
		// Planned, but not attempted because a dependency failed to publish.
		// Non-suppressible (§16) — a package that was in the plan and produced
		// nothing must be accounted for.
		log.Warn().Str("code", plan.CodeBlocked).Str("reason", reason).Msg("skipped")
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
			log.Warn().Err(err).Msg(t.kind.String() + " interrupted")
			if rel.Pkg.Space.RevertOnFail {
				r.revert(context.WithoutCancel(ctx), rel, log)
			}
			return
		}
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

	// beforeAll runs at the package's first task: version when the package has
	// one (it exists exactly when DueTo is non-empty), build otherwise.
	first := t.kind == taskVersion || (t.kind == taskBuild && len(rel.DueTo) == 0)
	if first {
		if err := tc.hook(ctx, "beforeAll", rel.Pkg.Space.BeforeAllScript, true); err != nil {
			fail(err, "beforeAll hook failed")
			return
		}
	}

	space := rel.Pkg.Space
	var commands, before, after []string
	switch t.kind {
	case taskVersion:
		commands, before, after = space.VersionScript, space.BeforeVersionScript, space.PostVersionScript
	case taskBuild:
		commands, before, after = space.BuildScript, space.BeforeBuildScript, space.PostBuildScript
	default:
		// postPublish is not part of this frame: it only runs after the
		// package is fully published, further down.
		commands, before, after = space.PublishScript, space.BeforePublishScript, nil
	}
	if t.kind == taskVersion && len(tc.updates) == 0 {
		// Every provider this package was bumped for failed or was skipped
		// (the package itself proceeds on its own changes): there is nothing
		// to sync manifests to, so the version scripts — hooks included —
		// must not run.
		log.Info().Msg("version: no successfully updated providers, skipping scripts")
		commands, before, after = nil, nil, nil
	}

	if t.kind == taskPublish {
		if err := tc.loginGate(ctx); err != nil {
			fail(err, "login failed")
			return
		}
	}

	if what, err := tc.stageFrame(ctx, before, commands, after); err != nil {
		fail(err, what)
		return
	}
	if t.kind != taskPublish {
		log.Info().Msg(t.kind.String() + " succeeded")
		return
	}
	tc.publishTail(ctx, res, fail)
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
		// It runs in the *space* folder — the parent of every member package —
		// not in whichever package's publish happened to win the race to the
		// gate: a login script reading a local file must see the same folder
		// on every run.
		seq := Sequence{Runner: tc.Runner, Dir: filepath.Dir(tc.rel.Pkg.Dir), Stage: "login",
			Commands: space.LoginScript, Env: loginEnv(space.Name, tc.wsVars),
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

// stageFrame runs the task's gating frame: before hook, stage scripts, after
// hook — each sequence fail-fast, every failure failing the release, because
// all three exist to decide whether the release happens. what labels the
// failing piece for the log ("beforeBuild hook failed", "build script
// failed").
func (tc *taskCtx) stageFrame(ctx context.Context, before, commands, after []string) (what string, err error) {
	kind := tc.t.kind
	if err := tc.hook(ctx, "before"+stageTitle(kind), before, true); err != nil {
		return "before" + stageTitle(kind) + " hook failed", err
	}
	if len(commands) == 0 {
		// No script configured: the stage completes without running anything.
		tc.log.Debug().Msg(kind.String() + ": no script configured, nothing to execute")
	} else {
		tc.log.Info().Msg(kind.String() + " started")
		if err := tc.sequence(kind.String(), commands, true).RunMergingOutputs(ctx, tc.rel); err != nil {
			return kind.String() + " script failed", err
		}
	}
	if err := tc.hook(ctx, "post"+stageTitle(kind), after, true); err != nil {
		return "post" + stageTitle(kind) + " hook failed", err
	}
	return "", nil
}

// publishTail finishes a successful publish: the release recorders (changelog
// file, GitHub release, ...), the tag, the status flip, then the warn-only
// postPublish hook and the announce frame.
func (tc *taskCtx) publishTail(ctx context.Context, res *Result, fail func(error, string)) {
	rel, space := tc.rel, tc.rel.Pkg.Space
	// The publish succeeded, so from here to the status flip this leg of the
	// transaction is committing: it must durably record its completion (§17).
	// Recording and tagging therefore run detached from cancellation — a
	// Ctrl-C that killed the publish would have been an interruption, but one
	// that loses the tag *after* the publish re-releases a released version on
	// the next run, which is the one thing the model forbids.
	recCtx := context.WithoutCancel(ctx)
	for _, rec := range tc.Recorders {
		if err := rec.Record(recCtx, rel); err != nil {
			fail(err, "release recording failed")
			return
		}
	}
	if tc.Tagger != nil { // nil: tagging deferred to the release-commit phase
		if err := CreateReleaseTag(recCtx, tc.Tagger, rel); err != nil {
			fail(err, "tagging failed")
			return
		}
	}
	tc.mu.Lock()
	res.Status = StatusPublished
	res.Duration = time.Since(tc.started[tc.t.pkg])
	tc.mu.Unlock()
	tc.log.Info().Str("tag", rel.TagName()).Msg("published")

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
		tc.log.Info().Msg("announce started")
		_ = tc.sequence("announce", space.AnnounceScript, false).RunMergingOutputs(ctx, rel)
	}
	_ = tc.hook(ctx, "postAnnounce", space.PostAnnounceScript, false)
}

// CreateReleaseTag creates rel's annotated release tag — the one place the
// tag message is rendered and the PACKAGE_<KEY> export is honoured, shared by
// the in-run tagging above and the finalize phase's deferred tagging: an
// exported commit pins the tag there instead of HEAD (or the release commit).
func CreateReleaseTag(ctx context.Context, tagger Tagger, rel *plan.Release) error {
	tag := rel.TagName()
	return tagger.CreateTag(ctx, tag, "release "+tag, rel.ExportedCommit())
}

// loginEnv is the space-scoped environment of a login script. Login is a
// space affair — which package's publish happens to trigger it is a scheduling
// accident — so it deliberately carries no package variables: only the space,
// the stage and the workspace listing.
func loginEnv(space string, wsVars []string) []string {
	env := []string{"DISPAT_SPACE=" + space, "DISPAT_STAGE=login"}
	return append(env, wsVars...)
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

// providerUpdate is one live provider update, flattened into the
// DISPAT_UPDATED_* variables of a consumer's scripts.
type providerUpdate struct {
	Package    string
	Space      string
	OldVersion string
	NewVersion string
	// Channel is the channel the provider is releasing on, so that a version
	// script can tell a prerelease dependency from a stable one — the case
	// §9.4 reports as W203 and cannot make safe by itself.
	Channel string
}

// workspaceVersion is one entry of the workspace listing: a package and the
// version it will carry at the end of the run.
//
// dispat has no manifest model — reconciling declared ranges is the version
// script's job (§9.4) — so this is the input that job needs. The breadth
// matters: §9.4 requires reconciliation against *every* workspace dependency,
// not only those released in the same run, because a dependency may have been
// published by an earlier run whose dependent leg failed (§13.7a). Restricting
// it to this run's releases reopens exactly that hole.
type workspaceVersion struct {
	Package string
	Version string
	Channel string
	// Releasing reports whether the version is this run's plan (true) or the
	// package's existing baseline (false). A false here with a version newer
	// than the consumer's declared range is the W197 case.
	Releasing bool
}

// liveProviderUpdates returns — with mu held — the provider updates the
// package was bumped for, excluding providers that failed or were skipped:
// their new versions were never released, so manifests must not point at them.
// Providers whose publish is still pending (possible for the version/build
// stages when isBuildWaitingPublish is false) are included.
func liveProviderUpdates(pkg string, p *plan.Plan, results map[string]*Result) []providerUpdate {
	rel := p.Releases[pkg]
	updates := make([]providerUpdate, 0, len(rel.DueTo))
	for _, prov := range rel.DueTo {
		if r, ok := results[prov]; ok && (r.Status == StatusFailed || r.Status == StatusSkipped) {
			continue
		}
		pr := p.Releases[prov]
		updates = append(updates, providerUpdate{
			Package:    prov,
			Space:      pr.Pkg.Space.Name,
			OldVersion: pr.Previous().String(),
			NewVersion: pr.Next.String(),
			Channel:    pr.Channel,
		})
	}
	return updates
}

// WorkspaceEnv renders the workspace listing as plain variables, one set per
// package, readable from any shell without a parser:
//
//	DISPAT_WORKSPACE_PACKAGES            space-separated keys, iteration order
//	DISPAT_WORKSPACE_<KEY>_NAME          the raw package name
//	DISPAT_WORKSPACE_<KEY>_VERSION       end-of-run version
//	DISPAT_WORKSPACE_<KEY>_CHANNEL       end-of-run channel
//	DISPAT_WORKSPACE_<KEY>_RELEASING     true / false
//
// Two names may sanitise to one key ("core-utils", "core.utils"); the first
// in plan order keeps it and the loser is omitted from the listing with a
// warning, rather than silently overwriting fields one by one. A workspace
// hitting this renames one of the pair.
func WorkspaceEnv(p *plan.Plan, log zerolog.Logger) []string {
	entries := workspaceVersions(p)
	keys := make([]string, 0, len(entries))
	taken := make(map[string]string, len(entries))
	out := make([]string, 0, len(entries)*4+1)
	for _, e := range entries {
		k := plan.EnvKey(e.Package)
		if prev, dup := taken[k]; dup {
			log.Warn().Str("package", e.Package).Str("key", k).
				Msgf("workspace env: key collides with %q, package omitted from DISPAT_WORKSPACE_* variables", prev)
			continue
		}
		taken[k] = e.Package
		keys = append(keys, k)
		pre := "DISPAT_WORKSPACE_" + k
		out = append(out,
			pre+"_NAME="+e.Package,
			pre+"_VERSION="+e.Version,
			pre+"_CHANNEL="+e.Channel,
			pre+"_RELEASING="+boolEnv(e.Releasing))
	}
	return append(out, "DISPAT_WORKSPACE_PACKAGES="+strings.Join(keys, " "))
}

// updatedEnv renders the live provider updates the same way, under
// DISPAT_UPDATED_*. It is built per task because the update list is: which
// providers are still live differs between a package's build and its publish.
func updatedEnv(updates []providerUpdate) []string {
	keys := make([]string, 0, len(updates))
	taken := make(map[string]bool, len(updates))
	out := make([]string, 0, len(updates)*5+1)
	for _, u := range updates {
		k := plan.EnvKey(u.Package)
		if taken[k] {
			continue // same first-come rule as WorkspaceEnv, already warned there
		}
		taken[k] = true
		keys = append(keys, k)
		pre := "DISPAT_UPDATED_" + k
		out = append(out,
			pre+"_NAME="+u.Package,
			pre+"_SPACE="+u.Space,
			pre+"_OLD_VERSION="+u.OldVersion,
			pre+"_NEW_VERSION="+u.NewVersion,
			pre+"_CHANNEL="+u.Channel)
	}
	// Set even when empty: `for k in $DISPAT_UPDATED_PACKAGES` should iterate
	// zero times, not read an unset variable.
	return append(out, "DISPAT_UPDATED_PACKAGES="+strings.Join(keys, " "))
}

// RunEnv builds the environment of the run-level hooks (postAll and the
// commit/push hooks): the workspace listing plus the run outcome, rendered the
// same way as the per-task listings so a script reads both with one idiom:
//
//	DISPAT_PUBLISHED_PACKAGES            space-separated keys of published packages
//	DISPAT_FAILED_PACKAGES               keys of failed packages
//	DISPAT_SKIPPED_PACKAGES              keys of skipped (blocked) packages
//	DISPAT_CANCELLED_PACKAGES            keys of packages an interrupted run never ran
//	DISPAT_UNPLANNED_PACKAGES            keys of packages the plan did not release
//	                                     (unchanged, or held by Release-As: none)
//	DISPAT_RESULT_<KEY>_NAME             the raw package name
//	DISPAT_RESULT_<KEY>_STATUS           published / failed / skipped / cancelled
//	DISPAT_RESULT_<KEY>_OLD_VERSION      version before the run
//	DISPAT_RESULT_<KEY>_NEW_VERSION      version the run planned
//	DISPAT_RESULT_<KEY>_CHANNEL          release channel
//	DISPAT_RESULT_<KEY>_FAILED_STAGE     stage that failed (failed packages only)
//	DISPAT_RESULT_<KEY>_BLOCKED_BY       blocking provider (blocked packages only)
//
// Keys collide under the same first-in-plan-order rule as WorkspaceEnv; the
// list variables are set even when empty so a shell for-loop iterates zero
// times instead of reading an unset variable.
func RunEnv(p *plan.Plan, results map[string]*Result, log zerolog.Logger) []string {
	env := WorkspaceEnv(p, log)
	var published, failed, skipped, cancelled, unplanned []string
	taken := make(map[string]bool, len(p.Order))
	for _, name := range p.Order {
		k := plan.EnvKey(name)
		if taken[k] {
			continue // collision already warned about by WorkspaceEnv
		}
		taken[k] = true
		res, ok := results[name]
		if !ok {
			unplanned = append(unplanned, k)
			continue
		}
		switch res.Status {
		case StatusPublished:
			published = append(published, k)
		case StatusFailed:
			failed = append(failed, k)
		case StatusCancelled:
			cancelled = append(cancelled, k)
		default:
			skipped = append(skipped, k)
		}
		pre := "DISPAT_RESULT_" + k
		env = append(env,
			pre+"_NAME="+name,
			pre+"_STATUS="+res.Status.String(),
			pre+"_OLD_VERSION="+res.From.String(),
			pre+"_NEW_VERSION="+res.To.String(),
			pre+"_CHANNEL="+res.Channel)
		if res.FailedStage != "" {
			env = append(env, pre+"_FAILED_STAGE="+res.FailedStage)
		}
		if res.BlockedBy != "" {
			env = append(env, pre+"_BLOCKED_BY="+res.BlockedBy)
		}
	}
	return append(env,
		"DISPAT_PUBLISHED_PACKAGES="+strings.Join(published, " "),
		"DISPAT_FAILED_PACKAGES="+strings.Join(failed, " "),
		"DISPAT_SKIPPED_PACKAGES="+strings.Join(skipped, " "),
		"DISPAT_CANCELLED_PACKAGES="+strings.Join(cancelled, " "),
		"DISPAT_UNPLANNED_PACKAGES="+strings.Join(unplanned, " "))
}

// workspaceVersions lists every workspace package with the version it will
// carry at the end of the run: its planned version where it is releasing, its
// baseline otherwise (§9.4).
func workspaceVersions(p *plan.Plan) []workspaceVersion {
	out := make([]workspaceVersion, 0, len(p.Order))
	for _, name := range p.Order {
		rel := p.Releases[name]
		if rel == nil {
			continue
		}
		entry := workspaceVersion{Package: name, Channel: rel.Channel}
		if rel.Releasing() {
			entry.Version, entry.Releasing = rel.Next.String(), true
		} else if rel.HasBaseline {
			entry.Version = rel.Baseline.String()
		} else {
			entry.Version = rel.Current.String()
		}
		out = append(out, entry)
	}
	return out
}

// CommandEnv builds the full per-package DISPAT_* environment outside a
// release run: the package variables, the workspace listing (wsVars, built
// once per run with WorkspaceEnv and shared across packages) and the
// package's provider updates, all considered live since no run is deciding
// otherwise. It is what `dispat run <script>` hands a space's run scripts, so
// a script is movable between a stage and a run script without changing what
// it reads.
func CommandEnv(p *plan.Plan, pkg, stage string, wsVars []string) []string {
	return packageEnv(p, pkg, wsVars, liveProviderUpdates(pkg, p, nil), stage)
}

// packageEnv builds the DISPAT_* environment of one package's script or hook.
// stage is what DISPAT_STAGE carries: the stage name for a stage script, the
// hook name ("beforeBuild", "postPublish", ...) for a hook — every hook gets
// the same full environment as the stage scripts, distinguished only there.
func packageEnv(p *plan.Plan, pkg string, wsVars []string, updates []providerUpdate, stage string) []string {
	rel := p.Releases[pkg]
	env := []string{
		"DISPAT_PACKAGE=" + pkg,
		"DISPAT_SPACE=" + rel.Pkg.Space.Name,
		"DISPAT_OLD_VERSION=" + rel.Previous().String(),
		"DISPAT_STABLE_BASELINE=" + rel.Current.String(),
		"DISPAT_NEW_VERSION=" + rel.Next.String(),
		"DISPAT_BUMP=" + rel.Bump.String(),
		"DISPAT_TAG=" + rel.TagName(),
		"DISPAT_STAGE=" + stage,
		// Channel state (§11.1). A publish script needs the channel to choose
		// a dist-tag; the old value is there so that a graduation is
		// distinguishable from an ordinary release.
		"DISPAT_CHANNEL=" + rel.Channel,
		"DISPAT_OLD_CHANNEL=" + rel.BaselineChannel,
		"DISPAT_IS_PRERELEASE=" + boolEnv(rel.IsPrerelease()),
		// The same release named under the normative "{name}@{version}"
		// format. DISPAT_TAG follows the space's tagFormat, so a script
		// written against the SemVer spelling keeps a stable input whatever
		// local convention that format encodes.
		"DISPAT_SEMVER_TAG=" + rel.SemverTagName(),
		// The version decomposed, so a script never re-parses a tag:
		//
		//	DISPAT_VERSION      the core alone: 1.0.1
		//	DISPAT_CHANNEL      the channel alone (above)
		//	DISPAT_COUNTER      the counter alone (below; unset when stable)
		//	DISPAT_NEW_VERSION  version+channel+counter, SemVer: 1.0.1-beta.4
		//	DISPAT_TAG_VERSION  version+channel+counter as the space's
		//	                    tagFormat spells it — 1.0.1-beta4 under
		//	                    "{name}@v{version}-{channel}{counter}" — the
		//	                    version section of DISPAT_TAG without the name
		//	                    and its decoration
		"DISPAT_VERSION=" + rel.Next.Core().String(),
		"DISPAT_TAG_VERSION=" + rel.TagFormat().RenderVersion(rel.Next),
	}
	// The baseline — the newest tag of any kind, prereleases included — is
	// the counterpart of DISPAT_STABLE_BASELINE: what the computed version
	// must exceed and where the channel is read from. It is left unset when
	// the package has never released, so ${DISPAT_BASELINE+x} detects a first
	// release — DISPAT_OLD_VERSION cannot, because it falls back to the
	// stable baseline (initials or 0.0.0) there. When set, the two are equal.
	if rel.HasBaseline {
		env = append(env, "DISPAT_BASELINE="+rel.Baseline.String())
	}
	// The counters are left unset — not empty — when the version has none, so
	// a shell's ${DISPAT_COUNTER+x} distinguishes "a stable release" from "a
	// prerelease whose counter is empty text", which "" cannot.
	if c := rel.Counter(); c != "" {
		env = append(env, "DISPAT_COUNTER="+c)
	}
	if c := rel.PreviousCounter(); c != "" {
		env = append(env, "DISPAT_OLD_COUNTER="+c)
	}
	// The release notes, grouped exactly as the changelog and the GitHub
	// release group their sections: units bumping major are breaking changes,
	// minor are features, patch are fixes. One headline per line — bodies are
	// multiline prose that would destroy the line-per-entry contract, and they
	// stay in the changelog. Empty (not unset) when a group has no entries, so
	// a line-wise loop iterates zero times. The announce stage is the audience,
	// but like every listing they go to every stage, keeping scripts movable.
	env = append(env,
		"DISPAT_BREAKING_CHANGES="+unitLines(rel, ccme.BumpMajor),
		"DISPAT_FEATURES="+unitLines(rel, ccme.BumpMinor),
		"DISPAT_FIXES="+unitLines(rel, ccme.BumpPatch),
		// The dependencies section of the same notes: one "name: old -> new"
		// line per live provider update, matching what the changelog and the
		// GitHub release render — the DISPAT_UPDATED_* listing carries the
		// same data field by field for scripts that want it addressable.
		"DISPAT_DEPENDENCIES="+dependencyLines(updates))
	// Both listings go to every stage. The version stage is where manifests
	// are reconciled (§9.4), but a build baking versions into artefacts and a
	// publish choosing dist-tags read the same state, and giving each stage
	// the same environment keeps a script movable between them.
	env = append(env, wsVars...)
	env = append(env, updatedEnv(updates)...)
	// The accumulated script outputs: everything earlier scripts of the
	// package exported through their DISPAT_OUTPUT files, as
	// DISPAT_OUTPUT_<NAME> variables plus the DISPAT_OUTPUTS listing.
	env = append(env, outputsEnv(rel)...)
	return env
}

// dependencyLines renders the live provider updates the way the changelog's
// dependencies section does: "core: 1.2.3 -> 1.3.0", one per line.
func dependencyLines(updates []providerUpdate) string {
	var lines []string
	for _, u := range updates {
		lines = append(lines, u.Package+": "+u.OldVersion+" -> "+u.NewVersion)
	}
	return strings.Join(lines, "\n")
}

// unitLines returns the descriptions of the release's notes units carrying
// the given bump, newline-separated — the grouping changelog.RenderSections
// uses for its breaking/features/fixes sections. NotesUnits keeps the
// variables aligned with the changelog entry: a prerelease reports only its
// own changeset, a stable release the whole pending window.
func unitLines(rel *plan.Release, kind ccme.Bump) string {
	var lines []string
	for _, c := range rel.NotesUnits() {
		if c.Bump == kind {
			lines = append(lines, c.Header.Description)
		}
	}
	return strings.Join(lines, "\n")
}

func boolEnv(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// shouldSkip decides — with mu held — whether a package must be skipped: one
// of its changed providers failed (at any stage) or was skipped, and the
// package has no release reason of its own. It returns the blocking provider.
//
// A "reason of its own" is a direct bump or a channel change: a package moving
// between channels is being released for something a failed provider cannot
// invalidate, so it proceeds. Providers whose outcome is still pending count
// as neither; the check runs again before publish, when all provider publishes
// are final thanks to the task-graph edges.
func shouldSkip(pkg string, p *plan.Plan, results map[string]*Result) (bool, string) {
	rel := p.Releases[pkg]
	badProvider := ""
	anyPublished := false
	for _, prov := range p.Providers[pkg] {
		r, ok := results[prov]
		if !ok { // unchanged provider
			continue
		}
		switch r.Status {
		case StatusFailed, StatusSkipped:
			badProvider = prov
		case StatusPublished:
			anyPublished = true
		}
	}
	if badProvider == "" {
		return false, ""
	}
	if rel.OwnBump != ccme.BumpNone || rel.ChannelChanged() || anyPublished {
		return false, ""
	}
	return true, badProvider
}
