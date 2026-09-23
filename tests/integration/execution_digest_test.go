// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: the plan a distributed run fixes before it dispatches anything.
//
// CCME §28.3 has the orchestrator compute the plan once and name it, and
// §17.2 says what that name must not depend on: placement settings, worker
// availability, run identifiers, branch names and completion order. Both
// claims are made here against the binary, because a digest that quietly
// followed the number of workers would make every node disagree with the run
// that assigned it work.

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionWorkers builds an orchestrator's execution object with the named
// worker links, all reached through mailbox addresses nothing in these
// scenarios ever contacts: fixing the plan happens before any dispatch, and
// `dispat status` never dispatches at all.
func executionWorkers(concurrency int, names ...string) *models.ExecutionConfig {
	workers := make([]models.ExecutionWorkerConfig, 0, len(names))
	for _, name := range names {
		workers = append(workers, models.ExecutionWorkerConfig{
			Name: name, Endpoint: "ssh://git.example.test/srv/" + name + ".git",
		})
	}
	return &models.ExecutionConfig{
		Role:        models.ExecutionRoleOrchestrator,
		Name:        "control",
		Concurrency: models.Int(concurrency),
		SecretEnv:   "DISPAT_IT_EXECUTION_SECRET",
		Workers:     workers,
	}
}

// executionDigestConfig is the fixture's configuration: one space, one build
// command, one declared build output, and whatever execution object the row
// states.
func executionDigestConfig(buildScript string, outputs []string, execution *models.ExecutionConfig) models.File {
	cfg := libsConfig(buildScript, 1)
	space := cfg.Spaces["libs"]
	space.BuildOutputs = outputs
	cfg.Spaces["libs"] = space
	cfg.Execution = execution
	return cfg
}

// planDigestOf reads the one `plan fixed` line a distributed run writes.
func planDigestOf(t *testing.T, res harness.RunResult) string {
	t.Helper()
	for _, event := range res.Events {
		if event.Str("message") == "plan fixed" {
			digest := event.Str("planDigest")
			require.NotEmpty(t, digest, "the fixed plan is named by its digest")
			return digest
		}
	}
	t.Fatalf("no plan was fixed:\n%s", res.Stdout)
	return ""
}

// A plan whose final source identity cannot be read must not be advertised as
// fixed, even though the preceding history walk completed successfully.
func TestExecutionPlanDigestRefusesUnreadableHead(t *testing.T) {
	for _, args := range [][]string{{"status"}, {"release"}, {"run", "build", "--since", "all"}} {
		t.Run(args[0], func(t *testing.T) {
			r := executionDigestRepo(t, executionDigestConfig(echoBuild, models.PathList{"dist"}, executionWorkers(1, "build-a")))
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: "*rev-parse HEAD^{commit}", Code: 128,
			})
			env := append(fault.Env(), "DISPAT_IT_EXECUTION_SECRET="+executionSecret, "DISPAT_UNSAFE_DISABLE_LOCK=")
			failed := r.CommandEnv(env, append(args, "--log-format", "json")...)
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			assert.Equal(t, 1, fault.Matches())
			assert.Contains(t, failed.Stdout+failed.Stderr, "cannot fix the plan for distributed execution")
			assert.Contains(t, failed.Stdout+failed.Stderr, harness.GitFaultMarker)
			for _, event := range failed.Events {
				assert.NotEqual(t, "plan fixed", event.Str("message"), "unreadable source identity cannot name a plan")
				assert.NotEqual(t, "task assigned", event.Str("message"), "no worker may act on an unnamed plan")
			}
			assert.Empty(t, r.TagList())
			assert.False(t, remoteHoldsLock(t, r.Git("remote", "get-url", "origin")), "planning failure releases any acquired lock")
			assert.NotEmpty(t, planDigestOf(t, r.StatusOK("--log-format", "json")), "a healthy read can plan again")
		})
	}
}

// executionDigestRepo is the fixture every digest below is taken of: two
// packages, one consuming the other, with a remote to clone from.
func executionDigestRepo(t *testing.T, cfg models.File) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.WriteConfigModel(cfg)
	r.Commit("feat(core,app): bootstrap")
	r.AddBareRemote()
	r.Git("push", "-q", "origin", harness.DefaultBranch)
	return r
}

// TestExecutionPlanDigestIgnoresPlacementAndTime: one repository at one state
// names one plan, however many workers it is configured with, whatever they
// are called, in whatever order, at whatever capacity, from whichever
// checkout and at whatever time; and a change to what would be released or to
// the command that releases it names a different one.
func TestExecutionPlanDigestIgnoresPlacementAndTime(t *testing.T) {
	outputs := models.PathList{"dist"}
	r := executionDigestRepo(t, executionDigestConfig(echoBuild, outputs, executionWorkers(1, "build-a")))
	first := r.StatusOK("--log-format", "json")
	want := planDigestOf(t, first)
	assert.False(t, harness.IsCodePresent(executionEvents(first), executionRefusalCode),
		"fixing the plan needs no secret in the environment: status dispatches nothing")

	t.Run("more workers, other names, another order and another capacity", func(t *testing.T) {
		r.WriteConfigModel(executionDigestConfig(echoBuild, outputs,
			executionWorkers(4, "zulu", "build-a", "mike")))
		assert.Equal(t, want, planDigestOf(t, r.StatusOK("--log-format", "json")))
	})

	t.Run("a second checkout at another path", func(t *testing.T) {
		// The configuration is restored first: the clone reads what the remote
		// carries, and what it has to agree about is the plan, not the file.
		r.WriteConfigModel(executionDigestConfig(echoBuild, outputs, executionWorkers(1, "build-a")))
		clone := harness.Clone(t, r.Path())
		assert.Equal(t, want, planDigestOf(t, clone.StatusOK("--log-format", "json")))
		assert.NotEqual(t, r.Root, clone.Root, "the two checkouts really are at different paths")
	})

	t.Run("a second later", func(t *testing.T) {
		time.Sleep(time.Second)
		assert.Equal(t, want, planDigestOf(t, r.StatusOK("--log-format", "json")))
	})

	for name, change := range map[string]func(*harness.Repo){
		"a changed build command": func(r *harness.Repo) {
			r.WriteConfigModel(executionDigestConfig("echo building it differently", outputs,
				executionWorkers(1, "build-a")))
		},
		"a changed build output": func(r *harness.Repo) {
			r.WriteConfigModel(executionDigestConfig(echoBuild, models.PathList{"build"},
				executionWorkers(1, "build-a")))
		},
		"a new commit": func(r *harness.Repo) {
			r.WriteFile("packages/core/main.txt", "core changed\n")
			r.Commit("fix(core): one more change")
		},
	} {
		t.Run(name, func(t *testing.T) {
			// A fresh checkout per row, with a baseline of its own: two
			// repositories holding the same files still hold different
			// commits, and the head is part of what the digest names.
			changed := executionDigestRepo(t,
				executionDigestConfig(echoBuild, outputs, executionWorkers(1, "build-a")))
			before := planDigestOf(t, changed.StatusOK("--log-format", "json"))
			change(changed)
			assert.NotEqual(t, before, planDigestOf(t, changed.StatusOK("--log-format", "json")))
		})
	}
}

// TestExecutionPlanIsIndependentOfWorkers: the plan a reader sees is the plan
// they always saw. Configuring worker links adds the one line that names the
// fixed plan and changes nothing else about the diagnostics, the graph or the
// summary.
func TestExecutionPlanIsIndependentOfWorkers(t *testing.T) {
	outputs := models.PathList{"dist"}
	local := executionDigestRepo(t, executionDigestConfig(echoBuild, outputs, nil))
	localShape := executionRunShape(local.StatusOK("--log-format", "json"))
	assert.NotContains(t, strings.Join(localShape, "\n"), "plan fixed",
		"a repository with no workers computes no digest and says nothing about one")

	local.WriteConfigModel(executionDigestConfig(echoBuild, outputs, executionWorkers(2, "build-a", "build-b")))
	distributed := executionRunShape(local.StatusOK("--log-format", "json"))

	withoutFixed := make([]string, 0, len(distributed))
	for _, line := range distributed {
		if strings.HasSuffix(line, "|plan fixed") {
			continue
		}
		withoutFixed = append(withoutFixed, line)
	}
	assert.Equal(t, len(localShape)+1, len(distributed), "exactly one line is added")
	assert.Equal(t, localShape, withoutFixed)
}
