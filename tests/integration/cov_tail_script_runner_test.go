// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the shell a script runs under.
//
// A script is an opaque command line handed to a shell, and the two things
// that go wrong with that are invisible from inside the script: what the run
// recorded about executing it, and what happens when the script's own process
// finishes while something it started is still holding the output pipes. The
// second is the case a backgrounded daemon produces, and treating it as a
// failure would fail releases whose scripts did exactly what they were asked.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovTailScriptExecutionIsRecordedAtTrace: what a run may say about a
// script is deliberately narrow — the shell, the folder, how many bytes of
// command and how many environment entries, and how long it took — because
// the command text itself can contain a literal credential and must not be
// copied into a log. The claim here is that the record exists and that the
// command is not in it.
func TestCovTailScriptExecutionIsRecordedAtTrace(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["secretive"] = models.Script{"echo published with token hunter2"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	res := r.RunScriptOK("secretive", "--since", "all", "--log-level", "trace")
	assert.Contains(t, res.Stdout, "script finished",
		"the execution is recorded: %s", res.Stdout)
	assert.Contains(t, res.Stdout, "commandBytes",
		"as a size rather than as the text: %s", res.Stdout)
	assert.NotContains(t, res.Stdout, `"command":"echo published with token hunter2"`,
		"the command line is never copied into the record: %s", res.Stdout)
}

// TestCovTailScriptThatLeavesAChildHoldingTheOutputPipes: backgrounding a
// process is a legitimate thing for a release script to do, and a child that
// outlives the shell inherits the pipes the run reads the script's output
// through. Waiting for those pipes forever would hang the release, so the wait
// is bounded — and a script whose own process exited successfully has
// succeeded, whatever its children are still doing.
func TestCovTailScriptThatLeavesAChildHoldingTheOutputPipes(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	// The shell exits at once; the child it started keeps the write end of
	// the output pipe open past the bounded wait.
	cfg.Scripts["daemon"] = models.Script{"sleep 12 & echo started"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	started := time.Now()
	res := r.RunScript("daemon", "--since", "all")
	require.Equal(t, 0, res.Code,
		"the script's own process succeeded: stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "started", "and its output was read: %s", res.Stdout)
	if harness.IsTinyGo() {
		// TinyGo's scheduler is single-threaded: the blocking read of the
		// inherited pipe stalls every goroutine, the bounded wait included,
		// so the tiny binary returns when the child lets go of the pipe. The
		// outcome above still holds; only the bound is the gc runtime's.
		return
	}
	assert.Less(t, time.Since(started), 12*time.Second,
		"the run did not wait for the child it left behind")
}
