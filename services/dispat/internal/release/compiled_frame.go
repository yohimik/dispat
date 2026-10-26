// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

const (
	compiledFrameMissingGraphError                           = "release: no compiled task graph"
	compiledFrameUnknownTaskErrorFormat                      = "release: unknown executable task %q"
	compiledFramePeerRefusedErrorFormat                      = "release: task %q cannot be prepared for peer execution"
	compiledFrameMissingFrameErrorFormat                     = "release: task %q has no stage frame"
	compiledFrameCancelledProviderErrorFormat                = "release: task %q has a cancelled provider %q"
	compiledFrameUnknownOutcomeErrorFormat                   = "release: task %q has invalid provider outcome %q"
	compiledFrameUnsettledProviderOutcome     PackageOutcome = ""
)

type taskFrameOwnership uint8

const (
	peerTaskFrame taskFrameOwnership = iota
	localTaskFrame
)

// TaskFrameState is the verified dynamic state a delegable frame reads when
// it becomes ready. PredecessorOutputs contains each direct same-package
// predecessor's cumulative visible outputs, not just that stage's own new
// exports. An entry is required even when it exported nothing. ProviderOutcomes
// is the signed package settlement view; absent providers are still pending.
type TaskFrameState struct {
	WorkspaceVars      []string
	ProviderOutcomes   map[string]PackageOutcome
	PredecessorOutputs map[string][]plan.Output
}

// PrepareTaskFrame renders one independently compiled, delegable task with
// the same request kernel as the compiled executor. It never prepares a
// native, publication, record or observing frame for peer execution.
func (c *CompiledTaskGraph) PrepareTaskFrame(taskID string, state TaskFrameState) (StageRequest, error) {
	return c.prepareTaskFrame(taskID, state, peerTaskFrame)
}

// PrepareLocalTaskFrame renders a first-node owned command with the same
// signed predecessor state as a peer frame. The Executor retains authority
// to run it; this request only binds exact inputs into its pre-effect ticket.
func (c *CompiledTaskGraph) PrepareLocalTaskFrame(taskID string, state TaskFrameState) (StageRequest, error) {
	return c.prepareTaskFrame(taskID, state, localTaskFrame)
}

func (c *CompiledTaskGraph) prepareTaskFrame(taskID string, state TaskFrameState,
	ownership taskFrameOwnership) (StageRequest, error) {
	if c == nil {
		return StageRequest{}, fmt.Errorf(compiledFrameMissingGraphError)
	}
	t, found := c.byID[taskID]
	if !found || c.plans[t.pkg] == nil {
		return StageRequest{}, fmt.Errorf(compiledFrameUnknownTaskErrorFormat, taskID)
	}
	sp := c.plans[t.pkg]
	descriptor := sp.stage(t)
	if ownership == peerTaskFrame &&
		(descriptor.Placement != PlacementByBuild || descriptor.IsObserving ||
			t.kind == taskVersion || t.kind == taskSign || t.kind == taskSyncLock ||
			t.kind == taskPublish || t.kind == taskRecord || t.kind == taskBeforeAll) {
		return StageRequest{}, fmt.Errorf(compiledFramePeerRefusedErrorFormat, taskID)
	}
	if descriptor.frame == nil {
		return StageRequest{}, fmt.Errorf(compiledFrameMissingFrameErrorFormat, taskID)
	}
	visible, err := c.visibleTaskRelease(t, state.PredecessorOutputs)
	if err != nil {
		return StageRequest{}, err
	}
	results := make(map[string]*Result, len(visible.Updates))
	for _, update := range visible.Updates {
		outcome := state.ProviderOutcomes[update.Name]
		switch outcome {
		case compiledFrameUnsettledProviderOutcome, OutcomePending, OutcomePublished:
		case OutcomeFailed:
			results[update.Name] = &Result{Status: StatusFailed}
		case OutcomeSkipped:
			results[update.Name] = &Result{Status: StatusSkipped}
		case OutcomeRecordBlocked:
			results[update.Name] = &Result{RecordBlocked: compiledFrameProviderRecordBlockedState}
		case OutcomeCancelled:
			return StageRequest{}, fmt.Errorf(compiledFrameCancelledProviderErrorFormat, taskID, update.Name)
		default:
			return StageRequest{}, fmt.Errorf(compiledFrameUnknownOutcomeErrorFormat, taskID, outcome)
		}
	}
	updates := liveProviderUpdates(t.pkg, c.framePlan, results)
	request := renderStageRequest(stageRequestRenderOptions{Release: visible, Descriptor: descriptor,
		Frame: descriptor.frame(visible.Pkg.Space), WorkspaceVars: state.WorkspaceVars, Updates: updates})
	request.TaskID = taskID
	return request, nil
}

// AccumulateTaskOutputs is the same visible-release merge the Executor keeps
// in run.exports[t] at task finality. A peer applies it once per signed task
// result so a successor receives exports from all of its stage ancestors.
func (c *CompiledTaskGraph) AccumulateTaskOutputs(taskID string,
	predecessorOutputs map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
	if c == nil {
		return nil, fmt.Errorf(compiledFrameMissingGraphError)
	}
	t, found := c.byID[taskID]
	if !found || c.plans[t.pkg] == nil {
		return nil, fmt.Errorf(compiledFrameUnknownTaskErrorFormat, taskID)
	}
	visible, err := c.visibleTaskRelease(t, predecessorOutputs)
	if err != nil {
		return nil, err
	}
	MergeOutputs(visible, own)
	return visible.Outputs, nil
}

func (c *CompiledTaskGraph) visibleTaskRelease(t task,
	predecessorOutputs map[string][]plan.Output) (*plan.Release, error) {
	visible := *c.framePlan.Releases[t.pkg]
	visible.Outputs = slices.Clone(c.baselineOutputs[t.pkg])
	for _, before := range c.plans[t.pkg].predecessors(t) {
		outputs, ok := predecessorOutputs[taskGraphID(before)]
		if !ok {
			return nil, fmt.Errorf(compiledFrameUnsettledPredecessorErrorFormat,
				taskGraphID(t), taskGraphID(before))
		}
		MergeOutputs(&visible, outputs)
	}
	return &visible, nil
}
