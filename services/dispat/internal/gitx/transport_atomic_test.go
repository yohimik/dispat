// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTransportAtomicCreateAndAdvanceKeepTheOtherRefOnRejection(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	one, two, three := "dispat-reserved-one", "dispat-reserved-two", "dispat-reserved-three"
	require.NoError(t, f.git.PushCreateAtomic(ctx, f.bare, []CreateRef{
		{Branch: one, OID: f.first}, {Branch: two, OID: f.first},
	}))
	require.Equal(t, f.first, f.remoteOID(t, "refs/heads/"+one))
	require.Equal(t, f.first, f.remoteOID(t, "refs/heads/"+two))

	err := f.git.PushCreateAtomic(ctx, f.bare, []CreateRef{
		{Branch: two, OID: f.second}, {Branch: three, OID: f.second},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrLeaseRejected) || errors.Is(err, ErrPushUnknown))
	require.Equal(t, f.first, f.remoteOID(t, "refs/heads/"+two))
	heads, err := f.git.ListRemoteHeads(ctx, f.bare, "refs/heads/"+three)
	require.NoError(t, err)
	require.Empty(t, heads, "an atomic failure cannot leave one fresh reservation behind")

	require.NoError(t, f.git.PushAtomicAdvances(ctx, f.bare, []BranchAdvance{
		{Branch: one, OID: f.second, ExpectedOld: f.first},
		{Branch: two, OID: f.second, ExpectedOld: f.first},
	}))
	require.Equal(t, f.second, f.remoteOID(t, "refs/heads/"+one))
	require.Equal(t, f.second, f.remoteOID(t, "refs/heads/"+two))
	runGit(t, f.root, "commit", "-q", "--allow-empty", "-m", "third")
	thirdOID := strings.TrimSpace(runGit(t, f.root, "rev-parse", "HEAD"))

	err = f.git.PushAtomicAdvances(ctx, f.bare, []BranchAdvance{
		{Branch: one, OID: thirdOID, ExpectedOld: f.first},
		{Branch: two, OID: f.first, ExpectedOld: f.second},
	})
	require.Error(t, err)
	require.Equal(t, f.second, f.remoteOID(t, "refs/heads/"+one))
	require.Equal(t, f.second, f.remoteOID(t, "refs/heads/"+two),
		"a stale task lease cannot move its paired slot notice")
}

func TestImportTagAsBlobFromCarriesExactAnnotatedObjectWithBound(t *testing.T) {
	f := newTransportFixture(t)
	runGit(t, f.root, "tag", "-a", "pinned-release", "-m", "pinned annotation", f.second)
	tagOID := strings.TrimSpace(runGit(t, f.root, "rev-parse", "refs/tags/pinned-release"))
	destination := &LocalGitx{Dir: f.bare, Log: f.git.Log}
	blobOID, size, err := destination.ImportTagAsBlobFrom(t.Context(), f.root, tagOID, 1<<20)
	require.NoError(t, err)
	require.Positive(t, size)
	var content bytes.Buffer
	plumbing := NewPlumbing(destination)
	plumbing.ReadBlob(t.Context(), blobOID, &content, 1<<20)
	require.NoError(t, plumbing.Err())
	require.Contains(t, content.String(), "pinned annotation")
	_, _, err = destination.ImportTagAsBlobFrom(t.Context(), f.root, tagOID, size-1)
	require.ErrorIs(t, err, ErrTransportLimit)
}

func TestTransportAtomicPushRejectsDuplicatesAndOversizedBatchBeforeWriting(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	err := f.git.PushCreateAtomic(ctx, f.bare, []CreateRef{
		{Branch: "dispat-duplicate", OID: f.first},
		{Branch: "dispat-duplicate", OID: f.second},
	})
	require.Error(t, err)
	heads, err := f.git.ListRemoteHeads(ctx, f.bare, "refs/heads/dispat-duplicate")
	require.NoError(t, err)
	require.Empty(t, heads)
	tooMany := make([]CreateRef, MaxTransportBatch+1)
	require.ErrorIs(t, f.git.PushCreateAtomic(ctx, f.bare, tooMany), ErrTransportLimit)
}

func TestTransportPushesOnlyExplicitMailboxRefsWithFollowTagsConfigured(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	runGit(t, f.root, "tag", "-a", "release-probe", "-m", "release-probe", f.first)
	runGit(t, f.root, "config", "push.followTags", "true")
	require.NoError(t, f.git.PushCreate(ctx, f.bare, f.first, "dispat-single"))
	require.NoError(t, f.git.PushCreateAtomic(ctx, f.bare,
		[]CreateRef{{Branch: "dispat-atomic", OID: f.second}}))
	tag, err := f.git.RemoteTagObject(ctx, f.bare, "release-probe")
	require.NoError(t, err)
	require.Empty(t, tag, "a transport push cannot publish a reachable release tag")
	outcomes, err := f.git.DeleteRemoteBranchesLease(ctx, f.bare,
		[]BranchLease{{Branch: "dispat-single", ExpectedOld: f.first}})
	require.NoError(t, err)
	require.Equal(t, BranchDeleted, outcomes[0].Result)
	tag, err = f.git.RemoteTagObject(ctx, f.bare, "release-probe")
	require.NoError(t, err)
	require.Empty(t, tag, "closing a coordination ref cannot auto-publish a release tag")
	runGit(t, f.root, "push", f.bare, f.second+":refs/heads/control")
	tag, err = f.git.RemoteTagObject(ctx, f.bare, "release-probe")
	require.NoError(t, err)
	require.NotEmpty(t, tag, "the configured followTags hazard must be present in this fixture")
}

func TestTransportListsOnlyBoundedExactRefs(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	require.NoError(t, f.git.PushCreateAtomic(ctx, f.bare, []CreateRef{
		{Branch: "dispat-task-one", OID: f.first},
		{Branch: "dispat-task-two", OID: f.second},
		{Branch: "dispat-task-unwanted", OID: f.first},
	}))
	heads, err := f.git.ListRemoteHeadsExact(ctx, f.bare,
		[]string{"dispat-task-two", "dispat-task-absent", "dispat-task-one"})
	require.NoError(t, err)
	require.Equal(t, []RemoteHead{
		{Name: "dispat-task-one", OID: f.first},
		{Name: "dispat-task-two", OID: f.second},
	}, heads)
	_, err = f.git.ListRemoteHeadsExact(ctx, f.bare,
		[]string{"dispat-task-one", "dispat-task-one"})
	require.Error(t, err)
	_, err = f.git.ListRemoteHeadsExact(ctx, f.bare, make([]string, MaxTransportBatch+1))
	require.ErrorIs(t, err, ErrTransportLimit)
}

func TestFirstParentOIDsReplaysOnlyNewCommitsOldestFirst(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	commits := []string{f.first, f.second}
	for range 4 {
		runGit(t, f.root, "commit", "-q", "--allow-empty", "-m", "ledger step")
		commits = append(commits, strings.TrimSpace(runGit(t, f.root, "rev-parse", "HEAD")))
	}
	all, err := f.git.FirstParentOIDs(ctx, commits[5], commits[0])
	require.NoError(t, err)
	require.Equal(t, commits[1:], all)
	newest, err := f.git.FirstParentOIDs(ctx, commits[5], commits[4])
	require.NoError(t, err)
	require.Equal(t, commits[5:], newest)
	empty, err := f.git.FirstParentOIDs(ctx, commits[5], commits[5])
	require.NoError(t, err)
	require.Empty(t, empty)
	_, err = f.git.FirstParentOIDs(ctx, commits[5], strings.Repeat("a", 40))
	require.Error(t, err)
}
