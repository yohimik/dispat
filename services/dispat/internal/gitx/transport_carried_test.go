// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fastImportParentCommits writes thousands of unique valid commits with one
// Git process. Per-parent `commit-tree` subprocesses would make the fixture
// itself slower than the argv ceiling it is meant to test.
func fastImportParentCommits(t *testing.T, dir string, count int) []string {
	t.Helper()
	marks := filepath.Join(t.TempDir(), "parent-marks")
	var stream bytes.Buffer
	for i := 1; i <= count; i++ {
		fmt.Fprintf(&stream, "commit refs/heads/carrier-seeds\nmark :%d\n", i)
		fmt.Fprintf(&stream, "author dispat <transport@dispat.invalid> %d +0000\n", i)
		fmt.Fprintf(&stream, "committer dispat <transport@dispat.invalid> %d +0000\n", i)
		message := fmt.Sprintf("seed %d", i)
		fmt.Fprintf(&stream, "data %d\n%s\n", len(message), message)
	}
	cmd := exec.Command("git", "-C", dir, "fast-import", "--quiet", "--export-marks="+marks)
	cmd.Stdin = &stream
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "fast-import: %s", out)
	content, err := os.ReadFile(marks)
	require.NoError(t, err)
	parents := make([]string, 0, count)
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		fields := strings.Fields(line)
		require.Len(t, fields, 2)
		parents = append(parents, fields[1])
	}
	require.Len(t, parents, count)
	return parents
}

func TestCarriedCommitStreamsLargeFanInAndReadsLongParentLine(t *testing.T) {
	root, git := initRepo(t)
	const fanIn = 9000 // 369 KiB of parent OIDs, above a common argv ceiling.
	parents := fastImportParentCommits(t, root, fanIn)
	tree := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD^{tree}"))
	plumbing := NewPlumbing(git)
	commit := plumbing.CommitTreeCarried(t.Context(), tree, parents, "assignment")
	require.NoError(t, plumbing.Err())
	require.True(t, transportOID(commit))
	read, err := git.CommitParents(t.Context(), commit)
	require.NoError(t, err)
	require.Equal(t, parents, read, "the first-parent reservation and every carrier retain their exact order")
	content := runGit(t, root, "cat-file", "-p", commit)
	require.Contains(t, content, "author dispat transport <transport@dispat.invalid>")
	require.Contains(t, content, "committer dispat transport <transport@dispat.invalid>")
	require.True(t, strings.HasSuffix(content, "\ndispat transport assignment\n"))
}

func TestCarriedCommitRejectsMissingAndWrongTypeObjects(t *testing.T) {
	root, git := initRepo(t)
	commit := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	tree := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD^{tree}"))
	for name, scenario := range map[string]struct {
		tree    string
		parents []string
	}{
		"missing parent":   {tree: tree, parents: []string{strings.Repeat("a", 40)}},
		"tree as parent":   {tree: tree, parents: []string{tree}},
		"commit as tree":   {tree: commit, parents: []string{commit}},
		"duplicate parent": {tree: tree, parents: []string{commit, commit}},
	} {
		t.Run(name, func(t *testing.T) {
			plumbing := NewPlumbing(git)
			result := plumbing.CommitTreeCarried(t.Context(), scenario.tree, scenario.parents, "assignment")
			require.Empty(t, result)
			require.Error(t, plumbing.Err())
		})
	}
}

func TestCarriedCommitMatchesCommitTreeEncodingForSmallInput(t *testing.T) {
	root, git := initRepo(t)
	parent := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	tree := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD^{tree}"))
	plumbing := NewPlumbing(git)
	streamed := plumbing.CommitTreeCarried(t.Context(), tree, []string{parent}, "assignment")
	require.NoError(t, plumbing.Err())
	content := runGit(t, root, "cat-file", "-p", streamed)
	var stamp string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "author ") {
			_, stamp, _ = strings.Cut(line, "> ")
			break
		}
	}
	require.NotEmpty(t, stamp)
	t.Setenv("GIT_AUTHOR_NAME", TransportIdentityName)
	t.Setenv("GIT_AUTHOR_EMAIL", TransportIdentityEmail)
	t.Setenv("GIT_AUTHOR_DATE", stamp)
	t.Setenv("GIT_COMMITTER_NAME", TransportIdentityName)
	t.Setenv("GIT_COMMITTER_EMAIL", TransportIdentityEmail)
	t.Setenv("GIT_COMMITTER_DATE", stamp)
	canonical := strings.TrimSpace(runGit(t, root, "commit-tree", tree, "-p", parent,
		"-m", transportMessagePrefix+"assignment"))
	require.Equal(t, canonical, streamed, "streamed bytes preserve commit-tree identity and message")
}
