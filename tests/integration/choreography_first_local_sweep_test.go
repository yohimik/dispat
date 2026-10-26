package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A first node killed after a peer's accepted Claim restores the same pinned
// TLS listener and signs Cancel→Ack→Terminal before retiring the generation.
// The new invocation's empty window must not execute the abandoned command.
func TestChoreographyCompiledSweepRestoresReturnChannelForClaimedPeer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	const script = `printf 'recovery-start %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG";
sleep 60;
printf 'recovery-finished %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG"`
	rig := newExecutionSweepRig(t, script, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 120, Cancel: 5}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
	})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*", Code: 128})
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 90,
		append(peerGit.Env(), "HOME="+peerHome,
			"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))...)
	defer func() {
		stopped := peer.stop(t)
		t.Logf("peer stdout:\n%s\npeer stderr:\n%s", stopped.Stdout, stopped.Stderr)
	}()
	env := []string{"HOME=" + firstHome,
		"XDG_CONFIG_HOME=" + filepath.Join(firstHome, "config")}
	started := rig.repo.StartReleaseEnv(rig.env(env...), "run", "tests", "--since", "all",
		"--return-endpoint", rig.returnURL)
	startedTask := false
	for stop := time.After(25 * time.Second); !startedTask; {
		content, err := os.ReadFile(rig.builds)
		startedTask = err == nil && strings.Contains(string(content), "recovery-start core\n")
		if startedTask {
			break
		}
		select {
		case <-stop:
			first := started.Wait()
			t.Fatalf("peer command did not start before crash; stdout:\n%s\nstderr:\n%s", first.Stdout, first.Stderr)
		case <-time.After(50 * time.Millisecond):
		}
	}
	started.Signal(syscall.SIGKILL)
	first := started.Wait()
	require.NotEqual(t, 0, first.Code)
	base := filepath.Join(rig.repo.Root, ".git", "dispat", "return-runs")
	_, err := os.Stat(filepath.Join(base, "active-run"))
	require.NoError(t, err, "killed first node lost its active generation locator")
	second := rig.repo.CommandEnv(rig.env(env...), "run", "tests", "--since", "HEAD",
		"--return-endpoint", rig.returnURL)
	require.Equal(t, 0, second.Code, "recovery stdout:\n%s\nrecovery stderr:\n%s", second.Stdout, second.Stderr)
	content, err := os.ReadFile(rig.builds)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(content), "recovery-start core\n"))
	require.NotContains(t, string(content), "recovery-finished core\n")
	requireChoreographyTaskChain(t, rig.mailbox, rig.branches(),
		"choreography-claim", "cancel", "ack", "choreography-decision")
	_, err = os.Stat(filepath.Join(base, "active-run"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Zero(t, peerGit.Matches())
	require.False(t, remoteHoldsLock(t, rig.origin))
}

// Kill the first node after it has accepted a signed Result but before its
// terminal decision push. Recovery verifies the retained carried Assignment
// and Result and settles that attempt without running the command again.
func TestChoreographyCompiledSweepRecoversAcceptedResultBeforeDecision(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	const script = `printf 'result-start %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG"`
	rig := newExecutionSweepRig(t, script, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30, Cancel: 5}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
	})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*", Code: 128})
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60,
		append(peerGit.Env(), "HOME="+peerHome,
			"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))...)
	peerStopped := false
	defer func() {
		if !peerStopped {
			peer.stop(t)
		}
	}()
	decisionGate := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*dispat-ledger-*", Nth: 7, Hold: true,
	})
	env := append(rig.env("HOME="+firstHome,
		"XDG_CONFIG_HOME="+filepath.Join(firstHome, "config")), decisionGate.Env()...)
	first := rig.repo.StartReleaseEnv(env, "run", "tests", "--since", "all",
		"--return-endpoint", rig.returnURL)
	held := false
	for until := time.After(30 * time.Second); !held; {
		held = decisionGate.IsHeld()
		if held {
			break
		}
		select {
		case <-until:
			decisionGate.Resume()
			result := first.Wait()
			t.Fatalf("the terminal decision push was not held; matches=%d stdout:\n%s\nstderr:\n%s",
				decisionGate.Matches(), result.Stdout, result.Stderr)
		case <-time.After(25 * time.Millisecond):
		}
	}
	acceptedResult := false
	for _, branch := range rig.branches() {
		if !strings.HasPrefix(branch, "refs/heads/dispat-task-") {
			continue
		}
		kinds := executionReadChain(rig.mailbox, strings.TrimPrefix(branch, "refs/heads/"))
		if slices.Contains(kinds, "choreography-result") && !slices.Contains(kinds, "choreography-decision") {
			acceptedResult = true
		}
	}
	if !acceptedResult {
		decisionGate.Resume()
		result := first.Wait()
		t.Fatalf("held ledger push before accepted Result; matches=%d stdout:\n%s\nstderr:\n%s",
			decisionGate.Matches(), result.Stdout, result.Stderr)
	}
	first.Signal(syscall.SIGKILL)
	require.NoError(t, syscall.Kill(decisionGate.HeldPID(), syscall.SIGKILL))
	firstResult := first.Wait()
	require.NotEqual(t, 0, firstResult.Code)
	base := filepath.Join(rig.repo.Root, ".git", "dispat", "return-runs")
	_, err := os.Stat(filepath.Join(base, "active-run"))
	require.NoError(t, err)
	second := rig.repo.CommandEnv(rig.env("HOME="+firstHome,
		"XDG_CONFIG_HOME="+filepath.Join(firstHome, "config")),
		"run", "tests", "--since", "HEAD", "--return-endpoint", rig.returnURL)
	require.Equal(t, 0, second.Code, "recovery stdout:\n%s\nrecovery stderr:\n%s", second.Stdout, second.Stderr)
	peerResult := peer.stop(t)
	peerStopped = true
	content, err := os.ReadFile(rig.builds)
	require.NoError(t, err, "first stdout:\n%s\nfirst stderr:\n%s\nrecovery stdout:\n%s\nrecovery stderr:\n%s\npeer stdout:\n%s\npeer stderr:\n%s\nbranches: %v",
		firstResult.Stdout, firstResult.Stderr, second.Stdout, second.Stderr,
		peerResult.Stdout, peerResult.Stderr, rig.branches())
	require.Equal(t, 1, strings.Count(string(content), "result-start core\n"))
	requireChoreographyTaskChain(t, rig.mailbox, rig.branches(),
		"choreography-claim", "choreography-result", "choreography-decision")
	_, err = os.Stat(filepath.Join(base, "active-run"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Zero(t, peerGit.Matches())
}

// A live claimed frame exceeding its signed task deadline is cancelled by
// the first node. The peer stops the process and returns Ack before the
// canonical decision; the command is not silently offered again.
func TestChoreographyCompiledSweepTaskTimeoutAcknowledgesCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	const script = `printf 'timeout-start %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG";
sleep 60;
printf 'timeout-finished %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG"`
	rig := newExecutionSweepRig(t, script, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 3, Cancel: 5}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
	})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*", Code: 128})
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60,
		append(peerGit.Env(), "HOME="+peerHome,
			"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))...)
	defer func() {
		stopped := peer.stop(t)
		t.Logf("peer stdout:\n%s\npeer stderr:\n%s", stopped.Stdout, stopped.Stderr)
	}()
	res := rig.sweep([]string{"HOME=" + firstHome,
		"XDG_CONFIG_HOME=" + filepath.Join(firstHome, "config")})
	require.NotEqual(t, 0, res.Code, "the timed-out command unexpectedly succeeded: %s", res.Stdout)
	content, err := os.ReadFile(rig.builds)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(content), "timeout-start core\n"))
	require.NotContains(t, string(content), "timeout-finished core\n")
	requireChoreographyTaskChain(t, rig.mailbox, rig.branches(),
		"choreography-claim", "cancel", "ack", "choreography-decision")
	require.Zero(t, peerGit.Matches())
	_, err = os.Stat(filepath.Join(rig.repo.Root, ".git", "dispat", "return-runs", "active-run"))
	require.ErrorIs(t, err, os.ErrNotExist, "a clean Ack must permit signed seal and locator retirement")
}

// A user interrupt cannot discard an accepted Claim. The first node uses a
// bounded settlement context to sign Cancel and wait for the peer's Ack.
func TestChoreographyCompiledSweepInterruptSettlesAcceptedClaim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	const script = `printf 'interrupt-start %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG";
sleep 60;
printf 'interrupt-finished %s\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG"`
	rig := newExecutionSweepRig(t, script, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 120, Cancel: 5}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
	})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*", Code: 128})
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60,
		append(peerGit.Env(), "HOME="+peerHome,
			"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))...)
	defer peer.stop(t)
	first := rig.repo.StartReleaseEnv(rig.env("HOME="+firstHome,
		"XDG_CONFIG_HOME="+filepath.Join(firstHome, "config")),
		"run", "tests", "--since", "all", "--return-endpoint", rig.returnURL)
	started := false
	for until := time.After(25 * time.Second); !started; {
		content, err := os.ReadFile(rig.builds)
		started = err == nil && strings.Contains(string(content), "interrupt-start core\n")
		if started {
			break
		}
		select {
		case <-until:
			result := first.Wait()
			t.Fatalf("peer command did not start before interrupt; stdout:\n%s\nstderr:\n%s",
				result.Stdout, result.Stderr)
		case <-time.After(50 * time.Millisecond):
		}
	}
	first.Signal(os.Interrupt)
	res := first.Wait()
	require.NotEqual(t, 0, res.Code, "interrupted sweep unexpectedly succeeded: %s", res.Stdout)
	content, err := os.ReadFile(rig.builds)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(content), "interrupt-start core\n"))
	require.NotContains(t, string(content), "interrupt-finished core\n")
	requireChoreographyTaskChain(t, rig.mailbox, rig.branches(),
		"choreography-claim", "cancel", "ack", "choreography-decision")
	require.Zero(t, peerGit.Matches())
	_, err = os.Stat(filepath.Join(rig.repo.Root, ".git", "dispat", "return-runs", "active-run"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// Inspect the retained canonical task ref, rather than inferring an Ack from
// the command stopping or from the absence of an active-run locator.
func requireChoreographyTaskChain(t *testing.T, mailbox string, branches []string, kinds ...string) {
	t.Helper()
	for _, branch := range branches {
		if !strings.Contains(branch, "/dispat-task-") {
			continue
		}
		log := strings.Split(strings.TrimSpace(bareGit(t, mailbox, "log", "--first-parent",
			"--reverse", "--format=%s", branch)), "\n")
		next := 0
		for _, subject := range log {
			if next < len(kinds) && subject == "dispat transport "+kinds[next] {
				next++
			}
		}
		if next == len(kinds) {
			return
		}
	}
	t.Fatalf("no retained task branch has ordered signed commits %v: %v", kinds, branches)
}

// The linked peer answers preflight through HTTPS but has no task placement.
// Each ordinary command still claims and returns through the signed broker
// on the first node, and the peer's Git process must never push a ref.
func TestChoreographyCompiledSweepRunsFirstLocalThroughSignedBroker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionSweepRig(t, executionSweepScript, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyOrchestrator,
			Publish: models.RunOnlyOrchestrator}
	})
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*", Code: 128})
	firstHome := t.TempDir()
	peerHome := t.TempDir()
	peerEnv := append(peerGit.Env(), "HOME="+peerHome,
		"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60, peerEnv...)
	defer func() {
		stopped := peer.stop(t)
		t.Logf("peer stdout:\n%s\npeer stderr:\n%s\npeer Git push attempts: %d",
			stopped.Stdout, stopped.Stderr, peerGit.Matches())
	}()

	res := rig.sweep([]string{"HOME=" + firstHome,
		"XDG_CONFIG_HOME=" + filepath.Join(firstHome, "config")})
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	firstNode := ""
	for _, event := range executionEvents(res) {
		if event.Str("message") == "plan fixed" && event.Str("role") == "orchestrator" {
			firstNode = event.Str("node")
			break
		}
	}
	require.NotEmpty(t, firstNode, "the run did not report its first-node identity")
	placed := rig.nodesByPackage()
	for _, name := range executionSweepPackages {
		require.Equal(t, firstNode, placed[name],
			"ordinary task %s did not use the first-node broker: %v", name, rig.runs())
		outcome := executionTaskOutcomes(res)[name]
		require.Equal(t, firstNode, outcome.Str("node"), "the accepted task result names the first node")
		require.Equal(t, true, outcome["here"], "the task was assigned to the first node")
	}
	values := executionProbeValues(rig, "export")
	require.Equal(t, "from-core", values["api"])
	require.Equal(t, "from-core", values["web"])
	require.Zero(t, peerGit.Matches(), "the peer attempted an engine Git push")
	require.False(t, remoteHoldsLock(t, rig.origin), "a sweep must not create a release lock")
	require.Empty(t, rig.repo.TagList(), "a sweep must not publish release tags")
	require.NotEmpty(t, rig.branches(), "the sealed canonical feed remains readable after the return listener closes")
}

// A crash after the signed seal but before retiring active-run must not
// strand a clean sweep or authorize a new generation from an ambient tip.
// The next CLI run replays the retained bootstrap, graph and ledger first.
func TestChoreographyCompiledSweepReconcilesSealedPriorGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionSweepRig(t, executionSweepScript, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyOrchestrator,
			Publish: models.RunOnlyOrchestrator}
	})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 90,
		"HOME="+peerHome, "XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))
	defer peer.stop(t)
	env := []string{"HOME=" + firstHome, "XDG_CONFIG_HOME=" + filepath.Join(firstHome, "config")}
	first := rig.sweep(env)
	require.Equal(t, 0, first.Code, "first stdout:\n%s\nfirst stderr:\n%s", first.Stdout, first.Stderr)
	base := filepath.Join(rig.repo.Root, ".git", "dispat", "return-runs")
	entries, err := os.ReadDir(base)
	require.NoError(t, err)
	prior := ""
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) == 32 {
			require.Empty(t, prior, "fixture unexpectedly retained multiple generations")
			prior = entry.Name()
		}
	}
	require.NotEmpty(t, prior)
	_, err = os.Stat(filepath.Join(base, "active-run"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.WriteFile(filepath.Join(base, "active-run"), []byte(prior), 0o600))
	// The empty window still enters through the real CLI startup path and
	// must reconcile the old generation before it may start its own run.
	// It avoids testing a second, unrelated task-finality race here.
	second := rig.repo.CommandEnv(rig.env(env...), "run", "tests", "--since", "HEAD",
		"--return-endpoint", rig.returnURL)
	require.Equal(t, 0, second.Code, "second stdout:\n%s\nsecond stderr:\n%s", second.Stdout, second.Stderr)
	_, err = os.Stat(filepath.Join(base, "active-run"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.False(t, remoteHoldsLock(t, rig.origin), "sweep recovery created a release lock")
}

// Reconciliation also has to leave this checkout able to run real work in a
// new generation. This exercises the local finality pump again, rather than
// only the empty-window startup boundary above.
func TestChoreographyCompiledSweepRunsAgainAfterSealedReconciliation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	rig := newExecutionSweepRig(t, executionSweepScript, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyOrchestrator,
			Publish: models.RunOnlyOrchestrator}
	})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 90,
		"HOME="+peerHome, "XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))
	defer peer.stop(t)
	env := []string{"HOME=" + firstHome, "XDG_CONFIG_HOME=" + filepath.Join(firstHome, "config")}
	first := rig.sweep(env)
	require.Equal(t, 0, first.Code, "first stdout:\n%s\nfirst stderr:\n%s", first.Stdout, first.Stderr)
	base := filepath.Join(rig.repo.Root, ".git", "dispat", "return-runs")
	entries, err := os.ReadDir(base)
	require.NoError(t, err)
	prior := ""
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) == 32 {
			require.Empty(t, prior)
			prior = entry.Name()
		}
	}
	require.NotEmpty(t, prior)
	require.NoError(t, os.WriteFile(filepath.Join(base, "active-run"), []byte(prior), 0o600))
	second := rig.sweep(env)
	require.Equal(t, 0, second.Code, "second stdout:\n%s\nsecond stderr:\n%s", second.Stdout, second.Stderr)
	_, err = os.Stat(filepath.Join(base, "active-run"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// This task runs on a peer with read-only Git transport. The provider's
// declared file path must be carried as a typed export and resolved in each
// consumer checkout; a first-node absolute path is not a portable input.
func TestChoreographyCompiledSweepPeerCarriesDeclaredPathWithoutGitPush(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	const script = executionSweepScript + ` &&
case "$DISPAT_PACKAGE" in
  core) mkdir -p ../../coverage &&
        printf 'from-core\n' > ../../coverage/core.asset &&
        printf 'CORE_ASSET=%s\n' "$(cd ../.. && pwd)/coverage/core.asset" >> "$DISPAT_OUTPUT" ;;
  *) printf 'probe-private %s %s %s\n' "$DISPAT_PACKAGE" \
       "${DISPAT_OUTPUT_CORE_ASSET:-empty}" "$(cd ../.. && pwd)" >> "$DISPAT_IT_EXECUTION_LOG" &&
     test "$(cd "$(dirname "$DISPAT_OUTPUT_CORE_ASSET")" && pwd -P)/$(basename "$DISPAT_OUTPUT_CORE_ASSET")" = \
       "$(cd ../.. && pwd -P)/coverage/core.asset" &&
     test "$(cat "$DISPAT_OUTPUT_CORE_ASSET")" = from-core &&
     printf 'probe-path %s from-core\n' "$DISPAT_PACKAGE" >> "$DISPAT_IT_EXECUTION_LOG" ;;
esac`
	rig := newExecutionSweepRig(t, script, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
		cfg.RunOutputs = map[string][]string{"tests": {"coverage"}}
	})
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*", Code: 128})
	firstHome := t.TempDir()
	peerHome := t.TempDir()
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60,
		append(peerGit.Env(), "HOME="+peerHome,
			"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))...)
	defer func() {
		stopped := peer.stop(t)
		t.Logf("peer stdout:\n%s\npeer stderr:\n%s\npeer Git push attempts: %d",
			stopped.Stdout, stopped.Stderr, peerGit.Matches())
	}()

	res := rig.sweep([]string{"HOME=" + firstHome,
		"XDG_CONFIG_HOME=" + filepath.Join(firstHome, "config")})
	if res.Code != 0 {
		t.Logf("recorded peer frames and path probes: %v", rig.runs())
	}
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for _, name := range executionSweepPackages {
		require.Equal(t, executionNode, rig.nodesByPackage()[name],
			"task %s did not execute on the read-only peer: %v", name, rig.runs())
	}
	values := executionProbeValues(rig, "path")
	require.Equal(t, "from-core", values["api"])
	require.Equal(t, "from-core", values["web"])
	require.Zero(t, peerGit.Matches(), "the peer attempted an engine Git push")
	require.False(t, remoteHoldsLock(t, rig.origin), "a sweep must not create a release lock")
	require.Empty(t, rig.repo.TagList(), "a sweep must not publish release tags")
}

// A failed provider can still leave a verified partial file and scalar
// export. Continue releases its consumers only after the failed Result is
// terminal, and each consumer resolves the file in its own private checkout.
func TestChoreographyCompiledSweepContinueCarriesFailedProviderFileAndScalar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	const script = `printf '%s %s %s\n' "$DISPAT_EXECUTION_NODE" "$DISPAT_PACKAGE" "$DISPAT_STAGE" >> "$DISPAT_IT_EXECUTION_LOG";
case "$DISPAT_PACKAGE" in
  core) mkdir -p ../../coverage &&
        printf 'partial-file\n' > ../../coverage/core.asset &&
        printf 'CORE_ASSET=%s\n' "$(cd ../.. && pwd)/coverage/core.asset" >> "$DISPAT_OUTPUT" &&
        printf 'CORE_SCALAR=partial-scalar\n' >> "$DISPAT_OUTPUT" &&
        exit 7 ;;
  *) test "$DISPAT_OUTPUT_CORE_SCALAR" = partial-scalar &&
     test "$(cat "$DISPAT_OUTPUT_CORE_ASSET")" = partial-file &&
     printf 'probe-partial %s %s\n' "$DISPAT_PACKAGE" "$DISPAT_OUTPUT_CORE_SCALAR" >> "$DISPAT_IT_EXECUTION_LOG" ;;
esac`
	rig := newExecutionSweepRig(t, script, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10, Task: 30}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker,
			Publish: models.RunOnlyOrchestrator}
		cfg.RunOutputs = map[string][]string{"tests": {"coverage"}}
	})
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*", Code: 128})
	firstHome, peerHome := t.TempDir(), t.TempDir()
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox), 60,
		append(peerGit.Env(), "HOME="+peerHome,
			"XDG_CONFIG_HOME="+filepath.Join(peerHome, "config"))...)
	defer peer.stop(t)
	res := rig.sweep([]string{"HOME=" + firstHome,
		"XDG_CONFIG_HOME=" + filepath.Join(firstHome, "config")},
		"--on-error", "continue")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	content, err := os.ReadFile(rig.builds)
	require.NoError(t, err)
	for _, name := range []string{"api", "web"} {
		require.Contains(t, string(content), "probe-partial "+name+" partial-scalar",
			"consumer did not receive both partial exports: %s\nstdout:\n%s\nstderr:\n%s",
			content, res.Stdout, res.Stderr)
	}
	require.Zero(t, peerGit.Matches(), "the peer attempted an engine Git push")
	require.False(t, remoteHoldsLock(t, rig.origin))
	require.Empty(t, rig.repo.TagList())
}
