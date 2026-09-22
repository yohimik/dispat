// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// A `dispat run` sweep executed on worker nodes (CCME §28.10).
//
// A sweep with worker links runs the way a distributed release runs its task
// graph, minus everything a release is for. The refusals are the same and come
// first: a worker may not start one, and a lock bypass beside worker links is
// refused. The plan is fixed and named, every link is probed, and each
// package's task goes through the coordinator, placed by the package's own
// `runOnly` as its build would be. What a sweep does not do is take a release
// lock: it records nothing and authorizes no effect a lock would have to
// fence, so its messages are bound to a generation drawn from its own run
// identity, and a release of the same repositories may run beside it.
//
// With no worker links every function here returns before it does anything,
// and the sweep is the one `dispat run` has always run: the same events, no
// lock, no mailbox and no summary.

import (
	"context"
	"sort"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// checkSweepPlacements refuses a sweep that names a task only a worker may run
// while this invocation has no worker link to run it on.
//
// It is the release's own refusal asked of a sweep: a package whose `runOnly`
// pins its build to a worker is a package whose work must not run on the
// machine the run was started on, and a sweep's task is that work. A package
// the script does not reach has nothing to place and is passed over.
func (a *App) checkSweepPlacements(work *scriptWork, covered []string) error {
	if a.cfg.Execution.IsDistributed() {
		return nil
	}
	for _, rel := range work.resolveReachedReleases(covered) {
		space := rel.Pkg.Space
		placement := execution.ResolveStagePlacement(execution.StageBuild,
			space.RunOnly.ResolveBuild(), len(space.LoginScript) > 0)
		if !placement.IsDelegable() {
			continue
		}
		err := execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			"%s states runOnly %q for its build stage and this invocation has no worker link: "+
				"its %s task may only run on a worker, and there is none to run it on",
			rel.Pkg.Name, space.RunOnly.ResolveBuild(), work.stage())
		a.logError(err).Str("script", work.name).Msg("cannot run the script")
		return err
	}
	return nil
}

// resolveReachedReleases is every covered package the swept script reaches, in
// the plan's order: the packages that would have a task at all.
func (w *scriptWork) resolveReachedReleases(covered []string) []*plan.Release {
	reached := make([]*plan.Release, 0, len(covered))
	for _, name := range covered {
		rel := w.covered[name]
		if rel == nil {
			continue
		}
		if _, isDefined := rel.Pkg.Space.Script(w.name); !isDefined {
			continue
		}
		reached = append(reached, rel)
	}
	return reached
}

// openSweepDispatch fixes the plan, probes every link and opens the pool a
// sweep with worker links places its tasks on, and answers nil for a sweep
// that has none.
//
// The coordinator is answered even when the probe refused the pool, because it
// already owns the probe branches it created and the caller's deferred close
// is what deletes them.
func (a *App) openSweepDispatch(ctx context.Context, work *scriptWork, covered []string) (*execution.Coordinator, error) {
	if !a.cfg.Execution.IsDistributed() {
		return nil, nil
	}
	// The digest names the plan every assignment of the sweep carries. A sweep
	// covers the plan whole and releases nothing of it, so it is named with no
	// release selection.
	if err := a.recordFixedPlan(ctx, work.pl, ReleaseOptions{}); err != nil {
		return nil, err
	}
	coordinator, err := a.newCoordinator(execution.FormatSweepGeneration(a.runID), nil)
	if err != nil {
		return nil, a.reportSweepRefusal(err)
	}
	if err := coordinator.Preflight(ctx, work.resolvePlatforms(covered)); err != nil {
		return coordinator, a.reportSweepRefusal(err)
	}
	a.openDispatch(ctx, coordinator, work.pl, work.runner)
	work.coordinator = coordinator
	return coordinator, nil
}

// resolvePlatforms is what every task of the sweep requires of the node that
// would run it, and where the sweep is allowed to place it: the package's own
// `buildPlatforms` and `runOnly`, read as its build would read them.
func (w *scriptWork) resolvePlatforms(covered []string) []execution.PackagePlatforms {
	reached := w.resolveReachedReleases(covered)
	wanted := make([]execution.PackagePlatforms, 0, len(reached))
	for _, rel := range reached {
		space := rel.Pkg.Space
		wanted = append(wanted, execution.PackagePlatforms{
			Package:   rel.Pkg.Name,
			Platforms: space.BuildPlatforms,
			Placement: execution.ResolveStagePlacement(execution.StageBuild,
				space.RunOnly.ResolveBuild(), len(space.LoginScript) > 0),
		})
	}
	return wanted
}

// reportSweepRefusal writes a sweep's preflight refusal where it is decided,
// with the code, the class and the work it is about already on it.
func (a *App) reportSweepRefusal(err error) error {
	a.logError(err).Msg("cannot dispatch this sweep")
	return err
}

// finishSweepDispatch prints the distributed summary of a sweep that delegated
// its tasks, and does nothing for one that delegated nothing.
func (a *App) finishSweepDispatch(_ context.Context, coordinator *execution.Coordinator,
	pl *plan.Plan, rep sweepReport, started time.Time) error {
	if coordinator == nil {
		return nil
	}
	execution.RunSummary{
		Run:         a.runID,
		Local:       a.sender.Node,
		Order:       pl.Order,
		Tasks:       coordinator.TaskRecords(),
		Blocked:     formatSkippedTasks(rep.SkippedBy),
		Wall:        time.Since(started),
		Invocations: coordinator.GitInvocations(),
	}.Summarize(a.log)
	return nil
}

// formatSkippedTasks are the packages the sweep's cascade skipped, each with
// the provider that blocked it, in name order: the summary puts them in the
// plan's order itself.
func formatSkippedTasks(skippedBy map[string]string) []execution.BlockedTask {
	names := make([]string, 0, len(skippedBy))
	for name := range skippedBy {
		names = append(names, name)
	}
	sort.Strings(names)
	blocked := make([]execution.BlockedTask, 0, len(names))
	for _, name := range names {
		blocked = append(blocked, execution.BlockedTask{Package: name, Reason: skippedBy[name]})
	}
	return blocked
}
