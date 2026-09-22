// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: what a run does with a task the pool has no room for.
//
// One sentence of §28.2 decides all of it: a timeout alone cannot free
// capacity for a possibly overlapping attempt. An assignment nobody claimed is
// the one case where the run can do better than wait, because nothing was
// executed anywhere and holds nothing: revoking its coordination ref is a
// fence rather than a hope, so the slot is free and the task can simply be
// offered again. What is bounded is how often, because a pool with no capacity
// has to end in a refusal that says so rather than in a run that waits for
// ever.

import (
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestExecutionQueuedAssignmentIsReplacedAndThenRefused: the whole of the
// re-placement rule, driven by a node that is busy for longer than the second
// run is prepared to wait.
//
// The second run's assignment is never claimed, so each wait ends by revoking
// the ref rather than by abandoning the attempt: the node is not marked
// unhealthy, no slot is held, and the task is placed again. After the third
// attempt the run says what was actually wrong, which is that the pool had no
// capacity for it rather than anything about the package, and the run that was
// occupying the node still releases normally.
func TestExecutionQueuedAssignmentIsReplacedAndThenRefused(t *testing.T) {
	build, wait := executionExhaustedTimings()
	mailbox := executionMailbox(t)
	busy := newExecutionQueuedRepository(t, mailbox, "busy", build, 600)
	queued := newExecutionQueuedRepository(t, mailbox, "queued", build, wait)
	worker := startWorker(t, busy.repo, executionWorkerConfig(mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 0,
		executionBuildLogEnv+"="+busy.builds)

	occupying := busy.repo.StartReleaseEnv(busy.env(), "release")
	// The node is building the first run's package, so the second run's
	// assignment has nowhere to go for as long as that lasts.
	executionAwaitProbe(t, busy, executionNode)
	refused := queued.release()
	occupied := occupying.Wait()

	require.Equal(t, 1, refused.Code, "stdout:\n%s\nstderr:\n%s", refused.Stdout, refused.Stderr)
	replaced := executionReplacedAttempts(refused)
	assert.Len(t, replaced, executionMaximumPlacementAttempts-1,
		"the task was offered again after each revocation and no more\nstdout:\n%s", refused.Stdout)
	for index, event := range replaced {
		assert.Equal(t, float64(index+1), event["attempt"], "the attempts are numbered from one")
		assert.Equal(t, "queued:build", event.Str("task"))
	}
	_, isUnhealthy := executionLine(refused, "worker marked unhealthy")
	assert.False(t, isUnhealthy,
		"an assignment nobody claimed says nothing about the machine it was offered to")
	assert.True(t, harness.IsCodePresent(executionEvents(refused), executionIntegrityCode),
		"the task fails with the integrity code\nstdout:\n%s", refused.Stdout)
	assert.Empty(t, queued.repo.TagList(), "and nothing of it is recorded")
	assert.Empty(t, queued.nodesByPackage(), "no build of it ran anywhere: %v", queued.runs())

	require.Equal(t, 0, occupied.Code, "the run that was using the node still released\nstdout:\n%s",
		occupied.Stdout)
	assert.Equal(t, []string{"busy@0.1.0"}, busy.repo.TagList())
	worker.proc.Signal(syscall.SIGINT)
	require.Equal(t, 0, worker.proc.Wait().Code, "and the node is still serving afterwards")
}

// executionMaximumPlacementAttempts is how often one task is offered again
// after an assignment nobody claimed was revoked. It is spelled out rather
// than imported, because this suite knows the binary from outside.
const executionMaximumPlacementAttempts = 3

// executionExhaustedTimings are a build long enough to occupy the only node
// for the whole of the second run, and a wait short enough that the second run
// exhausts its attempts inside it.
func executionExhaustedTimings() (build time.Duration, wait int) {
	if harness.IsTinyGo() {
		return 120 * time.Second, 8
	}
	return 40 * time.Second, 3
}

// executionReplacedAttempts are the assignments a run revoked unclaimed and
// offered again, in order.
func executionReplacedAttempts(res harness.RunResult) []harness.Event {
	var replaced []harness.Event
	for _, event := range executionEvents(res) {
		if event.Str("message") == "the queued assignment was revoked and the task is placed again" {
			replaced = append(replaced, event)
		}
	}
	return replaced
}
