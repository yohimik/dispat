// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

//go:build windows

package execution

import (
	"os"

	"golang.org/x/sys/windows"
)

func isSameFileSystem(first, second string) (bool, error) {
	a, err := readVolumeSerial(first)
	if err != nil {
		return false, err
	}
	b, err := readVolumeSerial(second)
	if err != nil {
		return false, err
	}
	return a == b, nil
}

func readVolumeSerial(path string) (uint32, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return 0, err
	}
	return info.VolumeSerialNumber, nil
}
