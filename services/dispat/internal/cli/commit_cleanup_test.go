// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommitCleanupUsesActualEditorAndPreservesExplicitModes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, exec.Command("git", "init", "--quiet").Run())
	path := filepath.Join(root, "message")
	marker := filepath.Join(root, "edited")
	t.Setenv("DISPAT_COMMIT_EDITED", marker)
	text := "feat(core): add\n\n# retained only without stripping\n"
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	got, err := cleanCommitMessage(path, "default")
	require.NoError(t, err)
	assert.Equal(t, text, string(got))
	require.NoError(t, os.WriteFile(marker, nil, 0600))
	got, err = cleanCommitMessage(path, "default")
	require.NoError(t, err)
	assert.Equal(t, "feat(core): add\n", string(got))
	for _, mode := range []string{"whitespace", "verbatim"} {
		got, err = cleanCommitMessage(path, mode)
		require.NoError(t, err)
		assert.Equal(t, text, string(got))
	}
	require.NoError(t, exec.Command("git", "config", "core.commentChar", ";").Run())
	require.NoError(t, os.WriteFile(path, []byte("; comment\nfeat(core): add\n"), 0600))
	got, err = cleanCommitMessage(path, "strip")
	require.NoError(t, err)
	assert.Equal(t, "feat(core): add\n", string(got))
}

func TestCommitScissorsOnlyTruncatesWhenEdited(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, exec.Command("git", "init", "--quiet").Run())
	marker := filepath.Join(root, "edited")
	t.Setenv("DISPAT_COMMIT_EDITED", marker)
	path := filepath.Join(root, "message")
	text := "feat(core): add\n\n# ------------------------ >8 ------------------------\nnot part of the commit\n"
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	got, err := cleanCommitMessage(path, "scissors")
	require.NoError(t, err)
	assert.Equal(t, text, string(got))
	require.NoError(t, os.WriteFile(marker, nil, 0600))
	got, err = cleanCommitMessage(path, "scissors")
	require.NoError(t, err)
	assert.Equal(t, "feat(core): add\n", string(got))
}

func TestCommitInputBoundsAndAtomicReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "message")
	require.NoError(t, os.WriteFile(path, []byte("abcd"), 0640))
	_, err := readCommitInput(path, 3)
	require.ErrorContains(t, err, "exceeds")
	got, err := readCommitInput(path, 4)
	require.NoError(t, err)
	assert.Equal(t, "abcd", string(got))
	require.NoError(t, replaceCommitMessage(path, []byte("replacement")))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0640), info.Mode().Perm())
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	link := filepath.Join(root, "linked")
	require.NoError(t, os.Symlink(path, link))
	_, err = readCommitInput(link, 20)
	require.ErrorContains(t, err, "regular file")
	require.ErrorContains(t, replaceCommitMessage(link, nil), "regular file")
	_, err = cleanCommitMessage(filepath.Join(root, "missing"), "strip")
	require.Error(t, err)
	require.Error(t, replaceCommitMessage(filepath.Join(root, "missing"), nil))
}

func TestCommitValidatorRejectsInvalidInternalInputs(t *testing.T) {
	var out bytes.Buffer
	assert.Equal(t, 1, runCommitMessageValidator(nil, &out))
	assert.Contains(t, out.String(), "requires one message file")
	root := t.TempDir()
	config := filepath.Join(root, "parser.json")
	t.Setenv("DISPAT_COMMIT_PARSER", config)
	out.Reset()
	assert.Equal(t, 1, runCommitMessageValidator([]string{"missing"}, &out))
	assert.Contains(t, out.String(), "read parser")
	require.NoError(t, os.WriteFile(config, []byte("invalid JSON"), 0600))
	out.Reset()
	assert.Equal(t, 1, runCommitMessageValidator([]string{"missing"}, &out))
	assert.Contains(t, out.String(), "decode parser")
	require.NoError(t, os.WriteFile(config, []byte("{}"), 0600))
	out.Reset()
	assert.Equal(t, 1, runCommitMessageValidator([]string{"missing"}, &out))
	assert.Contains(t, out.String(), "clean commit")
}

func TestCommitScissorsUsesConfiguredCommentPrefix(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, exec.Command("git", "init", "--quiet").Run())
	marker := filepath.Join(root, "edited")
	require.NoError(t, os.WriteFile(marker, nil, 0600))
	t.Setenv("DISPAT_COMMIT_EDITED", marker)
	path := filepath.Join(root, "message")
	for _, tc := range []struct{ key, prefix string }{{"core.commentChar", ";"}, {"core.commentString", "//"}} {
		require.NoError(t, exec.Command("git", "config", tc.key, tc.prefix).Run())
		require.NoError(t, os.WriteFile(path, []byte("feat(core): add\n\n"+tc.prefix+" ------------------------ >8 ------------------------\nremoved\n"), 0600))
		got, err := cleanCommitMessage(path, "scissors")
		require.NoError(t, err)
		assert.Equal(t, "feat(core): add\n", string(got))
	}
}

func TestCommitMessageWriteFailureIsReportedWithoutTruncation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "readonly")
	require.NoError(t, os.Mkdir(dir, 0700))
	message := filepath.Join(dir, "message")
	before := []byte("feat(core): add\n")
	require.NoError(t, os.WriteFile(message, before, 0600))
	parser := filepath.Join(root, "parser")
	require.NoError(t, os.WriteFile(parser, []byte("{}"), 0600))
	t.Setenv("DISPAT_COMMIT_PARSER", parser)
	t.Setenv("DISPAT_COMMIT_CLEANUP", "verbatim")
	require.NoError(t, os.Chmod(dir, 0500))
	defer os.Chmod(dir, 0700)
	var out bytes.Buffer
	assert.Equal(t, 1, runCommitMessageValidator([]string{message}, &out))
	assert.Contains(t, out.String(), "write commit message")
	got, err := os.ReadFile(message)
	require.NoError(t, err)
	assert.Equal(t, before, got)
	require.NoError(t, os.Chmod(message, 0))
	defer os.Chmod(message, 0600)
	_, err = readCommitInput(message, 1024)
	require.Error(t, err)
}
