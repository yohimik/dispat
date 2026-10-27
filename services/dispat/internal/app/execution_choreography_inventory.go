// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// releaseChoreographyInventory fixes the complete node-neutral descriptor
// inventory before bootstrap. Runtime membership may change afterward;
// neither node names nor generated task-root OIDs enter the graph digest.
func (a *App) releaseChoreographyInventory(c *execution.Coordinator,
	pl *plan.Plan, compiled *release.CompiledTaskGraph) (
	[]execution.ChoreographyTaskSpec, []execution.ChoreographyCompiledTemplate,
	[]execution.ChoreographyPackagePolicy, string, error) {
	if a == nil || c == nil || pl == nil || compiled == nil || c.Local.Capacity <= choreographyNoCapacityCount {
		return nil, nil, nil, choreographyAbsentIdentityText, fmt.Errorf(choreographyIncompleteReleaseInventoryErrorText)
	}
	provisional := []execution.ChoreographyRuntimeNode{{Name: c.Local.Name,
		Capacity: c.Local.Capacity, First: choreographyFirstNodeState, AuthorityID: c.Local.AuthorityID}}
	specs, err := releaseChoreographySpecs(pl, compiled, provisional)
	if err != nil {
		return nil, nil, nil, choreographyAbsentIdentityText, err
	}
	applyChoreographyRetrySafety(specs, c.JoinPolicy.RetryAfterUncertainAttempt)
	applyChoreographyAttemptLimit(specs, c.JoinPolicy)
	templates, err := compileReleaseChoreographyTemplatesWithInputs(releaseChoreographyTemplateOptions{
		Compiled:    compiled,
		Tasks:       specs,
		Sources:     a.resolveInputSources(pl),
		Shell:       a.resolveShell,
		TaskTimeout: c.Timeouts.Task,
		Limits:      c.Limits,
	})
	if err != nil {
		return nil, nil, nil, choreographyAbsentIdentityText, err
	}
	policies := releaseChoreographyPackagePolicies(compiled)
	binding := execution.ChoreographyRunBinding{Run: c.Run, Generation: c.Generation,
		PlanDigest: c.PlanDigest}
	digest, err := execution.DigestChoreographyLogicalGraph(execution.ChoreographyLogicalGraphInventory{
		Binding: binding, AlgorithmVersion: c.AlgorithmVersion,
		SemanticGraphDigest: compiled.Digest(), Tasks: specs,
		Templates: templates, PackagePolicies: policies})
	return specs, templates, policies, digest, err
}

type sweepChoreographyInventoryOptions struct {
	Coordinator         *execution.Coordinator
	Work                *scriptWork
	Covered             []string
	Tasks               []execution.ReservationTask
	OnError             string
	SemanticGraphDigest string
}

func (a *App) sweepChoreographyInventory(options sweepChoreographyInventoryOptions) (
	[]execution.ChoreographyTaskSpec, []execution.ChoreographyCompiledTemplate,
	[]execution.ChoreographyPackagePolicy, string, error) {
	c, work, covered, tasks, onError, semanticGraphDigest := options.Coordinator, options.Work, options.Covered, options.Tasks, options.OnError, options.SemanticGraphDigest

	if a == nil || c == nil || work == nil || work.pl == nil || c.Local.Capacity <= choreographyNoCapacityCount {
		return nil, nil, nil, choreographyAbsentIdentityText, fmt.Errorf(choreographyIncompleteSweepInventoryErrorText)
	}
	provisional := []execution.ChoreographyRuntimeNode{{Name: c.Local.Name,
		Capacity: c.Local.Capacity, First: choreographyFirstNodeState, AuthorityID: c.Local.AuthorityID}}
	specs, _, err := sweepChoreographySpecs(sweepChoreographySpecOptions{
		Plan:          work.pl,
		Covered:       covered,
		Tasks:         tasks,
		Roster:        provisional,
		BuildCapacity: a.cfg.BuildConcurrency,
	})
	if err != nil {
		return nil, nil, nil, choreographyAbsentIdentityText, err
	}
	applyChoreographyRetrySafety(specs, c.JoinPolicy.RetryAfterUncertainAttempt)
	applyChoreographyAttemptLimit(specs, c.JoinPolicy)
	templates, err := compileSweepChoreographyTemplatesWithInputs(sweepChoreographyTemplateOptions{
		Work:        work,
		Tasks:       specs,
		Sources:     a.resolveInputSources(work.pl),
		Shell:       a.resolveShell,
		TaskTimeout: c.Timeouts.Task,
		Limits:      c.Limits,
	})
	if err != nil {
		return nil, nil, nil, choreographyAbsentIdentityText, err
	}
	policies := sweepChoreographyPackagePolicies(work.pl, covered, onError)
	binding := execution.ChoreographyRunBinding{Run: c.Run, Generation: c.Generation,
		PlanDigest: c.PlanDigest}
	digest, err := execution.DigestChoreographyLogicalGraph(execution.ChoreographyLogicalGraphInventory{
		Binding: binding, AlgorithmVersion: c.AlgorithmVersion,
		SemanticGraphDigest: semanticGraphDigest, Tasks: specs,
		Templates: templates, PackagePolicies: policies})
	return specs, templates, policies, digest, err
}
