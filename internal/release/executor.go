// Package release executes a computed plan: it builds and publishes every
// changed package with bounded parallelism while honouring the dependency
// graph and each space's isBuildWaitingPublish setting.
package release

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/monorel/internal/gitx"
	"github.com/yohimik/monorel/internal/plan"
	"github.com/yohimik/monorel/internal/script"
	"github.com/yohimik/monorel/internal/semver"
)

// Status is the terminal state of a package release.
type Status uint8

const (
	StatusPending Status = iota
	StatusPublished
	StatusFailed
	StatusSkipped
)

func (s Status) String() string {
	switch s {
	case StatusPublished:
		return "published"
	case StatusFailed:
		return "failed"
	case StatusSkipped:
		return "skipped"
	default:
		return "pending"
	}
}

// Result is the outcome of one changed package.
type Result struct {
	Name     string
	From, To semver.Version
	Status   Status
	// FailedStage names the stage that failed ("version", "build" or
	// "publish"); empty unless Status is StatusFailed. Informational (shown
	// in the summary).
	FailedStage string
	Err         error
	Duration    time.Duration
}

// Tagger creates release tags; *gitx.CLI satisfies it. A nil Tagger on the
// Executor defers tagging to a later phase (release-commit mode, where tags
// must point at the end-of-run commit).
type Tagger interface {
	CreateTag(ctx context.Context, name, message string) error
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
// Scripts receive MONOREL_* environment variables (package, space, versions,
// bump, stage, tag; the version stage also gets MONOREL_UPDATED_PROVIDERS as
// JSON).
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

func (k taskKind) String() string {
	switch k {
	case taskVersion:
		return "version"
	case taskBuild:
		return "build"
	default:
		return "publish"
	}
}

type task struct {
	pkg  string
	kind taskKind
}

// Run executes the plan and returns one Result per changed package. Unchanged
// packages are absent from the map. Failures never abort the run: dependent
// packages are skipped (unless they have a release reason of their own) and
// independent packages continue.
func (e *Executor) Run(ctx context.Context, p *plan.Plan) map[string]*Result {
	results := make(map[string]*Result)
	changed := make(map[string]bool)
	for name, rel := range p.Releases {
		if rel.Changed() {
			changed[name] = true
			results[name] = &Result{Name: name, From: rel.Current, To: rel.Next}
		}
	}
	if len(results) == 0 {
		return results
	}

	// Build the task graph.
	indeg := make(map[task]int)
	dependents := make(map[task][]task)
	addDep := func(before, after task) {
		indeg[after]++
		dependents[before] = append(dependents[before], after)
	}
	for name := range changed {
		b, pub := task{name, taskBuild}, task{name, taskPublish}
		if _, ok := indeg[b]; !ok {
			indeg[b] = 0
		}
		addDep(b, pub)
		// Packages bumped because of provider updates run a version task
		// right before their build; provider dependencies attach to it.
		first := b
		if len(p.Releases[name].DueTo) > 0 {
			ver := task{name, taskVersion}
			if _, ok := indeg[ver]; !ok {
				indeg[ver] = 0
			}
			addDep(ver, b)
			first = ver
		}
		for _, prov := range p.Providers[name] {
			if !changed[prov] {
				continue
			}
			addDep(task{prov, taskBuild}, first)
			if p.Releases[prov].Pkg.Space.BuildWaitsPublish {
				addDep(task{prov, taskPublish}, first)
			}
			addDep(task{prov, taskPublish}, pub)
		}
	}
	total := len(indeg)

	var mu sync.Mutex
	started := make(map[string]time.Time)

	// Separate ready queues per stage, so a stalled stage never blocks the
	// other stage's budget. Version tasks share the build budget: they are
	// short local manifest updates leading straight into the build.
	var readyBuild, readyPublish []task
	push := func(t task) {
		if t.kind == taskPublish {
			readyPublish = append(readyPublish, t)
		} else {
			readyBuild = append(readyBuild, t)
		}
	}
	for t, d := range indeg {
		if d == 0 {
			push(t)
		}
	}

	buildConc := max(1, e.BuildConcurrency)
	publishConc := max(1, e.PublishConcurrency)
	doneCh := make(chan task)
	launch := func(t task) {
		go func() {
			e.execute(ctx, t, p, results, &mu, started)
			doneCh <- t
		}()
	}
	inBuild, inPublish, finished := 0, 0, 0
	for finished < total {
		for len(readyBuild) > 0 && inBuild < buildConc {
			t := readyBuild[len(readyBuild)-1]
			readyBuild = readyBuild[:len(readyBuild)-1]
			inBuild++
			launch(t)
		}
		for len(readyPublish) > 0 && inPublish < publishConc {
			t := readyPublish[len(readyPublish)-1]
			readyPublish = readyPublish[:len(readyPublish)-1]
			inPublish++
			launch(t)
		}
		t := <-doneCh
		if t.kind == taskPublish {
			inPublish--
		} else {
			inBuild--
		}
		finished++
		for _, dep := range dependents[t] {
			indeg[dep]--
			if indeg[dep] == 0 {
				push(dep)
			}
		}
	}
	return results
}

// execute runs a single version, build or publish task to completion.
func (e *Executor) execute(ctx context.Context, t task, p *plan.Plan, results map[string]*Result, mu *sync.Mutex, started map[string]time.Time) {
	rel := p.Releases[t.pkg]
	res := results[t.pkg]
	log := e.Log.With().
		Str("package", t.pkg).
		Str("stage", t.kind.String()).
		Str("version", rel.Next.String()).
		Logger()

	mu.Lock()
	if res.Status != StatusPending { // failed or skipped at an earlier stage
		mu.Unlock()
		return
	}
	if skip, reason := shouldSkip(t.pkg, p, results); skip {
		res.Status = StatusSkipped
		_, ran := started[t.pkg] // earlier stages already modified the folder?
		mu.Unlock()
		log.Warn().Str("reason", reason).Msg("skipped")
		if ran && rel.Pkg.Space.RevertOnFail {
			e.revert(ctx, rel, log)
		}
		return
	}
	if _, ok := started[t.pkg]; !ok {
		started[t.pkg] = time.Now()
	}
	// For the version stage, resolve which provider updates are still live:
	// providers that failed or were skipped never got their new version out,
	// so manifests must not be synced to them.
	var updates []providerUpdate
	if t.kind == taskVersion {
		updates = liveProviderUpdates(t.pkg, p, results)
	}
	mu.Unlock()

	fail := func(err error, msg string) {
		mu.Lock()
		res.Status = StatusFailed
		res.FailedStage = t.kind.String()
		res.Err = fmt.Errorf("%s: %w", t.kind, err)
		res.Duration = time.Since(started[t.pkg])
		mu.Unlock()
		log.Error().Err(err).Msg(msg)
		if rel.Pkg.Space.RevertOnFail {
			e.revert(ctx, rel, log)
		}
	}

	var command string
	switch t.kind {
	case taskVersion:
		command = rel.Pkg.Space.VersionScript
	case taskBuild:
		command = rel.Pkg.Space.BuildScript
	default:
		command = rel.Pkg.Space.PublishScript
	}
	if t.kind == taskVersion && len(updates) == 0 {
		// Every provider this package was bumped for failed or was skipped
		// (the package itself proceeds on its own changes): there is nothing
		// to sync manifests to, so the version script must not run.
		log.Info().Msg("version: no successfully updated providers, skipping script")
		command = ""
	}
	if command == "" {
		// No script configured: the stage completes without running anything.
		log.Debug().Msg(t.kind.String() + ": no script configured, nothing to execute")
	} else {
		log.Info().Msg(t.kind.String() + " started")
		stdout := newLineWriter(log, zerolog.InfoLevel)
		stderr := newLineWriter(log, zerolog.WarnLevel)
		err := e.Runner.Run(ctx, rel.Pkg.Dir, command, e.scriptEnv(t, p, updates), stdout, stderr)
		stdout.Flush()
		stderr.Flush()
		if err != nil {
			fail(err, t.kind.String()+" script failed")
			return
		}
	}
	if t.kind != taskPublish {
		log.Info().Msg(t.kind.String() + " succeeded")
		return
	}

	// Publish succeeded: record the release (changelog file, GitHub release,
	// ...) and tag it.
	for _, rec := range e.Recorders {
		if err := rec.Record(ctx, rel); err != nil {
			fail(err, "release recording failed")
			return
		}
	}
	tag := gitx.TagName(t.pkg, rel.Next)
	if e.Tagger != nil { // nil: tagging deferred to the release-commit phase
		if err := e.Tagger.CreateTag(ctx, tag, "release "+tag); err != nil {
			fail(err, "tagging failed")
			return
		}
	}
	mu.Lock()
	res.Status = StatusPublished
	res.Duration = time.Since(started[t.pkg])
	mu.Unlock()
	log.Info().Str("tag", tag).Msg("published")
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

// providerUpdate is the JSON shape passed to version scripts via
// MONOREL_UPDATED_PROVIDERS.
type providerUpdate struct {
	Package    string `json:"package"`
	Space      string `json:"space"`
	OldVersion string `json:"oldVersion"`
	NewVersion string `json:"newVersion"`
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
			OldVersion: pr.Current.String(),
			NewVersion: pr.Next.String(),
		})
	}
	return updates
}

// scriptEnv builds the MONOREL_* environment for one task's script.
func (e *Executor) scriptEnv(t task, p *plan.Plan, updates []providerUpdate) []string {
	rel := p.Releases[t.pkg]
	env := []string{
		"MONOREL_PACKAGE=" + t.pkg,
		"MONOREL_SPACE=" + rel.Pkg.Space.Name,
		"MONOREL_OLD_VERSION=" + rel.Current.String(),
		"MONOREL_NEW_VERSION=" + rel.Next.String(),
		"MONOREL_BUMP=" + rel.Bump.String(),
		"MONOREL_TAG=" + gitx.TagName(t.pkg, rel.Next),
		"MONOREL_STAGE=" + t.kind.String(),
	}
	if t.kind == taskVersion {
		data, err := json.Marshal(updates)
		if err != nil { // unreachable for these plain structs
			data = []byte("[]")
		}
		env = append(env, "MONOREL_UPDATED_PROVIDERS="+string(data))
	}
	return env
}

// shouldSkip decides — with mu held — whether a package must be skipped: one
// of its changed providers failed (at any stage) or was skipped, and the
// package has no release reason of its own (no own conventional commits and
// no successfully published changed provider). Providers whose outcome is
// still pending count as neither; the check runs again before publish, when
// all provider publishes are final thanks to the task-graph edges.
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
	if rel.OwnBump != semver.BumpNone || anyPublished {
		return false, ""
	}
	return true, "provider " + badProvider + " failed or was skipped, and the package has no changes of its own"
}
