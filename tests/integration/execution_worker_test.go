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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

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
	build := executionBranchName("publishwork")
	orchestrator.offer(build, orchestrator.probe(build, "core",
		func(m map[string]any) { m["kind"] = "publish" }))
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
		"no mailbox": {
			adjust: func(settings *models.ExecutionConfig) { settings.Endpoint = "" },
			says:   "execution.endpoint"},
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
		first := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 6)

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
