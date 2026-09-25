// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// tagRepo is a repository with two commits on main and one on a branch main
// never merged, and a release of package a planned at 1.1.0.
func tagRepo(t *testing.T) (dir string, git *gitx.LocalGitx, rel *plan.Release, run func(...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir = t.TempDir()
	run = func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	run("commit", "-q", "--allow-empty", "-m", "first")
	run("checkout", "-q", "-b", "side")
	run("commit", "-q", "--allow-empty", "-m", "unmerged")
	run("checkout", "-q", "main")
	run("commit", "-q", "--allow-empty", "-m", "second")
	rel = &plan.Release{Pkg: &model.Package{Name: "a", Dir: dir, Space: &model.Space{Name: "libs"}},
		Bump: ccme.BumpMinor, NewWork: true, Next: ccme.Version{Major: 1, Minor: 1}}
	return dir, &gitx.LocalGitx{Dir: dir}, rel, run
}

// TestCreateReleaseTagWritesANewNameInOneProcess: a name the repository does
// not carry, which is nearly every release, costs the write and nothing else.
func TestCreateReleaseTagWritesANewNameInOneProcess(t *testing.T) {
	_, git, rel, run := tagRepo(t)
	before := gitx.GitInvocations()
	require.NoError(t, CreateReleaseTag(context.Background(), git, rel, false, zerolog.Nop()))
	assert.Equal(t, uint64(1), gitx.GitInvocations()-before)
	assert.Equal(t, run("rev-parse", "HEAD"), run("rev-parse", "a@1.1.0^{commit}"))
}

// TestCreateReleaseTagSkipsTheSameTagAtTheReleaseCommit is the W223 skip on a
// real repository: the flow tagged early, and the write is refused and
// recognised rather than reported.
func TestCreateReleaseTagSkipsTheSameTagAtTheReleaseCommit(t *testing.T) {
	_, git, rel, run := tagRepo(t)
	run("tag", "-a", "-m", "early", "a@1.1.0")
	var logged bytes.Buffer
	require.NoError(t, CreateReleaseTag(context.Background(), git, rel, false, zerolog.New(&logged)))
	assert.Contains(t, logged.String(), plan.CodeTagExists)
	assert.Equal(t, "early", run("tag", "-l", "--format=%(contents:subject)", "a@1.1.0"), "the early tag is kept")
}

// TestCreateReleaseTagLeavesATagAtAnotherCommitAlone is E221 on a real
// repository: a tag HEAD reaches at another commit is left where it is, with
// and without force (configuration/records.md, Force).
func TestCreateReleaseTagLeavesATagAtAnotherCommitAlone(t *testing.T) {
	for _, isForced := range []bool{false, true} {
		_, git, rel, run := tagRepo(t)
		run("tag", "a@1.1.0", "HEAD~1")
		at := run("rev-parse", "a@1.1.0")
		err := CreateReleaseTag(context.Background(), git, rel, isForced, zerolog.Nop())
		require.ErrorIsf(t, err, ErrTagAtOtherCommit, "force %v", isForced)
		assert.Equal(t, plan.CodeTagAtOtherCommit, TagFailureCode(err))
		assert.Equalf(t, at, run("rev-parse", "a@1.1.0"), "force %v: not moved", isForced)
	}
}

// TestCreateReleaseTagRewritesAnUnreachableTagOnlyUnderForce: a tag on a
// commit HEAD cannot reach is one no baseline saw. Force rewrites it to this
// release; without force the refused write is the failure, and the tag stays.
func TestCreateReleaseTagRewritesAnUnreachableTagOnlyUnderForce(t *testing.T) {
	for _, isForced := range []bool{false, true} {
		_, git, rel, run := tagRepo(t)
		run("tag", "a@1.1.0", "side")
		side := run("rev-parse", "side")
		err := CreateReleaseTag(context.Background(), git, rel, isForced, zerolog.Nop())
		if isForced {
			require.NoError(t, err)
			assert.Equal(t, run("rev-parse", "HEAD"), run("rev-parse", "a@1.1.0^{commit}"))
			continue
		}
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrTagAtOtherCommit)
		assert.Equal(t, side, run("rev-parse", "a@1.1.0^{commit}"))
	}
}

// TestCreateReleaseTagReportsAWriteRefusedForAnotherReason: a refused write
// with no tag of that name in the way is the write's own failure, and it is
// not tried again under force.
func TestCreateReleaseTagReportsAWriteRefusedForAnotherReason(t *testing.T) {
	for _, isForced := range []bool{false, true} {
		_, git, rel, run := tagRepo(t)
		rel.Outputs = []plan.Output{{Name: plan.PackageCommitExportPrefix + plan.EnvKey("a"), Value: strings.Repeat("0", 40)}}
		var logged bytes.Buffer
		git.Log = zerolog.New(&logged).Level(zerolog.DebugLevel) // the writes, not the reads
		err := CreateReleaseTag(context.Background(), git, rel, isForced, zerolog.Nop())
		require.Errorf(t, err, "force %v", isForced)
		assert.NotErrorIs(t, err, ErrTagAtOtherCommit)
		assert.Emptyf(t, run("tag", "-l", "a@1.1.0"), "force %v", isForced)
		assert.Equalf(t, 1, strings.Count(logged.String(), `"git failed"`), "force %v: one write, no retry", isForced)
	}
}

// TestFindTagReadsOneExactName: the lookup peels an annotated tag, sees only
// what HEAD reaches, and does not take a longer name for the one asked.
func TestFindTagReadsOneExactName(t *testing.T) {
	_, git, _, run := tagRepo(t)
	run("tag", "-a", "-m", "m", "a@1.0.0")
	run("tag", "b@2.0.0", "side")
	run("tag", "a@1.0.0-rc.0")
	ctx := context.Background()

	tag, found, err := git.FindTag(ctx, "a@1.0.0")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, gitx.Tag{Name: "a@1.0.0", Commit: run("rev-parse", "HEAD")}, tag, "peeled to the commit")

	_, found, err = git.FindTag(ctx, "b@2.0.0")
	require.NoError(t, err)
	assert.False(t, found, "a tag HEAD cannot reach is one the baseline never saw")

	_, found, err = git.FindTag(ctx, "a@1")
	require.NoError(t, err)
	assert.False(t, found)
	_, _, err = git.FindTag(ctx, "a@*")
	assert.Error(t, err, "a pattern is not a tag name")
}
