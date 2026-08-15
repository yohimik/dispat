package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The one selection path. Every command that can be pointed at a subset of the
// monorepo — run, preview, the step commands, compute — builds a workspace
// here and hands it to the filter package, so a --package term, a --space term
// and the folder the command was invoked from mean the same thing everywhere.

// planWorkspace assembles the resolution input from a computed plan: the
// packages in dependency order, so a narrowed selection still comes out in the
// order the commands schedule things in.
func (a *App) planWorkspace(pl *plan.Plan) filter.Workspace {
	pkgs := make([]*model.Package, 0, len(pl.Order))
	for _, name := range pl.Order {
		if rel := pl.Releases[name]; rel != nil {
			pkgs = append(pkgs, rel.Pkg)
		}
	}
	return filter.Workspace{Packages: pkgs, Spaces: a.spacePaths(), Groups: a.groupNames(), Root: a.root}
}

// discoveredWorkspace is the same for the one selecting command with no plan
// behind it: compute reads manifests, not history.
func (a *App) discoveredWorkspace(pkgs []*model.Package) filter.Workspace {
	return filter.Workspace{Packages: pkgs, Spaces: a.spacePaths(), Groups: a.groupNames(), Root: a.root}
}

// spacePaths maps every configured space onto its folder. A standalone package
// has no entry here by construction — it belongs to no space, which is what
// makes --package the only way to name one.
func (a *App) spacePaths() map[string]string {
	paths := make(map[string]string, len(a.cfg.Spaces))
	for name, sc := range a.cfg.Spaces {
		paths[name] = sc.Path
	}
	return paths
}

// groupNames lists the declared versioning groups. A space that versions as a
// group is not here — nothing declares it, its packages simply carry its name
// — and the filter reads the groups off the packages as well, so a term
// reaches both kinds and a declared group nobody joined is still recognised
// well enough to be told it holds nothing.
func (a *App) groupNames() []string {
	names := make([]string, 0, len(a.cfg.VersionGroups))
	for name := range a.cfg.VersionGroups {
		names = append(names, name)
	}
	return names
}

// narrow applies one release's selection to the computed plan and reports what
// that cost. An inactive filter — no terms, and an invocation folder that
// stands for none — returns immediately, so a whole-monorepo release pays
// nothing for the feature and its output is unchanged.
//
// The findings are logged here and the decision left to the caller: both are
// warnings, and --strict is what turns them into a refusal.
func (a *App) narrow(pl *plan.Plan, f filter.Filter) (plan.Narrowing, error) {
	sel, err := a.selectPackages(a.planWorkspace(pl), f)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot narrow the release")
		return plan.Narrowing{}, err
	}
	if !sel.Active() {
		return plan.Narrowing{}, nil
	}
	n := pl.Narrow(sel.Names)
	a.log.Info().Str("selection", sel.Description).Strs("releasing", n.Release).
		Msg("release narrowed to the selection")
	for _, w := range n.Withheld {
		a.log.Warn().Str("code", plan.CodeSelectionWithheld).Str("package", w.Pkg).
			Strs("waitingFor", w.Waiting).
			Msg("selected package cannot be released before its providers, leaving it for the next run")
	}
	for _, g := range n.Split {
		a.log.Warn().Str("code", plan.CodeSelectionSplit).Str("group", g.Name).
			Strs("releasing", g.Releasing).Strs("leftBehind", g.LeftBehind).
			Msg("selection releases part of a versioning group; the rest catches up on the next run")
	}
	return n, nil
}

// WindowOptions is the selection every package-covering command shares: a
// window decides which packages are on the table, the filter narrows it, and
// --consumers expands the result downstream.
type WindowOptions struct {
	// Filter narrows the command to named packages, spaces or versioning
	// groups (--package, --space, --group), or to the package or space folder
	// it was invoked from. It only ever narrows.
	Filter filter.Filter
	// Since replaces the "changed since the last release" window with "what
	// the commits since this git revision address" (rev..HEAD, so a branch base
	// counts only this branch's own commits). Selection follows the planner's
	// scope semantics: a commit's written scopes are authoritative, and only
	// scopeless units fall back to the files the commit changed (§6.2):
	//
	//	HEAD~1        what the last commit addressed (per-commit CI)
	//	origin/main   what this branch addressed (PR pipelines)
	//	core@1.2.0    everything since a release tag
	//	all           every package, changed or not (SinceAll)
	//
	// SinceAll is the one way to switch the changed-package window off
	// entirely, which is what makes `--since all --package core` cover core
	// whether or not it changed.
	Since string
	// Consumers additionally selects every package that transitively depends on
	// a selected one. A window covers only what the commits address, so without
	// this flag nothing reaches the downstream packages a provider change
	// affects; with it, a consumer pulled in brings its own consumers, all the
	// way down the graph. The expansion happens after the filter, on purpose:
	// `--package core --consumers` is a request for core's dependents, and
	// re-filtering them away would make the pair a no-op.
	Consumers bool
}

// coveredPackages is the whole selection rule, in one place: a window decides
// which packages are on the table — the packages --since addresses, every
// package for --since all, the release window otherwise — the filter narrows
// that window, and --consumers then expands the result downstream. The result
// comes back in the plan's dependency order, which is the order every sweep
// schedules in.
//
// A selection the window empties is an honest no-op, not an error; only a term
// that matches no package at all is one. An explicitly named package the window
// left out is said out loud, because "I asked for core and nothing happened"
// deserves a reason.
func (a *App) coveredPackages(ctx context.Context, pl *plan.Plan, opts WindowOptions) ([]string, error) {
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
	covered := sel.Keep(window)
	if sel.Active() {
		kept := make(map[string]bool, len(covered))
		for _, name := range covered {
			kept[name] = true
		}
		for _, name := range sel.Names {
			if !kept[name] {
				a.log.Info().Str("package", name).Msg("package is outside the window, nothing to do")
			}
		}
	}
	if opts.Consumers {
		covered = withConsumers(pl, covered)
	}
	return covered, nil
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

// ChangedSelection answers `if --changed`: the packages the window covers,
// under exactly the rule every sweeping command selects by — the release
// window, or what --since addresses, narrowed by the filter and expanded by
// --consumers. The condition is then simply "is that selection non-empty",
// so a gate and the run it guards can never disagree about what changed.
//
// The plan is the quiet step-command plan: diagnostics logged, no graph,
// a fatal plan refused — a gate may run many times in one pipeline, and it
// asks a question rather than printing a report.
func (a *App) ChangedSelection(ctx context.Context, opts WindowOptions) ([]string, error) {
	pl, err := a.stepPlan(ctx)
	if err != nil {
		return nil, err
	}
	return a.coveredPackages(ctx, pl, opts)
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

// selectPackages resolves one command's filter against the workspace. The
// error is returned rather than logged: each command has its own headline for
// "cannot do the thing".
func (a *App) selectPackages(ws filter.Workspace, f filter.Filter) (filter.Result, error) {
	res, err := filter.Resolve(f, ws)
	if err != nil {
		return filter.Result{}, err
	}
	if res.Active() {
		a.log.Debug().Str("selection", res.Description).Strs("packages", res.Names).
			Msg("selection narrowed")
	}
	return res, nil
}
