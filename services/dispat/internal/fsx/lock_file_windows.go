//go:build windows

package fsx

import "os"

const lockFileOpenFlags = os.O_CREATE | os.O_RDWR

// OpenLockFile uses the Go runtime's non-inheritable Windows file handle.
func OpenLockFile(path string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, lockFileOpenFlags, mode)
}
