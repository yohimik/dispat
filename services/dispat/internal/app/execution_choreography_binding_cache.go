// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	missingSweepPinnedReleaseErrorFormat     = "execution: sweep task %q lacks a pinned release"
	missingSweepSignedPredecessorErrorFormat = "execution: sweep task %q lacks signed predecessor %q"
	missingReleaseFramePolicyError           = "execution: signed release frame has no compiled graph or ledger root"
	missingSweepFramePolicyError             = "execution: signed sweep frame has no pinned plan or ledger root"
	missingSweepPinnedPackageErrorFormat     = "execution: sweep task %q has no pinned package"
	invalidReleaseSyntheticTemplateError     = "execution: first-only release task has an invalid signed synthetic template"
	missingChoreographyBindingLedgerRootOID  = ""
)

func freezeChoreographySweepPlan(pl *plan.Plan) *plan.Plan {
	if pl == nil {
		return nil
	}
	frozen := *pl
	frozen.Releases = make(map[string]*plan.Release, len(pl.Releases))
	for name, rel := range pl.Releases {
		if rel == nil {
			continue
		}
		copy := *rel
		copy.Outputs = slices.Clone(rel.Outputs)
		frozen.Releases[name] = &copy
	}
	return &frozen
}

type sweepSignedVisibleReleaseOptions struct {
	Plan         *plan.Plan
	Task         execution.ChoreographyTaskSpec
	Predecessors map[string][]plan.Output
	OwnOutputs   []plan.Output
}

func sweepSignedVisibleRelease(options sweepSignedVisibleReleaseOptions) (*plan.Release, error) {
	pl, spec, predecessors, own := options.Plan, options.Task, options.Predecessors, options.OwnOutputs
	if pl == nil || pl.Releases[spec.Package] == nil {
		return nil, fmt.Errorf(missingSweepPinnedReleaseErrorFormat, spec.Task)
	}
	visible := *pl.Releases[spec.Package]
	visible.Outputs = slices.Clone(visible.Outputs)
	parents := slices.Clone(spec.Predecessors)
	slices.Sort(parents)
	for _, parent := range parents {
		outputs, found := predecessors[parent]
		if !found {
			return nil, fmt.Errorf(missingSweepSignedPredecessorErrorFormat, spec.Task, parent)
		}
		release.MergeOutputs(&visible, outputs)
	}
	release.MergeOutputs(&visible, own)
	return &visible, nil
}

const missingReleaseBindingStateErrorFormat = "execution: compiled task %q has no pinned release frame policy"

// newReleaseChoreographyBindingCache makes any indexed-ready task renderable
// from retained signed finality. The executor's current Drain decision and
// its mutable Result table are deliberately absent from this constructor.
type releaseChoreographyBindingCacheOptions struct {
	Coordinator   *execution.Coordinator
	Compiled      *release.CompiledTaskGraph
	Plan          *plan.Plan
	Tasks         []execution.ChoreographyTaskSpec
	Templates     []execution.ChoreographyCompiledTemplate
	Persist       *execution.GitChoreographyPersist
	LedgerRootOID string
	WorkspaceVars []string
}

func (a *App) newReleaseChoreographyBindingCache(options releaseChoreographyBindingCacheOptions) (
	func(context.Context, string, string) (execution.ChoreographyTaskBindings, error), error) {
	c, compiled, pl := options.Coordinator, options.Compiled, options.Plan
	specs, templates := options.Tasks, options.Templates
	persist, ledgerRootOID, workspaceVars := options.Persist, options.LedgerRootOID, options.WorkspaceVars
	if a == nil || c == nil || compiled == nil || pl == nil || persist == nil ||
		ledgerRootOID == missingChoreographyBindingLedgerRootOID {
		return nil, fmt.Errorf(missingReleaseFramePolicyError)
	}
	mailbox, err := c.ChoreographyMailbox()
	if err != nil {
		return nil, err
	}
	contexts := make(map[string]execution.ChoreographyBindingOutputContext, len(specs))
	prepared := make(map[string]*plan.Release)
	for _, item := range compiled.PreparedTaskSpecs() {
		prepared[item.TaskID] = item.Release
	}
	byTask := make(map[string]execution.ChoreographyTaskSpec, len(specs))
	for _, spec := range specs {
		byTask[spec.Task] = spec
	}
	synthetic := make(map[string]bool, len(templates))
	for _, template := range templates {
		spec, found := byTask[template.Task]
		if !found || !spec.ExecutorOwned {
			continue
		}
		digest, digestErr := execution.DigestChoreographyCompiledTemplate(template)
		if digestErr != nil || digest != spec.TemplateDigest {
			return nil, fmt.Errorf(invalidReleaseSyntheticTemplateError)
		}
		fallback := execution.ChoreographyCompiledTemplate{
			Task: spec.Task, Kind: spec.Kind, Weight: spec.Weight, Limits: template.Limits}
		fallbackDigest, digestErr := execution.DigestChoreographyCompiledTemplate(fallback)
		if digestErr != nil {
			return nil, fmt.Errorf(invalidReleaseSyntheticTemplateError)
		}
		synthetic[spec.Task] = digest == fallbackDigest
	}
	outcomePackages := make(map[string][]string, len(specs))
	inputPackages := make(map[string][]string, len(specs))
	for _, spec := range specs {
		rel := pl.Releases[spec.Package]
		if rel == nil {
			rel = prepared[spec.Task]
		}
		if rel == nil || rel.Pkg == nil || rel.Pkg.Space == nil {
			if spec.Virtual {
				continue
			}
			return nil, fmt.Errorf(missingReleaseBindingStateErrorFormat, spec.Task)
		}
		contexts[spec.Task] = execution.ChoreographyBindingOutputContext{
			Repository: rel.Pkg.Repository, Package: rel.Pkg.Name,
			SourceRoot: rel.Pkg.Dir, Roots: slices.Clone(rel.Pkg.Space.BuildOutputs),
		}
		if planned := pl.Releases[spec.Package]; planned != nil {
			for _, update := range planned.Updates {
				outcomePackages[spec.Task] = append(outcomePackages[spec.Task], update.Name)
			}
		}
		inputPackages[spec.Task] = c.ChoreographyInputPackages(spec.Package)
	}
	cache, err := execution.NewChoreographyBindingCache(execution.ChoreographyBindingCacheOptions{
		Binding: execution.ChoreographyRunBinding{Run: c.Run, Generation: c.Generation,
			PlanDigest: c.PlanDigest, GraphDigest: c.GraphDigest},
		LedgerRootOID: ledgerRootOID, Reader: persist, Mailbox: mailbox,
		MaxDocumentBytes: c.Limits.MaxManifestBytes, Tasks: specs,
		OutputContexts:        contexts,
		OutcomePackagesByTask: outcomePackages, InputPackagesByTask: inputPackages,
		Accumulate: func(taskID string, predecessors map[string][]plan.Output,
			own []plan.Output) ([]plan.Output, error) {
			if byTask[taskID].Kind == string(execution.KindPrepare) {
				return compiled.AccumulatePreparedTaskOutputs(taskID, predecessors, own)
			}
			return compiled.AccumulateTaskOutputs(taskID, predecessors, own)
		},
	})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, taskID, node string) (execution.ChoreographyTaskBindings, error) {
		state, err := cache.State(ctx, taskID)
		if err != nil {
			return execution.ChoreographyTaskBindings{}, err
		}
		if synthetic[taskID] {
			return execution.ChoreographyTaskBindings{
				PredecessorResultOIDs: slices.Clone(state.PredecessorTaskOIDs)}, nil
		}
		frameState := release.TaskFrameState{
			WorkspaceVars:      slices.Clone(workspaceVars),
			ProviderOutcomes:   state.ProviderOutcomes,
			PredecessorOutputs: state.PredecessorOutputs,
		}
		spec := byTask[taskID]
		var request release.StageRequest
		if spec.Kind == string(execution.KindPrepare) {
			request, err = compiled.PreparePreparedTaskFrame(taskID, frameState)
		} else if spec.ExecutorOwned {
			request, err = compiled.PrepareLocalTaskFrame(taskID, frameState)
		} else {
			request, err = compiled.PrepareTaskFrame(taskID, frameState)
		}
		if err != nil {
			return execution.ChoreographyTaskBindings{}, err
		}
		if spec.ExecutorOwned {
			return c.ResolveLocalChoreographyBindings(ctx, node, request, state)
		}
		return c.ResolveChoreographyBindings(ctx, execution.ChoreographyBindingResolution{
			Node: node, Request: request, State: state,
			TemplateDigest: spec.TemplateDigest})
	}, nil
}

type sweepChoreographyBindingCacheOptions struct {
	Coordinator   *execution.Coordinator
	Work          *scriptWork
	Tasks         []execution.ChoreographyTaskSpec
	Persist       *execution.GitChoreographyPersist
	LedgerRootOID string
}

func (a *App) newSweepChoreographyBindingCache(options sweepChoreographyBindingCacheOptions) (
	func(context.Context, string, string) (execution.ChoreographyTaskBindings, error), error) {
	c, work, specs := options.Coordinator, options.Work, options.Tasks
	persist, ledgerRootOID := options.Persist, options.LedgerRootOID
	if a == nil || c == nil || work == nil || work.pl == nil || persist == nil ||
		ledgerRootOID == missingChoreographyBindingLedgerRootOID {
		return nil, fmt.Errorf(missingSweepFramePolicyError)
	}
	mailbox, err := c.ChoreographyMailbox()
	if err != nil {
		return nil, err
	}
	frozenPlan := freezeChoreographySweepPlan(work.pl)
	contexts := make(map[string]execution.ChoreographyBindingOutputContext, len(specs))
	byTask := make(map[string]execution.ChoreographyTaskSpec, len(specs))
	for _, spec := range specs {
		byTask[spec.Task] = spec
		rel := frozenPlan.Releases[spec.Package]
		if rel == nil || rel.Pkg == nil {
			return nil, fmt.Errorf(missingSweepPinnedPackageErrorFormat, spec.Task)
		}
		contexts[spec.Task] = execution.ChoreographyBindingOutputContext{
			Repository: rel.Pkg.Repository, Package: rel.Pkg.Name,
			SourceRoot: rel.Pkg.Dir, Roots: slices.Clone(work.roots),
		}
	}
	cache, err := execution.NewChoreographyBindingCache(execution.ChoreographyBindingCacheOptions{
		Binding: execution.ChoreographyRunBinding{Run: c.Run, Generation: c.Generation,
			PlanDigest: c.PlanDigest, GraphDigest: c.GraphDigest},
		LedgerRootOID: ledgerRootOID, Reader: persist, Mailbox: mailbox,
		MaxDocumentBytes: c.Limits.MaxManifestBytes, Tasks: specs,
		OutputContexts: contexts, IncludeFailedExports: work.onError == OnErrorContinue,
		Accumulate: func(taskID string, predecessors map[string][]plan.Output,
			own []plan.Output) ([]plan.Output, error) {
			visible, err := sweepSignedVisibleRelease(sweepSignedVisibleReleaseOptions{
				Plan: frozenPlan, Task: byTask[taskID], Predecessors: predecessors, OwnOutputs: own})
			if err != nil {
				return nil, err
			}
			return visible.Outputs, nil
		},
	})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, taskID, node string) (execution.ChoreographyTaskBindings, error) {
		state, err := cache.State(ctx, taskID)
		if err != nil {
			return execution.ChoreographyTaskBindings{}, err
		}
		spec := byTask[taskID]
		rel := frozenPlan.Releases[spec.Package]
		if rel == nil || rel.Pkg == nil || rel.Pkg.Space == nil {
			return execution.ChoreographyTaskBindings{}, fmt.Errorf(missingSweepPinnedPackageErrorFormat, taskID)
		}
		visible, err := sweepSignedVisibleRelease(sweepSignedVisibleReleaseOptions{
			Plan: frozenPlan, Task: spec, Predecessors: state.PredecessorOutputs})
		if err != nil {
			return execution.ChoreographyTaskBindings{}, err
		}
		request := release.StageRequest{TaskID: taskID, Release: visible,
			Stage: work.stage(), Dir: rel.Pkg.Dir, StaticEnv: rel.Pkg.Space.Env,
			Env: release.ComputedSweepFrameEnv(release.ComputedSweepFrameEnvOptions{
				Plan: frozenPlan, Visible: visible, Package: spec.Package,
				Stage: work.stage(), WorkspaceVars: work.wsVars})}
		return c.ResolveChoreographySweepBindings(ctx, execution.ChoreographyBindingResolution{
			Node: node, Request: request, State: state,
			TemplateDigest: spec.TemplateDigest})
	}, nil
}
