// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRawTransportParentsIgnoreHistoryOverlays(t *testing.T) {
	for _, overlay := range []string{"replace", "grafts", "shallow"} {
		t.Run(overlay, func(t *testing.T) {
			f := newTransportFixture(t)
			localRef := TransportRefPrefix + "exact-history"
			runGit(t, f.root, "update-ref", localRef, f.second)
			applyRawTransportOverlay(t, f, overlay)
			parents, err := f.git.CommitParents(t.Context(), f.second)
			require.NoError(t, err)
			require.Equal(t, []string{f.first}, parents)
			root, err := f.git.CommitParents(t.Context(), f.first)
			require.NoError(t, err)
			require.Empty(t, root)
			history, err := f.git.FirstParentOIDs(t.Context(), f.second, f.first)
			require.NoError(t, err)
			require.Equal(t, []string{f.second}, history)
			require.NoError(t, f.git.ResolveFetchedCommit(t.Context(), localRef, f.first, 2))
			require.ErrorIs(t, f.git.ResolveFetchedCommit(t.Context(), localRef, f.first, 1), ErrCommitNotOnRef)
		})
	}
}

func applyRawTransportOverlay(t *testing.T, f *transportFixture, overlay string) {
	t.Helper()
	switch overlay {
	case "replace":
		runGit(t, f.root, "replace", f.second, f.first)
	case "grafts":
		require.NoError(t, os.WriteFile(filepath.Join(f.root, ".git", "info", "grafts"), []byte(f.second+"\n"), 0o644))
	case "shallow":
		require.NoError(t, os.WriteFile(filepath.Join(f.root, ".git", "shallow"), []byte(f.second+"\n"), 0o644))
	}
}

func TestRawTransportWalksShareOneBatchAndRespectFirstParentBoundary(t *testing.T) {
	f := newTransportFixture(t)
	plumbing := NewPlumbing(f.git)
	tree := strings.TrimSpace(runGit(t, f.root, "rev-parse", f.second+"^{tree}"))
	merge := plumbing.CommitTree(t.Context(), tree, []string{f.second, f.first}, "ordered-parents")
	require.NoError(t, plumbing.Err())
	parents, err := f.git.CommitParents(t.Context(), merge)
	require.NoError(t, err)
	require.Equal(t, []string{f.second, f.first}, parents)
	before := GitInvocations()
	history, err := f.git.FirstParentOIDs(t.Context(), merge, f.first)
	require.NoError(t, err)
	require.Equal(t, []string{f.second, merge}, history)
	require.Equal(t, uint64(1), GitInvocations()-before, "one batch process walks all raw parents")
	localRef := TransportRefPrefix + "counted-history"
	runGit(t, f.root, "update-ref", localRef, merge)
	before = GitInvocations()
	require.NoError(t, f.git.ResolveFetchedCommit(t.Context(), localRef, f.first, 3))
	require.Equal(t, uint64(2), GitInvocations()-before, "one fresh ref read and one batch parent walk")
	other := plumbing.CommitTree(t.Context(), tree, nil, "other-root")
	require.NoError(t, plumbing.Err())
	_, err = f.git.FirstParentOIDs(t.Context(), merge, other)
	require.Error(t, err, "an unrelated existing root is not an empty first-parent range")
	runGit(t, f.root, "update-ref", localRef, other)
	require.ErrorIs(t, f.git.ResolveFetchedCommit(t.Context(), localRef, merge, 3), ErrCommitNotOnRef,
		"a moved local ref must be resolved again")
}

func TestRawTransportMissingParentsCannotBecomeRoots(t *testing.T) {
	f := newTransportFixture(t)
	tree := strings.TrimSpace(runGit(t, f.root, "rev-parse", f.second+"^{tree}"))
	missing := strings.Repeat("e", len(f.first))
	commit := writeRawTransportCommit(t, f.root, "tree "+tree+"\nparent "+missing+"\n"+rawTransportTestIdentity+"\nmissing parent\n")
	localRef := TransportRefPrefix + "missing-parent"
	runGit(t, f.root, "update-ref", localRef, commit)
	parents, err := f.git.CommitParents(t.Context(), commit)
	require.NoError(t, err, "the encoded direct parent is known even when its object is absent")
	require.Equal(t, []string{missing}, parents)
	_, err = f.git.FirstParentOIDs(t.Context(), commit, f.first)
	require.Error(t, err)
	require.ErrorIs(t, f.git.ResolveFetchedCommit(t.Context(), localRef, missing, 2), ErrCommitNotOnRef)
	_, err = f.git.CommitParents(t.Context(), missing)
	require.Error(t, err)
}

func writeRawTransportCommit(t *testing.T, root, content string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "hash-object", "--literally", "-t", "commit", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	return strings.TrimSpace(string(out))
}

const rawTransportTestIdentity = "author Test <test@example.test> 0 +0000\ncommitter Test <test@example.test> 0 +0000\n"

func TestRawCommitHeadersRejectMalformedIdentity(t *testing.T) {
	tree := strings.Repeat("a", 40)
	parent := strings.Repeat("b", 40)
	for name, content := range map[string]string{
		"no tree":             "parent " + parent + "\n\n",
		"invalid tree":        "tree HEAD\n\n",
		"duplicate tree":      "tree " + tree + "\ntree " + tree + "\n\n",
		"invalid parent":      "tree " + tree + "\nparent HEAD\n\n",
		"bare parent":         "tree " + tree + "\nparent\n\n",
		"tab parent":          "tree " + tree + "\nparent\t" + parent + "\n\n",
		"duplicate parent":    "tree " + tree + "\nparent " + parent + "\nparent " + parent + "\n\n",
		"parent after author": "tree " + tree + "\nauthor Name <name@example.test> 0 +0000\nparent " + parent + "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			header := &transportRawCommitHeader{}
			_, err := io.Copy(header, strings.NewReader(content))
			require.Error(t, err)
		})
	}
	f := newTransportFixture(t)
	oid := writeRawTransportCommit(t, f.root, "tree "+tree+"\nparent "+parent)
	_, err := f.git.CommitParents(t.Context(), oid)
	require.ErrorContains(t, err, transportCommitUnterminatedError)
	plumbing := NewPlumbing(f.git)
	blob := plumbing.HashObject(t.Context(), strings.NewReader("not a commit"))
	require.NoError(t, plumbing.Err())
	_, err = f.git.CommitParents(t.Context(), blob)
	require.ErrorContains(t, err, "not a commit header")
}

func TestRawCommitHeaderByteBoundariesAndMultilineSignatures(t *testing.T) {
	prefix := "tree " + strings.Repeat("a", 40) + "\ngpgsig test\n"
	for _, adjustment := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("line%+d", adjustment), func(t *testing.T) {
			line := " " + strings.Repeat("s", transportCommitHeaderLineMaximumBytes+adjustment-2) + "\n"
			header := &transportRawCommitHeader{}
			_, err := io.Copy(header, strings.NewReader(prefix+line+"\nmessage"))
			if adjustment > 0 {
				require.ErrorIs(t, err, ErrTransportLimit)
				return
			}
			require.NoError(t, err)
			require.True(t, header.isComplete)
		})
		t.Run(fmt.Sprintf("total%+d", adjustment), func(t *testing.T) {
			remaining := transportCommitHeaderMaximumBytes + adjustment - len(prefix) - 1
			var content strings.Builder
			content.WriteString(prefix)
			for remaining > 0 {
				lineBytes := min(remaining, 1024)
				if lineBytes == 1 {
					content.WriteByte('\n')
				} else {
					content.WriteString(" " + strings.Repeat("s", lineBytes-2) + "\n")
				}
				remaining -= lineBytes
			}
			content.WriteString("\nmessage")
			header := &transportRawCommitHeader{}
			_, err := io.Copy(header, strings.NewReader(content.String()))
			if adjustment > 0 {
				require.ErrorIs(t, err, ErrTransportLimit)
				return
			}
			require.NoError(t, err)
			require.True(t, header.isComplete)
		})
	}
}

func TestRawTransportPacksIgnoreOverlaysAndRefuseMissingClosure(t *testing.T) {
	for _, overlay := range []string{"replace", "grafts", "shallow"} {
		t.Run(overlay, func(t *testing.T) {
			f := newTransportFixture(t)
			applyRawTransportOverlay(t, f, overlay)
			var pack bytes.Buffer
			require.NoError(t, f.git.ExportCommitPack(t.Context(), f.second, &pack))
			store := &LocalGitx{Dir: f.bare, Log: zerolog.Nop()}
			require.NoError(t, store.ImportCommitPack(t.Context(), f.second, bytes.NewReader(pack.Bytes()), int64(pack.Len())))
			parents, err := store.CommitParents(t.Context(), f.second)
			require.NoError(t, err)
			require.Equal(t, []string{f.first}, parents)
			kind, err := store.ObjectType(t.Context(), f.first)
			require.NoError(t, err, "the complete raw parent closure traveled")
			require.Equal(t, "commit", kind)
			var delta bytes.Buffer
			require.NoError(t, f.git.ExportCommitPackSince(t.Context(), CommitPackDeltaOptions{
				CommitOID: f.second, BaseOID: f.first, Writer: &delta}))
			closureBlob, closureSize, err := f.git.ImportObjectClosurePackAsBlob(t.Context(), ObjectClosurePackImportOptions{
				SourceDir: f.root, IncludeOIDs: []string{f.second}, MaxBytes: 1 << 20})
			require.NoError(t, err)
			var closurePack bytes.Buffer
			reader := NewPlumbing(f.git)
			reader.ReadBlob(t.Context(), closureBlob, &closurePack, closureSize)
			require.NoError(t, reader.Err())
			closureStore := &LocalGitx{Dir: filepath.Join(t.TempDir(), "closure.git"), Log: zerolog.Nop()}
			require.NoError(t, os.MkdirAll(closureStore.Dir, 0o755))
			require.NoError(t, closureStore.InitBareStore(t.Context()))
			require.NoError(t, closureStore.ImportCommitPack(t.Context(), f.second, &closurePack, closureSize))
			_, err = closureStore.ObjectType(t.Context(), f.first)
			require.NoError(t, err, "the bootstrap closure export includes raw parents too")
			missing := strings.Repeat("e", len(f.first))
			tree := strings.TrimSpace(runGit(t, f.root, "rev-parse", f.first+"^{tree}"))
			broken := writeRawTransportCommit(t, f.root, "tree "+tree+"\nparent "+missing+"\n"+rawTransportTestIdentity+"\nmissing\n")
			require.NoError(t, os.WriteFile(filepath.Join(f.root, ".git", "shallow"), []byte(broken+"\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(f.root, ".git", "info", "grafts"), []byte(broken+"\n"), 0o644))
			require.Error(t, f.git.ExportCommitPack(t.Context(), broken, &bytes.Buffer{}),
				"an overlay cannot turn an unavailable closure into a successful truncated pack")
			_, _, err = f.git.ImportObjectClosurePackAsBlob(t.Context(), ObjectClosurePackImportOptions{
				SourceDir: f.root, IncludeOIDs: []string{broken}, MaxBytes: 1 << 20})
			require.Error(t, err)
		})
	}
}

func TestRawTransportSubtreesAndBlobsIgnoreReplacements(t *testing.T) {
	f := newTransportFixture(t)
	plumbing := NewPlumbing(f.git)
	original := plumbing.HashObject(t.Context(), strings.NewReader("original"))
	replacement := plumbing.HashObject(t.Context(), strings.NewReader("replacement"))
	oldTree := plumbing.MakeTree(t.Context(), []TreeEntry{{Mode: TreeModeFile, Type: "blob", OID: original, Name: "artifact"}})
	newTree := plumbing.MakeTree(t.Context(), []TreeEntry{{Mode: TreeModeFile, Type: "blob", OID: replacement, Name: "artifact"}})
	oldCommit := plumbing.CommitTree(t.Context(), oldTree, nil, "old")
	newCommit := plumbing.CommitTree(t.Context(), newTree, nil, "new")
	require.NoError(t, plumbing.Err())
	for _, pair := range [][2]string{{original, replacement}, {oldTree, newTree}, {oldCommit, newCommit}} {
		runGit(t, f.root, "replace", pair[0], pair[1])
	}
	assert.Equal(t, original, plumbing.ResolveSubtree(t.Context(), oldCommit, "artifact"))
	entry, found := plumbing.TreeEntryAtPath(t.Context(), oldCommit, "artifact")
	require.NoError(t, plumbing.Err())
	require.True(t, found)
	require.Equal(t, original, entry.OID)
	var content bytes.Buffer
	plumbing.ReadBlob(t.Context(), original, &content, 1024)
	require.NoError(t, plumbing.Err())
	require.Equal(t, "original", content.String())
	checkout := filepath.Join(t.TempDir(), "checkout")
	plumbing.WorktreeAdd(t.Context(), checkout, oldCommit)
	require.NoError(t, plumbing.Err())
	materialized, err := os.ReadFile(filepath.Join(checkout, "artifact"))
	require.NoError(t, err)
	require.Equal(t, "original", string(materialized), "private materialization uses the signed raw tree")
}

func TestRawTransportPinnedTagsIgnoreReplacements(t *testing.T) {
	f := newTransportFixture(t)
	runGit(t, f.root, "tag", "-a", "original-tag", f.first, "-m", "original")
	runGit(t, f.root, "tag", "-a", "replacement-tag", f.second, "-m", "replacement")
	original := strings.TrimSpace(runGit(t, f.root, "rev-parse", "original-tag"))
	replacement := strings.TrimSpace(runGit(t, f.root, "rev-parse", "replacement-tag"))
	runGit(t, f.root, "replace", original, replacement)
	blob, size, err := f.git.ImportTagAsBlobFrom(t.Context(), f.root, original, 1<<20)
	require.NoError(t, err)
	_, err = f.git.RestoreTagFromBlob(t.Context(), TagBlobRestoreOptions{
		BlobOID: blob, TagOID: original, PeeledOID: f.first, MaxBytes: size})
	require.NoError(t, err, "the restored exact tag keeps its original target")
}

func TestRawCommitReaderRequiresCleanProcessExitAndCancelsBodyStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the batch-process fixture uses a POSIX shell")
	}
	f := newTransportFixture(t)
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	shim := t.TempDir()
	marker := filepath.Join(shim, "body-started")
	script := "#!/bin/sh\ncase \"$*\" in\n*'cat-file --batch')\nIFS= read -r oid || exit 1\n" +
		"if [ \"$DISPAT_RAW_BODY_BLOCK\" = 1 ]; then\nprintf '%s commit 1000000000\\n' \"$oid\"\n" +
		"printf 'tree %040d\\n\\n' 0\nprintf ready > \"$DISPAT_RAW_BODY_MARKER\"\nsleep 600\nelse\n\"$DISPAT_RAW_REAL_GIT\" \"$@\" <<EOF\n$oid\nEOF\nexit 7\nfi\n;;\n*) exec \"$DISPAT_RAW_REAL_GIT\" \"$@\";;\nesac\n"
	require.NoError(t, os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755))
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DISPAT_RAW_REAL_GIT", realGit)
	t.Setenv("DISPAT_RAW_BODY_MARKER", marker)
	_, err = f.git.CommitParents(t.Context(), f.second)
	require.Error(t, err, "complete parent bytes do not waive a failed batch-process exit")
	t.Setenv("DISPAT_RAW_BODY_BLOCK", "1")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := f.git.CommitParents(ctx, f.second)
		done <- err
	}()
	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, 3*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not stop the streamed commit body")
	}
}
