// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Area 58: the repository root as a folder of the configuration. A space may
// be rooted there, in which case its packages are the repository's top-level
// folders; a standalone `packages` entry may be rooted there too, in which
// case the package is the repository itself: its manifest, its changelog and
// its sources sit at the top, and it owns every file no deeper package owns.
//
// The half these scenarios watch most closely is what the root folder holds
// besides the package: the root configuration file, which is not the
// package's own in-folder layer, and the folders of deeper packages, which
// keep their files, their manifests and their releases.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// rootPackageConfig is the configuration of a single-package repository: one
// standalone entry naming the repository itself, releasing through the shared
// build and publish scripts and recording what it released in a changelog and
// a release commit, so that each scenario's next commit carries only what the
// scenario wrote.
func rootPackageConfig(path string) models.File {
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo building"},
		"publish": {"echo publishing"},
		"where":   {"pwd > ran-in.txt"},
	}
	cfg.Packages = map[string]models.PackageConfig{"app": {Path: path, Flow: buildPublish()}}
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	return cfg
}

// TestRootPathSpaceReleasesTopLevelFolders: a space whose path is the
// repository releases the repository's top-level folders as its packages, and
// the folders that are nobody's package elsewhere are nobody's package here
// either.
func TestRootPathSpaceReleasesTopLevelFolders(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {"echo building"}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"all": {Path: models.PathList{"."}, Flow: buildPublish()},
	}
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	r.WriteConfigModel(cfg)
	r.WriteFile("core/main.txt", "core\n")
	r.WriteFile("web/main.txt", "web\n")
	r.WriteFile("notes/main.txt", "not a package\n")
	r.WriteFile(".hidden/main.txt", "not a package either\n")
	r.WriteFile(".dispatexclude", "notes\n")
	r.Commit("feat(core,web): bootstrap the top-level packages")

	res := r.StatusOK()
	assert.Equal(t, []string{"core", "web"}, releasingNames(res),
		"the top-level folders are the space's packages; a dot-folder and an excluded folder are not")

	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0"))
	assert.True(t, r.IsTagged("web@0.1.0"))
	assert.FileExists(t, r.Path("core", "CHANGELOG.md"), "records land in each package's own folder")
	assert.NoFileExists(t, r.Path("CHANGELOG.md"), "and not at the top, which is the space folder")

	r.ReleaseOK()
	assert.Len(t, r.TagList(), 2, "a second run of the same command releases nothing")

	t.Run("spelled with a trailing slash", func(t *testing.T) {
		cfg.Spaces = map[string]models.SpaceConfig{
			"all": {Path: models.PathList{"./"}, Flow: buildPublish()},
		}
		r.WriteConfigModel(cfg)
		assert.ElementsMatch(t, []string{"core", "web"}, plannedPackages(r.StatusOK()),
			`"./" names the folder "." names`)
	})

	t.Run("nothing may be listed beside the root", func(t *testing.T) {
		cfg.Spaces = map[string]models.SpaceConfig{
			"all": {Path: models.PathList{".", "core"}, Flow: buildPublish()},
		}
		r.WriteConfigModel(cfg)
		res := r.Status("--log-format", "json")
		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, diagnosticText(res), "overlap (one contains the other)")
	})
}

// TestRootPathPackageReleasesTheRepositoryItself: the single-package
// repository. One entry with `path: .` releases the repository as a package:
// it plans, tags under the repository's tag format, writes its changelog at
// the top, records the release in a commit, runs its scripts in the
// repository root, and releases again on the next commit.
func TestRootPathPackageReleasesTheRepositoryItself(t *testing.T) {
	r := harness.New(t)
	cfg := rootPackageConfig(".")
	r.WriteConfigModel(cfg)
	r.WriteFile("README.md", "the whole repository is the package\n")
	r.WriteFile("src/main.txt", "sources at the top\n")
	r.Commit("feat(app): bootstrap the repository package")

	res := r.StatusOK()
	assert.Equal(t, []string{"app"}, releasingNames(res))
	preview := r.Command("preview")
	require.Equal(t, 0, preview.Code, preview.Stderr)
	assert.Contains(t, preview.Stdout, "app@0.1.0", "preview renders the entry the release will write")

	r.ReleaseOK()
	assert.True(t, r.IsTagged("app@0.1.0"), "tags: %v", r.TagList())
	changelog, err := os.ReadFile(r.Path("CHANGELOG.md"))
	require.NoError(t, err, "the changelog of a repository package sits at the top")
	assert.Contains(t, string(changelog), "bootstrap the repository package")
	assert.Equal(t, "chore(release): app@0.1.0", subjects(r)[0])
	assert.Equal(t, []string{"CHANGELOG.md"}, committedFiles(r, "HEAD"),
		"the release commit stages what the release wrote, not the whole tree")

	r.WriteFile("README.md", "a second change\n")
	r.Commit("fix(app): correct the readme")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("app@0.1.1"), "a second commit releases again: %v", r.TagList())

	runRes := r.RunScriptOK("where", "--since", "all")
	assert.Equal(t, []string{"app"}, ranPackages(runRes), "the run at the top covers the repository package")
	ranIn, err := os.ReadFile(r.Path("ran-in.txt"))
	require.NoError(t, err)
	root, err := filepath.EvalSymlinks(r.Root)
	require.NoError(t, err)
	assert.Equal(t, root, strings.TrimSpace(string(ranIn)), "and its scripts run in the repository root")
}

// TestRootPathPackageOwnsOnlyWhatNoDeeperPackageOwns: ownership is by longest
// matching path prefix, so a repository package owns the files no package
// below it owns, and a space under packages/ keeps its own. The same rule
// decides which manifests belong to it, and it holds whether the deeper
// packages come from a space under a folder or from a space rooted at the
// repository beside it.
func TestRootPathPackageOwnsOnlyWhatNoDeeperPackageOwns(t *testing.T) {
	r := harness.New(t)
	cfg := rootPackageConfig(".")
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()},
	}
	cfg.AutoVersion = &models.AutoVersionConfig{Enabled: models.Bool(true)}
	r.WriteConfigModel(cfg)
	r.WriteFile("README.md", "the repository\n")
	r.WriteFile("package.json", `{"name": "app", "version": "0.0.0"}`)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name": "core", "version": "0.0.0"}`)
	r.Commit("feat(app,core): bootstrap both")
	r.ReleaseOK()
	require.True(t, r.IsTagged("app@0.1.0"))
	require.True(t, r.IsTagged("core@0.1.0"))
	assert.Contains(t, readFile(t, r, "package.json"), `"version": "0.1.0"`,
		"the manifest at the top is the repository package's own")
	assert.Contains(t, readFile(t, r, "packages", "core", "package.json"), `"version": "0.1.0"`,
		"and a deeper package's manifest stays its own")

	r.WriteFile("packages/core/main.txt", "changed deeper\n")
	r.Commit("fix: touch the nested package alone")
	assert.Equal(t, []string{"core"}, releasingNames(r.StatusOK()),
		"a commit touching only a deeper package's folder releases that package")

	r.WriteFile("README.md", "changed at the top\n")
	r.Commit("fix: touch the top alone")
	assert.Equal(t, []string{"app", "core"}, releasingNames(r.StatusOK()),
		"a commit touching the top releases the repository package")
	r.ReleaseOK()

	r.WriteFile("README.md", "changed again\n")
	r.WriteFile("packages/core/main.txt", "changed again too\n")
	r.Commit("fix: touch both")
	assert.Equal(t, []string{"app", "core"}, releasingNames(r.StatusOK()),
		"a commit touching both releases both")

	t.Run("beside a space rooted at the repository", func(t *testing.T) {
		second := harness.New(t)
		cfg := rootPackageConfig(".")
		cfg.Spaces = map[string]models.SpaceConfig{
			"all": {Path: models.PathList{"."}, Flow: buildPublish()},
		}
		second.WriteConfigModel(cfg)
		second.WriteFile("README.md", "the repository\n")
		second.WriteFile("core/main.txt", "a package of the root space\n")
		second.Commit("feat(app,core): a root space beside a root package")

		assert.Equal(t, []string{"app", "core"}, releasingNames(second.StatusOK()),
			"a root space and a root package coexist, as a folder holding another package's folder does")
		second.ReleaseOK()
		assert.True(t, second.IsTagged("app@0.1.0"))
		assert.True(t, second.IsTagged("core@0.1.0"))

		second.WriteFile("core/main.txt", "the space package alone\n")
		second.Commit("fix: touch the space package")
		assert.Equal(t, []string{"core"}, releasingNames(second.StatusOK()),
			"the deeper folder still owns its own files")
	})
}

// TestRootPathPackageIgnoresItsOwnRootConfigAsAPackageLayer: the file in the
// repository root is the root configuration file, so it is not read again as
// the repository package's own in-folder layer. Reading it there would hold
// the repository-wide keys it carries to the package level, where they are
// not legal, and would make the file that declares the package the nearest
// word about it.
func TestRootPathPackageIgnoresItsOwnRootConfigAsAPackageLayer(t *testing.T) {
	r := harness.New(t)
	cfg := rootPackageConfig(".")
	// Repository-wide keys a package level may not hold, beside the package
	// entry that declares the package in the same file.
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()},
	}
	cfg.NonPackageScopes = []string{"deps"}
	cfg.TagFormat = "root-{name}@{version}"
	cfg.Packages = map[string]models.PackageConfig{
		"app": {Path: ".", Flow: buildPublish(), TagFormat: "entry-{name}@{version}"},
	}
	r.WriteConfigModel(cfg)
	r.WriteFile("README.md", "the repository\n")
	r.SeedPackage("packages", "core")
	r.Commit("feat(app,core): bootstrap beside the repository-wide keys")

	res := r.StatusOK()
	assert.Equal(t, []string{"app", "core"}, releasingNames(res),
		"the package loads although its folder holds a file declaring spaces and packages")

	r.ReleaseOK()
	assert.True(t, r.IsTagged("entry-app@0.1.0"),
		"the entry is the nearest layer, so its tag format wins over the root file's: %v", r.TagList())
	assert.True(t, r.IsTagged("root-core@0.1.0"),
		"and the root file still reaches a package that states nothing: %v", r.TagList())
}

// TestRootPathPackageRefusals: what the root folder cannot be asked for.
// revertOnFail promises to roll the package folder back, which for this
// package is the whole working tree, so it is refused wherever the true it
// would act on was written; every other refusal of a standalone path is
// unchanged.
func TestRootPathPackageRefusals(t *testing.T) {
	t.Run("revertOnFail on the entry", func(t *testing.T) {
		r := harness.New(t)
		cfg := rootPackageConfig(".")
		cfg.Packages = map[string]models.PackageConfig{
			"app": {Path: ".", Flow: buildPublish(), RevertOnFail: models.Bool(true)},
		}
		r.WriteConfigModel(cfg)
		r.Commit("feat(app): bootstrap")
		refuseStatus(t, r, "revertOnFail cannot be used by a package whose path is the repository root")
	})

	t.Run("revertOnFail inherited from the root file", func(t *testing.T) {
		r := harness.New(t)
		cfg := rootPackageConfig(".")
		cfg.RevertOnFail = models.Bool(true)
		r.WriteConfigModel(cfg)
		r.Commit("feat(app): bootstrap")
		refuseStatus(t, r, "revertOnFail cannot be used by a package whose path is the repository root")
	})

	t.Run("revertOnFail contradicted on the entry", func(t *testing.T) {
		r := harness.New(t)
		cfg := rootPackageConfig(".")
		cfg.RevertOnFail = models.Bool(true)
		cfg.Packages = map[string]models.PackageConfig{
			"app": {Path: ".", Flow: buildPublish(), RevertOnFail: models.Bool(false)},
		}
		r.WriteConfigModel(cfg)
		r.WriteFile("README.md", "the repository\n")
		r.Commit("feat(app): bootstrap")
		assert.Equal(t, []string{"app"}, releasingNames(r.StatusOK()),
			"an explicit false on the entry is how the repository keeps the setting elsewhere")
	})

	t.Run("a build output holding another package", func(t *testing.T) {
		r := harness.New(t)
		cfg := rootPackageConfig(".")
		cfg.Spaces = map[string]models.SpaceConfig{
			"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()},
		}
		cfg.Packages = map[string]models.PackageConfig{
			"app": {Path: ".", Flow: buildPublish(), BuildOutputs: []string{"packages"}},
		}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(app,core): bootstrap")
		refuseStatus(t, r, "which holds the folder of package \"core\"")
	})

	t.Run("local changes under the release commit", func(t *testing.T) {
		// The shared configuration records its releases in a release commit,
		// and what that commit would stage is what the release protects: for
		// this package the whole working tree, as it is for any package whose
		// folder holds another's.
		r := harness.New(t)
		r.WriteConfigModel(rootPackageConfig("."))
		r.WriteFile("README.md", "the repository\n")
		r.Commit("feat(app): bootstrap")
		r.WriteFile("leftover.txt", "work the release commit would capture\n")

		res := r.Release()
		assert.NotEqual(t, 0, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "pre-existing local changes",
			"the release commit protects what it would stage, which for this package is the whole tree")
		assert.Empty(t, r.TagList())
	})

	t.Run("the path refusals that did not change", func(t *testing.T) {
		for name, tc := range map[string]struct{ path, want string }{
			"the parent":    {"..", "escapes the repository root"},
			"outside":       {"../outside", "escapes the repository root"},
			"climbing out":  {"sub/../..", "escapes the repository root"},
			"a missing one": {"nowhere", "nowhere"},
		} {
			t.Run(name, func(t *testing.T) {
				r := harness.New(t)
				r.WriteConfigModel(rootPackageConfig(tc.path))
				r.WriteFile("sub/main.txt", "a folder\n")
				r.Commit("feat(app): bootstrap")
				refuseStatus(t, r, tc.want)
			})
		}
	})
}

// TestRootPathPackageInALinkedPeer: a fleet member may be a single-package
// repository. The peer's own configuration names its own root, so the package
// is that repository and nothing else, and it releases and is tagged in its
// own repository while the entry repository releases its own package.
func TestRootPathPackageInALinkedPeer(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.writeConfig("sdk", func(cfg *models.File) {
		cfg.Spaces = nil
		cfg.Packages = map[string]models.PackageConfig{"sdk-pkg": {Path: "."}}
	})
	fleet.peer("sdk").Commit("chore: make the repository its own package")
	fleet.push("sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
	})
	fleet.peer("api").Commit("chore: declare the cross-repository edge")
	fleet.push("api")
	fleet.link("api", "sdk")

	api := fleet.peer("api")
	api.ReleaseOK("--package", "*")

	assert.Equal(t, []string{"api-pkg@0.1.0"}, api.TagList())
	assert.Equal(t, []string{"sdk-pkg@0.1.0"}, tagsIn(api.Repo, ".links/sdk"),
		"the peer's repository package is tagged in its own repository")
	assert.FileExists(t, api.Path(".links", "sdk", "CHANGELOG.md"),
		"and its changelog sits at the top of that repository")
	assert.FileExists(t, api.Path("packages", "api-pkg", "CHANGELOG.md"),
		"while the entry repository keeps its own layout")
}

// releasingNames lists the packages a plan will release, sorted: a package
// line carries a bump when the plan has work for it, and a selection that
// narrowed the run says so on the line instead. Both halves matter here,
// since a package rooted at the repository must neither miss the files it
// owns nor narrow the run to itself.
func releasingNames(res harness.RunResult) []string {
	var out []string
	for _, event := range res.Events {
		if event.Package() == "" || event.Str("bump") == "" {
			continue
		}
		if strings.Contains(event.Str("message"), "not selected") {
			continue
		}
		out = append(out, event.Package())
	}
	sort.Strings(out)
	return out
}

// ranPackages lists the packages a `dispat run` invocation ran its script
// for, sorted.
func ranPackages(res harness.RunResult) []string {
	var out []string
	for _, event := range res.Events {
		if event.Str("message") == "run script started" {
			out = append(out, event.Package())
		}
	}
	sort.Strings(out)
	return out
}

// committedFiles lists the paths one commit changed.
func committedFiles(r *harness.Repo, revision string) []string {
	out := r.Git("show", "--name-only", "--pretty=format:", revision)
	if strings.TrimSpace(out) == "" {
		return nil
	}
	return strings.Split(strings.TrimSpace(out), "\n")
}
