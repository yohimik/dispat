// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// The seam one task's stage frame travels through when the run executes it
// somewhere else (CCME §28.3).
//
// The task graph is not changed by any of this, and that is the point. A
// distributed run schedules exactly the tasks a local one does, in exactly the
// same order, under exactly the same budgets: what differs is where the
// commands of one frame are executed, which is a question asked at the single
// call site below. With no Remotex on the Executor every path here returns the
// local frame, so a repository that never asked for distributed execution runs
// the code it always did.
//
// Two halves travel apart. The computed DISPAT_* pairs are public metadata of
// the run and are carried as they are, while the configuration's own static
// pairs travel unresolved: `$NPM_TOKEN` is expanded on the node that runs the
// command, from that node's environment, so a secret never enters a mailbox.

import (
	"context"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// Remotex runs a package's stage frames on another execution node.
//
// It is declared here, at the consumer, because this is the whole of what the
// executor asks of a distributed run: bracket the frames that stay at home,
// execute the ones that do not, and answer what came of them. An
// implementation owns the placement, the transport and the protocol; nothing
// about any of the three is visible from the executor, which is what keeps the
// scheduling code one description of the release rather than two.
type Remotex interface {
	// Guard brackets a frame the orchestrator keeps for itself: the version
	// and syncLock stages, whose whole job is editing the working tree the
	// dispatched builds are snapshotted from. The returned function gives the
	// guard back and is called on every path.
	//
	// It takes the stage name rather than the task, because what the guard is
	// for is the kind of write the frame makes, and a caller naming the stage
	// cannot pass an implementation something it would have to interpret.
	Guard(ctx context.Context, stage string) (func(), error)
	// Build runs one package's build frame on a worker node and answers what
	// that node reported. The outcome is filled on both paths: a frame that
	// failed still exported whatever ran before the failure, exactly as a
	// local frame does.
	Build(ctx context.Context, request StageRequest) (StageOutcome, error)
	// Publish runs one package's publish frame on a worker node, with
	// authorize called at the moment the irreversible command may start
	// (§28.6). Nothing calls it yet: publication stays on the orchestrator
	// until the gate that moves it, and the method is declared now so that the
	// seam a later gate fills is the seam this one was designed around.
	Publish(ctx context.Context, request StageRequest, authorize func(context.Context) error) (StageOutcome, error)
}

// StageRequest is one stage frame as another node has to receive it:
// everything the frame consists of, and everything it runs under.
//
// The release is carried whole rather than summarised because what identifies
// the work is the plan's own vocabulary: the package, its folder, its
// repository and its version are all read from it, and a summary would be a
// second description of the release that could disagree with the first.
type StageRequest struct {
	// Release is the package's planned release, which names the package, its
	// folder and the repository it belongs to.
	Release *plan.Release
	// Stage is the stage name the frame belongs to ("build"), which is what
	// the task is identified by and what the failing part is reported against.
	Stage string
	// Frame is the commands in execution order.
	Frame StageFrame
	// Env are the computed DISPAT_* pairs of the stage, carried as they are.
	Env []string
	// StaticEnv are the configuration's own pairs, unresolved: a value naming
	// a secret travels as that reference and is expanded on the executing node.
	StaticEnv []string
	// Dir is the folder the commands run in, as the local path would name it.
	Dir string
}

// StageFrame is one stage's commands in execution order: the hook that
// precedes it, the stage's own commands, and the hook that follows. It is the
// gating frame stageFrame runs locally, as data, so that the two paths cannot
// disagree about what a stage consists of.
type StageFrame struct {
	Before   []string
	Commands []string
	After    []string
}

// StageOutcome is what a node made of one stage frame.
//
// It is answered beside an error rather than instead of one, because the two
// say different things: the error is why the release fails, and the outcome is
// what the attempt produced on its way there. The exports of a failed frame
// are as real as those of a successful one and reach the outcome scripts the
// same way.
type StageOutcome struct {
	// Node is where the frame ran, for the log and the summary.
	Node string
	// Exports are what the frame's scripts wrote to their DISPAT_OUTPUT files.
	Exports []plan.Output
	// FailedPart names the part of the frame that failed, empty when none did.
	FailedPart string
}

// The parts of a stage frame, as an executing node names the one that failed.
// They are the frame's own vocabulary rather than the hook names, because a
// node runs a frame without knowing which stage's hooks bracket it; mapping
// them back onto the stage's labels is the orchestrator's, below.
const (
	PartBefore   = "before"
	PartCommands = "commands"
	PartAfter    = "after"
	// PartInputs and PartOutputs are the two parts of a frame that are not
	// commands at all: installing the verified outputs of the providers this
	// task consumes, and capturing the outputs this task declared. A node that
	// failed at either ran no command of the stage, which is a different thing
	// for an operator to read than a script that exited non-zero.
	PartInputs  = "inputs"
	PartOutputs = "outputs"
)

// runStage runs one task's gating frame wherever this run executes it.
//
// It is the one call site the distributed profile adds to the executor, and
// every branch of it answers the same (what, err) pair stageFrame answers, so
// the failure labels an operator reads are the same sentences whichever node
// produced them.
func (tc *taskCtx) runStage(ctx context.Context, s stage) (what string, err error) {
	if tc.Remote == nil {
		return tc.stageFrame(ctx, s)
	}
	switch tc.t.kind {
	case taskBuild:
		return tc.remoteStage(ctx, s)
	case taskVersion, taskSyncLock:
		return tc.guardedStage(ctx, s)
	default:
		// Publication and its hooks stay on the orchestrator, which owns the
		// locks, the records and the tags.
		return tc.stageFrame(ctx, s)
	}
}

// guardedStage runs a frame the orchestrator keeps, under the guard that keeps
// its writes out of a snapshot somebody is taking at the same moment.
func (tc *taskCtx) guardedStage(ctx context.Context, s stage) (string, error) {
	stage := tc.t.kind.String()
	releaseGuard, err := tc.Remote.Guard(ctx, stage)
	if err != nil {
		return stage + " could not be guarded", err
	}
	defer releaseGuard()
	tc.log.Trace().Str("stage", stage).Msg("stage guarded against a concurrent snapshot")
	return tc.stageFrame(ctx, s)
}

// remoteStage sends one frame to a node and folds what came back into the
// release.
//
// The exports are merged before the error is looked at, for the reason the
// local path merges them before it returns one: what a frame exported before
// it failed is what the outcome scripts and the summary have to see.
func (tc *taskCtx) remoteStage(ctx context.Context, s stage) (string, error) {
	outcome, err := tc.Remote.Build(ctx, StageRequest{
		Release:   tc.rel,
		Stage:     tc.t.kind.String(),
		Frame:     StageFrame{Before: s.before, Commands: s.commands, After: s.after},
		Env:       computedPackageEnv(tc.plan, tc.t.pkg, tc.wsVars, tc.updates, tc.t.kind.String()),
		StaticEnv: tc.rel.Pkg.Space.Env,
		Dir:       tc.rel.Pkg.Dir,
	})
	MergeOutputs(tc.rel, outcome.Exports)
	// Before the error is looked at, for the reason the exports are: a frame
	// that failed on a node is a failure that node is reported for, and the
	// event saying so is built from this.
	tc.recordPlacement(outcome.Node)
	if err == nil {
		tc.log.Debug().Str("worker", outcome.Node).Int("exports", len(outcome.Exports)).
			Msg(tc.t.kind.String() + ": the node reported the frame finished")
		return "", nil
	}
	return formatRemoteFailure(tc.t.kind, outcome.FailedPart), err
}

// formatRemoteFailure names the failing piece of a remote frame in the words
// the local path uses for the same piece, so that "beforeBuild hook failed"
// means one thing whichever machine ran it. A node that could not say which
// part failed, or that never got as far as running one, is reported as the
// stage it was given.
func formatRemoteFailure(kind taskKind, part string) string {
	switch part {
	case PartBefore:
		return "before" + stageTitle(kind) + " hook failed"
	case PartCommands:
		return kind.String() + " script failed"
	case PartAfter:
		return "post" + stageTitle(kind) + " hook failed"
	case PartInputs:
		return kind.String() + " inputs could not be installed"
	case PartOutputs:
		return kind.String() + " outputs could not be carried"
	default:
		return "remote " + kind.String() + " failed"
	}
}
