package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The standalone step commands — changelog, autoversion, commit — expose the
// release pipeline's native steps to custom flows, so a stage script can run
// a step at the moment the flow needs it (a changelog written before the
// release commit lands inside the tagged tree) and the release stage later
// finds the work done and skips it.

// stepPlan computes the quiet plan the step commands run against: diagnostics
// printed, no graph (a stage-script invocation must not spam the full plan),
// and a fatal plan refused exactly like every other command.
func (a *App) stepPlan(ctx context.Context) (*plan.Plan, error) {
	pl, err := a.plan(ctx)
	if err != nil {
		return nil, err
	}
	a.printDiagnostics(pl)
	if pl.Fatal() {
		a.log.Error().Msg("refusing to run: the repository cannot produce a correct plan")
		return nil, fmt.Errorf("no correct plan exists")
	}
	return pl, nil
}

// stepTargets resolves which packages a step command covers, through the same
// filter as the run command: the releasing packages in dependency order,
// narrowed by --package, --space or the folder the command was invoked from. A
// term matching no package is an error; a selected package the planner held or
// converged is a logged no-op, so a custom flow never fails over one.
func (a *App) stepTargets(pl *plan.Plan, f filter.Filter) ([]string, error) {
	sel, err := a.selectPackages(a.planWorkspace(pl), f)
	if err != nil {
		return nil, err
	}
	var releasing []string
	for _, rel := range pl.Releasing() {
		releasing = append(releasing, rel.Pkg.Name)
	}
	targets := sel.Keep(releasing)
	if sel.Active() {
		kept := make(map[string]bool, len(targets))
		for _, name := range targets {
			kept[name] = true
		}
		for _, name := range sel.Names {
			if !kept[name] {
				a.log.Info().Str("package", name).Msg("package is not releasing, nothing to do")
			}
		}
	}
	return targets, nil
}
