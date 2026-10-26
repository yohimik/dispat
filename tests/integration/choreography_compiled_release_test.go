// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

const compiledParallelFailureSettleWait = 60 * time.Second
const compiledDrainedAdmissionSeconds = 2
const compiledDrainedMemberEventKind = "member-draining"
const compiledTaskPackageSettledEventKind = "task-package-settled"
const compiledCausalFailureOutcome = "failed"

// A peer that cannot read any canonical task ref signs Drain before pickup.
// The finite admission deadline then signs typed no-claim and lets onFail use
// the first node's slot after package settlement.
func TestChoreographyCompiledReleaseFencesUnclaimedQueueBeforeOnFail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 2,
			Cancel: 5, Admission: compiledDrainedAdmissionSeconds}
		one := 1
		cfg.Execution.Concurrency = &one
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
		space := cfg.Spaces["libs"]
		space.Flow.OnFail = []string{"onfail"}
		cfg.Spaces["libs"] = space
		cfg.Scripts["onfail"] = models.Script{executionOnFailScript}
	})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peerGit := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*fetch*dispat-task-*", Nth: 1, Onward: true})
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60,
		append(peerGit.Env(), "HOME="+peerHome,
			"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))...)
	defer peer.stop(t)
	res := rig.repo.StartReleaseEnv(rig.env("HOME="+firstHome,
		"XDG_CONFIG_HOME="+filepath.Join(firstHome, "config")),
		"release", "--return-endpoint", rig.returnURL).WaitFor(compiledParallelFailureSettleWait)
	require.NotEqual(t, 0, res.Code, "an unclaimed build unexpectedly succeeded: %s", res.Stdout)
	require.Positive(t, peerGit.Matches(), "the peer encountered the canonical task fetch failure")
	require.Empty(t, rig.nodesByPackage(), "the drained peer executed no build")
	var drained, expired, settled bool
	for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
		switch event.Kind {
		case compiledDrainedMemberEventKind:
			drained = true
		case executionFaultNoClaimExpiredEventKind:
			expired = event.Package == "core"
		case executionFaultNoClaimSettledEventKind:
			settled = expired && event.Package == "core" && event.PackageOutcome == "failed"
		}
	}
	require.True(t, drained && expired && settled,
		"signed Drain must precede typed no-claim and failed package settlement")
	onFailNode, onFailRan := executionProbeValues(rig, "onfail")["core"]
	require.True(t, onFailRan, "the first-node onFail hook did not run; stdout:\n%s\nstderr:\n%s",
		res.Stdout, res.Stderr)
	require.Equal(t, "orchestrator", onFailNode)
	for _, branch := range rig.branches() {
		if strings.Contains(branch, "/dispat-task-") {
			require.NotContains(t, bareGit(t, rig.mailbox, "log", "--first-parent",
				"--format=%s", branch), "dispat transport choreography-claim")
		}
	}
}

// The first node's own delegated Worker can hold its only physical slot when
// a timed-out build returns Ack. Its onFail hook must reuse the proven stopped
// lease and reach terminal finality without a second build or a slot deadlock.
func TestChoreographyCompiledReleaseFirstLocalCancelRunsOnFailAtCapacityOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	const build = `printf 'native-cancel-start %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG";
sleep 60;
printf 'native-cancel-finished %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG"`
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 3, Cancel: 5}
		one := 1
		cfg.Execution.Concurrency = &one
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyOrchestrator,
			Publish: models.RunOnlyOrchestrator}
		space := cfg.Spaces["libs"]
		space.Flow.OnFail = []string{"onfail"}
		cfg.Spaces["libs"] = space
		cfg.Scripts["build"] = models.Script{build}
		cfg.Scripts["onfail"] = models.Script{executionOnFailScript}
	})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*refs/heads/dispat-task-*", Code: 128})
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60,
		append(peerGit.Env(), "HOME="+peerHome,
			"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))...)
	defer peer.stop(t)
	res := rig.release("HOME="+firstHome,
		"XDG_CONFIG_HOME="+filepath.Join(firstHome, "config"))
	require.NotEqual(t, 0, res.Code, "the timed-out build unexpectedly succeeded: %s", res.Stdout)
	content, err := os.ReadFile(rig.builds)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(content), "native-cancel-start core\n"))
	require.NotContains(t, string(content), "native-cancel-finished core\n")
	onFailNode, onFailRan := executionProbeValues(rig, "onfail")["core"]
	require.True(t, onFailRan, "the first-node onFail hook did not run; stdout:\n%s\nstderr:\n%s",
		res.Stdout, res.Stderr)
	require.Equal(t, "orchestrator", onFailNode)
	requireChoreographyTaskChain(t, rig.mailbox, rig.branches(),
		"choreography-claim", "cancel", "ack", "choreography-decision")
	require.Zero(t, peerGit.Matches())
	_, err = os.Stat(filepath.Join(rig.repo.Root, ".git", "dispat", "return-runs", "active-run"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// A process can stop after the signed normal seal and native publication but
// before retiring its protected active-run locator or deleting its old lock.
// Startup must replay that generation and clean only its exact lock object;
// the next release may then plan from the published tag without rebuilding.
func TestChoreographyCompiledReleaseReconcilesCompletedGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	for _, retainLock := range []bool{false, true} {
		name := "lock-already-deleted"
		if retainLock {
			name = "exact-lock-retained"
		}
		t.Run(name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30}
				cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
					Publish: models.RunOnlyOrchestrator}
				cfg.Scripts["build"] = models.Script{executionRecordingScript}
			})
			firstHome, peerHome := t.TempDir(), t.TempDir()
			peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*refs/heads/dispat-task-*", Code: 128})
			peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 90,
				append(peerGit.Env(), "HOME="+peerHome,
					"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))...)
			defer peer.stop(t)
			env := []string{"HOME=" + firstHome,
				"XDG_CONFIG_HOME=" + filepath.Join(firstHome, "config")}
			first := rig.release(env...)
			require.Equal(t, 0, first.Code, "first stdout:\n%s\nfirst stderr:\n%s", first.Stdout, first.Stderr)
			require.Len(t, rig.repo.TagList(), 1)
			buildsBefore := len(rig.runs())
			base := filepath.Join(rig.repo.Root, ".git", "dispat", "return-runs")
			entries, err := os.ReadDir(base)
			require.NoError(t, err)
			prior := ""
			for _, entry := range entries {
				if entry.IsDir() && len(entry.Name()) == 32 {
					require.Empty(t, prior)
					prior = entry.Name()
				}
			}
			require.NotEmpty(t, prior)
			var saved struct {
				PreparedLocks map[string]struct {
					Object string `json:"object"`
				} `json:"preparedLocks"`
			}
			identity, err := os.ReadFile(filepath.Join(base, prior, "identity.json"))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(identity, &saved))
			require.NotEmpty(t, saved.PreparedLocks)
			if retainLock {
				for _, lock := range saved.PreparedLocks {
					require.NotEmpty(t, lock.Object)
					bareGit(t, rig.origin, "update-ref", "refs/tags/"+lockTag, lock.Object)
				}
				require.True(t, remoteHoldsLock(t, rig.origin))
			}
			require.NoError(t, os.WriteFile(filepath.Join(base, "active-run"), []byte(prior), 0o600))
			second := rig.release(env...)
			require.Equal(t, 0, second.Code, "second stdout:\n%s\nsecond stderr:\n%s", second.Stdout, second.Stderr)
			require.Equal(t, buildsBefore, len(rig.runs()), "recovery repeated a settled build")
			require.Len(t, rig.repo.TagList(), 1, "recovery repeated native publication")
			require.False(t, remoteHoldsLock(t, rig.origin))
			_, err = os.Stat(filepath.Join(base, "active-run"))
			require.ErrorIs(t, err, os.ErrNotExist)
			require.Zero(t, peerGit.Matches())
		})
	}
}

// In release-commit mode a package without a publish script runs a record
// vertex, but its tag is still written by finalize. Its signed task outcome
// must describe that deferred record, and cleanup must wait for the actual
// local or pushed tag before retiring the protected run locator.
func TestChoreographyCompiledReleaseDefersRecordOnlyTagUntilFinalize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	for _, push := range []bool{false, true} {
		name := "local-tag"
		if push {
			name = "pushed-tag"
		}
		t.Run(name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: push}
				cfg.Scripts["build"] = models.Script{executionRecordingScript}
				cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
					Publish: models.RunOnlyOrchestrator}
				space := cfg.Spaces["libs"]
				space.Flow = &models.SpaceFlowConfig{Build: []string{"build"}}
				cfg.Spaces["libs"] = space
				delete(cfg.Scripts, "publish")
			})
			peerHome := t.TempDir()
			peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60,
				"HOME="+peerHome, "XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))
			defer peer.stop(t)
			firstHome := t.TempDir()
			res := rig.release("HOME="+firstHome,
				"XDG_CONFIG_HOME="+filepath.Join(firstHome, "config"))
			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			require.Len(t, rig.repo.TagList(), 1)
			require.Equal(t, 1, len(rig.runs()), "the delegated build ran once")
			if push {
				require.NotEmpty(t, bareGit(t, rig.origin, "tag", "-l", "core@0.1.0"))
			}
			_, err := os.Stat(filepath.Join(rig.repo.Root, ".git", "dispat", "return-runs", "active-run"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

// Finalize can write the tag and then lose its journal reply before it records
// the object OID. The protected pre-tag target intent plus the exact native
// tag still settle the old sealed generation, without executing a second
// build or publication.
func TestChoreographyCompiledReleaseRecoversDeferredTagBeforeObjectSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	for _, push := range []bool{false, true} {
		name := "local-tag"
		if push {
			name = "pushed-tag"
		}
		t.Run(name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: push}
				cfg.Scripts["build"] = models.Script{executionRecordingScript}
				cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
					Publish: models.RunOnlyOrchestrator}
			})
			peerHome := t.TempDir()
			peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60,
				"HOME="+peerHome, "XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))
			defer peer.stop(t)
			firstHome := t.TempDir()
			env := []string{"HOME=" + firstHome,
				"XDG_CONFIG_HOME=" + filepath.Join(firstHome, "config")}
			first := rig.release(env...)
			require.Equal(t, 0, first.Code, "stdout:\n%s\nstderr:\n%s", first.Stdout, first.Stderr)
			buildsBefore := len(rig.runs())
			require.Equal(t, 1, buildsBefore)
			base := filepath.Join(rig.repo.Root, ".git", "dispat", "return-runs")
			entries, err := os.ReadDir(base)
			require.NoError(t, err)
			prior := ""
			for _, entry := range entries {
				if entry.IsDir() && len(entry.Name()) == 32 {
					require.Empty(t, prior)
					prior = entry.Name()
				}
			}
			require.NotEmpty(t, prior)
			path := filepath.Join(base, prior, "identity.json")
			document, err := os.ReadFile(path)
			require.NoError(t, err)
			var identity map[string]any
			require.NoError(t, json.Unmarshal(document, &identity))
			deferred, ok := identity["deferredTags"].(map[string]any)
			require.True(t, ok)
			require.Len(t, deferred, 1)
			for _, raw := range deferred {
				intent, ok := raw.(map[string]any)
				require.True(t, ok)
				require.NotEmpty(t, intent["target"])
				intent["object"] = ""
			}
			document, err = json.Marshal(identity)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, document, 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(base, "active-run"), []byte(prior), 0o600))
			second := rig.release(env...)
			require.Equal(t, 0, second.Code, "stdout:\n%s\nstderr:\n%s", second.Stdout, second.Stderr)
			require.Equal(t, buildsBefore, len(rig.runs()), "recovery repeated settled work")
			require.Len(t, rig.repo.TagList(), 1)
			_, err = os.Stat(filepath.Join(base, "active-run"))
			require.ErrorIs(t, err, os.ErrNotExist)

			// The same retained intent must not authorize cleanup after the
			// native tag is removed. Push mode checks the exact remote tag;
			// local mode checks the authoritative checkout's tag.
			if push {
				bareGit(t, rig.origin, "update-ref", "-d", "refs/tags/core@0.1.0")
			} else {
				rig.repo.Git("tag", "-d", "core@0.1.0")
			}
			require.NoError(t, os.WriteFile(filepath.Join(base, "active-run"), []byte(prior), 0o600))
			refused := rig.release(env...)
			require.NotEqual(t, 0, refused.Code,
				"missing native tag retired the protected run: stdout:\n%s\nstderr:\n%s",
				refused.Stdout, refused.Stderr)
			require.Equal(t, buildsBefore, len(rig.runs()), "failed recovery repeated settled work")
			_, err = os.Stat(filepath.Join(base, "active-run"))
			require.NoError(t, err, "failed recovery retired the protected run")
		})
	}
}

// A published package whose required native record was blocked cannot be
// treated as a clean sealed release during startup. Even an exact old lock
// remains until its destination effect can be verified.
func TestChoreographyCompiledReleaseRetainsRecordBlockedGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
		cfg.Commit = &models.CommitConfig{Push: true}
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
	})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 90,
		"HOME="+peerHome, "XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))
	defer peer.stop(t)
	env := []string{"HOME=" + firstHome,
		"XDG_CONFIG_HOME=" + filepath.Join(firstHome, "config")}
	tagFault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*core@0.1.0*", Code: 128,
	})
	first := rig.release(append(env, tagFault.Env()...)...)
	require.NotZero(t, tagFault.Matches(), "the native tag push was not faulted: %s", first.Stdout)
	require.Contains(t, first.Stdout, "recording the release on the remote failed")
	base := filepath.Join(rig.repo.Root, ".git", "dispat", "return-runs")
	entries, err := os.ReadDir(base)
	require.NoError(t, err)
	prior := ""
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) == 32 {
			require.Empty(t, prior)
			prior = entry.Name()
		}
	}
	require.NotEmpty(t, prior)
	var saved struct {
		PreparedLocks map[string]struct {
			Object string `json:"object"`
		} `json:"preparedLocks"`
	}
	identity, err := os.ReadFile(filepath.Join(base, prior, "identity.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(identity, &saved))
	require.NotEmpty(t, saved.PreparedLocks)
	for _, lock := range saved.PreparedLocks {
		require.NotEmpty(t, lock.Object)
		bareGit(t, rig.origin, "update-ref", "refs/tags/"+lockTag, lock.Object)
	}
	require.NoError(t, os.WriteFile(filepath.Join(base, "active-run"), []byte(prior), 0o600))
	second := rig.release(env...)
	require.NotEqual(t, 0, second.Code, "record-blocked generation was retired: %s", second.Stdout)
	require.Contains(t, second.Stdout, "unverified record-blocked publication")
	require.True(t, remoteHoldsLock(t, rig.origin), "the exact old lock was deleted without native proof")
	active, err := os.ReadFile(filepath.Join(base, "active-run"))
	require.NoError(t, err)
	require.Equal(t, prior, string(active))
}

// A real worker executes the ordinary build through the signed return
// channel, while the first node retains canonical task and publication authority.
// The peer's Git wrapper rejects canonical task pushes, so its worker-owned
// pending and outcome refs cannot hide a mistaken task push in this fixture.
func TestChoreographyCompiledReleaseDelegatesBuildButPublishesOnFirstNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.Scripts["publish"] = models.Script{
			`printf '%s %s %s\n' probe-publish "$DISPAT_PACKAGE" ` +
				`"${DISPAT_EXECUTION_NODE:-orchestrator}" >> "$DISPAT_IT_EXECUTION_LOG"`}
	})
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*refs/heads/dispat-task-*", Code: 128})
	firstHome := t.TempDir()
	peerHome := t.TempDir()
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60,
		append(peerGit.Env(), "HOME="+peerHome,
			"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))...)
	defer func() {
		stopped := peer.stop(t)
		t.Logf("peer stdout:\n%s\npeer stderr:\n%s\npeer Git push attempts: %d",
			stopped.Stdout, stopped.Stderr, peerGit.Matches())
	}()

	res := rig.release("HOME="+firstHome,
		"XDG_CONFIG_HOME="+filepath.Join(firstHome, "config"))
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.Equal(t, executionNode, rig.nodesByPackage()["core"], "the peer did not build core: %v", rig.runs())
	published := executionProbeValues(rig, "publish")
	require.NotEmpty(t, published["core"], "the first node did not publish core: %v", rig.runs())
	require.NotEqual(t, executionNode, published["core"], "the peer performed publication")
	require.Zero(t, peerGit.Matches(), "the peer attempted a canonical task Git push")
	require.Len(t, rig.repo.TagList(), 1, "the first node did not publish one release tag")
	require.False(t, remoteHoldsLock(t, rig.origin), "the release lock was not cleaned")
}

// The package's beforeAll vertex executes only on the first node, settles a
// signed native ticket, and releases the peer build only afterward. The peer
// has no canonical task push capability even while the native prelude is active.
func TestChoreographyCompiledReleaseSettlesFirstOnlyPreludeBeforePeerBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
		cfg.Scripts["prelude"] = models.Script{
			`printf 'probe-native %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG"`}
		cfg.Scripts["build"] = models.Script{
			`test "$(sed -n '1p' "$DISPAT_IT_EXECUTION_LOG")" = 'probe-native core' && ` +
				executionRecordingScript}
		space := cfg.Spaces["libs"]
		space.Flow.BeforeAll = []string{"prelude"}
		cfg.Spaces["libs"] = space
	})
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*refs/heads/dispat-task-*", Code: 128})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60,
		append(peerGit.Env(), "HOME="+peerHome,
			"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))...)
	defer peer.stop(t)
	res := rig.release("HOME="+firstHome,
		"XDG_CONFIG_HOME="+filepath.Join(firstHome, "config"))
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	content, err := os.ReadFile(rig.builds)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(content), "probe-native core\n"),
		"first-only prelude did not precede peer frame: %s", content)
	require.Equal(t, executionNode, rig.nodesByPackage()["core"])
	require.Zero(t, peerGit.Matches(), "the peer attempted a canonical task Git push")
	require.True(t, rig.repo.IsTagged("core@0.1.0"))
	require.False(t, remoteHoldsLock(t, rig.origin))
}

// Parallel graph successors share one physical peer slot. A failed stage
// must settle its package and run the first-node onFail hook without waiting
// on another sibling that has not acquired that slot.
func TestChoreographyCompiledReleaseCapacityOneParallelFailureSettles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	const failing = `guard="${DISPAT_IT_EXECUTION_LOG}.active";
if ! mkdir "$guard"; then printf 'probe-overlap %s\n' "$DISPAT_STAGE" >> "$DISPAT_IT_EXECUTION_LOG"; exit 88; fi;
printf 'probe-stage %s\n' "$DISPAT_STAGE" >> "$DISPAT_IT_EXECUTION_LOG";
sleep 0.2; rmdir "$guard"; exit 7`
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Concurrency = []int{3}
		cfg.Execution.Concurrency = models.Int(1)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
		cfg.Scripts["check"] = models.Script{failing}
		cfg.Scripts["lint"] = models.Script{failing}
		cfg.Scripts["on-fail"] = models.Script{
			`printf 'probe-onfail %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG"`}
		space := cfg.Spaces["libs"]
		space.Flow.Test = []string{"check"}
		space.Flow.Stages = map[string]models.StageConfig{
			"lint": {Script: []string{"lint"}, Parallel: []string{"test"}},
		}
		space.Flow.OnFail = []string{"on-fail"}
		cfg.Spaces["libs"] = space
	})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 60,
		"HOME="+peerHome, "XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))
	defer peer.stop(t)
	res := rig.repo.StartReleaseEnv(rig.env("HOME="+firstHome,
		"XDG_CONFIG_HOME="+filepath.Join(firstHome, "config")),
		"release", "--return-endpoint", rig.returnURL).WaitFor(compiledParallelFailureSettleWait)
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	content, err := os.ReadFile(rig.builds)
	require.NoError(t, err)
	require.Contains(t, string(content), "probe-stage ")
	require.NotContains(t, string(content), "probe-overlap ",
		"parallel scripts ran through the same physical slot: %s", content)
	require.Equal(t, 1, strings.Count(string(content), "probe-onfail core"),
		"failure outcome hook did not settle once: %s", content)
	causalFailed, noncausalSettled := false, false
	for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
		if event.Kind != compiledTaskPackageSettledEventKind || event.Package != "core" {
			continue
		}
		if event.PackageOutcome == compiledCausalFailureOutcome {
			causalFailed = true
		} else if causalFailed && event.PackageOutcome == "" {
			noncausalSettled = true
		}
	}
	require.True(t, causalFailed && noncausalSettled,
		"a noncausal sibling can settle only after the causal failed package outcome")
	require.Empty(t, rig.repo.TagList(), "a failed package must not be published")
	require.False(t, remoteHoldsLock(t, rig.origin))
}

// One ready sibling runs on the first node and one on the peer, each at
// capacity one. The first-local stopped Result retains its slot while onFail
// runs, so a second physical acquisition would deadlock this split case.
func TestChoreographyCompiledReleaseSplitFirstPeerFailureReusesStoppedSlot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	// Keep the first capacity-one node occupied until the other parallel
	// frame begins. A sequential offer cannot satisfy this gate.
	parallelGate := `printf 'probe-split %s %s\n' "$DISPAT_STAGE" "$DISPAT_EXECUTION_NODE" >> "$DISPAT_IT_EXECUTION_LOG"; ` +
		`i=0; while [ "$(grep -c '^probe-split ' "$DISPAT_IT_EXECUTION_LOG")" -lt 2 ]; do ` +
		`i=$((i+1)); if [ "$i" -ge 150 ]; then printf 'probe-timeout %s\n' "$DISPAT_STAGE" >> "$DISPAT_IT_EXECUTION_LOG"; exit 9; fi; ` +
		`sleep 0.1; done; `
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Concurrency = []int{3}
		cfg.Execution.Concurrency = models.Int(1)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyBoth,
			Publish: models.RunOnlyOrchestrator}
		cfg.Scripts["check"] = models.Script{parallelGate + `exit 7`}
		cfg.Scripts["lint"] = models.Script{parallelGate + `true`}
		cfg.Scripts["on-fail"] = models.Script{
			`printf 'probe-onfail %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG"`}
		space := cfg.Spaces["libs"]
		space.Flow.Test = []string{"check"}
		space.Flow.Stages = map[string]models.StageConfig{
			"lint": {Script: []string{"lint"}, Parallel: []string{"test"}},
		}
		space.Flow.OnFail = []string{"on-fail"}
		cfg.Spaces["libs"] = space
	})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 60,
		"HOME="+peerHome, "XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))
	defer peer.stop(t)
	res := rig.release("HOME="+firstHome,
		"XDG_CONFIG_HOME="+filepath.Join(firstHome, "config"))
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	content, err := os.ReadFile(rig.builds)
	require.NoError(t, err)
	var stageNodes []string
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "probe-split" {
			stageNodes = append(stageNodes, fields[2])
		}
	}
	require.Len(t, stageNodes, 2, "both parallel stages did not start: %s", content)
	require.NotContains(t, string(content), "probe-timeout", "the ready sibling was not offered concurrently: %s", content)
	require.Contains(t, stageNodes, executionNode, "no parallel stage ran on the peer: %s", content)
	require.NotEqual(t, stageNodes[0], stageNodes[1],
		"both parallel stages used one capacity-one node: %s", content)
	require.Equal(t, 1, strings.Count(string(content), "probe-onfail core"),
		"the first-node failure hook did not settle: %s", content)
	require.Empty(t, rig.repo.TagList())
	require.False(t, remoteHoldsLock(t, rig.origin))
}

// The unreleasing provider is an explicit vertex of the same signed graph.
// With one peer slot, the provider must settle before its consumer can claim
// that slot; recursively preparing it inside the consumer would deadlock.
func TestChoreographyCompiledReleasePreparesUnreleasedProviderBeforeConsumer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionPrepareWorkspace(t, func(_ *harness.Repo, cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Concurrency = models.Int(1)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
		cfg.Scripts["build"] = models.Script{executionPrepareBuild}
	}, pinPreparedPackage("assets", placedOn(models.RunOnlyWorker,
		models.RunOnlyOrchestrator)))
	rig.commitTo("ui uses prepared assets", "ui")
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 90,
		"HOME="+peerHome, "XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))
	defer peer.stop(t)
	res := rig.release("HOME="+firstHome,
		"XDG_CONFIG_HOME="+filepath.Join(firstHome, "config"))
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	placed := rig.nodesByPackage()
	require.Equal(t, executionNode, placed["assets"], "provider preparation did not run on the peer")
	require.Equal(t, executionNode, placed["ui"], "consumer build did not run on the peer")
	require.Equal(t, []string{"assets:" + executionPrepareKind}, executionPrepareTasks(t, rig.mailbox))
	require.True(t, rig.repo.IsTagged("assets@"+executionPrepareBaseline))
	require.False(t, rig.repo.IsTagged("assets@1.1.0"))
	require.True(t, rig.repo.IsTagged("ui@1.1.0"))
	require.False(t, remoteHoldsLock(t, rig.origin))
}
