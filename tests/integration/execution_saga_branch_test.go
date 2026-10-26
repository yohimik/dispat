// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

const executionSagaBuildOutputRoot = "dist"
const executionSagaOutputPath = "outputs/dist/marker.txt"
const executionSagaOutputContents = "saga-output"
const executionSagaDecisionDocumentPath = "dispat/choreography-decision.json"
const executionSagaDecisionSignaturePath = "dispat/choreography-decision.sig"
const executionSagaDecisionSignatureKind = "choreography-decision"
const executionSagaTerminalEventKind = "terminal"
const executionSagaPendingPrefix = "dispat-pending-"
const executionSagaOutputPrefix = "dispat-output-"
const executionSagaFailedPrefix = "dispat-failed-"
const executionSagaPendingDocumentPath = "dispat/pending.json"
const executionSagaPendingSignaturePath = "dispat/pending.sig"
const executionSagaResultDocumentPath = "dispat/result.json"
const executionSagaResultSignaturePath = "dispat/result.sig"
const executionSagaPendingSignatureKind = "pending"
const executionSagaResultSignatureKind = "result"
const executionSagaSuccessfulStatus = "succeeded"
const executionSagaFailedStatus = "failed"
const executionSagaCancelledStatus = "cancelled"
const executionSagaReleaseWait = 60 * time.Second
const executionSagaPendingHoldWait = 30 * time.Second
const executionSagaPendingHoldPoll = 25 * time.Millisecond

type executionSagaDecision struct {
	Succeeded bool
	ResultOID string
}

type executionSagaPending struct {
	PickupID string `json:"pickupId"`
	ClaimOID string `json:"claimOid"`
}

type executionSagaResult struct {
	PickupID      string `json:"pickupId"`
	PendingBranch string `json:"pendingBranch"`
	PendingOID    string `json:"pendingOid"`
	OutcomeBranch string `json:"outcomeBranch"`
	Status        string `json:"status"`
}

func executionSagaSingleParent(t *testing.T, mailbox, oid string) string {
	t.Helper()
	parts := strings.Fields(bareGit(t, mailbox, "rev-list", "--parents", "-n", "1", oid))
	require.Len(t, parts, 2, "a worker pickup commit has exactly one parent")
	require.Equal(t, oid, parts[0])
	return parts[1]
}

func executionSagaWorkerOutcomeTips(t *testing.T, mailbox, status string) []string {
	t.Helper()
	pendingRefs := executionProtocolRefs(t, mailbox, executionSagaPendingPrefix)
	outputRefs := executionProtocolRefs(t, mailbox, executionSagaOutputPrefix)
	failedRefs := executionProtocolRefs(t, mailbox, executionSagaFailedPrefix)
	require.NotEmpty(t, pendingRefs, "the worker created its signed pending branch before effects")
	refs, prefix := outputRefs, executionSagaOutputPrefix
	if status == executionSagaFailedStatus || status == executionSagaCancelledStatus {
		refs, prefix = failedRefs, executionSagaFailedPrefix
		require.Empty(t, outputRefs, "a failed pickup has no contradictory output branch")
	} else {
		require.Equal(t, executionSagaSuccessfulStatus, status)
		require.Empty(t, failedRefs, "a successful pickup has no failed branch")
	}
	require.NotEmpty(t, refs, "the worker created its signed outcome branch")
	require.Len(t, refs, len(pendingRefs), "each pending pickup has one outcome branch")
	var tips []string
	for _, ref := range refs {
		suffix := strings.TrimPrefix(ref, prefix)
		pendingRef := executionSagaPendingPrefix + suffix
		pendingOID := strings.TrimSpace(bareGit(t, mailbox, "rev-parse", "refs/heads/"+pendingRef))
		outcomeOID := strings.TrimSpace(bareGit(t, mailbox, "rev-parse", "refs/heads/"+ref))
		require.Equal(t, pendingOID, executionSagaSingleParent(t, mailbox, outcomeOID),
			"the output commit directly descends from this pickup's pending tip")
		pendingDocument := bareGit(t, mailbox, "show", pendingOID+":"+executionSagaPendingDocumentPath)
		pendingSignature := strings.TrimSpace(bareGit(t, mailbox, "show", pendingOID+":"+executionSagaPendingSignaturePath))
		require.Equal(t, executionSign(executionSagaPendingSignatureKind, executionSecret,
			[]byte(pendingDocument)), pendingSignature)
		var pending executionSagaPending
		require.NoError(t, json.Unmarshal([]byte(pendingDocument), &pending))
		require.NotEmpty(t, pending.PickupID)
		require.Equal(t, pending.ClaimOID, executionSagaSingleParent(t, mailbox, pendingOID),
			"the pending commit directly descends from the accepted canonical Claim")
		resultDocument := bareGit(t, mailbox, "show", outcomeOID+":"+executionSagaResultDocumentPath)
		resultSignature := strings.TrimSpace(bareGit(t, mailbox, "show", outcomeOID+":"+executionSagaResultSignaturePath))
		require.Equal(t, executionSign(executionSagaResultSignatureKind, executionSecret,
			[]byte(resultDocument)), resultSignature)
		var result executionSagaResult
		require.NoError(t, json.Unmarshal([]byte(resultDocument), &result))
		require.Equal(t, pending.PickupID, result.PickupID)
		require.Equal(t, pendingRef, result.PendingBranch)
		require.Equal(t, pendingOID, result.PendingOID)
		require.Equal(t, ref, result.OutcomeBranch)
		require.Equal(t, status, result.Status)
		tips = append(tips, outcomeOID)
	}
	return tips
}

func executionSagaRelease(t *testing.T, rig *executionRig) harness.RunResult {
	t.Helper()
	return rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL).
		WaitForDiagnostic(executionSagaReleaseWait, func() {
			t.Logf("pending refs: %v", executionProtocolRefs(t, rig.mailbox, executionSagaPendingPrefix))
			t.Logf("output refs: %v", executionProtocolRefs(t, rig.mailbox, executionSagaOutputPrefix))
			t.Logf("failed refs: %v", executionProtocolRefs(t, rig.mailbox, executionSagaFailedPrefix))
			if len(executionProtocolRefs(t, rig.mailbox, "dispat-ledger-")) != 0 {
				t.Logf("signed saga ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
			}
		})
}

func executionSagaSignedTerminalTips(t *testing.T, mailbox string) []string {
	t.Helper()
	refs := executionProtocolRefs(t, mailbox, "dispat-task-")
	require.NotEmpty(t, refs, "the completed task branches remain in coordination Git")
	tips := make(map[string]bool, len(refs))
	for _, ref := range refs {
		tips[strings.TrimSpace(bareGit(t, mailbox, "rev-parse", "refs/heads/"+ref))] = true
	}
	var completed []string
	for _, event := range executionLateJoinLedgerEvents(t, mailbox) {
		if event.Kind != executionSagaTerminalEventKind || !tips[event.TaskOID] {
			continue
		}
		paths := bareGit(t, mailbox, "ls-tree", "-r", "--name-only", event.TaskOID)
		if !strings.Contains(paths, executionSagaDecisionDocumentPath) {
			continue // branchless local-authority finality is ledger-only
		}
		document := bareGit(t, mailbox, "show", event.TaskOID+":"+executionSagaDecisionDocumentPath)
		signature := strings.TrimSpace(bareGit(t, mailbox, "show",
			event.TaskOID+":"+executionSagaDecisionSignaturePath))
		require.Equal(t, executionSign(executionSagaDecisionSignatureKind,
			executionSecret, []byte(document)), signature)
		var decision executionSagaDecision
		require.NoError(t, json.Unmarshal([]byte(document), &decision))
		if decision.Succeeded {
			require.NotEmpty(t, decision.ResultOID,
				"a finished task names the accepted Result or Ack receipt")
			completed = append(completed, event.TaskOID)
		}
	}
	require.NotEmpty(t, completed, "the signed ledger identifies a retained finished branch")
	return completed
}

func TestExecutionCompletedOutputLivesOnAssignedTaskBranch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.BuildOutputs = []string{executionSagaBuildOutputRoot}
		cfg.Scripts["build"] = models.Script{`mkdir -p dist && printf saga-output > dist/marker.txt`}
	}, pinPackages(map[string]*models.RunOnly{"core": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)}))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	res := executionSagaRelease(t, rig)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	workerTips := executionSagaWorkerOutcomeTips(t, rig.mailbox, executionSagaSuccessfulStatus)
	workerOutputFound := false
	for _, tip := range workerTips {
		paths := bareGit(t, rig.mailbox, "ls-tree", "-r", "--name-only", tip)
		if strings.Contains(paths, executionSagaOutputPath) {
			workerOutputFound = true
			require.Equal(t, executionSagaOutputContents,
				bareGit(t, rig.mailbox, "show", tip+":"+executionSagaOutputPath))
		}
	}
	require.True(t, workerOutputFound, "the worker outcome branch retains the declared output bytes")
	found := false
	for _, tip := range executionSagaSignedTerminalTips(t, rig.mailbox) {
		paths := bareGit(t, rig.mailbox, "ls-tree", "-r", "--name-only", tip)
		if !strings.Contains(paths, executionSagaOutputPath) {
			continue
		}
		found = true
		require.Equal(t, executionSagaOutputContents,
			bareGit(t, rig.mailbox, "show", tip+":"+executionSagaOutputPath))
	}
	require.True(t, found, "a completed build retains its declared bytes on the assigned task branch")
}

func TestExecutionCompletedNoOutputTaskKeepsSignedBranch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionRig(t, pinPackages(map[string]*models.RunOnly{"core": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)}))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	res := executionSagaRelease(t, rig)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for _, tip := range executionSagaWorkerOutcomeTips(t, rig.mailbox, executionSagaSuccessfulStatus) {
		paths := bareGit(t, rig.mailbox, "ls-tree", "-r", "--name-only", tip)
		require.NotContains(t, paths, "outputs/", "the worker's signed no-output Result is retained")
	}
	for _, tip := range executionSagaSignedTerminalTips(t, rig.mailbox) {
		paths := bareGit(t, rig.mailbox, "ls-tree", "-r", "--name-only", tip)
		require.NotContains(t, paths, "outputs/",
			"a no-output task still has a retained signed finished branch")
	}
}

func TestExecutionFailedWorkerPickupRetainsSignedFailedBranch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Scripts["build"] = models.Script{"exit 7"}
	}, pinPackages(map[string]*models.RunOnly{"core": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)}))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	res := executionSagaRelease(t, rig)
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	failedTips := executionSagaWorkerOutcomeTips(t, rig.mailbox, executionSagaFailedStatus)
	require.NotEmpty(t, failedTips)
	terminalFound := false
	for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
		if event.Kind != executionSagaTerminalEventKind {
			continue
		}
		paths := bareGit(t, rig.mailbox, "ls-tree", "-r", "--name-only", event.TaskOID)
		if !strings.Contains(paths, executionSagaDecisionDocumentPath) {
			continue
		}
		document := bareGit(t, rig.mailbox, "show", event.TaskOID+":"+executionSagaDecisionDocumentPath)
		signature := strings.TrimSpace(bareGit(t, rig.mailbox, "show",
			event.TaskOID+":"+executionSagaDecisionSignaturePath))
		require.Equal(t, executionSign(executionSagaDecisionSignatureKind,
			executionSecret, []byte(document)), signature)
		var decision executionSagaDecision
		require.NoError(t, json.Unmarshal([]byte(document), &decision))
		if !decision.Succeeded {
			terminalFound = true
		}
	}
	require.True(t, terminalFound,
		"the first node retained failed canonical task finality\nstdout:\n%s\nstderr:\n%s\nledger: %+v",
		res.Stdout, res.Stderr, executionLateJoinLedgerEvents(t, rig.mailbox))
}

func TestExecutionCancelledBeforeWorkerCommandRetainsFailedPickupBranch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend and Git fault wrapper are unavailable on Windows")
	}
	commandMarker := filepath.Join(t.TempDir(), "build-command-started")
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Scripts["build"] = models.Script{"touch " + harness.ShQuote(commandMarker)}
	}, pinPackages(map[string]*models.RunOnly{"core": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)}))
	pendingGate := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*refs/heads/dispat-pending-*", Nth: 1, Hold: true})
	defer pendingGate.Resume()
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60, pendingGate.Env()...)
	defer worker.stop(t)
	release := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	for until := time.After(executionSagaPendingHoldWait); !pendingGate.IsHeld(); {
		select {
		case <-until:
			pendingGate.Resume()
			res := release.WaitFor(executionSagaReleaseWait)
			t.Fatalf("the accepted worker pending push was not held; stdout:\n%s\nstderr:\n%s",
				res.Stdout, res.Stderr)
		case <-time.After(executionSagaPendingHoldPoll):
		}
	}
	branch := executionAwaitBranch(t, rig, "build")
	release.Signal(syscall.SIGINT)
	executionAwaitMessage(t, rig.mailbox, branch, "cancel")
	require.NoFileExists(t, commandMarker, "a pending pickup has not started its command")
	pendingGate.Resume()
	res := release.WaitFor(executionSagaReleaseWait)
	require.NotEqual(t, 0, res.Code, "an interrupted release cannot publish")
	require.NoFileExists(t, commandMarker, "a cancelled pickup never starts its command")
	require.NotEmpty(t, executionSagaWorkerOutcomeTips(t, rig.mailbox, executionSagaCancelledStatus))
	requireChoreographyTaskChain(t, rig.mailbox, rig.branches(),
		"choreography-claim", "cancel", "ack", "choreography-decision")
}
