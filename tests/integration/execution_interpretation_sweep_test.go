// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
)

const interpretedSweepScript = `sh ../../args.sh "$DISPAT_PACKAGE"`
const interpretedSweepHelper = `printf '%s|%s|%s|%s\n' "${DISPAT_EXECUTION_NODE:-orchestrator}" "$1" "$2" "$3" >> "$DISPAT_IT_EXECUTION_LOG"`
const interpretedSweepFirstArgument = "--watch"
const interpretedSweepSecondArgument = "two words"
const interpretedSweepExpectedRecord = "build-a|core|--watch|two words\n"

func TestExecutionInterpretationSweepsPinnedArgumentsOnWorker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the durable distributed node-slot backend is unavailable on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Scripts["tests"] = models.Script{interpretedSweepScript}
	}, executionSweepPinnedToWorkers)
	rig.repo.WriteFile("args.sh", interpretedSweepHelper+"\n")
	rig.repo.Commit("feat(core): add sweep argument recorder")
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	result := rig.repo.StartCommandEnv(rig.env(), "run", "tests", "--since", "all",
		"--distribution-mode", "interpretation", "--return-endpoint", rig.returnURL,
		"--root", rig.repo.Root,
		"--", interpretedSweepFirstArgument, interpretedSweepSecondArgument).
		WaitForDiagnostic(interpretationReleaseWait, func() {
			t.Logf("signed interpretation sweep ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
		})
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	data, err := os.ReadFile(rig.builds)
	require.NoError(t, err)
	require.Equal(t, interpretedSweepExpectedRecord, string(data),
		"the private interpreted script retains the two exact forwarded arguments on the worker")
	claims := interpretationWorkerClaims(t, rig.mailbox)
	require.Len(t, claims, 1)
	require.Equal(t, "core:run", claims[0].Task)
	require.Equal(t, executionSagaSuccessfulStatus,
		interpretationPickupOutcomes(t, rig.mailbox, claims)[claims[0].PickupID])
	require.Len(t, executionSagaSignedTerminalTips(t, rig.mailbox), 1)
	require.Empty(t, rig.repo.TagList(), "a sweep creates no release version")
	require.False(t, remoteHoldsLock(t, rig.origin), "a completed sweep releases its remote lock")
}
