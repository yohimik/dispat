package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlHistoryProtocolRejectsMalformedIdentityAndDiffFrames(t *testing.T) {
	sha := strings.Repeat("a", 40)
	header := func(oid, parents string) string {
		return strings.Join([]string{"", controlHistoryMarker, oid, parents, "Author", "author@example.invalid", "fix: change", ""}, "\x00")
	}
	for _, tc := range []struct{ name, input, diagnostic string }{
		{"unknown marker", "not-the-protocol", "marker"},
		{"short object", header("abc", ""), "object id"},
		{"nonhex object", header(strings.Repeat("g", 40), ""), "object id"},
		{"invalid parent", header(sha, "not-a-parent"), "parent"},
		{"missing diff path", header(sha, "") + ":100644 100644 " + sha + " " + sha + " M", "raw record"},
		{"invalid diff header", header(sha, "") + "unexpected\x00path\x00", "raw record"},
		{"incomplete diff metadata", header(sha, "") + ":160000 160000\x00source\x00", "raw metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			commits, err := parseControlGitlinkHistory(tc.input)
			require.ErrorContains(t, err, tc.diagnostic)
			assert.Nil(t, commits, "partial evidence must not escape after a malformed frame")
		})
	}
}

func TestMutationLockOpenFailureAndLateCancellationReleaseResources(t *testing.T) {
	root, g := initRepo(t)
	path, err := g.mutationLockPath(t.Context())
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(path, 0o700))
	unlock, err := g.AcquireMutation(t.Context())
	require.ErrorContains(t, err, "opening Git mutation lock")
	assert.Nil(t, unlock)
	require.NoError(t, os.Remove(path))
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancellation after the common directory has already been resolved
	unlock, err = acquireMutationPath(ctx, path)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, unlock)
	unlock, err = g.AcquireMutation(t.Context())
	require.NoError(t, err, "a failed attempt must not leave the repository locked")
	unlock()
	unlock()
	assert.FileExists(t, filepath.Join(root, ".git", mutationLockFile))
}

func TestTagSnapshotsIncludeVersionAndNamedMovingAliases(t *testing.T) {
	_, g := initRepo(t)
	for _, name := range []string{"lib@1.0.0", "v1", "lib/v1.0", "foreign"} {
		require.NoError(t, g.CreateTag(t.Context(), name, "release", "HEAD"))
	}
	matcher := NewTagSnapshotMatcher([]TagNamespace{{Package: "lib", Release: DefaultTagFormat,
		Aliases: []AliasFormat{"v{major}", "{name}/v{major}.{minor}"}}})
	refs, err := g.RelevantTagSnapshot(t.Context(), matcher)
	require.NoError(t, err)
	require.Len(t, refs, 3)
	assert.Contains(t, refs, "v1")
	assert.Contains(t, refs, "lib/v1.0")
	assert.NotContains(t, refs, "foreign")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = g.RelevantTagSnapshot(ctx, matcher)
	assert.Error(t, err, "a failed ref read cannot establish a publication snapshot")
}
