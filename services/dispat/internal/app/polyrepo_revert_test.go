// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// nestedRevertFixture is two repositories one inside the other, which is the
// shape of every composed fleet: an orchestrated source sits in the control
// checkout, a choreographed peer in the checkout that links it.
func nestedRevertFixture(t *testing.T, control bool) (*workspaceRecorder, string, string) {
	t.Helper()
	enabled := true
	outerCfg := &config.File{Commit: &config.CommitConfig{Enabled: &enabled}, Run: &config.RunConfig{}, UnsafeDisableLock: true}
	innerCfg := &config.File{Commit: &config.CommitConfig{Enabled: &enabled}, Run: &config.RunConfig{}, UnsafeDisableLock: true}
	outer, a := guardRepo(t, outerCfg)
	inner := filepath.Join(outer, ".links", "sdk")
	require.NoError(t, os.MkdirAll(filepath.Join(inner, "pkg"), 0o755))
	recordGit(t, inner, "init", "-q")
	recordGit(t, inner, "config", "user.email", "test@example.com")
	recordGit(t, inner, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(inner, "pkg", "input"), []byte("recorded"), 0o644))
	recordGit(t, inner, "add", ".")
	recordGit(t, inner, "commit", "-qm", "feat(lib): package")

	// "api" sorts before "sdk", so a name-ordered search finds the container
	// first: the deepest root is what decides, not the order.
	a.log = zerolog.Nop()
	a.workspace = &config.Workspace{ControlRoot: outer, Repositories: []config.Repository{
		{Name: "api", Root: outer, Config: outerCfg, Commit: outerCfg.Commit, Control: control, Entry: true},
		{Name: "sdk", Root: inner, GitlinkPath: ".links/sdk", Config: innerCfg, Commit: innerCfg.Commit,
			Imported: !control, Linker: "api"},
	}}
	if !control {
		a.workspace.Saga = config.SagaChoreography
	}
	return a.newWorkspaceRecorder(), outer, inner
}

// TestRevertDirRestoresThroughTheDeepestOwner: the regression this fixes is a
// revert running in the repository that merely contains the folder — which
// restores nothing — and, for a fleet with no control repository, a nil
// dereference when no root matched at all.
func TestRevertDirRestoresThroughTheDeepestOwner(t *testing.T) {
	for _, saga := range []struct {
		name    string
		control bool
	}{{"choreographed peers", false}, {"an orchestrated source", true}} {
		t.Run(saga.name, func(t *testing.T) {
			w, outer, inner := nestedRevertFixture(t, saga.control)
			file := filepath.Join(inner, "pkg", "input")
			require.NoError(t, os.WriteFile(file, []byte("half-written release"), 0o644))

			require.NoError(t, w.RevertDir(t.Context(), filepath.Join(inner, "pkg")))
			restored, err := os.ReadFile(file)
			require.NoError(t, err)
			assert.Equal(t, "recorded", string(restored), "the owning repository restored its own file")

			assert.Equal(t, "sdk", w.owner(filepath.Join(inner, "pkg")).repo.Name)
			assert.Equal(t, "api", w.owner(filepath.Join(outer, "pkg")).repo.Name,
				"a folder outside the nested checkout still belongs to the repository around it")
			assert.Nil(t, w.owner(filepath.Join(t.TempDir(), "elsewhere")))
		})
	}
}

// TestRevertDirRefusesAFolderNoRepositoryOwns: the fallback used to be the
// control repository, which a choreographed fleet does not have.
func TestRevertDirRefusesAFolderNoRepositoryOwns(t *testing.T) {
	w, _, _ := nestedRevertFixture(t, false)
	err := w.RevertDir(t.Context(), filepath.Join(t.TempDir(), "elsewhere"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "no participating repository owns")
}
