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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

const (
	executionQueueFirstTaskTimeoutSeconds  = 180
	executionQueueSecondTaskTimeoutSeconds = 30
	executionQueueGateMaximumPolls         = 150
	executionQueueGatePollSeconds          = 1
	executionQueueGateExitCode             = 7
	executionQueueGoBuildSeconds           = 3
	executionQueueTinyGoBuildSeconds       = 15
	executionQueueFirstLabel               = "first"
	executionQueueSecondLabel              = "second"
	executionQueueReadyWait                = 45 * time.Second
	executionQueueReadyPoll                = 100 * time.Millisecond
	executionQueueBeyondDeadline           = time.Second
	executionQueueGateNoSleep              = time.Duration(0)
	executionQueueGateScriptFormat         = `i=0; while [ ! -f "$DISPAT_IT_EXECUTION_GATE" ] && [ "$i" -lt %d ]; do sleep %d; i=$((i+1)); done; test -f "$DISPAT_IT_EXECUTION_GATE" || exit %d`
	executionQueueWorkerReadyMessage       = "worker ready"
	executionQueueTaskID                   = "second:1:"
	executionQueueTaskRefPrefix            = "dispat-task-"
	executionQueueLedgerRefPrefix          = "dispat-ledger-"
	executionQueueReservationDocument      = "dispat/reservation.json"
	executionQueueReservationSignature     = "dispat/reservation.sig"
	executionQueueReservationKind          = "reservation"
	executionQueueLedgerDocument           = "dispat/choreography-ledger.json"
	executionQueueLedgerSignature          = "dispat/choreography-ledger.sig"
	executionQueueLedgerKind               = "choreography-ledger"
	executionQueueStartedKind              = "execution-started"
	executionQueueClaimKind                = "claim-accepted"
	executionQueueTerminalKind             = "terminal"
	executionQueueCancelKind               = "cancel-issued"
	executionQueueRetryKind                = "retry-ready"
	executionQueueNoClaimKind              = "no-claim-expired"
	executionQueueLedgerScanLimit          = 128
	executionQueueExpectedClaims           = 1
)

// TestExecutionQueuedWorkIsNotATimeout (spec vector 21): a node's capacity is
// the node's, so two runs sharing one node of capacity one make the second
// run's assignment wait in the mailbox. Queue time is not run time: the second
// run can remain queued longer than its active-task deadline, then complete
// when the first task releases the slot without marking the node unhealthy.
//
// Before this gate the deadline started when the orchestrator began waiting,
// so the queued assignment timed out without ever having run: it leaked the
// node's slot, marked a node that was working perfectly well as unhealthy, and
// failed a package for the sin of being second.
func TestExecutionQueuedWorkIsNotATimeout(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "release-first-build")
	defer func() { _ = os.WriteFile(gate, nil, 0o600) }()
	mailbox := executionMailbox(t)
	first := newExecutionQueuedRepository(t, executionQueuedRepositoryOptions{
		Mailbox: mailbox, Label: executionQueueFirstLabel, TaskTimeoutSeconds: executionQueueFirstTaskTimeoutSeconds,
		IsGatedBuild: true})
	second := newExecutionQueuedRepository(t, executionQueuedRepositoryOptions{
		Mailbox: mailbox, Label: executionQueueSecondLabel, TaskTimeoutSeconds: executionQueueSecondTaskTimeoutSeconds,
		Build: executionQueueSecondBuildDuration()})
	worker := startWorker(t, first.repo, executionWorkerConfig(mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 0,
		executionBuildLogEnv+"="+first.builds, executionGateEnv+"="+gate)
	workerStopped := false
	defer func() {
		if t.Failed() && !workerStopped {
			_ = os.WriteFile(gate, nil, 0o600)
			worker.proc.Signal(syscall.SIGINT)
			reply := worker.proc.Wait()
			t.Logf("shared peer stdout:\n%s\nstderr:\n%s", reply.Stdout, reply.Stderr)
		}
	}()

	started := first.repo.StartReleaseEnv(first.env(), "release", "--return-endpoint", first.returnURL)
	executionAwaitProbe(t, first, executionNode)
	queued := second.repo.StartReleaseEnv(second.env(), "release", "--return-endpoint", second.returnURL)
	var secondRun string
	isSecondReady := assert.Eventually(t, func() bool {
		stdout, _ := queued.CapturedOutput()
		for _, event := range harness.ParseEvents(stdout) {
			if event.Str("message") != executionQueueWorkerReadyMessage || event.Str("worker") != executionNode {
				continue
			}
			run := executionQueueSignedReservation(t, mailbox)
			if run == "" || run != event.Str("run") || !executionQueueLedgerExists(t, mailbox, run) {
				continue
			}
			for _, signed := range executionQueueSignedLedgerEvents(t, mailbox, run) {
				if signed.Kind == executionQueueStartedKind {
					secondRun = run
					return true
				}
			}
		}
		return false
	}, executionQueueReadyWait, executionQueueReadyPoll,
		"the second run must have a signed reservation and execution-started event before its queue-time proof")
	if isSecondReady {
		assert.Never(t, func() bool { return executionHasQueuedBuild(first, second) },
			time.Duration(executionQueueSecondTaskTimeoutSeconds)*time.Second+executionQueueBeyondDeadline,
			executionQueueReadyPoll, "the second task must remain queued past its active-task deadline")
		for _, event := range executionQueueSignedLedgerEvents(t, mailbox, secondRun) {
			require.NotEqual(t, executionQueueTaskID, event.Task,
				"the second signed task has no claim, cancellation or timeout before its queued wait ends")
		}
	}
	gateErr := os.WriteFile(gate, nil, 0o600)
	other := queued.Wait()
	res := started.Wait()

	require.NoError(t, gateErr)
	require.True(t, isSecondReady, "the second run had an admitted worker while it waited in the queue")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.Equal(t, 0, other.Code, "stdout:\n%s\nstderr:\n%s", other.Stdout, other.Stderr)
	executionQueueAssertSingleSignedAttempt(t, mailbox, secondRun)
	for name, run := range map[string]harness.RunResult{executionQueueFirstLabel: res, executionQueueSecondLabel: other} {
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
	workerReply := worker.proc.Wait()
	workerStopped = true
	require.Equal(t, 0, workerReply.Code)
}

func executionQueueAssertSingleSignedAttempt(t *testing.T, mailbox, run string) {
	t.Helper()
	var claims, terminals int
	for _, event := range executionQueueSignedLedgerEvents(t, mailbox, run) {
		if event.Task != executionQueueTaskID {
			continue
		}
		switch event.Kind {
		case executionQueueClaimKind:
			claims++
		case executionQueueTerminalKind:
			terminals++
		case executionQueueCancelKind, executionQueueRetryKind, executionQueueNoClaimKind:
			t.Errorf("the queued task had an unexpected signed %s event", event.Kind)
		}
	}
	require.Equal(t, executionQueueExpectedClaims, claims,
		"the queued task had one accepted signed claim after the first task released the node")
	require.Equal(t, executionQueueExpectedClaims, terminals,
		"the queued task had one signed terminal after its accepted claim")
}

// A signed task root is required here because preflight can finish before the
// second run has a graph whose task could actually wait for a node slot.
func executionQueueSignedReservation(t *testing.T, mailbox string) string {
	t.Helper()
	for _, branch := range executionProtocolRefs(t, mailbox, executionQueueTaskRefPrefix) {
		ref := "refs/heads/" + branch
		document, err := exec.Command("git", "-C", mailbox, "show", ref+":"+executionQueueReservationDocument).Output()
		if err != nil {
			continue
		}
		var reservation struct {
			Run    string `json:"run"`
			Task   string `json:"task"`
			Branch string `json:"branch"`
		}
		if json.Unmarshal(document, &reservation) != nil || reservation.Task != executionQueueTaskID ||
			reservation.Branch != branch || reservation.Run == "" {
			continue
		}
		signature, err := exec.Command("git", "-C", mailbox, "show", ref+":"+executionQueueReservationSignature).Output()
		if err != nil || strings.TrimSpace(string(signature)) != executionSign(executionQueueReservationKind, executionSecret, document) {
			continue
		}
		return reservation.Run
	}
	return ""
}

func executionQueueLedgerExists(t *testing.T, mailbox, run string) bool {
	t.Helper()
	for _, branch := range executionProtocolRefs(t, mailbox, executionQueueLedgerRefPrefix) {
		if branch == executionQueueLedgerRefPrefix+run {
			return true
		}
	}
	return false
}

func executionQueueSignedLedgerEvents(t *testing.T, mailbox, run string) []executionLateJoinLedgerEvent {
	t.Helper()
	ref := "refs/heads/" + executionQueueLedgerRefPrefix + run
	commits := strings.Fields(bareGit(t, mailbox, "rev-list", "--first-parent", "-n",
		fmt.Sprint(executionQueueLedgerScanLimit), ref))
	events := make([]executionLateJoinLedgerEvent, 0, len(commits))
	for _, commit := range commits {
		document := bareGit(t, mailbox, "show", commit+":"+executionQueueLedgerDocument)
		signature := strings.TrimSpace(bareGit(t, mailbox, "show", commit+":"+executionQueueLedgerSignature))
		require.Equal(t, executionSign(executionQueueLedgerKind, executionSecret, []byte(document)), signature,
			"the observed queue event has the second run's exact signature")
		var record struct {
			Event *executionLateJoinLedgerEvent `json:"event"`
		}
		require.NoError(t, json.Unmarshal([]byte(document), &record))
		if record.Event == nil {
			break
		}
		events = append(events, *record.Event)
	}
	return events
}

// executionQueueSecondBuildDuration leaves room for reporting the completed
// result inside the second task's signed deadline even on the slower TinyGo
// path. The first task's gate supplies the queue wait independently.
func executionQueueSecondBuildDuration() time.Duration {
	if harness.IsTinyGo() {
		return executionQueueTinyGoBuildSeconds * time.Second
	}
	return executionQueueGoBuildSeconds * time.Second
}

func executionHasQueuedBuild(first, second *executionRig) bool {
	for _, rig := range []*executionRig{first, second} {
		for _, run := range rig.runs() {
			if run.Package == executionQueueSecondLabel {
				return true
			}
		}
	}
	return false
}

type executionQueuedRepositoryOptions struct {
	Mailbox            string
	Label              string
	TaskTimeoutSeconds int
	Build              time.Duration
	IsGatedBuild       bool
}

// newExecutionQueuedRepository gives each release an independent signed task
// deadline while both runs share one worker and mailbox.
func newExecutionQueuedRepository(t *testing.T, options executionQueuedRepositoryOptions) *executionRig {
	t.Helper()
	repo := harness.New(t)
	repo.SeedPackage("packages", options.Label)
	buildScript := executionRecordingScript + " && " + repo.TsmarkScript("timeline.log", options.Label, options.Build)
	if options.IsGatedBuild {
		buildScript = executionRecordingScript + " && " + repo.TsmarkCommandScript(harness.TsmarkCommandOptions{
			LogFile: "timeline.log", Label: options.Label, Sleep: executionQueueGateNoSleep,
			Command: fmt.Sprintf(executionQueueGateScriptFormat, executionQueueGateMaximumPolls,
				executionQueueGatePollSeconds, executionQueueGateExitCode),
		})
	}
	cfg := libsConfig(buildScript, 1)
	pinPackages(map[string]*models.RunOnly{
		options.Label: placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
	})(&cfg)
	cfg.Execution = &models.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers:   []models.ExecutionWorkerConfig{{Name: executionNode, Endpoint: "file://" + options.Mailbox}},
		Timeouts:  &models.ExecutionTimeoutsConfig{Preflight: 30, Task: options.TaskTimeoutSeconds, Cancel: 10},
	}
	repo.WriteConfigModel(cfg)
	repo.Commit("feat(" + options.Label + "): bootstrap")
	return newExecutionRigOver(t, repo, options.Mailbox)
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
		space.RunOnly = placedOn(models.RunOnlyChoreographer, models.RunOnlyOrchestrator)
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

// TestExecutionResultReturnsWithoutPeerGitPush keeps the live one-build,
// one-publication control for result settlement. A worker whose canonical task
// pushes are refused still returns its signed Result over the first node's return channel. The
// exact lost-reply receipt lookup is tested by TestWorkerLostResultReplyUsesExactReceipt.
func TestExecutionResultReturnsWithoutPeerGitPush(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 60, Cancel: 10}
	})
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*refs/heads/dispat-task-*", Code: 128})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Zero(t, fault.Matches(), "the peer never pushed a canonical Claim or Result")
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
// local publication starts, and no publish command runs anywhere. The lock
// the run lost is neither re-created nor deleted by it, because a lock on a
// remote after a loss is somebody else's or nobody's and never this run's to
// tidy. The run fails with the lock code.
func TestExecutionLockLossStopsNewEffects(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
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
	// build deleted the lock, no local publish stage can start.
	assert.Empty(t, executionPlacements(res, "core:publish"),
		"no publish task was placed after the lock disappeared")
	assert.Empty(t, executionReleaseTags(rig), "no version was recorded")
	assert.False(t, remoteHoldsLock(t, rig.origin),
		"the lock this run lost is not re-created by it")

	served := requireExecutionUnsettledClaimAfterLockLoss(t, executionUnsettledClaimExpectation{
		rig: rig, worker: worker, task: "core:1:"})
	assert.NotContains(t, served.Stdout, `"message":"publication authorized"`)
	assert.Len(t, rig.runs(), 1,
		"the build that deleted the lock ran, and nothing after it did: %v", rig.runs())
}

// TestExecutionInterruptCancelsRemoteTasks: the release is interrupted while a
// node is building.
//
// A signal is not a failure. The run withdraws the attempt it has in flight
// and waits, bounded, to be told that nothing of it is running; the node kills
// the process group its build started and answers with the phase the frame had
// reached; the package is cancelled without running its failure hook, and the
// next run owes it the same release. The node remains available after the
// signed Cancel, Ack and terminal decision settle.
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
	started := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)

	// The build is under way: an interrupt before the claim would be a
	// scenario about an assignment nobody took.
	executionAwaitProbe(t, rig, executionNode)
	started.Signal(syscall.SIGINT)
	res := started.Wait()

	assert.NotEqual(t, 0, res.Code, "an interrupted release exits non-zero")
	requireChoreographyTaskChain(t, rig.mailbox, rig.branches(),
		"choreography-claim", "cancel", "ack", "choreography-decision")
	assert.Equal(t, 1, executionBuildsOf(rig, "core"), "the cancelled command started once")
	assert.Empty(t, executionReleaseTags(rig), "nothing was recorded")
	for _, run := range rig.runs() {
		assert.NotEqual(t, "probe-onfail", run.Node,
			"an interrupted package runs no failure hook: %v", rig.runs())
	}

	// The node outlived the run that dispatched to it and is still serving.
	served := worker.stop(t)
	assert.Equal(t, 0, served.Code, "the serving node remains available after cancellation")
}

// TestExecutionKilledWorkerBlocksDependentsAndKeepsCapacity (spec vector 14):
// the only node able to run this plan's builds disappears mid-build.
//
// The accepted Claim has an unknown command outcome after the process dies.
// The first node must retain the physical capacity, active run identity and
// release lock until signed Ack or exact recovery settles it. Dependents do
// not execute and no package is published. Secondary tasks that never
// reported a Result must be blocked without inventing admitted finality.
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
	started := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)

	executionAwaitProbe(t, rig, executionNode)
	worker.proc.Signal(syscall.SIGKILL)
	res := started.Wait()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	events := executionEvents(res)
	assert.True(t, harness.IsCodePresent(events, executionRetainedCode),
		"the unsettled accepted Claim retains its signed coordination evidence\nstdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "signed retry safety policy forbids contact-loss successor",
		"this fixture has not opted into repeating uncertain work")
	assert.Contains(t, res.Stdout, "active claim must settle",
		"the first node retains the unknown accepted Claim for reconciliation")
	assert.NotContains(t, res.Stdout, "has no admitted result awaiting settlement",
		"a task without an admitted Result cannot be signed as if it reported one")
	// Nothing downstream of the package that failed was executed anywhere,
	// which is the observable form of "its dependents are blocked": a
	// consumer of a provider that produced nothing has nothing to build
	// against, and a pool with no healthy node has nowhere to put it either.
	built := map[string]bool{}
	for _, run := range rig.runs() {
		built[run.Package] = true
	}
	assert.Equal(t, map[string]bool{"assets": true}, built,
		"and no build of a dependent was placed anywhere: %v", rig.runs())
	assert.Empty(t, executionReleaseTags(rig), "nothing was published")
	assert.True(t, remoteHoldsLock(t, rig.origin),
		"the unresolved accepted Claim keeps native authorization fenced")
	assert.False(t, harness.IsCodePresent(events, executionUnknownCode),
		"and no publication outcome is in doubt, because none was authorized")
	_ = worker.proc.Wait()
}

// TestExecutionCleanupFailureKeepsPublishedOutcome (spec vector 15): deletion
// of this run's disposable preflight probe cannot be pushed after signed seal.
//
// Retained probe data is untidy and nothing else: the signed canonical task
// and ledger branches remain intentionally. The probe carries no release
// record, so its survival erases no record. The packages are published and
// tagged, the refs that remain are named in a warning under the retained
// code, and the run still exits zero (§28.9, "harmless retained transport
// data MAY be a warning").
func TestExecutionCleanupFailureKeepsPublishedOutcome(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
	})
	// The sealed v3 ledger and canonical task refs remain for reconciliation.
	// Only the disposable preflight probe is deleted at close.
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
	assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-worker-"),
		"the failed probe deletion left that exact disposable ref")
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

// executionAwaitProbe waits for a fixture script to record its stage start.
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
