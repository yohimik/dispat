// Goal 22: dependency edges declared by a space. A space states the edges
// between its own packages next to the space, in the same object keyed by
// consumer the root file uses, and every declaration merges into one graph —
// so an edge declared here orders a release exactly as one declared at the
// root does. The rule that makes the level worth having is that an edge must
// touch the space it is written in; one that touches neither end is refused
// with the space named, because a reader looking for it would have no space
// to look in.
package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// twoSpaceConfig is one config with two spaces — libs at packages/ and apps
// at services/ — so a cross-space edge has somewhere to cross to.
func twoSpaceConfig() models.File {
	cfg := libsConfig(markerBuild, 1)
	cfg.Spaces["apps"] = models.SpaceConfig{Path: "services", Flow: buildPublish()}
	return cfg
}

// TestSpaceDependenciesOrderTheRelease: an edge a space declares reaches the
// graph, so the provider is released before its consumer and the consumer is
// carried along by the provider's release.
func TestSpaceDependenciesOrderTheRelease(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	libs := cfg.Spaces["libs"]
	libs.Dependencies = models.Dependencies{{Consumer: "web", Provider: "core"}}
	cfg.Spaces["libs"] = libs
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("feat(core,web): bootstrap")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("web@0.1.0"))

	// The consumer says nothing of its own, so its release can only come from
	// the edge the space declared: the caret asks for the provider's bump to
	// travel down the graph, and the graph is what the space stated.
	r.WriteFile("packages/core/api.txt", "changed\n")
	r.Commit("fix(core)^: patch the provider")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("web@0.1.1"), "the consumer was carried along the space's edge")
	assert.Contains(t, res.Stdout, "web")
}

// TestSpaceDependenciesCrossSpaceEdge: an edge with one end in the space is
// exactly what the level is for, and either end may declare it.
func TestSpaceDependenciesCrossSpaceEdge(t *testing.T) {
	r := harness.New(t)
	cfg := twoSpaceConfig()
	cfg.Scripts["build"] = echoBuild
	// The app consumes the library, declared by the library's space.
	libs := cfg.Spaces["libs"]
	libs.Dependencies = models.Dependencies{{Consumer: "app", Provider: "core"}}
	cfg.Spaces["libs"] = libs
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("services", "app")
	r.Commit("feat(core,app): bootstrap")
	r.ReleaseOK()

	r.WriteFile("packages/core/api.txt", "changed\n")
	r.Commit("feat(core)^: a new export")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.2.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("app@0.1.1"), "the consumer in the other space followed")
	assert.Contains(t, res.Stdout, "propagated from core")
}

// TestSpaceDependenciesRefuseAnEdgeItDoesNotTouch: an edge between two
// packages of neither space is refused before anything runs, and the refusal
// says which space it was written in and where it belongs instead.
func TestSpaceDependenciesRefuseAnEdgeItDoesNotTouch(t *testing.T) {
	r := harness.New(t)
	cfg := twoSpaceConfig()
	apps := cfg.Spaces["apps"]
	apps.Dependencies = models.Dependencies{{Consumer: "web", Provider: "core"}}
	cfg.Spaces["apps"] = apps
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.SeedPackage("services", "app")
	r.Commit("feat(core,web,app): bootstrap")

	res := r.Release()
	require.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, `spaces[\"apps\"]: dependencies[\"web\"][0]`)
	assert.Contains(t, res.Stdout, `neither consumer \"web\" nor provider \"core\" is a package of space \"apps\"`)
	assert.Contains(t, res.Stdout, "belongs in the root dependencies object")
	assert.Empty(t, r.TagList(), "a refused config releases nothing")
}

// TestSpaceFileDependencies: the space folder's own config file declares
// edges too, and they add to what the root file's space entry says.
func TestSpaceFileDependencies(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	libs := cfg.Spaces["libs"]
	libs.Dependencies = models.Dependencies{{Consumer: "web", Provider: "core"}}
	cfg.Spaces["libs"] = libs
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.SeedPackage("packages", "web")
	spaceFile(t, r, "packages", models.SpaceFile{
		Dependencies: models.Dependencies{{Consumer: "web", Provider: "utils"}},
	})
	r.Commit("feat(core,utils,web): bootstrap")
	r.ReleaseOK()

	// Only the space file declares web -> utils, so utils moving the consumer
	// proves that declaration reached the graph beside the entry's.
	r.WriteFile("packages/utils/helper.txt", "changed\n")
	r.Commit("fix(utils)^: patch")
	r.ReleaseOK()
	assert.True(t, r.HasTag("utils@0.1.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("web@0.1.1"), "carried along the space file's edge")
	assert.False(t, r.HasTag("core@0.1.1"), "and nothing else moved")
}

// TestSpaceDependenciesComputeEditsThemInPlace: `dispat compute --write`
// corrects an edge where it was written, so a space's object stays the
// space's, and sends a new edge to the root object instead — which of the two
// spaces may hold a given edge is the author's call, not compute's.
func TestSpaceDependenciesComputeEditsThemInPlace(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	libs := cfg.Spaces["libs"]
	libs.Dependencies = models.Dependencies{
		{Consumer: "web", Provider: "core", Kind: "devDependencies"},
		{Consumer: "web", Provider: "ghost"},
	}
	cfg.Spaces["libs"] = libs
	r.WriteConfigModel(cfg)
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core"}`)
	r.WriteFile("packages/utils/package.json", `{"name": "@acme/utils"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "^1", "@acme/utils": "^1"}}`)
	r.Commit("feat(core,utils,web): bootstrap")

	res := r.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)

	var written map[string]any
	data, err := os.ReadFile(filepath.Join(r.Root, "dispat.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &written))

	spaces := written["spaces"].(map[string]any)
	space := spaces["libs"].(map[string]any)
	assert.Equal(t, map[string]any{"web": []any{"core"}}, space["dependencies"],
		"the kind was corrected in place and the dead edge dropped, still keyed by consumer")
	assert.Equal(t, map[string]any{"web": []any{"utils"}}, written["dependencies"],
		"the addition went to the root object")

	// The rewritten config is one compute reads back with nothing left to say.
	res = r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "in sync")
}
