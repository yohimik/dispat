// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final release fault scenarios exercise failures that cannot be assembled as
// repository state. The binary still drives real repositories and remotes;
// harness.GitFault replaces one selected Git invocation and passes every other
// call to the host Git.

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

type finalFaultFleet struct {
	control       *harness.Repo
	sourceRemote  string
	controlRemote string
	sourceBefore  string
	controlBefore string
}

// newFinalFaultFleet is one orchestrated source with both source and control
// recording enabled. A successful publication must create and push the source
// commit and tag before the control gitlink may advance.
func newFinalFaultFleet(t *testing.T) finalFaultFleet {
	t.Helper()
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap the provider")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	sourceRemote := filepath.Join(t.TempDir(), "source.git")
	control.Git("init", "-q", "--bare", sourceRemote)
	control.Git("-C", sourceRemote, "symbolic-ref", "HEAD", "refs/heads/"+harness.DefaultBranch)
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", sourceRemote)
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	controlRemote := control.AddBareRemote()

	cfg := polyrepoFile()
	cfg["concurrency"] = []int{1}
	cfg["logLevel"] = "debug"
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["changelog"] = map[string]any{"enabled": true}
	cfg["commit"] = map[string]any{
		"enabled": true, "push": true, "remote": "origin",
		"branch": harness.DefaultBranch, "verify": false,
	}
	cfg["repositoryOverrides"] = map[string]any{
		"lib-source": map[string]any{"commit": map[string]any{
			"enabled": true, "push": true, "remote": "origin",
			"branch": harness.DefaultBranch, "verify": false,
		}},
	}
	cfg["scripts"] = map[string]any{
		"build":   []string{"echo building"},
		"publish": []string{"printf 'published\\n' >> ../../publish-count"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure durable source and control records")
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	return finalFaultFleet{
		control:       control,
		sourceRemote:  sourceRemote,
		controlRemote: controlRemote,
		sourceBefore:  control.Git("-C", "sources/lib", "rev-parse", "HEAD"),
		controlBefore: control.Git("rev-parse", "HEAD"),
	}
}

func finalPublishCount(t *testing.T, control *harness.Repo) int {
	t.Helper()
	data, err := os.ReadFile(control.Path("sources", "lib", "publish-count"))
	require.NoError(t, err)
	return strings.Count(string(data), "published\n")
}

// TestFinalReleaseLockObjectFailureCleansOrReportsTheAttemptRef: acquisition
// has already created its private local attempt tag when resolving that tag's
// object fails. The run must stop before planning, remove the attempt on a
// live cleanup context, and leave no remote lock. If Git refuses the cleanup
// too, the remaining local ref is durable state and the log must name it.
func TestFinalReleaseLockObjectFailureCleansOrReportsTheAttemptRef(t *testing.T) {
	newRepo := func(t *testing.T) (*harness.Repo, string) {
		t.Helper()
		r := harness.New(t)
		cfg := libsConfig("echo built > ../../built", 1)
		cfg.LogLevel = "debug"
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): release only after the lock is proven")
		return r, r.AddBareRemote()
	}

	t.Run("the private attempt is removed", func(t *testing.T) {
		r, remote := newRepo(t)
		fault := harness.NewGitFault(t, harness.GitFault{
			Pattern: "*rev-parse refs/tags/dispat-release-lock-attempt-*",
		})

		res := r.CommandEnv(append(harness.LockEnabled, fault.Env()...))
		assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		combined := res.Stdout + res.Stderr
		assert.Contains(t, combined, "resolving the release lock object")
		assert.Contains(t, combined, harness.GitFaultMarker)
		assert.Equal(t, 1, fault.Matches(), "only the selected object lookup failed")
		assert.Empty(t, r.Git("tag", "--list", "dispat-release-lock-attempt-*"),
			"a failed acquisition leaves no private attempt ref")
		assert.False(t, remoteHoldsLock(t, remote), "no lock object was pushed")
		assert.NoFileExists(t, r.Path("built"), "planning and package work never started")
		assert.Zero(t, r.TagCount("core@"))
	})

	t.Run("a cleanup refusal names the stranded ref", func(t *testing.T) {
		r, remote := newRepo(t)
		fault := harness.NewGitFault(t, harness.GitFault{
			Pattern: "*dispat-release-lock-attempt-*",
			Nth:     2,
			Onward:  true,
		})

		res := r.CommandEnv(append(harness.LockEnabled, fault.Env()...))
		assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		combined := res.Stdout + res.Stderr
		assert.Contains(t, combined, "could not remove the local lock tag after resolving its object failed")
		assert.Contains(t, combined, harness.GitFaultMarker)
		assert.Equal(t, 3, fault.Matches(), "create, resolve, and cleanup were the only matching calls")
		attempts := strings.Fields(r.Git("tag", "--list", "dispat-release-lock-attempt-*"))
		require.Len(t, attempts, 1, "the refused cleanup leaves exactly its own attempt ref")
		assert.False(t, remoteHoldsLock(t, remote), "the failed acquisition never owned the remote lock")
		assert.NoFileExists(t, r.Path("built"))
		assert.Zero(t, r.TagCount("core@"))
	})
}

// TestFinalOrchestratedSourceCommitFaultNeedsAReviewedRetry: publication has
// happened when the source's native record commit runs. A Git failure there
// must leave no tag or control checkpoint, report the package as published,
// and retain generated files for inspection. After the operator discards that
// incomplete record, the same release converges cleanly.
func TestFinalOrchestratedSourceCommitFaultNeedsAReviewedRetry(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control
	sourceRoot := filepath.Join(canonicalRoot(t, control), "sources", "lib")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C " + sourceRoot + " *commit --only *",
	})

	failed := control.CommandEnv(fault.Env())
	assert.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "source release commit failed")
	assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, failed.Stdout, `"status":"published"`,
		"a native record failure does not relabel an uploaded package as failed")
	assert.Equal(t, 1, fault.Matches())
	assert.Equal(t, 1, finalPublishCount(t, control))
	assert.Empty(t, polyrepoTags(control, "sources/lib"), "no tag can precede the failed source commit")
	assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"),
		"the control repository does not record an incomplete source")
	assert.Equal(t, fleet.controlBefore, control.Git("rev-parse", "HEAD"))

	// A failed native commit intentionally retains the staged changelog for
	// review. Remove that incomplete generated record before retrying.
	control.Git("-C", "sources/lib", "reset", "--hard", "HEAD")
	_ = os.Remove(control.Path("sources", "lib", "packages", "lib", "CHANGELOG.md"))
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, 2, finalPublishCount(t, control), "the untagged publication is retried once")
	assert.Equal(t, 1, len(polyrepoTags(control, "sources/lib")))
	sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	assert.Equal(t, sourceAfter, control.Git("-C", fleet.sourceRemote, "rev-parse", "lib@0.1.0^{commit}"))
	assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
}

// TestFinalOrchestratedTagVerificationFaultNeverAdvancesControl: the source
// tag exists locally before dispat verifies its target and pushes it. If that
// verification cannot read the tag, the run must report a critical and keep
// both remotes and the control gitlink at their prior revisions. Publishing
// the retained source records and checkpoint explicitly lets retry converge
// without uploading the package again.
func TestFinalOrchestratedTagVerificationFaultNeverAdvancesControl(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control
	sourceRoot := filepath.Join(canonicalRoot(t, control), "sources", "lib")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C " + sourceRoot + " *rev-parse refs/tags/lib@0.1.0^{commit}*",
		Nth:     1,
	})

	failed := control.CommandEnv(fault.Env())
	assert.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, failed.Stdout, `"status":"published"`)
	assert.Equal(t, 1, fault.Matches())
	assert.Equal(t, 1, finalPublishCount(t, control))
	sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	require.NotEqual(t, fleet.sourceBefore, sourceAfter)
	assert.Equal(t, sourceAfter, control.Git("-C", "sources/lib", "rev-parse", "lib@0.1.0^{commit}"),
		"the local source tag remains as the truthful publication record")
	assert.Empty(t, control.Git("-C", fleet.sourceRemote, "tag", "--list", "lib@0.1.0"),
		"verification failed before any source ref was pushed")
	assert.Equal(t, fleet.sourceBefore,
		control.Git("-C", fleet.sourceRemote, "rev-parse", "refs/heads/"+harness.DefaultBranch))
	assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"),
		"the control repository never advances to an unverified source record")
	assert.Equal(t, fleet.controlBefore, control.Git("rev-parse", "HEAD"))

	control.Git("-C", "sources/lib", "push", "-q", "origin",
		"HEAD:refs/heads/"+harness.DefaultBranch,
		"refs/tags/lib@0.1.0:refs/tags/lib@0.1.0")
	control.Git("add", "sources/lib")
	control.Git("commit", "-q", "-m", "chore(release): repair lib@0.1.0 checkpoint")
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, 1, finalPublishCount(t, control), "the repaired tag prevents duplicate publication")
	assert.Equal(t, 1, len(polyrepoTags(control, "sources/lib")))
	assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
}

// TestFinalOrchestratedCheckpointCommitFaultPreservesTheRemoteSourceRecord:
// the source commit, branch and tag are already durable when the control
// checkpoint is created. A control Git failure must preserve all three and
// keep the old gitlink, so an explicit checkpoint repair followed by retry
// never republishes the package.
func TestFinalOrchestratedCheckpointCommitFaultPreservesTheRemoteSourceRecord(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control
	controlRoot := canonicalRoot(t, control)
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C " + controlRoot + " *commit --only *sources/lib*",
	})

	failed := control.CommandEnv(fault.Env())
	assert.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "control checkpoint failed")
	assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s", failed.Stdout)
	assert.Equal(t, 1, fault.Matches())
	assert.Equal(t, 1, finalPublishCount(t, control))
	sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	require.NotEqual(t, fleet.sourceBefore, sourceAfter)
	assert.Equal(t, sourceAfter, control.Git("-C", fleet.sourceRemote, "rev-parse", "lib@0.1.0^{commit}"),
		"the provider tag is durable before checkpointing starts")
	assert.Equal(t, sourceAfter, control.Git("-C", fleet.sourceRemote, "rev-parse", "refs/heads/"+harness.DefaultBranch))
	assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"),
		"a failed checkpoint never claims the new source revision")
	assert.Equal(t, fleet.controlBefore, control.Git("rev-parse", "HEAD"))

	control.Git("add", "sources/lib")
	control.Git("commit", "-q", "-m", "chore(release): repair lib@0.1.0 checkpoint")
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, 1, finalPublishCount(t, control), "the durable source tag prevents a second publication")
	assert.Equal(t, 1, len(polyrepoTags(control, "sources/lib")))
	assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
}

// TestFinalOrchestratedControlPushFaultKeepsTheLocalCheckpoint: the final
// control push is the last external write in an orchestrated record. Its
// failure must leave the source remote complete and the local checkpoint
// available for an operator to push; retry after that repair is a no-op for
// the already published provider.
func TestFinalOrchestratedControlPushFaultKeepsTheLocalCheckpoint(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control
	controlRoot := canonicalRoot(t, control)
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C " + controlRoot + " *push -- origin HEAD:refs/heads/" + harness.DefaultBranch + "*",
	})

	failed := control.CommandEnv(fault.Env())
	assert.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "control checkpoint failed")
	assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s", failed.Stdout)
	assert.Equal(t, 1, fault.Matches())
	assert.Equal(t, 1, finalPublishCount(t, control))
	sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	controlAfter := control.Git("rev-parse", "HEAD")
	require.NotEqual(t, fleet.controlBefore, controlAfter, "the checkpoint commit survives its push failure")
	assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
	assert.Equal(t, sourceAfter, control.Git("-C", fleet.sourceRemote, "rev-parse", "lib@0.1.0^{commit}"))
	assert.Equal(t, fleet.controlBefore,
		control.Git("-C", fleet.controlRemote, "rev-parse", "refs/heads/"+harness.DefaultBranch),
		"the refused push did not advance the control remote")

	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, 1, finalPublishCount(t, control), "repair and retry do not republish the provider")
	assert.Equal(t, 1, len(polyrepoTags(control, "sources/lib")))
	assert.Equal(t, controlAfter,
		control.Git("-C", fleet.controlRemote, "rev-parse", "refs/heads/"+harness.DefaultBranch))
}

// TestFinalTagSnapshotReadFaultsRefuseAnUnprovenFleet: the fixed ref snapshot
// is read once before planning and again before package work. Failure at either
// boundary must be E330, must not run the publish script, and must leave no
// partial source or control record. Once Git answers again, the unchanged
// fleet publishes exactly once and both durable repositories converge.
func TestFinalTagSnapshotReadFaultsRefuseAnUnprovenFleet(t *testing.T) {
	for _, tc := range []struct {
		name string
		nth  int
		want string
	}{
		{name: "initial fleet snapshot", nth: 1, want: "capturing fixed tag snapshot"},
		{name: "pre-publish validation", nth: 2, want: "reading relevant tag refs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newFinalFaultFleet(t)
			control := fleet.control
			sourceRoot := filepath.Join(canonicalRoot(t, control), "sources", "lib")
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: "*-C " + sourceRoot + " *for-each-ref *refs/tags*",
				Nth:     tc.nth,
			})

			failed := control.CommandEnv(fault.Env())
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			combined := failed.Stdout + failed.Stderr
			assert.Contains(t, combined, harness.GitFaultMarker)
			assert.Contains(t, combined, tc.want)
			assert.True(t, harness.IsCodePresent(failed.Events, "E330"), "stdout:\n%s", failed.Stdout)
			assert.Equal(t, tc.nth, fault.Matches(), "the selected snapshot read is reached exactly once")
			assert.NoFileExists(t, control.Path("sources", "lib", "publish-count"))
			assert.Empty(t, polyrepoTags(control, "sources/lib"))
			assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
			assert.Equal(t, fleet.controlBefore, control.Git("rev-parse", "HEAD"))

			retry := control.Release()
			require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
			assert.Equal(t, 1, finalPublishCount(t, control), "a refused fleet never consumed its publication")
			sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
			assert.Equal(t, sourceAfter,
				control.Git("-C", fleet.sourceRemote, "rev-parse", "lib@0.1.0^{commit}"))
			assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
		})
	}
}

// newFinalRecoveryRepo publishes one monorepo package while an unrelated
// commit lands on its remote. The first push is therefore rejected and the
// finalizer has to inspect and merge the remote before it can make the local
// release commit and tag durable there.
func newFinalRecoveryRepo(t *testing.T) (*harness.Repo, string) {
	t.Helper()
	r := harness.New(t)
	remote := r.AddBareRemote()
	cfg := libsConfig(midReleasePush(t, remote, "docs: landed while releasing", "REMOTE.md", "remote\n"), 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	cfg.Scripts["publish"] = models.Script{"printf 'published\\n' >> ../../publish-count"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): publish before recovering the remote")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	return r, remote
}

// repairFinalRecovery completes the same non-conflicting merge and ref push
// which the failed finalizer owed. A normal retry can then prove that the
// release tag prevents a second upload.
func repairFinalRecovery(t *testing.T, r *harness.Repo) {
	t.Helper()
	r.Git("fetch", "-q", "origin", harness.DefaultBranch)
	r.Git("merge", "-q", "--no-edit", "origin/"+harness.DefaultBranch)
	r.Git("push", "-q", "origin",
		"HEAD:refs/heads/"+harness.DefaultBranch,
		"refs/tags/core@0.1.0:refs/tags/core@0.1.0")
	retry := r.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	data, err := os.ReadFile(r.Path("publish-count"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "published\n"),
		"the repaired durable tag prevents a duplicate upload")
}

// TestFinalRecoveryReadFaultsKeepThePublishedRecordLocal: the package and its
// local release record already exist when recovery starts. If the remote tag
// inventory or the recovery fetch fails, the finalizer must report E224 and
// leave the immutable tag local without advancing either remote ref. Explicit
// repair makes retry a publication no-op.
func TestFinalRecoveryReadFaultsKeepThePublishedRecordLocal(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, want string
	}{
		{
			name:    "remote tag inventory",
			pattern: "*ls-remote --tags origin*",
			want:    "reading origin's tags before pushing the release again",
		},
		{
			name:    "recovery fetch",
			pattern: "*fetch --no-tags origin " + harness.DefaultBranch + "*",
			want:    "could not be merged with it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, remote := newFinalRecoveryRepo(t)
			remoteBefore := r.Git("-C", remote, "rev-parse", "refs/heads/"+harness.DefaultBranch)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: tc.pattern})

			failed := r.CommandEnv(fault.Env())
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			combined := failed.Stdout + failed.Stderr
			assert.Contains(t, combined, harness.GitFaultMarker)
			assert.Contains(t, combined, tc.want)
			assert.True(t, harness.IsCodePresent(failed.Events, "E224"), "stdout:\n%s", failed.Stdout)
			assert.Contains(t, failed.Stdout, `"status":"published"`)
			assert.Equal(t, 1, fault.Matches())
			assert.True(t, r.IsTagged("core@0.1.0"), "the local immutable record survives")
			release := r.Git("rev-list", "-n", "1", "core@0.1.0")
			assert.Equal(t, release, r.Git("rev-parse", "HEAD"), "recovery never rewrites the release commit")
			assert.Empty(t, r.Git("-C", remote, "tag", "--list", "core@0.1.0"))
			assert.NotEqual(t, remoteBefore,
				r.Git("-C", remote, "rev-parse", "refs/heads/"+harness.DefaultBranch),
				"the concurrent commit is durable while the release branch remains local")

			repairFinalRecovery(t, r)
			assert.Equal(t, release, r.Git("-C", remote, "rev-parse", "core@0.1.0^{commit}"))
		})
	}
}

// TestFinalDeferredTagWriteFaultRetainsTheReleaseCommit: commit mode defers
// tagging until after publication and the release commit. A direct Git write
// failure there is E220, leaves that commit and its changelog available for
// review, and leaves no false tag. With no durable tag the repaired retry must
// publish once more, then create the record successfully.
func TestFinalDeferredTagWriteFaultRetainsTheReleaseCommit(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig("echo built", 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	cfg.Scripts["publish"] = models.Script{"printf 'published\\n' >> ../../publish-count"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): publish before the deferred tag")
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "* tag *-a core@0.1.0 *"})

	failed := r.CommandEnv(fault.Env())
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.True(t, harness.IsCodePresent(failed.Events, "E220"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, failed.Stdout, `"status":"published"`)
	assert.Equal(t, 1, fault.Matches())
	assert.False(t, r.IsTagged("core@0.1.0"), "a failed tag write creates no baseline")
	assert.Contains(t, r.Git("log", "-1", "--format=%s"), "chore(release): core@0.1.0")
	assert.FileExists(t, r.Path("packages", "core", "CHANGELOG.md"))

	retry := r.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	data, err := os.ReadFile(r.Path("publish-count"))
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(data), "published\n"),
		"without the durable tag the repaired run must upload again")
	assert.True(t, r.IsTagged("core@0.1.0"), "the repaired run establishes the missing baseline")
}

// TestFinalCheckpointTreeRepliesCannotInventAControlRecord: checkpointing
// compares the old gitlink with the durable source revision before writing a
// control commit. A failed or malformed ls-tree reply must not be interpreted
// as "different" and advanced past; the source record remains durable while
// control stays at the exact pre-release commit. An explicit checkpoint repair
// makes retry a publication no-op.
func TestFinalCheckpointTreeRepliesCannotInventAControlRecord(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fault  harness.GitFault
		marker bool
	}{
		{
			name:   "tree read fails",
			fault:  harness.GitFault{Pattern: "*ls-tree -z HEAD -- sources/lib*"},
			marker: true,
		},
		{
			name: "tree entry is not a gitlink",
			fault: harness.GitFault{
				Pattern: "*ls-tree -z HEAD -- sources/lib*",
				Output:  "100644 blob deadbeef\tsources/lib",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newFinalFaultFleet(t)
			control := fleet.control
			fault := harness.NewGitFault(t, tc.fault)

			failed := control.CommandEnv(fault.Env())
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			combined := failed.Stdout + failed.Stderr
			if tc.marker {
				assert.Contains(t, combined, harness.GitFaultMarker)
			}
			assert.Contains(t, combined, "control checkpoint failed")
			assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s", failed.Stdout)
			assert.Equal(t, 1, fault.Matches())
			assert.Equal(t, 1, finalPublishCount(t, control))
			sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
			assert.Equal(t, sourceAfter,
				control.Git("-C", fleet.sourceRemote, "rev-parse", "lib@0.1.0^{commit}"))
			assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
			assert.Equal(t, fleet.controlBefore, control.Git("rev-parse", "HEAD"))

			control.Git("add", "sources/lib")
			control.Git("commit", "-q", "-m", "chore(release): repair lib@0.1.0 checkpoint")
			control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
			retry := control.Release()
			require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
			assert.Equal(t, 1, finalPublishCount(t, control))
			assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
		})
	}
}

func newFinalConflictRepo(t *testing.T) (*harness.Repo, string) {
	t.Helper()
	r := harness.New(t)
	remote := r.AddBareRemote()
	foreign := midReleasePush(t, remote, "docs: conflicting remote edit",
		"packages/core/main.txt", "remote\n")
	cfg := libsConfig("printf 'release\\n' > main.txt && "+foreign, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	cfg.Scripts["publish"] = models.Script{"printf 'published\\n' >> ../../publish-count"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): publish the local side of a later conflict")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	return r, remote
}

// TestFinalConflictSettlementFaultsKeepBothDurableInputs: after publication,
// a concurrent remote edit conflicts with the release commit. Every native
// settlement write is mandatory: resolving this side, preserving theirs on a
// quarantine branch, staging the audit note, and committing the merge. A Git
// failure at any point is E224, retains the immutable local release tag and
// the remote's foreign commit, and leaves the merge visible for repair.
func TestFinalConflictSettlementFaultsKeepBothDurableInputs(t *testing.T) {
	for _, tc := range []struct {
		name, pattern string
		quarantined   bool
	}{
		{name: "stage the release side", pattern: "*add -- packages/core/main.txt*"},
		{name: "push the quarantine branch", pattern: "*push * FETCH_HEAD:refs/heads/release-conflicts/*"},
		{name: "stage the conflict note", pattern: "*add -- *CHANGELOG.md*", quarantined: true},
		{name: "commit the settlement", pattern: "*commit --no-edit*", quarantined: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, remote := newFinalConflictRepo(t)
			before := r.Git("-C", remote, "rev-parse", "refs/heads/"+harness.DefaultBranch)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: tc.pattern})

			failed := r.CommandEnv(fault.Env())
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			combined := failed.Stdout + failed.Stderr
			assert.Contains(t, combined, harness.GitFaultMarker)
			assert.True(t, harness.IsCodePresent(failed.Events, "E224"), "stdout:\n%s", failed.Stdout)
			assert.Contains(t, failed.Stdout, `"status":"published"`)
			assert.Equal(t, 1, fault.Matches())
			assert.True(t, r.IsTagged("core@0.1.0"), "the published release keeps its local immutable tag")
			assert.FileExists(t, r.Path(".git", "MERGE_HEAD"), "the incomplete settlement remains reviewable")
			assert.Empty(t, r.Git("-C", remote, "tag", "--list", "core@0.1.0"))
			assert.NotEqual(t, before,
				r.Git("-C", remote, "rev-parse", "refs/heads/"+harness.DefaultBranch),
				"the foreign side remains the durable remote branch")
			refs := r.Git("-C", remote, "for-each-ref", "--format=%(refname)",
				"refs/heads/release-conflicts")
			assert.Equal(t, tc.quarantined, strings.TrimSpace(refs) != "",
				"the quarantine exists exactly after its push succeeds")
		})
	}
}
