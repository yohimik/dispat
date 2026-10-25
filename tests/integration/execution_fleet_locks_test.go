// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: fleet lock ownership is checked before local publication. A
// worker's successful build does not authorize the orchestrator to publish
// after any participating repository loses its release lock.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// The entry peer publishes locally; the other peer supplies a separate lock.
const (
	executionFleetEntry    = "api"
	executionFleetDelegate = "sdk"
)

// TestExecutionFleetPeerLockLossWithholdsLocalPublication: a local publish
// still belongs to the distributed run. Losing a different participating
// repository's lock during beforePublish withholds this package too, before
// its irreversible command or release record.
func TestExecutionFleetPeerLockLossWithholdsLocalPublication(t *testing.T) {
	mailbox := executionMailbox(t)
	marker := t.TempDir() + "/published"
	fleet := newChoreographyFleet(t, executionFleetEntry, executionFleetDelegate)
	fleet.writeConfig(executionFleetEntry, func(cfg *models.File) {
		cfg.Execution = &models.ExecutionConfig{
			SecretEnv: executionSecretEnv,
			Workers: []models.ExecutionWorkerConfig{
				{Name: executionNode, Endpoint: "file://" + mailbox}},
		}
		cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyOrchestrator)
		cfg.Scripts["prepublish"] = models.Script{
			`git --git-dir="$DISPAT_IT_FLEET_PEER_REMOTE" rev-parse refs/tags/` + lockTag +
				` >/dev/null && git --git-dir="$DISPAT_IT_FLEET_PEER_REMOTE" tag -d ` + lockTag,
		}
		cfg.Scripts["publish"] = models.Script{`printf published > "$DISPAT_IT_FLEET_PUBLISH_MARKER"`}
		cfg.Flow.BeforePublish = []string{"prepublish"}
	})
	fleet.peer(executionFleetEntry).Commit("chore: verify every fleet lock before local publication")
	fleet.push(executionFleetEntry)
	fleet.link(executionFleetEntry, executionFleetDelegate)
	worker := startWorker(t, fleet.peer(executionFleetEntry).Repo,
		executionWorkerConfig(mailbox), 0)

	res := fleet.peer(executionFleetEntry).CommandEnv(append(append(fileProtocolEnv(),
		harness.LockEnabled...), executionSecretEnv+"="+executionSecret,
		"DISPAT_IT_FLEET_PEER_REMOTE="+fleet.peer(executionFleetDelegate).remote,
		"DISPAT_IT_FLEET_PUBLISH_MARKER="+marker),
		"release", "--package", executionFleetEntry+"-pkg")

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionLockCode),
		"the local publication is refused under the distributed lock code")
	assert.NoFileExists(t, marker, "the publish command did not begin")
	assert.Empty(t, fleet.peer(executionFleetEntry).TagList(), "no local version was recorded")
	assert.False(t, remoteHoldsLock(t, fleet.peer(executionFleetDelegate).remote),
		"the lost peer lock was not recreated")
	stopAll(t, []*executionWorker{worker})
}
