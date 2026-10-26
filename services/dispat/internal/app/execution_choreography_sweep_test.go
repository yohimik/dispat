package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestSweepChoreographySpecsKeepNoopProviderGate(t *testing.T) {
	packages := map[string]*plan.Release{}
	for _, name := range []string{"a", "b", "c"} {
		packages[name] = &plan.Release{Pkg: &model.Package{
			Name: name, BuildWeight: 1, Space: &model.Space{},
		}}
	}
	packages["c"].Pkg.BuildWeight = 2
	pl := &plan.Plan{Releases: packages, Providers: map[string][]string{
		"b": {"a"}, "c": {"b"},
	}}
	roster := []execution.ChoreographyRuntimeNode{
		{Name: "first", Platform: "linux/amd64", Capacity: 2, First: true},
		{Name: "peer", Platform: "linux/amd64", Capacity: 2},
	}
	tasks := []execution.ReservationTask{
		{Task: "a:run", Kind: execution.KindRun, Placement: execution.PlacementWorker},
		{Task: "c:run", Kind: execution.KindRun, Placement: execution.PlacementAnyNode},
	}
	specs, budgets, err := sweepChoreographySpecs(sweepChoreographySpecOptions{
		Plan:          pl,
		Covered:       []string{"a", "b", "c"},
		Tasks:         tasks,
		Roster:        roster,
		BuildCapacity: 2,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"peer"}, specs[0].EligibleNodes)
	require.Equal(t, []string{"a:run"}, specs[1].Predecessors)
	require.True(t, specs[1].Virtual)
	require.Equal(t, []string{"b:run"}, specs[2].Predecessors)
	require.Equal(t, 2, specs[2].Weight)
	require.Equal(t, []string{"first", "peer"}, specs[2].EligibleNodes)
	require.Equal(t, []execution.ChoreographyRuntimeBudget{{
		Name: execution.ChoreographyBudgetBuild, Capacity: 2,
	}}, budgets)

	_, _, err = sweepChoreographySpecs(sweepChoreographySpecOptions{
		Plan:          pl,
		Covered:       []string{"a", "b", "c"},
		Tasks:         append(tasks, execution.ReservationTask{Task: "outside:run", Kind: execution.KindRun}),
		Roster:        roster,
		BuildCapacity: 2,
	})
	require.ErrorContains(t, err, "outside covered")
}
