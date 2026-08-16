package integration

// Goal 40: the e2e smoke walk. The live pre-1.0.0 release verification ran a
// protocol by hand — predict the plan with status, release, verify every
// record and manifest, prove convergence, repeat — and that protocol found
// what line coverage could not. This file is that protocol as a test: a toy
// polyglot monorepo (a Go library and CLI, an npm package, a Docker image,
// with real go.mod, package.json, Dockerfile and compose manifests, a version
// group and dependency edges) walked through the release shapes a real
// workspace meets, asserting the full status graph (every reason, every
// version), the tags, the changelog entries and the exact manifest contents
// at every step, with convergence proven between steps.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// smokeRepo is the toy workspace:
//
//	golibs/ (Go)     golib, gocli -> golib   independent versioning, gomod rewriting
//	web/    (npm)    js                      version group "app" (fixedMajorMinor)
//	images/ (docker) img -> js               version group "app", Dockerfile+compose
func smokeRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
	cfg.VersionGroups = map[string]models.VersionGroupConfig{
		"app": {Versioning: models.VersioningFixedMajorMinor},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"golibs": {Path: models.PathList{"golibs"}, Flow: buildPublish(),
			AutoVersion: &models.AutoVersionConfig{Manifests: "root"}},
		"web": {Path: models.PathList{"web"}, Flow: buildPublish(), VersionGroup: "app",
			AutoVersion: &models.AutoVersionConfig{Manifests: "root"}},
		"images": {Path: models.PathList{"images"}, Flow: buildPublish(), VersionGroup: "app",
			AutoVersion: &models.AutoVersionConfig{Manifests: "root"}},
	}
	cfg.Packages = map[string]models.PackageConfig{
		// An image's identity is its repository; the npm package that the
		// image is built FROM answers to that name in Docker manifests.
		"js": {ManifestNames: []string{"registry.example.com/js"}},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "gocli", Provider: "golib"},
		{Consumer: "img", Provider: "js"},
	}
	r.WriteConfigModel(cfg)

	r.SeedPackage("golibs", "golib")
	r.SeedPackage("golibs", "gocli")
	r.SeedPackage("web", "js")
	r.SeedPackage("images", "img")
	r.WriteFile("golibs/golib/go.mod", "module example.com/toy/golib\n\ngo 1.22\n")
	r.WriteFile("golibs/gocli/go.mod",
		"module example.com/toy/gocli\n\ngo 1.22\n\nrequire example.com/toy/golib v0.0.0\n")
	r.WriteFile("web/js/package.json", `{"name": "@toy/js", "version": "0.0.0"}`)
	r.WriteFile("images/img/Dockerfile",
		"FROM registry.example.com/js:0.0.0 AS assets\nCOPY --from=assets /dist /srv\n")
	r.WriteFile("images/img/compose.yaml",
		"services:\n  img:\n    image: registry.example.com/img:0.0.0\n  js:\n    image: registry.example.com/js:0.0.0\n")
	return r
}

// graphOf indexes one run's graph lines by package.
func graphOf(events []harness.Event, pkgs ...string) map[string]harness.Event {
	out := make(map[string]harness.Event, len(pkgs))
	for _, p := range pkgs {
		out[p] = harness.GraphLine(events, p)
	}
	return out
}

// assertGraph asserts one package's verdict, version transition and reason in
// one call, so every cycle states its whole expected graph.
func assertGraph(t *testing.T, g map[string]harness.Event, pkg, version, reason string) {
	t.Helper()
	line := g[pkg]
	assert.Equalf(t, version, line.Str("version"), "%s version", pkg)
	if reason == "" {
		assert.Equalf(t, "unchanged", line.Str("message"), "%s verdict", pkg)
		return
	}
	assert.Equalf(t, reason, line.Str("reason"), "%s reason", pkg)
}

// assertConverged proves a follow-up run releases nothing and every package
// reads unchanged at the expected version.
func assertConverged(t *testing.T, r *harness.Repo, at map[string]string) {
	t.Helper()
	res := r.StatusOK()
	g := graphOf(res.Events, "golib", "gocli", "js", "img")
	for pkg, version := range at {
		line := g[pkg]
		assert.Equalf(t, "unchanged", line.Str("message"), "%s after the cycle: %s", pkg, res.Stdout)
		assert.Equalf(t, version, line.Str("version"), "%s baseline", pkg)
	}
}

// TestSmokeReleaseCycles is the walk. The cycles build on one another — the
// pickup in cycle 4 only exists because cycle 3 released the provider alone —
// so this is one test, not a table.
func TestSmokeReleaseCycles(t *testing.T) {
	r := smokeRepo(t)

	// --- Cycle 1: bootstrap. Everything direct, manifests written. ---
	r.Commit("feat(golib,gocli,js,img): bootstrap the toy workspace")
	res := r.ReleaseOK()
	g := graphOf(res.Events, "golib", "gocli", "js", "img")
	assertGraph(t, g, "golib", "0.0.0 -> 0.1.0", "direct")
	assertGraph(t, g, "gocli", "0.0.0 -> 0.1.0", "direct")
	assertGraph(t, g, "js", "0.0.0 -> 0.1.0", "direct")
	assertGraph(t, g, "img", "0.0.0 -> 0.1.0", "direct")
	for _, tag := range []string{"golib@0.1.0", "gocli@0.1.0", "js@0.1.0", "img@0.1.0"} {
		require.True(t, r.HasTag(tag), "tags: %v", r.TagList())
	}
	assert.Contains(t, readFile(t, r, "golibs", "gocli", "go.mod"),
		"require example.com/toy/golib v0.1.0", "the go.mod follows the provider it released beside")
	assert.Contains(t, readFile(t, r, "web", "js", "package.json"), `"version": "0.1.0"`)
	assert.Contains(t, readFile(t, r, "images", "img", "Dockerfile"),
		"FROM registry.example.com/js:0.1.0 AS assets")
	compose := readFile(t, r, "images", "img", "compose.yaml")
	assert.Contains(t, compose, "registry.example.com/img:0.1.0")
	assert.Contains(t, compose, "registry.example.com/js:0.1.0")
	assertConverged(t, r, map[string]string{"golib": "0.1.0", "gocli": "0.1.0", "js": "0.1.0", "img": "0.1.0"})

	// --- Cycle 2: a minor moves the shared part; the image rides. ---
	r.CommitEmpty("feat(js): a minor the group shares")
	res = r.ReleaseOK()
	g = graphOf(res.Events, "js", "img", "golib")
	assertGraph(t, g, "js", "0.1.0 -> 0.2.0", "direct")
	assertGraph(t, g, "img", "0.1.0 -> 0.2.0", "fixed group versioning")
	assertGraph(t, g, "golib", "0.1.0", "")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W234", "img"), "the ride is explained")
	assert.Contains(t, readFile(t, r, "images", "img", "Dockerfile"),
		"FROM registry.example.com/js:0.2.0", "the rider's manifest follows the group")
	assert.Contains(t, readFile(t, r, "images", "img", "compose.yaml"), "registry.example.com/img:0.2.0")
	imgEntry := entryOf(t, spacedChangelog(t, r, "images", "img"), "img@0.2.0")
	assert.Contains(t, imgEntry, "- js: 0.1.0 -> 0.2.0",
		"a ride that picks up a provider's movement has a dependencies section")
	assertConverged(t, r, map[string]string{"js": "0.2.0", "img": "0.2.0"})

	// --- Cycle 3: the provider releases alone; the consumer stays put. ---
	r.CommitEmpty("fix(golib): the provider moves alone")
	res = r.ReleaseOK()
	g = graphOf(res.Events, "golib", "gocli")
	assertGraph(t, g, "golib", "0.1.0 -> 0.1.1", "direct")
	assertGraph(t, g, "gocli", "0.1.0", "")
	assert.Equal(t, 1, r.TagCount("gocli@"), "no propagation, no release; tags: %v", r.TagList())
	assert.Contains(t, readFile(t, r, "golibs", "gocli", "go.mod"),
		"require example.com/toy/golib v0.1.0",
		"an unreleased consumer's manifest keeps the old version: the pickup is deferred")

	// --- Cycle 4: the consumer's own next release performs the pickup. ---
	r.CommitEmpty("fix(gocli): the consumer's own reason")
	res = r.ReleaseOK()
	g = graphOf(res.Events, "golib", "gocli")
	assertGraph(t, g, "gocli", "0.1.0 -> 0.1.1", "direct")
	assertGraph(t, g, "golib", "0.1.1", "")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W197", "gocli"),
		"the reconciliation pickup is reported: %s", res.Stdout)
	assert.Contains(t, readFile(t, r, "golibs", "gocli", "go.mod"),
		"require example.com/toy/golib v0.1.1", "the range catches up to the provider released without it")
	cliEntry := entryOf(t, spacedChangelog(t, r, "golibs", "gocli"), "gocli@0.1.1")
	assert.NotContains(t, cliEntry, "golib",
		"the pickup is manifest-only: nothing forced this release and nothing released beside it")
	assertConverged(t, r, map[string]string{"golib": "0.1.1", "gocli": "0.1.1"})

	// --- Cycle 5: a caret carries the provider's fix to the consumer. ---
	r.CommitEmpty("fix(golib)^: a fix the consumer must ship")
	res = r.ReleaseOK()
	g = graphOf(res.Events, "golib", "gocli")
	assertGraph(t, g, "golib", "0.1.1 -> 0.1.2", "direct")
	assertGraph(t, g, "gocli", "0.1.1 -> 0.1.2", "propagated from golib")
	assert.Contains(t, readFile(t, r, "golibs", "gocli", "go.mod"),
		"require example.com/toy/golib v0.1.2")
	cliEntry = entryOf(t, spacedChangelog(t, r, "golibs", "gocli"), "gocli@0.1.2")
	assert.Contains(t, cliEntry, "- golib: 0.1.1 -> 0.1.2",
		"a forced release documents the movement that forced it")
	assertConverged(t, r, map[string]string{"golib": "0.1.2", "gocli": "0.1.2"})

	// --- Cycle 6: the group boards an rc train, continues it, graduates. ---
	r.CommitEmpty("feat(js)%rc: board the train")
	res = r.ReleaseOK()
	g = graphOf(res.Events, "js", "img")
	assertGraph(t, g, "js", "0.2.0 -> 0.3.0-rc.0", "direct")
	assertGraph(t, g, "img", "0.2.0 -> 0.3.0-rc.0", "fixed group versioning")
	assert.Contains(t, readFile(t, r, "images", "img", "Dockerfile"),
		"FROM registry.example.com/js:0.3.0-rc.0", "prerelease versions reach the manifests too")

	r.CommitEmpty("fix(js)%rc: more on the train")
	res = r.ReleaseOK()
	g = graphOf(res.Events, "js", "img")
	assertGraph(t, g, "js", "0.3.0-rc.0 -> 0.3.0-rc.1", "direct")
	assertGraph(t, g, "img", "0.3.0-rc.0 -> 0.3.0-rc.1", "fixed group versioning")
	jsRc1 := entryOf(t, spacedChangelog(t, r, "web", "js"), "js@0.3.0-rc.1")
	assert.Contains(t, jsRc1, "more on the train")
	assert.NotContains(t, jsRc1, "board the train", "rc.1 does not repeat what rc.0 published")

	r.CommitEmpty("fix(js)%rc>stable: graduate")
	res = r.ReleaseOK()
	g = graphOf(res.Events, "js", "img")
	assertGraph(t, g, "js", "0.3.0-rc.1 -> 0.3.0", "direct")
	assertGraph(t, g, "img", "0.3.0-rc.1 -> 0.3.0", "fixed group versioning")
	jsStable := entryOf(t, spacedChangelog(t, r, "web", "js"), "js@0.3.0")
	assertOrderedIn(t, jsStable, "### Features", "board the train",
		"### Fixes", "graduate", "more on the train")
	imgStable := entryOf(t, spacedChangelog(t, r, "images", "img"), "img@0.3.0")
	assert.Contains(t, imgStable, "- js: 0.2.0 -> 0.3.0",
		"the graduation documents the provider's movement over the whole train")
	assert.Contains(t, readFile(t, r, "images", "img", "Dockerfile"),
		"FROM registry.example.com/js:0.3.0")
	assert.Contains(t, readFile(t, r, "images", "img", "compose.yaml"), "registry.example.com/img:0.3.0")
	assert.Contains(t, readFile(t, r, "web", "js", "package.json"), `"version": "0.3.0"`)

	assertConverged(t, r, map[string]string{
		"golib": "0.1.2", "gocli": "0.1.2", "js": "0.3.0", "img": "0.3.0"})
}
