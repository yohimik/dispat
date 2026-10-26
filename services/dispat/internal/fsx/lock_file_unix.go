//go:build !windows

package fsx

import (
	"os"
	"syscall"
)

const lockFileOpenFlags = os.O_CREATE | os.O_RDWR | syscall.O_CLOEXEC

// OpenLockFile prevents child commands from retaining an owner's file lock.
// TinyGo does not add close-on-exec to ordinary os.OpenFile calls; setting it
// during open also avoids a race with a concurrent process spawn.
func OpenLockFile(path string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, lockFileOpenFlags, mode)
}
