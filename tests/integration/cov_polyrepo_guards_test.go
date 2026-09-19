// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for the preflight a composed release runs over each
// participating repository before it plans any package work, and for the one
// thing preflight cannot settle in advance: a path whose owner changes while
// the run is in flight.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covPolyrepoSimpleFleet is one source checked out under a control repository,
// with nothing configured beyond the link itself.
func covPolyrepoSimpleFleet(t *testing.T) *harness.Repo {
	t.Helper()
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	return control
}

// TestCovPolyrepoPreflightRefusesARepositoryItCannotRecordInto: each
// participating repository is checked once, before planning, for the things
// that would make its release commit impossible or wrong: work already sitting
// in a release path, a branch policy the checkout does not satisfy, and a push
// destination that is not a branch name at all. Each refusal names the
// repository and leaves every repository untouched.
func TestCovPolyrepoPreflightRefusesARepositoryItCannotRecordInto(t *testing.T) {
	t.Run("work already sitting in a release path", func(t *testing.T) {
		control := covPolyrepoSimpleFleet(t)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure a source that records its releases")
		// Somebody's unfinished edit in the folder the release commit stages.
		control.WriteFile("sources/lib/packages/lib/main.txt", "half-finished\n")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "pre-existing local changes")
		assert.Contains(t, out, "lib-source")
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
		assert.Equal(t, "half-finished\n",
			readFileString(t, control.Path("sources", "lib", "packages", "lib", "main.txt")),
			"the refusal leaves the unfinished work exactly as it was")
	})

	t.Run("a source on a branch the policy does not allow", func(t *testing.T) {
		control := covPolyrepoSimpleFleet(t)
		// The control repository satisfies its own policy; the source does not.
		control.Git("branch", "-m", harness.DefaultBranch, "release")
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.Run = &models.RunConfig{AllowBranch: []string{"release"}}
		control.WriteConfigModel(cfg)
		control.Commit("chore: allow releases from the release branch alone")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "lib-source")
		assert.Contains(t, out, "is not allowed to release")
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
	})

	t.Run("a push destination that is not a branch name", func(t *testing.T) {
		control := covPolyrepoSimpleFleet(t)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"lib-source": {Commit: &models.CommitConfig{
				Enabled: models.Bool(true), Push: true, Remote: "origin",
				Branch: "release branch", Verify: models.Bool(false),
			}},
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: name a branch git cannot write")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		assert.True(t, harness.IsCodePresent(res.Events, "E337"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, covPolyrepoOutput(res), "lib-source")
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
	})
}

// TestCovPolyrepoRefusesARecordPathThatIsNotAPath: the paths a release writes
// are resolved before anything is written, ancestors included, because a
// symlink in the middle of a path decides who owns its end. A component that
// is an ordinary file is not a folder anything can be written under, and both
// the changelog destination and a `commit.include` entry say so before the
// release starts rather than failing halfway through staging.
func TestCovPolyrepoRefusesARecordPathThatIsNotAPath(t *testing.T) {
	t.Run("a changelog under a file", func(t *testing.T) {
		control := covPolyrepoSimpleFleet(t)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.Changelog = &models.ChangelogConfig{
			Enabled: models.Bool(true), File: "main.txt/CHANGELOG.md",
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: write the changelog under a file")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "not a directory")
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
	})

	t.Run("an include path under a file", func(t *testing.T) {
		control := covPolyrepoSimpleFleet(t)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"lib-source": {Commit: &models.CommitConfig{
				Enabled: models.Bool(true),
				Include: []string{"packages/lib/main.txt/extra"},
			}},
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: stage a path under a file")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "not a directory")
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
	})
}

// TestCovPolyrepoIncludePathCannotChangeOwnerMidRun: preflight resolves every
// configured include path to the repository that owns it, and a symlink can be
// re-pointed after that. The paths are resolved again immediately before
// staging, so a link a hook moved into another repository is refused with the
// path named, the source keeps its release tag, and the control gitlink is
// never advanced onto a commit that would have carried somebody else's files.
func TestCovPolyrepoIncludePathCannotChangeOwnerMidRun(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	require.NoError(t, os.Symlink("packages/lib", source.Path("generated")))
	source.Commit("feat(lib): bootstrap library beside a generated link")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := covPolyrepoFile()
	cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg.Scripts["relink"] = models.Script{
		"rm ../../generated && ln -s " + harness.ShQuote(control.Path("packages")) + " ../../generated",
	}
	cfg.Flow = &models.SpaceFlowConfig{
		Build:         []string{"build"},
		BeforePublish: []string{"relink"},
		Publish:       []string{"publish"},
	}
	cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
		"lib-source": {Commit: &models.CommitConfig{
			Enabled: models.Bool(true), Include: []string{"generated"},
		}},
	}
	control.SeedPackage("packages", "tool")
	control.WriteConfigModel(cfg)
	control.Commit("chore: configure a source whose include path is a link")
	pinnedBefore := control.Git("rev-parse", "HEAD:sources/lib")
	sourceBefore := control.Git("-C", "sources/lib", "rev-parse", "HEAD")

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	out := covPolyrepoOutput(res)
	assert.Contains(t, out, "changed its resolved owner after preflight")
	assert.Contains(t, out, "lib-source")
	assert.Contains(t, out, "pre-publish repository validation failed",
		"the re-resolution runs before the publish command, not after it")
	assert.Equal(t, sourceBefore, control.Git("-C", "sources/lib", "rev-parse", "HEAD"),
		"no source commit was written from the relocated link")
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.Equal(t, pinnedBefore, control.Git("rev-parse", "HEAD:sources/lib"))
}

// TestCovPolyrepoSnapshotNoticesARelevantTagThatDisappeared: the fixed fleet
// inventory is every ref a configured package may write, read once before
// planning. A baseline tag deleted while the run is building is as much a
// change to that inventory as one that moved, and it has to be reported as
// what it was — a ref that is gone — before the package publishes against a
// history that no longer says what the plan read.
func TestCovPolyrepoSnapshotNoticesARelevantTagThatDisappeared(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")
	source.Git("tag", "-a", "lib@1.0.0", "-m", "first release")
	source.WriteFile("packages/lib/api.txt", "more\n")
	source.Commit("feat(lib): extend the API")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := covPolyrepoFile()
	cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg.Scripts["untag"] = models.Script{"git tag -d lib@1.0.0"}
	cfg.Scripts["publish"] = models.Script{"echo published > ../../../../published.txt"}
	cfg.Flow = &models.SpaceFlowConfig{
		Build:         []string{"build"},
		BeforePublish: []string{"untag"},
		Publish:       []string{"publish"},
	}
	control.WriteConfigModel(cfg)
	control.Commit("chore: configure a hook that removes a baseline tag")

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	assert.True(t, harness.IsCodePresent(res.Events, "E330"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	out := covPolyrepoOutput(res)
	assert.Contains(t, out, "was deleted")
	assert.Contains(t, out, "lib@1.0.0")
	assert.NotContains(t, polyrepoTags(control, "sources/lib"), "lib@1.1.0")
	assert.NoFileExists(t, control.Path("published.txt"),
		"the inventory is re-read before the publish command, not after it")
}

// TestCovPolyrepoExportThatIsNotACommitIsNotAdmitted: a native record step
// hands the run its source revision by exporting the package's exact commit,
// and the run admits only that. A value the right length that is not an object
// id at all is neither admitted into the fixed snapshot nor usable as the
// revision to tag, so the package publishes and its record fails saying the
// revision could not be resolved.
func TestCovPolyrepoExportThatIsNotACommitIsNotAdmitted(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := covPolyrepoFile()
	cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg.Scripts["build"] = models.Script{
		`printf 'PACKAGE_LIB=%s\n' zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz >> "$DISPAT_OUTPUT"`,
	}
	control.WriteConfigModel(cfg)
	control.Commit("chore: export something that is not a commit")
	pinnedBefore := control.Git("rev-parse", "HEAD:sources/lib")

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	assert.True(t, harness.IsCodePresent(res.Events, "E335"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Empty(t, polyrepoTags(control, "sources/lib"),
		"no tag is written at a revision that does not exist")
	assert.Equal(t, pinnedBefore, control.Git("rev-parse", "HEAD:sources/lib"))
}
