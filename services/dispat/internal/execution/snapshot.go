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
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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
	mu            sync.Mutex
	captured      map[string]capturedState
	baselines     map[string]baselineIndex
	cleanedRoots  map[string]bool
	preparedRoots map[string]bool
	// consumed is the state one package's build was actually dispatched from,
	// by package name. It is remembered because a later step has to compare
	// against it: what a publication may be authorized against is what the
	// artefact was built from, and "the working tree as it was then" has no
	// other name once the tree has moved on.
	consumed map[string]string
	// indexPaths is where each repository keeps its index, by folder. Git is
	// asked once per repository because the answer does not move during a
	// run, and every capture runs under the exclusive guard, where one
	// process fewer is one wait fewer for every frame.
	indexPaths map[string]string
}

type baselineIndex struct {
	path  string
	tree  string
	close func()
	roots []string
	files map[string]sourceFileFingerprint
}

type sourceFileFingerprint struct {
	mode os.FileMode
	hash [32]byte
}

type packageRootState struct {
	name   string
	source Source
	roots  []string
	git    *gitx.LocalGitx
	log    zerolog.Logger
}

type baselineCapture struct {
	sources []Source
	roots   map[string][]string
	open    func(string) *gitx.LocalGitx
	log     zerolog.Logger
}

type stateTreeCapture struct {
	git          *gitx.LocalGitx
	index        string
	source       Source
	log          zerolog.Logger
	excluded     []string
	baselineTree string
}

// scanOutputRootFiles records the original bytes under declared roots. A
// later preparation may force-capture only new or changed files, leaving an
// old ignored build product outside every task's pinned source snapshot.
func scanOutputRootFiles(repo string, roots []string) (map[string]sourceFileFingerprint, error) {
	files := make(map[string]sourceFileFingerprint)
	for _, root := range roots {
		absolute := filepath.Join(repo, filepath.FromSlash(root))
		if _, err := os.Lstat(absolute); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		err := filepath.WalkDir(absolute, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			var fingerprint sourceFileFingerprint
			fingerprint.mode = info.Mode()
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				fingerprint.hash = sha256.Sum256([]byte(target))
			} else if info.Mode().IsRegular() {
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				hash := sha256.New()
				_, copyErr := io.Copy(hash, file)
				closeErr := file.Close()
				if copyErr != nil {
					return copyErr
				}
				if closeErr != nil {
					return closeErr
				}
				copy(fingerprint.hash[:], hash.Sum(nil))
			} else {
				return fmt.Errorf("execution: unsupported output-root entry %s", path)
			}
			relative, err := filepath.Rel(repo, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(relative)] = fingerprint
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("execution: fingerprinting declared root %s: %w", root, err)
		}
	}
	return files, nil
}

// capturedState is one repository's last capture: what its working state
// hashed to, and the commit that state is offered to workers as.
type capturedState struct {
	tree   string
	commit string
}

// newSnapshots opens an empty memory of captures.
func newSnapshots() *snapshots {
	return &snapshots{captured: map[string]capturedState{}, consumed: map[string]string{},
		indexPaths: map[string]string{}, baselines: map[string]baselineIndex{},
		cleanedRoots: map[string]bool{}, preparedRoots: map[string]bool{}}
}

// cleanPackageRoots restores one package's declared roots from the source
// pinned before dispatch. Ignored products from an earlier run are absent from
// that source, so an idempotent preparation that creates the same bytes is a
// new file the later refresh can admit. The caller holds the snapshot guard's
// exclusive side before the first preparing frame of this package.
func (s *snapshots) cleanPackageRoots(ctx context.Context, request packageRootState) error {
	packageName, source, packageRoots, git, log := request.name, request.source, request.roots, request.git, request.log
	if s.cleanedRoots[packageName] || len(packageRoots) == 0 {
		return nil
	}
	baseline, seeded := s.baselines[source.Dir]
	if !seeded {
		return fmt.Errorf("execution: source baseline of %s was not captured before stage dispatch", source.Dir)
	}
	staging, err := os.MkdirTemp(git.Dir, ".dispat-clean-roots-")
	if err != nil {
		return fmt.Errorf("execution: staging clean roots of %s: %w", packageName, err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	pathspecs := make([]string, 0, len(packageRoots))
	for _, root := range packageRoots {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(root)))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
			return fmt.Errorf("execution: invalid declared output root %q of %s", root, packageName)
		}
		if err := os.MkdirAll(filepath.Join(staging, filepath.FromSlash(clean)), 0o755); err != nil {
			return fmt.Errorf("execution: staging declared root %s of %s: %w", clean, packageName, err)
		}
		pathspecs = append(pathspecs, ":(literal)"+clean)
	}
	indexEnv := append(os.Environ(), "GIT_INDEX_FILE="+baseline.path)
	listed := exec.CommandContext(ctx, "git", append([]string{"-C", git.Dir, "ls-files", "--cached", "-z", "--"}, pathspecs...)...)
	listed.Env = indexEnv
	paths, err := listed.CombinedOutput()
	if err != nil {
		return fmt.Errorf("execution: listing pinned files of %s: %w: %s", packageName, err, paths)
	}
	if len(paths) > 0 {
		checked := exec.CommandContext(ctx, "git", "-C", git.Dir, "checkout-index", "--force",
			"--prefix="+staging+string(filepath.Separator), "-z", "--stdin")
		checked.Env = indexEnv
		checked.Stdin = bytes.NewReader(paths)
		if output, err := checked.CombinedOutput(); err != nil {
			return fmt.Errorf("execution: restoring pinned roots of %s: %w: %s", packageName, err, output)
		}
	}
	cleanFiles, err := scanOutputRootFiles(staging, packageRoots)
	if err != nil {
		return fmt.Errorf("execution: inspecting pinned roots of %s: %w", packageName, err)
	}
	install := InstallRequest{Git: git, Manifest: &OutputManifest{Package: packageName, Roots: packageRoots},
		Dir: git.Dir, Staging: staging, Log: log}
	if err := replaceOutputRoots(install); err != nil {
		return fmt.Errorf("execution: restoring declared roots of %s: %w", packageName, err)
	}
	for path := range baseline.files {
		for _, root := range packageRoots {
			clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(root)))
			if path == clean || strings.HasPrefix(path, clean+"/") {
				delete(baseline.files, path)
				break
			}
		}
	}
	for path, fingerprint := range cleanFiles {
		baseline.files[path] = fingerprint
	}
	s.baselines[source.Dir] = baseline
	s.cleanedRoots[packageName] = true
	return nil
}

// refreshPackageRoots admits preparation writes beneath this package's roots
// exactly once, before any of its output-producing stages can start. Roots of
// other packages stay at their original baseline even if their outputs are
// already installed in the shared checkout.
func (s *snapshots) refreshPackageRoots(ctx context.Context, request packageRootState) error {
	packageName, source, packageRoots, git, log := request.name, request.source, request.roots, request.git, request.log
	if s.preparedRoots[packageName] || len(packageRoots) == 0 {
		return nil
	}
	baseline, seeded := s.baselines[source.Dir]
	if !seeded {
		return fmt.Errorf("execution: source baseline of %s was not captured before stage dispatch", source.Dir)
	}
	own := make(map[string]bool, len(packageRoots))
	for _, root := range packageRoots {
		own[root] = true
	}
	var other []string
	for _, root := range baseline.roots {
		if !own[root] {
			other = append(other, root)
		}
	}
	index, closeIndex, err := copyRepositoryIndex(git, baseline.path)
	if err != nil {
		return err
	}
	defer closeIndex()
	tree, err := writeStateTree(ctx, stateTreeCapture{git: git, index: index, source: source,
		log: log, excluded: other, baselineTree: baseline.tree})
	if err != nil {
		return err
	}
	current, err := scanOutputRootFiles(git.Dir, packageRoots)
	if err != nil {
		return err
	}
	var changed []string
	for path, fingerprint := range current {
		if baseline.files[path] != fingerprint {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	if len(changed) > 0 {
		plumbing := gitx.NewPlumbing(git)
		tree = plumbing.WriteTreeFromPaths(ctx, git.Dir, index, changed, true)
		if err := plumbing.Err(); err != nil {
			return fmt.Errorf("execution: capturing prepared declared roots of %s: %w", packageName, err)
		}
	}
	updated, err := os.Open(index)
	if err != nil {
		return fmt.Errorf("execution: reading prepared source index: %w", err)
	}
	defer func() { _ = updated.Close() }()
	stored, err := os.OpenFile(baseline.path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("execution: updating source baseline index: %w", err)
	}
	if _, err = io.Copy(stored, updated); err != nil {
		_ = stored.Close()
		return fmt.Errorf("execution: updating source baseline index: %w", err)
	}
	if err = stored.Close(); err != nil {
		return fmt.Errorf("execution: closing source baseline index: %w", err)
	}
	baseline.tree = tree
	s.baselines[source.Dir] = baseline
	s.preparedRoots[packageName] = true
	return nil
}

// captureBaselines freezes each repository's source state before any stage
// can admit output bytes into the shared checkout. Later captures stage native
// source and release-file changes around these original output-root entries.
func (s *snapshots) captureBaselines(ctx context.Context, request baselineCapture) error {
	sources, roots, open, log := request.sources, request.roots, request.open, request.log
	for _, source := range sources {
		if _, known := s.baselines[source.Dir]; known {
			continue
		}
		git := open(source.Dir)
		real, err := s.resolveIndexPath(ctx, git)
		if err != nil {
			return err
		}
		index, closeIndex, err := copyRepositoryIndex(git, real)
		if err != nil {
			return err
		}
		tree, err := writeStateTree(ctx, stateTreeCapture{git: git, index: index, source: source, log: log})
		if err != nil {
			closeIndex()
			return err
		}
		files, err := scanOutputRootFiles(source.Dir, roots[source.Dir])
		if err != nil {
			closeIndex()
			return err
		}
		s.baselines[source.Dir] = baselineIndex{path: index, tree: tree,
			close: closeIndex, roots: roots[source.Dir], files: files}
	}
	return nil
}

func (s *snapshots) closeBaselines() {
	for dir, baseline := range s.baselines {
		baseline.close()
		delete(s.baselines, dir)
	}
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
	baseline, isSeeded := s.baselines[source.Dir]
	indexSource := baseline.path
	if !isSeeded {
		var err error
		indexSource, err = s.resolveIndexPath(ctx, git)
		if err != nil {
			return "", err
		}
	}
	index, done, err := copyRepositoryIndex(git, indexSource)
	if err != nil {
		return "", err
	}
	defer done()
	tree, err := writeStateTree(ctx, stateTreeCapture{git: git, index: index, source: source,
		log: log, excluded: baseline.roots, baselineTree: baseline.tree})
	if err != nil {
		return "", err
	}
	plumbing := gitx.NewPlumbing(git)
	if commit, isUnchanged := s.reuse(source.Dir, tree); isUnchanged {
		log.Debug().Str("repository", source.Name).Str("tree", tree).Str("commit", commit).
			Msg("the prepared input state is unchanged and is reused")
		return commit, nil
	}
	commit := plumbing.CommitTree(ctx, tree, []string{source.Head}, KindSnapshot)
	if err := plumbing.Err(); err != nil {
		return "", fmt.Errorf("execution: writing the input state of %s: %w", source.Dir, err)
	}
	s.remember(source.Dir, tree, commit)
	log.Debug().Str("repository", source.Name).Str("tree", tree).Str("commit", commit).
		Str("head", source.Head).Msg("input state prepared")
	return commit, nil
}

// writeStateTree hashes one repository's whole working state into a tree, and
// tries a second time when the first attempt raced somebody writing a file.
//
// The staging walks the repository and then hashes what it found, so a file
// that exists when it is listed and is gone when it is read fails the whole
// capture: git reports the path it could not stat and exits. That is not a
// condition of the repository but of the instant, and a release produces such
// instants by itself, since the recorder writes a changelog through a
// temporary file and renames it while another package is being dispatched. The
// exclusive side of the snapshot guard keeps this run's own version and
// lockfile frames out of the way; it cannot keep out a build command writing
// into its own folder, and nothing should, because a build is exactly what a
// node was asked to do.
//
// One retry, because the second attempt walks a tree from which the file is
// either present or absent rather than becoming one or the other. A capture
// that fails twice is reported: two instants in a row is a repository being
// written to continuously, which is a condition a person has to look at.
func writeStateTree(ctx context.Context, request stateTreeCapture) (string, error) {
	git, index, source := request.git, request.index, request.source
	log, excluded, baselineTree := request.log, request.excluded, request.baselineTree
	for attempt := 1; ; attempt++ {
		// The whole repository, from its root: tracked modifications, untracked
		// files git does not ignore, and the removals of files that are gone.
		// The pathspec is literal so a folder whose name holds a glob character
		// is the folder it is named, and nothing is forced, because what a
		// build output is has not been declared yet at this point in the run.
		plumbing := gitx.NewPlumbing(git)
		var tree string
		if len(excluded) > 0 {
			tree = plumbing.WriteTreeExceptPaths(ctx, git.Dir, index, baselineTree, excluded)
		} else {
			tree = plumbing.WriteTreeFromPaths(ctx, git.Dir, index, []string{"."}, false)
		}
		err := plumbing.Err()
		if err == nil {
			return tree, nil
		}
		if attempt > 1 || ctx.Err() != nil {
			return "", fmt.Errorf("execution: capturing the input state of %s: %w", source.Dir, err)
		}
		log.Debug().Err(err).Str("repository", source.Name).
			Msg("the input state could not be hashed and is captured again")
	}
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

// resolveIndexPath is where one repository keeps its real index.
func (s *snapshots) resolveIndexPath(ctx context.Context, git *gitx.LocalGitx) (string, error) {
	s.mu.Lock()
	path, isKnown := s.indexPaths[git.Dir]
	s.mu.Unlock()
	if isKnown {
		return path, nil
	}
	path, err := git.IndexPath(ctx)
	if err != nil {
		return "", fmt.Errorf("execution: locating the index of %s: %w", git.Dir, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.indexPaths[git.Dir] = path
	return path, nil
}

// copyRepositoryIndex copies a repository's real index to a temporary file and
// answers its path together with the removal of it.
//
// A missing index is refused. Git treats a missing GIT_INDEX_FILE as empty,
// and the repository has a planned head, so it has committed files: a
// snapshot staged from no index would silently omit tracked files that are
// now ignored. Planning refuses a repository with no commits before any
// snapshot is taken. The removal is answered rather than deferred here so the
// caller holds it exactly as long as it uses the copy.
func copyRepositoryIndex(git *gitx.LocalGitx, real string) (string, func(), error) {
	folder, err := os.MkdirTemp("", "dispat-snapshot-index-")
	if err != nil {
		return "", nil, fmt.Errorf("execution: preparing a temporary index: %w", err)
	}
	path := filepath.Join(folder, "index")
	done := func() {
		if err := os.RemoveAll(folder); err != nil {
			// A copy of somebody's working state left in a temporary folder is
			// worth a line, and it is never worth failing a release over.
			git.Log.Warn().Err(err).Str("code", CodeTransportRetained).
				Str("category", CategoryTransportCleanup).Msg("a temporary index was not removed")
		}
	}
	copied, err := os.Create(path)
	if err != nil {
		done()
		return "", nil, fmt.Errorf("execution: preparing a temporary index: %w", err)
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
		return fmt.Errorf("execution: the repository has a missing index %s: %w", real, os.ErrNotExist)
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
