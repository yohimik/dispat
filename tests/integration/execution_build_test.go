// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: the build stages of a release, executed on other machines.
//
// The fixture is a small JS-shaped monorepo whose build scripts record where
// they ran, in a file outside every checkout, because the folder a node builds
// in is removed the moment the task ends. Everything else is the ordinary
// release: the same plan, the same budgets, the same tags, the same records.
//
// What the scenarios are about is the seam. A build frame goes to a node and
// comes back; the version and syncLock frames do not; the source a node builds
// from is the working tree as the orchestrator had it, admitted version edits
// included; and nothing a coordination branch carries reaches the history the
// release is recorded in.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionWorkspacePackages is the fixture's shape: assets is consumed by ui
// and docs, and app consumes both. It is the smallest workspace in which two
// builds may legitimately overlap and a third must wait for them.
var executionWorkspacePackages = []string{"assets", "ui", "docs", "app"}

// executionIntegrityCode is the diagnostic a task that could not be executed
// carries.
const executionIntegrityCode = "E227"
const executionTaskTimeoutSettlementWait = 90 * time.Second
const executionTaskTimeoutWorkerDiagnosticWait = 5 * time.Second
const executionTaskTimeoutCancelSeconds = 5
const executionTaskTimeoutScript = executionRecordingScript +
	` && if [ "$DISPAT_PACKAGE" = assets ]; then sleep 60; fi`

// newExecutionWorkspace seeds the fixture every delegated scenario is driven
// through and returns its rig.
//
// The build script is a function of the repository because half of these
// scenarios need a probe whose path only exists once the repository does: a
// timing mark, a nested invocation of the binary under test.
func newExecutionWorkspace(t *testing.T, script func(*harness.Repo) string,
	adjust ...func(*models.File)) *executionRig {
	t.Helper()
	repo := harness.New(t)
	mailbox := executionMailbox(t)
	cfg := harness.BaseFile(4, 4)
	cfg.Scripts = map[string]models.Script{
		"build": {script(repo)}, "publish": {"echo publishing"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "ui", Provider: "assets"},
		{Consumer: "docs", Provider: "assets"},
		{Consumer: "app", Provider: "ui"},
		{Consumer: "app", Provider: "docs"},
	}
	cfg.Execution = &models.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers: []models.ExecutionWorkerConfig{
			{Name: executionNode, Endpoint: "file://" + mailbox},
			{Name: executionSecondNode, Endpoint: "file://" + mailbox},
		},
		Timeouts: &models.ExecutionTimeoutsConfig{Preflight: 30},
	}
	for _, change := range adjust {
		change(&cfg)
	}
	for _, name := range executionWorkspacePackages {
		repo.SeedPackage("packages", name)
	}
	repo.WriteConfigModel(cfg)
	repo.Commit(fmt.Sprintf("feat(%s): bootstrap the workspace",
		strings.Join(executionWorkspacePackages, ",")))
	return newExecutionRigOver(t, repo, mailbox)
}

// executionStageWindow is how long the fixture's timed builds occupy their
// slot. It is generous on purpose: a node polls its mailbox with a back-off of
// up to five seconds, so a window that only just exceeded the poll would make
// every overlap claim a claim about scheduling luck.
const executionStageWindow = 8 * time.Second

// executionOneWorker narrows the fixture to a single node, for the scenarios
// whose claim is about what a run leaves behind rather than about placement.
//
// It pins the builds to that node, because the claims made on this fixture
// are about the delegated path: under the default the orchestrator is one
// more node of the pool and takes a frame whenever the single worker is full
// or gone, which is a placement these scenarios are not about. The publish
// stage keeps the default, which is the orchestrator anyway.
func executionOneWorker(cfg *models.File) {
	cfg.Execution.Workers = cfg.Execution.Workers[:1]
	cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker, Publish: models.RunOnlyBoth}
}

// recordingBuild is the build script of the scenarios that only need to know
// where each package was built.
func recordingBuild(*harness.Repo) string { return executionRecordingScript }

// startWorkers starts one node per name against this rig's mailbox.
func (r *executionRig) startWorkers(names []string, capacity int) []*executionWorker {
	r.t.Helper()
	workers := make([]*executionWorker, 0, len(names))
	for _, name := range names {
		workers = append(workers, r.startWorker(executionWorkerConfig(r.mailbox,
			func(settings *models.ExecutionConfig) {
				settings.Name = name
				settings.Concurrency = models.Int(capacity)
			}), 0))
	}
	return workers
}

// stopAll signals every node and waits for it, which is how a scenario ends
// the processes it started without losing the coverage a killed one never
// flushes.
func stopAll(t *testing.T, workers []*executionWorker) []harness.RunResult {
	t.Helper()
	replies := make([]harness.RunResult, 0, len(workers))
	for _, worker := range workers {
		worker.proc.Signal(syscall.SIGINT)
		replies = append(replies, worker.proc.Wait())
	}
	return replies
}

// TestExecutionBuildsRunOnWorkersFromThePreparedSnapshot: every build of a
// distributed release runs on a node, none of them here, and what each node
// builds is the working tree as the orchestrator had it at that moment, so a
// version edit this run made is a version edit the consumer's build reads.
func TestExecutionBuildsRunOnWorkersFromThePreparedSnapshot(t *testing.T) {
	rig := newExecutionWorkspace(t,
		func(*harness.Repo) string { return executionRecordingScript + " && " + executionManifestProbe },
		func(cfg *models.File) {
			space := cfg.Spaces["libs"]
			space.AutoVersion = &models.AutoVersionConfig{
				Kinds:        []string{"dependencies"},
				WriteVersion: models.Bool(true),
			}
			cfg.Spaces["libs"] = space
		}, pinPackages(map[string]*models.RunOnly{
			"assets": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
			"ui":     placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
			"docs":   placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
			"app":    placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
		}))
	for _, name := range executionWorkspacePackages {
		rig.repo.WriteFile(filepath.Join("packages", name, "package.json"),
			executionManifest(name, "", ""))
	}
	rig.repo.WriteFile(filepath.Join("packages", "ui", "package.json"),
		executionManifest("ui", "@acme/assets", "1.0.0"))
	rig.repo.Commit("feat(assets,ui,docs,app): declare the manifests")
	headBefore := rig.repo.Git("rev-parse", "HEAD")
	branchesBefore := rig.repo.Git("for-each-ref", "--format=%(refname)", "refs/heads/")
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)
	defer func() {
		for index, reply := range stopAll(t, workers) {
			if t.Failed() {
				t.Logf("worker %d stdout:\n%s\nstderr:\n%s", index, reply.Stdout, reply.Stderr)
			}
			assert.NotContains(t, reply.Stdout, executionSecret, "the secret never reaches a node's log")
		}
	}()

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	placed := rig.nodesByPackage()
	for _, name := range executionWorkspacePackages {
		assert.Contains(t, []string{executionNode, executionSecondNode}, placed[name],
			"%s built on a worker rather than here (runs: %v)", name, rig.runs())
	}
	assert.Equal(t, "0.1.0", executionProbeValue(t, rig, "manifest", "ui"),
		"the consumer's remote build read the version this run wrote into its manifest")

	assert.ElementsMatch(t,
		[]string{"assets@0.1.0", "ui@0.1.0", "docs@0.1.0", "app@0.1.0"}, rig.repo.TagList())
	assert.Equal(t, headBefore, rig.repo.Git("rev-parse", "HEAD"),
		"preparing an input state moved no head")
	assert.Equal(t, branchesBefore, rig.repo.Git("for-each-ref", "--format=%(refname)", "refs/heads/"),
		"and wrote no branch of its own")
	assert.Empty(t, rig.repo.Git("diff", "--cached", "--name-only"),
		"and staged nothing in the repository's own index")
	retained := rig.branches()
	assert.NotEmpty(t, retained, "the sealed canonical feed remains readable to peers")
	for _, branch := range retained {
		assert.NotContains(t, branch, "dispat-worker-", "preflight probe refs are cleaned")
	}

}

// executionManifestProbe records the version the package's own manifest
// carried when the build ran, which is how a scenario proves what a node built
// from.
const executionManifestProbe = `printf '%s %s %s\n' probe-manifest "$DISPAT_PACKAGE" ` +
	`"$(sed -n 's/.*"version": "\([^"]*\)".*/\1/p' package.json | head -1)" >> "$DISPAT_IT_EXECUTION_LOG"`

// executionManifest is one package's manifest, optionally declaring one
// workspace dependency for the version stage to reconcile.
func executionManifest(name, dependency, version string) string {
	if dependency == "" {
		return fmt.Sprintf("{\n  \"name\": \"@acme/%s\",\n  \"version\": \"1.0.0\"\n}\n", name)
	}
	return fmt.Sprintf("{\n  \"name\": \"@acme/%s\",\n  \"version\": \"1.0.0\",\n"+
		"  \"dependencies\": {\"%s\": \"%s\"}\n}\n", name, dependency, version)
}

// executionProbeValue is what one probe recorded for one package.
func executionProbeValue(t *testing.T, rig *executionRig, probe, name string) string {
	t.Helper()
	for _, run := range rig.runs() {
		if run.Node == executionProbePrefix+probe && run.Package == name {
			return run.Dir
		}
	}
	t.Fatalf("no %s probe for %s among %v", probe, name, rig.runs())
	return ""
}

// TestExecutionTasksSharingARepositoryUseSeparateBranches: two builds of one
// repository overlap on two nodes, each on a coordination branch of its own
// and in a checkout of its own, so one repository's work is never one node's
// queue.
func TestExecutionTasksSharingARepositoryUseSeparateBranches(t *testing.T) {
	const (
		uiGateName   = "ui-build.gate"
		docsGateName = "docs-build.gate"
	)
	gateDir := t.TempDir()
	uiGate := filepath.Join(gateDir, uiGateName)
	docsGate := filepath.Join(gateDir, docsGateName)
	rig := newExecutionWorkspace(t, func(*harness.Repo) string {
		// Each build waits for the other's marker. A completed release thus
		// proves both commands were alive together; serial placement cannot
		// satisfy either wait.
		rendezvous := `case "$DISPAT_PACKAGE" in ` +
			`ui) ` + stageRelationRendezvous(shellQuote(uiGate), shellQuote(docsGate)) + ` ;; ` +
			`docs) ` + stageRelationRendezvous(shellQuote(docsGate), shellQuote(uiGate)) + ` ;; esac`
		return executionRecordingScript + " && " + rendezvous
	}, func(cfg *models.File) {
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker, Publish: models.RunOnlyBoth}
	})
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 1)
	defer func() {
		for index, reply := range stopAll(t, workers) {
			if t.Failed() {
				t.Logf("worker %d stdout:\n%s\nstderr:\n%s", index, reply.Stdout, reply.Stderr)
			}
		}
	}()
	watch := newExecutionBranchWatch(t, rig)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assigned := watch.seen()
	assert.FileExists(t, uiGate)
	assert.FileExists(t, docsGate)
	placed := rig.nodesByPackage()
	assert.NotEqual(t, placed["ui"], placed["docs"], "the rendezvoused builds ran on two nodes")

	assert.Len(t, assigned, len(executionWorkspacePackages),
		"one coordination branch per dispatched task, and no two tasks shared one: %v", assigned)

	dirs := map[string]bool{}
	for _, run := range rig.runs() {
		assert.False(t, dirs[run.Dir], "%s ran in a checkout another task had used", run.Package)
		dirs[run.Dir] = true
		if relative, err := filepath.Rel(rig.repo.Root, run.Dir); err == nil && filepath.IsLocal(relative) {
			assert.True(t, strings.HasPrefix(relative, filepath.Join(".git", "dispat", "return-runs")+string(filepath.Separator)),
				"first-node work uses a private task checkout under Git state, not the source checkout: %s", run.Dir)
		}
	}
}

// TestExecutionTransportCommitsStayOutsideReleaseHistory: what a run puts on a
// mailbox is transport, and none of it reaches the history a release is
// recorded in.
func TestExecutionTransportCommitsStayOutsideReleaseHistory(t *testing.T) {
	rig := newExecutionWorkspace(t, recordingBuild, executionOneWorker)
	workers := rig.startWorkers([]string{executionNode}, 4)
	defer func() {
		for _, reply := range stopAll(t, workers) {
			if t.Failed() {
				t.Logf("worker stdout:\n%s\nstderr:\n%s", reply.Stdout, reply.Stderr)
			}
		}
	}()
	plannedHead := rig.repo.Git("rev-parse", "HEAD")

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	retained := rig.branches()
	assert.NotEmpty(t, retained, "the sealed canonical feed remains available for exact receipt recovery")
	for _, branch := range retained {
		assert.NotContains(t, branch, "dispat-worker-", "ephemeral preflight refs are cleaned")
	}
	require.NotEmpty(t, rig.repo.TagList())
	for _, tag := range rig.repo.TagList() {
		assert.Equal(t, plannedHead, rig.repo.Git("rev-list", "-1", tag),
			"%s points at the planned head rather than at a transport commit", tag)
	}
	assert.NotContains(t, rig.repo.Git("log", "--format=%s", harness.DefaultBranch),
		"dispat transport", "no transport commit is on the release branch")
}

// TestExecutionBudgetsHoldAcrossNodes: the run's build budget is the run's,
// whatever the pool underneath it is, so three nodes do not make a budget of
// two into a budget of six.
func TestExecutionBudgetsHoldAcrossNodes(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "consumers")
	rig := newExecutionWorkspace(t, func(repo *harness.Repo) string {
		meeting := `
case "$DISPAT_PACKAGE" in
  ui) ` + stageRelationRendezvous(`"$DISPAT_IT_EXECUTION_GATE.ui"`,
			`"$DISPAT_IT_EXECUTION_GATE.docs"`) + ` ;;
  docs) ` + stageRelationRendezvous(`"$DISPAT_IT_EXECUTION_GATE.docs"`,
			`"$DISPAT_IT_EXECUTION_GATE.ui"`) + ` ;;
esac`
		return executionRecordingScript + " && " + repo.TsmarkCommandScript(harness.TsmarkCommandOptions{
			LogFile: "timeline.log", Label: "$DISPAT_PACKAGE", Sleep: executionStageWindow,
			Command: meeting,
		})
	}, func(cfg *models.File) {
		cfg.Concurrency = []int{2, 1}
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker, Publish: models.RunOnlyBoth}
	})
	workers := rig.startWorkersHolding([]string{executionNode, executionSecondNode}, 2,
		executionGateEnv+"="+gate)
	defer stopAll(t, workers)

	res := rig.release(executionGateEnv + "=" + gate)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.FileExists(t, gate+".ui", "ui entered the bounded consumer rendezvous")
	require.FileExists(t, gate+".docs", "docs entered the bounded consumer rendezvous")
	timeline := rig.repo.Timeline("timeline.log")
	require.Len(t, timeline, len(executionWorkspacePackages))
	harness.AssertConcurrencyBudget(t, timeline, 2)
}

// TestExecutionWorkerCapacityHoldsAcrossRuns: a node's capacity is the node's,
// so two independent releases sharing one node of capacity one never have two
// of its tasks in flight, and both of them still finish.
func TestExecutionWorkerCapacityHoldsAcrossRuns(t *testing.T) {
	mailbox := executionMailbox(t)
	gate := filepath.Join(t.TempDir(), "first-build-may-finish")
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0o600) })
	first := newExecutionRepositoryOn(t, mailbox, "first", gate)
	second := newExecutionRepositoryOn(t, mailbox, "second")
	worker := startWorker(t, first.repo, executionWorkerConfig(mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 0,
		executionBuildLogEnv+"="+first.builds)

	started := first.repo.StartReleaseEnv(first.env(), "release", "--return-endpoint", first.returnURL)
	if !assert.Eventually(t, func() bool { return len(first.runs()) == 1 }, 60*time.Second, 50*time.Millisecond,
		"the first build did not take the worker's only capacity slot") {
		started.Signal(syscall.SIGINT)
		firstResult := started.Wait()
		worker.proc.Signal(syscall.SIGINT)
		workerResult := worker.proc.Wait()
		t.Fatalf("first release never started its command; stdout:\n%s\nstderr:\n%s\nworker stdout:\n%s\nworker stderr:\n%s",
			firstResult.Stdout, firstResult.Stderr, workerResult.Stdout, workerResult.Stderr)
	}
	priorSlots := make(map[string]bool)
	for _, branch := range executionProtocolRefs(t, mailbox, "dispat-slot-") {
		priorSlots[branch] = true
	}
	otherStarted := second.repo.StartReleaseEnv(second.env(), "release", "--return-endpoint", second.returnURL)
	var waitingSlot string
	require.Eventually(t, func() bool {
		waitingSlot = executionNewActivatedSlot(t, mailbox, priorSlots)
		return waitingSlot != ""
	}, 60*time.Second, 50*time.Millisecond, "the second run did not activate its slot notice")
	require.Never(t, func() bool {
		return len(second.runs()) != 0 || executionNewActivatedSlot(t, mailbox, priorSlots) != waitingSlot
	}, 700*time.Millisecond, 50*time.Millisecond,
		"a full worker must leave the second notice unchanged and the task unclaimed")
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	other := otherStarted.Wait()
	res := started.Wait()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.Equal(t, 0, other.Code, "stdout:\n%s\nstderr:\n%s", other.Stdout, other.Stderr)
	timeline := append(first.repo.Timeline("timeline.log"), second.repo.Timeline("timeline.log")...)
	require.Len(t, timeline, 2, "both releases built their package")
	harness.AssertConcurrencyBudget(t, timeline, 1)
	worker.proc.Signal(syscall.SIGINT)
	require.Equal(t, 0, worker.proc.Wait().Code)
}

// executionActiveSlotForTask returns the build notice for a package while a
// node is full. The signed assignment supplies the package and task kind;
// graph task IDs are deliberately opaque here.
func executionNewActivatedSlot(t *testing.T, mailbox string, priorSlots map[string]bool) string {
	t.Helper()
	for _, branch := range executionProtocolRefs(t, mailbox, "dispat-slot-") {
		if priorSlots[branch] {
			continue
		}
		notice := executionMessage(t, mailbox, branch, "slot")
		root, _ := notice["graphRootOid"].(string)
		if root == "" {
			continue
		}
		require.NotEmpty(t, notice["bootstrapOid"], "activated v3 slot binds the signed bootstrap")
		require.Empty(t, notice["taskBranch"], "compiled pickup notices are task-neutral")
		require.Empty(t, notice["assignment"], "a busy worker has no assigned task in this run")
		document := bareGit(t, mailbox, "show", "refs/heads/"+branch+":dispat/slot.json")
		signature := strings.TrimSpace(bareGit(t, mailbox, "show", "refs/heads/"+branch+":dispat/slot.sig"))
		require.Equal(t, executionSign("slot", executionSecret, []byte(document)), signature,
			"the activated notice is signed by the run")
		return strings.TrimSpace(bareGit(t, mailbox, "rev-parse", "refs/heads/"+branch))
	}
	return ""
}

// newExecutionRepositoryOn is one independent single-package repository
// delegating to a mailbox somebody else may also be using, with a timing probe
// labelled by the repository so two runs are told apart.
func newExecutionRepositoryOn(t *testing.T, mailbox, label string, gate ...string) *executionRig {
	t.Helper()
	repo := harness.New(t)
	repo.SeedPackage("packages", label)
	script := executionRecordingScript
	if len(gate) != 0 {
		quoted := "'" + strings.ReplaceAll(gate[0], "'", "'\\''") + "'"
		script += " && for i in $(seq 1 600); do test -f " + quoted + " && break; sleep .1; done && test -f " + quoted
	}
	cfg := libsConfig(script+" && "+
		repo.TsmarkScript("timeline.log", label, 3*time.Second), 1)
	pinPackages(map[string]*models.RunOnly{
		label: placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
	})(&cfg)
	cfg.Execution = &models.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers:   []models.ExecutionWorkerConfig{{Name: executionNode, Endpoint: "file://" + mailbox}},
		Timeouts:  &models.ExecutionTimeoutsConfig{Preflight: 30},
	}
	repo.WriteConfigModel(cfg)
	repo.Commit("feat(" + label + "): bootstrap")
	return newExecutionRigOver(t, repo, mailbox)
}

// TestExecutionLockfilePreparationRunsOnTheOrchestratorOnly: the shared
// manifest and lock-file preparation is the orchestrator's (§28.3), so it runs
// here, serialized, and every dispatched build reads the state it left.
func TestExecutionLockfilePreparationRunsOnTheOrchestratorOnly(t *testing.T) {
	rig := newExecutionWorkspace(t,
		func(*harness.Repo) string { return executionRecordingScript + " && " + executionLockProbe },
		func(cfg *models.File) {
			space := cfg.Spaces["libs"]
			space.AutoVersion = &models.AutoVersionConfig{
				Kinds:        []string{"dependencies"},
				WriteVersion: models.Bool(true),
				SyncLock:     []string{"synclock"},
			}
			cfg.Spaces["libs"] = space
			cfg.Scripts["synclock"] = models.Script{executionLockWriter}
		})
	for _, name := range executionWorkspacePackages {
		rig.repo.WriteFile(filepath.Join("packages", name, "package.json"),
			executionManifest(name, "", ""))
	}
	rig.repo.Commit("feat(assets,ui,docs,app): declare the manifests")
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	prepared, read := 0, 0
	for _, run := range rig.runs() {
		switch run.Node {
		case executionProbePrefix + "synclock":
			prepared++
			assert.Equal(t, "orchestrator", run.Dir,
				"the lock-file preparation of %s ran on a worker", run.Package)
		case executionProbePrefix + "lock":
			read++
			assert.Equal(t, "complete", run.Dir,
				"%s built from a checkout whose lock file was complete", run.Package)
		}
	}
	assert.Positive(t, prepared, "the syncLock stage ran: %v", rig.runs())
	assert.Equal(t, len(executionWorkspacePackages), read, "every build read the lock file")
	stopAll(t, workers)
}

// executionLockWriter is the shared preparation: it writes a lock file in the
// repository root, which is what a build of any package then reads.
const executionLockWriter = `printf 'complete\n' > ../../lockfile.txt && ` +
	`printf '%s %s %s\n' probe-synclock "$DISPAT_PACKAGE" "${DISPAT_EXECUTION_NODE:-orchestrator}" ` +
	`>> "$DISPAT_IT_EXECUTION_LOG"`

// executionLockProbe records what the lock file held when the build ran.
const executionLockProbe = `printf '%s %s %s\n' probe-lock "$DISPAT_PACKAGE" ` +
	`"$(cat ../../lockfile.txt 2>/dev/null || echo missing)" >> "$DISPAT_IT_EXECUTION_LOG"`

// TestExecutionOrchestratorRoleNodeServesDelegatedTask: serving is a posture
// rather than a role (§28.1). A node configured as an orchestrator, with
// worker links of its own, serves the work addressed to it and never consults
// those links while it does.
func TestExecutionOrchestratorRoleNodeServesDelegatedTask(t *testing.T) {
	rig := newExecutionWorkspace(t, recordingBuild, executionOneWorker)
	unused := executionMailbox(t)
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) {
			settings.Workers = []models.ExecutionWorkerConfig{
				{Name: "somebody-else", Endpoint: "file://" + unused},
			}
		}), 0)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	placed := rig.nodesByPackage()
	for _, name := range executionWorkspacePackages {
		assert.Equal(t, executionNode, placed[name], "%s ran on the serving node", name)
	}
	assert.Empty(t, executionMailboxBranches(t, unused),
		"a node serving a task delegates nothing of its own")
	stopAll(t, []*executionWorker{worker})
}

// TestExecutionWorkerTaskRefusesNestedRelease: every command of a task runs
// under worker authority, so a build script that tries to start a release or
// write a release record is refused wherever in the tree it is, and the
// package fails at its build stage.
func TestExecutionWorkerTaskRefusesNestedRelease(t *testing.T) {
	for name, args := range map[string][]string{
		"a nested release":     {"release"},
		"a nested release tag": {"commit", "--tag"},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionWorkspace(t, func(repo *harness.Repo) string {
				return executionRecordingScript + " && " + repo.DispatCommand(args...)
			}, executionOneWorker)
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

			res := rig.release()

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			failed := executionFailedStages(res)
			require.NotEmpty(t, failed, "a package failed\nstdout:\n%s", res.Stdout)
			for _, stage := range failed {
				assert.Equal(t, "build", stage, "a refused nested command fails its package at the build stage")
			}
			reply := stopAll(t, []*executionWorker{worker})[0]
			assert.Contains(t, reply.Stdout, executionAuthorityCode,
				"the nested command was refused on the node")
			assert.Empty(t, rig.repo.TagList(), "and nothing was tagged anywhere")
		})
	}
}

// TestExecutionRemoteBuildFailureFailsThePackageLocally: a node reporting a
// failure fails that package here, at its build stage, with the outcome script
// running on the orchestrator, while the node stays healthy enough to take
// every other task of the same run.
func TestExecutionRemoteBuildFailureFailsThePackageLocally(t *testing.T) {
	for name, adjust := range map[string]func(*models.SpaceFlowConfig){
		"a hook before the stage": func(flow *models.SpaceFlowConfig) {
			flow.BeforeBuild = []string{"failui"}
		},
		"the stage's own script": func(flow *models.SpaceFlowConfig) {
			flow.Build = []string{"build", "failui"}
		},
		"a hook after the stage": func(flow *models.SpaceFlowConfig) {
			flow.PostBuild = []string{"failui"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionWorkspace(t, recordingBuild, executionOneWorker, func(cfg *models.File) {
				space := cfg.Spaces["libs"]
				space.Flow.OnFail = []string{"onfail"}
				adjust(space.Flow)
				cfg.Spaces["libs"] = space
				cfg.Scripts["onfail"] = models.Script{executionOnFailScript}
				cfg.Scripts["failui"] = models.Script{`[ "$DISPAT_PACKAGE" != ui ] || exit 4`}
			})
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

			res := rig.release()

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Equal(t, []string{"build"}, executionFailedStages(res),
				"the package failed at its build stage\nstdout:\n%s", res.Stdout)
			assert.Equal(t, "orchestrator", executionProbeValue(t, rig, "onfail", "ui"),
				"the outcome script ran here, never on the node")
			assert.Contains(t, res.Stdout, "(exit 4)",
				"the run names the exit status of the command that failed on the node\nstdout:\n%s", res.Stdout)
			placed := rig.nodesByPackage()
			assert.Equal(t, executionNode, placed["docs"], "an unrelated package still built: %v", rig.runs())
			assert.Equal(t, executionNode, placed["app"],
				"and the node that reported the failure still took the next task")
			assert.True(t, rig.repo.IsTagged("docs@0.1.0"), "the unrelated package still released")
			assert.False(t, rig.repo.IsTagged("ui@0.1.0"), "and the failed one did not")
			stopAll(t, []*executionWorker{worker})
		})
	}
}

// executionOnFailScript records that the outcome script ran and where.
const executionOnFailScript = `printf '%s %s %s\n' probe-onfail "$DISPAT_PACKAGE" ` +
	`"${DISPAT_EXECUTION_NODE:-orchestrator}" >> "$DISPAT_IT_EXECUTION_LOG"`

// executionFailedStages is the stage each failed package failed at, read off
// the run's own summary lines.
func executionFailedStages(res harness.RunResult) []string {
	var stages []string
	for _, event := range executionEvents(res) {
		if event.Str("message") == "summary" && event.Str("status") == "failed" {
			stages = append(stages, event.Str("failedStage"))
		}
	}
	return stages
}

// TestExecutionStrayWritesAreReported: a build that edits a tracked file of
// the checkout it was given has written where this run will not carry it, so
// the count is reported and the orchestrator's own tree is untouched.
func TestExecutionStrayWritesAreReported(t *testing.T) {
	rig := newExecutionWorkspace(t, func(*harness.Repo) string {
		return executionRecordingScript + " && printf 'edited\n' > main.txt"
	}, executionOneWorker)
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	stray, isStray := executionLine(res,
		"the task wrote tracked files outside what it declared, and they are not admitted")
	require.True(t, isStray, "stdout:\n%s", res.Stdout)
	assert.Equal(t, executionRetainedCode, stray.Code())
	assert.Equal(t, "assets\n", readRepoFile(t, rig.repo, filepath.Join("packages", "assets", "main.txt")),
		"the orchestrator's own checkout is unchanged")
	stopAll(t, []*executionWorker{worker})
}

// TestExecutionTaskTimeoutCancelsAndSettlesAttempt: each bounded retry of a
// timed-out build is stopped by a signed Cancel and Ack before one terminal
// decision. Other packages may use the released slot once; the timed-out package never publishes.
func TestExecutionTaskTimeoutCancelsAndSettlesAttempt(t *testing.T) {
	rig := newExecutionWorkspace(t, func(*harness.Repo) string {
		return executionTaskTimeoutScript
	}, func(cfg *models.File) {
		cfg.Execution.RetryAfterUncertainAttempt = true
		cfg.Execution.MaxAttempts = models.Int(3)
		cfg.Concurrency = []int{1, 1}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 3, Cancel: executionTaskTimeoutCancelSeconds}
		executionOneWorker(cfg)
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	startedRelease := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	res := startedRelease.WaitForDiagnostic(executionTaskTimeoutSettlementWait, func() {
		stdout, stderr := startedRelease.CapturedOutput()
		t.Logf("first-node stdout:\n%s\nstderr:\n%s", stdout, stderr)
		t.Logf("signed task-timeout ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
		stopped := worker.proc.KillForDiagnostic(executionTaskTimeoutWorkerDiagnosticWait)
		t.Logf("worker stdout:\n%s\nstderr:\n%s", stopped.Stdout, stopped.Stderr)
	})

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode),
		"the timed-out attempt is reported\nstdout:\n%s", res.Stdout)
	requireChoreographyTaskChain(t, rig.mailbox, rig.branches(),
		"choreography-claim", "cancel", "ack", "choreography-decision")
	assert.Equal(t, 3, executionBuildsOf(rig, "assets"),
		"each of the three signed cancelled attempts started one command")
	var accepted, cancelled, acknowledged, terminal int
	for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
		if event.Task != "assets:1:" {
			continue
		}
		switch event.Kind {
		case "claim-accepted":
			accepted++
		case "cancel-issued":
			cancelled++
		case "ack-accepted":
			acknowledged++
		case "terminal":
			terminal++
		}
	}
	assert.Equal(t, 3, accepted, "the bounded retry policy signed each new attempt")
	assert.Equal(t, accepted, cancelled, "each accepted timed-out attempt was cancelled")
	assert.Equal(t, accepted, acknowledged, "each cancellation received its exact Ack")
	assert.Equal(t, 1, terminal, "the logical task has one final decision")
	for _, name := range []string{"docs", "ui", "app"} {
		assert.LessOrEqual(t, executionBuildsOf(rig, name), 1,
			"a later package may use the settled slot but cannot run a duplicate command: %s", name)
	}
	assert.Equal(t, 0, rig.repo.TagCount("assets@"),
		"the timed-out package was not published")
	assert.False(t, remoteHoldsLock(t, rig.origin), "and the lock was given back")

	reply := stopAll(t, []*executionWorker{worker})[0]
	assert.Equal(t, 0, reply.Code, "stdout:\n%s\nstderr:\n%s", reply.Stdout, reply.Stderr)
}

// executionBranchWatch records every activated build task ref while a run is
// going on. Retained refs remain as signed recovery evidence after the run.
type executionBranchWatch struct {
	t     *testing.T
	done  chan struct{}
	once  sync.Once
	found chan executionBranchObservation
}

type executionBranchObservation struct {
	names []string
	err   error
}

func newExecutionBranchWatch(t *testing.T, rig *executionRig) *executionBranchWatch {
	t.Helper()
	watch := &executionBranchWatch{t: t, done: make(chan struct{}), found: make(chan executionBranchObservation, 1)}
	t.Cleanup(func() { watch.once.Do(func() { close(watch.done) }) })
	go func() {
		seen := map[string]bool{}
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watch.done:
				names := make([]string, 0, len(seen))
				for name := range seen {
					names = append(names, name)
				}
				watch.found <- executionBranchObservation{names: names}
				return
			case <-ticker.C:
				observed, err := observedBuildBranches(rig.mailbox)
				if err != nil {
					watch.found <- executionBranchObservation{err: err}
					return
				}
				for _, name := range observed {
					seen[name] = true
				}
			}
		}
	}()
	return watch
}

// executionBranchKind is the diagnostic label of a legacy node-addressed
// branch. Active task refs omit the kind; use executionAssignedTaskKind for
// them. It reads from the right because node names may contain hyphens.
func executionBranchKind(branch string) string {
	words := strings.Split(branch, "-")
	if len(words) < 2 {
		return ""
	}
	return words[len(words)-2]
}

// seen stops the watch and answers the branch names it saw, each once.
func (w *executionBranchWatch) seen() []string {
	w.t.Helper()
	w.once.Do(func() { close(w.done) })
	observation := <-w.found
	require.NoError(w.t, observation.err)
	return observation.names
}

// observedBuildBranches reads the listed object IDs, so cleanup may delete a
// ref without invalidating the commit that the watcher already observed.
func observedBuildBranches(mailbox string) ([]string, error) {
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", mailbox}, args...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %v: %w: %s", args, err, stderr.String())
		}
		return string(out), nil
	}
	listed, err := git("for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads/dispat-task-*")
	if err != nil {
		return nil, err
	}
	var observed []string
	for _, line := range strings.Split(strings.TrimSpace(listed), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("malformed task ref observation %q", line)
		}
		commits, err := git("rev-list", "--first-parent", "--reverse", fields[1])
		if err != nil {
			return nil, err
		}
		for _, commit := range strings.Fields(commits) {
			entries, err := git("ls-tree", "-r", "--name-only", commit)
			if err != nil {
				return nil, err
			}
			if !strings.Contains("\n"+entries+"\n", "\ndispat/assignment.json\n") {
				continue // Bootstrap and inert reservation precede activation.
			}
			encoded, err := git("cat-file", "blob", commit+":dispat/assignment.json")
			if err != nil {
				return nil, err
			}
			var assignment struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal([]byte(encoded), &assignment); err != nil {
				return nil, fmt.Errorf("decode assignment on %s: %w", fields[0], err)
			}
			if assignment.Kind == "build" {
				observed = append(observed, fields[0])
			}
			break // This task's later commits retain the same assignment.
		}
	}
	return observed, nil
}

// TestExecutionSnapshotGitFaults: the first node's own Git failing while
// the pinned input baseline is captured refuses the run before it dispatches
// a task from a state it cannot describe, and publishes nothing.
func TestExecutionSnapshotGitFaults(t *testing.T) {
	for name, pattern := range map[string]string{
		"the index cannot be located": "*rev-parse*--git-path*index*",
		"the staging fails":           "*--literal-pathspecs add*",
		"the tree cannot be written":  "*write-tree*",
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionWorkspace(t, recordingBuild, executionOneWorker)
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: pattern, Onward: true, Nth: 1})

			res := rig.release(fault.Env()...)

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, fault.Matches(), "the fault reached the invocation it names")
			assert.Contains(t, res.Stdout, "cannot dispatch this release",
				"the first node names the failed source capture before task dispatch")
			assert.Empty(t, rig.runs(), "no command started from an uncaptured source")
			assert.Empty(t, rig.repo.TagList(), "nothing was published")
			assert.False(t, remoteHoldsLock(t, rig.origin), "the exact release lock was returned")
			stopAll(t, []*executionWorker{worker})
		})
	}
}

// TestExecutionWorkerTaskGitFaults: a node's own git failing around a task's
// checkout. What the task was for decides the outcome: a checkout that cannot
// be made is a failed task, while the two calls that only tidy up or count are
// warnings the release survives.
func TestExecutionWorkerTaskGitFaults(t *testing.T) {
	for name, tc := range map[string]struct {
		pattern string
		code    int
	}{
		"the checkout cannot be made":       {pattern: "*worktree add*", code: 1},
		"the checkout cannot be inspected":  {pattern: "*status --porcelain*", code: 0},
		"the checkout cannot be taken away": {pattern: "*worktree remove*", code: 0},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionWorkspace(t, recordingBuild, executionOneWorker)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: tc.pattern, Onward: true, Nth: 1})
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)

			res := rig.release()

			require.Equal(t, tc.code, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, fault.Matches(), "the fault reached the invocation it names")
			reply := stopAll(t, []*executionWorker{worker})[0]
			assert.Equal(t, 0, reply.Code, "the node carries on serving after its git failed")
		})
	}
}

// TestExecutionFirstNodeOwnsClaimAndSettlesOneAttempt: the serving node can
// read work and return a Result while canonical task pushes are refused. The
// first node alone advances the canonical task through one Claim and Decision,
// while the separate signed ledger records the accepted worker Result.
// Lost HTTPS replies are exercised by the exact-proposal Worker receipt tests.
func TestExecutionFirstNodeOwnsClaimAndSettlesOneAttempt(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
	})
	peerPush := harness.NewGitFault(t, harness.GitFault{Pattern: "*push*refs/heads/dispat-task-*", Code: 128})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 60, peerPush.Env()...)
	defer worker.stop(t)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.Zero(t, peerPush.Matches(), "the serving node never pushes a canonical task ref")
	require.Equal(t, 1, executionBuildsOf(rig, "core"), "one build executed")
	buildBranches := 0
	for _, branch := range executionProtocolRefs(t, rig.mailbox, "dispat-task-") {
		if executionAssignedTaskKind(t, rig.mailbox, branch) != "build" {
			continue
		}
		buildBranches++
		assignment := executionTaskAssignment(t, rig.mailbox, branch)
		taskID, _ := assignment["task"].(string)
		require.NotEmpty(t, taskID)
		chain := strings.Split(strings.TrimSpace(bareGit(t, rig.mailbox, "log", "--first-parent",
			"--reverse", "--format=%s", "refs/heads/"+branch)), "\n")
		for _, kind := range []string{"choreography-claim", "choreography-decision"} {
			assert.Equal(t, 1, strings.Count(strings.Join(chain, "\n"), "dispat transport "+kind), kind)
		}
		acceptedResults := 0
		for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
			if event.Task == taskID && event.Kind == "result-accepted" {
				acceptedResults++
			}
		}
		assert.Equal(t, 1, acceptedResults, "one signed worker Result was accepted")
	}
	require.Equal(t, 1, buildBranches, "one canonical build task ref remains for recovery")
}
