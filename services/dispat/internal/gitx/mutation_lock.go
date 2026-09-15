package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const mutationLockFile = "dispat-mutation.lock"

// AcquireMutation serializes a complete native Git mutation transaction with
// every other dispat process using this repository. The lock lives in Git's
// common directory, so linked worktrees sharing an object database and refs
// also share the same exclusion point.
//
// Acquisition polls a non-blocking platform lock so context cancellation can
// stop a waiter. The returned release is idempotent and closes the descriptor;
// callers must hold it across commit, tag, push, and any enclosing checkpoint,
// but not while running user scripts.
func (c *CLI) AcquireMutation(ctx context.Context) (release func(), err error) {
	return AcquireMutations(ctx, c)
}

// AcquireMutations acquires several repositories in Git-common-directory
// order. Duplicate common directories are locked once, which matters when a
// transaction names two linked worktrees of the same repository.
func AcquireMutations(ctx context.Context, repositories ...*CLI) (release func(), err error) {
	paths := make([]string, 0, len(repositories))
	seen := make(map[string]bool, len(repositories))
	for _, repository := range repositories {
		if repository == nil {
			continue
		}
		path, pathErr := repository.mutationLockPath(ctx)
		if pathErr != nil {
			return nil, pathErr
		}
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	var held []func()
	releaseAll := func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i]()
		}
	}
	for _, path := range paths {
		unlock, lockErr := acquireMutationPath(ctx, path)
		if lockErr != nil {
			releaseAll()
			return nil, lockErr
		}
		held = append(held, unlock)
	}
	var once sync.Once
	return func() { once.Do(releaseAll) }, nil
}

func acquireMutationPath(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening Git mutation lock %s: %w", path, err)
	}

	closeFile := func() { _ = f.Close() }
	retry := time.NewTicker(25 * time.Millisecond)
	defer retry.Stop()
	for {
		if err := ctx.Err(); err != nil {
			closeFile()
			return nil, err
		}
		locked, lockErr := tryMutationFileLock(f)
		if lockErr != nil {
			closeFile()
			return nil, fmt.Errorf("acquiring Git mutation lock %s: %w", path, lockErr)
		}
		if locked {
			var once sync.Once
			return func() {
				once.Do(func() {
					_ = unlockMutationFile(f)
					closeFile()
				})
			}, nil
		}

		select {
		case <-ctx.Done():
			closeFile()
			return nil, ctx.Err()
		case <-retry.C:
		}
	}
}

func (c *CLI) mutationLockPath(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolving Git common directory: %w", err)
	}
	common := strings.TrimSuffix(strings.TrimSuffix(out, "\n"), "\r")
	if common == "" {
		return "", errors.New("Git returned an empty common directory")
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(c.Dir, common)
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return "", fmt.Errorf("resolving Git common directory path: %w", err)
	}
	return filepath.Join(filepath.Clean(common), mutationLockFile), nil
}
