//go:build !windows

package integration

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInstallRefusesALiveSocketDestination proves the target guard identifies
// a service endpoint before release discovery or download. Moving a live
// socket aside would silently disconnect the process that owns it.
func TestInstallRefusesALiveSocketDestination(t *testing.T) {
	r := newToolRepo(t)
	bin, err := os.MkdirTemp("/tmp", "dispat-sock-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(bin) })
	target := filepath.Join(bin, "tool"+exeSuffix())
	listener, err := net.Listen("unix", target)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(target)
	})

	before := len(r.requests())
	res := r.Command("install", "https://github.com/acme/tool", "--api-url", r.api,
		"--bin-dir", bin, "--asset", "tool-{os}-{arch}")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "is a socket, not a file dispat may replace")
	assert.Equal(t, before, len(r.requests()), "target refusal happens before release discovery")

	conn, err := net.Dial("unix", target)
	require.NoError(t, err, "the refused install preserves the live socket")
	require.NoError(t, conn.Close())
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
