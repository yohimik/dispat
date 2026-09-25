// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A committed repository can lose its index independently of its worktree.
// Rebuilding one from `git add .` would omit a tracked file that is now
// ignored, so delegated capture must refuse instead of offering a smaller
// source snapshot to a worker.
func TestSnapshotRefusesMissingCommittedIndex(t *testing.T) {
	fixture := newSnapshotFixture(t)
	fixture.write(t, "ignored.txt", "tracked despite ignore\n")
	fixture.run(t, "add", "-f", "ignored.txt")
	fixture.run(t, "commit", "-q", "-m", "keep an ignored file tracked")
	fixture.head = strings.TrimSpace(fixture.run(t, "rev-parse", "HEAD"))
	index, err := fixture.git.IndexPath(t.Context())
	require.NoError(t, err)
	require.NoError(t, os.Remove(index))

	_, err = newSnapshots().capture(t.Context(), fixture.git, fixture.source(), zerolog.Nop())

	require.ErrorContains(t, err, "missing index")
	assert.NoFileExists(t, index, "capture does not rebuild or write the real index")
}
