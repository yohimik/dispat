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
	"errors"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

const nativeFrameInvalidationFailure = "native input invalidation failed"

// Remotex runs a package's stage frames on another execution node.
//
// It is declared here, at the consumer, because this is the whole of what the
// executor asks of a distributed run: bracket the frames that stay at home,
// execute the ones that do not, and answer what came of them. An
// implementation owns the placement, the transport and the protocol; nothing
// about any of the three is visible from the executor, which is what keeps the
// scheduling code one description of the release rather than two.
type Remotex interface {
	// Guard brackets a frame the orchestrator keeps for itself: the sign,
	// version and syncLock stages, whose whole job is editing the working tree
	// the dispatched builds are snapshotted from. It also prepares the named
	// package's declared output roots before its first preparing frame. The
	// returned function gives the guard back and is called on every path.
	//
	// It takes the stage name rather than the task, because what the guard is
	// for is the kind of write the frame makes, and a caller naming the stage
	// cannot pass an implementation something it would have to interpret.
	Guard(ctx context.Context, stage, packageName string) (func(), error)
	// Stage runs one package's frame of a stage the run may place elsewhere,
	// wherever this run places it, and answers what came of it. The request's
	// descriptor is the whole of what the implementation needs to know about
	// the stage: which runOnly value places it, what a node must be given
	// before it runs, and whether its outputs are admitted. Publication stays
	// on the orchestrator; authorize is retained in the interface for callers
	// that still provide it and is not a worker permission. The outcome is
	// filled on both paths:
	// a frame that failed still exported whatever ran before the failure,
	// exactly as a local frame does.
	//
	// A frame the run places on this machine is run by here, the executor's
	// own gating sequence, so a publication this run keeps is the one a
	// release without any of this performs.
	Stage(ctx context.Context, request StageRequest, here LocalFrame,
		authorize func(context.Context) error) (StageOutcome, error)
}

// NativeInputWritex fences new compiled claims while a first-node frame may
// rewrite prepared source bytes and while its relevant-state check settles.
type NativeInputWritex interface {
	BeginNativeInputWrite(context.Context) (func(), error)
}

// StageReservationRequest fixes the graph task before its package admission
// or any checkout write. A distributed placement waits for capacity here,
// before the executor counts the package task as in flight.
type StageReservationRequest struct {
	TaskID     string
	Release    *plan.Release
	Descriptor Stage
}

// StageReservationx owns one exact placement until its frame either starts or
// is refused. Close releases an unoffered placement; a landed or ambiguous
// offer remains fenced until its durable decision.
type StageReservationx interface {
	Stage(context.Context, StageRequest, LocalFrame,
		func(context.Context) error) (StageOutcome, error)
	Close() error
}

// StageReserverx is optional because a local run needs no remote capacity
// before package admission. A v3 coordinator uses it for placed stages.
type StageReserverx interface {
	ReserveStage(context.Context, StageReservationRequest) (StageReservationx, error)
}

// LocalFrame is one stage frame as this process runs it.
//
// It answers the pair the executor answers everywhere else: the sentence
// naming the part that failed, and the failure. Handing the placement the
// sequence rather than the commands is what keeps a frame that runs here
// indistinguishable from one that ran here before any of this existed, hooks,
// labels, exports and all.
type LocalFrame func(ctx context.Context) (string, error)

type localStageRuntimeKey struct{}

// LocalStageRuntime is one local frame's private execution environment.
type LocalStageRuntime struct {
	Dir         string
	Runner      script.Runnerx
	Env         []string
	OutputPaths []OutputPathMapping
}

// OutputPathMapping translates paths beneath declared output roots between
// the authoritative source checkout and one task's materialized checkout.
type OutputPathMapping struct {
	SourceRoot string
	TaskRoot   string
	Roots      []string
}

// WithLocalStageRuntime runs a local producer in a private checkout using
// the original package's shell and without an inherited workspace context.
func WithLocalStageRuntime(ctx context.Context, runtime LocalStageRuntime) context.Context {
	return context.WithValue(ctx, localStageRuntimeKey{}, runtime)
}

// ResolveLocalStageExecution applies a private checkout's directory, runner,
// inherited environment, and declared-output path mapping to one local frame.
func ResolveLocalStageExecution(ctx context.Context, fallbackDir string, fallbackRunner script.Runnerx,
	fallbackEnv []string) (string, script.Runnerx, []string, []OutputPathMapping) {
	if runtime, ok := ctx.Value(localStageRuntimeKey{}).(LocalStageRuntime); ok && runtime.Dir != "" {
		env := RemapOutputEnvironment(fallbackEnv, runtime.OutputPaths)
		return runtime.Dir, runtime.Runner, append(env, runtime.Env...), runtime.OutputPaths
	}
	return fallbackDir, fallbackRunner, fallbackEnv, nil
}

// StageRequest is one stage frame as another node has to receive it:
// everything the frame consists of, and everything it runs under.
//
// The release is carried whole rather than summarised because what identifies
// the work is the plan's own vocabulary: the package, its folder, its
// repository and its version are all read from it, and a summary would be a
// second description of the release that could disagree with the first.
type StageRequest struct {
	// TaskID is the compiled graph vertex identity used by the v3 task ref.
	TaskID string
	// Release is the package's planned release, which names the package, its
	// folder and the repository it belongs to.
	Release *plan.Release
	// Stage is the stage name the frame belongs to ("build", "test",
	// "publish"), which is what the task is identified by and what the failing
	// part is reported against.
	Stage string
	// Descriptor is the stage the frame belongs to, which says where it may
	// be placed, what it reads and what it produces.
	Descriptor Stage
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
	// Node is the WORKER the frame ran on, and is empty for a frame this run
	// placed on the orchestrator: the orchestrator is the writer of every
	// line and every event of the run, so naming it here would be naming the
	// writer as though it were somebody else.
	Node string
	// Exports are what the frame's scripts wrote to their DISPAT_OUTPUT files.
	Exports []plan.Output
	// FailedPart names the part of the frame that failed, empty when none did.
	FailedPart string
	// LocalFailure is the executor's own sentence for a frame that failed
	// here, empty for one that travelled. A failure on this machine is
	// described by the code that ran it, so an operator reads the same words
	// a local release has always printed; a failure somewhere else is
	// described by the part the node reported.
	LocalFailure string
	// NoClaim is set only when the placement proves that this task never
	// offered or accepted a claim. A failed or ambiguous offer leaves it nil.
	NoClaim *NoClaimFinality
	// UnresolvedOffer means a signed offer may still have an accepted claim
	// without a verified Result, Ack, or no-claim fence. The executor must not
	// settle package failure or run outcome hooks while that effect is unknown.
	UnresolvedOffer bool
}

// NoClaimFinality carries a blocker derived from the canonical task and
// shared outcome policy, not from an error string.
type NoClaimFinality struct {
	Blocker string
	// SignedTerminal says the canonical task ref was already terminalized by
	// the admission deadline. Executor now owes only package settlement.
	SignedTerminal bool
}

// DelegatedFailureEvidence binds a no-claim local failure without carrying
// potentially sensitive error text into a signed transport document.
type DelegatedFailureEvidence struct {
	Part        string
	ErrorSHA256 string
}

const (
	DelegatedFailurePreludePart = "prelude"
	DelegatedFailureInputsPart  = PartInputs
	DelegatedFailureFramePart   = "frame"

	noClaimWithoutFailedFrameErrorFormat = "release: stage reported no claim without a failed frame"
	noClaimWithoutBlockerErrorFormat     = "release: failed no-claim stage named no blocker"
)

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
	// PartAuthorization is the step a publication waits at between its hook
	// and its own command: the revalidation the run makes before it authorizes
	// the effect (§27.2, §28.6). A frame reported against it ran no publish
	// command at all, which is what an operator has to read first.
	PartAuthorization = "authorization"
	// PartDeadline is the frame the executing node ended itself, at the
	// deadline its assignment stated. It is the node's answer rather than the
	// orchestrator's: a run whose network went away cannot end anything, so
	// the bound has to be enforced where the work is, and a frame reported
	// against it stopped because of the clock and not because of the package.
	PartDeadline = "deadline"
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
	if tc.stage.Placement == PlacementOrchestratorOnly {
		// The sign and version stages and the lock-file preparation write the
		// working tree every dispatched task is snapshotted from, so they stay
		// here and they stay out of each other's way: every reconciliation of
		// a manifest is an orchestrator task (§28.3).
		return tc.guardedStage(ctx, s)
	}
	return tc.placedStage(ctx, s)
}

// guardedStage runs a frame the orchestrator keeps, under the guard that keeps
// its writes out of a snapshot somebody is taking at the same moment.
func (tc *taskCtx) guardedStage(ctx context.Context, s stage) (string, error) {
	stage := tc.stage.Name
	if writer, ok := tc.Remote.(NativeInputWritex); ok {
		releaseNative, err := writer.BeginNativeInputWrite(ctx)
		if err != nil {
			return nativeFrameInvalidationFailure, err
		}
		defer releaseNative()
	}
	releaseGuard, err := tc.Remote.Guard(ctx, stage, tc.t.pkg)
	if err != nil {
		return stage + " could not be guarded", err
	}
	defer releaseGuard()
	tc.log.Trace().Str("stage", stage).Msg("stage guarded against a concurrent snapshot")
	what, frameErr := tc.stageFrame(ctx, s)
	if tc.AfterNativeFrame != nil {
		if invalidationErr := tc.AfterNativeFrame(ctx, stage, tc.t.pkg); invalidationErr != nil {
			if frameErr != nil {
				return what, errors.Join(frameErr, invalidationErr)
			}
			return nativeFrameInvalidationFailure, invalidationErr
		}
	}
	return what, frameErr
}

// placedStage runs one frame wherever this run places it and folds what came
// back into the release.
//
// The exports are merged before the error is looked at, for the reason the
// local path merges them before it returns one: what a frame exported before
// it failed is what the outcome scripts and the summary have to see. A stage
// whose commands wait for authorization hands the placement the executor's
// own revalidation, so whichever machine runs the commands, the check that
// decides whether they may start is made here, on the node that owns the
// locks and the records (§27.2, §28.6).
func (tc *taskCtx) placedStage(ctx context.Context, s stage) (string, error) {
	var authorize func(context.Context) error
	if tc.stage.IsAuthorized {
		authorize = tc.revalidatePublication
	}
	request := tc.stageRequest(s)
	var outcome StageOutcome
	var err error
	if tc.stageReservation != nil {
		outcome, err = tc.stageReservation.Stage(ctx, request, tc.localFrame(s), authorize)
	} else {
		outcome, err = tc.Remote.Stage(ctx, request, tc.localFrame(s), authorize)
	}
	return tc.foldPlacement(outcome, err)
}

// revalidatePublication is the executor's own pre-publish check, as a callback
// a placement can make at the moment it needs it. A run that composed none
// answers that the publication may proceed, which is what the local path does
// with an unset callback.
func (tc *taskCtx) revalidatePublication(ctx context.Context) error {
	if tc.BeforePublish == nil {
		return nil
	}
	return tc.BeforePublish(ctx, tc.rel)
}

// stageRequest is one task's frame as another node has to receive it. The two
// halves of the environment travel apart: what dispat computed is carried as
// it is, and the configuration's own pairs travel unresolved so that a value
// naming a secret expands on the node that runs the command.
func (tc *taskCtx) stageRequest(s stage) StageRequest {
	request := renderStageRequest(tc.rel, tc.stage, s, tc.wsVars, tc.updates)
	request.TaskID = taskGraphID(tc.t)
	return request
}

func renderStageRequest(rel *plan.Release, descriptor Stage, s stage,
	wsVars []string, updates []providerUpdate) StageRequest {
	return StageRequest{
		Release:    rel,
		Stage:      descriptor.Name,
		Descriptor: descriptor,
		Frame:      StageFrame{Before: s.before, Commands: s.commands, After: s.after},
		Env:        computedReleaseEnv(rel, wsVars, updates, descriptor.Name),
		StaticEnv:  rel.Pkg.Space.Env,
		Dir:        rel.Pkg.Dir,
	}
}

// localFrame is this task's frame as this process runs it: the executor's own
// gating sequence, handed to the placement so that a frame the run keeps is
// indistinguishable from one it kept before any of this existed.
func (tc *taskCtx) localFrame(s stage) LocalFrame {
	return func(ctx context.Context) (string, error) { return tc.stageFrame(ctx, s) }
}

// foldPlacement folds what a placement made of one frame back into the
// release: the exports it produced, the node it named, and the sentence the
// failure is reported with.
func (tc *taskCtx) foldPlacement(outcome StageOutcome, err error) (string, error) {
	if outcome.UnresolvedOffer {
		tc.unresolvedEffect = true
	}
	if err != nil {
		tc.placementFailedPart = outcome.FailedPart
	}
	if outcome.NoClaim != nil {
		if err == nil {
			return "", errors.New(noClaimWithoutFailedFrameErrorFormat)
		}
		if outcome.NoClaim.Blocker == "" {
			return "", errors.New(noClaimWithoutBlockerErrorFormat)
		}
		copy := *outcome.NoClaim
		tc.noClaim = &copy
	}
	MergeOutputs(tc.rel, outcome.Exports)
	// Before the error is looked at, for the reason the exports are: a frame
	// that failed on a node is a failure that node is reported for, and the
	// event saying so is built from this. A frame that ran here names nobody,
	// which is what keeps the writer of the line and the node it is about the
	// same thing only when they really are.
	if outcome.Node != "" {
		tc.recordPlacement(outcome.Node)
	}
	if err == nil {
		event := tc.log.Debug().Int("exports", len(outcome.Exports))
		if outcome.Node != "" {
			event = event.Str("worker", outcome.Node)
		}
		event.Msg(tc.stage.Name + ": the frame finished where it was placed")
		return "", nil
	}
	if outcome.LocalFailure != "" {
		return outcome.LocalFailure, err
	}
	return formatRemoteFailure(tc.stage, outcome.FailedPart), err
}

// formatRemoteFailure names the failing piece of a remote frame in the words
// the local path uses for the same piece, so that "beforeBuild hook failed"
// means one thing whichever machine ran it. A node that could not say which
// part failed, or that never got as far as running one, is reported as the
// stage it was given.
func formatRemoteFailure(label Stage, part string) string {
	switch part {
	case PartBefore:
		return "before" + label.Title + " hook failed"
	case PartCommands:
		return label.Name + " script failed"
	case PartAfter:
		return "post" + label.Title + " hook failed"
	case PartInputs:
		return label.Name + " inputs could not be installed"
	case PartOutputs:
		return label.Name + " outputs could not be carried"
	case PartAuthorization:
		// The same sentence the local path prints when the callback between
		// the hook and the command refuses: whichever machine asked, what
		// failed is the check that decides whether the publication happens.
		return "pre-publish repository validation failed"
	default:
		return "remote " + label.Name + " failed"
	}
}
