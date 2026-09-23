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
)

// AcquireMutation serializes a complete native Git transaction in this
// repository with every other transaction this process runs in it: a commit,
// a tag, a push, and any enclosing checkpoint. The exclusion belongs to Git's
// common directory, so linked worktrees sharing an object database and refs
// also share it.
//
// The exclusion is this process's own and claims nothing about any other. Git
// refuses a second writer of the index or of a ref through its own lock files,
// every transaction re-proves the revision it is about to record before it
// writes, and releases are serialized by the remote release lock.
//
// A waiter stops when ctx is done. The returned release is idempotent; callers
// hold it across the transaction and never while a user script runs.
func (c *LocalGitx) AcquireMutation(ctx context.Context) (release func(), err error) {
	return AcquireMutations(ctx, c)
}

// AcquireMutations acquires several repositories in Git-common-directory
// order and releases them in reverse. Duplicate common directories are
// acquired once, which matters when a transaction names two linked worktrees
// of the same repository: each common directory has a single slot, and taking
// it twice would wait for a release only the waiter itself could make.
//
// The one order is what keeps two transactions from waiting on each other: a
// transaction only ever waits for a slot that sorts after every slot it
// already holds, so no chain of waits can close into a cycle.
func AcquireMutations(ctx context.Context, repositories ...*LocalGitx) (release func(), err error) {
	commons := make([]string, 0, len(repositories))
	seen := make(map[string]bool, len(repositories))
	for _, repository := range repositories {
		if repository == nil {
			continue
		}
		common, resolveErr := repository.mutationCommonDir(ctx)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !seen[common] {
			seen[common] = true
			commons = append(commons, common)
		}
	}
	sort.Strings(commons)

	held := make([]chan struct{}, 0, len(commons))
	releaseAll := func() {
		for i := len(held) - 1; i >= 0; i-- {
			<-held[i]
		}
	}
	for _, common := range commons {
		slot := mutationSlot(common)
		// A context that is already done takes nothing, even where the slot is
		// free: a select with both cases ready picks one at random.
		if ctxErr := ctx.Err(); ctxErr != nil {
			releaseAll()
			return nil, ctxErr
		}
		select {
		case slot <- struct{}{}:
			held = append(held, slot)
		case <-ctx.Done():
			releaseAll()
			return nil, ctx.Err()
		}
	}
	var once sync.Once
	return func() { once.Do(releaseAll) }, nil
}

// mutationSlots holds one single-slot channel per canonical Git common
// directory this process has taken. Sending takes the repository and
// receiving gives it back, so a waiter can also wait on its context.
var mutationSlots sync.Map

// mutationSlot answers the one slot of a canonical common directory, creating
// it on first use.
func mutationSlot(common string) chan struct{} {
	if slot, ok := mutationSlots.Load(common); ok {
		return slot.(chan struct{})
	}
	slot, _ := mutationSlots.LoadOrStore(common, make(chan struct{}, 1))
	return slot.(chan struct{})
}

// mutationCommonDir answers the Git common directory a repository's mutations
// are serialized by, in the one spelling this process uses for that directory.
//
// Two things the plain `rev-parse` answer cannot do are done here. The answer
// is canonicalized through the filesystem, because the exclusion's identity is
// the directory rather than the string that names it: on a case-insensitive
// filesystem "/w/Repo/.git" and "/w/repo/.git" are one directory, and two
// spellings would be two slots, so two transactions in one repository would
// not exclude each other and one transaction naming both spellings would take
// the repository twice. And the answer is remembered, because a release takes
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

// mutationDirs is what makes one directory one slot: the spelling this process
// settled on for every real directory it has taken, and the resolved common
// directory of every repository folder it has been asked about.
//
// Both are process-wide because the hazard and the cost are both process-wide:
// one repository must not be taken under two names, and a release asks the
// same repository the same question hundreds of times.
var mutationDirs struct {
	mu       sync.Mutex
	known    []mutationDir
	byFolder map[string]string
}

// mutationDir is one real directory and the spelling this process names it by.
// The recorded FileInfo is what a later candidate is compared against, and
// what a re-stat of the same path is checked against before it is trusted: an
// inode a deleted folder left behind can be handed to a new one, and a stale
// entry would otherwise send the transaction somewhere else entirely.
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
// its common directory resolved from memory.
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
