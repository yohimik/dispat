package app

import (
	"bytes"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// The end-to-end behaviour of App — status, release, hooks, finalize, exit
// semantics — is exercised by the black-box suite in tests/integration
// against the compiled binary; here live the unit tests of App's own
// helpers.

func TestValidOnError(t *testing.T) {
	assert.True(t, ValidOnError(OnErrorSkip))
	assert.True(t, ValidOnError(OnErrorContinue))
	assert.False(t, ValidOnError(""))
	assert.False(t, ValidOnError("explode"))
}

func TestInitialVersionsMapping(t *testing.T) {
	// Viper lowercases map keys, so matching is case-insensitive; a key that
	// names no discovered package is warned about and ignored.
	var buf bytes.Buffer
	a := New(t.TempDir(), &config.File{
		InitialVersions: map[string]ccme.Version{
			"core":  {Major: 1, Minor: 2},
			"ghost": {Major: 9},
		},
	}, zerolog.New(&buf))

	out := a.initialVersions([]*model.Package{{Name: "Core"}})
	require.Len(t, out, 1)
	assert.Equal(t, "1.2.0", out["Core"].String(), "initials key matched case-insensitively")
	assert.Contains(t, buf.String(), "initials entry matches no discovered package")
}

func TestRenderCommitMessage(t *testing.T) {
	pkgs := []string{"core", "utils"}
	tags := []string{"core@1.1.0", "utils@2.0.1"}
	assert.Equal(t, "chore(release): core@1.1.0, utils@2.0.1",
		renderCommitMessage("", pkgs, tags), "default format")
	assert.Equal(t, "publish core, utils as core@1.1.0, utils@2.0.1",
		renderCommitMessage("publish {packages} as {tags}", pkgs, tags))
	assert.Equal(t, "no placeholders", renderCommitMessage("no placeholders", pkgs, tags))
}

func TestGithubReleaserResolution(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "envowner/envrepo")
	t.Setenv("GITHUB_TOKEN", "envtoken")
	t.Setenv("CUSTOM_TOKEN", "customtoken")

	gh, err := githubReleaser(config.GitHubConfig{}, zerolog.Nop())
	require.NoError(t, err)
	assert.Equal(t, "envowner", gh.Owner)
	assert.Equal(t, "envrepo", gh.Repo)
	assert.Equal(t, "envtoken", gh.Token)

	gh, err = githubReleaser(config.GitHubConfig{Owner: "acme", Repo: "mono", TokenEnv: "CUSTOM_TOKEN"}, zerolog.Nop())
	require.NoError(t, err)
	assert.Equal(t, "acme", gh.Owner)
	assert.Equal(t, "mono", gh.Repo)
	assert.Equal(t, "customtoken", gh.Token)

	t.Setenv("GITHUB_REPOSITORY", "")
	_, err = githubReleaser(config.GitHubConfig{}, zerolog.Nop())
	assert.ErrorContains(t, err, "no repository")

	t.Setenv("GITHUB_TOKEN", "")
	_, err = githubReleaser(config.GitHubConfig{Owner: "acme", Repo: "mono"}, zerolog.Nop())
	assert.ErrorContains(t, err, "GITHUB_TOKEN")
}
