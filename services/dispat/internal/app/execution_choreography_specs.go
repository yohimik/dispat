package app

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// releaseChoreographySpecs translates only the shared compiler's exported
// task shapes. It never parses task IDs or derives another dependency graph.
func releaseChoreographySpecs(pl *plan.Plan, compiled *release.CompiledTaskGraph,
	roster []execution.ChoreographyRuntimeNode) ([]execution.ChoreographyTaskSpec, error) {
	if pl == nil || compiled == nil || len(roster) == 0 {
		return nil, fmt.Errorf("execution: incomplete compiled choreography graph or roster")
	}
	shapes := compiled.TaskShapes()
	first := ""
	firstCapacity := 0
	for _, node := range roster {
		if node.First {
			if first != "" {
				return nil, fmt.Errorf("execution: release roster has multiple first nodes")
			}
			first, firstCapacity = node.Name, node.Capacity
		}
	}
	if first == "" || firstCapacity <= 0 {
		return nil, fmt.Errorf("execution: release roster has no first-node capacity")
	}
	prepared := make(map[string]release.PreparedTaskSpec)
	for _, item := range compiled.PreparedTaskSpecs() {
		prepared[item.TaskID] = item
	}
	specs := make([]execution.ChoreographyTaskSpec, 0, len(shapes))
	for _, shape := range shapes {
		kind := shape.Kind
		// Built-in test and any other delegable non-build stage are distinct
		// semantic graph vertices, but their executable frames travel under
		// the generic signed Stage kind with Assignment.Stage naming the
		// stage. Keep the graph digest's semantic kind while making broker
		// reservation and Assignment agree on transport kind.
		if !shape.Virtual && !shape.FirstOnly && kind != execution.KindBuild &&
			kind != execution.KindPrepare && kind != "publish" && kind != "record" {
			kind = execution.KindStage
		}
		spec := execution.ChoreographyTaskSpec{
			Task: shape.ID, Package: shape.Package, Kind: kind,
			Recording: shape.Recording, Virtual: shape.Virtual,
			ExecutorOwned: shape.FirstOnly || kind == "publish" || kind == "record",
			NeedsPreparedInput: !shape.Virtual && !shape.FirstOnly &&
				kind != "publish" && kind != "record",
			Predecessors: slices.Clone(shape.Predecessors),
		}
		if kind == execution.KindStage {
			spec.Stage = shape.Stage
		}
		if shape.Virtual {
			specs = append(specs, spec)
			continue
		}
		rel := pl.Releases[shape.Package]
		if shape.Kind == execution.KindPrepare {
			rel = prepared[shape.ID].Release
		}
		if rel == nil || rel.Pkg == nil || rel.Pkg.Space == nil || shape.Kind == "" {
			return nil, fmt.Errorf("execution: compiled task %q has no release policy", shape.ID)
		}
		if shape.Kind == "publish" || shape.Kind == "record" {
			budget, err := choreographyBudgetName(shape.Budget)
			if err != nil {
				return nil, err
			}
			if shape.Weight <= 0 || shape.Weight > firstCapacity {
				return nil, fmt.Errorf("execution: local authority task %q exceeds first-node capacity", shape.ID)
			}
			spec.LocalAuthority = true
			spec.Budget, spec.Weight = budget, shape.Weight
			spec.Placement = execution.PlacementOrchestrator
			spec.EligibleNodes = []string{first}
			specs = append(specs, spec)
			continue
		}
		placement := execution.PlacementOrchestrator
		if !shape.FirstOnly && shape.Placement == release.PlacementByBuild {
			placement = execution.ResolveStagePlacement(execution.StageBuild,
				rel.Pkg.Space.RunOnly.ResolveBuild(), len(rel.Pkg.Space.LoginScript) > 0)
		}
		budget, err := choreographyBudgetName(shape.Budget)
		if err != nil {
			return nil, err
		}
		spec.Budget = budget
		spec.Weight = shape.Weight
		spec.Placement = placement
		spec.Platforms = slices.Clone(rel.Pkg.Space.BuildPlatforms)
		if shape.FirstOnly {
			if shape.Weight > firstCapacity {
				return nil, fmt.Errorf("execution: first-only task %q exceeds first-node capacity", shape.ID)
			}
			spec.EligibleNodes = []string{first}
		} else {
			spec.EligibleNodes = execution.EligibleChoreographyNodes(roster,
				rel.Pkg.Space.BuildPlatforms, placement, shape.Weight)
		}
		if shape.Weight <= 0 || shape.FirstOnly && len(spec.EligibleNodes) == 0 {
			return nil, fmt.Errorf("execution: compiled task %q has no compatible choreography node", shape.ID)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// interpretedReleaseSpecs rechecks the post-preflight runtime roster against
// the private peer's independently compiled policy before any claim. Node
// capacity comes from the signed roster, while run budgets come from the
// captured semantic configuration and the shared compiler.
func (p *peerReleasePlan) interpretedReleaseSpecs(
	roster execution.ChoreographyRuntimeRoster) ([]execution.ChoreographyTaskSpec, error) {
	if p == nil || p.app == nil || p.app.cfg == nil || p.compiled == nil {
		return nil, fmt.Errorf("execution: peer release plan is incomplete")
	}
	budgets, err := releaseChoreographyBudgets(p.compiled,
		p.app.cfg.BuildConcurrency, p.app.cfg.PublishConcurrency)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(budgets, roster.Budgets) {
		return nil, fmt.Errorf("execution: retained run budgets differ from the independently compiled release")
	}
	return releaseChoreographySpecs(p.plan, p.compiled, roster.Nodes)
}

func choreographyBudgetName(budget release.StageBudget) (string, error) {
	switch budget {
	case release.BudgetBuild:
		return execution.ChoreographyBudgetBuild, nil
	case release.BudgetPublish:
		return execution.ChoreographyBudgetPublish, nil
	case release.BudgetSyncLock:
		return execution.ChoreographyBudgetSyncLock, nil
	default:
		return "", fmt.Errorf("execution: unknown compiled stage budget %d", budget)
	}
}

func releaseChoreographyBudgets(compiled *release.CompiledTaskGraph,
	build, publish int) ([]execution.ChoreographyRuntimeBudget, error) {
	if compiled == nil {
		return nil, fmt.Errorf("execution: no compiled graph for run budgets")
	}
	capacities := compiled.BudgetCapacities(build, publish)
	out := make([]execution.ChoreographyRuntimeBudget, 0, len(capacities))
	for budget, capacity := range capacities {
		name, err := choreographyBudgetName(budget)
		if err != nil {
			return nil, err
		}
		if capacity <= 0 {
			return nil, fmt.Errorf("execution: %s run budget has no capacity", name)
		}
		out = append(out, execution.ChoreographyRuntimeBudget{Name: name, Capacity: capacity})
	}
	slices.SortFunc(out, func(a, b execution.ChoreographyRuntimeBudget) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return out, nil
}
