package release

// The stage names a release reports and the optional test stage. The version
// stage runs under the name its configuration gave it; the test stage sits on
// the one path from a package's build to its publish, gates the publish, and
// carries exports in both directions like every other stage.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// stagesOf answers the stage of every event of this name one package
// produced, in the order they were observed.
func (f *fakeObserver) stagesOf(name, pkg string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var stages []string
	for _, ev := range f.events {
		if ev.Name == name && ev.Package == pkg {
			stages = append(stages, ev.Stage)
		}
	}
	return stages
}

// budgetRunner counts how many build and tests commands run at the same
// moment, which is what one shared slot has to keep at one.
type budgetRunner struct {
	*fakeRunner
	mu             sync.Mutex
	inFlight, peak int
}

func (b *budgetRunner) Run(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) error {
	isCounted := command == "build" || command == "check"
	if isCounted {
		b.mu.Lock()
		b.inFlight++
		b.peak = max(b.peak, b.inFlight)
		b.mu.Unlock()
		defer func() {
			b.mu.Lock()
			b.inFlight--
			b.mu.Unlock()
		}()
	}
	return b.fakeRunner.Run(ctx, dir, command, env, stdout, stderr)
}

// testsExport is what a delegated test frame reports as its export.
func testsExport() []plan.Output {
	return []plan.Output{{Name: "TESTED", Value: "yes", Source: "a:test"}}
}

// TestVersionStageRunsUnderItsConfiguredName: a version stage configured as
// propagate reports that name in DISPAT_STAGE, its hooks, its events and its
// failure, and one configured as version keeps every name it had.
func TestVersionStageRunsUnderItsConfiguredName(t *testing.T) {
	for _, row := range []struct {
		stage model.VersionStage
		name  string
		title string
	}{
		{model.StageVersion, "version", "Version"},
		{"", "version", "Version"},
		{model.StagePropagate, "propagate", "Propagate"},
	} {
		t.Run(row.name+"/"+string(row.stage), func(t *testing.T) {
			p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
			space := p.Releases["a"].Pkg.Space
			space.VersionStage = row.stage
			space.VersionScript = []string{"sync"}
			space.BeforeVersionScript = []string{"pre"}
			space.PostVersionScript = []string{"post"}
			space.OnFailScript = []string{"onfail"}
			r := &fakeRunner{fail: map[string]bool{"sync b": true}}
			observer := &fakeObserver{}
			ex := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Build: 2, Publish: 2})
			ex.Observer = observer
			res := ex.Run(context.Background(), p)

			require.Equal(t, StatusFailed, res["b"].Status)
			assert.Equal(t, row.name, res["b"].FailedStage)
			assert.True(t, strings.HasPrefix(res["b"].Err.Error(), row.name+": "), res["b"].Err.Error())
			assert.Equal(t, row.name, envValue(t, r.envPrefix(t, "sync"), "DISPAT_STAGE"))
			assert.Equal(t, "before"+row.title, envValue(t, r.envPrefix(t, "pre"), "DISPAT_STAGE"))
			onFail := r.envPrefix(t, "onfail")
			assert.Equal(t, row.name, envValue(t, onFail, "DISPAT_FAILED_STAGE"))
			assert.Contains(t, observer.stagesOf(EventStageStarted, "b"), row.name)
			failed := slices.IndexFunc(observer.events, func(ev Event) bool {
				return ev.Name == EventPackageFailed && ev.Package == "b"
			})
			require.GreaterOrEqual(t, failed, 0)
			assert.Equal(t, row.name, observer.events[failed].FailedStage)
		})
	}
}

// TestTestStageRunsBetweenBuildAndPublish: the frame runs after the build and
// before the publish, under its own name and hook names, and it is reported
// as a stage of its own.
func TestTestStageRunsBetweenBuildAndPublish(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.TestScript = []string{"check"}
	space.BeforeTestScript = []string{"pre-check"}
	space.PostTestScript = []string{"post-check"}
	r := &fakeRunner{}
	observer := &fakeObserver{}
	ex := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	ex.Observer = observer
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	order := []string{"build a", "pre-check a", "check a", "post-check a", "publish a"}
	for i := 1; i < len(order); i++ {
		assert.Less(t, r.indexOf(order[i-1]), r.indexOf(order[i]), "%s before %s", order[i-1], order[i])
	}
	assert.Equal(t, "test", envValue(t, r.envPrefix(t, "check"), "DISPAT_STAGE"))
	assert.Equal(t, "beforeTest", envValue(t, r.envPrefix(t, "pre-check"), "DISPAT_STAGE"))
	assert.Equal(t, "postTest", envValue(t, r.envPrefix(t, "post-check"), "DISPAT_STAGE"))
	assert.Equal(t, []string{"build", "test", "publish"}, observer.stagesOf(EventStageSucceeded, "a"))
}

// TestTestStageAbsentLeavesTheRunUnchanged: a space that configures none of
// the three sequences has no tests task, no event and no hook.
func TestTestStageAbsentLeavesTheRunUnchanged(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	r := &fakeRunner{}
	observer := &fakeObserver{}
	ex := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	ex.Observer = observer
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status)
	assert.Equal(t, []string{"build a", "publish a"}, r.events)
	assert.Equal(t, []string{"build", "publish"}, observer.stagesOf(EventStageSucceeded, "a"))
}

// TestTestStageHookAloneGivesTheStage: a hook somebody configured is never
// silent, so a postTest hook alone is a test stage with no commands.
func TestTestStageHookAloneGivesTheStage(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.PostTestScript = []string{"report"}
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Build: 1, Publish: 1}).
		Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status)
	assert.Equal(t, []string{"build a", "report a", "publish a"}, r.events)
}

// TestTestStageFailureGatesThePublish: a failed test frame fails the package
// at the test stage before anything is published, reverts its folder under
// revertOnFail, runs onFail with the stage it failed at, and skips a consumer
// that has no release reason of its own.
func TestTestStageFailureGatesThePublish(t *testing.T) {
	for _, failing := range []string{"pre-check", "check", "post-check"} {
		t.Run(failing, func(t *testing.T) {
			p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
			space := p.Releases["a"].Pkg.Space
			space.RevertOnFail = true
			space.TestScript = []string{"check"}
			space.BeforeTestScript = []string{"pre-check"}
			space.PostTestScript = []string{"post-check"}
			space.OnFailScript = []string{"onfail"}
			r := &fakeRunner{fail: map[string]bool{failing + " a": true}}
			rv := &fakeReverter{}
			tagger := &fakeTagger{}
			ex := newExecutor(execSpec{Runner: r, Tagger: tagger, Build: 2, Publish: 2})
			ex.Reverter = rv
			res := ex.Run(context.Background(), p)

			require.Equal(t, StatusFailed, res["a"].Status)
			assert.Equal(t, "test", res["a"].FailedStage)
			assert.Equal(t, -1, r.indexOf("publish a"), "a package that failed its tests is not published")
			assert.Contains(t, rv.dirs, "a")
			assert.Equal(t, "test", envValue(t, r.envPrefix(t, "onfail"), "DISPAT_FAILED_STAGE"))
			assert.NotContains(t, tagger.tags, p.Releases["a"].TagName())
			assert.Equal(t, StatusSkipped, res["b"].Status, "the consumer has no release reason of its own")
			assert.True(t, res["b"].Blocked)
			assert.Equal(t, "a", res["b"].BlockedBy)
		})
	}
}

// TestTestStageSharesTheBuildBudget: a test frame holds a build slot, so
// with one slot no build and no test frame ever run at the same moment.
func TestTestStageSharesTheBuildBudget(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a", "b", "c"}})
	p.Releases["a"].Pkg.Space.TestScript = []string{"check"}
	runner := &budgetRunner{fakeRunner: &fakeRunner{delay: 5 * time.Millisecond}}
	ex := newExecutor(execSpec{Runner: runner.fakeRunner, Tagger: &fakeTagger{}, Build: 1, Publish: 3})
	ex.Runner = runner
	res := ex.Run(context.Background(), p)

	for _, name := range []string{"a", "b", "c"} {
		require.Equal(t, StatusPublished, res[name].Status)
	}
	assert.Equal(t, 1, runner.peak, "one build slot is one build or test frame at a time")
}

// TestTestStageCarriesExports: what the build exported reaches the tests
// frame, and what the test frame exported reaches the publish, exactly as a
// build's export reaches the publish today.
func TestTestStageCarriesExports(t *testing.T) {
	space := &model.Space{
		Name:          "libs",
		BuildScript:   []string{`echo "ARTIFACT=core.tgz" >> "$DISPAT_OUTPUT"`},
		TestScript:    []string{`env > tests.env && echo "TESTED=core.tgz" >> "$DISPAT_OUTPUT"`},
		PublishScript: []string{`env > publish.env`},
	}
	p, dir := shellPlan(t, space)
	res := (&Executor{Runner: &script.ShellRunner{}, Log: zerolog.Nop()}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["core"].Status, "%v", res["core"].Err)

	read := func(file string) string {
		data, err := os.ReadFile(filepath.Join(dir, file))
		require.NoError(t, err, file)
		return string(data)
	}
	tests := read("tests.env")
	assert.Contains(t, tests, "DISPAT_OUTPUT_ARTIFACT=core.tgz\n", "the build's export reaches the test frame")
	assert.Contains(t, tests, "DISPAT_STAGE=test\n")
	publish := read("publish.env")
	assert.Contains(t, publish, "DISPAT_OUTPUT_ARTIFACT=core.tgz\n")
	assert.Contains(t, publish, "DISPAT_OUTPUT_TESTED=core.tgz\n", "the test frame's export reaches the publish")
	assert.Contains(t, publish, "DISPAT_OUTPUT_SOURCE_TESTED=core:test\n")
}

// TestTestStageTravelsThroughTheRemote: with a remote, the test frame is
// handed to it as the "test" stage, its exports are folded into the release,
// and a failure on a node is reported in the stage's own words.
func TestTestStageTravelsThroughTheRemote(t *testing.T) {
	t.Run("delegated", func(t *testing.T) {
		p := mkPlan(planSpec{Names: []string{"a"}})
		p.Releases["a"].Pkg.Space.TestScript = []string{"check"}
		remote := &fakeRemote{testsExports: testsExport()}
		ex := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
		ex.Remote = remote
		res := ex.Run(context.Background(), p)

		require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
		require.Len(t, remote.tests, 1)
		request := remote.tests[0]
		assert.Equal(t, "test", request.Stage)
		assert.Equal(t, []string{"check"}, request.Frame.Commands)
		assert.Equal(t, "test", envValue(t, request.Env, "DISPAT_STAGE"))
		assert.Equal(t, "build-a", res["a"].Worker)
		assert.Contains(t, p.Releases["a"].Outputs, testsExport()[0])
	})
	t.Run("failed on a node", func(t *testing.T) {
		p := mkPlan(planSpec{Names: []string{"a"}})
		p.Releases["a"].Pkg.Space.TestScript = []string{"check"}
		remote := &fakeRemote{failTests: "a", failTestsPart: PartCommands}
		ex := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
		ex.Remote = remote
		res := ex.Run(context.Background(), p)

		require.Equal(t, StatusFailed, res["a"].Status)
		assert.Equal(t, "test", res["a"].FailedStage)
		assert.Empty(t, remote.publishes)
		assert.Equal(t, "test script failed", formatRemoteFailure(stageOf(taskTest), PartCommands))
		assert.Equal(t, "beforeTest hook failed", formatRemoteFailure(stageOf(taskTest), PartBefore))
	})
	t.Run("placed here", func(t *testing.T) {
		p := mkPlan(planSpec{Names: []string{"a"}})
		p.Releases["a"].Pkg.Space.TestScript = []string{"check"}
		remote := &fakeRemote{placeHere: "a"}
		r := &fakeRunner{}
		ex := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
		ex.Remote = remote
		res := ex.Run(context.Background(), p)

		require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
		assert.GreaterOrEqual(t, r.indexOf("check a"), 0, "a frame placed here runs the local sequence")
	})
}
