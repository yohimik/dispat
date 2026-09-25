//go:build !windows

package integration

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unixDestinationRefusals are the rows of
// TestInstallRefusesADestinationItMustNotReplace that only a unix filesystem
// can hold. Naming what stands in the way is the point, because "not a
// regular file" tells a reader nothing they can act on.
func unixDestinationRefusals() []installDestinationRefusal {
	var socket string
	var listener net.Listener
	return []installDestinationRefusal{{
		name: "a named pipe",
		occupy: func(t *testing.T, r *toolRepo) []string {
			require.NoError(t, syscall.Mkfifo(r.installed(), 0o600))
			t.Cleanup(func() { _ = os.Remove(r.installed()) })
			return r.installArgsIn(r.bin)
		},
		want: "is a named pipe",
		kept: func(t *testing.T, r *toolRepo) {
			info, err := os.Lstat(r.installed())
			require.NoError(t, err)
			assert.NotZero(t, info.Mode()&os.ModeNamedPipe, "the pipe is still a pipe")
		},
	}, {
		name: "a device",
		occupy: func(t *testing.T, r *toolRepo) []string {
			if _, err := os.Stat("/dev/null"); err != nil {
				t.Skip("no /dev/null on this machine")
			}
			return r.installArgsIn("/dev", "--as", "null", "--check")
		},
		want: "is a device",
		kept: func(t *testing.T, r *toolRepo) {
			info, err := os.Stat(filepath.Join("/dev", "null"))
			require.NoError(t, err)
			assert.NotZero(t, info.Mode()&os.ModeDevice, "the device is untouched")
		},
	}, {
		// Moving a live socket aside would silently disconnect the process
		// that owns it. Its folder is short on purpose: a socket path has a
		// length limit a test folder can exceed.
		name: "a live socket",
		occupy: func(t *testing.T, r *toolRepo) []string {
			bin, err := os.MkdirTemp("/tmp", "dispat-sock-")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.RemoveAll(bin) })
			socket = filepath.Join(bin, "tool"+exeSuffix())
			listener, err = net.Listen("unix", socket)
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = listener.Close()
				_ = os.Remove(socket)
			})
			return r.installArgsIn(bin)
		},
		want: "is a socket, not a file dispat may replace",
		kept: func(t *testing.T, r *toolRepo) {
			conn, err := net.Dial("unix", socket)
			require.NoError(t, err, "the refused install preserves the live socket")
			require.NoError(t, conn.Close())
		},
	}}
}

// TestInstallRollbackKeepsTheCurrentToolWhenRotationCannotStart exercises the
// rollback failure before its first rename. A read-only install directory
// cannot hold the temporary parked name, so the working executable must stay
// installed and the backup must remain available for a later retry.
func TestInstallRollbackKeepsTheCurrentToolWhenRotationCannotStart(t *testing.T) {
	requireShell(t)
	skipIfSuperuser(t)
	r := newToolRepo(t)
	require.Equal(t, 0, r.install("--release", toolOld).Code)
	require.Equal(t, 0, r.install().Code)
	require.Equal(t, toolNew, r.version(r.installed()))
	require.Equal(t, toolOld, r.version(backupPath(r.installed())))

	require.NoError(t, os.Chmod(r.bin, 0o555))
	t.Cleanup(func() { _ = os.Chmod(r.bin, 0o755) })
	res := r.bare("--rollback")
	require.NoError(t, os.Chmod(r.bin, 0o755))

	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "re-run with the rights to replace")
	assert.Equal(t, toolNew, r.version(r.installed()), "failed rollback preserves the current tool")
	assert.Equal(t, toolOld, r.version(backupPath(r.installed())), "failed rollback preserves its backup")
}
