package release

import (
	"context"
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

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
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

// countPrefix counts events whose command is `command`, whatever folder it ran
// in — a login's folder is the scheduling accident of whichever package's
// publish reached the gate first.
func (r *fakeRunner) countPrefix(command string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if strings.HasPrefix(e, command+" ") {
			n++
		}
	}
	return n
}

// firstPrefix returns the index of the first event running `command`, or -1.
func (r *fakeRunner) firstPrefix(command string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.events {
		if strings.HasPrefix(e, command+" ") {
			return i
		}
	}
	return -1
}

// envPrefix returns the environment of the first recorded run of `command`.
func (r *fakeRunner) envPrefix(t *testing.T, command string) []string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if strings.HasPrefix(e, command+" ") {
			return r.envs[e]
		}
	}
	t.Fatalf("no event ran command %q; events: %v", command, r.events)
	return nil
}

type fakeTagger struct {
	mu      sync.Mutex
	tags    []string
	targets map[string]string // tag name -> requested target ("" = HEAD)
}

func (f *fakeTagger) CreateTag(_ context.Context, name, _ string, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags = append(f.tags, name)
	if f.targets == nil {
		f.targets = map[string]string{}
	}
	f.targets[name] = target
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

// planSpec describes the plan mkPlan builds: Names are the changed packages,
// WaitPublish applies to the "libs" space every package lives in, OwnBump
// marks packages with own commits, and consumers listed in Deps get DueTo
// set, mirroring plan.Compute (every listed package is changed).
type planSpec struct {
	WaitPublish bool
	OwnBump     map[string]ccme.Bump
	Deps        map[string][]string
	Names       []string
}

// mkPlan builds a plan of changed packages from its spec.
func mkPlan(spec planSpec) *plan.Plan {
	waitPublish, ownBump, deps, names := spec.WaitPublish, spec.OwnBump, spec.Deps, spec.Names
	libs := &model.Space{Name: "libs", BuildWaitsPublish: waitPublish, BuildScript: []string{"build"}, PublishScript: []string{"publish"}}
	p := &plan.Plan{
		Releases:  map[string]*plan.Release{},
		Providers: map[string][]string{},
	}
	for _, n := range names {
		own := ownBump[n]
		bump := ccme.MaxBump(own, ccme.BumpPatch) // every listed package is changed
		rel := &plan.Release{
			Pkg:     &model.Package{Name: n, Dir: n, Space: libs},
			Current: ccme.Version{Major: 1},
			OwnBump: own,
			Bump:    bump,
			NewWork: true, // a bump only releases when carried by unpublished work
		}
		if own != ccme.BumpNone {
			rel.Units = []*ccme.Unit{{
				Header: ccme.Header{Type: "fix", Description: "own change"},
				Bump:   ccme.BumpPatch,
				Valid:  true,
			}}
		}
		rel.Next = rel.Current.Bumped(bump)
		p.Releases[n] = rel
		p.Order = append(p.Order, n)
	}
	for consumer, provs := range deps {
		p.Providers[consumer] = provs
		p.Releases[consumer].DueTo = provs
	}
	return p
}

// execSpec is what a test's Executor is assembled from: the fakes it records
// through and the two stage budgets.
type execSpec struct {
	Runner    *fakeRunner
	Tagger    *fakeTagger
	Changelog *fakeChangelog
	Build     int
	Publish   int
}

func newExecutor(spec execSpec) *Executor {
	e := &Executor{
		BuildConcurrency:   spec.Build,
		PublishConcurrency: spec.Publish,
		Runner:             spec.Runner,
		Log:                zerolog.Nop(),
	}
	if spec.Tagger != nil {
		e.Tagger = spec.Tagger
	}
	if spec.Changelog != nil {
		e.Recorders = []ReleaseRecorder{spec.Changelog}
	}
	return e
}

func TestRunSuccessOrder(t *testing.T) {
	// b consumes a; waitPublish=true gives the strictest ordering guarantees.
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	r := &fakeRunner{}
	tg := &fakeTagger{}
	cl := &fakeChangelog{}
	res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: cl, Build: 4, Publish: 4}).Run(context.Background(), p)

	for _, n := range []string{"a", "b"} {
		require.Equal(t, StatusPublished, res[n].Status, "%s: %v", n, res[n].Err)
	}
	assert.Less(t, r.indexOf("build a"), r.indexOf("publish a"), "a builds before publishing")
	assert.Less(t, r.indexOf("build a"), r.indexOf("build b"), "provider builds before consumer")
	assert.Less(t, r.indexOf("publish a"), r.indexOf("publish b"), "provider publishes before consumer")
	assert.ElementsMatch(t, []string{"a@1.0.1", "b@1.0.1"}, tg.tags)
	assert.ElementsMatch(t, []string{"a", "b"}, cl.entries)
	assert.Equal(t, ccme.Version{Major: 1, Patch: 1}, res["a"].To)
}

func TestRunTagTargetFromPackageCommitExport(t *testing.T) {
	// A PACKAGE_<KEY> output pins the package's tag to the exported commit;
	// packages without the export keep tagging HEAD (empty target).
	p := mkPlan(planSpec{Names: []string{"a", "b"}})
	p.Releases["a"].Outputs = []plan.Output{{
		Name: plan.PackageCommitExportPrefix + plan.EnvKey("a"), Value: "abc1234", Source: "a:build"}}
	r := &fakeRunner{}
	tg := &fakeTagger{}
	res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: &fakeChangelog{}, Build: 2, Publish: 2}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status)
	require.Equal(t, StatusPublished, res["b"].Status)
	assert.Equal(t, "abc1234", tg.targets["a@1.0.1"], "the exported commit becomes the tag target")
	assert.Equal(t, "", tg.targets["b@1.0.1"], "no export means tagging HEAD")
}

func TestRunBuildWaitsPublish(t *testing.T) {
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["b"].Status)
	assert.Less(t, r.indexOf("publish a"), r.indexOf("build b"),
		"with isBuildWaitingPublish, b may only build after a is published")
}

func TestRunNoWaitProviderPublishFailureSkipsConsumers(t *testing.T) {
	// isBuildWaitingPublish=false, chain a -> b -> c, none with own commits.
	// a's PUBLISH fails. b may already have built (that is the trade-off the
	// flag opts into), but its publish always waits for a's publish: seeing
	// the failure, b is skipped, and c cascades to skipped the same way.
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}, "c": {"b"}}, Names: []string{"a", "b", "c"}})
	r := &fakeRunner{fail: map[string]bool{"publish a": true}}
	tg := &fakeTagger{}
	cl := &fakeChangelog{}
	res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: cl, Build: 4, Publish: 4}).Run(context.Background(), p)

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
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}, "c": {"b"}}, Names: []string{"a", "b", "c"}})
	r := &fakeRunner{fail: map[string]bool{"build b": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

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
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}, "c": {"b"}}, Names: []string{"a", "b", "c"}})
	r := &fakeRunner{fail: map[string]bool{"publish b": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

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
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	p.Releases["b"].Pkg.Space.VersionScript = []string{"version"}
	r := &fakeRunner{fail: map[string]bool{"publish a": true}}
	tg := &fakeTagger{}
	res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Error(t, res["a"].Err)
	require.Equal(t, StatusSkipped, res["b"].Status, "b has no changes of its own")
	assert.Equal(t, -1, r.indexOf("version b"), "skipped consumer must not run its version script")
	assert.Equal(t, -1, r.indexOf("build b"), "skipped consumer must not run any script")
	assert.Empty(t, tg.tags)

	// W194: planned, not attempted. A package that was in the plan and
	// produced nothing has to be accounted for — that is why the code is
	// non-suppressible (§16).
	assert.True(t, res["b"].Blocked, "the skip must be reported as a blocked release")
	assert.Equal(t, "a", res["b"].BlockedBy)
}

func TestRunChannelChangeIsItsOwnReason(t *testing.T) {
	// A package moving between channels is being released for something a
	// failed provider cannot invalidate, so it proceeds rather than skipping.
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	b := p.Releases["b"]
	b.OwnBump, b.Bump = ccme.BumpNone, ccme.BumpNone
	b.BaselineChannel, b.Channel = "stable", "beta"

	r := &fakeRunner{fail: map[string]bool{"publish a": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, StatusPublished, res["b"].Status,
		"a channel change is a release reason of its own: %v", res["b"].Err)
	assert.False(t, res["b"].Blocked)
}

func TestRunFailureConsumerWithOwnChangesProceeds(t *testing.T) {
	p := mkPlan(planSpec{WaitPublish: true, OwnBump: map[string]ccme.Bump{"b": ccme.BumpPatch}, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	r := &fakeRunner{fail: map[string]bool{"build a": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	require.Equal(t, StatusPublished, res["b"].Status,
		"b must proceed despite provider failure: %v", res["b"].Err)
}

func TestRunSkipCascades(t *testing.T) {
	// chain a -> b -> c: a fails, b skipped, c skipped too; no version
	// script may run anywhere down the chain.
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}, "c": {"b"}}, Names: []string{"a", "b", "c"}})
	p.Releases["a"].Pkg.Space.VersionScript = []string{"version"}
	r := &fakeRunner{fail: map[string]bool{"build a": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	assert.Equal(t, StatusSkipped, res["b"].Status)
	assert.Equal(t, StatusSkipped, res["c"].Status)
	assert.Equal(t, -1, r.indexOf("version b"))
	assert.Equal(t, -1, r.indexOf("version c"))
}

func TestRunConcurrencyLimits(t *testing.T) {
	names := []string{"p1", "p2", "p3", "p4", "p5", "p6"}
	p := mkPlan(planSpec{Names: names})
	r := &fakeRunner{delay: 10 * time.Millisecond}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 2, Publish: 2}).Run(context.Background(), p)

	for _, n := range names {
		require.Equal(t, StatusPublished, res[n].Status, n)
	}
	assert.LessOrEqual(t, r.maxCur["build"], 2, "build budget")
	assert.LessOrEqual(t, r.maxCur["publish"], 2, "publish budget")
}

func TestRunSeparateStageBudgets(t *testing.T) {
	names := []string{"p1", "p2", "p3", "p4"}
	p := mkPlan(planSpec{Names: names})
	r := &fakeRunner{delay: 10 * time.Millisecond}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 3, Publish: 1}).Run(context.Background(), p)

	for _, n := range names {
		require.Equal(t, StatusPublished, res[n].Status, n)
	}
	assert.LessOrEqual(t, r.maxCur["build"], 3, "build budget")
	assert.LessOrEqual(t, r.maxCur["publish"], 1, "publishes must be serialized")
	// With independent budgets, builds are allowed to overlap while a
	// publish is running, so builds should actually reach parallelism > 1.
	assert.Greater(t, r.maxCur["build"], 1, "builds should overlap")
}

func TestRunHeldPackageIsNotExecuted(t *testing.T) {
	// A package held by `Release-As: none` has a bump and a computed version —
	// both are reported (W154) — but §13.6a excludes it from the plan, so it
	// must not be built, published or tagged. Gating on Changed() instead of
	// Releasing() releases it anyway, which is the whole point of the hold.
	p := mkPlan(planSpec{Names: []string{"a", "held"}})
	p.Releases["held"].Held = true

	r, tg := &fakeRunner{}, &fakeTagger{}
	res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.NotContains(t, res, "held", "a held package produces no result")
	assert.Equal(t, -1, r.indexOf("build held"), "and runs no script")
	assert.Equal(t, []string{"a@1.0.1"}, tg.tags, "and is not tagged")
}

func TestRunChannelOnlyReleaseIsExecuted(t *testing.T) {
	// The mirror image: no bump at all, but a channel change is a reason to
	// release like any other (§13.9), so the pipeline must run.
	p := mkPlan(planSpec{Names: []string{"a"}})
	rel := p.Releases["a"]
	rel.OwnBump, rel.Bump = ccme.BumpNone, ccme.BumpNone
	rel.BaselineChannel, rel.Channel = "stable", "beta"
	rel.HasBaseline, rel.Baseline = true, ccme.Version{Major: 1}
	rel.Next = ccme.Version{Major: 1, Patch: 1, Prerelease: []string{"beta", "0"}}

	r, tg := &fakeRunner{}, &fakeTagger{}
	res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Contains(t, res, "a")
	assert.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.Equal(t, "beta", res["a"].Channel)
	assert.Equal(t, []string{"a@1.0.1-beta.0"}, tg.tags)
}

func TestRunTagsUseTheSpaceFormat(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.TagFormat = "services/{name}@v{version}"

	r, tg := &fakeRunner{}, &fakeTagger{}
	newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	assert.Equal(t, []string{"services/a@v1.0.1"}, tg.tags)
	assert.Equal(t, "services/a@v1.0.1", envValue(t, r.envs["publish a"], "DISPAT_TAG"))
}

func TestRunChannelEnvironment(t *testing.T) {
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})

	// a graduates off a beta line; b rides along on stable.
	a := p.Releases["a"]
	a.HasBaseline, a.Baseline = true, ccme.Version{Minor: 9, Prerelease: []string{"beta", "3"}}
	a.BaselineChannel, a.Channel = "beta", "stable"
	a.Next = ccme.Version{Major: 1}

	r := &fakeRunner{}
	newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	env := r.envs["build a"]
	assert.Equal(t, "stable", envValue(t, env, "DISPAT_CHANNEL"))
	assert.Equal(t, "beta", envValue(t, env, "DISPAT_OLD_CHANNEL"),
		"a graduation must be distinguishable from an ordinary release")
	assert.Equal(t, "false", envValue(t, env, "DISPAT_IS_PRERELEASE"))
	assert.Equal(t, "0.9.0-beta.3", envValue(t, env, "DISPAT_OLD_VERSION"),
		"the version last published, not the stable baseline")
	assert.Equal(t, "0.9.0-beta.3", envValue(t, env, "DISPAT_BASELINE"),
		"the newest tag of any kind, prereleases included")
	assert.Equal(t, "1.0.0", envValue(t, env, "DISPAT_STABLE_BASELINE"),
		"the baseline versions are computed from, which differs on a train")
	assertEnvUnset(t, env, "DISPAT_COUNTER",
		"a stable version has no counter, and no counter means no variable")
	assert.Equal(t, "3", envValue(t, env, "DISPAT_OLD_COUNTER"),
		"the previous release was 0.9.0-beta.3")
}

func TestRunPrereleaseEnvironmentUnderCustomFormat(t *testing.T) {
	// A space spelling the prerelease into its tags gets both spellings in
	// the environment: DISPAT_TAG the local convention, DISPAT_SEMVER_TAG the
	// normative one, and the channel and counter broken out so a script never
	// re-parses either.
	p := mkPlan(planSpec{Names: []string{"a"}})
	rel := p.Releases["a"]
	rel.Pkg.Space.TagFormat = "{name}@v{version}-{channel}{counter}"
	rel.BaselineChannel, rel.Channel = "stable", "beta"
	rel.Next = ccme.Version{Major: 1, Patch: 1, Prerelease: []string{"beta", "4"}}

	r, tg := &fakeRunner{}, &fakeTagger{}
	newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	env := r.envs["publish a"]
	assert.Equal(t, "a@v1.0.1-beta4", envValue(t, env, "DISPAT_TAG"),
		"name + version + channel + counter, spelled by the space's tagFormat")
	assert.Equal(t, "a@1.0.1-beta.4", envValue(t, env, "DISPAT_SEMVER_TAG"),
		"the SemVer spelling stays stable whatever tagFormat encodes")
	assert.Equal(t, "1.0.1-beta.4", envValue(t, env, "DISPAT_NEW_VERSION"),
		"the version itself is SemVer throughout")
	assert.Equal(t, "1.0.1-beta4", envValue(t, env, "DISPAT_TAG_VERSION"),
		"version + channel + counter as the tagFormat spells them, no name, no 'v'")
	assert.Equal(t, "1.0.1", envValue(t, env, "DISPAT_VERSION"),
		"the core version alone, channel and counter stripped")
	assert.Equal(t, "beta", envValue(t, env, "DISPAT_CHANNEL"))
	assert.Equal(t, "4", envValue(t, env, "DISPAT_COUNTER"))
	assertEnvUnset(t, env, "DISPAT_OLD_COUNTER",
		"the previous release was stable, so there is no old counter")
	assert.Equal(t, []string{"a@v1.0.1-beta4"}, tg.tags,
		"the created tag follows the space's format")
}

func assertEnvUnset(t *testing.T, env []string, key, why string) {
	t.Helper()
	for _, kv := range env {
		assert.False(t, strings.HasPrefix(kv, key+"="), "%s must be unset: %s", key, why)
	}
}

func TestRunVersionStageWorkspaceVersions(t *testing.T) {
	// §9.4 requires reconciling against *every* workspace dependency, not only
	// those released in this run — a dependency may have been published by an
	// earlier run whose dependent leg failed. dispat has no manifest model, so
	// it exports the data the version script needs to do that itself.
	libs := &model.Space{Name: "libs", BuildScript: []string{"build"}, PublishScript: []string{"publish"}, VersionScript: []string{"version"}}
	p := &plan.Plan{
		Order:     []string{"a", "quiet", "b"},
		Releases:  map[string]*plan.Release{},
		Providers: map[string][]string{"b": {"a"}},
	}
	stable := func(r *plan.Release) *plan.Release {
		// Both must be set, or ChannelChanged() reads "" != "stable" and the
		// package counts as a channel-only release.
		r.Channel, r.BaselineChannel = "stable", "stable"
		return r
	}
	p.Releases["a"] = stable(&plan.Release{
		Pkg:  &model.Package{Name: "a", Dir: "a", Space: libs},
		Bump: ccme.BumpMinor, NewWork: true, Current: ccme.Version{Major: 1},
		Next: ccme.Version{Major: 1, Minor: 1},
	})
	p.Releases["quiet"] = stable(&plan.Release{ // not releasing: reports its baseline
		Pkg:         &model.Package{Name: "quiet", Dir: "quiet", Space: libs},
		HasBaseline: true, Baseline: ccme.Version{Major: 3, Minor: 2},
		Current: ccme.Version{Major: 3, Minor: 2}, Next: ccme.Version{Major: 3, Minor: 2},
	})
	p.Releases["b"] = stable(&plan.Release{
		Pkg:  &model.Package{Name: "b", Dir: "b", Space: libs},
		Bump: ccme.BumpPatch, NewWork: true, Current: ccme.Version{Major: 2},
		Next: ccme.Version{Major: 2, Patch: 1}, DueTo: []string{"a"},
	})

	r := &fakeRunner{}
	newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	env := r.envs["version b"]
	assert.Equal(t, "A QUIET B", envValue(t, env, "DISPAT_WORKSPACE_PACKAGES"),
		"every workspace package in plan order — not only this run's releases — ready for a shell for-loop")
	assert.Equal(t, "quiet", envValue(t, env, "DISPAT_WORKSPACE_QUIET_NAME"))
	assert.Equal(t, "3.2.0", envValue(t, env, "DISPAT_WORKSPACE_QUIET_VERSION"))
	assert.Equal(t, "false", envValue(t, env, "DISPAT_WORKSPACE_QUIET_RELEASING"))
	assert.Equal(t, "1.1.0", envValue(t, env, "DISPAT_WORKSPACE_A_VERSION"))
	assert.Equal(t, "true", envValue(t, env, "DISPAT_WORKSPACE_A_RELEASING"))

	assert.Equal(t, "A", envValue(t, env, "DISPAT_UPDATED_PACKAGES"))
	assert.Equal(t, "a", envValue(t, env, "DISPAT_UPDATED_A_NAME"))
	assert.Equal(t, "libs", envValue(t, env, "DISPAT_UPDATED_A_SPACE"))
	assert.Equal(t, "1.0.0", envValue(t, env, "DISPAT_UPDATED_A_OLD_VERSION"))
	assert.Equal(t, "1.1.0", envValue(t, env, "DISPAT_UPDATED_A_NEW_VERSION"))
	assert.Equal(t, "stable", envValue(t, env, "DISPAT_UPDATED_A_CHANNEL"))

	assert.Equal(t, "", envValue(t, r.envs["build a"], "DISPAT_UPDATED_PACKAGES"),
		"empty, not unset: a for-loop over it must iterate zero times")

	// Both listings reach every stage, identically: a build baking versions
	// into artefacts and a publish choosing dist-tags read the same state as
	// the version script, and a script stays movable between stages.
	for _, key := range []string{"build a", "publish a", "build b", "publish b"} {
		assert.Equal(t, "A QUIET B", envValue(t, r.envs[key], "DISPAT_WORKSPACE_PACKAGES"),
			"%s must see the same workspace listing", key)
		assert.Equal(t, "3.2.0", envValue(t, r.envs[key], "DISPAT_WORKSPACE_QUIET_VERSION"))
	}
	for _, key := range []string{"build b", "publish b"} {
		assert.Equal(t, "A", envValue(t, r.envs[key], "DISPAT_UPDATED_PACKAGES"),
			"%s must see the live provider updates too", key)
		assert.Equal(t, "1.1.0", envValue(t, r.envs[key], "DISPAT_UPDATED_A_NEW_VERSION"))
	}
}

func TestWorkspaceEnvKeySanitisation(t *testing.T) {
	// "@acme/ui" is a fine package name and an impossible variable name, so
	// the key is sanitised and the raw name travels in the _NAME field.
	libs := &model.Space{Name: "libs", BuildScript: []string{"build"}, PublishScript: []string{"publish"}}
	mk := func(name string) *plan.Release {
		return &plan.Release{
			Pkg:     &model.Package{Name: name, Dir: name, Space: libs},
			Channel: "stable", BaselineChannel: "stable",
			Bump: ccme.BumpPatch, NewWork: true, Current: ccme.Version{Major: 1},
			Next: ccme.Version{Major: 1, Patch: 1},
		}
	}
	p := &plan.Plan{
		Order:     []string{"@acme/ui", "core-utils", "core.utils"},
		Releases:  map[string]*plan.Release{"@acme/ui": mk("@acme/ui"), "core-utils": mk("core-utils"), "core.utils": mk("core.utils")},
		Providers: map[string][]string{},
	}

	r := &fakeRunner{}
	newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	env := r.envs["build @acme/ui"]
	assert.Equal(t, "_ACME_UI CORE_UTILS", envValue(t, env, "DISPAT_WORKSPACE_PACKAGES"),
		"colliding core.utils is omitted, first in plan order wins")
	assert.Equal(t, "@acme/ui", envValue(t, env, "DISPAT_WORKSPACE__ACME_UI_NAME"))
	assert.Equal(t, "core-utils", envValue(t, env, "DISPAT_WORKSPACE_CORE_UTILS_NAME"))
}

func TestRunUnchangedExcluded(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	// add an unchanged package to the plan
	p.Releases["quiet"] = &plan.Release{
		Pkg:     &model.Package{Name: "quiet", Dir: "quiet", Space: p.Releases["a"].Pkg.Space},
		Current: ccme.Version{Major: 3},
		Next:    ccme.Version{Major: 3},
	}
	p.Order = append(p.Order, "quiet")
	res := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

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
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	p.Releases["a"].Pkg.Space.VersionScript = []string{"version"}
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["b"].Status, "%v", res["b"].Err)
	// Only the provider-bumped package runs the version stage.
	assert.Equal(t, -1, r.indexOf("version a"), "a has no provider updates")
	require.NotEqual(t, -1, r.indexOf("version b"), "b must run the version stage")
	assert.Less(t, r.indexOf("publish a"), r.indexOf("version b"),
		"version runs after provider tasks (waitPublish)")
	assert.Less(t, r.indexOf("version b"), r.indexOf("build b"),
		"version runs exactly before the consumer's build")

	env := r.envs["version b"]
	assert.Equal(t, "b", envValue(t, env, "DISPAT_PACKAGE"))
	assert.Equal(t, "version", envValue(t, env, "DISPAT_STAGE"))

	assert.Equal(t, "A", envValue(t, env, "DISPAT_UPDATED_PACKAGES"))
	assert.Equal(t, "a", envValue(t, env, "DISPAT_UPDATED_A_NAME"))
	assert.Equal(t, "libs", envValue(t, env, "DISPAT_UPDATED_A_SPACE"))
	assert.Equal(t, "1.0.0", envValue(t, env, "DISPAT_UPDATED_A_OLD_VERSION"))
	assert.Equal(t, "1.0.1", envValue(t, env, "DISPAT_UPDATED_A_NEW_VERSION"))
	assert.Equal(t, "a: 1.0.0 -> 1.0.1", envValue(t, env, "DISPAT_DEPENDENCIES"),
		"the changelog's dependencies lines, version movement included")
	assert.Equal(t, "", envValue(t, r.envs["build a"], "DISPAT_DEPENDENCIES"),
		"empty (not unset) when nothing was updated")
}

func TestRunVersionOrderingNoWaitPublish(t *testing.T) {
	// isBuildWaitingPublish=false: the version stage only waits for provider
	// builds, not their publishes.
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	p.Releases["b"].Pkg.Space.VersionScript = []string{"version"}
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

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
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	p.Releases["b"].Pkg.Space.VersionScript = []string{"version"}
	r := &fakeRunner{fail: map[string]bool{"build a": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	require.Equal(t, StatusSkipped, res["b"].Status)
	assert.Equal(t, -1, r.indexOf("version b"))
	assert.Equal(t, -1, r.indexOf("build b"))
}

func TestRunVersionScriptFailureFailsPackage(t *testing.T) {
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	p.Releases["a"].Pkg.Space.VersionScript = []string{"version"}
	r := &fakeRunner{fail: map[string]bool{"version b": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status)
	require.Equal(t, StatusFailed, res["b"].Status)
	assert.Equal(t, -1, r.indexOf("build b"), "build must not run after a failed version stage")
}

func TestRunVersionSkippedWhenAllProvidersFailed(t *testing.T) {
	// b has its own bump AND a provider bump; the provider fails. b still
	// releases (own changes), but the version script must NOT run: there is
	// no successfully updated provider to sync manifests to.
	p := mkPlan(planSpec{WaitPublish: true, OwnBump: map[string]ccme.Bump{"b": ccme.BumpPatch}, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	p.Releases["a"].Pkg.Space.VersionScript = []string{"version"}
	r := &fakeRunner{fail: map[string]bool{"publish a": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	require.Equal(t, StatusPublished, res["b"].Status, "%v", res["b"].Err)
	assert.Equal(t, -1, r.indexOf("version b"), "version script must not run when every provider failed")
	assert.NotEqual(t, -1, r.indexOf("build b"), "build still runs for the own-bump release")
}

func TestRunVersionFiltersFailedProviders(t *testing.T) {
	// b consumes a1 (fails) and a2 (succeeds) and has its own bump: the
	// version script runs, but only a2 appears in DISPAT_UPDATED_*.
	p := mkPlan(planSpec{WaitPublish: true, OwnBump: map[string]ccme.Bump{"b": ccme.BumpPatch}, Deps: map[string][]string{"b": {"a1", "a2"}}, Names: []string{"a1", "a2", "b"}})
	p.Releases["b"].Pkg.Space.VersionScript = []string{"version"}
	r := &fakeRunner{fail: map[string]bool{"publish a1": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a1"].Status)
	require.Equal(t, StatusPublished, res["a2"].Status)
	require.Equal(t, StatusPublished, res["b"].Status, "%v", res["b"].Err)
	require.NotEqual(t, -1, r.indexOf("version b"))

	env := r.envs["version b"]
	assert.Equal(t, "A2", envValue(t, env, "DISPAT_UPDATED_PACKAGES"),
		"failed provider must be filtered out")
	assert.Equal(t, "a2", envValue(t, env, "DISPAT_UPDATED_A2_NAME"))
	assertEnvUnset(t, env, "DISPAT_UPDATED_A1_NAME", "a1 failed, its version was never released")
}

func TestRunScriptEnv(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["a"].Status)

	env := r.envs["build a"]
	assert.Equal(t, "a", envValue(t, env, "DISPAT_PACKAGE"))
	assert.Equal(t, "libs", envValue(t, env, "DISPAT_SPACE"))
	assert.Equal(t, "1.0.0", envValue(t, env, "DISPAT_OLD_VERSION"))
	assert.Equal(t, "1.0.1", envValue(t, env, "DISPAT_NEW_VERSION"))
	assert.Equal(t, "1.0.1", envValue(t, env, "DISPAT_VERSION"),
		"stable release: the core is the whole version")
	assert.Equal(t, "1.0.1", envValue(t, env, "DISPAT_TAG_VERSION"),
		"the default format spells the version as SemVer")
	assert.Equal(t, "patch", envValue(t, env, "DISPAT_BUMP"))
	assert.Equal(t, "a@1.0.1", envValue(t, env, "DISPAT_TAG"))
	assert.Equal(t, "build", envValue(t, env, "DISPAT_STAGE"))
	assertEnvUnset(t, env, "DISPAT_BASELINE",
		"a package that has never released has no baseline tag, and no variable")
	assert.Equal(t, "publish", envValue(t, r.envs["publish a"], "DISPAT_STAGE"))
}

func TestRunWithoutScripts(t *testing.T) {
	// No scripts at all: the release must still run every task, tag and
	// record changelogs — it just executes nothing.
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	space := p.Releases["a"].Pkg.Space
	space.BuildScript, space.PublishScript, space.VersionScript = nil, nil, nil
	r := &fakeRunner{}
	tg := &fakeTagger{}
	cl := &fakeChangelog{}
	res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: cl, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status)
	require.Equal(t, StatusPublished, res["b"].Status)
	assert.Empty(t, r.events, "no shell commands may run")
	assert.ElementsMatch(t, []string{"a@1.0.1", "b@1.0.1"}, tg.tags)
	assert.ElementsMatch(t, []string{"a", "b"}, cl.entries)
}

func TestRunRevertOnFail(t *testing.T) {
	for _, stage := range []string{"version", "build", "publish"} {
		t.Run(stage, func(t *testing.T) {
			p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
			space := p.Releases["a"].Pkg.Space
			space.RevertOnFail = true
			space.VersionScript = []string{"version"}
			// Fail package "a" for build/publish stages; "b" for version
			// (only consumers run the version stage).
			failPkg := "a"
			if stage == "version" {
				failPkg = "b"
			}
			rv := &fakeReverter{}
			r := &fakeRunner{fail: map[string]bool{stage + " " + failPkg: true}}
			ex := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4})
			ex.Reverter = rv
			res := ex.Run(context.Background(), p)

			require.Equal(t, StatusFailed, res[failPkg].Status)
			assert.Contains(t, rv.dirs, failPkg, "failed package folder must be reverted")
		})
	}
}

func TestRunRevertDisabledByDefault(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}}) // RevertOnFail stays false
	rv := &fakeReverter{}
	ex := newExecutor(execSpec{Runner: &fakeRunner{fail: map[string]bool{"build a": true}}, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1})
	ex.Reverter = rv
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Empty(t, rv.dirs, "no revert without revertOnFail")
}

func TestRunRevertOnRecorderFailure(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.RevertOnFail = true
	rv := &fakeReverter{}
	ex := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{fail: true}, Build: 1, Publish: 1})
	ex.Reverter = rv
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Contains(t, rv.dirs, "a", "recorder failure is a publish-stage failure and must revert")
}

func TestRunRevertErrorKeepsFailedStatus(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.RevertOnFail = true
	rv := &fakeReverter{err: errors.New("revert boom")}
	ex := newExecutor(execSpec{Runner: &fakeRunner{fail: map[string]bool{"build a": true}}, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1})
	ex.Reverter = rv
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.ErrorContains(t, res["a"].Err, "boom", "original script error is preserved")
	assert.Len(t, rv.dirs, 1, "revert was attempted")
}

func TestRunNoRevertOnPlainSkip(t *testing.T) {
	// b is skipped before anything ran in its folder: nothing to revert.
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	p.Releases["a"].Pkg.Space.RevertOnFail = true
	rv := &fakeReverter{}
	ex := newExecutor(execSpec{Runner: &fakeRunner{fail: map[string]bool{"build a": true}}, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4})
	ex.Reverter = rv
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusSkipped, res["b"].Status)
	assert.Equal(t, []string{"a"}, rv.dirs, "only the failed package reverts, not the untouched skipped one")
}

func TestRunRecorderFailureFailsPackage(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	tg := &fakeTagger{}
	res := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: tg, Changelog: &fakeChangelog{fail: true}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.ErrorContains(t, res["a"].Err, "recorder boom")
	assert.Empty(t, tg.tags, "a failed recorder must prevent tagging")
}

func TestRunMultipleRecorders(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	first, second := &fakeChangelog{}, &fakeChangelog{}
	ex := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Changelog: first, Build: 1, Publish: 1})
	ex.Recorders = append(ex.Recorders, second)
	res := ex.Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status)
	assert.Equal(t, []string{"a"}, first.entries)
	assert.Equal(t, []string{"a"}, second.entries)
}

func TestRunCatchUpConsumerWithUnchangedProvider(t *testing.T) {
	// Catch-up release: b missed a's already-published update in a previous
	// run. a is unchanged now (no tasks), b runs its full pipeline; the
	// version stage receives a's released version (old == new).
	libs := &model.Space{Name: "libs", BuildScript: []string{"build"}, PublishScript: []string{"publish"}, VersionScript: []string{"version"}}
	p := &plan.Plan{
		Order: []string{"a", "b"},
		Releases: map[string]*plan.Release{
			"a": { // unchanged: already released at 2.0.0
				Pkg:     &model.Package{Name: "a", Dir: "a", Space: libs},
				Current: ccme.Version{Major: 2},
				Next:    ccme.Version{Major: 2},
				Tagged:  true,
			},
			"b": { // catch-up patch due to a
				Pkg:     &model.Package{Name: "b", Dir: "b", Space: libs},
				Current: ccme.Version{Major: 1},
				Bump:    ccme.BumpPatch,
				NewWork: true,
				Next:    ccme.Version{Major: 1, Patch: 1},
				Tagged:  true,
				DueTo:   []string{"a"},
			},
		},
		Providers: map[string][]string{"b": {"a"}},
	}
	r := &fakeRunner{}
	tg := &fakeTagger{}
	res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Len(t, res, 1, "only b is processed")
	require.Equal(t, StatusPublished, res["b"].Status, "%v", res["b"].Err)
	assert.Equal(t, []string{"b@1.0.1"}, tg.tags)
	assert.Equal(t, -1, r.indexOf("build a"), "unchanged provider runs nothing")
	require.NotEqual(t, -1, r.indexOf("version b"), "catch-up still syncs manifests")

	env := r.envs["version b"]
	assert.Equal(t, "A", envValue(t, env, "DISPAT_UPDATED_PACKAGES"))
	assert.Equal(t, "2.0.0", envValue(t, env, "DISPAT_UPDATED_A_OLD_VERSION"),
		"already-released provider: old == new")
	assert.Equal(t, "2.0.0", envValue(t, env, "DISPAT_UPDATED_A_NEW_VERSION"))
}

func TestRunNilTaggerDefersTagging(t *testing.T) {
	// A nil Tagger (release-commit mode) publishes without tagging.
	p := mkPlan(planSpec{Names: []string{"a"}})
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

func TestRunLoginOncePerSpace(t *testing.T) {
	// Three packages of one space race to publish; the login runs exactly once
	// and every publish waits for it.
	p := mkPlan(planSpec{Names: []string{"a", "b", "c"}})
	p.Releases["a"].Pkg.Space.LoginScript = []string{"login"} // space shared by all three
	r := &fakeRunner{delay: 5 * time.Millisecond}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	for _, n := range []string{"a", "b", "c"} {
		require.Equal(t, StatusPublished, res[n].Status, "%s: %v", n, res[n].Err)
	}
	assert.Equal(t, 1, r.countPrefix("login"), "one login per space, however many publishes race to it")
	for _, n := range []string{"a", "b", "c"} {
		assert.Less(t, r.firstPrefix("login"), r.indexOf("publish "+n),
			"every publish of the space waits for the login")
	}
}

func TestRunLoginIsPerSpaceNotPerScript(t *testing.T) {
	// Two spaces sharing one login command still log in once each: credentials
	// and registries are a property of the space, not of the script text.
	mkSpace := func(name string) *model.Space {
		return &model.Space{Name: name, BuildScript: []string{"build"},
			PublishScript: []string{"publish"}, LoginScript: []string{"login"}}
	}
	p := &plan.Plan{
		Releases:  map[string]*plan.Release{},
		Providers: map[string][]string{},
	}
	for name, sp := range map[string]*model.Space{"a": mkSpace("s1"), "b": mkSpace("s2")} {
		p.Releases[name] = &plan.Release{
			Pkg:     &model.Package{Name: name, Dir: name, Space: sp},
			Current: ccme.Version{Major: 1},
			Bump:    ccme.BumpPatch,
			NewWork: true,
			Next:    ccme.Version{Major: 1, Patch: 1},
		}
		p.Order = append(p.Order, name)
	}
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 2, Publish: 2}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	require.Equal(t, StatusPublished, res["b"].Status, "%v", res["b"].Err)
	assert.Equal(t, 2, r.countPrefix("login"), "n spaces with the same login script log in n times")
}

func TestRunLoginEnvIsSpaceScoped(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.LoginScript = []string{"login"}
	r := &fakeRunner{}
	newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	env := r.envPrefix(t, "login")
	assert.Equal(t, "libs", envValue(t, env, "DISPAT_SPACE"))
	assert.Equal(t, "login", envValue(t, env, "DISPAT_STAGE"))
	assert.Equal(t, "A", envValue(t, env, "DISPAT_WORKSPACE_PACKAGES"),
		"the workspace listing reaches the login like every other script")
	assertEnvUnset(t, env, "DISPAT_PACKAGE",
		"login is a space affair; which package triggered it is a scheduling accident")
}

func TestRunLoginFailureFailsEverySpacePublish(t *testing.T) {
	// libs' login fails: both its packages fail at the publish stage (their
	// builds already ran). The other space is unaffected.
	libs := &model.Space{Name: "libs", BuildScript: []string{"build"},
		PublishScript: []string{"publish"}, LoginScript: []string{"login"}}
	other := &model.Space{Name: "other", BuildScript: []string{"build"},
		PublishScript: []string{"publish"}}
	p := &plan.Plan{
		Releases:  map[string]*plan.Release{},
		Providers: map[string][]string{},
	}
	for name, sp := range map[string]*model.Space{"a": libs, "b": libs, "c": other} {
		p.Releases[name] = &plan.Release{
			Pkg:     &model.Package{Name: name, Dir: name, Space: sp},
			Current: ccme.Version{Major: 1},
			Bump:    ccme.BumpPatch,
			NewWork: true,
			Next:    ccme.Version{Major: 1, Patch: 1},
		}
		p.Order = append(p.Order, name)
	}
	// The login runs in the space folder — the parent of every member package
	// (the packages' dirs here are bare names, so their shared parent is ".").
	// Which publish reaches the gate first no longer decides the cwd.
	r := &fakeRunner{fail: map[string]bool{"login .": true}}
	tg := &fakeTagger{}
	res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: &fakeChangelog{}, Build: 2, Publish: 2}).Run(context.Background(), p)

	for _, n := range []string{"a", "b"} {
		require.Equal(t, StatusFailed, res[n].Status, n)
		assert.Equal(t, "publish", res[n].FailedStage, n)
		assert.ErrorContains(t, res[n].Err, "login", n)
		assert.Equal(t, -1, r.indexOf("publish "+n), "%s must not publish without a login", n)
		assert.NotEqual(t, -1, r.indexOf("build "+n), "%s built before the login failed", n)
	}
	assert.Equal(t, 1, r.countPrefix("login"), "the failed login is not retried by the second publish")
	require.Equal(t, StatusPublished, res["c"].Status, "%v", res["c"].Err)
	assert.Equal(t, []string{"c@1.0.1"}, tg.tags, "only the other space's package is tagged")
}

func TestRunHooksOrderAndEnvironment(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	sp := p.Releases["a"].Pkg.Space
	sp.BeforeAllScript = []string{"h-all"}
	sp.BeforeBuildScript = []string{"h-bb"}
	sp.PostBuildScript = []string{"h-pb"}
	sp.BeforePublishScript = []string{"h-bp"}
	sp.PostPublishScript = []string{"h-pp"}
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	order := []string{"h-all a", "h-bb a", "build a", "h-pb a", "h-bp a", "publish a", "h-pp a"}
	for i := 1; i < len(order); i++ {
		assert.Less(t, r.indexOf(order[i-1]), r.indexOf(order[i]),
			"%s must run before %s", order[i-1], order[i])
	}
	// Every hook gets the full stage environment, distinguished only by
	// DISPAT_STAGE carrying the hook's name.
	env := r.envs["h-bb a"]
	assert.Equal(t, "beforeBuild", envValue(t, env, "DISPAT_STAGE"))
	assert.Equal(t, "a", envValue(t, env, "DISPAT_PACKAGE"))
	assert.Equal(t, "1.0.1", envValue(t, env, "DISPAT_NEW_VERSION"))
	assert.Equal(t, "A", envValue(t, env, "DISPAT_WORKSPACE_PACKAGES"))
	assert.Equal(t, "postPublish", envValue(t, r.envs["h-pp a"], "DISPAT_STAGE"))
	assert.Equal(t, "beforeAll", envValue(t, r.envs["h-all a"], "DISPAT_STAGE"))
}

func TestRunVersionHooksAndBeforeAllAtVersionTask(t *testing.T) {
	// b consumes a, so b has a version task; a does not. beforeAll runs at each
	// package's first task: the version task for b, the build for a.
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	sp := p.Releases["b"].Pkg.Space // shared by a and b
	sp.VersionScript = []string{"version"}
	sp.BeforeAllScript = []string{"h-all"}
	sp.BeforeVersionScript = []string{"h-bv"}
	sp.PostVersionScript = []string{"h-pv"}
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["b"].Status, "%v", res["b"].Err)
	assert.Less(t, r.indexOf("h-all a"), r.indexOf("build a"), "a's first task is its build")
	assert.Equal(t, -1, r.indexOf("h-bv a"), "a has no version task, so no version hooks")
	order := []string{"h-all b", "h-bv b", "version b", "h-pv b", "build b"}
	for i := 1; i < len(order); i++ {
		assert.Less(t, r.indexOf(order[i-1]), r.indexOf(order[i]),
			"%s must run before %s", order[i-1], order[i])
	}
	assert.Equal(t, "beforeVersion", envValue(t, r.envs["h-bv b"], "DISPAT_STAGE"))
	assert.Equal(t, "postVersion", envValue(t, r.envs["h-pv b"], "DISPAT_STAGE"))
}

func TestRunGatingHookFailuresFailTheRelease(t *testing.T) {
	// Every hook up to beforePublish exists to gate the release: its failure
	// fails the package and stops the pipeline right there.
	for _, tc := range []struct {
		hook    string
		set     func(sp *model.Space)
		blocked string // the event that must never run after the failure
	}{
		{"beforeAll", func(sp *model.Space) { sp.BeforeAllScript = []string{"hook"} }, "build a"},
		{"beforeBuild", func(sp *model.Space) { sp.BeforeBuildScript = []string{"hook"} }, "build a"},
		{"postBuild", func(sp *model.Space) { sp.PostBuildScript = []string{"hook"} }, "publish a"},
		{"beforePublish", func(sp *model.Space) { sp.BeforePublishScript = []string{"hook"} }, "publish a"},
	} {
		t.Run(tc.hook, func(t *testing.T) {
			p := mkPlan(planSpec{Names: []string{"a"}})
			tc.set(p.Releases["a"].Pkg.Space)
			r := &fakeRunner{fail: map[string]bool{"hook a": true}}
			tg := &fakeTagger{}
			cl := &fakeChangelog{}
			res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: cl, Build: 1, Publish: 1}).Run(context.Background(), p)

			require.Equal(t, StatusFailed, res["a"].Status)
			assert.ErrorContains(t, res["a"].Err, "boom")
			assert.Equal(t, -1, r.indexOf(tc.blocked),
				"%s must not run after the %s hook failed", tc.blocked, tc.hook)
			assert.Empty(t, tg.tags, "a failed release must not be tagged")
			assert.Empty(t, cl.entries, "or recorded")
		})
	}
}

func TestRunVersionHookFailuresFailTheConsumer(t *testing.T) {
	for _, tc := range []struct {
		hook    string
		set     func(sp *model.Space)
		blocked string
	}{
		{"beforeVersion", func(sp *model.Space) { sp.BeforeVersionScript = []string{"hook"} }, "version b"},
		{"postVersion", func(sp *model.Space) { sp.PostVersionScript = []string{"hook"} }, "build b"},
	} {
		t.Run(tc.hook, func(t *testing.T) {
			p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
			sp := p.Releases["b"].Pkg.Space
			sp.VersionScript = []string{"version"}
			tc.set(sp)
			r := &fakeRunner{fail: map[string]bool{"hook b": true}}
			res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

			require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
			require.Equal(t, StatusFailed, res["b"].Status)
			assert.Equal(t, "version", res["b"].FailedStage)
			assert.Equal(t, -1, r.indexOf(tc.blocked),
				"%s must not run after the %s hook failed", tc.blocked, tc.hook)
		})
	}
}

func TestRunPostPublishHookOnlyWarns(t *testing.T) {
	// postPublish observes a release that is already out: its failure must not
	// re-label a published package as failed, and the tag stays.
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.PostPublishScript = []string{"hook"}
	r := &fakeRunner{fail: map[string]bool{"hook a": true}}
	tg := &fakeTagger{}
	res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.NoError(t, res["a"].Err)
	assert.Equal(t, []string{"a@1.0.1"}, tg.tags)
	assert.NotEqual(t, -1, r.indexOf("hook a"), "the hook did run")
}

func TestRunScriptSequenceRunsInOrder(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	sp := p.Releases["a"].Pkg.Space
	sp.BuildScript = []string{"b1", "b2"}
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.Less(t, r.indexOf("b1 a"), r.indexOf("b2 a"), "sequence order is configuration order")
	assert.Less(t, r.indexOf("b2 a"), r.indexOf("publish a"), "the whole sequence precedes the next stage")
}

func TestRunScriptSequenceFailFast(t *testing.T) {
	// A gating sequence stops at the first failure: the remaining commands
	// must not run, and the package fails.
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.BuildScript = []string{"b1", "b2"}
	r := &fakeRunner{fail: map[string]bool{"b1 a": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "build", res["a"].FailedStage)
	assert.Equal(t, -1, r.indexOf("b2 a"), "the sequence stops at the first failure")
	assert.Equal(t, -1, r.indexOf("publish a"))
}

func TestRunWarnOnlySequenceRunsEveryCommand(t *testing.T) {
	// A warn-only sequence (postPublish) keeps going: one command failing does
	// not stop the others, and the package stays published.
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.PostPublishScript = []string{"p1", "p2"}
	r := &fakeRunner{fail: map[string]bool{"p1 a": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.NotEqual(t, -1, r.indexOf("p2 a"),
		"a failed command in a warn-only sequence must not stop the rest")
}

func TestRunEnvOutcomeListing(t *testing.T) {
	// a publishes, b (consuming a) fails its build, c (consuming b) is
	// blocked, quiet is in the workspace but not planned to release.
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}, "c": {"b"}}, Names: []string{"a", "b", "c"}})
	p.Releases["quiet"] = &plan.Release{
		Pkg:     &model.Package{Name: "quiet", Dir: "quiet", Space: p.Releases["a"].Pkg.Space},
		Current: ccme.Version{Major: 3},
		Next:    ccme.Version{Major: 3},
	}
	p.Order = append(p.Order, "quiet")
	r := &fakeRunner{fail: map[string]bool{"build b": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	env := RunEnv(p, res, zerolog.Nop())
	assert.Equal(t, "A", envValue(t, env, "DISPAT_PUBLISHED_PACKAGES"))
	assert.Equal(t, "B", envValue(t, env, "DISPAT_FAILED_PACKAGES"))
	assert.Equal(t, "C", envValue(t, env, "DISPAT_SKIPPED_PACKAGES"))
	assert.Equal(t, "QUIET", envValue(t, env, "DISPAT_UNPLANNED_PACKAGES"))

	assert.Equal(t, "published", envValue(t, env, "DISPAT_RESULT_A_STATUS"))
	assert.Equal(t, "1.0.0", envValue(t, env, "DISPAT_RESULT_A_OLD_VERSION"))
	assert.Equal(t, "1.0.1", envValue(t, env, "DISPAT_RESULT_A_NEW_VERSION"))
	assert.Equal(t, "failed", envValue(t, env, "DISPAT_RESULT_B_STATUS"))
	assert.Equal(t, "build", envValue(t, env, "DISPAT_RESULT_B_FAILED_STAGE"))
	assert.Equal(t, "skipped", envValue(t, env, "DISPAT_RESULT_C_STATUS"))
	assert.Equal(t, "b", envValue(t, env, "DISPAT_RESULT_C_BLOCKED_BY"))
	assertEnvUnset(t, env, "DISPAT_RESULT_QUIET_STATUS",
		"an unplanned package has no result to report")
	assertEnvUnset(t, env, "DISPAT_RESULT_A_FAILED_STAGE",
		"a published package has no failed stage")

	assert.Equal(t, "3.0.0", envValue(t, env, "DISPAT_WORKSPACE_QUIET_VERSION"),
		"the workspace listing rides along for every run hook")
}

func TestRunAnnounceStageOrderAndEnvironment(t *testing.T) {
	// announce is a fourth stage after the publish frame: postPublish first,
	// then beforeAnnounce, the announce script, postAnnounce.
	p := mkPlan(planSpec{Names: []string{"a"}})
	sp := p.Releases["a"].Pkg.Space
	sp.PostPublishScript = []string{"h-pp"}
	sp.AnnounceScript = []string{"announce"}
	sp.BeforeAnnounceScript = []string{"h-ba"}
	sp.PostAnnounceScript = []string{"h-pa"}
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	order := []string{"publish a", "h-pp a", "h-ba a", "announce a", "h-pa a"}
	for i := 1; i < len(order); i++ {
		assert.Less(t, r.indexOf(order[i-1]), r.indexOf(order[i]),
			"%s must run before %s", order[i-1], order[i])
	}
	assert.Equal(t, "announce", envValue(t, r.envs["announce a"], "DISPAT_STAGE"))
	assert.Equal(t, "beforeAnnounce", envValue(t, r.envs["h-ba a"], "DISPAT_STAGE"))
	assert.Equal(t, "postAnnounce", envValue(t, r.envs["h-pa a"], "DISPAT_STAGE"))
	assert.Equal(t, "a", envValue(t, r.envs["announce a"], "DISPAT_PACKAGE"),
		"announce gets the full stage environment")
}

func TestRunAnnounceFailuresOnlyWarn(t *testing.T) {
	// The whole announce frame observes a release that is already out: the
	// stage and both hooks only warn, and no failure stops the others.
	p := mkPlan(planSpec{Names: []string{"a"}})
	sp := p.Releases["a"].Pkg.Space
	sp.AnnounceScript = []string{"announce"}
	sp.BeforeAnnounceScript = []string{"h-ba"}
	sp.PostAnnounceScript = []string{"h-pa"}
	r := &fakeRunner{fail: map[string]bool{"h-ba a": true, "announce a": true}}
	tg := &fakeTagger{}
	res := newExecutor(execSpec{Runner: r, Tagger: tg, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.NoError(t, res["a"].Err)
	assert.Equal(t, []string{"a@1.0.1"}, tg.tags, "the release stands")
	assert.NotEqual(t, -1, r.indexOf("announce a"),
		"a failed beforeAnnounce must not stop the announce")
	assert.NotEqual(t, -1, r.indexOf("h-pa a"),
		"a failed announce must not stop postAnnounce")
}

func TestRunAnnounceSkippedWhenPublishFails(t *testing.T) {
	// No release, nothing to announce: a failed publish must keep the whole
	// announce frame off.
	p := mkPlan(planSpec{Names: []string{"a"}})
	sp := p.Releases["a"].Pkg.Space
	sp.AnnounceScript = []string{"announce"}
	sp.BeforeAnnounceScript = []string{"h-ba"}
	sp.PostAnnounceScript = []string{"h-pa"}
	r := &fakeRunner{fail: map[string]bool{"publish a": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	for _, ev := range []string{"h-ba a", "announce a", "h-pa a"} {
		assert.Equal(t, -1, r.indexOf(ev), "%s must not run after a failed publish", ev)
	}
}

func TestRunReleaseNotesEnvironment(t *testing.T) {
	// The notes variables carry the same grouping the changelog and the GitHub
	// release render as sections: major units are breaking changes, minor
	// features, patch fixes — headlines only, newline-separated.
	p := mkPlan(planSpec{Names: []string{"a"}})
	rel := p.Releases["a"]
	rel.Units = []*ccme.Unit{
		{Header: ccme.Header{Type: "feat", Description: "drop the old API"}, Bump: ccme.BumpMajor, Valid: true},
		{Header: ccme.Header{Type: "feat", Description: "add streaming"}, Bump: ccme.BumpMinor, Valid: true},
		{Header: ccme.Header{Type: "feat", Description: "add retries"}, Bump: ccme.BumpMinor, Valid: true},
		{Header: ccme.Header{Type: "fix", Description: "close a leak"}, Bump: ccme.BumpPatch, Valid: true},
	}
	rel.Pkg.Space.AnnounceScript = []string{"announce"}
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	env := r.envs["announce a"]
	assert.Equal(t, "drop the old API", envValue(t, env, "DISPAT_BREAKING_CHANGES"))
	assert.Equal(t, "add streaming\nadd retries", envValue(t, env, "DISPAT_FEATURES"),
		"entries are newline-separated in unit order")
	assert.Equal(t, "close a leak", envValue(t, env, "DISPAT_FIXES"))

	// Like every listing they reach every stage, and an empty group is empty
	// text, not an unset variable.
	assert.Equal(t, "drop the old API", envValue(t, r.envs["build a"], "DISPAT_BREAKING_CHANGES"))
	quiet := mkPlan(planSpec{OwnBump: map[string]ccme.Bump{"b": ccme.BumpPatch}, Names: []string{"b"}})
	r2 := &fakeRunner{}
	newExecutor(execSpec{Runner: r2, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), quiet)
	assert.Equal(t, "", envValue(t, r2.envs["build b"], "DISPAT_BREAKING_CHANGES"))
	assert.Equal(t, "own change", envValue(t, r2.envs["build b"], "DISPAT_FIXES"),
		"mkPlan's own-change unit is a patch fix")
}

func TestRunOnFailScript(t *testing.T) {
	// onFail fires once when the package fails, after the status settles,
	// with the failure specifics in the environment — and its own failure
	// only warns: the package cannot fail any harder.
	p := mkPlan(planSpec{Names: []string{"a"}})
	sp := p.Releases["a"].Pkg.Space
	sp.OnFailScript = []string{"on-fail"}
	sp.OnSkipScript = []string{"on-skip"}
	r := &fakeRunner{fail: map[string]bool{"build a": true, "on-fail a": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "build", res["a"].FailedStage)
	assert.Equal(t, 1, r.countPrefix("on-fail"), "once per package, not per task")
	assert.Equal(t, 0, r.countPrefix("on-skip"), "a failed package was not skipped")

	env := r.envs["on-fail a"]
	assert.Equal(t, "onFail", envValue(t, env, "DISPAT_STAGE"))
	assert.Equal(t, "build", envValue(t, env, "DISPAT_FAILED_STAGE"))
	assert.Contains(t, envValue(t, env, "DISPAT_ERROR"), "boom")
	assert.Equal(t, "a", envValue(t, env, "DISPAT_PACKAGE"), "the full package environment rides along")
}

func TestRunOnSkipScript(t *testing.T) {
	// b is skipped because its provider a failed: onSkip fires for b with the
	// blocking provider named; onFail fires for a and only a.
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	sp := p.Releases["a"].Pkg.Space // shared by both packages
	sp.OnFailScript = []string{"on-fail"}
	sp.OnSkipScript = []string{"on-skip"}
	r := &fakeRunner{fail: map[string]bool{"publish a": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	require.Equal(t, StatusSkipped, res["b"].Status)
	assert.NotEqual(t, -1, r.indexOf("on-fail a"))
	assert.Equal(t, -1, r.indexOf("on-skip a"), "a failed, it was not skipped")
	assert.Equal(t, 1, r.countPrefix("on-skip"), "once per skipped package")
	assert.Equal(t, -1, r.indexOf("on-fail b"), "b was skipped, it did not fail")

	env := r.envs["on-skip b"]
	assert.Equal(t, "onSkip", envValue(t, env, "DISPAT_STAGE"))
	assert.Equal(t, "a", envValue(t, env, "DISPAT_BLOCKED_BY"))
	assert.Equal(t, "b", envValue(t, env, "DISPAT_PACKAGE"))
}

func TestRunOutcomeScriptsSilentOnSuccess(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	sp := p.Releases["a"].Pkg.Space
	sp.OnFailScript = []string{"on-fail"}
	sp.OnSkipScript = []string{"on-skip"}
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.Equal(t, 0, r.countPrefix("on-fail"))
	assert.Equal(t, 0, r.countPrefix("on-skip"))
}

func TestRunLaunchOrderDeterministic(t *testing.T) {
	// The task graph is built in sorted name order and the scheduler is FIFO,
	// so with a serialising budget the launch order is a pure function of the
	// plan: two identical runs produce identical event sequences, in sorted
	// order — not whatever order the changed-set map iterates in.
	spec := planSpec{Names: []string{"c", "a", "b"}}
	runOnce := func() []string {
		r := &fakeRunner{}
		res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), mkPlan(spec))
		for n, re := range res {
			require.Equal(t, StatusPublished, re.Status, "%s: %v", n, re.Err)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		var builds []string
		for _, e := range r.events {
			if strings.HasPrefix(e, "build ") {
				builds = append(builds, e)
			}
		}
		return builds
	}
	first := runOnce()
	assert.Equal(t, []string{"build a", "build b", "build c"}, first)
	assert.Equal(t, first, runOnce(), "identical plans launch identically")
}
