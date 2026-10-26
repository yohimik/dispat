//go:build linux || darwin

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	workerRestartTaskDirNode       = "build-a"
	workerRestartTaskDirRun        = "unresolved-run"
	workerRestartTaskDirTask       = "unresolved-build"
	workerRestartTaskDirMarker     = "retained-output"
	workerRestartTaskDirMarkerText = "a child may still write here"
	workerRestartTaskDirCapacity   = 1
	workerRestartTaskDirAttempt    = 1
	workerRestartTaskDirMode       = 0o700
	workerRestartTaskDirFileMode   = 0o600
	workerRestartTaskDirIdle       = 20 * time.Millisecond
	workerRestartTaskDirBound      = 2 * time.Second
)

func TestWorkerRestartKeepsUnsettledTaskCheckout(t *testing.T) {
	for _, test := range []struct {
		name string
		hold bool
	}{
		{name: "held physical lease", hold: true},
		{name: "no held lease", hold: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorityRoot := t.TempDir()
			require.NoError(t, os.Chmod(authorityRoot, workerRestartTaskDirMode))
			authority, err := OpenNodeSlotAuthority(authorityRoot, workerRestartTaskDirNode,
				workerRestartTaskDirCapacity)
			require.NoError(t, err)
			if test.hold {
				_, acquired, err := authority.TryReserve(t.Context(), NodeSlotIntent{
					Run: workerRestartTaskDirRun, Task: workerRestartTaskDirTask,
					Attempt: workerRestartTaskDirAttempt, Weight: workerRestartTaskDirCapacity})
				require.NoError(t, err)
				require.True(t, acquired)
			}
			worker, mailbox := newWorkerFixture(t, "", ChainTip{}, nil)
			mailbox.heads = nil
			worker.NodeSlots = authority
			worker.Report.Capacity = workerRestartTaskDirCapacity
			worker.IdleTimeout = workerRestartTaskDirIdle
			marker := filepath.Join(worker.StateDir, taskWorkDir,
				workerRestartTaskDirRun, workerRestartTaskDirMarker)
			require.NoError(t, os.MkdirAll(filepath.Dir(marker), workerRestartTaskDirMode))
			require.NoError(t, os.WriteFile(marker, []byte(workerRestartTaskDirMarkerText),
				workerRestartTaskDirFileMode))
			ctx, cancel := context.WithTimeout(context.Background(), workerRestartTaskDirBound)
			defer cancel()
			worker.Serve(ctx)
			if test.hold {
				require.FileExists(t, marker,
					"a restarted worker must not delete a checkout with unresolved physical capacity")
				held, err := authority.Held(t.Context())
				require.NoError(t, err)
				require.Len(t, held, workerRestartTaskDirCapacity)
			} else {
				require.NoFileExists(t, marker,
					"settled prior task folders can be removed at restart")
			}
		})
	}
}
