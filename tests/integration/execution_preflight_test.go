// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: what a release does before it dispatches anything.
//
// A run that delegates work asks every configured node what it is, once the
// plan is fixed and before the first command of the first package. So the
// claims here are about order as much as about outcome: a node that cannot
// take this run's work fails the release with no stage script having run, no
// tag written, the lock given back and the mailbox left as it was found.

import (
	"encoding/json"
	"os"
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

// TestExecutionPreflightRefusesBeforeAnyStage: every way the pool can fail
// the check, and the same five absences after each of them.
func TestExecutionPreflightRefusesBeforeAnyStage(t *testing.T) {
	for name, tc := range map[string]struct {
		adjust       func(*models.File)
		node         func(*models.ExecutionConfig)
		prepareState func(*testing.T, *executionWorker)
		secret       string
		isAlive      bool
		says         string
	}{
		"no node is listening at all": {
			says: "did not pass preflight"},
		"a node signing with another secret": {
			isAlive: true, secret: "another-secret", says: "did not pass preflight"},
		"a node whose answered-work record cannot be persisted": {
			isAlive: true, says: "did not pass preflight",
			prepareState: func(t *testing.T, worker *executionWorker) {
				require.NoError(t, os.MkdirAll(filepath.Join(worker.stateDir, executionNode, "seen.json.tmp"), 0o755))
			},
		},
		"a node that would move fewer bytes than this run transfers": {
			isAlive: true,
			node: func(settings *models.ExecutionConfig) {
				settings.Transfer = &models.ExecutionTransferConfig{MaxBytes: 1024}
			},
			says: "transfer.maxBytes"},
		"a package no configured node can build": {
			isAlive: true,
			adjust: func(cfg *models.File) {
				space := cfg.Spaces["libs"]
				space.BuildPlatforms = models.PathList{"plan9/mips"}
				cfg.Spaces["libs"] = space
			},
			says: "buildPlatforms"},
	} {
		t.Run(name, func(t *testing.T) {
			adjust := func(cfg *models.File) {
				// A node that is not there must not hold the release open for
				// the default minute.
				cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 5}
				if tc.adjust != nil {
					tc.adjust(cfg)
				}
			}
			rig := newExecutionRig(t, adjust)
			var worker *executionWorker
			if tc.isAlive {
				secret := executionSecret
				if tc.secret != "" {
					secret = tc.secret
				}
				worker = startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox, orNothing(tc.node)), 0,
					executionSecretEnv+"="+secret)
				if tc.prepareState != nil {
					tc.prepareState(t, worker)
				}
			}

			res := rig.release()

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
			assert.Contains(t, diagnosticText(res), tc.says)
			assert.Equal(t, 0, buildRuns(rig.repo), "no stage of any package ran")
			assert.Empty(t, rig.repo.TagList(), "nothing was tagged")
			assert.False(t, remoteHoldsLock(t, rig.origin), "and the lock was given back")
			assert.Empty(t, rig.branches(), "the run closed the coordination branches it created")

			if worker != nil {
				worker.proc.Signal(syscall.SIGINT)
				stopped := worker.proc.Wait()
				require.Equal(t, 0, stopped.Code)
				if tc.prepareState != nil {
					assert.Contains(t, stopped.Stdout+stopped.Stderr, "seen.json.tmp",
						"the worker refused to answer because its replay record could not be saved")
				}
			}

			// The proof that nothing was left half done: the same repository
			// releases cleanly as soon as it stops delegating.
			withoutWorkers(t, rig)
			local := rig.repo.CommandEnv(append(harness.LockEnabled, executionSecretEnv+"="+executionSecret))
			require.Equal(t, 0, local.Code, "stdout:\n%s\nstderr:\n%s", local.Stdout, local.Stderr)
			assert.Equal(t, []string{"core@0.1.0"}, rig.repo.TagList())
		})
	}
}

// orNothing turns an optional adjustment into the list of adjustments a
// configuration takes.
func orNothing(adjust func(*models.ExecutionConfig)) func(*models.ExecutionConfig) {
	if adjust == nil {
		return func(*models.ExecutionConfig) {}
	}
	return adjust
}

// withoutWorkers rewrites the repository's configuration so it delegates
// nothing, which is how a scenario proves a refused run left the repository
// releasable.
func withoutWorkers(t *testing.T, rig *executionRig) {
	t.Helper()
	cfg := libsConfig(markerBuild, 1)
	rig.repo.WriteConfigModel(cfg)
	rig.repo.Commit("chore: release this repository on one machine")
}

// TestExecutionPreflightPassesThenReleasesLocally: a healthy node is reported
// ready, and the release it belongs to completes. What the node is asked to
// do with the stages is another gate's subject; what this proves is that a
// pool that passed preflight costs the run nothing else.
func TestExecutionPreflightPassesThenReleasesLocally(t *testing.T) {
	rig := newExecutionRig(t)
	worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	ready, isReady := executionLine(res, "worker ready")
	require.True(t, isReady, "stdout:\n%s", res.Stdout)
	assert.Equal(t, executionNode, ready.Str("worker"),
		"the orchestrator names the node it is reporting on in worker, never in node")
	assert.NotEmpty(t, ready.Str("os"))
	assert.NotEmpty(t, ready.Str("run"))
	fixed, isFixed := executionLine(res, "plan fixed")
	require.True(t, isFixed)
	assert.Equal(t, ready.Str("run"), fixed.Str("run"),
		"the line naming the plan names the run every assignment carries")
	assert.NotEmpty(t, fixed.Str("planDigest"))

	assert.Equal(t, []string{"core@0.1.0"}, rig.repo.TagList())
	assert.Empty(t, rig.branches(), "a finished run leaves its mailbox as it found it")
	assert.False(t, remoteHoldsLock(t, rig.origin))
	assert.NotContains(t, res.Stdout, executionSecret, "the secret never reaches a log")

	worker.proc.Signal(syscall.SIGINT)
	reply := worker.proc.Wait()
	require.Equal(t, 0, reply.Code, "stdout:\n%s\nstderr:\n%s", reply.Stdout, reply.Stderr)
	claimed, isClaimed := executionLine(reply, "task claimed")
	require.True(t, isClaimed, "the node answered the run's probe\nstdout:\n%s", reply.Stdout)
	assert.Equal(t, "probe", claimed.Str("kind"))
	assert.Equal(t, ready.Str("run"), claimed.Str("run"))
	assert.NotContains(t, reply.Stdout, executionSecret)
}

// TestExecutionPreflightIgnoresUnauthenticReplies: a reply this run cannot
// authenticate is not an answer. It is logged and left where it is, and the
// probe's own deadline is what decides how long that is tolerated, so a party
// with push access to a mailbox cannot talk a run into dispatching.
func TestExecutionPreflightIgnoresUnauthenticReplies(t *testing.T) {
	for name, forge := range map[string]func(*executionFakeOrchestrator, string, map[string]any) map[string]any{
		"a report signed with another secret": func(o *executionFakeOrchestrator, _ string, reply map[string]any) map[string]any {
			o.secret = "another-secret"
			return reply
		},
		"a report bound to another run": func(_ *executionFakeOrchestrator, _ string, reply map[string]any) map[string]any {
			reply["run"] = "0123456789abcdef0123456789abcdef"
			return reply
		},
		"a result carrying no report at all": func(_ *executionFakeOrchestrator, _ string, reply map[string]any) map[string]any {
			delete(reply, "report")
			return reply
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 10}
			})
			node := newExecutionFakeOrchestrator(t, rig.mailbox)
			started := rig.repo.StartReleaseEnv(rig.env(), "release")

			branch := executionAwaitBranch(t, rig, "probe")
			assignment := executionMessage(t, rig.mailbox, branch, "assignment")
			offered := strings.TrimSpace(bareGit(t, rig.mailbox, "rev-parse", "refs/heads/"+branch))
			claim := node.commit(executionMessageOptions{secret: executionSecret, kind: "claim", isSigned: true},
				executionDocument(t, executionClaim(assignment, offered)), offered)
			bareGit(t, rig.mailbox, "update-ref", "refs/heads/"+branch, claim)
			// The document is forged first: a scenario may change the secret
			// the reply is signed with, and that has to be read afterwards.
			document := executionDocument(t, forge(node, branch, executionReply(assignment, offered)))
			result := node.commit(executionMessageOptions{secret: node.secret, kind: "result", isSigned: true},
				document, claim)
			bareGit(t, rig.mailbox, "update-ref", "refs/heads/"+branch, result)

			res := started.Wait()

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
			rejected, isRejected := executionLine(res, "result rejected")
			require.True(t, isRejected, "stdout:\n%s", res.Stdout)
			assert.NotEmpty(t, rejected.Str("reason"))
			assert.Equal(t, 0, buildRuns(rig.repo), "no stage ran")
			assert.Empty(t, rig.repo.TagList())
		})
	}
}

// executionAwaitBranch waits until the mailbox carries one coordination
// branch of this kind and answers its name.
func executionAwaitBranch(t *testing.T, rig *executionRig, kind string) string {
	t.Helper()
	for range 600 {
		for _, ref := range rig.branches() {
			name := strings.TrimPrefix(ref, "refs/heads/")
			if strings.Contains(name, "-"+kind+"-") {
				return name
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no %s branch reached the mailbox", kind)
	return ""
}

// executionClaim is what a node writes when it takes one assignment.
func executionClaim(assignment map[string]any, offered string) map[string]any {
	claim := executionCopy(assignment)
	claim["assignment"] = offered
	return claim
}

// executionReply is the result a node writes back, with the report a healthy
// one carries.
func executionReply(assignment map[string]any, offered string) map[string]any {
	reply := executionCopy(assignment)
	reply["assignment"] = offered
	reply["status"] = "succeeded"
	reply["report"] = map[string]any{
		"protocol": executionProtocolVersion, "dispat": "dev",
		"os": "linux", "arch": "amd64", "capacity": 1,
		"limits": map[string]any{
			"maxFiles": 20000, "maxBytes": 2147483648, "maxManifestBytes": 8388608},
	}
	return reply
}

// executionCopy is one message's header, copied so that a reply is the
// assignment's own identity with this moment on it.
func executionCopy(message map[string]any) map[string]any {
	copied := map[string]any{}
	for key, value := range message {
		copied[key] = value
	}
	return copied
}

// executionDocument is one crafted message as the bytes that are signed.
func executionDocument(t *testing.T, message map[string]any) []byte {
	t.Helper()
	document, err := json.Marshal(message)
	require.NoError(t, err)
	return document
}

// TestExecutionMailboxGitFaults: the orchestrator's own git failing where the
// mailbox is written. Each one fails preflight rather than dispatching
// something it could not describe, and nothing of the release runs.
func TestExecutionMailboxGitFaults(t *testing.T) {
	for name, pattern := range map[string]string{
		"an object write that fails": "*hash-object*",
		"a tree write that fails":    "*mktree*",
		"a commit that fails":        "*commit-tree*",
		"a push that fails":          "*push*dispat-worker-*",
		"a poll that fails":          "*ls-remote*dispat-worker-*",
		"a fetch that fails":         "*fetch*dispat-worker-*",
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 5}
			})
			worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 0)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: pattern, Onward: true, Nth: 1})

			res := rig.release(fault.Env()...)

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
			assert.Positive(t, fault.Matches(), "the fault reached the invocation it names")
			assert.Equal(t, 0, buildRuns(rig.repo), "no stage ran")
			assert.Empty(t, rig.repo.TagList(), "nothing was tagged")

			worker.proc.Signal(syscall.SIGINT)
			require.Equal(t, 0, worker.proc.Wait().Code)
		})
	}
}

// TestExecutionRetainedBranchIsAWarning: a coordination branch this run could
// not close is untidy rather than unsafe, so it is reported with the retained
// code and the release still ends as it would have.
func TestExecutionRetainedBranchIsAWarning(t *testing.T) {
	rig := newExecutionRig(t)
	worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 0)
	// The delete is what fails, and only that: everything before it is the
	// run this scenario is about.
	// Only the delete, whose refspec has an empty source half: every other
	// push of the run names an object before the colon.
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*push* :refs/heads/dispat-worker-*"})

	res := rig.release(fault.Env()...)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionRetainedCode),
		"a branch that could not be closed is a warning\nstdout:\n%s", res.Stdout)
	assert.Equal(t, []string{"core@0.1.0"}, rig.repo.TagList(), "and the release is still a release")

	worker.proc.Signal(syscall.SIGINT)
	require.Equal(t, 0, worker.proc.Wait().Code)
	assert.NotEmpty(t, rig.branches(), "the branch it could not close is still there")
}
