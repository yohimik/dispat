// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// fakeRemote stands in for a pool of nodes: it records every frame it was
// given, answers what a scenario told it to, and reports how the guard was
// taken.
type fakeRemote struct {
	mu sync.Mutex
	// builds are the requests Build was called with, in order.
	builds []StageRequest
	// guards are the stage names Guard was called for, in order.
	guards []string
	// held counts the guards taken and not yet given back, with its peak, so
	// a test can prove a frame ran inside its bracket.
	held, peakHeld int
	// exports is what every build reports as its frame's exports.
	exports []plan.Output
	// failBuild fails the build of this package, at this part.
	failBuild, failPart string
	// guardErr fails every guard.
	guardErr error
	// beforeBuild runs inside Build, before it answers, which is how a
	// scenario cancels a run from inside a dispatched frame.
	beforeBuild func()
	// placeHere is the package whose build this pool places on the
	// orchestrator rather than on a node, which is what `runOnly:
	// orchestrator` and a full pool both come to.
	placeHere string
	// ranHere are the packages whose frame was run through the local
	// sequence, in order.
	ranHere []string

	// publishAway is the package whose publish this pool delegates. Every
	// other publish takes the local path, which is what the placement does for
	// everything but an explicit `runOnly: worker`.
	publishAway string
	// publishes are the requests Publish was called with, in order, and
	// publishedHere are the packages whose publish took the local path.
	publishes     []StageRequest
	publishedHere []string
	// steps is what a delegated publication did, in order, so that a test can
	// say where the authorization happened rather than only that it did.
	steps []string
	// authorizations counts the authorization callbacks made.
	authorizations int
	// publishExports is what a delegated publication reports as its exports.
	publishExports []plan.Output
	// failPublishPart fails the delegated publication at this part.
	failPublishPart string
}

// buildHere is the placement that keeps the frame: it runs the executor's own
// sequence and answers a node nobody has to be told about.
func (f *fakeRemote) buildHere(ctx context.Context, request StageRequest, here LocalFrame) (StageOutcome, error) {
	what, err := here(ctx)
	f.mu.Lock()
	f.ranHere = append(f.ranHere, request.Release.Pkg.Name)
	f.mu.Unlock()
	if err != nil {
		return StageOutcome{LocalFailure: what}, err
	}
	return StageOutcome{}, nil
}

func (f *fakeRemote) Guard(_ context.Context, stage string) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.guardErr != nil {
		return nil, f.guardErr
	}
	f.guards = append(f.guards, stage)
	f.held++
	if f.held > f.peakHeld {
		f.peakHeld = f.held
	}
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.held--
	}, nil
}

func (f *fakeRemote) Build(ctx context.Context, request StageRequest, here LocalFrame) (StageOutcome, error) {
	f.mu.Lock()
	isPlacedHere := f.placeHere == request.Release.Pkg.Name
	f.mu.Unlock()
	if isPlacedHere {
		return f.buildHere(ctx, request, here)
	}
	f.mu.Lock()
	f.builds = append(f.builds, request)
	isHeld := f.held
	shouldFail := f.failBuild == request.Release.Pkg.Name
	f.mu.Unlock()
	if f.beforeBuild != nil {
		f.beforeBuild()
	}
	outcome := StageOutcome{Node: "build-a", Exports: f.exports}
	if isHeld > 0 {
		// A build dispatched while a version frame holds the guard would be
		// snapshotting a folder somebody is writing into.
		return outcome, errors.New("a guard was held while a frame was dispatched")
	}
	if shouldFail {
		outcome.FailedPart = f.failPart
		return outcome, errors.New("the node reported a failure")
	}
	return outcome, nil
}

// Publish is the placement decision of a publish frame: the local path unless
// the scenario delegated this package, which is exactly how the real one
// behaves for everything but an explicit worker placement.
func (f *fakeRemote) Publish(ctx context.Context, request StageRequest, here LocalFrame,
	authorize func(context.Context) error) (StageOutcome, error) {
	f.mu.Lock()
	isDelegated := f.publishAway == request.Release.Pkg.Name
	f.mu.Unlock()
	if !isDelegated {
		what, err := here(ctx)
		f.mu.Lock()
		f.publishedHere = append(f.publishedHere, request.Release.Pkg.Name)
		f.mu.Unlock()
		if err != nil {
			return StageOutcome{LocalFailure: what}, err
		}
		return StageOutcome{}, nil
	}
	return f.publishAwayFrom(ctx, request, authorize)
}

// publishAwayFrom is the handshake as the executor sees it: the node reports
// the hook done, the orchestrator authorizes, and only then does the command
// start. Every step is recorded so a test can assert the order rather than the
// count.
func (f *fakeRemote) publishAwayFrom(ctx context.Context, request StageRequest,
	authorize func(context.Context) error) (StageOutcome, error) {
	f.mu.Lock()
	f.publishes = append(f.publishes, request)
	f.steps = append(f.steps, "hook done")
	f.mu.Unlock()
	if authorize != nil {
		err := authorize(ctx)
		f.mu.Lock()
		f.authorizations++
		f.mu.Unlock()
		if err != nil {
			return StageOutcome{Node: "build-a", FailedPart: PartAuthorization}, err
		}
	}
	f.mu.Lock()
	f.steps = append(f.steps, "command started")
	outcome := StageOutcome{Node: "build-a", Exports: f.publishExports}
	if f.failPublishPart != "" {
		outcome.FailedPart = f.failPublishPart
		f.mu.Unlock()
		return outcome, errors.New("the node reported a failure")
	}
	f.mu.Unlock()
	return outcome, nil
}

// requests answers the recorded builds under the lock.
func (f *fakeRemote) requests() []StageRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]StageRequest(nil), f.builds...)
}

// TestRunWithoutARemoteIsTheLocalRun: the seam with nothing behind it runs the
// frames it always ran, in the order it always ran them, and reports the same
// events. It is the whole containment of the distributed profile: a
// repository that configures no worker pays nothing for the existence of one.
func TestRunWithoutARemoteIsTheLocalRun(t *testing.T) {
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	runner := &fakeRunner{}
	observer := &fakeObserver{}
	executor := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 4, Publish: 4})
	executor.Observer = observer
	require.Nil(t, executor.Remote, "the local run is the zero value")

	results := executor.Run(context.Background(), p)

	for _, name := range []string{"a", "b"} {
		require.Equal(t, StatusPublished, results[name].Status, "%s: %v", name, results[name].Err)
	}
	assert.Equal(t, []string{"build a", "publish a", "build b", "publish b"}, runner.events,
		"the launch order of a local run")
	assert.Equal(t, []string{
		"stage.started:build", "stage.succeeded:build",
		"stage.started:publish", "stage.succeeded:publish",
		"package.published:",
	}, observer.forPackage("a"), "the event sequence of a package with no version stage")
	assert.Equal(t, []string{
		"stage.started:version", "stage.succeeded:version",
		"stage.started:build", "stage.succeeded:build",
		"stage.started:publish", "stage.succeeded:publish",
		"package.published:",
	}, observer.forPackage("b"), "the event sequence of a package the provider moved")
}

// TestRemoteBuildsRunOnceWithTheLocalFrame: every releasing package's build
// frame is handed to the pool exactly once, and what travels is what the local
// path would have executed — the same commands, the same folder, the computed
// environment, and the configuration's static pairs still unresolved.
func TestRemoteBuildsRunOnceWithTheLocalFrame(t *testing.T) {
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	space := p.Releases["a"].Pkg.Space
	space.BeforeBuildScript = []string{"pre"}
	space.PostBuildScript = []string{"post"}
	space.Env = []string{"NPM_TOKEN=$NPM_TOKEN"}
	remote := &fakeRemote{exports: []plan.Output{{Name: "IMAGE", Value: "acme/a:1.0.1", Source: "a:build"}}}
	runner := &fakeRunner{}
	executor := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 4, Publish: 4})
	executor.Remote = remote

	results := executor.Run(context.Background(), p)

	for _, name := range []string{"a", "b"} {
		require.Equal(t, StatusPublished, results[name].Status, "%s: %v", name, results[name].Err)
	}
	requests := remote.requests()
	require.Len(t, requests, 2, "one build frame per releasing package")
	byPackage := map[string]StageRequest{}
	for _, request := range requests {
		byPackage[request.Release.Pkg.Name] = request
	}
	request := byPackage["a"]
	assert.Equal(t, "build", request.Stage)
	assert.Equal(t, []string{"pre"}, request.Frame.Before, "the bracketing hooks travel with the stage")
	assert.Equal(t, []string{"build"}, request.Frame.Commands)
	assert.Equal(t, []string{"post"}, request.Frame.After)
	assert.Equal(t, "a", request.Dir, "the folder the local path would have run in")
	assert.Contains(t, request.Env, "DISPAT_STAGE=build")
	assert.Contains(t, request.Env, "DISPAT_VERSION=1.0.1")
	assert.Equal(t, []string{"NPM_TOKEN=$NPM_TOKEN"}, request.StaticEnv,
		"a static pair travels as the reference it is, never as a resolved secret")
	for _, pair := range request.Env {
		assert.NotContains(t, pair, "NPM_TOKEN=", "the static pairs are not folded into the computed ones")
	}

	assert.Equal(t, 0, runner.countPrefix("pre"), "no part of the build frame ran here")
	assert.Equal(t, 0, runner.countPrefix("build"))
	assert.Equal(t, 0, runner.countPrefix("post"))
	assert.Equal(t, 2, runner.countPrefix("publish"), "publication stays on the orchestrator")
	assert.Equal(t, "acme/a:1.0.1", envValue(t, runner.envPrefix(t, "publish"),
		"DISPAT_OUTPUT_IMAGE"), "what the node exported reaches the stages after it")
}

// TestRemoteBuildFailureFailsThePackageLocally: a node reporting a failure
// fails the package at its build stage with the label the failing part
// deserves, and the outcome scripts still run here, on the orchestrator.
func TestRemoteBuildFailureFailsThePackageLocally(t *testing.T) {
	for name, tc := range map[string]struct {
		part string
		what string
	}{
		"a hook before the stage": {part: PartBefore, what: "beforeBuild hook failed"},
		"the stage's own script":  {part: PartCommands, what: "build script failed"},
		"a hook after the stage":  {part: PartAfter, what: "postBuild hook failed"},
		"a node that never said":  {part: "", what: "remote build failed"},
	} {
		t.Run(name, func(t *testing.T) {
			p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
			p.Releases["a"].Pkg.Space.OnFailScript = []string{"onfail"}
			runner := &fakeRunner{}
			executor := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 4, Publish: 4})
			executor.Remote = &fakeRemote{failBuild: "a", failPart: tc.part}

			results := executor.Run(context.Background(), p)

			require.Equal(t, StatusFailed, results["a"].Status)
			assert.Equal(t, "build", results["a"].FailedStage)
			assert.Equal(t, StatusSkipped, results["b"].Status, "the consumer is blocked")
			assert.Equal(t, "a", results["b"].BlockedBy)
			assert.Equal(t, 1, runner.countPrefix("onfail"), "the outcome script ran on the orchestrator")
			assert.Equal(t, tc.what, formatRemoteFailure(taskBuild, tc.part))
		})
	}
}

// TestRemoteBuildCancellationCancelsThePackage: a run interrupted while a
// frame is in flight cancels the package rather than failing it, and runs no
// outcome script — the same rule a killed local script follows.
func TestRemoteBuildCancellationCancelsThePackage(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.OnFailScript = []string{"onfail"}
	ctx, cancel := context.WithCancel(context.Background())
	remote := &fakeRemote{failBuild: "a", failPart: PartCommands}
	remote.beforeBuild = cancel
	runner := &fakeRunner{}
	executor := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	executor.Remote = remote

	results := executor.Run(ctx, p)

	require.Equal(t, StatusCancelled, results["a"].Status)
	assert.Equal(t, 0, runner.countPrefix("onfail"), "an interruption runs no outcome script")
}

// TestGuardBracketsTheOrchestratorsOwnFrames: the version and syncLock stages
// are the writers of the working tree a dispatched build is snapshotted from,
// so each of them runs inside the guard, and a guard that cannot be taken
// fails the package at its own stage.
func TestGuardBracketsTheOrchestratorsOwnFrames(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true, SyncLock: []string{"locksync"}})
	seedFile(t, root, "a/package.json", `{"name": "@acme/a", "version": "1.0.0"}`)
	p := avPlan(root, space, "a")
	fillUpdates(p)
	remote := &fakeRemote{}
	executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 2, Publish: 2})
	executor.Remote = remote

	results := executor.Run(context.Background(), p)

	require.Equal(t, StatusPublished, results["a"].Status, "%v", results["a"].Err)
	assert.Equal(t, []string{"version", "syncLock"}, remote.guards,
		"the two frames that write the folder a snapshot is taken of")
	assert.Equal(t, 0, remote.held, "every guard was given back")
	assert.Len(t, remote.requests(), 1, "and the build still went to a node")
	assert.Equal(t, filepath.Join(root, "a"), remote.requests()[0].Dir)
}

// TestGuardFailureFailsTheStageItBrackets: a guard that cannot be taken is a
// run that cannot keep its snapshots whole, so the stage fails rather than
// proceeding unguarded.
func TestGuardFailureFailsTheStageItBrackets(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true})
	seedFile(t, root, "a/package.json", `{"name": "@acme/a", "version": "1.0.0"}`)
	p := avPlan(root, space, "a")
	fillUpdates(p)
	executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 2, Publish: 2})
	executor.Remote = &fakeRemote{guardErr: errors.New("the run was interrupted")}

	results := executor.Run(context.Background(), p)

	require.Equal(t, StatusFailed, results["a"].Status)
	assert.Equal(t, "version", results["a"].FailedStage)
}

// TestRemoteStageRequestCarriesTheAccumulatedExports: a build frame dispatched
// after an earlier stage exported something carries that state, because the
// computed environment is the one the local path would have built at the same
// moment.
func TestRemoteStageRequestCarriesTheAccumulatedExports(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Outputs = []plan.Output{{Name: "TOKEN", Value: "earlier", Source: "a:version"}}
	remote := &fakeRemote{}
	executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	executor.Remote = remote

	executor.Run(context.Background(), p)

	require.Len(t, remote.requests(), 1)
	assert.Contains(t, remote.requests()[0].Env, "DISPAT_OUTPUT_TOKEN=earlier")
	assert.Contains(t, remote.requests()[0].Env, "DISPAT_OUTPUTS=TOKEN")
}

// TestRunCollectingOutputsIsRunMergingOutputsWithoutAPlan: the export rules a
// node runs somebody else's frame under are the rules the local path runs its
// own under, which is what makes one implementation enough.
func TestRunCollectingOutputsIsRunMergingOutputsWithoutAPlan(t *testing.T) {
	runner := &exportingRunner{line: "DISPAT_OUTPUT_IMAGE=acme/core:1"}
	sequence := Sequence{Runner: runner, Dir: "core", Stage: "build",
		Commands: []string{"build"}, Log: zerolog.Nop(), FailFast: true}

	outs, err := sequence.RunCollectingOutputs(context.Background(), "core:build")

	require.NoError(t, err)
	require.Len(t, outs, 1)
	assert.Equal(t, plan.Output{Name: "IMAGE", Value: "acme/core:1", Source: "core:build"}, outs[0])

	rel := &plan.Release{Pkg: &model.Package{Name: "core", Space: &model.Space{Name: "libs"}}}
	require.NoError(t, sequence.RunMergingOutputs(context.Background(), rel))
	assert.Equal(t, outs, rel.Outputs, "the merging caller folds exactly what the collecting one answers")
}

// TestEmptyCollectingSequenceRunsNothing pins the one shape both callers share
// with the local path: a stage with no configured command executes nothing and
// exports nothing.
func TestEmptyCollectingSequenceRunsNothing(t *testing.T) {
	runner := &exportingRunner{}
	sequence := Sequence{Runner: runner, Dir: "core", Stage: "build", Log: zerolog.Nop()}

	outs, err := sequence.RunCollectingOutputs(context.Background(), "core:build")

	require.NoError(t, err)
	assert.Empty(t, outs)
	assert.Zero(t, runner.runs)
}

// exportingRunner is a runner that writes one export line into whatever file
// DISPAT_OUTPUT names, which is the only thing the collecting sequence is
// about.
type exportingRunner struct {
	line string
	runs int
}

func (r *exportingRunner) Run(_ context.Context, _, _ string, env []string, _, _ io.Writer) error {
	r.runs++
	if r.line == "" {
		return nil
	}
	for _, pair := range env {
		path, isOutput := strings.CutPrefix(pair, OutputEnvVar+"=")
		if !isOutput {
			continue
		}
		return os.WriteFile(path, []byte(r.line+"\n"), 0o644)
	}
	return nil
}

// TestABuildPlacedHereRunsTheLocalFrame: a build the run places on the
// orchestrator runs the executor's own sequence, hooks and all, in the
// package's own folder; nothing about it travels, and nothing about it names
// a worker, because the machine that ran it is the machine writing the line.
func TestABuildPlacedHereRunsTheLocalFrame(t *testing.T) {
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	space := p.Releases["a"].Pkg.Space
	space.BeforeBuildScript = []string{"pre"}
	space.PostBuildScript = []string{"post"}
	remote := &fakeRemote{placeHere: "a"}
	runner := &fakeRunner{}
	executor := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 4, Publish: 4})
	executor.Remote = remote

	results := executor.Run(context.Background(), p)

	for _, name := range []string{"a", "b"} {
		require.Equal(t, StatusPublished, results[name].Status, "%s: %v", name, results[name].Err)
	}
	assert.Equal(t, []string{"a"}, remote.ranHere, "the placed frame ran through the local sequence")
	assert.Equal(t, 1, runner.countPrefix("pre"), "its hooks ran here with it")
	assert.Equal(t, 1, runner.countPrefix("build"))
	assert.Equal(t, 1, runner.countPrefix("post"))
	assert.Empty(t, results["a"].Worker, "a frame that ran here names no worker")

	requests := remote.requests()
	require.Len(t, requests, 1, "only the other package's frame travelled")
	assert.Equal(t, "b", requests[0].Release.Pkg.Name)
	assert.Equal(t, "build-a", results["b"].Worker, "and that one still names the node it ran on")
}

// TestABuildPlacedHereFailsLikeALocalBuild: a frame that failed on this
// machine fails its package at its build stage with the sentence the local
// path has always printed, and the outcome script still runs.
func TestABuildPlacedHereFailsLikeALocalBuild(t *testing.T) {
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	p.Releases["a"].Pkg.Space.OnFailScript = []string{"onfail"}
	runner := &fakeRunner{fail: map[string]bool{"build a": true}}
	executor := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 4, Publish: 4})
	executor.Remote = &fakeRemote{placeHere: "a"}

	results := executor.Run(context.Background(), p)

	require.Equal(t, StatusFailed, results["a"].Status)
	assert.Equal(t, "build", results["a"].FailedStage)
	assert.Empty(t, results["a"].Worker, "a failure here is nobody else's")
	assert.Equal(t, StatusSkipped, results["b"].Status, "the consumer is blocked")
	assert.Equal(t, 1, runner.countPrefix("onfail"), "the outcome script ran")
}

// TestRemotePublicationAuthorizesBetweenTheHookAndTheCommand: the whole point
// of the seam. The node reports its hook done, the run makes the check only it
// can make, and the command starts after that and never before. What the
// frame exported still reaches the stages that follow, and the tail of the
// publication still runs here.
func TestRemotePublicationAuthorizesBetweenTheHookAndTheCommand(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	space := p.Releases["a"].Pkg.Space
	space.BeforePublishScript = []string{"prepub"}
	remote := &fakeRemote{publishAway: "a",
		publishExports: []plan.Output{{Name: "URL", Value: "acme/a", Source: "a:publish"}}}
	runner := &fakeRunner{}
	tagger := &fakeTagger{}
	executor := newExecutor(execSpec{Runner: runner, Tagger: tagger, Build: 2, Publish: 2})
	executor.Remote = remote
	checked := 0
	executor.BeforePublish = func(context.Context, *plan.Release) error {
		checked++
		return nil
	}

	results := executor.Run(context.Background(), p)

	require.Equal(t, StatusPublished, results["a"].Status, "%v", results["a"].Err)
	assert.Equal(t, 1, checked, "the authorization was asked exactly once")
	assert.Equal(t, []string{"hook done", "command started"}, remote.steps,
		"and it happened between the hook and the command")
	assert.Equal(t, 1, remote.authorizations)
	require.Len(t, remote.publishes, 1)
	assert.Equal(t, []string{"prepub"}, remote.publishes[0].Frame.Before,
		"the hook travels with the frame it brackets")
	assert.Equal(t, []string{"publish"}, remote.publishes[0].Frame.Commands)
	assert.Equal(t, 0, runner.countPrefix("prepub"), "no part of the frame ran here")
	assert.Equal(t, 0, runner.countPrefix("publish"))
	assert.Equal(t, []string{"a@1.0.1"}, tagger.tags, "and the tail of the publication ran here")
	assert.Equal(t, []plan.Output{{Name: "URL", Value: "acme/a", Source: "a:publish"}},
		p.Releases["a"].Outputs, "what the node exported reached the release")
	assert.Equal(t, "build-a", results["a"].Worker)
}

// TestRefusedAuthorizationNeverStartsTheCommand: a check that refuses fails the
// package with the sentence the local path prints for the same refusal, and
// the node is never told to start anything.
func TestRefusedAuthorizationNeverStartsTheCommand(t *testing.T) {
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	remote := &fakeRemote{publishAway: "a"}
	tagger := &fakeTagger{}
	executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: tagger, Build: 2, Publish: 2})
	executor.Remote = remote
	executor.BeforePublish = func(_ context.Context, rel *plan.Release) error {
		if rel.Pkg.Name != "a" {
			return nil
		}
		return errors.New("the relevant inputs changed after the build")
	}

	results := executor.Run(context.Background(), p)

	require.Equal(t, StatusFailed, results["a"].Status)
	assert.Equal(t, "publish", results["a"].FailedStage)
	assert.Equal(t, []string{"hook done"}, remote.steps, "the command never started")
	assert.Empty(t, tagger.tags, "and nothing was tagged")
	assert.Equal(t, "pre-publish repository validation failed",
		formatRemoteFailure(taskPublish, PartAuthorization),
		"a withheld publication reads as the local path's own refusal")
	assert.Equal(t, StatusSkipped, results["b"].Status, "the consumer is blocked")
}

// TestPublishPlacedHereIsTheLocalPublish: a publication this run keeps runs the
// executor's own sequence, revalidation included, in this checkout. It is the
// default placement, so it is also what every package of a distributed run
// does unless the operator asked otherwise.
func TestPublishPlacedHereIsTheLocalPublish(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.BeforePublishScript = []string{"prepub"}
	remote := &fakeRemote{placeHere: "a"}
	runner := &fakeRunner{}
	executor := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 2, Publish: 2})
	executor.Remote = remote
	checked := 0
	executor.BeforePublish = func(context.Context, *plan.Release) error {
		checked++
		return nil
	}

	results := executor.Run(context.Background(), p)

	require.Equal(t, StatusPublished, results["a"].Status, "%v", results["a"].Err)
	assert.Equal(t, []string{"a"}, remote.publishedHere)
	assert.Equal(t, 1, runner.countPrefix("prepub"), "the hook ran here")
	assert.Equal(t, 1, runner.countPrefix("publish"), "and so did the command")
	assert.Equal(t, 1, checked, "the revalidation still ran between the two")
	assert.Empty(t, remote.steps, "nothing was delegated")
	assert.Empty(t, results["a"].Worker, "a publication that ran here names no worker")
}

// TestRemotePublishFailureIsALocalFailure: a node whose publish command failed
// fails the package at its publish stage with the label the failing part
// deserves, nothing is tagged, and the lane the publication held is given back
// so the packages behind it still run.
func TestRemotePublishFailureIsALocalFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		part string
		what string
	}{
		"the hook before the stage": {part: PartBefore, what: "beforePublish hook failed"},
		"the publish command":       {part: PartCommands, what: "publish script failed"},
	} {
		t.Run(name, func(t *testing.T) {
			p := mkPlan(planSpec{Names: []string{"a", "c"}})
			tagger := &fakeTagger{}
			// Two packages publish concurrently, so the lane counter is written
			// by two goroutines and read by the test.
			var lanes atomic.Int32
			executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: tagger, Build: 2, Publish: 2})
			executor.Remote = &fakeRemote{publishAway: "a", failPublishPart: tc.part}
			executor.AcquirePublish = func(context.Context, *plan.Release) (func(), error) {
				lanes.Add(1)
				return func() { lanes.Add(-1) }, nil
			}

			results := executor.Run(context.Background(), p)

			require.Equal(t, StatusFailed, results["a"].Status)
			assert.Equal(t, "publish", results["a"].FailedStage)
			assert.Equal(t, []string{"c@1.0.1"}, tagger.tags, "the other package still published")
			assert.Equal(t, int32(0), lanes.Load(), "the publication lane was given back on every path")
			assert.Equal(t, tc.what, formatRemoteFailure(taskPublish, tc.part))
		})
	}
}
