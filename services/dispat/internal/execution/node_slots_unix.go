//go:build !windows

package execution

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

func lockNodeSlotFile(ctx context.Context, file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(nodeSlotLockRetryMilliseconds * time.Millisecond):
		}
	}
}

func unlockNodeSlotFile(file *os.File) { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }

func syncNodeSlotDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
