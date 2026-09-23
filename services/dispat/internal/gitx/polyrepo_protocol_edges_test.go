package gitx

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestMutationLockLateCancellationReleasesWhatItTook: a transaction over two
// repositories that is cancelled while waiting for the second gives back the
// first. Otherwise one interrupted checkpoint would leave a repository held
// for the life of the process and every later record there would wait on it.
func TestMutationLockLateCancellationReleasesWhatItTook(t *testing.T) {
	firstRoot, first := initRepo(t)
	secondRoot, second := initRepo(t)
	firstCommon, err := first.mutationCommonDir(t.Context())
	require.NoError(t, err)
	secondCommon, err := second.mutationCommonDir(t.Context())
	require.NoError(t, err)
	if firstCommon > secondCommon {
		// The waiter must reach the held repository second, which is the
		// order the lock takes them in, whatever the temporary names were.
		firstRoot, secondRoot = secondRoot, firstRoot
		first, second = second, first
	}
	holding, err := second.AcquireMutation(t.Context())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	waited := make(chan error, 1)
	go func() {
		unlock, waitErr := AcquireMutations(ctx, first, second)
		if unlock != nil {
			unlock()
		}
		waited <- waitErr
	}()
	// The waiter holds the first repository while it waits for the second.
	require.Eventually(t, func() bool {
		probe, cancelProbe := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancelProbe()
		unlock, probeErr := first.AcquireMutation(probe)
		if probeErr == nil {
			unlock()
		}
		return errors.Is(probeErr, context.DeadlineExceeded)
	}, 5*time.Second, 10*time.Millisecond, "the waiter takes the first repository before it waits")
	cancel()
	require.ErrorIs(t, <-waited, context.Canceled)

	unlock, err := first.AcquireMutation(t.Context())
	require.NoError(t, err, "a cancelled acquisition must not leave the first repository held")
	unlock()
	holding()
	unlock, err = AcquireMutations(t.Context(), second, first)
	require.NoError(t, err)
	unlock()
	unlock()
	assert.NoFileExists(t, filepath.Join(firstRoot, ".git", "dispat-mutation.lock"))
	assert.NoFileExists(t, filepath.Join(secondRoot, ".git", "dispat-mutation.lock"))
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
