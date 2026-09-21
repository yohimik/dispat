// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57, fourth part: the build dependencies a run does not release, the
// edges the release rules do not propagate along, and the edge that says a
// consumer still waits for its provider's registry (CCME §28.5).
//
// The fixture is the previous one with a history behind it. Every package is
// tagged at the bootstrap commit, so a scenario decides which of them this run
// releases simply by committing against them: a consumer that changed is a
// consumer whose provider has not, which is the whole situation §28.5 is
// about. `assets` still builds a `dist` its consumers refuse to build without,
// so "the bytes reached the consumer" is something the run either did or
// failed on rather than something a log claims.
//
// What must not happen is asserted as hard as what must. A prepared provider
// is a package that produced bytes and released nothing, so the scenarios read
// the tags, the changelog files and the delivered events, and each of the
// three has to say that `assets` is exactly where it was.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionPreparePackages is the fixture's shape: `assets` is consumed by
// `ui` and `docs`, and `solo` is consumed by nobody, which is what makes
// "an unrelated package released anyway" a claim this fixture can carry.
var executionPreparePackages = []string{"assets", "docs", "solo", "ui"}

// executionPrepareBaseline is the version every package is tagged at before a
// scenario commits anything, which is what lets one commit decide what this
// run releases.
const executionPrepareBaseline = "1.0.0"

// executionPrepareKind is the word a preparation travels under, in the task
// name, in the assignment and in the branch label.
const executionPrepareKind = "prepare"

// executionPrepareBuild is the build script of this fixture: every package
// records where it ran and writes a `dist`, and the consumers of `assets`
// refuse to build without the exact bytes it wrote.
const executionPrepareBuild = executionRecordingScript + " &&\n" + executionPrepareBuildBody

// executionPrepareFailingBuild is the same script with the provider's build
// failing after it has recorded itself, which is what makes "it was built once
// and not again" a claim the fixture can carry about a failure.
const executionPrepareFailingBuild = executionRecordingScript + ` &&
{ [ "$DISPAT_PACKAGE" != assets ] || exit 3; } &&
` + executionPrepareBuildBody

// executionPrepareBuildBody is what a build of this fixture does once it has
// recorded that it ran.
const executionPrepareBuildBody = `mkdir -p dist &&
case "$DISPAT_PACKAGE" in
  assets)
    printf 'assets %s %s\n' "$DISPAT_NEW_VERSION" "${DISPAT_EXECUTION_NODE:-orchestrator}" > dist/bundle.js ;;
  solo)
    printf 'solo %s\n' "$DISPAT_NEW_VERSION" > dist/solo.txt ;;
  *)
    test -f ../assets/dist/bundle.js &&
    cp ../assets/dist/bundle.js dist/from-assets.txt ;;
esac &&
printf '%s %s %s\n' probe-inputs "$DISPAT_PACKAGE" \
  "$(cat dist/* | tr ' ' '_' | tr '\n' '+')" >> "$DISPAT_IT_EXECUTION_LOG"`

// newExecutionPrepareWorkspace seeds the fixture and tags every package at the
// bootstrap commit, so that a scenario's own commits are the only pending work
// in the repository.
//
// The adjustment is handed the repository as well as the configuration,
// because half of these scenarios need a script whose path only exists once
// the repository does: a timing mark, a file a build waits on.
func newExecutionPrepareWorkspace(t *testing.T,
	adjust ...func(*harness.Repo, *models.File)) *executionRig {
	t.Helper()
	repo := harness.New(t)
	mailbox := executionMailbox(t)
	cfg := harness.BaseFile(4, 4)
	cfg.BuildOutputs = []string{"dist"}
	cfg.Scripts = map[string]models.Script{
		"build": {executionPrepareBuild}, "publish": {"echo publishing"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "ui", Provider: "assets"},
		{Consumer: "docs", Provider: "assets"},
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
		change(repo, &cfg)
	}
	for _, name := range executionPreparePackages {
		repo.SeedPackage("packages", name)
	}
	repo.WriteConfigModel(cfg)
	repo.WriteFile(".gitignore", "dist/\n")
	repo.Commit(fmt.Sprintf("feat(%s): bootstrap the workspace",
		strings.Join(executionPreparePackages, ",")))
	for _, name := range executionPreparePackages {
		tagExecutionBaseline(repo, name, executionPrepareBaseline)
	}
	return newExecutionRigOver(t, repo, mailbox)
}

// tagExecutionBaseline records one package as already released at a version,
// which is how these scenarios get a repository with a history without
// spending a release on making one.
func tagExecutionBaseline(repo *harness.Repo, name, version string) {
	repo.Git("tag", "-a", name+"@"+version, "-m", "chore(release): "+name+"@"+version)
}

// commitTo writes a file inside every named package and commits them under one
// subject naming exactly those scopes, which is how a scenario decides what
// this run releases.
func (r *executionRig) commitTo(subject string, packages ...string) {
	r.t.Helper()
	for _, name := range packages {
		r.repo.WriteFile(filepath.Join("packages", name, "work.txt"), subject+"\n")
	}
	r.repo.Commit(fmt.Sprintf("feat(%s): %s", strings.Join(packages, ","), subject))
}

// commitPropagatingTo is commitTo with the depth sigil that carries the bump
// on to the consumers of the named packages, which is what makes a claim about
// which edges propagation follows a claim about this repository.
func (r *executionRig) commitPropagatingTo(subject string, packages ...string) {
	r.t.Helper()
	for _, name := range packages {
		r.repo.WriteFile(filepath.Join("packages", name, "work.txt"), subject+"\n")
	}
	r.repo.Commit(fmt.Sprintf("feat(%s)^: %s", strings.Join(packages, ","), subject))
}

// executionPlannedPackages is what `dispat status` says this repository would
// release, which is the only authority on whether a package is in the plan.
func executionPlannedPackages(t *testing.T, rig *executionRig) []string {
	t.Helper()
	status := rig.repo.CommandEnv(rig.env(), "status", "--log-format", "json")
	require.Equal(t, 0, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	var planned []string
	for _, event := range executionEvents(status) {
		// The graph marks a package it would release as changed and every
		// other one as unchanged, and the second word ends in the first, which
		// is why the two are told apart rather than searched for.
		message := event.Str("message")
		if event.Package() != "" && message != "unchanged" && strings.HasSuffix(message, "changed") {
			planned = append(planned, event.Package())
		}
	}
	return planned
}

// executionPrepareTasks are the preparations this run finished, read off the
// lines that name the work rather than off the branch names: a branch name is
// a routing hint, and the task is what the run calls the work.
func executionPrepareTasks(res harness.RunResult) []string {
	var tasks []string
	for _, event := range executionEvents(res) {
		if event.Str("message") == "task finished" &&
			strings.HasSuffix(event.Str("task"), ":"+executionPrepareKind) {
			tasks = append(tasks, event.Str("task"))
		}
	}
	return tasks
}

// startWorkersHolding starts one node per name with variables of its own in
// its environment, which is what a claim about a declared value resolving on
// the node rather than here needs: the orchestrator must not be able to
// resolve it.
func (r *executionRig) startWorkersHolding(names []string, capacity int,
	extraEnv ...string) []*executionWorker {
	r.t.Helper()
	workers := make([]*executionWorker, 0, len(names))
	for _, name := range names {
		workers = append(workers, r.startWorker(executionWorkerConfig(r.mailbox,
			func(settings *models.ExecutionConfig) {
				settings.Name = name
				settings.Concurrency = models.Int(capacity)
			}), 0, extraEnv...))
	}
	return workers
}

// executionPreparedReports are the summary lines a run writes about the
// providers it built without releasing them.
func executionPreparedReports(res harness.RunResult) []harness.Event {
	var reports []harness.Event
	for _, event := range executionEvents(res) {
		if event.Str("message") == "provider prepared, not released" {
			reports = append(reports, event)
		}
	}
	return reports
}

// TestExecutionUnselectedProviderIsPreparedNotReleased is conformance vector 9:
// a consumer's build reads what its provider produces whether or not this run
// releases the provider, so the run builds the provider once, hands its bytes
// to every consumer, and releases nothing of it.
func TestExecutionUnselectedProviderIsPreparedNotReleased(t *testing.T) {
	sink := newWebhookSink(t)
	rig := newExecutionPrepareWorkspace(t, func(_ *harness.Repo, cfg *models.File) {
		cfg.Webhooks = []models.WebhookConfig{{URL: sink.srv.URL}}
	})
	rig.commitTo("the page and the manual use the new asset", "docs", "ui")
	assert.ElementsMatch(t, []string{"docs", "ui"}, executionPlannedPackages(t, rig),
		"the provider is not in the plan: only its consumers changed")
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 1, executionBuildCount(rig, "assets"),
		"the provider was built once for both consumers: %v", rig.runs())
	assert.Equal(t, []string{"assets:" + executionPrepareKind}, executionPrepareTasks(res),
		"and it was built as a preparation rather than as a release\nstdout:\n%s", res.Stdout)

	produced := executionProbeValues(rig, "inputs")
	require.Contains(t, produced, "assets")
	bundle := produced["assets"]
	assert.Contains(t, bundle, executionPrepareBaseline,
		"a prepared provider builds at the version it already carries")
	assert.Equal(t, bundle, produced["ui"], "ui read exactly the bytes the preparation produced")
	assert.Equal(t, bundle, produced["docs"], "and so did docs")

	assert.ElementsMatch(t, []string{
		"assets@" + executionPrepareBaseline, "docs@" + executionPrepareBaseline,
		"solo@" + executionPrepareBaseline, "ui@" + executionPrepareBaseline,
		"docs@1.1.0", "ui@1.1.0",
	}, rig.repo.TagList(), "the provider carries no new tag")
	assert.NoFileExists(t, rig.repo.Path("packages", "assets", "CHANGELOG.md"),
		"and no changelog entry")
	for _, payload := range sink.payloads(t) {
		assert.NotEqualf(t, "assets", payload["package"],
			"no event of this run claims anything about the provider: %v", payload)
	}

	reports := executionPreparedReports(res)
	require.Len(t, reports, 1, "the run reports what it built and did not release\nstdout:\n%s", res.Stdout)
	assert.Equal(t, "assets", reports[0].Package())
	assert.Equal(t, "completed", reports[0].Str("computation"))
	assert.Equal(t, "admitted", reports[0].Str("outputs"))
	assert.Equal(t, "none", reports[0].Str("publication"))
	assert.Empty(t, rig.branches(), "the run closed every coordination branch it created")
	stopAll(t, workers)
}

// TestExecutionPreparedProviderFailureBlocksItsConsumers: a provider that
// could not be built is a prerequisite that was not met, so every consumer of
// it fails at its build stage with the integrity code naming the provider, the
// provider is not built again for the second consumer, and a package that
// reads none of it releases as usual.
func TestExecutionPreparedProviderFailureBlocksItsConsumers(t *testing.T) {
	rig := newExecutionPrepareWorkspace(t, func(_ *harness.Repo, cfg *models.File) {
		cfg.Scripts["build"] = models.Script{executionPrepareFailingBuild}
	})
	rig.commitTo("the page, the manual and the standalone all changed", "docs", "solo", "ui")
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for _, name := range []string{"ui", "docs"} {
		assert.Truef(t, harness.IsCodePresentForPackage(executionEvents(res), executionIntegrityCode, name),
			"%s failed with the integrity code\nstdout:\n%s", name, res.Stdout)
	}
	assert.Equal(t, 1, executionBuildCount(rig, "assets"),
		"the failed preparation was not attempted again for the second consumer: %v", rig.runs())
	for _, event := range executionEvents(res) {
		if event.Code() != executionIntegrityCode || event.Package() == "" {
			continue
		}
		assert.Containsf(t, event.Str("error"), "assets",
			"the failure names the provider that could not be built: %v", event)
	}
	assert.ElementsMatch(t, []string{"build", "build"}, executionFailedStages(res),
		"both consumers failed at their build stage\nstdout:\n%s", res.Stdout)
	assert.True(t, rig.repo.IsTagged("solo@1.1.0"),
		"a package that reads nothing of the provider released: %v", rig.repo.TagList())
	assert.False(t, rig.repo.IsTagged("ui@1.1.0"))
	assert.False(t, rig.repo.IsTagged("docs@1.1.0"))

	reports := executionPreparedReports(res)
	require.Len(t, reports, 1)
	assert.Equal(t, "failed", reports[0].Str("computation"))
	assert.Equal(t, "none", reports[0].Str("outputs"))
	stopAll(t, workers)
}

// TestExecutionDevDependencyOutputReachesConsumer is conformance vector 22a: a
// build-only edge supplies bytes exactly as a runtime edge does, even where
// the release rules propagate nothing along it. The propagation kinds decide
// whether a change in the provider releases the consumer; they decide nothing
// about what the consumer's build may read.
func TestExecutionDevDependencyOutputReachesConsumer(t *testing.T) {
	rig := newExecutionPrepareWorkspace(t, func(_ *harness.Repo, cfg *models.File) {
		cfg.Dependencies = []models.DependencyConfig{
			{Consumer: "ui", Provider: "assets"},
			{Consumer: "docs", Provider: "assets", Kind: "devDependencies"},
		}
	})

	rig.commitPropagatingTo("the asset itself changed", "assets")
	planned := executionPlannedPackages(t, rig)
	assert.Contains(t, planned, "assets")
	assert.Contains(t, planned, "ui", "a runtime consumer follows its provider")
	assert.NotContains(t, planned, "docs",
		"a development consumer does not: the propagation kinds exclude the edge")

	// The release of the provider and of the consumer that follows it is taken
	// as read, so that the run under test is one where the provider is in no
	// plan at all and the development edge is the only thing left.
	tagExecutionBaseline(rig.repo, "assets", "1.1.0")
	tagExecutionBaseline(rig.repo, "ui", "1.1.0")
	rig.commitTo("the manual changed on its own", "docs")
	assert.Equal(t, []string{"docs"}, executionPlannedPackages(t, rig))
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"assets:" + executionPrepareKind}, executionPrepareTasks(res),
		"the development provider was built although nothing propagates along the edge")
	produced := executionProbeValues(rig, "inputs")
	require.Contains(t, produced, "docs")
	assert.Equal(t, produced["assets"], produced["docs"],
		"and the consumer's build read exactly what it produced")
	assert.True(t, rig.repo.IsTagged("docs@1.1.0"))
	assert.Equal(t, 2, rig.repo.TagCount("assets@"),
		"the provider released nothing: %v", rig.repo.TagList())
	stopAll(t, workers)
}

// TestExecutionDependencyCycleFailsBeforeDispatch is conformance vector 22b: a
// cyclic dependency graph has no publish order, so the run is refused where
// every other repository-scoped error is refused, with nothing offered to a
// mailbox and no lock left behind.
func TestExecutionDependencyCycleFailsBeforeDispatch(t *testing.T) {
	rig := newExecutionPrepareWorkspace(t, func(_ *harness.Repo, cfg *models.File) {
		cfg.Dependencies = append(cfg.Dependencies,
			models.DependencyConfig{Consumer: "assets", Provider: "ui"})
	})
	rig.commitTo("both ends of the cycle changed", "assets", "ui")

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(executionEvents(res), "E200"),
		"a cyclic graph is refused before a plan exists\nstdout:\n%s", res.Stdout)
	assert.Empty(t, rig.runs(), "no stage of any package ran")
	assert.Empty(t, rig.branches(), "and the mailbox was never written to")
	assert.Empty(t, executionLockRefs(t, rig.origin), "and no lock was left on the remote")
}

// executionLockRefs are the release-lock refs a remote holds, which after a
// refused run is none.
func executionLockRefs(t *testing.T, bare string) []string {
	t.Helper()
	var held []string
	for _, ref := range strings.Fields(bareGit(t, bare, "for-each-ref", "--format=%(refname)")) {
		if strings.Contains(ref, "dispat-release-lock") {
			held = append(held, ref)
		}
	}
	return held
}

// newExecutionRegistryEdgeWorkspace is the fixture both registry-edge
// scenarios are driven through: every provider relation is the publish one, so
// a consumer's first task waits for its provider's publication, and every
// stage records the window it occupied so that "before" is a measurement
// rather than a claim.
//
// The provider's publish occupies a window of its own, because a publication
// that took no time at all would make "the consumer built afterwards" true of
// a run that never waited for anything.
func newExecutionRegistryEdgeWorkspace(t *testing.T, assetsPublish string) *executionRig {
	t.Helper()
	return newExecutionPrepareWorkspace(t, func(repo *harness.Repo, cfg *models.File) {
		mark := repo.TsmarkScript("timeline.log", "$DISPAT_PACKAGE-$DISPAT_STAGE", time.Second)
		cfg.IsBuildWaitingPublish = models.StageRelationOf(true)
		cfg.Scripts["build"] = models.Script{executionPrepareBuild, mark}
		cfg.Scripts["publish"] = models.Script{
			`case "$DISPAT_PACKAGE" in assets) ` + assetsPublish + ` ;; esac`, mark,
		}
	})
}

// TestExecutionRegistryEdgeStillWaitsForPublication is conformance vector 11:
// carrying a provider's bytes to its consumer weakens nothing about the
// registry. A space that declares the publish relation still holds every
// consumer's build behind its provider's publication, and the bytes travel as
// well, so the two prerequisites hold at once.
func TestExecutionRegistryEdgeStillWaitsForPublication(t *testing.T) {
	rig := newExecutionRegistryEdgeWorkspace(t, "echo publishing")
	rig.commitTo("everything moved", "assets", "docs", "ui")
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	timeline := rig.repo.Timeline("timeline.log")
	published := harness.Find(t, timeline, "assets-publish")
	for _, name := range []string{"ui", "docs"} {
		harness.AssertSequential(t, published, harness.Find(t, timeline, name+"-build"))
	}
	produced := executionProbeValues(rig, "inputs")
	require.Contains(t, produced, "assets")
	assert.Equal(t, produced["assets"], produced["ui"],
		"and the consumer still received the provider's declared bytes")
	stopAll(t, workers)
}

// TestExecutionRegistryEdgeWithdrawsAConsumerWhosePublicationFailed is the
// other half of vector 11: a provider that never published leaves its
// consumers nothing to follow, so they are never placed on any node at all and
// are reported blocked rather than failed.
func TestExecutionRegistryEdgeWithdrawsAConsumerWhosePublicationFailed(t *testing.T) {
	rig := newExecutionRegistryEdgeWorkspace(t, "exit 4")
	rig.commitTo("everything moved", "assets", "docs", "ui")
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for _, name := range []string{"ui", "docs"} {
		assert.Truef(t, harness.IsCodePresentForPackage(executionEvents(res), "W194", name),
			"%s was reported blocked\nstdout:\n%s", name, res.Stdout)
		assert.Emptyf(t, executionPlacements(res, name+":build"),
			"and its build was never placed anywhere: %v", rig.runs())
		assert.Equalf(t, 0, executionBuildCount(rig, name),
			"nor did it run anywhere: %v", rig.runs())
	}
	assert.False(t, rig.repo.IsTagged("assets@1.1.0"), "the provider published nothing")
	assert.False(t, rig.repo.IsTagged("ui@1.1.0"))
	assert.False(t, rig.repo.IsTagged("docs@1.1.0"))
	stopAll(t, workers)
}

// executionPlacements are the lines saying one task was placed somewhere, on a
// node or on the machine that started the run.
func executionPlacements(res harness.RunResult, task string) []harness.Event {
	var placed []harness.Event
	for _, event := range executionEvents(res) {
		if event.Str("task") != task {
			continue
		}
		message := event.Str("message")
		if message == "task assigned" || message == "task placed on the node that started the run" {
			placed = append(placed, event)
		}
	}
	return placed
}

// TestExecutionPreparationLeavesThePlanDigestUnchanged: building a provider
// the run does not release adds nothing to Plan(I). The digest the run
// executes is the digest a reader was shown before it started, which is what
// makes the plan a node is held to the plan an operator read.
func TestExecutionPreparationLeavesThePlanDigestUnchanged(t *testing.T) {
	rig := newExecutionPrepareWorkspace(t)
	rig.commitTo("the page and the manual use the new asset", "docs", "ui")
	status := rig.repo.CommandEnv(rig.env(), "status", "--log-format", "json")
	require.Equal(t, 0, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	want := planDigestOf(t, status)
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, want, planDigestOf(t, res),
		"the run that prepared a provider executed the plan the reader was shown")
	require.Len(t, executionPrepareTasks(res), 1, "and it really did prepare one")
	assert.NotContains(t, executionPlannedPackages(t, rig), "assets",
		"and the provider is no more pending afterwards than it was before")
	stopAll(t, workers)
}

// executionPrepareEnvProbe records what a prepared build was told about the
// package it was building, which is the one thing a script of that package
// could use to tell a preparation from a release.
const executionPrepareEnvProbe = `printf '%s %s %s\n' probe-env "$DISPAT_PACKAGE" ` +
	`"$DISPAT_STAGE/$DISPAT_BUMP/$DISPAT_NEW_VERSION/${TRAVELLED:-unset}" >> "$DISPAT_IT_EXECUTION_LOG"`

// TestExecutionAPreparedBuildIsToldItIsNotReleasing: a prepared provider's
// scripts read the environment `dispat run` gives the same package, so a
// script cannot behave differently because somebody else's release happened to
// need its output. The declared environment still resolves on the node that
// runs the command, which is what keeps a secret out of a mailbox.
func TestExecutionAPreparedBuildIsToldItIsNotReleasing(t *testing.T) {
	rig := newExecutionPrepareWorkspace(t, func(_ *harness.Repo, cfg *models.File) {
		cfg.Scripts["build"] = models.Script{executionPrepareBuild, executionPrepareEnvProbe}
		space := cfg.Spaces["libs"]
		space.Env = map[string]string{"TRAVELLED": "$DISPAT_IT_EXECUTION_NODE_NAME"}
		cfg.Spaces["libs"] = space
	})
	rig.commitTo("the page uses the new asset", "ui")
	workers := rig.startWorkersHolding([]string{executionNode, executionSecondNode}, 2,
		"DISPAT_IT_EXECUTION_NODE_NAME=resolved-on-the-node")

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	environments := executionProbeValues(rig, "env")
	require.Contains(t, environments, "assets")
	assert.Equal(t, "build/none/"+executionPrepareBaseline+"/resolved-on-the-node", environments["assets"],
		"the prepared build ran the build stage, bumped nothing, and kept the version it carries")
	assert.Equal(t, "build/minor/1.1.0/resolved-on-the-node", environments["ui"],
		"and the consumer's own release is a release")
	assert.NotContains(t, res.Stdout, "resolved-on-the-node",
		"a declared value is resolved on the node and never written here")
	assert.NotContains(t, executionMailboxDocuments(t, rig.mailbox), "resolved-on-the-node",
		"and never reaches a mailbox")
	stopAll(t, workers)
}

// stageRelationNoneBuild is the infrastructure build of the `none` fixture: it
// refuses to finish until the application build has started, which is what
// turns "these two overlapped" into something the run either did or could not
// finish at all.
const stageRelationNoneBuild = executionRecordingScript + ` &&
` + stageRelationGateWaitEnv + ` &&
mkdir -p dist && printf 'infra %s\n' "$DISPAT_NEW_VERSION" > dist/plan.tf`

// stageRelationGateWaitEnv blocks until the gate file exists, bounded so that
// a missing overlap fails an assertion rather than hanging the suite.
const stageRelationGateWaitEnv = `i=0; while [ ! -f "$DISPAT_IT_EXECUTION_GATE" ] && [ $i -lt 600 ]; ` +
	`do sleep 0.05; i=$((i+1)); done`

// stageRelationNoneConsumerBuild is the application build: it opens the gate
// and records whether the provider's declared output folder was in the
// checkout it was given.
const stageRelationNoneConsumerBuild = executionRecordingScript + ` &&
touch "$DISPAT_IT_EXECUTION_GATE" &&
{ [ -d ../../infra/infra/dist ] && state=present || state=absent; } &&
printf '%s %s %s\n' probe-inputs "$DISPAT_PACKAGE" "$state" >> "$DISPAT_IT_EXECUTION_LOG"`

// newStageRelationNoneWorkspace is the worked example of the `none` relation
// on two machines: one infrastructure package whose consumers read nothing it
// builds, and one application that deploys onto it.
func newStageRelationNoneWorkspace(t *testing.T) *executionRig {
	t.Helper()
	repo := harness.New(t)
	mailbox := executionMailbox(t)
	mark := repo.TsmarkScript("timeline.log", "$DISPAT_PACKAGE-$DISPAT_STAGE", 0)
	cfg := harness.BaseFile(3, 3)
	cfg.Scripts = map[string]models.Script{
		"infra-build": {stageRelationNoneBuild, mark},
		"app-build":   {stageRelationNoneConsumerBuild, mark},
		"publish":     {mark},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"infra": {Path: models.PathList{"packages/infra"},
			BuildOutputs:          []string{"dist"},
			IsBuildWaitingPublish: &models.StageRelation{Build: models.StageWaitNone},
			Flow: &models.SpaceFlowConfig{
				Build: []string{"infra-build"}, Publish: []string{"publish"}}},
		"apps": {Path: models.PathList{"packages/apps"},
			Flow: &models.SpaceFlowConfig{
				Build: []string{"app-build"}, Publish: []string{"publish"}}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "front", Provider: "infra"}}
	// Both builds are pinned to a node, because the claim is about what a
	// machine that was handed a checkout found in it: a build placed here
	// would read the folder the other build had just written.
	cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker, Publish: models.RunOnlyBoth}
	cfg.Execution = &models.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers: []models.ExecutionWorkerConfig{
			{Name: executionNode, Endpoint: "file://" + mailbox},
			{Name: executionSecondNode, Endpoint: "file://" + mailbox},
		},
		Timeouts: &models.ExecutionTimeoutsConfig{Preflight: 30},
	}
	repo.SeedPackage("packages/infra", "infra")
	repo.SeedPackage("packages/apps", "front")
	repo.WriteConfigModel(cfg)
	repo.WriteFile(".gitignore", "dist/\n")
	repo.Commit("feat(infra,front): the application deploys onto the infrastructure")
	for _, name := range []string{"infra", "front"} {
		tagExecutionBaseline(repo, name, executionPrepareBaseline)
	}
	return newExecutionRigOver(t, repo, mailbox)
}

// TestStageRelationNoneCarriesNoBuildOutputs is goal 60's claim under §28: a
// relation that says a consumer's build reads nothing the provider builds is a
// declaration the transport obeys. The two builds run at once on two machines,
// the consumer's checkout never holds the provider's declared output folder,
// and the publications still follow one another.
func TestStageRelationNoneCarriesNoBuildOutputs(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "front-build.gate")
	rig := newStageRelationNoneWorkspace(t)
	rig.commitTo("both ends moved", "front", "infra")
	workers := rig.startWorkersHolding([]string{executionNode, executionSecondNode}, 2,
		executionGateEnv+"="+gate)

	res := rig.release(executionGateEnv + "=" + gate)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	placed := rig.nodesByPackage()
	assert.Contains(t, []string{executionNode, executionSecondNode}, placed["front"],
		"the consumer built on a node: %v", rig.runs())
	timeline := rig.repo.Timeline("timeline.log")
	// The gate is the evidence: the provider's build could not finish until
	// the consumer's had started, so a run that held the consumer back would
	// have timed out rather than produced this timeline.
	harness.AssertOverlaps(t, harness.Find(t, timeline, "infra-build"),
		harness.Find(t, timeline, "front-build"))
	assert.Equal(t, "absent", executionProbeValues(rig, "inputs")["front"],
		"the consumer's checkout never held the provider's declared outputs")
	harness.AssertSequential(t, harness.Find(t, timeline, "infra-publish"),
		harness.Find(t, timeline, "front-publish"))
	assert.ElementsMatch(t, []string{
		"infra@" + executionPrepareBaseline, "front@" + executionPrepareBaseline,
		"infra@1.1.0", "front@1.1.0",
	}, rig.repo.TagList())
	stopAll(t, workers)
}

// TestStageRelationNoneNeedsNoPreparation: a provider nobody reads is a
// provider nothing has to build. A run whose only pending work is behind a
// `none` relation prepares nothing at all, because the bytes it would carry
// are bytes the consumer declared it does not read.
func TestStageRelationNoneNeedsNoPreparation(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "front-build.gate")
	rig := newStageRelationNoneWorkspace(t)
	// The gate is opened before the run: nothing here waits for the
	// infrastructure build, because this run has none to wait for.
	require.NoError(t, os.WriteFile(gate, nil, 0o644))
	rig.commitTo("only the application moved", "front")
	assert.Equal(t, []string{"front"}, executionPlannedPackages(t, rig))
	workers := rig.startWorkersHolding([]string{executionNode, executionSecondNode}, 2,
		executionGateEnv+"="+gate)

	res := rig.release(executionGateEnv + "=" + gate)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Empty(t, executionPrepareTasks(res),
		"nothing was built for a provider the consumer reads nothing of\nstdout:\n%s", res.Stdout)
	assert.Empty(t, executionPreparedReports(res))
	assert.Equal(t, 0, executionBuildCount(rig, "infra"), "runs: %v", rig.runs())
	assert.Equal(t, "absent", executionProbeValues(rig, "inputs")["front"])
	assert.True(t, rig.repo.IsTagged("front@1.1.0"))
	stopAll(t, workers)
}

// executionMailboxDocuments is every protocol document a mailbox holds, as one
// string, for the claims that are about what a branch does not carry.
func executionMailboxDocuments(t *testing.T, mailbox string) string {
	t.Helper()
	var carried strings.Builder
	for _, line := range strings.Split(bareGit(t, mailbox, "rev-list", "--all", "--objects"), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasSuffix(fields[1], ".json") {
			continue
		}
		carried.WriteString(bareGit(t, mailbox, "cat-file", "-p", fields[0]))
	}
	return carried.String()
}
