// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import "context"

const treeEntryAtPathMissing = false

// TreeEntryAtPath reads one direct entry from a commit or tree. A missing path
// is distinct from an empty tree at that path, whose object ID still exists.
func (p *Plumbing) TreeEntryAtPath(ctx context.Context, revision, path string) (TreeEntry, bool) {
	if p.err != nil {
		return TreeEntry{}, treeEntryAtPathMissing
	}
	entries, err := p.git.treeEntriesAtPaths(ctx, revision, []string{path})
	if err != nil {
		p.fail(err)
		return TreeEntry{}, treeEntryAtPathMissing
	}
	entry, found := entries[path]
	return TreeEntry{Mode: entry.mode, OID: entry.oid, Name: path}, found
}
