// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: interruption while a publisher is preparing must withdraw its
// assignment and report cancellation without ever issuing a publish permit.

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

func TestExecutionInterruptedPublisherBeforeAuthorization(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyWorker)
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
		cfg.Scripts["prepublish"] = models.Script{executionPrePublishProbe + " && sleep 600"}
		space := cfg.Spaces["libs"]
		space.Flow.BeforePublish = []string{"prepublish"}
		cfg.Spaces["libs"] = space
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 45, Cancel: 15}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	t.Cleanup(func() {
		if worker != nil {
			worker.stop(t)
		}
	})
	started := rig.repo.StartReleaseEnv(rig.env(), "release")
	isFinished := false
	t.Cleanup(func() {
		if !isFinished {
			started.Signal(os.Interrupt)
			_ = started.Wait()
		}
	})

	executionAwaitProbe(t, rig, "probe-prepublish")
	started.Signal(syscall.SIGINT)
	res := started.Wait()
	isFinished = true

	require.NotEqual(t, 0, res.Code, "interruption stops the release\nstdout:\n%s", res.Stdout)
	assert.Empty(t, executionAuthorizations(res), "no Go was issued while the hook prepared")
	assert.Empty(t, executionProbedPackages(rig, "publish"), "the publish command never started")
	assert.Empty(t, executionReleaseTags(rig))
	_, isUnknown := executionLine(res, executionUnknownPublicationMessage)
	assert.False(t, isUnknown, "no authorized effect has an unknown outcome")
	ack, isAcknowledged := executionLine(res, "the withdrawn attempt was acknowledged")
	require.True(t, isAcknowledged, "the node confirms it stopped\nstdout:\n%s", res.Stdout)
	assert.Equal(t, false, ack["commandStarted"], "the publish command had not begun")
	summary, isSummarized := executionLine(res, "summary")
	require.True(t, isSummarized, "the package has a release outcome\nstdout:\n%s", res.Stdout)
	assert.Equal(t, "cancelled", summary.Str("status"))
	assert.Empty(t, rig.branches())
	assert.False(t, remoteHoldsLock(t, rig.origin))
	worker.stop(t)
	worker = nil
}
