package config

// The flow beyond its script lists: the object form read through the real
// loader, the ladder replacing a stage entry whole, the stages a flow defines
// resolved onto the space with their graph, and a configuration no order
// realises refused with its code once the workspace is read.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// flowStagesConfig is stagesConfig with the scripts the user stages below
// name.
func flowStagesConfig() File {
	cfg := stagesConfig()
	cfg.Scripts["lint"] = Script{"make lint"}
	cfg.Scripts["pre-lint"] = Script{"echo pre-lint"}
	cfg.Scripts["post-lint"] = Script{"echo post-lint"}
	cfg.Scripts["announce"] = Script{"echo announce"}
	return cfg
}

// loadRawFlow loads a configuration whose core package writes the given flow
// object exactly as a file would, and answers the discovered packages or the
// refusal.
func loadRawFlow(t *testing.T, flow map[string]any) ([]*model.Package, error) {
	t.Helper()
	cfg := flowStagesConfig()
	cfg.Spaces["libs"] = SpaceConfig{Path: PathList{"packages/libs"}}
	root := writeModelRepo(t, cfg, propagateDirs...)
	writePackageRaw(t, root, "packages/libs/core", map[string]any{"flow": flow})
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	if err != nil {
		return nil, err
	}
	pkgs, _, _, err := DiscoverPackages(loaded, root)
	return pkgs, err
}

// coreOf answers core's resolved space.
func coreOf(t *testing.T, pkgs []*model.Package) *model.Space {
	t.Helper()
	return packagesByName(pkgs)["core"].Space
}

// stageNodes lists a graph's nodes in order, the record included.
func stageNodes(graph model.StageGraph) []string {
	var names []string
	for _, node := range graph.Nodes {
		names = append(names, node.Name)
	}
	return names
}

// TestFlowObjectFormResolvesOntoTheSpace: a stage written as an object keeps
// its scripts in the typed field and gives the space its label and its place
// in the graph; a defined stage resolves to commands with its hooks.
func TestFlowObjectFormResolvesOntoTheSpace(t *testing.T) {
	pkgs, err := loadRawFlow(t, map[string]any{
		"build":      "build",
		"publish":    map[string]any{"script": "publish", "label": "Ship it", "after": "build"},
		"announce":   map[string]any{"script": "announce", "after": "build"},
		"lint":       map[string]any{"script": "lint", "label": "Lint", "parallel": "announce"},
		"beforeLint": "pre-lint",
		"postLint":   []any{"post-lint"},
		"docs":       "report",
	})
	require.NoError(t, err)
	require.NoError(t, CheckStageGraphs(pkgs))
	space := coreOf(t, pkgs)
	assert.Equal(t, []string{"echo publish"}, space.PublishScript)
	assert.Equal(t, []string{"echo announce"}, space.AnnounceScript)
	assert.Equal(t, map[string]string{"publish": "Ship it", "lint": "Lint"}, space.StageLabels)
	assert.Equal(t, []model.UserStage{
		{Name: "docs", Commands: []string{"collect-reports"}},
		{Name: "lint", Commands: []string{"make lint"}, BeforeHook: []string{"echo pre-lint"}, PostHook: []string{"echo post-lint"}},
	}, space.UserStages)
	graph := space.StageGraph
	assert.Equal(t, []string{"version", "build", "docs", "lint", "publish", "record", "announce"}, stageNodes(graph),
		"the stage order, the record right after the publish")
	assert.True(t, graph.IsBefore("build", "announce") && graph.IsBefore("announce", "publish"),
		"the announce moved before a constrained publish gates")
	assert.False(t, graph.IsBefore("lint", "announce") || graph.IsBefore("announce", "lint"), "parallel stages stay unordered")
	assert.True(t, graph.IsBefore("docs", "publish"), "the publication edge keeps a user stage before a placed publish")
	assert.Empty(t, space.StageRefusals)
}

func TestFlowConstraintOnlyUserStageKeepsItsOrderingNode(t *testing.T) {
	pkgs, err := loadRawFlow(t, map[string]any{
		"build": "build",
		"lint":  map[string]any{"before": "build"},
	})
	require.NoError(t, err)
	require.NoError(t, CheckStageGraphs(pkgs))
	space := coreOf(t, pkgs)
	assert.Equal(t, []model.UserStage{{Name: "lint"}}, space.UserStages)
	assert.True(t, space.StageGraph.IsNode("lint"))
	assert.True(t, space.StageGraph.IsBefore("lint", "build"))
}

// TestFlowUserStageScriptsAreCheckedLikeTheOthers: a defined stage or its
// hook naming a script nobody declared is refused by the entry's own name.
func TestFlowUserStageScriptsAreCheckedLikeTheOthers(t *testing.T) {
	_, err := loadRawFlow(t, map[string]any{"build": "build", "lint": "nothing-of-the-kind"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `flow.lint references unknown script "nothing-of-the-kind"`)
	_, err = loadRawFlow(t, map[string]any{"build": "build", "beforeLint": "nothing-of-the-kind"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `flow.beforeLint references unknown script`)
}

// TestFlowGraphRefusalsCarryTheirCode: a configuration no order realises is
// refused once the workspace is read, with the condition's code and the
// execution-configuration identifier, naming the package and the entry.
func TestFlowGraphRefusalsCarryTheirCode(t *testing.T) {
	for name, row := range map[string]struct {
		flow map[string]any
		code string
		want string
	}{
		"a user stage the package does not define": {map[string]any{"build": "build",
			"lint": map[string]any{"script": "lint", "after": "docs"}}, model.CodeStageUnknown, `flow.lint.after names "docs"`},
		"the record": {map[string]any{"build": "build", "lint": map[string]any{"script": "lint", "before": "record"}},
			model.CodeStageUnknown, `flow.lint.before names "record"`},
		"the stage itself": {map[string]any{"build": "build", "lint": map[string]any{"script": "lint", "parallel": "lint"}},
			model.CodeStageUnknown, "flow.lint.parallel names the stage itself"},
		"before and after naming one stage": {map[string]any{"build": "build", "publish": "publish",
			"lint": map[string]any{"script": "lint", "before": "build", "after": "build"}}, model.CodeStageCycle, "lie on a cycle"},
		"a cycle": {map[string]any{"build": "build",
			"lint": map[string]any{"script": "lint", "after": "docs"},
			"docs": map[string]any{"script": "report", "after": "lint"}}, model.CodeStageCycle, "docs, lint lie on a cycle"},
		"a preparing stage moved": {map[string]any{"build": "build",
			"version": map[string]any{"script": "sync", "after": "build"}}, model.CodeStageCycle, "version, build lie on a cycle"},
		"a parallel pair the edges relate": {map[string]any{"build": "build", "publish": "publish",
			"lint": map[string]any{"script": "lint", "parallel": "publish"}}, model.CodeStageParallelRelated, "lint and publish as parallel"},
		"a stage beside a placed publish": {map[string]any{"build": "build",
			"publish": map[string]any{"script": "publish", "after": "build"},
			"lint":    map[string]any{"script": "lint", "parallel": "publish"}}, model.CodeStageParallelRelated,
			"place lint after the publish"},
	} {
		t.Run(name, func(t *testing.T) {
			pkgs, err := loadRawFlow(t, row.flow)
			require.NoError(t, err, "the refusal waits until the workspace is read")
			err = CheckStageGraphs(pkgs)
			require.Error(t, err)
			assert.Equal(t, row.code, DiagnosticCode(err))
			var categorized interface{ DiagnosticCategory() string }
			require.ErrorAs(t, err, &categorized)
			assert.Equal(t, "execution-configuration", categorized.DiagnosticCategory(), "the refusal's stable identifier")
			assert.Contains(t, err.Error(), `package "core"`)
			assert.Contains(t, err.Error(), row.want)
		})
	}
}

// TestFlowConstraintOnAMissingBuiltInStageStatesNothing: a constraint naming
// a built-in stage the package does not run is dropped, so one shared flow
// fits packages with and without that stage, and the stage keeps or loses its
// default place by what remains.
func TestFlowConstraintOnAMissingBuiltInStageStatesNothing(t *testing.T) {
	pkgs, err := loadRawFlow(t, map[string]any{"build": "build",
		"lint": map[string]any{"script": "lint", "after": "tests", "before": "publish", "parallel": "announce"}})
	require.NoError(t, err)
	require.NoError(t, CheckStageGraphs(pkgs))
	graph := coreOf(t, pkgs).StageGraph
	assert.True(t, graph.IsBefore("build", "lint"), "the lint keeps its default place")
	assert.True(t, graph.IsBefore("lint", model.RecordStage))

	pkgs, err = loadRawFlow(t, map[string]any{"build": "build", "tests": "check",
		"lint": map[string]any{"script": "lint", "after": "tests", "before": "publish"}})
	require.NoError(t, err)
	require.NoError(t, CheckStageGraphs(pkgs))
	graph = coreOf(t, pkgs).StageGraph
	assert.Equal(t, []string{"version", "tests"}, graph.Predecessors("lint"),
		"the remaining constraint places the lint after the tests, in place of its default edge from the build")
}

func TestExplicitCommandlessPublishKeepsItsStageConstraints(t *testing.T) {
	pkgs, err := loadRawFlow(t, map[string]any{
		"build":   "build",
		"publish": map[string]any{"after": "lint"},
		"lint":    map[string]any{"script": "lint"},
	})
	require.NoError(t, err)
	require.NoError(t, CheckStageGraphs(pkgs))
	graph := coreOf(t, pkgs).StageGraph
	assert.True(t, graph.IsNode(public.FlowStagePublish))
	assert.True(t, graph.IsBefore("lint", public.FlowStagePublish))
	assert.True(t, graph.IsBefore(public.FlowStagePublish, model.RecordStage))
}

// TestFlowGraphRefusalsAreOrderedByPackage: every package's diagnostics are
// reported together, by package name and then in the order the resolution
// gives them.
func TestFlowGraphRefusalsAreOrderedByPackage(t *testing.T) {
	cfg := flowStagesConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Flow = &SpaceFlowConfig{Build: []string{"build"},
			Stages: map[string]public.StageConfig{"lint": {Script: []string{"lint"}, After: []string{"ghost"}}}}
	})
	cfg.Packages = map[string]PackageConfig{"core": {Flow: &SpaceFlowConfig{
		Stages: map[string]public.StageConfig{"lint": {Script: []string{"lint"}, After: []string{"docs"}},
			"docs": {Script: []string{"report"}, After: []string{"lint"}}}}}}
	pkgs, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
	require.NoError(t, err)
	err = CheckStageGraphs(pkgs)
	require.Error(t, err)
	var refused *StageGraphError
	require.ErrorAs(t, err, &refused)
	var order []string
	for _, d := range refused.Diagnostics {
		order = append(order, d.Package+":"+d.Code)
	}
	assert.Equal(t, []string{"core:" + model.CodeStageCycle, "utils:" + model.CodeStageUnknown}, order)
	assert.Equal(t, model.CodeStageCycle, DiagnosticCode(err), "the first diagnostic's code is the error's")
}

// TestFlowLadderReplacesAStageEntryWhole: a nearer level that states a stage
// entry in either form replaces the inherited entry whole, scripts, label and
// constraints alike, and keeps the entries it does not state; a defined
// stage's hooks are entries of their own.
func TestFlowLadderReplacesAStageEntryWhole(t *testing.T) {
	cfg := flowStagesConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Flow = &SpaceFlowConfig{
			Build: []string{"build"}, Publish: []string{"publish"}, Announce: []string{"announce"},
			Stages: map[string]public.StageConfig{
				"build":    {Label: "Make"},
				"announce": {Label: "Tell", After: []string{"build"}},
				"lint":     {Script: []string{"lint"}, Before: []string{"build"}},
			},
			StageHooks: map[string][]string{"beforeLint": {"pre-lint"}, "postLint": {"post-lint"}},
		}
	})
	cfg.Packages = map[string]PackageConfig{
		"core": {Flow: &SpaceFlowConfig{
			Announce: []string{"report"},
			Stages: map[string]public.StageConfig{
				"lint":  {After: []string{"build"}},
				"build": {Label: "Compile"},
			},
			StageHooks: map[string][]string{"postLint": {}},
		}},
	}
	pkgs, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
	require.NoError(t, err)
	require.NoError(t, CheckStageGraphs(pkgs))
	byName := packagesByName(pkgs)

	core, utils := byName["core"].Space, byName["utils"].Space
	assert.Equal(t, []string{"collect-reports"}, core.AnnounceScript, "the list replaced the entry")
	assert.Equal(t, map[string]string{"build": "Compile"}, core.StageLabels,
		"the restated announce lost its inherited label; the build object added one")
	assert.Nil(t, core.BuildScript, "an object without a script states no script")
	assert.Equal(t, []model.UserStage{{Name: "lint", BeforeHook: []string{"echo pre-lint"}}}, core.UserStages,
		"the restated entry carries no script, and the empty hook cleared the inherited one")
	assert.True(t, core.StageGraph.IsBefore("build", "lint"), "lint takes the place the nearer entry gives it")
	assert.True(t, core.StageGraph.IsBefore("publish", "announce"), "the restated announce is unconstrained again")

	assert.True(t, utils.StageGraph.IsBefore("lint", "build"), "the sibling keeps the space's order")
	assert.True(t, utils.StageGraph.IsBefore("announce", "publish"), "the placed announce lands before the publication")
	assert.Equal(t, map[string]string{"build": "Make", "announce": "Tell"}, utils.StageLabels)
	assert.Equal(t, []model.UserStage{{Name: "lint", Commands: []string{"make lint"},
		BeforeHook: []string{"echo pre-lint"}, PostHook: []string{"echo post-lint"}}}, utils.UserStages)
}

// TestFlowVersionObjectFollowsTheSpelling: the version stage's label is kept
// under the name the stage runs as, a nearer level restating the pair under
// either spelling replaces the inherited entry, and one object stating both
// spellings is refused with the execution-configuration code.
func TestFlowVersionObjectFollowsTheSpelling(t *testing.T) {
	cfg := flowStagesConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Flow = &SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"},
			Propagate: []string{"sync"}, Stages: map[string]public.StageConfig{"propagate": {Label: "Sync"}}}
	})
	cfg.Packages = map[string]PackageConfig{"core": {Flow: &SpaceFlowConfig{Version: []string{"sync"}}}}
	pkgs, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Equal(t, model.StagePropagate, byName["utils"].Space.VersionStage)
	assert.Equal(t, map[string]string{"propagate": "Sync"}, byName["utils"].Space.StageLabels)
	assert.Equal(t, model.StageVersion, byName["core"].Space.VersionStage)
	assert.Nil(t, byName["core"].Space.StageLabels, "the restated pair dropped the inherited label")

	_, err = loadRawFlow(t, map[string]any{"build": "build",
		"version": map[string]any{"label": "a"}, "propagate": map[string]any{"label": "b"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flow.version and flow.propagate are mutually exclusive")
	assert.Equal(t, DiagnosticExecution, DiagnosticCode(err))
}

// TestFlowEveryPackageHasAVersionAndABuild: the graph holds the version stage
// and the build whatever the flow says, the publish only where the flow gives
// it something to do, and a publish with only a label runs nothing.
func TestFlowEveryPackageHasAVersionAndABuild(t *testing.T) {
	pkgs, err := loadRawFlow(t, map[string]any{"publish": map[string]any{"label": "x"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"version", "build"}, coreOf(t, pkgs).StageGraph.Stages())
	assert.Equal(t, []string{"version", "build", "record"}, stageNodes(coreOf(t, pkgs).StageGraph))

	cfg := flowStagesConfig()
	withLibs(&cfg, func(s *SpaceConfig) { s.Flow = &SpaceFlowConfig{} })
	pkgs, err = discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
	require.NoError(t, err)
	assert.Equal(t, []string{"version", "build"}, coreOf(t, pkgs).StageGraph.Stages())
}
