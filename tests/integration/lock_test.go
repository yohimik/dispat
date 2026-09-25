package integration

// Goal 30: the release lock. One tag on the remote decides who releases.
//
// Two releases of one repository at once is not a race dispat can win by
// being careful, so it refuses to enter it: the first run to push
// dispat-release-lock releases, the second is told to come back later, and
// the tag is gone by the time either of them exits. Every claim here is about
// what is on the remote at a given moment, so the scenarios read it directly
// from the bare repository — and the ones that need to know what was there
// *during* a run read it from a hook, which runs while the lock is held.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// lockTag is the ref every release contends for.
const lockTag = "dispat-release-lock"

// releaseLocked runs a release with the lock switched back on — the harness
// disables it for every other scenario, since most fixtures have no remote to
// take it on.
func releaseLocked(r *harness.Repo, flags ...string) harness.RunResult {
	r.T.Helper()
	return r.CommandEnv(harness.LockEnabled, flags...)
}

// bareGit runs one git command inside a bare repository, which is how these
// tests see the remote as another machine would. It names an identity of its
// own: a crafted `commit-tree` needs an author, and the gate's container has
// no global configuration to lend one, unlike a developer's machine.
func bareGit(t *testing.T, bare string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", bare,
		"-c", "user.name=dispat integration", "-c", "user.email=integration@dispat.test"}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// remoteHoldsLock reports whether the lock tag is on the remote right now.
func remoteHoldsLock(t *testing.T, bare string) bool {
	t.Helper()
	return strings.Contains(bareGit(t, bare, "tag"), lockTag)
}

// lockObject is the tag object the remote's lock resolves to. Two runs never
// write the same one, which is what makes "is this still their lock?" a
// question with an answer.
func lockObject(t *testing.T, bare string) string {
	t.Helper()
	return strings.TrimSpace(bareGit(t, bare, "rev-parse", "refs/tags/"+lockTag))
}

// holdLock puts somebody else's lock on the remote: an annotated tag written
// straight into the bare repository, exactly as another machine's release
// would have left it.
func holdLock(t *testing.T, r *harness.Repo, bare string) string {
	t.Helper()
	r.Git("push", "-q", "origin", "HEAD")
	bareGit(t, bare, "-c", "user.email=other@dispat.test", "-c", "user.name=other clone",
		"tag", "-a", lockTag, "-m", "held by another release", harness.DefaultBranch)
	return lockObject(t, bare)
}

// assertLockCleared fails unless the tag is gone from both copies.
func assertLockCleared(t *testing.T, r *harness.Repo, bare string) {
	t.Helper()
	assert.False(t, r.IsTagged(lockTag), "the local lock tag outlived the run: %v", r.TagList())
	assert.False(t, remoteHoldsLock(t, bare), "the lock tag outlived the run on the remote")
}

// probeConfig adds a beforeAll hook that records what the remote's tags are
// while the run holds the lock. Reading the file afterwards is how a test
// asserts on a ref that only exists mid-run.
func probeConfig(cfg *models.File) {
	cfg.Scripts["probe"] = models.Script{"git ls-remote --tags origin > lock.probe"}
	cfg.Run = &models.RunConfig{BeforeAll: []string{"probe"}}
}

// heldDuringRun reports whether the mid-run probe saw the lock.
func heldDuringRun(t *testing.T, r *harness.Repo) bool {
	t.Helper()
	data, err := os.ReadFile(r.Path("lock.probe"))
	require.NoError(t, err, "the beforeAll probe never ran")
	return strings.Contains(string(data), lockTag)
}

// TestReleaseLockRoundTrip: the ordinary life of a lock. It is on the remote
// while the run works and gone from both copies once it is over.
func TestReleaseLockRoundTrip(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	probeConfig(&cfg)
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()

	res := releaseLocked(r)
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.True(t, heldDuringRun(t, r), "the run held the lock while it worked")
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	assert.Contains(t, bareGit(t, bare, "tag"), "core@0.1.0", "the release still reached the remote")
	assertLockCleared(t, r, bare)
}

// TestReleaseLockHeldElsewhere: the refusal. A lock already on the remote
// stops the run before it plans anything, and, the part that matters, the run
// leaves the holder's tag exactly as it found it, whatever `commit.force`
// says. A refusal that stomped on the lock would hand both runs the
// repository.
func TestReleaseLockHeldElsewhere(t *testing.T) {
	for name, force := range map[string]*bool{
		"commit.force unset": nil,
		// Forcing rewrites this run's own refs; the lock is somebody else's.
		"commit.force true": models.Bool(true),
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			cfg := libsConfig(markerBuild, 1)
			if force != nil {
				cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true, Force: force}
			}
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first")
			bare := r.AddBareRemote()
			held := holdLock(t, r, bare)

			res := releaseLocked(r)
			assert.Equal(t, 1, res.Code, "a repository somebody else is releasing is refused")
			assert.Contains(t, res.Stdout, "unable to create the release lock tag")
			assert.Contains(t, res.Stdout, "delete the tag on the remote",
				"the refusal says what to do if nothing really is releasing")
			assert.Equal(t, 0, buildRuns(r), "nothing was built")
			assert.Equal(t, 0, r.TagCount("core@"), "nothing was tagged")
			assert.Equal(t, held, lockObject(t, bare), "the holder's lock is untouched")
			assert.False(t, r.IsTagged(lockTag), "and the refused run kept no local lock either")

			// The holder finishes and drops the lock; the same repository releases.
			bareGit(t, bare, "tag", "-d", lockTag)
			res = releaseLocked(r)
			require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
			assert.Equal(t, 1, r.TagCount("core@"))
			assertLockCleared(t, r, bare)
		})
	}
}

// TestReleaseLockRefusalWithoutAHolderToName: the refusal names who holds the
// lock when the lock's own message says so. A lock someone wrote by hand as a
// lightweight tag carries no message, and a message Git cannot fetch or read
// names nobody either: the run is refused all the same, without a holder
// line, and the lock stays exactly as it was.
func TestReleaseLockRefusalWithoutAHolderToName(t *testing.T) {
	for _, tc := range []struct {
		name          string
		isLightweight bool
		pattern       string
	}{
		{name: "a lightweight lock tag", isLightweight: true},
		{name: "a message Git cannot fetch", pattern: "*fetch --no-tags * refs/tags/" + lockTag + "*"},
		{name: "a fetched message Git cannot read", pattern: "*cat-file -p FETCH_HEAD*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := harness.New(t)
			r.WriteConfigModel(libsConfig(markerBuild, 1))
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first")
			bare := r.AddBareRemote()
			held := holdLock(t, r, bare)
			env := harness.LockEnabled
			var fault *harness.GitFault
			if tc.isLightweight {
				bareGit(t, bare, "tag", "-d", lockTag)
				bareGit(t, bare, "tag", lockTag, harness.DefaultBranch)
				held = lockObject(t, bare)
			} else {
				fault = harness.NewGitFault(t, harness.GitFault{Pattern: tc.pattern})
				env = append(append([]string{}, env...), fault.Env()...)
			}

			res := r.CommandEnv(env)
			assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
			assert.Contains(t, res.Stdout, "unable to create the release lock tag")
			assert.NotContains(t, res.Stdout, "held for", "no holder is named")
			assert.Equal(t, 0, buildRuns(r), "nothing was built")
			assert.Equal(t, 0, r.TagCount("core@"), "nothing was tagged")
			assert.Equal(t, held, lockObject(t, bare), "the lock is untouched")
			if fault != nil {
				assert.NotZero(t, fault.Matches(), "the holder's message was asked for")
			}
		})
	}
}

// TestReleaseLockBlocksConcurrentRuns: the claim the whole feature is for,
// with two real releases against one remote. The first holds the lock inside
// a hook until the test lets it go; the second, started in that window, is
// refused. No sleeps: the second run starts only once the lock is provably on
// the remote.
func TestReleaseLockBlocksConcurrentRuns(t *testing.T) {
	first := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	// The hook holds the run open until the test creates the gate file.
	cfg.Scripts["gate"] = models.Script{"while [ ! -f release.gate ]; do sleep 0.05; done"}
	cfg.Run = &models.RunConfig{BeforeAll: []string{"gate"}}
	first.WriteConfigModel(cfg)
	first.SeedPackage("packages", "core")
	first.Commit("feat(core): first")
	bare := first.AddBareRemote()

	// A second checkout of the same project, coordinating through the same
	// remote: a second CI runner, or a colleague's machine.
	second := harness.New(t)
	second.WriteConfigModel(libsConfig(markerBuild, 1))
	second.SeedPackage("packages", "core")
	second.Commit("feat(core): first")
	second.Git("remote", "add", "origin", bare)

	proc := first.StartReleaseEnv(harness.LockEnabled)
	require.Eventually(t, func() bool { return remoteHoldsLock(t, bare) },
		20*time.Second, 20*time.Millisecond, "the first run never took the lock")

	res := releaseLocked(second)
	assert.Equal(t, 1, res.Code, "a second release while one is running is refused")
	assert.Contains(t, res.Stdout, "unable to create the release lock tag")
	assert.Equal(t, 0, buildRuns(second), "the refused run built nothing")

	second.WriteFile("release.gate", "go\n") // wrong repository: the first run waits on its own
	first.WriteFile("release.gate", "go\n")
	out := proc.Wait()
	require.Equal(t, 0, out.Code, "stdout:\n%s\nstderr:\n%s", out.Stdout, out.Stderr)
	assert.Equal(t, 1, first.TagCount("core@"), "the run that held the lock released")
	assertLockCleared(t, first, bare)

	// And now that the lock is free, the run that was turned away goes through.
	res = releaseLocked(second)
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Equal(t, 1, second.TagCount("core@"))
}

// TestReleaseLockBlocksASecondRunInTheSameCheckout: two releases started in
// one working directory share its tags, its index and its files, and the lock
// is the only thing that keeps them apart. Each attempt offers a tag object of
// its own under the one remote name, so the second run cannot retarget the
// first run's claim: it is refused with E336, and it leaves the checkout
// exactly as it found it, the first run's own attempt tag included. The first
// run is held by a stand-in git on its first call after the lock is on the
// remote, so the second starts inside that window by construction.
func TestReleaseLockBlocksASecondRunInTheSameCheckout(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(markerBuild, 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()
	r.Git("push", "-q", "origin", "HEAD")
	hold := harness.NewGitFault(t, harness.GitFault{
		ArmAfter: "*push*refs/tags/" + lockTag + "*", Pattern: "*", Nth: 1, Hold: true})

	first := r.StartReleaseEnv(append(hold.Env(), harness.LockEnabled...))
	require.Eventually(t, func() bool { return hold.IsHeld() && remoteHoldsLock(t, bare) },
		20*time.Second, 20*time.Millisecond, "the first run never held the lock")
	held := lockObject(t, bare)
	tagsBefore, headBefore := r.TagList(), r.Git("rev-parse", "HEAD")

	second := releaseLocked(r)
	assert.Equal(t, 1, second.Code, "stdout:\n%s\nstderr:\n%s", second.Stdout, second.Stderr)
	assert.True(t, harness.IsCodePresent(second.Events, "E336"), "stdout:\n%s", second.Stdout)
	assert.Equal(t, held, lockObject(t, bare), "the first run's lock is untouched")
	assert.Equal(t, tagsBefore, r.TagList(), "the refused run leaves every tag as it found it")
	assert.Equal(t, headBefore, r.Git("rev-parse", "HEAD"))
	assert.Empty(t, r.Git("status", "--porcelain"), "and no file of the checkout")
	assert.Zero(t, buildRuns(r), "nothing was built while the first run was held")

	hold.Resume()
	out := first.Wait()
	require.Equal(t, 0, out.Code, "stdout:\n%s\nstderr:\n%s", out.Stdout, out.Stderr)
	assert.Equal(t, 1, buildRuns(r), "only the run that held the lock built")
	assert.Equal(t, 1, r.TagCount("core@"), "and released")
	assertLockCleared(t, r, bare)
}

// TestReleaseLockIndependentOfPush: the lock is not the release push. A
// repository that pushes nothing — no release commit, no tags on the remote —
// still takes it, because two such runs race over the same versions just as
// badly. All the remote ever sees is the lock, and then not even that.
func TestReleaseLockIndependentOfPush(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1) // no commit object at all: nothing is pushed
	probeConfig(&cfg)
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()

	res := releaseLocked(r)
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.True(t, heldDuringRun(t, r), "the lock is taken with the push turned off")
	assert.True(t, r.IsTagged("core@0.1.0"), "the release tag is local, as configured")
	assertLockCleared(t, r, bare)
	assert.Empty(t, strings.TrimSpace(bareGit(t, bare, "tag")), "no release tag was pushed")
	assert.Empty(t, strings.TrimSpace(bareGit(t, bare, "branch", "--list")),
		"and no branch: the lock moved no work onto the remote")
}

// TestReleaseLockWithoutRemote: with the lock on, a repository with no remote
// cannot coordinate with anyone, so it does not release. This is the cost of
// the guard, and it is stated as plainly here as in the docs.
func TestReleaseLockWithoutRemote(t *testing.T) {
	r := singlePackageRepo(t, markerBuild)
	r.Commit("feat(core): first")

	res := releaseLocked(r)
	assert.Equal(t, 1, res.Code, "no remote, no lock, no release")
	assert.Contains(t, res.Stdout, "unable to create the release lock tag")
	assert.Equal(t, 0, buildRuns(r))
	assert.Equal(t, 0, r.TagCount("core@"))
	assert.False(t, r.IsTagged(lockTag), "the failed attempt left no tag behind")
}

// TestReleaseLockKillSwitch: the escape hatch, through the binary. Only a
// value that plainly reads as true turns the lock off; a typo does not, which
// is the point — an unguarded release is not something to fall into.
func TestReleaseLockKillSwitch(t *testing.T) {
	for name, tc := range map[string]struct {
		value    string
		releases bool
	}{
		"true releases without a remote": {value: "true", releases: true},
		"1 does too":                     {value: "1", releases: true},
		"TRUE, however it is typed":      {value: "TRUE", releases: true},
		"false keeps the lock on":        {value: "false"},
		"0 keeps it on":                  {value: "0"},
		"a typo keeps it on":             {value: "ture"},
		"empty keeps it on":              {value: ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := singlePackageRepo(t, markerBuild) // deliberately no remote
			r.Commit("feat(core): first")

			res := r.CommandEnv([]string{"DISPAT_UNSAFE_DISABLE_LOCK=" + tc.value})
			if !tc.releases {
				assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
				assert.Contains(t, res.Stdout, "unable to create the release lock tag")
				return
			}
			require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
			assert.Equal(t, 1, r.TagCount("core@"), "the release ran unguarded, as asked")
			assert.False(t, r.IsTagged(lockTag), "and took no lock at all")
		})
	}
}

// TestReleaseLockConfigSwitch: `unsafeDisableLock: true` says for the
// repository what the variable says for one invocation, so a checkout with no
// remote releases without anyone having to remember an environment variable.
// Neither switch overrides the other: either one is enough, and it takes both
// staying quiet to keep the lock on.
func TestReleaseLockConfigSwitch(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1) // deliberately no remote
	cfg.UnsafeDisableLock = true
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")

	// The variable is not set here, and the harness default that would set it
	// is overridden back to "false": the config alone is doing this.
	res := r.CommandEnv(harness.LockEnabled)
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Equal(t, 1, r.TagCount("core@"), "the release ran unguarded, as the config asked")
	assert.False(t, r.IsTagged(lockTag), "and took no lock at all")

	// Turning it back off in the config brings the lock back, remote or no
	// remote.
	cfg.UnsafeDisableLock = false
	r.WriteConfigModel(cfg)
	r.WriteFile("packages/core/more.txt", "more\n")
	r.Commit("feat(core): more")
	res = r.CommandEnv(harness.LockEnabled)
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "unable to create the release lock tag")
	assert.Equal(t, 1, r.TagCount("core@"), "and the second release never happened")
}

// TestReleaseLockClearedWhateverHappens: the lock is given back on every way
// out of a run — a package that failed, a run refused by a guard after the
// lock was taken, and a run interrupted mid-build. A lock that survived any of
// these would block the repository until somebody deleted it by hand.
func TestReleaseLockClearedWhateverHappens(t *testing.T) {
	t.Run("a failed package", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(libsConfig(failIfMarker, 1))
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first")
		bare := r.AddBareRemote()
		r.WriteFile("packages/core/FAIL", "")

		res := releaseLocked(r)
		require.Equal(t, 1, res.Code, "the build was made to fail")
		assertLockCleared(t, r, bare)
	})

	t.Run("a guard refusing the run", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(markerBuild, 1)
		cfg.Run = &models.RunConfig{AllowBranch: []string{"release/*"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first")
		bare := r.AddBareRemote()

		res := releaseLocked(r)
		require.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stdout, "refusing to release")
		assert.Equal(t, 0, buildRuns(r))
		assertLockCleared(t, r, bare)
	})

	// Both signals the binary handles, because both strand the lock if the
	// release path gets them wrong, and they arrive from different places: a
	// Ctrl-C at a terminal, and a SIGTERM from whatever runs the job. The
	// second is the one that matters most in practice — a cancelled CI job,
	// a `docker stop`, a pod eviction — and it is the case nobody types by
	// hand, so nobody would notice it regressing.
	for name, sig := range map[string]os.Signal{
		"an interrupted run": os.Interrupt,
		"a terminated run":   syscall.SIGTERM,
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			cfg := libsConfig("echo ran >> ../../build.log; sleep 30", 1)
			probeConfig(&cfg)
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first")
			bare := r.AddBareRemote()

			proc := r.StartReleaseEnv(harness.LockEnabled)
			require.Eventually(t, func() bool { return buildRuns(r) > 0 },
				20*time.Second, 20*time.Millisecond, "the build never started")
			proc.Signal(sig)
			res := proc.Wait()

			assert.NotEqual(t, 0, res.Code, "a signalled run does not exit 0")
			// Both halves, or the claim is vacuous: a run that never took the
			// lock would also end with the remote clean.
			assert.True(t, heldDuringRun(t, r), "the run really held the lock when the signal arrived")
			// It is given back under a context detached from cancellation, so
			// the signal that stopped the run cannot also stop the push that
			// releases it.
			assertLockCleared(t, r, bare)
		})
	}
}

// TestReleaseLockStaleLocalTag: a run killed hard leaves the tag in its own
// clone. That local ref says nothing about who holds the lock — the remote
// answers that — so the next release overwrites it and carries on.
func TestReleaseLockStaleLocalTag(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(markerBuild, 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()
	r.Git("tag", "-a", lockTag, "-m", "left behind by a killed run")

	res := releaseLocked(r)
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Equal(t, 1, r.TagCount("core@"))
	assertLockCleared(t, r, bare)
}

// TestReleaseLockCleanupFailureFailsTheCompletedRun: the release is out by the
// time the lock is given back, so its published result stays true. A remote
// that stops accepting writes is reported with E336 and makes the completed
// run fail rather than concealing the lock the next run will meet.
//
// The remote refuses the delete itself rather than being re-pointed: the lock
// is given back at the destination resolved when it was taken, so a mutated
// alias no longer reaches the cleanup at all (see
// `TestFleetLockCleanupUsesRemoteResolvedAtAcquisition`).
func TestReleaseLockCleanupFailureFailsTheCompletedRun(t *testing.T) {
	sink := newWebhookSink(t)
	r := harness.New(t)
	bare := r.AddBareRemote()
	hook := filepath.Join(bare, "hooks", "pre-receive")
	cfg := libsConfig(markerBuild, 1)
	cfg.Webhooks = []models.WebhookConfig{{URL: sink.srv.URL, Events: []string{"release.finished"}}}
	// postAll runs after the task graph and before the lock is given back.
	cfg.Scripts["break"] = models.Script{
		"printf '#!/bin/sh\nexit 1\n' > " + hook,
		"chmod +x " + hook,
	}
	cfg.Run = &models.RunConfig{PostAll: []string{"break"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")

	res := releaseLocked(r)
	assert.NotZero(t, res.Code, "a stranded release lock makes the completed run fail")
	assert.Equal(t, 1, r.TagCount("core@"))
	assert.Contains(t, res.Stdout, `"code":"E336"`)
	assert.Contains(t, res.Stdout, "could not remove the release lock tag from the remote")
	assert.Contains(t, res.Stdout, "delete the tag on the remote",
		"the log says what the next run will run into, and what to do")
	assert.True(t, remoteHoldsLock(t, bare), "the lock really is stranded, as reported")
	finished := sink.find(t, "release.finished")
	assert.Equal(t, "failed", finished["status"])
	assert.Equal(t, float64(1), finished["published"])
	packages := finished["packages"].([]any)
	require.Len(t, packages, 1)
	assert.Equal(t, "published", packages[0].(map[string]any)["status"])
}

// TestReleaseLockAppliesOnlyToRelease: everything else dispat does is
// read-only, per package, or repeatable, and none of it plans a whole
// repository's versions. None of it takes the lock, which is why these all
// still work in a repository with no remote at all.
func TestReleaseLockAppliesOnlyToRelease(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")

	for name, args := range map[string][]string{
		"status":      {"status"},
		"preview":     {"preview"},
		"run":         {"run", "build"},
		"changelog":   {"changelog"},
		"autoversion": {"autoversion"},
		"commit":      {"commit"},
		"scanner":     {"scanner"},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.CommandEnv(harness.LockEnabled, args...)
			assert.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.False(t, r.IsTagged(lockTag), "%s took the release lock", name)
		})
	}
}

// TestReleaseLockTakenEvenWhenNothingToRelease: the lock is unconditional.
//
// Whether there is work to do is not known until after planning, and planning
// is the thing the lock exists to serialise, so "do not lock when the plan is
// empty" is not a rule `dispat release` is in a position to follow. A run
// with nothing to publish therefore takes the lock and gives it back, and its
// beforeAll hook runs while it is held, because the hook runs for every plan
// that was not refused before it. `--require-release` is the refusal that
// comes first: such a run takes the lock, gives it straight back, and exits 3
// without running any hook. The lock-free way to ask the same question is
// `dispat status --require-release`, which is what a CI gate calls, and which
// TestReleaseLockAppliesOnlyToRelease pins.
func TestReleaseLockTakenEvenWhenNothingToRelease(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	probeConfig(&cfg)
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()
	require.Equal(t, 0, releaseLocked(r).Code)
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	built := buildRuns(r)

	// The converged repository plans nothing: the hook runs while the lock is
	// held, and the lock is given back although nothing was built or tagged.
	require.NoError(t, os.Remove(r.Path("lock.probe")))
	res := releaseLocked(r)
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.True(t, heldDuringRun(t, r), "the empty run held the lock while its hook ran")
	assert.Equal(t, built, buildRuns(r), "an empty plan builds nothing")
	assert.Equal(t, 1, r.TagCount("core@"), "and tags nothing")
	assertLockCleared(t, r, bare)

	require.NoError(t, os.Remove(r.Path("lock.probe")))
	res = releaseLocked(r, "--require-release")
	require.Equal(t, 3, res.Code, "stdout:\n%s", res.Stdout)
	assert.NoFileExists(t, r.Path("lock.probe"), "--require-release refuses before beforeAll")
	assert.Equal(t, built, buildRuns(r), "an empty plan still builds nothing")
	assert.Equal(t, 1, r.TagCount("core@"), "and tags nothing")
	assertLockCleared(t, r, bare)

	// The lock was genuinely taken and genuinely returned. With no hook to
	// ask, the proof is the tag object on the remote: a run that took the lock
	// wrote one, and the next holder's lock is a different object.
	held := holdLock(t, r, bare)
	res = releaseLocked(r, "--require-release")
	require.Equal(t, 1, res.Code,
		"a held lock refuses the run before the plan can answer --require-release\nstdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "unable to create the release lock tag")
	assert.NotContains(t, res.Stdout, "nothing to release",
		"the lock is reached first, so its refusal is the one reported")
	assert.Equal(t, held, lockObject(t, bare), "the holder's lock is untouched")
	bareGit(t, bare, "tag", "-d", lockTag)

	// The control: the same flag on a run that does have something to release
	// takes the lock exactly as before.
	r.WriteFile("packages/core/work.txt", "x")
	r.Commit("feat(core): something to release")
	res = releaseLocked(r, "--require-release")
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.True(t, heldDuringRun(t, r), "a releasing run still holds the lock while it works")
	assertLockCleared(t, r, bare)
}

// TestReleaseLockIsNotAReleaseTag: the lock is on the remote *while* the plan
// is computed, so a repository whose tag format is broad enough to match it
// has to ignore it anyway. A version read out of the lock tag would move a
// package's baseline to nothing at all.
func TestReleaseLockIsNotAReleaseTag(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	cfg.TagFormat = "{version}" // the broadest format there is: every tag matches
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()

	res := releaseLocked(r)
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	require.True(t, r.IsTagged("0.1.0"), "tags: %v", r.TagList())

	// The second run plans with the lock tag in view and still reads 0.1.0 as
	// the baseline.
	r.WriteFile("packages/core/more.txt", "more\n")
	r.Commit("feat(core): more")
	res = releaseLocked(r)
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.True(t, r.IsTagged("0.2.0"), "tags: %v", r.TagList())
	assertLockCleared(t, r, bare)
}

// TestReleaseLockReplacedBeforePublishWithholdsThePublication: a release asks
// again, before every publish command, whether it still holds its lock, and
// it asks the remote. A beforePublish hook that replaces the lock, exactly as
// a second machine's release would leave it, makes that question answer no:
// the publication is refused with E336 before its command starts, nothing is
// tagged, and the replacement is somebody else's lock, so this run's cleanup
// leaves it where it is.
func TestReleaseLockReplacedBeforePublishWithholdsThePublication(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	cfg.Scripts["publish"] = models.Script{"echo published > ../../publish.marker"}
	r.SeedPackage("packages", "core")
	r.WriteConfigModel(cfg)
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()
	r.Git("push", "-q", "origin", "HEAD")
	cfg.Scripts["replace"] = models.Script{
		"git -C " + bare + " tag -d " + lockTag,
		"git -C " + bare + " -c user.email=other@dispat.test -c 'user.name=other clone'" +
			" tag -a " + lockTag + " -m 'held by another release' " + harness.DefaultBranch,
	}
	cfg.Spaces["libs"].Flow.BeforePublish = []string{"replace"}
	r.WriteConfigModel(cfg)

	res := releaseLocked(r)

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	refused, isRefused := executionLine(res, "publication not authorized")
	require.True(t, isRefused, "stdout:\n%s", res.Stdout)
	assert.Equal(t, executionLockCode, refused.Code())
	assert.Equal(t, "native-recording-or-lock", refused.Str("category"))
	lost, isLost := executionLine(res, "the release lock was lost, so no new effect may start")
	require.True(t, isLost, "stdout:\n%s", res.Stdout)
	assert.Equal(t, "lost", lost.Str("reason"))
	assert.NoFileExists(t, r.Path("publish.marker"), "the publish command never started")
	assert.Zero(t, r.TagCount("core@"), "nothing was tagged")
	assert.True(t, remoteHoldsLock(t, bare), "the replacement survives this run's cleanup")
	assert.Contains(t, bareGit(t, bare, "cat-file", "tag", lockTag), "held by another release")
}

// TestReleaseLockDeletedBetweenPublicationsWithholdsTheSecond: a lock that
// disappears from the remote while a run is publishing is gone rather than
// replaced, and a run that no longer holds it may start no new effect. The
// provider's publication runs under the lock and its command deletes the tag
// on the remote; the consumer's publication, read back first, finds no lock and
// is refused with E336 before its command starts. What already published
// stays published and recorded, and the run fails.
func TestReleaseLockDeletedBetweenPublicationsWithholdsTheSecond(t *testing.T) {
	r := harness.New(t)
	bare := r.AddBareRemote()
	cfg := libsConfig(markerBuild, 1)
	cfg.Scripts["publish"] = models.Script{
		`echo "$DISPAT_PACKAGE" >> ../../publish.log`,
		`if [ "$DISPAT_PACKAGE" = core ]; then git -C ` + harness.ShQuote(bare) + " tag -d " + lockTag + "; fi",
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.Commit("feat(core,app): first")
	r.Git("push", "-q", "origin", "HEAD")

	res := releaseLocked(r)

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	refused, isRefused := executionLine(res, "publication not authorized")
	require.True(t, isRefused, "stdout:\n%s", res.Stdout)
	assert.Equal(t, executionLockCode, refused.Code())
	assert.Equal(t, "app", refused.Package())
	published, err := os.ReadFile(r.Path("publish.log"))
	require.NoError(t, err)
	assert.Equal(t, "core\n", string(published), "the consumer's publish command never started")
	assert.True(t, r.IsTagged("core@0.1.0"), "the provider stays published and recorded: %v", r.TagList())
	assert.Zero(t, r.TagCount("app@"), "the consumer is not tagged")
	assert.Equal(t, []string{"core"}, executionReleasedPackages(res), "the summary counts the provider published")
	assert.False(t, remoteHoldsLock(t, bare), "nothing put a lock back")
}

// TestReleaseLockVerifyOffSkipsTheLockRead: `commit.verify: false` is the
// setting for a remote that rejects `ls-remote` and accepts pushes, and reading
// the lock back before a publication is another `ls-remote`. With the setting
// on, a remote no read reaches withholds the publication after three reads;
// with it off the lock is never read back, the run says so once at warn level,
// and the release publishes and gives its lock back.
func TestReleaseLockVerifyOffSkipsTheLockRead(t *testing.T) {
	for name, isVerified := range map[string]bool{"verify on": true, "verify off": false} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			cfg := libsConfig(markerBuild, 1)
			cfg.Commit = &models.CommitConfig{Verify: models.Bool(isVerified)}
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first")
			bare := r.AddBareRemote()
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: executionLockReadPattern})

			res := r.CommandEnv(append(fault.Env(), harness.LockEnabled...))

			assertLockCleared(t, r, bare)
			if isVerified {
				require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.Equal(t, 3, fault.Matches(), "the lock is read back, three times, before the publication")
				assert.True(t, harness.IsCodePresent(res.Events, executionLockCode), "stdout:\n%s", res.Stdout)
				assert.Zero(t, r.TagCount("core@"))
				return
			}
			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Zero(t, fault.Matches(), "the lock is never read back")
			assert.Equal(t, 1, executionLineCount(res,
				"the release lock is not read back before each publication: commit.verify is off for this remote, "+
					"so a publication cannot be withheld when another run has taken the lock over"))
			assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
		})
	}
}

// TestReleaseLockRefusesAnAmbiguousPushDestination: the lock is taken on the
// push destination of every repository the run records into, so a remote that
// pushes to two URLs is a lock that would exist in two places and coordinate
// nothing. The run refuses before any package work, rather than locking one
// destination while publishing to another, whether the remote is the one
// repository's own or a source repository's of a fleet, and nothing is tagged
// or committed. Both name the refusal E336, as the lock page's first step
// promises for any lock that cannot be taken.
func TestReleaseLockRefusesAnAmbiguousPushDestination(t *testing.T) {
	for _, row := range []struct {
		name string
		// setup returns the repository the release runs in and the folder,
		// relative to it, of the repository whose remote has two destinations.
		setup func(t *testing.T) (*harness.Repo, string)
	}{
		{
			name: "one repository",
			setup: func(t *testing.T) (*harness.Repo, string) {
				r := newPushingCheckout(t)
				second := r.Path("second-remote.git")
				r.Git("init", "-q", "--bare", second)
				first := r.Git("remote", "get-url", "origin")
				r.Git("remote", "set-url", "--add", "--push", "origin", first)
				r.Git("remote", "set-url", "--add", "--push", "origin", second)
				return r, "."
			},
		},
		{
			name: "a source repository of a fleet",
			setup: func(t *testing.T) (*harness.Repo, string) {
				source := harness.New(t)
				source.SeedPackage("packages", "lib")
				source.Commit("feat(lib): bootstrap library")

				control := harness.New(t)
				addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
				control.AddBareRemote()
				first := filepath.Join(t.TempDir(), "first.git")
				second := filepath.Join(t.TempDir(), "second.git")
				control.Git("init", "-q", "--bare", first)
				control.Git("init", "-q", "--bare", second)
				control.Git("-C", "sources/lib", "remote", "set-url", "origin", first)
				control.Git("-C", "sources/lib", "remote", "set-url", "--push", "--add", "origin", first)
				control.Git("-C", "sources/lib", "remote", "set-url", "--push", "--add", "origin", second)

				cfg := polyrepoModelFile()
				cfg.Spaces = polyrepoModelSpaces(map[string]string{"libs": "sources/lib/packages"})
				cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
					"lib-source": {Commit: &models.CommitConfig{
						Enabled: models.Bool(true), Push: true, Remote: "origin",
						Branch: harness.DefaultBranch, Verify: models.Bool(false),
					}},
				}
				control.WriteConfigModel(cfg)
				control.Commit("chore: configure a source with two push destinations")
				return control, "sources/lib"
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			r, ambiguous := row.setup(t)
			before := r.Git("-C", ambiguous, "rev-parse", "HEAD")

			res := r.CommandEnv(harness.LockEnabled, "release")
			assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.True(t, harness.IsCodePresent(res.Events, "E336"), "stdout:\n%s", res.Stdout)
			assert.Contains(t, res.Stdout+res.Stderr, "has 2 push destinations")
			assert.Equal(t, before, r.Git("-C", ambiguous, "rev-parse", "HEAD"), "nothing was committed")
			assert.Empty(t, polyrepoTags(r, ambiguous), "nothing was tagged")
			assert.Empty(t, r.TagList())
			assert.Zero(t, buildRuns(r), "no package work ran")
		})
	}
}
