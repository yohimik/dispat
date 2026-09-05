// Goal 25: the configuration ladder from the root down. The root file is the
// bottom layer of the same fold a space and a package go through, so a
// space-shaped setting written once at the top reaches every space and every
// standalone package, and any level below can still say otherwise — including
// saying "false" against a "true", which is what makes the boolean options
// three-state rather than plain.
//
// The claims are made through the binary: a tag only one tag format could
// produce, a build log only one build script could write, a version only one
// versioning mode could compute.
package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// levelsConfig is two spaces that state nothing of their own, so everything
// they have comes from the root file.
func levelsConfig() models.File {
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":       {"echo root >> ../../build.log"},
		"build-libs":  {"echo libs >> ../../build.log"},
		"build-core":  {"echo core >> ../../build.log"},
		"publish":     {"echo publishing"},
		"login":       {"echo login >> ../../login.log"},
		"build-apps":  {"echo apps >> ../../build.log"},
		"publish-app": {"echo publishing"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}},
		"apps": {Path: models.PathList{"services"}},
	}
	cfg.Flow = buildPublish()
	return cfg
}

// TestLevelsRootFlowReachesEverySpace: one `flow` at the root runs for every
// package of every space, and a space or a package replaces the entries it
// names while keeping the rest.
func TestLevelsRootFlowReachesEverySpace(t *testing.T) {
	r := harness.New(t)
	cfg := levelsConfig()
	cfg.Spaces["libs"] = models.SpaceConfig{Path: models.PathList{"packages"},
		Flow: &models.SpaceFlowConfig{Build: []string{"build-libs"}}}
	cfg.Packages = map[string]models.PackageConfig{
		"core": {Flow: &models.SpaceFlowConfig{Build: []string{"build-core"}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.SeedPackage("services", "app")
	r.Commit("feat(core,utils,app): bootstrap")

	res := r.ReleaseOK()
	require.Len(t, r.TagList(), 3, "tags: %v", r.TagList())
	assert.Contains(t, res.Stdout, "publishing", "the root's publish stage ran for everyone")

	lines := logLines(r, "build.log")
	assert.ElementsMatch(t, []string{"core", "libs", "root"}, lines,
		"the package's own build, its space's, and the root's for the space that named none")
}

// TestLevelsRootBooleansAreThreeState: a root default reaches every space,
// and a space or a package can say false against it — which an ordinary bool
// could not express, because unset and false would look the same.
func TestLevelsRootBooleansAreThreeState(t *testing.T) {
	r := harness.New(t)
	cfg := levelsConfig()
	cfg.Scripts["mutate"] = models.Script{"echo dirty > mutated.txt"}
	cfg.Scripts["fail"] = models.Script{"exit 1"}
	cfg.RevertOnFail = models.Bool(true)
	cfg.Flow = &models.SpaceFlowConfig{Build: []string{"mutate"}, Publish: []string{"fail"}}
	cfg.Spaces["apps"] = models.SpaceConfig{Path: models.PathList{"services"}, RevertOnFail: models.Bool(false)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("services", "app")
	r.Commit("feat(core,app): bootstrap")

	res := r.Release()
	require.Equal(t, 1, res.Code, "both packages fail to publish\nstdout:\n%s", res.Stdout)

	// core inherits the root's true and is rolled back; app said false and
	// keeps what its version stage wrote.
	assert.NoFileExists(t, r.Path("packages/core/mutated.txt"),
		"the root's revertOnFail reached the space that said nothing")
	assert.FileExists(t, r.Path("services/app/mutated.txt"),
		"and the space that said false against it kept its changes")
}

// TestLevelsRootVersioningAppliesPerSpace: a root versioning mode applies
// under each space's own group, so two spaces version as two groups rather
// than one, and a space can still opt out.
func TestLevelsRootVersioningAppliesPerSpace(t *testing.T) {
	r := harness.New(t)
	cfg := levelsConfig()
	cfg.Versioning = models.VersioningFixed
	cfg.Spaces["apps"] = models.SpaceConfig{Path: models.PathList{"services"}, Versioning: models.VersioningIndependent}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.SeedPackage("services", "app")
	r.SeedPackage("services", "web")
	r.Commit("feat(core,utils,app,web): bootstrap")
	r.ReleaseOK()

	// One package of the fixed space moves, and its sibling rides along; the
	// independent space is untouched by both.
	r.WriteFile("packages/core/api.txt", "changed\n")
	r.Commit("feat(core): a new export")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.2.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("utils@0.2.0"), "the space's other package rode along")
	assert.False(t, r.HasTag("app@0.2.0"), "the independent space did not")
	assert.False(t, r.HasTag("web@0.2.0"))
}

// TestLevelsRootReachesAStandalonePackage: a package outside every space is
// its own space, so it folds through the same root defaults.
func TestLevelsRootReachesAStandalonePackage(t *testing.T) {
	r := harness.New(t)
	cfg := levelsConfig()
	cfg.TagFormat = "{name}@v{version}"
	cfg.Packages = map[string]models.PackageConfig{"tool": {Path: "tools/tool"}}
	delete(cfg.Spaces, "apps")
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("tools", "tool")
	r.Commit("feat(core,tool): bootstrap")

	r.ReleaseOK()
	assert.True(t, r.HasTag("tool@v0.1.0"), "the root's tagFormat reached it; tags: %v", r.TagList())
	assert.Equal(t, []string{"root", "root"}, logLines(r, "build.log"),
		"and so did the root's flow, for the space package and the standalone one")
}

// TestLevelsSpaceRecordsAndSrc: `changelog`, `github`, `src` and
// `concurrency` are stated once for a whole space, and a package can still
// depart from any of them.
func TestLevelsSpaceRecordsAndSrc(t *testing.T) {
	r := harness.New(t)
	cfg := levelsConfig()
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true), File: "ROOT.md"}
	cfg.Spaces["libs"] = models.SpaceConfig{Path: models.PathList{"packages"},
		Changelog: &models.ChangelogConfig{File: "LIBS.md"}, Src: "src"}
	cfg.Packages = map[string]models.PackageConfig{
		"utils": {Changelog: &models.ChangelogConfig{File: "UTILS.md"}},
	}
	r.WriteConfigModel(cfg)
	r.WriteFile("packages/core/src/main.txt", "core\n")
	r.WriteFile("packages/utils/src/main.txt", "utils\n")
	r.SeedPackage("services", "app")
	r.Commit("feat(core,utils,app): bootstrap")
	r.ReleaseOK()

	assert.FileExists(t, r.Path("packages/core/LIBS.md"), "the space named the file")
	assert.FileExists(t, r.Path("packages/utils/UTILS.md"), "the package named its own")
	assert.FileExists(t, r.Path("services/app/ROOT.md"), "the other space kept the root's")

	// The changelog files the release wrote are untracked, and a scopeless
	// commit sweeping them in would derive every package. "release" is a
	// declared non-package scope, so absorbing them says nothing about any
	// package and leaves the next commit to speak for itself.
	r.Commit("chore(release): record the changelog files")

	// The space's src narrows every one of its packages: a change outside it
	// is not a change to the package.
	r.WriteFile("packages/core/docs/guide.md", "docs only\n")
	r.Commit("fix: a scopeless commit touching only what src leaves out")
	res := r.ReleaseOK()
	assert.False(t, r.HasTag("core@0.1.1"), "tags: %v", r.TagList())
	assert.Contains(t, res.Stdout, "W131", "the unit resolved to no package")
}

// logLines returns the lines a build script appended to a log in the
// repository root, or nil when the script never ran.
func logLines(r *harness.Repo, name string) []string {
	data, err := os.ReadFile(r.Path(name))
	if err != nil {
		return nil
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
