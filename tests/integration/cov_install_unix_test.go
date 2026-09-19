//go:build !windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// The destinations that only exist on a unix filesystem. An install is two
// renames, and the first of them would move whatever stands at the destination
// out of the way: a named pipe somebody is reading, a device the kernel owns.
// Naming what stands in the way is the whole point, because "not a regular
// file" tells a reader nothing they can act on.

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInstallRefusesADestinationThatBelongsToSomethingElse: each refusal names
// the kind of thing that is in the way and arrives before any request, so a
// provisioning script pointed at the wrong name is told what it pointed at
// rather than being handed a rename it cannot undo.
func TestInstallRefusesADestinationThatBelongsToSomethingElse(t *testing.T) {
	r := newToolRepo(t)

	t.Run("a named pipe", func(t *testing.T) {
		// A short path of its own: a socket-length limit does not apply to a
		// fifo, but keeping it beside the install folder keeps the fixture
		// readable.
		require.NoError(t, syscall.Mkfifo(r.installed(), 0o600))
		t.Cleanup(func() { _ = os.Remove(r.installed()) })

		before := len(r.requests())
		res := r.install()
		assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout, "is a named pipe")
		assert.Equal(t, before, len(r.requests()), "the refusal costs no request")
	})

	t.Run("a device", func(t *testing.T) {
		if _, err := os.Stat("/dev/null"); err != nil {
			t.Skip("no /dev/null on this machine")
		}
		before := len(r.requests())
		res := r.Command("install", "https://github.com/acme/tool", "--api-url", r.api,
			"--asset", "tool-{os}-{arch}", "--bin-dir", "/dev", "--as", "null", "--check")
		assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout, "is a device")
		assert.Equal(t, before, len(r.requests()), "the refusal costs no request")
		assert.FileExists(t, filepath.Join("/dev", "null"), "and the device is untouched")
	})
}
