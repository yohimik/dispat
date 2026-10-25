// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Crafted input states and a signed legacy publish assignment exercise the
// worker's refusal of publication authority. Shared helpers also support
// build-input and transport identity tests.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// executionCraftedDigest is the plan digest every crafted message of this file
// states. Nothing parses it: what it has to be is the same in the assignment
// and in every message that answers it, because a forged assignment naming
// another plan must be refused.
const executionCraftedDigest = "0000000000000000000000000000000000000000000000000000000000000000"

// executionCraftedGeneration is the ownership every crafted message names, in
// the same spirit.
const executionCraftedGeneration = "generation"

// The two scripts a crafted publication's frame runs, each recording itself in
// the file the fixture reads afterwards. The second one is deliberately legacy: a worker must never run it.
const (
	executionCraftedHookScript = `printf '%s %s %s\n' hook "$DISPAT_PACKAGE" "$PWD" ` +
		`>> "$DISPAT_IT_EXECUTION_LOG"`
	executionCraftedPublishScript = `printf '%s %s %s\n' published "$DISPAT_PACKAGE" "$PWD" ` +
		`>> "$DISPAT_IT_EXECUTION_LOG"`
)

// executionInputState is one prepared input state a crafted assignment points
// at: the branch a node fetches it from and the exact commit it materializes.
type executionInputState struct {
	branch string
	commit string
}

// prepareInputState writes one commit holding one file into the mailbox, on a
// branch whose name says it carries a prepared input state rather than work.
//
// A node skips such a branch without reading it and fetches it by name when an
// assignment points at it, which is exactly what a real run's snapshot branch
// is for: this is that branch, made by hand.
func (o *executionFakeOrchestrator) prepareInputState(label string) executionInputState {
	o.t.Helper()
	branch := executionCraftedBranchName("snapshot", label)
	tree := gitIn(o.t, o.mailbox, fmt.Sprintf("100644 blob %s\tREADME.md\x00",
		o.hashObject("a package a crafted assignment reads\n")), "mktree", "-z")
	commit := strings.TrimSpace(bareGit(o.t, o.mailbox, "commit-tree", tree, "-m", "input state"))
	bareGit(o.t, o.mailbox, "update-ref", "refs/heads/"+branch, commit)
	return executionInputState{branch: branch, commit: commit}
}

// publication crafts a signed legacy assignment to prove workers refuse it.
func (o *executionFakeOrchestrator) publication(branch, task string, state executionInputState,
	changes ...func(map[string]any)) map[string]any {
	message := map[string]any{
		"protocol":   executionProtocolVersion,
		"kind":       "publish",
		"run":        o.run,
		"planDigest": executionCraftedDigest,
		"task":       task,
		"attempt":    1,
		"generation": executionCraftedGeneration,
		"node":       executionNode,
		"branch":     branch,
		"issuedAt":   time.Now().UTC().Format(time.RFC3339),
		"repositories": []any{map[string]any{
			"name": "", "path": "", "snapshot": state.commit, "branch": state.branch}},
		"package": map[string]any{"name": task, "version": "1.0.0", "dir": "."},
		"frame": map[string]any{
			"before":   []any{executionCraftedHookScript},
			"commands": []any{executionCraftedPublishScript},
		},
		"env":             []any{"DISPAT_PACKAGE=" + task},
		"permits":         map[string]any{"publish": true},
		"limits":          executionCraftedLimits(),
		"deadlineSeconds": 120,
	}
	for _, change := range changes {
		change(message)
	}
	return message
}

// executionCraftedLimits are the transfer ceilings a crafted assignment holds
// its node to.
func executionCraftedLimits() map[string]any {
	return map[string]any{"maxFiles": 1000, "maxBytes": 10 << 20, "maxManifestBytes": 1 << 20}
}

func executionCraftedBranchName(kind, label string) string {
	return "dispat-worker-" + executionNode + "-20260921-" + kind + "-" + label
}

func executionTipOID(t *testing.T, mailbox, branch string) string {
	t.Helper()
	return strings.TrimSpace(bareGit(t, mailbox, "rev-parse", "refs/heads/"+branch))
}

func executionRecordedTasks(rig *executionRig, marker string) []string {
	var recorded []string
	for _, run := range rig.runs() {
		if run.Node == marker {
			recorded = append(recorded, run.Package)
		}
	}
	return recorded
}
