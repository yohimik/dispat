//go:build !windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final execution-boundary scenarios exercise state transitions that only a
// real process can prove: cancellation after a script has changed its working
// tree, and failures that happen during the executor's post-publication tag
// reconciliation rather than while the plan is being assembled.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFinalInterruptedBuildRevertsBeforeExitAndRetriesCleanly: cancellation is
// a separate executor outcome from failure. Once an in-flight build has
// changed both tracked and untracked files, Ctrl-C must cancel it, run the
// rollback on a context detached from that cancellation, and wait for cleanup
// before the process exits. Outcome hooks are failure-only and must stay
// silent. Removing the external hold then makes the same release retryable.
func TestFinalInterruptedBuildRevertsBeforeExitAndRetriesCleanly(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig("", 1)
	cfg.RevertOnFail = models.Bool(true)
	cfg.Scripts["build"] = models.Script{
		`printf 'dirty\n' >> main.txt; printf 'transient\n' > extra.txt; ` +
			`touch ../../build-started; ` +
			`if [ -f ../../hold-build ]; then while :; do sleep 1; done; fi`,
	}
	cfg.Scripts["on-fail"] = models.Script{`touch ../../on-fail-ran`}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"},
		Flow: &models.SpaceFlowConfig{
			Build:   []string{"build"},
			Publish: []string{"publish"},
			OnFail:  []string{"on-fail"},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("hold-build", "hold the first build\n")
	r.Commit("feat(core): interrupt after the build dirties its folder")

	proc := r.StartRelease()
	require.Eventually(t, func() bool {
		_, err := os.Stat(r.Path("build-started"))
		return err == nil
	}, 15*time.Second, 20*time.Millisecond, "the mutating build never started")
	proc.Signal(os.Interrupt)
	interrupted := proc.Wait()

	require.NotZero(t, interrupted.Code, "stdout:\n%s\nstderr:\n%s", interrupted.Stdout, interrupted.Stderr)
	assert.Equal(t, "cancelled", summaryStatuses(interrupted.Events)["core"])
	assert.Zero(t, r.TagCount("core@"), "an interrupted publish has no durable release record")
	assert.NoFileExists(t, r.Path("on-fail-ran"), "cancellation does not launch the failure hook")
	data, err := os.ReadFile(r.Path("packages", "core", "main.txt"))
	require.NoError(t, err)
	assert.Equal(t, "core\n", string(data), "tracked build edits are restored before exit")
	assert.NoFileExists(t, r.Path("packages", "core", "extra.txt"),
		"untracked build output is removed before exit")

	r.Remove("hold-build")
	retry := r.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.True(t, r.IsTagged("core@0.1.0"), "the cancelled release remains owed on retry")
}

// TestFinalRollbackGitFailureLeavesVisibleRepairState: revertOnFail is itself
// a fallible Git operation. If Git refuses its tracked-file restore, the run
// must report that cleanup failure, retain both kinds of build residue for an
// operator to inspect, and create no release tag. Explicitly repairing that
// state makes the unchanged release safely retryable.
func TestFinalRollbackGitFailureLeavesVisibleRepairState(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig("", 1)
	cfg.RevertOnFail = models.Bool(true)
	cfg.Scripts["build"] = models.Script{
		`printf 'dirty\n' >> main.txt; printf 'transient\n' > extra.txt; test ! -f ../../fail-build`,
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): expose a refused rollback")
	r.WriteFile("fail-build", "fail until the residue is reviewed\n")
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*checkout -- packages/core*"})

	res := r.CommandEnv(fault.Env())
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "reverting package folder failed")
	assert.Equal(t, 1, fault.Matches(), "only the rollback's tracked-file restore is refused")
	assert.Zero(t, r.TagCount("core@"), "a failed build and failed rollback establish no release record")
	data, err := os.ReadFile(r.Path("packages", "core", "main.txt"))
	require.NoError(t, err)
	assert.Equal(t, "core\ndirty\n", string(data), "the refused restore leaves tracked evidence visible")
	assert.FileExists(t, r.Path("packages", "core", "extra.txt"),
		"cleanup stops after the refused restore, retaining untracked evidence too")

	r.Git("checkout", "--", "packages/core")
	r.Git("clean", "-fd", "--", "packages/core")
	r.Remove("fail-build")
	retry := r.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.True(t, r.IsTagged("core@0.1.0"), "manual cleanup returns the release to a safe retry")
}

// TestFinalFleetRollbackRefusesARepositoryThatMovedDuringTheBuild: fleet
// rollback is repository-aware and is guarded by the HEAD accepted during
// planning. A failing source script that commits its own edits must not have
// those edits silently checked out or cleaned through the stale snapshot.
// The new commit remains reviewable, no tag or control checkpoint advances,
// and an explicit reset makes the original release retryable.
func TestFinalFleetRollbackRefusesARepositoryThatMovedDuringTheBuild(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap the source package")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = map[string]any{
		"libs": map[string]any{
			"path":         []string{"sources/lib/packages"},
			"revertOnFail": true,
		},
	}
	cfg["scripts"] = map[string]any{
		"build": []string{
			`printf 'nested edit\n' >> main.txt; ` +
				`if [ -f ../../../../move-head ]; then git add main.txt && git commit -m 'nested build commit' && exit 3; fi`,
		},
		"publish": []string{"echo publishing"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure guarded source rollback")
	controlPin := control.Git("rev-parse", "HEAD")
	sourcePin := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	control.WriteFile("move-head", "move the source during the first build\n")

	res := control.Release()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "reverting package folder failed")
	assert.Contains(t, combined, "repository changed after planning")
	assert.Equal(t, "nested build commit", control.Git("-C", "sources/lib", "log", "-1", "--format=%s"),
		"the unplanned source commit remains visible for review")
	assert.NotEqual(t, sourcePin, control.Git("-C", "sources/lib", "rev-parse", "HEAD"))
	assert.Equal(t, controlPin, control.Git("rev-parse", "HEAD"), "the control repository does not checkpoint failed work")
	assert.Empty(t, polyrepoTags(control, "sources/lib"), "a failed build establishes no source release tag")

	control.Git("-C", "sources/lib", "reset", "--hard", sourcePin)
	control.Remove("move-head")
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, []string{"lib@0.1.0"}, polyrepoTags(control, "sources/lib"),
		"repairing the exact planned source revision makes retry safe")
}

// TestFinalPostPublishTagInventoryFailureStillWritesTheReleaseTag: planning
// reads the repository-wide tag inventory first; the executor reads this
// package's tags again only after publication, to recognize a nested tag. A
// transient failure of that second read cannot turn a published package into
// an unrecorded release when the immutable tag write itself still succeeds.
func TestFinalPostPublishTagInventoryFailureStillWritesTheReleaseTag(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig("echo built", 1)
	cfg.LogLevel = "debug"
	cfg.Scripts["publish"] = models.Script{"printf 'published\\n' >> ../../publish-count"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): publish despite a transient tag inventory read")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*tag --list --merged HEAD*",
		Nth:     2,
	})

	res := r.CommandEnv(fault.Env())
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "existing tags could not be listed before tagging")
	assert.Equal(t, 2, fault.Matches(), "planning succeeds and only the executor's second inventory read fails")
	assert.True(t, r.IsTagged("core@0.1.0"), "the successful tag write durably records the publication")

	retry := r.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	data, err := os.ReadFile(r.Path("publish-count"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "published\n"),
		"the durable tag makes the retry converge without publishing twice")
}

// TestFinalExistingTagTargetReadFailurePreservesThePublishedRecord: a release
// stage may create its tag itself. The outer executor then has to prove that
// tag points at its release commit before accepting it as W223. If Git cannot
// resolve the target at that exact boundary, the run reports a critical after
// publication rather than guessing, while retaining the tag for a safe,
// upload-free retry.
func TestFinalExistingTagTargetReadFailurePreservesThePublishedRecord(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig("echo built", 1)
	cfg.Scripts["publish"] = models.Script{
		"printf 'published\\n' >> ../../publish-count",
		"git tag -a core@0.1.0 -m 'release core@0.1.0'",
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): publish and establish the tag inside the flow")
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*rev-parse HEAD^{commit}*"})

	res := r.CommandEnv(fault.Env())
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "already exists and the release target")
	assert.Equal(t, 1, fault.Matches(), "only the post-publication target proof is refused")
	assert.True(t, r.IsTagged("core@0.1.0"), "the flow's immutable release record is retained")
	assert.Equal(t, r.Git("rev-parse", "HEAD"), r.Git("rev-parse", "core@0.1.0^{commit}"))

	retry := r.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	data, err := os.ReadFile(filepath.Join(r.Root, "publish-count"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "published\n"),
		"the retained tag prevents a second upload during recovery")
}
