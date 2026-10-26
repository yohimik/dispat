package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPinnedRepositoryCoordinatePreservesRecordPolicyWithoutCredentials(t *testing.T) {
	identity := parsePlanningRemote("https://user:secret@github.com/acme/lib.git?token=another-secret")
	require.Equal(t, planningRemoteCoordinate{host: "github.com", owner: "acme", repo: "lib"}, identity)
	require.NotContains(t, identity.url(), "secret")
	owner, repo := githubCoordinates(identity.url(), "")
	require.Equal(t, "acme", owner)
	require.Equal(t, "lib", repo)

	enterprise := parsePlanningRemote("git@code.example.test:team/repo.git")
	owner, repo = githubCoordinates(enterprise.url(), "https://code.example.test/api/v3")
	require.Equal(t, "team", owner)
	require.Equal(t, "repo", repo)
	owner, repo = githubCoordinates(enterprise.url(), "")
	require.True(t, strings.TrimSpace(owner+repo) == "", "an unrelated host is not a GitHub record destination")
}
