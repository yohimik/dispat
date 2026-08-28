package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Replace puts incoming where exe is, keeping the outgoing binary as exe's
// backup, and answers where that backup went. A path nothing occupies yet is a
// first install: there is nothing to keep, and the answer is empty.
//
// Two renames rather than a write, because this replaces a file that is
// running. Windows refuses to delete or write a running executable but allows
// it to be renamed out of the way, so moving the old one aside and moving the
// new one in works identically on every platform dispat ships for. Neither
// rename crosses a filesystem, since incoming was created in exe's own
// directory, and the path never changes, so nothing on PATH has to be told.
func Replace(exe, incoming string) (backup string, err error) {
	backup = BackupPath(exe)
	// A backup from an earlier update is not history worth keeping, and
	// Windows will not rename onto a file that exists.
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("selfupdate: removing the previous backup %s: %w", backup, err)
	}
	if _, err := os.Lstat(exe); os.IsNotExist(err) {
		// Nothing to step aside. The single rename below is then the whole
		// install, and it is atomic, so the path either holds the previous
		// nothing or the finished file and never a half-written one.
		if err := os.Rename(incoming, exe); err != nil {
			return "", fmt.Errorf("selfupdate: installing %s: %w", exe, err)
		}
		return "", nil
	}
	if err := os.Rename(exe, backup); err != nil {
		return "", fmt.Errorf("selfupdate: moving %s aside: %w", exe, err)
	}
	if err := os.Rename(incoming, exe); err != nil {
		// The window between the two renames is the only moment there is no
		// binary at exe. Put the old one back rather than leave the user with
		// nothing to run.
		if back := os.Rename(backup, exe); back != nil {
			return "", fmt.Errorf("selfupdate: installing %s failed (%w) and restoring %s failed too (%v): "+
				"the previous binary is at %s", exe, err, exe, back, backup)
		}
		return "", fmt.Errorf("selfupdate: installing %s: %w", exe, err)
	}
	// The backup's clock starts now. Without this it would carry the mtime
	// the outgoing binary was built or downloaded with, and PruneBackup would
	// read that as an age it does not have.
	touch(backup)
	return backup, nil
}

// Restore rotates a binary and its backup: what is installed becomes the
// backup and the backup becomes what is installed.
//
// It rotates rather than moves so that a restore is itself reversible and no
// version is ever lost, which is what lets a second one return. Nothing here
// asks what either file is, because the two callers ask different questions of
// it: dispat restoring dispat runs the backup first, and a restore of some
// other tool has nothing it could run it against.
func Restore(exe string) (err error) {
	backup := BackupPath(exe)
	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("%w at %s", ErrNoBackup, backup)
	}
	dir := filepath.Dir(exe)
	parked, err := os.CreateTemp(dir, tempPattern(exe, "rollback"))
	if err != nil {
		return fmt.Errorf("selfupdate: %s: %w (%v); re-run with the rights to replace %s",
			dir, ErrNotWritable, err, exe)
	}
	parkedName := parked.Name()
	parked.Close()
	// CreateTemp made the file so the name is ours; the rename needs it gone
	// on the platforms that will not rename onto an existing file.
	if err := os.Remove(parkedName); err != nil {
		return fmt.Errorf("selfupdate: %s: %w", parkedName, err)
	}

	if err := os.Rename(exe, parkedName); err != nil {
		return fmt.Errorf("selfupdate: moving %s aside: %w", exe, err)
	}
	if err := os.Rename(backup, exe); err != nil {
		if back := os.Rename(parkedName, exe); back != nil {
			return fmt.Errorf("selfupdate: restoring %s failed (%w) and putting the current binary "+
				"back failed too (%v): it is at %s", exe, err, back, parkedName)
		}
		return fmt.Errorf("selfupdate: restoring %s: %w", exe, err)
	}
	// The third leg of the rotate, and a plain rename rather than Replace:
	// Replace puts a file in exe's place, which is what the leg above already
	// did, and running it here would swap the two straight back.
	if err := os.Rename(parkedName, backup); err != nil {
		// exe is the restored binary either way, which is what was asked for;
		// only the new backup is missing. The sentinel is what lets the caller
		// report a restore that did happen rather than a failure.
		return fmt.Errorf("selfupdate: restored %s, but %w: %v", exe, ErrBackupNotKept, err)
	}
	touch(backup)
	return nil
}

// touch starts the backup's clock, so PruneBackup measures the age of the
// backup rather than the age of the binary that became one. A clock that
// cannot be set is not worth failing a completed install over: the copy is
// simply pruned at whatever date it carries.
func touch(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}
