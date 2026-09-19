//go:build !windows

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

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFinalFleetUnlockMutationDamageFailsAfterPublishing proves that a fleet
// never reports success when one repository's remote release lock could not be
// returned. postAll runs after publication and all durable fleet records, so
// replacing the source's mutation-lock file there reaches precisely the final
// cleanup boundary: the published result stays true, the control lock still
// gets released, and the stranded source lock makes the run fail with E336.
func TestFinalFleetUnlockMutationDamageFailsAfterPublishing(t *testing.T) {
	sink := newWebhookSink(t)
	fleet := newFinalFaultFleet(t)
	control := fleet.control

	var cfg map[string]any
	require.NoError(t, unmarshalJSONFile(control.Path("dispat.json"), &cfg))
	scripts := cfg["scripts"].(map[string]any)
	scripts["damage-source-mutation-lock"] = []string{
		`lock_path=$(git -C sources/lib rev-parse --path-format=absolute --git-path dispat-mutation.lock) && rm -f "$lock_path" && mkdir "$lock_path"`,
	}
	cfg["run"] = map[string]any{"postAll": []string{"damage-source-mutation-lock"}}
	cfg["webhooks"] = []map[string]any{{
		"url": sink.srv.URL, "events": []string{"release.finished"},
	}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: observe final fleet lock cleanup")
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	controlBefore := control.Git("rev-parse", "HEAD")

	res := control.CommandEnv(harness.LockEnabled, "release")
	combined := res.Stdout + res.Stderr
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, combined, "E336")
	assert.Contains(t, combined, "unable to acquire local mutation lock for fleet lock cleanup")
	assert.Contains(t, combined, `"commitMessage":"chore(release): lib@0.1.0"`)
	assert.Equal(t, 1, finalPublishCount(t, control), "the successful publication remains true")
	assert.NotEmpty(t, strings.TrimSpace(control.Git("-C", "sources/lib", "tag", "--list", "lib@0.1.0")))
	assert.NotEqual(t, fleet.sourceBefore, control.Git("-C", "sources/lib", "rev-parse", "HEAD"),
		"the source release record was committed")
	assert.NotEqual(t, controlBefore, control.Git("rev-parse", "HEAD"),
		"the control gitlink checkpoint was committed")
	assert.True(t, remoteHoldsLock(t, fleet.sourceRemote), "the cleanup failure is durable and reported")
	assert.False(t, remoteHoldsLock(t, fleet.controlRemote), "later cleanup still releases the control lock")

	finished := sink.find(t, "release.finished")
	assert.Equal(t, "failed", finished["status"])
	assert.Equal(t, float64(1), finished["published"])
	packages := finished["packages"].([]any)
	require.Len(t, packages, 1)
	assert.Equal(t, "published", packages[0].(map[string]any)["status"])

	// The deliberately damaged path belongs to this disposable repository,
	// but repairing it keeps the fixture honest for cleanup and diagnostics.
	common := strings.TrimSpace(control.Git("-C", "sources/lib", "rev-parse", "--path-format=absolute", "--git-common-dir"))
	require.NoError(t, os.Remove(filepath.Join(common, "dispat-mutation.lock")))
}

func unmarshalJSONFile(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}
