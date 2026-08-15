package integration

// Area 12: per-package overrides, versioning groups and .dispatexclude.
// packages.md promises that a package can override its space's configuration
// — from a top-level `packages` entry or from a dispat config file inside
// the package folder, most local winning — that declared versionGroups
// version their members as one across spaces, that `.dispatexclude` excludes
// folders from discovery, and that the per-package record and concurrency
// policies hold through a real release. Only the compiled binary can prove
// the layers compose: config load, discovery, planning, scheduling and the
// recorders all participate in every scenario here.

import (
	"encoding/json"
	"os"
	"strings"
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
	cfg.Scripts = map[string]models.Script{
		"build":     {"echo space-build >> ../../build.log"},
		"alt-build": {"echo override-build >> ../../override.log"},
		"publish":   {"echo publishing"},
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

// TestOverridesFlowScriptResolvesPerPackage: one `flow.build: build`, three
// commands. A flow entry names a script and the package the stage runs for
// decides which command that name means: its own `scripts` first, then its
// space's, then the file's. The release itself is the proof — this is the
// resolution the pipeline uses, not a second rule that only `dispat run`
// knows.
func TestOverridesFlowScriptResolvesPerPackage(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	stamp := func(level string) string {
		return "echo $DISPAT_PACKAGE:" + level + " >> ../../build.log"
	}
	cfg.Scripts = map[string]models.Script{"build": {stamp("file")}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(),
			Scripts: map[string]models.Script{"build": {stamp("space")}}},
		"apps": {Path: "apps", Flow: buildPublish()},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"core": {Scripts: map[string]models.Script{"build": {stamp("package")}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.SeedPackage("apps", "web")
	r.Commit("feat(core,utils,web): bootstrap all three")

	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("build.log"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"core:package", "utils:space", "web:file"},
		strings.Fields(string(data)),
		"each package built with the command its own level resolved")
	assert.Len(t, r.TagList(), 3, "all three still released")
}

// TestOverridesFlowScriptSuppliedByEveryPackage: a space's flow may name a
// script no level above it defines, as long as every package of the space
// supplies one. That is why an unresolved reference is reported per package
// and not per space: the space alone cannot answer the question.
func TestOverridesFlowScriptSuppliedByEveryPackage(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish()},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"core":  {Scripts: map[string]models.Script{"build": {"echo core-build >> ../../build.log"}}},
		"utils": {Scripts: map[string]models.Script{"build": {"echo utils-build >> ../../build.log"}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core,utils): each package brings its own build")

	r.ReleaseOK()
	data, err := os.ReadFile(r.Path("build.log"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"core-build", "utils-build"}, strings.Fields(string(data)))

	// Take the answer away from one package and the config stops loading, with
	// an error naming the package whose scope came up empty.
	delete(cfg.Packages, "utils")
	r.WriteConfigModel(cfg)
	res := r.Release()
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	// The fixture logs JSON, so the message's own quotes arrive escaped.
	out := strings.ReplaceAll(res.Stdout+res.Stderr, `\"`, `"`)
	assert.Contains(t, out, `package "utils"`, "the error names the package whose scope came up empty")
	assert.Contains(t, out, `flow.build references unknown script "build"`)
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

// TestOverridesDispatexclude: a folder listed in the space's .dispatexclude is
// not a package — it is never released, and a commit scoping it draws the
// unknown-scope diagnostic (E130) exactly as any non-package name would.
func TestOverridesDispatexclude(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "scratch")
	r.WriteFile("packages/.dispatexclude", "# not packages\nscratch\n")
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
// space's package to the same version (W234) — and converges once aligned.
func TestOverridesVersionGroupSpansSpaces(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
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
	assert.True(t, harness.HasCodeForPackage(res.Events, "W234", "app1"),
		"the ride must be explained by W234")

	// Convergence: aligned members release nothing on a second run.
	r.ReleaseOK()
	assert.Len(t, r.TagList(), 2)
}

// TestOverridesVersionGroupSharesOnlyTheMajor: a declared group may share a
// part of the version rather than all of it. The same two spaces under
// fixedMajor keep their own minors and patches, and come together only when a
// breaking change reaches the part they do share.
func TestOverridesVersionGroupSharesOnlyTheMajor(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
	cfg.VersionGroups = map[string]models.VersionGroupConfig{
		"platform": {Versioning: models.VersioningFixedMajor},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(), VersionGroup: "platform"},
		"svc":  {Path: "services", Flow: buildPublish(), VersionGroup: "platform"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "lib1")
	r.SeedPackage("services", "app1")

	// A minor is below the shared major: it stays inside its own space.
	r.Commit("feat(lib1): a minor of lib1's own")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("app1@"), "a minor must not cross the group; tags: %v", r.TagList())
	assert.False(t, harness.HasCode(res.Events, "W234"), "no ride below the shared major")

	// A breaking change reaches the shared major and moves both spaces.
	r.CommitEmpty("feat(lib1)!: a breaking change")
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@1.0.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@1.0.0"),
		"the other space's member joins the shared major; tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W234", "app1"))

	r.ReleaseOK()
	assert.Len(t, r.TagList(), 3, "converged")
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
	cfg.Scripts = map[string]models.Script{
		"build":   {echoBuild},
		"publish": {`echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT"`},
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
	cfg.Scripts = map[string]models.Script{
		"build":   {r.TsmarkScript("build.log", "$DISPAT_PACKAGE", 150*time.Millisecond)},
		"publish": {"echo publishing"},
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
	spc.Scripts = map[string]models.Script{"greet": {"echo greeted > greeted.txt"}}
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

// TestOverridesScriptsAcrossTheLayers: a script defined only in a package
// folder's own config file exists nowhere in the loaded root config, yet
// `dispat run` must find it — and run it in that package alone, siblings
// skipping as they would any script they do not resolve. The two levels above
// it reach further in the same run, and a name nothing defines anywhere stays
// an error.
func TestOverridesScriptsAcrossTheLayers(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["stamp"] = models.Script{"echo stamped > stamped.txt"}
	spc := cfg.Spaces["libs"]
	spc.Scripts = map[string]models.Script{"sweep": {"echo swept > swept.txt"}}
	cfg.Spaces["libs"] = spc
	cfg.Packages = map[string]models.PackageConfig{
		"extra": {Scripts: map[string]models.Script{"buff": {"echo buffed > buffed.txt"}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "extra")
	packageFile(t, r, "packages/core", models.PackageConfig{
		Scripts: map[string]models.Script{"polish": {"echo polished > polished.txt"}},
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

	// The space's and the file's own scripts reach both packages.
	r.RunScriptOK("sweep")
	assert.FileExists(t, r.Path("packages", "core", "swept.txt"))
	assert.FileExists(t, r.Path("packages", "extra", "swept.txt"))
	r.RunScriptOK("stamp")
	assert.FileExists(t, r.Path("packages", "core", "stamped.txt"))
	assert.FileExists(t, r.Path("packages", "extra", "stamped.txt"))

	res := r.RunScript("ghost")
	assert.NotEqual(t, 0, res.Code, "an undefined script stays a hard error")
}

// spaceFile marshals a typed space configuration and writes it as the space
// folder's own dispat.json — the layer between the root file's space entry
// and anything said about one package, authored the same model-first way as
// WriteConfigModel.
func spaceFile(t *testing.T, r *harness.Repo, spaceDir string, sf models.SpaceFile) {
	t.Helper()
	data, err := json.MarshalIndent(sf, "", "  ")
	require.NoError(t, err)
	r.WriteFile(spaceDir+"/dispat.json", string(data))
}

// TestOverridesSpacePackagesEntry: a space configures one of its own packages
// through its `packages` map, without the root file's global one — the
// override's tag format reaches that package alone, and its sibling keeps the
// space's.
func TestOverridesSpacePackagesEntry(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(), Packages: map[string]models.PackageConfig{
			"core": {TagFormat: "space-entry-{name}@{version}"},
		}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "extra")
	r.Commit("feat(core,extra): bootstrap both")

	r.ReleaseOK()

	assert.True(t, r.HasTag("space-entry-core@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("extra@0.1.0"), "the sibling keeps the repository default")
}

// TestOverridesSpaceFile: a dispat config file inside the space folder is the
// space, said again and nearer. It replaces the stages and options it names
// for every package of the space, inherits the rest, and leaves other spaces
// alone.
func TestOverridesSpaceFile(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":      {"echo root-build >> ../../root.log"},
		"file-build": {"echo file-build >> ../../file.log"},
		"publish":    {"echo publishing"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish()},
		"svc":  {Path: "services", Flow: buildPublish()},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("services", "api")
	spaceFile(t, r, "packages", models.SpaceFile{
		TagFormat: "libs-{name}@{version}",
		Flow:      &models.SpaceFlowConfig{Build: []string{"file-build"}},
	})
	r.Commit("feat(core,api): bootstrap both")

	r.ReleaseOK()

	assert.True(t, r.HasTag("libs-core@0.1.0"), "the space file's tagFormat wins: %v", r.TagList())
	assert.True(t, r.HasTag("api@0.1.0"), "the other space is untouched")
	assert.Equal(t, 1, countLines(r, "file.log"), "the space file's build ran for its package")
	assert.Equal(t, 1, countLines(r, "root.log"), "and the root's build only for the other space")
	assert.FileExists(t, r.Path("packages", "core", "CHANGELOG.md"),
		"the publish stage the file did not name is inherited")
}

// TestOverridesLadderNearestWins: all six layers name the same package and
// the same key, and the one nearest the package decides. The layers below it
// still apply where they say something the nearer ones do not.
func TestOverridesLadderNearestWins(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {
			Path:      "packages",
			Flow:      buildPublish(),
			TagFormat: "s1-{name}@{version}",
			Packages: map[string]models.PackageConfig{
				"core": {TagFormat: "p2-{name}@{version}"},
			},
		},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"core":  {TagFormat: "p1-{name}@{version}"},
		"extra": {Changelog: &models.ChangelogConfig{File: "HISTORY.md"}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "extra")
	spaceFile(t, r, "packages", models.SpaceFile{
		TagFormat: "s2-{name}@{version}",
		Packages: map[string]models.PackageConfig{
			"core": {TagFormat: "p3-{name}@{version}"},
		},
	})
	packageFile(t, r, "packages/core", models.PackageConfig{TagFormat: "p4-{name}@{version}"})
	r.Commit("feat(core,extra): bootstrap both")

	r.ReleaseOK()

	assert.True(t, r.HasTag("p4-core@0.1.0"), "the package's own file is nearest: %v", r.TagList())
	assert.True(t, r.HasTag("s2-extra@0.1.0"),
		"a package no entry names still takes the space file's format")
	assert.FileExists(t, r.Path("packages", "extra", "HISTORY.md"),
		"the root entry's record policy still reaches the package it names")
}

// TestOverridesSpaceLayerDependencies: an edge declared in a space's packages
// entry, and one declared in the space file, both reach the plan — the
// consumer waits for its provider and rides its release exactly as a
// top-level declaration would.
func TestOverridesSpaceLayerDependencies(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
	// A default propagation depth, so a provider's bump reaches its consumers
	// with no directive in the message: what the declared edges are for here.
	cfg.Parser = &models.ParserConfig{
		Propagation: &models.ParserPropagationConfig{Depth: "all"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(), Packages: map[string]models.PackageConfig{
			"mid": {Dependencies: models.Providers("core")},
		}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "mid")
	r.SeedPackage("packages", "web")
	spaceFile(t, r, "packages", models.SpaceFile{
		Packages: map[string]models.PackageConfig{"web": {Dependencies: models.Providers("mid")}},
	})
	r.Commit("feat(core,mid,web): bootstrap the chain")
	r.ReleaseOK()

	// Only the root of the chain changes: both declared edges must carry the
	// bump forward, one layer each.
	r.WriteFile("packages/core/f.txt", "changed\n")
	r.Commit("fix(core): a change that must propagate")
	r.ReleaseOK()

	assert.True(t, r.HasTag("core@0.1.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("mid@0.1.1"), "the space entry's edge carried the bump to mid: %v", r.TagList())
	assert.True(t, r.HasTag("web@0.1.1"), "the space file's edge carried it on to web: %v", r.TagList())
}

// countLines returns how many lines a log file in the monorepo root holds,
// zero when the script that writes it never ran.
func countLines(r *harness.Repo, name string) int {
	data, err := os.ReadFile(r.Path(name))
	if err != nil {
		return 0
	}
	return len(strings.Fields(strings.TrimSpace(string(data))))
}
