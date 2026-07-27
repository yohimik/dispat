package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/monorel/internal/conventional"
	"github.com/yohimik/monorel/internal/model"
	"github.com/yohimik/monorel/internal/plan"
	"github.com/yohimik/monorel/internal/semver"
)

// fakeRunner records "command pkgDir" events, captures script environments,
// tracks per-command concurrency and can fail selected events.
type fakeRunner struct {
	mu      sync.Mutex
	events  []string
	envs    map[string][]string
	fail    map[string]bool
	delay   time.Duration
	current map[string]int
	maxCur  map[string]int
}

func (r *fakeRunner) Run(_ context.Context, dir, command string, env []string, _, _ io.Writer) error {
	key := command + " " + dir
	r.mu.Lock()
	if r.current == nil {
		r.current = map[string]int{}
		r.maxCur = map[string]int{}
		r.envs = map[string][]string{}
	}
	r.events = append(r.events, key)
	r.envs[key] = env
	r.current[command]++
	if r.current[command] > r.maxCur[command] {
		r.maxCur[command] = r.current[command]
	}
	shouldFail := r.fail[key]
	r.mu.Unlock()

	if r.delay > 0 {
		time.Sleep(r.delay)
	}

	r.mu.Lock()
	r.current[command]--
	r.mu.Unlock()
	if shouldFail {
		return errors.New("boom")
	}
	return nil
}

func (r *fakeRunner) indexOf(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.events {
		if e == key {
			return i
		}
	}
	return -1
}

type fakeTagger struct {
	mu   sync.Mutex
	tags []string
}

func (f *fakeTagger) CreateTag(_ context.Context, name, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags = append(f.tags, name)
	return nil
}

type fakeChangelog struct {
	mu      sync.Mutex
	entries []string
	fail    bool
}

func (f *fakeChangelog) Record(_ context.Context, rel *plan.Release) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("recorder boom")
	}
	f.entries = append(f.entries, rel.Pkg.Name)
	return nil
}

// mkPlan builds a plan of changed packages. waitPublish applies to the "libs"
// space that every package lives in. ownBump marks packages with own commits.
// Consumers listed in deps get DueTo set, mirroring plan.Compute (every listed
// package is changed).
func mkPlan(waitPublish bool, ownBump map[string]semver.Bump, deps map[string][]string, names ...string) *plan.Plan {
	libs := &model.Space{Name: "libs", BuildWaitsPublish: waitPublish, BuildScript: "build", PublishScript: "publish"}
	p := &plan.Plan{
		Releases:  map[string]*plan.Release{},
		Providers: map[string][]string{},
		Consumers: map[string][]string{},
	}
	for _, n := range names {
		own := ownBump[n]
		bump := semver.Max(own, semver.BumpPatch) // every listed package is changed
		rel := &plan.Release{
			Pkg:     &model.Package{Name: n, Dir: n, Space: libs},
			Current: semver.Version{Major: 1},
			OwnBump: own,
			Bump:    bump,
		}
		if own != semver.BumpNone {
			rel.Commits = []conventional.Commit{{Kind: conventional.KindFix, Scope: n, Description: "own change"}}
		}
		rel.Next = rel.Current.Bumped(bump)
		p.Releases[n] = rel
		p.Order = append(p.Order, n)
	}
	for consumer, provs := range deps {
		p.Providers[consumer] = provs
		p.Releases[consumer].DueTo = provs
		for _, prov := range provs {
			p.Consumers[prov] = append(p.Consumers[prov], consumer)
		}
	}
	return p
}

type fakeReverter struct {
	mu   sync.Mutex
	dirs []string
	err  error
}

func (f *fakeReverter) RevertDir(_ context.Context, dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dirs = append(f.dirs, dir)
	return f.err
}

func newExecutor(r *fakeRunner, tg *fakeTagger, cl *fakeChangelog, buildConc, publishConc int) *Executor {
	return &Executor{
		BuildConcurrency:   buildConc,
		PublishConcurrency: publishConc,
		Runner:             r,
		Tagger:             tg,
		Recorders:          []ReleaseRecorder{cl},
		Log:                zerolog.Nop(),
	}
}

func TestRunSuccessOrder(t *testing.T) {
	// b consumes a; waitPublish=true gives the strictest ordering guarantees.
	p := mkPlan(true, nil, map[string][]string{"b": {"a"}}, "a", "b")
	r := &fakeRunner{}
	tg := &fakeTagger{}
	cl := &fakeChangelog{}
	res := newExecutor(r, tg, cl, 4, 4).Run(context.Background(), p)

	for _, n := range []string{"a", "b"} {
		require.Equal(t, StatusPublished, res[n].Status, "%s: %v", n, res[n].Err)
	}
	assert.Less(t, r.indexOf("build a"), r.indexOf("publish a"), "a builds before publishing")
	assert.Less(t, r.indexOf("build a"), r.indexOf("build b"), "provider builds before consumer")
	assert.Less(t, r.indexOf("publish a"), r.indexOf("publish b"), "provider publishes before consumer")
	assert.ElementsMatch(t, []string{"a@1.0.1", "b@1.0.1"}, tg.tags)
	assert.ElementsMatch(t, []string{"a", "b"}, cl.entries)
	assert.Equal(t, semver.Version{Major: 1, Patch: 1}, res["a"].To)
}

func TestRunBuildWaitsPublish(t *testing.T) {
	p := mkPlan(true, nil, map[string][]string{"b": {"a"}}, "a", "b")
	r := &fakeRunner{}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["b"].Status)
	assert.Less(t, r.indexOf("publish a"), r.indexOf("build b"),
		"with isBuildWaitingPublish, b may only build after a is published")
}

func TestRunNoWaitProviderPublishFailureSkipsConsumers(t *testing.T) {
	// isBuildWaitingPublish=false, chain a -> b -> c, none with own commits.
	// a's PUBLISH fails. b may already have built (that is the trade-off the
	// flag opts into), but its publish always waits for a's publish: seeing
	// the failure, b is skipped, and c cascades to skipped the same way.
	p := mkPlan(false, nil, map[string][]string{"b": {"a"}, "c": {"b"}}, "a", "b", "c")
	r := &fakeRunner{fail: map[string]bool{"publish a": true}}
	tg := &fakeTagger{}
	cl := &fakeChangelog{}
	res := newExecutor(r, tg, cl, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "publish", res["a"].FailedStage)
	require.Equal(t, StatusSkipped, res["b"].Status, "%v", res["b"].Err)
	require.Equal(t, StatusSkipped, res["c"].Status, "%v", res["c"].Err)
	// Whether b's build ran before a's publish failure landed is timing-
	// dependent (the trade-off of isBuildWaitingPublish=false), but b must
	// never publish against an unpublished provider, and nothing is tagged.
	assert.Equal(t, -1, r.indexOf("publish b"))
	assert.Equal(t, -1, r.indexOf("publish c"))
	assert.Empty(t, tg.tags, "nothing may be tagged")
	assert.Empty(t, cl.entries)
}

func TestRunNoWaitConsumerOwnBuildFailureCascades(t *testing.T) {
	// isBuildWaitingPublish=false, chain a -> b -> c. b's own BUILD fails:
	// b is failed and c is skipped — a broken build gates consumers in both
	// modes.
	p := mkPlan(false, nil, map[string][]string{"b": {"a"}, "c": {"b"}}, "a", "b", "c")
	r := &fakeRunner{fail: map[string]bool{"build b": true}}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status)
	require.Equal(t, StatusFailed, res["b"].Status)
	assert.Equal(t, "build", res["b"].FailedStage)
	require.Equal(t, StatusSkipped, res["c"].Status)
	assert.Equal(t, -1, r.indexOf("build c"))
}

func TestRunNoWaitConsumerPublishFailureDownstreamSkipped(t *testing.T) {
	// isBuildWaitingPublish=false, chain a -> b -> c. b's own PUBLISH fails:
	// a is unaffected, b is failed, and c — whose publish always waits for
	// b's — is skipped (no changes of its own).
	p := mkPlan(false, nil, map[string][]string{"b": {"a"}, "c": {"b"}}, "a", "b", "c")
	r := &fakeRunner{fail: map[string]bool{"publish b": true}}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status)
	require.Equal(t, StatusFailed, res["b"].Status)
	assert.Equal(t, "publish", res["b"].FailedStage)
	require.Equal(t, StatusSkipped, res["c"].Status, "%v", res["c"].Err)
	assert.Equal(t, -1, r.indexOf("publish c"))
}

func TestRunFailureSkipsConsumer(t *testing.T) {
	// isBuildWaitingPublish=true: the provider's PUBLISH failure is final
	// before the consumer's version stage (which waits for provider builds
	// AND publishes), so the skip decision is deterministic.
	p := mkPlan(true, nil, map[string][]string{"b": {"a"}}, "a", "b")
	p.Releases["b"].Pkg.Space.VersionScript = "version"
	r := &fakeRunner{fail: map[string]bool{"publish a": true}}
	tg := &fakeTagger{}
	res := newExecutor(r, tg, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Error(t, res["a"].Err)
	require.Equal(t, StatusSkipped, res["b"].Status, "b has no changes of its own")
	assert.Equal(t, -1, r.indexOf("version b"), "skipped consumer must not run its version script")
	assert.Equal(t, -1, r.indexOf("build b"), "skipped consumer must not run any script")
	assert.Empty(t, tg.tags)
}

func TestRunFailureConsumerWithOwnChangesProceeds(t *testing.T) {
	p := mkPlan(true, map[string]semver.Bump{"b": semver.BumpPatch},
		map[string][]string{"b": {"a"}}, "a", "b")
	r := &fakeRunner{fail: map[string]bool{"build a": true}}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	require.Equal(t, StatusPublished, res["b"].Status,
		"b must proceed despite provider failure: %v", res["b"].Err)
}

func TestRunSkipCascades(t *testing.T) {
	// chain a -> b -> c: a fails, b skipped, c skipped too; no version
	// script may run anywhere down the chain.
	p := mkPlan(true, nil, map[string][]string{"b": {"a"}, "c": {"b"}}, "a", "b", "c")
	p.Releases["a"].Pkg.Space.VersionScript = "version"
	r := &fakeRunner{fail: map[string]bool{"build a": true}}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	assert.Equal(t, StatusSkipped, res["b"].Status)
	assert.Equal(t, StatusSkipped, res["c"].Status)
	assert.Equal(t, -1, r.indexOf("version b"))
	assert.Equal(t, -1, r.indexOf("version c"))
}

func TestRunConcurrencyLimits(t *testing.T) {
	names := []string{"p1", "p2", "p3", "p4", "p5", "p6"}
	p := mkPlan(false, nil, nil, names...)
	r := &fakeRunner{delay: 10 * time.Millisecond}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 2, 2).Run(context.Background(), p)

	for _, n := range names {
		require.Equal(t, StatusPublished, res[n].Status, n)
	}
	assert.LessOrEqual(t, r.maxCur["build"], 2, "build budget")
	assert.LessOrEqual(t, r.maxCur["publish"], 2, "publish budget")
}

func TestRunSeparateStageBudgets(t *testing.T) {
	names := []string{"p1", "p2", "p3", "p4"}
	p := mkPlan(false, nil, nil, names...)
	r := &fakeRunner{delay: 10 * time.Millisecond}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 3, 1).Run(context.Background(), p)

	for _, n := range names {
		require.Equal(t, StatusPublished, res[n].Status, n)
	}
	assert.LessOrEqual(t, r.maxCur["build"], 3, "build budget")
	assert.LessOrEqual(t, r.maxCur["publish"], 1, "publishes must be serialized")
	// With independent budgets, builds are allowed to overlap while a
	// publish is running, so builds should actually reach parallelism > 1.
	assert.Greater(t, r.maxCur["build"], 1, "builds should overlap")
}

func TestRunUnchangedExcluded(t *testing.T) {
	p := mkPlan(false, nil, nil, "a")
	// add an unchanged package to the plan
	p.Releases["quiet"] = &plan.Release{
		Pkg:     &model.Package{Name: "quiet", Dir: "quiet", Space: p.Releases["a"].Pkg.Space},
		Current: semver.Version{Major: 3},
		Next:    semver.Version{Major: 3},
	}
	p.Order = append(p.Order, "quiet")
	res := newExecutor(&fakeRunner{}, &fakeTagger{}, &fakeChangelog{}, 1, 1).Run(context.Background(), p)

	assert.NotContains(t, res, "quiet", "unchanged package must not appear in results")
	assert.Len(t, res, 1)
}

func envValue(t *testing.T, env []string, key string) string {
	t.Helper()
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	t.Fatalf("env %v is missing %s", env, key)
	return ""
}

func TestRunVersionTaskOrderingAndEnv(t *testing.T) {
	// b consumes a; the space has a version script.
	p := mkPlan(true, nil, map[string][]string{"b": {"a"}}, "a", "b")
	p.Releases["a"].Pkg.Space.VersionScript = "version"
	r := &fakeRunner{}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["b"].Status, "%v", res["b"].Err)
	// Only the provider-bumped package runs the version stage.
	assert.Equal(t, -1, r.indexOf("version a"), "a has no provider updates")
	require.NotEqual(t, -1, r.indexOf("version b"), "b must run the version stage")
	assert.Less(t, r.indexOf("publish a"), r.indexOf("version b"),
		"version runs after provider tasks (waitPublish)")
	assert.Less(t, r.indexOf("version b"), r.indexOf("build b"),
		"version runs exactly before the consumer's build")

	env := r.envs["version b"]
	assert.Equal(t, "b", envValue(t, env, "MONOREL_PACKAGE"))
	assert.Equal(t, "version", envValue(t, env, "MONOREL_STAGE"))

	var updates []map[string]string
	require.NoError(t, json.Unmarshal([]byte(envValue(t, env, "MONOREL_UPDATED_PROVIDERS")), &updates))
	require.Len(t, updates, 1)
	assert.Equal(t, "a", updates[0]["package"])
	assert.Equal(t, "libs", updates[0]["space"])
	assert.Equal(t, "1.0.0", updates[0]["oldVersion"])
	assert.Equal(t, "1.0.1", updates[0]["newVersion"])
}

func TestRunVersionScriptFailureFailsPackage(t *testing.T) {
	p := mkPlan(false, nil, map[string][]string{"b": {"a"}}, "a", "b")
	p.Releases["a"].Pkg.Space.VersionScript = "version"
	r := &fakeRunner{fail: map[string]bool{"version b": true}}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status)
	require.Equal(t, StatusFailed, res["b"].Status)
	assert.Equal(t, -1, r.indexOf("build b"), "build must not run after a failed version stage")
}

func TestRunVersionOrderingNoWaitPublish(t *testing.T) {
	// isBuildWaitingPublish=false: the version stage only waits for provider
	// builds, not their publishes.
	p := mkPlan(false, nil, map[string][]string{"b": {"a"}}, "a", "b")
	p.Releases["b"].Pkg.Space.VersionScript = "version"
	r := &fakeRunner{}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["b"].Status, "%v", res["b"].Err)
	require.NotEqual(t, -1, r.indexOf("version b"))
	assert.Less(t, r.indexOf("build a"), r.indexOf("version b"),
		"version waits for the provider's build")
	assert.Less(t, r.indexOf("version b"), r.indexOf("build b"),
		"version runs exactly before the consumer's build")
	// The consumer's publish still waits for the provider's publish.
	assert.Less(t, r.indexOf("publish a"), r.indexOf("publish b"))
}

func TestRunVersionSkippedOnProviderBuildFailureNoWait(t *testing.T) {
	// isBuildWaitingPublish=false: the provider's BUILD failure is final
	// before the consumer's version stage, so the consumer (no own changes)
	// is skipped and neither version nor build run.
	p := mkPlan(false, nil, map[string][]string{"b": {"a"}}, "a", "b")
	p.Releases["b"].Pkg.Space.VersionScript = "version"
	r := &fakeRunner{fail: map[string]bool{"build a": true}}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	require.Equal(t, StatusSkipped, res["b"].Status)
	assert.Equal(t, -1, r.indexOf("version b"))
	assert.Equal(t, -1, r.indexOf("build b"))
}

func TestRunVersionSkippedWhenAllProvidersFailed(t *testing.T) {
	// b has its own bump AND a provider bump; the provider fails. b still
	// releases (own changes), but the version script must NOT run: there is
	// no successfully updated provider to sync manifests to.
	p := mkPlan(true, map[string]semver.Bump{"b": semver.BumpPatch},
		map[string][]string{"b": {"a"}}, "a", "b")
	p.Releases["a"].Pkg.Space.VersionScript = "version"
	r := &fakeRunner{fail: map[string]bool{"publish a": true}}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	require.Equal(t, StatusPublished, res["b"].Status, "%v", res["b"].Err)
	assert.Equal(t, -1, r.indexOf("version b"), "version script must not run when every provider failed")
	assert.NotEqual(t, -1, r.indexOf("build b"), "build still runs for the own-bump release")
}

func TestRunVersionFiltersFailedProviders(t *testing.T) {
	// b consumes a1 (fails) and a2 (succeeds) and has its own bump: the
	// version script runs, but only a2 appears in MONOREL_UPDATED_PROVIDERS.
	p := mkPlan(true, map[string]semver.Bump{"b": semver.BumpPatch},
		map[string][]string{"b": {"a1", "a2"}}, "a1", "a2", "b")
	p.Releases["b"].Pkg.Space.VersionScript = "version"
	r := &fakeRunner{fail: map[string]bool{"publish a1": true}}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a1"].Status)
	require.Equal(t, StatusPublished, res["a2"].Status)
	require.Equal(t, StatusPublished, res["b"].Status, "%v", res["b"].Err)
	require.NotEqual(t, -1, r.indexOf("version b"))

	var updates []map[string]string
	raw := envValue(t, r.envs["version b"], "MONOREL_UPDATED_PROVIDERS")
	require.NoError(t, json.Unmarshal([]byte(raw), &updates))
	require.Len(t, updates, 1, "failed provider must be filtered out")
	assert.Equal(t, "a2", updates[0]["package"])
}

func TestRunScriptEnv(t *testing.T) {
	p := mkPlan(false, nil, nil, "a")
	r := &fakeRunner{}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 1, 1).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["a"].Status)

	env := r.envs["build a"]
	assert.Equal(t, "a", envValue(t, env, "MONOREL_PACKAGE"))
	assert.Equal(t, "libs", envValue(t, env, "MONOREL_SPACE"))
	assert.Equal(t, "1.0.0", envValue(t, env, "MONOREL_OLD_VERSION"))
	assert.Equal(t, "1.0.1", envValue(t, env, "MONOREL_NEW_VERSION"))
	assert.Equal(t, "patch", envValue(t, env, "MONOREL_BUMP"))
	assert.Equal(t, "a@1.0.1", envValue(t, env, "MONOREL_TAG"))
	assert.Equal(t, "build", envValue(t, env, "MONOREL_STAGE"))
	assert.Equal(t, "publish", envValue(t, r.envs["publish a"], "MONOREL_STAGE"))
}

func TestRunWithoutScripts(t *testing.T) {
	// No scripts at all: the release must still run every task, tag and
	// record changelogs — it just executes nothing.
	p := mkPlan(true, nil, map[string][]string{"b": {"a"}}, "a", "b")
	space := p.Releases["a"].Pkg.Space
	space.BuildScript, space.PublishScript, space.VersionScript = "", "", ""
	r := &fakeRunner{}
	tg := &fakeTagger{}
	cl := &fakeChangelog{}
	res := newExecutor(r, tg, cl, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status)
	require.Equal(t, StatusPublished, res["b"].Status)
	assert.Empty(t, r.events, "no shell commands may run")
	assert.ElementsMatch(t, []string{"a@1.0.1", "b@1.0.1"}, tg.tags)
	assert.ElementsMatch(t, []string{"a", "b"}, cl.entries)
}

func TestRunRevertOnFail(t *testing.T) {
	for _, stage := range []string{"version", "build", "publish"} {
		t.Run(stage, func(t *testing.T) {
			p := mkPlan(true, nil, map[string][]string{"b": {"a"}}, "a", "b")
			space := p.Releases["a"].Pkg.Space
			space.RevertOnFail = true
			space.VersionScript = "version"
			// Fail package "a" for build/publish stages; "b" for version
			// (only consumers run the version stage).
			failPkg := "a"
			if stage == "version" {
				failPkg = "b"
			}
			rv := &fakeReverter{}
			r := &fakeRunner{fail: map[string]bool{stage + " " + failPkg: true}}
			ex := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4)
			ex.Reverter = rv
			res := ex.Run(context.Background(), p)

			require.Equal(t, StatusFailed, res[failPkg].Status)
			assert.Contains(t, rv.dirs, failPkg, "failed package folder must be reverted")
		})
	}
}

func TestRunRevertDisabledByDefault(t *testing.T) {
	p := mkPlan(false, nil, nil, "a") // RevertOnFail stays false
	rv := &fakeReverter{}
	ex := newExecutor(&fakeRunner{fail: map[string]bool{"build a": true}}, &fakeTagger{}, &fakeChangelog{}, 1, 1)
	ex.Reverter = rv
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Empty(t, rv.dirs, "no revert without revertOnFail")
}

func TestRunRevertOnRecorderFailure(t *testing.T) {
	p := mkPlan(false, nil, nil, "a")
	p.Releases["a"].Pkg.Space.RevertOnFail = true
	rv := &fakeReverter{}
	ex := newExecutor(&fakeRunner{}, &fakeTagger{}, &fakeChangelog{fail: true}, 1, 1)
	ex.Reverter = rv
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Contains(t, rv.dirs, "a", "recorder failure is a publish-stage failure and must revert")
}

func TestRunRevertErrorKeepsFailedStatus(t *testing.T) {
	p := mkPlan(false, nil, nil, "a")
	p.Releases["a"].Pkg.Space.RevertOnFail = true
	rv := &fakeReverter{err: errors.New("revert boom")}
	ex := newExecutor(&fakeRunner{fail: map[string]bool{"build a": true}}, &fakeTagger{}, &fakeChangelog{}, 1, 1)
	ex.Reverter = rv
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.ErrorContains(t, res["a"].Err, "boom", "original script error is preserved")
	assert.Len(t, rv.dirs, 1, "revert was attempted")
}

func TestRunNoRevertOnPlainSkip(t *testing.T) {
	// b is skipped before anything ran in its folder: nothing to revert.
	p := mkPlan(true, nil, map[string][]string{"b": {"a"}}, "a", "b")
	p.Releases["a"].Pkg.Space.RevertOnFail = true
	rv := &fakeReverter{}
	ex := newExecutor(&fakeRunner{fail: map[string]bool{"build a": true}}, &fakeTagger{}, &fakeChangelog{}, 4, 4)
	ex.Reverter = rv
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusSkipped, res["b"].Status)
	assert.Equal(t, []string{"a"}, rv.dirs, "only the failed package reverts, not the untouched skipped one")
}

func TestRunRecorderFailureFailsPackage(t *testing.T) {
	p := mkPlan(false, nil, nil, "a")
	tg := &fakeTagger{}
	res := newExecutor(&fakeRunner{}, tg, &fakeChangelog{fail: true}, 1, 1).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.ErrorContains(t, res["a"].Err, "recorder boom")
	assert.Empty(t, tg.tags, "a failed recorder must prevent tagging")
}

func TestRunMultipleRecorders(t *testing.T) {
	p := mkPlan(false, nil, nil, "a")
	first, second := &fakeChangelog{}, &fakeChangelog{}
	ex := newExecutor(&fakeRunner{}, &fakeTagger{}, first, 1, 1)
	ex.Recorders = append(ex.Recorders, second)
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status)
	assert.Equal(t, []string{"a"}, first.entries)
	assert.Equal(t, []string{"a"}, second.entries)
}

func TestRunNilTaggerDefersTagging(t *testing.T) {
	// A nil Tagger (release-commit mode) publishes without tagging.
	p := mkPlan(false, nil, nil, "a")
	cl := &fakeChangelog{}
	ex := &Executor{
		BuildConcurrency:   1,
		PublishConcurrency: 1,
		Runner:             &fakeRunner{},
		Tagger:             nil,
		Recorders:          []ReleaseRecorder{cl},
		Log:                zerolog.Nop(),
	}
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.Equal(t, []string{"a"}, cl.entries, "recorders still run")
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusPending: "pending", StatusPublished: "published",
		StatusFailed: "failed", StatusSkipped: "skipped",
	}
	for st, want := range cases {
		assert.Equal(t, want, st.String())
	}
}

func TestLineWriter(t *testing.T) {
	var got []string
	log := zerolog.New(writerFunc(func(p []byte) (int, error) {
		got = append(got, string(p))
		return len(p), nil
	}))
	w := newLineWriter(log, zerolog.InfoLevel)
	fmt.Fprint(w, "hello\nwor")
	fmt.Fprint(w, "ld\ntail")
	w.Flush()
	require.Len(t, got, 3, "hello, world and the flushed tail")
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
