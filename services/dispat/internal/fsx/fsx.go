// Package fsx holds the CLI's checked file writes. Every writer that rewrites
// a file a user keeps — a config, a changelog — uses an atomic replace so a
// crash mid-write can truncate the temp file but never the file itself.
// pkg/writer carries its own copy on purpose: it is a separate module, and
// exporting a generic filesystem helper from its public API would outlive the
// convenience.
package fsx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path via a same-directory temp file, fsync and
// rename. The temp file lands beside the target so the rename never crosses a
// filesystem, and it is removed on every failure.
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
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
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
