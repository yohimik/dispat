// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 62: the stages a flow orders and defines. A built-in stage moved by a
// constraint, a stage the configuration defines with its hooks and exports, a
// set of constraints no order satisfies refused before any command, a package
// recorded with no publish stage and one with no script at all, two unordered
// stages running at once, and a defined stage placed on a worker with the
// build's outputs installed.

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// flowStageLog is a script appending its runtime stage name and package to
// stages.log in the repository root, under the given name.
func flowStageLog(names ...string) map[string]models.Script {
	scripts := make(map[string]models.Script, len(names))
	for _, name := range names {
		scripts[name] = models.Script{"echo $DISPAT_STAGE:$DISPAT_PACKAGE >> ../../stages.log"}
	}
	return scripts
}

// flowStageSequence is stages.log's entries for one package, in file order.
func flowStageSequence(t *testing.T, r *harness.Repo, pkg string) []string {
	t.Helper()
	data, err := os.ReadFile(r.Path("stages.log"))
	require.NoError(t, err)
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if name, p, ok := strings.Cut(line, ":"); ok && p == pkg {
			out = append(out, name)
		}
	}
	return out
}

// TestFlowConstraintMovesABuiltInStage: beside a constrained publish,
// `announce: {after: build}` runs the announce frame after the build frame
// and before the publish, unordered with the tests, and there it gates: a
// failing announce fails the package before anything is published or tagged,
// where the same announce after the publish only warns.
func TestFlowConstraintMovesABuiltInStage(t *testing.T) {
	cfg := harness.BaseFile(1)
	cfg.Scripts = flowStageLog("build", "tests", "publish", "announce",
		"beforeBuild", "postBuild", "beforeTests", "postTests", "beforePublish", "postPublish",
		"beforeAnnounce", "postAnnounce")
	cfg.Scripts["boom"] = models.Script{"exit 1"}
	flow := &models.SpaceFlowConfig{
		Build: []string{"build"}, Tests: []string{"tests"}, Publish: []string{"publish"},
		Announce: []string{"announce"},
		Stages: map[string]models.StageConfig{
			"publish":  {Script: []string{"publish"}, After: []string{"build"}},
			"announce": {Script: []string{"announce"}, After: []string{"build"}}},
		BeforeBuild: []string{"beforeBuild"}, PostBuild: []string{"postBuild"},
		BeforeTests: []string{"beforeTests"}, PostTests: []string{"postTests"},
		BeforePublish: []string{"beforePublish"}, PostPublish: []string{"postPublish"},
		BeforeAnnounce: []string{"beforeAnnounce"}, PostAnnounce: []string{"postAnnounce"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: models.PathList{"packages"}, Flow: flow}}

	t.Run("the announce frame runs after the build and before the publish", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first release")

		r.ReleaseOK()
		sequence := flowStageSequence(t, r, "core")
		at := func(name string) int { return slices.Index(sequence, name) }
		assert.Equal(t, []string{"beforeBuild", "build", "postBuild"}, sequence[:3])
		assert.Equal(t, []string{"beforePublish", "publish", "postPublish"}, sequence[len(sequence)-3:])
		assert.Less(t, at("beforeAnnounce"), at("announce"))
		assert.Less(t, at("announce"), at("postAnnounce"))
		assert.ElementsMatch(t, []string{"beforeAnnounce", "announce", "postAnnounce", "beforeTests", "tests", "postTests"},
			sequence[3:len(sequence)-3], "the announce and the tests run between them, unordered with each other")
		assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	})

	t.Run("an announce before the publish gates the release", func(t *testing.T) {
		r := harness.New(t)
		cfg.Spaces["libs"] = models.SpaceConfig{Path: models.PathList{"packages"}, Flow: flow}
		// A built-in stage's scripts live in its typed field; its object half
		// carries the constraints.
		cfg.Packages = map[string]models.PackageConfig{"core": {Flow: &models.SpaceFlowConfig{
			Announce: []string{"boom"},
			Stages:   map[string]models.StageConfig{"announce": {After: []string{"build"}}}}}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first release")

		res := r.Release()
		require.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
		assert.Empty(t, r.TagList(), "nothing is tagged behind a failed gating stage")
		assert.NotContains(t, flowStageSequence(t, r, "core"), "publish")
		failed := findEvent(t, res.Events, "announce script failed")
		assert.Equal(t, "announce", failed.Str("stage"))
	})
}

// TestFlowUserStageRunsWithHooksAndExports: a stage the flow defines runs
// under its own name between the build and the publish, with `before<Name>`
// and `post<Name>` hooks reporting their names, reads the build's export,
// hands its own to the publish under its own source, and is logged under the
// label the flow gave it.
func TestFlowUserStageRunsWithHooksAndExports(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(`echo "ARTIFACT=core-$DISPAT_NEW_VERSION.tgz" >> "$DISPAT_OUTPUT"`, 1)
	cfg.Scripts["scan"] = models.Script{
		`echo "$DISPAT_STAGE $DISPAT_OUTPUT_ARTIFACT" > ../../scan.txt && echo "SCANNED=$DISPAT_OUTPUT_ARTIFACT" >> "$DISPAT_OUTPUT"`}
	cfg.Scripts["publish"] = models.Script{
		`echo "$DISPAT_OUTPUT_SCANNED $DISPAT_OUTPUT_SOURCE_SCANNED" > ../../publish.txt`}
	for name, script := range flowStageLog("beforeScan", "postScan", "beforePublish") {
		cfg.Scripts[name] = script
	}
	space := cfg.Spaces["libs"]
	space.Flow.Stages = map[string]models.StageConfig{
		"scan": {Script: []string{"scan"}, Label: "Security scan", After: []string{"build"}}}
	space.Flow.StageHooks = map[string][]string{"beforeScan": {"beforeScan"}, "postScan": {"postScan"}}
	space.Flow.BeforePublish = []string{"beforePublish"}
	cfg.Spaces["libs"] = space
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.ReleaseOK()
	assert.Equal(t, "scan core-0.1.0.tgz\n", readFile(t, r, "scan.txt"), "the build's export reached the stage")
	assert.Equal(t, "core-0.1.0.tgz core:scan\n", readFile(t, r, "publish.txt"), "the stage's export reached the publish")
	assert.Equal(t, []string{"beforeScan", "postScan", "beforePublish"}, flowStageSequence(t, r, "core"),
		"the hooks bracket the stage under its name, before the publish frame")
	labelled := 0
	for _, ev := range res.Events {
		if ev.Str("stage") == "scan" {
			assert.Equal(t, "Security scan", ev.Str("label"))
			labelled++
		}
	}
	assert.NotZero(t, labelled, "the stage's lines carry its label\n%s", res.Stdout)
	assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
}

// TestFlowConflictsAreRefusedBeforeAnyWork: a constraint naming a stage the
// package does not run, a cycle (before and after naming one stage included)
// and a parallel pair the edges relate are each refused with their condition's
// code once the configuration is read, by the release, by status and by a
// script sweep, naming the package and the entry, and no script runs.
func TestFlowConflictsAreRefusedBeforeAnyWork(t *testing.T) {
	for name, row := range map[string]struct {
		stages map[string]models.StageConfig
		code   string
		names  string
	}{
		"a cycle": {map[string]models.StageConfig{
			"lint": {Script: []string{"lint"}, After: []string{"docs"}},
			"docs": {Script: []string{"lint"}, After: []string{"lint"}}},
			"E231", "docs, lint lie on a cycle"},
		"before and after naming one stage": {map[string]models.StageConfig{
			"lint": {Script: []string{"lint"}, Before: []string{"build"}, After: []string{"build"}}},
			"E231", "build, lint lie on a cycle"},
		"a name that is no stage of the package": {map[string]models.StageConfig{
			"lint": {Script: []string{"lint"}, After: []string{"scan"}}},
			"E230", `flow.lint.after names \"scan\"`},
		"a parallel pair the edges relate": {map[string]models.StageConfig{
			"lint": {Script: []string{"lint"}, Parallel: []string{"publish"}}},
			"E232", "lint and publish as parallel"},
	} {
		for _, command := range [][]string{{}, {"status"}, {"run", "build"}} {
			t.Run(name+" "+strings.Join(append([]string{"release"}, command...), " "), func(t *testing.T) {
				r := harness.New(t)
				cfg := libsConfig(markerBuild, 1)
				cfg.Scripts["lint"] = models.Script{"echo lint >> ../../build.log"}
				space := cfg.Spaces["libs"]
				space.Flow.Stages = row.stages
				cfg.Spaces["libs"] = space
				r.WriteConfigModel(cfg)
				r.SeedPackage("packages", "core")
				r.Commit("feat(core): first release")

				res := r.Command(command...)
				require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.True(t, harness.IsCodePresent(res.Events, row.code), "no %s in\n%s", row.code, res.Stdout)
				assert.Contains(t, res.Stdout, `package \"core\"`, "the refusal names the package")
				assert.Contains(t, res.Stdout, row.names, "the refusal names the entry")
				assert.Contains(t, res.Stdout, `"category":"execution-configuration"`, "the refusal's stable identifier")
				assert.Empty(t, r.TagList())
				assert.Equal(t, 0, buildRuns(r), "no script ran")
			})
		}
	}
}

// TestFlowPackagesWithoutAPublishAreStillRecorded: a package whose flow has no
// publish stage is tagged and recorded after its last stage, and a package
// whose flow configures no script at all still has its build stage, runs no
// command and is recorded after it, so no release is silently skipped and
// status still plans both.
func TestFlowPackagesWithoutAPublishAreStillRecorded(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = flowStageLog("build", "publish")
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: models.PathList{"packages"},
		Flow: &models.SpaceFlowConfig{Build: []string{"build"}}}}
	cfg.Packages = map[string]models.PackageConfig{"bare": {Flow: &models.SpaceFlowConfig{Build: []string{}}}}
	r.WriteConfigModel(cfg)
	seedIndependentPackages(r, []string{"built", "bare"})

	r.StatusOK()
	res := r.ReleaseOK()
	assert.ElementsMatch(t, []string{"built@0.1.0", "bare@0.1.0"}, r.TagList())
	assert.Equal(t, []string{"build"}, flowStageSequence(t, r, "built"), "the build ran and no publish did")
	assert.Empty(t, flowStageSequence(t, r, "bare"), "a package with no script runs nothing")
	published := map[string]bool{}
	for _, ev := range res.Events {
		if ev.Str("message") == "published" {
			published[ev.Package()] = true
		}
		assert.NotEqual(t, "publish", ev.Str("stage"), "no publish stage exists for either package")
	}
	assert.Equal(t, map[string]bool{"built": true, "bare": true}, published, "both are reported published\n%s", res.Stdout)
	assert.FileExists(t, r.Path("packages", "built", "CHANGELOG.md"))
	assert.FileExists(t, r.Path("packages", "bare", "CHANGELOG.md"))
}

// TestFlowParallelStagesRunAtOnce: a stage declared parallel to the tests
// starts while the tests run, and the publish waits for both.
func TestFlowParallelStagesRunAtOnce(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 2, 1)
	cfg.Scripts["tests"] = models.Script{
		`i=0; while [ ! -f ../../lint-started ] && [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done; ` +
			`test -f ../../lint-started && echo tests-done >> ../../order.log`}
	cfg.Scripts["lint"] = models.Script{`touch ../../lint-started && echo lint-done >> ../../order.log`}
	cfg.Scripts["publish"] = models.Script{`echo publish >> ../../order.log`}
	space := cfg.Spaces["libs"]
	space.Flow.Tests = []string{"tests"}
	space.Flow.Stages = map[string]models.StageConfig{"lint": {Script: []string{"lint"}, Parallel: []string{"tests"}}}
	cfg.Spaces["libs"] = space
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	r.ReleaseOK()
	order := strings.Fields(readFile(t, r, "order.log"))
	require.Len(t, order, 3, "%v", order)
	assert.Equal(t, "publish", order[2], "the publish waits for both")
	assert.ElementsMatch(t, []string{"tests-done", "lint-done"}, order[:2],
		"the tests waited for the lint to start, so the two ran at once")
}

// TestExecutionUserStageRunsOnAWorkerWithItsOutputs: a stage the flow defines
// after the build is placed the way the build is, so with the builds pinned to
// workers it runs on a worker as a task of its own, reads exactly the bytes
// its own build produced, and its export reaches the publication on the
// orchestrator.
func TestExecutionUserStageRunsOnAWorkerWithItsOutputs(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker, Publish: models.RunOnlyBoth}
		cfg.Scripts["verify"] = models.Script{executionTestsCheck}
		cfg.Scripts["publish"] = models.Script{executionOutputPublish + ` &&
printf '%s %s %s\n' probe-tested "$DISPAT_PACKAGE" "$DISPAT_OUTPUT_TESTED_ON" >> "$DISPAT_IT_EXECUTION_LOG"`}
		space := cfg.Spaces["libs"]
		space.Flow.Stages = map[string]models.StageConfig{
			"verify": {Script: []string{"verify"}, Label: "Verify the bundle", After: []string{"build"}}}
		cfg.Spaces["libs"] = space
	})
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 1)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	produced := executionProbeValues(rig, "inputs")
	verified := executionProbeValues(rig, "tests")
	nodes := executionProbeValues(rig, "tests-node")
	published := executionProbeValues(rig, "tested")
	for _, name := range executionWorkspacePackages {
		assert.Equal(t, 1, executionBuildCount(rig, name), "%s was built once: %v", name, rig.runs())
		require.Contains(t, verified, name, "%s ran its verify stage", name)
		assert.Equal(t, produced[name], verified[name], "%s's verify read the bytes its build produced", name)
		assert.Contains(t, []string{executionNode, executionSecondNode}, nodes[name],
			"%s's verify ran on a worker, as its build was pinned to", name)
		assert.Equal(t, nodes[name], published[name], "%s's verify export reached its publication", name)
	}
	assert.Contains(t, res.Stdout, `"task":"assets:verify"`, "the frame travels as a task of its own")
	assert.Contains(t, res.Stdout, `"label":"Verify the bundle"`, "the orchestrator logs the stage under its label")
	assert.ElementsMatch(t,
		[]string{"assets@0.1.0", "ui@0.1.0", "docs@0.1.0", "app@0.1.0"}, rig.repo.TagList())
	assert.Empty(t, rig.branches(), "the run closed every coordination branch it created")
	stopAll(t, workers)
}
