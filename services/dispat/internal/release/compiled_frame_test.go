// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestCompiledFrameUsesTheExecutorRequestKernel(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Outputs = []plan.Output{{Name: "TOKEN", Value: "earlier", Source: "a:version"}}
	remote := &fakeRemote{}
	executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	executor.Remote = remote
	compiled, err := executor.CompileTaskGraph(p)
	require.NoError(t, err)
	var build task
	for _, candidate := range compiled.byID {
		if candidate.pkg == "a" && candidate.kind == taskBuild {
			build = candidate
		}
	}
	require.Equal(t, taskBuild, build.kind)
	predecessors := make(map[string][]plan.Output)
	for _, before := range compiled.plans["a"].predecessors(build) {
		predecessors[taskGraphID(before)] = nil
	}
	request, err := compiled.PrepareTaskFrame(taskGraphID(build), TaskFrameState{
		WorkspaceVars: WorkspaceEnv(p, zerolog.Nop()), PredecessorOutputs: predecessors})
	require.NoError(t, err)
	executor.Run(context.Background(), p)
	require.Len(t, remote.requests(), 1)
	actual := remote.requests()[0]
	require.Equal(t, actual.Frame, request.Frame)
	require.Equal(t, actual.Env, request.Env)
	require.Equal(t, actual.StaticEnv, request.StaticEnv)
	require.Equal(t, actual.Stage, request.Stage)
	require.Equal(t, taskGraphID(build), actual.TaskID)
	require.Equal(t, actual.TaskID, request.TaskID)
	require.Equal(t, actual.Release.Outputs, request.Release.Outputs)

	for predecessor := range predecessors {
		delete(predecessors, predecessor)
		_, err := compiled.PrepareTaskFrame(taskGraphID(build), TaskFrameState{
			WorkspaceVars: WorkspaceEnv(p, zerolog.Nop()), PredecessorOutputs: predecessors})
		require.ErrorContains(t, err, "lacks settled predecessor")
		break
	}
}

func TestCompiledLocalVersionFrameBindsScriptAndSignedPredecessor(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"core", "app"},
		Deps: map[string][]string{"app": {"core"}}})
	p.Releases["app"].Pkg.Space.BeforeAllScript = []string{"prepare-native"}
	p.Releases["app"].Pkg.Space.VersionScript = []string{"write-version"}
	p.Releases["app"].Pkg.Space.StageGraph = graphOf(t,
		spec("version"), spec("build", after("version")), spec("publish", after("build")))
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	version := taskGraphID(task{pkg: "app", kind: taskVersion})
	predecessors := make(map[string][]plan.Output)
	for _, before := range compiled.plans["app"].predecessors(task{pkg: "app", kind: taskVersion}) {
		predecessors[taskGraphID(before)] = nil
	}
	request, err := compiled.PrepareLocalTaskFrame(version, TaskFrameState{
		WorkspaceVars: []string{"WORKSPACE_PIN=held"}, PredecessorOutputs: predecessors,
		ProviderOutcomes: map[string]PackageOutcome{"core": OutcomePublished}})
	require.NoError(t, err)
	require.Equal(t, version, request.TaskID)
	require.Contains(t, request.Frame.Commands, "write-version")
	require.Contains(t, request.Env, "WORKSPACE_PIN=held")
	_, err = compiled.PrepareTaskFrame(version, TaskFrameState{PredecessorOutputs: predecessors})
	require.ErrorContains(t, err, "cannot be prepared for peer execution")
	for predecessor := range predecessors {
		delete(predecessors, predecessor)
		_, err = compiled.PrepareLocalTaskFrame(version,
			TaskFrameState{PredecessorOutputs: predecessors})
		require.ErrorContains(t, err, "lacks settled predecessor")
		break
	}
}

func TestCompiledLocalPublishTicketRetainsConfiguredCommandAndOutputInput(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.PublishScript = []string{"publish-command"}
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	publish := taskGraphID(task{pkg: "a", kind: taskPublish})
	build := taskGraphID(task{pkg: "a", kind: taskBuild})
	request, err := compiled.PrepareLocalTaskFrame(publish, TaskFrameState{
		PredecessorOutputs: map[string][]plan.Output{build: {{Name: "TOKEN", Value: "signed", Source: build}}}})
	require.NoError(t, err)
	require.Equal(t, []string{"publish-command"}, request.Frame.Commands)
	require.Equal(t, publish, request.TaskID)
	require.Contains(t, request.Release.Outputs,
		plan.Output{Name: "TOKEN", Value: "signed", Source: build})
	_, err = compiled.PrepareTaskFrame(publish, TaskFrameState{
		PredecessorOutputs: map[string][]plan.Output{build: nil}})
	require.ErrorContains(t, err, "cannot be prepared for peer execution")
}

func TestCompiledFrameFiltersOnlySettledDeadProviderUpdates(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"core", "app"}, Deps: map[string][]string{"app": {"core"}}})
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	var build task
	for _, candidate := range compiled.byID {
		if candidate.pkg == "app" && candidate.kind == taskBuild {
			build = candidate
		}
	}
	predecessors := make(map[string][]plan.Output)
	for _, before := range compiled.plans["app"].predecessors(build) {
		predecessors[taskGraphID(before)] = nil
	}
	state := TaskFrameState{PredecessorOutputs: predecessors,
		ProviderOutcomes: map[string]PackageOutcome{"core": OutcomePublished}}
	request, err := compiled.PrepareTaskFrame(taskGraphID(build), state)
	require.NoError(t, err)
	require.Contains(t, request.Env, "DISPAT_UPDATED_PACKAGES=CORE")
	state.ProviderOutcomes["core"] = OutcomeFailed
	request, err = compiled.PrepareTaskFrame(taskGraphID(build), state)
	require.NoError(t, err)
	require.Contains(t, request.Env, "DISPAT_UPDATED_PACKAGES=")
	state.ProviderOutcomes["core"] = OutcomeCancelled
	_, err = compiled.PrepareTaskFrame(taskGraphID(build), state)
	require.ErrorContains(t, err, "cancelled provider")
}

func TestCompiledFrameMergesOnlyDirectPredecessorExportsInStageOrder(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.UserStages = []model.UserStage{{Name: "lint", Commands: []string{"lint"}},
		{Name: "merge", Commands: []string{"merge"}}}
	space.StageGraph = graphOf(t, spec("version"), spec("build", after("version")),
		spec("lint", after("version")), spec("merge", after("build", "lint")),
		spec("publish", after("merge")))
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	taskID := taskGraphID(task{pkg: "a", kind: taskUser, name: "merge"})
	predecessors := map[string][]plan.Output{
		taskGraphID(task{pkg: "a", kind: taskBuild}): {
			{Name: "TOKEN", Value: "build", Source: "a:build"}},
		taskGraphID(task{pkg: "a", kind: taskUser, name: "lint"}): {
			{Name: "TOKEN", Value: "lint", Source: "a:lint"}},
	}
	request, err := compiled.PrepareTaskFrame(taskID, TaskFrameState{PredecessorOutputs: predecessors})
	require.NoError(t, err)
	require.Contains(t, request.Env, "DISPAT_OUTPUT_TOKEN=lint")
	require.Contains(t, request.Env, "DISPAT_OUTPUT_SOURCE_TOKEN=a:lint")
	require.Equal(t, []string{"merge"}, request.Frame.Commands)
}

func TestCompiledFrameCarriesAncestorExportsThroughOneDirectPredecessor(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.UserStages = []model.UserStage{{Name: "generate", Commands: []string{"generate"}},
		{Name: "transform", Commands: []string{"transform"}}}
	space.StageGraph = graphOf(t, spec("version"), spec("generate", after("version")),
		spec("transform", after("generate")), spec("build", after("transform")),
		spec("publish", after("build")))
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	generate := task{pkg: "a", kind: taskUser, name: "generate"}
	transform := task{pkg: "a", kind: taskUser, name: "transform"}
	build := task{pkg: "a", kind: taskBuild}
	first := map[string][]plan.Output{}
	for _, before := range compiled.plans["a"].predecessors(generate) {
		first[taskGraphID(before)] = nil
	}
	generated, err := compiled.AccumulateTaskOutputs(taskGraphID(generate), first,
		[]plan.Output{{Name: "IMAGE", Value: "image", Source: "a:generate"}})
	require.NoError(t, err)
	transformed, err := compiled.AccumulateTaskOutputs(taskGraphID(transform),
		map[string][]plan.Output{taskGraphID(generate): generated},
		[]plan.Output{{Name: "TOKEN", Value: "token", Source: "a:transform"}})
	require.NoError(t, err)
	request, err := compiled.PrepareTaskFrame(taskGraphID(build), TaskFrameState{
		PredecessorOutputs: map[string][]plan.Output{taskGraphID(transform): transformed}})
	require.NoError(t, err)
	require.Contains(t, request.Env, "DISPAT_OUTPUT_IMAGE=image")
	require.Contains(t, request.Env, "DISPAT_OUTPUT_TOKEN=token")
}
