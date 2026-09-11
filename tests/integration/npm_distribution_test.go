package integration

// Goal 36: the npm distribution shares the CLI's major/minor line, while
// its own patches and the version of the binary it installs remain distinct.

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// npmDistributionRepo models the production publication boundary without a
// network destination: the provider's publish writes a receipt, which the
// npm build must read before recording its immutable binary version.
func npmDistributionRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
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
	r.WriteConfigModel(cfg)
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
