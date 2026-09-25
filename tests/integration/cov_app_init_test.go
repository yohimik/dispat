// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: where `dispat init` will and will not write.
//
// The starter config establishes the effective monorepo root for every later
// command, so the command is deliberately narrow about where it puts one: the
// repository root, in one of three formats, over nothing that is already
// there. Goal 17 owns the composing case. What is here is each refusal, and
// the default format nobody has to type.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovInitWritesJSONByDefaultAndRefusesTheRest: with no format asked for,
// the starter is JSON, and a format dispat cannot write is refused with
// nothing created.
func TestCovInitWritesJSONByDefaultAndRefusesTheRest(t *testing.T) {
	t.Run("no format is JSON", func(t *testing.T) {
		r := harness.New(t)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")

		res := r.Command("init")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "created dispat.json")
		assert.Contains(t, readRepoFile(t, r, "dispat.json"), `"spaces"`)
		r.StatusOK()
	})

	t.Run("a format dispat cannot write", func(t *testing.T) {
		r := harness.New(t)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")

		res := r.Command("init", "--format", "ini")
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "unknown config format")
		assert.Empty(t, r.Git("status", "--porcelain", "--untracked-files=all", "--", "dispat.*"),
			"nothing was written")
	})
}
