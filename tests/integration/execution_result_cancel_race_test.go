// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a real worker must acknowledge a first-node Cancel that
// wins while its already-finished build Result is being prepared for return.

import (
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestExecutionWorkerAcknowledgesWithdrawalThatBeatsItsResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the durable distributed backend and Git fault wrapper are unavailable on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
			Preflight: 10, Task: 30, Cancel: 5,
		}
	})
	pushReject, resultGate := harness.NewGitFaultChain(t,
		harness.GitFault{Pattern: "*push*", Code: 128},
		harness.GitFault{Pattern: "*commit-tree*dispat transport result*", Nth: 1, Hold: true})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60, pushReject.Env()...)
	defer func() {
		resultGate.Resume()
		served := worker.stop(t)
		t.Logf("worker stdout:\n%s\nstderr:\n%s", served.Stdout, served.Stderr)
	}()
	release := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	held := false
	for until := time.After(30 * time.Second); !held; {
		held = resultGate.IsHeld()
		if held {
			break
		}
		select {
		case <-until:
			resultGate.Resume()
			result := release.Wait()
			t.Fatalf("the Result preparation was not held; matches=%d stdout:\n%s\nstderr:\n%s",
				resultGate.Matches(), result.Stdout, result.Stderr)
		case <-time.After(25 * time.Millisecond):
		}
	}
	branch := executionAwaitBranch(t, rig, "build")
	chain := executionChain(t, rig.mailbox, branch)
	require.Contains(t, chain, "choreography-claim")
	require.NotContains(t, chain, "choreography-result", "the held Result must not have landed")
	release.Signal(syscall.SIGINT)
	executionAwaitMessage(t, rig.mailbox, branch, "cancel")
	resultGate.Resume()
	result := release.Wait()
	require.NotEqual(t, 0, result.Code, "the interrupted release cannot publish: %s", result.Stdout)
	requireChoreographyTaskChain(t, rig.mailbox, rig.branches(),
		"choreography-claim", "cancel", "ack", "choreography-decision")
	require.Equal(t, 1, len(rig.runs()), "the build ran once and was not reoffered")
	require.Empty(t, executionReleaseTags(rig))
	require.False(t, remoteHoldsLock(t, rig.origin))
	require.Zero(t, pushReject.Matches(), "a peer attempted an engine Git push")
}
