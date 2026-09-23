// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a real worker must acknowledge a withdrawal that wins the Git
// lease while its already-finished build result is about to be reported.

import (
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestExecutionWorkerAcknowledgesWithdrawalThatBeatsItsResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git push rendezvous uses a POSIX shell")
	}
	cancelWait := 5
	if harness.IsTinyGo() {
		cancelWait = 20
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
			Preflight: 30, Task: 45, Cancel: cancelWait,
		}
	})
	barrier := newTerminalLeaseRendezvous(t)
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 20, barrier.env...)
	isWorkerDone := false
	release := rig.repo.StartReleaseEnv(rig.env(), "release")
	isReleaseDone := false
	t.Cleanup(func() {
		_ = os.WriteFile(barrier.proceed, nil, 0o600)
		if !isReleaseDone {
			release.Signal(syscall.SIGINT)
			_ = release.Wait()
		}
		if !isWorkerDone {
			worker.proc.Signal(syscall.SIGINT)
			_ = worker.proc.Wait()
		}
	})

	require.True(t, waitForExecutionFile(barrier.entered, 30*time.Second),
		"the worker did not reach its second build-branch push; trace: %s", readExecutionFile(barrier.trace))
	branch := executionAwaitBranch(t, rig, "build")
	assert.Equal(t, []string{"assignment", "claim"}, executionChain(t, rig.mailbox, branch),
		"the worker finished the command but its Result has not reached Git")
	release.Signal(syscall.SIGINT)
	executionAwaitMessage(t, rig.mailbox, branch, "cancel")
	cancelOID := executionTipOID(t, rig.mailbox, branch)
	require.NotEmpty(t, cancelOID)
	require.NoError(t, os.WriteFile(barrier.proceed, nil, 0o600))

	result := release.Wait()
	isReleaseDone = true
	served := worker.stop(t)
	isWorkerDone = true

	require.NotEqual(t, 0, result.Code, "the interrupted release cannot publish\nstdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	ack, isAcknowledged := executionLine(result, "the withdrawn attempt was acknowledged")
	require.True(t, isAcknowledged, "the real worker settled the result/cancel lease race\nstdout:\n%s\nworker:\n%s", result.Stdout, served.Stdout)
	assert.Equal(t, true, ack["commandStarted"])
	assert.Equal(t, "after", ack["phase"])
	assert.Contains(t, served.Stdout, `"message":"cancellation acknowledged"`)
	assert.Empty(t, rig.branches(), "the acknowledged attempt leaves no coordination branch")
	assert.False(t, remoteHoldsLock(t, rig.origin), "the interrupted run releases its remote lock")
	assert.Empty(t, executionReleaseTags(rig))
	assert.Len(t, rig.runs(), 1, "the build ran once; a lost Result was not rerun")
	assert.Len(t, strings.Fields(readExecutionFile(barrier.trace)), 3,
		"the worker pushed Claim, lost the Result lease, then pushed Ack")
}

func readExecutionFile(path string) string {
	contents, _ := os.ReadFile(path)
	return string(contents)
}
