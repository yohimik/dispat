package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
)

// Replace puts incoming where exe is, keeping the outgoing binary as exe's
// backup, and answers where that backup went. A path nothing occupies yet is a
// first install: any existing rollback copy stays in place and is reported.
//
// Two renames rather than a write, because this replaces a file that is
// running. Windows refuses to delete or write a running executable but allows
// it to be renamed out of the way, so moving the old one aside and moving the
// new one in works identically on every platform dispat ships for. Neither
// rename crosses a filesystem, since incoming was created in exe's own
// directory, and the path never changes, so nothing on PATH has to be told.
func Replace(exe, incoming string) (string, error) {
	backup := BackupPath(exe)
	if _, err := os.Lstat(exe); os.IsNotExist(err) {
		exists, checkErr := inspectPreviousBackup(backup)
		if checkErr != nil {
			return "", checkErr
		}
		if !exists {
			backup = ""
		}
		return installFirst(exe, incoming, backup)
	}
	// Keep an older backup until the new binary is installed. Removing it
	// before the swap would erase a working rollback even when the incoming
	// rename fails and the old executable has to be restored.
	prior, err := parkPreviousBackup(backup)
	if err != nil {
		return "", err
	}
	return installReplacement(exe, incoming, prior)
}

// installFirst has one atomic rename because no executable needs to move away.
func installFirst(exe, incoming, backup string) (string, error) {
	if err := os.Rename(incoming, exe); err != nil {
		return "", fmt.Errorf("selfupdate: installing %s: %w", exe, err)
	}
	if backup != "" {
		// The rollback was already present, but its retention period starts
		// when it becomes the installed binary's backup.
		touch(backup)
	}
	return backup, nil
}

// installReplacement restores both recoverable versions if its second rename
// fails. A failed restoration reports exactly where each version remains.
func installReplacement(exe, incoming string, prior previousBackup) (string, error) {
	backup := prior.path
	if err := os.Rename(exe, backup); err != nil {
		return "", prior.restore(fmt.Errorf("selfupdate: moving %s aside: %w", exe, err))
	}
	installErr := os.Rename(incoming, exe)
	if installErr != nil {
		// The window between the two renames is the only moment there is no
		// binary at exe. Put the old one back rather than leave the user with
		// nothing to run.
		if back := os.Rename(backup, exe); back != nil {
			return "", fmt.Errorf("selfupdate: installing %s failed (%w) and restoring %s failed too (%v): "+
				"the current binary is at %s%s", exe, installErr, exe, back, backup, prior.retained())
		}
		return "", prior.restore(fmt.Errorf("selfupdate: installing %s: %w", exe, installErr))
	}
	// The backup's clock starts now. Without this it would carry the mtime
	// the outgoing binary was built or downloaded with, and PruneBackup would
	// read that as an age it does not have.
	touch(backup)
	return backup, prior.discard()
}

// previousBackup holds the rollback binary only until a replacement commits.
// Its private directory leaves no existing destination for Windows' rename and
// prevents a rename from overwriting an unrelated file on Unix.
type previousBackup struct {
	path   string
	parked string
	dir    string
}

func parkPreviousBackup(path string) (previousBackup, error) {
	b := previousBackup{path: path}
	exists, err := inspectPreviousBackup(path)
	if err != nil || !exists {
		return b, err
	}
	b.dir, err = os.MkdirTemp(filepath.Dir(path), "dispat-previous-backup-*")
	if err != nil {
		return b, fmt.Errorf("selfupdate: parking the previous backup %s: %w", path, err)
	}
	b.parked = filepath.Join(b.dir, filepath.Base(path))
	if err := os.Rename(path, b.parked); err != nil {
		_ = os.Remove(b.dir)
		return previousBackup{}, fmt.Errorf("selfupdate: parking the previous backup %s: %w", path, err)
	}
	return b, nil
}

func inspectPreviousBackup(path string) (bool, error) {
	prior, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("selfupdate: checking the previous backup %s: %w", path, err)
	}
	if !prior.Mode().IsRegular() {
		return false, fmt.Errorf("selfupdate: previous backup %s is not a regular file", path)
	}
	return true, nil
}

func (b previousBackup) restore(cause error) error {
	if b.parked == "" {
		return cause
	}
	if _, err := os.Lstat(b.path); err == nil {
		return fmt.Errorf("%w; previous backup remains at %s because %s is occupied", cause, b.parked, b.path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("%w; checking %s failed (%v), previous backup remains at %s", cause, b.path, err, b.parked)
	}
	if err := os.Rename(b.parked, b.path); err != nil {
		return fmt.Errorf("%w; restoring previous backup %s failed (%v), previous backup remains at %s", cause, b.path, err, b.parked)
	}
	if err := os.Remove(b.dir); err != nil {
		return fmt.Errorf("%w; previous backup restored, but empty staging directory %s remains: %v", cause, b.dir, err)
	}
	return cause
}

func (b previousBackup) retained() string {
	if b.parked == "" {
		return ""
	}
	return " and previous backup at " + b.parked
}

func (b previousBackup) discard() error {
	if b.parked == "" {
		return nil
	}
	if err := os.Remove(b.parked); err != nil {
		return fmt.Errorf("%w: old backup remains at %s: %v", ErrPreviousBackupCleanup, b.parked, err)
	}
	if err := os.Remove(b.dir); err != nil {
		return fmt.Errorf("%w: empty staging directory %s remains: %v", ErrPreviousBackupCleanup, b.dir, err)
	}
	return nil
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
