package integration

// The e2e smoke walk's companion: where TestSmokeReleaseCycles walks the
// release protocol in depth, this file walks the CLI's key commands in
// breadth — one toy repository taken through the commands CI pipelines and
// day-to-day use lean on (init, status and its --require-release exit codes,
// preview, compute's detect/check/write loop, the if gate, run with a window
// and --consumers, exec, release, the changelog step command and the
// scanner), asserting each command's observable artefact and exit code over
// the process boundary.
//
// Both smoke tests are what the release build runs against the exact binary
// it is about to export (see services/dispat/Dockerfile's test stage, which
// selects -run 'TestSmoke' with DISPAT_TEST_BINARY pointing into its /out):
// the per-feature suites own the deep cases, and this walk is deliberately
// happy-path so the gate answers "do the shipped bytes work" rather than
// re-proving the feature matrix.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// featuresRepo is the walk's workspace: one space at packages/ holding core
// and web, npm manifests that express web's dependency on core so compute has
// an edge to detect, marker build scripts so run and release executions are
// countable, and — deliberately — no dependencies entry in the config, because
// adopting the computed edge is one of the walk's acts.
func featuresRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":   {markerBuild},
		"publish": {"echo publishing"},
		"mark":    {"echo execd >> exec.log"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(),
			AutoVersion: &models.AutoVersionConfig{Manifests: "root"}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@toy/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@toy/web", "version": "0.0.0", "dependencies": {"@toy/core": "workspace:*"}}`)
	return r
}

// TestSmokeKeyFeatures is the walk. The acts build on one another — the exit
// 3 only exists because the release before it converged, the pickup window
// only exists because the fix landed after it — so this is one test, not a
// table.
func TestSmokeKeyFeatures(t *testing.T) {
	// --- Act 0: init, on a repository that has nothing yet. ---
	blank := harness.New(t)
	res := blank.Command("init", "--format", "yaml")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "created dispat.yaml")
	assert.Equal(t, 1, blank.Command("init", "--format", "yaml").Code,
		"an existing config must never be overwritten")

	// --- Act 1: the pending plan, read three ways. ---
	r := featuresRepo(t)
	r.Commit("feat(core,web): bootstrap the toy workspace")

	res = r.StatusOK()
	g := graphOf(res.Events, "core", "web")
	assertGraph(t, g, "core", "0.0.0 -> 0.1.0", "direct")
	assertGraph(t, g, "web", "0.0.0 -> 0.1.0", "direct")
	assert.Equal(t, 0, r.Status("--require-release").Code,
		"a pending release is --require-release's exit 0")

	res = r.Command("preview", "--package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "## core@0.1.0", "the header names the pending tag")
	assert.Contains(t, res.Stdout, "- bootstrap the toy workspace")

	// --- Act 2: compute detects the edge the manifests already express,
	// the check gate fails while the config lags, --write adopts it. ---
	res = r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "+ add     web -> core (dependencies)")
	assert.Equal(t, 1, r.Command("compute", "--check").Code,
		"the gate fails while the config lags the manifests")
	res = r.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "applied 1 change(s)")
	assert.Equal(t, 0, r.Command("compute", "--check").Code, "adopted, the gate passes")
	r.Remove("dispat.json.backup")
	r.Commit("chore: record the computed dependency edge")

	// --- Act 3: the if gate answers from the release window and from an
	// explicit one. ---
	assert.Equal(t, "gate-go", changedGate(r), "a pending release holds the gate")
	assert.Equal(t, "gate-idle", changedGate(r, "--since", "HEAD~1"),
		"the config commit addressed no package")

	// --- Act 4: run sweeps the selection, exec runs the named script. ---
	r.RunScriptOK("build", "--since", "all")
	assert.Equal(t, 2, buildRuns(r), "one run per package")
	res = r.Command("exec", "mark")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, "execd", strings.TrimSpace(readFile(t, r, "exec.log")),
		"the root script ran once, from the repository root")

	// --- Act 5: the release, and everything it leaves behind. ---
	res = r.ReleaseOK()
	g = graphOf(res.Events, "core", "web")
	assertGraph(t, g, "core", "0.0.0 -> 0.1.0", "direct")
	assertGraph(t, g, "web", "0.0.0 -> 0.1.0", "direct")
	for _, tag := range []string{"core@0.1.0", "web@0.1.0"} {
		require.True(t, r.HasTag(tag), "tags: %v", r.TagList())
	}
	assert.Equal(t, 4, buildRuns(r), "the release ran the build stage once per package")
	assert.Contains(t, readFile(t, r, "packages", "core", "package.json"), `"version": "0.1.0"`)
	assert.Contains(t, readFile(t, r, "packages", "core", "CHANGELOG.md"), "bootstrap the toy workspace")
	assert.Contains(t, readFile(t, r, "packages", "web", "CHANGELOG.md"), "web@0.1.0",
		"the entry header names the released tag")

	// --- Act 6: convergence, as the release workflow's plan job reads it. ---
	res = r.StatusOK()
	g = graphOf(res.Events, "core", "web")
	assertGraph(t, g, "core", "0.1.0", "")
	assertGraph(t, g, "web", "0.1.0", "")
	assert.Equal(t, 3, r.Status("--require-release").Code,
		"nothing to release is exit 3 — the contract release.yml's plan job gates on")

	res = r.Command("changelog", "--package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "outside the window, nothing to do",
		"a step command on a converged plan is a logged no-op, never a failure")

	// --- Act 7: a provider-only fix; the window and --consumers reach it. ---
	r.WriteFile("packages/core/patch.txt", "p")
	r.Commit("fix(core): tighten a bolt")

	r.RunScriptOK("build", "--since", "HEAD~1", "--consumers")
	assert.Equal(t, 6, buildRuns(r), "the changed provider and its consumer, nothing else")

	res = r.Command("preview", "--package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "## core@0.1.1")
	assert.Contains(t, res.Stdout, "- tighten a bolt")

	res = r.ReleaseOK()
	g = graphOf(res.Events, "core", "web")
	assertGraph(t, g, "core", "0.1.0 -> 0.1.1", "direct")
	assertGraph(t, g, "web", "0.1.0", "")
	assert.Equal(t, 1, r.TagCount("web@"), "a fix without a caret does not propagate")
	assert.Contains(t, readFile(t, r, "packages", "core", "package.json"), `"version": "0.1.1"`)
	assert.Equal(t, 3, r.Status("--require-release").Code, "converged again")

	// --- Act 8: the scanner reads the workspace the walk just released. ---
	res = r.Command("scanner", ".")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "packages/core/package.json")
	assert.Contains(t, res.Stdout, "packages/web/package.json")
	assert.Contains(t, res.Stdout, "manifest(s)")
}
