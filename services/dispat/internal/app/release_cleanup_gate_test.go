package app

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReleaseCleanupRetainsAuthorityWhileAcceptedEffectsAreUnsettled(t *testing.T) {
	var cleaned, unlocked int
	blocked := true
	cleanup := &releaseCleanup{
		BeforeUnlock: func() error {
			if blocked {
				return errors.New("accepted attempt remains unresolved")
			}
			return nil
		},
		cleanPins: func() { cleaned++ },
		unlock:    func() error { unlocked++; return nil },
	}
	require.ErrorContains(t, cleanup.finish(), "accepted attempt remains unresolved")
	require.Zero(t, cleaned, "fleet pins remain for effect reconciliation")
	require.Zero(t, unlocked, "the release lock still fences a new generation")
	blocked = false
	require.NoError(t, cleanup.finish())
	require.NoError(t, cleanup.finish())
	require.Equal(t, 1, cleaned)
	require.Equal(t, 1, unlocked)
}

func TestReleaseCleanupDoesNotCertifyAFailedUnlock(t *testing.T) {
	var attempts int
	cleanup := &releaseCleanup{unlock: func() error {
		attempts++
		return errors.New("remote lock delete was not established")
	}}
	require.ErrorContains(t, cleanup.finish(), "not established")
	require.True(t, cleanup.isUnlocked, "the uncertain delete is never repeated blindly")
	require.ErrorContains(t, cleanup.unlockErr, "not established")
	require.NoError(t, cleanup.finish())
	require.Equal(t, 1, attempts)
}

func TestReleaseCleanupRequiresSignedChoreographyClosureBeforeUnlock(t *testing.T) {
	a := &App{choreographyRetained: true, choreographyBound: true}
	var unlocked int
	cleanup := &releaseCleanup{
		BeforeUnlock: a.requireChoreographyQuiescent,
		unlock:       func() error { unlocked++; return nil },
	}
	require.ErrorContains(t, cleanup.finish(), "has not closed")
	a.isCoordinatorClosed = true
	a.coordinatorCloseErr = errors.New("active claim has no signed stop")
	require.ErrorContains(t, cleanup.finish(), "active claim has no signed stop")
	require.Zero(t, unlocked, "an accepted effect retains the global release lock")
	a.coordinatorCloseErr = nil
	require.NoError(t, cleanup.finish())
	require.Equal(t, 1, unlocked)
}
