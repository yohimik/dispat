// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final production-review fault scenarios for the planning boundary.
//
// These drive the real binary with one selected Git inquiry made to fail. A
// repository can be perfectly ordinary while its object store, ref inventory,
// or history becomes unreadable, and none of those failures may be weakened
// into an empty plan. The stand-in passes every other invocation to real Git,
// so each case reaches the same process boundary as an operator's command.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func finalPlanRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): bootstrap")
	return r
}

// TestFinalPlanFaultsRefuseAnUnreadableRepositorySnapshot: completeness, refs,
// and commits are the three inputs from which the planner constructs one
// snapshot. If any input cannot be read, status fails and identifies the Git
// inquiry instead of presenting a partial plan as current.
func TestFinalPlanFaultsRefuseAnUnreadableRepositorySnapshot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern string
		want    string
	}{
		{
			name:    "repository completeness",
			pattern: "*rev-parse --is-shallow-repository*",
			want:    "checking repository",
		},
		{
			name:    "release tag inventory",
			pattern: "*tag --list --merged HEAD*",
			want:    "loading tags",
		},
		{
			name:    "pending commit window",
			pattern: "*log --format=*--diff-merges=first-parent HEAD*",
			want:    "plan: core: git log",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := finalPlanRepo(t)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: tc.pattern})

			res := r.CommandEnv(fault.Env(), "status")
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			combined := res.Stdout + res.Stderr
			assert.Contains(t, combined, harness.GitFaultMarker)
			assert.Contains(t, combined, tc.want)
			assert.NotContains(t, combined, "release plan ready")
			assert.Equal(t, 1, fault.Matches(), "the one snapshot inquiry was attempted once")
			assert.Empty(t, r.TagList(), "status never records a release")
		})
	}
}

// TestFinalPlanFaultDoesNotDegradeAncestryToHistoryOrder: corrections require
// an ancestry answer; commit order alone is not enough under merges and
// rebases. A failed DAG read therefore aborts the plan instead of accepting a
// correction on the weaker fallback.
//
// The target is a released commit on purpose. Ancestry between two commits of
// the pending windows is read off the parent lists the window read already
// carries, and asks git nothing; a commit behind the baseline is in no window,
// so that question is the one still put to the repository DAG.
func TestFinalPlanFaultDoesNotDegradeAncestryToHistoryOrder(t *testing.T) {
	r := finalPlanRepo(t)
	target := r.Git("rev-parse", "HEAD")
	r.Git("tag", "-a", "core@0.1.0", "-m", "the bootstrap, released")
	r.CommitEmpty("fix(core): correct the bootstrap\n\nEdits: " + target)
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*rev-list --parents HEAD*"})

	res := r.CommandEnv(fault.Env(), "status")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "ancestry query failed")
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, 1, fault.Matches(), "the repository DAG was requested once")
	assert.Equal(t, []string{"core@0.1.0"}, r.TagList(), "an untrusted correction plan records nothing")
}

// TestFinalPlanFaultStopsRunSinceBeforeTheScript: run --since selects packages
// from the commit window before launching the named script. If Git cannot read
// that window, the command fails and the script does not run against a guessed
// selection.
func TestFinalPlanFaultStopsRunSinceBeforeTheScript(t *testing.T) {
	r := finalPlanRepo(t)
	marker := r.Path("ran.txt")
	cfg := libsConfig("printf 'ran\\n' >> "+harness.ShQuote(marker), 1)
	r.WriteConfigModel(cfg)
	r.WriteFile("packages/core/change.txt", "pending\n")
	r.Commit("fix(core): pending change")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*log --format=*--diff-merges=first-parent HEAD~1..HEAD*",
	})

	res := r.CommandEnv(fault.Env(), "run", "build", "--since", "HEAD~1")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "resolving commits since")
	assert.NotContains(t, combined, "run finished")
	assert.Equal(t, 1, fault.Matches(), "the requested selection window was read once")
	assert.NoFileExists(t, marker, "the script cannot run without a trustworthy selection")
}
