// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestCompiledGraphIdentityIgnoresPlanMapInsertionOrder(t *testing.T) {
	first := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	second := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	second.Releases = map[string]*plan.Release{
		"app": second.Releases["app"], "ui": second.Releases["ui"], "core": second.Releases["core"],
	}
	second.Providers = map[string][]string{
		"ui": second.Providers["ui"], "app": second.Providers["app"],
	}
	compile := func(p *plan.Plan) *CompiledTaskGraph {
		t.Helper()
		graph, err := newExecutor(execSpec{}).CompileTaskGraph(p)
		require.NoError(t, err)
		return graph
	}
	want := compile(first)
	for range 20 {
		got := compile(second)
		require.Equal(t, want.Digest(), got.Digest())
		require.Equal(t, want.TaskShapes(), got.TaskShapes())
	}
}

func TestCompiledGraphFirstOnlyAuthorityMatchesStagePlacement(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.BeforeAllScript = []string{"before-all"}
	compiled, err := newExecutor(execSpec{}).CompileTaskGraph(p)
	require.NoError(t, err)
	seen := make(map[string]TaskShape)
	for _, shape := range compiled.TaskShapes() {
		seen[shape.Kind] = shape
	}
	require.True(t, seen["beforeAll"].FirstOnly)
	require.False(t, seen["build"].FirstOnly)
	require.False(t, seen["publish"].FirstOnly)
}

func TestNativeKindsStayExecutorOwnedAtBuildPlacement(t *testing.T) {
	buildPlacement := Stage{Placement: PlacementByBuild}
	for _, kind := range []taskKind{taskVersion, taskSign, taskSyncLock, taskBeforeAll} {
		require.True(t, firstOnlyTask(task{kind: kind}, buildPlacement), kind.String())
	}
	for _, kind := range []taskKind{taskBuild, taskTest, taskUser, taskPrepared} {
		require.False(t, firstOnlyTask(task{kind: kind}, buildPlacement), kind.String())
	}
	require.False(t, firstOnlyTask(task{kind: taskPublish}, buildPlacement))
	require.False(t, firstOnlyTask(task{kind: taskRecord}, buildPlacement))
	require.True(t, firstOnlyTask(task{kind: taskUser}, Stage{Placement: PlacementByPublish}))
	require.True(t, firstOnlyTask(task{kind: taskUser}, Stage{
		Placement: PlacementByBuild, IsObserving: true}))
}
