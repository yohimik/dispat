// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeExactTag(t *testing.T, dir, name, target, kind string) string {
	t.Helper()
	content := "object " + target + "\ntype " + kind + "\ntag " + name +
		"\ntagger Example <example@example.com> 0 +0000\n\n" + name + "\n"
	cmd := exec.Command("git", "-C", dir, "hash-object", "-t", "tag", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "hash tag: %s", out)
	return strings.TrimSpace(string(out))
}

func TestExactObjectClosureCarriesNestedTreeAndBlobTagsWithoutRefs(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	tree := strings.TrimSpace(runGit(t, f.root, "rev-parse", "HEAD^{tree}"))
	var file bytes.Buffer
	file.WriteString("unrelated tag payload\n")
	blobCmd := exec.Command("git", "-C", f.root, "hash-object", "-w", "--stdin")
	blobCmd.Stdin = &file
	blobOut, err := blobCmd.CombinedOutput()
	require.NoError(t, err, "hash blob: %s", blobOut)
	blob := strings.TrimSpace(string(blobOut))
	inner := writeExactTag(t, f.root, "inner", f.second, "commit")
	outer := writeExactTag(t, f.root, "outer", inner, "tag")
	treeTag := writeExactTag(t, f.root, "tree", tree, "tree")
	blobTag := writeExactTag(t, f.root, "blob", blob, "blob")
	source := &LocalGitx{Dir: f.root}
	require.Equal(t, inner, mustTagTarget(t, source, outer))
	require.Equal(t, f.second, mustPeel(t, source, outer))
	require.Equal(t, tree, mustPeel(t, source, treeTag))
	require.Equal(t, blob, mustPeel(t, source, blobTag))
	require.Equal(t, blob, mustPeel(t, source, blob))

	destination := &LocalGitx{Dir: f.bare}
	before := strings.TrimSpace(runGit(t, f.bare, "for-each-ref", "--format=%(objectname) %(refname)"))
	packBlob, size, err := destination.ImportObjectClosurePackAsBlob(ctx, ObjectClosurePackImportOptions{SourceDir: f.root,
		IncludeOIDs: []string{outer, outer, treeTag, blobTag}, ExcludeCommitOIDs: []string{f.second, f.second}, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Positive(t, size)
	require.Equal(t, "blob", mustObjectType(t, destination, packBlob))
	restored, err := destination.RestoreObjectClosurePackFromBlob(ctx, packBlob, 1<<20)
	require.NoError(t, err)
	require.Equal(t, size, restored)
	require.Equal(t, "tag", mustObjectType(t, destination, outer))
	require.Equal(t, "tag", mustObjectType(t, destination, inner))
	require.Equal(t, "tree", mustObjectType(t, destination, tree))
	require.Equal(t, "blob", mustObjectType(t, destination, blob))
	require.Equal(t, inner, mustTagTarget(t, destination, outer))
	// The exact commit target is a separate signed bootstrap carrier.
	_, err = destination.ObjectType(ctx, f.second)
	require.Error(t, err)
	require.Equal(t, before, strings.TrimSpace(runGit(t, f.bare, "for-each-ref", "--format=%(objectname) %(refname)")))

	_, _, err = destination.ImportObjectClosurePackAsBlob(ctx, ObjectClosurePackImportOptions{SourceDir: f.root,
		IncludeOIDs: []string{outer, outer, treeTag, blobTag}, ExcludeCommitOIDs: []string{f.second, f.second}, MaxBytes: size - 1})
	require.ErrorIs(t, err, ErrTransportLimit)
	_, err = destination.RestoreObjectClosurePackFromBlob(ctx, packBlob, size-1)
	require.ErrorIs(t, err, ErrTransportLimit)
}

func TestObjectClosureRejectsNoncommitExclusionAndInvalidObjects(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	destination := &LocalGitx{Dir: f.bare}
	_, _, err := destination.ImportObjectClosurePackAsBlob(ctx, ObjectClosurePackImportOptions{SourceDir: f.root,
		IncludeOIDs: []string{f.second}, ExcludeCommitOIDs: []string{strings.Repeat("0", 40)}, MaxBytes: 1 << 20})
	require.Error(t, err)
	_, _, err = destination.ImportObjectClosurePackAsBlob(ctx, ObjectClosurePackImportOptions{SourceDir: f.root,
		IncludeOIDs: []string{f.second}, ExcludeCommitOIDs: []string{strings.TrimSpace(runGit(t, f.root, "rev-parse", "HEAD^{tree}"))}, MaxBytes: 1 << 20})
	require.ErrorContains(t, err, "not a commit")
	_, err = (&LocalGitx{Dir: f.root}).TagTarget(ctx, f.second)
	require.ErrorContains(t, err, "not an annotated tag")
}

func TestObjectClosureIgnoresLocalGitReplaceRefs(t *testing.T) {
	f := newTransportFixture(t)
	inner := writeExactTag(t, f.root, "real", f.second, "commit")
	outer := writeExactTag(t, f.root, "outer", inner, "tag")
	other := writeExactTag(t, f.root, "replacement", f.first, "commit")
	runGit(t, f.root, "replace", inner, other)
	source := &LocalGitx{Dir: f.root}
	require.Equal(t, f.second, mustTagTarget(t, source, inner))
	require.Equal(t, f.second, mustPeel(t, source, outer))
	destination := &LocalGitx{Dir: f.bare}
	blobOID, _, err := destination.ImportObjectClosurePackAsBlob(t.Context(), ObjectClosurePackImportOptions{SourceDir: f.root,
		IncludeOIDs: []string{outer}, ExcludeCommitOIDs: []string{f.second}, MaxBytes: 1 << 20})
	require.NoError(t, err)
	_, err = destination.RestoreObjectClosurePackFromBlob(t.Context(), blobOID, 1<<20)
	require.NoError(t, err)
	require.Equal(t, f.second, mustTagTarget(t, destination, inner))
}

func TestImportExactObjectFromCanonicalStoreWithoutRefs(t *testing.T) {
	f := newTransportFixture(t)
	tree := strings.TrimSpace(runGit(t, f.root, "rev-parse", "HEAD^{tree}"))
	inner := writeExactTag(t, f.root, "inner", f.second, "commit")
	outer := writeExactTag(t, f.root, "outer", inner, "tag")
	destination := &LocalGitx{Dir: f.bare}
	before := strings.TrimSpace(runGit(t, f.bare, "for-each-ref", "--format=%(objectname) %(refname)"))
	require.NoError(t, destination.ImportObjectFrom(t.Context(), f.root, outer, 1<<20))
	require.Equal(t, f.second, mustPeel(t, destination, outer))
	require.Equal(t, "tree", mustObjectType(t, destination, tree))
	require.Equal(t, before, strings.TrimSpace(runGit(t, f.bare, "for-each-ref", "--format=%(objectname) %(refname)")))
	require.ErrorIs(t, destination.ImportObjectFrom(t.Context(), f.root, outer, 1), ErrTransportLimit)
}

func mustObjectType(t *testing.T, git *LocalGitx, oid string) string {
	t.Helper()
	kind, err := git.ObjectType(t.Context(), oid)
	require.NoError(t, err)
	return kind
}

func mustTagTarget(t *testing.T, git *LocalGitx, oid string) string {
	t.Helper()
	target, err := git.TagTarget(t.Context(), oid)
	require.NoError(t, err)
	return target
}

func mustPeel(t *testing.T, git *LocalGitx, oid string) string {
	t.Helper()
	peeled, err := git.PeelObject(t.Context(), oid)
	require.NoError(t, err)
	return peeled
}
