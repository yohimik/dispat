package integration

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

// TestAdmissionRemovedProviderCreatesNoDebt: a consumer ships its own work
// while its provider fails, then the project retires that provider before it
// publishes. The provider's unit is still in the history the consumer released
// past, but there is no current package or edge for it to be owed through, so
// planning converges with nothing released.
func TestAdmissionRemovedProviderCreatesNoDebt(t *testing.T) {
	r := admissionRepo(t, admissionShape{})
	r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
	require.NotZero(t, r.Release().Code, "the provider publish fails")
	require.Equal(t, 1, r.TagCount("cli@0.2.0"), "the consumer shipped its own change")

	configBytes, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	var cfg models.File
	require.NoError(t, json.Unmarshal(configBytes, &cfg))
	cfg.Dependencies = nil
	r.WriteConfigModel(cfg)
	r.Git("rm", "-r", "--", "packages/libs/core")
	r.WriteFile("packages/libs/.keep", "the library space remains\n")
	manifestPath := r.Path("packages", "apps", "cli", "package.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(manifestBytes, &manifest))
	delete(manifest, "dependencies")
	manifestBytes, err = json.Marshal(manifest)
	require.NoError(t, err)
	r.WriteFile("packages/apps/cli/package.json", string(manifestBytes))
	r.Commit("chore: retire core and remove its dependency")

	status := r.Status()
	require.Equal(t, 0, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	assert.Contains(t, status.Stdout, `"releasing":0`)
	assert.NotContains(t, status.Stdout+status.Stderr, "unknown provider")
	assert.Equal(t, 1, r.TagCount("cli@0.2.0"), "the consumer is not released again")
	assert.Zero(t, r.TagCount("cli@0.2.1"))
}
