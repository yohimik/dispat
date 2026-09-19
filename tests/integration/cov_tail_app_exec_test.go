// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Long-tail coverage for where a one-shot script runs and what environment it
// is handed. Both are decided before a shell exists, so every refusal names
// the flag and the value rather than leaving the reader with whatever a shell
// says about a working directory it could not enter or a variable that was
// never set.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovTailExecRefusesAPlaceItCannotRunIn: --in takes a folder or a level,
// and each way of naming neither is refused before the script is handed to a
// shell: a space the configuration does not declare, and a path that is there
// but is a file.
func TestCovTailExecRefusesAPlaceItCannotRunIn(t *testing.T) {
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
