// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: what a distributed run does when something stops answering.
//
// The scenarios here are about the two clocks a delegated task lives under and
// about the branches a node is asked to ignore. Both are properties of the
// whole exchange rather than of any one message, so both are driven through
// two real processes and asserted on what they left on the mailbox and in
// their logs.

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestExecutionQueuedWorkIsNotATimeout (spec vector 21): a node's capacity is
// the node's, so two runs sharing one node of capacity one make the second
// run's assignment wait in the mailbox. Queue time is not run time: the task
// deadline measures the work, so a wait that is longer than one build and
// shorter than two still completes both runs, and neither takes a healthy node
// out of its pool.
//
// Before this gate the deadline started when the orchestrator began waiting,
// so the queued assignment timed out without ever having run: it leaked the
// node's slot, marked a node that was working perfectly well as unhealthy, and
// failed a package for the sin of being second.
func TestExecutionQueuedWorkIsNotATimeout(t *testing.T) {
	build, wait := executionQueueTimings()
	mailbox := executionMailbox(t)
	first := newExecutionQueuedRepository(t, mailbox, "first", build, wait)
	second := newExecutionQueuedRepository(t, mailbox, "second", build, wait)
	worker := startWorker(t, first.repo, executionWorkerConfig(mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 0,
		executionBuildLogEnv+"="+first.builds)

	started := first.repo.StartReleaseEnv(first.env(), "release")
	other := second.release()
	res := started.Wait()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.Equal(t, 0, other.Code, "stdout:\n%s\nstderr:\n%s", other.Stdout, other.Stderr)
	for name, run := range map[string]harness.RunResult{"first": res, "second": other} {
		t.Run(name, func(t *testing.T) {
			_, isUnhealthy := executionLine(run, "worker marked unhealthy")
			assert.False(t, isUnhealthy,
				"a node that was busy with another run's task is not a node that stopped answering")
		})
	}
	timeline := append(first.repo.Timeline("timeline.log"), second.repo.Timeline("timeline.log")...)
	require.Len(t, timeline, 2, "both releases built their package")
	harness.AssertConcurrencyBudget(t, timeline, 1)
	worker.proc.Signal(syscall.SIGINT)
	require.Equal(t, 0, worker.proc.Wait().Code)
}

// executionQueueTimings are a build long enough to make the second run queue
// behind the first, and a task deadline that is longer than one of them and
// shorter than two. The pair is what makes the claim testable at all: with a
// deadline longer than both, nothing is being asserted.
func executionQueueTimings() (build time.Duration, wait int) {
	if harness.IsTinyGo() {
		return 15 * time.Second, 25
	}
	return 3 * time.Second, 5
}

// newExecutionQueuedRepository is one independent single-package repository
// delegating to a shared mailbox, with a build that takes long enough to make
// the other run wait and a task deadline that would have expired during that
// wait.
func newExecutionQueuedRepository(t *testing.T, mailbox, label string,
	build time.Duration, wait int) *executionRig {
	t.Helper()
	repo := harness.New(t)
	repo.SeedPackage("packages", label)
	cfg := libsConfig(executionRecordingScript+" && "+
		repo.TsmarkScript("timeline.log", label, build), 1)
	cfg.Execution = &models.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers:   []models.ExecutionWorkerConfig{{Name: executionNode, Endpoint: "file://" + mailbox}},
		Timeouts:  &models.ExecutionTimeoutsConfig{Preflight: 30, Task: wait, Cancel: 10},
	}
	repo.WriteConfigModel(cfg)
	repo.Commit("feat(" + label + "): bootstrap")
	return newExecutionRigOver(t, repo, mailbox)
}

// TestExecutionTransportBranchesAreNotRejectedAssignments: a prepared input
// state and a relayed result both appear in the namespace addressed to a node,
// and neither is work. A node that read them found no message of this protocol
// and reported a branch the run itself had created as an assignment nobody
// could read, once per snapshot, at warning level and under the authority code
// a real forgery uses.
func TestExecutionTransportBranchesAreNotRejectedAssignments(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		space := cfg.Spaces["libs"]
		space.RunOnly = &models.RunOnly{Build: "worker", Publish: "orchestrator"}
		cfg.Spaces["libs"] = space
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	// The build was delegated, so the run pushed a prepared input state onto
	// this node's namespace: that is what makes the absence below a claim
	// rather than a coincidence. The branch itself is gone by now, because a
	// run closes the branches it created on its way out.
	assert.Equal(t, map[string]string{"core": executionNode}, rig.nodesByPackage())
	served := worker.stop(t)

	assert.Empty(t, executionRejections(served),
		"a branch the run itself created is not a rejected assignment")
	_, isWarned := executionLine(served, "assignment rejected")
	assert.False(t, isWarned, "and it produces no warning for an operator to chase")
}

// TestExecutionStaleReceiptIsRejected (spec vector 7b): every identity a reply
// is bound to, wrong in turn.
//
// The orchestrator accepts a receipt only when its signature, its header and
// every one of the identities it echoes are this attempt's: the run, the plan
// it executes, the task, the attempt number, the ownership generation, the
// node it was addressed to, the branch it was found on, and the exact
// assignment object it answers. A reply that fails any of them is ignored with
// one warning naming the reason and nothing it contained, and the attempt goes
// on waiting, so the package fails on its own deadline rather than on somebody
// else's word.
//
// The last row is the control: the same saboteur, bound correctly, is accepted
// and the package is released. Without it the rows above would pass for a
// fixture that simply never worked.
func TestExecutionStaleReceiptIsRejected(t *testing.T) {
	for name, tc := range map[string]struct {
		mangle func(executionOrderedJSON) executionOrderedJSON
		secret string
		reason string
	}{
		"a reply of another run": {
			mangle: executionRebind("run", "0123456789abcdef0123456789abcdef"), reason: "replay"},
		"a reply for another kind of work": {
			mangle: executionRebind("kind", "publish"), reason: "replay"},
		"a reply of another planning": {
			mangle: executionRebind("planDigest",
				strings.Repeat("a", 64)), reason: "replay"},
		"a reply of another attempt": {
			mangle: executionRebind("attempt", 2), reason: "replay"},
		"a reply under another ownership": {
			mangle: executionRebind("generation", "an-older-acquisition"), reason: "replay"},
		"a reply answering another assignment": {
			mangle: executionRebind("assignment", strings.Repeat("0", 40)), reason: "replay"},
		"a reply from another node": {
			mangle: executionRebind("node", executionSecondNode), reason: "node"},
		"a reply found on another branch": {
			mangle: executionRebind("branch", "dispat-worker-build-a-20260922-build-elsewhere"),
			reason: "branch"},
		"a reply signed with another secret": {
			secret: "not-the-runs-secret", reason: "signature"},
		"a reply bound to this very attempt": {},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionSaboteurRig(t)
			done := executionSabotage(t, rig, tc.mangle, tc.secret)
			res := rig.release()
			executionAwaitAnswered(t, done)

			if tc.reason == "" {
				require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.NotEmpty(t, rig.repo.TagList(), "the correctly bound receipt released the package")
				return
			}
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, executionRefusedReplies(res), tc.reason,
				"the reply is ignored for exactly one reason\nstdout:\n%s", res.Stdout)
			assert.Empty(t, rig.repo.TagList(), "and grants the attempt nothing")
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode),
				"the package fails on its own deadline")
		})
	}
}

// A claim is the point where queue time becomes task time. A party able to
// write the mailbox must not make the run believe work started by putting an
// invalid claim on its branch.
func TestExecutionForgedClaimsAreRejectedBeforeWork(t *testing.T) {
	for name, row := range map[string]struct {
		mangle func(executionOrderedJSON) executionOrderedJSON
		secret string
		reason string
	}{
		"signed with another secret": {
			secret: "not-the-runs-secret", reason: "signature",
		},
		"unparseable header": {
			mangle: executionRebind("issuedAt", []any{"not a time"}), reason: "unreadable",
		},
		"expired issue time": {
			mangle: executionRebind("issuedAt", "2026-09-01T00:00:00Z"), reason: "issued-at",
		},
		"answers another assignment": {
			mangle: executionRebind("assignment", strings.Repeat("0", 40)), reason: "replay",
		},
		"claims another kind of work": {
			mangle: executionRebind("kind", "publish"), reason: "replay",
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionSaboteurRig(t)
			worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, nil)
			worker.claimMangle, worker.claimSecret, worker.isClaimOnly = row.mangle, row.secret, true
			worker.serve()
			t.Cleanup(worker.close)

			res := rig.release()
			executionAwaitAnswered(t, worker.answered)

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, executionRefusedReplies(res), row.reason,
				"the forged claim cannot satisfy the attempt")
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode))
			assert.Empty(t, rig.repo.TagList(), "no invalid claim can lead to publication")
			assert.Empty(t, rig.runs(), "no build ran on the strength of a forged claim")
			assertRejectedTipRetained(t, rig, res)
		})
	}
}

// A ready message is the last worker reply before a publish authorization.
// Invalid ready messages must not let a mailbox writer start an irreversible
// command, even when the worker's earlier claim was valid.
func TestExecutionForgedReadyCannotAuthorizePublication(t *testing.T) {
	for name, row := range map[string]struct {
		mangle func(executionOrderedJSON) executionOrderedJSON
		secret string
		reason string
	}{
		"signed with another secret": {
			secret: "not-the-runs-secret", reason: "signature",
		},
		"unparseable header": {
			mangle: executionRebind("issuedAt", []any{"not a time"}), reason: "unreadable",
		},
		"names another claim": {
			mangle: executionRebind("claim", strings.Repeat("0", 40)), reason: "replay",
		},
		"readies another kind of work": {
			mangle: executionRebind("kind", "build"), reason: "replay",
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyWorker)
				cfg.Scripts["build"] = models.Script{executionRecordingScript}
				cfg.Scripts["publish"] = models.Script{executionRecordingScript}
				wait := 6
				if harness.IsTinyGo() {
					wait = 30
				}
				cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
					Preflight: 30, Task: wait, Cancel: 5}
			})
			worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, nil)
			worker.readyMangle, worker.readySecret, worker.isReadyOnly = row.mangle, row.secret, true
			worker.serve()
			t.Cleanup(worker.close)

			res := rig.release()
			executionAwaitAnswered(t, worker.answered)

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, executionRefusedReplies(res), row.reason,
				"the forged ready cannot authorize publication")
			assert.Len(t, rig.runs(), 1, "only the local build ran: %v", rig.runs())
			assert.Equal(t, executionOrchestratorLabel, rig.runs()[0].Node)
			assert.Empty(t, rig.repo.TagList(), "no publication was recorded")
			_, authorized := executionLine(res, "publication authorized")
			assert.False(t, authorized, "the coordinator never sent Go")
			assertRejectedTipRetained(t, rig, res)
		})
	}
}

// A refused message is not cleanup authority, even when cancellation runs
// after the refusal. Keep exactly the rejected commit for investigation.
func assertRejectedTipRetained(t *testing.T, rig *executionRig, result harness.RunResult) {
	t.Helper()
	rejected, isRejected := executionLine(result, "result rejected")
	require.True(t, isRejected, "stdout:\n%s", result.Stdout)
	branch := "refs/heads/" + rejected.Str("branch")
	require.Equal(t, []string{branch}, rig.branches(), "only the rejected branch remains")
	assert.Equal(t, rejected.Str("commit"), strings.TrimSpace(bareGit(t, rig.mailbox,
		"rev-parse", branch)), "cancellation and cleanup leave the rejected tip untouched")
	assert.True(t, harness.IsCodePresent(executionEvents(result), executionRetainedCode),
		"retained coordination content needs investigation\nstdout:\n%s", result.Stdout)
	assert.NotContains(t, result.Stdout, "the attempt is fenced by revoking its coordination ref",
		"a refused deletion cannot be reported as a completed fence")
}

// TestExecutionOldRunReceiptGrantsNothing (spec vector 20): a receipt of an
// earlier run, complete and correctly signed, replayed onto this run's branch.
//
// It grants nothing, and "nothing" is the whole claim: the attempt is not
// satisfied by it, no output set is admitted from it, no version is tagged and
// the run fails on the deadline of an attempt nobody answered. A receipt is a
// statement about one attempt of one run, and an implementation that read it
// as a statement about work would be one an old mailbox could release packages
// through.
func TestExecutionOldRunReceiptGrantsNothing(t *testing.T) {
	rig := newExecutionSaboteurRig(t)
	// The identities of a run that finished yesterday: its own id and the
	// ownership it held, both of which this run has never heard of.
	done := executionSabotage(t, rig, func(document executionOrderedJSON) executionOrderedJSON {
		return document.set("run", "beefbeefbeefbeefbeefbeefbeefbeef").
			set("generation", "yesterdays-acquisition")
	}, "")

	res := rig.release()
	executionAwaitAnswered(t, done)

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, executionRefusedReplies(res), "replay")
	assert.Empty(t, rig.repo.TagList(), "an old run's receipt discharges nothing")
	assert.Empty(t, rig.runs(), "and no build ran anywhere on the strength of it")
}

// executionAwaitAnswered waits, bounded, for the saboteur to have answered.
// It is bounded because a fixture that never answers must fail its scenario
// rather than hang the suite: the release has already finished by the time it
// is called, so anything it waits for has either happened or never will.
func executionAwaitAnswered(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the saboteur never answered this run's build assignment")
	}
}

// executionRefusedReplies are the reasons the orchestrator refused a node's
// replies, which is the mirror of the reasons a node refuses an assignment:
// the same enum, read off the line the other party writes.
func executionRefusedReplies(res harness.RunResult) []string {
	var reasons []string
	for _, event := range executionEvents(res) {
		if event.Str("message") == "result rejected" {
			reasons = append(reasons, event.Str("reason"))
		}
	}
	return reasons
}

// newExecutionSaboteurRig is one package whose build is delegated, with a task
// deadline short enough that an attempt nobody validly answered fails quickly.
func newExecutionSaboteurRig(t *testing.T) *executionRig {
	t.Helper()
	wait := 6
	if harness.IsTinyGo() {
		wait = 30
	}
	return newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
			Preflight: 30, Task: wait, Cancel: 5}
	})
}

// executionRebind is the mangling one row applies: exactly one identity of an
// otherwise correct receipt, replaced.
func executionRebind(key string, value any) func(executionOrderedJSON) executionOrderedJSON {
	return func(document executionOrderedJSON) executionOrderedJSON {
		return document.set(key, value)
	}
}

// executionSabotage answers this run's build assignment the way a party with
// push access to the mailbox would: it claims the branch and pushes one result
// of its own, mangled as the row asks.
//
// It answers the probe as well, because a run whose preflight nobody answers
// never dispatches anything and would be a scenario about preflight instead.
// The channel is closed once it has answered, so a scenario waits for the
// thing it is about rather than for a duration.
func executionSabotage(t *testing.T, rig *executionRig,
	mangle func(executionOrderedJSON) executionOrderedJSON, secret string) <-chan struct{} {
	t.Helper()
	worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, nil)
	worker.mangle = mangle
	if secret != "" {
		worker.resultSecret = secret
	}
	worker.serve()
	t.Cleanup(worker.close)
	return worker.answered
}

// TestExecutionLostResultPushIsRecognizedNotRepeated: a node's result push
// lands on the remote and its answer never comes back.
//
// The node re-reads the branch, finds its own object there and treats the
// rejection as the success it was, so the orchestrator accepts one result for
// one build. Nothing is repeated: the build script ran exactly once, the
// package is released and no publication outcome is left unknown, because
// nothing about a publication happened.
func TestExecutionLostResultPushIsRecognizedNotRepeated(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 60, Cancel: 10}
	})
	// The second push a node makes onto a build branch: the first is its
	// claim, so this is the result, applied and then reported as rejected.
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*-build-*", Nth: 2, After: true})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Positive(t, fault.Matches(), "the fault reached the invocation it names")
	assert.Len(t, rig.runs(), 1, "the build ran exactly once: %v", rig.runs())
	assert.Equal(t, map[string]string{"core": executionNode}, rig.nodesByPackage())
	assert.NotEmpty(t, rig.repo.TagList(), "and the package was released")
	assert.False(t, harness.IsCodePresent(executionEvents(res), executionUnknownCode),
		"nothing about a publication was left unknown")
	stopAll(t, []*executionWorker{worker})
}

// executionUnknownCode is the code a publication whose outcome cannot be
// established reports.
const executionUnknownCode = "E228"

// TestExecutionLockLossStopsNewEffects (spec vector 14): the remote release
// lock is deleted while the run's first build is executing.
//
// After that loss the run starts nothing: no further assignment is written, no
// publication is authorized, and no publish command runs anywhere. The lock
// the run lost is neither re-created nor deleted by it, because a lock on a
// remote after a loss is somebody else's or nobody's and never this run's to
// tidy. The run fails with the lock code.
func TestExecutionLockLossStopsNewEffects(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyWorker)
		cfg.Scripts["build"] = models.Script{executionRecordingScript + " && " +
			`git -C "$DISPAT_IT_EXECUTION_ORIGIN" tag -d dispat-release-lock`}
		cfg.Scripts["publish"] = models.Script{executionRecordingScript}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 60, Cancel: 10}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0,
		"DISPAT_IT_EXECUTION_ORIGIN="+rig.origin)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionLockCode),
		"the run reports the lock it no longer holds\nstdout:\n%s", res.Stdout)
	// The run revalidates ownership before placing the next task. Since the
	// build deleted the lock, no publish assignment can be offered at all.
	assert.Empty(t, executionPlacements(res, "core:publish"),
		"no publish assignment was offered after the lock disappeared")
	assert.Empty(t, rig.repo.TagList(), "no version was recorded")
	assert.False(t, remoteHoldsLock(t, rig.origin),
		"the lock this run lost is not re-created by it")

	served := worker.stop(t)
	assert.NotContains(t, served.Stdout, `"message":"publication authorized"`)
	assert.Len(t, rig.runs(), 1,
		"the build that deleted the lock ran, and nothing after it did: %v", rig.runs())
}

// TestExecutionLostPublishReplyIsNeverRepublished (spec vector 13): a node
// publishes and then stops existing before it can say so.
//
// This is the one outcome the profile exists for. The publish command ran once
// and the registry may well hold the version, but nothing available to this
// run says whether it does: §28.6 allows neither a missing reply nor a missing
// tag to be read as failure. So the package fails at its publish stage with
// E228 and no tag, no second attempt is authorized under that authorization,
// the run exits non-zero, and the exclusion covering the repository the
// publisher was writing into is left on the remote for an operator, with a
// remedy that names the order of recovery rather than telling them to delete a
// tag.
//
// The node is killed outright rather than asked to stop, which is what makes
// the reply lost: a node that was signalled would acknowledge, and an
// acknowledged publisher is a known outcome.
func TestExecutionLostPublishReplyIsNeverRepublished(t *testing.T) {
	wait, cancel := 6, 3
	if harness.IsTinyGo() {
		wait, cancel = 30, 15
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyWorker)
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		// The marker first, then a sleep longer than every wait of the run:
		// the effect has happened and the node will never report it.
		cfg.Scripts["publish"] = models.Script{executionPublishProbe + " && sleep 600"}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
			Preflight: 30, Task: wait, Cancel: cancel}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	started := rig.repo.StartReleaseEnv(rig.env(), "release")

	// The publish command has run: from here on nobody can say what the
	// registry holds, which is the state this scenario is about.
	executionAwaitProbe(t, rig, "probe-publish")
	worker.proc.Signal(syscall.SIGKILL)
	res := started.Wait()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionUnknownCode),
		"the outcome is reported as one nobody can establish\nstdout:\n%s", res.Stdout)
	unknown, isUnknown := executionLine(res,
		"the outcome of an authorized publication cannot be established")
	require.True(t, isUnknown, "stdout:\n%s", res.Stdout)
	assert.Equal(t, "publication-unknown", unknown.Str("category"))
	assert.Equal(t, "core:publish", unknown.Str("task"))
	assert.Equal(t, executionNode, unknown.Str("worker"))
	assert.Equal(t, false, unknown["quiesced"], "the publisher never said it had stopped")

	retained, isRetained := executionLine(res, "release lock retained")
	require.True(t, isRetained, "stdout:\n%s", res.Stdout)
	assert.Contains(t, retained.Str("remedy"), "release-lock.md",
		"the remedy names the page that describes clearing a lock left behind")
	assert.Contains(t, retained.Str("error"), "only then delete the lock tag",
		"and states the order of recovery rather than the deletion alone")
	assert.True(t, remoteHoldsLock(t, rig.origin),
		"the exclusion covering the unaccounted publication is left for an operator")
	assert.Empty(t, executionReleaseTags(rig),
		"and nothing is recorded for a publication nobody can vouch for")

	published := 0
	for _, run := range rig.runs() {
		if run.Node == "probe-publish" {
			published++
		}
	}
	assert.Equal(t, 1, published, "the publish command ran exactly once: %v", rig.runs())
	// The summary tells this outcome from a failure, which is the whole point
	// of §28.9's last paragraph.
	summary, isSummarized := executionLine(res, "distributed execution summary")
	require.True(t, isSummarized)
	assert.Equal(t, float64(1), summary["unknown"])
	assert.Equal(t, float64(0), summary["published"])
	_ = worker.proc.Wait()
}

// executionAwaitProbe waits for one of the fixture's probe markers to appear,
// which is how a scenario synchronises with a command that has actually run
// rather than with a duration.
func executionAwaitProbe(t *testing.T, rig *executionRig, probe string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		for _, run := range rig.runs() {
			if run.Node == probe {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no %s marker appeared within the deadline: %v", probe, rig.runs())
}

// TestExecutionInterruptCancelsRemoteTasks: the release is interrupted while a
// node is building.
//
// A signal is not a failure. The run withdraws the attempt it has in flight
// and waits, bounded, to be told that nothing of it is running; the node kills
// the process group its build started and answers with the phase the frame had
// reached; the package is `cancelled` rather than failed, so no failure hook
// runs and the next run owes it the same release. The node itself is still
// serving afterwards, because an orchestrator going away is not a reason for a
// machine to stop.
func TestExecutionInterruptCancelsRemoteTasks(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript + " && sleep 600"}
		cfg.Scripts["onfail"] = models.Script{
			`printf '%s %s %s\n' probe-onfail "$DISPAT_PACKAGE" here >> "$DISPAT_IT_EXECUTION_LOG"`}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
			Preflight: 30, Task: 600, Cancel: 60}
		// The settling of a withdrawal is a decision rather than an outcome,
		// so the line that reports it is a debug one.
		cfg.LogLevel = "debug"
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	started := rig.repo.StartReleaseEnv(rig.env(), "release")

	// The build is under way: an interrupt before the claim would be a
	// scenario about an assignment nobody took.
	executionAwaitProbe(t, rig, executionNode)
	started.Signal(syscall.SIGINT)
	res := started.Wait()

	assert.NotEqual(t, 0, res.Code, "an interrupted release exits non-zero")
	withdrawn, isWithdrawn := executionLine(res, "attempt withdrawn")
	require.True(t, isWithdrawn, "stdout:\n%s", res.Stdout)
	assert.Equal(t, "core:build", withdrawn.Str("task"))
	assert.Equal(t, executionNode, withdrawn.Str("worker"))
	settled, isSettled := executionLine(res, "the withdrawn attempt was acknowledged")
	require.True(t, isSettled, "the node said that nothing of it is running\nstdout:\n%s", res.Stdout)
	assert.Equal(t, "commands", settled.Str("phase"),
		"and said what the frame had reached when it was withdrawn")
	assert.Equal(t, true, settled["commandStarted"],
		"the stage's own command sequence had begun, which is the fact the flag states; "+
			"only a publication reads it as a statement about an effect")
	assert.Empty(t, executionReleaseTags(rig), "nothing was recorded")
	for _, run := range rig.runs() {
		assert.NotEqual(t, "probe-onfail", run.Node,
			"an interrupted package runs no failure hook: %v", rig.runs())
	}

	// The node outlived the run that dispatched to it and is still serving.
	served := worker.stop(t)
	assert.Contains(t, served.Stdout, `"message":"cancellation acknowledged"`)
	assert.Contains(t, served.Stdout, `"message":"task withdrawn"`)
}

// TestExecutionKilledWorkerBlocksDependentsAndKeepsCapacity (spec vector 14):
// the only node able to run this plan's builds disappears mid-build.
//
// Four things follow and none of them may be traded for another. The package
// being built fails, because nothing of it can be admitted. Its dependents are
// blocked rather than attempted, because a consumer of a provider that did not
// publish has nothing to build against. The node's slot is not freed and the
// node leaves the pool, since the work may still be running on that machine,
// so the next task that needed a node fails at once with the placement error
// rather than waiting for a machine that will not come back. And the locks are
// still released, because a compute attempt holds no authorization and is
// fenced by revoking its ref.
func TestExecutionKilledWorkerBlocksDependentsAndKeepsCapacity(t *testing.T) {
	wait, cancel := 4, 2
	if harness.IsTinyGo() {
		wait, cancel = 20, 10
	}
	rig := newExecutionWorkspace(t, func(*harness.Repo) string {
		return executionRecordingScript + " && sleep 600"
	}, func(cfg *models.File) {
		cfg.Concurrency = []int{1, 1}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
			Preflight: 30, Task: wait, Cancel: cancel}
		executionOneWorker(cfg)
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	started := rig.repo.StartReleaseEnv(rig.env(), "release")

	executionAwaitProbe(t, rig, executionNode)
	worker.proc.Signal(syscall.SIGKILL)
	res := started.Wait()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	events := executionEvents(res)
	assert.True(t, harness.IsCodePresent(events, executionIntegrityCode),
		"the abandoned attempt is reported\nstdout:\n%s", res.Stdout)
	unhealthy, isUnhealthy := executionLine(res, "worker marked unhealthy")
	require.True(t, isUnhealthy, "stdout:\n%s", res.Stdout)
	assert.Equal(t, "task-deadline", unhealthy.Str("reason"))
	// Nothing downstream of the package that failed was executed anywhere,
	// which is the observable form of "its dependents are blocked": a
	// consumer of a provider that produced nothing has nothing to build
	// against, and a pool with no healthy node has nowhere to put it either.
	summary, isSummarized := executionLine(res, "distributed execution summary")
	require.True(t, isSummarized, "stdout:\n%s", res.Stdout)
	assert.Equal(t, float64(0), summary["computed"],
		"no task of this run completed its computation")
	built := map[string]bool{}
	for _, run := range rig.runs() {
		built[run.Package] = true
	}
	assert.Equal(t, map[string]bool{"assets": true}, built,
		"and no build of a dependent was placed anywhere: %v", rig.runs())
	assert.Empty(t, executionReleaseTags(rig), "nothing was published")
	assert.False(t, remoteHoldsLock(t, rig.origin),
		"a compute attempt is fenced by revoking its ref, so the exclusion goes back")
	assert.False(t, harness.IsCodePresent(events, executionUnknownCode),
		"and no publication outcome is in doubt, because none was authorized")
	_ = worker.proc.Wait()
}

// TestExecutionCleanupFailureKeepsPublishedOutcome (spec vector 15): the
// batched deletion of this run's own coordination branches cannot be pushed.
//
// Retained transport data is untidy and nothing else: a coordination branch
// carries messages between two machines and no part of a release, so its
// survival erases no record. The packages are published and tagged, the refs
// that remain are named in a warning under the retained code, and the run
// still exits zero (§28.9, "harmless retained transport data MAY be a
// warning").
func TestExecutionCleanupFailureKeepsPublishedOutcome(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
	})
	// A push whose source side is empty is a deletion, and the only deletions
	// this run makes are of the branches it created itself.
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push* :refs/heads/dispat-worker-*", Nth: 1, Onward: true})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release(fault.Env()...)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Positive(t, fault.Matches(), "the fault reached the deletion it names")
	assert.NotEmpty(t, executionReleaseTags(rig), "the package is published and tagged")
	retained, isRetained := executionLine(res, "coordination branches were not closed")
	require.True(t, isRetained, "stdout:\n%s", res.Stdout)
	assert.Equal(t, executionRetainedCode, retained.Str("code"))
	assert.Equal(t, "transport-cleanup", retained.Str("category"))
	assert.Contains(t, retained.Str("error"), "dispat-worker-",
		"the refs that remain are named, so an operator can remove them")
	assert.NotEmpty(t, rig.branches(), "and they really are still there")
	stopAll(t, []*executionWorker{worker})
}

// TestExecutionTaskReceiptDischargesNothing (spec vector 26, the half that
// applies here): a build succeeds on a node and the publication fails.
//
// The task receipt is not a release. Only the durable source record discharges
// the obligation, so the next plan names the same package at the same version,
// and no tag was derived from the fact that a node reported a successful
// build.
func TestExecutionTaskReceiptDischargesNothing(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.Scripts["publish"] = models.Script{"exit 7"}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Len(t, rig.runs(), 1, "the build did run, and on the node: %v", rig.runs())
	assert.Empty(t, executionReleaseTags(rig),
		"a receipt for a completed build is not a release record")
	assert.Equal(t, "0.1.0", executionPlannedVersions(t, rig)["core"],
		"so the next plan still owes the same version")
	stopAll(t, []*executionWorker{worker})
}

// TestExecutionPrepublicationFailureRemainsRebuildable (spec vector 16b): a
// run fails before publication, and then everything the transport left is
// deleted.
//
// Nothing about the pending work lived there. The next run reads the same tags
// and records, plans the same version, rebuilds it on a node whose cache was
// thrown away, and completes: synchronization state is not a second recovery
// ledger (§28.6).
func TestExecutionPrepublicationFailureRemainsRebuildable(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript + " && " +
			`test -z "$DISPAT_IT_EXECUTION_FAIL"`}
	})
	failing := rig.startWorker(executionWorkerConfig(rig.mailbox), 0,
		"DISPAT_IT_EXECUTION_FAIL=1")
	first := rig.release()
	require.Equal(t, 1, first.Code, "the first run fails before publication")
	require.Empty(t, executionReleaseTags(rig), "and records nothing")
	stopAll(t, []*executionWorker{failing})

	// Everything the transport left: the node's whole state folder, the
	// orchestrator's private fetched refs, and whatever branches are still on
	// the mailbox.
	require.NoError(t, os.RemoveAll(failing.stateDir))
	executionForgetTransport(t, rig)
	assert.Equal(t, "0.1.0", executionPlannedVersions(t, rig)["core"],
		"the same version is still owed")

	healthy := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	second := rig.release()

	require.Equal(t, 0, second.Code, "stdout:\n%s\nstderr:\n%s", second.Stdout, second.Stderr)
	assert.Contains(t, rig.repo.TagList(), "core@0.1.0",
		"the version the failed run planned is the version the next one released")
	stopAll(t, []*executionWorker{healthy})
}

// TestExecutionDeletedTransportStateLeavesRecordsAuthoritative (spec vector
// 16a): after a completed run, every trace of the transport is deleted and the
// inputs are replanned unchanged.
//
// The debt is discharged by the source records and by nothing else, so the
// next plan proposes no release at all although the mailbox, the node's cache
// and the private refs are gone.
func TestExecutionDeletedTransportStateLeavesRecordsAuthoritative(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	res := rig.release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.Contains(t, rig.repo.TagList(), "core@0.1.0")
	stopAll(t, []*executionWorker{worker})

	require.NoError(t, os.RemoveAll(worker.stateDir))
	executionForgetTransport(t, rig)

	assert.Empty(t, executionPlannedVersions(t, rig),
		"the record discharges the debt, so nothing is planned once the transport is gone")
}

// executionForgetTransport deletes everything the transport left behind: the
// coordination branches still on the mailbox and the orchestrator's own
// fetched refs.
func executionForgetTransport(t *testing.T, rig *executionRig) {
	t.Helper()
	for _, ref := range rig.branches() {
		bareGit(t, rig.mailbox, "update-ref", "-d", ref)
	}
	for _, ref := range strings.Fields(rig.repo.Git("for-each-ref",
		"--format=%(refname)", "refs/dispat-transport/")) {
		rig.repo.Git("update-ref", "-d", ref)
	}
}

// executionPlannedVersions is what the next plan proposes, by package, read
// from `dispat status` rather than from anything this run remembers.
func executionPlannedVersions(t *testing.T, rig *executionRig) map[string]string {
	t.Helper()
	status := rig.repo.CommandEnv(rig.env(), "status", "--log-format", "json")
	require.Equal(t, 0, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	planned := map[string]string{}
	for _, event := range executionEvents(status) {
		if !strings.Contains(event.Str("message"), "changed") {
			continue
		}
		_, next, isBump := strings.Cut(event.Str("version"), " -> ")
		if isBump {
			planned[event.Package()] = next
		}
	}
	return planned
}
