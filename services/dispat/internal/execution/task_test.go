// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// What a node makes of a frame it was given, in the parts that are decisions
// rather than subprocesses: the environment one sequence runs under, where a
// checkout puts each repository, and what an attempt's folder is called.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// TestTaskEnvironmentResolvesStaticPairsOnTheNode: what a static pair means is
// decided here, from this node's environment, which is why it travelled
// unresolved; and nothing it says can displace a computed variable, this
// node's authority or the stage it is running.
func TestTaskEnvironmentResolvesStaticPairsOnTheNode(t *testing.T) {
	t.Setenv("NPM_TOKEN", "the-secret")
	worker := &Worker{Node: "build-a"}
	assignment := Assignment{
		Header:    Header{Kind: KindBuild},
		Env:       []string{"DISPAT_PACKAGE=core", "DISPAT_VERSION=1.2.3"},
		StaticEnv: []string{"REGISTRY=https://registry.test", "TOKEN=$NPM_TOKEN", "DISPAT_PACKAGE=forged"},
	}
	carried := &plan.Release{Outputs: []plan.Output{{Name: "IMAGE", Value: "acme/core:1", Source: "core:build"}}}

	env := worker.formatTaskEnv(assignment, "beforeBuild", carried)

	assert.Equal(t, "the-secret", envValueOf(env, "TOKEN"), "a static reference is expanded here")
	assert.Equal(t, "https://registry.test", envValueOf(env, "REGISTRY"))
	assert.Equal(t, "core", envValueOf(env, "DISPAT_PACKAGE"), "a computed variable wins a name clash")
	assert.Equal(t, "beforeBuild", envValueOf(env, "DISPAT_STAGE"))
	assert.Equal(t, "acme/core:1", envValueOf(env, "DISPAT_OUTPUT_IMAGE"))
	assert.Equal(t, "core:build", envValueOf(env, "DISPAT_OUTPUT_SOURCE_IMAGE"))
	assert.Equal(t, "IMAGE", envValueOf(env, "DISPAT_OUTPUTS"))
	assert.Equal(t, WorkerAuthority, envValueOf(env, AuthorityEnv),
		"every command of a task runs under worker authority")
	assert.Equal(t, "build-a", envValueOf(env, NodeEnv))
}

// TestTaskEnvironmentDropsAnInheritedWorkspace: a node serving a task is not
// taking part in whatever composed run happened to start it, so the context
// that would tell a nested dispat otherwise is emptied.
func TestTaskEnvironmentDropsAnInheritedWorkspace(t *testing.T) {
	blanks := formatInheritedBlanks([]string{
		"PATH=/usr/bin",
		"DISPAT_INTERNAL_WORKSPACE_ROOT=/elsewhere",
		"DISPAT_INTERNAL_WORKSPACE_LIVE_PINS=/tmp/pins",
		"DISPAT_PACKAGE=core",
	})

	assert.Equal(t, []string{"DISPAT_INTERNAL_WORKSPACE_ROOT=", "DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="}, blanks,
		"only the composed-workspace context is emptied, and every name of it is")
}

// TestCheckoutLayoutFollowsTheWorkspace: each repository is materialized where
// the orchestrator said it sits, so a relative path means on a node what it
// means at home.
func TestCheckoutLayoutFollowsTheWorkspace(t *testing.T) {
	checkout := NewCheckout(nil, "/tasks/checkout", zerolog.Nop())

	assert.Equal(t, "/tasks/checkout", checkout.Dir("."))
	assert.Equal(t, "/tasks/checkout", checkout.Dir(""))
	assert.Equal(t, "/tasks/checkout/packages/core", checkout.Dir(".", "packages/core"))
	assert.Equal(t, "/tasks/checkout/sdk/packages/core", checkout.Dir("sdk", "packages/core"))
}

// TestOwnerPathOfTheTaskPackage: the frame runs in the repository that owns
// the package, wherever that repository sits in the checkout.
func TestOwnerPathOfTheTaskPackage(t *testing.T) {
	assignment := Assignment{
		Package: &AssignmentPackage{Name: "core", Repository: "sdk", Dir: "packages/core"},
		Repositories: []AssignmentRepository{
			{Name: "app", Path: "."}, {Name: "sdk", Path: "sdk"},
		},
	}

	assert.Equal(t, "sdk", ownerPathOf(assignment))
	assignment.Package.Repository = ""
	assert.Equal(t, ".", ownerPathOf(assignment), "a single history owns the root")
}

// TestTaskFolderIsAName: distinct exact task identities have distinct bounded
// folders, including names a lossy path sanitizer would have conflated.
func TestTaskFolderIsAName(t *testing.T) {
	first := formatTaskFolder("run1", "α:build", 1)
	assert.Equal(t, first, formatTaskFolder("run1", "α:build", 1),
		"the same attempt always cleans the same folder")
	assert.NotEqual(t, first, formatTaskFolder("run1", "β:build", 1))
	assert.NotEqual(t, first, formatTaskFolder("run2", "α:build", 1))
	assert.NotEqual(t, first, formatTaskFolder("run1", "α:build", 2))
	escaping := formatTaskFolder("run1", "../../etc/passwd:build", 2)
	assert.Equal(t, escaping, filepath.Base(escaping),
		"whatever a package is called, the folder it produces is one path segment")
	assert.Less(t, len(formatTaskFolder("run1", strings.Repeat("α", 300)+":build", 1)), 255)
}

// TestStageTitleMatchesTheHookNames: the DISPAT_STAGE a node's hook reads is
// the word the same hook reads at home.
func TestStageTitleMatchesTheHookNames(t *testing.T) {
	assert.Equal(t, "Build", formatStageTitle("build"))
	assert.Equal(t, "Publish", formatStageTitle("publish"))
	assert.Empty(t, formatStageTitle(""))
}

// TestExportedValuesTravelBothWays: what a node reports and what an
// orchestrator merges are the same values, provenance included.
func TestExportedValuesTravelBothWays(t *testing.T) {
	outputs := []plan.Output{
		{Name: "IMAGE", Value: "acme/core:1", Source: "core:build"},
		{Name: "DIGEST", Value: "sha256:abc"},
	}

	round := formatOutputs(formatExports(outputs))

	require.Len(t, round, 2)
	assert.Equal(t, outputs, round)
}

// envValueOf is the effective value of one name, under the last-wins rule a
// process environment resolves duplicates by.
func envValueOf(env []string, name string) string {
	value := ""
	for _, pair := range env {
		if found, rest, isPair := cutEnvPair(pair); isPair && found == name {
			value = rest
		}
	}
	return value
}

func cutEnvPair(pair string) (string, string, bool) {
	for index := range len(pair) {
		if pair[index] == '=' {
			return pair[:index], pair[index+1:], true
		}
	}
	return "", "", false
}
