package integration

// Goal 36: the npm distribution shares the CLI's major/minor line, while
// its own patches and the version of the binary it installs remain distinct.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// npmDistributionRepo models the production publication boundary without a
// network destination: the provider's publish writes a receipt, which the
// npm build must read before recording its immutable binary version.
func npmDistributionConfig() models.File {
	cfg := harness.BaseFile(4, 2)
	cfg.Initials = map[string]string{"dispat": "1.10.0", "cli": "1.10.0"}
	cfg.VersionGroups = map[string]models.VersionGroupConfig{
		"cli": {Versioning: models.VersioningFixedMajorMinor},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"services": {
			Path: models.PathList{"services"}, VersionGroup: "cli",
			IsBuildWaitingPublish: models.Bool(true), Flow: buildPublish(),
			TagFormat: "services/{name}/v{version}",
			Scripts: map[string]models.Script{
				"build":   {"echo build"},
				"publish": {`test ! -f ../../fail-provider && mkdir -p ../../published && printf '%s' "$DISPAT_NEW_VERSION" > ../../published/version`},
			},
		},
		"packages": {
			Path: models.PathList{"packages"}, VersionGroup: "cli", Flow: buildPublish(),
			TagFormat:   "packages/{name}/v{version}",
			AutoVersion: &models.AutoVersionConfig{Enabled: models.Bool(true)},
			Scripts: map[string]models.Script{
				"build":   {`test "$(cat ../../published/version)" = "$DISPAT_WORKSPACE_DISPAT_VERSION" && printf '%s' "$DISPAT_WORKSPACE_DISPAT_VERSION" > binary-version.txt`},
				"publish": {`test ! -f ../../fail-npm`},
			},
		},
	}
	cfg.Dependencies = models.Dependencies{{Consumer: "cli", Provider: "dispat", Keep: true}}
	return cfg
}

func npmDistributionRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(npmDistributionConfig())
	r.WriteFile(".gitignore", "published/\nfail-provider\nfail-npm\n")
	r.SeedPackage("services", "dispat")
	r.WriteFile("packages/cli/package.json", `{"name":"@dispat/bin","version":"1.10.0"}`+"\n")
	r.Commit("fix(dispat,cli): prepare npm distribution")
	return r
}

func assertNPMDistribution(t *testing.T, r *harness.Repo, npmVersion, binaryVersion string) {
	t.Helper()
	data, err := os.ReadFile(r.Path("packages", "cli", "package.json"))
	require.NoError(t, err)
	var manifest struct{ Version string }
	require.NoError(t, json.Unmarshal(data, &manifest))
	assert.Equal(t, npmVersion, manifest.Version)
	data, err = os.ReadFile(r.Path("packages", "cli", "binary-version.txt"))
	require.NoError(t, err)
	assert.Equal(t, binaryVersion, string(data))
	assert.True(t, r.HasTag("packages/cli/v"+npmVersion), "tags: %v", r.TagList())
}

func TestNPMDistributionKeepsIndependentPatchesAndPinnedBinary(t *testing.T) {
	r := npmDistributionRepo(t)
	r.ReleaseOK()
	assertNPMDistribution(t, r, "1.10.1", "1.10.1")

	r.CommitEmpty("fix(cli): repair npm installation")
	r.ReleaseOK()
	assertNPMDistribution(t, r, "1.10.2", "1.10.1")
	assert.Equal(t, 1, r.TagCount("services/dispat/v"), "an npm patch must not release the provider")

	// A dependency orders work; CCME propagation selects its consumers.
	r.CommitEmpty("fix(dispat)^: repair the binary")
	r.ReleaseOK()
	assertNPMDistribution(t, r, "1.10.3", "1.10.2")
	assert.True(t, r.HasTag("services/dispat/v1.10.2"))

	r.CommitEmpty("feat(dispat): advance the shared minor")
	r.ReleaseOK()
	assertNPMDistribution(t, r, "1.11.0", "1.11.0")
	assert.True(t, r.HasTag("services/dispat/v1.11.0"))
	tags := r.TagList()
	r.ReleaseOK()
	assert.Equal(t, tags, r.TagList(), "a completed release converges")
}

func TestNPMDistributionWaitsForPublicationAndRetriesItsOwnFailure(t *testing.T) {
	r := npmDistributionRepo(t)
	r.WriteFile("fail-provider", "fail\n")
	res := r.Release()
	require.NotZero(t, res.Code)
	assert.Zero(t, r.TagCount("packages/cli/v"))
	_, err := os.Stat(r.Path("packages", "cli", "binary-version.txt"))
	assert.True(t, os.IsNotExist(err), "npm build must not run before the provider publishes")

	r.Remove("fail-provider")
	r.WriteFile("fail-npm", "fail\n")
	res = r.Release()
	require.NotZero(t, res.Code)
	assert.True(t, r.HasTag("services/dispat/v1.10.1"), "provider publication survives npm failure")
	assert.Zero(t, r.TagCount("packages/cli/v"))

	r.Remove("fail-npm")
	r.ReleaseOK()
	assertNPMDistribution(t, r, "1.10.1", "1.10.1")
	assert.Equal(t, 1, r.TagCount("services/dispat/v"), "retry must not republish the provider")
}

func TestNPMDistributionPinsPrereleaseAndGraduatedBinaries(t *testing.T) {
	r := npmDistributionRepo(t)
	r.ReleaseOK()
	r.CommitEmpty("feat(dispat)^%beta++1: begin a shared beta")
	r.ReleaseOK()
	assertNPMDistribution(t, r, "1.11.0-beta.0", "1.11.0-beta.0")
	assert.True(t, r.HasTag("services/dispat/v1.11.0-beta.0"))

	r.CommitEmpty("fix(dispat)%beta>stable: graduate the shared beta")
	r.ReleaseOK()
	assertNPMDistribution(t, r, "1.11.0", "1.11.0")
	assert.True(t, r.HasTag("services/dispat/v1.11.0"))
}

// A newly discovered distribution must not replay releases from before it
// existed. The package-only cancellation establishes that history boundary;
// the following source record supplies the first npm release intent.
func TestNPMDistributionStartsOnExistingNativeLine(t *testing.T) {
	r := harness.New(t)
	cfg := npmDistributionConfig()
	npmSpace := cfg.Spaces["packages"]
	delete(cfg.Spaces, "packages")
	cfg.Dependencies = nil
	cfg.Initials["dispat"] = "1.9.0"
	r.WriteConfigModel(cfg)
	r.WriteFile(".gitignore", "published/\n")
	r.SeedPackage("services", "dispat")
	r.Commit("feat(dispat)^minor: publish the native line")
	r.ReleaseOK()
	require.True(t, r.HasTag("services/dispat/v1.10.0"))

	cfg.Spaces["packages"] = npmSpace
	cfg.Dependencies = models.Dependencies{{Consumer: "cli", Provider: "dispat", Keep: true}}
	r.WriteConfigModel(cfg)
	r.WriteFile("packages/cli/package.json", `{"name":"@dispat/bin","version":"1.10.0"}`+"\n")
	r.Commit("chore(cli): introduce npm distribution")
	assert.Contains(t, r.StatusOK().Stdout, "1.11.0", "old minor propagation is pending for the newly added consumer")

	r.CommitEmpty("cancel(cli): establish npm baseline")
	r.CommitEmpty("fix(cli): distribute the native CLI")
	r.ReleaseOK()
	assertNPMDistribution(t, r, "1.10.1", "1.10.0")
	assert.Equal(t, 1, r.TagCount("services/dispat/v"), "introducing npm must not republish the provider")
}

// Exact npm release intent must work without a fictitious baseline tag,
// including a fresh version after an unrecorded publication was unpublished.
// Maintenance corrections must also remove the native release they replace.
func TestNPMDistributionFirstReleaseUsesExactVersionAndChoreCorrections(t *testing.T) {
	for _, version := range []string{"1.10.0", "1.10.1"} {
		t.Run(version, func(t *testing.T) {
			r := harness.New(t)
			cfg := npmDistributionConfig()
			cfg.Initials["cli"] = "0.0.0"
			cfg.Initials["docs"] = "1.10.9"
			r.WriteConfigModel(cfg)
			r.WriteFile(".gitignore", "published/\nfail-provider\n")
			r.SeedPackage("services", "dispat")
			r.WriteFile("packages/cli/package.json", `{"name":"@dispat/bin","version":"1.10.0"}`+"\n")
			r.WriteFile("packages/docs/package.json", `{"name":"dispat-docs","version":"1.10.9"}`+"\n")
			r.Commit("chore: establish published baselines")
			r.Git("tag", "services/dispat/v1.10.0")
			r.Git("tag", "packages/docs/v1.10.9")
			r.WriteFile("published/version", "1.10.0")
			r.WriteFile("fail-provider", "native publication must not run\n")
			r.CommitEmpty("fix(dispat,cli,docs)^: supporting repairs")
			incorrect := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
			r.CommitEmpty("chore: correct supporting intent\n\nEdits: " + incorrect)
			if version == "1.10.1" {
				// An unpublished npm version stays reserved even without a Git tag.
				r.CommitEmpty("fix(cli): initial npm attempt\n\nRelease-As: 1.10.0")
			}
			r.CommitEmpty("chore(cli,docs): prepare release notes\n\nDeletes: *")
			r.CommitEmpty("fix(cli): distribute the native CLI\n\nRelease-As: " + version + "\n\n---\n\nfix(docs): explain npm installation")
			notes := r.Shell("dispat preview --package cli --changelog")
			require.Zero(t, notes.Code, "%s\n%s", notes.Stdout, notes.Stderr)
			assert.Contains(t, notes.Stdout, "distribute the native CLI")
			assert.NotContains(t, notes.Stdout, "(corrects")
			assert.NotContains(t, notes.Stdout, "- supporting repairs")
			r.ReleaseOK()
			assertNPMDistribution(t, r, version, "1.10.0")
			if version == "1.10.1" {
				assert.False(t, r.HasTag("packages/cli/v1.10.0"), "recovery must not fabricate the failed attempt tag")
			}
			assert.True(t, r.HasTag("packages/docs/v1.10.10"))
			assert.Equal(t, 1, r.TagCount("services/dispat/v"))
			assert.Equal(t, 4, len(r.TagList()), "only npm and docs may add release tags")
			tags := r.TagList()
			r.ReleaseOK()
			assert.Equal(t, tags, r.TagList(), "the exact initial release converges")
		})
	}
}

// Exercise the real package configuration, substituting only external tools.
// A future unpublished provider must not turn an ordinary CI build into a
// release download, or require Node tools on the host runner.
func TestNPMDistributionSeparatesCIBuildFromReleasePackaging(t *testing.T) {
	r := npmDistributionRepo(t)
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	config, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "packages", "cli", "dispat.yaml"))
	require.NoError(t, err)
	r.WriteFile("packages/cli/dispat.yaml", string(config))
	r.WriteFile("scripts/buildx-cache.sh", "#!/bin/sh\nexit 0\n")
	for _, name := range []string{"docker", "pnpm", "node"} {
		guard := ""
		if name == "pnpm" {
			guard = "test \"$pnpm_config_verify_deps_before_run\" = false || exit 42\n"
		}
		r.WriteFile("bin/"+name, "#!/bin/sh\n"+guard+"printf '%s %s\\n' '"+name+"' \"$*\" >> \"$PWD/../../tools.log\"\n")
		require.NoError(t, os.Chmod(r.Path("bin", name), 0o700))
	}
	r.Commit("fix(cli): wire independent CI packaging")
	res := r.Shell(`PATH="$PWD/bin:$PATH" dispat run build --since all -p cli`)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	calls, err := os.ReadFile(r.Path("tools.log"))
	require.NoError(t, err)
	assert.Contains(t, string(calls), "docker buildx build")
	assert.NotContains(t, string(calls), "pnpm")
	assert.NotContains(t, string(calls), "node")
	assert.Empty(t, r.TagList(), "ordinary builds must not publish")
	require.NoError(t, os.Remove(r.Path("tools.log")))
	res = r.Shell(`PATH="$PWD/bin:$PATH" dispat release`)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	calls, err = os.ReadFile(r.Path("tools.log"))
	require.NoError(t, err)
	assert.Equal(t, "pnpm build\nnode build/scripts/pack.js\npnpm compile:test\nnode test-build/smoke-artifact.js\nnode build/scripts/publish.js\n", string(calls))
	assert.True(t, r.HasTag("packages/cli/v1.10.1"))
}

// The site's real dependency declarations must gate its expensive hooks and
// deployment on npm publication, including retries and npm-only patches.
func TestNPMDistributionGatesDocsOnPublishedCLI(t *testing.T) {
	r := npmDistributionRepo(t)
	cfg := npmDistributionConfig()
	cfg.Dependencies = nil
	cfg.Initials["docs"] = "1.10.0"
	r.WriteConfigModel(cfg)
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	for _, name := range []string{"cli", "docs"} {
		config, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "packages", name, "dispat.yaml"))
		require.NoError(t, err)
		// Keep the production graph and version policy; replace only commands
		// that otherwise build images or write to external destinations.
		policy, _, found := strings.Cut(string(config), "\nscripts:")
		require.True(t, found)
		commands := `
scripts:
  build: test "$(cat ../../published/version)" = "$DISPAT_WORKSPACE_DISPAT_VERSION"
  publish: test ! -f ../../fail-npm && printf '%s' "$DISPAT_NEW_VERSION" > ../../published/npm
flow:
  build: build
  publish: publish
`
		if name == "docs" {
			commands = `
scripts:
  before-build: test -f ../../published/npm && touch ../../published/docs-built
  build: echo build
  publish: test -f ../../published/npm && touch ../../published/docs
flow:
  beforeBuild: before-build
  build: build
  publish: publish
`
		}
		r.WriteFile("packages/"+name+"/dispat.yaml", policy+commands)
	}
	for _, name := range []string{"docs", "infra", "dispat-alpine"} {
		r.SeedPackage("packages", name)
		if name != "docs" {
			r.WriteFile("packages/"+name+"/dispat.yaml", "scripts:\n  build: echo build\n  publish: echo publish\n")
		}
	}
	r.Commit("fix(cli,docs,infra,dispat-alpine): order publication")
	r.WriteFile("fail-npm", "fail\n")
	res := r.Release()
	require.NotZero(t, res.Code)
	assert.Zero(t, r.TagCount("packages/docs/v"))
	assert.NoFileExists(t, r.Path("published", "docs-built"), "docs hooks must wait for npm publication")
	assert.NoFileExists(t, r.Path("published", "docs"), "a failed npm publish must block deployment")

	r.Remove("fail-npm")
	r.ReleaseOK()
	assert.FileExists(t, r.Path("published", "docs"))
	assert.True(t, r.HasTag("packages/docs/v1.10.1"))
	assert.Equal(t, 1, r.TagCount("services/dispat/v"), "retry reuses native publication")

	// Repeat after the initial release: an existing npm tag is not proof
	// that a newly planned npm patch has been published.
	for _, name := range []string{"npm", "docs", "docs-built"} {
		r.Remove("published/" + name)
	}
	r.WriteFile("fail-npm", "fail\n")
	r.CommitEmpty("fix(cli,docs): update npm integration")
	res = r.Release()
	require.NotZero(t, res.Code)
	assert.NoFileExists(t, r.Path("published", "docs-built"))
	assert.NoFileExists(t, r.Path("published", "docs"))
	assert.Equal(t, 1, r.TagCount("packages/docs/v"))
	r.Remove("fail-npm")
	r.ReleaseOK()
	assert.True(t, r.HasTag("packages/docs/v1.10.2"))
	assert.Equal(t, 1, r.TagCount("services/dispat/v"))

	// A docs-only patch needs the already published CLI, not a new npm tag.
	r.CommitEmpty("fix(docs): clarify installation")
	r.ReleaseOK()
	assert.True(t, r.HasTag("packages/docs/v1.10.3"))
	assert.Equal(t, 2, r.TagCount("packages/cli/v"))
}
