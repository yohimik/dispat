package integration

// Area 19: the autowriter command through the compiled binary. `dispat
// autowriter` is `dispat writer` pointed at a selection instead of a list of
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

// TestAutoWriterEditsEveryCoveredPackage: one invocation, every package the
// window covers, and the file it writes is the fixture with exactly the edited
// bytes changed. A second pass writes nothing at all, which is what makes the
// command safe inside a flow that may run twice.
func TestAutoWriterEditsEveryCoveredPackageThroughTheBinary(t *testing.T) {
	r := arRepo(t)

	res := r.Command("autowriter", "--since", "all",
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
	res = r.Command("autowriter", "--since", "all",
		"--set", "@acme/core=^{version}", "--set-version", "{version}")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, before, r.Git("status", "--porcelain"), "the second pass converges")
}

// TestAutoWriterSelectsLikeEveryOtherCommand: the terms, the folder
// inference and the window flags mean here what they mean everywhere else.
func TestAutoWriterSelectsLikeEveryOtherCommand(t *testing.T) {
	r := arRepo(t)

	// A package term: only web is visited, so only web's manifest moves.
	res := r.Command("autowriter", "--package", "web", "--set", "left-pad=^2.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"left-pad": "^2.0.0"`)
	assert.Contains(t, res.Stdout, "packages/web/package.json", "the listing names the file it wrote")
	assert.NotContains(t, res.Stdout, "packages/core/package.json")

	// The invocation folder stands in for the term nobody typed.
	res = r.CommandAt("packages/web", "autowriter", "--set", "left-pad=^3.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"left-pad": "^3.0.0"`)

	// --consumers reaches web from core, which declares nothing itself.
	res = r.Command("autowriter", "--package", "core", "--consumers", "--set", "left-pad=^4.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"left-pad": "^4.0.0"`,
		"the consumer was pulled in")

	// A term matching no package is an error, never an empty selection.
	assert.Equal(t, 1, r.Command("autowriter", "-p", "ghost", "--set", "left-pad=^5.0.0").Code)
}

// TestAutoWriterOnlyUpdatedFollowsThePlan: with --only-updated an edit stands
// only while the package it names is one this run releases. After the release
// has happened, the same command is a clean no-op.
func TestAutoWriterOnlyUpdatedFollowsThePlan(t *testing.T) {
	r := arRepo(t)

	res := r.Command("autowriter", "--since", "all", "--only-updated",
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
	res = r.Command("autowriter", "--since", "all", "--only-updated", "--set", "@acme/core=^9.9.9")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "nothing to write")
	assert.NotContains(t, arRead(t, r, "packages", "web", "package.json"), "9.9.9",
		"a run that updates nothing writes nothing")
}

// TestAutoWriterManifestScope: root stops at the package folder, all descends
// into it, and the own-version write stays on the root manifests either way.
func TestAutoWriterManifestScope(t *testing.T) {
	r := arRepo(t)
	r.WriteFile("packages/web/example/package.json",
		"{\n  \"name\": \"example\",\n  \"version\": \"9.9.9\",\n  \"dependencies\": {\n    \"@acme/core\": \"^0.0.0\"\n  }\n}\n")
	r.Commit("chore(web): an example project")

	require.Equal(t, 0, r.Command("autowriter", "--since", "all", "--set", "@acme/core=^1.0.0").Code)
	assert.Contains(t, arRead(t, r, "packages", "web", "example", "package.json"), `"@acme/core": "^0.0.0"`,
		"the root scope does not descend")

	res := r.Command("autowriter", "--since", "all", "--manifests", "all",
		"--set", "@acme/core=^2.0.0", "--set-version", "5.5.5")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	example := arRead(t, r, "packages", "web", "example", "package.json")
	assert.Contains(t, example, `"@acme/core": "^2.0.0"`, "the all scope reaches the nested manifest")
	assert.Contains(t, example, `"version": "9.9.9"`, "a nested manifest keeps its own version")
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"version": "5.5.5"`)
}

// TestAutoWriterRedirects: --link points a dependency at a folder and
// takes the redirect away again, across the selection.
func TestAutoWriterRedirectsThroughTheBinary(t *testing.T) {
	r := arRepo(t)

	res := r.Command("autowriter", "--since", "all", "--link", "@acme/core=../core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), "file:../core")

	res = r.Command("autowriter", "--since", "all", "--link", "@acme/core=")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"@acme/core": "^0.0.0"`,
		"an empty path restores the declared range")
}

// TestAutoWriterOutcomesReachTheExitCode: every way this command can refuse,
// over the process boundary. --strict is asked across the whole sweep, because
// an edit missing from one package's manifest is the ordinary case when one
// invocation covers several.
func TestAutoWriterOutcomesReachTheExitCode(t *testing.T) {
	r := arRepo(t)

	res := r.Command("autowriter", "--since", "all", "--strict", "--set", "@acme/core=^1.0.0")
	assert.Equal(t, 0, res.Code, "core declares nothing, web declares it: the edit landed; stderr:\n%s", res.Stderr)

	res = r.Command("autowriter", "--since", "all", "--strict", "--set", "nowhere=^1.0.0")
	assert.Equal(t, 1, res.Code, "an edit no manifest anywhere declares is stale")
	assert.Contains(t, res.Stdout, "matched no manifest")

	res = r.Command("autowriter", "--since", "all", "--set", "nowhere=^1.0.0")
	assert.Equal(t, 0, res.Code, "without --strict the same run is tolerated")

	// An empty selection tried nothing, so --strict has nothing to call stale.
	r.Command("commit", "--tag")
	res = r.Command("autowriter", "--package", "core", "--strict", "--set", "nowhere=^1.0.0")
	assert.Equal(t, 0, res.Code, "a window that covered nothing is a clean no-op; stdout:\n%s", res.Stdout)

	assert.Equal(t, 2, r.Command("autowriter").Code, "nothing to write")
	assert.Equal(t, 2, r.Command("autowriter", "--set", "nope").Code, "a malformed edit spec")
	assert.Equal(t, 2, r.Command("autowriter", "--set-version", "1.0.0", "--manifests", "sideways").Code)
	assert.Equal(t, 2, r.Command("autowriter", "--set-version", "1.0.0", "--manifests", "none").Code,
		"none is auto-versioning's scope, not this command's")
	assert.Equal(t, 2, r.Command("autowriter", "extra", "--set-version", "1.0.0").Code,
		"packages are flags, not positional arguments")

	// A selection in which no covered package has a manifest anything can
	// write is an error, because writing nothing without saying so is how a
	// mistyped selection hides.
	r.Remove("packages/core/package.json")
	r.Remove("packages/web/package.json")
	r.Commit("chore: drop the manifests")
	res = r.Command("autowriter", "--since", "all", "--set", "@acme/core=^1.0.0")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "no covered package has a manifest this command can write")
	r.Git("revert", "--no-edit", "HEAD")

	// A placeholder naming no package of the workspace is refused before
	// anything is written.
	res = r.Command("autowriter", "--since", "all", "--set", "left-pad=^{version}")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "names no package in this workspace")
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"left-pad": "^1.0.0"`)
}

// TestAutoWriterJSONEvents: the structured output CI ingests carries one
// event per manifest with its outcomes, plus the run's own tally.
func TestAutoWriterJSONEvents(t *testing.T) {
	r := arRepo(t)
	res := r.Command("autowriter", "--since", "all", "--set", "@acme/core=^1.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	updated := findEvent(t, res.Events, "manifest updated")
	assert.Equal(t, "packages/web/package.json", updated.Str("path"))
	assert.Equal(t, "web", updated.Package(), "the event says whose manifest it was")

	done := findEvent(t, res.Events, "autowriter complete")
	assert.EqualValues(t, 1, done["applied"])
	assert.EqualValues(t, 2, done["packages"])
}

// TestAutoWriterSyncLock: the lock files do not fall behind the ranges. The
// scripts run only where a manifest actually changed, so a converged re-run
// regenerates nothing.
func TestAutoWriterSyncLock(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 2)
	cfg.Scripts["locksync"] = models.Script{"cp package.json lock-snapshot.json"}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path:        models.PathList{"packages"},
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

	res := r.Command("autowriter", "--since", "all", "--set", "@acme/core=^1.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "lock-snapshot.json"), `"@acme/core": "^1.0.0"`,
		"syncLock sees the rewritten manifest")
	assert.NoFileExists(t, r.Path("packages", "core", "lock-snapshot.json"),
		"core's manifest never changed, so nothing had to be regenerated")

	r.Remove("packages/web/lock-snapshot.json")
	res = r.Command("autowriter", "--since", "all", "--set", "@acme/core=^1.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.NoFileExists(t, r.Path("packages", "web", "lock-snapshot.json"),
		"a converged pass changes nothing, so it regenerates nothing")

	res = r.Command("autowriter", "--since", "all", "--sync-lock=false", "--set", "@acme/core=^2.0.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.NoFileExists(t, r.Path("packages", "web", "lock-snapshot.json"),
		"--sync-lock=false leaves the lock files to the caller")
}

// arGoRepo is the derived-link fixture. Links only exist in five manifest
// formats, and npm is deliberately not one this command writes, so the local
// flags need a go.mod workspace to show anything at all: api requires core, and
// core sits in a sibling folder so the derived path has a real "../" to find.
func arGoRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 2)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "api", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "api")
	r.WriteFile("packages/core/go.mod", "module github.com/acme/core\n\ngo 1.26\n")
	r.WriteFile("packages/api/go.mod",
		"module github.com/acme/api\n\ngo 1.26\n\nrequire github.com/acme/core v0.0.0\n")
	r.Commit("feat(core,api): bootstrap")
	return r
}

// TestAutoWriterSetLocalDerivesEveryWorkspaceRange: --set-local writes the
// provider's planned version into every declaration that names a package here,
// with no dependency typed on the command line. --range spells it.
func TestAutoWriterSetLocalDerivesEveryWorkspaceRange(t *testing.T) {
	r := arRepo(t)

	res := r.Command("autowriter", "--since", "all", "--set-local", "--range", "^{version}")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	web := arRead(t, r, "packages", "web", "package.json")
	assert.Contains(t, web, `"@acme/core": "^0.1.0"`, "the workspace dependency followed its provider")
	assert.Contains(t, web, `"left-pad": "^1.0.0"`, "a third-party dependency is not this command's business")
}

// TestAutoWriterSetLocalConverges: a second pass computes the same ranges, so
// it writes nothing and reports nothing applied. A derived edit that reported
// itself applied every run would re-trigger the syncLock scripts forever.
func TestAutoWriterSetLocalConverges(t *testing.T) {
	r := arRepo(t)

	res := r.Command("autowriter", "--since", "all", "--set-local", "--range", "^{version}")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	first := arRead(t, r, "packages", "web", "package.json")

	res = r.Command("autowriter", "--since", "all", "--set-local", "--range", "^{version}")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, first, arRead(t, r, "packages", "web", "package.json"),
		"the second pass is a byte-for-byte no-op")
	assert.Contains(t, res.Stdout, `"applied":0`, "and it reports nothing applied")
}

// TestAutoWriterSetLocalYieldsToTheCommandLine: a dependency named by --set
// keeps what the operator asked for, because naming it is the more specific
// instruction.
func TestAutoWriterSetLocalYieldsToTheCommandLine(t *testing.T) {
	r := arRepo(t)

	res := r.Command("autowriter", "--since", "all", "--set-local", "--range", "^{version}",
		"--set", "@acme/core=workspace:*")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"), `"@acme/core": "workspace:*"`,
		"the explicit --set wins over the derived range")
}

// TestAutoWriterLinkLocalRoundTrips: --link-local points every workspace
// dependency at its folder and --unlink-local takes every one of them away
// again, leaving the manifest exactly as it started. That round trip is the
// whole contract: the derived paths and their removal have to agree.
func TestAutoWriterLinkLocalRoundTrips(t *testing.T) {
	r := arGoRepo(t)
	before := arRead(t, r, "packages", "api", "go.mod")

	res := r.Command("autowriter", "--since", "all", "--link-local")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	linked := arRead(t, r, "packages", "api", "go.mod")
	assert.Contains(t, linked, "replace github.com/acme/core => ../core",
		"the path is relative to the manifest's own folder")

	res = r.Command("autowriter", "--since", "all", "--unlink-local")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, before, arRead(t, r, "packages", "api", "go.mod"),
		"unlinking restores the file byte for byte")
}

// TestAutoWriterLinkLocalReachesAnIndirectRequire: a Go build honours replace
// directives from the main module alone, so a provider the consumer reaches
// only through another module still has to be redirected from the consumer's
// own go.mod. go.mod records that provider as an indirect require, which is
// not a declaration and so is no business of --set-local; the link half has to
// see it anyway, or the linked provider compiles against the published copy of
// it and the whole redirect achieves nothing.
func TestAutoWriterLinkLocalReachesAnIndirectRequire(t *testing.T) {
	r := arGoRepo(t)
	// leaf is reached only through core: api requires it, marked indirect, and
	// imports nothing from it itself.
	r.SeedPackage("packages", "leaf")
	r.WriteFile("packages/leaf/go.mod", "module github.com/acme/leaf\n\ngo 1.26\n")
	r.WriteFile("packages/core/go.mod",
		"module github.com/acme/core\n\ngo 1.26\n\nrequire github.com/acme/leaf v0.0.0\n")
	r.WriteFile("packages/api/go.mod",
		"module github.com/acme/api\n\ngo 1.26\n\nrequire github.com/acme/core v0.0.0\n\n"+
			"require github.com/acme/leaf v0.0.0 // indirect\n")
	r.Commit("feat(core,api,leaf): a provider reached only transitively")
	before := arRead(t, r, "packages", "api", "go.mod")

	res := r.Command("autowriter", "--since", "all", "--link-local")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	api := arRead(t, r, "packages", "api", "go.mod")
	assert.Contains(t, api, "replace github.com/acme/core => ../core",
		"the direct provider is linked as before")
	assert.Contains(t, api, "replace github.com/acme/leaf => ../leaf",
		"and so is the one only the indirect require names")

	res = r.Command("autowriter", "--since", "all", "--unlink-local")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, before, arRead(t, r, "packages", "api", "go.mod"),
		"both come away again, byte for byte")
}

// TestAutoWriterSetLocalLeavesAnIndirectRequireAlone: the range half stops at
// the declarations. An indirect require is a version the toolchain wrote, and
// reconciling it here would be dispat editing bookkeeping it does not own.
func TestAutoWriterSetLocalLeavesAnIndirectRequireAlone(t *testing.T) {
	r := arGoRepo(t)
	r.SeedPackage("packages", "leaf")
	r.WriteFile("packages/leaf/go.mod", "module github.com/acme/leaf\n\ngo 1.26\n")
	r.WriteFile("packages/api/go.mod",
		"module github.com/acme/api\n\ngo 1.26\n\nrequire github.com/acme/core v0.0.0\n\n"+
			"require github.com/acme/leaf v0.0.0 // indirect\n")
	r.Commit("feat(api,leaf): an indirect require")

	res := r.Command("autowriter", "--since", "all", "--set-local", "--range", "{version}")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	api := arRead(t, r, "packages", "api", "go.mod")
	assert.Contains(t, api, "github.com/acme/leaf v0.0.0 // indirect",
		"the indirect require keeps the version the toolchain gave it")
}

// TestAutoWriterLinkLocalSkipsNpm: npm refuses an override for a directly
// declared dependency unless the specs match exactly, and a derived link is
// aimed at exactly those. Writing one would hand the user a package.json that
// npm errors on, so the manifest is left alone and the reason is logged.
func TestAutoWriterLinkLocalSkipsNpm(t *testing.T) {
	r := arRepo(t)
	before := arRead(t, r, "packages", "web", "package.json")

	res := r.Command("autowriter", "--since", "all", "--link-local")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, before, arRead(t, r, "packages", "web", "package.json"),
		"package.json is untouched by a derived link")
	assert.Contains(t, res.Stdout+res.Stderr, "npm refuses an override",
		"and the operator is told why")
}

// TestAutoWriterLinkLocalWarnsAboutPublishing: nothing in the release path
// removes a local link, so leaving one in place ships a manifest consumers
// cannot resolve. That is worth saying every time, not only in the docs.
func TestAutoWriterLinkLocalWarnsAboutPublishing(t *testing.T) {
	r := arGoRepo(t)

	res := r.Command("autowriter", "--since", "all", "--link-local")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "must be removed before publishing")
}

// TestAutoWriterLocalFlagsReachTheExitCode: the usage mistakes and the
// narrowing, over the process boundary.
func TestAutoWriterLocalFlagsReachTheExitCode(t *testing.T) {
	r := arGoRepo(t)

	assert.Equal(t, 2, r.Command("autowriter", "--link-local", "--unlink-local").Code,
		"opposite instructions in one invocation")

	// A bare local flag is a complete request: there is nothing to type.
	res := r.Command("autowriter", "--since", "all", "--set-local")
	assert.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	res = r.Command("autowriter", "--since", "all", "--link-local")
	assert.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	// --strict asks whether a *requested* edit found a target. A derived one
	// comes from a declaration that exists, so it can never be stale.
	res = r.Command("autowriter", "--since", "all", "--strict", "--link-local")
	assert.Equal(t, 0, res.Code, "derived edits do not trip the stale gate; stdout:\n%s", res.Stdout)
}

// TestAutoWriterLinkLocalResolvesFromTheManifestFolder: a link path is
// relative to the manifest that holds it, not to the package folder. A go.mod
// two levels down resolves "../../" against its own directory, so getting this
// wrong writes a path that does not exist.
func TestAutoWriterLinkLocalResolvesFromTheManifestFolder(t *testing.T) {
	r := arGoRepo(t)
	r.WriteFile("packages/api/sub/go.mod",
		"module github.com/acme/api/sub\n\ngo 1.26\n\nrequire github.com/acme/core v0.0.0\n")
	r.Commit("feat(api): a nested module")

	res := r.Command("autowriter", "--since", "all", "--manifests", "all", "--link-local")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "api", "go.mod"),
		"replace github.com/acme/core => ../core")
	assert.Contains(t, arRead(t, r, "packages", "api", "sub", "go.mod"),
		"replace github.com/acme/core => ../../core",
		"the nested module resolves from its own folder, one level deeper")
}

// TestAutoWriterSetLocalAndLinkLocalInOnePass: the two derive from one walk of
// the same declarations, so asking for both writes both.
func TestAutoWriterSetLocalAndLinkLocalInOnePass(t *testing.T) {
	r := arGoRepo(t)

	res := r.Command("autowriter", "--since", "all", "--set-local", "--link-local")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	api := arRead(t, r, "packages", "api", "go.mod")
	assert.Contains(t, api, "github.com/acme/core v0.1.0", "the range followed the provider")
	assert.Contains(t, api, "replace github.com/acme/core => ../core", "and the link was written")
}

// TestAutoWriterSetLocalSpellsEachEcosystemItsOwnWay: --range goes through the
// same renderer auto-versioning uses, so one keyword covers a workspace whose
// packages are not all one ecosystem. go.mod keeps its canonical "v" and a
// Docker tag stays a bare label, because a caret in a FROM line is not
// something a registry can resolve.
func TestAutoWriterSetLocalSpellsEachEcosystemItsOwnWay(t *testing.T) {
	r := arGoRepo(t)
	r.WriteFile("packages/api/Dockerfile", "FROM github.com/acme/core:0.0.0\n")
	r.Commit("feat(api): a base image from the workspace")

	res := r.Command("autowriter", "--since", "all", "--manifests", "all",
		"--set-local", "--range", "caret")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, arRead(t, r, "packages", "api", "go.mod"), "github.com/acme/core v0.1.0",
		"go.mod declares exact canonical versions")
	assert.Contains(t, arRead(t, r, "packages", "api", "Dockerfile"), "FROM github.com/acme/core:0.1.0",
		"a tag is a label, so the caret is dropped rather than written")
}

// TestAutoWriterSetLocalTemplateRangeIsVerbatim: a --range template is the
// operator spelling the range themselves, so it passes through untouched and
// the ecosystem rules do not soften it. Over a mixed workspace that can hand a
// Docker manifest something no registry accepts, and the writer refuses it
// rather than write it. Keyword policies are what cross ecosystems.
func TestAutoWriterSetLocalTemplateRangeIsVerbatim(t *testing.T) {
	r := arGoRepo(t)
	r.WriteFile("packages/api/Dockerfile", "FROM github.com/acme/core:0.0.0\n")
	r.Commit("feat(api): a base image from the workspace")

	res := r.Command("autowriter", "--since", "all", "--manifests", "all",
		"--set-local", "--range", "^{version}")
	assert.Equal(t, 1, res.Code, "the package carrying the Dockerfile fails")
	assert.Contains(t, res.Stdout, "refusing to write", "and says exactly what it refused")
	assert.Contains(t, arRead(t, r, "packages", "api", "Dockerfile"), "core:0.0.0",
		"the file it refused is left as it was")
}

// TestAutoWriterLinkLocalLeavesTheComputedGraphAlone: a link is a declaration
// compute already reads, so writing one adds no edge the config did not have.
// Worth pinning: the graph changing under a local checkout would be a nasty
// surprise on the next release.
func TestAutoWriterLinkLocalLeavesTheComputedGraphAlone(t *testing.T) {
	r := arGoRepo(t)

	before := r.Command("compute", "--check")
	res := r.Command("autowriter", "--since", "all", "--link-local")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	after := r.Command("compute", "--check")
	assert.Equal(t, before.Code, after.Code,
		"linking locally suggests no config change that was not already suggested")
}
