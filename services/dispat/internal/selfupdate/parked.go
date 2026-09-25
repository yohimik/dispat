package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// What a crashed update leaves behind, and how the next one clears it.
//
// A replacement parks the previous backup in a private staging directory
// beside the binary until the new binary is in place, and a download is staged
// beside the binary until it is renamed into place. A process killed between
// those steps leaves either one behind, where no later run would find it: the
// rollback copy sits out of the backup's place, so a rollback reports that
// there is none, and a staged download holds about 15 MiB until somebody
// notices. Every replacement, rollback and restore therefore starts by putting
// the first back and removing the second.

const (
	// parkedBackupPrefix names the staging directory a replacement parks the
	// previous backup in.
	parkedBackupPrefix = "dispat-previous-backup-"
	// stagedDownloadPrefix names a download staged beside the binary it is
	// destined for; see tempPattern.
	stagedDownloadPrefix = "dispat-download-"
	// abandonedAfter is how old a staging directory or a staged download is
	// before it counts as a crash's leftover rather than work in flight. It is
	// longer than the longest a download may take and the smoke test after it,
	// so a concurrent update is never cleared from under itself.
	abandonedAfter = time.Hour
)

// recoverParkedBackups puts back what a crashed update left beside backup:
// a parked rollback copy returns to the backup's place when that place is
// free and is dropped when a newer backup already holds it, and a staged
// download is removed. Only leftovers older than abandonedAfter are touched.
//
// It is best-effort by design. Housekeeping must never be the reason an
// update or a rollback fails, so every leftover it cannot handle stays where
// it is for a later run, and nothing is reported.
func recoverParkedBackups(backup string, now time.Time) {
	dir := filepath.Dir(backup)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		switch {
		case strings.HasPrefix(entry.Name(), parkedBackupPrefix):
			recoverParkedBackup(path, backup, now)
		case strings.HasPrefix(entry.Name(), stagedDownloadPrefix):
			removeAbandonedDownload(path, now)
		}
	}
}

// recoverParkedBackup handles one staging directory. It acts only on a
// directory holding nothing or exactly this backup's regular file: a staging
// directory of another binary sharing the folder belongs to that binary's
// next update.
func recoverParkedBackup(staging, backup string, now time.Time) {
	if info, err := os.Lstat(staging); err != nil || !info.IsDir() || !isAbandoned(info, now) {
		return
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) > 1 {
		return
	}
	if len(entries) == 1 {
		entry := entries[0]
		if entry.Name() != filepath.Base(backup) || !entry.Type().IsRegular() {
			return
		}
		if !settleParkedBackup(filepath.Join(staging, entry.Name()), backup) {
			return
		}
	}
	_ = os.Remove(staging)
}

// settleParkedBackup moves a parked copy back into a free backup place, or
// removes it when a newer backup already holds that place, and answers
// whether the copy is gone from the staging directory. Anything else standing
// in the backup's place leaves the copy parked: it is the only rollback there
// is until that place is cleared.
func settleParkedBackup(parked, backup string) bool {
	slot, err := os.Lstat(backup)
	switch {
	case os.IsNotExist(err):
		return os.Rename(parked, backup) == nil
	case err == nil && slot.Mode().IsRegular():
		return os.Remove(parked) == nil
	}
	return false
}

// removeAbandonedDownload removes a staged download a crashed update left.
func removeAbandonedDownload(path string, now time.Time) {
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && isAbandoned(info, now) {
		_ = os.Remove(path)
	}
}

// isAbandoned reports whether a leftover's last change is older than
// abandonedAfter. A staged download changes with every write and a staging
// directory with every rename into it, so the age is measured from the last
// step a live update took there.
func isAbandoned(info os.FileInfo, now time.Time) bool {
	return now.Sub(info.ModTime()) >= abandonedAfter
}
