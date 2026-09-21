//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import "os"

// IsProcessRunning reports whether a process id names a process that is still
// there. On Windows os.FindProcess opens the process and fails when there is
// nothing to open, which is the same question signal zero answers elsewhere.
func IsProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = process.Release()
	return true
}
