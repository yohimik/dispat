// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTreeEntryAtPathDistinguishesAbsentEmptyTreeAndBlob(t *testing.T) {
	_, git := initRepo(t)
	ctx := t.Context()
	p := NewPlumbing(git)
	emptyTree := p.MakeTree(ctx, nil)
	blob := p.HashObject(ctx, strings.NewReader("payload"))
	root := p.MakeTree(ctx, []TreeEntry{
		{Mode: TreeModeDir, Type: "tree", OID: emptyTree, Name: "outputs"},
		{Mode: TreeModeFile, Type: "blob", OID: blob, Name: "payload"},
	})
	commit := p.CommitTree(ctx, root, nil, "tree-entry-test")
	require.NoError(t, p.Err())
	for _, row := range []struct {
		path, mode, oid string
		found           bool
	}{
		{path: "missing"},
		{path: "outputs", mode: TreeModeDir, oid: emptyTree, found: true},
		{path: "payload", mode: TreeModeFile, oid: blob, found: true},
	} {
		entry, found := p.TreeEntryAtPath(ctx, commit, row.path)
		require.NoError(t, p.Err())
		require.Equal(t, row.found, found)
		if found {
			require.Equal(t, row.mode, entry.Mode)
			require.Equal(t, row.oid, entry.OID)
			require.Equal(t, row.path, entry.Name)
		}
	}
}
