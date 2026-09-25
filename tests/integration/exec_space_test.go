// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// D2: a space's effective configuration is the root file's space entry with
// the space folder's own config file merged over it, and `dispat run` has
// always resolved a script that way. `dispat exec --for space:<name>` read the
// root file's entry alone, so a script or an env value written only in the
// space folder's file was invisible to the one command whose whole purpose is
// running a declared script by hand. These scenarios pin the two commands to
// the same effective set, and pin `--in space:<name>` to the folder semantics
// the reference documents, which the space file cannot move.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// execSpaceRepo is the fixture: one name declared only in the space folder's
// file, one name declared in both the root file's space entry and that file so
// the nearer layer is visible, and an env value declared at all three levels.
func execSpaceRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"which":  {"echo level=root"},
		"shared": {"echo shared MSG=$MSG FILE_ONLY=${FILE_ONLY-unset}"},
	}
	cfg.Env = map[string]string{"MSG": "from-root"}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {
			Path:    models.PathList{"packages"},
			Scripts: map[string]models.Script{"which": {"echo level=space-entry"}},
			Env:     map[string]string{"MSG": "from-space-entry"},
		},
	}
	r.WriteConfigModel(cfg)
	// The space folder speaks for itself: one script nobody else declares, one
	// that overrides the entry's, and the env the entry also wrote.
	spaceFile(t, r, "packages", models.SpaceFile{
		Scripts: map[string]models.Script{
			"vet":   {"echo vet=space-file"},
			"which": {"echo level=space-file"},
		},
		Env: map[string]string{"MSG": "from-space-file", "FILE_ONLY": "yes"},
	})
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "api")
	r.Commit("feat(core): first release")
	return r
}

// TestExecForSpaceReadsTheSpaceFoldersConfig: `dispat run` and `dispat exec
// --for space:` resolve one effective script set, so a script written in the
// space folder's own file is reachable by hand, the nearer layer still wins
// over the root file's space entry, and the environment moves with the text.
func TestExecForSpaceReadsTheSpaceFoldersConfig(t *testing.T) {
	r := execSpaceRepo(t)

	// The baseline: `dispat run` resolves the space folder's own script for
	// every package of the space, which is what makes the miss below a bug
	// rather than a rule.
	res := r.RunScriptOK("vet", "-p", "*")
	assert.Contains(t, res.Stdout, "vet=space-file")

	// The same name, asked for by hand at the level that declares it.
	res = r.Command("exec", "vet", "--for", "space:libs")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "vet=space-file")

	// Two layers declare `which`, and the space folder's file is the nearer
	// one, exactly as it is for a package under `dispat run`.
	res = r.Command("exec", "which", "--for", "space:libs")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=space-file")

	// An inferred subject reads the same set: standing in the space folder is
	// naming it.
	res = r.CommandAt("packages", "exec", "which", "--for", "cwd")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=space-file")

	// The subject carries its environment, and the space's environment is the
	// merged one too: the file's value wins over the entry's, and a name only
	// the file declares arrives at all.
	res = r.Command("exec", "shared", "--for", "space:libs", "--fallback")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-space-file")
	assert.Contains(t, res.Stdout, "FILE_ONLY=yes")

	// Widening the set does not weaken the exact mode: a name no layer of the
	// space declares is still a reported miss naming the level that was read.
	res = r.Command("exec", "ghost", "--for", "space:libs")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "no script")
	assert.Contains(t, res.Stdout, "libs", "the message names the level that was read")
}

// TestExecInSpaceIsStillTheSpacesPrimaryFolder: `--in space:<name>` keeps the
// documented working-directory semantics — the space's first configured folder
// — and the space folder's own config file, which cannot restate `path`, moves
// neither that folder nor the subject the script belongs to.
func TestExecInSpaceIsStillTheSpacesPrimaryFolder(t *testing.T) {
	r := execSpaceRepo(t)

	res := r.Command("exec", "vet", "--for", "space:libs", "--in", "space:libs")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "vet=space-file")

	// The flags answer different questions: the folder is the space's, the
	// script and the environment stay at the top level.
	res = r.Command("exec", "shared", "--in", "space:libs")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-root")
	assert.Contains(t, res.Stdout, "FILE_ONLY=unset",
		"--in moves the folder alone, never the environment")
}

// TestExecRefusesAPlaceItCannotRunIn: --in takes a folder or a level,
// and each way of naming neither is refused before the script is handed to a
// shell: a space the configuration does not declare, and a path that is there
// but is a file.
func TestExecRefusesAPlaceItCannotRunIn(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["where"] = models.Script{"pwd"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/main.txt", "core\n")
	r.Commit("feat(core): bootstrap")

	t.Run("a space the configuration does not declare", func(t *testing.T) {
		res := r.Command("exec", "where", "--in", "space:nowhere")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "unknown space")
		assert.Contains(t, res.Stdout+res.Stderr, "nowhere")
	})

	t.Run("a path that is a file rather than a folder", func(t *testing.T) {
		res := r.Command("exec", "where", "--in", "packages/core/main.txt")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "is not a folder")
	})

	t.Run("a folder that is one is what the refusals are measured against", func(t *testing.T) {
		res := r.Command("exec", "where", "--in", "space:libs")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "packages")
	})
}
