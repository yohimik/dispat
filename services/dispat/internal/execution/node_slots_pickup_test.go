// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNodeSlotsKeepIndependentPickupsChargedAcrossRestart(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, "worker", 2)
	require.NoError(t, err)
	first := NodeSlotIntent{Run: "run", Task: "build", Attempt: 1, Weight: 1,
		PickupID: NewChoreographyPickupID()}
	second := first
	second.PickupID = NewChoreographyPickupID()
	firstLease, firstHeld, err := authority.TryReserve(t.Context(), first)
	require.NoError(t, err)
	require.True(t, firstHeld)
	secondLease, secondHeld, err := authority.TryReserve(t.Context(), second)
	require.NoError(t, err)
	require.True(t, secondHeld)
	require.NotEqual(t, firstLease.ID, secondLease.ID)
	_, _, err = authority.TryReserve(t.Context(), first)
	require.Error(t, err, "an unanswered pickup cannot reserve another physical lease")
	reopened, err := OpenNodeSlotAuthority(root, "worker", 2)
	require.NoError(t, err)
	held, err := reopened.Held(t.Context())
	require.NoError(t, err)
	require.ElementsMatch(t, []NodeSlotLease{firstLease, secondLease}, held)
	third := first
	third.PickupID = NewChoreographyPickupID()
	_, available, err := reopened.TryReserve(t.Context(), third)
	require.NoError(t, err)
	require.False(t, available, "unknown pickups remain charged against capacity")
}
