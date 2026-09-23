// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A publisher that reached its gate but never acknowledged a denied permit
// must not run the irreversible command or turn the refusal into an unknown
// publication. The node is unusable for further work in this run.
func TestExecutionUnacknowledgedWithdrawalBeforeAuthorization(t *testing.T) {
	wait, cancel := 12, 4
	if harness.IsTinyGo() {
		wait, cancel = 30, 20
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.RunOnly = placedOn(models.RunOnlyBoth, models.RunOnlyWorker)
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
		cfg.Scripts["prepublish"] = models.Script{executionPrePublishProbe + ` &&
cd "$DISPAT_IT_COORD_REPO" &&
printf 'changed\n' > packages/core/late-input.txt &&
git add -- packages/core/late-input.txt &&
git -c user.name=fixture -c user.email=fixture@example.com commit -q -m 'chore: change published inputs' -- packages/core/late-input.txt`}
		space := cfg.Spaces["libs"]
		space.Flow.BeforePublish = []string{"prepublish"}
		cfg.Spaces["libs"] = space
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: wait, Cancel: cancel}
	})
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*-publish-*", Nth: 3, Onward: true})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0,
		append(fault.Env(), "DISPAT_IT_COORD_REPO="+rig.repo.Root)...)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Positive(t, fault.Matches(), "the worker's acknowledgement push was blocked")
	assert.Equal(t, []string{"core"}, executionProbedPackages(rig, "prepublish"))
	assert.Empty(t, executionProbedPackages(rig, "publish"), "no permit was sent")
	assert.Empty(t, executionReleaseTags(rig))
	assert.True(t, harness.IsCodePresentForPackage(executionEvents(res), executionIntegrityCode, "core"),
		"the source change withholds the publication\nstdout:\n%s", res.Stdout)
	_, unknown := executionLine(res, executionUnknownPublicationMessage)
	assert.False(t, unknown, "a denied permit is not an unknown publication")
	_, timedOut := executionLine(res, "the withdrawal was not acknowledged within the cancel wait")
	assert.True(t, timedOut, "the coordinator waited for the worker to stop\nstdout:\n%s", res.Stdout)
	unhealthy, isUnhealthy := executionLine(res, "worker marked unhealthy")
	require.True(t, isUnhealthy, "the unanswering node cannot take further work\nstdout:\n%s", res.Stdout)
	assert.Equal(t, "cancel-unacknowledged", unhealthy.Str("reason"))
	assert.Empty(t, rig.branches(), "this run closes its own coordination branches")
	assert.False(t, remoteHoldsLock(t, rig.origin), "a denied permit cannot leave an unknown-effect lock")
	stopAll(t, []*executionWorker{worker})
}
