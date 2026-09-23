// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for the recorder that writes a composed fleet's release
// records: what it stages beside a package, where it refuses to write, which
// refs it is allowed to move, and what a repository looks like afterwards when
// one of those steps failed. Goal 52 owns what the composition means; nothing
// here repeats it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covPolyrepoFile is the typed twin of polyrepoFile: inert release stages,
// every external recorder off, and the composed mode switched on. A test adds
// spaces, packages and policy to the returned value, so what it is actually
// configuring stays visible in the test.
func covPolyrepoFile() models.File {
	cfg := harness.BaseFile(2)
	cfg.Polyrepo = true
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(false)}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(false)}
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo building"},
		"publish": {"echo publishing"},
	}
	cfg.Flow = &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}
	return cfg
}

// covPolyrepoSpaces builds the central space declarations, whose paths keep
// their ordinary control-relative spelling.
func covPolyrepoSpaces(paths map[string]string) map[string]models.SpaceConfig {
	spaces := make(map[string]models.SpaceConfig, len(paths))
	for name, path := range paths {
		spaces[name] = models.SpaceConfig{Path: models.PathList{path}}
	}
	return spaces
}

// covPolyrepoOutput returns the combined output of one invocation, which is
// where a composition refusal is printed before the JSON event stream starts.
func covPolyrepoOutput(res harness.RunResult) string { return res.Stdout + res.Stderr }

// TestCovPolyrepoCommitIncludeIsHeldToItsOwner: `commit.include` names the
// shared artifacts a release stages beside its package folders, and in a
// composed fleet each of those paths has an owner. A path inside its own
// repository is staged in that repository's release commit; a path that leaves
// its owner, names another repository, or is written absolutely is refused
// before any package work, because staging it would put one repository's files
// into another repository's history.
func TestCovPolyrepoCommitIncludeIsHeldToItsOwner(t *testing.T) {
	t.Run("a path inside the source is staged with the package", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.WriteFile("shared/lock.txt", "first\n")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.Scripts["publish"] = models.Script{"echo regenerated > ../../shared/lock.txt"}
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"lib-source": {Commit: &models.CommitConfig{
				Enabled: models.Bool(true),
				Include: []string{"shared", "generated/missing.txt"},
			}},
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure a source that regenerates a shared artifact")

		res := control.ReleaseOK()
		assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0")
		assert.Equal(t, "regenerated",
			strings.TrimSpace(control.Git("-C", "sources/lib", "show", "HEAD:shared/lock.txt")),
			"the include path outside the package folder rides in the source release commit")
		assert.True(t, harness.IsCodePresent(res.Events, "W227"),
			"an include path that does not exist is warned about rather than staged")
	})

	t.Run("a path that leaves its owner is refused", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"lib-source": {Commit: &models.CommitConfig{
				Enabled: models.Bool(true),
				Include: []string{"../.."},
			}},
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure an include path that escapes its source")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "escapes its owner")
		assert.Contains(t, out, "lib-source")
		assert.Empty(t, polyrepoTags(control, "sources/lib"), "a refused include stages nothing")
	})

	t.Run("an absolute path is refused", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"lib-source": {Commit: &models.CommitConfig{
				Enabled: models.Bool(true),
				Include: []string{control.Path("sources", "lib", "packages")},
			}},
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure an absolute include path")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "escapes its owner")
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
	})

	t.Run("a control path that reaches into a source is refused", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		control.SeedPackage("packages", "tool")
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{
			"libs":  "sources/lib/packages",
			"tools": "packages",
		})
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Include: []string{"sources/lib"}}
		// The source keeps its own policy, so the include path under test has
		// exactly one claimant: the control repository that wrote it.
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"lib-source": {Commit: &models.CommitConfig{Enabled: models.Bool(false)}},
		}
		control.WriteConfigModel(cfg)
		control.Commit("feat(tool): configure a control include reaching into a source")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "spans repository")
		assert.Contains(t, out, "lib-source")
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
		assert.Empty(t, control.TagList(), "the control package is not tagged either")
	})
}

// TestCovPolyrepoChangelogPathIsHeldToItsOwner: a package's changelog is
// written by its owning repository, so a `changelog.file` spelling that climbs
// out of that repository would have one repository's release write a record
// into another's working tree. It is refused while the run is still preparing,
// before any package is built or tagged.
func TestCovPolyrepoChangelogPathIsHeldToItsOwner(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := covPolyrepoFile()
	cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg.Changelog = &models.ChangelogConfig{
		Enabled: models.Bool(true),
		File:    "../../../../CHANGELOG.md",
	}
	control.WriteConfigModel(cfg)
	control.Commit("chore: configure a changelog path that leaves its source")

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	out := covPolyrepoOutput(res)
	assert.Contains(t, out, "changelog path")
	assert.Contains(t, out, "escapes its owner")
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.NoFileExists(t, filepath.Join(filepath.Dir(control.Root), "CHANGELOG.md"))
}

// TestCovPolyrepoDetachedSourcePushesOnlyWhatItCanName: a detached source may
// still push the immutable release tag, because a tag names the commit it was
// created at and needs no branch destination. The branch requirement becomes
// real only when the run actually has to create a commit: a publish stage that
// writes into the package folder turns a tag-only release into one, and the
// release is refused at that point rather than committing to a destination
// nobody named.
func TestCovPolyrepoDetachedSourcePushesOnlyWhatItCanName(t *testing.T) {
	newFleet := func(t *testing.T, publish string) (*harness.Repo, string) {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		bare := filepath.Join(t.TempDir(), "source.git")
		control.Git("init", "-q", "--bare", bare)
		control.Git("-C", bare, "symbolic-ref", "HEAD", "refs/heads/"+harness.DefaultBranch)
		control.Git("-C", "sources/lib", "remote", "set-url", "origin", bare)
		control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
		control.Git("-C", "sources/lib", "checkout", "-q", "--detach")

		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.Scripts["publish"] = models.Script{publish}
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"lib-source": {Commit: &models.CommitConfig{
				Enabled: models.Bool(true), Push: true, Remote: "origin",
			}},
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure a detached source")
		return control, bare
	}

	t.Run("a clean detached release pushes its tag and no branch", func(t *testing.T) {
		control, bare := newFleet(t, "echo publishing")
		before := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
		branchBefore := control.Git("-C", bare, "rev-parse", "refs/heads/"+harness.DefaultBranch)

		control.ReleaseOK()
		assert.Equal(t, before, control.Git("-C", "sources/lib", "rev-parse", "HEAD"),
			"nothing was staged, so the detached checkout is where it was")
		assert.Equal(t, before, control.Git("-C", bare, "rev-parse", "lib@0.1.0^{commit}"),
			"the immutable tag reaches the source remote from a detached HEAD")
		assert.Equal(t, branchBefore, control.Git("-C", bare, "rev-parse", "refs/heads/"+harness.DefaultBranch),
			"and no branch was moved to a destination nobody named")
	})

	t.Run("a publish that writes into the package needs a branch", func(t *testing.T) {
		control, bare := newFleet(t, "echo generated > generated.txt")
		before := control.Git("-C", "sources/lib", "rev-parse", "HEAD")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		// The pre-publish branch check sees a clean tree, so it is the record
		// step that meets the commit this publish created. A recording failure
		// is reported in its own right (E335) and never becomes a tag.
		assert.True(t, harness.IsCodePresent(res.Events, "E335"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "E337")
		assert.Contains(t, out, "commit.branch")
		assert.Equal(t, before, control.Git("-C", "sources/lib", "rev-parse", "HEAD"))
		assert.Empty(t, polyrepoTags(control, "sources/lib"),
			"the release tag is not written when its commit has nowhere to go")
		assert.NotContains(t, control.Git("-C", bare, "tag", "--list"), "lib@0.1.0")
	})
}

// TestCovPolyrepoDetachedControlRefusesACheckpointItCannotPush: the control
// repository's checkpoint is an ordinary branch commit, so a detached control
// checkout configured to push has no destination for it. dispat says so before
// the source package publishes, rather than after publication has made the
// missing checkpoint a repair job. A gitlink that already names the recorded
// source revision needs no checkpoint at all, and that release proceeds.
func TestCovPolyrepoDetachedControlRefusesACheckpointItCannotPush(t *testing.T) {
	newFleet := func(t *testing.T) *harness.Repo {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		controlBare := filepath.Join(t.TempDir(), "control.git")
		control.Git("init", "-q", "--bare", controlBare)
		control.Git("-C", controlBare, "symbolic-ref", "HEAD", "refs/heads/"+harness.DefaultBranch)
		control.Git("remote", "add", "origin", controlBare)
		return control
	}

	t.Run("a checkpoint the control cannot push is refused before publication", func(t *testing.T) {
		control := newFleet(t)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.Scripts["publish"] = models.Script{"echo published > ../../published.txt"}
		// The source writes and commits its own changelog, so this release
		// moves the gitlink and the control repository owes a checkpoint.
		cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
		cfg.Commit = &models.CommitConfig{
			Enabled: models.Bool(true), Push: true, Remote: "origin", Verify: models.Bool(false),
		}
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"lib-source": {Commit: &models.CommitConfig{Enabled: models.Bool(true)}},
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure a control that must checkpoint")
		control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
		control.Git("checkout", "-q", "--detach")
		controlBefore := control.Git("rev-parse", "HEAD")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		assert.True(t, harness.IsCodePresent(res.Events, "E337"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "control")
		assert.Contains(t, out, "commit.branch")
		assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
		assert.NoFileExists(t, control.Path("published.txt"),
			"the refusal lands before the publish command runs")
	})

	t.Run("a gitlink that already names the recorded revision needs no branch", func(t *testing.T) {
		control := newFleet(t)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.Commit = &models.CommitConfig{
			Enabled: models.Bool(true), Push: true, Remote: "origin", Verify: models.Bool(false),
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure a control whose pointer is already current")
		control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
		control.Git("checkout", "-q", "--detach")
		controlBefore := control.Git("rev-parse", "HEAD")

		control.ReleaseOK()
		assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0",
			"a tag-only source release needs no checkpoint and no control branch")
		assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))
	})
}

// TestCovPolyrepoBeforeCommitHookCannotReplaceThePlannedSource proves the
// post-publication record checks the source HEAD after user hooks. If a hook
// commits there, the recorder must not tag that unplanned revision or advance
// the control gitlink. Once the stray commit and generated record are repaired,
// the unchanged source work can be released normally.
func TestCovPolyrepoBeforeCommitHookCannotReplaceThePlannedSource(t *testing.T) {
	control, sourceBare, _ := covPolyrepoPushableFleet(t)
	cfg := covPolyrepoFile()
	cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(false)}
	cfg.Scripts["sneak"] = models.Script{"git commit -q --allow-empty -m 'chore: a hook moved source HEAD'"}
	cfg.Run = &models.RunConfig{BeforeCommit: []string{"sneak"}}
	cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
		"lib-source": {Commit: &models.CommitConfig{
			Enabled: models.Bool(true), Push: true, Remote: "origin",
			Branch: harness.DefaultBranch,
		}},
	}
	control.WriteConfigModel(cfg)
	control.Commit("chore: configure a source record hook that commits")
	sourceBefore := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	controlBefore := control.Git("rev-parse", "HEAD")

	res := control.Release()
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, "E335"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, covPolyrepoOutput(res), "repository changed after planning")
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.Equal(t, sourceBefore, control.Git("-C", sourceBare, "rev-parse", "refs/heads/"+harness.DefaultBranch))
	assert.Equal(t, sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
	assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))
	assert.Equal(t, sourceBefore, control.Git("-C", "sources/lib", "rev-parse", "HEAD~1"),
		"the hook's commit occurred, so the record refusal is the HEAD guard")

	control.Git("-C", "sources/lib", "reset", "--hard", sourceBefore)
	require.NoError(t, os.Remove(control.Path("sources/lib/packages/lib/CHANGELOG.md")))
	cfg.Run = nil
	control.WriteConfigModel(cfg)
	control.Commit("chore: remove the source record hook after repair")
	control.ReleaseOK()
	assert.Equal(t, []string{"lib@0.1.0"}, polyrepoTags(control, "sources/lib"))
}

// TestCovPolyrepoAliasTagsFollowTheirOwnForcePolicy: a moving alias must
// replace the ref it already occupies and a fixed alias must not, so the push
// separates them: the release tag and the fixed aliases go as ordinary refs
// and only the explicitly moving ones are allowed to overwrite. Both halves
// have to reach the source remote, and the second release has to leave the
// first one's fixed alias exactly where it was.
func TestCovPolyrepoAliasTagsFollowTheirOwnForcePolicy(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	bare := filepath.Join(t.TempDir(), "source.git")
	control.Git("init", "-q", "--bare", bare)
	control.Git("-C", bare, "symbolic-ref", "HEAD", "refs/heads/"+harness.DefaultBranch)
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", bare)
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	cfg := covPolyrepoFile()
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {
			Path: models.PathList{"sources/lib/packages"},
			AliasTags: []models.AliasTagConfig{
				{Format: "{name}-v{major}", Moving: true},
				{Format: "{name}-exactly-{version}", Force: models.Bool(false)},
			},
		},
	}
	cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
		"lib-source": {Commit: &models.CommitConfig{
			Enabled: models.Bool(true), Push: true, Remote: "origin",
			Branch: harness.DefaultBranch,
		}},
	}
	control.WriteConfigModel(cfg)
	control.Commit("chore: configure a moving alias beside a fixed one")

	control.ReleaseOK()
	first := control.Git("-C", "sources/lib", "rev-parse", "lib@0.1.0^{commit}")
	assert.Equal(t, first, control.Git("-C", bare, "rev-parse", "lib-v0^{commit}"),
		"the moving alias is pushed with the release")
	assert.Equal(t, first, control.Git("-C", bare, "rev-parse", "lib-exactly-0.1.0^{commit}"))

	control.WriteFile("sources/lib/packages/lib/api.txt", "more\n")
	commitPolyrepoSource(t, control, "sources/lib", "feat(lib): extend the API")
	checkpointPolyrepoSource(t, control, "sources/lib")
	control.ReleaseOK()

	second := control.Git("-C", "sources/lib", "rev-parse", "lib@0.2.0^{commit}")
	require.NotEqual(t, first, second)
	assert.Equal(t, second, control.Git("-C", bare, "rev-parse", "lib-v0^{commit}"),
		"the moving alias is overwritten on the remote at the newer release")
	assert.Equal(t, first, control.Git("-C", bare, "rev-parse", "lib-exactly-0.1.0^{commit}"),
		"and the fixed alias of the earlier release is left where it was")
	assert.Equal(t, second, control.Git("-C", bare, "rev-parse", "lib-exactly-0.2.0^{commit}"))
}

// TestCovPolyrepoSourceRecordReportsEveryFailureItCollected: a changelog the
// recorder cannot write is not a reason to withhold the release tag, which is
// the only durable statement that the package published. The tag is written,
// the run still fails, the failure names the repository and the tag, and the
// control checkpoint is withheld because the source record is incomplete.
func TestCovPolyrepoSourceRecordReportsEveryFailureItCollected(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := covPolyrepoFile()
	cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	control.WriteConfigModel(cfg)
	control.Commit("chore: configure a fleet whose changelog cannot be written")
	// A folder standing where the record file belongs: the writer refuses it by
	// name rather than replacing it.
	require.NoError(t, os.MkdirAll(control.Path("sources", "lib", "packages", "lib", "CHANGELOG.md"), 0o755))
	pinnedBefore := control.Git("rev-parse", "HEAD:sources/lib")

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	out := covPolyrepoOutput(res)
	assert.Contains(t, out, "lib-source")
	assert.Contains(t, out, "lib@0.1.0")
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0",
		"the release tag is the truthful record of a package that published")
	assert.Equal(t, pinnedBefore, control.Git("rev-parse", "HEAD:sources/lib"),
		"an incomplete source record withholds the control checkpoint")
}

// TestCovPolyrepoRemoteVerificationRefusesWhatItCannotPushTo: with pushing on,
// each participating repository's remote is checked once before any release
// work. A remote that answers nothing and a checkout that is behind its remote
// branch are both refused there, naming the repository, because discovering
// either after publication would leave a package tagged and unreachable.
func TestCovPolyrepoRemoteVerificationRefusesWhatItCannotPushTo(t *testing.T) {
	t.Run("a remote that cannot be reached", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		control.Git("-C", "sources/lib", "remote", "set-url", "origin",
			filepath.Join(t.TempDir(), "not-a-repository"))

		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"lib-source": {Commit: &models.CommitConfig{
				Enabled: models.Bool(true), Push: true, Remote: "origin",
			}},
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure a source whose remote is not there")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "lib-source")
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
	})

	t.Run("a checkout behind its remote branch", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		bare := filepath.Join(t.TempDir(), "source.git")
		control.Git("init", "-q", "--bare", bare)
		control.Git("-C", bare, "symbolic-ref", "HEAD", "refs/heads/"+harness.DefaultBranch)
		control.Git("-C", "sources/lib", "remote", "set-url", "origin", bare)
		control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

		// Somebody else pushed while this checkout stood still. The clone the
		// run would push from no longer contains the remote branch tip.
		other := harness.New(t)
		other.Git("remote", "add", "upstream", bare)
		other.Git("fetch", "-q", "upstream")
		other.Git("checkout", "-q", "-B", harness.DefaultBranch, "upstream/"+harness.DefaultBranch)
		other.WriteFile("packages/lib/other.txt", "somebody else\n")
		other.Commit("chore(lib): another checkout moved first")
		other.Git("push", "-q", "upstream", "HEAD:refs/heads/"+harness.DefaultBranch)
		control.Git("-C", "sources/lib", "fetch", "-q", "origin")

		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"lib-source": {Commit: &models.CommitConfig{
				Enabled: models.Bool(true), Push: true, Remote: "origin",
				Branch: harness.DefaultBranch,
			}},
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure a source that is behind its remote")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "behind remote branch")
		assert.Contains(t, out, "lib-source")
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
	})
}

// TestCovPolyrepoAmbiguousLockDestinationRefusesTheFleet: the fleet lock is
// taken on each repository's push destination, so a remote that pushes to two
// URLs is a lock that would exist in two places and coordinate nothing. The
// run refuses with E336 before planning, and no repository is mutated.
func TestCovPolyrepoAmbiguousLockDestinationRefusesTheFleet(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	control.AddBareRemote()
	first := filepath.Join(t.TempDir(), "first.git")
	second := filepath.Join(t.TempDir(), "second.git")
	control.Git("init", "-q", "--bare", first)
	control.Git("init", "-q", "--bare", second)
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", first)
	control.Git("-C", "sources/lib", "remote", "set-url", "--push", "--add", "origin", first)
	control.Git("-C", "sources/lib", "remote", "set-url", "--push", "--add", "origin", second)

	cfg := covPolyrepoFile()
	cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
		"lib-source": {Commit: &models.CommitConfig{
			Enabled: models.Bool(true), Push: true, Remote: "origin",
			Branch: harness.DefaultBranch, Verify: models.Bool(false),
		}},
	}
	control.WriteConfigModel(cfg)
	control.Commit("chore: configure a source with two push destinations")

	res := control.CommandEnv(harness.LockEnabled)
	assert.Equal(t, 1, res.Code)
	assert.True(t, harness.IsCodePresent(res.Events, "E336"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, covPolyrepoOutput(res), "push destination")
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
}

// TestCovPolyrepoRevertOnFailRestoresTheOwningRepository: `revertOnFail` puts
// the package folder back the way the run found it, and in a composed fleet
// the folder belongs to a source repository rather than to the control
// checkout. A control-owned package reverts in the control repository, and
// neither revert reaches into the other repository's working tree.
func TestCovPolyrepoRevertOnFailRestoresTheOwningRepository(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	control.SeedPackage("packages", "tool")
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := covPolyrepoFile()
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs":  {Path: models.PathList{"sources/lib/packages"}, RevertOnFail: models.Bool(true)},
		"tools": {Path: models.PathList{"packages"}, RevertOnFail: models.Bool(true)},
	}
	cfg.Scripts["publish"] = models.Script{"echo scribbled >> main.txt", "exit 3"}
	control.WriteConfigModel(cfg)
	control.Commit("chore: configure a fleet that reverts a failed publish")

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	assert.Equal(t, "lib\n", readFileString(t, control.Path("sources", "lib", "packages", "lib", "main.txt")),
		"the source package folder is restored inside the source repository")
	assert.Equal(t, "tool\n", readFileString(t, control.Path("packages", "tool", "main.txt")),
		"and the control-owned package folder in the control repository")
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.Empty(t, control.TagList())
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
