// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: who may start a release, and what a release that delegates work to
// other machines may not be started with.
//
// Every claim here is about something that does not happen. A node that may
// not initiate refuses before it has pushed a lock, a task executing somebody
// else's work refuses before it has written a ref, and a run that could not be
// coordinated refuses before it has reached a mailbox. So the assertions are
// the absences: no lock push, no tag, no commit, no branch on the endpoint,
// beside the one diagnostic that says why.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

const (
	// executionAuthorityEnv is the marker a worker's task runner puts in the
	// environment of every command of an assignment. It is dispat's own
	// internal variable and is spelled out here rather than imported, because
	// this suite drives the compiled binary and knows it only from outside.
	executionAuthorityEnv = "DISPAT_INTERNAL_EXECUTION_AUTHORITY"
	// executionNodeEnv names the node a task runs on, beside the marker.
	executionNodeEnv = "DISPAT_EXECUTION_NODE"
	// executionAuthorityCode is the diagnostic a refusal of authority carries.
	executionAuthorityCode = "E226"
	// The two outcome classes of §28.9 these refusals belong to.
	executionAuthorityCategory     = "execution-authority"
	executionConfigurationCategory = "execution-configuration"
	// executionSecretEnv is the variable the distributed fixtures name as the
	// holder of the signing secret. It is in the suite's own namespace, which
	// is the only one the harness lets through to the binary.
	executionSecretEnv = "DISPAT_IT_EXECUTION_SECRET"
	// lockPushPattern matches the invocation that puts the release lock on the
	// remote. A fault carrying it and failing nothing is how these scenarios
	// count the pushes that did not happen.
	lockPushPattern = "*push*dispat-release-lock*"
)

// executionWorkerMarker is the environment of a command running under worker
// authority: the marker itself and the node it is running on.
var executionWorkerMarker = []string{
	executionAuthorityEnv + "=worker",
	executionNodeEnv + "=build-a",
}

// requireExecutionRefusal asserts that the run reported one refusal carrying
// both names for itself: the numbered code a CI filter switches on, and the
// specification's outcome class beside it.
func requireExecutionRefusal(t *testing.T, res harness.RunResult, code, category string) {
	t.Helper()
	for _, event := range executionEvents(res) {
		if event.Code() != code {
			continue
		}
		assert.Equal(t, category, event.Str("category"),
			"the refusal names the outcome class beside its code")
		return
	}
	t.Fatalf("no %s diagnostic\nstdout:\n%s\nstderr:\n%s", code, res.Stdout, res.Stderr)
}

// executionMailbox is a bare repository standing in for a worker's mailbox.
// Nothing in this file ever reaches it: that it stays empty is the assertion.
func executionMailbox(t *testing.T) string {
	t.Helper()
	bare := t.TempDir()
	bareGit(t, bare, "init", "-q", "--bare", ".")
	return bare
}

// executionMailboxBranches lists the coordination branches a run left in a
// mailbox, which for every scenario here is none.
func executionMailboxBranches(t *testing.T, mailbox string) []string {
	t.Helper()
	out := strings.TrimSpace(bareGit(t, mailbox, "for-each-ref", "--format=%(refname)", "refs/heads/"))
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// executionDistributedConfig is the one-link orchestrator configuration the
// distributed refusals are varied through: one worker node, its mailbox, and
// the variable naming the secret its messages are signed with.
func executionDistributedConfig(mailbox string) *models.ExecutionConfig {
	return &models.ExecutionConfig{
		Role:      models.ExecutionRoleOrchestrator,
		SecretEnv: executionSecretEnv,
		Workers:   []models.ExecutionWorkerConfig{{Name: "build-a", Endpoint: "file://" + mailbox}},
	}
}

// executionReleasableRepo is a repository with one package, a remote to take
// the release lock on, and whatever the scenario adjusted its configuration
// to say.
func executionReleasableRepo(t *testing.T, adjust func(*models.File)) (*harness.Repo, string) {
	t.Helper()
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	cfg := libsConfig(echoBuild, 1)
	adjust(&cfg)
	r.WriteConfigModel(cfg)
	r.Commit("feat(core): bootstrap")
	return r, r.AddBareRemote()
}

// executionSays is the adjustment that states one execution object.
// executionSaysNothing is the node that never mentions execution at all,
// which is what every refusal below is read against.
func executionSays(execution *models.ExecutionConfig) func(*models.File) {
	return func(cfg *models.File) { cfg.Execution = execution }
}

func executionSaysNothing(*models.File) {}

// TestExecutionWorkerRoleRefusesRelease: a node that calls itself a worker
// refuses to start a release before it has taken any ownership at all. The
// lock push is counted rather than faulted: what has to be true is that the
// invocation never happened. The same node still answers `dispat status`,
// because reading the plan is not initiating a release.
func TestExecutionWorkerRoleRefusesRelease(t *testing.T) {
	r, bare := executionReleasableRepo(t, executionSays(&models.ExecutionConfig{
		Role: models.ExecutionRoleWorker, Name: "build-a",
		Endpoint: "file:///srv/mailboxes/build-a.git",
	}))
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: lockPushPattern})

	res := r.CommandEnv(append(fault.Env(), harness.LockEnabled...))

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireExecutionRefusal(t, res, executionAuthorityCode, executionAuthorityCategory)
	assert.Contains(t, diagnosticText(res), "execution.role")
	assert.Zero(t, fault.Matches(), "the refusal comes before the release lock is pushed")
	assert.False(t, remoteHoldsLock(t, bare), "and before anything is on the remote")
	assert.Empty(t, r.TagList(), "a refused release tags nothing")

	status := r.StatusOK()
	assert.Equal(t, "● changed", harness.GraphLine(status.Events, "core").Str("message"),
		"a worker still reads the plan it is asked about")
}

// TestExecutionWorkerAuthorityRefusesNativeRefWrites: a task executing
// somebody else's work may run the helpers a build needs and may not start a
// release or write this release's records, however it is spelled. The
// refusal is the command line's, so it costs nothing and reaches a nested
// invocation from a build script exactly as it reaches a direct one.
func TestExecutionWorkerAuthorityRefusesNativeRefWrites(t *testing.T) {
	for name, args := range map[string][]string{
		"the bare release":       {},
		"the release by name":    {"release"},
		"a release commit":       {"commit", "--tag", "--push"},
		"a GitHub release":       {"github"},
		"a changelog entry":      {"changelog"},
		"a native version write": {"autoversion"},
		"a computed config":      {"compute"},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := executionReleasableRepo(t, executionSaysNothing)
			head := r.Git("rev-parse", "HEAD")

			res := r.CommandEnv(executionWorkerMarker, args...)

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireExecutionRefusal(t, res, executionAuthorityCode, executionAuthorityCategory)
			assert.Empty(t, r.TagList(), "a refused command writes no tag")
			assert.Equal(t, head, r.Git("rev-parse", "HEAD"), "and no commit")
		})
	}

	t.Run("what a build legitimately runs stays allowed", func(t *testing.T) {
		r, _ := executionReleasableRepo(t, executionSaysNothing)
		for name, args := range map[string][]string{
			"the plan":          {"status"},
			"a declared script": {"exec", "build"},
			"a condition":       {"if", "!ABSENT", "--then", "echo building"},
			// A build script reports its own progress, and moving that build
			// to a worker may not change what the receivers hear.
			"a webhook event": {"trigger", "progress", "50"},
		} {
			t.Run(name, func(t *testing.T) {
				res := r.CommandEnv(executionWorkerMarker, args...)
				assert.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.False(t, harness.IsCodePresent(executionEvents(res), executionAuthorityCode))
			})
		}
	})
}

// TestExecutionNestedCommandInheritsWorkerAuthority: the marker travels the
// way every other variable does, so a build script that shells out to dispat
// is refused exactly as the task's own command is. This is the indirect
// initiation the specification names, reached through a real script.
func TestExecutionNestedCommandInheritsWorkerAuthority(t *testing.T) {
	r, _ := executionReleasableRepo(t, executionSaysNothing)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["nested"] = models.Script{r.DispatCommand("release")}
	r.WriteConfigModel(cfg)
	r.Commit("chore: add a script that starts a release")

	res := r.CommandEnv(executionWorkerMarker, "exec", "nested")

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, executionAuthorityCode)
	assert.Empty(t, r.TagList(), "the nested release tagged nothing")
}

// TestExecutionRefusesUnsafeLockBypass: the lock bypass exists for a
// repository with no remote to coordinate through. A run that dispatches work
// to other machines is the opposite case, so the two settings that switch the
// lock off become a refusal instead of a warning, whichever participant states
// one, and nothing is dispatched.
func TestExecutionRefusesUnsafeLockBypass(t *testing.T) {
	t.Run("the environment switch", func(t *testing.T) {
		mailbox := executionMailbox(t)
		r, bare := executionReleasableRepo(t, executionSays(executionDistributedConfig(mailbox)))
		fault := harness.NewGitFault(t, harness.GitFault{Pattern: lockPushPattern})

		// No LockEnabled: the harness switches the lock off for every run, and
		// that is exactly the state a distributed release must refuse.
		res := r.CommandEnv(append(fault.Env(), executionSecretEnv+"=hunter2"))

		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
		assert.Contains(t, diagnosticText(res), "DISPAT_UNSAFE_DISABLE_LOCK")
		assert.Zero(t, fault.Matches(), "the refusal comes before any lock push")
		assert.False(t, remoteHoldsLock(t, bare))
		assert.Empty(t, r.TagList(), "a refused release tags nothing")
		assert.Empty(t, executionMailboxBranches(t, mailbox), "and dispatches nothing")
	})

	t.Run("the configured switch", func(t *testing.T) {
		mailbox := executionMailbox(t)
		r, _ := executionReleasableRepo(t, func(cfg *models.File) {
			cfg.Execution = executionDistributedConfig(mailbox)
			cfg.UnsafeDisableLock = true
		})

		res := r.CommandEnv(append(harness.LockEnabled, executionSecretEnv+"=hunter2"))

		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
		assert.Contains(t, diagnosticText(res), "unsafeDisableLock")
		assert.Empty(t, r.TagList(), "a refused release tags nothing")
		assert.Empty(t, executionMailboxBranches(t, mailbox), "and dispatches nothing")
	})

	t.Run("a peer of a linked fleet", func(t *testing.T) {
		mailbox := executionMailbox(t)
		fleet := newChoreographyFleet(t, "api", "sdk")
		fleet.writeConfig("sdk", func(cfg *models.File) { cfg.UnsafeDisableLock = true })
		fleet.peer("sdk").Commit("chore: release this repository without the lock")
		fleet.push("sdk")
		fleet.writeConfig("api", func(cfg *models.File) {
			cfg.Execution = executionDistributedConfig(mailbox)
		})
		fleet.peer("api").Commit("chore: delegate the builds of this repository")
		fleet.push("api")
		fleet.link("api", "sdk")

		res := fleet.peer("api").CommandEnv(
			append(fileProtocolEnv(), harness.LockEnabled...), "--package", "*")

		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
		assert.Contains(t, diagnosticText(res), "sdk",
			"the refusal names the repository that would release unlocked")
		assert.Empty(t, fleet.peer("api").TagList(), "a refused release tags nothing")
		assert.Empty(t, tagsIn(fleet.peer("api").Repo, ".links/sdk"))
		assert.Empty(t, executionMailboxBranches(t, mailbox), "and dispatches nothing")
	})

	t.Run("with no workers the bypass is the warning it always was", func(t *testing.T) {
		mailbox := executionMailbox(t)
		r, _ := executionReleasableRepo(t, executionSaysNothing)
		// The one shape the typed model cannot express: an explicit empty
		// worker list, which means what an absent one means.
		cfg := libsConfig(echoBuild, 1)
		raw := map[string]any{}
		data, err := json.Marshal(cfg)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &raw))
		raw["execution"] = map[string]any{
			"secretEnv": executionSecretEnv,
			"workers":   []any{},
			"name":      "build-a",
			"endpoint":  "file://" + mailbox,
		}
		r.WriteConfigRaw(raw)
		r.Commit("chore: state an empty worker list")

		res := r.ReleaseOK()

		assert.True(t, harness.IsCodePresent(res.Events, "W331"),
			"the bypass still warns\nstdout:\n%s", res.Stdout)
		assert.False(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode))
		assert.Equal(t, []string{"core@0.1.0"}, r.TagList())
		assert.Empty(t, executionMailboxBranches(t, mailbox))
	})
}

// TestExecutionMissingSecretRefusesDistributedRelease: the configuration
// requires the variable to be named, and the run requires it to hold
// something. The refusal names the variable, never what it holds, which is
// the whole reason the secret is named by a variable rather than written in
// the file.
func TestExecutionMissingSecretRefusesDistributedRelease(t *testing.T) {
	for name, tc := range map[string]struct {
		env  []string
		code int
	}{
		"the variable is unset":  {env: nil, code: 1},
		"the variable is empty":  {env: []string{executionSecretEnv + "="}, code: 1},
		"the variable holds one": {env: []string{executionSecretEnv + "=hunter2"}},
	} {
		t.Run(name, func(t *testing.T) {
			mailbox := executionMailbox(t)
			r, bare := executionReleasableRepo(t, executionSays(executionDistributedConfig(mailbox)))
			if tc.code == 0 {
				// A run that can be coordinated goes on to ask the node it
				// would dispatch to what it is, so the node has to be there.
				defer startWorker(t, r, executionWorkerConfig(mailbox), 0,
					executionSecretEnv+"=hunter2").stop(t)
			}

			res := r.CommandEnv(append(harness.LockEnabled, tc.env...))

			if tc.code == 0 {
				require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.False(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
					"a distributed run that could be coordinated is not refused")
				assert.Equal(t, []string{"core@0.1.0"}, r.TagList())
				assert.False(t, remoteHoldsLock(t, bare), "and gave the lock back")
				return
			}
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
			assert.Contains(t, diagnosticText(res), executionSecretEnv)
			assert.NotContains(t, diagnosticText(res), "hunter2")
			assert.Empty(t, r.TagList(), "a refused release tags nothing")
			assert.Empty(t, executionMailboxBranches(t, mailbox), "and dispatches nothing")
		})
	}
}

// TestExecutionLinkedPeerExecutionKeysAreIgnored: execution settings are
// node-startup settings read from the entry configuration alone. A peer may
// carry its own, because any peer may be another run's entry, and a run
// started elsewhere releases exactly as if it did not. The one debug line
// saying so is what tells a reader that a peer calling itself a worker
// changed nothing.
func TestExecutionLinkedPeerExecutionKeysAreIgnored(t *testing.T) {
	t.Run("an imported source of a control repository", func(t *testing.T) {
		released := func(t *testing.T, execution *models.ExecutionConfig) ([]string, harness.RunResult) {
			t.Helper()
			source := harness.New(t)
			source.SeedPackage("packages", "lib")
			sourceCfg := covPolyrepoFile()
			sourceCfg.Polyrepo = false
			sourceCfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "packages"})
			sourceCfg.Execution = execution
			source.WriteConfigModel(sourceCfg)
			source.Commit("feat(lib): bootstrap library")

			control := harness.New(t)
			addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
			cfg := covPolyrepoFile()
			cfg.Configs = []string{"sources/lib/dispat.json"}
			control.WriteConfigModel(cfg)
			control.Commit("chore: import the source configuration")

			res := control.ReleaseOK("--log-level", "debug")
			return polyrepoTags(control, "sources/lib"), res
		}

		plain, _ := released(t, nil)
		stated, res := released(t, &models.ExecutionConfig{
			Role: models.ExecutionRoleWorker, Name: "build-a",
			Endpoint: "file:///srv/mailboxes/build-a.git",
		})

		assert.Equal(t, []string{"lib@0.1.0"}, plain)
		assert.Equal(t, plain, stated, "a peer's execution settings change nothing about this run")
		assert.True(t, executionIgnoredRepositories(res)["lib-source"],
			"the run says which repository's settings it ignored\nstdout:\n%s", res.Stdout)
	})

	t.Run("a peer of a linked fleet", func(t *testing.T) {
		released := func(t *testing.T, execution *models.ExecutionConfig) ([]string, harness.RunResult) {
			t.Helper()
			fleet := newChoreographyFleet(t, "api", "sdk")
			if execution != nil {
				fleet.writeConfig("sdk", func(cfg *models.File) { cfg.Execution = execution })
				fleet.peer("sdk").Commit("chore: state what this node is")
				fleet.push("sdk")
			}
			fleet.writeConfig("api", func(cfg *models.File) {
				cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
			})
			fleet.peer("api").Commit("chore: declare the cross-repository edge")
			fleet.push("api")
			fleet.link("api", "sdk")

			api := fleet.peer("api")
			res := api.CommandEnv(fileProtocolEnv(), "--package", "*", "--log-level", "debug")
			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			return append(api.TagList(), tagsIn(api.Repo, ".links/sdk")...), res
		}

		plain, _ := released(t, nil)
		stated, res := released(t, &models.ExecutionConfig{Role: models.ExecutionRoleWorker})

		assert.Equal(t, []string{"api-pkg@0.1.0", "sdk-pkg@0.1.0"}, plain)
		assert.Equal(t, plain, stated, "a peer's execution settings change nothing about this run")
		assert.True(t, executionIgnoredRepositories(res)["sdk"],
			"the run says which repository's settings it ignored\nstdout:\n%s", res.Stdout)
	})
}

// executionIgnoredRepositories is the set of repositories a run reported
// ignoring execution settings for.
func executionIgnoredRepositories(res harness.RunResult) map[string]bool {
	ignored := map[string]bool{}
	for _, event := range res.Events {
		if event.Str("message") == "execution settings ignored outside the entry configuration" {
			ignored[event.Str("repository")] = true
		}
	}
	return ignored
}
