package app

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// The end-to-end behaviour of App — status, release, hooks, finalize, exit
// semantics — is exercised through the cli package's tests against real git
// repositories; here live the unit tests of App's own helpers.

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
