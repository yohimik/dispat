package integration

// Regression for test-plan goals 18 and 21: deep revision windows keep their
// exact lower bound, `all` removes it, and consumer expansion remains
// transitive after the ninth commit rather than stopping at the first edge.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yohimik/dispat/pkg/models/v2"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestSelectionWindowsFromHEAD1ThroughHEAD9(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	names := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "web"}
	for _, name := range names {
		r.SeedPackage("packages", name)
	}
	for i := 1; i < len(names); i++ {
		cfg.Dependencies = append(cfg.Dependencies, models.DependencyConfig{Consumer: names[i], Provider: names[i-1]})
	}
	r.WriteConfigModel(cfg)
	r.Commit("chore: seed deep selection graph")
	for i := 1; i <= 9; i++ {
		name := fmt.Sprintf("p%d", i)
		r.WriteFile("packages/"+name+"/change.txt", fmt.Sprintf("%d\n", i))
		r.Commit("fix(" + name + "): revision window marker")
	}
	for depth := 1; depth <= 9; depth++ {
		rev := fmt.Sprintf("HEAD~%d", depth)
		assert.Equal(t, names[9-depth:9], forWindow(t, r, "--since", rev), "depth %d", depth)
	}
	assert.Equal(t, names, forWindow(t, r, "--since", "all"))
	assert.Equal(t, []string{"web"}, forWindow(t, r, "--since", "HEAD~9", "--package", "web", "--consumers"),
		"consumer expansion reaches the transitive web leaf before the filter narrows to it")
}

func TestRunSceneFixtureSelection(t *testing.T) {
	var fixture struct {
		Commit     string                                         `json:"commit"`
		Batches    [][]string                                     `json:"batches"`
		Selected   []string                                       `json:"selected"`
		Unselected []string                                       `json:"unselected"`
		Outcomes   struct{ OK, Failed, Unselected, Released int } `json:"outcomes"`
	}
	_, source, _, ok := runtime.Caller(0)
	assert.True(t, ok)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "packages", "docs", "demo", "fixtures", "run", "expected.json"))
	if !assert.NoError(t, err) || !assert.NoError(t, json.Unmarshal(b, &fixture)) {
		return
	}
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 2)
	cfg.Scripts["tests"] = models.Script{"echo $DISPAT_PACKAGE >> ../../run-scene.log"}
	all := append(append([]string(nil), fixture.Selected...), fixture.Unselected...)
	for _, name := range all {
		r.SeedPackage("packages", name)
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "api", Provider: "core"}, {Consumer: "api", Provider: "utils"},
		{Consumer: "web", Provider: "api"}, {Consumer: "sdk", Provider: "utils"},
	}
	r.WriteConfigModel(cfg)
	r.Commit("chore: seed Run scene")
	r.WriteFile("packages/utils/fix.txt", "closed\n")
	r.Commit(fixture.Commit)
	res := r.RunScript("tests", "--since", "HEAD~1", "--consumers")
	assert.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.ElementsMatch(t, fixture.Selected, logged(r, "run-scene.log"))
	assert.Equal(t, fixture.Outcomes.OK, len(logged(r, "run-scene.log")))
	assert.Equal(t, []string{"utils"}, fixture.Batches[0])
	assert.Equal(t, []string{"web"}, fixture.Batches[2])
}

// This mirrors the package names used by the CI test image. It exercises the
// current integration-test binary, so a selection regression cannot be hidden
// by a previously installed dispat executable.
func TestCITestModuleSelectionRunsAffectedOnceAndAllRunsEveryModule(t *testing.T) {
	r := harness.New(t)
	names := []string{"ccme", "config", "manifest", "models", "scanner", "writer", "tools", "dispat", "integration"}
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"tests": {`echo "$DISPAT_PACKAGE" >> "$(git rev-parse --show-toplevel)/selected.log"`},
	}
	cfg.Packages = make(map[string]models.PackageConfig, len(names))
	for _, name := range names {
		path := "modules/" + name
		cfg.Packages[name] = models.PackageConfig{Path: path}
		r.WriteFile(path+"/source.txt", "seed\n")
	}
	r.WriteConfigModel(cfg)
	r.Commit("chore: seed CI modules")

	r.WriteFile("modules/integration/source.txt", "changed\n")
	r.Commit("test(integration): change one module")
	r.RunScriptOK("tests", "--since", "HEAD~1", "--consumers")
	assert.Equal(t, []string{"integration"}, logged(r, "selected.log"))

	reset(r, "selected.log")
	r.RunScriptOK("tests", "--since", "all")
	selected := logged(r, "selected.log")
	assert.Len(t, selected, len(names))
	assert.ElementsMatch(t, names, selected)
}
