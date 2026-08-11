package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackupPathKeepsAWindowsExtension: on Unix the name simply gains
// .backup. On Windows the extension has to survive, because a file Windows
// refuses to execute can neither be checked before a rollback nor run by hand
// after one.
func TestBackupPathKeepsAWindowsExtension(t *testing.T) {
	assert.Equal(t, "/usr/local/bin/dispat.backup", BackupPath("/usr/local/bin/dispat"))
	assert.Equal(t, `C:\bin\dispat.backup.exe`, BackupPath(`C:\bin\dispat.exe`))
}

// TestPruneBackupWaitsOutTheWeek: the copy is there to roll back to, so it
// stays for as long as rolling back is plausible and then goes away on its
// own. Nothing else about it is ever written, which is what makes this safe
// to run on every single invocation.
func TestPruneBackupWaitsOutTheWeek(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	require.NoError(t, os.WriteFile(exe, []byte("current"), 0o755))
	backup := BackupPath(exe)
	require.NoError(t, os.WriteFile(backup, []byte("previous"), 0o755))

	now := time.Now()
	assert.False(t, PruneBackup(exe, now), "a fresh backup stays")
	assert.FileExists(t, backup)

	sixDays := now.Add(-6 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(backup, sixDays, sixDays))
	assert.False(t, PruneBackup(exe, now), "six days in, it still stays")
	assert.FileExists(t, backup)

	old := now.Add(-BackupTTL - time.Minute)
	require.NoError(t, os.Chtimes(backup, old, old))
	assert.True(t, PruneBackup(exe, now), "past the week it goes")
	assert.NoFileExists(t, backup)
	assert.FileExists(t, exe, "only the backup is ever removed")

	assert.False(t, PruneBackup(exe, now), "with nothing there it is a no-op")
}

// TestPruneBackupIsSilentAboutEverythingElse: it runs at the top of every
// command, so no state it can meet may become the reason that command failed.
func TestPruneBackupIsSilentAboutEverythingElse(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, PruneBackup("", time.Now()), "no path at all")
	assert.False(t, PruneBackup(filepath.Join(dir, "nothing", "dispat"), time.Now()), "no such directory")

	// A directory where the backup would be is not a backup.
	exe := filepath.Join(dir, "dispat")
	require.NoError(t, os.Mkdir(BackupPath(exe), 0o755))
	assert.False(t, PruneBackup(exe, time.Now().Add(BackupTTL*2)))
	assert.DirExists(t, BackupPath(exe))
}

// TestRollbackRotatesSoItIsReversible: the binary being replaced becomes the
// new backup rather than being thrown away, so a rollback can itself be
// rolled back and no version is lost by pressing the button twice.
func TestRollbackRotatesSoItIsReversible(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.1.0")
	fakeBinary(t, BackupPath(exe), "1.0.0")

	from, to, err := Rollback(context.Background(), exe)
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", from)
	assert.Equal(t, "1.0.0", to)
	assert.Contains(t, string(read(t, exe)), "1.0.0", "the kept binary is in place")
	assert.Contains(t, string(read(t, BackupPath(exe))), "1.1.0", "the one it replaced is the new backup")

	from, to, err = Rollback(context.Background(), exe)
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", from)
	assert.Equal(t, "1.1.0", to)
	assert.Contains(t, string(read(t, exe)), "1.1.0", "a second rollback returns")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "the file parked mid-rotate is not left behind: %v", names(entries))
}

// TestRollbackRestartsTheBackupsClock: the new backup has just become one, so
// its week starts now rather than at whatever mtime it happened to carry.
func TestRollbackRestartsTheBackupsClock(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.1.0")
	fakeBinary(t, BackupPath(exe), "1.0.0")
	old := time.Now().Add(-30 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(exe, old, old))

	_, _, err := Rollback(context.Background(), exe)
	require.NoError(t, err)
	assert.False(t, PruneBackup(exe, time.Now()), "the new backup is new, however old the file was")
	assert.FileExists(t, BackupPath(exe))
}

// TestRollbackWithNothingToRollBackTo: the ordinary state of a binary that
// has never been updated, and an answer the command can act on rather than a
// surprise.
func TestRollbackWithNothingToRollBackTo(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.0.0")

	_, _, err := Rollback(context.Background(), exe)
	assert.ErrorIs(t, err, ErrNoBackup)

	_, err = BackupVersion(context.Background(), exe)
	assert.ErrorIs(t, err, ErrNoBackup)
}

// TestRollbackRefusesABackupThatDoesNotRun: the same rule as the download.
// A corrupt file must not be discovered after it is the only dispat left.
func TestRollbackRefusesABackupThatDoesNotRun(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.1.0")
	brokenBinary(t, BackupPath(exe))

	_, _, err := Rollback(context.Background(), exe)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not run")
	assert.Contains(t, string(read(t, exe)), "1.1.0", "the working binary is untouched")
}

// TestParseVersionOutputReadsTheVersionLine: what --version prints is a logo,
// a blank line and one line that matters. Anything else answers "", which
// every caller treats as "cannot tell" rather than as a match.
func TestParseVersionOutputReadsTheVersionLine(t *testing.T) {
	assert.Equal(t, "1.2.3", parseVersionOutput("\n███\n\ndispat 1.2.3 (darwin_arm64)\n"))
	assert.Equal(t, "1.2.3", parseVersionOutput("dispat 1.2.3 (linux_amd64, go install)"))
	assert.Equal(t, "", parseVersionOutput("dispat"), "no platform, no version")
	assert.Equal(t, "", parseVersionOutput("some other program 1.2.3 (x)"))
	assert.Equal(t, "", parseVersionOutput(""))
}
