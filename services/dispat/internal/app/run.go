package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// Run script error policies: what a failing run script does to the failed
// package's dependents (the --on-error flag of `dispat run`).
const (
	// OnErrorSkip (default) skips the changed dependents of a failed package
	// — transitively, exactly like a release skips consumers of a failed
	// provider. Independent packages always keep running.
	OnErrorSkip = "skip"
	// OnErrorContinue still runs the dependents; the run's exit code reports
	// the failure either way.
	OnErrorContinue = "continue"
)

// ValidOnError reports whether the value is a known run error policy.
func ValidOnError(v string) bool { return v == OnErrorSkip || v == OnErrorContinue }

// SinceAll is the reserved --since value selecting every package, changed or
// not — "run this everywhere".
const SinceAll = "all"

// RunOptions selects what RunScript runs over, beyond the script name.
type RunOptions struct {
	// OnError is the failure policy for the failed package's dependents:
	// OnErrorSkip (the default when empty) or OnErrorContinue.
	OnError string
	// Filter narrows the run to named packages or spaces (--package,
	// --space), or to the package or space folder the command was invoked
	// from. It only ever narrows: the window below decides which packages are
	// on the table, and the filter picks from them.
	Filter filter.Filter
	// Since replaces the "changed since the last release" window with "what
	// the commits since this git revision address" (rev..HEAD, so a branch
	// base counts only this branch's own commits). Selection follows the
	// planner's scope semantics: a commit's written scopes are authoritative,
	// and only scopeless units fall back to the files the commit changed
	// (§6.2):
	//
	//	HEAD~1        what the last commit addressed (per-commit CI)
	//	origin/main   what this branch addressed (PR pipelines)
	//	core@1.2.0    everything since a release tag
	//	all           every package, changed or not (SinceAll)
	//
	// SinceAll is the one way to switch the changed-package window off
	// entirely, which is what makes `--since all --package core` run core
	// whether or not it changed. Graph ordering and output carrying still
	// apply within the selected set.
	Since string
	// Consumers additionally selects every package that transitively depends
	// on a selected one. A window covers only what the commits address, so
	// without this flag nothing re-runs the downstream packages a provider
	// change affects; with it, a consumer pulled in brings its own consumers,
	// all the way down the graph. The expansion happens after the filter, on
	// purpose: `--package core --consumers` is a request for core's
	// dependents, and re-filtering them away would make the pair a no-op. The
	// added packages run whether or not they changed, after their selected
	// providers, with the same skip cascade as any selected package.
	Consumers bool
}

// RunScript computes the plan and executes the named script inside each
// changed package that resolves it, with the package's full DISPAT_*
// environment, honouring the dependency graph: a package's script starts only
// after every changed provider's has finished, and independent packages run
// concurrently within the build concurrency budget.
//
// A package resolves the name through its own scripts, then its space's, then
// the top level's, so where a name is defined is what a run covers: a
// top-level script reaches every changed package, a space's reaches that
// space's, and a package's reaches that package alone. A selected package
// that resolves nothing completes as a no-op — but a name nothing defines, or
// a selection in which no package resolves it, is an error, because running
// nothing silently is how a typo hides. opts decides which packages the run
// covers (see RunOptions); any failure makes the whole command fail.
func (a *App) RunScript(ctx context.Context, name string, opts RunOptions) error {
	if !a.scriptDefinedAnywhere(name) {
		msg := fmt.Sprintf("no script %q is defined at the top level, in a space or in a package", name)
		if note, ok := lookupNote(name); ok {
			msg = note
		}
		err := errors.New(msg)
		a.log.Error().Err(err).Msg("unknown run script")
		return err
	}
	onError := opts.OnError
	if onError == "" {
		onError = OnErrorSkip
	}

	pl, err := a.computePlan(ctx)
	if err != nil {
		return err
	}
	if pl.Fatal() {
		a.log.Error().Msg("refusing to run: the repository cannot produce a correct plan")
		return errors.New("no correct plan exists")
	}
	selected, err := a.runSelection(ctx, pl, opts)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot run the script")
		return err
	}

	// The task graph: one node per selected package, edges from every
	// selected provider — the same shape the release executor schedules, at
	// package rather than stage granularity.
	changed := make(map[string]*plan.Release)
	sched := graph.NewScheduler[string]()
	for _, pkg := range selected {
		changed[pkg] = pl.Releases[pkg]
		sched.Add(pkg)
	}
	// selected, not the changed map: its plan order keeps the scheduler's
	// insertion order — and with it the launch order — deterministic.
	for _, pkg := range selected {
		for _, prov := range pl.Providers[pkg] {
			if _, ok := changed[prov]; ok {
				sched.AddEdge(prov, pkg)
			}
		}
	}

	run := &scriptRun{
		app: a, pl: pl, changed: changed,
		results: make(map[string]*runOutcome, len(changed)),
		// The workspace listing depends only on the plan, so it is built once
		// here and shared by every package's environment.
		wsVars: release.WorkspaceEnv(pl, a.log),
		name:   name, stage: "run:" + name, onError: onError,
		runner: &script.ShellRunner{Shell: a.cfg.Shell},
	}
	// One class, one budget: the run command reuses the executor's pump,
	// build budget and build weights included.
	drainErr := graph.Drain(ctx, sched,
		func(string) struct{} { return struct{}{} },
		func(struct{}) int { return a.cfg.BuildConcurrency },
		func(pkg string) int { return pl.Releases[pkg].Pkg.BuildWeight },
		func(pkg string) { run.execute(ctx, pkg) })

	ran, failed, skipped, resolved := 0, 0, 0, 0
	for _, res := range run.results {
		if res.defined {
			resolved++
		}
		switch {
		case res.failed:
			failed++
		case res.skipped:
			skipped++
		case res.ran:
			ran++
		}
	}
	a.log.Info().Str("script", name).Int("ran", ran).Int("failed", failed).
		Int("skipped", skipped).Msg("run finished")
	if drainErr != nil {
		a.log.Warn().Err(drainErr).Msg("run interrupted")
		return drainErr
	}
	// The script exists somewhere — the guard above said so — but not where
	// this run looked. Silence here would read like a clean run of nothing,
	// so the mismatch between the name's level and the selection is an error.
	// A selection that is empty to begin with is not: nothing changed, and a
	// run over no packages is an honest no-op.
	if len(selected) > 0 && resolved == 0 {
		err := fmt.Errorf("no selected package defines script %q (selected: %s)",
			name, strings.Join(selected, ", "))
		a.log.Error().Err(err).Msg("nothing to run")
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d run script(s) failed", failed)
	}
	return nil
}

// scriptDefinedAnywhere reports whether any level of the configuration binds
// the name to a command. It is the typo guard, and it runs before the plan so
// a misspelling costs nothing: a name that exists somewhere may still turn out
// to reach none of the packages a run selects, which the run itself reports.
// The three cheap levels are asked first; only a name none of them knows is
// worth a package discovery, which is the one place a script defined solely in
// a package folder's own config file can be seen.
func (a *App) scriptDefinedAnywhere(name string) bool {
	if _, ok := a.cfg.Script(name); ok {
		return true
	}
	for _, sc := range a.cfg.Spaces {
		if _, ok := sc.Script(name); ok {
			return true
		}
	}
	key := strings.ToLower(name)
	for _, po := range a.cfg.Packages {
		if _, ok := po.Scripts[key]; ok {
			return true
		}
	}
	pkgs, _, err := config.DiscoverPackages(a.cfg, a.root)
	if err != nil {
		return false
	}
	for _, p := range pkgs {
		if _, ok := p.Space.Scripts[key]; ok {
			return true
		}
	}
	return false
}

// runSelection is the whole selection rule, in one place: a window decides
// which packages are on the table — the packages --since addresses, every
// package for --since all, the release window otherwise — the filter narrows
// that window, and --consumers then expands the result downstream. A selection
// the filter empties is an honest no-op, not an error; only a term that
// matches no package at all is one.
func (a *App) runSelection(ctx context.Context, pl *plan.Plan, opts RunOptions) ([]string, error) {
	sel, err := a.selectPackages(a.planWorkspace(pl), opts.Filter)
	if err != nil {
		return nil, err
	}
	var window []string
	if opts.Since != "" {
		if window, err = a.sincePackages(ctx, pl, opts.Since); err != nil {
			return nil, err
		}
	} else {
		for _, rel := range pl.Releasing() {
			window = append(window, rel.Pkg.Name)
		}
	}
	selected := sel.Keep(window)
	if opts.Consumers {
		selected = withConsumers(pl, selected)
	}
	return selected, nil
}

// withConsumers expands a selection with every package that transitively
// depends on a selected one, returning the union in the plan's dependency
// order. The whole declared graph is walked — not just edges among the
// already-selected — so a consumer pulled in brings its own consumers too.
func withConsumers(pl *plan.Plan, selected []string) []string {
	consumers := make(map[string][]string) // provider -> direct consumers
	for _, name := range pl.Order {
		for _, prov := range pl.Providers[name] {
			consumers[prov] = append(consumers[prov], name)
		}
	}
	in := make(map[string]bool, len(selected))
	queue := append([]string(nil), selected...)
	for _, name := range selected {
		in[name] = true
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		for _, c := range consumers[name] {
			if !in[c] {
				in[c] = true
				queue = append(queue, c)
			}
		}
	}
	out := make([]string, 0, len(in))
	for _, name := range pl.Order {
		if in[name] {
			out = append(out, name)
		}
	}
	return out
}

// sincePackages resolves --since onto package names: every package for
// SinceAll, otherwise the packages the commits in rev..HEAD address under the
// planner's own scope semantics — a written scope-set is authoritative, and
// only scopeless units fall back to the commit's changed files (§6.2).
func (a *App) sincePackages(ctx context.Context, pl *plan.Plan, rev string) ([]string, error) {
	if rev == SinceAll {
		return append([]string(nil), pl.Order...), nil
	}
	opts, err := a.planOptions()
	if err != nil {
		return nil, err
	}
	selected, err := plan.PackagesChangedSince(ctx, a.git, opts, rev)
	if err != nil {
		return nil, fmt.Errorf("resolving --since %q: %w", rev, err)
	}
	return selected, nil
}

// runOutcome is one package's terminal state within a `dispat run`. defined
// is independent of the other three: it records that the package resolved the
// script at all, which is what tells a run that covered only packages without
// it apart from one that genuinely ran nothing.
type runOutcome struct{ failed, skipped, ran, defined bool }

// scriptRun is the shared state of one RunScript invocation: the plan, the
// changed packages, the per-package outcomes (mu guards results; everything
// else is read-only once built) and the run's own parameters.
type scriptRun struct {
	app     *App
	pl      *plan.Plan
	changed map[string]*plan.Release
	results map[string]*runOutcome
	wsVars  []string // the shared workspace listing, built once per run
	mu      sync.Mutex
	name    string
	stage   string
	onError string
	runner  script.Runner
}

// blocker returns — with mu held by the caller — the first provider whose
// script failed or was skipped, or "".
func (s *scriptRun) blocker(pkg string) string {
	for _, prov := range s.pl.Providers[pkg] {
		if r, ok := s.results[prov]; ok && (r.failed || r.skipped) {
			return prov
		}
	}
	return ""
}

// execute runs the named script for one package to completion.
func (s *scriptRun) execute(ctx context.Context, pkg string) {
	rel := s.changed[pkg]
	res := &runOutcome{}
	defer func() {
		s.mu.Lock()
		s.results[pkg] = res
		s.mu.Unlock()
	}()

	// Carry the changed providers' exports in before anything of this
	// package runs: the run-command counterpart of the pipeline's
	// per-package accumulation, transitive by construction (each provider
	// carried its own providers' exports before exporting). Providers
	// merge in name order, so a name two of them export resolves
	// deterministically; the package's own later export overrides either.
	provs := append([]string(nil), s.pl.Providers[pkg]...)
	sort.Strings(provs)
	for _, prov := range provs {
		if pr, ok := s.changed[prov]; ok {
			release.MergeOutputs(rel, pr.Outputs)
		}
	}

	// The one resolution: the package's own scripts over its space's over the
	// top level's, already merged into the effective map when the package's
	// space was built.
	cmd, ok := rel.Pkg.Space.Scripts[strings.ToLower(s.name)]
	res.defined = ok

	if s.onError == OnErrorSkip {
		// Providers finished before this package was handed out, so their
		// outcomes are final; a skipped provider cascades the skip.
		s.mu.Lock()
		blocker := s.blocker(pkg)
		s.mu.Unlock()
		if blocker != "" {
			res.skipped = true
			s.app.log.Warn().Str("package", pkg).Str("stage", s.stage).Str("blockedBy", blocker).
				Msg("run script skipped: a dependency's script failed or was skipped")
			return
		}
	}

	if !ok {
		s.app.log.Debug().Str("package", pkg).Str("space", rel.Pkg.Space.Name).
			Msgf("package does not define script %q, skipping", s.name)
		return
	}
	log := s.app.log.With().Str("package", pkg).Str("stage", s.stage).Logger()
	log.Info().Msg("run script started")
	seq := release.Sequence{Runner: s.runner, Dir: rel.Pkg.Dir, Stage: s.stage, Commands: []string{cmd},
		Env: release.CommandEnv(s.pl, pkg, s.stage, s.wsVars), Log: log, FailFast: true}
	if err := seq.RunMergingOutputs(ctx, rel); err != nil {
		res.failed = true
		log.Error().Err(err).Msg("run script failed")
		return
	}
	res.ran = true
}
