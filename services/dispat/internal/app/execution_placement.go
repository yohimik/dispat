// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// The one placement question a release has to answer before it starts: is
// there anywhere at all to run the work this plan names?
//
// `runOnly` is a filter over the pool, and a filter that leaves nothing is
// not a preference the run can fall back from. A package whose build may only
// be delegated, in a configuration with no worker link, is work this run
// cannot execute anywhere: running it here would be running it in the one
// place the operator excluded, and running it nowhere would be a release that
// silently skipped a package. So it is refused, before the first hook of the
// run and with nothing published, with the package, the stage and the missing
// key named.
//
// Everything else the key says needs a pool to decide and is decided there.
// A configuration with no `execution` object and no `worker` value in play
// passes through here without a line, which is what keeps a repository that
// never asked for distributed execution releasing exactly as it did.

import (
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// checkStagePlacements refuses a release that names a frame only a worker may
// run while this configuration has no worker to run it on.
//
// It reads the plan rather than the configuration because what matters is the
// work this run would actually do: a package pinned to a worker that nothing
// selected, or that has nothing to release, is a statement about a stage this
// run never reaches.
func (a *App) checkStagePlacements(pl *plan.Plan) error {
	if a.cfg.Execution.IsDistributed() {
		return nil
	}
	for _, rel := range pl.Releasing() {
		space := rel.Pkg.Space
		isLoggingIn := len(space.LoginScript) > 0
		// In stage order rather than through a map, so a package that pinned
		// both of its stages is always refused for the same one.
		for _, stage := range []struct{ name, value string }{
			{execution.StageBuild, space.RunOnly.ResolveBuild()},
			{execution.StagePublish, space.RunOnly.ResolvePublish()},
		} {
			if !execution.ResolveStagePlacement(stage.name, stage.value, isLoggingIn).IsDelegable() {
				continue
			}
			return a.reportPreflightFailure(execution.NewDiagnostic(
				execution.CodeConfiguration, execution.CategoryConfiguration,
				"%s states runOnly %q for its %s stage and this configuration declares no execution.workers: "+
					"a stage that may only run on a worker has nowhere to run",
				rel.Pkg.Name, stage.value, stage.name))
		}
	}
	return nil
}
