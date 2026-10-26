// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestPinnedSweepSinceKeepsOriginalCommitAfterTagMoves(t *testing.T) {
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.name", "Test")
	fixtureGit(t, root, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte(`{}`), 0o644))
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-qm", "fix(core): first")
	first := recordGit(t, root, "rev-parse", "HEAD")
	fixtureGit(t, root, "tag", "baseline")
	fixtureGit(t, root, "commit", "-qm", "feat(core): later", "--allow-empty")
	a := &App{root: root, git: &gitx.LocalGitx{Dir: root, Log: zerolog.Nop()}}
	anchor, err := a.pinSweepSince(t.Context(), "baseline")
	require.NoError(t, err)
	require.Equal(t, first, anchor.OID)
	fixtureGit(t, root, "tag", "-f", "baseline")
	require.Equal(t, first, anchor.OID)
	require.NotEqual(t, first, recordGit(t, root, "rev-parse", "baseline"))
	all, err := a.pinSweepSince(t.Context(), SinceAll)
	require.NoError(t, err)
	require.Nil(t, all)
}
