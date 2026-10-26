package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestSweepOutcomePolicyKeepsDirectBlockerAndContinueSemantics(t *testing.T) {
	releases := make(map[string]*plan.Release)
	for _, name := range []string{"a", "b", "c", "d"} {
		releases[name] = &plan.Release{Pkg: &model.Package{Name: name}}
	}
	pl := &plan.Plan{Releases: releases, Providers: map[string][]string{
		"b": {"a"}, "c": {"b"}, "d": {"b", "a"},
	}}
	covered := []string{"a", "b", "c", "d"}
	skip, err := newSweepOutcomeEvaluator(pl, covered, OnErrorSkip)
	require.NoError(t, err)
	require.NoError(t, skip.ApplyOutcome("a", "failed"))
	blocked, by, err := skip.ShouldSkip("b")
	require.NoError(t, err)
	require.True(t, blocked)
	require.Equal(t, "a", by)
	// b may have no selected script, yet the existing sweep marks it skipped
	// after a fails. Its signed skip then becomes c's direct blocker.
	require.NoError(t, skip.ApplyOutcome("b", "skipped"))
	blocked, by, err = skip.ShouldSkip("c")
	require.NoError(t, err)
	require.True(t, blocked)
	require.Equal(t, "b", by)
	blocked, by, err = skip.ShouldSkip("d")
	require.NoError(t, err)
	require.True(t, blocked)
	require.Equal(t, "b", by)
	require.ErrorContains(t, skip.ApplyOutcome("b", "failed"), "changed settled outcome")
	require.ErrorContains(t, skip.ApplyOutcome("a", "published"), "invalid")

	continued, err := newSweepOutcomeEvaluator(pl, covered, OnErrorContinue)
	require.NoError(t, err)
	require.NoError(t, continued.ApplyOutcome("a", "failed"))
	blocked, by, err = continued.ShouldSkip("b")
	require.NoError(t, err)
	require.False(t, blocked)
	require.Empty(t, by)
	// An unavailable provider output is handled by the frame's exact
	// export merge; it is not converted into a false dependency skip.
	require.NoError(t, continued.ApplyOutcome("b", "cancelled"))
	blocked, _, err = continued.ShouldSkip("c")
	require.NoError(t, err)
	require.False(t, blocked)
}
