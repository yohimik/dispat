// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: the folder a serving node owns, and what it refuses to serve
// without.
//
// Almost nothing a node keeps is precious: the object cache is a bare
// repository full of fetched coordination objects and can be deleted at any
// moment. The exception is the record of which work this node has already
// answered, because a node that lost half of it would answer a replayed
// assignment twice. So the record and the lock that guards it are the two
// things a node refuses to start without, and the refusal is one sentence
// rather than a node that serves with half a memory.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecutionWorkerStateRefusals: the state folder unusable in each of the
// ways a file system can make it so. None of them starts a node, and each says
// so before a single branch of the mailbox is read.
func TestExecutionWorkerStateRefusals(t *testing.T) {
	rig := newExecutionRig(t)

	for name, prepare := range map[string]func(*testing.T, string){
		"a node folder that cannot be created": func(t *testing.T, state string) {
			// Something else already holds the name the node's own folder needs.
			require.NoError(t, os.WriteFile(filepath.Join(state, executionNode), nil, 0o644))
		},
		"a lock that cannot be read": func(t *testing.T, state string) {
			require.NoError(t, os.MkdirAll(filepath.Join(state, executionNode, "worker.lock"), 0o755))
		},
		"an answered-work record that is not JSON": func(t *testing.T, state string) {
			require.NoError(t, os.MkdirAll(filepath.Join(state, executionNode), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(state, executionNode, "seen.json"),
				[]byte("{this is not a record"), 0o644))
		},
		"an answered-work record that cannot be read": func(t *testing.T, state string) {
			require.NoError(t, os.MkdirAll(filepath.Join(state, executionNode, "seen.json"), 0o755))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))
			state := t.TempDir()
			prepare(t, state)

			res := runWorker(t, rig, []string{executionSecretEnv + "=" + executionSecret},
				"worker", "--root", root, "--state-dir", state, "--idle-timeout", "1")

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			_, isRefused := executionLine(res, "cannot serve tasks")
			assert.True(t, isRefused, "the node said why it cannot serve\nstdout:\n%s", res.Stdout)
			_, isStarted := executionLine(res, "worker started")
			assert.False(t, isStarted, "and never started")
			assert.Empty(t, executionMailboxBranches(t, rig.mailbox),
				"nothing of the mailbox was read")
		})
	}
}

// TestExecutionWorkerKeepsItsStateUnderTheCacheDirectory: an invocation that
// names no folder still has one. The node puts its cache and its record under
// the user's cache directory, because everything there is reconstructible and
// a node that lost it pays one fetch, and it says in its started line where
// that was.
func TestExecutionWorkerKeepsItsStateUnderTheCacheDirectory(t *testing.T) {
	rig := newExecutionRig(t)
	root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))
	home := t.TempDir()

	res := runWorker(t, rig, []string{executionSecretEnv + "=" + executionSecret, "HOME=" + home},
		"worker", "--root", root, "--idle-timeout", "1")

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	started, isStarted := executionLine(res, "worker started")
	require.True(t, isStarted, "stdout:\n%s", res.Stdout)
	assert.Contains(t, started.Str("stateDir"), home,
		"the node kept its folder under the cache directory of the account it runs as")
	assert.Equal(t, executionNode, filepath.Base(started.Str("stateDir")),
		"and named it after itself, so two nodes on one machine keep their records apart")
}
