// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a serving node, driven through the compiled binary.
//
// What a worker is, is what it does with a mailbox: it answers the work
// addressed to it, refuses everything it cannot authenticate without saying
// what it refused, and stops when it is told to. Every message here is
// written by hand, because the interesting ones are exactly the ones no
// dispat would produce.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestExecutionWorkerAnswersProbe: the whole exchange one probe produces, as
// the mailbox records it, and the node's own description of itself.
func TestExecutionWorkerAnswersProbe(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	branch := executionBranchName("answered")
	orchestrator.offer(branch, orchestrator.probe(branch, "preflight"))

	worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(3) }), 4)
	executionAwaitMessage(t, rig.mailbox, branch, "result")
	res := worker.proc.Wait()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"assignment", "claim", "result"}, executionChain(t, rig.mailbox, branch),
		"one attempt is one chain, advanced by one party per step")

	result := executionMessage(t, rig.mailbox, branch, "result")
	assert.Equal(t, "succeeded", result["status"])
	assert.Equal(t, branch, result["branch"], "a reply binds itself to the branch it was found on")
	assert.Equal(t, executionNode, result["node"])
	assert.Equal(t, float64(executionProtocolVersion), result["protocol"])
	report, isReported := result["report"].(map[string]any)
	require.True(t, isReported, "a probe is answered with what the node is: %v", result)
	assert.Equal(t, runtime.GOOS, report["os"], "the platform is the binary's own")
	assert.Equal(t, runtime.GOARCH, report["arch"])
	assert.Equal(t, float64(3), report["capacity"], "and the capacity is the node's configuration")
	assert.NotEmpty(t, report["gitVersion"])

	started, isStarted := executionLine(res, "worker started")
	require.True(t, isStarted, "stdout:\n%s", res.Stdout)
	assert.Equal(t, executionNode, started.Str("node"))
	stopped, isStopped := executionLine(res, "worker stopped")
	require.True(t, isStopped)
	assert.Equal(t, "idle", stopped.Str("reason"),
		"a node started for one release ends by itself")
}

// An accepted claim is durable before its task starts. If the replay record
// cannot be replaced, the worker must leave the claimed attempt unanswered;
// after the state path is repaired it must still serve fresh work.
func TestExecutionWorkerRefusesWorkWhenItsReplayRecordCannotBeSaved(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 8)

	control := executionBranchName("state-write-control")
	orchestrator.offer(control, orchestrator.probe(control, "control"))
	executionAwaitMessage(t, rig.mailbox, control, "result")
	seen := filepath.Join(worker.stateDir, executionNode, "seen.json")
	require.FileExists(t, seen, "the first answer established the durable replay record")
	require.NoError(t, os.Remove(seen))
	require.NoError(t, os.Mkdir(seen, 0o755))

	blocked := executionBranchName("state-write-blocked")
	orchestrator.offer(blocked, orchestrator.probe(blocked, "blocked"))
	executionAwaitMessage(t, rig.mailbox, blocked, "claim")
	require.Never(t, func() bool {
		return len(executionChain(t, rig.mailbox, blocked)) > 2
	}, 2*time.Second, 50*time.Millisecond,
		"work cannot start when the node cannot remember its claim")
	require.NoError(t, os.Remove(seen))

	fresh := executionBranchName("state-write-recovered")
	orchestrator.offer(fresh, orchestrator.probe(fresh, "recovered"))
	executionAwaitMessage(t, rig.mailbox, fresh, "result")
	reply := worker.proc.Wait()
	require.Equal(t, 0, reply.Code, "stdout:\n%s\nstderr:\n%s", reply.Stdout, reply.Stderr)
	assert.Equal(t, []string{"assignment", "claim"}, executionChain(t, rig.mailbox, blocked))
	assert.Equal(t, []string{"assignment", "claim", "result"}, executionChain(t, rig.mailbox, fresh))
	assert.Contains(t, reply.Stdout, "the mailbox could not be served")
}

// A correctly signed assignment can still describe no executable frame.
// The worker claims it, reports failure, and never reaches a hook or publish
// command; a malformed assignment must not stop it serving unrelated work.
func TestExecutionWorkerFailsIncompletePublicationFrames(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	state := orchestrator.prepareInputState("incomplete-frames")
	for label, change := range map[string]func(map[string]any){
		"missing-package":     func(message map[string]any) { delete(message, "package") },
		"missing-frame":       func(message map[string]any) { delete(message, "frame") },
		"empty-input-closure": func(message map[string]any) { message["repositories"] = []any{} },
	} {
		branch := executionCraftedBranchName("publish", label)
		orchestrator.offer(branch, orchestrator.publication(branch, label, state, change))
	}
	noPermit := executionCraftedBranchName("publish", "no-permit")
	orchestrator.offer(noPermit, orchestrator.publication(noPermit, "no-permit", state,
		func(message map[string]any) { message["permits"] = map[string]any{} }))
	control := executionCraftedBranchName("probe", "incomplete-control")
	orchestrator.offer(control, orchestrator.probe(control, "incomplete-control"))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(4) }), 4)

	for _, label := range []string{"missing-package", "missing-frame", "empty-input-closure"} {
		branch := executionCraftedBranchName("publish", label)
		result := executionAwaitMessage(t, rig.mailbox, branch, "result")
		assert.Equal(t, "failed", result["status"], label)
		assert.Equal(t, []string{"assignment", "claim", "result"},
			executionChain(t, rig.mailbox, branch), label)
	}
	executionAwaitMessage(t, rig.mailbox, control, "result")
	reply := worker.proc.Wait()
	require.Equal(t, 0, reply.Code, "stdout:\n%s\nstderr:\n%s", reply.Stdout, reply.Stderr)
	assert.Empty(t, rig.runs(), "neither hooks nor publication commands may run")
	assert.Contains(t, reply.Stdout, "the assignment describes no frame to run")
	assert.Contains(t, executionRejections(reply), "permit")
	assert.Equal(t, []string{"assignment"}, executionChain(t, rig.mailbox, noPermit),
		"a signed assignment without a publish permit is refused before claim")
	assert.Equal(t, []string{"assignment", "claim", "result"},
		executionChain(t, rig.mailbox, control), "the worker kept serving")
}

// TestExecutionWorkerRejectsAssignments: every acceptance rule, through the
// binary. None of these is claimed, each is refused with a reason the log can
// be filtered on and nothing of what it said, and a valid probe offered
// beside them is still answered: a mailbox full of rubbish does not stop a
// node from serving.
func TestExecutionWorkerRejectsAssignments(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	forged := "forged-" + strings.Repeat("x", 16)

	refused := map[string]struct {
		document func(string) map[string]any
		options  []func(*executionMessageOptions)
		reason   string
	}{
		"a signature from another secret": {
			options: []func(*executionMessageOptions){
				func(o *executionMessageOptions) { o.secret = forged }},
			reason: "signature"},
		"a document signed as another kind of message": {
			options: []func(*executionMessageOptions){
				func(o *executionMessageOptions) { o.signedAs = "result" }},
			reason: "signature"},
		"a tip nobody signed at all": {
			options: []func(*executionMessageOptions){
				func(o *executionMessageOptions) { o.isSigned = false }},
			reason: "unreadable"},
		"work addressed to another node": {
			document: func(branch string) map[string]any {
				return orchestrator.probe(branch, "preflight",
					func(m map[string]any) { m["node"] = "build-b" })
			},
			reason: "node"},
		"a message bound to another branch": {
			document: func(branch string) map[string]any {
				return orchestrator.probe(branch, "preflight",
					func(m map[string]any) { m["branch"] = executionBranchName("elsewhere") })
			},
			reason: "branch"},
		"a protocol this node does not speak": {
			document: func(branch string) map[string]any {
				return orchestrator.probe(branch, "preflight",
					func(m map[string]any) { m["protocol"] = executionProtocolVersion + 1 })
			},
			reason: "protocol"},
		"a message issued outside the replay window": {
			document: func(branch string) map[string]any {
				return orchestrator.probe(branch, "preflight",
					func(m map[string]any) { m["issuedAt"] = "2026-09-01T00:00:00Z" })
			},
			reason: "issued-at"},
		"a document larger than this node's ceiling": {
			document: func(branch string) map[string]any {
				return orchestrator.probe(branch, "preflight",
					func(m map[string]any) { m["generation"] = strings.Repeat("g", 4096) })
			},
			reason: "oversize"},
	}
	for name, tc := range refused {
		branch := executionBranchName(executionLabel(name))
		document := orchestrator.probe(branch, "preflight")
		if tc.document != nil {
			document = tc.document(branch)
		}
		orchestrator.offer(branch, document, tc.options...)
	}
	// A chain no legal sequence of pushes could have produced: an assignment
	// written directly onto another assignment.
	illegal := executionBranchName("illegalchain")
	first := orchestrator.offer(illegal, orchestrator.probe(illegal, "preflight"))
	orchestrator.offer(illegal, orchestrator.probe(illegal, "preflight"),
		func(o *executionMessageOptions) { o.parents = []string{first} })
	// And one valid probe, which has to be answered all the same.
	valid := executionBranchName("valid")
	orchestrator.offer(valid, orchestrator.probe(valid, "preflight"))

	worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) {
			settings.Transfer = &models.ExecutionTransferConfig{MaxManifestBytes: 2048}
		}), 4)
	executionAwaitMessage(t, rig.mailbox, valid, "result")
	res := worker.proc.Wait()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	reasons := executionRejections(res)
	for name, tc := range refused {
		t.Run(name, func(t *testing.T) {
			branch := executionBranchName(executionLabel(name))
			assert.Equal(t, []string{"assignment"}, executionChain(t, rig.mailbox, branch),
				"nothing was claimed and nothing was answered")
			assert.Contains(t, reasons, tc.reason)
		})
	}
	t.Run("an assignment on an illegal chain", func(t *testing.T) {
		assert.Equal(t, []string{"assignment", "assignment"}, executionChain(t, rig.mailbox, illegal))
		assert.Contains(t, reasons, "chain")
	})
	assert.NotContains(t, res.Stdout, forged, "a rejected message is never echoed")
	assert.NotContains(t, res.Stdout, executionSecret, "and neither is the secret")
	assert.Equal(t, []string{"assignment", "claim", "result"}, executionChain(t, rig.mailbox, valid),
		"the node kept serving through all of it")

	t.Run("work this node already answered is refused on a new branch", func(t *testing.T) {
		// The same triple, offered again after the node answered it: the
		// record of what was answered outlives the process that answered it.
		replay := executionBranchName("replay")
		orchestrator.offer(replay, orchestrator.probe(replay, "preflight"))
		fresh := executionBranchName("fresh")
		orchestrator.offer(fresh, orchestrator.probe(fresh, "second"))

		second := worker.restart(t, rig.repo, 4)
		executionAwaitMessage(t, rig.mailbox, fresh, "result")
		res := second.proc.Wait()

		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, executionRejections(res), "replay")
		assert.Equal(t, []string{"assignment"}, executionChain(t, rig.mailbox, replay))
		assert.Equal(t, []string{"assignment", "claim", "result"}, executionChain(t, rig.mailbox, fresh),
			"and other work of the same run is still answered")
	})
}

// TestExecutionWorkerPrunesAnsweredWorkWhileServing: a node can stay up past
// the full acceptance horizon. An old answer still blocks a replay while live,
// then a later answer removes it from the durable record without restarting
// the node.
//
// The old answer is seeded to expire a margin after the attempt starts, and
// the replay has to be answered inside that margin. The test controls the
// margin: a machine too loaded to answer in time gets a longer one on a fresh
// node rather than a failure, so the wall time is as short as the machine
// allows and a slow runner costs time rather than a false result.
func TestExecutionWorkerPrunesAnsweredWorkWhileServing(t *testing.T) {
	for _, margin := range []time.Duration{3 * time.Second, 10 * time.Second, 30 * time.Second, 90 * time.Second} {
		live := false
		t.Run(fmt.Sprintf("the old answer expires %s ahead", margin), func(t *testing.T) {
			live = pruneAnsweredWorkWithin(t, margin)
		})
		if live {
			return
		}
	}
	t.Fatal("no attempt answered the replay while the seeded answer was still live")
}

// pruneAnsweredWorkWithin runs one attempt of
// TestExecutionWorkerPrunesAnsweredWorkWhileServing with the old answer
// expiring margin from now. It answers false, and skips, when the node only
// reached the replay after the expiry, which proves nothing either way.
func pruneAnsweredWorkWithin(t *testing.T, margin time.Duration) bool {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))
	state := t.TempDir()
	nodeState := filepath.Join(state, executionNode)
	require.NoError(t, os.MkdirAll(nodeState, 0o755))
	seenPath := filepath.Join(nodeState, "seen.json")
	oldKey := orchestrator.run + " preflight 1"
	// A header may be issued 24 hours ahead of acceptance and remain valid
	// for 24 hours after issuance. Seed just inside that 48-hour boundary.
	expires := time.Now().Add(margin)
	oldAt := expires.Add(-48 * time.Hour)
	seed, err := json.Marshal(map[string]time.Time{oldKey: oldAt})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(seenPath, seed, 0o644))

	replay := executionBranchName("aaa-seen-prune-replay")
	orchestrator.offer(replay, orchestrator.probe(replay, "preflight",
		func(message map[string]any) {
			message["issuedAt"] = oldAt.Add(24 * time.Hour).Format(time.RFC3339)
		}))
	proc := rig.repo.StartCommandEnv([]string{executionSecretEnv + "=" + executionSecret},
		"worker", "--root", root, "--state-dir", state, "--idle-timeout", "30")
	worker := &executionWorker{t: t, proc: proc, stateDir: state, root: root}
	control := executionBranchName("zzz-seen-prune-control")
	orchestrator.offer(control, orchestrator.probe(control, "control"))
	executionAwaitMessage(t, rig.mailbox, control, "result")
	// The branches are served in name order, so the replay was answered
	// before the control result appeared.
	if time.Until(expires) <= 0 {
		worker.stop(t)
		t.Skipf("the node answered after the seeded answer expired; a longer margin follows")
		return false
	}
	before, err := os.ReadFile(seenPath)
	require.NoError(t, err)
	stored := map[string]time.Time{}
	require.NoError(t, json.Unmarshal(before, &stored))
	heldAt, ok := stored[oldKey]
	require.True(t, ok, "the live answer remains until its replay window ends")
	assert.True(t, heldAt.Equal(oldAt), "the replay must not refresh the old answer")
	assert.Equal(t, []string{"assignment"}, executionChain(t, rig.mailbox, replay))

	if remaining := time.Until(expires); remaining > 0 {
		time.Sleep(remaining + 100*time.Millisecond)
	}
	fresh := executionBranchName("seen-prune-fresh")
	orchestrator.offer(fresh, orchestrator.probe(fresh, "fresh"))
	executionAwaitMessage(t, rig.mailbox, fresh, "result")
	res := worker.stop(t)
	assert.Contains(t, executionRejections(res), "replay")
	assert.Equal(t, []string{"assignment"}, executionChain(t, rig.mailbox, replay))
	content, err := os.ReadFile(seenPath)
	require.NoError(t, err)
	stored = map[string]time.Time{}
	require.NoError(t, json.Unmarshal(content, &stored))
	assert.NotContains(t, stored, oldKey, "the fresh answer must prune the expired one from disk")
	assert.Contains(t, stored, orchestrator.run+" fresh 1")
	return true
}

// A future-issued assignment accepted 25 hours ago can still pass the
// 24-hour header window today. Its durable accepted-work tuple must survive a
// worker restart and reject that replay; a tuple older than 48 hours need not.
func TestExecutionWorkerKeepsFutureIssuedReplayKnowledge(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))
	state := t.TempDir()
	nodeState := filepath.Join(state, executionNode)
	require.NoError(t, os.MkdirAll(nodeState, 0o755))
	seenPath := filepath.Join(nodeState, "seen.json")
	now := time.Now()
	accepted := now.Add(-25 * time.Hour)
	liveKey := orchestrator.run + " future-issued 1"
	expiredKey := orchestrator.run + " expired 1"
	seed, err := json.Marshal(map[string]time.Time{
		liveKey: accepted, expiredKey: now.Add(-49 * time.Hour),
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(seenPath, seed, 0o644))

	replay := executionBranchName("aaa-future-issued-replay")
	orchestrator.offer(replay, orchestrator.probe(replay, "future-issued",
		func(message map[string]any) {
			message["issuedAt"] = accepted.Add(24*time.Hour - time.Second).Format(time.RFC3339)
		}))
	control := executionBranchName("zzz-future-issued-control")
	orchestrator.offer(control, orchestrator.probe(control, "control"))
	proc := rig.repo.StartCommandEnv([]string{executionSecretEnv + "=" + executionSecret},
		"worker", "--root", root, "--state-dir", state, "--idle-timeout", "10")
	worker := &executionWorker{t: t, proc: proc, stateDir: state, root: root}
	executionAwaitMessage(t, rig.mailbox, control, "result")
	reply := worker.stop(t)

	assert.Contains(t, executionRejections(reply), "replay")
	assert.Equal(t, []string{"assignment"}, executionChain(t, rig.mailbox, replay),
		"the still-valid assignment did not run a second time")
	content, err := os.ReadFile(seenPath)
	require.NoError(t, err)
	stored := map[string]time.Time{}
	require.NoError(t, json.Unmarshal(content, &stored))
	assert.Contains(t, stored, liveKey)
	assert.NotContains(t, stored, expiredKey)
}

// executionLabel is a branch-safe label for one table row's name.
func executionLabel(name string) string {
	var label strings.Builder
	for _, letter := range name {
		if letter >= 'a' && letter <= 'z' {
			label.WriteRune(letter)
		}
	}
	return label.String()
}

// TestExecutionWorkerLeavesWorkItCannotRun: an assignment of a kind this
// build does not execute is left exactly where the orchestrator put it, so
// the work stays queued for a node that can run it.
func TestExecutionWorkerLeavesWorkItCannotRun(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	// A relay carries one node's result onto another node's endpoint: it is a
	// kind of the protocol that no node ever executes, which is what this is
	// about. The kinds a node does execute grow gate by gate, and the claim is
	// about the ones it does not.
	build := executionBranchName("relaywork")
	orchestrator.offer(build, orchestrator.probe(build, "core",
		func(m map[string]any) { m["kind"] = "relay" }))
	valid := executionBranchName("probework")
	orchestrator.offer(valid, orchestrator.probe(valid, "preflight"))

	worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 4)
	executionAwaitMessage(t, rig.mailbox, valid, "result")
	res := worker.proc.Wait()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"assignment"}, executionChain(t, rig.mailbox, build))
	assert.NotContains(t, executionRejections(res), "chain",
		"work of another kind is not a refusal, it is work for somebody else")
	inspected, isInspected := executionLine(res, "assignment inspected")
	require.True(t, isInspected, "stdout:\n%s", res.Stdout)
	assert.Equal(t, executionNode, inspected.Str("node"))
}

// TestExecutionWorkerStartRefusals: what a node has to be told before it can
// serve, each missing piece named, plus the two refusals that are about the
// process rather than the configuration.
func TestExecutionWorkerStartRefusals(t *testing.T) {
	rig := newExecutionRig(t)

	for name, tc := range map[string]struct {
		adjust func(*models.ExecutionConfig)
		env    []string
		says   string
	}{
		"no name": {
			adjust: func(settings *models.ExecutionConfig) { settings.Name = "" },
			says:   "execution.name"},
		"no mailbox and no checkout to find one in": {
			adjust: func(settings *models.ExecutionConfig) { settings.Endpoint = "" },
			says:   "start the worker in a checkout"},
		"no secret variable": {
			adjust: func(settings *models.ExecutionConfig) { settings.SecretEnv = "" },
			says:   "execution.secretEnv"},
		"a secret variable that is unset": {
			env:  []string{executionSecretEnv + "="},
			says: executionSecretEnv},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := executionWorkerConfig(rig.mailbox)
			if tc.adjust != nil {
				tc.adjust(cfg.Execution)
			}
			root := writeNodeConfig(t, cfg)

			res := runWorker(t, rig, append([]string{executionSecretEnv + "=" + executionSecret}, tc.env...),
				"worker", "--root", root, "--state-dir", t.TempDir(), "--idle-timeout", "1")

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
			assert.Contains(t, diagnosticText(res), tc.says)
			_, isStarted := executionLine(res, "worker started")
			assert.False(t, isStarted, "a node that could not be told what it is never starts")
		})
	}

	t.Run("a node without a configuration file at all", func(t *testing.T) {
		res := runWorker(t, rig, nil, "worker", "--root", t.TempDir(), "--idle-timeout", "1")

		require.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "config file not found")
	})

	t.Run("a task may not start a node of its own", func(t *testing.T) {
		root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))

		res := runWorker(t, rig, append(executionWorkerMarker, executionSecretEnv+"="+executionSecret),
			"worker", "--root", root, "--state-dir", t.TempDir(), "--idle-timeout", "1")

		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		requireExecutionRefusal(t, res, executionAuthorityCode, executionAuthorityCategory)
	})

	t.Run("two nodes may not share one state folder", func(t *testing.T) {
		// The first node is interrupted below, so its idle window only has to
		// outlast the second start; the second is started once the first
		// holds the folder, or under load it is the first that gets refused.
		first := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 60)
		lock := filepath.Join(first.stateDir, executionNode, "worker.lock")
		require.Eventually(t, func() bool {
			_, err := os.Stat(lock)
			return err == nil
		}, 30*time.Second, 25*time.Millisecond, "the first node takes the state folder")

		res := runWorker(t, rig, []string{executionSecretEnv + "=" + executionSecret},
			"worker", "--root", first.root, "--state-dir", first.stateDir, "--idle-timeout", "1")

		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
		assert.Contains(t, diagnosticText(res), "state folder")
		first.proc.Signal(os.Interrupt)
		require.Equal(t, 0, first.proc.Wait().Code)
	})

	t.Run("a lock left behind by a process that is gone is taken over", func(t *testing.T) {
		root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))
		state := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(state, executionNode), 0o755))
		// A process id the kernel hands out last and that nothing here holds.
		require.NoError(t, os.WriteFile(filepath.Join(state, executionNode, "worker.lock"),
			[]byte("4194304"), 0o644))

		res := runWorker(t, rig, []string{executionSecretEnv + "=" + executionSecret},
			"worker", "--root", root, "--state-dir", state, "--idle-timeout", "1")

		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		_, isStarted := executionLine(res, "worker started")
		assert.True(t, isStarted, "a crashed node can be restarted without a person deleting a file")
	})

	t.Run("a lock its writer never filled is taken over after the grace", func(t *testing.T) {
		// Creating the lock and writing the process id are two steps, so an
		// empty lock is either an owner that is starting or one that died in
		// between. It is waited for, then replaced, and the node starts.
		root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))
		state := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(state, executionNode), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(state, executionNode, "worker.lock"), nil, 0o644))

		res := runWorker(t, rig, []string{executionSecretEnv + "=" + executionSecret},
			"worker", "--root", root, "--state-dir", state, "--idle-timeout", "1")

		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		_, isStarted := executionLine(res, "worker started")
		assert.True(t, isStarted, "an empty lock does not keep a node from restarting")
	})

	t.Run("a lock holding no process id is taken over", func(t *testing.T) {
		root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))
		state := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(state, executionNode), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(state, executionNode, "worker.lock"),
			[]byte("not-a-pid"), 0o644))

		res := runWorker(t, rig, []string{executionSecretEnv + "=" + executionSecret},
			"worker", "--root", root, "--state-dir", state, "--idle-timeout", "1")

		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	})

	t.Run("an answered-work record from another week is pruned", func(t *testing.T) {
		root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))
		state := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(state, executionNode), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(state, executionNode, "seen.json"),
			[]byte(`{"old-run old-task 1":"2020-01-01T00:00:00Z"}`), 0o644))
		orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
		branch := executionBranchName("pruned")
		orchestrator.offer(branch, orchestrator.probe(branch, "preflight"))

		res := runWorker(t, rig, []string{executionSecretEnv + "=" + executionSecret},
			"worker", "--root", root, "--state-dir", state, "--idle-timeout", "3")

		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, []string{"assignment", "claim", "result"}, executionChain(t, rig.mailbox, branch))
		record, err := os.ReadFile(filepath.Join(state, executionNode, "seen.json"))
		require.NoError(t, err)
		assert.NotContains(t, string(record), "old-run",
			"a triple older than the replay window is refused by the window anyway")
	})
}

// TestExecutionWorkerConcurrentStaleStateClaimHasOneOwner: taking over a
// stale lock is several filesystem operations, and processes started at once
// can each finish one they read as a win. Every claim settles before it is
// trusted, so of four workers started together against one stale lock exactly
// one serves the state folder and the others are refused.
func TestExecutionWorkerConcurrentStaleStateClaimHasOneOwner(t *testing.T) {
	rig := newExecutionRig(t)
	root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))
	state := t.TempDir()
	dir := filepath.Join(state, executionNode)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "worker.lock"), []byte("4194304"), 0o644))

	const contenders = 4
	workers := make([]*harness.Proc, contenders)
	for i := range workers {
		workers[i] = rig.repo.StartCommandEnv(
			[]string{executionSecretEnv + "=" + executionSecret},
			"worker", "--root", root, "--state-dir", state, "--idle-timeout", "3")
	}
	started := 0
	for _, worker := range workers {
		res := worker.Wait()
		if res.Code == 0 {
			_, ok := executionLine(res, "worker started")
			require.True(t, ok, "a successful worker actually served: %s", res.Stdout)
			started++
			continue
		}
		requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
	}
	assert.Equal(t, 1, started, "only one process served the shared state")
}

// TestExecutionWorkerStopsWhenAnotherProcessClaimsItsFolder: a serving node
// reads its worker.lock again before every claim. Once another process's id
// is written there, the node leaves the assignment where the orchestrator put
// it, stops through its ordinary exit path with E225, and leaves the other
// process's lock in place.
func TestExecutionWorkerStopsWhenAnotherProcessClaimsItsFolder(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	served := executionBranchName("before-takeover")
	orchestrator.offer(served, orchestrator.probe(served, "served"))
	executionAwaitMessage(t, rig.mailbox, served, "result")

	// This test process is running and is not the worker, which is what a
	// process that took the folder over looks like from inside it.
	lock := filepath.Join(worker.stateDir, executionNode, "worker.lock")
	other := strconv.Itoa(os.Getpid())
	require.NoError(t, os.WriteFile(lock, []byte(other), 0o644))
	left := executionBranchName("after-takeover")
	orchestrator.offer(left, orchestrator.probe(left, "left"))
	res := worker.proc.Wait()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
	assert.Contains(t, diagnosticText(res), "served by process "+other)
	stopped, isStopped := executionLine(res, "worker stopped")
	require.True(t, isStopped, "the node stopped through its ordinary exit path\nstdout:\n%s", res.Stdout)
	assert.Equal(t, "disowned", stopped.Str("reason"))
	assert.Equal(t, []string{"assignment"}, executionChain(t, rig.mailbox, left),
		"the assignment is left for the process that owns the folder")
	held, err := os.ReadFile(lock)
	require.NoError(t, err, "the other process's lock is not removed")
	assert.Equal(t, other, string(held))
}

// TestExecutionWorkerStopsOnSignal: a node asked to stop stops, cleanly and
// with exit 0, because a serving command that was asked to stop has done what
// it was asked.
func TestExecutionWorkerStopsOnSignal(t *testing.T) {
	rig := newExecutionRig(t)
	worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 0)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	branch := executionBranchName("signalled")
	orchestrator.offer(branch, orchestrator.probe(branch, "preflight"))
	executionAwaitMessage(t, rig.mailbox, branch, "result")

	worker.proc.Signal(syscall.SIGINT)
	res := worker.proc.Wait()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	stopped, isStopped := executionLine(res, "worker stopped")
	require.True(t, isStopped, "stdout:\n%s", res.Stdout)
	assert.Equal(t, "signal", stopped.Str("reason"))
}

// TestExecutionWorkerSurvivesALostStateDir: the object cache is a cache. A
// node whose folder disappears under it makes another one and carries on.
func TestExecutionWorkerSurvivesALostStateDir(t *testing.T) {
	rig := newExecutionRig(t)
	worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 0)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	first := executionBranchName("beforeloss")
	orchestrator.offer(first, orchestrator.probe(first, "first"))
	executionAwaitMessage(t, rig.mailbox, first, "result")

	require.NoError(t, os.RemoveAll(filepath.Join(worker.stateDir, executionNode, "cache")))
	second := executionBranchName("afterloss")
	orchestrator.offer(second, orchestrator.probe(second, "second"))
	executionAwaitMessage(t, rig.mailbox, second, "result")

	worker.proc.Signal(syscall.SIGINT)
	res := worker.proc.Wait()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"assignment", "claim", "result"}, executionChain(t, rig.mailbox, second))
}

// TestExecutionWorkerGitFaults: a node whose git fails once reports it,
// stays up, and answers the next thing it is asked. The cache being
// dispensable is what makes that possible: every failure reopens it.
func TestExecutionWorkerGitFaults(t *testing.T) {
	for name, pattern := range map[string]string{
		"a poll that fails":          "*ls-remote*dispat-worker-*",
		"a fetch that fails":         "*fetch*dispat-worker-*",
		"an object write that fails": "*hash-object*",
		"a tree write that fails":    "*mktree*",
		"a commit that fails":        "*commit-tree*",
		"a push that fails":          "*push*dispat-worker-*",
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionRig(t)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: pattern, Nth: 1})
			worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 0, fault.Env()...)
			orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
			branch := executionBranchName("afterfault")
			orchestrator.offer(branch, orchestrator.probe(branch, "preflight"))

			executionAwaitMessage(t, rig.mailbox, branch, "result")
			worker.proc.Signal(syscall.SIGINT)
			res := worker.proc.Wait()

			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, fault.Matches(), "the fault reached the invocation it names")
			assert.Equal(t, []string{"assignment", "claim", "result"}, executionChain(t, rig.mailbox, branch),
				"and the node answered once the fault had passed")
		})
	}
}
