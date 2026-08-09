package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/config"
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
	// Package, when set, targets exactly one package: the script runs there
	// whether or not the package changed, and the dependency graph plays no
	// part. Naming an unknown package is an error, as is a target whose space
	// does not define the script — a targeted run that runs nothing would be
	// how a typo hides.
	Package string
	// Dir, when set (the `dispat <script>` shorthand), narrows the run the
	// same way when it points inside a package's folder; anywhere else — the
	// monorepo root itself, or outside every package — the run covers the
	// changed packages as usual.
	Dir string
	// Since replaces the "changed since the last release" selection with
	// "what the commits since this git revision address" (rev..HEAD, so a
	// branch base counts only this branch's own commits). Selection follows
	// the planner's scope semantics: a commit's written scopes are
	// authoritative, and only scopeless units fall back to the files the
	// commit changed (§6.2):
	//
	//	HEAD~1        what the last commit addressed (per-commit CI)
	//	origin/main   what this branch addressed (PR pipelines)
	//	core@1.2.0    everything since a release tag
	//	all           every package, changed or not (SinceAll)
	//
	// Graph ordering and output carrying still apply within the selected set.
	// Since is mutually exclusive with Package (the CLI rejects the pair) and
	// overrides Dir's implicit narrowing — an explicit flag beats inference.
	Since string
}

// RunScript computes the plan and executes the named space run script inside
// each changed package with the package's full DISPAT_* environment,
// honouring the dependency graph: a package's script starts only after every
// changed provider's has finished, and independent packages run concurrently
// within the build concurrency budget. A changed package whose space does not
// define the script completes as a no-op; a name no space defines at all is
// an error, because running nothing silently is how a typo hides. opts can
// narrow the run to one package (see RunOptions); any failure makes the
// whole command fail.
func (a *App) RunScript(ctx context.Context, name string, opts RunOptions) error {
	defined := false
	for _, sc := range a.cfg.Spaces {
		if _, ok := sc.RunScript(name); ok {
			defined = true
			break
		}
	}
	for _, po := range a.cfg.Packages {
		if defined {
			break
		}
		if _, ok := po.RunScripts[strings.ToLower(name)]; ok {
			defined = true
		}
	}
	if !defined {
		// A script defined only in a package folder's own config file is not
		// in the loaded config at all; discovery reads those files, so the
		// typo guard consults it before rejecting.
		if pkgs, _, err := config.DiscoverPackages(a.cfg, a.root); err == nil {
			for _, p := range pkgs {
				if _, ok := p.Space.RunScripts[strings.ToLower(name)]; ok {
					defined = true
					break
				}
			}
		}
	}
	if !defined {
		msg := fmt.Sprintf("no space or package defines run script %q", name)
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
	target, err := a.runTarget(pl, name, opts)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot run the script")
		return err
	}

	// What the run covers: one named package (targeted, changed or not), the
	// packages --since selects, or the packages a release would touch.
	var selected []string
	switch {
	case target != "":
		selected = []string{target}
	case opts.Since != "":
		if selected, err = a.sincePackages(ctx, pl, opts.Since); err != nil {
			a.log.Error().Err(err).Msg("cannot run the script")
			return err
		}
	default:
		for _, rel := range pl.Releasing() {
			selected = append(selected, rel.Pkg.Name)
		}
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
	for pkg := range changed {
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

	ran, failed, skipped := 0, 0, 0
	for _, res := range run.results {
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
	if failed > 0 {
		return fmt.Errorf("%d run script(s) failed", failed)
	}
	return nil
}

// runTarget resolves the single package a run is narrowed to, or "" for the
// ordinary changed-packages run. An explicit RunOptions.Package must exist
// and its space must define the script; RunOptions.Dir narrows only when it
// points inside some package's folder, and silently does not when it points
// anywhere else (the monorepo root, a space folder, outside the tree) — or
// when --since is in play, because an explicit flag beats inference.
func (a *App) runTarget(pl *plan.Plan, name string, opts RunOptions) (string, error) {
	target := opts.Package
	if target == "" && opts.Dir != "" && opts.Since == "" {
		target = a.packageAt(pl, opts.Dir)
	}
	if target == "" {
		return "", nil
	}
	rel := pl.Releases[target]
	if rel == nil {
		return "", fmt.Errorf("unknown package %q (discovered: %s)", target, strings.Join(pl.Order, ", "))
	}
	if _, ok := rel.Pkg.Space.RunScripts[strings.ToLower(name)]; !ok {
		return "", fmt.Errorf("space %q of package %q does not define run script %q",
			rel.Pkg.Space.Name, target, name)
	}
	return target, nil
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

// packageAt returns the package whose folder contains dir, or "" when dir is
// not inside any package (the root itself included). Both sides are compared
// absolute, so the relative --root default and absolute package dirs meet on
// equal terms.
func (a *App) packageAt(pl *plan.Plan, dir string) string {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for _, name := range pl.Order {
		rel := pl.Releases[name]
		if rel == nil {
			continue
		}
		pkgDir, err := filepath.Abs(rel.Pkg.Dir)
		if err != nil {
			continue
		}
		if absDir == pkgDir || strings.HasPrefix(absDir, pkgDir+string(filepath.Separator)) {
			return name
		}
	}
	return ""
}

// runOutcome is one package's terminal state within a `dispat run`.
type runOutcome struct{ failed, skipped, ran bool }

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

	cmd, ok := rel.Pkg.Space.RunScripts[strings.ToLower(s.name)]
	if !ok {
		s.app.log.Debug().Str("package", pkg).Str("space", rel.Pkg.Space.Name).
			Msgf("space does not define run script %q, skipping", s.name)
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
