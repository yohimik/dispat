package app

import (
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// includePreparedReleaseGraph adds the exact unreleasing provider closure to
// the compiler's scheduler before its digest or reservations are published.
// The same graph is then drained by RunCompiled on the first node and derived
// from pinned inputs by an interpreting peer.
func (a *App) includePreparedReleaseGraph(pl *plan.Plan, compiled *release.CompiledTaskGraph) error {
	if a == nil || pl == nil || compiled == nil {
		return fmt.Errorf(choreographyMissingPreparedCompilerErrorText)
	}
	prepared, err := execution.DiscoverChoreographyPreparedTasks(compiled.ReservationStages(),
		execution.ChoreographyPreparedDiscovery{
			Inputs: a.resolveOutputProviders(pl), Prepare: a.resolvePreparedProviders(pl, nil),
		})
	if err != nil {
		return err
	}
	return includeDiscoveredPreparedReleaseGraph(compiled, prepared)
}

func includeDiscoveredPreparedReleaseGraph(compiled *release.CompiledTaskGraph,
	prepared []execution.ChoreographyPreparedTask) error {
	edges, err := resolvePreparedGraphEdges(compiled, prepared)
	if err != nil {
		return err
	}
	consumers := make(map[string][]string, len(prepared))
	for _, edge := range edges {
		consumers[edge.From] = append(consumers[edge.From], edge.To)
	}
	specs := make([]release.PreparedTaskSpec, choreographyNoEntriesCount, len(prepared))
	for _, item := range prepared {
		if item.Task == choreographyAbsentIdentityText || item.Kind != execution.KindPrepare || item.Release == nil ||
			item.Release.Pkg == nil || item.Release.Pkg.Name != item.Package ||
			item.Weight != item.Release.Pkg.BuildWeight {
			return fmt.Errorf(choreographyInvalidPreparedProviderErrorFormat, item.Task)
		}
		specs = append(specs, release.PreparedTaskSpec{
			TaskID: item.Task, Package: item.Package, Release: item.Release,
			ConsumerTaskIDs: slices.Clone(consumers[item.Task]), Weight: item.Weight,
			Placement: release.PlacementByBuild,
		})
	}
	return compiled.IncludePreparedTasks(specs)
}

type preparedGraphEdge struct{ From, To string }

// resolvePreparedGraphEdges maps typed stage consumers to the compiler's
// exact task identities. It does not construct another scheduler or digest.
func resolvePreparedGraphEdges(compiled *release.CompiledTaskGraph,
	prepared []execution.ChoreographyPreparedTask) ([]preparedGraphEdge, error) {
	if compiled == nil {
		return nil, fmt.Errorf(choreographyMissingPreparedReleaseTasksErrorText)
	}
	byStage := make(map[string]string)
	known := make(map[string]bool)
	for _, shape := range compiled.TaskShapes() {
		known[shape.ID] = choreographyKnownMemberState
		// beforeAll reports under the first stage it gates but is not that
		// stage. A prepared provider belongs to the actual consumer frame,
		// whose reservation names Stage, so exclude this hook-only vertex.
		if shape.Stage == choreographyAbsentIdentityText || shape.Virtual || shape.Kind == choreographyBeforeAllStageName {
			continue
		}
		key := shape.Package + choreographyStageKeySeparatorText + shape.Stage
		if prior := byStage[key]; prior != choreographyAbsentIdentityText && prior != shape.ID {
			return nil, fmt.Errorf(choreographyAmbiguousStageErrorFormat, shape.Stage)
		}
		byStage[key] = shape.ID
	}
	for _, item := range prepared {
		if item.Task == choreographyAbsentIdentityText || known[item.Task] {
			return nil, fmt.Errorf(choreographyRepeatedPreparedTaskErrorFormat, item.Task)
		}
		known[item.Task] = choreographyKnownMemberState
	}
	edges := make([]preparedGraphEdge, choreographyNoEntriesCount)
	for _, item := range prepared {
		for _, consumer := range item.Consumers {
			taskID := consumer.Task
			if taskID == choreographyAbsentIdentityText {
				taskID = byStage[consumer.Package+choreographyStageKeySeparatorText+consumer.Stage]
			}
			if taskID == choreographyAbsentIdentityText || !known[taskID] || taskID == item.Task {
				return nil, fmt.Errorf(choreographyMissingPreparedConsumerErrorFormat, item.Task)
			}
			edges = append(edges, preparedGraphEdge{From: item.Task, To: taskID})
		}
	}
	slices.SortFunc(edges, func(a, b preparedGraphEdge) int {
		if a.From != b.From {
			return compareGraphString(a.From, b.From)
		}
		return compareGraphString(a.To, b.To)
	})
	return slices.Compact(edges), nil
}

func compareGraphString(a, b string) int {
	if a < b {
		return choreographySortBeforeValue
	}
	if a > b {
		return choreographySortAfterValue
	}
	return choreographySortEqualValue
}
