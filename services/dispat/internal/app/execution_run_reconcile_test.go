package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func TestInterruptedFirstRunBeforeRemoteEffectsRetiresExactLocator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("distributed first-node journal requires durable directory replacement")
	}
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q")
	settings := &config.File{Execution: &config.ExecutionConfig{
		Workers: []config.ExecutionWorkerConfig{{Name: "peer"}},
	}}
	first := New(root, settings, zerolog.Nop())
	first.startExecutionRun()
	require.NoError(t, first.prepareFirstRunIdentity(t.Context(), "https://127.0.0.1:43881", ""))
	prior := first.runID
	require.NoError(t, first.returnJournal.close())
	first.returnJournal = nil

	restarted := New(root, settings, zerolog.Nop())
	require.NoError(t, restarted.reconcilePriorFirstRun(t.Context()))
	gitDir, err := restarted.git.GitDir(t.Context())
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(gitDir, "dispat", "return-runs", "active-run"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(gitDir, "dispat", "return-runs", prior, "identity.json"))
	require.NoError(t, err, "the protected identity remains for audit")
}

func TestInterruptedFirstRunRetainsPreparedLockBeforeReconciliation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("distributed first-node journal requires durable directory replacement")
	}
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q")
	settings := &config.File{Execution: &config.ExecutionConfig{
		Workers: []config.ExecutionWorkerConfig{{Name: "peer"}},
	}}
	first := New(root, settings, zerolog.Nop())
	first.startExecutionRun()
	require.NoError(t, first.prepareFirstRunIdentity(t.Context(), "https://127.0.0.1:43881", ""))
	require.NoError(t, first.returnJournal.recordPreparedLock("", release.PreparedLockIdentity{
		Run: first.runID, Remote: "origin", LocalTag: "dispat-release-lock-attempt-a",
		Object: strings.Repeat("b", 40),
	}))
	require.NoError(t, first.returnJournal.close())
	first.returnJournal = nil

	restarted := New(root, settings, zerolog.Nop())
	require.ErrorContains(t, restarted.reconcilePriorFirstRun(t.Context()), "cannot be read")
	gitDir, err := restarted.git.GitDir(t.Context())
	require.NoError(t, err)
	active, err := readActiveRun(filepath.Join(gitDir, "dispat", "return-runs", "active-run"))
	require.NoError(t, err)
	require.Equal(t, first.runID, active)
}

func TestInterruptedFirstRunDeletesOnlyItsUnstartedRemoteLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("distributed first-node journal requires durable directory replacement")
	}
	for _, foreign := range []bool{false, true} {
		name := "owned"
		if foreign {
			name = "foreign-replaced"
		}
		t.Run(name, func(t *testing.T) {
			root, remote := t.TempDir(), t.TempDir()
			fixtureGit(t, root, "init", "-q")
			fixtureGit(t, root, "config", "user.name", "Test")
			fixtureGit(t, root, "config", "user.email", "test@example.com")
			require.NoError(t, os.WriteFile(filepath.Join(root, "README"), []byte("source\n"), 0o644))
			fixtureGit(t, root, "add", "README")
			fixtureGit(t, root, "commit", "-qm", "initial")
			fixtureGit(t, remote, "init", "--bare", "-q")
			settings := &config.File{Execution: &config.ExecutionConfig{
				Workers: []config.ExecutionWorkerConfig{{Name: "peer"}},
			}}
			first := New(root, settings, zerolog.Nop())
			first.startExecutionRun()
			require.NoError(t, first.prepareFirstRunIdentity(t.Context(), "https://127.0.0.1:43881", ""))
			lock := &release.Lock{Git: first.git, Remote: remote, Run: first.runID, Log: zerolog.Nop(),
				OnPrepared: func(_ context.Context, prepared release.PreparedLockIdentity) error {
					return first.returnJournal.recordPreparedLock("", prepared)
				}}
			require.NoError(t, lock.Acquire(t.Context()))
			owned, err := first.git.RemoteTagObject(t.Context(), remote, release.LockTagName)
			require.NoError(t, err)
			require.NotEmpty(t, owned)
			if foreign {
				fixtureGit(t, root, "tag", "-a", "foreign-lock", "-m", "another run", "HEAD")
				fixtureGit(t, root, "push", "--force", remote,
					"refs/tags/foreign-lock:refs/tags/"+release.LockTagName)
			}
			require.NoError(t, first.returnJournal.close())
			first.returnJournal = nil
			restarted := New(root, settings, zerolog.Nop())
			err = restarted.reconcilePriorFirstRun(t.Context())
			current, readErr := restarted.git.RemoteTagObject(t.Context(), remote, release.LockTagName)
			require.NoError(t, readErr)
			if foreign {
				require.ErrorContains(t, err, "was replaced")
				require.NotEmpty(t, current)
				require.NotEqual(t, owned, current, "a foreign lock was deleted")
			} else {
				require.NoError(t, err)
				require.Empty(t, current, "the exact old lock was not released")
			}
		})
	}
}

type lostPreparedLockDeleteReply struct {
	object  string
	deletes int
}

func (f *lostPreparedLockDeleteReply) RemoteTagObject(context.Context, string, string) (string, error) {
	return f.object, nil
}

func (f *lostPreparedLockDeleteReply) DeleteRemoteTagLease(_ context.Context, _, _, expected string) error {
	if f.object != expected {
		return errors.New("another object holds the lock")
	}
	f.deletes++
	f.object = "" // The remote applied the delete, but its reply was lost.
	return errors.New("connection ended after push")
}

func TestInterruptedFirstRunSettlesLostPreparedLockDeleteReply(t *testing.T) {
	const oid = "0123456789abcdef0123456789abcdef01234567"
	remote := &lostPreparedLockDeleteReply{object: oid}
	identity := firstRunIdentity{Run: "run-one", PreparedLocks: map[string]release.PreparedLockIdentity{
		"": {Run: "run-one", Remote: "file:///remote", LocalTag: "dispat-release-lock-attempt-a", Object: oid},
	}}
	require.NoError(t, reconcileUnstartedReleaseLocksWithGit(t.Context(), identity, remote))
	require.Equal(t, 1, remote.deletes)
	require.Empty(t, remote.object)
}
