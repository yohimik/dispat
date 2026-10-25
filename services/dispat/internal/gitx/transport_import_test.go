// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestImportCommitFromAnotherRepositoryWithoutCreatingRefs(t *testing.T) {
	sourceDir, _ := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "source.txt"), []byte("snapshot bytes\n"), 0o644))
	runGit(t, sourceDir, "add", "source.txt")
	runGit(t, sourceDir, "commit", "-q", "-m", "source snapshot")
	oid := strings.TrimSpace(runGit(t, sourceDir, "rev-parse", "HEAD"))
	runGit(t, sourceDir, "tag", "-a", "source-record", "-m", "source-record", oid)

	storeDir := filepath.Join(t.TempDir(), "coordinator.git")
	out, err := exec.Command("git", "init", "-q", "--bare", storeDir).CombinedOutput()
	require.NoError(t, err, "git init --bare: %s", out)
	store := &LocalGitx{Dir: storeDir, Log: zerolog.Nop()}
	require.NoError(t, store.ImportCommitFrom(t.Context(), sourceDir, oid))
	got, err := store.ResolveCommit(t.Context(), oid)
	require.NoError(t, err)
	require.Equal(t, oid, got)
	require.Equal(t, "snapshot bytes", strings.TrimSpace(runGit(t, storeDir, "show", oid+":source.txt")))
	require.Empty(t, strings.TrimSpace(runGit(t, storeDir, "for-each-ref", "--format=%(refname)")),
		"the source import leaves no snapshot, relay, remote-tracking or tag ref")
	// A source snapshot is a detached commit made for this task, not a commit
	// advertised by a source ref. The local import must carry that exact object.
	command := exec.Command("git", "-C", sourceDir, "commit-tree", oid+"^{tree}", "-p", oid)
	command.Stdin = strings.NewReader("detached task snapshot\n")
	detached, err := command.CombinedOutput()
	require.NoError(t, err, "git commit-tree: %s", detached)
	detachedOID := strings.TrimSpace(string(detached))
	require.NoError(t, store.ImportCommitFrom(t.Context(), sourceDir, detachedOID))
	got, err = store.ResolveCommit(t.Context(), detachedOID)
	require.NoError(t, err)
	require.Equal(t, detachedOID, got)
	require.Empty(t, strings.TrimSpace(runGit(t, storeDir, "for-each-ref", "--format=%(refname)")))
	require.NoError(t, store.ImportCommitFrom(t.Context(), sourceDir, oid), "retry is idempotent")
	require.ErrorIs(t, store.ImportCommitFrom(t.Context(), sourceDir, strings.Repeat("a", 40)), ErrCommitNotOnRef)
	require.Error(t, store.ImportCommitFrom(t.Context(), sourceDir, "HEAD"), "a ref name cannot replace an exact commit id")
}
