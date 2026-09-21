// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57, fourth part: `runOnly`, which says where a package's build and its
// publish are allowed to run.
//
// The operator's reason for the key is trust rather than speed: a build that
// signs its artefact, a publish that logs in, a stage that reads a credential
// no other machine has. So the claims here are about placement being obeyed.
// A stage pinned to this machine runs here and nowhere else; a stage pinned
// to a worker runs there and is refused outright when there is no worker; and
// the default leaves the run free to use either, preferring a worker and
// falling back here rather than queueing behind one.
//
// The second half of the key is that a pinned build is not a lesser build. A
// build the run keeps produces the same verified, transferable output set a
// delegated one does, so the packages that consume it are handed the same
// bytes and cannot tell where they came from.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionOrchestratorLabel is what the fixture's scripts record when they
// ran on the machine the release was started on: a task carries the node
// variable and this machine does not, and its absence is the claim.
const executionOrchestratorLabel = "orchestrator"

// placedOn is one runOnly value as the config model carries it.
func placedOn(build, publish string) *models.RunOnly {
	return &models.RunOnly{Build: build, Publish: publish}
}

// pinPackages gives named packages a placement of their own, through the root
// file's `packages` override layer.
func pinPackages(placements map[string]*models.RunOnly) func(*models.File) {
	return func(cfg *models.File) {
		if cfg.Packages == nil {
			cfg.Packages = map[string]models.PackageConfig{}
		}
		for name, placement := range placements {
			entry := cfg.Packages[name]
			entry.RunOnly = placement
			cfg.Packages[name] = entry
		}
	}
}

// TestExecutionOrchestratorOnlyBuildFeedsWorkers: a package pinned to the
// machine the release was started on is built there, once, and the packages
// that consume it are built on workers from exactly the bytes it produced.
// That is the whole promise of the pin: it costs the run a worker, not the
// transfer.
func TestExecutionOrchestratorOnlyBuildFeedsWorkers(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		// Which branch the consumers were handed is a debug decision rather
		// than user-visible progress, and it is part of the claim.
		cfg.LogLevel = "debug"
	}, pinPackages(map[string]*models.RunOnly{
		"assets": placedOn(models.RunOnlyOrchestrator, models.RunOnlyOrchestrator),
	}))
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	placed := rig.nodesByPackage()
	assert.Equal(t, executionOrchestratorLabel, placed["assets"],
		"the pinned build ran here, with no node variable in its environment: %v", rig.runs())
	assert.Equal(t, 1, executionBuildCount(rig, "assets"), "and exactly once: %v", rig.runs())
	for _, name := range []string{"ui", "docs", "app"} {
		assert.Contains(t, []string{executionNode, executionSecondNode}, placed[name],
			"%s was still delegated: %v", name, rig.runs())
	}

	produced := executionProbeValues(rig, "inputs")
	bundle := produced["assets"]
	require.NotEmpty(t, bundle)
	assert.Contains(t, bundle, executionOrchestratorLabel, "the bytes say where they were built")
	assert.Equal(t, bundle, produced["ui"], "ui read exactly the bytes the pinned build produced")
	assert.Equal(t, bundle, produced["docs"], "and so did docs")
	assert.Equal(t, bundle+bundle, produced["app"])

	assert.Contains(t, executionRelayedPackages(res), "assets",
		"the pinned build's outputs reached the nodes over a relay branch\nstdout:\n%s", res.Stdout)

	assert.ElementsMatch(t,
		[]string{"assets@0.1.0", "ui@0.1.0", "docs@0.1.0", "app@0.1.0"}, rig.repo.TagList())
	assert.Empty(t, rig.branches(), "the run closed every coordination branch it created")
	stopAll(t, workers)
}

// executionRelayedPackages is every package whose admitted outputs this run
// copied onto a node's own mailbox.
func executionRelayedPackages(res harness.RunResult) []string {
	var relayed []string
	for _, event := range executionEvents(res) {
		if event.Str("message") == "relay pushed" {
			relayed = append(relayed, event.Str("package"))
		}
	}
	return relayed
}

// TestExecutionOrchestratorOnlyBuildConsumesWorkerOutputs: the pin works in
// the other direction too. A consumer pinned here builds from the outputs its
// providers produced on workers, because admitting a node's result installs
// it in this checkout before anything that reads the working tree runs.
func TestExecutionOrchestratorOnlyBuildConsumesWorkerOutputs(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, pinPackages(map[string]*models.RunOnly{
		"app": placedOn(models.RunOnlyOrchestrator, models.RunOnlyOrchestrator),
	}))
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	placed := rig.nodesByPackage()
	assert.Equal(t, executionOrchestratorLabel, placed["app"], "the pinned consumer built here")
	for _, name := range []string{"assets", "ui", "docs"} {
		assert.Contains(t, []string{executionNode, executionSecondNode}, placed[name],
			"%s built on a worker: %v", name, rig.runs())
	}

	produced := executionProbeValues(rig, "inputs")
	bundle := produced["assets"]
	require.NotEmpty(t, bundle)
	assert.Equal(t, bundle+bundle, produced["app"],
		"the providers' outputs were in this checkout before the pinned build ran")
	assert.ElementsMatch(t,
		[]string{"assets@0.1.0", "ui@0.1.0", "docs@0.1.0", "app@0.1.0"}, rig.repo.TagList())
	assert.Empty(t, rig.branches())
	stopAll(t, workers)
}

// executionPlacementPackages are the independent packages every claim about
// capacity is made on: nothing among them waits for anything but a slot.
var executionPlacementPackages = []string{"alpha", "beta", "gamma"}

// newExecutionPlacementRig seeds a workspace of independent packages against
// one mailbox.
func newExecutionPlacementRig(t *testing.T, names []string, script func(*harness.Repo) string,
	adjust ...func(*models.File)) *executionRig {
	t.Helper()
	repo := harness.New(t)
	mailbox := executionMailbox(t)
	cfg := libsConfig(script(repo), 4, 4)
	cfg.Execution = &models.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers:   []models.ExecutionWorkerConfig{{Name: executionNode, Endpoint: "file://" + mailbox}},
		Timeouts:  &models.ExecutionTimeoutsConfig{Preflight: 30},
	}
	for _, change := range adjust {
		change(&cfg)
	}
	repo.WriteConfigModel(cfg)
	seedIndependentPackages(repo, names)
	return newExecutionRigOver(t, repo, mailbox)
}

// executionTimedBuild is the build script of the capacity claims: it records
// where it ran and holds its slot long enough that an overlap is an overlap
// rather than scheduling luck.
func executionTimedBuild(window time.Duration) func(*harness.Repo) string {
	return func(repo *harness.Repo) string {
		return executionRecordingScript + " && " +
			repo.TsmarkScript("timeline.log", "$DISPAT_PACKAGE", window)
	}
}

// TestExecutionWorkerOnlyBuildNeverRunsOnTheOrchestrator: a build pinned to a
// worker waits for that worker however much room this machine has. Three of
// them on one node of capacity one therefore take three turns, and not one of
// them is ever run here.
func TestExecutionWorkerOnlyBuildNeverRunsOnTheOrchestrator(t *testing.T) {
	pinned := map[string]*models.RunOnly{}
	for _, name := range executionPlacementPackages {
		pinned[name] = placedOn(models.RunOnlyWorker, models.RunOnlyBoth)
	}
	rig := newExecutionPlacementRig(t, executionPlacementPackages,
		executionTimedBuild(2*time.Second),
		func(cfg *models.File) { cfg.Execution.Concurrency = models.Int(4) },
		pinPackages(pinned))
	workers := rig.startWorkers([]string{executionNode}, 1)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	placed := rig.nodesByPackage()
	for _, name := range executionPlacementPackages {
		assert.Equal(t, executionNode, placed[name],
			"%s ran on the worker although this machine had four free slots: %v", name, rig.runs())
	}
	timeline := rig.repo.Timeline("timeline.log")
	require.Len(t, timeline, len(executionPlacementPackages), "every package built")
	// One node of capacity one runs them one at a time.
	harness.AssertConcurrencyBudget(t, timeline, 1)
	stopAll(t, workers)
}

// TestExecutionBothUsesTheOrchestratorWhenWorkersAreFull: under the default
// the orchestrator is one more node, and it is the node of last resort. With
// the only worker busy it takes the next frame rather than letting it queue,
// and the run's own build budget still bounds the two of them together.
func TestExecutionBothUsesTheOrchestratorWhenWorkersAreFull(t *testing.T) {
	rig := newExecutionPlacementRig(t, executionPlacementPackages,
		executionTimedBuild(executionStageWindow), func(cfg *models.File) {
			cfg.Concurrency = []int{2, 1}
			cfg.Execution.Concurrency = models.Int(2)
		})
	workers := rig.startWorkers([]string{executionNode}, 1)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	placed := rig.nodesByPackage()
	here, away := 0, 0
	for _, name := range executionPlacementPackages {
		require.Contains(t, placed, name, "%s built somewhere: %v", name, rig.runs())
		if placed[name] == executionOrchestratorLabel {
			here++
			continue
		}
		away++
	}
	assert.Positive(t, here, "a frame was taken here while the only worker was busy: %v", rig.runs())
	assert.Positive(t, away, "and the worker was still preferred for the others: %v", rig.runs())
	timeline := rig.repo.Timeline("timeline.log")
	require.Len(t, timeline, len(executionPlacementPackages))
	// The run's own build budget bounds every node of the pool together.
	harness.AssertConcurrencyBudget(t, timeline, 2)
	assert.Len(t, rig.repo.TagList(), len(executionPlacementPackages))
	stopAll(t, workers)
}

// TestExecutionBothPrefersAFreeWorker: the other half of the default. With
// the worker free there is nothing for this machine to take, so it takes
// nothing: the orchestrator is a fallback and never a first choice.
func TestExecutionBothPrefersAFreeWorker(t *testing.T) {
	rig := newExecutionPlacementRig(t, []string{"alpha"}, executionTimedBuild(time.Second),
		func(cfg *models.File) { cfg.Execution.Concurrency = models.Int(4) })
	workers := rig.startWorkers([]string{executionNode}, 1)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, executionNode, rig.nodesByPackage()["alpha"],
		"the free worker took the frame although this machine had four slots: %v", rig.runs())
	stopAll(t, workers)
}

// TestExecutionRunOnlyPerStagePair: a pair states the two stages apart. The
// build side is asserted end to end; the publish side is asserted as the
// value the ladder resolved to, because delegating a publish is the gate
// after this one and until then every publish runs on the orchestrator
// anyway, which is what the pair asks for here.
func TestExecutionRunOnlyPerStagePair(t *testing.T) {
	pinned := map[string]*models.RunOnly{}
	for _, name := range executionPlacementPackages {
		pinned[name] = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
	}
	rig := newExecutionPlacementRig(t, executionPlacementPackages,
		executionTimedBuild(time.Second), pinPackages(pinned))
	workers := rig.startWorkers([]string{executionNode}, 2)

	status := rig.repo.StatusOK("--log-level", "debug")
	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for _, name := range executionPlacementPackages {
		assert.Equal(t, []string{models.RunOnlyWorker, models.RunOnlyOrchestrator},
			executionResolvedList(t, executionResolvedPackage(t, status, name), "runOnly"),
			"the ladder resolved %s's two stages apart", name)
		assert.Equal(t, executionNode, rig.nodesByPackage()[name],
			"%s built on the worker the pair names: %v", name, rig.runs())
	}
	assert.Len(t, rig.repo.TagList(), len(executionPlacementPackages),
		"and every package still published from here")
	stopAll(t, workers)
}

// TestExecutionWorkerOnlyWithoutWorkersRefusesTheRelease: a stage that may
// only run on a worker, in a configuration that declares none, is work this
// run could not execute anywhere. It is refused before the first hook, with
// the package, the stage and the missing key named, and `dispat status` still
// reports the plan because it executes nothing.
func TestExecutionWorkerOnlyWithoutWorkersRefusesTheRelease(t *testing.T) {
	for name, row := range map[string]struct {
		placement *models.RunOnly
		stage     string
	}{
		"both stages pinned to a worker": {
			placement: placedOn(models.RunOnlyWorker, models.RunOnlyWorker), stage: "build"},
		"the build alone": {
			placement: placedOn(models.RunOnlyWorker, models.RunOnlyBoth), stage: "build"},
		"the publish alone": {
			placement: placedOn(models.RunOnlyBoth, models.RunOnlyWorker), stage: "publish"},
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			cfg := libsConfig(markerBuild, 1)
			cfg.RunOnly = row.placement
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): bootstrap")

			status := r.StatusOK("--log-format", "json")
			res := r.Release("--log-format", "json")

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
				"no %s diagnostic\nstdout:\n%s", executionRefusalCode, res.Stdout)
			text := diagnosticText(res)
			assert.Contains(t, text, "core")
			assert.Contains(t, text, row.stage)
			assert.Contains(t, text, "execution.workers")
			assert.Equal(t, 0, buildRuns(r), "no stage of the run started")
			assert.Empty(t, r.TagList(), "and nothing was released")
			assert.False(t, harness.IsCodePresent(executionEvents(status), executionRefusalCode),
				"status executes nothing and says nothing about placement")
		})
	}
}

// TestExecutionRunOnlyRefusals: every shape and every word the key does not
// have, refused as the configuration is read, with the key path named and the
// execution code attached.
func TestExecutionRunOnlyRefusals(t *testing.T) {
	for name, row := range map[string]struct {
		stated any
		want   string
	}{
		"a misspelled value":       {stated: "orchestartor", want: "is not a placement"},
		"a value of another key":   {stated: "any", want: "is not a placement"},
		"a list of one":            {stated: []any{"worker"}, want: "states both stages"},
		"a list of three":          {stated: []any{"worker", "worker", "worker"}, want: "states both stages"},
		"a misspelled stage value": {stated: []any{"worker", "nowhere"}, want: "is not a placement"},
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			r.WriteConfigModel(libsConfig(echoBuild, 1))
			r.SeedPackage("packages", "core")
			r.WriteFile(filepath.Join("packages", "core", "dispat.json"),
				executionRawJSON(r, map[string]any{"runOnly": row.stated}))
			r.Commit("feat(core): bootstrap")

			res := r.Status("--log-format", "json")

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, diagnosticText(res), row.want)
			assert.Contains(t, diagnosticText(res), "runOnly")
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
				"no %s diagnostic\nstdout:\n%s\nstderr:\n%s", executionRefusalCode, res.Stdout, res.Stderr)
			assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
		})
	}

	t.Run("a publish on a worker beside a login script", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Scripts["login"] = models.Script{"echo logging in"}
		libs := cfg.Spaces["libs"]
		libs.Flow.Login = []string{"login"}
		libs.RunOnly = placedOn(models.RunOnlyBoth, models.RunOnlyWorker)
		cfg.Spaces["libs"] = libs
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")

		res := r.Status("--log-format", "json")

		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, diagnosticText(res), "flow.login")
		assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode))
		assert.Empty(t, r.TagList())
	})
}

// TestExecutionRunOnlyLadder: the key rides the ordinary ladder and replaces
// as a pair. The nearest level that states it wins, a level that states
// nothing inherits, and a workspace where nobody stated it says nothing at
// all. The resolved values are read out of `dispat status --log-level debug`,
// which is where a run says what a package's configuration came to without
// releasing anything.
func TestExecutionRunOnlyLadder(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyOrchestrator)
	libs := cfg.Spaces["libs"]
	libs.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyWorker)
	cfg.Spaces["libs"] = libs
	cfg.Packages = map[string]models.PackageConfig{"tool": {Path: "tools/tool"}}
	r.WriteConfigModel(cfg)
	for _, name := range []string{"core", "utils"} {
		r.SeedPackage("packages", name)
	}
	r.WriteFile(filepath.Join("tools", "tool", "main.txt"), "tool\n")
	// The space folder's own file, the layer between the root file's entry
	// and anything said about one package.
	r.WriteFile(filepath.Join("packages", "dispat.json"),
		executionRawJSON(r, map[string]any{"runOnly": []any{"worker", "orchestrator"}}))
	r.WriteFile(filepath.Join("packages", "core", "dispat.json"),
		executionRawJSON(r, map[string]any{"runOnly": "both"}))
	r.Commit("feat(core,utils,tool): bootstrap")

	res := r.StatusOK("--log-level", "debug")

	assert.Equal(t, []string{models.RunOnlyBoth, models.RunOnlyBoth},
		executionResolvedList(t, executionResolvedPackage(t, res, "core"), "runOnly"),
		"the package folder's own file is the nearest statement about the package")
	assert.Equal(t, []string{models.RunOnlyWorker, models.RunOnlyOrchestrator},
		executionResolvedList(t, executionResolvedPackage(t, res, "utils"), "runOnly"),
		"the space folder's file outranks the root file's space entry, as a pair")
	assert.Equal(t, []string{models.RunOnlyOrchestrator, models.RunOnlyOrchestrator},
		executionResolvedList(t, executionResolvedPackage(t, res, "tool"), "runOnly"),
		"a standalone package is its own space and inherits the repository default")
	assert.Empty(t, r.TagList(), "status releases nothing")
}

// TestExecutionRunOnlyIsInertWithoutWorkers: the key is validated wherever it
// is written, and with no `execution` object and no worker value in play it
// changes nothing at all. The comparison is against a twin repository without
// the key, line for line.
func TestExecutionRunOnlyIsInertWithoutWorkers(t *testing.T) {
	var wantTags, wantShape []string
	for _, row := range []struct {
		name      string
		placement *models.RunOnly
	}{
		{"no runOnly key", nil},
		{"the default written out", placedOn(models.RunOnlyBoth, models.RunOnlyBoth)},
		{"both stages pinned to this machine",
			placedOn(models.RunOnlyOrchestrator, models.RunOnlyOrchestrator)},
		{"a pair naming this machine twice",
			placedOn(models.RunOnlyBoth, models.RunOnlyOrchestrator)},
	} {
		t.Run(row.name, func(t *testing.T) {
			r := executionLocalRepo(t, func(r *harness.Repo) {
				cfg := libsConfig(echoBuild, 1)
				cfg.RunOnly = row.placement
				r.WriteConfigModel(cfg)
			})

			res := r.ReleaseOK()

			tags, shape := r.TagList(), executionRunShape(res)
			require.Equal(t, []string{"core@0.1.0"}, tags)
			assert.False(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
				"a local release is never refused for a placement it can satisfy")
			if wantTags == nil {
				wantTags, wantShape = tags, shape
				return
			}
			assert.Equal(t, wantTags, tags, "the tags a release writes do not depend on runOnly")
			assert.Equal(t, wantShape, shape, "nor does the run a reader sees")
		})
	}
}

// TestExecutionOrchestratorBuildFailureIsALocalFailure: a pinned build that
// fails fails its package exactly as a local build always did. The outcome
// script runs here, the unrelated packages still release on their nodes, and
// nothing about the failure names a worker, because no worker was involved in
// it.
func TestExecutionOrchestratorBuildFailureIsALocalFailure(t *testing.T) {
	rig := newExecutionWorkspace(t, recordingBuild, executionOneWorker, func(cfg *models.File) {
		space := cfg.Spaces["libs"]
		space.Flow.OnFail = []string{"onfail"}
		cfg.Spaces["libs"] = space
		cfg.Scripts["onfail"] = models.Script{executionOnFailScript}
		cfg.Scripts["build"] = models.Script{
			executionRecordingScript + ` && [ "$DISPAT_PACKAGE" != ui ] || exit 4`}
	}, pinPackages(map[string]*models.RunOnly{
		"ui": placedOn(models.RunOnlyOrchestrator, models.RunOnlyOrchestrator),
	}))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"build"}, executionFailedStages(res),
		"the package failed at its build stage\nstdout:\n%s", res.Stdout)
	assert.Equal(t, executionOrchestratorLabel, rig.nodesByPackage()["ui"],
		"the pinned build ran here: %v", rig.runs())
	assert.Equal(t, executionOrchestratorLabel, executionProbeValue(t, rig, "onfail", "ui"),
		"and its outcome script ran here too")
	for _, event := range executionEvents(res) {
		if event.Package() == "ui" {
			assert.Empty(t, event.Str("worker"),
				"a failure on this machine names no worker: %v", event)
		}
	}
	assert.True(t, rig.repo.IsTagged("docs@0.1.0"), "an unrelated package still released")
	assert.False(t, rig.repo.IsTagged("ui@0.1.0"), "and the failed one did not")
	assert.Contains(t, []string{executionNode, executionSecondNode}, rig.nodesByPackage()["docs"],
		"a frame this run kept did not stop it delegating the others: %v", rig.runs())
	stopAll(t, []*executionWorker{worker})
}

// TestExecutionOrchestratorOnlyBuildIsNotHeldToTheWorkersPlatforms: a package
// pinned to this machine is never offered to a worker, so what the workers
// run says nothing about whether the run can build it. Preflight therefore
// leaves it alone, and whether this machine satisfies its platforms is
// answered where the frame is placed: with a platform nothing has, the
// package fails at its build stage with the integrity code and the run
// publishes nothing.
func TestExecutionOrchestratorOnlyBuildIsNotHeldToTheWorkersPlatforms(t *testing.T) {
	rig := newExecutionPlacementRig(t, []string{"alpha"}, executionTimedBuild(time.Second),
		pinPackages(map[string]*models.RunOnly{
			"alpha": placedOn(models.RunOnlyOrchestrator, models.RunOnlyOrchestrator),
		}), func(cfg *models.File) {
			cfg.BuildPlatforms = []string{"plan9/mips"}
		})
	workers := rig.startWorkers([]string{executionNode}, 1)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.False(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
		"preflight did not refuse a package it would never have offered to a worker")
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode),
		"the pin was answered where the frame is placed\nstdout:\n%s", res.Stdout)
	assert.Equal(t, []string{"build"}, executionFailedStages(res))
	assert.Empty(t, rig.repo.TagList(), "nothing was published")
	stopAll(t, workers)
}
