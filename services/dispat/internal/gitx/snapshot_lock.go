// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const snapshotLockName = "dispat-snapshot-write.lock"

// AcquireSnapshotWrite keeps a working-tree snapshot from staging a temporary
// changelog file before its atomic rename. The Git directory belongs to this
// worktree, so separate processes agree on the lock without serializing other
// worktrees of the same repository. A crashed writer leaves the directory in
// place and later captures fail closed rather than adopting its orphaned temp.
func (c *LocalGitx) AcquireSnapshotWrite(ctx context.Context) (func() error, error) {
	dir, err := c.snapshotGitDir(ctx)
	if err != nil {
		return nil, err
	}
	lock := filepath.Join(dir, snapshotLockName)
	bound, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		if err := bound.Err(); err != nil {
			return nil, fmt.Errorf("waiting for snapshot write lock %s (an interrupted changelog write may have left a temporary file): %w", lock, err)
		}
		if err := os.Mkdir(lock, 0o700); err == nil {
			if err := bound.Err(); err != nil {
				return nil, errors.Join(err, os.Remove(lock))
			}
			owned, err := os.Stat(lock)
			if err != nil {
				return nil, errors.Join(fmt.Errorf("inspecting snapshot write lock %s: %w", lock, err), os.Remove(lock))
			}
			var once sync.Once
			var releaseErr error
			return func() error {
				once.Do(func() {
					current, err := os.Stat(lock)
					if err != nil {
						releaseErr = fmt.Errorf("inspecting snapshot write lock %s before release: %w", lock, err)
						return
					}
					if !os.SameFile(owned, current) {
						releaseErr = fmt.Errorf("snapshot write lock %s was replaced before release", lock)
						return
					}
					if err := os.Remove(lock); err != nil {
						releaseErr = fmt.Errorf("releasing snapshot write lock %s: %w", lock, err)
					}
				})
				return releaseErr
			}, nil
		} else if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("acquiring snapshot write lock %s: %w", lock, err)
		}
		select {
		case <-bound.Done():
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (c *LocalGitx) snapshotGitDir(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", fmt.Errorf("resolving snapshot lock Git directory: %w", err)
	}
	dir := strings.TrimSuffix(strings.TrimSuffix(out, "\n"), "\r")
	if dir == "" {
		return "", errors.New("Git returned an empty snapshot lock directory")
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("Git returned a nonabsolute snapshot lock directory %q", dir)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolving snapshot lock directory: %w", err)
	}
	dir, err = canonicalMutationDir(filepath.Clean(dir))
	if err != nil {
		return "", fmt.Errorf("resolving snapshot lock directory: %w", err)
	}
	return dir, nil
}

// AcquireSnapshotWriteForFile also permits a standalone changelog writer that
// is not inside Git: no release snapshot can race that file. Once Git metadata
// is present, a failed Git-directory lookup is an error rather than a bypass.
func AcquireSnapshotWriteForFile(ctx context.Context, path string) (func() error, error) {
	inside, err := hasGitMetadata(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if !inside && os.Getenv("GIT_DIR") == "" {
		return func() error { return nil }, nil
	}
	return (&LocalGitx{Dir: filepath.Dir(path)}).AcquireSnapshotWrite(ctx)
}

func hasGitMetadata(dir string) (bool, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return false, fmt.Errorf("resolving changelog directory %s: %w", dir, err)
	}
	dir = absolute
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("checking Git metadata in %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, nil
		}
		dir = parent
	}
}
