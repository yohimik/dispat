// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

const interpretationReleaseWait = 60 * time.Second
const interpretationCompletedBranchPrefix = "dispat-completed-"
const interpretationBranchTimeFormat = "20060102T150405.000000000Z"
const interpretationInvalidZeroAttempts = 0
const interpretationInvalidNegativeAttempts = -1
const interpretationInvalidAboveCeilingAttempts = 65
const interpretationSecondAttemptLimit = 2
const interpretationFailedExitCode = 7

func interpretationWorkerClaims(t *testing.T, mailbox string) []executionLateJoinLedgerEvent {
	t.Helper()
	var claims []executionLateJoinLedgerEvent
	for _, event := range executionLateJoinLedgerEvents(t, mailbox) {
		if event.Kind == pickupFailoverClaimKind && event.Node == executionNode {
			claims = append(claims, event)
		}
	}
	return claims
}

// interpretationPickupOutcomes follows every worker-owned branch back to its
// accepted Claim. A failed retry and its later success have distinct physical
// histories even though the first node chooses one logical terminal.
func interpretationPickupOutcomes(t *testing.T, mailbox string,
	claims []executionLateJoinLedgerEvent) map[string]string {
	t.Helper()
	byPickup := make(map[string]executionLateJoinLedgerEvent, len(claims))
	for _, claim := range claims {
		require.NotEmpty(t, claim.PickupID)
		_, duplicate := byPickup[claim.PickupID]
		require.False(t, duplicate, "each accepted pickup has a unique identity")
		byPickup[claim.PickupID] = claim
	}
	pendingRefs := executionProtocolRefs(t, mailbox, executionSagaPendingPrefix)
	require.Len(t, pendingRefs, len(claims))
	seen := make(map[string]string, len(claims))
	for _, outcome := range []struct {
		prefix string
		status string
	}{
		{interpretationCompletedBranchPrefix, executionSagaSuccessfulStatus},
		{executionSagaFailedPrefix, executionSagaFailedStatus},
	} {
		for _, ref := range executionProtocolRefs(t, mailbox, outcome.prefix) {
			suffix := strings.TrimPrefix(ref, outcome.prefix)
			timestamp, _, found := strings.Cut(suffix, "-")
			require.True(t, found)
			_, err := time.Parse(interpretationBranchTimeFormat, timestamp)
			require.NoError(t, err, "the ref names its pickup creation time")
			pendingRef := executionSagaPendingPrefix + suffix
			require.Contains(t, pendingRefs, pendingRef)
			pendingOID := strings.TrimSpace(bareGit(t, mailbox, "rev-parse", "refs/heads/"+pendingRef))
			outcomeOID := strings.TrimSpace(bareGit(t, mailbox, "rev-parse", "refs/heads/"+ref))
			require.Equal(t, pendingOID, executionSagaSingleParent(t, mailbox, outcomeOID))
			pendingDocument := bareGit(t, mailbox, "show", pendingOID+":"+executionSagaPendingDocumentPath)
			pendingSignature := strings.TrimSpace(bareGit(t, mailbox, "show", pendingOID+":"+executionSagaPendingSignaturePath))
			require.Equal(t, executionSign(executionSagaPendingSignatureKind, executionSecret,
				[]byte(pendingDocument)), pendingSignature)
			var pending executionSagaPending
			require.NoError(t, json.Unmarshal([]byte(pendingDocument), &pending))
			claim, accepted := byPickup[pending.PickupID]
			require.True(t, accepted, "the pending branch belongs to an accepted pickup")
			require.Equal(t, claim.TaskOID, pending.ClaimOID)
			require.Equal(t, pending.ClaimOID, executionSagaSingleParent(t, mailbox, pendingOID))
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
			require.Equal(t, outcome.status, result.Status)
			_, duplicate := seen[result.PickupID]
			require.False(t, duplicate, "one pickup cannot have contradictory outcome branches")
			seen[result.PickupID] = result.Status
		}
	}
	require.Len(t, seen, len(claims), "every accepted pickup has exactly one signed outcome")
	return seen
}

func TestExecutionInterpretationFixesTheSamePlanAsCompilation(t *testing.T) {
	rig := newExecutionRig(t)
	compiled := rig.repo.CommandEnv(rig.env(), "status", "--distribution-mode", "compilation")
	interpreted := rig.repo.CommandEnv(rig.env(), "status", "--distribution-mode", "interpretation")
	require.Equal(t, 0, compiled.Code, "stdout:\n%s\nstderr:\n%s", compiled.Stdout, compiled.Stderr)
	require.Equal(t, 0, interpreted.Code, "stdout:\n%s\nstderr:\n%s", interpreted.Stdout, interpreted.Stderr)
	require.Equal(t, planDigestOf(t, compiled), planDigestOf(t, interpreted),
		"mode changes task execution, not semantic release selection")
	require.Empty(t, rig.repo.TagList())
	require.Empty(t, executionProtocolRefs(t, rig.mailbox, "dispat-task-"))
}

func TestExecutionInterpretationRunsWorkerTaskWithoutOutputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the durable distributed node-slot backend is unavailable on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
	}, pinPackages(map[string]*models.RunOnly{
		"core": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
	}))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	result := rig.repo.StartReleaseEnv(rig.env(), "release", "--distribution-mode", "interpretation",
		"--return-endpoint", rig.returnURL).WaitForDiagnostic(interpretationReleaseWait, func() {
		t.Logf("signed interpretation ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
	})
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	require.Equal(t, 1, executionBuildCount(rig, "core"))
	require.Equal(t, executionNode, rig.nodesByPackage()["core"])
	require.Equal(t, []string{"core@0.1.0"}, rig.repo.TagList())
	claims := interpretationWorkerClaims(t, rig.mailbox)
	require.Len(t, claims, 1, "the default is one total accepted pickup")
	completedRefs := executionProtocolRefs(t, rig.mailbox, interpretationCompletedBranchPrefix)
	require.Len(t, completedRefs, 1, "the interpreted worker retains one completed branch")
	pendingRefs := executionProtocolRefs(t, rig.mailbox, executionSagaPendingPrefix)
	require.Len(t, pendingRefs, 1, "the command required one confirmed pending branch")
	suffix := strings.TrimPrefix(completedRefs[0], interpretationCompletedBranchPrefix)
	timestamp, _, found := strings.Cut(suffix, "-")
	require.True(t, found)
	_, err := time.Parse(interpretationBranchTimeFormat, timestamp)
	require.NoError(t, err, "the ref names its pickup creation time")
	pendingRef := executionSagaPendingPrefix + suffix
	require.Equal(t, pendingRefs[0], pendingRef)
	pendingOID := strings.TrimSpace(bareGit(t, rig.mailbox, "rev-parse", "refs/heads/"+pendingRef))
	completedOID := strings.TrimSpace(bareGit(t, rig.mailbox, "rev-parse", "refs/heads/"+completedRefs[0]))
	require.Equal(t, pendingOID, executionSagaSingleParent(t, rig.mailbox, completedOID))
	pendingDocument := bareGit(t, rig.mailbox, "show", pendingOID+":"+executionSagaPendingDocumentPath)
	pendingSignature := strings.TrimSpace(bareGit(t, rig.mailbox, "show", pendingOID+":"+executionSagaPendingSignaturePath))
	require.Equal(t, executionSign(executionSagaPendingSignatureKind, executionSecret,
		[]byte(pendingDocument)), pendingSignature)
	var pending executionSagaPending
	require.NoError(t, json.Unmarshal([]byte(pendingDocument), &pending))
	require.NotEmpty(t, pending.PickupID)
	require.Equal(t, claims[0].PickupID, pending.PickupID)
	require.Equal(t, pending.ClaimOID, executionSagaSingleParent(t, rig.mailbox, pendingOID))
	resultDocument := bareGit(t, rig.mailbox, "show", completedOID+":"+executionSagaResultDocumentPath)
	resultSignature := strings.TrimSpace(bareGit(t, rig.mailbox, "show", completedOID+":"+executionSagaResultSignaturePath))
	require.Equal(t, executionSign(executionSagaResultSignatureKind, executionSecret,
		[]byte(resultDocument)), resultSignature)
	var pickupResult executionSagaResult
	require.NoError(t, json.Unmarshal([]byte(resultDocument), &pickupResult))
	require.Equal(t, pending.PickupID, pickupResult.PickupID)
	require.Equal(t, executionSagaSuccessfulStatus, pickupResult.Status)
	require.Equal(t, pendingOID, pickupResult.PendingOID)
	require.Equal(t, completedRefs[0], pickupResult.OutcomeBranch)
	require.NotContains(t, bareGit(t, rig.mailbox, "ls-tree", "-r", "--name-only", completedOID), "outputs/")
	require.NotEmpty(t, executionSagaSignedTerminalTips(t, rig.mailbox),
		"a no-output task still retains signed canonical completion")
}

func TestExecutionInterpretationRetriesSettledFailureWithinTotalAttemptLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the durable distributed node-slot backend is unavailable on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Execution.MaxAttempts = models.Int(interpretationSecondAttemptLimit)
		cfg.Scripts["build"] = models.Script{executionRecordingScript + ` &&
test "$(wc -l < "$DISPAT_IT_EXECUTION_LOG")" -gt 1`}
	}, pinPackages(map[string]*models.RunOnly{
		"core": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
	}))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	result := rig.repo.StartReleaseEnv(rig.env(), "release", "--distribution-mode", "interpretation",
		"--return-endpoint", rig.returnURL).WaitForDiagnostic(interpretationReleaseWait, func() {
		t.Logf("signed interpretation retry ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
	})
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	claims := interpretationWorkerClaims(t, rig.mailbox)
	require.Len(t, claims, interpretationSecondAttemptLimit)
	require.NotEqual(t, claims[0].PickupID, claims[1].PickupID)
	outcomes := interpretationPickupOutcomes(t, rig.mailbox, claims)
	require.Equal(t, executionSagaFailedStatus, outcomes[claims[0].PickupID])
	require.Equal(t, executionSagaSuccessfulStatus, outcomes[claims[1].PickupID])
	events := executionLateJoinLedgerEvents(t, rig.mailbox)
	failedResultIndex, secondClaimIndex := -1, -1
	for index, event := range events {
		if event.Kind == pickupFailoverResultKind && event.PickupID == claims[0].PickupID {
			failedResultIndex = index
		}
		if event.Kind == pickupFailoverClaimKind && event.PickupID == claims[1].PickupID {
			secondClaimIndex = index
		}
	}
	require.GreaterOrEqual(t, failedResultIndex, 0, "the first failed Result was signed")
	require.Greater(t, secondClaimIndex, failedResultIndex,
		"the second pickup follows settled first-attempt failure, never deliberate fanout")
	require.Equal(t, interpretationSecondAttemptLimit, executionBuildCount(rig, "core"))
	require.Len(t, executionProtocolRefs(t, rig.mailbox, executionSagaPendingPrefix), interpretationSecondAttemptLimit)
	require.Len(t, executionProtocolRefs(t, rig.mailbox, executionSagaFailedPrefix), 1)
	require.Len(t, executionProtocolRefs(t, rig.mailbox, interpretationCompletedBranchPrefix), 1)
	require.Equal(t, []string{"core@0.1.0"}, rig.repo.TagList())
}

func TestExecutionInterpretationExhaustsTotalAttemptsAfterSettledFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the durable distributed node-slot backend is unavailable on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Execution.MaxAttempts = models.Int(interpretationSecondAttemptLimit)
		cfg.Scripts["build"] = models.Script{executionRecordingScript +
			fmt.Sprintf(" && exit %d", interpretationFailedExitCode)}
	}, pinPackages(map[string]*models.RunOnly{
		"core": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
	}))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	result := rig.repo.StartReleaseEnv(rig.env(), "release", "--distribution-mode", "interpretation",
		"--return-endpoint", rig.returnURL).WaitForDiagnostic(interpretationReleaseWait, func() {
		t.Logf("signed interpretation exhaustion ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
	})
	require.NotEqual(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	claims := interpretationWorkerClaims(t, rig.mailbox)
	require.Len(t, claims, interpretationSecondAttemptLimit)
	for _, status := range interpretationPickupOutcomes(t, rig.mailbox, claims) {
		require.Equal(t, executionSagaFailedStatus, status)
	}
	require.Equal(t, interpretationSecondAttemptLimit, executionBuildCount(rig, "core"))
	require.Len(t, executionProtocolRefs(t, rig.mailbox, executionSagaFailedPrefix), interpretationSecondAttemptLimit)
	require.Empty(t, executionProtocolRefs(t, rig.mailbox, interpretationCompletedBranchPrefix))
	require.Empty(t, rig.repo.TagList())
}

func TestExecutionInterpretationDefaultsToOneFailedPickup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the durable distributed node-slot backend is unavailable on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Scripts["build"] = models.Script{executionRecordingScript +
			fmt.Sprintf(" && exit %d", interpretationFailedExitCode)}
	}, pinPackages(map[string]*models.RunOnly{
		"core": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
	}))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	result := rig.repo.StartReleaseEnv(rig.env(), "release", "--distribution-mode", "interpretation",
		"--return-endpoint", rig.returnURL).WaitForDiagnostic(interpretationReleaseWait, func() {
		t.Logf("signed interpretation failure ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
	})
	require.NotEqual(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	claims := interpretationWorkerClaims(t, rig.mailbox)
	require.Len(t, claims, 1,
		"the default one total attempt does not retry a settled failure")
	require.Equal(t, executionSagaFailedStatus,
		interpretationPickupOutcomes(t, rig.mailbox, claims)[claims[0].PickupID])
	require.Equal(t, 1, executionBuildCount(rig, "core"))
	require.Len(t, executionProtocolRefs(t, rig.mailbox, executionSagaFailedPrefix), 1)
	require.Empty(t, executionProtocolRefs(t, rig.mailbox, interpretationCompletedBranchPrefix))
	require.Empty(t, rig.repo.TagList())
}

func TestExecutionInterpretationRefusesInvalidTotalAttemptsBeforeEffects(t *testing.T) {
	for _, attempts := range []int{
		interpretationInvalidZeroAttempts,
		interpretationInvalidNegativeAttempts,
		interpretationInvalidAboveCeilingAttempts,
	} {
		t.Run(fmt.Sprint(attempts),
			func(t *testing.T) {
				rig := newExecutionRig(t, func(cfg *models.File) {
					cfg.Execution.MaxAttempts = models.Int(attempts)
				})
				result := rig.repo.CommandEnv(rig.env(), "release", "--distribution-mode", "interpretation")
				require.NotEqual(t, 0, result.Code)
				require.Contains(t, diagnosticText(result), "execution.maxAttempts")
				require.Equal(t, 0, executionBuildCount(rig, "core"))
				require.Empty(t, rig.repo.TagList())
				require.Empty(t, executionProtocolRefs(t, rig.mailbox, "dispat-task-"))
			})
	}
}
