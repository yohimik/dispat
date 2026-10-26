// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionProtocolRefs lists one coordination namespace without reading a
// moving task tip as input authority. The tests inspect ref names only.
func executionProtocolRefs(t *testing.T, mailbox, prefix string) []string {
	t.Helper()
	out := strings.TrimSpace(bareGit(t, mailbox, "for-each-ref", "--format=%(refname:short)",
		"refs/heads/"+prefix+"*"))
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func TestExecutionReservationsAndSlotsExistBeforeFirstFrame(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "continue")
	defer func() { _ = os.WriteFile(gate, nil, 0o600) }()
	rig := newExecutionPlacementRig(t, []string{"alpha", "beta", "gamma"},
		func(*harness.Repo) string {
			return executionRecordingScript + " && " + fmt.Sprintf(
				`for i in $(seq 1 600); do test -f %q && exit 0; sleep .1; done; exit 7`, gate)
		}, func(cfg *models.File) {
			cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
			cfg.Execution.Workers = append(cfg.Execution.Workers, models.ExecutionWorkerConfig{
				Name: executionSecondNode, Endpoint: cfg.Execution.Workers[0].Endpoint})
		})
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)
	started := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0o600) })

	require.Eventually(t, func() bool { return len(rig.runs()) > 0 }, 90*time.Second, 100*time.Millisecond,
		"the first package frame never started")
	taskRefs := executionProtocolRefs(t, rig.mailbox, "dispat-task-")
	assert.GreaterOrEqual(t, len(taskRefs), 3,
		"each logical delegated task has one canonical ref before any frame starts")
	for _, ref := range taskRefs {
		assert.Regexp(t, `^dispat-task-[0-9a-f]{32}-[0-9a-f]{32}$`, ref,
			"a retry must reuse the logical task ref")
	}
	assert.Len(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"), 1,
		"one retained signed event feed exists before the first frame")
	slotRefs := executionProtocolRefs(t, rig.mailbox, "dispat-slot-")
	assert.Len(t, slotRefs, 2,
		"each authorized node has one graph notice ref, independent of its physical capacity")
	for _, ref := range slotRefs {
		assert.Regexp(t, `^dispat-slot-[0-9a-f]{32}-[0-9a-f]{32}-0$`, ref)
	}
	for _, branch := range executionProtocolRefs(t, rig.mailbox, "dispat-worker-") {
		assert.NotContains(t, branch, "-snapshot-", "source objects use direct assignment parents")
		assert.NotContains(t, branch, "-relay-", "predecessor outputs use direct assignment parents")
	}

	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	res := started.Wait()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Len(t, rig.repo.TagList(), 3)
	assert.Equal(t, taskRefs, executionProtocolRefs(t, rig.mailbox, "dispat-task-"),
		"the same canonical task refs remain for sealed-run receipt recovery")
	assert.Equal(t, slotRefs, executionProtocolRefs(t, rig.mailbox, "dispat-slot-"),
		"the fixed slot notice refs remain with the signed run history")
	for name, node := range rig.nodesByPackage() {
		assert.Contains(t, []string{executionNode, executionSecondNode}, node, name)
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		assert.Equal(t, 1, executionBuildCount(rig, name), "a task is executed by one chosen worker")
	}
	stopAll(t, workers)
}

func TestExecutionEndpointAliasesAreRefusedBeforeDispatch(t *testing.T) {
	alias := filepath.Join(t.TempDir(), "same-mailbox.git")
	rig := newExecutionPlacementRig(t, []string{"alpha"}, func(*harness.Repo) string {
		return executionRecordingScript
	},
		func(cfg *models.File) {
			cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
			cfg.Execution.Workers = append(cfg.Execution.Workers, models.ExecutionWorkerConfig{
				Name: executionSecondNode, Endpoint: "file://" + alias})
		})
	require.NoError(t, os.Symlink(rig.mailbox, alias))
	first := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	second := rig.startWorker(executionWorkerConfig(alias, func(cfg *models.ExecutionConfig) {
		cfg.Name = executionSecondNode
	}), 0)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "a distributed run requires one canonical Git mailbox")
	assert.Empty(t, rig.runs(), "no frame starts under mismatched endpoint identities")
	assert.Empty(t, rig.repo.TagList())
	assert.Empty(t, executionProtocolRefs(t, rig.mailbox, "dispat-task-"))
	assert.False(t, remoteHoldsLock(t, rig.origin))
	stopAll(t, []*executionWorker{first, second})
}

func TestExecutionFirstReservationBatchRefusalStartsNoFrame(t *testing.T) {
	rig := newExecutionPlacementRig(t, []string{"alpha"}, func(*harness.Repo) string {
		return executionRecordingScript
	}, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
	})
	hook := `#!/bin/sh
set -eu
while read -r old new ref; do
 case "$ref" in
 refs/heads/dispat-task-*|refs/heads/dispat-slot-*)
  printf 'rejecting first initialization batch\n' >&2
  exit 1 ;;
 esac
done
`
	require.NoError(t, os.WriteFile(filepath.Join(rig.mailbox, "hooks", "pre-receive"), []byte(hook), 0o755))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()
	stopAll(t, []*executionWorker{worker})

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
		"pre-dispatch endpoint refusal has E225\nstdout:\n%s", res.Stdout)
	assert.Empty(t, rig.runs(), "no package frame starts before initialization settles")
	assert.Empty(t, rig.repo.TagList())
	assert.Empty(t, executionProtocolRefs(t, rig.mailbox, "dispat-task-"))
	assert.Empty(t, executionProtocolRefs(t, rig.mailbox, "dispat-slot-"))
	assert.False(t, remoteHoldsLock(t, rig.origin))
}

func TestExecutionPartialReservationBatchRefusalStartsNoFrame(t *testing.T) {
	const packagesAcrossTwoInitializationBatches = 33
	names := make([]string, packagesAcrossTwoInitializationBatches)
	for i := range names {
		names[i] = fmt.Sprintf("unit%02d", i)
	}
	rig := newExecutionPlacementRig(t, names, func(*harness.Repo) string {
		return executionRecordingScript
	}, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
	})
	counter := filepath.Join(t.TempDir(), "initialization-pushes")
	hook := `#!/bin/sh
set -eu
counter='` + strings.ReplaceAll(counter, "'", "'\\''") + `'
seen=0
while read -r old new ref; do
 case "$ref" in refs/heads/dispat-task-*|refs/heads/dispat-slot-*) seen=1;; esac
done
[ "$seen" = 1 ] || exit 0
n=0
[ ! -f "$counter" ] || n=$(cat "$counter")
n=$((n + 1))
printf '%s\n' "$n" > "$counter"
[ "$n" -ne 2 ] || { printf 'rejecting second initialization batch\n' >&2; exit 1; }
`
	// Git hooks do not inherit the fixture's command environment reliably.
	require.NoError(t, os.WriteFile(filepath.Join(rig.mailbox, "hooks", "pre-receive"), []byte(hook), 0o755))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()
	stopAll(t, []*executionWorker{worker})

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
		"pre-dispatch endpoint refusal has E225\nstdout:\n%s", res.Stdout)
	count, err := os.ReadFile(counter)
	require.NoError(t, err, "the second atomic reservation chunk was reached")
	pushes, err := strconv.Atoi(strings.TrimSpace(string(count)))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, pushes, 2)
	assert.Empty(t, rig.runs(), "no package frame starts until all initialization chunks settle")
	assert.Empty(t, rig.repo.TagList())
	assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-task-"),
		"the accepted first batch remains for exact recovery after a partial initialization")
	assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"))
	assert.False(t, remoteHoldsLock(t, rig.origin))
}
