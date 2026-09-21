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

func newSnapshotFixture(t *testing.T) *snapshotFixture {
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

func (f *snapshotFixture) run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", f.dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

func (f *snapshotFixture) write(t *testing.T, name, body string) {
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

// TestSnapshotOfAnUnbornHistory: a repository with no commit yet has no
// planned head to descend from, and the prepared state is a root commit rather
// than a failure.
func TestSnapshotOfAnUnbornHistory(t *testing.T) {
	fixture := newSnapshotFixture(t)
	source := fixture.source()
	source.Head = ""

	commit, err := newSnapshots().capture(t.Context(), fixture.git, source, zerolog.Nop())

	require.NoError(t, err)
	assert.Empty(t, strings.Fields(fixture.run(t, "rev-list", "--parents", "-1", commit))[1:],
		"a state with no planned head descends from nothing")
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
			releaseGuard, err := coordinator.Guard(t.Context(), "version")
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
