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
	"fmt"
	"path/filepath"
	"strings"
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
func executionOneWorker(cfg *models.File) {
	cfg.Execution.Workers = cfg.Execution.Workers[:1]
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
		})
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
	assert.Empty(t, rig.branches(), "a finished run closes the branches it created")

	for _, reply := range stopAll(t, workers) {
		assert.NotContains(t, reply.Stdout, executionSecret, "the secret never reaches a node's log")
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
	rig := newExecutionWorkspace(t, func(repo *harness.Repo) string {
		return executionRecordingScript + " && " +
			repo.TsmarkScript("timeline.log", "$DISPAT_PACKAGE", executionStageWindow)
	})
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 1)
	watch := newExecutionBranchWatch(t, rig)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assigned := watch.seen()
	timeline := rig.repo.Timeline("timeline.log")
	harness.AssertOverlaps(t, harness.Find(t, timeline, "ui"), harness.Find(t, timeline, "docs"))
	placed := rig.nodesByPackage()
	assert.NotEqual(t, placed["ui"], placed["docs"], "the two overlapping builds ran on two nodes")

	assert.Len(t, assigned, len(executionWorkspacePackages),
		"one coordination branch per dispatched task, and no two tasks shared one: %v", assigned)

	dirs := map[string]bool{}
	for _, run := range rig.runs() {
		assert.False(t, dirs[run.Dir], "%s ran in a checkout another task had used", run.Package)
		dirs[run.Dir] = true
		assert.NotContains(t, run.Dir, rig.repo.Root, "and never in the orchestrator's own checkout")
	}
	stopAll(t, workers)
}

// TestExecutionTransportCommitsStayOutsideReleaseHistory: what a run puts on a
// mailbox is transport, and none of it reaches the history a release is
// recorded in.
func TestExecutionTransportCommitsStayOutsideReleaseHistory(t *testing.T) {
	rig := newExecutionWorkspace(t, recordingBuild, executionOneWorker)
	workers := rig.startWorkers([]string{executionNode}, 4)
	plannedHead := rig.repo.Git("rev-parse", "HEAD")

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Empty(t, rig.branches(), "the run closed every branch it created")
	require.NotEmpty(t, rig.repo.TagList())
	for _, tag := range rig.repo.TagList() {
		assert.Equal(t, plannedHead, rig.repo.Git("rev-list", "-1", tag),
			"%s points at the planned head rather than at a transport commit", tag)
	}
	assert.NotContains(t, rig.repo.Git("log", "--format=%s", harness.DefaultBranch),
		"dispat transport", "no transport commit is on the release branch")
	stopAll(t, workers)
}

// TestExecutionBudgetsHoldAcrossNodes: the run's build budget is the run's,
// whatever the pool underneath it is, so three nodes do not make a budget of
// two into a budget of six.
func TestExecutionBudgetsHoldAcrossNodes(t *testing.T) {
	rig := newExecutionWorkspace(t, func(repo *harness.Repo) string {
		return executionRecordingScript + " && " +
			repo.TsmarkScript("timeline.log", "$DISPAT_PACKAGE", executionStageWindow)
	}, func(cfg *models.File) { cfg.Concurrency = []int{2, 1} })
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	timeline := rig.repo.Timeline("timeline.log")
	require.Len(t, timeline, len(executionWorkspacePackages))
	harness.AssertConcurrencyBudget(t, timeline, 2)
	stopAll(t, workers)
}

// TestExecutionWorkerCapacityHoldsAcrossRuns: a node's capacity is the node's,
// so two independent releases sharing one node of capacity one never have two
// of its tasks in flight, and both of them still finish.
func TestExecutionWorkerCapacityHoldsAcrossRuns(t *testing.T) {
	mailbox := executionMailbox(t)
	first := newExecutionRepositoryOn(t, mailbox, "first")
	second := newExecutionRepositoryOn(t, mailbox, "second")
	worker := startWorker(t, first.repo, executionWorkerConfig(mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 0,
		executionBuildLogEnv+"="+first.builds)

	started := first.repo.StartReleaseEnv(first.env(), "release")
	other := second.release()
	res := started.Wait()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.Equal(t, 0, other.Code, "stdout:\n%s\nstderr:\n%s", other.Stdout, other.Stderr)
	timeline := append(first.repo.Timeline("timeline.log"), second.repo.Timeline("timeline.log")...)
	require.Len(t, timeline, 2, "both releases built their package")
	harness.AssertConcurrencyBudget(t, timeline, 1)
	worker.proc.Signal(syscall.SIGINT)
	require.Equal(t, 0, worker.proc.Wait().Code)
}

// newExecutionRepositoryOn is one independent single-package repository
// delegating to a mailbox somebody else may also be using, with a timing probe
// labelled by the repository so two runs are told apart.
func newExecutionRepositoryOn(t *testing.T, mailbox, label string) *executionRig {
	t.Helper()
	repo := harness.New(t)
	repo.SeedPackage("packages", label)
	cfg := libsConfig(executionRecordingScript+" && "+
		repo.TsmarkScript("timeline.log", label, 3*time.Second), 1)
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
			settings.Role = models.ExecutionRoleOrchestrator
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

// TestExecutionTaskTimeoutLeaksTheSlot: a node that stops answering within the
// task deadline keeps its slot and leaves the pool, so the package fails and
// every later task that needed a node is failed at once rather than waiting
// for a machine that will not come back.
func TestExecutionTaskTimeoutLeaksTheSlot(t *testing.T) {
	rig := newExecutionWorkspace(t, func(*harness.Repo) string {
		return executionRecordingScript + " && sleep 60"
	}, func(cfg *models.File) {
		cfg.Concurrency = []int{1, 1}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 3}
		executionOneWorker(cfg)
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode),
		"the abandoned attempt is reported\nstdout:\n%s", res.Stdout)
	unhealthy, isUnhealthy := executionLine(res, "the node stopped answering and its capacity is held")
	require.True(t, isUnhealthy, "stdout:\n%s", res.Stdout)
	assert.Equal(t, executionNode, unhealthy.Str("worker"),
		"the orchestrator names the node it is reporting on in worker, never in node")
	assert.Empty(t, rig.repo.TagList(), "nothing was published")
	assert.False(t, remoteHoldsLock(t, rig.origin), "and the lock was given back")

	reply := stopAll(t, []*executionWorker{worker})[0]
	assert.Equal(t, 0, reply.Code, "stdout:\n%s\nstderr:\n%s", reply.Stdout, reply.Stderr)
	assert.NotContains(t, reply.Stdout, "assignment rejected",
		"the prepared input states this run pushed are not rejected messages")
}

// executionBranchWatch records every coordination branch that appears in a
// mailbox while a run is going on, because the run deletes them all at the end
// and the names are the claim.
type executionBranchWatch struct {
	done  chan struct{}
	found chan []string
}

func newExecutionBranchWatch(t *testing.T, rig *executionRig) *executionBranchWatch {
	t.Helper()
	watch := &executionBranchWatch{done: make(chan struct{}), found: make(chan []string, 1)}
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
				watch.found <- names
				return
			case <-ticker.C:
				for _, ref := range executionMailboxBranches(t, rig.mailbox) {
					name := strings.TrimPrefix(ref, "refs/heads/")
					if executionBranchKind(name) == "build" {
						seen[name] = true
					}
				}
			}
		}
	}()
	return watch
}

// executionBranchKind is the kind label of a coordination branch, which is
// the second-to-last word of `dispat-worker-<id>-<date>-<kind>-<random>`. It
// is read here rather than matched as a substring because a node is called
// build-a, so a snapshot branch addressed to it also holds the word "build".
func executionBranchKind(branch string) string {
	words := strings.Split(branch, "-")
	if len(words) < 2 {
		return ""
	}
	return words[len(words)-2]
}

// seen stops the watch and answers the branch names it saw, each once.
func (w *executionBranchWatch) seen() []string {
	close(w.done)
	return <-w.found
}

// TestExecutionSnapshotGitFaults: the orchestrator's own git failing where an
// input state is prepared fails the task that needed it rather than
// dispatching a state nobody could describe, and publishes nothing.
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
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode),
				"the task is refused with the integrity code\nstdout:\n%s", res.Stdout)
			assert.Empty(t, rig.repo.TagList(), "nothing was published")
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

// TestExecutionLostResultPushIsRecognized: a node whose push applied and whose
// answer was lost re-reads the branch, finds what it meant to put there, and
// reports once rather than failing the work it had already finished.
func TestExecutionLostResultPushIsRecognized(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	branch := executionBranchName("lostreply")
	orchestrator.offer(branch, orchestrator.probe(branch, "preflight"))
	// The push applies and is then reported as a rejected lease, which is what
	// a lost response looks like from inside the process: the porcelain line
	// says the ref was refused while the remote already holds the object.
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*" + branch + "*", Nth: 1, After: true,
		Output: "!\trefs/heads/" + branch + ":refs/heads/" + branch + "\t[rejected]\n",
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 4, fault.Env()...)

	executionAwaitMessage(t, rig.mailbox, branch, "result")
	res := worker.proc.Wait()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Positive(t, fault.Matches())
	resolved, isResolved := executionLine(res, "the rejected update was already on the branch")
	require.True(t, isResolved, "stdout:\n%s", res.Stdout)
	assert.Equal(t, branch, resolved.Str("branch"))
	assert.Equal(t, []string{"assignment", "claim", "result"}, executionChain(t, rig.mailbox, branch),
		"the attempt produced exactly one of each message")
}
