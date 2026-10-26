// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
)

const executionSagaBuildOutputRoot = "dist"
const executionSagaOutputPath = "outputs/dist/marker.txt"
const executionSagaOutputContents = "saga-output"
const executionSagaDecisionDocumentPath = "dispat/choreography-decision.json"
const executionSagaDecisionSignaturePath = "dispat/choreography-decision.sig"
const executionSagaDecisionSignatureKind = "choreography-decision"
const executionSagaTerminalEventKind = "terminal"

type executionSagaDecision struct {
	Succeeded bool
	ResultOID string
}

func executionSagaSignedTerminalTips(t *testing.T, mailbox string) []string {
	t.Helper()
	refs := executionProtocolRefs(t, mailbox, "dispat-task-")
	require.NotEmpty(t, refs, "the completed task branches remain in coordination Git")
	tips := make(map[string]bool, len(refs))
	for _, ref := range refs {
		tips[strings.TrimSpace(bareGit(t, mailbox, "rev-parse", "refs/heads/"+ref))] = true
	}
	var completed []string
	for _, event := range executionLateJoinLedgerEvents(t, mailbox) {
		if event.Kind != executionSagaTerminalEventKind || !tips[event.TaskOID] {
			continue
		}
		paths := bareGit(t, mailbox, "ls-tree", "-r", "--name-only", event.TaskOID)
		if !strings.Contains(paths, executionSagaDecisionDocumentPath) {
			continue // branchless local-authority finality is ledger-only
		}
		document := bareGit(t, mailbox, "show", event.TaskOID+":"+executionSagaDecisionDocumentPath)
		signature := strings.TrimSpace(bareGit(t, mailbox, "show",
			event.TaskOID+":"+executionSagaDecisionSignaturePath))
		require.Equal(t, executionSign(executionSagaDecisionSignatureKind,
			executionSecret, []byte(document)), signature)
		var decision executionSagaDecision
		require.NoError(t, json.Unmarshal([]byte(document), &decision))
		if decision.Succeeded {
			require.NotEmpty(t, decision.ResultOID,
				"a finished task names the accepted Result or Ack receipt")
			completed = append(completed, event.TaskOID)
		}
	}
	require.NotEmpty(t, completed, "the signed ledger identifies a retained finished branch")
	return completed
}

func TestExecutionCompletedOutputLivesOnAssignedTaskBranch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.BuildOutputs = []string{executionSagaBuildOutputRoot}
		cfg.Scripts["build"] = models.Script{`mkdir -p dist && printf saga-output > dist/marker.txt`}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	res := rig.release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	found := false
	for _, tip := range executionSagaSignedTerminalTips(t, rig.mailbox) {
		paths := bareGit(t, rig.mailbox, "ls-tree", "-r", "--name-only", tip)
		if !strings.Contains(paths, executionSagaOutputPath) {
			continue
		}
		found = true
		require.Equal(t, executionSagaOutputContents,
			bareGit(t, rig.mailbox, "show", tip+":"+executionSagaOutputPath))
	}
	require.True(t, found, "a completed build retains its declared bytes on the assigned task branch")
}

func TestExecutionCompletedNoOutputTaskKeepsSignedBranch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionRig(t)
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	defer worker.stop(t)
	res := rig.release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for _, tip := range executionSagaSignedTerminalTips(t, rig.mailbox) {
		paths := bareGit(t, rig.mailbox, "ls-tree", "-r", "--name-only", tip)
		require.NotContains(t, paths, "outputs/",
			"a no-output task still has a retained signed finished branch")
	}
}
