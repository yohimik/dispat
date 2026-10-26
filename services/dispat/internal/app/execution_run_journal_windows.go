//go:build windows

package app

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockRunFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		firstRunJournalWindowsLockReservedValue, firstRunJournalWindowsLockBytes, firstRunJournalWindowsLockHighBytes, &overlapped)
}

func syncRunDirectory(path string) error {
	_ = path
	return errors.New(firstRunJournalWindowsDurabilityUnavailableErrorText)
}
