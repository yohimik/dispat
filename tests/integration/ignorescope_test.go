// Goal 26: change-scope ignore. A package's own files can be kept from
// counting as changes to it, so the folders that do not deserve a release —
// docs, fixtures, a scratch area — stop triggering one while the package
// stays the package: its scripts still run there, its changelog is still
// written there, and the release commit still stages all of it.
//
// The patterns are written as an `ignore` list at any level or as a
// .dispatignore file in any folder, the levels concatenate, and a "!" pattern
// re-includes what a broader level excluded.
package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestIgnoreScopeKeepsAFolderFromTriggeringARelease: a scopeless commit
// touching only ignored files releases nothing and says so; the same commit
// touching one ordinary file releases as usual.
func TestIgnoreScopeKeepsAFolderFromTriggeringARelease(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{"core": {Ignore: []string{"docs/"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	// The changelog the release wrote is untracked, and a scopeless commit
	// sweeping it in would derive the package it sits in. "release" is a
	// declared non-package scope, so absorbing it says nothing about core.
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/docs/guide.md", "documentation only\n")
	r.Commit("fix: rewrite the guide")
	res := r.ReleaseOK()
	assert.False(t, r.HasTag("core@0.1.1"), "an ignored file is not a change; tags: %v", r.TagList())
	assert.True(t, harness.HasCode(res.Events, "W131"), "the unit resolved to no package")

	// One file outside the ignored folder is enough to bring the package back.
	r.WriteFile("packages/core/docs/api.md", "more documentation\n")
	r.WriteFile("packages/core/main.txt", "real work\n")
	r.Commit("fix: the guide and the code")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.1"), "tags: %v", r.TagList())
}

// TestIgnoreScopeDoesNotHideThePackage: ignoring is about what counts as a
// change, not about what the package is. A commit naming the package by scope
// still releases it, and the release commit still stages the ignored files.
func TestIgnoreScopeDoesNotHideThePackage(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	cfg.Packages = map[string]models.PackageConfig{"core": {Ignore: []string{"docs/"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()

	r.WriteFile("packages/core/docs/guide.md", "documentation only\n")
	r.Commit("fix(core): the guide is part of the package")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.1"), "a scope always addresses the package; tags: %v", r.TagList())

	// The release commit stages the whole folder, so the changelog written
	// next to the ignored docs is committed with everything else.
	r.WriteFile("packages/core/docs/api.md", "more\n")
	r.Git("add", "-A")
	r.Git("commit", "-q", "-m", "fix(core): another documentation change")
	r.ReleaseOK()
	assert.Equal(t, "", r.Git("status", "--porcelain"), "the release commit left nothing behind")
}

// TestIgnoreScopeLevelsConcatenate: the repository, the space and the package
// all contribute patterns, and only the package can lift what a broader level
// excluded — for itself alone.
func TestIgnoreScopeLevelsConcatenate(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Ignore = []string{"*.md"}
	libs := cfg.Spaces["libs"]
	libs.Ignore = []string{"fixtures/"}
	cfg.Spaces["libs"] = libs
	cfg.Packages = map[string]models.PackageConfig{"core": {Ignore: []string{"!CHANGES.md"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core,utils): bootstrap")
	r.ReleaseOK()

	// The space's patterns reach every one of its packages, and the
	// repository's reach both spaces.
	r.WriteFile("packages/core/fixtures/a.json", "{}\n")
	r.WriteFile("packages/utils/notes.md", "notes\n")
	r.Commit("fix: fixtures and notes, neither of them a release")
	r.ReleaseOK()
	assert.Equal(t, 2, len(r.TagList()), "nothing new; tags: %v", r.TagList())

	// The package re-includes one of the repository's exclusions, and its
	// sibling does not inherit that.
	r.WriteFile("packages/core/CHANGES.md", "core changed\n")
	r.WriteFile("packages/utils/CHANGES.md", "utils did not\n")
	r.Commit("fix: a change one package counts and the other does not")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.1"), "tags: %v", r.TagList())
	assert.False(t, r.HasTag("utils@0.1.1"), "the re-inclusion is the package's own")
}

// TestIgnoreScopeFileAndKeyAgree: a .dispatignore file says exactly what the
// `ignore` key says, at whichever level its folder sits.
func TestIgnoreScopeFileAndKeyAgree(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.WriteFile(".dispatignore", "# documentation is not a release\n*.md\n")
	r.WriteFile("packages/core/.dispatignore", "testdata/\n")
	r.Commit("feat(core,utils): bootstrap")
	r.ReleaseOK()

	r.WriteFile("packages/core/testdata/a.json", "{}\n")
	r.WriteFile("packages/utils/README.md", "read me\n")
	r.Commit("fix: a fixture and a readme")
	res := r.ReleaseOK()
	assert.Equal(t, 2, len(r.TagList()), "neither counted; tags: %v", r.TagList())
	assert.True(t, harness.HasCode(res.Events, "W131"))
}

// TestIgnoreScopeAppliesToSince: `--since` picks its packages from the same
// file-derived scope, so it honours the same patterns.
func TestIgnoreScopeAppliesToSince(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{"core": {Ignore: []string{"docs/"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core,utils): bootstrap")
	base := r.Git("rev-parse", "HEAD")

	r.WriteFile("packages/core/docs/guide.md", "documentation only\n")
	r.WriteFile("packages/utils/main.txt", "real work\n")
	r.Commit("chore: touch both packages")

	res := r.Command("run", "build", "--since", base)
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Equal(t, 1, buildRuns(r), "only the package with a real change ran\nstdout:\n%s", res.Stdout)
	var ran []string
	for _, e := range res.Events {
		if e.Str("message") == "run script started" {
			ran = append(ran, e.Package())
		}
	}
	assert.Equal(t, []string{"utils"}, ran, "the ignored change selected nobody")
}

// TestIgnoreScopeRefusesAPatternItCannotCarryOut: a pattern that means
// nothing as written fails the load, with the package that holds it named.
func TestIgnoreScopeRefusesAPatternItCannotCarryOut(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{"core": {Ignore: []string{"docs/", "!"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	res := r.Release()
	require.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "re-includes nothing")
	assert.Empty(t, r.TagList(), "a refused config releases nothing")
}
