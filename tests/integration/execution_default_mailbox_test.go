// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: the repository being released is the mailbox.
//
// A worker link that states no endpoint reaches the remote the release takes
// its lock on, at the push URL git resolves for it, and the worker polls that
// same repository. So the whole arrangement needs one remote: the one the
// repository already has. These scenarios run a release and a sweep through
// it, and refuse the one push URL no mailbox may be, one that carries a
// credential, before anything is locked or run.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionLockMessageEnv names the file a hook copies the remote lock tag
// into while the run holds it.
const executionLockMessageEnv = "DISPAT_IT_LOCK_MESSAGE"

// TestExecutionReleaseUsesItsOwnRemoteAsTheMailbox: a link with no endpoint
// reaches the repository's own remote. The build is placed on the worker,
// which polls that remote; the release tag names the planned head and its
// history holds no transport commit; the lock taken on that remote names the
// run the plan was fixed for; one debug line says where the link reaches; and
// afterwards the remote holds neither a coordination branch nor the lock.
func TestExecutionReleaseUsesItsOwnRemoteAsTheMailbox(t *testing.T) {
	rig := newExecutionRigOnOrigin(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.Scripts["read-lock"] = models.Script{`git --git-dir="$` + executionOriginEnv + `" cat-file tag ` +
			lockTag + ` > "$` + executionLockMessageEnv + `"`}
		cfg.Run = &models.RunConfig{BeforeAll: []string{"read-lock"}}
	})
	plannedHead := rig.repo.Git("rev-parse", "HEAD")
	lockMessage := filepath.Join(t.TempDir(), "lock.message")
	worker := rig.startWorker(executionWorkerConfig(rig.origin), 0)

	res := rig.release(executionOriginEnv+"="+rig.origin, executionLockMessageEnv+"="+lockMessage)
	stopAll(t, []*executionWorker{worker})

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, executionNode, rig.nodesByPackage()["core"], "the build ran on the worker: %v", rig.runs())
	require.True(t, rig.repo.IsTagged("core@0.1.0"), "tags: %v", rig.repo.TagList())
	assert.Equal(t, plannedHead, rig.repo.Git("rev-list", "-1", "core@0.1.0"),
		"the release tag names the planned head")
	assert.NotContains(t, rig.repo.Git("log", "--format=%s", "core@0.1.0"), "dispat transport",
		"no transport commit is in the release tag's history")
	assert.Empty(t, executionCoordinationBranches(t, rig.origin), "the remote keeps no coordination branch")
	assert.False(t, remoteHoldsLock(t, rig.origin), "and no lock")

	fixed, isFixed := executionLine(res, "plan fixed")
	require.True(t, isFixed, "stdout:\n%s", res.Stdout)
	message, err := os.ReadFile(lockMessage)
	require.NoError(t, err, "the beforeAll hook read the lock")
	assert.Contains(t, string(message), "\nrun "+fixed.Str("run")+"\n", "the lock names the run the plan was fixed for")

	reached, isReached := executionLine(res, "worker link reaches the release remote")
	require.True(t, isReached, "stdout:\n%s", res.Stdout)
	assert.Equal(t, executionNode, reached.Str("worker"))
	assert.Equal(t, "origin", reached.Str("remote"))
	assert.Equal(t, rig.origin, reached.Str("endpoint"), "a path carries nothing to redact")
}

// TestExecutionSweepUsesItsOwnRemoteAsTheMailbox: a sweep reaches the same
// remote and takes no lock there. A lock another release holds on that remote
// neither stops the sweep nor is touched by it; the task runs on the worker,
// its declared output comes back and is merged here, and no coordination
// branch is left behind.
func TestExecutionSweepUsesItsOwnRemoteAsTheMailbox(t *testing.T) {
	rig := newExecutionRigOnOrigin(t, func(cfg *models.File) {
		cfg.Scripts["tests"] = models.Script{executionCoverageScript}
		cfg.RunOutputs = map[string][]string{"tests": {"coverage"}}
		executionSweepPinnedToWorkers(cfg)
	})
	rig.repo.WriteFile(".gitignore", "/coverage/\n")
	rig.repo.Commit("chore: ignore the coverage folder")
	head := strings.TrimSpace(rig.repo.Git("rev-parse", "HEAD"))
	rig.repo.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	bareGit(t, rig.origin, "tag", "-a", lockTag, "-m", "a release is running", head)
	held := lockObject(t, rig.origin)
	worker := rig.startWorker(executionWorkerConfig(rig.origin), 0)

	res := rig.sweep(nil)
	stopAll(t, []*executionWorker{worker})

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, executionNode, rig.nodesByPackage()["core"], "the task ran on the worker: %v", rig.runs())
	assert.Equal(t, "core from "+executionNode+"\n", readRepoFile(t, rig.repo, "coverage/core.out"),
		"the task's run output was merged into this checkout")
	assert.Equal(t, held, lockObject(t, rig.origin), "the other release's lock is the one that was there")
	assert.Empty(t, executionCoordinationBranches(t, rig.origin), "no coordination branch is left behind")
}

// TestExecutionDefaultMailboxRefusesACredentialCarryingRemote: a push URL that
// carries a token cannot be where a link with no endpoint reaches, because an
// endpoint never carries a secret. A release and a sweep naming the node alone
// are both refused with E225 before any lock or task, naming the link and the
// remote and never the token. Naming the same repository by a clean endpoint
// runs, and a link with an empty endpoint is a usage error.
func TestExecutionDefaultMailboxRefusesACredentialCarryingRemote(t *testing.T) {
	const pushURL = "https://x-access-token:ghs_itFAKE@example.invalid/acme/project.git"
	refused := func(t *testing.T, rig *executionRig, res harness.RunResult) {
		t.Helper()
		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		requireExecutionRefusal(t, res, "E225", "execution-configuration")
		assert.Contains(t, diagnosticText(res), "worker link "+executionNode+" states no endpoint")
		assert.Contains(t, diagnosticText(res), "the release remote origin")
		assert.NotContains(t, res.Stdout+res.Stderr, "ghs_itFAKE", "the token is never written")
		_, isLocked := executionLine(res, "release lock acquired")
		assert.False(t, isLocked, "the refusal comes before any lock")
		assert.False(t, remoteHoldsLock(t, rig.origin))
		assert.Empty(t, rig.runs(), "no task ran anywhere")
	}

	t.Run("a release with a configured link", func(t *testing.T) {
		rig := newExecutionRigOnOrigin(t, func(cfg *models.File) {
			cfg.Scripts["build"] = models.Script{executionRecordingScript}
		})
		rig.repo.Git("remote", "set-url", "--push", "origin", pushURL)

		refused(t, rig, rig.release())
	})

	t.Run("a sweep naming the node on the command line", func(t *testing.T) {
		rig := newExecutionRigOnOrigin(t, func(cfg *models.File) {
			cfg.Scripts["tests"] = models.Script{executionSweepScript}
			cfg.Execution.Workers = nil
		})
		rig.repo.Git("remote", "set-url", "--push", "origin", pushURL)

		refused(t, rig, rig.sweep(nil, "--worker", executionNode))

		worker := rig.startWorker(executionWorkerConfig(rig.origin), 0)
		res := rig.sweep(nil, "--worker", executionNode+"=file://"+rig.origin)
		stopAll(t, []*executionWorker{worker})
		require.Equal(t, 0, res.Code, "a clean endpoint to the same repository runs\nstdout:\n%s\nstderr:\n%s",
			res.Stdout, res.Stderr)
		assert.Equal(t, executionNode, rig.nodesByPackage()["core"], "%v", rig.runs())

		res = rig.sweep(nil, "--worker", executionNode+"=")
		assert.Equal(t, 2, res.Code, "an empty endpoint is a usage error\nstderr:\n%s", res.Stderr)
	})
}

// executionCoordinationBranches are the coordination branches a remote holds,
// without the release branches it holds beside them when it is the
// repository's own remote.
func executionCoordinationBranches(t *testing.T, remote string) []string {
	t.Helper()
	var branches []string
	for _, branch := range executionMailboxBranches(t, remote) {
		if strings.HasPrefix(branch, "refs/heads/dispat-worker-") {
			branches = append(branches, branch)
		}
	}
	return branches
}
