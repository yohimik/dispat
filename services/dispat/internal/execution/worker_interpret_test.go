// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	interpretedTestRun                    = "interpreted-run"
	interpretedTestGeneration             = "generation"
	interpretedTestPlan                   = "plan"
	interpretedTestGraph                  = "graph"
	interpretedTestRoot                   = "root"
	interpretedTestBootstrap              = "bootstrap"
	interpretedTestTask                   = "core:build"
	interpretedTestBranch                 = "dispat-task-interpreted-core"
	interpretedTestNode                   = "worker-a"
	interpretedTestInputs                 = "inputs"
	interpretedTestSource                 = "source"
	interpretedTestSnapshot               = "snapshot"
	interpretedTestCommand                = "go build ./..."
	interpretedTestChanged                = "go test ./..."
	interpretedTestWeight                 = 1
	interpretedTestAttempt                = 1
	interpretedTestPolicyRefusalMessage   = "a private retry policy cannot be replaced by the signed carrier policy"
	interpretedTestSignedPreparedOID      = "signed-prepared"
	interpretedTestCallbackFailure        = "independent ready callback reached"
	interpretedTestInitialProposalMessage = "the initial proposal only asks the broker to sign FrameReady"
)

func TestInterpretedPreparationHandshakeWaitsForSignedFrameReady(t *testing.T) {
	callbackError := errors.New(interpretedTestCallbackFailure)
	calls := interpretedRunNoPredecessorEvents
	run := &workerCompiledRun{interpreted: &InterpretedRun{
		PrepareReady: func(context.Context, InterpretedReadyInput) (InterpretedReadyFrame, error) {
			calls++
			return InterpretedReadyFrame{}, callbackError
		},
	}}
	selected := ChoreographyReadyTask{Spec: ChoreographyTaskSpec{
		Task: interpretedTestTask, NeedsPreparedInput: true}}
	identity := ChoreographyTaskBindings{}
	options := interpretedReadyFrameOptions{run: run, selected: selected,
		inputsDigest: interpretedTestInputs, identity: identity}
	frame, err := prepareInterpretedReadyFrame(t.Context(), options)
	require.NoError(t, err)
	require.Nil(t, frame)
	require.Equal(t, interpretedRunNoPredecessorEvents, calls,
		interpretedTestInitialProposalMessage)
	identity.PreparedInputOID = interpretedTestSignedPreparedOID
	options.identity = identity
	_, err = prepareInterpretedReadyFrame(t.Context(), options)
	require.ErrorIs(t, err, callbackError)
	require.Equal(t, interpretedTestAttempt, calls)
}

func TestInterpretedReadyFrameRequiresIndependentExactBindingsAndExecutable(t *testing.T) {
	template := ChoreographyCompiledTemplate{Task: interpretedTestTask,
		Kind: KindBuild, Weight: interpretedTestWeight,
		Frame: &AssignmentFrame{Commands: []string{interpretedTestCommand}}}
	templateDigest, err := DigestChoreographyCompiledTemplate(template)
	require.NoError(t, err)
	bindings := ChoreographyTaskBindings{Repositories: []AssignmentRepository{{
		Name: interpretedTestSource, Snapshot: interpretedTestSnapshot}}}
	assignment, err := BindChoreographyCompiledTemplate(ChoreographyTemplateBindingOptions{
		Template: template, Bindings: bindings,
		Header: Header{Protocol: ChoreographyProtocolVersion, Kind: KindBuild,
			Run: interpretedTestRun, Generation: interpretedTestGeneration,
			PlanDigest: interpretedTestPlan, Task: interpretedTestTask,
			Attempt: interpretedTestAttempt, Node: interpretedTestNode,
			Branch: interpretedTestBranch},
		BootstrapOID: interpretedTestBootstrap, GraphDigest: interpretedTestGraph})
	require.NoError(t, err)
	assignment.GraphRootOID, assignment.InputsDigest = interpretedTestRoot, interpretedTestInputs
	selected := ChoreographyReadyTask{Spec: ChoreographyTaskSpec{Task: interpretedTestTask,
		Kind: KindBuild, Branch: interpretedTestBranch, Weight: interpretedTestWeight,
		TemplateDigest: templateDigest}, Attempt: interpretedTestAttempt}
	run := &workerCompiledRun{binding: ChoreographyRunBinding{Run: interpretedTestRun,
		Generation: interpretedTestGeneration, PlanDigest: interpretedTestPlan,
		GraphDigest: interpretedTestGraph}, rootOID: interpretedTestRoot,
		bootstrapOID: interpretedTestBootstrap,
		member:       ChoreographyRuntimeNode{Name: interpretedTestNode},
		templates:    map[string]ChoreographyCompiledTemplate{interpretedTestTask: template}}
	frame := InterpretedReadyFrame{Assignment: assignment, Bindings: bindings}
	options := interpretedReadyFrameOptions{run: run, selected: selected,
		inputsDigest: interpretedTestInputs, frame: frame}
	require.NoError(t, verifyInterpretedReadyFrame(options))
	changed := frame
	changed.Assignment.Frame = &AssignmentFrame{Commands: []string{interpretedTestChanged}}
	options.frame = changed
	require.Error(t, verifyInterpretedReadyFrame(options))
	changed = frame
	changed.Bindings.Repositories = []AssignmentRepository{{
		Name: interpretedTestSource, Snapshot: interpretedTestChanged}}
	options.frame = changed
	require.Error(t, verifyInterpretedReadyFrame(options))
}

func TestInterpretedRunRejectsChangedPrivateTaskPolicy(t *testing.T) {
	binding := ChoreographyRunBinding{Run: interpretedTestRun,
		Generation: interpretedTestGeneration, PlanDigest: interpretedTestPlan,
		GraphDigest: interpretedTestGraph}
	signed := ChoreographyTaskSpec{Task: interpretedTestTask, Kind: KindBuild,
		Weight: interpretedTestWeight, Branch: interpretedTestBranch,
		MaxAttempts: interpretedTestAttempt}
	carrier := ChoreographyGraphCarrier{SemanticGraphDigest: interpretedTestGraph,
		Tasks: []ChoreographyTaskSpec{signed}}
	private := signed
	private.Branch = interpretedRunAbsentIdentity
	private.EligibleNodes = nil
	interpreted := &InterpretedRun{Binding: binding,
		SemanticGraphDigest: interpretedTestGraph, Tasks: []ChoreographyTaskSpec{private},
		PrepareReady: func(context.Context, InterpretedReadyInput) (InterpretedReadyFrame, error) {
			return InterpretedReadyFrame{}, nil
		}, Close: func() error { return nil }}
	bootstrap := RunBootstrap{ChoreographyRunBinding: binding}
	require.NoError(t, verifyInterpretedRun(bootstrap, carrier, interpreted))
	interpreted.Tasks[0].MaxAttempts++
	require.Error(t, verifyInterpretedRun(bootstrap, carrier, interpreted),
		interpretedTestPolicyRefusalMessage)
}
