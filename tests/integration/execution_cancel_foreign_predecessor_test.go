// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a cancelled release must never adopt a different party's
// nonterminal reply or withdrawal as its own retry/cleanup lease.

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

func TestExecutionWithdrawalRefusesForeignPredecessors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git push rendezvous uses a POSIX shell")
	}
	for _, scenario := range []struct {
		name string
		kind string
	}{
		{name: "ready signed by another party", kind: "ready"},
		{name: "withdrawal for another run", kind: "cancel"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.LogLevel = "debug"
				cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
				cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 15, Cancel: 5}
			})
			worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, nil)
			worker.isClaimOnly = true
			worker.serve()
			t.Cleanup(worker.close)
			barrier := newTerminalLeaseRendezvous(t)
			release := rig.repo.StartReleaseEnv(append(rig.env(), barrier.env...), "release")
			isReleaseDone := false
			t.Cleanup(func() {
				_ = os.WriteFile(barrier.proceed, nil, 0o600)
				if !isReleaseDone {
					release.Signal(syscall.SIGINT)
					_ = release.Wait()
				}
			})
			select {
			case <-worker.answered:
			case <-time.After(12 * time.Second):
				t.Fatal("the worker did not claim the assignment")
			}
			branch := executionAwaitBranch(t, rig, "build")
			release.Signal(syscall.SIGINT)
			require.True(t, waitForExecutionFile(barrier.entered, 30*time.Second),
				"the release did not reach the withdrawal lease")

			assignment := executionMessage(t, rig.mailbox, branch+"^", "assignment")
			claim := executionMessage(t, rig.mailbox, branch, "claim")
			offered, ok := claim["assignment"].(string)
			require.True(t, ok)
			claimed := executionTipOID(t, rig.mailbox, branch)
			document := executionOrderedJSON{}.with(executionReplyHeader(assignment, executionNode)...).with(
				executionField{"assignment", offered})
			secret := executionSecret
			if scenario.kind == "ready" {
				document = document.with(executionField{"claim", claimed})
				secret = "another-party-secret"
			} else {
				document = document.with(executionField{"tip", claimed}).set("run", strings.Repeat("a", 32))
			}
			foreign := worker.pushSigned(branch, claimed, scenario.kind, document, "", true, secret)
			require.NoError(t, os.WriteFile(barrier.proceed, nil, 0o600))
			res := release.Wait()
			isReleaseDone = true

			require.NotEqual(t, 0, res.Code, "a foreign lease cannot settle this run\nstdout:\n%s", res.Stdout)
			assert.Equal(t, foreign, executionTipOID(t, rig.mailbox, branch),
				"the foreign tip remains intact for investigation")
			assert.Equal(t, []string{"refs/heads/" + branch}, rig.branches())
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionRetainedCode),
				"the refused cleanup is reported\nstdout:\n%s", res.Stdout)
			assert.Empty(t, rig.repo.TagList())
			assert.Empty(t, rig.runs(), "no build command ran")
			assert.False(t, remoteHoldsLock(t, rig.origin), "an unstarted build retains no release lock")
			trace, err := os.ReadFile(barrier.trace)
			require.NoError(t, err)
			pushes := strings.Fields(string(trace))
			require.Len(t, pushes, 3,
				"the assignment, refused withdrawal and cleanup each use an owned lease")
			assert.True(t, strings.HasSuffix(pushes[0], ":"), "the assignment creates the branch")
			assert.True(t, strings.HasSuffix(pushes[1], ":"+offered) ||
				strings.HasSuffix(pushes[1], ":"+claimed),
				"the interrupted release withdraws from an owned assignment or claim: %v", pushes)
			assert.Equal(t, pushes[1], pushes[2],
				"cleanup keeps the same owned lease after the foreign move")
			assert.NotContains(t, string(trace), ":"+foreign,
				"the release never retries atop a foreign tip")
		})
	}
}

// A genuine Ready can overtake a withdrawal that was still leased against
// Claim. The coordinator must authenticate Ready, retry Cancel on top of it,
// and accept only an acknowledgement of that exact Cancel.
func TestExecutionWithdrawalRetriesAfterReadyMovesTheLease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git push rendezvous uses a POSIX shell")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyWorker)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 15, Cancel: 5}
	})
	worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, nil)
	worker.isClaimOnly = true
	worker.serve()
	t.Cleanup(worker.close)
	barrier := newTerminalLeaseRendezvousFor(t, "publish")
	release := rig.repo.StartReleaseEnv(append(rig.env(), barrier.env...), "release")
	isReleaseDone := false
	t.Cleanup(func() {
		_ = os.WriteFile(barrier.proceed, nil, 0o600)
		if !isReleaseDone {
			release.Signal(syscall.SIGINT)
			_ = release.Wait()
		}
	})
	select {
	case <-worker.answered:
	case <-time.After(12 * time.Second):
		t.Fatal("the worker did not claim the publication assignment")
	}
	branch := executionAwaitBranch(t, rig, "publish")
	release.Signal(syscall.SIGINT)
	require.True(t, waitForExecutionFile(barrier.entered, 30*time.Second),
		"the release did not reach its withdrawal lease")
	assignment := executionMessage(t, rig.mailbox, branch+"^", "assignment")
	claim := executionMessage(t, rig.mailbox, branch, "claim")
	offered, ok := claim["assignment"].(string)
	require.True(t, ok)
	claimed := executionTipOID(t, rig.mailbox, branch)
	ready := executionOrderedJSON{}.with(executionReplyHeader(assignment, executionNode)...).with(
		executionField{"assignment", offered}, executionField{"claim", claimed})
	readyOID := worker.pushSigned(branch, claimed, "ready", ready, "", true, executionSecret)
	require.NoError(t, os.WriteFile(barrier.proceed, nil, 0o600))
	executionAwaitMessage(t, rig.mailbox, branch, "cancel")
	cancelOID := executionTipOID(t, rig.mailbox, branch)
	ack := executionOrderedJSON{}.with(executionReplyHeader(assignment, executionNode)...).with(
		executionField{"assignment", offered}, executionField{"cancel", cancelOID},
		executionField{"phase", "before"}, executionField{"commandStarted", false})
	ackOID := worker.pushSigned(branch, cancelOID, "ack", ack, "", true, executionSecret)
	res := release.Wait()
	isReleaseDone = true

	require.NotEqual(t, 0, res.Code, "the interrupted release cannot publish\nstdout:\n%s", res.Stdout)
	settled, isAcknowledged := executionLine(res, "the withdrawn attempt was acknowledged")
	require.True(t, isAcknowledged, "the worker answered the retried withdrawal\nstdout:\n%s", res.Stdout)
	assert.Equal(t, false, settled["commandStarted"])
	assert.Empty(t, rig.branches())
	assert.False(t, remoteHoldsLock(t, rig.origin))
	assert.Empty(t, executionReleaseTags(rig))
	assert.Empty(t, executionProbedPackages(rig, "publish"))
	trace, err := os.ReadFile(barrier.trace)
	require.NoError(t, err)
	pushes := strings.Fields(string(trace))
	require.Len(t, pushes, 4,
		"assignment, lost withdrawal lease, retry on Ready, then cleanup of Ack")
	assert.True(t, strings.HasSuffix(pushes[0], ":"))
	assert.True(t, strings.HasSuffix(pushes[1], ":"+offered) ||
		strings.HasSuffix(pushes[1], ":"+claimed),
		"the first withdrawal leases an owned assignment or claim: %v", pushes)
	assert.True(t, strings.HasSuffix(pushes[2], ":"+readyOID))
	assert.True(t, strings.HasSuffix(pushes[3], ":"+ackOID))
}
