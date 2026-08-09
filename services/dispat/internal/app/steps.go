package app

import (
	"context"
	"fmt"
	"strings"

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

// stepTargets resolves which packages a step command covers, mirroring the
// run command's narrowing: an explicit pkg targets exactly that package
// (unknown is an error; known but not releasing is a logged no-op, so a flow
// does not fail over a package the planner held or converged). With no pkg,
// dir narrows to the package whose folder it points inside, under the same
// not-releasing rule; anywhere else (the monorepo root included) the command
// covers every releasing package, in dependency order.
func (a *App) stepTargets(pl *plan.Plan, pkg, dir string) ([]string, error) {
	if pkg == "" && dir != "" {
		pkg = a.packageAt(pl, dir)
	} else if pkg != "" && pl.Releases[pkg] == nil {
		return nil, fmt.Errorf("unknown package %q (discovered: %s)", pkg, strings.Join(pl.Order, ", "))
	}
	if pkg != "" {
		if !pl.Releases[pkg].Releasing() {
			a.log.Info().Str("package", pkg).Msg("package is not releasing, nothing to do")
			return nil, nil
		}
		return []string{pkg}, nil
	}
	var targets []string
	for _, rel := range pl.Releasing() {
		targets = append(targets, rel.Pkg.Name)
	}
	return targets, nil
}
