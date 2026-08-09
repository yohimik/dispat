package integration

// Area 12: per-package overrides, versioning groups and .dispatignore.
// packages.md promises that a package can override its space's configuration
// — from a top-level `packages` entry or from a dispat config file inside
// the package folder, most local winning — that declared versionGroups
// version their members as one across spaces, that `.dispatignore` excludes
// folders from discovery, and that the per-package record and concurrency
// policies hold through a real release. Only the compiled binary can prove
// the layers compose: config load, discovery, planning, scheduling and the
// recorders all participate in every scenario here.

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// packageFile marshals a typed package override and writes it as the
// package's in-folder dispat.json — the same model-authored stance as
// WriteConfigModel, one level down.
func packageFile(t *testing.T, r *harness.Repo, pkgDir string, po models.PackageConfig) {
	t.Helper()
	data, err := json.MarshalIndent(po, "", "  ")
	require.NoError(t, err)
	r.WriteFile(pkgDir+"/dispat.json", string(data))
}

// TestOverridesFlowBuildPerPackage: a `packages` entry replaces one flow
// entry for one package — the override's build runs for it, the space's
// build for its sibling, and the stages the entry does not name are
// inherited (both packages still publish).
func TestOverridesFlowBuildPerPackage(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{
		"build":     "echo space-build >> ../../build.log",
		"alt-build": "echo override-build >> ../../override.log",
		"publish":   "echo publishing",
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish()},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"core": {Flow: &models.SpaceFlowConfig{Build: []string{"alt-build"}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "extra")
	r.Commit("feat(core,extra): bootstrap both")

	r.ReleaseOK()

	assert.Equal(t, 1, buildRuns(r), "only the sibling runs the space build")
	data, err := os.ReadFile(r.Path("override.log"))
	require.NoError(t, err, "core must have run its own build")
	assert.Equal(t, "override-build\n", string(data), "core runs its own build exactly once")
	assert.True(t, r.HasTag("core@0.1.0"))
	assert.True(t, r.HasTag("extra@0.1.0"))

	// Convergence: the override changes what runs, not what releases.
	res := r.ReleaseOK()
	assert.Equal(t, 0, res.Code)
	assert.Equal(t, 2, len(r.TagList()), "a second run releases nothing new")
}

// TestOverridesInFolderFileWins: the package folder's own config file is the
// most local layer — its tagFormat beats the space's `packages` entry, which
// beats the space — proven by the tag the release actually creates.
func TestOverridesInFolderFileWins(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{
		"core": {TagFormat: "entry-{name}@{version}"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "extra")
	packageFile(t, r, "packages/core", models.PackageConfig{TagFormat: "file-{name}@{version}"})
	r.Commit("feat(core,extra): bootstrap")

	r.ReleaseOK()

	assert.True(t, r.HasTag("file-core@0.1.0"), "the in-folder file's tagFormat wins: %v", r.TagList())
	assert.False(t, r.HasTag("entry-core@0.1.0"))
	assert.True(t, r.HasTag("extra@0.1.0"), "the sibling keeps the repository default")
}

// TestOverridesDispatignore: a folder listed in the space's .dispatignore is
// not a package — it is never released, and a commit scoping it draws the
// unknown-scope diagnostic (E130) exactly as any non-package name would.
func TestOverridesDispatignore(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "scratch")
	r.WriteFile("packages/.dispatignore", "# not packages\nscratch\n")
	r.Commit("feat(core): real work")
	r.CommitEmpty("feat(scratch): addressed at a non-package")

	res := r.ReleaseOK()

	assert.True(t, r.HasTag("core@0.1.0"))
	assert.Equal(t, 0, r.TagCount("scratch@"), "an ignored folder never releases")
	assert.True(t, harness.HasCode(res.Events, "E130"),
		"the ignored folder's name is an unknown scope, like any non-package")
}

// TestOverridesVersionGroupSpansSpaces: a declared versionGroups group joined
// by two spaces versions as one — a change in one space rides the other
// space's package to the same version (W210) — and converges once aligned.
func TestOverridesVersionGroupSpansSpaces(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{"build": echoBuild, "publish": "echo publishing"}
	cfg.VersionGroups = map[string]models.VersionGroupConfig{
		"platform": {Versioning: models.VersioningFixed},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(), VersionGroup: "platform"},
		"svc":  {Path: "services", Flow: buildPublish(), VersionGroup: "platform"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "lib1")
	r.SeedPackage("services", "app1")
	r.Commit("feat(lib1): moves the whole group")

	res := r.ReleaseOK()

	assert.True(t, r.HasTag("lib1@0.1.0"))
	assert.True(t, r.HasTag("app1@0.1.0"), "the other space's member rides to the group version: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "app1"),
		"the ride must be explained by W210")

	// Convergence: aligned members release nothing on a second run.
	r.ReleaseOK()
	assert.Len(t, r.TagList(), 2)
}

// TestOverridesPerPackageRecords: the record policies resolve per package —
// one package writes its changelog under an overridden file name while its
// sibling disables both records, and the GitHub recorder receives exactly
// the enabled package's release.
func TestOverridesPerPackageRecords(t *testing.T) {
	type ghRelease struct {
		TagName string `json:"tag_name"`
	}
	srv, bodies := githubFake(t)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")

	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{
		"build":   echoBuild,
		"publish": `echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT"`,
	}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), Owner: "acme", Repo: "mono",
		APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish()},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"core": {Changelog: &models.ChangelogConfig{File: "HISTORY.md"}},
		"extra": {
			Changelog: &models.ChangelogConfig{Enabled: models.Bool(false)},
			GitHub:    &models.GitHubConfig{Enabled: models.Bool(false)},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "extra")
	r.Commit("feat(core,extra): bootstrap both")

	r.ReleaseOK()

	assert.FileExists(t, r.Path("packages", "core", "HISTORY.md"), "the overridden file name")
	assert.NoFileExists(t, r.Path("packages", "core", "CHANGELOG.md"))
	assert.NoFileExists(t, r.Path("packages", "extra", "CHANGELOG.md"), "changelog disabled per package")

	releases := decodeAll[ghRelease](t, bodies())
	require.Len(t, releases, 1, "only the enabled package reaches GitHub")
	assert.Equal(t, "core@0.1.0", releases[0].TagName)
}

// TestOverridesPackageConcurrencyWeight: a package whose concurrency equals
// the build budget occupies it whole — its build overlaps no other build in
// the run, while the ordinary packages are still free to overlap each other.
func TestOverridesPackageConcurrencyWeight(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(2, 3)
	cfg.Scripts = map[string]string{
		"build":   r.TsmarkScript("build.log", "$DISPAT_PACKAGE", 150*time.Millisecond),
		"publish": "echo publishing",
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish()},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"pkg0": {Concurrency: []int{2}},
	}
	r.WriteConfigModel(cfg)
	names := packageNames(3, "pkg")
	seedIndependentPackages(r, names)

	r.ReleaseOK()

	tl := r.Timeline("build.log")
	require.Len(t, tl, len(names))
	var heavy harness.Interval
	for _, iv := range tl {
		if iv.Label == "pkg0" {
			heavy = iv
		}
	}
	require.NotZero(t, heavy.Start, "pkg0 must have built")
	for _, iv := range tl {
		if iv.Label == "pkg0" {
			continue
		}
		disjoint := !heavy.Start.Before(iv.End) || !iv.Start.Before(heavy.End)
		assert.Truef(t, disjoint, "pkg0 (weight 2 of budget 2) must build alone; overlaps %s", iv.Label)
	}
}

// TestOverridesRunShorthandFromPackageFolder: the config ascent must walk
// past the package's own override file — a spaces-less config — and land on
// the monorepo root, so the run shorthand keeps working from inside a
// package folder that carries one.
func TestOverridesRunShorthandFromPackageFolder(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	spc := cfg.Spaces["libs"]
	spc.RunScripts = map[string]string{"greet": "echo greeted > greeted.txt"}
	cfg.Spaces["libs"] = spc
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	packageFile(t, r, "packages/core", models.PackageConfig{TagFormat: "v-{name}@{version}"})
	r.Commit("feat(core): bootstrap")

	res := r.CommandAt("packages/core", "greet")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.FileExists(t, r.Path("packages", "core", "greeted.txt"),
		"the shorthand resolved the root config and ran in the package")
}

// TestOverridesRunScriptOnlyInPackage: a run script defined only in a
// package folder's own config file exists nowhere in the loaded root config,
// yet `dispat run` must find it — and run it in that package alone, siblings
// skipping as they would any script their space does not define. A name no
// space or package defines anywhere stays an error.
func TestOverridesRunScriptOnlyInPackage(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{
		"extra": {RunScripts: map[string]string{"buff": "echo buffed > buffed.txt"}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "extra")
	packageFile(t, r, "packages/core", models.PackageConfig{
		RunScripts: map[string]string{"polish": "echo polished > polished.txt"},
	})
	r.Commit("feat(core,extra): bootstrap both")

	// Defined only in core's in-folder file: found through discovery.
	r.RunScriptOK("polish")
	assert.FileExists(t, r.Path("packages", "core", "polished.txt"))
	assert.NoFileExists(t, r.Path("packages", "extra", "polished.txt"), "siblings skip the script")

	// Defined only in extra's `packages` entry: found in the loaded config.
	r.RunScriptOK("buff")
	assert.FileExists(t, r.Path("packages", "extra", "buffed.txt"))
	assert.NoFileExists(t, r.Path("packages", "core", "buffed.txt"))

	res := r.RunScript("ghost")
	assert.NotEqual(t, 0, res.Code, "an undefined script stays a hard error")
}
