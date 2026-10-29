// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

const (
	pickupFailoverTaskTimeoutSeconds   = 4
	pickupFailoverCancelTimeoutSeconds = 2
	pickupFailoverAdmissionSeconds     = 45
	pickupFailoverWorkerCapacity       = 1
	pickupFailoverRunBudget            = 2
	pickupFailoverJoinWait             = 35 * time.Second
	pickupFailoverOutcomeWait          = 45 * time.Second
	pickupFailoverReleaseWait          = 75 * time.Second
	pickupFailoverPoll                 = 50 * time.Millisecond
	pickupFailoverDiagnosticBytes      = 64 * 1024
	pickupFailoverFirstMarkerName      = "first-computed"
	pickupFailoverSecondMarkerName     = "second-computed"
	pickupFailoverTaskPackage          = "alpha"
	pickupFailoverOutputPushPattern    = "*push*refs/heads/dispat-output-*"
	pickupFailoverCompletedPushPattern = "*push*refs/heads/dispat-completed-*"
	pickupFailoverContactLostKind      = "pickup-contact-lost"
	pickupFailoverClaimKind            = "claim-accepted"
	pickupFailoverResultKind           = "result-accepted"
	pickupFailoverTerminalKind         = "terminal"
	pickupFailoverAckKind              = "ack-accepted"
	pickupFailoverSettledKind          = "pickup-settled"
)

func pickupFailoverEventsForTask(events []executionLateJoinLedgerEvent, task string) []executionLateJoinLedgerEvent {
	selected := make([]executionLateJoinLedgerEvent, 0)
	for _, event := range events {
		if event.Task == task {
			selected = append(selected, event)
		}
	}
	return selected
}

func pickupFailoverFindEvent(events []executionLateJoinLedgerEvent, kind, node string) (int, executionLateJoinLedgerEvent) {
	for index, event := range events {
		if event.Kind == kind && (node == "" || event.Node == node) {
			return index, event
		}
	}
	return -1, executionLateJoinLedgerEvent{}
}

func TestExecutionTwoWorkersFailOverAfterSignedPickupContactLoss(t *testing.T) {
	testExecutionTwoWorkersFailOverAfterSignedPickupContactLoss(t, false)
}

func TestExecutionInterpretationTwoWorkersFailOverAfterSignedPickupContactLoss(t *testing.T) {
	testExecutionTwoWorkersFailOverAfterSignedPickupContactLoss(t, true)
}

func testExecutionTwoWorkersFailOverAfterSignedPickupContactLoss(t *testing.T, interpreted bool) {
	if runtime.GOOS == "windows" {
		t.Skip("the durable distributed node-slot backend is unavailable on Windows")
	}
	markers := t.TempDir()
	firstComputed := filepath.Join(markers, pickupFailoverFirstMarkerName)
	secondComputed := filepath.Join(markers, pickupFailoverSecondMarkerName)
	publicKey, seed := executionLateJoinIdentity(t)
	rig := newExecutionPlacementRig(t, []string{pickupFailoverTaskPackage},
		func(*harness.Repo) string {
			return executionRecordingScript + " && " + fmt.Sprintf(
				`case "$DISPAT_EXECUTION_NODE" in %s) printf first > %q ;; %s) printf second > %q ;; esac`,
				executionNode, firstComputed, executionSecondNode, secondComputed)
		},
		func(cfg *models.File) {
			cfg.Execution.RetryAfterUncertainAttempt = true
			cfg.Execution.MaxAttempts = models.Int(interpretationSecondAttemptLimit)
			cfg.Concurrency = []int{pickupFailoverRunBudget, pickupFailoverWorkerCapacity}
			cfg.Execution.Concurrency = models.Int(pickupFailoverRunBudget)
			cfg.Execution.Timeouts.Admission = pickupFailoverAdmissionSeconds
			cfg.Execution.Timeouts.Task = pickupFailoverTaskTimeoutSeconds
			cfg.Execution.Timeouts.Cancel = pickupFailoverCancelTimeoutSeconds
			cfg.Execution.Workers = append(cfg.Execution.Workers, models.ExecutionWorkerConfig{
				Name: executionSecondNode, Endpoint: cfg.Execution.Workers[0].Endpoint,
				LateJoin: true, IdentityPublicKey: publicKey})
		},
		pinPackages(map[string]*models.RunOnly{
			pickupFailoverTaskPackage: placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
		}))
	outputPushPattern := pickupFailoverOutputPushPattern
	if interpreted {
		outputPushPattern = pickupFailoverCompletedPushPattern
	}
	oldOutcomeGate := harness.NewGitFault(t, harness.GitFault{
		Pattern: outputPushPattern, Nth: 1, Hold: true})
	firstWorker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, oldOutcomeGate.Env()...)
	firstStopped := false
	defer func() {
		oldOutcomeGate.Resume()
		if !firstStopped {
			firstWorker.stop(t)
		}
	}()
	releaseArgs := []string{"release", "--return-endpoint", rig.returnURL}
	if interpreted {
		releaseArgs = append(releaseArgs, "--distribution-mode", "interpretation")
	}
	firstNode := rig.repo.StartReleaseEnv(rig.env(), releaseArgs...)
	require.Eventually(t, oldOutcomeGate.IsHeld, pickupFailoverJoinWait, pickupFailoverPoll,
		"the first worker must complete its command and hold the immutable outcome push")
	require.FileExists(t, firstComputed)
	require.NoFileExists(t, secondComputed)
	events := executionLateJoinLedgerEvents(t, rig.mailbox)
	_, firstClaim := pickupFailoverFindEvent(events, pickupFailoverClaimKind, executionNode)
	require.NotEmpty(t, firstClaim.PickupID, "the old physical pickup has a signed unique identity")
	task := firstClaim.Task
	require.NotEmpty(t, task)
	_, earlySecondClaim := pickupFailoverFindEvent(pickupFailoverEventsForTask(events, task),
		pickupFailoverClaimKind, executionSecondNode)
	require.Empty(t, earlySecondClaim.PickupID, "no deliberate concurrent pickup precedes contact loss")

	secondWorker := rig.startWorker(executionWorkerConfig(rig.mailbox, func(cfg *models.ExecutionConfig) {
		cfg.Name = executionSecondNode
		cfg.Concurrency = models.Int(pickupFailoverWorkerCapacity)
		cfg.IdentityKeyEnv = executionLateJoinIdentitySeedEnv
	}), 0, executionLateJoinIdentitySeedEnv+"="+seed)
	secondStopped := false
	defer func() {
		if !secondStopped {
			secondWorker.stop(t)
		}
	}()
	var observedMu sync.Mutex
	var lastObservedEvents []executionLateJoinLedgerEvent
	if !assert.Eventually(t, func() bool {
		ledger := executionLateJoinLedgerEvents(t, rig.mailbox)
		observedMu.Lock()
		lastObservedEvents = ledger
		observedMu.Unlock()
		events := pickupFailoverEventsForTask(ledger, task)
		lost, _ := pickupFailoverFindEvent(events, pickupFailoverContactLostKind, executionNode)
		second, _ := pickupFailoverFindEvent(events, pickupFailoverClaimKind, executionSecondNode)
		return lost >= 0 && second > lost
	}, pickupFailoverOutcomeWait, pickupFailoverPoll,
		"a second process may claim only after signed Cancel grace and contact loss") {
		observedMu.Lock()
		t.Logf("last authenticated failover ledger: %+v", lastObservedEvents)
		observedMu.Unlock()
		pickupFailoverLogOutput(t, "first node", firstNode)
		pickupFailoverLogOutput(t, "first worker", firstWorker.proc)
		pickupFailoverLogOutput(t, "second worker", secondWorker.proc)
		t.FailNow()
	}
	require.Eventually(t, func() bool {
		events := pickupFailoverEventsForTask(executionLateJoinLedgerEvents(t, rig.mailbox), task)
		_, result := pickupFailoverFindEvent(events, pickupFailoverResultKind, executionSecondNode)
		_, terminal := pickupFailoverFindEvent(events, pickupFailoverTerminalKind, executionSecondNode)
		return result.OutputOID != "" && terminal.TaskOID != ""
	}, pickupFailoverOutcomeWait, pickupFailoverPoll,
		"the spare worker must complete the successor before the old contact returns")
	require.FileExists(t, secondComputed)
	require.True(t, oldOutcomeGate.IsHeld(), "the old physical pickup remains charged")
	events = pickupFailoverEventsForTask(executionLateJoinLedgerEvents(t, rig.mailbox), task)
	_, prematureOldSettlement := pickupFailoverFindEvent(events, pickupFailoverSettledKind, executionNode)
	require.Empty(t, prematureOldSettlement.PickupID,
		"winning logical finality cannot release the old physical pickup")
	oldOutcomeGate.Resume()
	result := firstNode.WaitForDiagnostic(pickupFailoverReleaseWait, func() {
		t.Logf("failover signed ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
		stdout, stderr := firstWorker.proc.CapturedOutput()
		t.Logf("first worker stdout:\n%s\nstderr:\n%s", stdout, stderr)
		stdout, stderr = secondWorker.proc.CapturedOutput()
		t.Logf("second worker stdout:\n%s\nstderr:\n%s", stdout, stderr)
	})
	if result.Code != 0 {
		t.Logf("signed failover task events: %+v",
			pickupFailoverEventsForTask(executionLateJoinLedgerEvents(t, rig.mailbox), task))
	}
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	events = pickupFailoverEventsForTask(executionLateJoinLedgerEvents(t, rig.mailbox), task)
	lostIndex, lost := pickupFailoverFindEvent(events, pickupFailoverContactLostKind, executionNode)
	secondIndex, secondClaim := pickupFailoverFindEvent(events, pickupFailoverClaimKind, executionSecondNode)
	require.Greater(t, secondIndex, lostIndex)
	require.NotEqual(t, firstClaim.PickupID, secondClaim.PickupID)
	_, winner := pickupFailoverFindEvent(events, pickupFailoverResultKind, executionSecondNode)
	require.NotEmpty(t, winner.OutputOID)
	_, oldResult := pickupFailoverFindEvent(events, pickupFailoverResultKind, executionNode)
	require.Empty(t, oldResult.OutputOID, "the old immutable outcome cannot overwrite the accepted winner")
	_, oldAck := pickupFailoverFindEvent(events, pickupFailoverAckKind, executionNode)
	_, oldSettled := pickupFailoverFindEvent(events, pickupFailoverSettledKind, executionNode)
	require.Equal(t, firstClaim.PickupID, oldAck.PickupID)
	require.Equal(t, firstClaim.PickupID, oldSettled.PickupID)
	require.NotEqual(t, lost.PickupID, secondClaim.PickupID)
	var outputTips []string
	if interpreted {
		statuses := interpretationPickupOutcomes(t, rig.mailbox,
			[]executionLateJoinLedgerEvent{firstClaim, secondClaim})
		require.Equal(t, executionSagaSuccessfulStatus, statuses[firstClaim.PickupID])
		require.Equal(t, executionSagaSuccessfulStatus, statuses[secondClaim.PickupID])
		for _, ref := range executionProtocolRefs(t, rig.mailbox, interpretationCompletedBranchPrefix) {
			outputTips = append(outputTips, strings.TrimSpace(bareGit(t, rig.mailbox,
				"rev-parse", "refs/heads/"+ref)))
		}
	} else {
		outputTips = executionSagaWorkerOutcomeTips(t, rig.mailbox, executionSagaSuccessfulStatus)
	}
	require.Len(t, outputTips, pickupFailoverRunBudget,
		"both distinct pickups retain signed pending and outcome branches")
	require.Contains(t, outputTips, winner.OutputOID,
		"the accepted winner is one exact worker-owned outcome")
	firstWorker.stop(t)
	firstStopped = true
	secondWorker.stop(t)
	secondStopped = true
	require.Equal(t, []string{pickupFailoverTaskPackage + "@0.1.0"}, rig.repo.TagList())
}

func pickupFailoverLogOutput(t *testing.T, name string, process *harness.Proc) {
	t.Helper()
	stdout, stderr := process.CapturedOutput()
	if len(stdout) > pickupFailoverDiagnosticBytes {
		stdout = stdout[len(stdout)-pickupFailoverDiagnosticBytes:]
	}
	if len(stderr) > pickupFailoverDiagnosticBytes {
		stderr = stderr[len(stderr)-pickupFailoverDiagnosticBytes:]
	}
	t.Logf("%s stdout tail:\n%s\nstderr tail:\n%s", name, stdout, stderr)
}
