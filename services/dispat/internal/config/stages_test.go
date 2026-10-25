package config

// The names the release stages resolve to. The version stage runs under the
// key that configured it, `propagate` or `version`, and the test stage is
// configured through flow.test and its two hooks on every level that can
// state a flow.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// stagesConfig is validConfig with the scripts the stage entries below name.
func stagesConfig() File {
	cfg := propagateConfig()
	cfg.Scripts["check"] = Script{"make check"}
	cfg.Scripts["report"] = Script{"collect-reports"}
	return cfg
}

// resolveCore discovers the fixture and answers core's resolved space.
func resolveCore(t *testing.T, cfg File) *model.Space {
	t.Helper()
	pkgs, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
	require.NoError(t, err)
	return packagesByName(pkgs)["core"].Space
}

// TestVersionStageIsNamedByTheKeyThatConfiguredIt: the stage entry decides,
// then the native block, then the hooks in the order they run, and a space that
// wrote no propagate key keeps the name the stage always had.
func TestVersionStageIsNamedByTheKeyThatConfiguredIt(t *testing.T) {
	for name, row := range map[string]struct {
		mutate func(*SpaceConfig)
		want   model.VersionStage
	}{
		"nothing written": {func(*SpaceConfig) {}, model.StageVersion},
		"flow.version":    {func(s *SpaceConfig) { s.Flow.Version = []string{"sync"} }, model.StageVersion},
		"flow.propagate":  {func(s *SpaceConfig) { s.Flow.Propagate = []string{"sync"} }, model.StagePropagate},
		"autoVersion":     {func(s *SpaceConfig) { s.AutoVersion = &AutoVersionConfig{Range: "exact"} }, model.StageVersion},
		"autoPropagate":   {func(s *SpaceConfig) { s.AutoPropagate = &AutoVersionConfig{Range: "exact"} }, model.StagePropagate},
		"a beforePropagate hook alone": {func(s *SpaceConfig) {
			s.Flow.BeforePropagate = []string{"pre"}
		}, model.StagePropagate},
		"a postPropagate hook alone": {func(s *SpaceConfig) {
			s.Flow.PostPropagate = []string{"post"}
		}, model.StagePropagate},
		"the entry outranks the block": {func(s *SpaceConfig) {
			s.Flow.Version = []string{"sync"}
			s.AutoPropagate = &AutoVersionConfig{Range: "exact"}
		}, model.StageVersion},
		"the block outranks the hooks": {func(s *SpaceConfig) {
			s.AutoPropagate = &AutoVersionConfig{Range: "exact"}
			s.Flow.BeforeVersion = []string{"pre"}
		}, model.StagePropagate},
		"the entry outranks a hook of the other family": {func(s *SpaceConfig) {
			s.Flow.Version = []string{"sync"}
			s.Flow.BeforePropagate = []string{"pre"}
		}, model.StageVersion},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := stagesConfig()
			withLibs(&cfg, row.mutate)
			assert.Equal(t, row.want, resolveCore(t, cfg).VersionStage)
		})
	}
}

// TestVersionStageFollowsTheNearestLayer: the name is read from the ladder's
// result, so a package that restates the entry under the other key runs under
// that key's name while its sibling keeps the space's.
func TestVersionStageFollowsTheNearestLayer(t *testing.T) {
	cfg := stagesConfig()
	withLibs(&cfg, func(s *SpaceConfig) { s.Flow.Propagate = []string{"sync"} })
	cfg.Packages = map[string]PackageConfig{"core": {Flow: &SpaceFlowConfig{Version: []string{"sync"}}}}
	pkgs, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Equal(t, model.StageVersion, byName["core"].Space.VersionStage)
	assert.Equal(t, model.StagePropagate, byName["utils"].Space.VersionStage)
}

// TestTestStageResolvesLikeTheBuild: flow.test and its hooks resolve to
// commands, a nearer layer replaces what a farther one stated, and an entry
// the layer does not name is inherited.
func TestTestStageResolvesLikeTheBuild(t *testing.T) {
	cfg := stagesConfig()
	cfg.Flow = &SpaceFlowConfig{PostTest: []string{"report"}}
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Flow.Test = []string{"check"}
		s.Flow.BeforeTest = []string{"pre"}
	})
	cfg.Packages = map[string]PackageConfig{"core": {Flow: &SpaceFlowConfig{Test: []string{"sync"}}}}
	pkgs, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	core, utils, app := byName["core"].Space, byName["utils"].Space, byName["app"].Space
	assert.Equal(t, []string{"echo sync"}, core.TestScript, "the package's entry replaces the space's")
	assert.Equal(t, []string{"echo pre"}, core.BeforeTestScript, "a hook the package does not name is inherited")
	assert.Equal(t, []string{"collect-reports"}, core.PostTestScript, "the root's hook reaches every space")
	assert.Equal(t, []string{"make check"}, utils.TestScript)
	assert.Empty(t, app.TestScript, "a space that states no test stage has none")
	assert.Equal(t, []string{"collect-reports"}, app.PostTestScript)
}

// TestTestStageNamesAnUnknownScript: a test entry naming no script is the
// same load error a build entry naming no script is.
func TestTestStageNamesAnUnknownScript(t *testing.T) {
	cfg := stagesConfig()
	withLibs(&cfg, func(s *SpaceConfig) { s.Flow.Test = []string{"missing"} })
	_, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flow.test")
	assert.Contains(t, err.Error(), "missing")
}

// TestTestStageKeysReadFromAFile: the keys are matched like every other flow
// key, so a file spelling them in any case loads them.
func TestTestStageKeysReadFromAFile(t *testing.T) {
	root := writeRawFile(t, `{
  "scripts": {"check": "make check", "pre": "echo pre", "post": "echo post"},
  "spaces": {"libs": {"path": "packages/libs",
    "flow": {"test": "check", "beforeTest": "pre", "PostTest": "post"}}}
}`, "packages/libs/core")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	core := packagesByName(pkgs)["core"].Space
	assert.Equal(t, []string{"make check"}, core.TestScript)
	assert.Equal(t, []string{"echo pre"}, core.BeforeTestScript)
	assert.Equal(t, []string{"echo post"}, core.PostTestScript)
}
