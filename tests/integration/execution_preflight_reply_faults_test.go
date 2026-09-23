// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a mailbox writer cannot turn a malformed or out-of-sequence
// preflight answer into a usable node or a cleanup lease.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

func TestExecutionPreflightRejectsMalformedAndOutOfSequenceReplies(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		kind         string
		isMalformed  bool
		isWrongKind  bool
		isWrongOrder bool
		reason       string
	}{
		{name: "malformed signed claim", kind: "claim", isMalformed: true, reason: "unreadable"},
		{name: "claim for another kind of work", kind: "claim", isWrongKind: true, reason: "replay"},
		{name: "malformed signed report", kind: "result", isMalformed: true, reason: "unreadable"},
		{name: "report after a publish authorization", kind: "result", isWrongOrder: true, reason: "chain"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 5}
			})
			writer := newExecutionFakeOrchestrator(t, rig.mailbox)
			started := rig.repo.StartReleaseEnv(rig.env(), "release")
			branch := executionAwaitBranch(t, rig, "probe")
			assignment := executionMessage(t, rig.mailbox, branch, "assignment")
			offered := executionTipOID(t, rig.mailbox, branch)
			parent := offered
			if scenario.kind == "result" {
				claim := writer.commit(executionMessageOptions{secret: executionSecret, kind: "claim", isSigned: true},
					executionDocument(t, executionClaim(assignment, offered)), parent)
				bareGit(t, rig.mailbox, "update-ref", "refs/heads/"+branch, claim)
				parent = claim
			}
			if scenario.isWrongOrder {
				// A probe never authorizes work. Even a correctly signed report
				// cannot answer a Go that somebody inserted after its Claim.
				goCommit := writer.commit(executionMessageOptions{secret: executionSecret, kind: "go", isSigned: true},
					executionDocument(t, executionClaim(assignment, offered)), parent)
				bareGit(t, rig.mailbox, "update-ref", "refs/heads/"+branch, goCommit)
				parent = goCommit
			}
			message := executionClaim(assignment, offered)
			if scenario.kind == "result" {
				message = executionReply(assignment, offered)
			}
			if scenario.isWrongKind {
				message["kind"] = "build"
			}
			document := executionDocument(t, message)
			if scenario.isMalformed {
				document = []byte("{")
			}
			tip := writer.commit(executionMessageOptions{secret: executionSecret, kind: scenario.kind, isSigned: true},
				document, parent)
			bareGit(t, rig.mailbox, "update-ref", "refs/heads/"+branch, tip)

			res := started.Wait()
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
			rejected, isRejected := executionLine(res, "result rejected")
			require.True(t, isRejected, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Equal(t, scenario.reason, rejected.Str("reason"))
			assert.Equal(t, []string{"refs/heads/" + branch}, rig.branches(),
				"a rejected message cannot become cleanup ownership\nstdout:\n%s", res.Stdout)
			assert.Equal(t, tip, strings.TrimSpace(bareGit(t, rig.mailbox, "rev-parse", "refs/heads/"+branch)))
			assert.Equal(t, 0, buildRuns(rig.repo))
			assert.Empty(t, rig.repo.TagList())
		})
	}
}

func TestExecutionPreflightRefusesAuthenticUnusableNodeReports(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(map[string]any)
		why    string
	}{
		{name: "unsupported node protocol", change: func(report map[string]any) {
			report["protocol"] = executionProtocolVersion + 1
		}, why: "protocol"},
		{name: "node has no capacity", change: func(report map[string]any) {
			report["capacity"] = 0
		}, why: "capacity"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 5}
			})
			writer := newExecutionFakeOrchestrator(t, rig.mailbox)
			started := rig.repo.StartReleaseEnv(rig.env(), "release")
			branch := executionAwaitBranch(t, rig, "probe")
			assignment := executionMessage(t, rig.mailbox, branch, "assignment")
			offered := executionTipOID(t, rig.mailbox, branch)
			claim := writer.commit(executionMessageOptions{secret: executionSecret, kind: "claim", isSigned: true},
				executionDocument(t, executionClaim(assignment, offered)), offered)
			bareGit(t, rig.mailbox, "update-ref", "refs/heads/"+branch, claim)
			reply := executionReply(assignment, offered)
			report, ok := reply["report"].(map[string]any)
			require.True(t, ok)
			scenario.change(report)
			result := writer.commit(executionMessageOptions{secret: executionSecret, kind: "result", isSigned: true},
				executionDocument(t, reply), claim)
			bareGit(t, rig.mailbox, "update-ref", "refs/heads/"+branch, result)

			res := started.Wait()
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
			assert.Contains(t, diagnosticText(res), scenario.why,
				"the authentic answer still cannot describe a usable node")
			assert.Equal(t, 0, buildRuns(rig.repo))
			assert.Empty(t, rig.repo.TagList())
			assert.False(t, remoteHoldsLock(t, rig.origin))
			assert.Empty(t, rig.branches(), "authenticated reply can be cleaned after refusal")
		})
	}
}
