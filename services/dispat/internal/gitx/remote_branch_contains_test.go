// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifyRemoteBranchContainsPreparedReleaseHead(t *testing.T) {
	root, git := initRepo(t)
	addBareRemote(t, root)
	branch, err := git.CurrentBranch(t.Context())
	require.NoError(t, err)
	prepared, err := git.HeadSHA(t.Context())
	require.NoError(t, err)
	runGit(t, root, "push", "-q", "origin", branch)
	refsBefore, err := exec.Command("git", "-C", root, "show-ref").Output()
	require.NoError(t, err)
	require.NoError(t, git.VerifyRemoteBranchContains(t.Context(), "origin", branch, prepared))
	refsAfter, err := exec.Command("git", "-C", root, "show-ref").Output()
	require.NoError(t, err)
	require.Equal(t, string(refsBefore), string(refsAfter),
		"ancestry proof imports objects without changing local refs")
	runGit(t, root, "commit", "-q", "--allow-empty", "-m", "merge after release preparation")
	runGit(t, root, "push", "-q", "origin", branch)
	require.NoError(t, git.VerifyRemoteBranchContains(t.Context(), "origin", branch, prepared))
	newHead, err := git.HeadSHA(t.Context())
	require.NoError(t, err)
	require.ErrorContains(t, git.VerifyRemoteBranchContains(t.Context(), "origin", branch, newHead+"0"),
		"not a full object ID")
	runGit(t, root, "checkout", "-q", "--orphan", "unrelated")
	runGit(t, root, "rm", "-qrf", ".")
	runGit(t, root, "commit", "-q", "--allow-empty", "-m", "unrelated release branch")
	runGit(t, root, "push", "-q", "--force", "origin", "HEAD:refs/heads/"+branch)
	require.ErrorContains(t, git.VerifyRemoteBranchContains(t.Context(), "origin", branch, prepared),
		"does not contain prepared head")
}
