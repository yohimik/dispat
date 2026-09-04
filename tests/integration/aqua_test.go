package integration

// Aqua through the compiled binary. These scenarios extend test-plan goals
// 23–26: manifest discovery and writing cross the process boundary, compute
// consumes the same identity index, and autoversion writes Aqua's exact pin.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

type aquaDemoExpected struct {
	Commands         []string          `json:"commands"`
	SelectedPackages []string          `json:"selectedPackages"`
	VersionsBefore   map[string]string `json:"versionsBefore"`
	VersionsAfter    map[string]string `json:"versionsAfter"`
	Outcomes         struct {
		Applied        int `json:"applied"`
		SkippedDynamic int `json:"skippedDynamic"`
		Missing        int `json:"missing"`
	} `json:"outcomes"`
}

func aquaFixture(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(file), "..", "..", "packages", "docs", "demo", "fixtures", "aqua", name)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func TestAquaDemoFixtureThroughManifestCommands(t *testing.T) {
	r := harness.New(t)
	r.WriteFile(".aqua/aqua.yaml", string(aquaFixture(t, "aqua.yaml")))
	r.WriteFile(".aqua/tools.inc", string(aquaFixture(t, "tools.inc")))
	var expected aquaDemoExpected
	require.NoError(t, json.Unmarshal(aquaFixture(t, "expected.json"), &expected))
	require.Len(t, expected.Commands, 2, "the animation and this test share both commands")

	scan := r.Command("scanner", ".", "--root-only", "--log-format", "json")
	require.Equal(t, 0, scan.Code, "stdout:\n%s\nstderr:\n%s", scan.Stdout, scan.Stderr)
	seen := map[string]string{}
	for _, event := range scan.Events {
		deps, _ := event["deps"].([]any)
		for _, raw := range deps {
			dep := raw.(map[string]any)
			seen[dep["name"].(string)] = dep["range"].(string)
		}
	}
	for _, name := range expected.SelectedPackages {
		assert.Equal(t, expected.VersionsBefore[name], seen[name])
	}
	assert.Contains(t, scan.Stdout, "version_expr is dynamic and was not evaluated")

	write := r.Command("writer", ".aqua/tools.inc", "--manifest-format", "aqua",
		"--set", "cli/cli="+expected.VersionsAfter["cli/cli"], "--log-format", "json")
	require.Equal(t, 0, write.Code, "stdout:\n%s\nstderr:\n%s", write.Stdout, write.Stderr)
	assert.Equal(t, float64(expected.Outcomes.Applied), findEvent(t, write.Events, "write complete")["applied"])
	updated, err := os.ReadFile(r.Path(".aqua", "tools.inc"))
	require.NoError(t, err)
	assert.Contains(t, string(updated), "cli/cli@v2.1.0 # inline pins keep their comment")
	assert.Contains(t, string(updated), "version_expr: env.DYNAMIC_VERSION", "dynamic input is untouched")

	// A malformed sibling demonstrates the partial-result contract without
	// changing either healthy source. Strict turns the same report into exit 1.
	r.WriteFile("broken/aqua.yaml", "packages: [")
	partial := r.Command("scanner", ".", "--log-format", "json")
	assert.Equal(t, 0, partial.Code)
	assert.Equal(t, float64(1), findEvent(t, partial.Events, "scan complete")["failed"])
	assert.Equal(t, 1, r.Command("scanner", ".", "--strict").Code)
}

func TestAquaComputeAndAutoversionUseQualifiedOwnership(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"}, Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{Range: "caret"},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"core": {ManifestNames: []string{"corp:acme/core"}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/.aqua/aqua.yaml", "packages:\n- import: pins.inc\n")
	r.WriteFile("packages/web/.aqua/pins.inc", "packages:\n- name: acme/core@0.0.0\n  registry: corp\n")
	r.Commit("feat(core,web): bootstrap")

	compute := r.Command("compute")
	require.Equal(t, 0, compute.Code, "stdout:\n%s\nstderr:\n%s", compute.Stdout, compute.Stderr)
	assert.Contains(t, compute.Stdout, "web -> core", "qualified manifestNames resolves the Aqua identity")
	require.Equal(t, 0, r.Command("compute", "--write").Code)

	auto := r.Command("autoversion", "--sync-lock=false")
	require.Equal(t, 0, auto.Code, "stdout:\n%s\nstderr:\n%s", auto.Stdout, auto.Stderr)
	b, err := os.ReadFile(r.Path("packages", "web", ".aqua", "pins.inc"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "acme/core@0.1.0", "caret policy still writes an exact Aqua pin")
	assert.NotContains(t, string(b), "^0.1.0")
}
