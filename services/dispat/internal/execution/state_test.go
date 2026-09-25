// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		// The process that started this test stands in for the one serving
		// the folder: it is alive and it is not this process.
		lock := filepath.Join(state.Dir, stateLockFile)
		require.NoError(t, os.WriteFile(lock, []byte(strconv.Itoa(os.Getppid())), 0o644))
		defer func() { require.NoError(t, os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o644)) }()

		_, _, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")

		require.Error(t, err)
		assert.Equal(t, config.DiagnosticExecution, config.DiagnosticCode(err))
		assert.Equal(t, CategoryConfiguration, DiagnosticCategory(err))
		assert.Contains(t, err.Error(), strconv.Itoa(os.Getppid()))
	})

	t.Run("another node in the same folder is a folder of its own", func(t *testing.T) {
		other, otherRelease, err := OpenNodeState(root, "build-b", "file:///srv/mailbox.git")

		require.NoError(t, err)
		assert.NotEqual(t, state.Dir, other.Dir)
		require.NoError(t, otherRelease())
	})

	require.NoError(t, release())
	assert.NoFileExists(t, filepath.Join(state.Dir, stateLockFile))
	require.NoError(t, release(), "giving the folder back twice is giving it back once")

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

	t.Run("a lock naming this process's own id is an earlier process's", func(t *testing.T) {
		// A worker restarted in a container is process 1 every time, so the
		// lock its crashed predecessor left names the restarted process. It
		// has not written its claim yet, so the lock cannot be its own.
		require.NoError(t, os.WriteFile(filepath.Join(state.Dir, stateLockFile),
			[]byte(strconv.Itoa(os.Getpid())), 0o644))

		_, release, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")

		require.NoError(t, err, "the restarted worker takes its folder over")
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
			written <- os.WriteFile(lock, []byte(strconv.Itoa(os.Getppid())), 0o644)
		}()

		_, _, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")

		require.NoError(t, <-written)
		require.Error(t, err, "the owner was starting, not gone")
		assert.Contains(t, err.Error(), strconv.Itoa(os.Getppid()))
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

	t.Run("an oversized owner cannot claim the folder", func(t *testing.T) {
		lock := filepath.Join(state.Dir, stateLockFile)
		require.NoError(t, os.WriteFile(lock, []byte(strings.Repeat("9", stateLockOwnerMaxBytes+1)), 0o644))

		_, _, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")

		require.ErrorContains(t, err, "exceeds 64 bytes")
		require.NoError(t, os.Remove(lock))
	})
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

	t.Run("a valid future-issued message stays remembered past 24 hours", func(t *testing.T) {
		later, err := LoadSeenSet(path, now.Add(replayWindow+time.Hour))

		require.NoError(t, err)
		assert.True(t, later.IsSeen("run-1", "core", 1),
			"a message issued 24 hours ahead at acceptance could still be valid")
	})

	t.Run("entries older than the full acceptance horizon are dropped", func(t *testing.T) {
		later, err := LoadSeenSet(path, now.Add(seenRetentionWindow+time.Hour))

		require.NoError(t, err)
		assert.False(t, later.IsSeen("run-1", "core", 1),
			"even a future-issued message that old is refused by the header window")
	})

	t.Run("a record that cannot be read is reported rather than ignored", func(t *testing.T) {
		broken := filepath.Join(t.TempDir(), "seen.json")
		require.NoError(t, os.WriteFile(broken, []byte("{"), 0o644))

		_, err := LoadSeenSet(broken, now)

		require.Error(t, err)
	})
}

// TestSeenSetRecordPrunesWithoutRestart: a serving worker can remain alive
// longer than the full acceptance horizon, so each new answer must bound both its
// in-memory record and the file without losing a tuple still on the boundary.
func TestSeenSetRecordPrunesWithoutRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seen.json")
	first := time.Now().UTC().Truncate(time.Second)
	set, err := LoadSeenSet(path, first)
	require.NoError(t, err)
	require.NoError(t, set.Record("run-old", "probe", 1, first))
	require.NoError(t, set.Record("run-boundary", "probe", 1, first.Add(time.Second)))
	latest := first.Add(seenRetentionWindow + time.Second)
	require.NoError(t, set.Record("run-new", "probe", 1, latest))

	assert.False(t, set.IsSeen("run-old", "probe", 1))
	assert.True(t, set.IsSeen("run-boundary", "probe", 1))
	assert.True(t, set.IsSeen("run-new", "probe", 1))
	// A late duplicate cannot replace the newest timestamp of its tuple.
	require.NoError(t, set.Record("run-new", "probe", 1, first.Add(time.Second)))
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	stored := map[string]time.Time{}
	require.NoError(t, json.Unmarshal(content, &stored))
	assert.NotContains(t, stored, formatTriple("run-old", "probe", 1))
	assert.Equal(t, first.Add(time.Second), stored[formatTriple("run-boundary", "probe", 1)])
	assert.Equal(t, latest, stored[formatTriple("run-new", "probe", 1)])
}

// TestAStaleLockIsTakenOverWithoutTakingAFreshOne: the race the takeover used
// to lose. Two processes that read the same stale lock could interleave a
// remove and a create, so the second removed the first's fresh claim and both
// believed they owned the folder, each holding half the record of what had
// been answered.
//
// The claim that arrives between the read and the takeover is simulated by
// writing a live process id into the lock, which is exactly what the winner
// would have left there. The parent of the test binary is that process: it is
// running and it is not this one, whose own id in a lock it has not written
// yet can only be an earlier process's.
func TestAStaleLockIsTakenOverWithoutTakingAFreshOne(t *testing.T) {
	t.Run("a lock its process no longer holds is taken over", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), stateLockFile)
		require.NoError(t, os.WriteFile(path, []byte("999999"), 0o644))

		owner, err := claimNodeLock(path)

		require.NoError(t, err)
		assert.Zero(t, owner, "nobody holds it, so this process does")
		held, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, strconv.Itoa(os.Getpid()), string(held))
	})

	t.Run("a claim written between the read and the takeover is put back", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), stateLockFile)
		require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(os.Getppid())), 0o644))

		// The stale process id this caller read a moment ago, and a live one
		// in the file now: the winner of the race got there first.
		owner, err := takeOverNodeLock(path, 999999)

		require.NoError(t, err)
		assert.Equal(t, os.Getppid(), owner, "the loser reports the winner")
		held, err := os.ReadFile(path)
		require.NoError(t, err, "the winner's lock is still there")
		assert.Equal(t, strconv.Itoa(os.Getppid()), string(held))
		entries, err := os.ReadDir(filepath.Dir(path))
		require.NoError(t, err)
		assert.Len(t, entries, 1, "nothing is left beside it")
	})

	t.Run("a third process that claimed the path keeps it", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, stateLockFile)
		aside := path + ".taken." + strconv.Itoa(os.Getpid())
		require.NoError(t, os.WriteFile(aside, []byte(strconv.Itoa(os.Getpid())), 0o644))
		require.NoError(t, os.WriteFile(path, []byte("12345"), 0o644))

		owner, err := restoreNodeLock(path, aside, os.Getpid())

		require.NoError(t, err)
		assert.Equal(t, os.Getpid(), owner)
		held, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "12345", string(held), "the third process's claim is not replaced")
		_, err = os.Stat(aside)
		assert.True(t, os.IsNotExist(err), "and the copy is gone")
	})

	t.Run("a claim that lands between the removal and the create is reported", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), stateLockFile)
		require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(os.Getppid())), 0o644))

		owner, err := claimNodeLockAfterTakeover(path)

		require.NoError(t, err)
		assert.Equal(t, os.Getppid(), owner, "the process that created the lock first owns the folder")
	})
}

// TestNodeStateClaimSettlesBeforeItIsTrusted: processes started together can
// each finish a claim they read as a win, so a claim is trusted only if the
// lock still names its process once every contender has had the grace to
// write. A claimant whose lock was replaced, or taken away, in that window is
// refused with the E225 it would have met a moment later, and removes nothing.
func TestNodeStateClaimSettlesBeforeItIsTrusted(t *testing.T) {
	// The parent of the test binary is a process that is running and is not
	// this one, which is what a contender that wrote last looks like.
	contender := os.Getppid()
	for name, tc := range map[string]struct {
		replace func(lock string) error
		says    string
	}{
		"a lock another claimant wrote last is theirs": {
			replace: func(lock string) error {
				return os.WriteFile(lock, []byte(strconv.Itoa(contender)), 0o644)
			},
			says: "served by process " + strconv.Itoa(contender),
		},
		"a lock taken away during the settle proves nothing": {
			replace: os.Remove,
			says:    "served by another process",
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			lock := filepath.Join(root, "build-a", stateLockFile)
			opened := make(chan error, 1)
			go func() {
				_, _, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")
				opened <- err
			}()
			require.Eventually(t, func() bool {
				held, err := os.ReadFile(lock)
				return err == nil && string(held) == strconv.Itoa(os.Getpid())
			}, stateLockWriteGrace/2, stateLockWritePoll, "the claim is written before it settles")
			require.NoError(t, tc.replace(lock))

			err := <-opened

			require.Error(t, err)
			assert.Equal(t, config.DiagnosticExecution, config.DiagnosticCode(err))
			assert.Equal(t, CategoryConfiguration, DiagnosticCategory(err))
			assert.Contains(t, err.Error(), tc.says)
			if held, readErr := os.ReadFile(lock); readErr == nil {
				assert.Equal(t, strconv.Itoa(contender), string(held), "the other claim is left in place")
			}
		})
	}
}

// TestNodeStateAnswersForItsOwnerOnly: a serving node asks before every claim
// whether the folder is still its own, and gives back only a lock that still
// names it. Another process's id is that process's claim, whoever wrote it.
func TestNodeStateAnswersForItsOwnerOnly(t *testing.T) {
	root := t.TempDir()
	state, release, err := OpenNodeState(root, "build-a", "file:///srv/mailbox.git")
	require.NoError(t, err)
	lock := filepath.Join(state.Dir, stateLockFile)
	require.NoError(t, state.VerifyOwner(), "a folder this process claimed is its own")

	for _, content := range []string{"", "0", "not-a-pid"} {
		require.NoError(t, os.WriteFile(lock, []byte(content), 0o644))
		assert.NoError(t, state.VerifyOwner(), "a lock naming nobody %q is no other claim", content)
	}
	require.NoError(t, os.Remove(lock))
	assert.NoError(t, state.VerifyOwner(), "a lock that is gone is no other claim")

	other := strconv.Itoa(os.Getppid())
	require.NoError(t, os.WriteFile(lock, []byte(other), 0o644))
	err = state.VerifyOwner()
	require.Error(t, err)
	assert.Equal(t, config.DiagnosticExecution, config.DiagnosticCode(err))
	assert.Equal(t, CategoryConfiguration, DiagnosticCategory(err))
	assert.Contains(t, err.Error(), "served by process "+other)

	require.NoError(t, release(), "a lock another process wrote is not an error to leave")
	held, err := os.ReadFile(lock)
	require.NoError(t, err, "and it is left for its owner")
	assert.Equal(t, other, string(held))
}
