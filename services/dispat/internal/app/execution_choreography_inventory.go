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
	if a == nil || c == nil || pl == nil || compiled == nil || c.Local.Capacity <= 0 {
		return nil, nil, nil, "", fmt.Errorf("execution: preflight compiled inventory lacks pinned plan or first-node capacity")
	}
	provisional := []execution.ChoreographyRuntimeNode{{Name: c.Local.Name,
		Capacity: c.Local.Capacity, First: true, AuthorityID: c.Local.AuthorityID}}
	specs, err := releaseChoreographySpecs(pl, compiled, provisional)
	if err != nil {
		return nil, nil, nil, "", err
	}
	templates, err := compileReleaseChoreographyTemplatesWithInputs(compiled, specs,
		a.resolveInputSources(pl), a.resolveShell, c.Timeouts.Task, c.Limits)
	if err != nil {
		return nil, nil, nil, "", err
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

func (a *App) sweepChoreographyInventory(c *execution.Coordinator,
	work *scriptWork, covered []string, tasks []execution.ReservationTask,
	onError, semanticGraphDigest string) (
	[]execution.ChoreographyTaskSpec, []execution.ChoreographyCompiledTemplate,
	[]execution.ChoreographyPackagePolicy, string, error) {
	if a == nil || c == nil || work == nil || work.pl == nil || c.Local.Capacity <= 0 {
		return nil, nil, nil, "", fmt.Errorf("execution: preflight sweep inventory lacks pinned plan or first-node capacity")
	}
	provisional := []execution.ChoreographyRuntimeNode{{Name: c.Local.Name,
		Capacity: c.Local.Capacity, First: true, AuthorityID: c.Local.AuthorityID}}
	specs, _, err := sweepChoreographySpecs(work.pl, covered, tasks, provisional, a.cfg.BuildConcurrency)
	if err != nil {
		return nil, nil, nil, "", err
	}
	templates, err := compileSweepChoreographyTemplatesWithInputs(work, specs,
		a.resolveInputSources(work.pl), a.resolveShell, c.Timeouts.Task, c.Limits)
	if err != nil {
		return nil, nil, nil, "", err
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
