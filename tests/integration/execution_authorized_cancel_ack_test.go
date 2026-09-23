// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: after a publication was authorized, only the acknowledgement of
// this exact withdrawal can establish that its command never started.

import (
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

type authorizedAckCase struct {
	name             string
	isWrongCancel    bool
	isWrongKind      bool
	isWrongSignature bool
	isMalformed      bool
	isWrongProtocol  bool
}

func TestExecutionAuthorizedWithdrawalNeedsItsOwnAcknowledgement(t *testing.T) {
	for _, scenario := range []authorizedAckCase{
		{name: "stopped before command"},
		{name: "acknowledges an earlier tip", isWrongCancel: true},
		{name: "acknowledges another kind of work", isWrongKind: true},
		{name: "signed with another key", isWrongSignature: true},
		{name: "signed unreadable acknowledgement", isMalformed: true},
		{name: "signed unsupported protocol", isWrongProtocol: true},
	} {
		t.Run(scenario.name, func(t *testing.T) { runAuthorizedWithdrawalAck(t, scenario) })
	}
}

func runAuthorizedWithdrawalAck(t *testing.T, scenario authorizedAckCase) {
	t.Helper()
	cancelWait := 5
	if harness.IsTinyGo() {
		cancelWait = 20
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyWorker)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
			Preflight: 30, Task: 45, Cancel: cancelWait,
		}
	})
	worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, nil)
	worker.isReadyOnly = true
	worker.serve()
	t.Cleanup(worker.close)
	release := rig.repo.StartReleaseEnv(rig.env(), "release")
	isFinished := false
	t.Cleanup(func() {
		if !isFinished {
			release.Signal(syscall.SIGINT)
			_ = release.Wait()
		}
	})

	branch := executionAwaitBranch(t, rig, "publish")
	goMessage := executionAwaitMessage(t, rig.mailbox, branch, "go")
	release.Signal(syscall.SIGINT)
	executionAwaitMessage(t, rig.mailbox, branch, "cancel")
	cancelOID := executionTipOID(t, rig.mailbox, branch)
	assert.Equal(t, []string{"assignment", "claim", "ready", "go", "cancel"},
		executionChain(t, rig.mailbox, branch), "authorization was withdrawn before the fake node answered")

	acknowledged := cancelOID
	if scenario.isWrongCancel {
		acknowledged = strings.TrimSpace(bareGit(t, rig.mailbox, "rev-parse", "refs/heads/"+branch+"^"))
	}
	assignment, ok := goMessage["assignment"].(string)
	require.True(t, ok)
	ack := executionOrderedJSON{}.with(executionReplyHeader(goMessage, executionNode)...).with(
		executionField{"assignment", assignment},
		executionField{"cancel", acknowledged},
		executionField{"phase", "authorization-wait"},
		executionField{"commandStarted", false},
	)
	if scenario.isWrongKind {
		ack = ack.set("kind", "build")
	}
	if scenario.isWrongProtocol {
		ack = ack.set("protocol", executionProtocolVersion+1)
	}
	secret := executionSecret
	if scenario.isWrongSignature {
		secret = "another-node-secret"
	}
	if scenario.isMalformed {
		writer := newExecutionFakeOrchestrator(t, rig.mailbox)
		malformed := writer.commit(executionMessageOptions{secret: executionSecret, kind: "ack", isSigned: true},
			[]byte("{"), cancelOID)
		bareGit(t, rig.mailbox, "update-ref", "refs/heads/"+branch, malformed)
	} else {
		worker.pushSigned(branch, cancelOID, "ack", ack, "", true, secret)
	}
	res := release.Wait()
	isFinished = true

	require.NotEqual(t, 0, res.Code, "the interrupted release cannot publish\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Empty(t, executionReleaseTags(rig))
	assert.Empty(t, executionProbedPackages(rig, "publish"))
	if !scenario.isWrongCancel && !scenario.isWrongKind && !scenario.isWrongSignature &&
		!scenario.isMalformed && !scenario.isWrongProtocol {
		settled, isFound := executionLine(res, "the withdrawn attempt was acknowledged")
		require.True(t, isFound, "the node stopped before its command\nstdout:\n%s", res.Stdout)
		assert.Equal(t, false, settled["commandStarted"])
		_, isKnown := executionLine(res, "the authorized publication was withdrawn before its command started")
		assert.True(t, isKnown, "the release can classify this outcome as known\nstdout:\n%s", res.Stdout)
		assert.False(t, remoteHoldsLock(t, rig.origin), "an acknowledged publisher no longer needs the exclusion")
		assert.Empty(t, rig.branches(), "the authenticated acknowledgement is a cleanup lease\nstdout:\n%s", res.Stdout)
		return
	}
	rejected, isFound := executionLine(res, "stale or foreign receipt ignored")
	require.True(t, isFound, "a different acknowledgement cannot prove quiescence\nstdout:\n%s", res.Stdout)
	wantReason := "replay"
	if scenario.isWrongSignature {
		wantReason = "signature"
	}
	if scenario.isMalformed {
		wantReason = "unreadable"
	}
	if scenario.isWrongProtocol {
		wantReason = "protocol"
	}
	assert.Equal(t, wantReason, rejected.Str("reason"))
	_, isUnknown := executionLine(res, executionUnknownPublicationMessage)
	assert.True(t, isUnknown, "without an authentic acknowledgement the outcome remains unknown\nstdout:\n%s", res.Stdout)
	assert.True(t, remoteHoldsLock(t, rig.origin), "the unanswered authorization keeps its lock")
	assert.Equal(t, []string{"refs/heads/" + branch}, rig.branches(),
		"the foreign acknowledgement remains for investigation\nstdout:\n%s", res.Stdout)
	assert.Equal(t, []string{"assignment", "claim", "ready", "go", "cancel", "ack"},
		executionChain(t, rig.mailbox, branch))
}
