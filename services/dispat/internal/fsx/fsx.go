// Package fsx holds the one filesystem primitive the CLI shares: an atomic
// file replace. Every writer that rewrites a file a user keeps — a config, a
// changelog — goes through it, so a crash mid-write can truncate the temp file
// but never the file itself. pkg/writer carries its own copy on purpose: it is
// a separate module, and exporting a generic filesystem helper from its public
// API would outlive the convenience.
package fsx

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path via a same-directory temp file, fsync and
// rename. The temp file lands beside the target so the rename never crosses a
// filesystem, and it is removed on every failure.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
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
