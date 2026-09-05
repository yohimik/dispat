package integration

// Area 34: versioning "none". A space with versioning "none" holds packages
// that are never released — no versions, tags, changelogs or publishes —
// and exist to run scripts. They stay in the default `dispat run` window
// whenever they have pending changes (they always do: nothing ever consumes
// their window), they may depend on releasable packages, including through
// permanent local links, and a releasable package must not depend on them.
// These scenarios drive the whole contract through the real binary: the
// release exclusion and its graph line, the run window, the config-load
// rejection of the forbidden edge direction, the link-local suppression,
// explicit selection, an inert Release-As, and inert release-only settings.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// loadError collects every "error" field the run logged, plus stderr — where
// a config refusal lands depends on how far the load got before failing.
func loadError(res harness.RunResult) string {
	parts := []string{res.Stderr}
	for _, e := range res.Events {
		if v := e.Str("error"); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "\n")
}

// noneConfig is the two-space fixture of this area: a releasable "libs" space
// at packages/ and a "tools" space at tools/ with versioning "none", sharing
// one build/publish script pair.
func noneConfig(buildScript string) models.File {
	return spacesConfig(buildScript, map[string]models.SpaceConfig{
		"libs":  {Path: models.PathList{"packages"}, Flow: buildPublish()},
		"tools": {Path: models.PathList{"tools"}, Versioning: models.VersioningNone, Flow: buildPublish()},
	})
}

// TestVersioningNoneLifecycle: a change touching both spaces releases only
// the releasable one. The none package is reported as script-only in the
// graph, gets no tag and no changelog, and a converged second run leaves it
// exactly as script-only again — permanently, because nothing ever consumes
// its window.
func TestVersioningNoneLifecycle(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(noneConfig(echoBuild))
	r.SeedPackage("packages", "core")
	r.SeedPackage("tools", "smoke")

	// Run 1: both packages changed, one release.
	r.Commit("feat(core,smoke): bootstrap both spaces")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("smoke@"), "a none package is never tagged; tags: %v", r.TagList())

	line := harness.GraphLine(res.Events, "smoke")
	assert.Contains(t, line.Str("message"), "script-only (versioning: none)",
		"the graph says why the package is not in the plan")
	assert.NotContains(t, line, "version", "no version transition renders for it")

	_, err := os.Stat(r.Path("tools", "smoke", "CHANGELOG.md"))
	assert.True(t, os.IsNotExist(err), "no changelog is written for a none package")

	// Run 2: converged for the releasable half, script-only forever for the
	// other — with no W193-style noise explaining a release that never comes.
	res = r.ReleaseOK()
	assert.Equal(t, 1, len(r.TagList()), "converged: nothing new to release")
	line = harness.GraphLine(res.Events, "smoke")
	assert.Contains(t, line.Str("message"), "script-only (versioning: none)")
	assert.False(t, harness.HasCode(res.Events, "W193"), "no catch-up diagnostics for a none package")
}

// TestVersioningNoneRunDefaultWindow: the default `dispat run` window is the
// release plan plus every changed none package. A none package therefore runs
// scripts even when nothing is releasing, while an unchanged releasable
// package stays out until it changes again.
func TestVersioningNoneRunDefaultWindow(t *testing.T) {
	r := harness.New(t)
	cfg := noneConfig(echoBuild)
	cfg.Scripts["mark"] = models.Script{"echo ran >> ran.log"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("tools", "smoke")
	r.Commit("feat(core,smoke): bootstrap both spaces")
	r.ReleaseOK()

	// Nothing is releasing, yet the none package is on the window.
	r.RunScriptOK("mark")
	assert.FileExists(t, r.Path("tools", "smoke", "ran.log"),
		"a changed none package runs scripts with no release pending")
	assert.NoFileExists(t, r.Path("packages", "core", "ran.log"),
		"a released, unchanged package is off the window")

	// A new change puts the releasable package back on it; the none package
	// never left.
	r.CommitEmpty("fix(core): core changes again")
	r.RunScriptOK("mark")
	assert.FileExists(t, r.Path("packages", "core", "ran.log"))
	data, err := os.ReadFile(r.Path("tools", "smoke", "ran.log"))
	require.NoError(t, err)
	assert.Equal(t, 2, len(strings.Split(strings.TrimSpace(string(data)), "\n")),
		"the none package ran both times")
}

// TestVersioningNoneProviderEdgeRejected: a releasable package cannot depend
// on a none package — the config fails to load, naming the declaration that
// carries the edge — while the reverse direction and a none-to-none edge are
// ordinary edges.
func TestVersioningNoneProviderEdgeRejected(t *testing.T) {
	r := harness.New(t)
	cfg := noneConfig(echoBuild)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "core", Provider: "smoke"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("tools", "smoke")
	r.Commit("feat(core,smoke): bootstrap")

	res := r.Release()
	require.NotEqual(t, 0, res.Code, "the config must not load")
	err := loadError(res)
	assert.Contains(t, err, `package "core" cannot depend on "smoke"`)
	assert.Contains(t, err, `versioning "none" is never released`)

	// The same edge declared inside the none space's own entry is named by
	// that declaration.
	cfg = noneConfig(echoBuild)
	tools := cfg.Spaces["tools"]
	tools.Dependencies = []models.DependencyConfig{{Consumer: "core", Provider: "smoke"}}
	cfg.Spaces["tools"] = tools
	r.WriteConfigModel(cfg)
	res = r.Release()
	require.NotEqual(t, 0, res.Code)
	err = loadError(res)
	assert.Contains(t, err, `spaces["tools"]`)
	assert.Contains(t, err, `cannot depend on "smoke"`)

	// The allowed directions load and release.
	cfg = noneConfig(echoBuild)
	cfg.Spaces["tools"] = models.SpaceConfig{
		Path: models.PathList{"tools"}, Versioning: models.VersioningNone, Flow: buildPublish(),
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "smoke", Provider: "core"},
		{Consumer: "probe", Provider: "smoke"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("tools", "probe")
	r.Commit("feat(probe): a second none package")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("smoke@"))
	assert.Zero(t, r.TagCount("probe@"))
}

// TestVersioningNoneConsumerWithLocalLink: the state none packages exist
// for — a permanent local link at a releasable provider. Linking only none
// packages raises no "must be removed before publishing" warning, the
// provider keeps releasing normally, and a {version} placeholder naming the
// none package is refused instead of expanding to a version it will never
// have.
func TestVersioningNoneConsumerWithLocalLink(t *testing.T) {
	r := harness.New(t)
	cfg := noneConfig(echoBuild)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "smoke", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("tools", "smoke")
	r.WriteFile("packages/core/go.mod", "module github.com/acme/core\n\ngo 1.26\n")
	r.WriteFile("tools/smoke/go.mod",
		"module github.com/acme/smoke\n\ngo 1.26\n\nrequire github.com/acme/core v0.0.0\n")
	r.Commit("feat(core,smoke): bootstrap with manifests")

	// The link lands, silently: every package the sweep touches is none.
	res := r.Command("autowriter", "--package", "smoke", "--link-local")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	for _, e := range res.Events {
		assert.NotContains(t, e.Str("message"), "must be removed before publishing",
			"the unlink warning is for sweeps that will publish; this one never does")
	}
	manifest, err := os.ReadFile(r.Path("tools", "smoke", "go.mod"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "replace github.com/acme/core =>",
		"the local link was written")

	// The provider releases exactly as it would without the linked consumer.
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("smoke@"))
	assert.False(t, harness.HasCodeForPackage(res.Events, "W193", "smoke"))

	// A placeholder naming the none package cannot be resolved.
	res = r.Command("autowriter", "--since", "all", "--set", "github.com/acme/smoke={version}")
	require.NotEqual(t, 0, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "never carries a version")
}

// TestVersioningNonePackageSelection: naming a none package directly is not
// an error, and the answer differs by command. A release narrowed to it says
// why nothing will happen and exits cleanly; a run narrowed to it runs the
// script, because scripts are what the package is for.
func TestVersioningNonePackageSelection(t *testing.T) {
	r := harness.New(t)
	cfg := noneConfig(echoBuild)
	cfg.Scripts["mark"] = models.Script{"echo ran >> ran.log"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("tools", "smoke")
	r.Commit("feat(core,smoke): bootstrap")

	res := r.ReleaseOK("--package", "smoke")
	assert.Zero(t, len(r.TagList()), "nothing releases: the selection holds only a none package")
	found := false
	for _, e := range res.Events {
		if e.Package() == "smoke" && strings.Contains(e.Str("message"), "never released") {
			found = true
		}
	}
	assert.True(t, found, "the drop is said out loud, not silent")

	r.RunScriptOK("mark", "--package", "smoke")
	assert.FileExists(t, r.Path("tools", "smoke", "ran.log"))
	assert.NoFileExists(t, r.Path("packages", "core", "ran.log"))
}

// TestVersioningNoneReleaseAsInert: a Release-As footer aimed at a none
// package moves nothing and is reported as W238, while the same run stays
// clean for everything else.
func TestVersioningNoneReleaseAsInert(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(noneConfig(echoBuild))
	r.SeedPackage("packages", "core")
	r.SeedPackage("tools", "smoke")
	r.Commit("feat(core,smoke): bootstrap")

	r.CommitEmpty("feat(smoke): pinned work\n\nRelease-As: 2.0.0")
	res := r.ReleaseOK()
	assert.True(t, harness.HasCodeForPackage(res.Events, "W238", "smoke"),
		"the inert directive is reported")
	assert.Zero(t, r.TagCount("smoke@"), "pinning a none package tags nothing; tags: %v", r.TagList())
	assert.True(t, r.HasTag("core@0.1.0"), "the rest of the run is unaffected; tags: %v", r.TagList())
}

// TestVersioningNoneReleaseOnlySettingsInert: release-only settings on a
// none space load fine and do nothing — the space never reaches the stages
// that would read them. The build script still runs through `dispat run`,
// proving the space is otherwise alive.
func TestVersioningNoneReleaseOnlySettingsInert(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo built >> build.marker"},
		"publish": {"echo published >> ../../publish.marker"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"tools": {
			Path:       models.PathList{"tools"},
			Versioning: models.VersioningNone,
			TagFormat:  "tool-{package}-v{version}",
			Flow:       buildPublish(),
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("tools", "smoke")
	r.Commit("feat(smoke): bootstrap")

	r.ReleaseOK()
	assert.Zero(t, len(r.TagList()), "no tag under any format")
	assert.NoFileExists(t, r.Path("publish.marker"), "the publish stage never ran")
	assert.NoFileExists(t, r.Path("tools", "smoke", "build.marker"), "nor did build: no release, no stages")

	r.RunScriptOK("build")
	assert.FileExists(t, r.Path("tools", "smoke", "build.marker"),
		"the same script runs on request; only the release stages are out of reach")
}
