// Package fsx holds the CLI's checked file writes. Every writer that rewrites
// a file a user keeps — a config, a changelog — uses an atomic replace so a
// crash mid-write can truncate the temp file but never the file itself.
// pkg/writer carries its own copy on purpose: it is a separate module, and
// exporting a generic filesystem helper from its public API would outlive the
// convenience.
package fsx

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrAtomicTempRetained means an interrupted atomic write could not remove
// its same-directory temporary file. A snapshot must not capture that file.
var ErrAtomicTempRetained = errors.New("atomic write temporary file retained")

func removeAtomicTemp(path string, writeErr error, remove func(string) error) error {
	if cleanupErr := remove(path); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
		return errors.Join(writeErr, fmt.Errorf("%w at %s: %w", ErrAtomicTempRetained, path, cleanupErr))
	}
	return writeErr
}

// WriteFileAtomic replaces path via a same-directory temp file, fsync and
// rename. The temp file lands beside the target so the rename never crosses a
// filesystem. Failed cleanup is reported so snapshot users can retain exclusion.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace symlink %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	// A supported runtime can return a short count with no error when a file
	// size limit interrupts this write. Never rename that truncated temp file.
	n, err := tmp.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		tmp.Close()
		return removeAtomicTemp(name, err, os.Remove)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return removeAtomicTemp(name, err, os.Remove)
	}
	if err := tmp.Close(); err != nil {
		return removeAtomicTemp(name, err, os.Remove)
	}
	if err := os.Chmod(name, mode); err != nil {
		return removeAtomicTemp(name, err, os.Remove)
	}
	if err := os.Rename(name, path); err != nil {
		return removeAtomicTemp(name, err, os.Remove)
	}
	return nil
}

// WriteFileComplete writes a newly created or temporary file and refuses a
// short write even when a runtime reports it without an error.
func WriteFileComplete(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	n, err := file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
