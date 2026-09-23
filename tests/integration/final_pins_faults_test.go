// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final production-review faults around transient fleet pins, local mutation
// locks, and the fixed ref snapshot. These scenarios drive the compiled binary
// over real source/control repositories; only selected Git reads use GitFault.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func rewriteFinalFleetConfig(t *testing.T, fleet finalFaultFleet, publish string, verify bool) {
	t.Helper()
	raw, err := os.ReadFile(fleet.control.Path("dispat.json"))
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfg))
	scripts, ok := cfg["scripts"].(map[string]any)
	require.True(t, ok)
	scripts["publish"] = []string{publish}
	if verify {
		commit, ok := cfg["commit"].(map[string]any)
		require.True(t, ok)
		commit["verify"] = true
		overrides, ok := cfg["repositoryOverrides"].(map[string]any)
		require.True(t, ok)
		source, ok := overrides["lib-source"].(map[string]any)
		require.True(t, ok)
		sourceCommit, ok := source["commit"].(map[string]any)
		require.True(t, ok)
		sourceCommit["verify"] = true
	}
	writePolyrepoJSON(t, fleet.control, "dispat.json", cfg)
	fleet.control.Commit("chore: configure the final fault boundary")
	fleet.control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
}

func repairFinalPinRecord(t *testing.T, fleet finalFaultFleet, sourcePin string) {
	t.Helper()
	control := fleet.control
	control.Git("-C", "sources/lib", "tag", "-a", "lib@0.1.0", "-m", "release lib@0.1.0", sourcePin)
	control.Git("-C", "sources/lib", "push", "-q", "origin",
		"HEAD:refs/heads/"+harness.DefaultBranch,
		"refs/tags/lib@0.1.0:refs/tags/lib@0.1.0")
	control.Git("add", "sources/lib")
	control.Git("commit", "-q", "-m", "chore(release): repair lib@0.1.0 checkpoint")
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, 1, finalPublishCount(t, control),
		"the repaired immutable record prevents a second upload")
	assert.Equal(t, sourcePin, control.Git("rev-parse", "HEAD:sources/lib"))
}

// TestFinalLivePinWriteFaultsStopBeforeTagAndCheckpoint: publication has
// completed when the source recorder commits and atomically advertises that
// revision to later legs. If the private coordinator becomes unwritable or
// its destination is replaced by a directory, the commit remains reviewable
// but no tag or control checkpoint may claim the incomplete transaction.
// Explicitly recording that exact commit makes retry a publication no-op.
func TestFinalLivePinWriteFaultsStopBeforeTagAndCheckpoint(t *testing.T) {
	digest := sha256.Sum256([]byte("lib-source"))
	pinName := hex.EncodeToString(digest[:]) + ".pin"
	for _, tc := range []struct {
		name, sabotage string
		restore        func(*testing.T, string)
	}{
		{
			name:     "coordinator becomes unwritable",
			sabotage: `chmod 500 "$DISPAT_INTERNAL_WORKSPACE_LIVE_PINS"`,
			restore: func(t *testing.T, dir string) {
				t.Helper()
				if _, err := os.Stat(dir); err == nil {
					require.NoError(t, os.Chmod(dir, 0o700))
				}
			},
		},
		{
			name:     "atomic destination becomes a directory",
			sabotage: `mkdir "$DISPAT_INTERNAL_WORKSPACE_LIVE_PINS/` + pinName + `"`,
			restore:  func(*testing.T, string) {},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newFinalFaultFleet(t)
			control := fleet.control
			publish := `printf '%s\n' "$DISPAT_INTERNAL_WORKSPACE_LIVE_PINS" > ../../../../live-pin-path; ` +
				`printf 'published\n' >> ../../publish-count; ` + tc.sabotage
			rewriteFinalFleetConfig(t, fleet, publish, false)
			controlBefore := control.Git("rev-parse", "HEAD")

			failed := control.Release()
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			combined := failed.Stdout + failed.Stderr
			assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s", failed.Stdout)
			assert.Contains(t, combined, "source release commit failed")
			assert.Contains(t, failed.Stdout, `"status":"published"`)
			assert.Equal(t, 1, finalPublishCount(t, control))
			sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
			require.NotEqual(t, fleet.sourceBefore, sourceAfter, "the truthful source commit survives")
			assert.Empty(t, polyrepoTags(control, "sources/lib"), "no tag may outrun the failed live pin")
			assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
			assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))

			path, err := os.ReadFile(control.Path("live-pin-path"))
			require.NoError(t, err)
			tc.restore(t, strings.TrimSpace(string(path)))
			repairFinalPinRecord(t, fleet, sourceAfter)
		})
	}
}

// TestFinalMutationLockPathCollisionRefusesBeforePlanning: every snapshot and
// record transaction opens the same repository-local advisory lock. A
// directory occupying that path is real filesystem corruption; release must
// report E330 before publication and recover cleanly once the collision is
// removed.
func TestFinalMutationLockPathCollisionRefusesBeforePlanning(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control
	common := control.Git("-C", "sources/lib", "rev-parse", "--path-format=absolute", "--git-common-dir")
	lockPath := filepath.Join(strings.TrimSpace(common), "dispat-mutation.lock")
	require.NoError(t, os.Mkdir(lockPath, 0o700))

	failed := control.Release()
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.True(t, harness.IsCodePresent(failed.Events, "E330"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, combined, "opening Git mutation lock")
	assert.NotContains(t, combined, "release plan ready")
	assert.NoFileExists(t, control.Path("sources", "lib", "publish-count"))
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))

	require.NoError(t, os.Remove(lockPath))
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, 1, finalPublishCount(t, control))
}

// TestFinalImmutableBaselineRefDriftNeedsExactRepair: an immutable baseline is
// part of the fixed snapshot as a ref object, not only as a peeled commit. A
// hook deleting it or replacing its annotation while retaining the same
// commit must stop publication. Restoring the exact original tag object makes
// the unchanged plan safe to run once.
func TestFinalImmutableBaselineRefDriftNeedsExactRepair(t *testing.T) {
	for _, tc := range []struct {
		name, hook, want string
	}{
		{name: "baseline deleted", hook: "git tag -d lib@1.0.0", want: "was deleted"},
		{name: "annotation replaced at the same commit",
			hook: "git tag -f -a lib@1.0.0 -m replacement HEAD~1", want: "moved from"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := harness.New(t)
			source.SeedPackage("packages", "lib")
			source.Commit("feat(lib): baseline")
			source.Git("tag", "-a", "lib@1.0.0", "-m", "original baseline")
			source.WriteFile("packages/lib/next.txt", "next\n")
			source.Commit("feat(lib): next feature")
			control := harness.New(t)
			addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
			cfg := polyrepoFile()
			cfg["concurrency"] = []int{1}
			cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
			cfg["scripts"] = map[string]any{
				"build": []string{"echo building"}, "drift": []string{tc.hook},
				"publish": []string{"printf 'published\\n' >> ../../publish-count"},
			}
			cfg["flow"] = map[string]any{
				"build": []string{"build"}, "beforePublish": []string{"drift"}, "publish": []string{"publish"},
			}
			writePolyrepoJSON(t, control, "dispat.json", cfg)
			control.Commit("chore: configure exact baseline drift")
			originalTagObject := control.Git("-C", "sources/lib", "rev-parse", "refs/tags/lib@1.0.0")

			failed := control.Release()
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			combined := failed.Stdout + failed.Stderr
			assert.True(t, harness.IsCodePresent(failed.Events, "E330"), "stdout:\n%s", failed.Stdout)
			assert.Contains(t, combined, "relevant tag lib@1.0.0")
			assert.Contains(t, combined, tc.want)
			assert.NoFileExists(t, control.Path("sources", "lib", "publish-count"))
			assert.NotContains(t, polyrepoTags(control, "sources/lib"), "lib@1.1.0")

			control.Git("-C", "sources/lib", "update-ref", "refs/tags/lib@1.0.0", originalTagObject)
			cfg["flow"] = map[string]any{
				"build": []string{"build"}, "publish": []string{"publish"},
			}
			writePolyrepoJSON(t, control, "dispat.json", cfg)
			control.Commit("chore: remove the completed drift probe")
			retry := control.Release()
			require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
			assert.Equal(t, 1, finalPublishCount(t, control))
			assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@1.1.0")
		})
	}
}

// TestFinalSourceRecordPreflightGitFaultsRefuseBeforePublication: a pushable
// source has to prove its branch, remote position, and release paths before
// package work. Failure of any inquiry leaves every repository unchanged; a
// healthy retry then publishes and records exactly once.
func TestFinalSourceRecordPreflightGitFaultsRefuseBeforePublication(t *testing.T) {
	for _, tc := range []struct {
		name, pattern string
	}{
		{name: "current branch", pattern: "*rev-parse --abbrev-ref HEAD*"},
		{name: "remote position", pattern: "*ls-remote origin refs/heads/" + harness.DefaultBranch + "*"},
		{name: "release paths", pattern: "*status --porcelain=v1 -z --untracked-files=all --*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newFinalFaultFleet(t)
			rewriteFinalFleetConfig(t, fleet, "printf 'published\\n' >> ../../publish-count", true)
			control := fleet.control
			sourceRoot := filepath.Join(canonicalRoot(t, control), "sources", "lib")
			beforeControl := control.Git("rev-parse", "HEAD")
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: "*-C " + sourceRoot + " *" + tc.pattern,
			})

			failed := control.CommandEnv(fault.Env())
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			combined := failed.Stdout + failed.Stderr
			assert.Contains(t, combined, harness.GitFaultMarker)
			assert.Equal(t, 1, fault.Matches())
			assert.NotContains(t, combined, `"status":"published"`)
			assert.NoFileExists(t, control.Path("sources", "lib", "publish-count"))
			assert.Empty(t, polyrepoTags(control, "sources/lib"))
			assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
			assert.Equal(t, beforeControl, control.Git("rev-parse", "HEAD"))

			retry := control.Release()
			require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
			assert.Equal(t, 1, finalPublishCount(t, control))
		})
	}
}

// The fixed fleet snapshot must lock the real Git common directory before it
// plans or publishes. A successful but malformed rev-parse reply must not
// redirect that lock to the worktree or turn an absent directory into an
// apparently valid publication boundary.
func TestFinalMutationCommonDirectoryRepliesRefuseBeforePublication(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want string
	}{
		{name: "empty reply", reply: "\n", want: "empty common directory"},
		{name: "missing absolute directory", reply: filepath.Join(t.TempDir(), "missing-git-common"), want: "resolving Git common directory path"},
		{name: "relative worktree directory", reply: ".", want: "absolute Git common directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newFinalFaultFleet(t)
			control := fleet.control
			sourceRoot := filepath.Join(canonicalRoot(t, control), "sources", "lib")
			controlBefore := control.Git("rev-parse", "HEAD")
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: "*-C " + sourceRoot + " *rev-parse --path-format=absolute --git-common-dir*",
				Output:  tc.reply,
			})

			failed := control.CommandEnv(fault.Env())
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			combined := failed.Stdout + failed.Stderr
			assert.Contains(t, combined, tc.want)
			assert.Equal(t, 1, fault.Matches())
			assert.NoFileExists(t, control.Path("sources", "lib", "publish-count"))
			assert.Empty(t, polyrepoTags(control, "sources/lib"))
			assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
			assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))

			retry := control.Release()
			require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
			assert.Equal(t, 1, finalPublishCount(t, control))
		})
	}
}

// TestFinalMutationLockBreakAfterPublicationLeavesNoFalseRecord: the fixed
// snapshot was valid when package work began, but the advisory lock path is
// replaced after upload and before native recording. The package is already
// published, yet no source commit, tag, or checkpoint can be written. Once
// the filesystem is repaired, the absent tag makes retry upload and record
// the release once more.
func TestFinalMutationLockBreakAfterPublicationLeavesNoFalseRecord(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control
	common := strings.TrimSpace(control.Git("-C", "sources/lib", "rev-parse",
		"--path-format=absolute", "--git-common-dir"))
	lockPath := filepath.Join(common, "dispat-mutation.lock")
	publish := `printf 'published\n' >> ../../publish-count; ` +
		`lock=$(git rev-parse --path-format=absolute --git-common-dir)/dispat-mutation.lock; ` +
		`rm -f "$lock"; mkdir "$lock"`
	rewriteFinalFleetConfig(t, fleet, publish, false)
	controlBefore := control.Git("rev-parse", "HEAD")

	failed := control.Release()
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, combined, "opening Git mutation lock")
	assert.Contains(t, failed.Stdout, `"status":"published"`)
	assert.Equal(t, 1, finalPublishCount(t, control))
	assert.Equal(t, fleet.sourceBefore, control.Git("-C", "sources/lib", "rev-parse", "HEAD"))
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
	assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))

	require.NoError(t, os.Remove(lockPath))
	control.Git("-C", "sources/lib", "reset", "--hard", "HEAD")
	_ = os.Remove(control.Path("sources", "lib", "packages", "lib", "CHANGELOG.md"))
	rewriteFinalFleetConfig(t, fleet, "printf 'published\\n' >> ../../publish-count", false)
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, 2, finalPublishCount(t, control), "the unrecorded upload has no durable baseline")
}

// TestFinalSourceTagWriteFaultKeepsCommitBelowTheCheckpoint: the source
// release commit is already truthful when Git refuses its immutable tag. The
// control gitlink must remain old. Explicitly publishing that exact commit and
// checkpoint makes retry a no-op instead of uploading the package again.
func TestFinalSourceTagWriteFaultKeepsCommitBelowTheCheckpoint(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control
	sourceRoot := filepath.Join(canonicalRoot(t, control), "sources", "lib")
	controlBefore := control.Git("rev-parse", "HEAD")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C " + sourceRoot + " *tag *-a lib@0.1.0*",
	})

	failed := control.CommandEnv(fault.Env())
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, failed.Stdout, `"status":"published"`)
	assert.Equal(t, 1, fault.Matches())
	assert.Equal(t, 1, finalPublishCount(t, control))
	sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	require.NotEqual(t, fleet.sourceBefore, sourceAfter)
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
	assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))

	repairFinalPinRecord(t, fleet, sourceAfter)
}

// TestFinalCheckpointMutationLockFailurePreservesTheRemoteSource: the source
// commit, tag, branch, and live pin are durable before the source afterPush
// hook damages the control repository's local mutation-lock path. Checkpoint
// acquisition must fail without advancing control. Repairing that one gitlink
// and retrying does not republish the source.
func TestFinalCheckpointMutationLockFailurePreservesTheRemoteSource(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control
	common := strings.TrimSpace(control.Git("rev-parse", "--path-format=absolute", "--git-common-dir"))
	lockPath := filepath.Join(common, "dispat-mutation.lock")
	raw, err := os.ReadFile(control.Path("dispat.json"))
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfg))
	scripts := cfg["scripts"].(map[string]any)
	scripts["break-control-lock"] = []string{
		`rm -f ` + harness.ShQuote(lockPath) + `; mkdir ` + harness.ShQuote(lockPath),
	}
	cfg["run"] = map[string]any{"afterPush": []string{"break-control-lock"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure checkpoint lock damage")
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	controlBefore := control.Git("rev-parse", "HEAD")

	failed := control.Release()
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, combined, "control checkpoint failed")
	assert.Contains(t, combined, "opening Git mutation lock")
	assert.Equal(t, 1, finalPublishCount(t, control))
	sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	assert.Equal(t, sourceAfter, control.Git("-C", fleet.sourceRemote, "rev-parse", "lib@0.1.0^{commit}"))
	assert.Equal(t, sourceAfter,
		control.Git("-C", fleet.sourceRemote, "rev-parse", "refs/heads/"+harness.DefaultBranch))
	assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
	assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))

	require.NoError(t, os.Remove(lockPath))
	control.Git("add", "sources/lib")
	control.Git("commit", "-q", "-m", "chore(release): repair lib@0.1.0 checkpoint")
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, 1, finalPublishCount(t, control))
}
