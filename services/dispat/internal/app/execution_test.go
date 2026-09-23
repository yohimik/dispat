package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// TestPrePublishChecksTheCompleteLockSet: a publication into owner must be
// withheld when the run lost a different participating repository's lock.
// The run's gate also remembers the loss for every later effect, and a
// coordinator that borrows the gate reads the same decision.
func TestPrePublishChecksTheCompleteLockSet(t *testing.T) {
	checks := 0
	application := &App{log: zerolog.Nop(), runID: "run-1"}
	application.ownership = execution.NewOwnershipGate("run-1", func(context.Context) error {
		checks++
		return errors.New("peer repository lock disappeared")
	}, zerolog.Nop())
	release := &plan.Release{Pkg: &model.Package{Name: "pkg", Repository: "owner"}}

	err := application.checkLockOwnership(t.Context(), release)
	require.ErrorContains(t, err, "peer repository lock disappeared")
	assert.Equal(t, execution.CodeLockLost, err.(interface{ DiagnosticCode() string }).DiagnosticCode())
	assert.Equal(t, execution.CategoryNativeRecordingOrLock, execution.DiagnosticCategory(err))
	assert.Equal(t, 1, checks)

	require.Error(t, application.checkLockOwnership(t.Context(), release))
	assert.Equal(t, 1, checks, "the observed loss is final for this run")
}

// TestPrePublishWithoutALockAsksNothing: a run that holds no lock, a bypassed
// one, has no gate, and a single history that composes nothing and delegates
// nothing gets no callback at all, which is what it always got.
func TestPrePublishWithoutALockAsksNothing(t *testing.T) {
	application := &App{log: zerolog.Nop()}
	release := &plan.Release{Pkg: &model.Package{Name: "pkg"}}

	require.NoError(t, application.checkLockOwnership(t.Context(), release))
	assert.Nil(t, application.resolvePrePublishCheck(&plan.Plan{}, nil, nil))
}

// TestLocalFleetReleaseChecksTheWholeLockSet: a fleet that delegates nothing
// still asks, before each publication, whether it holds every lock it took,
// and it asks the remote of every participating repository rather than only
// the one the package publishes into. Real repositories, real remotes: the
// peer's lock going missing withholds a publication into the other repository
// with E336.
func TestLocalFleetReleaseChecksTheWholeLockSet(t *testing.T) {
	t.Setenv(lockDisableEnv, "")
	require.NoError(t, os.Unsetenv(lockDisableEnv))
	w, _ := recordFixture(t, false, false)
	control := w.byName[config.ControlRepository]
	w.app.cfg.UnsafeDisableLock = false
	control.repo.Config.UnsafeDisableLock = false
	w.byName["source"].repo.Config.UnsafeDisableLock = false
	controlRemote := t.TempDir()
	recordGit(t, controlRemote, "init", "-q", "--bare")
	recordGit(t, control.repo.Root, "remote", "add", "origin", controlRemote)
	unlock, err := w.acquire(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = unlock() })
	require.Len(t, w.held, 2, "both repositories are locked")

	w.app.openOwnershipGate(w)
	rel := &plan.Release{Pkg: &model.Package{Name: "lib", Repository: "source"}}
	require.NoError(t, w.app.checkLockOwnership(t.Context(), rel), "every lock is still held")

	recordGit(t, controlRemote, "tag", "-d", release.LockTagName)
	err = w.app.checkLockOwnership(t.Context(), rel)
	require.Error(t, err, "a publication into source is withheld for the control repository's lock")
	assert.Equal(t, execution.CodeLockLost, config.DiagnosticCode(err))
	assert.ErrorIs(t, err, release.ErrLockLost)
}

// TestOwnershipGateSkipsARemoteWithVerifyOff: commit.verify switches off every
// ls-remote a release makes of its remote, and reading the lock back is one.
// A single history with the setting off opens no gate and says so once, at
// warn level, because a run that forgoes the read must not read as though its
// ownership had been checked.
func TestOwnershipGateSkipsARemoteWithVerifyOff(t *testing.T) {
	t.Setenv(lockDisableEnv, "")
	require.NoError(t, os.Unsetenv(lockDisableEnv))
	root, a := guardRepo(t, &config.File{Run: &config.RunConfig{},
		Commit: &config.CommitConfig{Verify: public.Bool(false)}})
	origin := t.TempDir()
	recordGit(t, origin, "init", "-q", "--bare")
	recordGit(t, root, "remote", "add", "origin", origin)
	var logs bytes.Buffer
	a.log = zerolog.New(&logs)
	_, unlock, err := a.acquireReleaseLocks(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = unlock() })

	a.openOwnershipGate(nil)

	assert.Nil(t, a.ownership, "no lock is read back, so there is no gate")
	assert.Equal(t, 1, strings.Count(logs.String(), "the release lock is not read back before each publication"))
	assert.Contains(t, logs.String(), `"level":"warn"`)
}

// What a release refuses before it takes a lock is decided here; what the
// refusal costs a real run is in tests/integration/execution_authority_test.go.

// executionSecretEnv is the variable the distributed rows name. It is the
// suite's own namespace so that nothing outside these tests is read.
const executionSecretEnv = "DISPAT_IT_EXECUTION_SECRET"

// executionEntry builds a node with the given execution settings and a logger
// to read its refusals out of.
func executionEntry(t *testing.T, cfg *config.File) (*App, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	return New(t.TempDir(), cfg, zerolog.New(&logs).Level(zerolog.DebugLevel)), &logs
}

// executionWorkers is the one-link configuration the distributed rules are
// varied through.
func executionWorkers() *public.ExecutionConfig {
	return &public.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers:   []public.ExecutionWorkerConfig{{Name: "build-a", Endpoint: "/srv/mailboxes/a.git"}},
	}
}

// TestCheckExecutionEntryRefusals: every state a release may not be started
// in, and every state it may. The environment is set per row because two of
// the four rules read it, and a node that says nothing about execution is the
// row every other one is measured against.
func TestCheckExecutionEntryRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		execution   *public.ExecutionConfig
		bypass      bool
		isVerifyOff bool
		authority   string
		secret      string
		noSecret    bool
		code        string
		category    string
		want        string
	}{
		"no execution settings at all": {},
		"an orchestrator with no workers": {
			execution: &public.ExecutionConfig{Role: public.ExecutionRoleOrchestrator},
		},
		"a node with no workers releasing without the lock": {
			execution: &public.ExecutionConfig{Role: public.ExecutionRoleOrchestrator},
			bypass:    true,
		},
		"a task running under worker authority": {
			authority: "worker",
			code:      execution.CodeAuthority, category: execution.CategoryAuthority,
			want: "worker authority",
		},
		"a task under worker authority on a node that delegates": {
			execution: executionWorkers(),
			authority: "worker",
			code:      execution.CodeAuthority, category: execution.CategoryAuthority,
			want: "worker authority",
		},
		"a worker node": {
			execution: &public.ExecutionConfig{Role: public.ExecutionRoleWorker},
			code:      execution.CodeAuthority, category: execution.CategoryAuthority,
			want: "execution.role is \"worker\"",
		},
		"a worker node whose authority says nothing": {
			execution: &public.ExecutionConfig{Role: public.ExecutionRoleWorker},
			authority: "orchestrator",
			code:      execution.CodeAuthority, category: execution.CategoryAuthority,
			want: "execution.role is \"worker\"",
		},
		"workers and the configured bypass": {
			execution: executionWorkers(),
			bypass:    true,
			code:      execution.CodeConfiguration, category: execution.CategoryConfiguration,
			want: "unsafeDisableLock",
		},
		"workers and an unset secret": {
			execution: executionWorkers(),
			noSecret:  true,
			code:      execution.CodeConfiguration, category: execution.CategoryConfiguration,
			want: executionSecretEnv,
		},
		"workers and an empty secret": {
			execution: executionWorkers(),
			secret:    "",
			code:      execution.CodeConfiguration, category: execution.CategoryConfiguration,
			want: executionSecretEnv,
		},
		"workers, the lock and the secret": {
			execution: executionWorkers(),
			secret:    "hunter2",
		},
		"workers and a remote whose lock is never read back": {
			execution: executionWorkers(), isVerifyOff: true,
			secret: "hunter2",
			code:   execution.CodeConfiguration, category: execution.CategoryConfiguration,
			want: "commit.verify is off",
		},
		"no workers and a remote whose lock is never read back": {
			isVerifyOff: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(execution.AuthorityEnv, tc.authority)
			if tc.authority == "" {
				require.NoError(t, os.Unsetenv(execution.AuthorityEnv))
			}
			t.Setenv(executionSecretEnv, tc.secret)
			if tc.noSecret {
				require.NoError(t, os.Unsetenv(executionSecretEnv))
			}
			// The bypass rows state it in the configuration, so the
			// invocation's own switch is taken out of every row: what a test
			// of the lock refusal must not depend on is the environment it
			// happened to be started in.
			t.Setenv(lockDisableEnv, "")
			require.NoError(t, os.Unsetenv(lockDisableEnv))
			cfg := &config.File{Execution: tc.execution, UnsafeDisableLock: tc.bypass}
			if tc.isVerifyOff {
				cfg.Commit = &config.CommitConfig{Verify: public.Bool(false)}
			}
			a, logs := executionEntry(t, cfg)

			err := a.checkExecutionEntry(t.Context(), runRelease)

			if tc.code == "" {
				require.NoError(t, err)
				assert.Empty(t, logs.String(), "a release nothing refuses says nothing about execution")
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Equal(t, tc.code, config.DiagnosticCode(err))
			assert.Equal(t, tc.category, execution.DiagnosticCategory(err))
			assert.Contains(t, logs.String(), `"code":"`+tc.code+`"`)
			assert.Contains(t, logs.String(), `"category":"`+tc.category+`"`)
		})
	}
}

// TestCheckExecutionEntryNamesTheBypassedRepositories: the refusal replaces
// the warning a bypassed release would have printed, so it has to carry what
// that warning carried: which repositories release unlocked and which setting
// asked for it.
func TestCheckExecutionEntryNamesTheBypassedRepositories(t *testing.T) {
	t.Setenv(execution.AuthorityEnv, "")
	require.NoError(t, os.Unsetenv(execution.AuthorityEnv))
	t.Setenv(executionSecretEnv, "hunter2")
	t.Setenv(lockDisableEnv, "true")
	entry := &config.File{Execution: executionWorkers()}
	a, logs := executionEntry(t, entry)
	a.workspace = &config.Workspace{Repositories: []config.Repository{
		{Name: "sdk", Config: entry, Entry: true},
		{Name: "api", Config: &config.File{UnsafeDisableLock: true}},
		{Name: "web", Config: &config.File{}},
	}}

	err := a.checkExecutionEntry(t.Context(), runRelease)

	require.Error(t, err)
	assert.Equal(t, execution.CodeConfiguration, config.DiagnosticCode(err))
	// Every repository is bypassed here, because the environment switch is the
	// invocation's and reaches all of them; the configured one is named as the
	// second setting because api states it for itself.
	assert.Contains(t, logs.String(), `"repositories":["api","sdk","web"]`)
	assert.Contains(t, logs.String(), `"setting":["unsafeDisableLock","`+lockDisableEnv+`"]`)
}

// TestCheckExecutionEntryReportsIgnoredPeerSettings: a peer may carry its own
// execution object, because any peer may be another run's entry. It is never
// consulted, and the one line saying so is what tells a reader that a peer
// calling itself a worker changed nothing here. The entry states its own
// settings by definition, and a source that declares no configuration of its
// own is recorded against the entry's file, so neither is reported.
func TestCheckExecutionEntryReportsIgnoredPeerSettings(t *testing.T) {
	t.Setenv(execution.AuthorityEnv, "")
	require.NoError(t, os.Unsetenv(execution.AuthorityEnv))
	entry := &config.File{Execution: &public.ExecutionConfig{Name: "control"}}
	a, logs := executionEntry(t, entry)
	a.workspace = &config.Workspace{Repositories: []config.Repository{
		{Name: "control", Config: entry, Entry: true},
		{Name: "sdk", Config: &config.File{Execution: &public.ExecutionConfig{Role: public.ExecutionRoleWorker}}},
		{Name: "ui", Config: entry},
		{Name: "web", Config: &config.File{}},
		{Name: "docs"},
	}}

	require.NoError(t, a.checkExecutionEntry(t.Context(), runRelease))

	assert.Contains(t, logs.String(),
		`"repository":"sdk","message":"execution settings ignored outside the entry configuration"`)
	for _, quiet := range []string{"control", "ui", "web", "docs"} {
		assert.NotContains(t, logs.String(), `"repository":"`+quiet+`"`,
			"%s states no execution settings of its own", quiet)
	}
}

// TestCheckExecutionEntryRefusesASweepByTheSameRules: a sweep with worker
// links is held to the rules a release is (§28.10), and each refusal says it
// was a sweep that was refused. The bypass is refused although a sweep takes
// no lock, because it states that the repository has no remote to coordinate
// through, and a sweep that dispatches has one.
func TestCheckExecutionEntryRefusesASweepByTheSameRules(t *testing.T) {
	for name, tc := range map[string]struct {
		execution *public.ExecutionConfig
		bypass    bool
		authority string
		want      string
		code      string
	}{
		"a task under worker authority": {
			execution: executionWorkers(), authority: "worker",
			want: "cannot start a sweep", code: execution.CodeAuthority},
		"a worker node": {
			execution: &public.ExecutionConfig{Role: public.ExecutionRoleWorker},
			want:      "a worker cannot start a sweep", code: execution.CodeAuthority},
		"workers and the configured bypass": {
			execution: executionWorkers(), bypass: true,
			want: "none to dispatch a sweep through", code: execution.CodeConfiguration},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(execution.AuthorityEnv, tc.authority)
			if tc.authority == "" {
				require.NoError(t, os.Unsetenv(execution.AuthorityEnv))
			}
			t.Setenv(executionSecretEnv, "hunter2")
			t.Setenv(lockDisableEnv, "")
			require.NoError(t, os.Unsetenv(lockDisableEnv))
			a, logs := executionEntry(t, &config.File{Execution: tc.execution, UnsafeDisableLock: tc.bypass})

			err := a.checkExecutionEntry(t.Context(), runSweep)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Equal(t, tc.code, config.DiagnosticCode(err))
			assert.Contains(t, logs.String(), `"message":"cannot start sweep"`)
		})
	}
}

// TestCheckExecutionEntryLetsASweepSkipTheLockRead: a sweep takes no release
// lock and reads none back, so a remote with commit.verify off refuses a
// distributed release and leaves a distributed sweep alone.
func TestCheckExecutionEntryLetsASweepSkipTheLockRead(t *testing.T) {
	t.Setenv(execution.AuthorityEnv, "")
	require.NoError(t, os.Unsetenv(execution.AuthorityEnv))
	t.Setenv(executionSecretEnv, "hunter2")
	t.Setenv(lockDisableEnv, "")
	require.NoError(t, os.Unsetenv(lockDisableEnv))
	a, _ := executionEntry(t, &config.File{Execution: executionWorkers(),
		Commit: &config.CommitConfig{Verify: public.Bool(false)}})

	require.NoError(t, a.checkExecutionEntry(t.Context(), runSweep))
	require.Error(t, a.checkExecutionEntry(t.Context(), runRelease))
}

// linkedEntry is a single history with one origin and a configuration naming
// the given links, ready to start a distributed run, with its log captured.
func linkedEntry(t *testing.T, origin string, workers ...public.ExecutionWorkerConfig) (*App, *bytes.Buffer) {
	t.Helper()
	t.Setenv(execution.AuthorityEnv, "")
	require.NoError(t, os.Unsetenv(execution.AuthorityEnv))
	t.Setenv(executionSecretEnv, "hunter2")
	t.Setenv(lockDisableEnv, "")
	require.NoError(t, os.Unsetenv(lockDisableEnv))
	root, a := guardRepo(t, &config.File{Run: &config.RunConfig{},
		Execution: &public.ExecutionConfig{SecretEnv: executionSecretEnv, Workers: workers}})
	if origin != "" {
		recordGit(t, root, "remote", "add", "origin", origin)
	}
	var logs bytes.Buffer
	a.log = zerolog.New(&logs).Level(zerolog.DebugLevel)
	return a, &logs
}

// TestWorkerLinksReachTheReleaseRemoteByDefault: a link that states no
// endpoint reaches the repository being released, at the push URL of the
// remote the release takes its lock on, and says so in one debug line per
// such link; a link that states one keeps it.
func TestWorkerLinksReachTheReleaseRemoteByDefault(t *testing.T) {
	origin := t.TempDir()
	recordGit(t, origin, "init", "-q", "--bare")
	a, logs := linkedEntry(t, origin,
		public.ExecutionWorkerConfig{Name: "build-a"},
		public.ExecutionWorkerConfig{Name: "build-b", Endpoint: "/srv/mailboxes/b.git"})

	require.NoError(t, a.checkExecutionEntry(t.Context(), runRelease))

	assert.Equal(t, coordinationRemote{name: "origin", url: origin}, a.coordination)
	links, err := a.formatWorkerLinks(a.coordination)
	require.NoError(t, err)
	assert.Equal(t, []execution.Link{
		{Name: "build-a", Endpoint: origin},
		{Name: "build-b", Endpoint: "/srv/mailboxes/b.git"},
	}, links)
	assert.Equal(t, 1, strings.Count(logs.String(), "worker link reaches the release remote"),
		"one line per link that states no endpoint")
	assert.Contains(t, logs.String(), `"worker":"build-a","remote":"origin"`)
}

// TestWorkerLinksFollowTheConfiguredRemote: commit.remote names the remote a
// release pushes to and locks on, so it is the remote a link with no endpoint
// reaches too.
func TestWorkerLinksFollowTheConfiguredRemote(t *testing.T) {
	upstream := t.TempDir()
	recordGit(t, upstream, "init", "-q", "--bare")
	a, _ := linkedEntry(t, "", public.ExecutionWorkerConfig{Name: "build-a"})
	recordGit(t, a.root, "remote", "add", "upstream", upstream)
	a.cfg.Commit = &config.CommitConfig{Remote: "upstream"}

	require.NoError(t, a.checkExecutionEntry(t.Context(), runSweep))
	assert.Equal(t, coordinationRemote{name: "upstream", url: upstream}, a.coordination)
}

// TestWorkerLinksInAComposedWorkspaceReachTheEntryRemote: in a composed
// workspace the release remote a link reaches is the entry repository's, which
// is where the node reads every peer's history from.
func TestWorkerLinksInAComposedWorkspaceReachTheEntryRemote(t *testing.T) {
	w, _ := recordFixture(t, false, false)
	controlRemote := t.TempDir()
	recordGit(t, controlRemote, "init", "-q", "--bare")
	control := w.byName[config.ControlRepository]
	recordGit(t, control.repo.Root, "remote", "add", "origin", controlRemote)
	for i := range w.app.workspace.Repositories {
		repository := &w.app.workspace.Repositories[i]
		repository.Entry = repository.Name == config.ControlRepository
	}

	name, url, err := w.app.resolveCoordinationRemote(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "origin", name)
	assert.Equal(t, controlRemote, url, "the entry repository's remote, not the source's")
}

// TestWorkerLinksRefuseARemoteNoMailboxCouldBe: the push URL a link with no
// endpoint reaches is held to the rules of an endpoint before any lock. A URL
// carrying a token is refused with E225, naming the link and the remote, and
// the token is never written; a relative path is refused for its shape.
func TestWorkerLinksRefuseARemoteNoMailboxCouldBe(t *testing.T) {
	for name, tc := range map[string]struct {
		pushURL string
		want    string
	}{
		"a push URL carrying a token": {
			pushURL: "https://x-access-token:ghs_itFAKE@example.invalid/acme/project.git",
			want:    "carries credentials"},
		"a relative push URL": {
			pushURL: "../origin.git",
			want:    "cannot be a mailbox"},
	} {
		t.Run(name, func(t *testing.T) {
			a, logs := linkedEntry(t, tc.pushURL, public.ExecutionWorkerConfig{Name: "build-a"})

			err := a.checkExecutionEntry(t.Context(), runRelease)

			require.Error(t, err)
			assert.Equal(t, execution.CodeConfiguration, config.DiagnosticCode(err))
			assert.Equal(t, execution.CategoryConfiguration, execution.DiagnosticCategory(err))
			assert.Contains(t, err.Error(), "worker link build-a states no endpoint")
			assert.Contains(t, err.Error(), "the release remote origin")
			assert.Contains(t, err.Error(), tc.want)
			assert.NotContains(t, err.Error()+logs.String(), "ghs_itFAKE", "the token is never written")
			assert.Contains(t, logs.String(), `"code":"E225"`)
		})
	}
}

// TestWorkerLinksWithEndpointsResolveNothing: a run whose links all state an
// endpoint asks no remote anything, so a repository with no remote at all
// still starts it.
func TestWorkerLinksWithEndpointsResolveNothing(t *testing.T) {
	a, logs := linkedEntry(t, "", public.ExecutionWorkerConfig{Name: "build-a", Endpoint: "/srv/mailboxes/a.git"})

	require.NoError(t, a.checkExecutionEntry(t.Context(), runRelease))
	assert.Equal(t, coordinationRemote{}, a.coordination)
	assert.NotContains(t, logs.String(), "worker link reaches the release remote")
}

// TestWorkerLinksReachTheDestinationTheLockWasTakenOn: a release's coordinator
// reaches the release remote at the destination its own lock was pushed to,
// the single history's or the entry repository's, and holds that URL to the
// rules of an endpoint once more.
func TestWorkerLinksReachTheDestinationTheLockWasTakenOn(t *testing.T) {
	single := &App{releaseLock: &release.Lock{Remote: "/srv/locked.git"}}
	assert.Equal(t, "/srv/locked.git", single.resolveLockedEndpoint(nil))
	assert.Empty(t, (&App{}).resolveLockedEndpoint(nil), "a run that holds no lock has no destination")

	entry := &repositoryRecord{repo: &config.Repository{Name: config.ControlRepository, Entry: true}}
	peer := &repositoryRecord{repo: &config.Repository{Name: "sdk"}}
	fleet := &workspaceRecorder{held: []heldLock{
		{repository: peer, lock: &release.Lock{Remote: "/srv/sdk.git"}},
		{repository: entry, lock: &release.Lock{Remote: "/srv/control.git"}},
	}}
	assert.Equal(t, "/srv/control.git", single.resolveLockedEndpoint(fleet))

	a, _ := linkedEntry(t, "", public.ExecutionWorkerConfig{Name: "build-a"})
	coordinator, err := a.newCoordinator("generation", nil, coordinationRemote{name: "origin", url: "/srv/locked.git"})
	require.NoError(t, err)
	assert.Equal(t, []execution.Link{{Name: "build-a", Endpoint: "/srv/locked.git"}}, coordinator.Links)
	_, err = a.newCoordinator("generation", nil,
		coordinationRemote{name: "origin", url: "https://token@example.invalid/acme/project.git"})
	require.Error(t, err, "the destination is held to the rules again")
	assert.Equal(t, execution.CodeConfiguration, config.DiagnosticCode(err))
}
