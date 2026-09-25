// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// The package folders a run may restore on its own.
//
// In commit mode a release refuses to start while a releasing package's
// folder holds local changes (refuseDirtyReleasePaths, and the fleet's own
// check in prepare), so every byte in such a folder at the end of the run was
// written by the run. A package skipped after its version stage ran holds
// edits for a version that will never publish, and the release commit does
// not stage its folder, so leaving them would only leave a working tree the
// next release refuses. Restoring the folder is therefore the default there.
//
// Two folders are never restored by that default, because the restore would
// reach files another package owns: the repository root, and a folder that
// contains another package's folder.

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// serializedReverter restores a package folder of a single history under the
// repository's mutation lock. Packages are reverted from their own task
// goroutines, and two `git checkout` processes in one checkout race on its
// index lock; a fleet's owner record takes the same lock for the same reason
// (see repositoryRecord.revertDir).
type serializedReverter struct {
	git *gitx.LocalGitx
}

// RevertDir restores dir once no other Git transaction of this process runs
// in the repository.
func (s serializedReverter) RevertDir(ctx context.Context, dir string) error {
	unlock, err := s.git.AcquireMutation(ctx)
	if err != nil {
		return fmt.Errorf("waiting to restore %s: %w", dir, err)
	}
	defer unlock()
	return s.git.RevertDir(ctx, dir)
}

// resolveRunOwnedFolder is the executor's IsRunOwnedFolder: a package's folder
// is the run's own when the repository that owns it makes release commits,
// and the folder is neither that repository's root nor a folder holding
// another package's folder.
func (a *App) resolveRunOwnedFolder(pl *plan.Plan, fleet *workspaceRecorder) func(*plan.Release) bool {
	return func(rel *plan.Release) bool {
		root, isCommitEnabled := a.resolveFolderCommitPolicy(rel.Pkg.Dir, fleet)
		if !isCommitEnabled || filepath.Clean(rel.Pkg.Dir) == filepath.Clean(root) {
			return false
		}
		return !isAnotherPackageInside(pl, rel)
	}
}

// resolveFolderCommitPolicy answers the root of the repository that owns dir
// and whether that repository makes release commits: the run's own
// configuration for a single history, the owning repository's policy in a
// fleet. A folder no participating repository owns answers false.
func (a *App) resolveFolderCommitPolicy(dir string, fleet *workspaceRecorder) (string, bool) {
	if fleet == nil {
		return a.root, a.cfg.Commit.IsEnabled()
	}
	owner := fleet.owner(dir)
	if owner == nil {
		return "", false
	}
	return owner.repo.Root, owner.repo.Commit.IsEnabled()
}

// isAnotherPackageInside reports whether any other package of the workspace
// lives in rel's folder or below it, releasing or not.
func isAnotherPackageInside(pl *plan.Plan, rel *plan.Release) bool {
	for name, other := range pl.Releases {
		if name == rel.Pkg.Name || other == nil || other.Pkg == nil {
			continue
		}
		if pathWithin(rel.Pkg.Dir, other.Pkg.Dir) {
			return true
		}
	}
	return false
}
