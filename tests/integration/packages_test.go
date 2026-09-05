package integration

// Area 13: the top-level packages section — standalone packages living
// outside every space through a `path`, and package-declared dependency
// edges. packages.md promises that a standalone entry is a full package (its
// own flow, tags, records), that provider lists declared in a packages entry
// or an in-folder config file order the graph like top-level edges, and that
// `dispat compute` edits each declaration in the file that holds it:
// removals in the declaring package config, additions in the root list.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// dependsOn extracts a package's dependsOn list from status events.
func dependsOn(res harness.RunResult, pkg string) string {
	for _, e := range res.Events {
		if e.Package() != pkg {
			continue
		}
		if list, ok := e["dependsOn"].([]any); ok {
			parts := make([]string, len(list))
			for i, v := range list {
				parts[i], _ = v.(string)
			}
			return strings.Join(parts, ",")
		}
	}
	return ""
}

// dueTo extracts the providers a package was bumped for from status events.
func dueTo(res harness.RunResult, pkg string) string {
	for _, e := range res.Events {
		if e.Package() != pkg {
			continue
		}
		if list, ok := e["dueToProviders"].([]any); ok {
			parts := make([]string, len(list))
			for i, v := range list {
				parts[i], _ = v.(string)
			}
			return strings.Join(parts, ",")
		}
	}
	return ""
}

// TestPackagesStandalonePath: a packages entry with a path releases as a full
// package outside the space folders — its own flow runs in its own folder, it
// tags under the repository format, and the space packages are untouched.
func TestPackagesStandalonePath(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":     {"echo space >> ../../build.log"},
		"cli-build": {"echo cli-built >> ../../cli.log"},
		"publish":   {"echo publishing"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"cli": {Path: "tools/cli", Flow: &models.SpaceFlowConfig{
			Build: []string{"cli-build"}, Publish: []string{"publish"},
		}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("tools", "cli")
	r.Commit("feat(core,cli): bootstrap the space and the standalone package")

	r.ReleaseOK()

	assert.True(t, r.HasTag("core@0.1.0"))
	assert.True(t, r.HasTag("cli@0.1.0"), "the standalone package releases: %v", r.TagList())
	data, err := os.ReadFile(r.Path("cli.log"))
	require.NoError(t, err, "cli must have run its own build, inside tools/cli")
	assert.Equal(t, "cli-built\n", string(data))
	assert.Equal(t, 1, buildRuns(r), "the space build ran for core alone")
	assert.FileExists(t, r.Path("tools", "cli", "CHANGELOG.md"), "records apply to standalone packages")

	// Convergence: a second run releases nothing new.
	r.ReleaseOK()
	assert.Len(t, r.TagList(), 2)
}

// TestPackagesStandaloneConfigErrors: a standalone path may not leave the
// repository, and it must name a folder that exists — both are hard config
// errors, before anything is released.
func TestPackagesStandaloneConfigErrors(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{"cli": {Path: "../outside"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	res := r.Status()
	assert.NotEqual(t, 0, res.Code)
	assert.Contains(t, res.Stderr, "escapes the repository root")

	cfg.Packages = map[string]models.PackageConfig{"cli": {Path: "tools/cli"}}
	r.WriteConfigModel(cfg)
	res = r.Status()
	assert.NotEqual(t, 0, res.Code, "the path must name an existing folder")
	assert.Contains(t, res.Stdout+res.Stderr, "tools/cli",
		"the discovery error names the missing folder")
}

// TestPackagesDependencyEdges: provider lists declared in a packages entry
// and in a package folder's own config file order the graph exactly like
// top-level edges.
func TestPackagesDependencyEdges(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{
		"web": {Dependencies: models.Providers("core")},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.SeedPackage("packages", "app")
	packageFile(t, r, "packages/app", models.PackageConfig{Dependencies: models.Providers("core")})
	r.Commit("feat(core,web,app): bootstrap all three")

	status := r.StatusOK()
	assert.Contains(t, dependsOn(status, "web"), "core", "the entry-declared edge orders the graph")
	assert.Contains(t, dependsOn(status, "app"), "core", "the in-folder-declared edge orders the graph")
}

// TestPackagesDependenciesCarryKindAndKeep: a package's own list holds
// everything the top-level object holds, so a kind reaches propagation and a
// keep survives compute, both declared next to the package they belong to.
func TestPackagesDependenciesCarryKindAndKeep(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	// A default propagation depth, so a provider's bump reaches its consumers
	// with no directive in the message: what the declared kinds decide here.
	cfg.Parser = &models.ParserConfig{
		Propagation: &models.ParserPropagationConfig{Depth: "all"},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"web": {Dependencies: models.ProviderList{
			{Provider: "core"},
			{Provider: "tooling", Kind: "devDependencies"},
			{Provider: "pinned", Keep: true},
		}},
	}
	r.WriteConfigModel(cfg)
	for _, p := range []string{"core", "tooling", "pinned", "web"} {
		r.SeedPackage("packages", p)
	}
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web", "version": "0.0.0"}`)
	r.Commit("feat(core,tooling,pinned,web): bootstrap")

	status := r.StatusOK()
	assert.Contains(t, dependsOn(status, "web"), "core")
	assert.Contains(t, dependsOn(status, "web"), "pinned", "the kept edge is an ordinary edge to the planner")

	// devDependencies is the one kind propagation skips by default, so the
	// kind written in the package's own list decides whether a provider's bump
	// travels. Released first, so the next commits are the only pending work.
	require.Equal(t, 0, r.Release().Code)

	r.CommitEmpty("feat(tooling): a change that must not propagate")
	assert.NotContains(t, dueTo(r.StatusOK(), "web"), "tooling",
		"a devDependencies edge is the one propagation skips by default")

	r.CommitEmpty("feat(core): a change that must propagate")
	assert.Contains(t, dueTo(r.StatusOK(), "web"), "core",
		"while the plain edge beside it still carries a bump")

	// keep: true silences the removal wherever the edge is declared.
	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NotContains(t, res.Stdout, "remove  web -> pinned")
}

// TestPackagesComputeRemoveFromInFolder: a stale edge declared in a package
// folder's own config file is suggested with its source and removed from
// that file by --write — while a manifest-detected addition still lands in
// the root config's list. Each edited file gets its own backup.
func TestPackagesComputeRemoveFromInFolder(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.SeedPackage("packages", "app")
	packageFile(t, r, "packages/web", models.PackageConfig{
		TagFormat:    "web-{name}@{version}",
		Dependencies: models.Providers("core"),
	})
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web", "version": "0.0.0"}`)
	r.WriteFile("packages/app/package.json",
		`{"name": "@acme/app", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("feat(core,web,app): bootstrap")

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "- remove  web -> core")
	assert.Contains(t, res.Stdout, "packages/web/dispat.json: dependencies[0]",
		"the suggestion names the declaring file")
	assert.Contains(t, res.Stdout, "+ add     app -> core")
	assert.Equal(t, 1, r.Command("compute", "--check").Code, "package-declared drift gates CI")

	res = r.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "applied 2 change(s)")

	webCfg, err := os.ReadFile(r.Path("packages", "web", "dispat.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(webCfg), "core", "the stale edge left the declaring file")
	assert.Contains(t, string(webCfg), "web-{name}@{version}", "the file's other keys survive")
	assert.FileExists(t, r.Path("packages", "web", "dispat.json.backup"))

	rootCfg, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(rootCfg), `"app": [`, "the addition lands in the root object, keyed by consumer")
	assert.NotContains(t, string(rootCfg), `"web": [`)

	assert.Equal(t, 0, r.Command("compute", "--check").Code)
	r.StatusOK()
}

// TestPackagesComputeRemoveFromEntry: a stale edge declared under a packages
// entry of the root config is removed from exactly that nested list, leaving
// the entry's other keys — and the rest of the file — alone.
func TestPackagesComputeRemoveFromEntry(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{
		"web": {TagFormat: "web-{name}@{version}", Dependencies: models.Providers("core")},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web", "version": "0.0.0"}`)
	r.Commit("feat(core,web): bootstrap")

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, `packages["web"]: dependencies[0]`,
		"the suggestion names the entry")

	res = r.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	rootCfg, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(rootCfg), `"dependencies": []`, "the entry's list is emptied in place")
	assert.Contains(t, string(rootCfg), "web-{name}@{version}", "the entry's other keys survive")
	assert.FileExists(t, r.Path("dispat.json.backup"))

	assert.Equal(t, 0, r.Command("compute", "--check").Code)
	r.StatusOK()
}

// TestPackagesSrcNarrowsChangeDetection: a package's `src` narrows which of
// its files count as changes to it. A scopeless commit touching only the
// package's docs stops releasing it; one touching src releases it as before.
// Everything else about the package is unchanged: scripts still run in the
// package folder and the changelog is still written there, outside src.
//
// A commit that names the package by scope always addresses it, wherever its
// files are — src narrows the file-derived fallback, not the scope set.
func TestPackagesSrcNarrowsChangeDetection(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{"core": {Src: "lib"}}
	r.WriteConfigModel(cfg)
	r.WriteFile("packages/core/lib/parser.txt", "code\n")
	r.WriteFile("packages/core/docs/guide.md", "prose\n")
	r.SeedPackage("packages", "plain")
	r.Commit("feat(core,plain): first release of both")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.FileExists(t, r.Path("packages/core/CHANGELOG.md"),
		"the changelog is still written in the package folder, outside src")

	// A scopeless commit touching only what lies outside src releases nothing.
	r.WriteFile("packages/core/docs/guide.md", "more prose\n")
	r.Commit("fix: polish the guide")
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("core@"), "a change outside src is not a change to the package")

	// The same commit shape inside src does release it.
	r.WriteFile("packages/core/lib/parser.txt", "better code\n")
	r.Commit("fix: tighten the parser")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.1"), "a change inside src releases as before; tags: %v", r.TagList())

	// A package that declares no src keeps its whole folder.
	r.WriteFile("packages/plain/docs/guide.md", "prose\n")
	r.Commit("fix: document plain")
	r.ReleaseOK()
	assert.True(t, r.HasTag("plain@0.1.1"), "tags: %v", r.TagList())

	// Naming the package by scope reaches it wherever the files are.
	r.WriteFile("packages/core/docs/guide.md", "final prose\n")
	r.Commit("fix(core): the scope always addresses the package")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.2"), "tags: %v", r.TagList())
}

// TestPackagesSrcMustNameAFolder: a src that can never match narrows the
// package to nothing, which would stop it releasing without ever saying why.
// It fails the config load instead.
func TestPackagesSrcMustNameAFolder(t *testing.T) {
	for name, tc := range map[string]struct {
		src, want string
	}{
		"a folder that is not there": {"nope", "names no folder inside the package"},
		"a path leaving the package": {"../elsewhere", "leaves the package folder"},
		"the package folder itself":  {".", "is the package folder itself"},
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			cfg := libsConfig(echoBuild, 1)
			cfg.Packages = map[string]models.PackageConfig{"core": {Src: tc.src}}
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first release")

			res := r.Status()
			assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
			assert.Contains(t, res.Stdout+res.Stderr, tc.want)
		})
	}
}
