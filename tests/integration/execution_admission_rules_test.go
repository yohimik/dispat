// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: retired result writers cannot enter the v3 return channel. The
// execution package tests exercise the output entry and size checks against
// real Git objects admitted through the production readers.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

// A writer using the retired Git-push result path cannot answer a v3
// preflight or start a command. The individual path, mode, link, ordering and
// tree rules are exercised against real Git objects by the execution package
// manifest and install tests used by v3 result admission.
func TestExecutionRetiredOutputEntryWriterCannotClaim(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		executionOneWorker(cfg)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 5}
	})
	writer := newExecutionFakeWorker(t, rig.mailbox, executionNode,
		executionCraftedPath("dist/../../escape.js"))
	writer.serve()
	defer writer.close()

	res := rig.release()
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
	assert.Zero(t, buildRuns(rig.repo), "retired result transport cannot start a command")
	assert.Empty(t, rig.repo.TagList(), "no unverified output can publish")
}

// executionCraftedLink replaces the crafted set with one symlink, for the rows
// whose claim is about what a link may point at.
//
// A target nobody stated is written as a missing field rather than as an empty
// one, because the manifest digest is taken over the producing engine's own
// marshalling and that engine omits an empty target: a document carrying the
// key with nothing in it would be refused for its digest before the link rule
// was ever asked.
func executionCraftedLink(path, target string) func(*executionCraftedOutputs) {
	return func(c *executionCraftedOutputs) {
		c.entries = map[string]executionTreeFile{path: {mode: "120000", content: target}}
		entry := executionOrderedJSON{}.with(
			executionField{"path", path},
			executionField{"type", "symlink"},
			executionField{"mode", "0644"},
			executionField{"size", len(target)},
			executionField{"sha256", executionDigestOf(target)},
		)
		if target != "" {
			entry = entry.with(executionField{"target", target})
		}
		c.manifest = c.manifest.set("entries", []any{entry}).set("bytes", len(target))
	}
}
