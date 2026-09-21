// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The prepared input state one dispatched task is built from (CCME §28.3).
//
// A worker never reads the orchestrator's working tree, and it never plans:
// what it receives is an exact object id, and the files it builds are the
// files that object holds. Preparing that object is this file's whole job, and
// it has one hard constraint: the repository being released must come out of
// it untouched. Its index, its HEAD, its branches and its working tree are
// what the rest of the run depends on, so the capture goes through a copy of
// the index and writes nothing else.
//
// The copy is a copy rather than an empty index because an index carries the
// stat information git uses to decide what it has to rehash. Starting from
// nothing would re-read every file of the repository on every dispatch;
// starting from the real index re-reads what actually changed.

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// Source is one repository a task's input closure needs: who it is in the run,
// where its checkout sits relative to the task folder, which working tree the
// state is captured from, and the planned head that state descends from.
//
// The planned head is carried rather than resolved here because it is the plan
// input the digest was taken over (§28.3): a snapshot parented on anything
// else would describe a run nobody computed.
type Source struct {
	// Name is the repository's stable identity in the run, empty for a single
	// history that has no other name for itself.
	Name string
	// Path is where this repository's checkout sits relative to the task
	// folder, "." for the root.
	Path string
	// Dir is the working tree the prepared state is captured from.
	Dir string
	// Head is the planned head the snapshot commit is parented on.
	Head string
}

// snapshots is the run's memory of what it has already captured.
//
// One entry per repository, keyed by the working tree it was captured from and
// holding the tree that came out and the commit it was wrapped in. A dispatch
// that finds the tree unchanged reuses the commit, which is what keeps a run
// of twenty packages from pushing twenty identical source commits.
type snapshots struct {
	mu       sync.Mutex
	captured map[string]capturedState
	// consumed is the state one package's build was actually dispatched from,
	// by package name. It is remembered because a later step has to compare
	// against it: what a publication may be authorized against is what the
	// artefact was built from, and "the working tree as it was then" has no
	// other name once the tree has moved on.
	consumed map[string]string
}

// capturedState is one repository's last capture: what its working state
// hashed to, and the commit that state is offered to workers as.
type capturedState struct {
	tree   string
	commit string
}

// newSnapshots opens an empty memory of captures.
func newSnapshots() *snapshots {
	return &snapshots{captured: map[string]capturedState{}, consumed: map[string]string{}}
}

// rememberConsumedSnapshot records the prepared state one package's build was
// dispatched from, which is the state of its own repository and of no other:
// a comparison against a state of some third repository would be a comparison
// nobody could act on.
func (c *Coordinator) rememberConsumedSnapshot(packageName, repository string,
	sources []Source, commits []string) {
	for index, source := range sources {
		if source.Name != repository {
			continue
		}
		c.snapshots.mu.Lock()
		defer c.snapshots.mu.Unlock()
		c.snapshots.consumed[packageName] = commits[index]
		return
	}
}

// PreparedSnapshot is the input state one package's build consumed, and the
// empty string for a package this run built nowhere: a build the run placed on
// this machine read the working tree itself, and a package with no build task
// at all consumed nothing.
//
// It is exported because the comparison it is for is not this package's to
// make. What counts as a relevant change to a package's inputs is the
// workspace's question (which folders, which providers, which records are
// excluded), and answering it here would put a second description of the
// release graph inside the transport.
func (c *Coordinator) PreparedSnapshot(packageName string) string {
	if c.snapshots == nil {
		return ""
	}
	c.snapshots.mu.Lock()
	defer c.snapshots.mu.Unlock()
	return c.snapshots.consumed[packageName]
}

// capture prepares one repository's current working state as a commit whose
// parent is the planned head, and answers its object id.
//
// Everything it writes is an object; nothing it writes is a ref, and nothing
// it touches belongs to the repository. The temporary index is removed on
// every path, including the ones that fail, because an index left behind in a
// temporary folder is a copy of somebody's working state.
func (s *snapshots) capture(ctx context.Context, git *gitx.LocalGitx, source Source, log zerolog.Logger) (string, error) {
	index, done, err := copyRepositoryIndex(ctx, git)
	if err != nil {
		return "", err
	}
	defer done()
	plumbing := gitx.NewPlumbing(git)
	// The whole repository, from its root: tracked modifications, untracked
	// files git does not ignore, and the removals of files that are gone. The
	// pathspec is literal so a folder whose name holds a glob character is the
	// folder it is named, and nothing is forced, because what a build output
	// is has not been declared yet at this point in the run.
	tree := plumbing.WriteTreeFromPaths(ctx, git.Dir, index, []string{"."}, false)
	if err := plumbing.Err(); err != nil {
		return "", fmt.Errorf("execution: capturing the input state of %s: %w", source.Dir, err)
	}
	if commit, isUnchanged := s.reuse(source.Dir, tree); isUnchanged {
		log.Debug().Str("repository", source.Name).Str("tree", tree).Str("commit", commit).
			Msg("the prepared input state is unchanged and is reused")
		return commit, nil
	}
	commit := plumbing.CommitTree(ctx, tree, parentsOf(source.Head), KindSnapshot)
	if err := plumbing.Err(); err != nil {
		return "", fmt.Errorf("execution: writing the input state of %s: %w", source.Dir, err)
	}
	s.remember(source.Dir, tree, commit)
	log.Debug().Str("repository", source.Name).Str("tree", tree).Str("commit", commit).
		Str("head", source.Head).Msg("input state prepared")
	return commit, nil
}

// parentsOf is the commit list a snapshot descends from: the planned head, or
// nothing at all for a repository whose history is still unborn.
func parentsOf(head string) []string {
	if head == "" {
		return nil
	}
	return []string{head}
}

// reuse answers the commit a repository's unchanged state was last captured
// as.
func (s *snapshots) reuse(dir, tree string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, isCaptured := s.captured[dir]
	if !isCaptured || state.tree != tree {
		return "", false
	}
	return state.commit, true
}

// remember records what a repository's state was last captured as.
func (s *snapshots) remember(dir, tree, commit string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captured[dir] = capturedState{tree: tree, commit: commit}
}

// copyRepositoryIndex copies a repository's real index to a temporary file and
// answers its path together with the removal of it.
//
// A repository whose index does not exist yet (nothing has ever been staged)
// starts from an empty file, which is what git does with a missing index
// anyway. The removal is answered rather than deferred here so that the caller
// holds it for exactly as long as it uses the copy.
func copyRepositoryIndex(ctx context.Context, git *gitx.LocalGitx) (string, func(), error) {
	real, err := git.IndexPath(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("execution: locating the index of %s: %w", git.Dir, err)
	}
	copied, err := os.CreateTemp("", "dispat-snapshot-index-*")
	if err != nil {
		return "", nil, fmt.Errorf("execution: preparing a temporary index: %w", err)
	}
	path := copied.Name()
	done := func() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			// A copy of somebody's working state left in a temporary folder is
			// worth a line, and it is never worth failing a release over.
			git.Log.Warn().Err(err).Str("code", CodeTransportRetained).
				Str("category", CategoryTransportCleanup).Msg("a temporary index was not removed")
		}
	}
	if err := writeIndexCopy(real, copied); err != nil {
		done()
		return "", nil, err
	}
	return path, done, nil
}

// writeIndexCopy streams the repository's index into the copy and closes it.
// The index of a large repository is megabytes of packed entries, so it is
// copied rather than read into memory.
func writeIndexCopy(real string, copied *os.File) error {
	defer func() { _ = copied.Close() }()
	source, err := os.Open(real)
	if os.IsNotExist(err) {
		// Nothing has ever been staged here; an empty index is what git would
		// have read anyway.
		return nil
	}
	if err != nil {
		return fmt.Errorf("execution: reading the index %s: %w", real, err)
	}
	defer func() { _ = source.Close() }()
	if _, err := io.Copy(copied, source); err != nil {
		return fmt.Errorf("execution: copying the index %s: %w", real, err)
	}
	return nil
}
