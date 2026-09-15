package gitx

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteBranchProofRequiresTheExactPublishedTip(t *testing.T) {
	root, g := initRepo(t)
	remote := filepath.Join(t.TempDir(), "source.git")
	polyrepoGit(t, root, "init", "-q", "--bare", remote)
	polyrepoGit(t, root, "remote", "add", "origin", remote)
	first := polyrepoGit(t, root, "rev-parse", "HEAD")
	require.NoError(t, g.PushRelease(t.Context(), "origin", "main", nil, nil))
	require.NoError(t, g.VerifyRemoteBranch(t.Context(), "origin", "main", first))
	polyrepoGit(t, root, "commit", "--allow-empty", "-qm", "fix: unpublished source")
	second := polyrepoGit(t, root, "rev-parse", "HEAD")
	assert.ErrorContains(t, g.VerifyRemoteBranch(t.Context(), "origin", "main", second), "push that source branch")
	assert.Error(t, g.VerifyRemoteBranch(t.Context(), "origin", "missing", first))
	require.NoError(t, g.PushRelease(t.Context(), "origin", "main", nil, nil))
	require.NoError(t, g.VerifyRemoteBranch(t.Context(), "origin", "main", second))
	assert.Error(t, g.VerifyRemoteBranch(t.Context(), "origin", "main", first), "ancestry alone is not the pinned branch tip")
	assert.Error(t, g.VerifyRemoteBranch(t.Context(), "missing-remote", "main", second))
	assert.Error(t, g.VerifyRemoteRelease(t.Context(), "missing-remote", "lib@1.0.0", second))
}

func TestPushReleaseValidatesEveryRefBeforeWritingTheBranch(t *testing.T) {
	root, g := initRepo(t)
	remote := filepath.Join(t.TempDir(), "source.git")
	polyrepoGit(t, root, "init", "-q", "--bare", remote)
	polyrepoGit(t, root, "remote", "add", "origin", remote)
	for _, tc := range []struct {
		name, branch string
		tags, moving []string
	}{
		{name: "branch", branch: "bad:name"},
		{name: "immutable tag", branch: "main", tags: []string{"bad:name"}},
		{name: "moving alias", branch: "main", moving: []string{"bad:name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, g.PushRelease(t.Context(), "origin", tc.branch, tc.tags, tc.moving))
			assert.Empty(t, polyrepoGit(t, remote, "for-each-ref", "--format=%(refname)"))
		})
	}
	require.NoError(t, g.PushRelease(t.Context(), "missing-remote", "", nil, nil), "an empty record does not contact a remote")
}

func TestGitlinkReadsKeepExactPathsAndPropagateRepositoryFailures(t *testing.T) {
	root, g := initRepo(t)
	source := polyrepoGit(t, root, "rev-parse", "HEAD")
	const path = "sources/lib with spaces"
	polyrepoGit(t, root, "update-index", "--add", "--cacheinfo", "160000", source, path)
	polyrepoGit(t, root, "commit", "-qm", "chore: pin source")
	links, err := g.GitlinksAt(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{path: source}, links)
	pin, err := g.GitlinkCommit(t.Context(), "HEAD", path)
	require.NoError(t, err)
	assert.Equal(t, source, pin)
	_, err = g.GitlinkCommit(t.Context(), "HEAD", "sources/lib")
	assert.ErrorContains(t, err, "has no gitlink")
	_, err = g.GitlinkCommit(t.Context(), "missing-revision", path)
	assert.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = g.GitlinksAt(ctx, "HEAD")
	assert.Error(t, err)
}
