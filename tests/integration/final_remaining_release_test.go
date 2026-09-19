//go:build !windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFinalConflictInspectionFailureAbortsBeforeSettlement: a content merge
// conflict is not safe to settle unless Git can enumerate every unmerged path.
// A failed inspection therefore returns the original merge failure, aborts the
// merge on a live cleanup context, and preserves the published local record and
// the foreign remote tip for an operator instead of guessing at the index.
func TestFinalConflictInspectionFailureAbortsBeforeSettlement(t *testing.T) {
	r, remote := newFinalConflictRepo(t)
	remoteBefore := r.Git("-C", remote, "rev-parse", "refs/heads/"+harness.DefaultBranch)
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*diff --name-only --diff-filter=U*"})

	failed := r.CommandEnv(fault.Env())
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Contains(t, failed.Stdout+failed.Stderr, "could not be merged with it")
	assert.True(t, harness.IsCodePresent(failed.Events, "E224"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, failed.Stdout, `"status":"published"`)
	assert.Equal(t, 1, fault.Matches())
	assert.True(t, r.IsTagged("core@0.1.0"), "the published release keeps its immutable local tag")
	assert.NoFileExists(t, r.Path(".git", "MERGE_HEAD"), "the uninspected merge is aborted")
	assert.NotEqual(t, remoteBefore, r.Git("-C", remote, "rev-parse", "refs/heads/"+harness.DefaultBranch),
		"the foreign commit remains the durable remote tip")
	assert.Equal(t, "docs: conflicting remote edit",
		r.Git("-C", remote, "log", "-1", "--format=%s", "refs/heads/"+harness.DefaultBranch))
	assert.Equal(t, "remote", r.Git("-C", remote, "show",
		"refs/heads/"+harness.DefaultBranch+":packages/core/main.txt"))
	assert.Equal(t, "release", r.Git("show", "core@0.1.0:packages/core/main.txt"),
		"the local immutable tag retains the published side")
	assert.Empty(t, r.Git("-C", remote, "for-each-ref", "--format=%(refname)", "refs/heads/release-conflicts"),
		"settlement never invents a quarantine without a proven conflict set")
}

// TestFinalRecoveryAbortFailureReportsTheRetainedObstacle: an untracked local
// file that the concurrent remote commit would overwrite makes Git refuse the
// recovery merge before it starts. The cleanup still attempts merge --abort;
// its failure is reported separately while the release error remains the one
// controlling E224 and both sides remain available for explicit repair.
func TestFinalRecoveryAbortFailureReportsTheRetainedObstacle(t *testing.T) {
	r := harness.New(t)
	remote := r.AddBareRemote()
	foreign := midReleasePush(t, remote, "docs: add the remote recovery input", "REMOTE.md", "remote\n")
	cfg := libsConfig(foreign+" && printf 'local\\n' > ../../REMOTE.md", 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	cfg.Scripts["publish"] = models.Script{"printf 'published\\n' >> ../../publish-count"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): publish before an untracked recovery obstacle")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	failed := r.Release()
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Contains(t, failed.Stdout, "could not abort the merge")
	assert.Contains(t, failed.Stdout, "would be overwritten by merge")
	assert.True(t, harness.IsCodePresent(failed.Events, "E224"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, failed.Stdout, `"status":"published"`)
	assert.True(t, r.IsTagged("core@0.1.0"))
	assert.NoFileExists(t, r.Path(".git", "MERGE_HEAD"), "Git refused before creating merge state")
	body, err := os.ReadFile(r.Path("REMOTE.md"))
	require.NoError(t, err)
	assert.Equal(t, "local\n", string(body), "the untracked local input remains reviewable")
	assert.Empty(t, r.Git("-C", remote, "tag", "--list", "core@0.1.0"))
}

// TestFinalConflictQuarantineLookupRefusalsPreserveBothSides: settlement must
// prove its quarantine branch name is free before pushing the foreign side.
// An unavailable lookup and a reply saying the name is occupied both stop
// before any remote quarantine write and leave the merge open for review.
func TestFinalConflictQuarantineLookupRefusalsPreserveBothSides(t *testing.T) {
	for _, tc := range []struct {
		name, output, want string
	}{
		{name: "lookup failure", want: harness.GitFaultMarker},
		{name: "occupied name", output: "occupied quarantine ref\n", want: "already has a branch called release-conflicts/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, remote := newFinalConflictRepo(t)
			remoteBefore := r.Git("-C", remote, "rev-parse", "refs/heads/"+harness.DefaultBranch)
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: "*ls-remote --heads origin refs/heads/release-conflicts/*",
				Output:  tc.output,
			})

			failed := r.CommandEnv(fault.Env())
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			combined := failed.Stdout + failed.Stderr
			assert.Contains(t, combined, tc.want)
			assert.True(t, harness.IsCodePresent(failed.Events, "E224"), "stdout:\n%s", failed.Stdout)
			assert.Contains(t, failed.Stdout, `"status":"published"`)
			assert.Equal(t, 1, fault.Matches())
			assert.True(t, r.IsTagged("core@0.1.0"))
			assert.FileExists(t, r.Path(".git", "MERGE_HEAD"), "the refused settlement remains reviewable")
			assert.NotEqual(t, remoteBefore, r.Git("-C", remote, "rev-parse", "refs/heads/"+harness.DefaultBranch),
				"the foreign commit remains the durable remote tip")
			assert.Equal(t, "docs: conflicting remote edit",
				r.Git("-C", remote, "log", "-1", "--format=%s", "refs/heads/"+harness.DefaultBranch))
			assert.Equal(t, "remote", r.Git("-C", remote, "show",
				"refs/heads/"+harness.DefaultBranch+":packages/core/main.txt"))
			assert.Equal(t, "release", r.Git("show", "core@0.1.0:packages/core/main.txt"),
				"the local immutable tag retains the published side")
			assert.Empty(t, r.Git("-C", remote, "for-each-ref", "--format=%(refname)", "refs/heads/release-conflicts"))
		})
	}
}

// TestFinalSourceTagPushFailureKeepsTheAlreadyPushedBranch: source recording
// pushes its branch and immutable tag separately. If only the tag push fails,
// the advanced source branch and local tag are truthful partial publication
// records, while the source remote has no baseline tag and control must not
// checkpoint it. Explicitly pushing that exact tag and checkpoint makes retry
// a publication no-op.
func TestFinalSourceTagPushFailureKeepsTheAlreadyPushedBranch(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control
	sourceRoot := filepath.Join(canonicalRoot(t, control), "sources", "lib")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C " + sourceRoot + " *push -- origin refs/tags/lib@0.1.0:refs/tags/lib@0.1.0*",
	})

	failed := control.CommandEnv(fault.Env())
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "source push failed; repair source records before advancing the control gitlink")
	assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, failed.Stdout, `"status":"published"`)
	assert.Equal(t, 1, fault.Matches())
	assert.Equal(t, 1, finalPublishCount(t, control))
	sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	require.NotEqual(t, fleet.sourceBefore, sourceAfter)
	assert.Equal(t, sourceAfter,
		control.Git("-C", fleet.sourceRemote, "rev-parse", "refs/heads/"+harness.DefaultBranch),
		"the source branch push landed before the tag push failed")
	assert.Empty(t, control.Git("-C", fleet.sourceRemote, "tag", "--list", "lib@0.1.0"))
	assert.NotEmpty(t, strings.TrimSpace(control.Git("-C", "sources/lib", "tag", "--list", "lib@0.1.0")))
	assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"),
		"control never checkpoints a source whose tag is absent remotely")

	control.Git("-C", "sources/lib", "push", "-q", "origin",
		"refs/tags/lib@0.1.0:refs/tags/lib@0.1.0")
	control.Git("add", "sources/lib")
	control.Git("commit", "-q", "-m", "chore(release): repair lib@0.1.0 checkpoint")
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, 1, finalPublishCount(t, control), "the repaired immutable tag prevents duplicate publication")
	assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
}
