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
	return filter.Workspace{Packages: pkgs, Spaces: a.spacePaths(), Root: a.root}
}

// discoveredWorkspace is the same for the one selecting command with no plan
// behind it: compute reads manifests, not history.
func (a *App) discoveredWorkspace(pkgs []*model.Package) filter.Workspace {
	return filter.Workspace{Packages: pkgs, Spaces: a.spacePaths(), Root: a.root}
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
