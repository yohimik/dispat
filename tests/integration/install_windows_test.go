//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// unixDestinationRefusals has no rows on windows: a named pipe, a device and a
// unix socket cannot stand at an install destination there.
func unixDestinationRefusals() []installDestinationRefusal { return nil }
