package fsx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteFileAtomicCreates: a fresh file lands with the given mode and no
// temp file survives beside it.
func TestWriteFileAtomicCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	require.NoError(t, WriteFileAtomic(path, []byte("hello\n"), 0o644))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	assertOnly(t, dir, "CHANGELOG.md")
}

// TestWriteFileAtomicReplacesKeepingTheMode: the caller decides the mode, so a
// 0600 file replaced with its own mode stays 0600.
func TestWriteFileAtomicReplacesKeepingTheMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))

	require.NoError(t, WriteFileAtomic(path, []byte("new"), 0o600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assertOnly(t, dir, "config.json")
}

// TestWriteFileAtomicFailureLeavesTheTarget: a write that cannot complete
// leaves the previous content in place and no temp litter behind.
func TestWriteFileAtomicFailureLeavesTheTarget(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "kept.txt")
	require.NoError(t, os.WriteFile(path, []byte("kept"), 0o644))
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	assert.Error(t, WriteFileAtomic(path, []byte("lost"), 0o644))

	require.NoError(t, os.Chmod(dir, 0o755))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "kept", string(data))
	assertOnly(t, dir, "kept.txt")
}

// TestWriteFileAtomicRenameFailureCleansUp: a target that is a folder fails at
// the rename, and the temp file is cleaned up.
func TestWriteFileAtomicRenameFailureCleansUp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dispat.json")
	require.NoError(t, os.Mkdir(target, 0o755))

	assert.Error(t, WriteFileAtomic(target, []byte("{}"), 0o644))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only the pre-existing folder remains")
}

func TestWriteFileAtomicRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	link := filepath.Join(dir, "dispat.json")
	require.NoError(t, os.WriteFile(target, []byte("kept"), 0o644))
	require.NoError(t, os.Symlink(target, link))

	err := WriteFileAtomic(link, []byte("replaced"), 0o644)
	require.Error(t, err)
	data, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, "kept", string(data))
	info, statErr := os.Lstat(link)
	require.NoError(t, statErr)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the link itself survives")
}

// assertOnly fails when dir holds anything besides the named file — a leftover
// temp file is exactly the litter the helper promises not to leave.
func assertOnly(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, name, entries[0].Name())
}
