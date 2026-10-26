// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteReceiptObjectReadsExactRefAndAbsence(t *testing.T) {
	root, git := initRepo(t)
	addBareRemote(t, root)
	const releaseTag = "api-v1.2.3"
	missing, err := git.RemoteReceiptObject(t.Context(), "origin", releaseTag)
	require.NoError(t, err)
	require.Empty(t, missing)
	head, err := git.HeadSHA(t.Context())
	require.NoError(t, err)
	ref := ReceiptRefPrefix + releaseTag
	runGit(t, root, "update-ref", ref, head)
	runGit(t, root, "push", "-q", "origin", ref)
	stored, err := git.RemoteReceiptObject(t.Context(), "origin", releaseTag)
	require.NoError(t, err)
	require.Equal(t, head, stored)
	_, err = git.RemoteReceiptObject(t.Context(), "origin", "unsafe..tag")
	require.Error(t, err)
}
