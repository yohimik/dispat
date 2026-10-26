// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"fmt"
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

const (
	workerGroupTestTaskSeconds    = 2
	workerGroupTestCancelSeconds  = 1
	workerGroupTestAdmission      = 5
	workerGroupTestReleaseWait    = 35 * time.Second
	workerGroupTestWorkerWait     = 15 * time.Second
	workerGroupTestPoll           = 50 * time.Millisecond
	workerGroupTestKillWait       = 5 * time.Second
	workerGroupTestAfterCancel    = 2 * time.Second
	workerGroupTestBackgroundHold = 30
	workerGroupTestPackage        = "alpha"
	workerGroupTestStartedName    = "strict-runner-started"
	workerGroupTestCancelKind     = "cancel-issued"
)

func TestExecutionUnsettledWorkerProcessGroupRetainsLeaseAndSendsNoResultOrAck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the worker process-group proof and durable slots require Unix")
	}
	marker := filepath.Join(t.TempDir(), workerGroupTestStartedName)
	rig := newExecutionPlacementRig(t, []string{workerGroupTestPackage},
		func(*harness.Repo) string {
			return fmt.Sprintf(`printf started > %q; sh -c 'trap "" TERM; sleep %d' &`,
				marker, workerGroupTestBackgroundHold)
		},
		func(cfg *models.File) {
			cfg.Execution.RetryAfterUncertainAttempt = false
			cfg.LogLevel = "debug"
			cfg.Execution.Timeouts.Task = workerGroupTestTaskSeconds
			cfg.Execution.Timeouts.Cancel = workerGroupTestCancelSeconds
			cfg.Execution.Timeouts.Admission = workerGroupTestAdmission
		},
		pinPackages(map[string]*models.RunOnly{
			workerGroupTestPackage: placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
		}))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	workerStopped := false
	defer func() {
		if !workerStopped {
			worker.proc.Signal(os.Interrupt)
			worker.proc.WaitFor(workerGroupTestWorkerWait)
		}
	}()
	firstNode := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	firstStopped := false
	defer func() {
		if !firstStopped {
			firstNode.KillForDiagnostic(workerGroupTestKillWait)
		}
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}, workerGroupTestReleaseWait, workerGroupTestPoll)
	require.FileExists(t, marker, "the background child was launched by an accepted task")
	require.Eventually(t, func() bool {
		for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
			if event.Kind == workerGroupTestCancelKind &&
				strings.HasPrefix(event.Task, workerGroupTestPackage+":") {
				return true
			}
		}
		return false
	}, workerGroupTestReleaseWait, workerGroupTestPoll,
		"a timed-out accepted pickup receives a signed cancellation")
	time.Sleep(workerGroupTestAfterCancel)
	result := firstNode.KillForDiagnostic(workerGroupTestKillWait)
	firstStopped = true
	require.NotEqual(t, 0, result.Code, "unsettled command effects cannot publish")
	worker.proc.Signal(os.Interrupt)
	workerResult := worker.proc.WaitFor(workerGroupTestWorkerWait)
	workerStopped = true
	require.NotEqual(t, 0, workerResult.Code, "the worker retains an unresolved physical lease")
	require.Contains(t, workerResult.Stdout+workerResult.Stderr, "script process group remains unsettled")
	require.Len(t, executionProtocolRefs(t, rig.mailbox, executionSagaPendingPrefix), 1)
	require.Empty(t, executionProtocolRefs(t, rig.mailbox, executionSagaOutputPrefix))
	require.Empty(t, executionProtocolRefs(t, rig.mailbox, executionSagaFailedPrefix))
	for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
		if !strings.HasPrefix(event.Task, workerGroupTestPackage+":") {
			continue
		}
		require.NotEqual(t, pickupFailoverResultKind, event.Kind,
			"an unproven process group cannot produce a signed Result receipt")
		require.NotEqual(t, pickupFailoverAckKind, event.Kind,
			"an unproven process group cannot produce a signed Ack receipt")
		require.NotEqual(t, pickupFailoverSettledKind, event.Kind,
			"the physical lease cannot be released without exact finality")
	}
	require.Empty(t, executionReleaseTags(rig), "an unresolved task cannot publish a release tag")
}
