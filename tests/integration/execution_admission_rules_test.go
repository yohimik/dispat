// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: the rest of the ladder an output set is held to before a single
// file of it is installed anywhere.
//
// The neighbouring file drives the reasons a description gets wrong about
// itself: the wrong run, the wrong digest, a file that is not there. What is
// left is the half about what a path and an entry may be, and it is worth
// driving one row at a time because each word of the enum is a different way a
// harmless-looking description becomes a file somewhere it should not be, or a
// set whose contents depend on the machine that unpacks it.
//
// Every row is one crafted answer from a node that never ran a build, which is
// what a compromised or broken machine would send. None of them is ever
// admitted, and the producing package fails.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestExecutionOutputEntryRulesRefuseTheSet: the entry, path, link and
// ordering rules, one crafted answer each.
//
// The claim is the same in every row and is the reason the enum exists: the
// orchestrator names the rule that was broken, never the path that broke it,
// the set is not admitted, the package that was supposed to produce it fails
// with the integrity code, and nothing is published.
func TestExecutionOutputEntryRulesRefuseTheSet(t *testing.T) {
	for name, row := range map[string]struct {
		craft  func(*executionCraftedOutputs)
		reason string
	}{
		"entries the producer did not sort": {
			craft: func(c *executionCraftedOutputs) {
				c.entries["dist/b.js"] = executionTreeFile{mode: "100644", content: "one\n"}
				c.manifest = c.manifest.set("entries", []any{
					executionCraftedEntry("dist/b.js", "file", "0644", 4, executionDigestOf("one\n")),
					executionCraftedEntry("dist/app.js", "file", "0644", 4, executionDigestOf("one\n")),
				}).set("files", 2).set("bytes", 8)
			},
			reason: "path-unsorted"},
		"totals the entries do not add up to": {
			craft: func(c *executionCraftedOutputs) {
				c.manifest = c.manifest.set("files", 7)
			},
			reason: "tree-totals"},
		"an entry that is neither a file nor a link": {
			craft: func(c *executionCraftedOutputs) {
				c.manifest = c.manifest.set("entries", []any{
					executionCraftedEntry("dist/app.js", "socket", "0644", 4, executionDigestOf("one\n"))})
			},
			reason: "entry-type"},
		"a mode no build product carries": {
			craft: func(c *executionCraftedOutputs) {
				c.manifest = c.manifest.set("entries", []any{
					executionCraftedEntry("dist/app.js", "file", "0777", 4, executionDigestOf("one\n"))})
			},
			reason: "mode"},
		"an executable symlink": {
			craft: func(c *executionCraftedOutputs) {
				c.manifest = c.manifest.set("entries", []any{
					executionCraftedEntry("dist/app.js", "symlink", "0755", 4, executionDigestOf("one\n"))})
			},
			reason: "mode"},
		"a length below zero": {
			craft: func(c *executionCraftedOutputs) {
				c.manifest = c.manifest.set("entries", []any{
					executionCraftedEntry("dist/app.js", "file", "0644", -1, executionDigestOf("one\n"))})
			},
			reason: "tree-size"},
		"a path carrying a null byte":     {craft: executionCraftedPath("dist/app\x00.js"), reason: "path-nul"},
		"a path carrying a colon":         {craft: executionCraftedPath("dist/c:app.js"), reason: "path-colon"},
		"a path with an empty component":  {craft: executionCraftedPath("dist//app.js"), reason: "path-component"},
		"a path with a dot component":     {craft: executionCraftedPath("dist/./app.js"), reason: "path-component"},
		"a path under no declared root":   {craft: executionCraftedPath("build/app.js"), reason: "path-outside-root"},
		"a path that is a sibling of one": {craft: executionCraftedPath("dist-old/app.js"), reason: "path-outside-root"},
		"a symlink pointing at nothing": {
			craft:  executionCraftedLink("dist/link", ""),
			reason: "link-target-empty"},
		"a symlink target carrying a null byte": {
			craft:  executionCraftedLink("dist/link", "app\x00.js"),
			reason: "path-nul"},
		"a symlink target written with backslashes": {
			craft:  executionCraftedLink("dist/link", `sub\app.js`),
			reason: "path-backslash"},
		"a file the tree holds as a link": {
			craft: func(c *executionCraftedOutputs) {
				c.entries = map[string]executionTreeFile{
					"dist/app.js": {mode: "120000", content: "one\n"}}
			},
			reason: "tree-type"},
		"a mode the tree disagrees with": {
			craft: func(c *executionCraftedOutputs) {
				c.entries = map[string]executionTreeFile{
					"dist/app.js": {mode: "100755", content: "one\n"}}
			},
			reason: "tree-mode"},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionOutputWorkspace(t, executionOneWorker)
			worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, row.craft)
			worker.serve()
			defer worker.close()

			res := rig.release()

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			rejected, isRejected := executionLine(res, "outputs rejected")
			require.True(t, isRejected, "the orchestrator says which rule was broken\nstdout:\n%s", res.Stdout)
			assert.Equal(t, row.reason, rejected.Str("reason"))
			assert.Equal(t, executionIntegrityCode, rejected.Code())
			_, isAdmitted := executionLine(res, "outputs admitted")
			assert.False(t, isAdmitted, "and nothing the crafted answer described was admitted")
			assert.True(t, harness.IsCodePresentForPackage(executionEvents(res), executionIntegrityCode, "assets"),
				"the producing package fails\nstdout:\n%s", res.Stdout)
			assert.Empty(t, rig.repo.TagList(), "and nothing was published")
		})
	}
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

// TestExecutionOversizedManifestIsRefusedBeforeItIsRead: the ceiling that
// bounds what a consumer spends on a description it has not verified. A
// manifest larger than the run's own `maxManifestBytes` is refused without
// being parsed, so a node can neither exhaust the orchestrator's memory nor
// have an oversized set admitted.
func TestExecutionOversizedManifestIsRefusedBeforeItIsRead(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		executionOneWorker(cfg)
		cfg.Execution.Transfer = &models.ExecutionTransferConfig{MaxManifestBytes: 256}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 8}
	})
	worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, func(*executionCraftedOutputs) {})
	worker.serve()
	defer worker.close()

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	rejected, isRejected := executionLine(res, "result rejected")
	require.True(t, isRejected, "the reply is refused before it is parsed\nstdout:\n%s", res.Stdout)
	assert.Equal(t, "oversize", rejected.Str("reason"))
	_, isAdmitted := executionLine(res, "outputs admitted")
	assert.False(t, isAdmitted, "so nothing it described was admitted")
	assert.Empty(t, rig.repo.TagList(), "and nothing was published")
}
