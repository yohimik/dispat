// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const interpretedRetryPolicyMismatchError = "execution: signed interpretation retry policy differs from pinned configuration"

func (a *App) prepareInterpretedRelease(ctx context.Context, options execution.InterpretedRunOptions) (_ *interpretedPlan, err error) {
	planning := interpretedPlanningOptions(options)
	planning.Log, planning.MaxPackBytes = a.log, formatTransferLimits(a.cfg.Execution).MaxPackBytes
	private, err := recomputePeerReleasePlan(ctx, planning)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, private.close())
		}
	}()
	settings, policy := private.app.cfg.Execution, options.Bootstrap.JoinPolicy
	if settings.ResolveMaxAttempts() != policy.MaxAttempts ||
		settings.RetryAfterUncertainAttempt != policy.RetryAfterUncertainAttempt {
		return nil, fmt.Errorf(interpretedRetryPolicyMismatchError)
	}
	specs, err := private.interpretedReleaseSpecs(options.Roster)
	if err != nil {
		return nil, err
	}
	applyChoreographyRetrySafety(specs, policy.RetryAfterUncertainAttempt)
	applyChoreographyAttemptLimit(specs, policy)
	templates, err := compileReleaseChoreographyTemplatesWithInputs(releaseChoreographyTemplateOptions{
		Compiled: private.compiled, Tasks: specs, Sources: private.app.resolveInputSources(private.plan),
		Shell: private.app.resolveShell, TaskTimeout: time.Duration(settings.ResolveTimeouts().Task) * time.Second,
		Limits: formatTransferLimits(settings)})
	if err != nil {
		return nil, err
	}
	result := &interpretedPlan{App: private.app, Plan: private.plan, Tasks: specs, Templates: templates,
		Packages:            releaseChoreographyPackagePolicies(private.compiled),
		SemanticGraphDigest: private.compiled.Digest(), OutcomePolicy: private.compiled.NewOutcomeEvaluator(),
		Contexts:        make(map[string]execution.ChoreographyBindingOutputContext, len(specs)),
		OutcomePackages: make(map[string][]string, len(specs)), InputPackages: make(map[string][]string, len(specs)),
		Close: private.close}
	prepared := make(map[string]*plan.Release)
	for _, item := range private.compiled.PreparedTaskSpecs() {
		prepared[item.TaskID] = item.Release
	}
	byTask := make(map[string]execution.ChoreographyTaskSpec, len(specs))
	providers := private.app.resolveOutputProviders(private.plan)
	for _, spec := range specs {
		byTask[spec.Task] = spec
		rel := private.plan.Releases[spec.Package]
		if rel == nil {
			rel = prepared[spec.Task]
		}
		if rel == nil || rel.Pkg == nil || rel.Pkg.Space == nil {
			if spec.Virtual {
				continue
			}
			return nil, fmt.Errorf(missingReleaseBindingStateErrorFormat, spec.Task)
		}
		result.Contexts[spec.Task] = execution.ChoreographyBindingOutputContext{
			Repository: rel.Pkg.Repository, Package: rel.Pkg.Name,
			SourceRoot: rel.Pkg.Dir, Roots: slices.Clone(rel.Pkg.Space.BuildOutputs)}
		if planned := private.plan.Releases[spec.Package]; planned != nil {
			for _, update := range planned.Updates {
				result.OutcomePackages[spec.Task] = append(result.OutcomePackages[spec.Task], update.Name)
			}
		}
		for _, provider := range providers(spec.Package) {
			result.InputPackages[spec.Task] = append(result.InputPackages[spec.Task], provider.Package)
		}
	}
	result.Accumulate = func(taskID string, predecessors map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
		if byTask[taskID].Kind == execution.KindPrepare {
			return private.compiled.AccumulatePreparedTaskOutputs(taskID, predecessors, own)
		}
		return private.compiled.AccumulateTaskOutputs(taskID, predecessors, own)
	}
	workspaceVars := release.WorkspaceEnv(private.plan, private.app.log)
	result.PrepareRequest = func(taskID string, state execution.ChoreographyVerifiedBindingState) (release.StageRequest, error) {
		frameState := release.TaskFrameState{WorkspaceVars: slices.Clone(workspaceVars),
			ProviderOutcomes: state.ProviderOutcomes, PredecessorOutputs: state.PredecessorOutputs}
		if byTask[taskID].Kind == execution.KindPrepare {
			return private.compiled.PreparePreparedTaskFrame(taskID, frameState)
		}
		return private.compiled.PrepareTaskFrame(taskID, frameState)
	}
	return result, nil
}
