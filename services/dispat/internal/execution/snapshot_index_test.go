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
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// An unborn repository has never written an index. Its first delegated build
// still needs the working files, and capture must not create the real index.
func TestSnapshotCapturesAnUnbornRepositoryWithoutAnIndex(t *testing.T) {
	dir := t.TempDir()
	fixture := &snapshotFixture{dir: dir, git: &gitx.LocalGitx{Dir: dir, Log: zerolog.Nop()}}
	fixture.run(t, "init", "-q")
	fixture.write(t, "first.txt", "first\n")
	index, err := fixture.git.IndexPath(t.Context())
	require.NoError(t, err)
	require.NoFileExists(t, index)

	commit, err := newSnapshots().capture(t.Context(), fixture.git,
		Source{Path: ".", Dir: dir}, zerolog.Nop())

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"first.txt": "first"}, fixture.listing(t, commit))
	assert.NoFileExists(t, index, "capture does not create the repository's own index")
}

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
