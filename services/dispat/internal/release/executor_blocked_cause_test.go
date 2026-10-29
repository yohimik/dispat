// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestBlockedTaskKeepsItsOwnAdmissionCause(t *testing.T) {
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"docs": {"assets"}}, Names: []string{"assets", "docs"}})
	space := p.Releases["assets"].Pkg.Space
	space.TestScript, space.OnSkipScript = []string{"test"}, []string{"on-skip"}
	space.BeforePublishScript = []string{"before-publish"}
	space.RevertOnFail = true
	runner := &fakeRunner{fail: map[string]bool{"before-publish assets": true}}
	reverter := &fakeReverter{}
	e := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 2, Publish: 1})
	e.Reverter = reverter
	var mu sync.Mutex
	physical, settled := map[string]bool{}, map[string]bool{}
	blocked := map[string]string{}
	e.LocalTaskLifecycle = func(_ context.Context, taskID string) (func(context.Context, string) error, error) {
		mu.Lock()
		physical[taskID] = true
		mu.Unlock()
		return func(context.Context, string) error {
			mu.Lock()
			defer mu.Unlock()
			if !settled[taskID] {
				return fmt.Errorf("physical lease released before finality for %s", taskID)
			}
			delete(physical, taskID)
			return nil
		}, nil
	}
	finish := func(taskID string) {
		mu.Lock()
		settled[taskID] = true
		mu.Unlock()
	}
	e.BeginFirstOnlyTask = func(_ context.Context, taskID string) (func(context.Context, bool, PackageOutcome) error, error) {
		return func(context.Context, bool, PackageOutcome) error { finish(taskID); return nil }, nil
	}
	e.BeginLocalAuthorityTask = func(context.Context, string) error { return nil }
	e.LocalAuthoritySettled = func(_ context.Context, taskID string, _ *plan.Release, _ bool, _ PackageOutcome) error {
		finish(taskID)
		return nil
	}
	block := func(_ context.Context, taskID, blocker string, outcome PackageOutcome) error {
		mu.Lock()
		defer mu.Unlock()
		causal := taskID == taskGraphID(task{pkg: "docs", kind: taskBuild})
		if causal && (blocker != "assets" || outcome != OutcomeSkipped) || !causal && (blocker != "docs" || outcome != "") {
			return fmt.Errorf("blocked task %s has blocker %q and package outcome %q", taskID, blocker, outcome)
		}
		if !physical[taskID] || runner.countPrefix("on-skip") != 1 {
			return fmt.Errorf("blocked finality for %s precedes physical lease or skip hook", taskID)
		}
		blocked[taskID], settled[taskID] = blocker, true
		return nil
	}
	e.BlockFirstOnlyTask, e.BlockLocalAuthorityTask = block, block
	e.TaskOutcomeSettled = func(context.Context, string, PackageOutcome) error { return nil }
	compiled, err := e.CompileTaskGraph(p)
	require.NoError(t, err)
	results, err := e.RunCompiled(t.Context(), p, compiled)
	require.NoError(t, err, "every denied task must have admissible finality")
	require.Equal(t, StatusFailed, results["assets"].Status)
	require.Equal(t, StatusSkipped, results["docs"].Status)
	require.Equal(t, "assets", results["docs"].BlockedBy, "report provenance remains the actual failed provider")
	require.Equal(t, 1, runner.countPrefix("on-skip"))
	require.Equal(t, "assets", envValue(t, runner.envs["on-skip docs"], "DISPAT_BLOCKED_BY"))
	require.Empty(t, slices.DeleteFunc(slices.Clone(reverter.dirs), func(dir string) bool { return dir != "docs" }), "untouched consumer has no checkout changes to revert")
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, physical, "all physical slots release only after their own finality")
	for _, kind := range []taskKind{taskBuild, taskTest, taskPublish} {
		taskID := taskGraphID(task{pkg: "docs", kind: kind})
		want := "docs"
		if kind == taskBuild {
			want = "assets"
		}
		require.Equal(t, want, blocked[taskID])
	}
	for _, event := range runner.events {
		if strings.HasSuffix(event, " docs") {
			require.NotContains(t, []string{"build docs", "test docs", "publish docs"}, event)
		}
	}
}
