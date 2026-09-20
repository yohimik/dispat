// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordSourceLockFailureAfterCommitPreservesTheUnadvertisedRevision proves
// the boundary between a source release commit and its tag. Hooks run outside
// the commit lock; if an afterCommit hook makes the next lock acquisition
// impossible, the truthful commit remains for repair while no tag, remote ref,
// or control checkpoint may advertise it. Recording that exact revision and
// checkpointing it makes retry a publication no-op.
func TestRecordSourceLockFailureAfterCommitPreservesTheUnadvertisedRevision(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control
	common := strings.TrimSpace(control.Git("-C", "sources/lib", "rev-parse",
		"--path-format=absolute", "--git-common-dir"))
	lockPath := filepath.Join(common, "dispat-mutation.lock")

	raw, err := os.ReadFile(control.Path("dispat.json"))
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfg))
	scripts := cfg["scripts"].(map[string]any)
	scripts["break-source-lock-after-commit"] = []string{
		`case "$PWD" in */sources/lib) lock=$(git rev-parse --path-format=absolute --git-common-dir)/dispat-mutation.lock; rm -f "$lock"; mkdir "$lock";; esac`,
	}
	cfg["run"] = map[string]any{"afterCommit": []string{"break-source-lock-after-commit"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure the post-commit lock fault")
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+control.Git("branch", "--show-current"))
	controlBefore := control.Git("rev-parse", "HEAD")

	failed := control.Release()
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.Contains(t, combined, "opening Git mutation lock")
	assert.Contains(t, failed.Stdout, `"status":"published"`)
	assert.Equal(t, 1, finalPublishCount(t, control))
	sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	require.NotEqual(t, fleet.sourceBefore, sourceAfter, "the completed source commit remains repairable")
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.Equal(t, fleet.sourceBefore,
		control.Git("-C", fleet.sourceRemote, "rev-parse", "refs/heads/"+control.Git("branch", "--show-current")))
	assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
	assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))

	require.NoError(t, os.Remove(lockPath))
	repairFinalPinRecord(t, fleet, sourceAfter)
}
