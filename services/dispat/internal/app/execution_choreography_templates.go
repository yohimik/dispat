package app

import (
	"fmt"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

const missingCompiledTaskTemplateErrorFormat = "execution: compiled task %q has no executable template: %w"

func compileReleaseChoreographyTemplates(coordinator *execution.Coordinator,
	compiled *release.CompiledTaskGraph, specs []execution.ChoreographyTaskSpec) ([]execution.ChoreographyCompiledTemplate, error) {
	if coordinator == nil || compiled == nil {
		return nil, fmt.Errorf(choreographyIncompleteReleaseTemplateCompilerErrorText)
	}
	return compileReleaseChoreographyTemplatesWithInputs(releaseChoreographyTemplateOptions{
		Compiled:    compiled,
		Tasks:       specs,
		Sources:     coordinator.DispatchSources(),
		Shell:       coordinator.DispatchShell(),
		TaskTimeout: coordinator.Timeouts.Task,
		Limits:      coordinator.Limits,
	})
}

type releaseChoreographyTemplateOptions struct {
	Compiled    *release.CompiledTaskGraph
	Tasks       []execution.ChoreographyTaskSpec
	Sources     func(string) []execution.Source
	Shell       func(string) []string
	TaskTimeout time.Duration
	Limits      execution.TransferLimits
}

func compileReleaseChoreographyTemplatesWithInputs(options releaseChoreographyTemplateOptions) ([]execution.ChoreographyCompiledTemplate, error) {
	compiled, specs, sources, shell, taskTimeout, limits := options.Compiled, options.Tasks, options.Sources, options.Shell, options.TaskTimeout, options.Limits

	if compiled == nil {
		return nil, fmt.Errorf(choreographyMissingReleaseTemplateGraphErrorText)
	}
	templates := make([]execution.ChoreographyCompiledTemplate, choreographyNoEntriesCount, len(specs))
	firstOnly := make(map[string]bool)
	for _, shape := range compiled.TaskShapes() {
		firstOnly[shape.ID] = shape.FirstOnly
	}
	for index := range specs {
		spec := &specs[index]
		if spec.Virtual {
			continue
		}
		var template execution.ChoreographyCompiledTemplate
		var request release.StageRequest
		var requestErr error
		if spec.HybridSegment != execution.HybridInterpretedSegmentName {
			request, requestErr = compiled.CompileFrameTemplate(spec.Task)
		}
		if spec.HybridSegment == execution.HybridInterpretedSegmentName ||
			requestErr != nil && (spec.LocalAuthority || firstOnly[spec.Task]) {
			template = execution.ChoreographyCompiledTemplate{
				Task: spec.Task, Kind: spec.Kind, Stage: spec.Stage, Weight: spec.Weight, Limits: limits}
		} else {
			if requestErr != nil {
				return nil, fmt.Errorf(missingCompiledTaskTemplateErrorFormat, spec.Task, requestErr)
			}
			var err error
			template, err = execution.CompileStageTemplateWithInputs(execution.ChoreographyStageTemplateOptions{
				Spec:            *spec,
				Request:         request,
				SourcesFor:      sources,
				ShellFor:        shell,
				DeadlineSeconds: int(taskTimeout.Seconds()),
				Limits:          limits,
			})
			if err != nil {
				return nil, fmt.Errorf(missingCompiledTaskTemplateErrorFormat, spec.Task, err)
			}
		}
		digest, err := execution.DigestChoreographyCompiledTemplate(template)
		if err != nil {
			return nil, err
		}
		spec.TemplateDigest = digest
		templates = append(templates, template)
	}
	return templates, nil
}

func compileSweepChoreographyTemplates(coordinator *execution.Coordinator,
	work *scriptWork, specs []execution.ChoreographyTaskSpec) ([]execution.ChoreographyCompiledTemplate, error) {
	if coordinator == nil {
		return nil, fmt.Errorf(choreographyIncompleteSweepTemplateCompilerErrorText)
	}
	return compileSweepChoreographyTemplatesWithInputs(sweepChoreographyTemplateOptions{
		Work:        work,
		Tasks:       specs,
		Sources:     coordinator.DispatchSources(),
		Shell:       coordinator.DispatchShell(),
		TaskTimeout: coordinator.Timeouts.Task,
		Limits:      coordinator.Limits,
	})
}

type sweepChoreographyTemplateOptions struct {
	Work        *scriptWork
	Tasks       []execution.ChoreographyTaskSpec
	Sources     func(string) []execution.Source
	Shell       func(string) []string
	TaskTimeout time.Duration
	Limits      execution.TransferLimits
}

func compileSweepChoreographyTemplatesWithInputs(options sweepChoreographyTemplateOptions) ([]execution.ChoreographyCompiledTemplate, error) {
	work, specs, sources, shell, taskTimeout, limits := options.Work, options.Tasks, options.Sources, options.Shell, options.TaskTimeout, options.Limits

	if work == nil || work.pl == nil {
		return nil, fmt.Errorf(choreographyIncompleteSweepTemplateCompilerErrorText)
	}
	templates := make([]execution.ChoreographyCompiledTemplate, choreographyNoEntriesCount, len(specs))
	for index := range specs {
		spec := &specs[index]
		if spec.Virtual {
			continue
		}
		rel := work.pl.Releases[spec.Package]
		if rel == nil || rel.Pkg == nil || rel.Pkg.Space == nil {
			return nil, fmt.Errorf(choreographyMissingSweepTaskPolicyErrorFormat, spec.Task)
		}
		commands, found := rel.Pkg.Space.Script(work.name)
		if !found {
			return nil, fmt.Errorf(choreographyMissingSweepTaskScriptErrorFormat, spec.Task)
		}
		request := release.StageRequest{TaskID: spec.Task, Release: rel,
			Stage: work.stage(), Dir: rel.Pkg.Dir, StaticEnv: rel.Pkg.Space.Env,
			Frame: release.StageFrame{Commands: script.AppendArgsToLast(commands, work.args)}}
		template, err := execution.CompileSweepTemplateWithInputs(execution.ChoreographySweepTemplateOptions{
			Spec:            *spec,
			Request:         request,
			Script:          work.name,
			Roots:           work.roots,
			Sources:         sources,
			Shell:           shell,
			DeadlineSeconds: int(taskTimeout.Seconds()),
			Limits:          limits,
		})
		if err != nil {
			return nil, err
		}
		digest, err := execution.DigestChoreographyCompiledTemplate(template)
		if err != nil {
			return nil, err
		}
		spec.TemplateDigest = digest
		templates = append(templates, template)
	}
	return templates, nil
}
