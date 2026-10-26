package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func TestReleaseChoreographySpecsKeepBuildPlacementAndFirstOnlyPublication(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte(`{
  "scripts":{"build":"true","publish":"true","announce":"true"},
  "spaces":{"libs":{"path":["packages"],"runOnly":["worker","orchestrator"],
    "flow":{"build":["build"],"publish":["publish"],"announce":["announce"]}}}
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "package.json"),
		[]byte(`{"name":"core","version":"0.0.0"}`), 0o644))
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.name", "Test")
	fixtureGit(t, root, "config", "user.email", "test@example.com")
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-qm", "fix(core): first")
	loaded, err := config.Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	selected, err := New(root, loaded, zerolog.Nop()).selectedPlan(t.Context(), ReleaseOptions{})
	require.NoError(t, err)
	compiled, err := (&release.Executor{PublishGroup: publishByRepository}).CompileTaskGraph(selected)
	require.NoError(t, err)
	roster := []execution.ChoreographyRuntimeNode{
		{Name: "first", Platform: "linux/amd64", Capacity: 2, First: true},
		{Name: "peer", Platform: "linux/amd64", Capacity: 2},
	}
	specs, err := releaseChoreographySpecs(selected, compiled, roster)
	require.NoError(t, err)
	var build, publish, announce *execution.ChoreographyTaskSpec
	for i := range specs {
		switch specs[i].Kind {
		case "build":
			build = &specs[i]
		case "publish":
			publish = &specs[i]
		case "announce":
			announce = &specs[i]
		}
	}
	require.NotNil(t, build)
	require.Equal(t, []string{"peer"}, build.EligibleNodes)
	require.False(t, build.LocalAuthority)
	require.NotNil(t, publish)
	require.True(t, publish.LocalAuthority)
	require.Equal(t, []string{"first"}, publish.EligibleNodes)
	require.Positive(t, publish.Weight)
	require.Equal(t, execution.ChoreographyBudgetPublish, publish.Budget)
	require.NotNil(t, announce)
	require.Equal(t, []string{"first"}, announce.EligibleNodes,
		"an observing Stage retains first-node authority even when its stage descriptor uses build placement")
	budgets, err := releaseChoreographyBudgets(compiled, 2, 1)
	require.NoError(t, err)
	require.Equal(t, []execution.ChoreographyRuntimeBudget{
		{Name: execution.ChoreographyBudgetBuild, Capacity: 2},
		{Name: execution.ChoreographyBudgetPublish, Capacity: 1},
		{Name: execution.ChoreographyBudgetSyncLock, Capacity: 1},
	}, budgets)
}

func TestCompiledParallelTestAndUserStageHaveNoPrecedenceEdge(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte(`{
  "scripts":{"build":"true","check":"true","lint":"true","on-fail":"true"},
  "spaces":{"libs":{"path":["packages"],"runOnly":["both","orchestrator"],"flow":{"build":["build"],
    "test":["check"],"lint":{"script":["lint"],"parallel":["test"]},"onFail":["on-fail"]}}}
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "package.json"),
		[]byte(`{"name":"core","version":"0.0.0"}`), 0o644))
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.name", "Test")
	fixtureGit(t, root, "config", "user.email", "test@example.com")
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-qm", "fix(core): first")
	loaded, err := config.Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	selected, err := New(root, loaded, zerolog.Nop()).selectedPlan(t.Context(), ReleaseOptions{})
	require.NoError(t, err)
	compiled, err := (&release.Executor{PublishGroup: publishByRepository}).CompileTaskGraph(selected)
	require.NoError(t, err)
	var testShape, lintShape *release.TaskShape
	for _, shape := range compiled.TaskShapes() {
		copy := shape
		switch shape.Stage {
		case "test":
			testShape = &copy
		case "lint":
			lintShape = &copy
		}
	}
	require.NotNil(t, testShape)
	require.NotNil(t, lintShape)
	require.False(t, testShape.FirstOnly, "parallel test unexpectedly requires first-node execution: %+v", testShape)
	require.False(t, lintShape.FirstOnly, "parallel lint unexpectedly requires first-node execution: %+v", lintShape)
	require.False(t, slices.Contains(testShape.Predecessors, lintShape.ID),
		"test unexpectedly waits for parallel lint")
	require.False(t, slices.Contains(lintShape.Predecessors, testShape.ID),
		"lint unexpectedly waits for parallel test")
	require.Equal(t, testShape.Predecessors, lintShape.Predecessors,
		"parallel stages are not simultaneously ready: test=%+v lint=%+v", testShape, lintShape)
	specs, err := releaseChoreographySpecs(selected, compiled, []execution.ChoreographyRuntimeNode{
		{Name: "first", Capacity: 1, First: true}, {Name: "peer", Capacity: 1},
	})
	require.NoError(t, err)
	for _, spec := range specs {
		if spec.Task == testShape.ID || spec.Task == lintShape.ID {
			require.ElementsMatch(t, []string{"first", "peer"}, spec.EligibleNodes,
				"parallel stage has unexpected eligibility: %+v", spec)
		}
	}
}

func TestPreparedChoreographySpecsUseExactCompiledConsumerID(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte(`{
	  "scripts":{"prelude":"true","build":"true"},
	  "spaces":{"libs":{"path":["packages"],"flow":{"beforeAll":["prelude"],"build":["build"]}}}
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "package.json"),
		[]byte(`{"name":"core","version":"0.0.0"}`), 0o644))
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.name", "Test")
	fixtureGit(t, root, "config", "user.email", "test@example.com")
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-qm", "fix(core): first")
	loaded, err := config.Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	selected, err := New(root, loaded, zerolog.Nop()).selectedPlan(t.Context(), ReleaseOptions{})
	require.NoError(t, err)
	compiled, err := (&release.Executor{}).CompileTaskGraph(selected)
	require.NoError(t, err)
	core := selected.Releases["core"]
	require.NotNil(t, core)
	provider := &plan.Release{Pkg: &model.Package{Name: "assets", Space: core.Pkg.Space, BuildWeight: 1}}
	selected.Releases["assets"] = provider
	roster := []execution.ChoreographyRuntimeNode{{Name: "first", Capacity: 2, First: true}}
	baseDigest := compiled.Digest()
	prepared := []execution.ChoreographyPreparedTask{{
		ReservationTask: execution.ReservationTask{Task: "assets:prepare", Kind: execution.KindPrepare,
			Placement: execution.PlacementOrchestrator},
		Package: "assets", Release: provider, Weight: 1,
		Consumers: []execution.ChoreographyPreparedConsumer{{Package: "core", Stage: "build"}},
	}}
	require.NoError(t, includeDiscoveredPreparedReleaseGraph(compiled, prepared))
	require.NotEqual(t, baseDigest, compiled.Digest())
	extended, err := releaseChoreographySpecs(selected, compiled, roster)
	require.NoError(t, err)
	var buildID, beforeAllID string
	for _, shape := range compiled.TaskShapes() {
		if shape.Package == "core" && shape.Kind == "build" {
			buildID = shape.ID
		} else if shape.Package == "core" && shape.Kind == "beforeAll" {
			beforeAllID = shape.ID
		}
	}
	require.NotEmpty(t, buildID)
	require.NotEmpty(t, beforeAllID)
	found := false
	for _, spec := range extended {
		if spec.Task == beforeAllID {
			require.NotContains(t, spec.Predecessors, "assets:prepare",
				"the hook reports under build but is not the prepared consumer frame")
		}
		if spec.Task == buildID {
			require.Contains(t, spec.Predecessors, "assets:prepare")
			found = true
		}
	}
	require.True(t, found, "compiled build task %q is absent", buildID)
	require.NotNil(t, compiled.PreparedTaskSpecs()[0].Release,
		"broker eligibility uses the compiler's captured provider policy")
}
