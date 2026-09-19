// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// settleFleet is a choreographed fleet on disk: a consumer repository holding
// an out-of-date fleet link to a provider it depends on.
type settleFleet struct {
	recorder *workspaceRecorder
	rel      *plan.Release
	api      string
	sdk      string
	// head is the provider revision the settlement has to record.
	head string
}

// settleGit is recordGit for a fixture a benchmark shares with a test, which
// is why it takes testing.TB rather than *testing.T.
func settleGit(t testing.TB, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func settleRepo(t testing.TB, root string, cfg *config.File) {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0o755))
	settleGit(t, root, "init", "-q")
	settleGit(t, root, "symbolic-ref", "HEAD", "refs/heads/main")
	settleGit(t, root, "config", "user.email", "test@example.com")
	settleGit(t, root, "config", "user.name", "Test")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkg", "input"), []byte("one"), 0o644))
	settleGit(t, root, "add", ".")
	settleGit(t, root, "commit", "-qm", "feat(pkg): initial")
}

func newSettleFleet(t testing.TB, consumerCommits, providerCommits bool) *settleFleet {
	t.Helper()
	apiCfg := &config.File{Commit: &config.CommitConfig{Enabled: &consumerCommits}, Run: &config.RunConfig{}, UnsafeDisableLock: true}
	sdkCfg := &config.File{Commit: &config.CommitConfig{Enabled: &providerCommits}, Run: &config.RunConfig{}, UnsafeDisableLock: true}
	base := t.TempDir()
	api, sdk := filepath.Join(base, "api"), filepath.Join(base, "sdk")
	settleRepo(t, api, apiCfg)
	settleRepo(t, sdk, sdkCfg)
	pinned := settleGit(t, sdk, "rev-parse", "HEAD")

	// The link as a fleet carries it: a declared submodule, a pin, and the
	// empty folder that keeps the consumer's checkout clean.
	require.NoError(t, os.MkdirAll(filepath.Join(api, ".links", "sdk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(api, ".gitmodules"),
		[]byte("[submodule \"sdk\"]\n\tpath = .links/sdk\n\turl = "+sdk+"\n"), 0o644))
	settleGit(t, api, "add", ".gitmodules")
	settleGit(t, api, "update-index", "--add", "--cacheinfo", "160000", pinned, ".links/sdk")
	settleGit(t, api, "commit", "-qm", "chore: link sdk")

	// The provider moves on, so the consumer's recorded link is behind.
	require.NoError(t, os.WriteFile(filepath.Join(sdk, "pkg", "input"), []byte("two"), 0o644))
	settleGit(t, sdk, "commit", "-qam", "fix(lib): repair")
	head := settleGit(t, sdk, "rev-parse", "HEAD")

	a := New(api, apiCfg, zerolog.Nop())
	a.workspace = &config.Workspace{ControlRoot: api, Saga: config.SagaChoreography,
		Repositories: []config.Repository{
			{Name: "api", Root: api, Config: apiCfg, Commit: apiCfg.Commit, Imported: true, Entry: true,
				Links: map[string]string{"sdk": ".links/sdk"}},
			{Name: "sdk", Root: sdk, Config: sdkCfg, Commit: sdkCfg.Commit, Imported: true, Linker: "api",
				GitlinkPath: ".links/sdk", Links: map[string]string{"api": ".links/api"}},
		}}
	w := a.newWorkspaceRecorder()
	pkg := &model.Package{Name: "app", Repository: "api", RepoRoot: api,
		Dir: filepath.Join(api, "pkg"), Space: &model.Space{Name: "apps"}}
	rel := &plan.Release{Pkg: pkg, Channel: "stable", Next: ccme.Version{Major: 1}, Bump: ccme.BumpMinor, NewWork: true}
	w.linkPlan = map[string][]string{"app": {"sdk"}}
	return &settleFleet{recorder: w, rel: rel, api: api, sdk: sdk, head: head}
}

func (f *settleFleet) settle(t testing.TB, ctx context.Context) error {
	t.Helper()
	lanes, err := f.recorder.settleLanes(f.rel)
	if err != nil {
		return err
	}
	held, err := f.recorder.takeLanes(ctx, lanes)
	require.NoError(t, err)
	defer releaseLanes(held, "")
	return f.recorder.settleLinks(ctx, f.rel, held)
}

// TestSettleLinksRecordsTheRevisionTheReleaseIncorporates: the settlement is
// what puts the provider's revision into the consumer's tree, before the
// release commit that the tag will sit on.
func TestSettleLinksRecordsTheRevisionTheReleaseIncorporates(t *testing.T) {
	f := newSettleFleet(t, true, true)
	before := settleGit(t, f.api, "rev-parse", "HEAD")

	require.NoError(t, f.settle(t, t.Context()))
	after := settleGit(t, f.api, "rev-parse", "HEAD")
	assert.NotEqual(t, before, after, "the consumer recorded a settlement commit")
	assert.Equal(t, f.head, settleGit(t, f.api, "rev-parse", "HEAD:.links/sdk"),
		"the consumer's tree now pins the revision its release incorporates")
	assert.Equal(t, after, f.recorder.byName["api"].expectedHead)
	assert.Equal(t, "main", settleGit(t, f.api, "rev-parse", "--abbrev-ref", "HEAD"))

	// Settling again is the retry: the links already say this, so nothing is
	// written and nothing moves.
	require.NoError(t, f.settle(t, t.Context()))
	assert.Equal(t, after, settleGit(t, f.api, "rev-parse", "HEAD"), "an exact settlement commits nothing")
}

// TestSettleLinksIsAdmittedByThePrePublishGuard: the settlement moves the
// consumer's HEAD after the plan was computed, which is precisely what the
// pre-publish guard refuses. It has to be announced, not discovered.
func TestSettleLinksIsAdmittedByThePrePublishGuard(t *testing.T) {
	f := newSettleFleet(t, true, true)
	require.NoError(t, f.recorder.captureSnapshot(t.Context(), []*model.Package{f.rel.Pkg}))
	planned := settleGit(t, f.api, "rev-parse", "HEAD")
	for _, record := range f.recorder.ordered {
		record.expectedHead = settleGit(t, record.repo.Root, "rev-parse", "HEAD")
	}

	require.NoError(t, f.settle(t, t.Context()))
	require.NoError(t, f.recorder.verifySnapshot(t.Context(), f.rel),
		"the settled revision is the one the guard now expects")

	// Proof that the guard would have refused it: put the planned revision
	// back and the same verification stops the release with E330.
	f.recorder.byName["api"].expectedHead = planned
	err := f.recorder.verifySnapshot(t.Context(), f.rel)
	require.Error(t, err)
	assert.Equal(t, config.DiagnosticRepositoryInvalid, config.DiagnosticCode(err))
	assert.ErrorContains(t, err, "HEAD moved")
}

// TestSettleLinksRefusesAnIncompleteRoute: evidence that stops halfway is
// worse than none, because the package would already have published by the
// time the next plan reports it.
func TestSettleLinksRefusesAnIncompleteRoute(t *testing.T) {
	f := newSettleFleet(t, true, false)
	before := settleGit(t, f.api, "rev-parse", "HEAD")
	err := f.settle(t, t.Context())
	require.Error(t, err)
	assert.Equal(t, config.DiagnosticBoundary, config.DiagnosticCode(err))
	assert.ErrorContains(t, err, "release commits are disabled")
	assert.Equal(t, before, settleGit(t, f.api, "rev-parse", "HEAD"), "nothing was recorded before the refusal")
	assert.ErrorContains(t, err, "add repositoryBaselines")
}

// TestSettleLinksRecordsNothingForATagOnlyConsumer: a consumer that writes no
// release commit has nowhere to record evidence, and says so rather than
// failing.
func TestSettleLinksRecordsNothingForATagOnlyConsumer(t *testing.T) {
	f := newSettleFleet(t, false, true)
	before := settleGit(t, f.api, "rev-parse", "HEAD")
	require.NoError(t, f.settle(t, t.Context()))
	assert.Equal(t, before, settleGit(t, f.api, "rev-parse", "HEAD"))
}

// TestSettleLinksRefusesADetachedPush: a repository that will push a
// settlement needs a branch to push it on, and E337 is the same refusal an
// orchestrated checkpoint makes.
func TestSettleLinksRefusesADetachedPush(t *testing.T) {
	f := newSettleFleet(t, true, true)
	f.recorder.byName["api"].repo.Commit.Push = true
	f.recorder.byName["api"].branch = ""
	err := f.settle(t, t.Context())
	require.Error(t, err)
	assert.Equal(t, "E337", config.DiagnosticCode(err))
	assert.ErrorContains(t, err, "set commit.branch")
}

// TestSettleLanesAreTakenInNameOrder: two consumers whose routes overlap take
// the same lanes in the same order, which is what keeps the wait graph free
// of cycles. The handshake is bounded: a cycle would time out here.
func TestSettleLanesAreTakenInNameOrder(t *testing.T) {
	f := newSettleFleet(t, true, true)
	lanes, err := f.recorder.settleLanes(f.rel)
	require.NoError(t, err)
	assert.Equal(t, []string{"api", "sdk"}, lanes, "lanes are named in sorted order")

	held, err := f.recorder.takeLanes(t.Context(), lanes)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		second, err := f.recorder.takeLanes(ctx, lanes)
		releaseLanes(second, "")
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("a second settlement took the lanes while the first held them: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseLanes(held, "")
	select {
	case err := <-done:
		require.NoError(t, err, "the lanes are free once the first settlement released them")
	case <-time.After(2 * time.Second):
		t.Fatal("the second settlement never acquired the lanes")
	}
}

// TestRouteRepositoriesAreVerifiedLikeAnyOtherRecorder: a repository this
// release commits and pushes into is verified before the run starts, exactly
// as the control repository of an orchestrated fleet is.
func TestRouteRepositoriesAreVerifiedLikeAnyOtherRecorder(t *testing.T) {
	f := newSettleFleet(t, true, true)
	pl := &plan.Plan{Order: []string{"app"}, Releases: map[string]*plan.Release{"app": f.rel}}
	selected := f.recorder.selectedRepositories(pl)
	assert.True(t, selected["api"])
	assert.True(t, selected["sdk"], "the provider records this release's fleet link")
}

// TestLockBypassIsPerRepositoryInAChoreographedFleet: one peer's unsafe
// setting is its own. An orchestrated fleet keeps releasing under the control
// repository's policy.
func TestLockBypassIsPerRepositoryInAChoreographedFleet(t *testing.T) {
	f := newSettleFleet(t, true, true)
	api, sdk := f.recorder.byName["api"], f.recorder.byName["sdk"]
	api.repo.Config.UnsafeDisableLock = true
	sdk.repo.Config.UnsafeDisableLock = false
	f.recorder.app.cfg = api.repo.Config

	bypassed, byConfig := f.recorder.lockBypass(api)
	assert.True(t, bypassed)
	assert.True(t, byConfig)
	bypassed, _ = f.recorder.lockBypass(sdk)
	assert.False(t, bypassed, "the entry's setting does not unlock its peers")

	f.recorder.app.workspace.Saga = config.SagaOrchestration
	bypassed, byConfig = f.recorder.lockBypass(sdk)
	assert.True(t, bypassed, "an orchestrated fleet releases under one policy")
	assert.True(t, byConfig)

	t.Setenv("DISPAT_UNSAFE_DISABLE_LOCK", "true")
	f.recorder.app.workspace.Saga = config.SagaChoreography
	bypassed, byConfig = f.recorder.lockBypass(sdk)
	assert.True(t, bypassed, "the environment switch is the invocation's, and covers the run")
	assert.False(t, byConfig)
}

// TestWorkspaceContextEnvHandsDownTheEntryAndTheSaga: a nested dispat command
// has to compose the same fleet, which means starting where the run started
// and under the saga it chose.
func TestWorkspaceContextEnvHandsDownTheEntryAndTheSaga(t *testing.T) {
	f := newSettleFleet(t, true, true)
	f.recorder.app.workspace.Repositories[0].ConfigPath = filepath.Join(f.api, "dispat.json")
	f.recorder.app.workspace.Repositories[1].ConfigPath = filepath.Join(f.sdk, "dispat.json")

	env := workspaceContextEnv(f.recorder.app.workspace)
	assert.Contains(t, env, workspaceRootEnv+"="+f.api)
	assert.Contains(t, env, workspaceConfigEnv+"=dispat.json")
	assert.Contains(t, env, workspaceSagaEnv+"="+config.SagaChoreography)
	assert.Contains(t, env, workspaceImportsEnv+"=null",
		"a choreographed fleet imports nothing: the nested command walks the links itself")

	f.recorder.app.workspace.Saga = config.SagaOrchestration
	for _, pair := range workspaceContextEnv(f.recorder.app.workspace) {
		assert.NotContains(t, pair, workspaceSagaEnv, "an orchestrated run hands down no saga")
	}
}

// TestSetLinkPlanReadsTheReleasesForeignInputs: what a release has to record
// is exactly the repositories its plan read history from, other than its own.
func TestSetLinkPlanReadsTheReleasesForeignInputs(t *testing.T) {
	f := newSettleFleet(t, true, true)
	f.recorder.linkPlan = nil
	pl := &plan.Plan{
		Order:                []string{"app"},
		Releases:             map[string]*plan.Release{"app": f.rel},
		RepositoryInputOrder: []string{"api", "sdk"},
		RepositoryInputs:     map[string][]uint64{"app": {0b11}},
	}
	f.recorder.setLinkPlan(pl)
	assert.Equal(t, map[string][]string{"app": {"sdk"}}, f.recorder.linkPlan,
		"a release records the repositories it read, never its own")

	// A release that reads only its own repository settles nothing.
	pl.RepositoryInputs["app"] = []uint64{0b01}
	f.recorder.setLinkPlan(pl)
	assert.Empty(t, f.recorder.linkPlan)

	// An orchestrated fleet records checkpoints instead, and plans no links.
	f.recorder.app.workspace.Saga = config.SagaOrchestration
	f.recorder.linkPlan = nil
	pl.RepositoryInputs["app"] = []uint64{0b11}
	f.recorder.setLinkPlan(pl)
	assert.Nil(t, f.recorder.linkPlan)
}

// TestSettleLinksRefusesARepositoryNoLinkReaches: a release that reads a
// repository the fleet does not join cannot record what it incorporated, and
// says so before anything publishes.
func TestSettleLinksRefusesARepositoryNoLinkReaches(t *testing.T) {
	f := newSettleFleet(t, true, true)
	f.recorder.app.workspace.Repositories[0].Links = nil
	f.recorder.app.workspace.Repositories[1].Links = nil
	_, err := f.recorder.settleLanes(f.rel)
	require.Error(t, err)
	assert.Equal(t, config.DiagnosticBoundary, config.DiagnosticCode(err))
	assert.ErrorContains(t, err, "no chain of fleet links joins")
	assert.Nil(t, f.recorder.routeRepositories(f.rel), "a route that cannot be planned selects nothing")
}

// TestSettleLinksPushesWhatItRecorded: a settlement is durable only once the
// remote holds it, and a run interrupted between the commit and the push
// converges by pushing a head the remote is missing.
func TestSettleLinksPushesWhatItRecorded(t *testing.T) {
	f := newSettleFleet(t, true, true)
	remote := filepath.Join(t.TempDir(), "api.git")
	settleGit(t, f.api, "init", "-q", "--bare", remote)
	settleGit(t, f.api, "remote", "add", "origin", remote)
	settleGit(t, f.api, "push", "-q", "origin", "HEAD:refs/heads/main")

	// The first settlement commits while pushing is off: the local record
	// exists and the remote does not have it, which is what an interruption
	// between the two leaves behind.
	require.NoError(t, f.settle(t, t.Context()))
	settled := settleGit(t, f.api, "rev-parse", "HEAD")
	assert.NotEqual(t, settled, settleGit(t, remote, "rev-parse", "refs/heads/main"))

	api := f.recorder.byName["api"]
	api.repo.Commit.Push = true
	api.branch = "main"
	require.NoError(t, f.settle(t, t.Context()), "the retry pushes the head the remote is missing")
	assert.Equal(t, settled, settleGit(t, remote, "rev-parse", "refs/heads/main"))
	assert.Equal(t, settled, settleGit(t, f.api, "rev-parse", "HEAD"), "nothing new was committed")

	// Once the remote holds it, settling again is a read and nothing more.
	require.NoError(t, f.settle(t, t.Context()))
	assert.Equal(t, settled, settleGit(t, remote, "rev-parse", "refs/heads/main"))
}

// settleHookLog records which hooks a settlement fired, in order.
func settleHookLog(t testing.TB, f *settleFleet) *[]string {
	t.Helper()
	fired := &[]string{}
	for _, record := range f.recorder.ordered {
		name := record.repo.Name
		record.repo.Config.Scripts = map[string]config.Script{}
		run := &config.RunConfig{}
		for hook, refs := range map[string]*[]string{
			"beforeCommit": &run.BeforeCommit, "afterCommit": &run.AfterCommit,
			"postCommit": &run.PostCommit, "beforePush": &run.BeforePush, "afterPush": &run.AfterPush,
		} {
			script := name + ":" + hook
			record.repo.Config.Scripts[script] = config.Script{script}
			*refs = []string{script}
		}
		record.repo.Config.Run = run
		record.hooks = &runHooks{cfg: record.repo.Config, root: record.repo.Root, log: zerolog.Nop(),
			runner: scriptRunnerFunc(func(_ context.Context, _, command string, _ []string, _, _ io.Writer) error {
				*fired = append(*fired, command)
				return nil
			})}
	}
	return fired
}

// scriptRunnerFunc is a script.Runnerx that only records what it was asked to
// run, which is all a hook-order assertion needs.
type scriptRunnerFunc func(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) error

func (f scriptRunnerFunc) Run(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) error {
	return f(ctx, dir, command, env, stdout, stderr)
}

// TestSettleLinksFiresTheRecordingRepositoryHooks: a settlement is a commit
// dispat makes in that repository, so the repository's commit hooks run
// around it exactly as they do around a checkpoint — and a settlement that
// records nothing is not a commit, so it fires nothing at all.
func TestSettleLinksFiresTheRecordingRepositoryHooks(t *testing.T) {
	f := newSettleFleet(t, true, true)
	fired := settleHookLog(t, f)

	require.NoError(t, f.settle(t, t.Context()))
	assert.Equal(t, []string{"api:beforeCommit", "api:afterCommit", "api:postCommit"}, *fired,
		"the repository that recorded the link fires its commit hooks, and the far end fires none")

	*fired = nil
	require.NoError(t, f.settle(t, t.Context()))
	assert.Empty(t, *fired, "the fast path records nothing and is not a commit")
}

// TestSettleLinksFiresThePushHooksAroundAPush: the push half of the bracket,
// which only a settlement that actually reaches a remote fires.
func TestSettleLinksFiresThePushHooksAroundAPush(t *testing.T) {
	f := newSettleFleet(t, true, true)
	remote := filepath.Join(t.TempDir(), "api.git")
	settleGit(t, f.api, "init", "-q", "--bare", remote)
	settleGit(t, f.api, "remote", "add", "origin", remote)
	settleGit(t, f.api, "push", "-q", "origin", "HEAD:refs/heads/main")
	sdkRemote := filepath.Join(t.TempDir(), "sdk.git")
	settleGit(t, f.sdk, "init", "-q", "--bare", sdkRemote)
	settleGit(t, f.sdk, "remote", "add", "origin", sdkRemote)
	settleGit(t, f.sdk, "push", "-q", "origin", "HEAD:refs/heads/main")
	fired := settleHookLog(t, f)
	api := f.recorder.byName["api"]
	api.repo.Commit.Push = true
	api.branch = "main"
	f.recorder.byName["sdk"].branch = "main"

	require.NoError(t, f.settle(t, t.Context()))
	assert.Equal(t, []string{
		"api:beforeCommit", "api:afterCommit", "api:postCommit", "api:beforePush", "api:afterPush",
	}, *fired)
	assert.Equal(t, settleGit(t, f.api, "rev-parse", "HEAD"),
		settleGit(t, remote, "rev-parse", "refs/heads/main"))

	*fired = nil
	require.NoError(t, f.settle(t, t.Context()))
	assert.Empty(t, *fired, "an exact settlement already on the remote pushes nothing and fires nothing")
}

// TestSettleLinksVerifiesADetachedPeerAgainstItsRosterBranch: every linked
// checkout is detached, so the repository at the far end of a route states no
// branch of its own. The roster is where the fleet wrote down which branch
// that peer releases on, and it is enough to ask a remote a question.
func TestSettleLinksVerifiesADetachedPeerAgainstItsRosterBranch(t *testing.T) {
	f := newSettleFleet(t, true, true)
	sdkRemote := filepath.Join(t.TempDir(), "sdk.git")
	settleGit(t, f.sdk, "init", "-q", "--bare", sdkRemote)
	settleGit(t, f.sdk, "remote", "add", "origin", sdkRemote)
	settleGit(t, f.sdk, "push", "-q", "origin", "HEAD:refs/heads/main")
	settleGit(t, f.sdk, "checkout", "-q", "--detach")
	apiRemote := filepath.Join(t.TempDir(), "api.git")
	settleGit(t, f.api, "init", "-q", "--bare", apiRemote)
	settleGit(t, f.api, "remote", "add", "origin", apiRemote)
	settleGit(t, f.api, "push", "-q", "origin", "HEAD:refs/heads/main")
	api := f.recorder.byName["api"]
	api.repo.Commit.Push = true
	api.branch = "main"

	// Nothing states a branch for the peer: the refusal names both remedies.
	err := f.settle(t, t.Context())
	require.Error(t, err)
	assert.ErrorContains(t, err, "has no branch to verify revision")
	assert.ErrorContains(t, err, "commit.branch")
	assert.ErrorContains(t, err, "fleet roster")

	// The roster of the repository that links it states one, which is the
	// branch the link follows.
	f.recorder.app.workspace.Repositories[0].Config.Repositories = []config.RepositoryLinkConfig{
		{Name: "sdk", Branch: "main"}}
	require.NoError(t, f.settle(t, t.Context()))
	assert.Equal(t, f.head, settleGit(t, f.api, "rev-parse", "HEAD:.links/sdk"))

	branch, source := f.recorder.verificationBranch(f.recorder.byName["sdk"])
	assert.Equal(t, "main", branch)
	assert.Equal(t, "repositories[api]", source)

	// A repository that states its own branch keeps deciding for itself.
	f.recorder.byName["sdk"].repo.Commit.Branch = "release"
	branch, source = f.recorder.verificationBranch(f.recorder.byName["sdk"])
	assert.Equal(t, "release", branch)
	assert.Equal(t, "commit.branch", source)
}

// TestSettlePlanIsComputedOncePerRelease: the route tree answers two
// questions in a run — which lanes to take, and which repositories to verify
// before it starts — and computing it twice would walk the link graph twice.
func TestSettlePlanIsComputedOncePerRelease(t *testing.T) {
	f := newSettleFleet(t, true, true)
	first, err := f.recorder.planLinks(f.rel)
	require.NoError(t, err)
	second, err := f.recorder.planLinks(f.rel)
	require.NoError(t, err)
	assert.Same(t, first, second, "the route tree is memoised for the release")

	pl := &plan.Plan{Order: []string{"app"}, Releases: map[string]*plan.Release{"app": f.rel},
		RepositoryInputOrder: []string{"api", "sdk"}, RepositoryInputs: map[string][]uint64{"app": {0b11}}}
	f.recorder.setLinkPlan(pl)
	third, err := f.recorder.planLinks(f.rel)
	require.NoError(t, err)
	assert.NotSame(t, first, third, "a new plan drops the routes the previous one computed")
}

// BenchmarkSettleLinksExactFastPath measures what a settlement costs when the
// links already say what the release incorporates, which is every run after
// the first: the fast path is one tree read per node.
func BenchmarkSettleLinksExactFastPath(b *testing.B) {
	f := newSettleFleet(b, true, true)
	ctx := context.Background()
	lanes, err := f.recorder.settleLanes(f.rel)
	if err != nil {
		b.Fatal(err)
	}
	held, err := f.recorder.takeLanes(ctx, lanes)
	if err != nil {
		b.Fatal(err)
	}
	if err := f.recorder.settleLinks(ctx, f.rel, held); err != nil {
		b.Fatal(err)
	}
	before := gitx.GitInvocations()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := f.recorder.settleLinks(ctx, f.rel, held); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	releaseLanes(held, "")
	b.ReportMetric(float64(gitx.GitInvocations()-before)/float64(b.N), "GitInvocations/op")
}
