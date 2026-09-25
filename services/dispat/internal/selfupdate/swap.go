package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
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
//
// Whatever an earlier update left parked by a crash is put back or cleared
// first (see recoverParkedBackups), so the backup it finds is the one a
// completed update would have left.
func Replace(exe, incoming string) (string, error) {
	backup := BackupPath(exe)
	recoverParkedBackups(backup, time.Now())
	if _, err := os.Lstat(exe); os.IsNotExist(err) {
		// A first install writes nothing at the backup's path, so what stands
		// there decides only whether a rollback copy is reported, never
		// whether the install may happen.
		if present, err := isPreviousBackupPresent(backup); err != nil || !present {
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
	present, err := isPreviousBackupPresent(path)
	if err != nil {
		return b, fmt.Errorf("selfupdate: %w", err)
	}
	if !present {
		return b, nil
	}
	b.dir, err = os.MkdirTemp(filepath.Dir(path), parkedBackupPrefix+"*")
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

// isPreviousBackupPresent reports whether a rollback copy stands at path. A
// path holding anything but a regular file is an error naming what is in the
// way and the remedy, because an update would otherwise have to move somebody's
// folder, link or device aside to keep its own backup there.
func isPreviousBackupPresent(path string) (bool, error) {
	prior, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking the previous backup %s: %w", path, err)
	}
	if !prior.Mode().IsRegular() {
		return false, blockedBackup(path, slotKind(prior.Mode()))
	}
	return true, nil
}

// CheckBackupSlot reports what would stop a replacement of exe from keeping
// the outgoing binary as its backup, before anything is downloaded. A path
// nothing occupies yet is a first install, which keeps no backup, so only a
// replacement is ever refused.
func CheckBackupSlot(exe string) error {
	if _, err := os.Lstat(exe); os.IsNotExist(err) {
		return nil
	}
	_, err := isPreviousBackupPresent(BackupPath(exe))
	return err
}

// CheckRestorableBackup reports whether exe's backup is a binary a restore may
// put in exe's place: ErrNoBackup when there is none, and the remedy when
// something else stands there. A symbolic link to a regular file is accepted,
// because an install that replaced a link on PATH keeps that link as its
// backup, and restoring it puts back exactly what was there.
func CheckRestorableBackup(exe string) error {
	backup := BackupPath(exe)
	info, err := os.Lstat(backup)
	if err != nil {
		return fmt.Errorf("%w at %s", ErrNoBackup, backup)
	}
	if info.Mode().IsRegular() {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if resolved, err := os.Stat(backup); err == nil && resolved.Mode().IsRegular() {
			return nil
		}
		return fmt.Errorf("selfupdate: %w", blockedBackup(backup, "link to something that is not a file"))
	}
	return fmt.Errorf("selfupdate: %w", blockedBackup(backup, slotKind(info.Mode())))
}

// blockedBackup names what stands where a backup belongs and what to do
// about it.
func blockedBackup(path, kind string) error {
	return fmt.Errorf("%s is a %s where the previous binary is kept; move or remove it, then re-run", path, kind)
}

// slotKind names what occupies a backup's place, because "not a regular
// file" tells a reader nothing they can act on.
func slotKind(mode os.FileMode) string {
	switch {
	case mode.IsDir():
		return "folder"
	case mode&os.ModeSymlink != 0:
		return "symbolic link"
	case mode&os.ModeDevice != 0:
		return "device"
	case mode&os.ModeSocket != 0:
		return "socket"
	case mode&os.ModeNamedPipe != 0:
		return "named pipe"
	}
	return "special file"
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
// runs either file, because the two callers ask different questions of it:
// dispat restoring dispat runs the backup first, and a restore of some other
// tool has nothing it could run it against.
//
// A backup a crash left parked is put back first, and only a backup that
// CheckRestorableBackup accepts is rotated in: a folder standing at the
// backup's path is somebody's, and moving it into exe's place would put it on
// PATH.
func Restore(exe string) (err error) {
	backup := BackupPath(exe)
	recoverParkedBackups(backup, time.Now())
	if err := CheckRestorableBackup(exe); err != nil {
		return err
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
