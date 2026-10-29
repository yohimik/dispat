// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a serving node, driven through the compiled binary.
//
// A serving node answers authenticated work addressed to it, refuses work it
// cannot authenticate without exposing its contents, and stops when asked.
// The current protocol cases use real compiled runs; retired protocol cases
// retain hostile documents to prove that they cannot start a command.

import (
	"encoding/json"
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

const (
	executionReplayWorkerIdleSeconds  = 120
	executionReplayStateFileName      = "seen.json"
	executionReplayWriteErrorText     = "replacing the answered-work record "
	executionReplayPreflightCode      = "E225"
	executionReplayWorkerReadyMessage = "worker ready"
	executionReplayWorkerField        = "worker"
)

// TestExecutionWorkerAnswersProbe: the whole exchange one probe produces, as
// the mailbox records it, and the node's own description of itself.
func TestExecutionWorkerAnswersProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
	})
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*refs/heads/dispat-task-*", Code: 128})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(3) }), 20,
		peerGit.Env()...)
	release := rig.release()
	require.Equal(t, 0, release.Code, "stdout:\n%s\nstderr:\n%s", release.Stdout, release.Stderr)
	res := worker.stop(t)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	ready, isReady := executionLine(release, "worker ready")
	require.True(t, isReady, "the first node accepted the signed return-channel report: %s", release.Stdout)
	assert.Equal(t, executionNode, ready.Str("worker"))
	assert.Equal(t, runtime.GOOS, ready.Str("os"))
	assert.Equal(t, runtime.GOARCH, ready.Str("arch"))
	assert.Equal(t, float64(3), ready["capacity"])
	assert.NotEmpty(t, ready.Str("dispat"))
	assert.Equal(t, executionNode, rig.nodesByPackage()["core"],
		"the accepted report made the peer eligible for the real build")
	assert.Zero(t, peerGit.Matches(), "the peer returned Probe, Claim and Result without pushing a canonical task ref")
	started, isStarted := executionLine(res, "worker started")
	require.True(t, isStarted, "stdout:\n%s", res.Stdout)
	assert.Equal(t, executionNode, started.Str("node"))
	stopped, isStopped := executionLine(res, "worker stopped")
	require.True(t, isStopped)
	assert.Equal(t, "signal", stopped.Str("reason"))
}

// The replay record is durable before a worker answers a v3 probe or starts
// an assigned command. A failed state write must leave the next run without
// an accepted claim or command; repairing the path lets the same process work.
func TestExecutionWorkerRefusesWorkWhenItsReplayRecordCannotBeSaved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionSweepRig(t, executionRecordingScript, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), executionReplayWorkerIdleSeconds)
	defer worker.stop(t)
	first := rig.sweep(nil)
	require.Equal(t, 0, first.Code, "first v3 sweep:\n%s\n%s", first.Stdout, first.Stderr)
	before := len(rig.runs())
	seen := filepath.Join(worker.stateDir, executionNode, executionReplayStateFileName)
	require.FileExists(t, seen)
	require.NoError(t, os.Remove(seen))
	require.NoError(t, os.Mkdir(seen, 0o755))
	blocked := rig.sweep(nil)
	require.NotEqual(t, 0, blocked.Code, "a worker unable to save replay state answered work")
	assert.True(t, harness.IsCodePresent(executionEvents(blocked), executionReplayPreflightCode))
	_, isBlockedReady := executionLine(blocked, executionReplayWorkerReadyMessage)
	assert.False(t, isBlockedReady, "a worker cannot join the run before replay state is durable")
	workerStdout, workerStderr := worker.proc.CapturedOutput()
	require.Contains(t, workerStdout+workerStderr, executionReplayWriteErrorText+seen,
		"the worker reached the injected replay-record failure")
	assert.Equal(t, before, len(rig.runs()), "no command starts without durable replay state")
	require.NoError(t, os.Remove(seen))
	recovered := rig.sweep(nil)
	require.Equal(t, 0, recovered.Code, "recovered v3 sweep:\n%s\n%s", recovered.Stdout, recovered.Stderr)
	ready, isReady := executionLine(recovered, executionReplayWorkerReadyMessage)
	require.True(t, isReady, "the same worker accepted a fresh preflight after repair")
	assert.Equal(t, executionNode, ready.Str(executionReplayWorkerField))
	require.FileExists(t, seen, "recovery durably recreated the replay record")
	runs := rig.runs()
	require.Len(t, runs, before+len(executionSweepPackages), "every package ran once after repair")
	var recoveredPackages []string
	for _, run := range runs[before:] {
		assert.Equal(t, executionNode, run.Node)
		recoveredPackages = append(recoveredPackages, run.Package)
	}
	assert.ElementsMatch(t, executionSweepPackages, recoveredPackages)
}

// A correctly signed legacy publish assignment still grants no publication
// authority. The worker refuses it before claim and continues serving builds.
func TestExecutionWorkerRefusesLegacyPublication(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	state := orchestrator.prepareInputState("legacy-publish")
	// Legacy non-probe refs are no longer polled. Give this hostile document a
	// probe routing hint so the worker actually authenticates and refuses its
	// signed publish authority; the branch hint never grants that authority.
	legacy := executionCraftedBranchName("probe", "legacy-publish")
	orchestrator.offer(legacy, orchestrator.publication(legacy, "legacy", state))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(2) }), 4)

	reply := worker.proc.Wait()
	require.Equal(t, 0, reply.Code, "stdout:\n%s\nstderr:\n%s", reply.Stdout, reply.Stderr)
	assert.Empty(t, rig.runs(), "neither hooks nor publication commands may run")
	assert.Contains(t, executionRejections(reply), "permit")
	assert.Equal(t, []string{"assignment"}, executionChain(t, rig.mailbox, legacy),
		"a signed publish assignment is refused before claim")
	stopped, isStopped := executionLine(reply, "worker stopped")
	require.True(t, isStopped, "the node kept serving after the refusal: %s", reply.Stdout)
	assert.Equal(t, "idle", stopped.Str("reason"))
}

// Retired handcrafted v2 probe refs and malformed v3-shaped probes cannot
// grant a claim. The real v3 signed-carrier refusal ladder lives in
// execution.TestChoreographyWorkerRefusesCraftedCarriedInputs and the header,
// cancellation and bootstrap verifier tests; this black-box test keeps the
// hostile documents and checks that none executes through the current CLI.
func TestExecutionWorkerRetiredAssignmentsCannotClaim(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	forged := "forged-" + strings.Repeat("x", 16)
	refused := map[string]struct {
		document func(string) map[string]any
		options  []func(*executionMessageOptions)
	}{
		"foreign signer": {options: []func(*executionMessageOptions){
			func(o *executionMessageOptions) { o.secret = forged }}},
		"signed as result": {options: []func(*executionMessageOptions){
			func(o *executionMessageOptions) { o.signedAs = "result" }}},
		"unsigned tip": {options: []func(*executionMessageOptions){
			func(o *executionMessageOptions) { o.isSigned = false }}},
		"another node": {document: func(branch string) map[string]any {
			return orchestrator.probe(branch, "preflight", func(m map[string]any) { m["node"] = "build-b" })
		}},
		"another branch": {document: func(branch string) map[string]any {
			return orchestrator.probe(branch, "preflight", func(m map[string]any) { m["branch"] = executionBranchName("elsewhere") })
		}},
		"unsupported protocol": {document: func(branch string) map[string]any {
			return orchestrator.probe(branch, "preflight", func(m map[string]any) { m["protocol"] = executionProtocolVersion + 1 })
		}},
		"stale issue time": {document: func(branch string) map[string]any {
			return orchestrator.probe(branch, "preflight", func(m map[string]any) { m["issuedAt"] = "2026-09-01T00:00:00Z" })
		}},
		"oversized document": {document: func(branch string) map[string]any {
			return orchestrator.probe(branch, "preflight", func(m map[string]any) { m["generation"] = strings.Repeat("g", 4096) })
		}},
	}
	for name, tc := range refused {
		branch := executionBranchName(executionLabel(name))
		document := orchestrator.probe(branch, "preflight")
		if tc.document != nil {
			document = tc.document(branch)
		}
		orchestrator.offer(branch, document, tc.options...)
	}
	illegal := executionBranchName("illegalchain")
	first := orchestrator.offer(illegal, orchestrator.probe(illegal, "preflight"))
	orchestrator.offer(illegal, orchestrator.probe(illegal, "preflight"),
		func(o *executionMessageOptions) { o.parents = []string{first} })
	worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) {
			settings.Transfer = &models.ExecutionTransferConfig{MaxManifestBytes: 2048}
		}), 4)
	time.Sleep(3 * time.Second)
	res := worker.stop(t)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for name := range refused {
		branch := executionBranchName(executionLabel(name))
		assert.Equal(t, []string{"assignment"}, executionChain(t, rig.mailbox, branch), name)
	}
	assert.Equal(t, []string{"assignment", "assignment"}, executionChain(t, rig.mailbox, illegal))
	assert.Empty(t, rig.runs(), "no hostile document started a command")
	assert.NotContains(t, res.Stdout, forged, "the foreign secret is never logged")
	assert.NotContains(t, res.Stdout, executionSecret, "the signing secret is never logged")
}

// A serving v3 worker prunes expired answered-work tuples when it writes a
// fresh accepted probe. The live boundary tuple remains durable without a
// process restart, and the new run still executes normally.
func TestExecutionWorkerPrunesAnsweredWorkWhileServing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionSweepRig(t, `printf 'worker-v3 %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG"`, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
	})
	root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))
	state := t.TempDir()
	nodeState := filepath.Join(state, executionNode)
	require.NoError(t, os.MkdirAll(nodeState, 0o755))
	seenPath := filepath.Join(nodeState, "seen.json")
	now := time.Now().UTC()
	oldKey := "run-expired preflight 1"
	liveKey := "run-boundary preflight 1"
	seed, err := json.Marshal(map[string]time.Time{
		oldKey: now.Add(-49 * time.Hour), liveKey: now.Add(-25 * time.Hour),
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(seenPath, seed, 0o644))
	proc := rig.repo.StartCommandEnv(append(executionNodeHomeEnv(t, state),
		executionSecretEnv+"="+executionSecret, executionBuildLogEnv+"="+rig.builds),
		"choreographer", "--root", root, "--state-dir", state, "--idle-timeout", "60")
	worker := &executionWorker{t: t, proc: proc, stateDir: state, root: root}
	defer worker.stop(t)
	result := rig.sweep(nil)
	require.Equal(t, 0, result.Code, "v3 sweep:\n%s\n%s", result.Stdout, result.Stderr)
	content, err := os.ReadFile(seenPath)
	require.NoError(t, err)
	stored := map[string]time.Time{}
	require.NoError(t, json.Unmarshal(content, &stored))
	assert.NotContains(t, stored, oldKey)
	assert.Contains(t, stored, liveKey)
	assert.Greater(t, len(stored), 1, "the accepted v3 run wrote fresh replay knowledge")
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
	proc := rig.repo.StartCommandEnv(append(executionNodeHomeEnv(t, state),
		executionSecretEnv+"="+executionSecret),
		"choreographer", "--root", root, "--state-dir", state, "--idle-timeout", "10")
	worker := &executionWorker{t: t, proc: proc, stateDir: state, root: root}
	control := rig.release()
	require.Equal(t, 0, control.Code, "stdout:\n%s\nstderr:\n%s", control.Stdout, control.Stderr)
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

// TestExecutionWorkerReadsAnAnsweredWorkRecordACrashCut: a worker killed
// while it appends a claim to seen.json leaves the object without its closing
// brace. The next worker must start on it, refuse a replay of what it holds,
// and leave a whole record behind, because refusing to start would leave
// deleting the record, and with it the replay protection, as the only way out.
func TestExecutionWorkerReadsAnAnsweredWorkRecordACrashCut(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))
	state := t.TempDir()
	nodeState := filepath.Join(state, executionNode)
	require.NoError(t, os.MkdirAll(nodeState, 0o755))
	seenPath := filepath.Join(nodeState, "seen.json")
	liveKey := orchestrator.run + " cut-live 1"
	cutKey := orchestrator.run + " cut-unfinished 1"
	whole, err := json.Marshal(map[string]time.Time{liveKey: time.Now().Add(-time.Hour)})
	require.NoError(t, err)
	cut := append(whole[:len(whole)-1:len(whole)-1], []byte(`,"`+cutKey+`":"20`)...)
	require.NoError(t, os.WriteFile(seenPath, cut, 0o644))

	replay := executionBranchName("aaa-cut-replay")
	orchestrator.offer(replay, orchestrator.probe(replay, "cut-live"))
	proc := rig.repo.StartCommandEnv(append(executionNodeHomeEnv(t, state),
		executionSecretEnv+"="+executionSecret),
		"choreographer", "--root", root, "--state-dir", state, "--idle-timeout", "10")
	worker := &executionWorker{t: t, proc: proc, stateDir: state, root: root}
	control := rig.release()
	require.Equal(t, 0, control.Code, "stdout:\n%s\nstderr:\n%s", control.Stdout, control.Stderr)
	reply := worker.stop(t)

	assert.Contains(t, executionRejections(reply), "replay")
	assert.Equal(t, []string{"assignment"}, executionChain(t, rig.mailbox, replay),
		"an answer held before the cut is not answered again")
	content, err := os.ReadFile(seenPath)
	require.NoError(t, err)
	stored := map[string]time.Time{}
	require.NoError(t, json.Unmarshal(content, &stored), "the first claim repairs the record")
	assert.Contains(t, stored, liveKey)
	assert.Greater(t, len(stored), 1, "the real v3 probe added a complete answer after the cut")
	assert.NotContains(t, stored, cutKey, "the claim that never finished is forgotten")
}

// The signed v3 clock acceptance and refusal ladder moved to
// execution.TestWorkerV3AcceptsModestClockSkew; the retired handcrafted
// v2 probe cannot exercise the live return-channel path.

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

	worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 4)
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
		"a late identity seed variable that is unset": {
			adjust: func(settings *models.ExecutionConfig) { settings.IdentityKeyEnv = executionLateJoinIdentitySeedEnv },
			says:   executionLateJoinIdentitySeedEnv},
		"a malformed late identity seed": {
			adjust: func(settings *models.ExecutionConfig) { settings.IdentityKeyEnv = executionLateJoinIdentitySeedEnv },
			env:    []string{executionLateJoinIdentitySeedEnv + "=not-a-seed"},
			says:   "canonical base64url Ed25519 seed"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := executionWorkerConfig(rig.mailbox)
			if tc.adjust != nil {
				tc.adjust(cfg.Execution)
			}
			root := writeNodeConfig(t, cfg)

			res := runWorker(t, rig, append([]string{executionSecretEnv + "=" + executionSecret}, tc.env...),
				"choreographer", "--root", root, "--state-dir", t.TempDir(), "--idle-timeout", "1")

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
			assert.Contains(t, diagnosticText(res), tc.says)
			_, isStarted := executionLine(res, "worker started")
			assert.False(t, isStarted, "a node that could not be told what it is never starts")
		})
	}

	t.Run("a node without a configuration file at all", func(t *testing.T) {
		res := runWorker(t, rig, nil, "choreographer", "--root", t.TempDir(), "--idle-timeout", "1")

		require.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "config file not found")
	})

	t.Run("a node whose configuration file does not parse", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte(`{"execution":`), 0o644))

		res := runWorker(t, rig, nil, "choreographer", "--root", root, "--idle-timeout", "1")

		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "invalid configuration")
		_, isStarted := executionLine(res, "worker started")
		assert.False(t, isStarted, "a node that could not read its configuration never starts")
	})

	t.Run("a task may not start a node of its own", func(t *testing.T) {
		root := writeNodeConfig(t, executionWorkerConfig(rig.mailbox))

		res := runWorker(t, rig, append(executionWorkerMarker, executionSecretEnv+"="+executionSecret),
			"choreographer", "--root", root, "--state-dir", t.TempDir(), "--idle-timeout", "1")

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
			"choreographer", "--root", first.root, "--state-dir", first.stateDir, "--idle-timeout", "1")

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
			"choreographer", "--root", root, "--state-dir", state, "--idle-timeout", "1")

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
			"choreographer", "--root", root, "--state-dir", state, "--idle-timeout", "1")

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
			"choreographer", "--root", root, "--state-dir", state, "--idle-timeout", "1")

		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	})

	t.Run("an answered-work record from another week is pruned", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the distributed durable node-slot backend is not available on Windows")
		}
		sweepRig := newExecutionSweepRig(t, executionSweepScript, func(cfg *models.File) {
			cfg.Execution.Workers = cfg.Execution.Workers[:1]
			cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10}
			cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
				Publish: models.RunOnlyOrchestrator}
		})
		root := writeNodeConfig(t, executionWorkerConfig(sweepRig.mailbox))
		state := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(state, executionNode), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(state, executionNode, "seen.json"),
			[]byte(`{"old-run old-task 1":"2020-01-01T00:00:00Z"}`), 0o644))
		env := append([]string{executionSecretEnv + "=" + executionSecret,
			executionBuildLogEnv + "=" + sweepRig.builds}, executionNodeHomeEnv(t, state)...)
		proc := sweepRig.repo.StartCommandEnv(env, "choreographer", "--root", root,
			"--state-dir", state, "--idle-timeout", "20")
		defer func() { proc.Signal(syscall.SIGINT); _ = proc.Wait() }()
		res := sweepRig.sweep(nil)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		record, err := os.ReadFile(filepath.Join(state, executionNode, "seen.json"))
		require.NoError(t, err)
		assert.NotContains(t, string(record), "old-run",
			"a triple older than the replay window is refused by the window anyway")
		assert.Contains(t, string(record), "core:run", "a compiled v3 claim rewrote the live replay record")
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
			append(executionNodeHomeEnv(t, state), executionSecretEnv+"="+executionSecret),
			"choreographer", "--root", root, "--state-dir", state, "--idle-timeout", "3")
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
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Execution.Timeouts.Preflight = 3
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60)
	served := rig.release()
	require.Equal(t, 0, served.Code, "stdout:\n%s\nstderr:\n%s", served.Stdout, served.Stderr)
	require.Contains(t, rig.repo.TagList(), "core@0.1.0")

	// This test process is running and is not the worker, which is what a
	// process that took the folder over looks like from inside it.
	lock := filepath.Join(worker.stateDir, executionNode, "worker.lock")
	other := strconv.Itoa(os.Getpid())
	require.NoError(t, os.WriteFile(lock, []byte(other), 0o644))
	rig.repo.WriteFile("packages/core/after-takeover.txt", "new input\n")
	rig.repo.Commit("feat(core): request work after state takeover")
	left := rig.release()
	res := worker.proc.Wait()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
	assert.Contains(t, diagnosticText(res), "served by process "+other)
	stopped, isStopped := executionLine(res, "worker stopped")
	require.True(t, isStopped, "the node stopped through its ordinary exit path\nstdout:\n%s", res.Stdout)
	assert.Equal(t, "disowned", stopped.Str("reason"))
	assert.NotEqual(t, 0, left.Code, "the old owner cannot take a new signed run")
	assert.NotContains(t, rig.repo.TagList(), "core@0.2.0", "the new input was not published")
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
	lock := filepath.Join(worker.stateDir, executionNode, "worker.lock")
	require.Eventually(t, func() bool {
		_, err := os.Stat(lock)
		return err == nil
	}, 10*time.Second, 25*time.Millisecond, "the node started serving before it was signalled")

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
	rig := newExecutionRig(t, pinPackages(map[string]*models.RunOnly{
		"core": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
	}))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	first := rig.release()
	require.Equal(t, 0, first.Code, "stdout:\n%s\nstderr:\n%s", first.Stdout, first.Stderr)
	require.Contains(t, rig.repo.TagList(), "core@0.1.0")
	firstLedger := executionProtocolRefs(t, rig.mailbox, "dispat-ledger-")
	require.Len(t, firstLedger, 1, "the first release has one signed event feed")

	const (
		activeCacheDirectory  = "cache"
		retiredCacheDirectory = "cache-lost"
	)
	nodeState := filepath.Join(worker.stateDir, executionNode)
	cache := filepath.Join(nodeState, activeCacheDirectory)
	retired := filepath.Join(nodeState, retiredCacheDirectory)
	// Move the live cache out of its expected path atomically. Removing its
	// object files while the worker fetches into them can fail with ENOTEMPTY.
	originalCache, err := os.Stat(cache)
	require.NoError(t, err)
	require.NoError(t, os.Rename(cache, retired))
	retiredCache, err := os.Stat(retired)
	require.NoError(t, err)
	require.True(t, os.SameFile(originalCache, retiredCache), "the live cache was moved")
	if activeCache, err := os.Stat(cache); err == nil {
		require.False(t, os.SameFile(originalCache, activeCache), "the worker recreated a distinct cache")
	} else {
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	rig.repo.WriteFile("packages/core/after-loss.txt", "new input\n")
	rig.repo.Commit("feat(core): build after cache loss")
	second := rig.release()
	require.Equal(t, 0, second.Code, "stdout:\n%s\nstderr:\n%s", second.Stdout, second.Stderr)
	require.Contains(t, rig.repo.TagList(), "core@0.2.0")
	activeCache, err := os.Stat(cache)
	require.NoError(t, err, "the second release recreated the worker cache")
	require.False(t, os.SameFile(originalCache, activeCache), "the new cache has its own filesystem identity")
	secondLedgers := executionProtocolRefs(t, rig.mailbox, "dispat-ledger-")
	require.Len(t, secondLedgers, 2, "the second release has its own signed event feed")
	secondLedger := ""
	for _, ref := range secondLedgers {
		if ref != firstLedger[0] {
			secondLedger = ref
		}
	}
	require.NotEmpty(t, secondLedger, "the second signed run is retained")
	ledgerCommits := strings.Fields(bareGit(t, rig.mailbox, "rev-list", "--first-parent",
		"-n", strconv.Itoa(lateJoinLedgerScanLimit), "refs/heads/"+secondLedger))
	claimFound := false
	for _, oid := range ledgerCommits {
		document := bareGit(t, rig.mailbox, "show", oid+":dispat/choreography-ledger.json")
		signature := strings.TrimSpace(bareGit(t, rig.mailbox, "show", oid+":dispat/choreography-ledger.sig"))
		require.Equal(t, executionSign("choreography-ledger", executionSecret, []byte(document)), signature,
			"the second run's claim is in its signed event feed")
		var record struct {
			Event *executionLateJoinLedgerEvent `json:"event"`
		}
		require.NoError(t, json.Unmarshal([]byte(document), &record))
		if record.Event != nil && record.Event.Kind == pickupFailoverClaimKind && record.Event.Task == "core:1:" {
			assert.Equal(t, executionNode, record.Event.Node, "the second build was claimed by this worker")
			claimFound = true
			break
		}
	}
	require.True(t, claimFound, "the second signed run contains a worker build claim")

	worker.proc.Signal(syscall.SIGINT)
	res := worker.proc.Wait()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 2, strings.Count(res.Stdout, `"message":"task claimed"`),
		"the same worker process claimed both runs across the cache loss")
}

// Transient Git read errors on the v3 serving node cannot make it push or
// start a command twice. Its next poll may reread the same signed task and
// complete the actual compiled sweep. A push-reject transport is also used
// as an authority check: the worker never pushes a canonical task ref.
func TestExecutionWorkerGitFaults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	for _, tc := range []struct {
		name, pattern string
		wantMatches   int
		mayFail       bool
	}{
		{"poll fails once", "*ls-remote*", 1, false},
		{"fetch fails once", "*-C * fetch *", 1, false},
		{"blob write fails once", "*hash-object*", 1, true},
		{"tree write fails once", "*mktree*", 1, true},
		{"commit write fails once", "*commit-tree*", 1, true},
		{"canonical task mutation forbidden", "*push*refs/heads/dispat-task-*", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newExecutionSweepRig(t,
				`printf 'worker-git-fault %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG"`,
				func(cfg *models.File) {
					cfg.Execution.Workers = cfg.Execution.Workers[:1]
					cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 15, Task: 30, Cancel: 5}
					cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
						Publish: models.RunOnlyOrchestrator}
				})
			nth := 1
			if tc.wantMatches == 0 {
				nth = 0 // reject every attempted canonical task push
			}
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: tc.pattern, Nth: nth, Code: 128})
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60, fault.Env()...)
			defer worker.stop(t)
			res := rig.sweep(nil)
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("worker Git fault sweep exit %d:\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
				}
			})
			if !tc.mayFail {
				require.Equal(t, 0, res.Code, "v3 sweep after %s:\n%s\n%s", tc.name, res.Stdout, res.Stderr)
			}
			if tc.wantMatches == 0 {
				assert.Zero(t, fault.Matches(), "the worker must never push a canonical task ref")
			} else {
				assert.Positive(t, fault.Matches(), "the injected Git operation was reached")
			}
			content, err := os.ReadFile(rig.builds)
			if tc.mayFail && os.IsNotExist(err) {
				content = nil
			} else {
				require.NoError(t, err)
			}
			for _, pkg := range executionSweepPackages {
				count := strings.Count(string(content), "worker-git-fault "+pkg+"\n")
				if tc.mayFail {
					assert.LessOrEqual(t, count, 1, "an uncertain result cannot rerun a command")
				} else {
					assert.Equal(t, 1, count, "the peer ran each package script once")
				}
			}
		})
	}
}
