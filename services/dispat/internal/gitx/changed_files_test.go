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

// TestChangedFilesListWhatTheNameOnlyHistoryListed pins ChangedFiles to the
// paths a history read with --name-only lists, commit for commit, over the
// shapes where a path list can differ: a root commit, a rename (log lists the
// new name alone, where diff-tree would list both), a merge (its changes
// against the first parent), a commit that changes nothing, and a path with
// characters git quotes. The expectation is read with the exact command the
// planner's history read ran before it stopped listing paths.
func TestChangedFilesListWhatTheNameOnlyHistoryListed(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return string(out)
	}
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}

	write("packages/core/big.txt", strings.Repeat("a line that survives the move\n", 40))
	git("add", ".")
	git("commit", "-qm", "feat(core): a file worth renaming")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "util"), 0o755))
	git("mv", "packages/core/big.txt", "packages/util/big.txt")
	git("commit", "-qm", "refactor: move it to util")
	git("checkout", "-q", "-b", "side")
	write("packages/util/side.txt", "s")
	git("add", ".")
	git("commit", "-qm", "fix(util): side work")
	git("checkout", "-q", "-")
	write("packages/core/main two.txt", "m")
	write("packages/core/naïve.txt", "n")
	git("add", ".")
	git("commit", "-qm", "fix: main work")
	git("merge", "-q", "--no-ff", "-m", "chore: merge side", "side")
	git("commit", "-q", "--allow-empty", "-m", "chore: nothing changed")

	// The history read as it was: one walk with the paths of every commit.
	out := git("log", "--format="+logRecordSep+"%H"+logFieldSep+"%P"+logFieldSep+
		"%an"+logFieldSep+"%ae"+logFieldSep+"%B"+logFieldSep,
		"--name-only", "--diff-merges=first-parent", "HEAD")
	want, err := parseCommits(out)
	require.NoError(t, err)
	require.Len(t, want, 7)

	commits, err := cli.Commits(ctx, "")
	require.NoError(t, err)
	require.Len(t, commits, len(want))
	shas := make([]string, len(commits))
	for i, c := range commits {
		assert.True(t, c.AreFilesDeferred)
		assert.Empty(t, c.Files)
		shas[i] = c.SHA
	}
	got, err := cli.ChangedFiles(ctx, shas)
	require.NoError(t, err)
	require.Len(t, got, len(want))
	for _, c := range want {
		assert.Equal(t, c.Files, got[c.SHA], "the paths of %q", strings.SplitN(c.Message, "\n", 2)[0])
	}
	// The shapes the comparison is about are really there.
	assert.Equal(t, []string{"packages/util/big.txt"}, got[want[4].SHA], "a rename lists its new name")
	assert.Equal(t, []string{"packages/util/side.txt"}, got[want[1].SHA], "a merge lists its first-parent diff")
	assert.Nil(t, got[want[0].SHA], "an empty commit lists nothing")
}

// TestChangedFilesAnswersEachCommitOnceInOneProcess: a list naming a commit
// twice is one question, and however many commits are asked about, one git
// process answers them.
func TestChangedFilesAnswersEachCommitOnceInOneProcess(t *testing.T) {
	_, cli := initRepo(t)
	ctx := context.Background()
	head, err := cli.HeadSHA(ctx)
	require.NoError(t, err)

	before := GitInvocations()
	files, err := cli.ChangedFiles(ctx, []string{head, head})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), GitInvocations()-before)
	assert.Equal(t, map[string][]string{head: {"packages/core/main.txt"}}, files)

	before = GitInvocations()
	files, err = cli.ChangedFiles(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, files)
	assert.Zero(t, GitInvocations()-before, "nothing to ask, nothing started")

	_, err = cli.ChangedFiles(ctx, []string{"HEAD"})
	assert.ErrorContains(t, err, "not a full commit id", "only commit ids are answered")
}
