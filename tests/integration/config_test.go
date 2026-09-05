package integration

// Area 10: config loading, resolution and the options that only change
// something at runtime. Each test targets a behaviour a config-loading unit
// test cannot witness: which file the binary picks when no `--config` names
// one and how far it climbs to find it, a flag beating the file at *runtime*
// rather than in the parsed struct, a custom shell actually being invoked, an
// unknown key stopping the run instead of being ignored into a script-less
// release, a fused prerelease tag format written and read back across runs,
// the parser options changing what the commit log means, and the layers'
// rejections landing before any work is done.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestConfigUnknownKeyIsRejected: the decoder rejects unknown keys rather
// than silently ignoring them — the one config mistake that is
// otherwise invisible until a script that should have run never does. An
// unknown key is exactly the shape the typed model cannot express, so this
// one config is authored as a raw map[string]any.
func TestConfigUnknownKeyIsRejected(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigRaw(map[string]any{
		"scripts": map[string]any{"build": "echo building", "publish": "echo publishing"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "packages", "flow": map[string]any{"build": "build", "publish": "publish"}},
		},
		"conncurrency": 4,
	})
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Status()
	assert.Equal(t, 1, res.Code,
		"an unknown key must fail config loading, not be silently ignored\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	// What the operator needs from the failure is which key to go and fix, so
	// the misspelling itself has to reach the terminal. The wording around it
	// is not pinned here; the name is.
	assert.Contains(t, res.Stderr, "conncurrency",
		"the error names the key the file got wrong\nstderr:\n%s", res.Stderr)

	res = r.Status("--require-release")
	assert.Equal(t, 1, res.Code,
		"a broken configuration is exit 1, never the exit 3 of --require-release's clean-but-empty plan")
}

// TestConfigUnknownKeyInsideASpaceIsRejected: the same refusal one level down,
// where a mistyped key is easier to miss and its effect — a space that quietly
// keeps the defaults — is harder to attribute. The message names the key
// wherever in the file it sits.
func TestConfigUnknownKeyInsideASpaceIsRejected(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigRaw(map[string]any{
		"scripts": map[string]any{"build": "echo building", "publish": "echo publishing"},
		"spaces": map[string]any{
			"libs": map[string]any{
				"path":      "packages",
				"flow":      map[string]any{"build": "build", "publish": "publish"},
				"tagFromat": "v{version}",
			},
		},
	})
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Status()
	assert.Equal(t, 1, res.Code,
		"an unknown key inside a space fails the load too\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, strings.ToLower(res.Stderr), "tagfromat",
		"the error names the key\nstderr:\n%s", res.Stderr)
}

// TestConfigFileFallbackResolution: without --config the binary resolves the
// first of dispat.json, dispat.yaml, dispat.yml, dispat.toml that exists —
// the names `dispat init` writes under its formats — and with none present it
// fails with an error naming what it tried. JSON is valid YAML, so the typed
// model marshalled to JSON serves as the yaml-named config.
func TestConfigFileFallbackResolution(t *testing.T) {
	r := harness.New(t)
	data, err := json.MarshalIndent(libsConfig(echoBuild, 1), "", "  ")
	require.NoError(t, err)
	r.WriteFile("dispat.yaml", string(data))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Status()
	assert.Equal(t, 0, res.Code,
		"dispat.yaml must be found without --config\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	require.NoError(t, os.Remove(r.Path("dispat.yaml")))
	res = r.Status()
	assert.Equal(t, 1, res.Code, "no config file at all must fail the run")
	assert.Contains(t, res.Stderr, "no dispat config file found",
		"the error must say what is missing rather than fail obscurely")
}

// TestConfigConcurrencyFlagOverridesFile proves flag precedence at runtime,
// not just at parse time: the file pins a serial budget, --concurrency
// raises it, and only a real measured overlap tells the two apart.
func TestConfigConcurrencyFlagOverridesFile(t *testing.T) {
	names := packageNames(4, "pkg")
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(r.TsmarkScript("build.log", "$DISPAT_PACKAGE", 150*time.Millisecond), 1))
	seedIndependentPackages(r, names)

	r.ReleaseOK("--concurrency", fmt.Sprintf("%d,%d", len(names), len(names)))

	tl := r.Timeline("build.log")
	require.Len(t, tl, len(names))
	harness.AssertConcurrencyBudget(t, tl, len(names))
}

// TestConfigCustomShellIsUsed proves the "shell" option actually changes
// the interpreter scripts run under, not merely that it is accepted: a
// bashism invalid under the default /bin/sh -c must succeed once "shell"
// names bash.
func TestConfigCustomShellIsUsed(t *testing.T) {
	const bash = "/bin/bash"
	if _, err := os.Stat(bash); err != nil {
		t.Skip("bash not available at /bin/bash")
	}
	r := harness.New(t)
	cfg := libsConfig("arr=(a b c); echo ${arr[1]} > shellcheck.txt", 1)
	cfg.Shell = []string{bash, "-c"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	r.ReleaseOK()
	data, err := os.ReadFile(r.Path("packages", "core", "shellcheck.txt"))
	require.NoError(t, err)
	assert.Equal(t, "b\n", string(data))
}

// TestConfigNonPackageScopesReplacesDefault: setting nonPackageScopes
// *replaces* the built-in ["release"] default rather than extending it — an
// easy thing to assume the wrong way, since most config keys layer defaults
// underneath. The custom scope becomes exempt; "release" stops being one.
func TestConfigNonPackageScopesReplacesDefault(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.NonPackageScopes = []string{"infra"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")
	r.ReleaseOK()

	// The custom scope is exempt: a commit naming only it is inert, not an
	// unknown-package error.
	r.CommitEmpty("chore(infra): touch up the pipeline config")
	res := r.StatusOK()
	assert.False(t, harness.HasCode(res.Events, "E130"), "infra is exempt by explicit configuration")

	// "release" is no longer exempt, because setting the key replaced the
	// built-in default rather than adding to it.
	r.CommitEmpty("chore(release): pretend to be dispat's own commit")
	res = r.StatusOK()
	assert.True(t, harness.HasCode(res.Events, "E130"),
		"release must no longer be exempt once nonPackageScopes was set explicitly")
}

// TestConfigFusedPrereleaseTagFormatRoundTrips exercises a tagFormat fusing
// channel and counter with no separator — configuration.md documents the
// split at the letter/digit boundary ("beta0" -> "beta"/"0") — across three
// runs, so later runs must *read back* what earlier ones wrote rather than
// merely render it once. A second space overriding the repository format
// with the normative one pins that a tag format is a per-space property: the
// fused spelling must not bleed onto the other space's tags.
func TestConfigFusedPrereleaseTagFormatRoundTrips(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.TagFormat = "{name}@v{version}-{channel}{counter}"
	cfg.Spaces["apps"] = models.SpaceConfig{Path: models.PathList{"apps"}, Flow: buildPublish(),
		TagFormat: "{name}@{version}"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("apps", "web")
	r.Commit("feat(core)%beta: start the train\n---\nfeat(web)%beta: ride along in the other space")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@v0.1.0-beta0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("web@0.1.0-beta.0"),
		"the space override keeps the normative spelling; tags: %v", r.TagList())

	// A run with no new commits must read v0.1.0-beta0 back and find
	// nothing pending — the format round-trips, not just renders.
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("core@"), "a released prerelease must not re-release itself")
	assert.Equal(t, 1, r.TagCount("web@"), "both formats must round-trip")

	// A new commit continues the counter, which only works if the previous
	// run parsed "beta0" back into channel "beta", counter "0".
	r.CommitEmpty("fix(core): tweak")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@v0.1.0-beta1"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("web@"), "the other space had no new work")
}

// TestConfigParserOptions: the top-level `parser` object reconfigures the
// commit-message parser end to end. A custom type table makes `docs` release
// a patch, the configured default propagation depth carries it to the direct
// consumer with no caret written, strictTypes turns an unknown type into an
// error (E140) that commitErrors' default policy tolerates, and an invalid
// parser value fails the load rather than the first release.
func TestConfigParserOptions(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	cfg.Parser = &models.ParserConfig{
		Types:       map[string]string{"feat": "minor", "fix": "patch", "docs": "patch"},
		StrictTypes: true,
		Propagation: &models.ParserPropagationConfig{Depth: "1"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")

	// docs is release-worthy under the custom table, and the configured
	// default depth reaches app without a caret anywhere in the message.
	r.Commit("docs(core): documentation now ships")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.0.1"), "docs bumps patch under the custom table; tags: %v", r.TagList())
	assert.True(t, r.HasTag("app@0.0.1"),
		"the configured propagation depth carries the bump with no caret; tags: %v", r.TagList())

	// strictTypes: an unknown type is an error, not a shrug — reported, and
	// under the default commitErrors policy the unit just contributes nothing.
	r.CommitEmpty("wat(core): a type nobody declared")
	res := r.StatusOK()
	assert.True(t, harness.HasCode(res.Events, "E140"), "strictTypes raises E140 for the unknown type")
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("core@"), "the invalid unit releases nothing")

	// A bad parser value is a load error: exit 1 before any planning.
	bad := libsConfig(echoBuild, 1)
	bad.Parser = &models.ParserConfig{Types: map[string]string{"docs": "huge"}}
	r.WriteConfigModel(bad)
	badRes := r.Status()
	assert.Equal(t, 1, badRes.Code, "an invalid parser option must fail the load\nstdout:\n%s\nstderr:\n%s",
		badRes.Stdout, badRes.Stderr)
}

// TestConfigCommitErrorsPolicy: a commit whose scope names no package is a
// unit-scoped error (§16). Under the default "warn" the unit contributes
// nothing and the run releases the rest; under "error" the release is
// refused outright — while `status` still exits 0 either way, because seeing
// the plan is the point of the operation.
func TestConfigCommitErrorsPolicy(t *testing.T) {
	t.Run("warn", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): real work")
		r.CommitEmpty("fix(nosuch): typo in the scope")

		res := r.ReleaseOK()
		assert.True(t, harness.HasCode(res.Events, "E130"), "the diagnostic is reported even when tolerated")
		assert.True(t, r.HasTag("core@0.1.0"), "the sibling work still releases; tags: %v", r.TagList())
	})
	t.Run("error", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.CommitErrors = "error"
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): real work")
		r.CommitEmpty("fix(nosuch): typo in the scope")

		status := r.StatusOK()
		assert.True(t, harness.HasCode(status.Events, "E130"), "status reports the plan it would refuse")

		res := r.Release()
		require.Equal(t, 1, res.Code, "commitErrors=error must refuse the release\nstdout:\n%s", res.Stdout)
		assert.True(t, harness.HasCode(res.Events, "E130"))
		assert.Empty(t, r.TagList(), "nothing may be released under a refused plan")
	})
}

// TestConfigInitialsBaselines: the initials map seeds the baseline of a
// package whose latest tag is missing or unparseable. The unparseable-tag
// half is the subtle one: the pre-last tag must NOT be used — the version
// comes from initials while the pending window is still measured from the
// broken tag, so already released commits are not counted twice — and an
// initials key naming no discovered package only warns.
func TestConfigInitialsBaselines(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Initials = map[string]string{"core": "1.0.0", "ghost": "5.0.0"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")

	// A feat, then an unparseable tag over it, then a fix: only the fix is
	// pending, and it bumps on top of the configured 1.0.0.
	r.Commit("feat(core): released under the broken tag")
	r.Git("tag", "-a", "core@0.1.0.broken", "-m", "broken tag")
	r.CommitEmpty("fix(core): repair")

	res := r.ReleaseOK()
	assert.True(t, r.HasTag("core@1.0.1"),
		"initials 1.0.0 + only the fix since the unparseable tag; tags: %v", r.TagList())
	assert.Contains(t, res.Stdout, "baselineFromInitials")
	assert.Contains(t, res.Stdout, "initials entry matches no discovered package",
		"the ghost key warns instead of failing the run")

	// Converged: the new tag is parseable, initials no longer apply to core.
	r.CommitEmpty("fix(core): once more")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@1.0.2"), "the next bump reads the real tag back; tags: %v", r.TagList())
}

// TestConfigFormatsSmoke: one minimal end-to-end smoke per supported config
// format — the same monorepo releases through the binary under a
// dispat.json, a dispat.yaml and a dispat.toml (the binary's own starter).
// The json leg also pins that `status` reports without tagging. Everything
// deeper about the formats is the config loader's unit territory
// (TestLoadFormats there); resolution order is TestConfigFileFallbackResolution.
func TestConfigFormatsSmoke(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): first release")
		r.StatusOK()
		assert.Empty(t, r.TagList(), "status must not tag")
		r.ReleaseOK()
		assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	})
	t.Run("yaml", func(t *testing.T) {
		r := harness.New(t)
		data, err := json.MarshalIndent(libsConfig(echoBuild, 1), "", "  ")
		require.NoError(t, err)
		r.WriteFile("dispat.yaml", string(data)) // JSON is valid YAML
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first release")
		r.ReleaseOK()
		assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	})
	t.Run("toml", func(t *testing.T) {
		// The starter leaves GitHub at its enabled default; blank the env so
		// a CI runner's GITHUB_* variables can never reach the real API.
		t.Setenv("GITHUB_REPOSITORY", "")
		t.Setenv("GITHUB_TOKEN", "")
		r := harness.New(t)
		res := r.Command("init", "--format", "toml") // the binary's own TOML starter
		require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first release")
		r.ReleaseOK()
		assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	})
}

// TestConfigResolutionAscendsToTheMonorepoRoot: without --config, resolution
// climbs from --root through its parents until a config file is found, and
// the config's own directory becomes the effective monorepo root — so the
// CLI works from inside a package folder. The release invoked from
// packages/core must behave exactly as one invoked from the top: same tags,
// same changelog placement, converging second run included.
func TestConfigResolutionAscendsToTheMonorepoRoot(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): first release")

	res := r.CommandAt("packages/core", "status")
	require.Equal(t, 0, res.Code,
		"status must find the root config from inside the package\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Empty(t, r.TagList(), "status must not tag")

	res = r.CommandAt("packages/core", "release")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, r.HasTag("core@0.1.0"),
		"the tag lands in the monorepo's repository, not a nested one; tags: %v", r.TagList())
	assert.FileExists(t, r.Path("packages", "core", "CHANGELOG.md"),
		"the changelog lands under the resolved root")

	res = r.CommandAt("packages/core", "release")
	require.Equal(t, 0, res.Code, "a converged release from the subfolder is a clean no-op")
	assert.Equal(t, 1, r.TagCount("core@"))

	// An explicit --config stays anchored to --root: it must not ascend.
	res = r.CommandAt("packages/core", "status", "--config", "dispat.json")
	assert.Equal(t, 1, res.Code, "an explicit --config must fail from the subfolder, not fall back")
}

// TestConfigGitRepositoryGuard: a config without a git repository around it —
// and an `init` pointed outside one — both fail with one clear error before
// any work, instead of a raw git failure halfway through planning. This is
// the one test that must not use harness.New, which git-inits its repository;
// it drives the built binary against a bare temp directory instead.
func TestConfigGitRepositoryGuard(t *testing.T) {
	dispat, _ := harness.Build(t)
	root := t.TempDir() // a config, packages, but no .git
	data, err := json.MarshalIndent(libsConfig(echoBuild, 1), "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), data, 0o644))

	out, runErr := exec.Command(dispat, "status", "--root", root).CombinedOutput()
	require.Error(t, runErr, "status without a repository must fail\n%s", out)
	assert.Contains(t, string(out), "not a git repository root",
		"the guard names the problem instead of surfacing a raw git error")

	out, runErr = exec.Command(dispat, "init", "--root", t.TempDir()).CombinedOutput()
	require.Error(t, runErr, "init outside a repository root must fail\n%s", out)
	assert.Contains(t, string(out), "not a git repository root")
}

// TestConfigDispatexcludeSelectsTheConfigFile: a folder holding two config
// files says which one is real by naming the other in its .dispatexclude, and
// the rule holds at each of the three places a config file can sit — the
// repository root, a space folder and a package folder. Every choice is
// proved by a tag only that file's tagFormat could produce.
func TestConfigDispatexcludeSelectsTheConfigFile(t *testing.T) {
	writeYAML := func(r *harness.Repo, path string, value any) {
		t.Helper()
		data, err := json.MarshalIndent(value, "", "  ")
		require.NoError(t, err)
		r.WriteFile(path, string(data)) // JSON is valid YAML
	}

	t.Run("repository root", func(t *testing.T) {
		r := harness.New(t)
		decoy := libsConfig(echoBuild, 1)
		decoy.TagFormat = "json-{name}@{version}"
		r.WriteConfigModel(decoy)
		real := libsConfig(echoBuild, 1)
		real.TagFormat = "yaml-{name}@{version}"
		writeYAML(r, "dispat.yaml", real)
		r.WriteFile(".dispatexclude", "# the json file is generated\ndispat.json\n")
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first release")

		r.ReleaseOK()
		assert.True(t, r.HasTag("yaml-core@0.1.0"), "tags: %v", r.TagList())
	})

	t.Run("space folder", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(libsConfig(echoBuild, 1))
		r.SeedPackage("packages", "core")
		spaceFile(t, r, "packages", models.SpaceFile{TagFormat: "json-{name}@{version}"})
		writeYAML(r, "packages/dispat.yaml", models.SpaceFile{TagFormat: "yaml-{name}@{version}"})
		r.WriteFile("packages/.dispatexclude", "dispat.json\n")
		r.Commit("feat(core): first release")

		r.ReleaseOK()
		assert.True(t, r.HasTag("yaml-core@0.1.0"), "tags: %v", r.TagList())
	})

	t.Run("package folder", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(libsConfig(echoBuild, 1))
		r.SeedPackage("packages", "core")
		packageFile(t, r, "packages/core", models.PackageConfig{TagFormat: "json-{name}@{version}"})
		writeYAML(r, "packages/core/dispat.yaml", models.PackageConfig{TagFormat: "yaml-{name}@{version}"})
		r.WriteFile("packages/core/.dispatexclude", "dispat.json\n")
		r.Commit("feat(core): first release")

		r.ReleaseOK()
		assert.True(t, r.HasTag("yaml-core@0.1.0"), "tags: %v", r.TagList())
	})
}

// TestConfigResolutionAscendsPastASpaceFile: a space folder's file declares
// packages, which is also what a monorepo of standalone packages declares.
// Run from inside such a space — and from the space folder itself — the CLI
// must still resolve to the root above, because that root claims the folder.
func TestConfigResolutionAscendsPastASpaceFile(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	spaceFile(t, r, "packages", models.SpaceFile{
		Packages: map[string]models.PackageConfig{"core": {TagFormat: "space-{name}@{version}"}},
	})
	r.Commit("feat(core): first release")

	for _, from := range []string{"packages/core", "packages"} {
		res := r.CommandAt(from, "status")
		require.Equal(t, 0, res.Code,
			"status from %s must find the root config\nstdout:\n%s\nstderr:\n%s", from, res.Stdout, res.Stderr)
	}

	res := r.CommandAt("packages/core", "release")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, r.HasTag("space-core@0.1.0"),
		"the space file's entry applied and the tag landed in the monorepo's repository: %v", r.TagList())
}

// TestConfigSpaceLayerRejections: what the new layers may not say, each one
// failing the load before any work rather than being half-applied.
func TestConfigSpaceLayerRejections(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(r *harness.Repo)
		want  string
	}{
		{
			name: "a space packages entry cannot set path",
			setup: func(r *harness.Repo) {
				cfg := libsConfig(echoBuild, 1)
				cfg.Spaces = map[string]models.SpaceConfig{
					"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), Packages: map[string]models.PackageConfig{
						"core": {Path: "elsewhere"},
					}},
				}
				r.WriteConfigModel(cfg)
			},
			want: "path cannot be set",
		},
		{
			name: "a space file cannot set path",
			setup: func(r *harness.Repo) {
				r.WriteConfigModel(libsConfig(echoBuild, 1))
				r.WriteFile("packages/dispat.json", `{"path": "elsewhere"}`)
			},
			want: "path cannot be set in a space folder's config file",
		},
		{
			name: "a space file cannot declare spaces",
			setup: func(r *harness.Repo) {
				r.WriteConfigModel(libsConfig(echoBuild, 1))
				r.WriteFile("packages/dispat.json", `{"spaces": {"inner": {"path": "pkgs"}}}`)
			},
			want: "monorepo root of its own",
		},
		{
			name: "a package entry cannot hold packages",
			setup: func(r *harness.Repo) {
				r.WriteConfigRaw(map[string]any{
					"scripts": map[string]any{"build": echoBuild, "publish": "echo publishing"},
					"spaces": map[string]any{"libs": map[string]any{"path": "packages",
						"flow": map[string]any{"build": "build", "publish": "publish"}}},
					"packages": map[string]any{
						"core": map[string]any{"packages": map[string]any{"inner": map[string]any{}}},
					},
				})
			},
			want: "cannot be set on a package entry",
		},
		{
			name: "a space packages key must match a folder of that space",
			setup: func(r *harness.Repo) {
				cfg := libsConfig(echoBuild, 1)
				cfg.Spaces = map[string]models.SpaceConfig{
					"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), Packages: map[string]models.PackageConfig{
						"ghost": {TagFormat: "v{version}"},
					}},
				}
				r.WriteConfigModel(cfg)
			},
			// The error travels as a JSON log field, so the assertion stays
			// clear of the quotes the encoder escapes.
			want: "matches no folder of space",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := harness.New(t)
			r.SeedPackage("packages", "core")
			tc.setup(r)
			r.Commit("feat(core): first release")

			res := r.Status()
			require.Equal(t, 1, res.Code,
				"the config must be refused\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, tc.want)
			assert.Empty(t, r.TagList(), "nothing may be released from a refused config")
		})
	}
}

// TestConfigParserQuiet: parser.quiet hides the commit-message parser's own
// findings from the log for a repository whose history predates the
// convention. It hides lines and nothing else — the diagnostics are still
// counted, the summary says how many went unprinted, a hidden error still
// refuses the release under commitErrors "error", and the planner's own
// diagnostics are never hidden, because they explain release outcomes a
// reader of the commit log cannot account for.
func TestConfigParserQuiet(t *testing.T) {
	// seed returns a repository whose history carries one parser error (an
	// unknown type under strictTypes, E140) and one planner error (a scope
	// naming no package, E130), plus real work so a plan exists.
	seed := func(t *testing.T, quiet bool, commitErrors string) *harness.Repo {
		t.Helper()
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.CommitErrors = commitErrors
		cfg.Parser = &models.ParserConfig{
			Quiet:       quiet,
			StrictTypes: true,
			Types:       map[string]string{"feat": "minor", "fix": "patch"},
		}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): real work")
		r.CommitEmpty("wat(core): a type nobody declared")
		r.CommitEmpty("fix(nosuch): a scope naming no package")
		return r
	}

	loud := seed(t, false, "").StatusOK()
	require.True(t, harness.HasCode(loud.Events, "E140"), "the parser finding prints by default")
	require.True(t, harness.HasCode(loud.Events, "E130"), "so does the planner's")

	quiet := seed(t, true, "").StatusOK()
	assert.False(t, harness.HasCode(quiet.Events, "E140"), "parser.quiet hides the parser's finding")
	assert.True(t, harness.HasCode(quiet.Events, "E130"),
		"a planner finding explains a release outcome and is never hidden")
	assert.Contains(t, quiet.Stdout, `"hidden":1`,
		"a hidden diagnostic is still counted, and the count says how many")

	// The flag overrides the config in both directions.
	r := seed(t, true, "")
	shown := r.Command("status", "--quiet-parser=false")
	require.Equal(t, 0, shown.Code, "stderr:\n%s", shown.Stderr)
	assert.True(t, harness.HasCode(shown.Events, "E140"), "--quiet-parser=false brings the findings back")

	r = seed(t, false, "")
	hushed := r.Command("status", "--quiet-parser")
	require.Equal(t, 0, hushed.Code, "stderr:\n%s", hushed.Stderr)
	assert.False(t, harness.HasCode(hushed.Events, "E140"), "--quiet-parser hides them for one invocation")

	// Display only: a hidden error still refuses the release.
	blocked := seed(t, true, "error").Release()
	assert.Equal(t, 1, blocked.Code, "a hidden error still blocks under commitErrors=error")
	assert.False(t, harness.HasCode(blocked.Events, "E140"))
	assert.Contains(t, blocked.Stdout, "refusing to release",
		"the refusal itself is never hidden, or the run would stop for no stated reason")
}

// TestConfigCustomObjectIsIgnored: `custom` is a free-form object at every
// level that dispat never reads. It exists so a repository's own tooling can
// keep data in the config file without tripping the unknown-key guard, so the
// whole claim is that a release behaves exactly as if it were absent.
func TestConfigCustomObjectIsIgnored(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Custom = map[string]any{"team": "platform", "budget": 3, "nested": map[string]any{"a": true}}
	libs := cfg.Spaces["libs"]
	libs.Custom = map[string]any{"owner": "libs-team"}
	cfg.Spaces["libs"] = libs
	cfg.Packages = map[string]models.PackageConfig{"core": {Custom: map[string]any{"tier": "one"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): custom data")

	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
}

// TestConfigRefSplitsTheFile: a configuration split across files with `$ref`
// releases exactly as the same configuration written in one file. The
// fragments are deliberately awkward — one JSON, one YAML, one a folder down
// referencing a fourth beside itself — because the claim is that where the
// text lives changes nothing at all, and that a path inside a fragment still
// means what it would have meant inline.
func TestConfigRefSplitsTheFile(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("cfg/scripts.yaml", "build: echo building\npublish: echo publishing\n")
	r.WriteFile("cfg/flow.json", `{"build": ["build"], "publish": ["publish"]}`)
	r.WriteFile("cfg/spaces.json", `{"libs": {"path": "packages", "flow": {"$ref": "./flow.json"}}}`)
	r.WriteConfigRaw(map[string]any{
		"logLevel":    "info",
		"logFormat":   "json",
		"github":      map[string]any{"enabled": false},
		"updateCheck": false,
		"scripts":     map[string]any{"$ref": "./cfg/scripts.yaml"},
		"spaces":      map[string]any{"$ref": "./cfg/spaces.json"},
	})
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.Contains(t, res.Stdout, "building", "the referenced scripts ran")

	// The files it was made of are on the record, which is how a split
	// configuration answers "where did that come from".
	trace := r.Status("--log-level", "trace")
	require.Equal(t, 0, trace.Code, "stderr:\n%s", trace.Stderr)
	for _, file := range []string{"dispat.json", "cfg/scripts.yaml", "cfg/spaces.json", "cfg/flow.json"} {
		assert.Contains(t, trace.Stdout, file, "every file read is traced")
	}
}

// TestConfigRefCycleFailsBeforeAnyWork: a reference that reaches its own file
// again is refused with the path it took, and the run stops where every
// configuration error stops — before a tag, a commit or a script.
func TestConfigRefCycleFailsBeforeAnyWork(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("cfg/spaces.json", `{"libs": {"path": "packages", "flow": {"$ref": "../dispat.json"}}}`)
	r.WriteConfigRaw(map[string]any{
		"scripts": map[string]any{"build": "echo building > built.txt"},
		"spaces":  map[string]any{"$ref": "./cfg/spaces.json"},
	})
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Release()
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stderr, "$ref cycle")
	assert.Contains(t, res.Stderr, "a file cannot reference itself")
	assert.Empty(t, r.TagList(), "nothing released")
	assert.NoFileExists(t, r.Path("built.txt"), "no script ran")
}

// TestConfigRefMissingFragmentIsNamed: the everyday mistake — a fragment that
// moved or was never committed — names the file that pointed at it, the key
// that did, and what was missing.
func TestConfigRefMissingFragmentIsNamed(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigRaw(map[string]any{
		"scripts": map[string]any{"build": "echo building"},
		"spaces":  map[string]any{"$ref": "./cfg/spaces.json"},
	})
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Status()
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stderr, "spaces: $ref", "the key that pointed at it")
	assert.Contains(t, res.Stderr, "cfg/spaces.json", "and what it pointed at")
	assert.Contains(t, res.Stderr, "cannot read")
}

// TestConfigNamesKeepTheirCaseEndToEnd: the whole chain a name travels, for a
// configuration that writes its names with capitals.
//
// A map key keeps the case its file wrote, and matching folds instead. That
// makes the name the author chose the name everything downstream reports:
// the package, its synthetic space, the tag it publishes under, the DISPAT_*
// variables its scripts read, and the selectors that address it — from a
// command line, from a commit scope, from an autoVersion.only list — all of
// which may spell it any way at all.
func TestConfigNamesKeepTheirCaseEndToEnd(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"Build":   {"env > \"../../$DISPAT_PACKAGE.env\""},
		"publish": {"echo publishing"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"Libs": {Path: models.PathList{"packages"}, Flow: &models.SpaceFlowConfig{
			Build: []string{"build"}, Publish: []string{"publish"}}},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"MyLib": {Path: "tools/mylib", Flow: &models.SpaceFlowConfig{Build: []string{"BUILD"}},
			TagFormat: "{name}@{version}"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "Core")
	r.WriteFile("tools/mylib/main.txt", "mylib\n")
	r.Commit("feat(core,mylib): two packages spelled with capitals")

	// A selector spelled another way still addresses the package, and the plan
	// reports it under its own name.
	res := r.Command("status", "--package", "mylib")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, `"releasing":["MyLib"]`,
		"the selection resolved to the package, reported under its own spelling")
	assert.Contains(t, res.Stdout, `"package":"Core","space":"Libs"`,
		"and the space is reported as the config spells it")

	r.ReleaseOK()

	// The tags carry the names the config wrote, not folded ones.
	assert.True(t, r.HasTag("Core@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("MyLib@0.1.0"), "tags: %v", r.TagList())

	// The environment a script runs with reports the same names. The build
	// script is declared as `Build` and referenced as `build` and `BUILD`, so
	// the flow entries resolved through the fold on their way here.
	env, err := os.ReadFile(r.Path("Core.env"))
	require.NoError(t, err, "the script's own $DISPAT_PACKAGE named the file")
	text := string(env)
	assert.Contains(t, text, "DISPAT_PACKAGE=Core")
	assert.Contains(t, text, "DISPAT_SPACE=Libs")
	assert.Contains(t, text, "DISPAT_WORKSPACE_MYLIB_NAME=MyLib",
		"the workspace listing uppercases the variable name and keeps the package's own")

	env, err = os.ReadFile(r.Path("MyLib.env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "DISPAT_PACKAGE=MyLib")
	assert.Contains(t, string(env), "DISPAT_SPACE=MyLib", "a standalone package is its own space")
	assert.Contains(t, string(env), "DISPAT_TAG=MyLib@0.1.0")

	// A commit scope addresses it the same way.
	r.CommitEmpty("fix(MYLIB): addressed by a third spelling")
	r.ReleaseOK()
	assert.True(t, r.HasTag("MyLib@0.1.1"), "tags: %v", r.TagList())
	assert.False(t, r.HasTag("Core@0.1.1"), "and nothing else was dragged along")
}

// TestConfigRefusesTwoSpellingsOfOneName: the other half of the rule at the
// process boundary. Two keys of one object that fold together have no lookup
// that could choose between them, so the load says so and nothing runs.
func TestConfigRefusesTwoSpellingsOfOneName(t *testing.T) {
	r := harness.New(t)
	// Two spellings of one script name cannot be authored through the typed
	// model, which is a Go map: the raw JSON is the only way to write them.
	r.WriteConfig(`{
  "logLevel": "info",
  "logFormat": "json",
  "scripts": {"build": "echo one", "Build": "echo two"},
  "spaces": {"libs": {"path": "packages", "flow": {"build": ["build"]}}}
}`)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")

	res := r.Release()
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout+res.Stderr, "collide case-insensitively")
	assert.Empty(t, r.TagList(), "nothing ran")
}
