package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// TestFleetReleaseLockWaitsForTheRepositoryTransaction: taking a repository's
// remote release lock writes a local attempt ref, so it waits for a Git
// transaction this process is running in that repository. A wait that runs
// out is E336 and leaves no attempt ref behind; once the transaction ends the
// lock is taken and given back cleanly.
func TestFleetReleaseLockWaitsForTheRepositoryTransaction(t *testing.T) {
	w, _ := recordFixture(t, false, false)
	source := w.byName["source"]
	w.app.cfg.UnsafeDisableLock = false
	source.repo.Config.UnsafeDisableLock = false
	w.ordered = []*repositoryRecord{source}

	unlock, err := source.git.AcquireMutation(t.Context())
	require.NoError(t, err)
	defer unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Millisecond)
	defer cancel()
	_, err = w.acquire(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, "E336", config.DiagnosticCode(err))
	assert.Empty(t, recordGit(t, source.repo.Root, "tag", "--list", gitx.LockAttemptTagPrefix+"*"),
		"waiting for the local transaction must not leave an attempt tag")
	unlock()

	releaseLock, err := w.acquire(t.Context())
	require.NoError(t, err)
	releaseLock()
	assert.Empty(t, recordGit(t, source.repo.Root, "tag", "--list", gitx.LockAttemptTagPrefix+"*"))
	assert.Empty(t, recordGit(t, source.repo.Root, "ls-remote", "--tags", "origin", gitx.LockTagName))
}
