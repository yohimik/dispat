// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordSourceChangeAfterCommitPreservesTheUnadvertisedRevision proves the
// boundary between a source release commit and its tag. Hooks run between the
// two transactions; if an afterCommit hook commits into the source, the tag
// transaction re-proves the recorded revision and refuses, so the truthful
// release commit remains for repair while no tag, remote ref, or control
// checkpoint may advertise it. Recording that exact revision and checkpointing
// it makes retry a publication no-op.
func TestRecordSourceChangeAfterCommitPreservesTheUnadvertisedRevision(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control

	raw, err := os.ReadFile(control.Path("dispat.json"))
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfg))
	scripts := cfg["scripts"].(map[string]any)
	scripts["move-source-after-commit"] = []string{
		`case "$PWD" in */sources/lib) git -c user.name=intruder -c user.email=intruder@dispat.test ` +
			`commit -q --allow-empty -m 'chore(lib): an unplanned source commit';; esac`,
	}
	cfg["run"] = map[string]any{"afterCommit": []string{"move-source-after-commit"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure the post-commit source change")
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+control.Git("branch", "--show-current"))
	controlBefore := control.Git("rev-parse", "HEAD")

	failed := control.Release()
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.Contains(t, combined, "HEAD moved from recorded source revision")
	assert.Contains(t, failed.Stdout, `"status":"published"`)
	assert.Equal(t, 1, finalPublishCount(t, control))
	releaseCommit := control.Git("-C", "sources/lib", "rev-parse", "HEAD~1")
	require.NotEqual(t, fleet.sourceBefore, releaseCommit, "the completed source commit remains repairable")
	assert.Equal(t, "chore(release): lib@0.1.0",
		control.Git("-C", "sources/lib", "log", "-1", "--format=%s", releaseCommit))
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.Equal(t, fleet.sourceBefore,
		control.Git("-C", fleet.sourceRemote, "rev-parse", "refs/heads/"+control.Git("branch", "--show-current")))
	assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
	assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))

	control.Git("-C", "sources/lib", "reset", "-q", "--hard", releaseCommit)
	repairFinalPinRecord(t, fleet, releaseCommit)
}
