// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for the git layer, driven through the flows that reach
// it: a remote URL carrying a credential, a staged rename under the dirty
// guard, a remote that already records the version a release is about to plan,
// and a release whose commit has nothing to stage.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestGitRemoteCredentialsNeverReachTheLog: a release's error text reaches
// hook scripts through DISPAT_ERROR and its log reaches whatever CI ingests,
// so the remote a push names is recorded with its user information, query and
// fragment taken out. The credential is in the URL because that is how a CI
// runner is given one.
func TestGitRemoteCredentialsNeverReachTheLog(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	// Port 1 refuses immediately, so the run fails on the remote rather than
	// waiting on one.
	r.Git("remote", "add", "origin",
		"https://ci-bot:s3cr3t@127.0.0.1:1/acme/mono.git?token=abc123#fragment")

	res := r.Release()
	require.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	out := res.Stdout + res.Stderr
	assert.Contains(t, out, "REDACTED", "the remote is still named, without its secrets")
	assert.NotContains(t, out, "s3cr3t", "the password must not be recorded anywhere")
	assert.NotContains(t, out, "token=abc123", "nor a credential carried in the query")
	assert.NotContains(t, out, "fragment", "nor anything after it")
	assert.Empty(t, r.TagList(), "and the refusal came before any release work")
}

// TestGitDirtyGuardReadsARenameAsOneEntry: git's machine-readable status
// writes a rename as the destination followed by the source, and only the
// first of the two carries a status prefix. Reading the second as an entry of
// its own would report a path with its first three characters eaten — a file
// nobody has, in a refusal telling somebody to go and commit it.
func TestGitDirtyGuardReadsARenameAsOneEntry(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.RevertOnFail = models.Bool(true)
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/renameable.txt", "work in progress\n")
	r.Commit("feat(core): bootstrap")

	// git mv stages the rename, which is the shape porcelain reports as R.
	r.Git("mv", "packages/core/renameable.txt", "packages/core/renamed.txt")

	res := r.Release()
	require.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "pre-existing local changes")
	assert.Contains(t, res.Stdout, "packages/core/renamed.txt", "the destination the rename created")
	assert.NotContains(t, res.Stdout, "kages/core/renameable.txt",
		"the source is the rename's second half, not an entry with a status prefix to strip")
	assert.Empty(t, r.TagList(), "the guard refuses before anything is released")
	assert.FileExists(t, r.Path("packages", "core", "renamed.txt"), "and the work is untouched")
}

// TestGitPushRefusesAPlanTheRemoteAlreadyRecorded: a checkout that plans a
// version the remote has already recorded is refused before it runs anything,
// and `commit.force` decides nothing about it.
//
// This is the shape the older behaviour was written for, where such a run was
// allowed to reach its push and the tag was skipped or force-replaced there.
// Both answers planned a published version a second time; the refusal is the
// answer now, and the remote's annotated tag is read peeled, because the
// listing carries the ref and its target and only one of the two names a
// commit.
func TestGitPushRefusesAPlanTheRemoteAlreadyRecorded(t *testing.T) {
	setup := func(t *testing.T, force bool) (*harness.Repo, string, string) {
		t.Helper()
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Commit = &models.CommitConfig{
			Enabled: models.Bool(true), Push: true, Force: models.Bool(force),
		}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")
		bare := r.AddBareRemote()
		r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
		bootstrap := r.Git("rev-parse", "HEAD")
		// Somebody's earlier run left the tag there, annotated the way a
		// release writes one.
		bareGit(t, bare, "-c", "user.email=other@dispat.test", "-c", "user.name=other clone",
			"tag", "-a", "core@0.1.0", "-m", "an earlier release", harness.DefaultBranch)
		return r, bare, bootstrap
	}

	// remoteTagCommit is what refs/tags/core@0.1.0 peels to on the remote.
	remoteTagCommit := func(t *testing.T, bare string) string {
		t.Helper()
		return strings.TrimSpace(bareGit(t, bare, "rev-list", "-n", "1", "core@0.1.0"))
	}

	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit.force=%v", force), func(t *testing.T) {
			r, bare, bootstrap := setup(t, force)

			res := r.Release()
			require.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.True(t, harness.IsCodePresent(res.Events, "E196"), "stdout:\n%s", res.Stdout)
			assert.Equal(t, bootstrap, remoteTagCommit(t, bare),
				"the published ref is exactly where it was")
			assert.Empty(t, r.TagList(), "and this clone wrote none of its own; tags: %v", r.TagList())
		})
	}
}

// TestGitReleaseCommitIsSkippedWhenNothingWasStaged: with the changelog off
// and no manifest to rewrite, the release commit would have nothing in it. An
// empty commit is not a record of anything, so none is made, and the tag then
// names the commit the release was planned on — which is where it would have
// pointed had there been no commit stage at all.
func TestGitReleaseCommitIsSkippedWhenNothingWasStaged(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(false)}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	planned := r.Git("rev-parse", "HEAD")

	res := r.ReleaseOK()
	assert.NotContains(t, res.Stdout, "created release commit")
	assert.Equal(t, planned, r.Git("rev-parse", "HEAD"), "no commit was made")
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	assert.Equal(t, planned, r.Git("rev-list", "-n", "1", "core@0.1.0"),
		"and the tag names the commit that was planned")

	// A second run converges: the package has nothing pending, and the
	// staging question is asked again on the same clean tree.
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("core@"), "tags: %v", r.TagList())
}
