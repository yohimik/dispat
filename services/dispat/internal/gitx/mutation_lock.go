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
func (c *LocalGitx) AcquireMutation(ctx context.Context) (release func(), err error) {
	return AcquireMutations(ctx, c)
}

// AcquireMutations acquires several repositories in Git-common-directory
// order. Duplicate common directories are locked once, which matters when a
// transaction names two linked worktrees of the same repository.
func AcquireMutations(ctx context.Context, repositories ...*LocalGitx) (release func(), err error) {
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

func (c *LocalGitx) mutationLockPath(ctx context.Context) (string, error) {
	common, err := c.mutationCommonDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(common, mutationLockFile), nil
}

// mutationCommonDir answers the Git common directory this repository's lock
// lives in, in the one spelling this process uses for that directory.
//
// Two things the plain `rev-parse` answer cannot do are done here. The answer
// is canonicalized through the filesystem, because the lock's identity is the
// directory rather than the string that names it: on a case-insensitive
// filesystem "/w/Repo/.git" and "/w/repo/.git" are one directory and one lock
// file, and opening it twice would take two file descriptions of it — flock
// belongs to the description, so the second acquisition would wait for a
// release the first only makes afterwards, and the caller would spin until
// its context gave up. And the answer is remembered, because a release takes
// this lock on every commit, tag and push while the common directory of a
// repository does not move: one `git rev-parse` per repository per process
// rather than one per acquisition.
func (c *LocalGitx) mutationCommonDir(ctx context.Context) (string, error) {
	key, err := filepath.Abs(c.Dir)
	if err != nil {
		key = filepath.Clean(c.Dir)
	}
	if cached, ok := cachedCommonDir(key); ok {
		return cached, nil
	}
	out, err := c.run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolving Git common directory: %w", err)
	}
	common := strings.TrimSuffix(strings.TrimSuffix(out, "\n"), "\r")
	if common == "" {
		return "", errors.New("Git returned an empty common directory")
	}
	if !filepath.IsAbs(common) {
		return "", fmt.Errorf("Git did not return an absolute Git common directory: %q", common)
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return "", fmt.Errorf("resolving Git common directory path: %w", err)
	}
	common, err = canonicalMutationDir(filepath.Clean(common))
	if err != nil {
		return "", err
	}
	rememberCommonDir(key, common)
	return common, nil
}

// mutationDirs is what makes one directory one lock: the spelling this process
// settled on for every real directory it has locked in, and the resolved
// common directory of every repository folder it has been asked about.
//
// Both are process-wide because the hazard and the cost are both process-wide:
// one process must not hold two descriptions of one lock file, and a release
// asks the same repository the same question hundreds of times.
var mutationDirs struct {
	mu       sync.Mutex
	known    []mutationDir
	byFolder map[string]string
}

// mutationDir is one real directory and the spelling this process names it by.
// The recorded FileInfo is what a later candidate is compared against, and
// what a re-stat of the same path is checked against before it is trusted: an
// inode a deleted folder left behind can be handed to a new one, and a stale
// entry would otherwise send the lock somewhere else entirely.
type mutationDir struct {
	info os.FileInfo
	path string
}

func canonicalMutationDir(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("resolving Git common directory path: %w", err)
	}
	mutationDirs.mu.Lock()
	defer mutationDirs.mu.Unlock()
	live := mutationDirs.known[:0]
	answer := ""
	for _, known := range mutationDirs.known {
		fresh, statErr := os.Stat(known.path)
		if statErr != nil || !os.SameFile(fresh, known.info) {
			continue // the folder went away, or that path now names another one
		}
		live = append(live, known)
		if answer == "" && os.SameFile(fresh, info) {
			answer = known.path
		}
	}
	mutationDirs.known = live
	if answer != "" {
		return answer, nil
	}
	mutationDirs.known = append(mutationDirs.known, mutationDir{info: info, path: path})
	return path, nil
}

// cachedCommonDir answers from the per-process table, and only while the
// answer still exists: a temporary repository that was removed must not have
// its lock resolved from memory.
func cachedCommonDir(folder string) (string, bool) {
	mutationDirs.mu.Lock()
	defer mutationDirs.mu.Unlock()
	common, ok := mutationDirs.byFolder[folder]
	if !ok {
		return "", false
	}
	if _, err := os.Stat(common); err != nil {
		delete(mutationDirs.byFolder, folder)
		return "", false
	}
	return common, true
}

func rememberCommonDir(folder, common string) {
	mutationDirs.mu.Lock()
	defer mutationDirs.mu.Unlock()
	if mutationDirs.byFolder == nil {
		mutationDirs.byFolder = make(map[string]string)
	}
	mutationDirs.byFolder[folder] = common
}
