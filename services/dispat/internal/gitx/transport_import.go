// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"fmt"
	"path/filepath"
)

// ImportCommitFrom copies the exact commit and its reachable object closure
// from a local source repository into this object store without creating a
// ref in either repository. A carried assignment can then name that commit as
// a secondary parent, so its one task-branch push transports the snapshot or
// admitted local result without a runtime snapshot/relay branch.
//
// Git's object id proves the bytes were preserved: an import that does not
// leave this store holding the requested commit is an error. The caller's
// context bounds the fetch; the object's reachable history is transferred as
// required by Git and is not truncated at a guessed depth.
func (c *LocalGitx) ImportCommitFrom(ctx context.Context, sourceRepoPath, oid string) error {
	if !transportOID(oid) {
		return fmt.Errorf("gitx: invalid source commit object id")
	}
	if !filepath.IsAbs(sourceRepoPath) {
		return fmt.Errorf("gitx: source repository path must be absolute")
	}
	source := &LocalGitx{Dir: sourceRepoPath, Log: c.Log}
	resolved, err := source.ResolveCommit(ctx, oid)
	if err != nil || resolved != oid {
		return fmt.Errorf("gitx: source repository does not hold commit %s: %w", oid, ErrCommitNotOnRef)
	}
	if existing, err := c.ResolveCommit(ctx, oid); err == nil && existing == oid {
		return nil
	}
	if _, err := c.run(ctx, "fetch", "--no-tags", "--no-write-fetch-head", "--", sourceRepoPath, oid); err != nil {
		return fmt.Errorf("gitx: importing commit %s from %s: %w", oid, sourceRepoPath, err)
	}
	if imported, err := c.ResolveCommit(ctx, oid); err != nil || imported != oid {
		return fmt.Errorf("gitx: imported commit %s is unavailable: %w", oid, ErrCommitNotOnRef)
	}
	return nil
}
