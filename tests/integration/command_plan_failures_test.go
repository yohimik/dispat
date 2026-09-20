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

// TestStandaloneCommandsPropagateAPlanningReadFailure verifies that every
// read-only entry point which computes its own plan preserves a broken Git
// read as an error. In particular, `for --unchanged` must not turn an unknown
// complement into an empty, successful loop.
func TestStandaloneCommandsPropagateAPlanningReadFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "commit", args: []string{"commit", "--package", "core"}},
		{name: "github", args: []string{"github", "--package", "core"}},
		{name: "preview", args: []string{"preview", "--package", "core"}},
		{name: "unchanged selection", args: []string{"for", "--unchanged", "--do", "touch ../../loop-ran"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := singlePackageRepo(t, echoBuild)
			r.Commit("feat(core): bootstrap")
			beforeHead := r.Git("rev-parse", "HEAD")
			beforeStatus := r.Git("status", "--porcelain=v1")
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: "*rev-parse --is-shallow-repository*",
				Code:    128,
			})

			res := r.CommandEnv(fault.Env(), tc.args...)
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, harness.GitFaultMarker)
			assert.Equal(t, 1, fault.Matches(), "the planning read was attempted once")
			assert.Equal(t, beforeHead, r.Git("rev-parse", "HEAD"))
			assert.Equal(t, beforeStatus, r.Git("status", "--porcelain=v1"))
			assert.NoFileExists(t, r.Path("loop-ran"), "a failed complement never runs its loop")
		})
	}
}

// TestCommitPropagatesAWindowReadFailureBeforeWriting checks the second read
// phase of the standalone commit command. The plan itself is sound, but the
// explicit --since window cannot be resolved; the staged work must remain
// exactly as the caller left it.
func TestCommitPropagatesAWindowReadFailureBeforeWriting(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): bootstrap")
	r.WriteFile("packages/core/staged.txt", "keep staged\n")
	r.Git("add", "packages/core/staged.txt")
	beforeHead := r.Git("rev-parse", "HEAD")
	beforeIndex := r.Git("diff", "--cached")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*log --format=*--diff-merges=first-parent HEAD~1..HEAD*",
		Code:    128,
	})

	res := r.CommandEnv(fault.Env(), "commit", "--package", "core", "--since", "HEAD~1")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "resolving commits since")
	assert.Contains(t, res.Stdout+res.Stderr, harness.GitFaultMarker)
	assert.Equal(t, 1, fault.Matches())
	assert.Equal(t, beforeHead, r.Git("rev-parse", "HEAD"))
	assert.Equal(t, beforeIndex, r.Git("diff", "--cached"), "the caller's staged work is preserved")
}

// TestReleasePropagatesAnUnreadableAllowedBranchBeforeMutation reaches the
// release-only branch guard after a valid plan. A repository that cannot name
// its current branch cannot be assumed to satisfy run.allowBranch.
func TestReleasePropagatesAnUnreadableAllowedBranchBeforeMutation(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	cfg.Run = &models.RunConfig{AllowBranch: []string{"*"}}
	cfg.UnsafeDisableLock = true
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	beforeHead := r.Git("rev-parse", "HEAD")
	beforeStatus := r.Git("status", "--porcelain=v1")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*rev-parse --abbrev-ref HEAD*",
		Code:    128,
	})

	res := r.CommandEnv(fault.Env(), "release")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "refusing to release")
	assert.Contains(t, res.Stdout+res.Stderr, harness.GitFaultMarker)
	assert.Equal(t, 1, fault.Matches())
	assert.Equal(t, beforeHead, r.Git("rev-parse", "HEAD"))
	assert.Equal(t, beforeStatus, r.Git("status", "--porcelain=v1"))
	assert.False(t, r.IsTagged("core@0.1.0"))
	assert.Equal(t, 0, buildRuns(r), "the branch guard fires before release scripts")
}
