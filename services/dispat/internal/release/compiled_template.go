// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import "fmt"

const (
	invalidCompiledFrameTemplateErrorFormat   = "release: task %q has no static executable frame: %s"
	compiledTemplateMissingGraphReason        = "compiled graph is absent"
	compiledTemplateMissingTaskReason         = "task identity is absent"
	compiledTemplateMissingPreparedReason     = "prepared provider is absent"
	compiledTemplateMissingStagePlanReason    = "stage plan is absent"
	compiledTemplateMissingCommandFrameReason = "command frame is absent"
	compiledTemplateMissingPolicyReason       = "package policy is absent"
)

// CompileFrameTemplate returns a task's immutable command frame. Its
// effective source, admitted predecessor outputs and computed environment
// are filled from signed events at claim time.
func (c *CompiledTaskGraph) CompileFrameTemplate(taskID string) (StageRequest, error) {
	if c == nil || c.byID == nil {
		return StageRequest{}, fmt.Errorf(invalidCompiledFrameTemplateErrorFormat, taskID, compiledTemplateMissingGraphReason)
	}
	vertex, found := c.byID[taskID]
	if !found {
		return StageRequest{}, fmt.Errorf(invalidCompiledFrameTemplateErrorFormat, taskID, compiledTemplateMissingTaskReason)
	}
	var descriptor Stage
	if vertex.kind == taskPrepared {
		_, exists := c.prepared[vertex]
		if !exists {
			return StageRequest{}, fmt.Errorf(invalidCompiledFrameTemplateErrorFormat, taskID, compiledTemplateMissingPreparedReason)
		}
		descriptor = BuildStage()
	} else {
		stagePlan := c.plans[vertex.pkg]
		if stagePlan == nil {
			return StageRequest{}, fmt.Errorf(invalidCompiledFrameTemplateErrorFormat, taskID, compiledTemplateMissingStagePlanReason)
		}
		descriptor = stagePlan.stage(vertex)
	}
	if descriptor.frame == nil {
		return StageRequest{}, fmt.Errorf(invalidCompiledFrameTemplateErrorFormat, taskID, compiledTemplateMissingCommandFrameReason)
	}
	planned := c.framePlan.Releases[vertex.pkg]
	if planned == nil || planned.Pkg == nil || planned.Pkg.Space == nil {
		return StageRequest{}, fmt.Errorf(invalidCompiledFrameTemplateErrorFormat, taskID, compiledTemplateMissingPolicyReason)
	}
	frame := descriptor.frame(planned.Pkg.Space)
	return StageRequest{TaskID: taskID, Release: planned, Stage: descriptor.Name,
		Descriptor: descriptor, Frame: StageFrame{
			Before: frame.before, Commands: frame.commands, After: frame.after},
		StaticEnv: planned.Pkg.Space.Env, Dir: planned.Pkg.Dir}, nil
}
