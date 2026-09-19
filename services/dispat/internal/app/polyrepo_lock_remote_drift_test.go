package app

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestFleetLockCleanupUsesRemoteResolvedAtAcquisition(t *testing.T) {
	w, _ := recordFixture(t, false, false)
	source := w.byName["source"]
	w.app.cfg.UnsafeDisableLock = false
	source.repo.Config.UnsafeDisableLock = false
	w.ordered = []*repositoryRecord{source}
	original := recordGit(t, source.repo.Root, "remote", "get-url", "origin")

	replacement := t.TempDir()
	out, err := exec.Command("git", "init", "-q", "--bare", replacement).CombinedOutput()
	require.NoError(t, err, "%s", out)

	releaseLocks, err := w.acquire(t.Context())
	require.NoError(t, err)
	assert.NotEmpty(t, recordGit(t, source.repo.Root, "ls-remote", "--tags", original, gitx.LockTagName))

	recordGit(t, source.repo.Root, "remote", "set-url", "origin", replacement)
	releaseLocks()

	assert.Empty(t, recordGit(t, source.repo.Root, "ls-remote", "--tags", original, gitx.LockTagName),
		"cleanup must remove the owned lock from the destination used at acquisition")
	assert.Empty(t, recordGit(t, source.repo.Root, "ls-remote", "--tags", replacement, gitx.LockTagName),
		"cleanup must never follow a mutated alias to a different destination")
}
