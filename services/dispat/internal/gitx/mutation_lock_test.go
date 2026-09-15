package gitx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mutationHelperEnv = "DISPAT_TEST_MUTATION_LOCK_HELPER"

// TestMutationLockHelper is invoked as a separate process by the contention
// test. A process boundary is material here: an in-process mutex would pass a
// goroutine test while still allowing two dispat commands to mutate one Git
// index and HEAD concurrently.
func TestMutationLockHelper(t *testing.T) {
	if os.Getenv(mutationHelperEnv) == "" {
		return
	}
	repo, ready, stop := os.Getenv("DISPAT_TEST_MUTATION_REPO"),
		os.Getenv("DISPAT_TEST_MUTATION_READY"), os.Getenv("DISPAT_TEST_MUTATION_STOP")
	release, err := (&CLI{Dir: repo}).AcquireMutation(context.Background())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(ready, []byte("held"), 0o600))
	for {
		if _, err := os.Stat(stop); err == nil {
			break
		} else if !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	release()
	release() // callers may safely defer and explicitly release during recovery
}

func TestMutationLockCoordinatesLinkedWorktreesAcrossProcesses(t *testing.T) {
	root, cli := initRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, root, "worktree", "add", "--detach", linked, "HEAD")

	signals := t.TempDir()
	ready, stop := filepath.Join(signals, "ready"), filepath.Join(signals, "stop")
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(exe, "-test.run=^TestMutationLockHelper$")
	cmd.Env = append(os.Environ(), mutationHelperEnv+"=1",
		"DISPAT_TEST_MUTATION_REPO="+linked,
		"DISPAT_TEST_MUTATION_READY="+ready,
		"DISPAT_TEST_MUTATION_STOP="+stop)
	output := newLockedBuffer()
	cmd.Stdout, cmd.Stderr = output, output
	require.NoError(t, cmd.Start())
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
			<-done
		}
	})
	waitForFile(t, ready, done, func() error { return waitErr }, output)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	release, err := cli.AcquireMutation(ctx)
	assert.Nil(t, release)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), err)

	require.NoError(t, os.WriteFile(stop, []byte("release"), 0o600))
	select {
	case <-done:
		require.NoError(t, waitErr, output.String())
	case <-time.After(5 * time.Second):
		t.Fatal("mutation-lock helper did not release")
	}

	linkedCLI := &CLI{Dir: linked}
	release, err = AcquireMutations(context.Background(), linkedCLI, cli, linkedCLI)
	require.NoError(t, err)
	release()
	release()

	lockPath, err := cli.mutationLockPath(context.Background())
	require.NoError(t, err)
	// Windows refuses removal while any process still holds the file. On every
	// platform this also leaves the fixture clean and exercises recreation.
	require.NoError(t, os.Remove(lockPath), "release must close the lock descriptor")
	release, err = cli.AcquireMutation(context.Background())
	require.NoError(t, err)
	release()
}

func TestMutationLockHonorsAlreadyCanceledContext(t *testing.T) {
	_, cli := initRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	release, err := cli.AcquireMutation(ctx)
	assert.Nil(t, release)
	require.ErrorIs(t, err, context.Canceled)
}

func waitForFile(t *testing.T, path string, done <-chan struct{}, waitErr func() error, output interface{ String() string }) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		select {
		case <-done:
			t.Fatalf("mutation-lock helper exited before acquiring: %v\n%s", waitErr(), output.String())
		case <-deadline.C:
			t.Fatalf("mutation-lock helper did not acquire\n%s", output.String())
		case <-tick.C:
		}
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newLockedBuffer() *lockedBuffer { return &lockedBuffer{} }

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
