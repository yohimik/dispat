// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// The former push barrier held a v2 withdrawal update so a peer could push a
// terminal Result or Ack onto the same branch. V3 peers return proposals to
// the first node and write only their own pending and outcome refs. The live
// v3 Result/Cancel race
// is TestExecutionWorkerAcknowledgesWithdrawalThatBeatsItsResult; foreign
// Result and Ack binding is also checked by the signed broker and worker
// tests in services/dispat/internal/execution.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

func TestExecutionRetiredTerminalPushWriterCannotClaim(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 3, Task: 3, Cancel: 3}
	})
	worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, nil)
	worker.isClaimOnly = true
	worker.serve()
	t.Cleanup(worker.close)

	res := rig.release()
	require.NotEqual(t, 0, res.Code, "a v2 terminal writer cannot serve a v3 run\nstdout:\n%s", res.Stdout)
	assert.Empty(t, rig.runs(), "no build command started")
	assert.Empty(t, executionReleaseTags(rig), "no package version was published")
	select {
	case <-worker.answered:
		t.Fatal("the retired terminal writer claimed a v3 task")
	default:
	}
}
