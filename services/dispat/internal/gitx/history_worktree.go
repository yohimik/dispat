// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"fmt"
)

// AddHistoryWorktree adds a worktree at dir whose HEAD is commit, detached and
// with nothing checked out.
//
// Every history read of LocalGitx is spelled against HEAD, so a reader opened
// on this folder sees the repository as it stood at commit: the commits HEAD
// reaches and the tags merged into it. Nothing is checked out because nothing
// that reads history needs the files, and a large tree would cost a full
// checkout for no reader.
func (c *LocalGitx) AddHistoryWorktree(ctx context.Context, dir, commit string) error {
	if _, err := c.run(ctx, "worktree", "add", "--detach", "--no-checkout", "--", dir, commit); err != nil {
		return fmt.Errorf("gitx: adding a history worktree at %s: %w", commit, err)
	}
	return nil
}

// RemoveWorktree removes a worktree this repository added. It is forced
// because a worktree with nothing checked out reads to git as one whose every
// file was deleted.
func (c *LocalGitx) RemoveWorktree(ctx context.Context, dir string) error {
	if _, err := c.run(ctx, "worktree", "remove", "--force", "--", dir); err != nil {
		return fmt.Errorf("gitx: removing the worktree %s: %w", dir, err)
	}
	return nil
}
