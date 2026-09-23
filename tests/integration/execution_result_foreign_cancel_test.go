// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: losing a finished Result's Git lease to a foreign withdrawal must
// not make a real worker acknowledge that party's cancellation.

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

type foreignWithdrawalCase struct {
	name            string
	isMalformed     bool
	isWrongProtocol bool
	reason          string
}

func TestExecutionWorkerRefusesForeignWithdrawalAfterResultLeaseLoss(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git push rendezvous uses a POSIX shell")
	}
	for _, scenario := range []foreignWithdrawalCase{
		{name: "another run", reason: "replay"},
		{name: "malformed signed cancellation", isMalformed: true, reason: "unreadable"},
		{name: "signed unsupported protocol", isWrongProtocol: true, reason: "protocol"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runWorkerForeignWithdrawalAfterResultLeaseLoss(t, scenario)
		})
	}
}

func runWorkerForeignWithdrawalAfterResultLeaseLoss(t *testing.T, scenario foreignWithdrawalCase) {
	t.Helper()
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 45, Cancel: 5}
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
		"the worker did not reach its Result push; trace: %s", readExecutionFile(barrier.trace))
	branch := executionAwaitBranch(t, rig, "build")
	require.Equal(t, []string{"assignment", "claim"}, executionChain(t, rig.mailbox, branch))
	assignment := executionMessage(t, rig.mailbox, branch+"^", "assignment")
	claim := executionMessage(t, rig.mailbox, branch, "claim")
	offered, ok := claim["assignment"].(string)
	require.True(t, ok)
	claimed := executionTipOID(t, rig.mailbox, branch)
	writer := newExecutionFakeOrchestrator(t, rig.mailbox)
	foreignDocument := executionOrderedJSON{}.with(executionReplyHeader(assignment, executionNode)...).with(
		executionField{"assignment", offered}, executionField{"tip", claimed},
	).set("run", strings.Repeat("a", 32))
	if scenario.isWrongProtocol {
		foreignDocument = foreignDocument.set("run", assignment["run"]).set("protocol", executionProtocolVersion+1)
	}
	foreignBytes, err := json.Marshal(foreignDocument)
	require.NoError(t, err)
	if scenario.isMalformed {
		foreignBytes = []byte("{")
	}
	foreign := writer.commit(executionMessageOptions{secret: executionSecret, kind: "cancel", isSigned: true},
		foreignBytes, claimed)
	bareGit(t, rig.mailbox, "update-ref", "refs/heads/"+branch, foreign)
	require.NoError(t, os.WriteFile(barrier.proceed, nil, 0o600))
	release.Signal(syscall.SIGINT)
	res := release.Wait()
	isReleaseDone = true
	served := worker.stop(t)
	isWorkerDone = true

	require.NotEqual(t, 0, res.Code, "a foreign withdrawal cannot complete the release\nstdout:\n%s", res.Stdout)
	assert.Equal(t, foreign, executionTipOID(t, rig.mailbox, branch),
		"the worker and release leave the foreign tip for investigation")
	assert.Equal(t, []string{"refs/heads/" + branch}, rig.branches())
	assert.NotContains(t, served.Stdout, `"message":"cancellation acknowledged"`,
		"the worker did not acknowledge somebody else's cancellation")
	assert.Contains(t, served.Stdout, `"message":"the task result could not be reported"`,
		"the worker must observe the rejected Result lease before this check is meaningful")
	assert.Contains(t, executionRejections(served), scenario.reason,
		"the worker names why the signed withdrawal cannot settle this attempt")
	assert.NotContains(t, executionChain(t, rig.mailbox, branch), "ack")
	assert.Empty(t, executionReleaseTags(rig))
	assert.Len(t, rig.runs(), 1, "the build finished once before its report lost the lease")
}
