//go:build !windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal: composing an inherited workspace is interruptible. A nested command
// that accepted a live run context validates each source checkout against the
// pin the release around it publishes, and a checkout at a revision that pin
// does not admit yet is read again for a few seconds: the release commits into
// a source before it publishes the revision it admitted. That wait is bounded,
// but the bound is not the answer to Ctrl-C: an operator who interrupts a
// waiting command must get the terminal back at once, and the run must say
// what it was waiting for rather than exit in silence.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// livePinReadsLeft is how long the binary's bounded pin reads still have to
// run once the second read has happened: eight more intervals of a quarter of
// a second. The scenario's oracle is that an interrupt beats it, so the
// assertions are written against this number rather than a bare stopwatch.
const livePinReadsLeft = 8 * 250 * time.Millisecond

// liveWorkspaceEnv builds the inherited workspace context a release exports to
// the scripts it runs: the composed control invocation, the exact package
// owner map, the exact source repository identities, and the private live-pin
// directory the coordinator publishes verified source revisions through.
//
// Written here rather than driven through a real parent release because the
// scenario is about the nested command alone: a parent would publish the pin
// this test has to withhold, and would take the same interrupt.
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

// TestCancelWorkspaceCompositionStopsOnInterrupt: a nested command waiting for
// the live pin of a source whose checkout nobody has admitted stops on SIGINT
// instead of reading out its bound. The error names the wait it was in, and
// says it was cancelled, so it cannot be mistaken for the checkout refusal the
// bound would have ended in.
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

	// The checkout moves past its gitlink and the live context names no
	// revision for it, which is what a nested command sees between the
	// release's commit and its pin; here the pin never comes.
	control.Git("-C", "sources/sdk", "-c", "user.name=release", "-c", "user.email=release@dispat.test",
		"commit", "-q", "--allow-empty", "-m", "chore(sdk): a commit no pin admits")
	// Every HEAD read of the pin validation is counted and passed through, so
	// the scenario knows when the command is inside its reads and can
	// interrupt the wait rather than the start of the process.
	reads := harness.NewGitFault(t, harness.GitFault{Pattern: "*/sources/sdk rev-parse HEAD", Nth: 1 << 20})

	env := liveWorkspaceEnv(t, control, map[string]string{"sdk": "sdk-source"}, []string{"sdk-source"})
	proc := control.StartCommandEnv(append(env, reads.Env()...), "status")
	require.Eventually(t, func() bool { return reads.Matches() >= 2 }, time.Minute, 10*time.Millisecond,
		"the command reads the checkout again because no pin admits it")
	signalled := time.Now()
	proc.Signal(os.Interrupt)
	res := proc.Wait()
	waited := time.Since(signalled)

	output := res.Stdout + res.Stderr
	assert.NotEqual(t, 0, res.Code, "an interrupted composition does not exit 0")
	assert.Contains(t, output, "waiting for a stable live run pin",
		"the run must say which wait it was interrupted in; stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, output, "context canceled", "the interrupt is what ended the wait")
	assert.NotContains(t, output, "is checked out at",
		"the bounded reads must not be what answers a Ctrl-C")
	assert.Less(t, waited, livePinReadsLeft, "the interrupt has to end the wait at once, not after the bound")
}
