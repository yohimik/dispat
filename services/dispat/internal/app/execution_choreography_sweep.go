package app

import (
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

type sweepChoreographySpecOptions struct {
	Plan          *plan.Plan
	Covered       []string
	Tasks         []execution.ReservationTask
	Roster        []execution.ChoreographyRuntimeNode
	BuildCapacity int
}

// sweepChoreographySpecs uses the same covered-package vertices and direct
// provider edges as runSweep. A covered package without the selected script
// remains a virtual gate, so its consumers retain their dependency order.
func sweepChoreographySpecs(options sweepChoreographySpecOptions) ([]execution.ChoreographyTaskSpec, []execution.ChoreographyRuntimeBudget, error) {
	pl, covered, tasks, roster, buildCapacity := options.Plan, options.Covered, options.Tasks, options.Roster, options.BuildCapacity

	if pl == nil || len(roster) == choreographyNoEntriesCount || buildCapacity <= choreographyNoCapacityCount {
		return nil, nil, fmt.Errorf(choreographyIncompleteSweepGraphErrorText)
	}
	known := make(map[string]execution.ReservationTask, len(tasks))
	for _, task := range tasks {
		if task.Kind != execution.KindRun || task.Task == choreographyAbsentIdentityText || known[task.Task].Task != choreographyAbsentIdentityText {
			return nil, nil, fmt.Errorf(choreographyInvalidSweepTaskErrorText)
		}
		known[task.Task] = task
	}
	coveredSet := make(map[string]bool, len(covered))
	for _, name := range covered {
		if name == choreographyAbsentIdentityText || coveredSet[name] || pl.Releases[name] == nil {
			return nil, nil, fmt.Errorf(choreographyInvalidCoveredPackageErrorText)
		}
		coveredSet[name] = choreographyKnownMemberState
	}
	specs := make([]execution.ChoreographyTaskSpec, choreographyNoEntriesCount, len(covered))
	for _, name := range covered {
		id := name + choreographyTaskPartSeparatorText + execution.KindRun
		predecessors := make([]string, choreographyNoEntriesCount, len(pl.Providers[name]))
		for _, provider := range pl.Providers[name] {
			if coveredSet[provider] {
				predecessors = append(predecessors, provider+choreographyTaskPartSeparatorText+execution.KindRun)
			}
		}
		spec := execution.ChoreographyTaskSpec{
			Task: id, Package: name, Kind: execution.KindRun,
			Predecessors: predecessors,
		}
		task, reached := known[id]
		if !reached {
			spec.Virtual = choreographyVirtualTaskState
			specs = append(specs, spec)
			continue
		}
		delete(known, id)
		spec.Budget = execution.ChoreographyBudgetBuild
		spec.NeedsPreparedInput = choreographyNeedsPreparedInputState
		spec.Weight = pl.Releases[name].Pkg.BuildWeight
		spec.Placement = task.Placement
		spec.Platforms = slices.Clone(task.Platforms)
		spec.EligibleNodes = execution.EligibleChoreographyNodes(execution.ChoreographyEligibleNodeOptions{
			Roster:    roster,
			Platforms: task.Platforms,
			Placement: task.Placement,
			Weight:    spec.Weight,
		})
		if spec.Weight <= choreographyNoCapacityCount {
			return nil, nil, fmt.Errorf(choreographyNoCompatibleSweepNodeErrorFormat, id)
		}
		specs = append(specs, spec)
	}
	if len(known) != choreographyNoEntriesCount {
		return nil, nil, fmt.Errorf(choreographyUncoveredSweepTaskErrorText)
	}
	return specs, []execution.ChoreographyRuntimeBudget{{
		Name: execution.ChoreographyBudgetBuild, Capacity: buildCapacity,
	}}, nil
}

func (p *peerSweepPlan) interpretedSweepSpecs(
	roster execution.ChoreographyRuntimeRoster) ([]execution.ChoreographyTaskSpec, error) {
	if p == nil || p.app == nil || p.app.cfg == nil {
		return nil, fmt.Errorf(choreographyIncompletePeerSweepPlanErrorText)
	}
	specs, budgets, err := sweepChoreographySpecs(sweepChoreographySpecOptions{
		Plan:          p.plan,
		Covered:       p.covered,
		Tasks:         p.tasks,
		Roster:        roster.Nodes,
		BuildCapacity: p.app.cfg.BuildConcurrency,
	})
	if err != nil {
		return nil, err
	}
	if !slices.Equal(budgets, roster.Budgets) {
		return nil, fmt.Errorf(choreographyRetainedSweepBudgetMismatchErrorText)
	}
	return specs, nil
}
