// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// A package's release as its flow's graph orders it (§13.11): the stages the
// flow defines, stages that run at once, the record of a package with no
// publish, the stages that observe the release, what each stage reads, and
// the beforeAll hook as the task before the first stages.

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// graphOf resolves the given stages for a space, which is what the
// configuration does for one it loads.
func graphOf(t *testing.T, specs ...model.StageSpec) model.StageGraph {
	t.Helper()
	g, refusals := model.ResolveStageGraph(specs)
	require.Empty(t, refusals)
	return g
}

// spec is one stage with constraints, for graphOf.
func spec(name string, constraints ...func(*model.StageSpec)) model.StageSpec {
	s := model.StageSpec{Name: name}
	for _, c := range constraints {
		c(&s)
	}
	return s
}

func after(names ...string) func(*model.StageSpec) {
	return func(s *model.StageSpec) { s.After = names }
}
func parallel(names ...string) func(*model.StageSpec) {
	return func(s *model.StageSpec) { s.Parallel = names }
}

// lintStage is a stage the flow defines, with both hooks.
func lintStage() model.UserStage {
	return model.UserStage{Name: "lint", Commands: []string{"lint"}, BeforeHook: []string{"h-bl"}, PostHook: []string{"h-pl"}}
}

// newRunFor is the run Executor.Run builds for a plan, with nothing started,
// for the test that execute one task against outcomes staged by hand.
func newRunFor(p *plan.Plan, changed map[string]bool, runner *fakeRunner) *run {
	e := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 4, Publish: 4})
	results, _ := newReleasingResults(p)
	plans := newStagePlans(p, changed)
	_, publishReach := e.buildTaskGraph(p, changed, plans)
	r := &run{Executor: e, plan: p, plans: plans, wsVars: WorkspaceEnv(p, e.Log), logins: newSpaceLogins(p),
		stop: func(error) {}, publishReach: publishReach, reachedProviders: map[string][]string{},
		results: results, started: map[string]time.Time{}, avChanged: map[string]bool{},
		reconciledProviders: map[string][]string{}, builtPackages: map[string]bool{},
		exports: map[task][]plan.Output{}, failedTasks: map[task]bool{}, inFlight: map[string]int{},
		outcomes: map[string]*pendingOutcome{}}
	r.prepareNativeWrites(context.Background(), changed)
	return r
}

// failedAfterBuild stages a provider's outcome as a run records it once the
// provider failed after it built: its consumers' version stages, which wait
// on no provider, may be admitted against that outcome.
func (r *run) failedAfterBuild(pkg string) {
	r.results[pkg].Status, r.results[pkg].FailedStage = StatusFailed, "publish"
}

// TestUserStageRunsAfterTheBuildAndBeforeThePublish: a defined stage runs
// under its own name with its own hooks, after the build and before the
// publish, unordered with the test, and is reported as a stage of its own
// with the label the flow gave it.
func TestUserStageRunsAfterTheBuildAndBeforeThePublish(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.TestScript = []string{"check"}
	space.UserStages = []model.UserStage{lintStage()}
	space.StageLabels = map[string]string{"lint": "Lint it"}
	r := &fakeRunner{}
	obs, res := runObserved(t, p, r)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	for _, pair := range [][2]string{{"build a", "h-bl a"}, {"h-bl a", "lint a"}, {"lint a", "h-pl a"}, {"h-pl a", "publish a"},
		{"build a", "check a"}, {"check a", "publish a"}} {
		assert.Less(t, r.indexOf(pair[0]), r.indexOf(pair[1]), "%s before %s", pair[0], pair[1])
	}
	assert.Equal(t, "lint", envValue(t, r.envs["lint a"], "DISPAT_STAGE"))
	assert.Equal(t, "beforeLint", envValue(t, r.envs["h-bl a"], "DISPAT_STAGE"))
	assert.Equal(t, "postLint", envValue(t, r.envs["h-pl a"], "DISPAT_STAGE"))
	assert.ElementsMatch(t, []string{"build", "test", "lint", "publish"}, obs.stagesOf(EventStageSucceeded, "a"))
	for _, ev := range obs.events {
		if ev.Stage == "lint" {
			assert.Equal(t, "Lint it", ev.Label)
		} else {
			assert.Empty(t, ev.Label, "%s carries no label", ev.Stage)
		}
	}
}

// TestUserStageFailureFailsThePackageUnderItsName: a defined stage before the
// record gates the release like the built-in ones, and the failure names the
// stage and its label.
func TestUserStageFailureFailsThePackageUnderItsName(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.UserStages = []model.UserStage{lintStage()}
	space.StageLabels = map[string]string{"lint": "Lint it"}
	space.OnFailScript = []string{"onfail"}
	r := &fakeRunner{fail: map[string]bool{"lint a": true}}
	obs, res := runObserved(t, p, r)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "lint", res["a"].FailedStage)
	assert.Equal(t, "Lint it", res["a"].FailedStageLabel)
	assert.Equal(t, -1, r.indexOf("publish a"), "nothing publishes behind a failed stage")
	assert.Equal(t, "lint", envValue(t, r.envPrefix(t, "onfail"), "DISPAT_FAILED_STAGE"))
	var failed Event
	for _, ev := range obs.events {
		if ev.Name == EventPackageFailed {
			failed = ev
		}
	}
	assert.Equal(t, "lint", failed.FailedStage)
	assert.Equal(t, "Lint it", failed.Label)
}

// TestAnnouncePlacedBeforeThePublishGates: beside a constrained publish the
// flow may move the announce before it, where it precedes the record and its
// failure fails the package, because the release is not out yet.
func TestAnnouncePlacedBeforeThePublishGates(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.AnnounceScript = []string{"announce"}
	space.StageGraph = graphOf(t, spec("version"), spec("build"), spec("publish", after("build")), spec("announce", after("build")))
	r := &fakeRunner{}
	_, res := runObserved(t, p, r)
	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.Less(t, r.indexOf("build a"), r.indexOf("announce a"))
	assert.Less(t, r.indexOf("announce a"), r.indexOf("publish a"))

	r = &fakeRunner{fail: map[string]bool{"announce a": true}}
	_, res = runObserved(t, p, r)
	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "announce", res["a"].FailedStage)
	assert.Equal(t, -1, r.indexOf("publish a"))
}

// TestObservingStageNeverWithholdsTheRecord: a stage after the publish runs
// once the package published and only warns; its failure blocks the stages
// that wait on it and nothing else; and when the package failed it never
// runs.
func TestObservingStageNeverWithholdsTheRecord(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.UserStages = []model.UserStage{
		{Name: "deploy", Commands: []string{"deploy"}, PostHook: []string{"h-pd"}},
		{Name: "smoke", Commands: []string{"smoke"}},
		{Name: "verify", Commands: []string{"verify"}},
	}
	space.PostPublishScript = []string{"h-pp"}
	// The publish states its own place so that a stage may be placed after
	// it: beside an unconstrained publish a user stage keeps the publish's
	// own default edge from it.
	space.StageGraph = graphOf(t, spec("version"), spec("build"), spec("publish", after("build")),
		spec("deploy", after("publish")), spec("smoke", after("deploy")), spec("verify", after("smoke")))
	r := &fakeRunner{fail: map[string]bool{"deploy a": true}}
	obs, res := runObserved(t, p, r)
	require.Equal(t, StatusPublished, res["a"].Status, "a failed observer leaves the package published")
	assert.Less(t, r.indexOf("publish a"), r.indexOf("h-pp a"))
	assert.Less(t, r.indexOf("h-pp a"), r.indexOf("deploy a"), "the observer follows the publish tail")
	assert.NotEqual(t, -1, r.indexOf("h-pd a"), "a failed observing stage still runs its post hook")
	assert.Equal(t, -1, r.indexOf("smoke a"), "a stage waiting on a failed observer does not run")
	assert.Equal(t, -1, r.indexOf("verify a"), "an omitted observer blocks all its descendants")
	assert.Equal(t, []string{"build", "publish"}, obs.stagesOf(EventStageSucceeded, "a"))

	r = &fakeRunner{fail: map[string]bool{"publish a": true}}
	obs, res = runObserved(t, p, r)
	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, -1, r.indexOf("deploy a"), "nothing observes a release that is not out")
	assert.NotContains(t, obs.stagesOf(EventStageStarted, "a"), "deploy")
}

func TestObservingStagesWaitForRequiredReleaseRecord(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.UserStages = []model.UserStage{{Name: "deploy", Commands: []string{"deploy"}},
		{Name: "verify", Commands: []string{"verify"}}}
	space.StageGraph = graphOf(t, spec("version"), spec("build"), spec("publish", after("build")),
		spec("deploy", after("publish")), spec("verify", after("deploy")))
	r := newRunFor(p, map[string]bool{"a": true}, &fakeRunner{})
	res := r.results["a"]
	res.Status, res.RecordBlocked = StatusPublished, true
	for _, stage := range []string{"deploy", "verify"} {
		task := task{"a", taskUser, stage}
		ctx := &taskCtx{run: r, t: task, stage: r.plans["a"].stage(task)}
		assert.False(t, r.settleAdmission(context.Background(), ctx, res).isAdmitted, stage)
		assert.True(t, r.failedTasks[task], stage)
	}
	assert.Equal(t, StatusPublished, res.Status)
}

// TestPackageWithoutAPublishIsRecordedAfterItsLastStage: a package whose flow
// has no publish is tagged and recorded once every stage before the record
// succeeded, with no publish event, and an unconstrained announce still
// follows the record.
func TestPackageWithoutAPublishIsRecordedAfterItsLastStage(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.PublishScript = nil
	space.AnnounceScript = []string{"announce"}
	space.UserStages = []model.UserStage{lintStage()}
	r := &fakeRunner{}
	tagger := &fakeTagger{}
	changelog := &fakeChangelog{}
	obs := &fakeObserver{}
	e := newExecutor(execSpec{Runner: r, Tagger: tagger, Changelog: changelog, Build: 2, Publish: 2})
	e.Observer = obs
	res := e.Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.Equal(t, -1, r.indexOf("publish a"))
	assert.Less(t, r.indexOf("lint a"), r.indexOf("announce a"), "the announce follows the record")
	assert.Equal(t, []string{"a@1.0.1"}, tagger.tags)
	assert.Equal(t, []string{"a"}, changelog.entries)
	assert.Equal(t, []string{
		"stage.started:build", "stage.succeeded:build",
		"stage.started:lint", "stage.succeeded:lint",
		"package.published:",
		"stage.started:announce", "stage.succeeded:announce",
	}, obs.forPackage("a"))

	r = &fakeRunner{fail: map[string]bool{"lint a": true}}
	_, res = runObserved(t, p, r)
	assert.Equal(t, StatusFailed, res["a"].Status, "a stage before the record fails the package as a build does")
}

// TestEveryPackageBuildsAndIsRecorded: a package whose flow configures no
// script still has its build stage, which runs no command, and is recorded
// after it.
func TestEveryPackageBuildsAndIsRecorded(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.BuildScript, space.PublishScript = nil, nil
	r := &fakeRunner{}
	tagger := &fakeTagger{}
	obs := &fakeObserver{}
	e := newExecutor(execSpec{Runner: r, Tagger: tagger, Build: 1, Publish: 1})
	e.Observer = obs
	res := e.Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.Empty(t, r.events, "nothing ran")
	assert.Equal(t, []string{"a@1.0.1"}, tagger.tags)
	assert.Equal(t, []string{"stage.started:build", "stage.succeeded:build", "package.published:"}, obs.forPackage("a"))
}

// TestParallelStagesAreHandedOutInTheStageOrder: two stages the flow leaves
// unordered become ready together once what they wait for completed, in the
// stage order, and the publish waits for both.
func TestParallelStagesAreHandedOutInTheStageOrder(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.TestScript = []string{"check"}
	space.UserStages = []model.UserStage{lintStage()}
	changed := map[string]bool{"a": true}
	sched, _ := (&Executor{}).buildTaskGraph(p, changed, newStagePlans(p, changed))

	assert.Equal(t, []task{{"a", taskBuild, ""}}, sched.Ready())
	readied := sched.Done(task{"a", taskBuild, ""})
	assert.Equal(t, []task{{"a", taskUser, "lint"}, {"a", taskTest, ""}}, readied, "the stage order: rank, then name")
	assert.Empty(t, sched.Done(task{"a", taskTest, ""}), "the publish waits for every stage before it")
	assert.Equal(t, []task{{"a", taskPublish, ""}}, sched.Done(task{"a", taskUser, "lint"}))
}

// TestBeforeAllRunsOnceBeforeEveryFirstStage: beforeAll is a task of its own,
// so two first stages wait for one hook, and its failure names the first
// stage in the stage order.
func TestBeforeAllRunsOnceBeforeEveryFirstStage(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.BeforeAllScript = []string{"h-all"}
	space.UserStages = []model.UserStage{lintStage()}
	space.StageGraph = graphOf(t, spec("version"), spec("build", after("version")), spec("publish"),
		spec("lint", after("version"), parallel("build")))
	r := &fakeRunner{}
	_, res := runObserved(t, p, r)
	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.Equal(t, 1, r.countPrefix("h-all"))
	assert.Less(t, r.indexOf("h-all a"), r.indexOf("build a"))
	assert.Less(t, r.indexOf("h-all a"), r.indexOf("lint a"))

	r = &fakeRunner{fail: map[string]bool{"h-all a": true}}
	_, res = runObserved(t, p, r)
	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "build", res["a"].FailedStage, "the stage the package never entered")
	assert.Equal(t, -1, r.indexOf("build a"))
	assert.Equal(t, -1, r.indexOf("lint a"))
}

// TestStagePlanTargetsAndRecording: what a provider's readiness edge lands
// on, what a consumer's build waits for, and which task records the release,
// with and without a publish and a lock-file stage.
func TestStagePlanTargetsAndRecording(t *testing.T) {
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	rel := p.Releases["b"]
	space := rel.Pkg.Space

	sp := newStagePlan(rel)
	assert.Equal(t, []task{{"b", taskBuild, ""}}, sp.built())
	assert.Equal(t, task{"b", taskPublish, ""}, sp.recording)
	assert.Equal(t, []task{{"b", taskBuild, ""}}, sp.readinessTargets(), "the version stage waits for no provider")
	assert.Equal(t, []task{{"b", taskVersion, ""}}, sp.sources())

	space.UserStages = []model.UserStage{lintStage()}
	space.StageGraph = graphOf(t, spec("version"), spec("build", after("version")), spec("publish"),
		spec("lint", after("version"), parallel("build")))
	sp = newStagePlan(rel)
	assert.Equal(t, []task{{"b", taskBuild, ""}, {"b", taskUser, "lint"}}, sp.readinessTargets(),
		"every stage whose predecessors are all preparing waits for the provider")

	space.StageGraph = model.StageGraph{}
	space.AutoVersion = &model.AutoVersion{Manifests: model.ScopeNone, SyncLock: []string{"lock"}}
	sp = newStagePlan(rel)
	assert.Equal(t, []task{{"b", taskSyncLock, ""}}, sp.readinessTargets(), "the lock-file stage stands for every stage behind it")

	space.AutoVersion = nil
	space.PublishScript = nil
	space.UserStages = nil
	space.StageGraph = graphOf(t, spec("version"), spec("build", after("version")),
		spec("publish", after("build")))
	sp = newStagePlan(rel)
	assert.Equal(t, task{"b", taskPublish, ""}, sp.recording,
		"a placed publish boundary remains present without a script")
	assert.Contains(t, sp.tasks, task{"b", taskPublish, ""})

	space.StageGraph = model.StageGraph{}
	sp = newStagePlan(rel)
	assert.Equal(t, task{"b", taskRecord, ""}, sp.recording)
	sched := graph.NewScheduler[task]()
	sp.addEdges(sched)
	assert.Equal(t, []task{{"b", taskVersion, ""}}, sched.Ready())
	assert.Equal(t, []task{{"b", taskBuild, ""}}, sched.Done(task{"b", taskVersion, ""}))
	assert.Equal(t, []task{{"b", taskRecord, ""}}, sched.Done(task{"b", taskBuild, ""}), "the record follows the last stage")
}

// TestUserStageInputsFollowThePlaceTheBuildReaches: a defined stage the build
// reaches is given the package's own outputs, one it does not the providers'
// outputs the build is given, and one after the record runs on the
// orchestrator and observes.
func TestUserStageInputsFollowThePlaceTheBuildReaches(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	rel := p.Releases["a"]
	space := rel.Pkg.Space
	space.UserStages = []model.UserStage{lintStage(), {Name: "deploy", Commands: []string{"deploy"}}}
	space.StageGraph = graphOf(t, spec("version"), spec("build", after("version")), spec("publish", after("build")),
		spec("lint", after("version"), parallel("build")), spec("deploy", after("publish")))

	sp := newStagePlan(rel)
	lint := sp.stage(task{"a", taskUser, "lint"})
	assert.Equal(t, InputsProviderOutputs, lint.Inputs)
	assert.Equal(t, PlacementByBuild, lint.Placement)
	assert.Equal(t, BudgetBuild, lint.Budget)
	assert.False(t, lint.IsObserving)
	deploy := sp.stage(task{"a", taskUser, "deploy"})
	assert.True(t, deploy.IsObserving)
	assert.Equal(t, PlacementOrchestratorOnly, deploy.Placement)
	assert.Equal(t, BudgetBuild, deploy.Budget, "a user stage draws the build budget wherever it sits")
	assert.Equal(t, InputsNone, deploy.Inputs)

	space.StageGraph = model.StageGraph{}
	sp = newStagePlan(rel)
	assert.Equal(t, InputsOwnOutputs, sp.stage(task{"a", taskUser, "lint"}).Inputs, "after the build it reads what the build produced")
	assert.Equal(t, "Lint", sp.stage(task{"a", taskUser, "lint"}).Title)
}

func TestUserStageOutputsFollowGraphAncestorsIntoBuild(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	rel := p.Releases["a"]
	space := rel.Pkg.Space
	space.BuildOutputs = []string{"dist"}
	space.UserStages = []model.UserStage{
		{Name: "generate", Commands: []string{"generate"}},
		{Name: "transform", Commands: []string{"transform"}},
	}
	space.StageGraph = graphOf(t, spec("version"), spec("generate", after("version")),
		spec("transform", after("generate")), spec("build", after("transform")),
		spec("publish", after("build")))
	sp := newStagePlan(rel)
	generate := sp.stage(task{"a", taskUser, "generate"})
	transform := sp.stage(task{"a", taskUser, "transform"})
	build := sp.stage(task{"a", taskBuild, ""})
	assert.True(t, generate.IsProducingOutputs)
	assert.True(t, transform.IsProducingOutputs)
	assert.Equal(t, []string{"generate"}, transform.OwnOutputStages)
	assert.Equal(t, InputsOwnOutputs, transform.Inputs)
	assert.Equal(t, []string{"generate", "transform"}, build.OwnOutputStages)
	assert.Equal(t, InputsOwnOutputs, build.Inputs)
}

// exportScriptRunner is a runner whose scripts export through DISPAT_OUTPUT:
// a command named "export NAME=value" writes that line to its output file.
type exportScriptRunner struct {
	*fakeRunner
	mu   sync.Mutex
	seen map[string][]string
}

func (x *exportScriptRunner) Run(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) error {
	x.mu.Lock()
	if x.seen == nil {
		x.seen = map[string][]string{}
	}
	x.seen[command+" "+dir] = env
	x.mu.Unlock()
	if line, isExport := strings.CutPrefix(command, "export "); isExport {
		if err := os.WriteFile(envValueOrEmpty(env, OutputEnvVar), []byte(line+"\n"), 0o644); err != nil {
			return err
		}
	}
	return x.fakeRunner.Run(ctx, dir, command, env, stdout, stderr)
}

// envValueOrEmpty reads one variable off an environment, empty when unset.
func envValueOrEmpty(env []string, key string) string {
	for _, kv := range env {
		if value, isKey := strings.CutPrefix(kv, key+"="); isKey {
			return value
		}
	}
	return ""
}

// TestExportsReachOnlyTheStagesAStageReaches: what a stage exports reaches
// the stages behind it and nothing beside it, and the record sees every
// export of the stages before it, merged in the stage order.
func TestExportsReachOnlyTheStagesAStageReaches(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.BuildScript = []string{"export FROM_BUILD=1"}
	space.TestScript = []string{"export FROM_TESTS=2"}
	space.UserStages = []model.UserStage{{Name: "lint", Commands: []string{"export FROM_LINT=3"}}}
	space.PublishScript = []string{"publish"}
	r := &exportScriptRunner{fakeRunner: &fakeRunner{}}
	e := newExecutor(execSpec{Runner: r.fakeRunner, Tagger: &fakeTagger{}, Build: 2, Publish: 1})
	e.Runner = r
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)

	tests, lint, publish := r.seen["export FROM_TESTS=2 a"], r.seen["export FROM_LINT=3 a"], r.seen["publish a"]
	assert.Equal(t, "1", envValueOrEmpty(tests, "DISPAT_OUTPUT_FROM_BUILD"))
	assert.Empty(t, envValueOrEmpty(tests, "DISPAT_OUTPUT_FROM_LINT"), "the test does not read a stage beside it")
	assert.Empty(t, envValueOrEmpty(lint, "DISPAT_OUTPUT_FROM_TESTS"), "nor the lint the test")
	assert.Equal(t, "FROM_BUILD FROM_LINT FROM_TESTS", envValueOrEmpty(publish, "DISPAT_OUTPUTS"),
		"the publish reads every export before it, in the stage order")
	assert.Equal(t, "a:lint", envValueOrEmpty(publish, "DISPAT_OUTPUT_SOURCE_FROM_LINT"))
	var names []string
	for _, out := range p.Releases["a"].Outputs {
		names = append(names, out.Name)
	}
	assert.Equal(t, []string{"FROM_BUILD", "FROM_LINT", "FROM_TESTS"}, names, "the release carries what its record saw")
}
