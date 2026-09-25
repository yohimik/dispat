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

// A crash between the steps of a replacement leaves the previous backup parked
// in a staging directory and a download staged beside the binary. These tests
// stage exactly those leftovers and check what the next update, rollback or
// restore makes of them.

// parkedLeftover stages what a replacement killed after parking the previous
// backup leaves: a staging directory holding that backup, aged as asked.
func parkedLeftover(t *testing.T, exe, body string, age time.Duration) string {
	t.Helper()
	staging, err := os.MkdirTemp(filepath.Dir(exe), parkedBackupPrefix+"*")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(staging, filepath.Base(BackupPath(exe))), []byte(body), 0o755))
	aged(t, staging, age)
	return staging
}

// aged sets a file's or directory's times to age ago.
func aged(t *testing.T, path string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(path, when, when))
}

// TestRestoreRecoversABackupACrashLeftParked: with the backup's place free, a
// parked copy is the only rollback there is, so it is put back before the
// restore looks for one instead of the restore reporting that none exists.
func TestRestoreRecoversABackupACrashLeftParked(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(exe, []byte("current"), 0o755))
	staging := parkedLeftover(t, exe, "previous", 2*abandonedAfter)

	require.NoError(t, Restore(exe))
	assert.Equal(t, "previous", string(read(t, exe)))
	assert.Equal(t, "current", string(read(t, BackupPath(exe))))
	assert.NoDirExists(t, staging, "the staging directory goes with its copy")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "only the tool and its backup remain: %v", names(entries))
}

// TestRollbackRecoversABackupACrashLeftParked: the same recovery reaches a
// rollback before it runs the backup, which is the first thing it does.
func TestRollbackRecoversABackupACrashLeftParked(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.1.0")
	staging := parkedLeftover(t, exe, "", 2*abandonedAfter)
	fakeBinary(t, filepath.Join(staging, filepath.Base(BackupPath(exe))), "1.0.0")
	aged(t, staging, 2*abandonedAfter)

	from, to, err := Rollback(context.Background(), exe)
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", from)
	assert.Equal(t, "1.0.0", to)
	assert.NoDirExists(t, staging)
}

// TestReplaceRecoversABackupACrashLeftParked: a first install onto a path a
// crash emptied reports the recovered copy as its rollback, and a replacement
// supersedes it with the binary it replaces, leaving no staging directory.
func TestReplaceRecoversABackupACrashLeftParked(t *testing.T) {
	for _, tc := range []struct {
		name           string
		currentPresent bool
		wantBackup     string
	}{
		{name: "current present", currentPresent: true, wantBackup: "current"},
		{name: "current absent", wantBackup: "parked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "tool")
			if tc.currentPresent {
				require.NoError(t, os.WriteFile(exe, []byte("current"), 0o755))
			}
			parkedLeftover(t, exe, "parked", 2*abandonedAfter)
			incoming := filepath.Join(dir, "incoming")
			require.NoError(t, os.WriteFile(incoming, []byte("incoming"), 0o755))

			backup, err := Replace(exe, incoming)
			require.NoError(t, err)
			assert.Equal(t, BackupPath(exe), backup)
			assert.Equal(t, "incoming", string(read(t, exe)))
			assert.Equal(t, tc.wantBackup, string(read(t, backup)))
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Len(t, entries, 2, "only the binary and its backup remain: %v", names(entries))
		})
	}
}

// TestRecoveryDropsAParkedCopyANewerBackupSupersedes: a backup already in its
// place is newer than anything parked before it, so the parked copy is the
// one that goes.
func TestRecoveryDropsAParkedCopyANewerBackupSupersedes(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(exe, []byte("current"), 0o755))
	require.NoError(t, os.WriteFile(BackupPath(exe), []byte("newer"), 0o755))
	staging := parkedLeftover(t, exe, "older", 2*abandonedAfter)

	recoverParkedBackups(BackupPath(exe), time.Now())
	assert.Equal(t, "newer", string(read(t, BackupPath(exe))))
	assert.NoDirExists(t, staging)
}

// TestRecoveryLeavesWhatIsNotItsOwn: an update in flight, another binary's
// staging directory in the same folder, a copy whose place something else
// blocks and a staging directory holding more than one file are all left
// exactly as they are.
func TestRecoveryLeavesWhatIsNotItsOwn(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(exe, []byte("current"), 0o755))

	fresh := parkedLeftover(t, exe, "in flight", time.Minute)
	other := parkedLeftover(t, filepath.Join(dir, "other"), "other's", 2*abandonedAfter)
	crowded := parkedLeftover(t, exe, "parked", 0)
	require.NoError(t, os.WriteFile(filepath.Join(crowded, "unexpected"), nil, 0o644))
	aged(t, crowded, 2*abandonedAfter)

	recoverParkedBackups(BackupPath(exe), time.Now())
	assert.NoFileExists(t, BackupPath(exe), "nothing was recovered from any of them")
	for _, staging := range []string{fresh, other, crowded} {
		assert.DirExists(t, staging)
	}

	// A folder standing in the backup's place keeps an abandoned copy parked:
	// it is the only rollback there is until that place is cleared.
	require.NoError(t, os.RemoveAll(fresh))
	require.NoError(t, os.RemoveAll(crowded))
	blocked := parkedLeftover(t, exe, "parked", 2*abandonedAfter)
	require.NoError(t, os.Mkdir(BackupPath(exe), 0o755))
	recoverParkedBackups(BackupPath(exe), time.Now())
	assert.Equal(t, "parked", string(read(t, filepath.Join(blocked, filepath.Base(BackupPath(exe))))))
}

// TestRecoveryRemovesAnAbandonedStagingDirectory: a crash after the parked
// copy was discarded and before its directory was leaves an empty directory,
// which is removed once it is old enough.
func TestRecoveryRemovesAnAbandonedStagingDirectory(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tool")
	staging, err := os.MkdirTemp(dir, parkedBackupPrefix+"*")
	require.NoError(t, err)
	aged(t, staging, 2*abandonedAfter)

	recoverParkedBackups(BackupPath(exe), time.Now())
	assert.NoDirExists(t, staging)
}

// TestRecoveryRemovesAnAbandonedDownload: a download staged beside the binary
// by an update that never finished is removed once it is older than any live
// download could be, and one still being written is left alone.
func TestRecoveryRemovesAnAbandonedDownload(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat.exe")
	stale := filepath.Join(dir, stagedDownloadPrefix+"123.exe")
	require.NoError(t, os.WriteFile(stale, []byte("15 MiB"), 0o644))
	aged(t, stale, 2*abandonedAfter)
	live := filepath.Join(dir, stagedDownloadPrefix+"456.exe")
	require.NoError(t, os.WriteFile(live, []byte("half"), 0o644))
	folder := filepath.Join(dir, stagedDownloadPrefix+"folder")
	require.NoError(t, os.Mkdir(folder, 0o755))
	aged(t, folder, 2*abandonedAfter)

	recoverParkedBackups(BackupPath(exe), time.Now())
	assert.NoFileExists(t, stale)
	assert.FileExists(t, live, "a download in flight is not a leftover")
	assert.DirExists(t, folder, "a folder is nobody's download")

	// A folder that cannot be listed is left to a later run.
	recoverParkedBackups(filepath.Join(dir, "absent", "tool.backup"), time.Now())
}

// TestSlotKindNamesWhatIsInTheWay: "not a regular file" tells a reader
// nothing they can act on, so each shape a backup's place can hold is named.
func TestSlotKindNamesWhatIsInTheWay(t *testing.T) {
	for want, mode := range map[string]os.FileMode{
		"folder":        os.ModeDir,
		"symbolic link": os.ModeSymlink,
		"device":        os.ModeDevice,
		"socket":        os.ModeSocket,
		"named pipe":    os.ModeNamedPipe,
		"special file":  os.ModeIrregular,
	} {
		assert.Equal(t, want, slotKind(mode))
	}
}
