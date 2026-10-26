// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const missingCompiledStageTemplateErrorFormat = "execution: task %q has no immutable stage request: %s"

func (c *Coordinator) DispatchSources() func(string) []Source { return c.dispatch.Sources }
func (c *Coordinator) DispatchShell() func(string) []string   { return c.dispatch.Shell }

// CompileStageTemplate captures the node-neutral command fields before any
// predecessor result can change a task's effective input state.
func (c *Coordinator) CompileStageTemplate(spec ChoreographyTaskSpec,
	request release.StageRequest) (ChoreographyCompiledTemplate, error) {
	if c == nil {
		return ChoreographyCompiledTemplate{}, fmt.Errorf(missingCompiledStageTemplateErrorFormat,
			spec.Task, missingCompiledStageInputsReason)
	}
	return CompileStageTemplateWithInputs(spec, request, c.dispatch.Sources, c.dispatch.Shell,
		resolveTaskDeadlineSeconds(c.Timeouts.Task), c.Limits)
}

const missingCompiledStageInputsReason = "stage request or dispatch sources are absent"
const missingCompiledSweepInputsReason = "sweep kind or script is absent"

// CompileStageTemplateWithInputs works before network preflight from the
// pinned workspace callbacks. The same template is reused after preflight.
func CompileStageTemplateWithInputs(spec ChoreographyTaskSpec, request release.StageRequest,
	sourcesFor func(string) []Source, shellFor func(string) []string,
	deadlineSeconds int, limits TransferLimits) (ChoreographyCompiledTemplate, error) {
	if sourcesFor == nil || shellFor == nil ||
		spec.Task == "" || request.TaskID != spec.Task || request.Release == nil ||
		request.Release.Pkg == nil || request.Release.Pkg.Space == nil {
		return ChoreographyCompiledTemplate{}, fmt.Errorf(missingCompiledStageTemplateErrorFormat,
			spec.Task, missingCompiledStageInputsReason)
	}
	sources := sourcesFor(request.Release.Pkg.Name)
	dir, err := resolvePackageDir(sources, request)
	if err != nil {
		return ChoreographyCompiledTemplate{}, err
	}
	space := request.Release.Pkg.Space
	template := ChoreographyCompiledTemplate{
		Task: spec.Task, Kind: spec.Kind, Stage: spec.Stage, Weight: spec.Weight,
		Package: &AssignmentPackage{
			Name: request.Release.Pkg.Name, Version: request.Release.Next.String(),
			Repository: request.Release.Pkg.Repository, Dir: dir,
			OutputRoots: slices.Clone(space.BuildOutputs),
		},
		Frame: &AssignmentFrame{Before: slices.Clone(request.Frame.Before),
			Commands: slices.Clone(request.Frame.Commands), After: slices.Clone(request.Frame.After)},
		StaticEnv: slices.Clone(request.StaticEnv), Shell: slices.Clone(shellFor(request.Dir)),
		Platforms:       slices.Clone(space.BuildPlatforms),
		Outputs:         resolveDeclaredOutputs(request),
		DeadlineSeconds: deadlineSeconds, Limits: limits,
	}
	if _, err := DigestChoreographyCompiledTemplate(template); err != nil {
		return ChoreographyCompiledTemplate{}, err
	}
	return template, nil
}

func (c *Coordinator) CompileSweepTemplate(spec ChoreographyTaskSpec,
	request release.StageRequest, script string, roots []string) (ChoreographyCompiledTemplate, error) {
	if c == nil {
		return ChoreographyCompiledTemplate{}, fmt.Errorf(missingCompiledStageTemplateErrorFormat,
			spec.Task, missingCompiledStageInputsReason)
	}
	return CompileSweepTemplateWithInputs(spec, request, script, roots,
		c.dispatch.Sources, c.dispatch.Shell, resolveTaskDeadlineSeconds(c.Timeouts.Task), c.Limits)
}

func CompileSweepTemplateWithInputs(spec ChoreographyTaskSpec,
	request release.StageRequest, script string, roots []string,
	sources func(string) []Source, shell func(string) []string,
	deadlineSeconds int, limits TransferLimits) (ChoreographyCompiledTemplate, error) {
	if spec.Kind != KindRun || script == "" {
		return ChoreographyCompiledTemplate{}, fmt.Errorf(missingCompiledStageTemplateErrorFormat,
			spec.Task, missingCompiledSweepInputsReason)
	}
	template, err := CompileStageTemplateWithInputs(spec, request, sources, shell,
		deadlineSeconds, limits)
	if err != nil {
		return ChoreographyCompiledTemplate{}, err
	}
	template.Script = script
	template.Outputs = cleanSweepRoots(roots)
	template.Package.SourceDir = ""
	template.Package.OutputRoots = nil
	template.Frame.Before = nil
	template.Frame.After = nil
	return template, nil
}
