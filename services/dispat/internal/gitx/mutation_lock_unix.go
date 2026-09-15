//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || (solaris && !illumos)

package gitx

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryMutationFileLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EINTR):
		return false, nil
	default:
		return false, err
	}
}

func unlockMutationFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
