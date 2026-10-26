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
		return nil, fmt.Errorf("execution: release template compiler has no coordinator or graph")
	}
	return compileReleaseChoreographyTemplatesWithInputs(compiled, specs,
		coordinator.DispatchSources(), coordinator.DispatchShell(),
		coordinator.Timeouts.Task, coordinator.Limits)
}

func compileReleaseChoreographyTemplatesWithInputs(compiled *release.CompiledTaskGraph,
	specs []execution.ChoreographyTaskSpec, sources func(string) []execution.Source,
	shell func(string) []string, taskTimeout time.Duration,
	limits execution.TransferLimits) ([]execution.ChoreographyCompiledTemplate, error) {
	if compiled == nil {
		return nil, fmt.Errorf("execution: release template compiler has no graph")
	}
	templates := make([]execution.ChoreographyCompiledTemplate, 0, len(specs))
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
		request, requestErr := compiled.CompileFrameTemplate(spec.Task)
		if requestErr != nil && (spec.LocalAuthority || firstOnly[spec.Task]) {
			template = execution.ChoreographyCompiledTemplate{
				Task: spec.Task, Kind: spec.Kind, Weight: spec.Weight, Limits: limits}
		} else {
			if requestErr != nil {
				return nil, fmt.Errorf(missingCompiledTaskTemplateErrorFormat, spec.Task, requestErr)
			}
			var err error
			template, err = execution.CompileStageTemplateWithInputs(*spec, request,
				sources, shell, int(taskTimeout.Seconds()), limits)
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
		return nil, fmt.Errorf("execution: sweep template compiler has no coordinator or plan")
	}
	return compileSweepChoreographyTemplatesWithInputs(work, specs,
		coordinator.DispatchSources(), coordinator.DispatchShell(),
		coordinator.Timeouts.Task, coordinator.Limits)
}

func compileSweepChoreographyTemplatesWithInputs(work *scriptWork,
	specs []execution.ChoreographyTaskSpec, sources func(string) []execution.Source,
	shell func(string) []string, taskTimeout time.Duration,
	limits execution.TransferLimits) ([]execution.ChoreographyCompiledTemplate, error) {
	if work == nil || work.pl == nil {
		return nil, fmt.Errorf("execution: sweep template compiler has no coordinator or plan")
	}
	templates := make([]execution.ChoreographyCompiledTemplate, 0, len(specs))
	for index := range specs {
		spec := &specs[index]
		if spec.Virtual {
			continue
		}
		rel := work.pl.Releases[spec.Package]
		if rel == nil || rel.Pkg == nil || rel.Pkg.Space == nil {
			return nil, fmt.Errorf("execution: sweep task %q has no package policy", spec.Task)
		}
		commands, found := rel.Pkg.Space.Script(work.name)
		if !found {
			return nil, fmt.Errorf("execution: sweep task %q has no declared script", spec.Task)
		}
		request := release.StageRequest{TaskID: spec.Task, Release: rel,
			Stage: work.stage(), Dir: rel.Pkg.Dir, StaticEnv: rel.Pkg.Space.Env,
			Frame: release.StageFrame{Commands: script.AppendArgsToLast(commands, work.args)}}
		template, err := execution.CompileSweepTemplateWithInputs(*spec, request,
			work.name, work.roots, sources, shell, int(taskTimeout.Seconds()), limits)
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
