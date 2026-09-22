// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The orchestrator's side of a command sweep executed on worker nodes (CCME
// §28.10).
//
// A sweep is `dispat run`: one declared script, run in every package the
// selection covers, providers first. It is not a release. It records nothing,
// publishes nothing and holds no release lock, so its tasks are the simplest
// work this profile places: one package's commands for the swept script,
// with no hook around them, no provider output installed before them, and a
// placement decided by the package's own `runOnly` exactly as its build's is.

import (
	"context"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// SweepTask is one package's task of a command sweep, as the run hands it to
// its coordinator.
type SweepTask struct {
	// Request is the task as a node receives it: the release it belongs to,
	// the swept script's commands as the frame's commands and nothing before
	// or after them, the computed environment, the unresolved static pairs
	// and the package folder.
	Request release.StageRequest
	// Script is the declared script the sweep runs, which the task's commands
	// read as DISPAT_STAGE `run:<script>`.
	Script string
	// Here is the task as this process runs it, in this checkout, answering
	// what its commands exported.
	Here func(ctx context.Context) ([]plan.Output, error)
}

// Sweep places one package's sweep task and runs it where it was placed.
//
// The placement is the build stage's, asked of the package's own `runOnly`
// and `buildPlatforms`: a sweep is the work a pool exists for, so `both` is
// offered to the workers first and to this machine when none has room, and a
// package that may only run here or only elsewhere is held to that.
func (c *Coordinator) Sweep(ctx context.Context, sweep SweepTask) (release.StageOutcome, error) {
	task := formatSweepTaskName(sweep.Request.Release.Pkg.Name)
	space := sweep.Request.Release.Pkg.Space
	placement := ResolveStagePlacement(StageBuild, space.RunOnly.ResolveBuild(), len(space.LoginScript) > 0)
	return c.placeTask(ctx, task, placement, space.BuildPlatforms, "",
		func(ctx context.Context, lease *Lease, attempt int) (release.StageOutcome, error) {
			if lease.IsLocal {
				return c.sweepHere(ctx, lease, task, sweep)
			}
			return c.dispatchSweep(ctx, lease, task, attempt, sweep)
		})
}

// formatSweepTaskName names one package's sweep task: the package under the
// sweep's own kind, so that a log, a branch name and the summary all say what
// kind of work they are looking at.
func formatSweepTaskName(packageName string) string { return packageName + ":" + KindRun }

// sweepHere runs one sweep task in this checkout, as a sweep with no workers
// runs every one of them.
func (c *Coordinator) sweepHere(ctx context.Context, lease *Lease, task string,
	sweep SweepTask) (release.StageOutcome, error) {
	defer lease.Release()
	c.Log.Info().Str("run", c.Run).Str("task", task).
		Msg("task placed on the node that started the run")
	exports, err := sweep.Here(ctx)
	if err != nil {
		return release.StageOutcome{Exports: exports, LocalFailure: "run script failed"}, err
	}
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("status", StatusSucceeded).
		Int("exports", len(exports)).Msg("task finished")
	return release.StageOutcome{Exports: exports}, nil
}

// dispatchSweep offers one sweep task to the node the pool chose and waits
// for that node to report.
//
// It is a build's dispatch with its inputs left out: the package's input
// closure is captured and offered exactly as a build's is, and nothing any
// other task produced is installed before the commands, because a sweep
// installs nothing (§28.10).
func (c *Coordinator) dispatchSweep(ctx context.Context, lease *Lease, task string, attempt int,
	sweep SweepTask) (release.StageOutcome, error) {
	request := sweep.Request
	outcome := release.StageOutcome{Node: lease.Node}
	sources := c.dispatch.Sources(request.Release.Pkg.Name)
	dir, err := resolvePackageDir(sources, request)
	if err != nil {
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, attempt, err)
	}
	commits, err := c.captureInputs(ctx, sources)
	if err != nil {
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, attempt, err)
	}
	repositories, err := c.offerInputs(ctx, lease.Node, sources, commits)
	if err != nil {
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, attempt, err)
	}
	offer, err := c.offerAssignment(ctx, lease, task,
		c.formatSweepAssignment(lease.Node, task, attempt, dir, repositories, sweep))
	if err != nil {
		return outcome, err
	}
	defer offer.observer.forget(offer.branch)
	return c.awaitResult(ctx, lease, task, attempt, KindRun, outcome, offer,
		func(ctx context.Context, outcome release.StageOutcome, reply taskReply, branch string) (release.StageOutcome, error) {
			return c.readSweepOutcome(task, attempt, outcome, reply)
		})
}

// formatSweepAssignment is the document one sweep task travels as: a build's
// assignment with the swept script's commands as its whole frame, the script
// named, and no inputs.
func (c *Coordinator) formatSweepAssignment(node, task string, attempt int, dir string,
	repositories []AssignmentRepository, sweep SweepTask) *Assignment {
	request := sweep.Request
	branch := FormatBranch(node, KindRun, time.Now())
	return &Assignment{
		Header:       c.formatOrchestratorHeader(KindRun, task, attempt, node, branch),
		Repositories: repositories,
		Package: &AssignmentPackage{
			Name:       request.Release.Pkg.Name,
			Version:    request.Release.Next.String(),
			Repository: request.Release.Pkg.Repository,
			Dir:        dir,
		},
		Frame:           &AssignmentFrame{Commands: request.Frame.Commands},
		Script:          sweep.Script,
		Env:             request.Env,
		StaticEnv:       request.StaticEnv,
		Exports:         formatExports(request.Release.Outputs),
		Shell:           c.dispatch.Shell(request.Dir),
		Platforms:       request.Release.Pkg.Space.BuildPlatforms,
		DeadlineSeconds: resolveTaskDeadlineSeconds(c.Timeouts.Task),
		Limits:          c.Limits,
	}
}

// readSweepOutcome turns one accepted sweep result into what the task comes
// to: the exports it produced and the failure it reports.
func (c *Coordinator) readSweepOutcome(task string, attempt int,
	outcome release.StageOutcome, reply taskReply) (release.StageOutcome, error) {
	result := reply.result
	outcome, err := c.readReportedOutcome(task, attempt, outcome, result)
	if err != nil {
		return outcome, err
	}
	c.reportTaskFinished(task, result, outcome)
	return outcome, nil
}
