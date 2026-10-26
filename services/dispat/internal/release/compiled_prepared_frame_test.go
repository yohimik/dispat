// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestPreparedFrameUsesSignedPredecessorExportsWithoutMutatingPinnedPlan(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	prepared := PreparedTaskSpec{TaskID: "ui:prepare", Package: "ui", Release: p.Releases["ui"],
		ConsumerTaskIDs: []string{taskGraphID(task{pkg: "app", kind: taskBuild})},
		Weight:          1, Placement: PlacementByBuild}
	require.NoError(t, compiled.IncludePreparedTasks([]PreparedTaskSpec{prepared}))

	request, err := compiled.PreparePreparedTaskFrame(prepared.TaskID, TaskFrameState{
		PredecessorOutputs: map[string][]plan.Output{
			"signed-parent": {{Name: "TOKEN", Value: "retained", Source: "parent:build"}},
		},
	})
	require.NoError(t, err)
	require.Contains(t, request.Env, "DISPAT_OUTPUT_TOKEN=retained")
	require.Empty(t, p.Releases["ui"].Outputs)

	accumulated, err := compiled.AccumulatePreparedTaskOutputs(prepared.TaskID,
		map[string][]plan.Output{"signed-parent": {{Name: "TOKEN", Value: "retained"}}},
		[]plan.Output{{Name: "OWN", Value: "answer"}})
	require.NoError(t, err)
	require.Contains(t, accumulated, plan.Output{Name: "OWN", Value: "answer"})
	require.Contains(t, accumulated, plan.Output{Name: "TOKEN", Value: "retained"})
}

func TestCompiledFrameIgnoresPlanOutputsMutatedAfterCompilation(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Outputs = []plan.Output{{Name: "TOKEN", Value: "baseline"}}
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	buildID := taskGraphID(task{pkg: "a", kind: taskBuild})
	predecessors := make(map[string][]plan.Output)
	for _, before := range compiled.plans["a"].predecessors(compiled.byID[buildID]) {
		predecessors[taskGraphID(before)] = nil
	}
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for index := 0; index < 500; index++ {
			p.Releases["a"].Outputs = []plan.Output{{Name: "TOKEN", Value: "mutable"}}
		}
	}()
	for index := 0; index < 500; index++ {
		request, frameErr := compiled.PrepareTaskFrame(buildID,
			TaskFrameState{PredecessorOutputs: predecessors})
		require.NoError(t, frameErr)
		require.Contains(t, request.Env, "DISPAT_OUTPUT_TOKEN=baseline")
	}
	writer.Wait()
}
