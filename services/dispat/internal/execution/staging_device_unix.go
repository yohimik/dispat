// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

//go:build !windows

package execution

import (
	"fmt"
	"os"
	"syscall"
)

func isSameFileSystem(first, second string) (bool, error) {
	a, err := os.Stat(first)
	if err != nil {
		return false, err
	}
	b, err := os.Stat(second)
	if err != nil {
		return false, err
	}
	aStat, aOK := a.Sys().(*syscall.Stat_t)
	bStat, bOK := b.Sys().(*syscall.Stat_t)
	if !aOK || !bOK {
		return false, fmt.Errorf("filesystem identity is unavailable for %s and %s", first, second)
	}
	return aStat.Dev == bStat.Dev, nil
}
