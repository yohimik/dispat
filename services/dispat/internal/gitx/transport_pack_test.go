// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"bytes"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestCommitPackStreamsToAnotherStoreWithoutRemoteRefs(t *testing.T) {
	source, git := initRepo(t)
	content := bytes.Repeat([]byte("portable artifact\n"), 1024)
	require.NoError(t, os.WriteFile(filepath.Join(source, "artifact.bin"), content, 0o644))
	runGit(t, source, "add", "artifact.bin")
	runGit(t, source, "commit", "-q", "-m", "result")
	oid := strings.TrimSpace(runGit(t, source, "rev-parse", "HEAD"))
	var pack bytes.Buffer
	require.NoError(t, git.ExportCommitPack(t.Context(), oid, &pack))
	require.NotZero(t, pack.Len())
	storeDir := filepath.Join(t.TempDir(), "first.git")
	out, err := exec.Command("git", "init", "-q", "--bare", storeDir).CombinedOutput()
	require.NoError(t, err, "%s", out)
	store := &LocalGitx{Dir: storeDir, Log: zerolog.Nop()}
	require.NoError(t, store.ImportCommitPack(t.Context(), oid, bytes.NewReader(pack.Bytes()), int64(pack.Len())))
	got, err := store.ResolveCommit(t.Context(), oid)
	require.NoError(t, err)
	require.Equal(t, oid, got)
	require.Equal(t, content, []byte(runGit(t, storeDir, "show", oid+":artifact.bin")))
	require.Empty(t, strings.TrimSpace(runGit(t, storeDir, "for-each-ref", "--format=%(refname)")))
	require.Error(t, store.ImportCommitPack(t.Context(), oid, bytes.NewReader(pack.Bytes()), int64(pack.Len()-1)))
	require.Error(t, store.ImportCommitPack(t.Context(), strings.Repeat("a", 40), bytes.NewReader(pack.Bytes()), int64(pack.Len())))
}

func TestCommitPackDeltaKeepsOutcomeWithoutResendingClaimInputs(t *testing.T) {
	source, git := initRepo(t)
	input := make([]byte, 256<<10)
	_, err := rand.Read(input)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(source, "input.bin"), input, 0o644))
	runGit(t, source, "add", "input.bin")
	runGit(t, source, "commit", "-q", "-m", "claim")
	claim := strings.TrimSpace(runGit(t, source, "rev-parse", "HEAD"))
	storeDir := filepath.Join(t.TempDir(), "first.git")
	out, err := exec.Command("git", "clone", "-q", "--bare", source, storeDir).CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.NoError(t, os.WriteFile(filepath.Join(source, "output.txt"), []byte("finished\n"), 0o644))
	runGit(t, source, "add", "output.txt")
	runGit(t, source, "commit", "-q", "-m", "outcome")
	result := strings.TrimSpace(runGit(t, source, "rev-parse", "HEAD"))
	cacheDir := filepath.Join(t.TempDir(), "worker.git")
	out, err = exec.Command("git", "clone", "-q", "--bare", source, cacheDir).CombinedOutput()
	require.NoError(t, err, "%s", out)
	runGit(t, cacheDir, "update-ref", "-d", "HEAD")
	git = &LocalGitx{Dir: cacheDir, Log: zerolog.Nop()}
	var full, delta bytes.Buffer
	require.NoError(t, git.ExportCommitPack(t.Context(), result, &full))
	require.NoError(t, git.ExportCommitPackSince(t.Context(), CommitPackDeltaOptions{
		CommitOID: result, BaseOID: claim, Writer: &delta}))
	require.Less(t, delta.Len(), full.Len()/4)
	store := &LocalGitx{Dir: storeDir, Log: zerolog.Nop()}
	require.NoError(t, store.ImportCommitPack(t.Context(), result, bytes.NewReader(delta.Bytes()), int64(delta.Len())))
	require.Equal(t, "finished\n", runGit(t, storeDir, "show", result+":output.txt"))
	require.Equal(t, input, []byte(runGit(t, storeDir, "show", result+":input.bin")))
	require.Equal(t, claim, strings.TrimSpace(runGit(t, storeDir, "rev-parse", result+"^")))
	require.Error(t, git.ExportCommitPackSince(t.Context(), CommitPackDeltaOptions{
		CommitOID: claim, BaseOID: result, Writer: &bytes.Buffer{}}))
}
