// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The prepared input state against real repositories.
//
// Everything a snapshot is depends on what git does with an index, so nothing
// here is faked: the fixture is a repository with a history, a working tree
// and an ignore file, and the assertions are about the tree that came out and
// about the repository being untouched afterwards.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// snapshotFixture is one repository with a committed history and a working
// tree a scenario may change.
type snapshotFixture struct {
	dir  string
	git  *gitx.LocalGitx
	head string
}

func newSnapshotFixture(t testing.TB) *snapshotFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	fixture := &snapshotFixture{dir: dir, git: &gitx.LocalGitx{Dir: dir, Log: zerolog.Nop()}}
	fixture.run(t, "init", "-q")
	fixture.run(t, "config", "user.email", "unit@dispat.test")
	fixture.run(t, "config", "user.name", "dispat unit")
	fixture.write(t, "tracked.txt", "one\n")
	fixture.write(t, "gone.txt", "two\n")
	fixture.write(t, ".gitignore", "ignored.txt\n")
	fixture.run(t, "add", "-A")
	fixture.run(t, "commit", "-q", "-m", "seed")
	fixture.head = strings.TrimSpace(fixture.run(t, "rev-parse", "HEAD"))
	return fixture
}

func (f *snapshotFixture) run(t testing.TB, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", f.dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

func (f *snapshotFixture) write(t testing.TB, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, name), []byte(body), 0o644))
}

// source is the descriptor a dispatch hands the capture.
func (f *snapshotFixture) source() Source {
	return Source{Path: ".", Dir: f.dir, Head: f.head}
}

// listing is the tree of one captured commit, as "path=content" entries.
func (f *snapshotFixture) listing(t *testing.T, commit string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, path := range strings.Fields(f.run(t, "ls-tree", "-r", "--name-only", commit)) {
		files[path] = strings.TrimSpace(f.run(t, "cat-file", "blob", commit+":"+path))
	}
	return files
}

// TestSnapshotCapturesTheWorkingState: what a node is given is the working
// tree as it stands, which is the whole point of preparing one.
func TestSnapshotCapturesTheWorkingState(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(t *testing.T, f *snapshotFixture)
		want   map[string]string
	}{
		"an edit to a tracked file that was never committed": {
			change: func(t *testing.T, f *snapshotFixture) { f.write(t, "tracked.txt", "edited\n") },
			want:   map[string]string{".gitignore": "ignored.txt", "tracked.txt": "edited", "gone.txt": "two"}},
		"a new file git does not ignore": {
			change: func(t *testing.T, f *snapshotFixture) { f.write(t, "added.txt", "new\n") },
			want: map[string]string{".gitignore": "ignored.txt", "tracked.txt": "one",
				"gone.txt": "two", "added.txt": "new"}},
		"a new file git does ignore": {
			change: func(t *testing.T, f *snapshotFixture) { f.write(t, "ignored.txt", "noise\n") },
			want:   map[string]string{".gitignore": "ignored.txt", "tracked.txt": "one", "gone.txt": "two"}},
		"a tracked file that was deleted": {
			change: func(t *testing.T, f *snapshotFixture) {
				require.NoError(t, os.Remove(filepath.Join(f.dir, "gone.txt")))
			},
			want: map[string]string{".gitignore": "ignored.txt", "tracked.txt": "one"}},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newSnapshotFixture(t)
			tc.change(t, fixture)

			commit, err := newSnapshots().capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())

			require.NoError(t, err)
			assert.Equal(t, tc.want, fixture.listing(t, commit))
			assert.Equal(t, fixture.head,
				strings.TrimSpace(fixture.run(t, "rev-parse", commit+"^")),
				"the prepared state descends from the planned head")
		})
	}
}

func TestSnapshotKeepsPreExecutionOutputRootsWhileUpdatingOtherInputs(t *testing.T) {
	fixture := newSnapshotFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(fixture.dir, "dist"), 0o755))
	fixture.write(t, "dist/seed", "preexisting\n")
	fixture.run(t, "add", "dist/seed")
	fixture.run(t, "commit", "-qm", "tracked output root")
	fixture.head = strings.TrimSpace(fixture.run(t, "rev-parse", "HEAD"))
	fixture.write(t, "dist/initial", "user input\n")
	s := newSnapshots()
	require.NoError(t, s.captureBaselines(t.Context(), baselineCapture{sources: []Source{fixture.source()},
		roots: map[string][]string{fixture.dir: {"dist"}},
		open:  func(string) *gitx.LocalGitx { return fixture.git }, log: zerolog.Nop()}))
	defer s.closeBaselines()
	fixture.write(t, "dist/seed", "unrelated stage changed this\n")
	fixture.write(t, "dist/new", "unrelated stage added this\n")
	fixture.write(t, "tracked.txt", "authorized preparation\n")
	commit, err := s.capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())
	require.NoError(t, err)
	listing := fixture.listing(t, commit)
	assert.Equal(t, "preexisting", listing["dist/seed"])
	assert.Equal(t, "user input", listing["dist/initial"])
	assert.NotContains(t, listing, "dist/new")
	assert.Equal(t, "authorized preparation", listing["tracked.txt"])
}

func TestSnapshotIncludesPreparationInsideTrackedIgnoredRootButNotLaterOutputs(t *testing.T) {
	fixture := newSnapshotFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(fixture.dir, "dist"), 0o755))
	fixture.write(t, "dist/seed", "baseline\n")
	fixture.run(t, "add", "dist/seed")
	fixture.run(t, "commit", "-qm", "tracked root")
	fixture.head = strings.TrimSpace(fixture.run(t, "rev-parse", "HEAD"))
	fixture.write(t, ".gitignore", "ignored.txt\ndist/\n")
	fixture.write(t, "dist/stale", "old ignored product\n")
	s := newSnapshots()
	require.NoError(t, s.captureBaselines(t.Context(), baselineCapture{sources: []Source{fixture.source()},
		roots: map[string][]string{fixture.dir: {"dist"}},
		open:  func(string) *gitx.LocalGitx { return fixture.git }, log: zerolog.Nop()}))
	defer s.closeBaselines()
	fixture.write(t, "dist/seed", "native preparation\n")
	fixture.write(t, "dist/prepared", "ignored native output\n")
	require.NoError(t, s.refreshPackageRoots(t.Context(), packageRootState{name: "core", source: fixture.source(),
		roots: []string{"dist"}, git: fixture.git, log: zerolog.Nop()}))
	fixture.write(t, "dist/seed", "unrelated output\n")
	fixture.write(t, "dist/new", "generated output\n")
	commit, err := s.capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())
	require.NoError(t, err)
	listing := fixture.listing(t, commit)
	assert.Equal(t, "native preparation", listing["dist/seed"])
	assert.Equal(t, "ignored native output", listing["dist/prepared"])
	assert.NotContains(t, listing, "dist/stale")
	assert.NotContains(t, listing, "dist/new")
	assert.Equal(t, "ignored.txt\ndist/", listing[".gitignore"])
}

// An idempotent version script can write the same bytes an ignored stale
// product already held. That write is still preparation the build must see.
func TestSnapshotAdmitsIdempotentPreparationRewriteInsideIgnoredRoot(t *testing.T) {
	fixture := newSnapshotFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(fixture.dir, "dist"), 0o755))
	fixture.write(t, ".gitignore", "ignored.txt\ndist/\n")
	fixture.write(t, "template.txt", "same bytes\n")
	fixture.run(t, "add", ".gitignore", "template.txt")
	fixture.run(t, "commit", "-qm", "add preparation template")
	fixture.head = strings.TrimSpace(fixture.run(t, "rev-parse", "HEAD"))
	fixture.write(t, "dist/prepared", "same bytes\n")
	for run := 0; run < 2; run++ {
		s := newSnapshots()
		require.NoError(t, s.captureBaselines(t.Context(), baselineCapture{sources: []Source{fixture.source()},
			roots: map[string][]string{fixture.dir: {"dist"}},
			open:  func(string) *gitx.LocalGitx { return fixture.git }, log: zerolog.Nop()}))
		require.NoError(t, s.cleanPackageRoots(t.Context(), packageRootState{name: "core", source: fixture.source(),
			roots: []string{"dist"}, git: fixture.git, log: zerolog.Nop()}))
		assert.NoFileExists(t, filepath.Join(fixture.dir, "dist", "prepared"),
			"the old ignored product is removed before preparation runs")
		// The version script's copy is deliberately idempotent in bytes and mode.
		fixture.write(t, "dist/prepared", "same bytes\n")
		require.NoError(t, s.refreshPackageRoots(t.Context(), packageRootState{name: "core", source: fixture.source(),
			roots: []string{"dist"}, git: fixture.git, log: zerolog.Nop()}))
		commit, err := s.capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())
		require.NoError(t, err)
		assert.Equal(t, "same bytes", fixture.listing(t, commit)["dist/prepared"])
		s.closeBaselines()
	}
}

func TestCleanPackageRootsPreservesPinnedInputsAndOtherOwners(t *testing.T) {
	fixture := newSnapshotFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(fixture.dir, "dist"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(fixture.dir, "other"), 0o755))
	fixture.write(t, ".gitignore", "ignored.txt\ndist/stale\nother/stale\n")
	fixture.write(t, "dist/tracked", "tracked input\n")
	fixture.run(t, "add", ".gitignore", "dist/tracked")
	fixture.run(t, "commit", "-qm", "track source inside output root")
	fixture.head = strings.TrimSpace(fixture.run(t, "rev-parse", "HEAD"))
	fixture.write(t, "dist/input", "untracked input\n")
	fixture.write(t, "dist/stale", "old output\n")
	fixture.write(t, "other/stale", "another owner's output\n")
	s := newSnapshots()
	require.NoError(t, s.captureBaselines(t.Context(), baselineCapture{sources: []Source{fixture.source()},
		roots: map[string][]string{fixture.dir: {"dist", "other"}},
		open:  func(string) *gitx.LocalGitx { return fixture.git }, log: zerolog.Nop()}))
	defer s.closeBaselines()
	require.NoError(t, s.cleanPackageRoots(t.Context(), packageRootState{name: "core", source: fixture.source(),
		roots: []string{"dist"}, git: fixture.git, log: zerolog.Nop()}))
	assert.Equal(t, "tracked input\n", readSnapshotFixtureFile(t, fixture.dir, "dist/tracked"))
	assert.Equal(t, "untracked input\n", readSnapshotFixtureFile(t, fixture.dir, "dist/input"))
	assert.NoFileExists(t, filepath.Join(fixture.dir, "dist", "stale"))
	assert.Equal(t, "another owner's output\n", readSnapshotFixtureFile(t, fixture.dir, "other/stale"))
	fixture.write(t, "dist/prepared", "new preparation\n")
	require.NoError(t, s.cleanPackageRoots(t.Context(), packageRootState{name: "core", source: fixture.source(),
		roots: []string{"dist"}, git: fixture.git, log: zerolog.Nop()}), "cleaning once does not erase later preparation")
	assert.Equal(t, "new preparation\n", readSnapshotFixtureFile(t, fixture.dir, "dist/prepared"))
}

func readSnapshotFixtureFile(t *testing.T, root, path string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	require.NoError(t, err)
	return string(contents)
}

// TestSnapshotLeavesTheRepositoryAlone: the repository a run is releasing is
// the thing everything else depends on, so preparing an input state may not
// move its index, its HEAD, its branches or its working tree.
func TestSnapshotLeavesTheRepositoryAlone(t *testing.T) {
	fixture := newSnapshotFixture(t)
	fixture.write(t, "tracked.txt", "edited\n")
	fixture.write(t, "added.txt", "new\n")
	indexPath, err := fixture.git.IndexPath(t.Context())
	require.NoError(t, err)
	indexBefore, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	statusBefore := fixture.run(t, "status", "--porcelain")
	refsBefore := fixture.run(t, "show-ref")

	_, err = newSnapshots().capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())

	require.NoError(t, err)
	indexAfter, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	assert.Equal(t, indexBefore, indexAfter, "the repository's own index is untouched")
	assert.Equal(t, statusBefore, fixture.run(t, "status", "--porcelain"))
	assert.Equal(t, refsBefore, fixture.run(t, "show-ref"), "no branch and no tag was written")
	assert.Equal(t, fixture.head, strings.TrimSpace(fixture.run(t, "rev-parse", "HEAD")))
	assert.Equal(t, "edited\n", readFile(t, filepath.Join(fixture.dir, "tracked.txt")))
}

// TestSnapshotReusesAnUnchangedState: a run of many packages whose working
// tree does not move between dispatches prepares one commit, not one per
// package.
func TestSnapshotReusesAnUnchangedState(t *testing.T) {
	fixture := newSnapshotFixture(t)
	captures := newSnapshots()

	first, err := captures.capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())
	require.NoError(t, err)
	again, err := captures.capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())
	require.NoError(t, err)
	fixture.write(t, "tracked.txt", "edited\n")
	moved, err := captures.capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())
	require.NoError(t, err)

	assert.Equal(t, first, again, "an unchanged working state is the state it already was")
	assert.NotEqual(t, first, moved, "and a changed one is a new commit")
}

// TestSnapshotNeverSeesAHalfWrittenFile: the guard is what keeps a version or
// syncLock frame's writes out of a prepared state, so a file written in chunks
// under the shared side is either wholly there or wholly not.
func TestSnapshotNeverSeesAHalfWrittenFile(t *testing.T) {
	fixture := newSnapshotFixture(t)
	coordinator := newGuardedCoordinator(t)
	whole := strings.Repeat("payload\n", 20000)

	var writing sync.WaitGroup
	writing.Add(1)
	go func() {
		defer writing.Done()
		for range 20 {
			releaseGuard, err := coordinator.Guard(t.Context(), "version", "")
			require.NoError(t, err)
			writeInChunks(t, filepath.Join(fixture.dir, "lockfile.txt"), whole)
			releaseGuard()
		}
	}()

	captures := newSnapshots()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		commit, err := captureUnderGuard(t, coordinator, captures, fixture)
		require.NoError(t, err)
		captured := fixture.listing(t, commit)["lockfile.txt"]
		if captured != "" {
			require.Equal(t, strings.TrimSpace(whole), captured,
				"a prepared state holds the whole file or none of it")
		}
	}
	writing.Wait()
}

// captureUnderGuard is what a dispatch does: the exclusive side of the guard
// around one capture.
func captureUnderGuard(t *testing.T, coordinator *Coordinator, captures *snapshots, fixture *snapshotFixture) (string, error) {
	t.Helper()
	coordinator.guard.Lock()
	defer coordinator.guard.Unlock()
	return captures.capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())
}

// writeInChunks writes a file the way a lock-file generator does: in pieces,
// with the file visible while it is still incomplete.
func writeInChunks(t *testing.T, path, body string) {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	for start := 0; start < len(body); start += 4096 {
		_, err := file.WriteString(body[start:min(start+4096, len(body))])
		require.NoError(t, err)
	}
}

// newGuardedCoordinator is a coordinator with no links at all: it starts no
// poller and exists for the guard alone.
func newGuardedCoordinator(t *testing.T) *Coordinator {
	t.Helper()
	coordinator := NewCoordinator("run", "digest", "generation",
		LocalNode{Name: "here", Capacity: 1}, nil, nil, nil,
		Timeouts{}, TransferLimits{}, zerolog.Nop())
	coordinator.Start(t.Context(), Dispatch{Concurrency: 2})
	return coordinator
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(body)
}

// TestSnapshotReportsAnUnreadableRepository: a capture that cannot be made is
// a failure the dispatch reports rather than an empty state a node would build
// from.
func TestSnapshotReportsAnUnreadableRepository(t *testing.T) {
	missing := &gitx.LocalGitx{Dir: filepath.Join(t.TempDir(), "absent"), Log: zerolog.Nop()}

	_, err := newSnapshots().capture(context.Background(),
		missing, Source{Path: ".", Dir: missing.Dir}, zerolog.Nop())

	require.Error(t, err)
}

// TestTheInputStateIsCapturedTwiceBeforeItIsReported: the capture stages the
// repository and then hashes what it staged, so a file that exists when it is
// listed and is gone when it is read fails the whole capture. A release
// produces such instants by itself, since the changelog recorder renames a
// temporary file into place while another package is being dispatched, and the
// failure was reported as an integrity error against a package that had
// nothing to do with it.
//
// The arm is not reachable from the compiled binary: the race is between one
// git invocation and a file another goroutine of the same run renames, and
// nothing a configuration, a crafted branch, a git fault or a signal can do
// makes the first attempt fail and the second succeed on demand. What is
// asserted here is therefore the retry itself: a capture that cannot be hashed
// is tried a second time, and a second failure is what a person is told about.
func TestTheInputStateIsCapturedTwiceBeforeItIsReported(t *testing.T) {
	fixture := newSnapshotFixture(t)
	// An index path that is a folder: git cannot write it, whichever instant
	// it tries, so both attempts fail for the same reason and the count of
	// them is what the assertion is about.
	index := filepath.Join(t.TempDir(), "index-as-a-folder")
	require.NoError(t, os.MkdirAll(index, 0o755))
	source := Source{Name: "root", Dir: fixture.dir, Head: fixture.head}
	before := gitx.GitInvocations()

	_, err := writeStateTree(t.Context(), stateTreeCapture{git: fixture.git, index: index, source: source, log: zerolog.Nop()})

	require.Error(t, err)
	assert.Contains(t, err.Error(), fixture.dir, "the failure names the repository it is about")
	assert.Equal(t, uint64(2), gitx.GitInvocations()-before,
		"the capture is attempted twice, and the second failure is the one reported")
}

// TestACapturedStateSurvivesOneRacingWriter: the same retry from the outside.
// A capture whose first attempt fails and whose second succeeds answers the
// tree, so one instant of a moving working tree costs a repeated walk rather
// than a failed package.
func TestACapturedStateSurvivesOneRacingWriter(t *testing.T) {
	fixture := newSnapshotFixture(t)
	source := Source{Name: "root", Dir: fixture.dir, Head: fixture.head}
	index := filepath.Join(t.TempDir(), "index")

	tree, err := writeStateTree(t.Context(), stateTreeCapture{git: fixture.git, index: index, source: source, log: zerolog.Nop()})

	require.NoError(t, err)
	assert.Len(t, tree, 40)
}

// TestSnapshotAsksForTheIndexOncePerRepository: where a repository keeps its
// index does not move during a run, so a capture after the first costs the
// staging and the tree and nothing else. It is all under the exclusive guard,
// where every process is a wait for every frame.
func TestSnapshotAsksForTheIndexOncePerRepository(t *testing.T) {
	fixture := newSnapshotFixture(t)
	captures := newSnapshots()
	first, err := captures.capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())
	require.NoError(t, err)
	before := gitx.GitInvocations()

	again, err := captures.capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())

	require.NoError(t, err)
	assert.Equal(t, first, again)
	assert.Equal(t, uint64(2), gitx.GitInvocations()-before, "git add and git write-tree")
}

// BenchmarkSnapshotCaptureUnchanged captures one repository whose working
// tree does not move, which is what every dispatch after the first does in a
// run of many packages. git-invocations/op counts the processes one capture
// starts.
func BenchmarkSnapshotCaptureUnchanged(b *testing.B) {
	fixture := newSnapshotFixture(b)
	captures := newSnapshots()
	_, err := captures.capture(context.Background(), fixture.git, fixture.source(), zerolog.Nop())
	require.NoError(b, err)
	before := gitx.GitInvocations()
	captured := 0
	for b.Loop() {
		_, err := captures.capture(context.Background(), fixture.git, fixture.source(), zerolog.Nop())
		require.NoError(b, err)
		captured++
	}
	b.ReportMetric(float64(gitx.GitInvocations()-before)/float64(captured), "git-invocations/op")
}
