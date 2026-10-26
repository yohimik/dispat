// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

const lateJoinAdmissionTimeoutSeconds = 60
const lateJoinFirstTaskStartWaitSeconds = 30
const lateJoinFirstTaskPollMilliseconds = 25
const lateJoinPeerCompleteWaitSeconds = 45
const lateJoinLatchPollMilliseconds = 50
const lateJoinLatchIterations = 1200
const lateJoinExpirySeconds = 2
const lateJoinExpiryObserveSeconds = 20
const lateJoinLedgerScanLimit = 128
const admittedClaimDeadlineSlack = 150 * time.Millisecond
const admittedClaimTimeoutSeconds = 5
const executionLateJoinIdentitySeedEnv = "DISPAT_IT_JOIN_IDENTITY_SEED"

func executionWaitLateJoinMarker(path string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	poll := time.NewTicker(lateJoinLatchPollMilliseconds * time.Millisecond)
	defer poll.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-poll.C:
		}
	}
}

func TestExecutionSignedStartWaitsForGlobalBeforeAll(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	markers := t.TempDir()
	hookStarted := filepath.Join(markers, "before-all-started")
	gate := filepath.Join(markers, "before-all-continue")
	taskEffect := filepath.Join(markers, "task-effect")
	defer func() { _ = os.WriteFile(gate, nil, 0o600) }()
	rig := newExecutionPlacementRig(t, []string{"alpha"},
		func(*harness.Repo) string {
			return executionRecordingScript + " && " + fmt.Sprintf("printf task > %q", taskEffect)
		},
		func(cfg *models.File) {
			cfg.Run = &models.RunConfig{BeforeAll: []string{"gate"}}
			cfg.Scripts["gate"] = models.Script{fmt.Sprintf(
				`printf started > %q; i=0; while [ ! -f %q ] && [ "$i" -lt %d ]; do i=$((i + 1)); sleep .05; done; test -f %q`,
				hookStarted, gate, lateJoinLatchIterations, gate)}
		},
		pinPackages(map[string]*models.RunOnly{
			"alpha": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
		}))
	workers := rig.startWorkers([]string{executionNode}, 1)
	defer func() {
		for _, worker := range workers {
			worker.stop(t)
		}
	}()
	first := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	require.Eventually(t, func() bool {
		_, err := os.Stat(hookStarted)
		return err == nil
	}, lateJoinFirstTaskStartWaitSeconds*time.Second, lateJoinFirstTaskPollMilliseconds*time.Millisecond)
	require.Eventually(t, func() bool {
		return len(executionProtocolRefs(t, rig.mailbox, "dispat-task-")) > 0
	}, lateJoinFirstTaskStartWaitSeconds*time.Second, lateJoinFirstTaskPollMilliseconds*time.Millisecond,
		"the complete task graph is initialized before global hooks finish")
	for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
		require.NotEqual(t, "execution-started", event.Kind,
			"the global hook still holds the execution-start fence")
		require.NotEqual(t, "claim-accepted", event.Kind,
			"no worker can claim before the global gate succeeds")
	}
	require.NoFileExists(t, taskEffect)
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	result := first.Wait()
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	require.FileExists(t, taskEffect)
	require.Equal(t, executionNode, rig.nodesByPackage()["alpha"])
}

func TestExecutionFailedGlobalBeforeAllAdmitsNoCompiledTaskEffect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	marker := filepath.Join(t.TempDir(), "task-effect")
	rig := newExecutionPlacementRig(t, []string{"alpha"},
		func(*harness.Repo) string { return fmt.Sprintf("printf task > %q", marker) },
		func(cfg *models.File) {
			cfg.Run = &models.RunConfig{BeforeAll: []string{"gate"}}
			cfg.Scripts["gate"] = models.Script{"exit 7"}
		},
		pinPackages(map[string]*models.RunOnly{
			"alpha": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
		}))
	workers := rig.startWorkers([]string{executionNode}, 1)
	defer func() {
		for _, worker := range workers {
			worker.stop(t)
		}
	}()
	result := rig.release()
	require.NotEqual(t, 0, result.Code)
	require.NoFileExists(t, marker)
	require.Empty(t, rig.repo.TagList())
}

func executionLateJoinIdentity(t *testing.T) (publicKey, seed string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(public),
		base64.RawURLEncoding.EncodeToString(private.Seed())
}

type executionLateJoinLedgerEvent struct {
	Kind                 string `json:"kind"`
	Task                 string `json:"task"`
	Node                 string `json:"node"`
	RefusalReason        string `json:"refusalReason"`
	PreparedInputOID     string `json:"preparedInputOid"`
	Package              string `json:"package"`
	PackageOutcome       string `json:"packageOutcome"`
	TaskOID              string `json:"taskOid"`
	PriorTaskOID         string `json:"priorTaskOid"`
	AdmissionDeadlineUTC string `json:"admissionDeadlineUtc"`
}

func TestExecutionAcceptedClaimSurvivesLateAdmissionDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	markers := t.TempDir()
	started := filepath.Join(markers, "claimed-command-started")
	gate := filepath.Join(markers, "claimed-command-continue")
	completed := filepath.Join(markers, "claimed-command-completed")
	defer func() { _ = os.WriteFile(gate, nil, 0o600) }()
	rig := newExecutionPlacementRig(t, []string{"alpha"},
		func(*harness.Repo) string {
			return executionRecordingScript + " && " + fmt.Sprintf(
				`printf started > %q; i=0; while [ ! -f %q ] && [ "$i" -lt %d ]; do i=$((i + 1)); sleep .05; done; test -f %q && printf done > %q`,
				started, gate, lateJoinLatchIterations, gate, completed)
		},
		func(cfg *models.File) {
			cfg.Execution.Timeouts.Admission = admittedClaimTimeoutSeconds
		},
		pinPackages(map[string]*models.RunOnly{
			"alpha": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
		}))
	workers := rig.startWorkers([]string{executionNode}, 1)
	defer func() {
		for _, worker := range workers {
			worker.stop(t)
		}
	}()
	first := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	require.Eventually(t, func() bool {
		_, err := os.Stat(started)
		return err == nil
	}, lateJoinFirstTaskStartWaitSeconds*time.Second, lateJoinFirstTaskPollMilliseconds*time.Millisecond,
		"the eager peer must claim and start the command before the admission deadline")
	var deadline time.Time
	for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
		if event.Kind == "execution-started" {
			var err error
			deadline, err = time.Parse(time.RFC3339Nano, event.AdmissionDeadlineUTC)
			require.NoError(t, err)
		}
	}
	require.False(t, deadline.IsZero(), "signed start fixes the admission deadline")
	require.True(t, time.Now().Before(deadline), "the command was accepted before admission closed")
	if wait := time.Until(deadline) + admittedClaimDeadlineSlack; wait > 0 {
		time.Sleep(wait)
	}
	require.NoFileExists(t, completed, "the accepted command remains active after late admission closes")
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	result := first.Wait()
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	require.FileExists(t, completed)
}

func TestExecutionBusyAdmittedWorkerQueueSurvivesAdmissionDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	markers := t.TempDir()
	started := filepath.Join(markers, "first-worker-command-started")
	gate := filepath.Join(markers, "first-worker-command-continue")
	secondDone := filepath.Join(markers, "second-worker-command-done")
	defer func() { _ = os.WriteFile(gate, nil, 0o600) }()
	rig := newExecutionPlacementRig(t, []string{"alpha", "beta"},
		func(*harness.Repo) string {
			return executionRecordingScript + ` && case "$DISPAT_PACKAGE" in
			alpha) ` + fmt.Sprintf(`printf started > %q; i=0; while [ ! -f %q ] && [ "$i" -lt %d ]; do
				i=$((i + 1)); sleep .05; done; test -f %q`,
				started, gate, lateJoinLatchIterations, gate) + ` ;;
			beta) ` + fmt.Sprintf(`printf done > %q`, secondDone) + ` ;;
			esac`
		},
		func(cfg *models.File) {
			cfg.Concurrency = []int{2, 1}
			cfg.Execution.Concurrency = models.Int(1)
			cfg.Execution.Timeouts.Admission = admittedClaimTimeoutSeconds
		},
		pinPackages(map[string]*models.RunOnly{
			"alpha": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
			"beta":  placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
		}))
	workers := rig.startWorkers([]string{executionNode}, 1)
	defer func() {
		for _, worker := range workers {
			worker.stop(t)
		}
	}()
	first := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	require.Eventually(t, func() bool {
		_, err := os.Stat(started)
		return err == nil
	}, lateJoinFirstTaskStartWaitSeconds*time.Second, lateJoinFirstTaskPollMilliseconds*time.Millisecond,
		"the first worker task must hold the only admitted node slot")
	var deadline time.Time
	for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
		if event.Kind == "execution-started" {
			var err error
			deadline, err = time.Parse(time.RFC3339Nano, event.AdmissionDeadlineUTC)
			require.NoError(t, err)
		}
	}
	require.False(t, deadline.IsZero(), "signed start fixes the admission deadline")
	require.True(t, time.Now().Before(deadline), "the first task began before admission closed")
	if wait := time.Until(deadline) + admittedClaimDeadlineSlack; wait > 0 {
		time.Sleep(wait)
	}
	require.NoFileExists(t, secondDone, "the second task still waits for the busy admitted worker")
	for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
		require.False(t, event.Kind == "no-claim-expired" && event.Package == "beta",
			"a busy admitted worker is eligible capacity after the late admission deadline")
	}
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	result := first.Wait()
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	require.FileExists(t, secondDone, "the queued worker task claims after the first task settles")
}

func executionLateJoinLedgerEvents(t *testing.T, mailbox string) []executionLateJoinLedgerEvent {
	t.Helper()
	refs := executionProtocolRefs(t, mailbox, "dispat-ledger-")
	require.Len(t, refs, 1, "one signed ledger exists for the compiled run")
	oid := strings.TrimSpace(bareGit(t, mailbox, "rev-parse", "refs/heads/"+refs[0]))
	events := make([]executionLateJoinLedgerEvent, 0)
	for step := 0; step < lateJoinLedgerScanLimit; step++ {
		document := bareGit(t, mailbox, "show", oid+":dispat/choreography-ledger.json")
		signature := strings.TrimSpace(bareGit(t, mailbox, "show", oid+":dispat/choreography-ledger.sig"))
		require.Equal(t, executionSign("choreography-ledger", executionSecret, []byte(document)), signature,
			"each observed ledger event has the run's exact signature")
		var record struct {
			Event *executionLateJoinLedgerEvent `json:"event"`
		}
		require.NoError(t, json.Unmarshal([]byte(document), &record))
		if record.Event == nil {
			slices.Reverse(events)
			return events
		}
		events = append(events, *record.Event)
		parents := strings.Fields(bareGit(t, mailbox, "rev-list", "--parents", "-n", "1", oid))
		require.GreaterOrEqual(t, len(parents), 2, "a signed event links to its prior ledger state")
		oid = parents[1]
	}
	t.Fatal("the signed ledger exceeded the bounded integration scan")
	return nil
}

func executionAssertLateJoinSlotNotice(t *testing.T, mailbox, node string) {
	t.Helper()
	refs := executionProtocolRefs(t, mailbox, "dispat-slot-")
	require.NotEmpty(t, refs, "an authorized late node has a fixed signed notice ref")
	for _, ref := range refs {
		document := bareGit(t, mailbox, "show", "refs/heads/"+ref+":dispat/slot.json")
		var notice struct {
			Node         string `json:"node"`
			GraphRootOID string `json:"graphRootOid"`
		}
		require.NoError(t, json.Unmarshal([]byte(document), &notice))
		if notice.Node != node {
			continue
		}
		signature := strings.TrimSpace(bareGit(t, mailbox, "show", "refs/heads/"+ref+":dispat/slot.sig"))
		require.Equal(t, executionSign("slot", executionSecret, []byte(document)), signature)
		require.NotEmpty(t, notice.GraphRootOID, "the late worker sees the activated complete graph")
		return
	}
	t.Fatalf("no signed late participation notice exists for %q among %v", node, refs)
}

func executionLateJoinBuild(gate, peerDone, firstStarted, firstDone string) string {
	return fmt.Sprintf(`case "$DISPAT_PACKAGE" in alpha) printf started > %q ;; esac; `,
		firstStarted) + executionRecordingScript + ` && case "$DISPAT_PACKAGE" in
		alpha) ` + fmt.Sprintf(`i=0; while [ ! -f %q ] && [ "$i" -lt %d ]; do
			i=$((i + 1)); sleep .05; done; test -f %q && printf done > %q`,
		gate, lateJoinLatchIterations, gate, firstDone) + ` ;;
		beta) ` + fmt.Sprintf(`printf done > %q`, peerDone) + ` ;;
		esac`
}

// A late worker is authorized in the signed bootstrap but absent from the
// initial roster. It becomes usable after an unrelated first-node build has
// already started, without changing the fixed task graph or giving it Git
// write authority.
func TestExecutionLateWorkerJoinsActiveCompiledRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	markers := t.TempDir()
	gate := filepath.Join(markers, "release-first-continue")
	peerDone := filepath.Join(markers, "release-peer-done")
	firstDone := filepath.Join(markers, "release-first-done")
	firstStarted := filepath.Join(markers, "release-first-started")
	publicKey, seed := executionLateJoinIdentity(t)
	defer func() { _ = os.WriteFile(gate, nil, 0o600) }()
	rig := newExecutionPlacementRig(t, []string{"alpha", "beta"},
		func(*harness.Repo) string { return executionLateJoinBuild(gate, peerDone, firstStarted, firstDone) },
		func(cfg *models.File) {
			cfg.Execution.Concurrency = models.Int(1)
			cfg.Concurrency = []int{2, 1}
			cfg.Execution.Workers[0].LateJoin = true
			cfg.Execution.Workers[0].IdentityPublicKey = publicKey
			cfg.Execution.Timeouts.Admission = lateJoinAdmissionTimeoutSeconds
		},
		pinPackages(map[string]*models.RunOnly{
			"alpha": placedOn(models.RunOnlyOrchestrator, models.RunOnlyOrchestrator),
			"beta":  placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
		}))
	first := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	started := false
	for deadline := time.After(lateJoinFirstTaskStartWaitSeconds * time.Second); !started; {
		_, markerErr := os.Stat(firstStarted)
		started = markerErr == nil
		if started {
			break
		}
		select {
		case <-deadline:
			first.Signal(os.Interrupt)
			res := first.Wait()
			t.Fatalf("first-node task never started before admission; stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		case <-time.After(lateJoinFirstTaskPollMilliseconds * time.Millisecond):
		}
	}
	before := executionProtocolRefs(t, rig.mailbox, "dispat-task-")
	require.GreaterOrEqual(t, len(before), 2, "finite first-only and worker-only task refs exist before late admission")
	require.Len(t, executionProtocolRefs(t, rig.mailbox, "dispat-graph-"), 1,
		"the signed complete graph is public before late admission")
	executionAssertLateJoinSlotNotice(t, rig.mailbox, executionNode)
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*", Code: 128})
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox, func(cfg *models.ExecutionConfig) {
		cfg.IdentityKeyEnv = executionLateJoinIdentitySeedEnv
	}), 0, append(peerGit.Env(), executionLateJoinIdentitySeedEnv+"="+seed)...)
	peerStopped := false
	defer func() {
		if !peerStopped {
			peer.stop(t)
		}
	}()
	if !executionWaitLateJoinMarker(peerDone, lateJoinPeerCompleteWaitSeconds*time.Second) {
		t.Logf("signed release ledger before interruption: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
		peerResult := peer.stop(t)
		peerStopped = true
		_ = os.WriteFile(gate, nil, 0o600)
		first.Signal(os.Interrupt)
		firstResult := first.Wait()
		t.Fatalf("late peer did not complete beta while alpha held its slot; first stdout:\n%s\nfirst stderr:\n%s\nworker stdout:\n%s\nworker stderr:\n%s",
			firstResult.Stdout, firstResult.Stderr, peerResult.Stdout, peerResult.Stderr)
	}
	require.NoFileExists(t, firstDone, "the first-node task remains blocked when beta completes")
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	res := first.Wait()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.Equal(t, executionNode, rig.nodesByPackage()["beta"], "the late worker took the existing beta task")
	require.FileExists(t, firstDone, "the first-node task released only after the peer completed")
	require.Zero(t, peerGit.Matches(), "the late worker returns claims and results without Git pushes")
	require.ElementsMatch(t, before, executionProtocolRefs(t, rig.mailbox, "dispat-task-"),
		"admission does not add task branches; sealed refs remain for recovery")
}

func TestExecutionPendingWorkerClaimDoesNotConsumeFirstOnlyBuildBudget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	markers := t.TempDir()
	gate := filepath.Join(markers, "first-only-continue")
	firstStarted := filepath.Join(markers, "first-only-started")
	firstDone := filepath.Join(markers, "first-only-done")
	peerDone := filepath.Join(markers, "worker-only-done")
	publicKey, seed := executionLateJoinIdentity(t)
	defer func() { _ = os.WriteFile(gate, nil, 0o600) }()
	rig := newExecutionPlacementRig(t, []string{"beta", "alpha"},
		func(*harness.Repo) string {
			return executionLateJoinBuild(gate, peerDone, firstStarted, firstDone)
		},
		func(cfg *models.File) {
			cfg.Concurrency = []int{1, 1}
			cfg.Execution.Concurrency = models.Int(1)
			cfg.Execution.Timeouts.Admission = lateJoinAdmissionTimeoutSeconds
			cfg.Execution.Workers[0].LateJoin = true
			cfg.Execution.Workers[0].IdentityPublicKey = publicKey
		},
		pinPackages(map[string]*models.RunOnly{
			"alpha": placedOn(models.RunOnlyOrchestrator, models.RunOnlyOrchestrator),
			"beta":  placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
		}))
	first := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	require.Eventually(t, func() bool {
		_, err := os.Stat(firstStarted)
		return err == nil
	}, lateJoinFirstTaskStartWaitSeconds*time.Second, lateJoinFirstTaskPollMilliseconds*time.Millisecond,
		"worker-only pending work cannot hold the sole build budget ahead of first-only work")
	require.NoFileExists(t, peerDone)
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox, func(cfg *models.ExecutionConfig) {
		cfg.IdentityKeyEnv = executionLateJoinIdentitySeedEnv
	}), 0, executionLateJoinIdentitySeedEnv+"="+seed)
	defer peer.stop(t)
	require.Eventually(t, func() bool {
		for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
			if event.Kind == "member-joined" {
				return true
			}
		}
		return false
	}, lateJoinPeerCompleteWaitSeconds*time.Second, lateJoinLatchPollMilliseconds*time.Millisecond,
		"the later worker joins while the first-only build holds the sole build budget")
	require.NoFileExists(t, peerDone, "the worker-only build waits for the sole build budget")
	require.NoFileExists(t, firstDone)
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	require.Eventually(t, func() bool {
		_, err := os.Stat(peerDone)
		return err == nil
	}, lateJoinPeerCompleteWaitSeconds*time.Second, lateJoinLatchPollMilliseconds*time.Millisecond,
		"the worker-only build runs after the first-only build releases the sole budget")
	result := first.Wait()
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
}

// A sweep uses the same compiled task graph and authenticated late admission,
// while retaining its own no-record and no-release-lock authority.
func TestExecutionLateWorkerJoinsActiveCompiledSweep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	markers := t.TempDir()
	gate := filepath.Join(markers, "sweep-first-continue")
	peerDone := filepath.Join(markers, "sweep-peer-done")
	firstDone := filepath.Join(markers, "sweep-first-done")
	firstStarted := filepath.Join(markers, "sweep-first-started")
	publicKey, seed := executionLateJoinIdentity(t)
	defer func() { _ = os.WriteFile(gate, nil, 0o600) }()
	var sweepScript string
	rig := newExecutionPlacementRig(t, []string{"alpha", "beta"},
		func(*harness.Repo) string {
			sweepScript = executionLateJoinBuild(gate, peerDone, firstStarted, firstDone)
			return echoBuild
		},
		func(cfg *models.File) {
			cfg.Scripts["tests"] = models.Script{sweepScript}
			cfg.Execution.Concurrency = models.Int(1)
			cfg.Concurrency = []int{2, 1}
			cfg.Execution.Workers[0].LateJoin = true
			cfg.Execution.Workers[0].IdentityPublicKey = publicKey
			cfg.Execution.Timeouts.Admission = lateJoinAdmissionTimeoutSeconds
		},
		pinPackages(map[string]*models.RunOnly{
			"alpha": placedOn(models.RunOnlyOrchestrator, models.RunOnlyOrchestrator),
			"beta":  placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
		}))
	first := rig.repo.StartCommandEnv(rig.env(), "run", "tests", "--since", "all",
		"--return-endpoint", rig.returnURL)
	started := false
	for deadline := time.After(lateJoinFirstTaskStartWaitSeconds * time.Second); !started; {
		_, markerErr := os.Stat(firstStarted)
		started = markerErr == nil
		if started {
			break
		}
		select {
		case <-deadline:
			if refs := executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"); len(refs) == 1 {
				t.Logf("signed sweep ledger before interruption: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
			} else {
				t.Logf("signed sweep ledger refs before interruption: %v", refs)
			}
			first.Signal(os.Interrupt)
			res := first.Wait()
			t.Fatalf("first-node sweep task never started before admission; stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		case <-time.After(lateJoinFirstTaskPollMilliseconds * time.Millisecond):
		}
	}
	before := executionProtocolRefs(t, rig.mailbox, "dispat-task-")
	require.GreaterOrEqual(t, len(before), 2, "the sweep published first-only and worker-only task refs before late admission")
	require.Len(t, executionProtocolRefs(t, rig.mailbox, "dispat-graph-"), 1,
		"the sweep signed its complete graph before late admission")
	executionAssertLateJoinSlotNotice(t, rig.mailbox, executionNode)
	peerGit := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*", Code: 128})
	peer := rig.startWorker(executionWorkerConfig(rig.mailbox, func(cfg *models.ExecutionConfig) {
		cfg.IdentityKeyEnv = executionLateJoinIdentitySeedEnv
	}), 0, append(peerGit.Env(), executionLateJoinIdentitySeedEnv+"="+seed)...)
	peerStopped := false
	defer func() {
		if !peerStopped {
			peer.stop(t)
		}
	}()
	if !executionWaitLateJoinMarker(peerDone, lateJoinPeerCompleteWaitSeconds*time.Second) {
		t.Logf("signed sweep ledger before interruption: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
		peerResult := peer.stop(t)
		peerStopped = true
		_ = os.WriteFile(gate, nil, 0o600)
		first.Signal(os.Interrupt)
		firstResult := first.Wait()
		t.Fatalf("late peer did not complete beta while alpha held its sweep slot; first stdout:\n%s\nfirst stderr:\n%s\nworker stdout:\n%s\nworker stderr:\n%s",
			firstResult.Stdout, firstResult.Stderr, peerResult.Stdout, peerResult.Stderr)
	}
	require.NoFileExists(t, firstDone, "the first-node sweep task remains blocked when beta completes")
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	res := first.Wait()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.Equal(t, executionNode, rig.nodesByPackage()["beta"], "the late worker took the existing beta sweep task")
	require.FileExists(t, firstDone, "the first-node sweep task released only after the peer completed")
	require.Zero(t, peerGit.Matches(), "the late worker returned sweep claims and results without Git pushes")
	require.ElementsMatch(t, before, executionProtocolRefs(t, rig.mailbox, "dispat-task-"),
		"late admission does not add sweep task branches")
	require.Empty(t, rig.repo.TagList(), "a sweep writes no release tags")
}

// The signed admission deadline closes a worker-only task that nobody can
// claim. The first-only sibling remains active until the test releases it.
func TestExecutionLateWorkerDeadlineSignsNoClaimWithoutResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distributed durable node-slot backend is not available on Windows")
	}
	markers := t.TempDir()
	gate := filepath.Join(markers, "expiry-first-continue")
	firstDone := filepath.Join(markers, "expiry-first-done")
	firstStarted := filepath.Join(markers, "expiry-first-started")
	publicKey, _ := executionLateJoinIdentity(t)
	defer func() { _ = os.WriteFile(gate, nil, 0o600) }()
	rig := newExecutionPlacementRig(t, []string{"alpha", "beta"},
		func(*harness.Repo) string {
			return executionLateJoinBuild(gate, filepath.Join(markers, "expiry-peer-done"), firstStarted, firstDone)
		},
		func(cfg *models.File) {
			cfg.Execution.Concurrency = models.Int(1)
			cfg.Concurrency = []int{2, 1}
			cfg.Execution.Workers[0].LateJoin = true
			cfg.Execution.Workers[0].IdentityPublicKey = publicKey
			cfg.Execution.Timeouts.Admission = lateJoinExpirySeconds
		},
		pinPackages(map[string]*models.RunOnly{
			"alpha": placedOn(models.RunOnlyOrchestrator, models.RunOnlyOrchestrator),
			"beta":  placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
		}))
	first := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	require.Eventually(t, func() bool {
		_, markerErr := os.Stat(firstStarted)
		return markerErr == nil
	}, lateJoinFirstTaskStartWaitSeconds*time.Second, lateJoinFirstTaskPollMilliseconds*time.Millisecond,
		"unrelated first-node work should start before admission expiry")
	keylessRoot := writeNodeConfig(t, executionWorkerConfig(rig.mailbox, func(cfg *models.ExecutionConfig) {
		cfg.IdentityKeyEnv = executionLateJoinIdentitySeedEnv
	}))
	keyless := runWorker(t, rig, []string{executionSecretEnv + "=" + executionSecret},
		"worker", "--root", keylessRoot, "--state-dir", t.TempDir(), "--idle-timeout", "5")
	require.NotEqual(t, 0, keyless.Code, "the shared message secret cannot authenticate a late join")
	require.Contains(t, diagnosticText(keyless), "identity", "the keyless worker is refused by the late-join path")
	var expired, settled executionLateJoinLedgerEvent
	require.Eventually(t, func() bool {
		seenExpired := false
		for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
			if event.Kind == "no-claim-expired" {
				expired = event
				seenExpired = true
			}
			if event.Kind == "no-claim-package-settled" && seenExpired {
				settled = event
				return true
			}
		}
		return false
	}, lateJoinExpiryObserveSeconds*time.Second, 100*time.Millisecond,
		"the deadline must settle typed no-claim and package failure events while alpha is still running")
	require.Equal(t, "beta", expired.Package)
	require.Empty(t, expired.PackageOutcome, "task no-claim does not preempt package cleanup")
	require.Equal(t, "failed", settled.PackageOutcome)
	require.Equal(t, expired.TaskOID, settled.TaskOID, "package settlement retains the exact typed task tip")
	require.Equal(t, expired.TaskOID, settled.PriorTaskOID, "settlement names its causal no-claim tip")
	require.NotEmpty(t, expired.TaskOID)
	require.NotContains(t, rig.nodesByPackage(), "beta", "a shared-HMAC-only CLI worker cannot claim beta")
	require.NoFileExists(t, firstDone, "unrelated first-node work remains active at no-claim finality")
	blockedTipFound := false
	for _, branch := range executionProtocolRefs(t, rig.mailbox, "dispat-task-") {
		oid := strings.TrimSpace(bareGit(t, rig.mailbox, "rev-parse", "refs/heads/"+branch))
		if oid != expired.TaskOID {
			continue
		}
		blockedTipFound = true
		contents := bareGit(t, rig.mailbox, "ls-tree", "-r", "--name-only", oid)
		require.Contains(t, contents, "dispat/choreography-no-claim-expired.json",
			"the no-claim terminal has its own typed decision")
		require.NotContains(t, contents, "dispat/choreography-result.json",
			"admission expiry cannot fabricate a command result")
	}
	require.True(t, blockedTipFound, "the ledger no-claim event names an exact canonical task tip")
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	res := first.Wait()
	require.NotEqual(t, 0, res.Code, "a worker-only package without an admitted worker fails the release")
	require.FileExists(t, firstDone, "the unrelated first-node task completed")
	require.NotEmpty(t, rig.nodesByPackage()["alpha"], "the first-node command recorded its node")
	require.NotEqual(t, executionNode, rig.nodesByPackage()["alpha"],
		"the unrelated build ran on the first node, not the absent late worker")
	require.NotContains(t, rig.nodesByPackage(), "beta", "no worker command ran")
}
