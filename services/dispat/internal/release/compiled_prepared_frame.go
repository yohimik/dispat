// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

const (
	invalidPreparedFrameStateErrorFormat = "release: prepared task %q has no signed predecessor state"
	preparedFrameInitialPredecessorCount = 0
)

// PreparePreparedTaskFrame fills an unreleasing provider's pinned build
// template from signed predecessor outputs. It never reads the executor's
// mutable result table or reenters preparation while a graph slot is held.
func (c *CompiledTaskGraph) PreparePreparedTaskFrame(taskID string,
	state TaskFrameState) (StageRequest, error) {
	if c == nil {
		return StageRequest{}, fmt.Errorf(invalidPreparedFrameStateErrorFormat, taskID)
	}
	vertex, found := c.byID[taskID]
	if !found || vertex.kind != taskPrepared {
		return StageRequest{}, fmt.Errorf(invalidPreparedFrameStateErrorFormat, taskID)
	}
	template, err := c.CompileFrameTemplate(taskID)
	if err != nil {
		return StageRequest{}, err
	}
	if template.Release == nil {
		return StageRequest{}, fmt.Errorf(invalidPreparedFrameStateErrorFormat, taskID)
	}
	visible := *template.Release
	visible.Outputs = slices.Clone(c.baselineOutputs[vertex.pkg])
	parents := make([]string, preparedFrameInitialPredecessorCount, len(state.PredecessorOutputs))
	for task := range state.PredecessorOutputs {
		parents = append(parents, task)
	}
	slices.Sort(parents)
	for _, parent := range parents {
		MergeOutputs(&visible, state.PredecessorOutputs[parent])
	}
	template.Release = &visible
	template.Env = computedReleaseEnv(&visible, state.WorkspaceVars, nil, template.Stage)
	return template, nil
}

// AccumulatePreparedTaskOutputs keeps the provider's signed output values
// alongside the pinned baseline for a releasing consumer's effective frame.
func (c *CompiledTaskGraph) AccumulatePreparedTaskOutputs(taskID string,
	predecessors map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
	state := TaskFrameState{PredecessorOutputs: predecessors}
	request, err := c.PreparePreparedTaskFrame(taskID, state)
	if err != nil {
		return nil, err
	}
	visible := *request.Release
	MergeOutputs(&visible, own)
	return visible.Outputs, nil
}

// ComputedSweepFrameEnv renders a sweep's inherited signed outputs while
// retaining the run command's provider-version policy from its pinned plan.
func ComputedSweepFrameEnv(pl *plan.Plan, visible *plan.Release, pkg, stage string,
	workspaceVars []string) []string {
	return computedReleaseEnv(visible, workspaceVars, liveProviderUpdates(pkg, pl, nil), stage)
}
