//go:build !windows

package fsx

import (
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLockFileOwnershipEndsWithoutWaitingForChildCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.lock")
	owner, err := OpenLockFile(path, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = owner.Close() })
	require.NoError(t, syscall.Flock(int(owner.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	child := exec.CommandContext(t.Context(), "sleep", "30")
	require.NoError(t, child.Start())
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	require.NoError(t, owner.Close())
	require.NoError(t, child.Process.Signal(syscall.Signal(0)), "the child is still running")
	recovered, err := OpenLockFile(path, 0o600)
	require.NoError(t, err)
	defer recovered.Close()
	require.NoError(t, syscall.Flock(int(recovered.Fd()), syscall.LOCK_EX|syscall.LOCK_NB),
		"a child command must not retain its parent's ownership lock")
}
