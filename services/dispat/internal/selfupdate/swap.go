package selfupdate

import (
	"fmt"
	"os"
	"time"
)

// Replace puts incoming where exe is, keeping the outgoing binary as exe's
// backup, and answers where that backup went.
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
	now := time.Now()
	_ = os.Chtimes(backup, now, now)
	return backup, nil
}
