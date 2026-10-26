//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const (
	nodeSlotWindowsLockReserved      = 0
	nodeSlotWindowsLockByteCount     = 1
	nodeSlotWindowsLockHighByteCount = 0
	nodeSlotWindowsLockRetryDelay    = 10 * time.Millisecond
)

func lockNodeSlotFile(ctx context.Context, file *os.File) error {
	for {
		var overlapped windows.Overlapped
		err := windows.LockFileEx(windows.Handle(file.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			nodeSlotWindowsLockReserved, nodeSlotWindowsLockByteCount,
			nodeSlotWindowsLockHighByteCount, &overlapped)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(nodeSlotWindowsLockRetryDelay):
		}
	}
}

func unlockNodeSlotFile(file *os.File) {
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), nodeSlotWindowsLockReserved,
		nodeSlotWindowsLockByteCount, nodeSlotWindowsLockHighByteCount, &overlapped)
}

// Windows lacks the directory fsync contract required for distributed slot
// authority, so OpenNodeSlotAuthority refuses this platform. This stub keeps
// the unsupported backend buildable without claiming durable persistence.
func syncNodeSlotDir(string) error { return nil }
