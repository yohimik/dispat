// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A worker polls by fetching mailbox refs. Git automatically runs maintenance
// after each fetch; it must remain a child of that fetch so a long-lived
// container without an init reaper cannot collect orphaned Git processes.
func TestFetchKeepsAutomaticMaintenanceAttached(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	runGit(t, f.root, "config", "maintenance.auto", "true")
	runGit(t, f.root, "config", "maintenance.autoDetach", "true")
	runGit(t, f.root, "config", "gc.autoDetach", "true")
	require.NoError(t, f.git.PushCreate(ctx, f.bare, f.first, "dispat-worker-maintenance"))

	trace := filepath.Join(t.TempDir(), "git-trace.json")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	require.NoError(t, f.git.FetchRefs(ctx, f.bare, []string{"dispat-worker-maintenance"}))

	// The override is scoped to this Git invocation, including its child.
	// Repository configuration remains as the operator set it.
	assert.Equal(t, "true", strings.TrimSpace(runGit(t, f.root, "config", "--get", "maintenance.autoDetach")))
	assert.Equal(t, "true", strings.TrimSpace(runGit(t, f.root, "config", "--get", "gc.autoDetach")))
	for _, key := range []string{"maintenance.autoDetach", "gc.autoDetach"} {
		out, err := f.git.run(ctx, "config", "--get", key)
		require.NoError(t, err)
		assert.Equal(t, "false", strings.TrimSpace(out), key)
	}

	data, err := os.ReadFile(trace)
	require.NoError(t, err)
	var attached int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event struct {
			Event string   `json:"event"`
			Argv  []string `json:"argv"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		if event.Event != "child_start" || len(event.Argv) < 3 || event.Argv[0] != "git" ||
			event.Argv[1] != "maintenance" || event.Argv[2] != "run" {
			continue
		}
		attached++
		assert.Contains(t, event.Argv, "--auto")
		assert.Contains(t, event.Argv, "--no-detach")
		assert.NotContains(t, event.Argv, "--detach")
	}
	require.Positive(t, attached, "fetch did not exercise automatic maintenance")
}
