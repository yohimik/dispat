package gitx

import "context"

// GitDir returns the canonical private directory for this checkout. It also
// handles linked worktrees and submodules, whose .git entry is not a directory.
func (c *LocalGitx) GitDir(ctx context.Context) (string, error) {
	return c.snapshotGitDir(ctx)
}
