package app

import (
	"context"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The package sweep: the one graph-ordered execution every command that covers
// a set of packages shares. It decides who runs, in what order, under which
// concurrency budget, and what a failure does to the dependents; the command
// supplies only what running means for one package.
//
// `dispat run` was the first of these and is still the shape of it — a script
// per package, providers first, independent packages together — but nothing in
// here knows about scripts. That is what lets `dispat autowriter` and the step
// commands ride the same rails, and what makes the next such command one small
// file rather than a copy of this one.

// task is one package's resolved work, ready to be run. A nil task means the
// package has nothing to do: a no-op, not a failure.
type task func(ctx context.Context) error

// packageWork is one command's per-package work inside a sweep.
//
// It comes in two phases because the sweep has to know a package had something
// to do even when the cascade skips it before it ever runs. resolve answers
// that question — and is where a work reports the packages it passes over, in
// its own words — while the task it returns is the work itself.
type packageWork interface {
	// stage is the value of the `stage` field on every event the sweep logs
	// about this work: "run:lint", "changelog", "autowriter".
	stage() string
	// resolve prepares one package's work. A nil task means the package has
	// nothing to do; an error fails that package alone, exactly as a failing
	// task does.
	resolve(ctx context.Context, rel *plan.Release) (task, error)
}

// sweepOptions is how a command parameterises the sweep.
type sweepOptions struct {
	// OnError is the failure policy for a failed package's dependents:
	// OnErrorSkip (the default when empty) or OnErrorContinue.
	OnError string
	// Budget is how many packages may run at once. Zero takes the configured
	// build concurrency, which is what the graph-shaped commands want; 1 is a
	// serial sweep, which is what the commands writing one shared resource —
	// the git index, a lock file — need.
	Budget int
}

// sweepReport is what one sweep did, in counts. Resolved is independent of the
// other three: it records how many covered packages had something to do at all,
// which is what tells a sweep that covered only packages with nothing to do
// apart from one that genuinely did nothing.
type sweepReport struct{ Ran, Failed, Skipped, Resolved int }

// outcome is one package's terminal state within a sweep.
type outcome struct{ failed, skipped, ran, defined bool }

// sweep is the state of one sweep: the plan, the covered packages, the
// per-package outcomes (mu guards results; everything else is read-only once
// built) and the work being swept.
type sweep struct {
	app     *App
	pl      *plan.Plan
	work    packageWork
	onError string
	covered map[string]*plan.Release
	results map[string]*outcome
	mu      sync.Mutex
}

// runSweep executes work over the covered packages, honouring the dependency
// graph: a package starts only after every covered provider has finished, and
// independent packages run concurrently within the budget.
//
// The returned error is the scheduler's alone — an interruption, or a graph
// that can never drain. A package's own failure is in the report, because what
// a failed or a wholly unresolved sweep means is the command's call: `dispat
// run` treats "nothing resolved" as the typo it usually is, while a step
// command over packages that turn out not to be releasing is an honest no-op.
func (a *App) runSweep(ctx context.Context, pl *plan.Plan, covered []string,
	work packageWork, opts sweepOptions) (sweepReport, error) {
	onError := opts.OnError
	if onError == "" {
		onError = OnErrorSkip
	}
	s := &sweep{
		app: a, pl: pl, work: work, onError: onError,
		covered: coveredReleases(pl, covered),
		results: make(map[string]*outcome, len(covered)),
	}

	// The task graph: one node per covered package, edges from every covered
	// provider — the same shape the release executor schedules, at package
	// rather than stage granularity.
	sched := graph.NewScheduler[string]()
	for _, pkg := range covered {
		sched.Add(pkg)
	}
	// covered, not the map: its plan order keeps the scheduler's insertion
	// order — and with it the launch order — deterministic.
	for _, pkg := range covered {
		for _, prov := range pl.Providers[pkg] {
			if _, ok := s.covered[prov]; ok {
				sched.AddEdge(prov, pkg)
			}
		}
	}

	budget := opts.Budget
	if budget == 0 {
		// A work that must not run two packages at once says so itself, so no
		// caller can lose that by forgetting to ask for it.
		budget = budgetFor(work)
	}
	if budget == 0 {
		budget = a.cfg.BuildConcurrency
	}
	// One class, one budget: a sweep reuses the executor's pump, build weights
	// included, so a heavyweight package occupies the slots it is worth here
	// too.
	drainErr := graph.Drain(ctx, sched,
		func(string) struct{} { return struct{}{} },
		func(struct{}) int { return budget },
		func(pkg string) int { return pl.Releases[pkg].Pkg.BuildWeight },
		func(pkg string) { s.execute(ctx, pkg) })

	return s.report(), drainErr
}

// serialWork marks the works that must not run two packages at once. The git
// index is one file and a repository has one HEAD, so anything committing,
// tagging or pushing is serial by nature; everything else writes only inside
// the package folder it was handed and rides the build budget.
type serialWork interface{ serial() bool }

// budgetFor asks the work whether it is one of those, answering 0 — "no
// opinion, take the configured budget" — when it is not.
func budgetFor(work packageWork) int {
	if s, ok := work.(serialWork); ok && s.serial() {
		return 1
	}
	return 0
}

// coveredReleases indexes the covered packages by name, which is how both the
// sweep and the works that carry data between packages ask "is this provider
// one of ours?" without walking the slice every time.
func coveredReleases(pl *plan.Plan, covered []string) map[string]*plan.Release {
	rels := make(map[string]*plan.Release, len(covered))
	for _, pkg := range covered {
		rels[pkg] = pl.Releases[pkg]
	}
	return rels
}

// report tallies the outcomes once the drain is over, when nothing else holds
// the mutex any more.
func (s *sweep) report() sweepReport {
	var rep sweepReport
	for _, res := range s.results {
		if res.defined {
			rep.Resolved++
		}
		switch {
		case res.failed:
			rep.Failed++
		case res.skipped:
			rep.Skipped++
		case res.ran:
			rep.Ran++
		}
	}
	return rep
}

// blocker returns — with mu held by the caller — the first provider whose work
// failed or was skipped, or "".
func (s *sweep) blocker(pkg string) string {
	for _, prov := range s.pl.Providers[pkg] {
		if r, ok := s.results[prov]; ok && (r.failed || r.skipped) {
			return prov
		}
	}
	return ""
}

// execute takes one package through the sweep: resolve, cascade, run.
func (s *sweep) execute(ctx context.Context, pkg string) {
	res := &outcome{}
	defer func() {
		s.mu.Lock()
		s.results[pkg] = res
		s.mu.Unlock()
	}()

	log := s.app.log.With().Str("package", pkg).Str("stage", s.work.stage()).Logger()
	// What this package would do is asked before the cascade runs, so a package
	// skipped for a failed provider still counts as one the sweep reached.
	t, err := s.work.resolve(ctx, s.covered[pkg])
	if err != nil {
		res.failed = true
		log.Error().Err(err).Msg("package failed")
		return
	}
	res.defined = t != nil

	if s.onError == OnErrorSkip {
		// Providers finished before this package was handed out, so their
		// outcomes are final; a skipped provider cascades the skip.
		s.mu.Lock()
		blocker := s.blocker(pkg)
		s.mu.Unlock()
		if blocker != "" {
			res.skipped = true
			log.Warn().Str("blockedBy", blocker).
				Msg("package skipped: a dependency failed or was skipped")
			return
		}
	}

	if t == nil {
		return // the work said its piece while resolving
	}
	if err := t(ctx); err != nil {
		res.failed = true
		log.Error().Err(err).Msg("package failed")
		return
	}
	res.ran = true
}
