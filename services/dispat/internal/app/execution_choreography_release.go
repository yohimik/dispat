// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

type releaseChoreographyBindingOptions struct {
	Coordinator   *execution.Coordinator
	Plan          *plan.Plan
	Compiled      *release.CompiledTaskGraph
	ReturnChannel *runReturnChannel
}

// bindReleaseChoreography gives the first node the exact effective DAG used
// for preflight and RunCompiled, including unreleasing prepared providers.
// It binds the shared release skip policy to the canonical broker before any
// executor task can request a claim or publish an effect.
func (a *App) bindReleaseChoreography(ctx context.Context, options releaseChoreographyBindingOptions) (*execution.FirstNodeBroker, error) {
	coordinator, pl, compiled, returnChannel := options.Coordinator, options.Plan, options.Compiled, options.ReturnChannel

	if a == nil || a.cfg == nil || coordinator == nil || pl == nil || compiled == nil ||
		returnChannel == nil {
		return nil, fmt.Errorf(choreographyReleaseGraphMismatchErrorText)
	}
	specs, err := releaseChoreographySpecs(pl, compiled, coordinator.ChoreographyNodes())
	if err != nil {
		return nil, err
	}
	applyChoreographyRetrySafety(specs, coordinator.JoinPolicy.RetryAfterUncertainAttempt)
	applyChoreographyAttemptLimit(specs, coordinator.JoinPolicy)
	if err := bindHybridTaskPolicy(coordinator, specs, compiled.Digest()); err != nil {
		return nil, err
	}
	templates, err := compileReleaseChoreographyTemplates(coordinator, compiled, specs)
	if err != nil {
		return nil, err
	}
	policies := releaseChoreographyPackagePolicies(compiled)
	binding := execution.ChoreographyRunBinding{Run: coordinator.Run,
		Generation: coordinator.Generation, PlanDigest: coordinator.PlanDigest}
	digest, err := execution.DigestChoreographyLogicalGraph(execution.ChoreographyLogicalGraphInventory{
		Binding: binding, AlgorithmVersion: coordinator.AlgorithmVersion,
		SemanticGraphDigest: compiled.Digest(), Tasks: specs,
		Templates: templates, PackagePolicies: policies})
	if err != nil {
		return nil, err
	}
	if digest != coordinator.GraphDigest {
		return nil, fmt.Errorf(choreographyReleaseLogicalGraphMismatchErrorText)
	}
	budgets, err := releaseChoreographyBudgets(compiled,
		a.cfg.BuildConcurrency, a.cfg.PublishConcurrency)
	if err != nil {
		return nil, err
	}
	return a.initializeAndBindChoreography(ctx, choreographyBindOptions{
		Coordinator: coordinator, ReturnChannel: returnChannel,
		Tasks: specs, Templates: templates, PackagePolicies: policies,
		SemanticGraphDigest: compiled.Digest(), Budgets: budgets,
		OutcomePolicy: compiled.NewOutcomeEvaluator(),
		ResolveBindingsFactory: func(persist *execution.GitChoreographyPersist,
			ledgerRootOID string) (func(context.Context, string, string) (execution.ChoreographyTaskBindings, error), error) {
			return a.newReleaseChoreographyBindingCache(releaseChoreographyBindingCacheOptions{
				Coordinator: coordinator, Compiled: compiled, Plan: pl,
				Tasks: specs, Templates: templates, Persist: persist,
				LedgerRootOID: ledgerRootOID, WorkspaceVars: release.WorkspaceEnv(pl, a.log)})
		},
	})
}
