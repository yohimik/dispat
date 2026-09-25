// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for the standalone `dispat commit` step inside a composed
// fleet. Outside a release stage the step owns the whole transaction: it
// writes the source commit under whatever identity the invocation names, tags
// and pushes it, and then advances the control gitlink itself, because nothing
// else is going to. That makes it the one place several record paths are
// reachable from a command line.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covPolyrepoPushableFleet assembles one source and one control repository,
// each with its own bare remote and its branch already published, which is
// what any scenario that pushes needs before it starts.
func covPolyrepoPushableFleet(t *testing.T) (control *harness.Repo, sourceBare, controlBare string) {
	t.Helper()
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.WriteFile("shared/lock.txt", "first\n")
	source.Commit("feat(lib): bootstrap library")

	control = harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	sourceBare = filepath.Join(t.TempDir(), "source.git")
	control.Git("init", "-q", "--bare", sourceBare)
	control.Git("-C", sourceBare, "symbolic-ref", "HEAD", "refs/heads/"+harness.DefaultBranch)
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", sourceBare)
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	controlBare = filepath.Join(t.TempDir(), "control.git")
	control.Git("init", "-q", "--bare", controlBare)
	control.Git("-C", controlBare, "symbolic-ref", "HEAD", "refs/heads/"+harness.DefaultBranch)
	control.Git("remote", "add", "origin", controlBare)
	return control, sourceBare, controlBare
}

// TestCovPolyrepoCommitStepOwnsItsWholeTransaction: invoked from a shell
// rather than from a release stage, `dispat commit` is the whole record. Its
// overrides replace the configured commit policy for that one invocation, the
// push covers the branch it wrote and the tags it created, and because no
// enclosing release is going to do it afterwards the step also moves and
// pushes the control gitlink, which it may only do once the source tag is
// reachable from the source remote.
func TestCovPolyrepoCommitStepOwnsItsWholeTransaction(t *testing.T) {
	t.Run("a tagged step commits, tags, pushes and checkpoints", func(t *testing.T) {
		control, sourceBare, controlBare := covPolyrepoPushableFleet(t)
		cfg := covPolyrepoFile()
		cfg.Spaces = map[string]models.SpaceConfig{
			"libs": {
				Path:      models.PathList{"sources/lib/packages"},
				AliasTags: []models.AliasTagConfig{{Format: "{name}-v{major}", Moving: true}},
			},
		}
		cfg.Commit = &models.CommitConfig{
			Enabled: models.Bool(true), Remote: "origin", Branch: harness.DefaultBranch,
			MessageFormat: "chore(control): checkpoint {tags}",
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure a fleet a step will record")
		control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
		controlBefore := control.Git("rev-parse", "HEAD")
		sourceBefore := control.Git("-C", "sources/lib", "rev-parse", "HEAD")

		// The work the step is being asked to record.
		control.WriteFile("sources/lib/packages/lib/generated.txt", "built\n")
		control.WriteFile("sources/lib/shared/lock.txt", "regenerated\n")

		res := control.Command("commit", "--tag", "--push",
			"--name", "step release bot", "--email", "step-release@dispat.test",
			"--remote", "origin", "--message-format", "chore(source): step recorded {tags}",
			"--include", "shared")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

		sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
		assert.NotEqual(t, sourceBefore, sourceAfter, "the step writes the source release commit")
		assert.Equal(t, "step release bot <step-release@dispat.test>",
			control.Git("-C", "sources/lib", "log", "-1", "--format=%an <%ae>"),
			"the invocation's identity replaces the configured one")
		assert.Equal(t, "chore(source): step recorded lib@0.1.0",
			control.Git("-C", "sources/lib", "log", "-1", "--format=%s"))
		assert.Equal(t, "regenerated\n",
			control.Git("-C", "sources/lib", "show", "HEAD:shared/lock.txt")+"\n",
			"the invocation's include path is staged with the package folder")

		assert.Equal(t, sourceAfter, control.Git("-C", sourceBare, "rev-parse", "refs/heads/"+harness.DefaultBranch))
		assert.Equal(t, sourceAfter, control.Git("-C", sourceBare, "rev-parse", "lib@0.1.0^{commit}"))
		assert.Equal(t, sourceAfter, control.Git("-C", sourceBare, "rev-parse", "lib-v0^{commit}"),
			"the moving alias is pushed with the release")

		controlAfter := control.Git("rev-parse", "HEAD")
		assert.NotEqual(t, controlBefore, controlAfter, "the step owns the checkpoint outside a release")
		assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
		assert.Equal(t, "chore(control): checkpoint lib@0.1.0", control.Git("log", "-1", "--format=%s"))
		assert.Equal(t, controlAfter, control.Git("-C", controlBare, "rev-parse", "refs/heads/"+harness.DefaultBranch))
	})

	t.Run("an untagged step checkpoints against the pushed branch", func(t *testing.T) {
		control, sourceBare, controlBare := covPolyrepoPushableFleet(t)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.Commit = &models.CommitConfig{
			Enabled: models.Bool(true), Remote: "origin", Branch: harness.DefaultBranch,
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure a fleet an untagged step will record")
		control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
		control.WriteFile("sources/lib/packages/lib/generated.txt", "built\n")

		res := control.Command("commit", "--push")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

		sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
		assert.Empty(t, polyrepoTags(control, "sources/lib"),
			"no tag was asked for, so the branch is the only durable record")
		assert.Equal(t, sourceAfter, control.Git("-C", sourceBare, "rev-parse", "refs/heads/"+harness.DefaultBranch),
			"the checkpoint may only reference a revision the source remote carries")
		assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
		assert.Equal(t, control.Git("rev-parse", "HEAD"),
			control.Git("-C", controlBare, "rev-parse", "refs/heads/"+harness.DefaultBranch))
	})

	t.Run("a step told not to force leaves a moving alias unforced", func(t *testing.T) {
		control, sourceBare, _ := covPolyrepoPushableFleet(t)
		cfg := covPolyrepoFile()
		cfg.Spaces = map[string]models.SpaceConfig{
			"libs": {
				Path:      models.PathList{"sources/lib/packages"},
				AliasTags: []models.AliasTagConfig{{Format: "{name}-v{major}", Moving: true}},
			},
		}
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(false)}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure a fleet whose step must not force")
		control.WriteFile("sources/lib/packages/lib/generated.txt", "built\n")

		res := control.Command("commit", "--tag", "--push", "--no-force",
			"--remote", "origin")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
		assert.Equal(t, sourceAfter, control.Git("-C", sourceBare, "rev-parse", "lib@0.1.0^{commit}"))
		assert.Equal(t, sourceAfter, control.Git("-C", sourceBare, "rev-parse", "lib-v0^{commit}"),
			"a first write of the alias needs no force at all")
	})
}

// TestCovPolyrepoBeforePushHookCannotMoveTheRecordedRevision: the hooks around
// a push are user scripts, and a script that commits in the repository about
// to be pushed would make the push publish something the release never
// planned. The pin is re-proved inside the push transaction, after the hook and
// before the push, so the push does not happen and the run says which revision
// it recorded.
func TestCovPolyrepoBeforePushHookCannotMoveTheRecordedRevision(t *testing.T) {
	control, sourceBare, _ := covPolyrepoPushableFleet(t)
	cfg := covPolyrepoFile()
	cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(false)}
	cfg.Scripts["sneak"] = models.Script{"git commit -q --allow-empty -m 'chore: a hook that moved HEAD'"}
	cfg.Run = &models.RunConfig{BeforePush: []string{"sneak"}}
	cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
		"lib-source": {Commit: &models.CommitConfig{
			Enabled: models.Bool(true), Push: true, Remote: "origin",
			Branch: harness.DefaultBranch,
		}},
	}
	control.WriteConfigModel(cfg)
	control.Commit("chore: configure a source whose push hook commits")

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	assert.True(t, harness.IsCodePresent(res.Events, "E335"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	out := covPolyrepoOutput(res)
	assert.Contains(t, out, "HEAD moved from recorded source revision")
	assert.Contains(t, out, "lib-source")

	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0",
		"the tag was already written at the revision the run planned")
	assert.NotContains(t, control.Git("-C", sourceBare, "tag", "--list"), "lib@0.1.0",
		"nothing reached the remote")
	assert.Equal(t, control.Git("-C", "sources/lib", "rev-parse", "HEAD~1"),
		control.Git("-C", "sources/lib", "rev-parse", "lib@0.1.0^{commit}"),
		"the tag still names the planned revision rather than the hook's commit")
}
