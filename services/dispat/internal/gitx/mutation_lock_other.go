//go:build aix || illumos || (!darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows)

package gitx

import (
	"errors"
	"os"
)

func tryMutationFileLock(*os.File) (bool, error) {
	return false, errors.New("Git mutation locking is unsupported on this platform")
}

func unlockMutationFile(*os.File) error { return nil }
