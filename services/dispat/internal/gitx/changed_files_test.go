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

// TestChangedFilesListTheChangesSection62Counts pins ChangedFiles to the
// changed-file list of §6.2, one shape per commit: a root commit lists every
// path, a rename both of its paths (vector 29) however similar git finds the
// two versions, a deletion the deleted path, a mode-only change the path, a
// merge its changes against the first parent, and a commit that changes
// nothing no path at all. Names git would quote, a letter outside ASCII, a tab
// or a space, come back exactly as the repository records them.
func TestChangedFilesListTheChangesSection62Counts(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	commit := func(msg string) string {
		t.Helper()
		git("add", "-A")
		git("commit", "-q", "--allow-empty", "-m", msg)
		return git("rev-parse", "HEAD")
	}

	rootCommit := git("rev-list", "--max-parents=0", "HEAD")
	write("packages/core/big.txt", strings.Repeat("a line that survives the move\n", 40))
	write("packages/core/tool.sh", "#!/bin/sh\n")
	commit("feat(core): a file worth moving")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "util"), 0o755))
	git("mv", "packages/core/big.txt", "packages/util/big.txt")
	moved := commit("refactor: move it to util")
	require.NoError(t, os.Remove(filepath.Join(root, "packages", "core", "main.txt")))
	deleted := commit("fix(core): drop the main file")
	require.NoError(t, os.Chmod(filepath.Join(root, "packages", "core", "tool.sh"), 0o755))
	modeOnly := commit("fix(core): make the tool executable")
	git("checkout", "-q", "-b", "side")
	write("packages/util/side.txt", "s")
	commit("fix(util): side work")
	git("checkout", "-q", "-")
	write("packages/core/main two.txt", "m")
	write("packages/core/naïve.txt", "n")
	write("packages/core/tab\tname.txt", "t")
	quoted := commit("fix: names git quotes")
	git("merge", "-q", "--no-ff", "-m", "chore: merge side", "side")
	merge := git("rev-parse", "HEAD")
	empty := commit("chore: nothing changed")

	commits, err := cli.Commits(ctx, "")
	require.NoError(t, err)
	shas := make([]string, len(commits))
	for i, c := range commits {
		assert.True(t, c.AreFilesDeferred)
		assert.Empty(t, c.Files)
		shas[i] = c.SHA
	}
	got, err := cli.ChangedFiles(ctx, shas)
	require.NoError(t, err)
	require.Len(t, got, len(commits))

	assert.Equal(t, []string{"packages/core/main.txt"}, got[rootCommit], "a root commit lists every path it adds")
	assert.Equal(t, []string{"packages/core/big.txt", "packages/util/big.txt"}, got[moved],
		"a rename lists the path it left and the path it made")
	assert.Equal(t, []string{"packages/core/main.txt"}, got[deleted], "a deletion lists the deleted path")
	assert.Equal(t, []string{"packages/core/tool.sh"}, got[modeOnly], "a mode-only change lists the path")
	assert.Equal(t, []string{"packages/core/main two.txt", "packages/core/naïve.txt", "packages/core/tab\tname.txt"},
		got[quoted], "every name as the repository records it, never quoted")
	assert.Equal(t, []string{"packages/util/side.txt"}, got[merge], "a merge lists its first-parent diff")
	assert.Nil(t, got[empty], "an empty commit lists nothing")
}

// TestChangedFilesRefusesABrokenListing: the listing's framing is what
// separates one commit's paths from the next, so a record that breaks it is
// an error rather than a shorter list of paths.
func TestChangedFilesRefusesABrokenListing(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for name, out := range map[string]string{
		"no record separator":  sha + logFieldSep + "\x00",
		"no field separator":   logRecordSep + sha + "\x00\npath\x00",
		"a short id":           logRecordSep + "abc" + logFieldSep + "\x00",
		"an unterminated path": logRecordSep + sha + logFieldSep + "\x00\npath",
		// Read as the next record, which it cannot be: refused, never
		// taken for another commit or dropped from this one.
		"a path opening with the separator byte": logRecordSep + sha + logFieldSep + "\x00\n" + logRecordSep + "x\x00",
	} {
		_, err := parseChangedFiles(out)
		assert.ErrorContains(t, err, "malformed changed files record", name)
	}
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
