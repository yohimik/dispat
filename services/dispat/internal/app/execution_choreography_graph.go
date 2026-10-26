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
		return fmt.Errorf("execution: prepared release graph has no plan or compiler")
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
	specs := make([]release.PreparedTaskSpec, 0, len(prepared))
	for _, item := range prepared {
		if item.Task == "" || item.Kind != execution.KindPrepare || item.Release == nil ||
			item.Release.Pkg == nil || item.Release.Pkg.Name != item.Package ||
			item.Weight != item.Release.Pkg.BuildWeight {
			return fmt.Errorf("execution: invalid prepared provider %q", item.Task)
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
		return nil, fmt.Errorf("execution: prepared graph has no compiled release tasks")
	}
	byStage := make(map[string]string)
	known := make(map[string]bool)
	for _, shape := range compiled.TaskShapes() {
		known[shape.ID] = true
		// beforeAll reports under the first stage it gates but is not that
		// stage. A prepared provider belongs to the actual consumer frame,
		// whose reservation names Stage, so exclude this hook-only vertex.
		if shape.Stage == "" || shape.Virtual || shape.Kind == "beforeAll" {
			continue
		}
		key := shape.Package + "\x00" + shape.Stage
		if prior := byStage[key]; prior != "" && prior != shape.ID {
			return nil, fmt.Errorf("execution: ambiguous compiled stage %s", shape.Stage)
		}
		byStage[key] = shape.ID
	}
	for _, item := range prepared {
		if item.Task == "" || known[item.Task] {
			return nil, fmt.Errorf("execution: repeated prepared task identity %q", item.Task)
		}
		known[item.Task] = true
	}
	edges := make([]preparedGraphEdge, 0)
	for _, item := range prepared {
		for _, consumer := range item.Consumers {
			taskID := consumer.Task
			if taskID == "" {
				taskID = byStage[consumer.Package+"\x00"+consumer.Stage]
			}
			if taskID == "" || !known[taskID] || taskID == item.Task {
				return nil, fmt.Errorf("execution: prepared provider %s has no exact consumer task", item.Task)
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
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
