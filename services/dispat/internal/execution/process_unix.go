//go:build unix

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"errors"
	"syscall"
)

// IsProcessRunning reports whether a process id names a process that is still
// there, which is what tells a worker state lock somebody else holds from one
// a crashed worker left behind.
//
// Signal zero is the portable way to ask: it performs the permission checks
// and the existence check and delivers nothing. A process this user may not
// signal answers that it exists, which is the safe answer for a lock: a node
// running under another account is still a node holding the folder.
func IsProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
