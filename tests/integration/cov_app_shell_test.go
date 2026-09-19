// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: dispat failing to run a script, as opposed to a script
// failing.
//
// The shell helpers propagate a script's own exit code, which is what makes
// them usable in a pipeline. That rule has two boundaries, and both of them
// are dispat's answer rather than the script's: an interpreter that cannot be
// started at all, and a script that was killed by a signal, which has no exit
// code to propagate. Each becomes dispat's own failure, said in dispat's own
// words, because neither is the script saying anything.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovShellRunSeparatesAScriptItCannotRunFromOneThatFailed: a missing
// interpreter and a script killed by a signal both exit 1 with dispat saying
// what happened, rather than being reported as the script's own answer.
func TestCovShellRunSeparatesAScriptItCannotRunFromOneThatFailed(t *testing.T) {
	t.Run("an interpreter that is not there", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Shell = []string{"/nonexistent/interpreter", "-c"}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")

		res := r.Command("exec", "build", "--log-format", "json")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, diagnosticText(res), "could not run the script")
	})

	t.Run("a script killed by a signal", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Scripts["suicide"] = models.Script{"kill -TERM $$"}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")

		res := r.Command("exec", "suicide", "--log-format", "json", "--log-level", "debug")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, diagnosticText(res), "terminated by a signal")
	})

	t.Run("a script that simply fails keeps its own code", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Scripts["picky"] = models.Script{"exit 7"}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")

		res := r.Command("exec", "picky")
		assert.Equal(t, 7, res.Code, "a script's own code is propagated unchanged")
	})
}
