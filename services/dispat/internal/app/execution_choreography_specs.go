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
	if pl == nil || compiled == nil || len(roster) == choreographyNoEntriesCount {
		return nil, fmt.Errorf(choreographyIncompleteGraphRosterErrorText)
	}
	shapes := compiled.TaskShapes()
	first := choreographyAbsentIdentityText
	firstCapacity := choreographyNoCapacityCount
	for _, node := range roster {
		if node.First {
			if first != choreographyAbsentIdentityText {
				return nil, fmt.Errorf(choreographyMultipleFirstNodesErrorText)
			}
			first, firstCapacity = node.Name, node.Capacity
		}
	}
	if first == choreographyAbsentIdentityText || firstCapacity <= choreographyNoCapacityCount {
		return nil, fmt.Errorf(choreographyMissingFirstCapacityErrorText)
	}
	prepared := make(map[string]release.PreparedTaskSpec)
	for _, item := range compiled.PreparedTaskSpecs() {
		prepared[item.TaskID] = item
	}
	preparedProviders := make(map[string][]string)
	for _, policy := range compiled.PackagePolicies() {
		providers := append(slices.Clone(policy.Providers), policy.Reached...)
		providers = append(providers, policy.Direct...)
		slices.Sort(providers)
		preparedProviders[policy.Package] = slices.Compact(providers)
	}
	specs := make([]execution.ChoreographyTaskSpec, choreographyNoEntriesCount, len(shapes))
	for _, shape := range shapes {
		kind := shape.Kind
		// Built-in test and any other delegable non-build stage are distinct
		// semantic graph vertices, but their executable frames travel under
		// the generic signed Stage kind with Assignment.Stage naming the
		// stage. Keep the graph digest's semantic kind while making broker
		// reservation and Assignment agree on transport kind.
		if !shape.Virtual && !shape.FirstOnly && kind != execution.KindBuild &&
			kind != execution.KindPrepare && kind != choreographyPublishStageName && kind != choreographyRecordStageName {
			kind = execution.KindStage
		}
		spec := execution.ChoreographyTaskSpec{
			Task: shape.ID, Package: shape.Package, Kind: kind,
			Recording: shape.Recording, Virtual: shape.Virtual,
			ExecutorOwned: shape.FirstOnly || kind == choreographyPublishStageName || kind == choreographyRecordStageName,
			NeedsPreparedInput: !shape.Virtual && !shape.FirstOnly &&
				kind != choreographyPublishStageName && kind != choreographyRecordStageName,
			Predecessors:      slices.Clone(shape.Predecessors),
			PreparedProviders: slices.Clone(preparedProviders[shape.Package]),
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
		if rel == nil || rel.Pkg == nil || rel.Pkg.Space == nil || shape.Kind == choreographyAbsentIdentityText {
			return nil, fmt.Errorf(choreographyMissingTaskReleasePolicyErrorFormat, shape.ID)
		}
		if shape.Kind == choreographyPublishStageName || shape.Kind == choreographyRecordStageName {
			budget, err := choreographyBudgetName(shape.Budget)
			if err != nil {
				return nil, err
			}
			if shape.Weight <= choreographyNoCapacityCount || shape.Weight > firstCapacity {
				return nil, fmt.Errorf(choreographyLocalAuthorityCapacityErrorFormat, shape.ID)
			}
			spec.LocalAuthority = choreographyLocalAuthorityState
			spec.Budget, spec.Weight = budget, shape.Weight
			spec.Placement = execution.PlacementOrchestrator
			spec.EligibleNodes = []string{first}
			specs = append(specs, spec)
			continue
		}
		placement := execution.PlacementOrchestrator
		if !shape.FirstOnly && shape.Placement == release.PlacementByBuild {
			placement = execution.ResolveStagePlacement(execution.StageBuild,
				rel.Pkg.Space.RunOnly.ResolveBuild(), len(rel.Pkg.Space.LoginScript) > choreographyNoEntriesCount)
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
				return nil, fmt.Errorf(choreographyFirstOnlyCapacityErrorFormat, shape.ID)
			}
			spec.EligibleNodes = []string{first}
		} else {
			spec.EligibleNodes = execution.EligibleChoreographyNodes(execution.ChoreographyEligibleNodeOptions{
				Roster:    roster,
				Platforms: rel.Pkg.Space.BuildPlatforms,
				Placement: placement,
				Weight:    shape.Weight,
			})
		}
		if shape.Weight <= choreographyNoCapacityCount || shape.FirstOnly && len(spec.EligibleNodes) == choreographyNoEntriesCount {
			return nil, fmt.Errorf(choreographyNoCompatibleTaskNodeErrorFormat, shape.ID)
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
		return nil, fmt.Errorf(choreographyIncompletePeerReleasePlanErrorText)
	}
	budgets, err := releaseChoreographyBudgets(p.compiled,
		p.app.cfg.BuildConcurrency, p.app.cfg.PublishConcurrency)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(budgets, roster.Budgets) {
		return nil, fmt.Errorf(choreographyRetainedReleaseBudgetMismatchErrorText)
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
		return choreographyAbsentIdentityText, fmt.Errorf(choreographyUnknownStageBudgetErrorFormat, budget)
	}
}

func releaseChoreographyBudgets(compiled *release.CompiledTaskGraph,
	build, publish int) ([]execution.ChoreographyRuntimeBudget, error) {
	if compiled == nil {
		return nil, fmt.Errorf(choreographyMissingBudgetGraphErrorText)
	}
	capacities := compiled.BudgetCapacities(build, publish)
	out := make([]execution.ChoreographyRuntimeBudget, choreographyNoEntriesCount, len(capacities))
	for budget, capacity := range capacities {
		name, err := choreographyBudgetName(budget)
		if err != nil {
			return nil, err
		}
		if capacity <= choreographyNoCapacityCount {
			return nil, fmt.Errorf(choreographyEmptyRunBudgetErrorFormat, name)
		}
		out = append(out, execution.ChoreographyRuntimeBudget{Name: name, Capacity: capacity})
	}
	slices.SortFunc(out, func(a, b execution.ChoreographyRuntimeBudget) int {
		if a.Name < b.Name {
			return choreographySortBeforeValue
		}
		if a.Name > b.Name {
			return choreographySortAfterValue
		}
		return choreographySortEqualValue
	})
	return out, nil
}
