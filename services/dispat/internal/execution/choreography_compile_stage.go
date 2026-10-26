// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const missingCompiledStageTemplateErrorFormat = "execution: task %q has no immutable stage request: %s"
const missingCompiledTemplateIdentity = ""

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
	return CompileStageTemplateWithInputs(ChoreographyStageTemplateOptions{
		Spec:            spec,
		Request:         request,
		SourcesFor:      c.dispatch.Sources,
		ShellFor:        c.dispatch.Shell,
		DeadlineSeconds: resolveTaskDeadlineSeconds(c.Timeouts.Task),
		Limits:          c.Limits,
	})
}

const missingCompiledStageInputsReason = "stage request or dispatch sources are absent"
const missingCompiledSweepInputsReason = "sweep kind or script is absent"

type ChoreographyStageTemplateOptions struct {
	Spec            ChoreographyTaskSpec
	Request         release.StageRequest
	SourcesFor      func(string) []Source
	ShellFor        func(string) []string
	DeadlineSeconds int
	Limits          TransferLimits
}

// CompileStageTemplateWithInputs works before network preflight from the
// pinned workspace callbacks. The same template is reused after preflight.
func CompileStageTemplateWithInputs(options ChoreographyStageTemplateOptions) (ChoreographyCompiledTemplate, error) {
	spec, request, sourcesFor, shellFor, deadlineSeconds, limits := options.Spec, options.Request, options.SourcesFor, options.ShellFor, options.DeadlineSeconds, options.Limits

	if sourcesFor == nil || shellFor == nil ||
		spec.Task == missingCompiledTemplateIdentity || request.TaskID != spec.Task || request.Release == nil ||
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

type ChoreographySweepFrameOptions struct {
	Spec    ChoreographyTaskSpec
	Request release.StageRequest
	Script  string
	Roots   []string
}

func (c *Coordinator) CompileSweepTemplate(options ChoreographySweepFrameOptions) (ChoreographyCompiledTemplate, error) {
	spec, request, script, roots := options.Spec, options.Request, options.Script, options.Roots

	if c == nil {
		return ChoreographyCompiledTemplate{}, fmt.Errorf(missingCompiledStageTemplateErrorFormat,
			spec.Task, missingCompiledStageInputsReason)
	}
	return CompileSweepTemplateWithInputs(ChoreographySweepTemplateOptions{
		Spec:            spec,
		Request:         request,
		Script:          script,
		Roots:           roots,
		Sources:         c.dispatch.Sources,
		Shell:           c.dispatch.Shell,
		DeadlineSeconds: resolveTaskDeadlineSeconds(c.Timeouts.Task),
		Limits:          c.Limits,
	})
}

type ChoreographySweepTemplateOptions struct {
	Spec            ChoreographyTaskSpec
	Request         release.StageRequest
	Script          string
	Roots           []string
	Sources         func(string) []Source
	Shell           func(string) []string
	DeadlineSeconds int
	Limits          TransferLimits
}

func CompileSweepTemplateWithInputs(options ChoreographySweepTemplateOptions) (ChoreographyCompiledTemplate, error) {
	spec, request, script, roots, sources, shell, deadlineSeconds, limits := options.Spec, options.Request, options.Script, options.Roots, options.Sources, options.Shell, options.DeadlineSeconds, options.Limits

	if spec.Kind != KindRun || script == missingCompiledTemplateIdentity {
		return ChoreographyCompiledTemplate{}, fmt.Errorf(missingCompiledStageTemplateErrorFormat,
			spec.Task, missingCompiledSweepInputsReason)
	}
	template, err := CompileStageTemplateWithInputs(ChoreographyStageTemplateOptions{
		Spec:            spec,
		Request:         request,
		SourcesFor:      sources,
		ShellFor:        shell,
		DeadlineSeconds: deadlineSeconds,
		Limits:          limits,
	})
	if err != nil {
		return ChoreographyCompiledTemplate{}, err
	}
	template.Script = script
	template.Outputs = cleanSweepRoots(roots)
	template.Package.SourceDir = missingCompiledTemplateIdentity
	template.Package.OutputRoots = nil
	template.Frame.Before = nil
	template.Frame.After = nil
	return template, nil
}
