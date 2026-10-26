// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// The former foreign-predecessor fixture used a v2 worker that pushed Ready
// and Cancel directly to a task branch. Compiled v3 gives only the first node
// Git write authority. The real v3 foreign-Cancel variants are exercised by
// TestExecutionWorkerRefusesForeignWithdrawalAfterResultLeaseLoss and
// TestChoreographyWorkerRefusesForeignCancelOnClaimedRef.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

func TestExecutionRetiredForeignPredecessorWriterCannotClaim(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 3, Task: 3, Cancel: 3}
	})
	worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, nil)
	worker.isClaimOnly = true
	worker.serve()
	t.Cleanup(worker.close)

	res := rig.release()
	require.NotEqual(t, 0, res.Code, "a v2 peer writer cannot serve a v3 run\nstdout:\n%s", res.Stdout)
	assert.Empty(t, rig.runs(), "no build command started")
	assert.Empty(t, executionReleaseTags(rig), "no package version was published")
	select {
	case <-worker.answered:
		t.Fatal("the retired peer writer claimed a v3 task")
	default:
	}
}
