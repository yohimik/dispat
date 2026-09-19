//go:build !windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal: composing an inherited workspace is interruptible. A nested command
// that accepted a live run context validates each source checkout while
// holding that source's Git mutation lock, so composition can queue behind
// whatever release is recording there. The wait is bounded at thirty seconds
// so a lock nobody will release cannot hang a command forever, but the bound
// is not the answer to Ctrl-C: an operator who interrupts a queued command
// must get the terminal back at once, and the run must say what it was
// waiting for rather than exit in silence.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// mutationLockWait is how long the binary bounds one live pin validation lock
// acquisition. The scenario's oracle is that an interrupt beats it by a wide
// margin, so the assertions are written against this number rather than
// against a bare stopwatch reading.
const mutationLockWait = 30 * time.Second

// liveWorkspaceEnv builds the inherited workspace context a release exports to
// the scripts it runs: the composed control invocation, the exact package
// owner map, the exact source repository identities, and the private live-pin
// directory the coordinator publishes verified source revisions through.
//
// Written here rather than driven through a real parent release because the
// scenario is about the nested command alone: a parent would hold the lock
// this test has to hold itself, and would take the same interrupt.
func liveWorkspaceEnv(t *testing.T, control *harness.Repo, owners map[string]string, repositories []string) []string {
	t.Helper()
	root, err := filepath.EvalSymlinks(control.Root)
	require.NoError(t, err)
	configPath, err := filepath.EvalSymlinks(filepath.Join(control.Root, "dispat.json"))
	require.NoError(t, err)

	ownersJSON, err := json.Marshal(owners)
	require.NoError(t, err)
	repositoriesJSON, err := json.Marshal(repositories)
	require.NoError(t, err)
	metadata, err := json.Marshal(map[string]any{
		"root": root, "config": configPath, "owners": owners, "repositories": repositories,
	})
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "context.json"), append(metadata, '\n'), 0o600))
	return []string{
		"DISPAT_INTERNAL_WORKSPACE_ROOT=" + control.Root,
		"DISPAT_INTERNAL_WORKSPACE_CONFIG=dispat.json",
		"DISPAT_INTERNAL_WORKSPACE_CONFIGS=[]",
		"DISPAT_INTERNAL_WORKSPACE_OWNERS=" + string(ownersJSON),
		"DISPAT_INTERNAL_WORKSPACE_REPOSITORIES=" + string(repositoriesJSON),
		"DISPAT_INTERNAL_WORKSPACE_LIVE_PINS=" + dir,
	}
}

// holdMutationLock takes the Git mutation lock of the checkout at relPath, the
// way another dispat process recording there would hold it, and keeps it for
// the rest of the test.
//
// The descriptor is this process's own: flock is a property of the open file
// description, so an independent open is exactly what a second process brings,
// and a waiter cannot tell the two apart.
func holdMutationLock(t *testing.T, control *harness.Repo, relPath string) {
	t.Helper()
	common := strings.TrimSpace(control.Git("-C", relPath, "rev-parse", "--path-format=absolute", "--git-common-dir"))
	require.NotEmpty(t, common, "the checkout reports no Git common directory")
	file, err := os.OpenFile(filepath.Join(common, "dispat-mutation.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	})
	require.NoError(t, syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB),
		"the lock was already held, so the scenario would prove nothing")
}

// TestCancelWorkspaceCompositionStopsOnInterrupt: a nested command queued
// behind another run's Git mutation lock stops on SIGINT instead of sitting
// out the thirty-second bound. The error names the wait it was in, and says
// it was cancelled rather than timed out, so the two outcomes cannot be
// mistaken for one another.
func TestCancelWorkspaceCompositionStopsOnInterrupt(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "sdk")
	source.Commit("feat(sdk): bootstrap the library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "sdk-source", "sources/sdk", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libraries": "sources/sdk/packages"})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble the control repository")

	// The composition below has to wait, so somebody else has to be holding
	// the source's lock before the command starts.
	holdMutationLock(t, control, "sources/sdk")

	env := liveWorkspaceEnv(t, control, map[string]string{"sdk": "sdk-source"}, []string{"sdk-source"})
	proc := control.StartCommandEnv(env, "status")

	// Long enough for the binary to reach composition and install its signal
	// handler, and short enough to leave the bound untouched. An interrupt
	// that arrived before the handler would kill the process outright and
	// leave the output empty, which the assertions below report as a failure
	// rather than passing on.
	time.Sleep(2 * time.Second)
	signalled := time.Now()
	proc.Signal(os.Interrupt)
	res := proc.Wait()
	waited := time.Since(signalled)

	output := res.Stdout + res.Stderr
	assert.NotEqual(t, 0, res.Code, "an interrupted composition does not exit 0")
	assert.Contains(t, output, "acquiring live pin validation lock",
		"the run must say which wait it was interrupted in; stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, output, "context canceled",
		"the interrupt is what ended the wait, not the bound")
	assert.NotContains(t, output, "context deadline exceeded",
		"the thirty-second bound must not be what answers a Ctrl-C")
	assert.Less(t, waited, mutationLockWait-10*time.Second,
		"the interrupt has to end the wait at once, not after the bound")
}
