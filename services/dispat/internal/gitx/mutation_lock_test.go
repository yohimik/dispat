package gitx

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireMutationHeld proves that repository cannot be taken right now: an
// acquisition bounded by a short deadline must run out of time rather than
// succeed. The bound only decides how long the proof takes, never its answer,
// because the slot is held for the whole wait.
func requireMutationHeld(t *testing.T, repository *LocalGitx, msgAndArgs ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	release, err := repository.AcquireMutation(ctx)
	if err == nil {
		release()
	}
	assert.Nil(t, release, msgAndArgs...)
	assert.ErrorIs(t, err, context.DeadlineExceeded, msgAndArgs...)
}

// TestMutationLockSerializesTransactionsInOneRepository: every transaction a
// process runs in one repository runs alone. Eight goroutines each take the
// repository several times and count who is inside; the count never passes
// one, and none of them is left waiting.
func TestMutationLockSerializesTransactionsInOneRepository(t *testing.T) {
	root, cli := initRepo(t)
	var inside, overlaps atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				release, err := (&LocalGitx{Dir: root}).AcquireMutation(ctx)
				cancel()
				if !assert.NoError(t, err) {
					return
				}
				if inside.Add(1) != 1 {
					overlaps.Add(1)
				}
				time.Sleep(time.Millisecond)
				inside.Add(-1)
				release()
			}
		}()
	}
	wg.Wait()
	assert.Zero(t, overlaps.Load(), "two transactions of one repository ran at once")

	release, err := cli.AcquireMutation(context.Background())
	require.NoError(t, err)
	requireMutationHeld(t, cli, "a second transaction waits for the first")
	release()
	assert.NoFileExists(t, filepath.Join(root, ".git", "dispat-mutation.lock"),
		"the exclusion is the process's own and leaves nothing in the repository")
}

// TestMutationLockIsSharedByLinkedWorktrees: a linked worktree shares its
// object database and refs with the checkout it was added to, so the two are
// one repository to the lock. One transaction naming both, in any order and
// even twice, takes that repository once rather than waiting for itself.
func TestMutationLockIsSharedByLinkedWorktrees(t *testing.T) {
	root, cli := initRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, root, "worktree", "add", "--detach", linked, "HEAD")
	linkedCLI := &LocalGitx{Dir: linked}

	release, err := linkedCLI.AcquireMutation(context.Background())
	require.NoError(t, err)
	requireMutationHeld(t, cli, "the checkout waits while its linked worktree records")
	release()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err = AcquireMutations(ctx, linkedCLI, cli, linkedCLI)
	require.NoError(t, err, "one repository named three times is taken once")
	requireMutationHeld(t, linkedCLI)
	release()

	release, err = cli.AcquireMutation(context.Background())
	require.NoError(t, err, "giving the repository back once frees it")
	release()
}

// TestMutationLockAcquiresRepositoriesInOneOrder: two transactions that each
// need the same two repositories, named in opposite orders, never wait on one
// another. Both orders run against each other many times; every acquisition
// completes well inside its bound.
func TestMutationLockAcquiresRepositoriesInOneOrder(t *testing.T) {
	_, first := initRepo(t)
	_, second := initRepo(t)
	var wg sync.WaitGroup
	for index := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			repositories := []*LocalGitx{first, second}
			if index%2 == 1 {
				repositories = []*LocalGitx{second, first}
			}
			for range 20 {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				release, err := AcquireMutations(ctx, repositories...)
				cancel()
				if !assert.NoError(t, err, "opposite orders must not deadlock") {
					return
				}
				release()
			}
		}()
	}
	wg.Wait()
}

// TestMutationLockWaiterStopsWithItsContext: a waiter is somebody's command,
// and cancelling the command ends the wait at once. The cancelled waiter takes
// nothing, so the repository is free as soon as its holder gives it back.
func TestMutationLockWaiterStopsWithItsContext(t *testing.T) {
	_, cli := initRepo(t)
	release, err := cli.AcquireMutation(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan error, 1)
	go func() {
		waiting, waitErr := cli.AcquireMutation(ctx)
		if waiting != nil {
			waiting()
		}
		waited <- waitErr
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case waitErr := <-waited:
		assert.ErrorIs(t, waitErr, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled waiter kept waiting")
	}
	release()

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	release, err = cli.AcquireMutation(ctx)
	require.NoError(t, err, "the cancelled waiter left the repository free")
	release()
}

// TestMutationLockHonorsAlreadyCanceledContext: a context that is already done
// takes nothing, whether the common directory still has to be resolved or is
// already known and its slot is free.
func TestMutationLockHonorsAlreadyCanceledContext(t *testing.T) {
	_, cli := initRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	release, err := cli.AcquireMutation(ctx)
	assert.Nil(t, release)
	require.ErrorIs(t, err, context.Canceled)

	warm, err := cli.AcquireMutation(context.Background())
	require.NoError(t, err)
	warm()
	for range 20 {
		release, err = cli.AcquireMutation(ctx)
		assert.Nil(t, release)
		require.ErrorIs(t, err, context.Canceled, "a free slot is no reason to ignore a done context")
	}
}

// TestMutationLockReleaseIsIdempotent: a release may be deferred and also
// called early on a recovery path. The second call gives back nothing, above
// all not a hold some later transaction has taken since.
func TestMutationLockReleaseIsIdempotent(t *testing.T) {
	_, cli := initRepo(t)
	release, err := cli.AcquireMutation(context.Background())
	require.NoError(t, err)
	release()
	release()

	later, err := cli.AcquireMutation(context.Background())
	require.NoError(t, err)
	release()
	requireMutationHeld(t, cli, "a stale release must not free a later holder")
	later()
	later()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	again, err := cli.AcquireMutation(ctx)
	require.NoError(t, err, "the later holder's own release freed the repository")
	again()
}
