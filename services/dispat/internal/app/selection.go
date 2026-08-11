package app

import (
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
