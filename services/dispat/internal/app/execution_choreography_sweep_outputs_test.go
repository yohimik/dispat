package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestSweepOutputAccumulatorCarriesAcceptedFailedProviderExportsThroughNoop(t *testing.T) {
	releases := make(map[string]*plan.Release)
	for _, name := range []string{"a", "b", "c", "outside"} {
		releases[name] = &plan.Release{Pkg: &model.Package{Name: name}}
	}
	releases["c"].Outputs = []plan.Output{{Name: "DEFAULT", Value: "fallback", Source: "plan"}}
	pl := &plan.Plan{Releases: releases, Providers: map[string][]string{
		"b": {"a", "outside"}, "c": {"b"},
	}}
	acc, err := newSweepOutputAccumulator(pl, []string{"a", "b", "c"})
	require.NoError(t, err)
	_, err = acc.Prepare("b")
	require.ErrorContains(t, err, "no accepted final outcome")
	require.Empty(t, releases["b"].Outputs)
	_, err = acc.Prepare("a")
	require.NoError(t, err)
	// A's signed final result is failed but still contains a valid export
	// written before the command exited. A raw retryable attempt is never
	// passed to Finalize.
	require.NoError(t, acc.Finalize("a", []plan.Output{{Name: "TOKEN", Value: "partial", Source: "a:run"}}))
	releases["outside"].Outputs = []plan.Output{{Name: "FOREIGN", Value: "ignored"}}
	fromA, err := acc.Prepare("b")
	require.NoError(t, err)
	require.Equal(t, []plan.Output{{Name: "TOKEN", Value: "partial", Source: "a:run"}}, fromA)
	// B has no selected script. Its no-op or derived skip still carries the
	// provider outputs in the original sweep's release value.
	require.NoError(t, acc.Finalize("b", nil))
	fromB, err := acc.Prepare("c")
	require.NoError(t, err)
	require.Equal(t, []plan.Output{
		{Name: "DEFAULT", Value: "fallback", Source: "plan"},
		{Name: "TOKEN", Value: "partial", Source: "a:run"},
	}, fromB)
	require.NoError(t, acc.Finalize("c", []plan.Output{{Name: "TOKEN", Value: "own", Source: "c:run"}}))
	require.Equal(t, []plan.Output{
		{Name: "DEFAULT", Value: "fallback", Source: "plan"},
		{Name: "TOKEN", Value: "own", Source: "c:run"},
	}, releases["c"].Outputs)
	require.ErrorContains(t, acc.Finalize("c", nil), "out of order")
}
