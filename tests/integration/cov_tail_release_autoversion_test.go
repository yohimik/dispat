// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Long-tail coverage for native auto-versioning: the manifests it cannot read,
// the names it refuses to derive anything from, the selectors that decide a
// declaration is none of its business, and the spellings a range takes in an
// ecosystem that has no caret. Every scenario reads the files back off disk,
// because "the rewrite was narrowed" and "the rewrite silently did nothing"
// look the same in a log.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covTailAVSpace is the libs space with one autoVersion block and nothing
// else: every scenario here differs only in that block and in the manifests
// on disk.
func covTailAVSpace(av *models.AutoVersionConfig) models.SpaceConfig {
	return models.SpaceConfig{Path: models.PathList{"packages"}, Flow: buildPublish(), AutoVersion: av}
}

// covTailReadFile is the manifest as the run left it.
func covTailReadFile(t *testing.T, r *harness.Repo, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(r.Path(parts...))
	require.NoError(t, err)
	return string(data)
}

// TestCovTailAutoVersionReportsManifestsItCannotParse: a manifest that does
// not parse is missing from the name index every later reconciliation reads,
// so a consumer naming that package could silently go unversioned. It is a
// warning rather than a debug line for that reason, said once where the index
// is built and once where the package's own files are reconciled, and the
// manifests that did parse are still rewritten.
func TestCovTailAutoVersionReportsManifestsItCannotParse(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = covTailAVSpace(&models.AutoVersionConfig{Manifests: "all"})
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	// web's root manifest is not JSON at all: the identity of the package is
	// what the index loses, and the nested one still has to be reconciled.
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web", "version":`)
	r.WriteFile("packages/web/tools/package.json",
		`{"name": "@acme/web-tools", "version": "0.0.0", "dependencies": {"@acme/core": "^0.0.1"}}`)
	r.Commit("feat(core,web): bootstrap")

	res := r.ReleaseOK()
	assert.Contains(t, res.Stdout, "root manifest failed to parse",
		"the index says which package it lost an identity for")
	assert.Contains(t, res.Stdout, "some manifests failed to parse",
		"and the package's own reconciliation says it read a partial scan")

	assert.Contains(t, covTailReadFile(t, r, "packages", "web", "tools", "package.json"),
		`"@acme/core": "^0.1.0"`, "the manifests that did parse are still reconciled")
	assert.True(t, r.IsTagged("web@0.1.0"), "and an unreadable manifest is not a failed release; tags: %v", r.TagList())
}

// TestCovTailAutoVersionDerivesNothingFromAnAmbiguousName: two packages
// declaring one manifest name make that name answer to nothing, because
// rewriting a range for it would pick one of them arbitrarily. The name is
// reported (W220) and the declaration naming it is left exactly as written.
func TestCovTailAutoVersionDerivesNothingFromAnAmbiguousName(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = covTailAVSpace(&models.AutoVersionConfig{Enabled: models.Bool(true)})
	r.WriteConfigModel(cfg)
	for _, name := range []string{"one", "two"} {
		r.SeedPackage("packages", name)
		// One manifest identity, two packages behind it.
		r.WriteFile("packages/"+name+"/package.json", `{"name": "@acme/shared", "version": "0.0.0"}`)
	}
	r.SeedPackage("packages", "app")
	r.WriteFile("packages/app/package.json",
		`{"name": "@acme/app", "version": "0.0.0", "dependencies": {"@acme/shared": "workspace:*"}}`)
	r.Commit("feat(one,two,app): bootstrap")

	res := r.ReleaseOK()
	assert.True(t, harness.IsCodePresent(res.Events, "W220"), "stdout:\n%s", res.Stdout)
	assert.Contains(t, covTailReadFile(t, r, "packages", "app", "package.json"),
		`"@acme/shared": "workspace:*"`,
		"a name answering to two packages answers to neither")
	assert.Contains(t, covTailReadFile(t, r, "packages", "app", "package.json"),
		`"version": "0.1.0"`, "the package's own version is not an ambiguous name")
}

// TestCovTailAutoVersionSelectorsNarrowTheRewrite: the three selectors each
// leave a declaration alone for a different reason — the field it sits in is
// not one of the configured kinds, the provider is not one of the configured
// names, or the range as written is not one the match globs claim. One
// manifest carries all three next to a declaration nothing narrows, so the
// rewrite that does happen proves the others were narrowed rather than broken.
func TestCovTailAutoVersionSelectorsNarrowTheRewrite(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = covTailAVSpace(&models.AutoVersionConfig{
		Kinds: []string{"dependencies"},
		Only:  []string{"core", "extra"},
		Match: []string{"workspace:*"},
	})
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "web", Provider: "core"},
		{Consumer: "web", Provider: "extra"},
		{Consumer: "web", Provider: "tools", Kind: "devDependencies"},
		{Consumer: "web", Provider: "aside"},
	}
	r.WriteConfigModel(cfg)
	for _, name := range []string{"core", "extra", "tools", "aside"} {
		r.SeedPackage("packages", name)
		r.WriteFile("packages/"+name+"/package.json",
			`{"name": "@acme/`+name+`", "version": "0.0.0"}`)
	}
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json", `{
  "name": "@acme/web",
  "version": "0.0.0",
  "dependencies": {
    "@acme/core": "workspace:*",
    "@acme/extra": "1.0.0",
    "@acme/aside": "workspace:*"
  },
  "devDependencies": {"@acme/tools": "workspace:*"}
}`)
	r.Commit("feat(core,extra,tools,aside,web): bootstrap")

	r.ReleaseOK()
	web := covTailReadFile(t, r, "packages", "web", "package.json")
	assert.Contains(t, web, `"@acme/core": "^0.1.0"`, "the declaration no selector narrows is rewritten")
	assert.Contains(t, web, `"@acme/extra": "1.0.0"`,
		"a range the match globs do not claim is a hand pin the policy protects")
	assert.Contains(t, web, `"@acme/aside": "workspace:*"`,
		"a provider outside `only` is none of this block's business")
	assert.Contains(t, web, `"@acme/tools": "workspace:*"`,
		"and a field outside `kinds` is not rewritten wherever the provider is listed")
}

// TestCovTailAutoVersionResolvesAProviderByItsDeclaredPath: a declaration
// naming a package by a name no manifest in the workspace carries is still a
// workspace edge when it points at the folder with a file: range. That is how
// a workspace whose declared names and folder names disagree is reconciled at
// all, and the replace strategy resolves the same declaration the same way.
func TestCovTailAutoVersionResolvesAProviderByItsDeclaredPath(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = covTailAVSpace(&models.AutoVersionConfig{Range: "exact"})
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	// "the-core" is a name nothing in the workspace declares; the path is
	// what says which package it is.
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"the-core": "file:../core"}}`)
	r.Commit("feat(core,web): bootstrap")

	r.ReleaseOK()
	assert.Contains(t, covTailReadFile(t, r, "packages", "web", "package.json"),
		`"the-core": "0.1.0"`, "the declared path named the provider the declared name did not")
}

// TestCovTailAutoVersionOnlyUpdatedLeavesTheRestBehind: `--only-updated` is
// the flag of a job wired to run after every commit — it asks for this run's
// updates alone, so a range that had fallen behind a provider released in an
// earlier run stays behind rather than quietly catching up, and a replace rule
// scoped to such a provider expands into nothing. Without the flag the same
// fixture catches both up, which is what proves the flag is doing the
// narrowing.
func TestCovTailAutoVersionOnlyUpdatedLeavesTheRestBehind(t *testing.T) {
	fixture := func(t *testing.T) *harness.Repo {
		t.Helper()
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Spaces["libs"] = covTailAVSpace(&models.AutoVersionConfig{
			WriteVersion: models.Bool(false),
			Replace: []models.AutoVersionReplaceConfig{{
				Files: []string{"README.md"},
				Find:  "{provider}: pinned",
				Write: "{provider}: {providerVersion}",
			}},
		})
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("packages", "web")
		r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.1.0"}`)
		r.WriteFile("packages/web/package.json",
			`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "^0.0.1"}}`)
		r.WriteFile("packages/web/README.md", "core: pinned\n")
		r.Commit("feat(core,web): bootstrap")
		// core is already released at this commit and has nothing pending;
		// web's files still name the version before it, which is the "fallen
		// behind" state both runs below start from.
		r.Git("tag", "core@0.1.0")
		return r
	}

	t.Run("the flag keeps a run to its own updates", func(t *testing.T) {
		r := fixture(t)
		res := r.Command("autoversion", "--only-updated", "--since", "all")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, covTailReadFile(t, r, "packages", "web", "package.json"),
			`"@acme/core": "^0.0.1"`, "no provider this run updates, so no range moves")
		assert.Equal(t, "core: pinned\n", covTailReadFile(t, r, "packages", "web", "README.md"),
			"and a rule scoped to such a provider expands into nothing")
	})

	t.Run("without it the same run catches both up", func(t *testing.T) {
		r := fixture(t)
		res := r.Command("autoversion", "--since", "all")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, covTailReadFile(t, r, "packages", "web", "package.json"),
			`"@acme/core": "^0.1.0"`, "the catch-up is what the flag was turning off")
		assert.Equal(t, "core: 0.1.0\n", covTailReadFile(t, r, "packages", "web", "README.md"))
	})
}

// TestCovTailAutoVersionRangePolicySpellsEachEcosystem: the keyword policies
// are npm's, and an ecosystem that has no caret cannot be handed one. Python
// pins with ==, and a policy that is neither a keyword nor a {version}
// template is written through verbatim, which is how a workspace protocol
// survives a reconciliation that is otherwise about versions.
func TestCovTailAutoVersionRangePolicySpellsEachEcosystem(t *testing.T) {
	t.Run("a python specifier pins whatever keyword was asked for", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Spaces["libs"] = covTailAVSpace(&models.AutoVersionConfig{Range: "caret"})
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "lib"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "lib")
		r.SeedPackage("packages", "app")
		r.WriteFile("packages/lib/pyproject.toml", "[project]\nname = \"acme-lib\"\nversion = \"0.0.0\"\n")
		r.WriteFile("packages/app/pyproject.toml",
			"[project]\nname = \"acme-app\"\nversion = \"0.0.0\"\ndependencies = [\"acme-lib==0.0.1\"]\n")
		r.Commit("feat(lib,app): bootstrap")

		r.ReleaseOK()
		app := covTailReadFile(t, r, "packages", "app", "pyproject.toml")
		assert.Contains(t, app, "acme-lib==0.1.0", "a caret is not a thing a specifier can carry")
		assert.Contains(t, app, `version = "0.1.0"`, "and the package's own version still advances")
	})

	t.Run("a literal policy is written through as it stands", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Spaces["libs"] = covTailAVSpace(&models.AutoVersionConfig{Range: "workspace:^"})
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("packages", "web")
		r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
		r.WriteFile("packages/web/package.json",
			`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
		r.Commit("feat(core,web): bootstrap")

		r.ReleaseOK()
		assert.Contains(t, covTailReadFile(t, r, "packages", "web", "package.json"),
			`"@acme/core": "workspace:^"`,
			"a policy naming no version is a protocol, not a range to compute")
	})
}
