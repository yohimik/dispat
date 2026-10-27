// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

const interpretedRunMismatchError = "execution: independently interpreted run differs from signed graph identity or task policy"
const interpretedRunMissingCallbackError = "execution: interpreted run has no independent pinned-input planner"
const interpretedRunAbsentIdentity = ""
const interpretedReadyFrameMismatchError = "execution: independently interpreted ready frame differs from signed task inputs or executable template"
const interpretedReadyFrameMissingError = "execution: interpreted claim was accepted before its signed prepared frame and independent ready frame"
const interpretedReadyFrameCauseFormat = "%s: %w"
const interpretedRunNoPredecessorEvents = 0
const interpretedRunNoPackagePolicies = 0

// InterpretedRun is derived independently from the authenticated pinned
// bootstrap. Task order and graph identity must match the shared compiler.
// No first-node assignment is used to choose a task in this mode.
type InterpretedRun struct {
	Binding             ChoreographyRunBinding
	SemanticGraphDigest string
	Tasks               []ChoreographyTaskSpec
	OutcomePolicy       ChoreographyOutcomePolicyx
	// Predecessors are exact signed terminal events in the shared graph's
	// direct predecessor order; virtual gates have no signed event. The app
	// verifies admitted Results before deriving a frame or exported value.
	PrepareReady func(context.Context, InterpretedReadyInput) (InterpretedReadyFrame, error)
	Close        func() error
}

// InterpretedReadyFrame is computed from the private pinned plan and verified
// predecessor results before a claim. The accepted signed Assignment must
// match these executable bytes and effective input bindings before effects.
type InterpretedReadyFrame struct {
	Assignment Assignment
	Bindings   ChoreographyTaskBindings
}

type InterpretedReadyInput struct {
	Task                  ChoreographyReadyTask
	Predecessors          []ChoreographyEvent
	InputsDigest          string
	PredecessorResultOIDs []string
	PreparedInputOID      string
}

// PrepareInterpretedRun materializes the signed bootstrap in private source
// checkouts, recomputes plan and graph, and returns the exact ready frame.
// Eligibility uses the signed post-preflight roster from the retained ledger
// root, never ambient node config. The object-store path comes from the
// verified mailbox, not a moving URL.
type InterpretedRunOptions struct {
	Bootstrap      RunBootstrap
	BootstrapOID   string
	GraphRootOID   string
	Roster         ChoreographyRuntimeRoster
	ObjectStoreDir string
	Carrier        ChoreographyGraphCarrier
}

type PrepareInterpretedRun func(context.Context, InterpretedRunOptions) (*InterpretedRun, error)

func interpretedPredecessors(frontier *ChoreographyFrontier, selected ChoreographyReadyTask) []ChoreographyEvent {
	events := make([]ChoreographyEvent, interpretedRunNoPredecessorEvents, len(selected.Spec.Predecessors))
	for _, taskID := range selected.Spec.Predecessors {
		predecessor := frontier.tasks[taskID]
		if predecessor != nil && !predecessor.spec.Virtual {
			events = append(events, predecessor.terminal)
		}
	}
	return events
}

type interpretedReadyFrameOptions struct {
	run          *workerCompiledRun
	selected     ChoreographyReadyTask
	inputsDigest string
	identity     ChoreographyTaskBindings
	frame        InterpretedReadyFrame
}

func prepareInterpretedReadyFrame(ctx context.Context,
	options interpretedReadyFrameOptions) (*InterpretedReadyFrame, error) {
	if options.run.interpreted == nil {
		return nil, nil
	}
	// The first identity-only proposal causes the broker to sign FrameReady
	// and refuse Claim. No effect or attempt begins before the prepared input
	// carrier exists; the next selection can independently derive its frame.
	if options.selected.Spec.NeedsPreparedInput && options.identity.PreparedInputOID == interpretedRunAbsentIdentity {
		return nil, nil
	}
	prepared, err := options.run.interpreted.PrepareReady(ctx, InterpretedReadyInput{
		Task: options.selected, Predecessors: interpretedPredecessors(options.run.frontier, options.selected),
		InputsDigest: options.inputsDigest, PredecessorResultOIDs: options.identity.PredecessorResultOIDs,
		PreparedInputOID: options.identity.PreparedInputOID})
	if err != nil {
		return nil, err
	}
	options.frame = prepared
	if err := verifyInterpretedReadyFrame(options); err != nil {
		return nil, err
	}
	return &prepared, nil
}

func verifyInterpretedReadyFrame(options interpretedReadyFrameOptions) error {
	run, selected, inputsDigest, identity, frame := options.run, options.selected,
		options.inputsDigest, options.identity, options.frame
	if !slices.Equal(frame.Bindings.PredecessorResultOIDs, identity.PredecessorResultOIDs) ||
		frame.Bindings.PreparedInputOID != identity.PreparedInputOID ||
		frame.Assignment.Protocol != ChoreographyProtocolVersion ||
		frame.Assignment.Run != run.binding.Run ||
		frame.Assignment.Generation != run.binding.Generation ||
		frame.Assignment.PlanDigest != run.binding.PlanDigest ||
		frame.Assignment.Task != selected.Spec.Task || frame.Assignment.Kind != selected.Spec.Kind ||
		frame.Assignment.Attempt != selected.Attempt ||
		frame.Assignment.Node != run.member.Name ||
		frame.Assignment.Branch != selected.Spec.Branch ||
		frame.Assignment.Weight != selected.Spec.Weight ||
		frame.Assignment.TemplateDigest != selected.Spec.TemplateDigest ||
		frame.Assignment.BootstrapOID != run.bootstrapOID ||
		frame.Assignment.GraphDigest != run.binding.GraphDigest ||
		frame.Assignment.GraphRootOID != run.rootOID ||
		frame.Assignment.InputsDigest != inputsDigest {
		return fmt.Errorf(interpretedReadyFrameMismatchError)
	}
	if err := VerifyChoreographyAssignmentTemplate(run.templates[selected.Spec.Task], frame.Assignment); err != nil {
		return fmt.Errorf(interpretedReadyFrameCauseFormat, interpretedReadyFrameMismatchError, err)
	}
	digest, err := DigestChoreographyTaskBindings(frame.Bindings)
	if err != nil {
		return err
	}
	if frame.Assignment.BindingsDigest != digest ||
		!reflect.DeepEqual(frame.Assignment.Repositories, frame.Bindings.Repositories) ||
		!reflect.DeepEqual(frame.Assignment.Inputs, frame.Bindings.Inputs) ||
		!reflect.DeepEqual(frame.Assignment.Exports, frame.Bindings.Exports) ||
		!reflect.DeepEqual(frame.Assignment.Env, frame.Bindings.Env) {
		return fmt.Errorf(interpretedReadyFrameMismatchError)
	}
	return nil
}

func interpretedTaskPolicy(spec ChoreographyTaskSpec) ChoreographyTaskSpec {
	spec.Branch = interpretedRunAbsentIdentity
	spec.ReservationOID = interpretedRunAbsentIdentity
	spec.TemplateDigest = interpretedRunAbsentIdentity
	spec.EligibleNodes = nil
	return spec
}

func verifyInterpretedRun(bootstrap RunBootstrap, carrier ChoreographyGraphCarrier,
	interpreted *InterpretedRun) error {
	if interpreted == nil || interpreted.PrepareReady == nil || interpreted.Close == nil ||
		interpreted.Binding != bootstrap.ChoreographyRunBinding ||
		interpreted.SemanticGraphDigest != carrier.SemanticGraphDigest ||
		len(interpreted.Tasks) != len(carrier.Tasks) {
		return fmt.Errorf(interpretedRunMismatchError)
	}
	byTask := make(map[string]ChoreographyTaskSpec, len(interpreted.Tasks))
	for _, task := range interpreted.Tasks {
		if task.Task == interpretedRunAbsentIdentity || byTask[task.Task].Task != interpretedRunAbsentIdentity {
			return fmt.Errorf(interpretedRunMismatchError)
		}
		byTask[task.Task] = interpretedTaskPolicy(task)
	}
	for _, signed := range carrier.Tasks {
		derived, found := byTask[signed.Task]
		if !found || !reflect.DeepEqual(derived, interpretedTaskPolicy(signed)) {
			return fmt.Errorf(interpretedRunMismatchError)
		}
	}
	if len(carrier.PackagePolicies) != interpretedRunNoPackagePolicies && interpreted.OutcomePolicy == nil {
		return fmt.Errorf(interpretedRunMismatchError)
	}
	return nil
}
