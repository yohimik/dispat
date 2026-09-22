// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57, the sweep: `dispat run` executed on worker nodes (CCME §28.10).
//
// A sweep is not a release. It records nothing, takes no release lock and
// installs nothing one task produced into another, so what these scenarios
// assert is the little a sweep does share with a release, the refusals, the
// fixed plan, the placement and the summary, and the one thing it adds: the
// folders a script declares it writes come back from the machines that wrote
// them and are merged into the orchestrator's checkout.
//
// The fixture is three packages of one space, `core` providing both `api` and
// `web`, and a `tests` script that records where it ran and what it read. The
// record lives outside every checkout, because the folder a node ran a task
// in is removed the moment the task ends.

import (
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionSweepPackages are the fixture's packages: `core` is the provider of
// the other two, so the two consumers become ready at the same moment.
var executionSweepPackages = []string{"core", "api", "web"}

// executionSweepScript is the `tests` script of the fixture: one line naming
// the node, the package and the stage the script read, and an export from the
// provider that each consumer records reading.
const executionSweepScript = `printf '%s %s %s\n' "${DISPAT_EXECUTION_NODE:-orchestrator}" ` +
	`"$DISPAT_PACKAGE" "$DISPAT_STAGE" >> "$DISPAT_IT_EXECUTION_LOG" &&
case "$DISPAT_PACKAGE" in
  core) echo "CORE_STAMP=from-core" >> "$DISPAT_OUTPUT" ;;
  *) printf '%s %s %s\n' probe-export "$DISPAT_PACKAGE" "${DISPAT_OUTPUT_CORE_STAMP:-missing}" \
       >> "$DISPAT_IT_EXECUTION_LOG" ;;
esac`

// newExecutionSweepRig seeds the fixture with two worker links on one mailbox
// and the given `tests` script, and whatever the scenario adjusts.
func newExecutionSweepRig(t *testing.T, script string, adjust ...func(*models.File)) *executionRig {
	t.Helper()
	repo := harness.New(t)
	mailbox := executionMailbox(t)
	cfg := libsConfig(echoBuild, 2, 1)
	cfg.Scripts["tests"] = models.Script{script}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "api", Provider: "core"},
		{Consumer: "web", Provider: "core"},
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
	for _, name := range executionSweepPackages {
		repo.SeedPackage("packages", name)
	}
	repo.WriteFile(".gitignore", "/coverage/\n")
	repo.WriteConfigModel(cfg)
	repo.Commit("feat(core,api,web): bootstrap the sweep fixture")
	return newExecutionRigOver(t, repo, mailbox)
}

// sweep runs `dispat run tests --since all` on this rig, with the release
// lock switched on, the signing secret and the record file in the
// environment, and whatever else the scenario passes.
func (r *executionRig) sweep(env []string, flags ...string) harness.RunResult {
	r.t.Helper()
	return r.repo.CommandEnv(r.env(env...), append([]string{"run", "tests", "--since", "all"}, flags...)...)
}

// executionSweepPinnedToWorkers keeps every task of the sweep off the
// orchestrator, for the scenarios whose claim is about what a delegated task
// carries rather than about where the pool placed it.
func executionSweepPinnedToWorkers(cfg *models.File) {
	cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker, Publish: models.RunOnlyBoth}
}

// executionTaskOutcomes are the summary's task lines, by package.
func executionTaskOutcomes(res harness.RunResult) map[string]harness.Event {
	outcomes := map[string]harness.Event{}
	for _, event := range executionEvents(res) {
		if event.Str("message") == "task outcome" {
			outcomes[event.Package()] = event
		}
	}
	return outcomes
}

// TestExecutionRunSweepPlacesScriptsOnWorkers: with worker links a sweep runs
// every package's task on a node, none of them here, providers first; the
// provider's export reaches a consumer that ran on another machine; the
// summary names the node of every task; and the sweep takes no release lock
// and leaves no coordination branch behind.
func TestExecutionRunSweepPlacesScriptsOnWorkers(t *testing.T) {
	rig := newExecutionSweepRig(t, executionSweepScript)
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 1)
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: lockPushPattern})

	res := rig.sweep(fault.Env())

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	placed := rig.nodesByPackage()
	for _, name := range executionSweepPackages {
		assert.Contains(t, []string{executionNode, executionSecondNode}, placed[name],
			"%s ran on a worker node: %v", name, rig.runs())
	}
	for _, run := range rig.runs() {
		if !strings.HasPrefix(run.Node, executionProbePrefix) {
			assert.Equal(t, "run:tests", run.Dir, "the task read the stage a sweep names")
		}
	}
	assert.NotEqual(t, placed["api"], placed["web"],
		"the two consumers were ready together and ran on two machines: %v", rig.runs())
	exports := executionProbeValues(rig, "export")
	assert.Equal(t, "from-core", exports["api"], "the provider's export reached api")
	assert.Equal(t, "from-core", exports["web"], "and web, one of which ran on another machine than core")

	fixed, isFixed := executionLine(res, "plan fixed")
	require.True(t, isFixed, "the sweep names the plan every assignment carries\nstdout:\n%s", res.Stdout)
	assert.NotEmpty(t, fixed.Str("planDigest"))
	assert.NotEmpty(t, fixed.Str("run"))
	outcomes := executionTaskOutcomes(res)
	for _, name := range executionSweepPackages {
		require.Contains(t, outcomes, name, "stdout:\n%s", res.Stdout)
		line := outcomes[name]
		assert.Equal(t, placed[name], line.Str("worker"), "the summary names where %s ran", name)
		assert.Equal(t, "run", line.Str("stage"))
		assert.Equal(t, "completed", line.Str("computation"))
		assert.Equal(t, "none", line.Str("publication"))
		assert.Equal(t, "orchestrator", line.Str("role"), "the line names its writer")
	}
	assert.EqualValues(t, 1, outcomes["core"]["exports"], "the provider's line counts its export")
	finished, isFinished := executionLine(res, "run finished")
	require.True(t, isFinished)
	assert.EqualValues(t, 3, finished["ran"])

	assert.Zero(t, fault.Matches(), "a sweep never pushes a release lock")
	assert.False(t, remoteHoldsLock(t, rig.origin))
	assert.Empty(t, rig.repo.TagList(), "and records nothing")
	assert.Empty(t, rig.branches(), "every coordination branch is closed")
	assert.Empty(t, executionWorkerRefs(t, rig.origin))
	stopAll(t, workers)
}

// TestExecutionRunSweepRunsWhileAReleaseHoldsTheLock: a sweep takes no lock,
// so a release holding the repository's lock does not stop it, and the sweep
// leaves that lock exactly as it found it (§28.10, vector 31).
func TestExecutionRunSweepRunsWhileAReleaseHoldsTheLock(t *testing.T) {
	rig := newExecutionSweepRig(t, executionSweepScript, func(cfg *models.File) {
		executionSweepPinnedToWorkers(cfg)
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
	})
	head := strings.TrimSpace(rig.repo.Git("rev-parse", "HEAD"))
	rig.repo.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	bareGit(t, rig.origin, "tag", "-a", lockTag, "-m", "a release is running", head)
	held := strings.TrimSpace(bareGit(t, rig.origin, "rev-parse", "refs/tags/"+lockTag))
	workers := rig.startWorkers([]string{executionNode}, 2)

	res := rig.sweep(nil)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Len(t, rig.nodesByPackage(), 3, "every task ran: %v", rig.runs())
	assert.Equal(t, held, strings.TrimSpace(bareGit(t, rig.origin, "rev-parse", "refs/tags/"+lockTag)),
		"the release's lock is the one that was there")
	stopAll(t, workers)
}

// TestExecutionRunSweepWithoutLinksIsLocalAndLockless: with no execution
// object, and with one that names no worker, a sweep is the sweep it always
// was: the same lines in the same order, every task here, no plan fixed, no
// summary, no lock and nothing on the remote.
func TestExecutionRunSweepWithoutLinksIsLocalAndLockless(t *testing.T) {
	var sequences [][]string
	for _, execution := range []*models.ExecutionConfig{
		nil,
		{SecretEnv: executionSecretEnv, Timeouts: &models.ExecutionTimeoutsConfig{Preflight: 30}},
	} {
		// One package at a time, so that the order of the lines is the plan's
		// and two sweeps can be compared line for line.
		rig := newExecutionSweepRig(t, executionSweepScript, func(cfg *models.File) {
			cfg.Execution = execution
			cfg.Concurrency = []int{1, 1}
		})
		fault := harness.NewGitFault(t, harness.GitFault{Pattern: lockPushPattern})

		res := rig.sweep(fault.Env())

		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		for _, name := range executionSweepPackages {
			assert.Equal(t, "orchestrator", rig.nodesByPackage()[name], "%s ran here", name)
		}
		assert.Equal(t, "from-core", executionProbeValues(rig, "export")["web"])
		for _, message := range []string{"plan fixed", "task outcome", "distributed execution summary"} {
			_, isPresent := executionLine(res, message)
			assert.False(t, isPresent, "a sweep with no link writes no %q line", message)
		}
		assert.Zero(t, fault.Matches(), "and pushes no lock")
		assert.Empty(t, executionWorkerRefs(t, rig.origin), "and writes nothing to the remote")
		var sequence []string
		for _, event := range executionEvents(res) {
			sequence = append(sequence, event.Str("message")+" "+event.Package()+" "+event.Str("stage"))
		}
		sequences = append(sequences, sequence)
	}
	assert.Equal(t, sequences[0], sequences[1], "an execution object with no link changes no line of a sweep")
}

// TestExecutionRunWorkerFlagNamesAMachineTheFileDoesNot: a pipeline names the
// machine it created a minute ago on the command line, and the sweep places
// its tasks there; `dispat status` with the same flag fixes and prints the
// plan's name and reaches no node; and a link stated on the command line is
// refused exactly where one stated in the file would be.
func TestExecutionRunWorkerFlagNamesAMachineTheFileDoesNot(t *testing.T) {
	secretOnly := func(cfg *models.File) {
		cfg.Execution = &models.ExecutionConfig{SecretEnv: executionSecretEnv,
			Timeouts: &models.ExecutionTimeoutsConfig{Preflight: 30}}
		executionSweepPinnedToWorkers(cfg)
	}
	rig := newExecutionSweepRig(t, executionSweepScript, secretOnly)
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.sweep(nil, "--worker", executionNode+"=file://"+rig.mailbox)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for _, name := range executionSweepPackages {
		assert.Equal(t, executionNode, rig.nodesByPackage()[name], "%s ran on the named machine", name)
	}
	assert.Empty(t, rig.branches())
	stopAll(t, []*executionWorker{worker})

	untouched, other := executionMailbox(t), executionMailbox(t)
	named := rig.repo.CommandEnv(rig.env(), "status", "--worker", "fresh=file://"+untouched)
	require.Equal(t, 0, named.Code, "stdout:\n%s\nstderr:\n%s", named.Stdout, named.Stderr)
	fixed, isFixed := executionLine(named, "plan fixed")
	require.True(t, isFixed, "status with a link names the plan\nstdout:\n%s", named.Stdout)
	renamed := rig.repo.CommandEnv(rig.env(), "status", "--worker", "another=file://"+other)
	again, _ := executionLine(renamed, "plan fixed")
	assert.Equal(t, fixed.Str("planDigest"), again.Str("planDigest"), "the links are no part of the digest")
	assert.Empty(t, executionMailboxBranches(t, untouched), "status probes and assigns nothing")
	assert.Empty(t, executionMailboxBranches(t, other))
	_, isFixedWithout := executionLine(rig.repo.CommandEnv(rig.env(), "status"), "plan fixed")
	assert.False(t, isFixedWithout, "and without a link there is no plan to name")
}

// TestExecutionRunWorkerFlagRefusals: every rule a configured link is held to,
// applied to one the command line states, before any task runs anywhere.
func TestExecutionRunWorkerFlagRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		execution *models.ExecutionConfig
		flag      string
		env       []string
		isBypass  bool
		code      int
		want      string
		diagnosis string
	}{
		"a value that is not name=endpoint": {
			flag: "build-a", code: 2, want: "name=endpoint"},
		"a configured link spelled again": {
			execution: &models.ExecutionConfig{SecretEnv: executionSecretEnv,
				Workers: []models.ExecutionWorkerConfig{{Name: executionNode, Endpoint: "file:///srv/mailbox"}}},
			flag: "BUILD-A=file:///srv/other", code: 1, want: "already used by", diagnosis: "E225"},
		"a credential in the endpoint": {
			execution: &models.ExecutionConfig{SecretEnv: executionSecretEnv},
			flag:      "w=https://user:hunter2@example.test/mailbox.git", code: 1, want: "endpoint",
			diagnosis: "E225"},
		"no signing secret named": {
			flag: "w=file:///srv/mailbox", code: 1, want: "secretEnv is required", diagnosis: "E225"},
		"a process under worker authority": {
			execution: &models.ExecutionConfig{SecretEnv: executionSecretEnv},
			flag:      "w=file:///srv/mailbox", env: executionWorkerMarker, code: 1,
			want: "worker authority", diagnosis: executionAuthorityCode},
		"a worker's own file": {
			execution: &models.ExecutionConfig{Role: models.ExecutionRoleWorker, Name: "w",
				Endpoint: "file:///srv/mailbox", SecretEnv: executionSecretEnv},
			flag: "other=file:///srv/mailbox", code: 1, want: "dispatches nothing",
			diagnosis: executionAuthorityCode},
		"the lock bypass beside a link": {
			execution: &models.ExecutionConfig{SecretEnv: executionSecretEnv},
			flag:      "w=file:///srv/mailbox", isBypass: true, code: 1,
			want: "none to dispatch a sweep through", diagnosis: "E225"},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionSweepRig(t, executionSweepScript,
				func(cfg *models.File) { cfg.Execution = tc.execution })
			env := rig.env(tc.env...)
			if tc.isBypass {
				env = append([]string{executionSecretEnv + "=" + executionSecret,
					executionBuildLogEnv + "=" + rig.builds}, tc.env...)
			}

			res := rig.repo.CommandEnv(env, "run", "tests", "--since", "all", "--worker", tc.flag,
				"--log-format", "json")

			require.Equal(t, tc.code, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, diagnosticText(res), tc.want)
			assert.NotContains(t, res.Stdout+res.Stderr, "hunter2", "a refused endpoint is never echoed")
			if tc.diagnosis != "" {
				assert.True(t, harness.IsCodePresent(executionEvents(res), tc.diagnosis),
					"the refusal carries %s\nstdout:\n%s\nstderr:\n%s", tc.diagnosis, res.Stdout, res.Stderr)
			}
			assert.Empty(t, rig.runs(), "no task ran anywhere")
			assert.Empty(t, rig.branches())
		})
	}
}

// executionCoverageScript writes one coverage file per package into the
// repository's `coverage/` folder, one line naming the node, beside a file
// every task writes with the same bytes; `quiet` writes nothing at all.
const executionCoverageScript = executionSweepScript + ` &&
case "$DISPAT_PACKAGE" in
  quiet) : ;;
  *) mkdir -p ../../coverage &&
     printf '%s from %s\n' "$DISPAT_PACKAGE" "${DISPAT_EXECUTION_NODE:-orchestrator}" \
       > "../../coverage/$DISPAT_PACKAGE.out" &&
     printf 'same\n' > ../../coverage/shared.txt ;;
esac`

// executionSweepOutputs declares the fixture's `coverage/` as the `tests`
// script's output and adds `quiet`, the package whose script writes nothing.
func executionSweepOutputs(cfg *models.File) {
	cfg.RunOutputs = map[string][]string{"tests": {"coverage"}}
	executionSweepPinnedToWorkers(cfg)
}

// TestExecutionRunOutputsMergeIntoTheOrchestratorsCheckout: every delegated
// task's coverage file comes back and is merged into the orchestrator's own
// `coverage/`: the file of the same path is replaced, a file no task wrote is
// kept, a file every task wrote with the same bytes is merged once, and a
// package whose script wrote nothing is admitted as an empty set.
func TestExecutionRunOutputsMergeIntoTheOrchestratorsCheckout(t *testing.T) {
	rig := newExecutionSweepRig(t, executionCoverageScript, executionSweepOutputs)
	rig.repo.SeedPackage("packages", "quiet")
	rig.repo.Commit("feat(quiet): a package with nothing to report")
	rig.repo.WriteFile("coverage/keep.txt", "kept\n")
	rig.repo.WriteFile("coverage/core.out", "stale\n")
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.sweep(nil)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	placed := rig.nodesByPackage()
	for _, name := range executionSweepPackages {
		assert.Equal(t, name+" from "+placed[name]+"\n", readRepoFile(t, rig.repo, "coverage/"+name+".out"),
			"%s's file came back from the node that wrote it", name)
	}
	assert.Equal(t, "kept\n", readRepoFile(t, rig.repo, "coverage/keep.txt"), "a file no task wrote is kept")
	assert.Equal(t, "same\n", readRepoFile(t, rig.repo, "coverage/shared.txt"),
		"identical bytes from every task are merged once")
	assert.NoFileExists(t, rig.repo.Path("coverage", "quiet.out"))
	outcomes := executionTaskOutcomes(res)
	require.Contains(t, outcomes, "quiet", "stdout:\n%s", res.Stdout)
	assert.Equal(t, "admitted", outcomes["quiet"].Str("outputs"), "a root the script did not write is an empty set")
	assert.Nil(t, outcomes["quiet"]["files"])
	assert.Equal(t, "admitted", outcomes["core"].Str("outputs"))
	assert.EqualValues(t, 2, outcomes["core"]["files"])
	merged := 0
	for _, event := range executionEvents(res) {
		if event.Str("message") == "outputs merged" {
			merged++
		}
	}
	assert.Equal(t, 4, merged, "each task's set is merged once, the empty one included")
	assert.Empty(t, rig.repo.TagList())
	stopAll(t, workers)
}

// executionConflictScript has `api` and `web` write one path with different
// bytes, each beside a file of its own, while `core` writes only its own.
const executionConflictScript = executionSweepScript + ` &&
mkdir -p ../../coverage &&
printf '%s\n' "$DISPAT_PACKAGE" > "../../coverage/$DISPAT_PACKAGE.out" &&
case "$DISPAT_PACKAGE" in
  api|web) printf '%s\n' "$DISPAT_PACKAGE" > ../../coverage/shared.txt ;;
esac`

// TestExecutionRunOutputsConflictLeavesNeitherFile: two tasks of one sweep
// that write one path with different bytes fail the sweep with E227 naming
// the path and both tasks, and the root holds neither task's file at that
// path; each task's set is installed whole or not at all, so neither
// disagreeing task's own file is merged either, while the task that agreed
// with everybody is (§28.10, vector 32).
func TestExecutionRunOutputsConflictLeavesNeitherFile(t *testing.T) {
	rig := newExecutionSweepRig(t, executionConflictScript, executionSweepOutputs)
	rig.repo.WriteFile("coverage/shared.txt", "before\n")
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.sweep(nil)

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	rejected, isRejected := executionLine(res, "outputs rejected")
	require.True(t, isRejected, "stdout:\n%s", res.Stdout)
	assert.Equal(t, "path-conflict", rejected.Str("reason"))
	assert.Equal(t, executionIntegrityCode, rejected.Code())
	assert.Equal(t, "io-integrity", rejected.Str("category"))
	text := diagnosticText(res)
	assert.Contains(t, text, "coverage/shared.txt", "the refusal names the path")
	assert.Contains(t, text, "api:run", "and both tasks")
	assert.Contains(t, text, "web:run")
	assert.Equal(t, "before\n", readRepoFile(t, rig.repo, "coverage/shared.txt"),
		"the root holds neither task's file at the disputed path")
	assert.NoFileExists(t, rig.repo.Path("coverage", "api.out"), "a disagreeing set is left out whole")
	assert.NoFileExists(t, rig.repo.Path("coverage", "web.out"))
	assert.Equal(t, "core\n", readRepoFile(t, rig.repo, "coverage/core.out"), "the undisputed set is merged")
	outcomes := executionTaskOutcomes(res)
	assert.Equal(t, "rejected", outcomes["api"].Str("outputs"))
	assert.Equal(t, "rejected", outcomes["web"].Str("outputs"))
	assert.Equal(t, "admitted", outcomes["core"].Str("outputs"))
	stopAll(t, workers)
}

// TestExecutionRunOutputsConfigRefusals: a root that is not a place a sweep
// may write is refused as the configuration is read or discovered, so `dispat
// status` reports it before any sweep writes anything.
func TestExecutionRunOutputsConfigRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		adjust func(*models.File)
		raw    func(*harness.Repo)
		want   string
	}{
		"a root leaving the repository": {
			adjust: func(cfg *models.File) { cfg.RunOutputs = map[string][]string{"tests": {"../coverage"}} },
			want:   "leaves the repository"},
		"a package folder": {
			adjust: func(cfg *models.File) { cfg.RunOutputs = map[string][]string{"tests": {"packages/core"}} },
			want:   `is or holds the folder of package "core"`},
		"a folder holding every package": {
			adjust: func(cfg *models.File) { cfg.RunOutputs = map[string][]string{"tests": {"packages"}} },
			want:   "is or holds the folder of package"},
		"a package's build output root": {
			adjust: func(cfg *models.File) {
				cfg.BuildOutputs = []string{"dist"}
				cfg.RunOutputs = map[string][]string{"tests": {"packages/core/dist"}}
			},
			want: `overlaps the build output "dist" of package "core"`},
		"a root holding a NUL byte": {
			adjust: func(cfg *models.File) { cfg.RunOutputs = map[string][]string{"tests": {"coverage\x00"}} },
			want:   "NUL byte"},
		"a root written with backslashes": {
			adjust: func(cfg *models.File) { cfg.RunOutputs = map[string][]string{"tests": {`coverage\unit`}} },
			want:   "slash-separated"},
		"a root naming a drive": {
			adjust: func(cfg *models.File) { cfg.RunOutputs = map[string][]string{"tests": {"C:/coverage"}} },
			want:   "colon"},
		"an absolute root": {
			adjust: func(cfg *models.File) { cfg.RunOutputs = map[string][]string{"tests": {"/srv/coverage"}} },
			want:   "relative to the repository root"},
		"a root reaching into repository metadata": {
			adjust: func(cfg *models.File) { cfg.RunOutputs = map[string][]string{"tests": {".git/coverage"}} },
			want:   "repository metadata"},
		"the repository root itself": {
			adjust: func(cfg *models.File) { cfg.RunOutputs = map[string][]string{"tests": {"."}} },
			want:   "names the repository root itself"},
		"an empty root": {
			adjust: func(cfg *models.File) { cfg.RunOutputs = map[string][]string{"tests": {""}} },
			want:   "names no folder"},
		"one root inside another": {
			adjust: func(cfg *models.File) {
				cfg.RunOutputs = map[string][]string{"tests": {"coverage", "coverage/unit"}}
			},
			want: "one root, or one inside the other"},
		"stated in a package folder's own file": {
			raw: func(repo *harness.Repo) {
				repo.WriteFile("packages/core/dispat.json", `{"runOutputs": {"tests": ["coverage"]}}`)
			},
			want: "runOutputs"},
	} {
		t.Run(name, func(t *testing.T) {
			adjust := func(*models.File) {}
			if tc.adjust != nil {
				adjust = tc.adjust
			}
			rig := newExecutionSweepRig(t, executionSweepScript, adjust)
			if tc.raw != nil {
				tc.raw(rig.repo)
			}

			res := rig.repo.CommandEnv(rig.env(), "status")

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, diagnosticText(res), tc.want)
		})
	}
}

// TestExecutionRunOutputsInASpaceAreUnknown: the key is root-only, so a space
// stating it is an unknown key rather than a sweep scoped to that space.
func TestExecutionRunOutputsInASpaceAreUnknown(t *testing.T) {
	rig := newExecutionSweepRig(t, executionSweepScript)
	rig.repo.WriteConfigRaw(map[string]any{
		"logFormat": "json",
		"scripts":   map[string]any{"tests": executionSweepScript},
		"spaces": map[string]any{"libs": map[string]any{
			"path": "packages", "runOutputs": map[string]any{"tests": []any{"coverage"}}}},
	})

	res := rig.repo.CommandEnv(rig.env(), "status")

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, diagnosticText(res), "runOutputs")
}

// executionSweepCraft turns the fake worker's default build set into a
// sweep's: the root is `coverage` relative to the repository, the one file is
// `coverage/app.out`, and the row's own change is applied on top.
func executionSweepCraft(change func(*executionCraftedOutputs)) func(*executionCraftedOutputs) {
	return func(c *executionCraftedOutputs) {
		c.entries = map[string]executionTreeFile{"coverage/app.out": {mode: "100644", content: "one\n"}}
		c.manifest = c.manifest.set("roots", []any{"coverage"}).set("entries", []any{
			executionCraftedEntry("coverage/app.out", "file", "0644", 4, executionDigestOf("one\n")),
		})
		change(c)
	}
}

// TestExecutionRunOutputsCraftedRefusals: a node reporting a sweep set nobody
// may merge is refused through the one validator every output set goes
// through, with the rule's own word, and nothing of it reaches the
// orchestrator's checkout.
func TestExecutionRunOutputsCraftedRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		craft  func(*executionCraftedOutputs)
		limits *models.ExecutionTransferConfig
		reason string
	}{
		"a file the tree does not hold": {
			craft: func(c *executionCraftedOutputs) {
				c.manifest = c.manifest.set("entries", []any{
					executionCraftedEntry("coverage/app.out", "file", "0644", 4, executionDigestOf("one\n")),
					executionCraftedEntry("coverage/zz.out", "file", "0644", 4, executionDigestOf("one\n")),
				}).set("files", 2).set("bytes", 8)
			},
			reason: "tree-missing"},
		"a path leaving its root": {
			craft: func(c *executionCraftedOutputs) {
				c.manifest = c.manifest.set("entries", []any{
					executionCraftedEntry("coverage/../../escape.out", "file", "0644", 4, executionDigestOf("one\n")),
				})
			},
			reason: "path-escape"},
		"a path under no declared root": {
			craft: func(c *executionCraftedOutputs) {
				c.entries = map[string]executionTreeFile{"reports/app.out": {mode: "100644", content: "one\n"}}
				c.manifest = c.manifest.set("entries", []any{
					executionCraftedEntry("reports/app.out", "file", "0644", 4, executionDigestOf("one\n")),
				})
			},
			reason: "path-outside-root"},
		"more bytes than the run allows": {
			craft:  func(*executionCraftedOutputs) {},
			limits: &models.ExecutionTransferConfig{MaxBytes: 2},
			reason: "too-many-bytes"},
		"a result describing no outputs at all": {
			craft:  func(c *executionCraftedOutputs) { c.isOutputsOmitted = true },
			reason: "bytes-missing"},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionSweepRig(t, executionSweepScript, func(cfg *models.File) {
				executionSweepOutputs(cfg)
				cfg.Execution.Workers = cfg.Execution.Workers[:1]
				cfg.Execution.Transfer = tc.limits
			})
			worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, executionSweepCraft(tc.craft))
			worker.serve()
			defer worker.close()

			res := rig.sweep(nil)

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			rejected, isRejected := executionLine(res, "outputs rejected")
			require.True(t, isRejected, "stdout:\n%s", res.Stdout)
			assert.Equal(t, tc.reason, rejected.Str("reason"))
			assert.Equal(t, executionIntegrityCode, rejected.Code())
			assert.NoDirExists(t, rig.repo.Path("coverage"), "nothing of the set was merged")
		})
	}
}

// TestExecutionRunSweepTaskTimesOutLikeABuild: a node that does not report a
// sweep task within `timeouts.task` fails that task exactly as it fails a
// build, the dependents are skipped, and the sweep leaves nothing behind.
func TestExecutionRunSweepTaskTimesOutLikeABuild(t *testing.T) {
	rig := newExecutionSweepRig(t, executionSweepScript+" && sleep 60", func(cfg *models.File) {
		executionSweepPinnedToWorkers(cfg)
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 3}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.sweep(nil)

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresentForPackage(executionEvents(res), executionIntegrityCode, "core"),
		"the task that did not report is failed with the integrity code\nstdout:\n%s", res.Stdout)
	assert.Equal(t, []string{"core"}, func() []string {
		var ran []string
		for name := range rig.nodesByPackage() {
			ran = append(ran, name)
		}
		return ran
	}(), "the dependents never ran")
	finished, isFinished := executionLine(res, "run finished")
	require.True(t, isFinished)
	assert.EqualValues(t, 1, finished["failed"])
	assert.EqualValues(t, 2, finished["skipped"])
	blocked := 0
	for _, event := range executionEvents(res) {
		if event.Str("message") == "task outcome" && event.Str("blockedBy") == "core" {
			blocked++
		}
	}
	assert.Equal(t, 2, blocked, "the summary names what blocked each skipped package")
	assert.Empty(t, rig.repo.TagList())
	stopAll(t, []*executionWorker{worker})
}

// TestExecutionRunSweepUnderRunOnlyOrchestratorStaysLocal: a package whose
// build may only run on the orchestrator keeps its sweep task here too, with
// the links configured and the node listening: the node answers the probe and
// is given nothing else, and the summary says every task ran here.
func TestExecutionRunSweepUnderRunOnlyOrchestratorStaysLocal(t *testing.T) {
	rig := newExecutionSweepRig(t, executionSweepScript, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyOrchestrator, Publish: models.RunOnlyBoth}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.sweep(nil)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for _, name := range executionSweepPackages {
		assert.Equal(t, "orchestrator", rig.nodesByPackage()[name], "%s ran here", name)
	}
	assert.Equal(t, "from-core", executionProbeValues(rig, "export")["api"])
	for name, line := range executionTaskOutcomes(res) {
		assert.Equal(t, true, line["here"], "%s ran on the orchestrator", name)
		assert.Empty(t, line.Str("worker"))
	}
	reply := stopAll(t, []*executionWorker{worker})[0]
	assert.NotContains(t, reply.Stdout, `"kind":"run"`, "the node was given no sweep task")
}

// TestExecutionRunSweepUnderRunOnlyWorkerWithoutLinksIsRefused: a package
// whose build may only run on a worker cannot have its sweep task run here
// either, so a sweep with no link refuses with E225 before any script runs.
func TestExecutionRunSweepUnderRunOnlyWorkerWithoutLinksIsRefused(t *testing.T) {
	rig := newExecutionSweepRig(t, executionSweepScript, func(cfg *models.File) {
		cfg.Execution = nil
		executionSweepPinnedToWorkers(cfg)
	})

	res := rig.sweep(nil)

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireExecutionRefusal(t, res, "E225", executionConfigurationCategory)
	assert.Contains(t, diagnosticText(res), "runOnly")
	assert.Empty(t, rig.runs(), "no script ran")
}

// TestExecutionRunSweepFailuresAreThePackagesOwn: a sweep task that fails,
// on a worker or on the orchestrator, fails its package the way a failing
// script always has: the sweep exits 1, the dependents are skipped, and the
// summary reports the failed computation where it happened.
func TestExecutionRunSweepFailuresAreThePackagesOwn(t *testing.T) {
	for name, tc := range map[string]struct {
		runOnly string
		worker  string
		isHere  bool
	}{
		"on a worker":         {runOnly: models.RunOnlyWorker, worker: executionNode},
		"on the orchestrator": {runOnly: models.RunOnlyOrchestrator, isHere: true},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionSweepRig(t, executionSweepScript+` && [ "$DISPAT_PACKAGE" != core ]`,
				func(cfg *models.File) {
					cfg.Execution.Workers = cfg.Execution.Workers[:1]
					cfg.RunOnly = &models.RunOnly{Build: tc.runOnly, Publish: models.RunOnlyBoth}
				})
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

			res := rig.sweep(nil)

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Equal(t, []string{"core"}, executionSweptPackages(rig), "the dependents never ran")
			finished, isFinished := executionLine(res, "run finished")
			require.True(t, isFinished)
			assert.EqualValues(t, 1, finished["failed"])
			assert.EqualValues(t, 2, finished["skipped"])
			outcomes := executionTaskOutcomes(res)
			require.Contains(t, outcomes, "core", "stdout:\n%s", res.Stdout)
			assert.Equal(t, "failed", outcomes["core"].Str("computation"))
			assert.Equal(t, tc.worker, outcomes["core"].Str("worker"))
			assert.Equal(t, tc.isHere, outcomes["core"]["here"])
			stopAll(t, []*executionWorker{worker})
		})
	}
}

// executionSweptPackages is every package whose sweep task ran anywhere.
func executionSweptPackages(rig *executionRig) []string {
	var ran []string
	for _, name := range executionSweepPackages {
		if _, isRun := rig.nodesByPackage()[name]; isRun {
			ran = append(ran, name)
		}
	}
	return ran
}

// TestExecutionRunSweepRefusesAPoolThatDoesNotAnswer: a link that does not
// answer the probe refuses the sweep with E225 before any task is placed,
// and the probe branches the sweep created are closed.
func TestExecutionRunSweepRefusesAPoolThatDoesNotAnswer(t *testing.T) {
	rig := newExecutionSweepRig(t, executionSweepScript, func(cfg *models.File) {
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 1}
	})

	res := rig.sweep(nil)

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	refused, isRefused := executionLine(res, "cannot dispatch this sweep")
	require.True(t, isRefused, "stdout:\n%s", res.Stdout)
	assert.Equal(t, "E225", refused.Code())
	assert.Equal(t, executionConfigurationCategory, refused.Str("category"))
	assert.Empty(t, rig.runs(), "no task was placed anywhere")
	assert.Empty(t, rig.branches(), "and the probes were closed")
}

// TestExecutionRunInterruptedSweepMergesNothing: an interrupted sweep
// withdraws the task a node is running and merges nothing it was carrying,
// because a root assembled from whichever tasks answered before the
// interrupt is a root nobody asked for.
func TestExecutionRunInterruptedSweepMergesNothing(t *testing.T) {
	rig := newExecutionSweepRig(t, executionCoverageScript+" && sleep 600", func(cfg *models.File) {
		executionSweepOutputs(cfg)
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 600, Cancel: 30}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	started := rig.repo.StartReleaseEnv(rig.env(), "run", "tests", "--since", "all")

	executionAwaitProbe(t, rig, executionNode)
	started.Signal(syscall.SIGINT)
	res := started.Wait()

	assert.NotEqual(t, 0, res.Code, "an interrupted sweep exits non-zero")
	_, isSkipped := executionLine(res, "run outputs not merged: the sweep was interrupted")
	assert.True(t, isSkipped, "stdout:\n%s", res.Stdout)
	_, isWithdrawn := executionLine(res, "attempt withdrawn")
	assert.True(t, isWithdrawn, "the running task was withdrawn\nstdout:\n%s", res.Stdout)
	assert.NoDirExists(t, rig.repo.Path("coverage"), "nothing was merged")
	stopAll(t, []*executionWorker{worker})
}

// TestExecutionRunOutputsMergeFailureFailsTheSweep: a set whose file cannot
// be written as a file in the orchestrator's checkout, because a folder sits
// at its path, is refused with E227 and merged not at all, the sweep exits 1,
// and every other set is still merged.
func TestExecutionRunOutputsMergeFailureFailsTheSweep(t *testing.T) {
	rig := newExecutionSweepRig(t, executionCoverageScript, func(cfg *models.File) {
		executionSweepOutputs(cfg)
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
	})
	rig.repo.WriteFile("coverage/core.out/keep.txt", "a folder where the file would go\n")
	workers := rig.startWorkers([]string{executionNode}, 2)

	res := rig.sweep(nil)

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	rejected, isRejected := executionLine(res, "outputs rejected")
	require.True(t, isRejected, "stdout:\n%s", res.Stdout)
	assert.Equal(t, "destination-component", rejected.Str("reason"))
	assert.Equal(t, "core:run", rejected.Str("task"))
	assert.Equal(t, executionIntegrityCode, rejected.Code())
	assert.Equal(t, "a folder where the file would go\n", readRepoFile(t, rig.repo, "coverage/core.out/keep.txt"))
	assert.FileExists(t, rig.repo.Path("coverage", "api.out"), "the other sets are merged")
	assert.Equal(t, "rejected", executionTaskOutcomes(res)["core"].Str("outputs"))
	stopAll(t, workers)
}
