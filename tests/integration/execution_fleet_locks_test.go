// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: which exclusion a composed run may give back.
//
// A single history has one lock and no name for itself, so a run that
// authorized a publication it cannot account for keeps the only lock it holds.
// A fleet holds one lock per participating repository, and the rule is
// narrower than it looks: the exclusion that has to survive is the one
// covering the effect nobody can account for, and retaining anything else
// would be taking a repository out of service for a fact about a different
// one.
//
// So the claim is a pair. The repository whose publication went unanswered
// stays locked for an operator, with the order of recovery named; the peer
// that published normally in the same run gets its lock back and its release
// recorded.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// The two peers of this fixture: the one the release is started in, which
// publishes here, and the one whose publication is delegated to a node.
const (
	executionFleetEntry    = "api"
	executionFleetDelegate = "sdk"
)

// TestExecutionFleetRetainsOnlyTheUnansweredRepositoryLock: two linked peers,
// one delegated publication, and a node that can no longer write to its own
// branch once it has been authorized.
//
// The run cannot establish what became of that publication, so it reports the
// unknown class and leaves that repository's lock where it is. The other
// peer's package published here in the same run: its lock goes back and its
// version is recorded, because nothing about it is in doubt.
func TestExecutionFleetRetainsOnlyTheUnansweredRepositoryLock(t *testing.T) {
	wait, cancel := 6, 4
	if harness.IsTinyGo() {
		wait, cancel = 30, 20
	}
	mailbox := executionMailbox(t)
	builds := t.TempDir() + "/builds.log"
	fleet := newChoreographyFleet(t, executionFleetEntry, executionFleetDelegate)
	fleet.writeConfig(executionFleetDelegate, func(cfg *models.File) {
		// Only this peer's publications travel, so the entry's own publication
		// is never in doubt and the two halves of the claim stay apart.
		cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyWorker)
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
	})
	fleet.peer(executionFleetDelegate).Commit("chore: publish this repository from a node")
	fleet.push(executionFleetDelegate)
	fleet.writeConfig(executionFleetEntry, func(cfg *models.File) {
		cfg.Execution = &models.ExecutionConfig{
			SecretEnv: executionSecretEnv,
			Workers: []models.ExecutionWorkerConfig{
				{Name: executionNode, Endpoint: "file://" + mailbox}},
			Timeouts: &models.ExecutionTimeoutsConfig{Preflight: 30, Task: wait, Cancel: cancel},
		}
	})
	fleet.peer(executionFleetEntry).Commit("chore: delegate the publications of this fleet")
	fleet.push(executionFleetEntry)
	fleet.link(executionFleetEntry, executionFleetDelegate)

	// The node's third push onto its publish branch is its result: the first
	// two are its claim and its ready. Everything from there on fails, so the
	// acknowledgement of the withdrawal never arrives either.
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*-publish-*", Nth: 3, Onward: true})
	worker := startWorker(t, fleet.peer(executionFleetEntry).Repo,
		executionWorkerConfig(mailbox), 0,
		append([]string{executionBuildLogEnv + "=" + builds}, fault.Env()...)...)

	res := fleet.peer(executionFleetEntry).CommandEnv(append(append(fileProtocolEnv(),
		harness.LockEnabled...), executionSecretEnv+"="+executionSecret,
		executionBuildLogEnv+"="+builds, "--log-level", "debug"), "--package", "*")

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Positive(t, fault.Matches(), "the fault reached the invocation it names")
	unknown, isUnknown := executionLine(res, executionUnknownPublicationMessage)
	require.True(t, isUnknown, "the run reported the class\nstdout:\n%s", res.Stdout)
	assert.NotEqual(t, true, unknown["quiesced"], "nothing answered, so nothing has provably stopped")
	retained, isRetained := executionLine(res, executionRetainedLockMessage)
	require.True(t, isRetained, "and said it was leaving an exclusion behind\nstdout:\n%s", res.Stdout)
	assert.Equal(t, lockTag, retained.Str("tag"))
	assert.Contains(t, diagnosticText(res), executionFleetDelegate,
		"the remedy names the repository the lock belongs to")

	assert.True(t, remoteHoldsLock(t, fleet.peer(executionFleetDelegate).remote),
		"the repository whose publication went unanswered stays locked for an operator")
	assert.False(t, remoteHoldsLock(t, fleet.peer(executionFleetEntry).remote),
		"and the peer that published normally gets its lock back")
	assert.Equal(t, []string{executionFleetEntry + "-pkg@0.1.0"},
		fleet.peer(executionFleetEntry).TagList(),
		"the peer whose publication nothing is in doubt about is recorded")
	assert.NotContains(t, tagsIn(fleet.peer(executionFleetEntry).Repo, ".links/"+executionFleetDelegate),
		executionFleetDelegate+"-pkg@0.1.0",
		"and the one that may or may not have published is not")
	stopAll(t, []*executionWorker{worker})
}

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
