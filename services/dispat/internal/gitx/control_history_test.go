// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func controlHistoryGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func TestControlGitlinkHistoryReadsReachableHistoryInOneStream(t *testing.T) {
	control, cli := initRepo(t)
	source, _ := initRepo(t)
	oldSource := controlHistoryGit(t, source, "rev-parse", "HEAD")
	controlHistoryGit(t, control, "-c", "protocol.file.allow=always", "submodule", "add", "-q", "--name", "source", source, "sources/lib")
	controlHistoryGit(t, control, "commit", "-qm", "chore: add source", "-m", "body\n\nCheckpoint-Tag: lib@1.0.0")
	addCommit := controlHistoryGit(t, control, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(control, "sources", "lib", "next"), []byte("next"), 0o644))
	controlHistoryGit(t, filepath.Join(control, "sources", "lib"), "config", "user.name", "Test")
	controlHistoryGit(t, filepath.Join(control, "sources", "lib"), "config", "user.email", "test@example.com")
	controlHistoryGit(t, filepath.Join(control, "sources", "lib"), "add", "next")
	controlHistoryGit(t, filepath.Join(control, "sources", "lib"), "commit", "-qm", "feat(lib): next")
	newSource := controlHistoryGit(t, control, "-C", "sources/lib", "rev-parse", "HEAD")
	controlHistoryGit(t, control, "add", "sources/lib")
	controlHistoryGit(t, control, "commit", "-qm", "chore: move source")
	moveCommit := controlHistoryGit(t, control, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(control, "control-only"), []byte("control"), 0o644))
	controlHistoryGit(t, control, "add", "control-only")
	controlHistoryGit(t, control, "commit", "-qm", "chore: control only")
	controlOnly := controlHistoryGit(t, control, "rev-parse", "HEAD")

	history, err := cli.ControlGitlinkHistory(context.Background())
	require.NoError(t, err)
	require.Len(t, history, 4)
	assert.Equal(t, []string{controlOnly, moveCommit, addCommit},
		[]string{history[0].SHA, history[1].SHA, history[2].SHA})
	assert.Empty(t, history[0].Gitlinks, "ordinary commits remain in the chain without fake transitions")
	assert.Equal(t, []string{"control-only"}, history[0].Files)
	assert.Equal(t, GitlinkTransition{From: oldSource, To: newSource}, history[1].Gitlinks["sources/lib"])
	assert.Equal(t, []string{"sources/lib"}, history[1].Files)
	assert.Equal(t, GitlinkTransition{From: strings.Repeat("0", len(oldSource)), To: oldSource},
		history[2].Gitlinks["sources/lib"])
	assert.ElementsMatch(t, []string{".gitmodules", "sources/lib"}, history[2].Files)
	assert.Equal(t, "Test", history[2].AuthorName)
	assert.Equal(t, "test@example.com", history[2].AuthorEmail)
	assert.Equal(t, "chore: add source\n\nbody\n\nCheckpoint-Tag: lib@1.0.0", history[2].Message)
	assert.Empty(t, history[3].Parents, "the root is diffed against the empty tree")
	for i := 0; i < 3; i++ {
		require.Len(t, history[i].Parents, 1)
		assert.Equal(t, history[i+1].SHA, history[i].Parents[0])
	}
}

func TestControlGitlinkHistoryRetainsBranchCheckpointAndMergeDelta(t *testing.T) {
	control, cli := initRepo(t)
	source, _ := initRepo(t)
	baseBranch := controlHistoryGit(t, control, "rev-parse", "--abbrev-ref", "HEAD")
	oldSource := controlHistoryGit(t, source, "rev-parse", "HEAD")
	controlHistoryGit(t, control, "-c", "protocol.file.allow=always", "submodule", "add", "-q", "--name", "source", source, "sources/lib")
	controlHistoryGit(t, control, "commit", "-qm", "chore: add source")
	controlHistoryGit(t, control, "checkout", "-qb", "checkpoint")

	sourceCheckout := filepath.Join(control, "sources", "lib")
	controlHistoryGit(t, sourceCheckout, "config", "user.name", "Test")
	controlHistoryGit(t, sourceCheckout, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(sourceCheckout, "release"), []byte("release"), 0o644))
	controlHistoryGit(t, sourceCheckout, "add", "release")
	controlHistoryGit(t, sourceCheckout, "commit", "-qm", "feat(lib): branch release")
	newSource := controlHistoryGit(t, sourceCheckout, "rev-parse", "HEAD")
	controlHistoryGit(t, control, "add", "sources/lib")
	controlHistoryGit(t, control, "commit", "-qm", "chore(release): lib@1.1.0")
	branchCheckpoint := controlHistoryGit(t, control, "rev-parse", "HEAD")

	controlHistoryGit(t, control, "checkout", "-q", baseBranch)
	require.NoError(t, os.WriteFile(filepath.Join(control, "main-only"), []byte("main"), 0o644))
	controlHistoryGit(t, control, "add", "main-only")
	controlHistoryGit(t, control, "commit", "-qm", "chore: main work")
	mainParent := controlHistoryGit(t, control, "rev-parse", "HEAD")
	controlHistoryGit(t, control, "merge", "-q", "--no-ff", "-m", "chore: merge release checkpoint", "checkpoint")
	mergeSHA := controlHistoryGit(t, control, "rev-parse", "HEAD")

	history, err := cli.ControlGitlinkHistory(context.Background())
	require.NoError(t, err)
	merge := findControlHistoryCommit(t, history, mergeSHA)
	branch := findControlHistoryCommit(t, history, branchCheckpoint)
	assert.Equal(t, []string{mainParent, branchCheckpoint}, merge.Parents)
	assert.Equal(t, GitlinkTransition{From: oldSource, To: newSource}, merge.Gitlinks["sources/lib"],
		"the merge delta is measured against its first parent")
	assert.Equal(t, GitlinkTransition{From: oldSource, To: newSource}, branch.Gitlinks["sources/lib"],
		"a release checkpoint on the merged branch remains in reachable history")
}

func findControlHistoryCommit(t *testing.T, history []ControlHistoryCommit, sha string) ControlHistoryCommit {
	t.Helper()
	for _, commit := range history {
		if commit.SHA == sha {
			return commit
		}
	}
	t.Fatalf("control commit %s not found", sha)
	return ControlHistoryCommit{}
}

func TestControlGitlinkHistoryHonorsCancellation(t *testing.T) {
	_, cli := initRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cli.ControlGitlinkHistory(ctx)
	require.Error(t, err)
}

func TestParseControlGitlinkHistoryRejectsTruncatedRecords(t *testing.T) {
	_, err := parseControlGitlinkHistory("\x00" + controlHistoryMarker + "\x00short\x00")
	require.Error(t, err)
}
