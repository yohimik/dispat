package integration

// Area 19: the autoreplace command through the compiled binary. `dispat
// autoreplace` is `dispat writer` pointed at a selection instead of a list of
// files, and what only a real run can show about it lives here: that the
// manifests it edits are the ones it found by scanning each covered package,
// that the packages are the ones the plan and the window pick, that a
// {version} placeholder resolves against the plan the binary just computed,
// that the edits land byte for byte and converge on a second pass, and that
// every outcome — a stale edit, a malformed spec, nothing to write — reaches
// the process exit code.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// arCoreJSON and arWebJSON are this file's fixture manifests: core declares
// nothing, web declares core and a third-party dependency, so one invocation
// meets both the package that carries the edit and the package that does not.
const arCoreJSON = `{
  "name": "@acme/core",
  "version": "0.0.0"
}
`

const arWebJSON = `{
  "name": "@acme/web",
  "version": "0.0.0",
  "dependencies": {
    "@acme/core": "^0.0.0",
    "left-pad": "^1.0.0"
  }
}
`

// arRepo is the fixture: a libs space with core and web (web depending on
// core), each carrying a package.json, released once so both have a baseline
// and a fresh feat commit so both are on the release window.
func arRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 2)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", arCoreJSON)
	r.WriteFile("packages/web/package.json", arWebJSON)
	r.Commit("feat(core,web): bootstrap")
	return r
}

func arRead(t *testing.T, r *harness.Repo, parts ...string) string {
	t.Helper()
	body, err := os.ReadFile(r.Path(parts...))
	require.NoError(t, err)
	return string(body)
}

// TestAutoReplaceEditsEveryCoveredPackage: one invocation, every package the
// window covers, and the file it writes is the fixture with exactly the edited
// bytes changed. A second pass writes nothing at all, which is what makes the
// command safe inside a flow that may run twice.
func TestAutoReplaceEditsEveryCoveredPackage(t *testing.T) {
	r := arRepo(t)

	res := r.Command("autoreplace", "--since", "all",
		"--set", "@acme/core=^{version}", "--set-version", "{version}")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	// The plan bumps both packages to 0.1.0, so {version} is 0.1.0 in the
	// consumer's range and in each package's own version field.
	assert.Equal(t, strings.NewReplacer(
		`"version": "0.0.0"`, `"version": "0.1.0"`,
		`"@acme/core": "^0.0.0"`, `"@acme/core": "^0.1.0"`,
	).Replace(arWebJSON), arRead(t, r, "packages", "web", "package.json"),
		"every other byte of the manifest is left alone")
	assert.Equal(t, strings.Replace(arCoreJSON, `"version": "0.0.0"`, `"version": "0.1.0"`, 1),
		arRead(t, r, "packages", "core", "package.json"))

	before := r.Git("status", "--porcelain")
	res = r.Command("autoreplace", "--since", "all",
		"--set", "@acme/core=^{version}", "--set-version", "{version}")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, before, r.Git("status", "--porcelain"), "the second pass converges")
}

// TestAutoReplaceSelectsLikeEveryOtherCommand: the terms, the folder
// inference and the window flags mean here what they mean everywhere else.
func TestAutoReplaceSelectsLikeEveryOtherCommand(t *testing.T) {
	r := arRepo(t)

	// A package term: only web is visited, so only web's manifest moves.
	res := r.Command("autoreplace", "--package", "web", "--set", "left-pad=^2.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"left-pad": "^2.0.0"`)
	assert.Contains(t, res.Stdout, "packages/web/package.json", "the listing names the file it wrote")
	assert.NotContains(t, res.Stdout, "packages/core/package.json")

	// The invocation folder stands in for the term nobody typed.
	res = r.CommandAt("packages/web", "autoreplace", "--set", "left-pad=^3.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"left-pad": "^3.0.0"`)

	// --consumers reaches web from core, which declares nothing itself.
	res = r.Command("autoreplace", "--package", "core", "--consumers", "--set", "left-pad=^4.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"left-pad": "^4.0.0"`,
		"the consumer was pulled in")

	// A term matching no package is an error, never an empty selection.
	assert.Equal(t, 1, r.Command("autoreplace", "-p", "ghost", "--set", "left-pad=^5.0.0").Code)
}

// TestAutoReplaceOnlyUpdatedFollowsThePlan: with --only-updated an edit stands
// only while the package it names is one this run releases. After the release
// has happened, the same command is a clean no-op.
func TestAutoReplaceOnlyUpdatedFollowsThePlan(t *testing.T) {
	r := arRepo(t)

	res := r.Command("autoreplace", "--since", "all", "--only-updated",
		"--set", "@acme/core=^{version}", "--set", "left-pad=^9.9.9")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	web := arRead(t, r, "packages", "web", "package.json")
	assert.Contains(t, web, `"@acme/core": "^0.1.0"`, "core is releasing, so its edit stands")
	assert.Contains(t, web, `"left-pad": "^1.0.0"`, "left-pad is no package of this workspace")

	// Release everything, so nothing is pending any more, and ask again: the
	// edits all name packages this run does not update.
	r.Git("add", "-A")
	r.Commit("chore: reconcile")
	r.ReleaseOK()
	res = r.Command("autoreplace", "--since", "all", "--only-updated", "--set", "@acme/core=^9.9.9")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "nothing to write")
	assert.NotContains(t, arRead(t, r, "packages", "web", "package.json"), "9.9.9",
		"a run that updates nothing writes nothing")
}

// TestAutoReplaceManifestScope: root stops at the package folder, all descends
// into it, and the own-version write stays on the root manifests either way.
func TestAutoReplaceManifestScope(t *testing.T) {
	r := arRepo(t)
	r.WriteFile("packages/web/example/package.json",
		"{\n  \"name\": \"example\",\n  \"version\": \"9.9.9\",\n  \"dependencies\": {\n    \"@acme/core\": \"^0.0.0\"\n  }\n}\n")
	r.Commit("chore(web): an example project")

	require.Equal(t, 0, r.Command("autoreplace", "--since", "all", "--set", "@acme/core=^1.0.0").Code)
	assert.Contains(t, arRead(t, r, "packages", "web", "example", "package.json"), `"@acme/core": "^0.0.0"`,
		"the root scope does not descend")

	res := r.Command("autoreplace", "--since", "all", "--manifests", "all",
		"--set", "@acme/core=^2.0.0", "--set-version", "5.5.5")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	example := arRead(t, r, "packages", "web", "example", "package.json")
	assert.Contains(t, example, `"@acme/core": "^2.0.0"`, "the all scope reaches the nested manifest")
	assert.Contains(t, example, `"version": "9.9.9"`, "a nested manifest keeps its own version")
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"version": "5.5.5"`)
}

// TestAutoReplaceRedirects: --replace points a dependency at a folder and
// takes the redirect away again, across the selection.
func TestAutoReplaceRedirects(t *testing.T) {
	r := arRepo(t)

	res := r.Command("autoreplace", "--since", "all", "--replace", "@acme/core=../core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), "file:../core")

	res = r.Command("autoreplace", "--since", "all", "--replace", "@acme/core=")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"@acme/core": "^0.0.0"`,
		"an empty path restores the declared range")
}

// TestAutoReplaceOutcomesReachTheExitCode: every way this command can refuse,
// over the process boundary. --strict is asked across the whole sweep, because
// an edit missing from one package's manifest is the ordinary case when one
// invocation covers several.
func TestAutoReplaceOutcomesReachTheExitCode(t *testing.T) {
	r := arRepo(t)

	res := r.Command("autoreplace", "--since", "all", "--strict", "--set", "@acme/core=^1.0.0")
	assert.Equal(t, 0, res.Code, "core declares nothing, web declares it: the edit landed; stderr:\n%s", res.Stderr)

	res = r.Command("autoreplace", "--since", "all", "--strict", "--set", "nowhere=^1.0.0")
	assert.Equal(t, 1, res.Code, "an edit no manifest anywhere declares is stale")
	assert.Contains(t, res.Stdout, "matched no manifest")

	res = r.Command("autoreplace", "--since", "all", "--set", "nowhere=^1.0.0")
	assert.Equal(t, 0, res.Code, "without --strict the same run is tolerated")

	assert.Equal(t, 2, r.Command("autoreplace").Code, "nothing to write")
	assert.Equal(t, 2, r.Command("autoreplace", "--set", "nope").Code, "a malformed edit spec")
	assert.Equal(t, 2, r.Command("autoreplace", "--set-version", "1.0.0", "--manifests", "sideways").Code)
	assert.Equal(t, 2, r.Command("autoreplace", "--set-version", "1.0.0", "--manifests", "none").Code,
		"none is auto-versioning's scope, not this command's")
	assert.Equal(t, 2, r.Command("autoreplace", "extra", "--set-version", "1.0.0").Code,
		"packages are flags, not positional arguments")

	// A selection in which no covered package has a manifest anything can
	// write is an error, because writing nothing without saying so is how a
	// mistyped selection hides.
	r.Remove("packages/core/package.json")
	r.Remove("packages/web/package.json")
	r.Commit("chore: drop the manifests")
	res = r.Command("autoreplace", "--since", "all", "--set", "@acme/core=^1.0.0")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "no covered package has a manifest this command can write")
	r.Git("revert", "--no-edit", "HEAD")

	// A placeholder naming no package of the workspace is refused before
	// anything is written.
	res = r.Command("autoreplace", "--since", "all", "--set", "left-pad=^{version}")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "names no package in this workspace")
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"left-pad": "^1.0.0"`)
}

// TestAutoReplaceJSONEvents: the structured output CI ingests carries one
// event per manifest with its outcomes, plus the run's own tally.
func TestAutoReplaceJSONEvents(t *testing.T) {
	r := arRepo(t)
	res := r.Command("autoreplace", "--since", "all", "--set", "@acme/core=^1.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	updated := findEvent(t, res.Events, "manifest updated")
	assert.Equal(t, "packages/web/package.json", updated.Str("path"))
	assert.Equal(t, "web", updated.Package(), "the event says whose manifest it was")

	done := findEvent(t, res.Events, "autoreplace complete")
	assert.EqualValues(t, 1, done["applied"])
	assert.EqualValues(t, 2, done["packages"])
}

// TestAutoReplaceSyncLock: the lock files do not fall behind the ranges. The
// scripts run only where a manifest actually changed, so a converged re-run
// regenerates nothing.
func TestAutoReplaceSyncLock(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 2)
	cfg.Scripts["locksync"] = "cp package.json lock-snapshot.json"
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path:        "packages",
		Flow:        buildPublish(),
		AutoVersion: &models.AutoVersionConfig{SyncLock: []string{"locksync"}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", arCoreJSON)
	r.WriteFile("packages/web/package.json", arWebJSON)
	r.Commit("feat(core,web): bootstrap")

	res := r.Command("autoreplace", "--since", "all", "--set", "@acme/core=^1.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "lock-snapshot.json"), `"@acme/core": "^1.0.0"`,
		"syncLock sees the rewritten manifest")
	assert.NoFileExists(t, r.Path("packages", "core", "lock-snapshot.json"),
		"core's manifest never changed, so nothing had to be regenerated")

	r.Remove("packages/web/lock-snapshot.json")
	res = r.Command("autoreplace", "--since", "all", "--set", "@acme/core=^1.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.NoFileExists(t, r.Path("packages", "web", "lock-snapshot.json"),
		"a converged pass changes nothing, so it regenerates nothing")

	res = r.Command("autoreplace", "--since", "all", "--sync-lock=false", "--set", "@acme/core=^2.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.NoFileExists(t, r.Path("packages", "web", "lock-snapshot.json"),
		"--sync-lock=false leaves the lock files to the caller")
}

// TestAutoReplaceCommandWordKeepsItsScript: like every command word,
// "autoreplace" shadows a run script of the same name — the two-word spelling
// is how a repository that defines one still reaches it.
func TestAutoReplaceCommandWordKeepsItsScript(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["autoreplace"] = "echo the script ran"
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")

	res := r.Command("run", "autoreplace")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "the script ran")

	// The bare word is the command, which needs something to write.
	assert.Equal(t, 2, r.Command("autoreplace").Code)
}
