//go:build aix || illumos || (!darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows)

package execution

import (
	"errors"
	"os"
)

func tryNodeFileLock(*os.File) (bool, error) {
	return false, errors.New("worker state locking is unsupported on this platform")
}

func unlockNodeFile(*os.File) error { return nil }
