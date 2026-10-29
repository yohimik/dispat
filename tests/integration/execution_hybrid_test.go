// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
)

const hybridDistributionModeName = "hybrid"
const hybridGraphDocumentPath = "dispat/choreography-graph-root.json"
const hybridPreparationProbe = `printf '%s %s %s\n' probe-prepare "$DISPAT_PACKAGE" "${DISPAT_EXECUTION_NODE:-orchestrator}" >> "$DISPAT_IT_EXECUTION_LOG"`

func TestExecutionHybridCompilesBuildAndInterpretsFirstOnlyEffects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the durable distributed node-slot backend is unavailable on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.Scripts["prepare"] = models.Script{hybridPreparationProbe}
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
		space := cfg.Spaces["libs"]
		space.Flow.Sign = []string{"prepare"}
		cfg.Spaces["libs"] = space
	}, pinPackages(map[string]*models.RunOnly{
		"core": placedOn(models.RunOnlyChoreographer, models.RunOnlyOrchestrator),
	}))
	compiled := rig.repo.CommandEnv(rig.env(), "status", "--distribution-mode", "compilation")
	hybrid := rig.repo.CommandEnv(rig.env(), "status", "--distribution-mode", hybridDistributionModeName)
	require.Equal(t, 0, hybrid.Code, "%s", hybrid.Stderr)
	require.Equal(t, planDigestOf(t, compiled), planDigestOf(t, hybrid))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	result := rig.repo.StartReleaseEnv(rig.env(), "release", "--distribution-mode", hybridDistributionModeName,
		"--return-endpoint", rig.returnURL).WaitForDiagnostic(interpretationReleaseWait, func() {
		t.Logf("hybrid ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
	})
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	require.Equal(t, executionNode, rig.nodesByPackage()["core"])
	require.Equal(t, "orchestrator", executionProbeValues(rig, "prepare")["core"], "records=%+v\nstdout=%s", rig.runs(), result.Stdout)
	require.Equal(t, "orchestrator", executionProbeValues(rig, "publish")["core"])
	require.Equal(t, []string{"core@0.1.0"}, rig.repo.TagList())
	graphs := executionProtocolRefs(t, rig.mailbox, "dispat-graph-")
	require.Len(t, graphs, 1)
	var carrier struct {
		Tasks []struct {
			Task, Kind, HybridSegment, OperationPolicyDigest string
			Virtual, ExecutorOwned                           bool
			MaxAttempts                                      int
		}
		Templates []struct {
			Task  string
			Frame json.RawMessage
		}
	}
	require.NoError(t, json.Unmarshal([]byte(bareGit(t, rig.mailbox, "show",
		"refs/heads/"+graphs[0]+":"+hybridGraphDocumentPath)), &carrier))
	frames := make(map[string]json.RawMessage)
	for _, template := range carrier.Templates {
		frames[template.Task] = template.Frame
	}
	for _, task := range carrier.Tasks {
		if task.Virtual {
			continue
		}
		require.Len(t, task.OperationPolicyDigest, 64)
		require.Equal(t, 1, task.MaxAttempts)
		if task.ExecutorOwned {
			require.Equal(t, "interpreted", task.HybridSegment)
			require.Empty(t, frames[task.Task], "effect command policy is retained only in the pinned plan")
			continue
		}
		require.Equal(t, "compiled", task.HybridSegment)
		require.NotEmpty(t, frames[task.Task])
	}
	claims := interpretationWorkerClaims(t, rig.mailbox)
	require.Len(t, claims, 1)
	var claimedKind string
	for _, task := range carrier.Tasks {
		if task.Task == claims[0].Task {
			claimedKind = task.Kind
		}
	}
	require.Equal(t, "build", claimedKind)
}

func TestExecutionHybridSweepUsesCompiledArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the durable distributed node-slot backend is unavailable on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Scripts["tests"] = models.Script{interpretedSweepScript}
	}, executionSweepPinnedToWorkers)
	rig.repo.WriteFile("args.sh", interpretedSweepHelper+"\n")
	rig.repo.Commit("fix(core): add sweep argument recorder")
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	result := rig.repo.StartCommandEnv(rig.env(), "run", "tests", "--since", "all",
		"--distribution-mode", hybridDistributionModeName, "--return-endpoint", rig.returnURL,
		"--root", rig.repo.Root, "--", interpretedSweepFirstArgument, interpretedSweepSecondArgument).
		WaitForDiagnostic(interpretationReleaseWait, func() {
			t.Logf("hybrid sweep ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
		})
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	records, err := os.ReadFile(rig.builds)
	require.NoError(t, err)
	require.Equal(t, interpretedSweepExpectedRecord, string(records))
	require.Empty(t, rig.repo.TagList())
	require.Len(t, executionProtocolRefs(t, rig.mailbox, executionSagaOutputPrefix), 1)
}

func TestExecutionCompiledModesEnforceSignedAttemptLimits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the durable distributed node-slot backend is unavailable on Windows")
	}
	for _, mode := range []string{"compilation", hybridDistributionModeName} {
		for _, attempts := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%d", mode, attempts), func(t *testing.T) {
				rig := newExecutionRig(t, func(cfg *models.File) {
					if attempts > 1 {
						cfg.Execution.MaxAttempts = models.Int(attempts)
					}
					cfg.Scripts["build"] = models.Script{executionRecordingScript + ` && test "$(wc -l < "$DISPAT_IT_EXECUTION_LOG")" -gt 1`}
				}, pinPackages(map[string]*models.RunOnly{
					"core": placedOn(models.RunOnlyChoreographer, models.RunOnlyOrchestrator),
				}))
				worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
				defer worker.stop(t)
				result := rig.repo.StartReleaseEnv(rig.env(), "release", "--distribution-mode", mode,
					"--return-endpoint", rig.returnURL).WaitForDiagnostic(interpretationReleaseWait, func() {
					t.Logf("attempt ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
				})
				claims := interpretationWorkerClaims(t, rig.mailbox)
				require.Len(t, claims, attempts, "%s", result.Stderr)
				require.Equal(t, attempts, executionBuildCount(rig, "core"))
				if attempts == 1 {
					require.NotEqual(t, 0, result.Code)
					require.Empty(t, rig.repo.TagList())
					return
				}
				require.Equal(t, 0, result.Code, "%s", result.Stderr)
				require.NotEqual(t, claims[0].PickupID, claims[1].PickupID)
				require.False(t, strings.Contains(claims[1].Task, "publish"))
				require.Len(t, executionProtocolRefs(t, rig.mailbox, executionSagaPendingPrefix), attempts)
				require.Len(t, executionProtocolRefs(t, rig.mailbox, executionSagaFailedPrefix), 1)
				require.Len(t, executionProtocolRefs(t, rig.mailbox, executionSagaOutputPrefix), 1)
			})
		}
	}
}
