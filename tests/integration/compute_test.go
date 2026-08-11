package integration

// Area 10: the compute command through the compiled binary. compute derives
// the dependency graph from real manifests on disk and edits the config
// file; what must hold over the process boundary is the full loop — scan,
// suggest, gate CI, apply, back up — and that the very next status consumes
// the edges compute wrote.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestComputeDetectApplyStatus is the whole loop: two npm packages whose
// manifests declare a workspace dependency, `compute` previews the addition
// without writing, `--check` fails the CI gate, `--write` applies it (saving
// the previous config), the next `status` orders the graph by the new edge,
// and the gate passes.
func TestComputeDetectApplyStatus(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("feat(core,web): bootstrap")

	// Preview: the suggestion is printed, nothing changes.
	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "+ add     web -> core (dependencies)")
	assert.Contains(t, res.Stdout, `packages/web/package.json dependencies "@acme/core": "workspace:*"`)
	configBefore, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(configBefore), `"consumer"`, "preview writes nothing")

	// The CI gate fails while the config lags the manifests.
	assert.Equal(t, 1, r.Command("compute", "--check").Code)

	// Apply: the edge lands in the config, the previous copy is saved.
	res = r.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "applied 1 change(s)")
	configAfter, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(configAfter), `"provider": "core"`)
	backup, err := os.ReadFile(r.Path("dispat.json.backup"))
	require.NoError(t, err)
	assert.Equal(t, string(configBefore), string(backup), "backup is the pre-edit config")

	// The next status consumes the edge; the gate is green.
	status := r.StatusOK()
	dependsOn := ""
	for _, e := range status.Events {
		if e.Package() == "web" {
			if list, ok := e["dependsOn"].([]any); ok {
				parts := make([]string, len(list))
				for i, v := range list {
					parts[i], _ = v.(string)
				}
				dependsOn = strings.Join(parts, ",")
			}
		}
	}
	assert.Contains(t, dependsOn, "core", "the computed edge orders the graph")
	assert.Equal(t, 0, r.Command("compute", "--check").Code)
}

// TestComputeKeepAndRemoval: a declared edge no manifest supports is
// suggested for removal — unless it carries keep: true, the escape hatch for
// deliberate non-manifest coupling (a Docker chain). The kept edge survives
// --write byte-identically.
func TestComputeKeepAndRemoval(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "web", Provider: "ghost"},
		{Consumer: "web", Provider: "core", Keep: true},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "ghost")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web"}`)
	r.Commit("feat(core,ghost,web): bootstrap")

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "- remove  web -> ghost")
	assert.NotContains(t, res.Stdout, "web -> core", "keep: true silences the removal")

	require.Equal(t, 0, r.Command("compute", "--write").Code)
	configAfter, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(configAfter), "ghost")
	assert.Contains(t, string(configAfter), `"keep": true`)

	// With the stale edge gone the config loads and status still runs.
	r.StatusOK()
}

// TestComputeAmbiguousNameReportsW220 through the binary: two packages
// declaring the same manifest name derive no edges, and the ambiguity reaches
// the JSON events as W220.
func TestComputeAmbiguousNameReportsW220(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{"build": echoBuild}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{Build: []string{"build"}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.WriteFile("packages/a/package.json", `{"name": "@acme/same"}`)
	r.WriteFile("packages/b/package.json", `{"name": "@acme/same"}`)
	r.Commit("feat(a,b): two packages, one manifest name")

	res := r.Command("compute", "--check")
	assert.Zero(t, res.Code, "an ambiguous name derives nothing: no drift, exit 0; stdout:\n%s", res.Stdout)
	assert.True(t, harness.HasCode(res.Events, "W220"),
		"the ambiguity must reach the events: %s", res.Stdout)
}

// TestComputeEditsSpaceLayerDeclarations: an edge declared in a space's
// packages entry lives in the root config, and one declared in a space
// folder's file lives in that file. `compute --write` removes each stale
// edge where it was written, names the source in the listing, and backs up
// only the files it touched.
func TestComputeEditsSpaceLayerDeclarations(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{"build": echoBuild, "publish": "echo publishing"}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(), Packages: map[string]models.PackageConfig{
			"web": {Dependencies: []string{"ghost"}},
		}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "ghost")
	r.SeedPackage("packages", "web")
	spaceFile(t, r, "packages", models.SpaceFile{
		Packages: map[string]models.PackageConfig{"core": {Dependencies: []string{"ghost"}}},
	})
	// Both consumers carry a manifest, so their declarations are judged
	// against something: an edge no manifest declares is stale.
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web"}`)
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core"}`)
	r.Commit("feat(core,ghost,web): bootstrap")

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, `spaces["libs"]: packages["web"]: dependencies[0]`,
		"the listing names the space entry that holds the edge")
	assert.Contains(t, res.Stdout, `packages["core"]: dependencies[0]`,
		"and the space file that holds the other")

	require.Equal(t, 0, r.Command("compute", "--write").Code)

	rootAfter, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(rootAfter), "ghost", "the space entry's edge is gone from the root config")
	spaceAfter, err := os.ReadFile(r.Path("packages", "dispat.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(spaceAfter), "ghost", "and the space file's edge from the space file")
	assert.FileExists(t, r.Path("packages", "dispat.json.backup"), "each edited file keeps its own backup")

	// Both edits together still load, and the packages survive them.
	r.StatusOK()
}
