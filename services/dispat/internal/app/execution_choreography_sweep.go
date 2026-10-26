package app

import (
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// sweepChoreographySpecs uses the same covered-package vertices and direct
// provider edges as runSweep. A covered package without the selected script
// remains a virtual gate, so its consumers retain their dependency order.
func sweepChoreographySpecs(pl *plan.Plan, covered []string,
	tasks []execution.ReservationTask, roster []execution.ChoreographyRuntimeNode,
	buildCapacity int) ([]execution.ChoreographyTaskSpec, []execution.ChoreographyRuntimeBudget, error) {
	if pl == nil || len(roster) == 0 || buildCapacity <= 0 {
		return nil, nil, fmt.Errorf("execution: incomplete sweep graph, roster or build budget")
	}
	known := make(map[string]execution.ReservationTask, len(tasks))
	for _, task := range tasks {
		if task.Kind != execution.KindRun || task.Task == "" || known[task.Task].Task != "" {
			return nil, nil, fmt.Errorf("execution: invalid or repeated sweep task")
		}
		known[task.Task] = task
	}
	coveredSet := make(map[string]bool, len(covered))
	for _, name := range covered {
		if name == "" || coveredSet[name] || pl.Releases[name] == nil {
			return nil, nil, fmt.Errorf("execution: invalid or repeated covered package")
		}
		coveredSet[name] = true
	}
	specs := make([]execution.ChoreographyTaskSpec, 0, len(covered))
	for _, name := range covered {
		id := name + ":" + execution.KindRun
		predecessors := make([]string, 0, len(pl.Providers[name]))
		for _, provider := range pl.Providers[name] {
			if coveredSet[provider] {
				predecessors = append(predecessors, provider+":"+execution.KindRun)
			}
		}
		spec := execution.ChoreographyTaskSpec{
			Task: id, Package: name, Kind: execution.KindRun,
			Predecessors: predecessors,
		}
		task, reached := known[id]
		if !reached {
			spec.Virtual = true
			specs = append(specs, spec)
			continue
		}
		delete(known, id)
		spec.Budget = execution.ChoreographyBudgetBuild
		spec.NeedsPreparedInput = true
		spec.Weight = pl.Releases[name].Pkg.BuildWeight
		spec.Placement = task.Placement
		spec.Platforms = slices.Clone(task.Platforms)
		spec.EligibleNodes = execution.EligibleChoreographyNodes(roster,
			task.Platforms, task.Placement, spec.Weight)
		if spec.Weight <= 0 {
			return nil, nil, fmt.Errorf("execution: sweep task %q has no compatible node", id)
		}
		specs = append(specs, spec)
	}
	if len(known) != 0 {
		return nil, nil, fmt.Errorf("execution: sweep task is outside covered packages")
	}
	return specs, []execution.ChoreographyRuntimeBudget{{
		Name: execution.ChoreographyBudgetBuild, Capacity: buildCapacity,
	}}, nil
}

func (p *peerSweepPlan) interpretedSweepSpecs(
	roster execution.ChoreographyRuntimeRoster) ([]execution.ChoreographyTaskSpec, error) {
	if p == nil || p.app == nil || p.app.cfg == nil {
		return nil, fmt.Errorf("execution: peer sweep plan is incomplete")
	}
	specs, budgets, err := sweepChoreographySpecs(p.plan, p.covered, p.tasks,
		roster.Nodes, p.app.cfg.BuildConcurrency)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(budgets, roster.Budgets) {
		return nil, fmt.Errorf("execution: retained sweep budget differs from the captured policy")
	}
	return specs, nil
}
