// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommitTreeOIDIgnoresTransportCommitIdentity(t *testing.T) {
	fixture := newTransportFixture(t)
	first, err := fixture.git.CommitTreeOID(t.Context(), fixture.first)
	require.NoError(t, err)
	second, err := fixture.git.CommitTreeOID(t.Context(), fixture.second)
	require.NoError(t, err)
	require.NotEqual(t, fixture.first, fixture.second)
	require.Equal(t, first, second, "an empty second commit has identical captured bytes")
	_, err = fixture.git.CommitTreeOID(t.Context(), first)
	require.ErrorContains(t, err, "has type tree, expected commit", "a tree must not be accepted as a snapshot commit")
}
