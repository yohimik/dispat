// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a foreign Cancel on the canonical task ref must not make a real
// v3 worker acknowledge that party while its finished Result waits.

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
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
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 4, Cancel: 5}
	})
	pushReject, resultGate := harness.NewGitFaultChain(t,
		harness.GitFault{Pattern: "*push*refs/heads/dispat-task-*", Code: 128},
		harness.GitFault{Pattern: "*commit-tree*dispat transport result*", Nth: 1, Hold: true})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60, pushReject.Env()...)
	defer resultGate.Resume()
	release := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	for until := time.After(25 * time.Second); !resultGate.IsHeld(); {
		select {
		case <-until:
			resultGate.Resume()
			res := release.Wait()
			t.Fatalf("the finished Result was not held; matches=%d stdout:\n%s\nstderr:\n%s",
				resultGate.Matches(), res.Stdout, res.Stderr)
		case <-time.After(25 * time.Millisecond):
		}
	}
	branch := executionAwaitBranch(t, rig, "build")
	chain := executionChain(t, rig.mailbox, branch)
	require.Contains(t, chain, "choreography-claim")
	require.NotContains(t, chain, "choreography-result")
	assignment := executionTaskAssignment(t, rig.mailbox, branch)
	require.NotNil(t, assignment)
	claim := executionMessage(t, rig.mailbox, branch, "choreography-claim")
	offered, ok := claim["expectedOid"].(string)
	require.True(t, ok)
	claimed := executionTipOID(t, rig.mailbox, branch)
	writer := newExecutionFakeOrchestrator(t, rig.mailbox)
	foreignDocument := executionOrderedJSON{}.with(
		executionField{"protocol", 3}, executionField{"kind", assignment["kind"]},
		executionField{"run", assignment["run"]}, executionField{"planDigest", assignment["planDigest"]},
		executionField{"task", assignment["task"]}, executionField{"attempt", assignment["attempt"]},
		executionField{"generation", assignment["generation"]},
		executionField{"node", assignment["node"]}, executionField{"branch", assignment["branch"]},
		executionField{"issuedAt", time.Now().UTC().Format(time.RFC3339)},
		executionField{"assignment", offered}, executionField{"tip", claimed},
	).set("run", strings.Repeat("a", 32))
	if scenario.isWrongProtocol {
		foreignDocument = foreignDocument.set("run", assignment["run"]).set("protocol", 4)
	}
	foreignBytes, err := json.Marshal(foreignDocument)
	require.NoError(t, err)
	if scenario.isMalformed {
		foreignBytes = []byte("{")
	}
	foreign := writer.commit(executionMessageOptions{secret: executionSecret, kind: "cancel", isSigned: true},
		foreignBytes, claimed)
	bareGit(t, rig.mailbox, "update-ref", "refs/heads/"+branch, foreign)
	time.Sleep(time.Second)
	resultGate.Resume()
	res := release.Wait()
	served := worker.stop(t)

	require.NotEqual(t, 0, res.Code, "a foreign withdrawal cannot complete the release\nstdout:\n%s", res.Stdout)
	assert.Equal(t, foreign, executionTipOID(t, rig.mailbox, branch),
		"the worker and release leave the foreign tip for investigation")
	assert.Contains(t, rig.branches(), "refs/heads/"+branch)
	assert.NotContains(t, served.Stdout, `"message":"cancellation acknowledged"`,
		"the worker did not acknowledge somebody else's cancellation")
	assert.Contains(t, executionRejections(served), scenario.reason,
		"the worker names why the signed withdrawal cannot settle this attempt")
	assert.NotContains(t, executionChain(t, rig.mailbox, branch), "ack")
	assert.Empty(t, executionReleaseTags(rig))
	assert.Len(t, rig.runs(), 1, "the build finished once before its report lost the lease")
	assert.Zero(t, pushReject.Matches(), "a peer attempted a canonical task Git push")
}
