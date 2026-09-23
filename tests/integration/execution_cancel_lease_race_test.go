// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: cancellation's Git lease may lose to a terminal worker message;
// only an authentic message of that same attempt becomes cleanup evidence.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

type terminalRaceCase struct {
	name           string
	kind           string
	wrongTask      bool
	wrongSignature bool
	wrongCancel    bool
	isOwn          bool
}

func TestExecutionTerminalMessageRacesWithdrawalLease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git push rendezvous uses a POSIX shell")
	}
	for _, tc := range []terminalRaceCase{
		{name: "signed result", kind: "result", isOwn: true},
		{name: "signed acknowledgement", kind: "ack", isOwn: true},
		{name: "result of another task", kind: "result", wrongTask: true},
		{name: "result with another signature", kind: "result", wrongSignature: true},
		{name: "acknowledgement of another withdrawal", kind: "ack", wrongCancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) { runTerminalLeaseRace(t, tc) })
	}
}

func runTerminalLeaseRace(t *testing.T, tc terminalRaceCase) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 15, Cancel: 5}
	})
	worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, nil)
	worker.isClaimOnly = true
	worker.serve()
	t.Cleanup(worker.close)

	rendezvous := newTerminalLeaseRendezvous(t)
	release := rig.repo.StartReleaseEnv(append(rig.env(), rendezvous.env...), "release")
	select {
	case <-worker.answered:
	case <-time.After(12 * time.Second):
		_ = os.WriteFile(rendezvous.proceed, nil, 0o600)
		result := release.Wait()
		commands, _ := os.ReadFile(rendezvous.trace)
		t.Fatalf("worker never claimed the build assignment; matching pushes:\n%s\nstdout:\n%s\nstderr:\n%s",
			commands, result.Stdout, result.Stderr)
	}
	release.Signal(syscall.SIGINT)
	if !waitForExecutionFile(rendezvous.entered, 30*time.Second) {
		_ = os.WriteFile(rendezvous.proceed, nil, 0o600)
		result := release.Wait()
		t.Fatalf("the withdrawal push was not reached; stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	}
	reply := writeTerminalRaceReply(t, terminalReplySpec{rig: rig, worker: worker, scenario: tc})
	matchingPushes, err := os.ReadFile(rendezvous.trace)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(matchingPushes), ":"+reply.offered) ||
		strings.Contains(string(matchingPushes), ":"+reply.claimed),
		"the paused withdrawal used a preterminal expected-old value: %s", matchingPushes)
	require.NoError(t, os.WriteFile(rendezvous.proceed, nil, 0o600))

	result := release.Wait()

	require.NotEqual(t, 0, result.Code, "the interrupted release cannot publish\nstdout:\n%s", result.Stdout)
	assert.Empty(t, rig.repo.TagList())
	if tc.isOwn {
		assert.Empty(t, rig.branches(), "the signed terminal object became Close's lease\nstdout:\n%s", result.Stdout)
		assert.False(t, harness.IsCodePresent(executionEvents(result), executionRetainedCode),
			"the exact terminal lease needs no retained-branch warning\nstdout:\n%s", result.Stdout)
		return
	}
	assert.Equal(t, []string{"refs/heads/" + reply.branch}, rig.branches(),
		"an unbound terminal object cannot become this run's cleanup lease")
	assert.True(t, harness.IsCodePresent(executionEvents(result), executionRetainedCode),
		"the retained branch is reported for investigation\nstdout:\n%s", result.Stdout)
}

type terminalLeaseRendezvous struct {
	env     []string
	entered string
	proceed string
	trace   string
}

// Only the release gets this Git wrapper. Its first build-branch push is the
// assignment; its second is the withdrawal after SIGINT. Holding that second
// push before Git sees it gives the test an exact place to write the competing
// terminal message, independent of scheduler timing.
func newTerminalLeaseRendezvous(t *testing.T) terminalLeaseRendezvous {
	return newTerminalLeaseRendezvousFor(t, "build")
}

func newTerminalLeaseRendezvousFor(t *testing.T, kind string) terminalLeaseRendezvous {
	t.Helper()
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	wrapDir := t.TempDir()
	barrier := terminalLeaseRendezvous{
		entered: filepath.Join(wrapDir, "withdrawal-entered"),
		proceed: filepath.Join(wrapDir, "let-withdrawal-go"),
		trace:   filepath.Join(wrapDir, "matching-pushes"),
	}
	t.Cleanup(func() { _ = os.WriteFile(barrier.proceed, nil, 0o600) })
	require.NoError(t, os.WriteFile(filepath.Join(wrapDir, "git"), []byte(`#!/bin/sh
for argument do
  case "$argument" in
    --force-with-lease=refs/heads/dispat-worker-build-a-*-"$DISPAT_IT_CANCEL_KIND"-*:*)
      printf '%s\n' "$argument" >> "$DISPAT_IT_CANCEL_TRACE"
      if mkdir "$DISPAT_IT_CANCEL_FIRST" 2>/dev/null; then
        :
      else
        : > "$DISPAT_IT_CANCEL_ENTERED"
        attempts=0
        while [ ! -f "$DISPAT_IT_CANCEL_PROCEED" ] && [ "$attempts" -lt 600 ]; do
          sleep 0.05
          attempts=$((attempts + 1))
        done
        [ -f "$DISPAT_IT_CANCEL_PROCEED" ] || exit 99
      fi
      break ;;
  esac
done
exec "$DISPAT_IT_REAL_GIT" "$@"
`), 0o755))
	barrier.env = []string{
		"PATH=" + wrapDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DISPAT_IT_REAL_GIT=" + realGit,
		"DISPAT_IT_CANCEL_KIND=" + kind,
		"DISPAT_IT_CANCEL_FIRST=" + filepath.Join(wrapDir, "first-build-push"),
		"DISPAT_IT_CANCEL_ENTERED=" + barrier.entered,
		"DISPAT_IT_CANCEL_PROCEED=" + barrier.proceed,
		"DISPAT_IT_CANCEL_TRACE=" + barrier.trace,
	}
	return barrier
}

type terminalReplySpec struct {
	rig      *executionRig
	worker   *executionFakeWorker
	scenario terminalRaceCase
}

type terminalReply struct {
	branch  string
	offered string
	claimed string
}

// The fake node claimed the assignment and deliberately stopped there. Write
// one terminal message on that real Git branch while the release's withdrawal
// push is paused. Invalid rows vary only the signed document or its signature.
func writeTerminalRaceReply(t *testing.T, spec terminalReplySpec) terminalReply {
	t.Helper()
	branch := ""
	for _, ref := range executionMailboxBranches(t, spec.rig.mailbox) {
		candidate := strings.TrimPrefix(ref, "refs/heads/")
		underNode := strings.TrimPrefix(candidate, "dispat-worker-"+executionNode+"-")
		if underNode != candidate && strings.Contains(underNode, "-build-") {
			branch = candidate
			break
		}
	}
	require.NotEmpty(t, branch, "the paused withdrawal belongs to a build branch")
	assignment := executionMessage(t, spec.rig.mailbox, branch+"^", "assignment")
	claim := executionMessage(t, spec.rig.mailbox, branch, "claim")
	offered, ok := claim["assignment"].(string)
	require.True(t, ok)
	claimed := strings.TrimSpace(bareGit(t, spec.rig.mailbox, "rev-parse", "refs/heads/"+branch))
	previous := claimed
	var document executionOrderedJSON
	if spec.scenario.kind == "ack" {
		cancel := spec.worker.pushSigned(branch, claimed, "cancel",
			executionOrderedJSON{}.with(executionReplyHeader(assignment, executionNode)...).with(
				executionField{"assignment", offered}, executionField{"tip", claimed}),
			"", true, executionSecret)
		previous = cancel
		if spec.scenario.wrongCancel {
			cancel = offered
		}
		document = executionOrderedJSON{}.with(executionReplyHeader(assignment, executionNode)...).with(
			executionField{"assignment", offered}, executionField{"cancel", cancel},
			executionField{"phase", "before"})
	} else {
		document = executionOrderedJSON{}.with(executionReplyHeader(assignment, executionNode)...).with(
			executionField{"assignment", offered}, executionField{"status", "succeeded"},
			executionField{"platform", executionPlatform()})
		if spec.scenario.wrongTask {
			document = document.set("task", "another:build")
		}
	}
	secret := executionSecret
	if spec.scenario.wrongSignature {
		secret = "another-secret"
	}
	spec.worker.pushSigned(branch, previous, spec.scenario.kind, document, "", true, secret)
	return terminalReply{branch: branch, offered: offered, claimed: claimed}
}

func waitForExecutionFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
