// Package release executes a computed plan: it builds and publishes every
// changed package with bounded parallelism while honouring the dependency
// graph and each space's isBuildWaitingPublish setting.
package release

import (
	"context"
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
	Err      error
	Duration time.Duration
}

// Tagger creates release tags; *gitx.CLI satisfies it.
type Tagger interface {
	CreateTag(ctx context.Context, name, message string) error
}

// ChangelogWriter records a successful release; *changelog.FileWriter
// satisfies it.
type ChangelogWriter interface {
	Append(rel *plan.Release) error
}

// Executor runs the build and publish scripts of every changed package.
//
// Scheduling model: each changed package contributes two tasks, build and
// publish. Publish always depends on the package's own build. A consumer's
// build depends on each changed provider's build — and on the provider's
// publish when the provider's space sets isBuildWaitingPublish. A consumer's
// publish always waits for its providers' publishes, since publishing against
// a not-yet-published provider version would be invalid. Build and publish
// stages have independent parallelism budgets: at most BuildConcurrency build
// scripts and PublishConcurrency publish scripts run at any moment. A package
// never runs its two tasks concurrently.
type Executor struct {
	BuildConcurrency   int
	PublishConcurrency int
	Runner             script.Runner
	Tagger             Tagger
	Changelog          ChangelogWriter
	Log                zerolog.Logger
}

type taskKind uint8

const (
	taskBuild taskKind = iota
	taskPublish
)

func (k taskKind) String() string {
	if k == taskBuild {
		return "build"
	}
	return "publish"
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
		for _, prov := range p.Providers[name] {
			if !changed[prov] {
				continue
			}
			addDep(task{prov, taskBuild}, b)
			if p.Releases[prov].Pkg.Space.BuildWaitsPublish {
				addDep(task{prov, taskPublish}, b)
			}
			addDep(task{prov, taskPublish}, pub)
		}
	}
	total := len(indeg)

	var mu sync.Mutex
	started := make(map[string]time.Time)

	// Separate ready queues per stage, so a stalled stage never blocks the
	// other stage's budget.
	var readyBuild, readyPublish []task
	push := func(t task) {
		if t.kind == taskBuild {
			readyBuild = append(readyBuild, t)
		} else {
			readyPublish = append(readyPublish, t)
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
		if t.kind == taskBuild {
			inBuild--
		} else {
			inPublish--
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

// execute runs a single build or publish task to completion.
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
		mu.Unlock()
		log.Warn().Str("reason", reason).Msg("skipped")
		return
	}
	if t.kind == taskBuild {
		started[t.pkg] = time.Now()
	}
	mu.Unlock()

	fail := func(err error, msg string) {
		mu.Lock()
		res.Status = StatusFailed
		res.Err = fmt.Errorf("%s: %w", t.kind, err)
		res.Duration = time.Since(started[t.pkg])
		mu.Unlock()
		log.Error().Err(err).Msg(msg)
	}

	command := rel.Pkg.Space.BuildScript
	if t.kind == taskPublish {
		command = rel.Pkg.Space.PublishScript
	}
	log.Info().Msg(t.kind.String() + " started")
	stdout := newLineWriter(log, zerolog.InfoLevel)
	stderr := newLineWriter(log, zerolog.WarnLevel)
	err := e.Runner.Run(ctx, rel.Pkg.Dir, command, stdout, stderr)
	stdout.Flush()
	stderr.Flush()
	if err != nil {
		fail(err, t.kind.String()+" script failed")
		return
	}
	if t.kind == taskBuild {
		log.Info().Msg("build succeeded")
		return
	}

	// Publish succeeded: record the changelog entry and tag the release.
	if err := e.Changelog.Append(rel); err != nil {
		fail(err, "changelog update failed")
		return
	}
	tag := gitx.TagName(t.pkg, rel.Next)
	if err := e.Tagger.CreateTag(ctx, tag, "release "+tag); err != nil {
		fail(err, "tagging failed")
		return
	}
	mu.Lock()
	res.Status = StatusPublished
	res.Duration = time.Since(started[t.pkg])
	mu.Unlock()
	log.Info().Str("tag", tag).Msg("published")
}

// shouldSkip decides — with mu held — whether a package must be skipped: one
// of its changed providers failed or was skipped, and the package has no
// release reason of its own (no own conventional commits and no successfully
// published changed provider). Providers whose outcome is still pending count
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
	if rel.OwnBump != semver.BumpNone || anyPublished {
		return false, ""
	}
	return true, "provider " + badProvider + " failed or was skipped, and the package has no changes of its own"
}
