package integration

// Goal 36: the npm distribution shares the CLI's major/minor line, while
// its own patches and the version of the binary it installs remain distinct.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
	r.WriteFile("packages/cli/package.json", `{"name":"@dispat/cli","version":"1.10.0"}`+"\n")
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
	r.WriteFile("packages/cli/package.json", `{"name":"@dispat/cli","version":"1.10.0"}`+"\n")
	r.Commit("chore(cli): introduce npm distribution")
	assert.Contains(t, r.StatusOK().Stdout, "1.11.0", "old minor propagation is pending for the newly added consumer")

	r.CommitEmpty("cancel(cli): establish npm baseline")
	r.CommitEmpty("fix(cli): distribute the native CLI")
	r.ReleaseOK()
	assertNPMDistribution(t, r, "1.10.1", "1.10.0")
	assert.Equal(t, 1, r.TagCount("services/dispat/v"), "introducing npm must not republish the provider")
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
		r.WriteFile("bin/"+name, "#!/bin/sh\nprintf '%s %s\\n' '"+name+"' \"$*\" >> \"$PWD/../../tools.log\"\n")
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
