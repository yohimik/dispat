// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func newGuardReadFleet(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): pending work")
	r.AddBareRemote()
	return r
}

// TestReleaseBehindGuardRefusesUnreadableRemoteState proves both reads that
// establish the checkout's remote position are preconditions. If Git cannot
// name the local branch or query its remote tip, release stops before planning
// or package work and a healthy retry publishes exactly once.
func TestReleaseBehindGuardRefusesUnreadableRemoteState(t *testing.T) {
	for _, tc := range []struct {
		name, pattern string
	}{
		{name: "current branch", pattern: "*rev-parse --abbrev-ref HEAD*"},
		{name: "remote branch", pattern: "*ls-remote origin refs/heads/" + harness.DefaultBranch + "*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newGuardReadFleet(t)
			before := r.Git("rev-parse", "HEAD")
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: tc.pattern, Code: 128})

			failed := r.CommandEnv(fault.Env(), "release")
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			assert.Contains(t, failed.Stdout+failed.Stderr, harness.GitFaultMarker)
			assert.Equal(t, 1, fault.Matches())
			assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
			assert.Empty(t, r.TagList())
			assert.Zero(t, buildRuns(r))

			retry := r.Release()
			require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
			assert.True(t, r.IsTagged("core@0.1.0"))
			assert.Equal(t, 1, buildRuns(r))
		})
	}
}

// TestReleaseLockRefusesAnAmbiguousPushDestination proves a remote with two
// push URLs cannot coordinate one release lock. The run fails before package
// work rather than locking one destination while publishing to another.
func TestReleaseLockRefusesAnAmbiguousPushDestination(t *testing.T) {
	r := newGuardReadFleet(t)
	second := r.Path("second-remote.git")
	r.Git("init", "-q", "--bare", second)
	first := r.Git("remote", "get-url", "origin")
	r.Git("remote", "set-url", "--add", "--push", "origin", first)
	r.Git("remote", "set-url", "--add", "--push", "origin", second)
	before := r.Git("rev-parse", "HEAD")

	failed := r.CommandEnv(harness.LockEnabled, "release")
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Contains(t, failed.Stdout+failed.Stderr, "has 2 push destinations")
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Empty(t, r.TagList())
	assert.Zero(t, buildRuns(r))
}
