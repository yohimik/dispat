// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// TestNodeStateIsOwnedByOneProcess: the record of what a node has already
// answered has exactly one owner, so a second process serving the same node
// out of the same folder is refused, and a lock left behind by a process that
// is gone is taken over rather than needing a person.
func TestNodeStateIsOwnedByOneProcess(t *testing.T) {
	root := t.TempDir()

	state, release, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "build-a"), state.Dir)
	assert.DirExists(t, state.Cache)
	assert.Equal(t, filepath.Join(root, "build-a", "seen.json"), state.Seen)

	t.Run("a second process on the same folder is refused", func(t *testing.T) {
		_, _, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")

		require.Error(t, err)
		assert.Equal(t, config.DiagnosticExecution, config.DiagnosticCode(err))
		assert.Equal(t, CategoryConfiguration, DiagnosticCategory(err))
		assert.Contains(t, err.Error(), strconv.Itoa(os.Getpid()))
	})

	t.Run("another node in the same folder is a folder of its own", func(t *testing.T) {
		other, otherRelease, err := OpenNodeState(root, "build-b", "file:///srv/mailbox.git")

		require.NoError(t, err)
		assert.NotEqual(t, state.Dir, other.Dir)
		require.NoError(t, otherRelease())
	})

	require.NoError(t, release())
	held, err := os.ReadFile(filepath.Join(state.Dir, stateLockFile))
	require.NoError(t, err)
	assert.Equal(t, "0", string(held), "the lock inode remains but names no owner")

	t.Run("a lock from a process that is gone is taken over", func(t *testing.T) {
		// A process id nothing can be running under: the maximum a pid may
		// take on any of the platforms this runs on, which the kernel hands
		// out last and which no test process holds.
		require.NoError(t, os.WriteFile(filepath.Join(state.Dir, stateLockFile),
			[]byte("4194304"), 0o644))

		_, release, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")

		require.NoError(t, err)
		require.NoError(t, release())
	})

	t.Run("a lock its owner has created and not yet written is waited for", func(t *testing.T) {
		// The owner creates the file and writes its id in two steps. A second
		// process arriving between them used to read the empty file as stale
		// and remove a live owner's claim.
		lock := filepath.Join(state.Dir, stateLockFile)
		require.NoError(t, os.WriteFile(lock, nil, 0o644))
		written := make(chan error, 1)
		go func() {
			time.Sleep(10 * stateLockWritePoll)
			written <- os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o644)
		}()

		_, _, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")

		require.NoError(t, <-written)
		require.Error(t, err, "the owner was starting, not gone")
		assert.Contains(t, err.Error(), strconv.Itoa(os.Getpid()))
		require.NoError(t, os.Remove(lock))
	})

	leftovers := map[string]struct {
		content string
	}{
		"a lock whose writer died before writing is taken over after the grace": {content: ""},
		"content that is no process id names nobody":                            {content: "not-a-pid"},
		"a negative id is a process group, not an owner":                        {content: "-1"},
		"zero is this process group, not an owner":                              {content: "0"},
	}
	for name, leftover := range leftovers {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(filepath.Join(state.Dir, stateLockFile),
				[]byte(leftover.content), 0o644))

			_, release, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")

			require.NoError(t, err)
			require.NoError(t, release())
		})
	}
}

// TestNodeStateKeepsOneCachePerEndpoint: a node serving two mailboxes keeps
// their objects apart, so neither fetch invalidates the other.
func TestNodeStateKeepsOneCachePerEndpoint(t *testing.T) {
	root := t.TempDir()

	first, release, err := OpenNodeState(root, "build-a", "file:///srv/one.git")
	require.NoError(t, err)
	require.NoError(t, release())
	second, release, err := OpenNodeState(root, "build-a", "file:///srv/two.git")
	require.NoError(t, err)
	require.NoError(t, release())

	assert.NotEqual(t, first.Cache, second.Cache)
	assert.Equal(t, filepath.Dir(first.Cache), filepath.Dir(second.Cache))
	assert.Len(t, filepath.Base(first.Cache), 16+len(".git"),
		"a cache is named by a bounded hash of the endpoint rather than by the endpoint")
}

// TestSeenSetRemembersAcrossProcesses: the record is what makes duplicate
// delivery recognisable, so it survives the process, is pruned to the replay
// window, and is never left half written.
func TestSeenSetRemembersAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seen.json")
	now := parseTestTime(t, "2026-09-21T12:00:00Z")

	set, err := LoadSeenSet(path, now)
	require.NoError(t, err)
	assert.False(t, set.IsSeen("run-1", "core", 1))
	require.NoError(t, set.Record("run-1", "core", 1, now))
	assert.True(t, set.IsSeen("run-1", "core", 1))
	assert.False(t, set.IsSeen("run-1", "core", 2), "another attempt of one task is other work")
	assert.False(t, set.IsSeen("run-2", "core", 1), "another run of one task is other work")

	reopened, err := LoadSeenSet(path, now)
	require.NoError(t, err)
	assert.True(t, reopened.IsSeen("run-1", "core", 1), "the record outlives the process")
	assert.NoFileExists(t, path+".tmp", "the file is replaced rather than written in place")

	t.Run("entries older than the replay window are dropped", func(t *testing.T) {
		later, err := LoadSeenSet(path, now.Add(replayWindow+time.Hour))

		require.NoError(t, err)
		assert.False(t, later.IsSeen("run-1", "core", 1),
			"a message that old is refused by the window itself, so keeping it would only grow the file")
	})

	t.Run("a record that cannot be read is reported rather than ignored", func(t *testing.T) {
		broken := filepath.Join(t.TempDir(), "seen.json")
		require.NoError(t, os.WriteFile(broken, []byte("{"), 0o644))

		_, err := LoadSeenSet(broken, now)

		require.Error(t, err)
	})
}

// TestNodeStateLockKeepsOneInodeAcrossHolders: a crashed PID is overwritten
// under the kernel lock and the same inode remains after each release. No
// second process can claim another inode in a rename or removal gap.
func TestNodeStateLockKeepsOneInodeAcrossHolders(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "build-a")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, stateLockFile)
	require.NoError(t, os.WriteFile(path, []byte("4194304"), 0o644))
	original, err := os.Stat(path)
	require.NoError(t, err)

	_, release, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")
	require.NoError(t, err)
	claimed, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, os.SameFile(original, claimed))
	assert.FileExists(t, path)
	_, _, err = OpenNodeState(root, "build-a", "file:///srv/mailbox.git")
	require.Error(t, err)
	assert.Contains(t, err.Error(), strconv.Itoa(os.Getpid()))
	require.NoError(t, release())

	released, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, os.SameFile(original, released))
	_, release, err = OpenNodeState(root, "build-a", "file:///srv/mailbox.git")
	require.NoError(t, err)
	require.NoError(t, release())
	final, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, os.SameFile(original, final))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "only the stable lock and cache directory remain")
}
