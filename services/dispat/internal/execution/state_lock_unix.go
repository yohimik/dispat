//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || (solaris && !illumos)

package execution

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryNodeFileLock(file *os.File) (bool, error) {
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EAGAIN):
			return false, nil
		default:
			return false, err
		}
	}
}

func unlockNodeFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
