// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChoreographyInertHistorySettlesOnlyExactPhysicalClaim(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*ChoreographyEvent)
	}{
		{name: "exact winner leaves older unknown pickup"},
		{name: "wrong task", edit: func(e *ChoreographyEvent) { e.Task = "other" }},
		{name: "wrong claim", edit: func(e *ChoreographyEvent) { e.ClaimOID = "old-claim" }},
		{name: "wrong pickup", edit: func(e *ChoreographyEvent) { e.PickupID = "old-pickup" }},
		{name: "wrong node", edit: func(e *ChoreographyEvent) { e.Node = "other" }},
		{name: "wrong attempt", edit: func(e *ChoreographyEvent) { e.Attempt = 1 }},
		{name: "wrong receipt", edit: func(e *ChoreographyEvent) { e.ResultReceiptOID = "other" }},
		{name: "wrong output", edit: func(e *ChoreographyEvent) { e.OutputOID = "other" }},
		{name: "wrong budget", edit: func(e *ChoreographyEvent) { e.Budget = "other" }},
		{name: "wrong weight", edit: func(e *ChoreographyEvent) { e.Weight = 2 }},
	} {
		t.Run(change.name, func(t *testing.T) {
			spec := ChoreographyTaskSpec{Task: "build", Budget: "build", Weight: 1, ReservationOID: "root"}
			history := newChoreographyInertHistory(spec)
			for _, event := range []ChoreographyEvent{
				{Kind: ChoreographyClaimAccepted, Task: spec.Task, TaskOID: "old-claim", Node: "node", Attempt: 1, PickupID: "old-pickup"},
				{Kind: ChoreographyPickupContactLost, Task: spec.Task, TaskOID: "cancel", ClaimOID: "old-claim", Node: "node", Attempt: 1, PickupID: "old-pickup"},
				{Kind: ChoreographyClaimAccepted, Task: spec.Task, TaskOID: "new-claim", Node: "node", Attempt: 2, PickupID: "new-pickup"},
				{Kind: ChoreographyResultAccepted, Task: spec.Task, TaskOID: "new-claim", ClaimOID: "new-claim", Node: "node", Attempt: 2,
					PickupID: "new-pickup", ResultReceiptOID: "new-receipt", OutputOID: "new-output"},
			} {
				require.NoError(t, history.apply(spec, event))
			}
			require.Len(t, history.claims, 2, "contact loss and accepted raw reply cannot settle either claim")
			event := ChoreographyEvent{Kind: ChoreographyTerminal, Task: spec.Task, TaskOID: "terminal", ClaimOID: "new-claim",
				Node: "node", Attempt: 2, PickupID: "new-pickup", ResultReceiptOID: "new-receipt", OutputOID: "new-output",
				Budget: spec.Budget, Weight: spec.Weight}
			if change.edit != nil {
				change.edit(&event)
				require.Error(t, history.apply(spec, event))
				require.Len(t, history.claims, 2)
				require.False(t, history.terminal)
				return
			}
			require.NoError(t, history.apply(spec, event))
			require.Len(t, history.claims, 1)
			require.Contains(t, history.claims, "old-claim")
		})
	}
}

func TestChoreographyInertPrefixCannotBorrowLaterSettlement(t *testing.T) {
	spec := ChoreographyTaskSpec{Task: "native", Budget: "build", Weight: 1, ReservationOID: "root"}
	events := []ChoreographyEvent{
		{Sequence: 1, Kind: ChoreographyClaimAccepted, Task: spec.Task, TaskOID: "claim", Node: "node", Attempt: 1},
		{Sequence: 2, Kind: ChoreographyResultAccepted, Task: spec.Task, TaskOID: "receipt", PriorTaskOID: "claim", Node: "node", Attempt: 1, OutputOID: "output"},
		{Sequence: 3, Kind: ChoreographyRetryReady, Task: spec.Task, TaskOID: "retry", PriorTaskOID: "receipt", ResultReceiptOID: "receipt",
			Node: "node", Attempt: 1, OutputOID: "output", Budget: spec.Budget, Weight: spec.Weight},
	}
	persist := &GitChoreographyPersist{tasks: map[string]ChoreographyTaskSpec{spec.Task: spec},
		events: events, taskEvents: map[string][]int{spec.Task: {0, 1, 2}}}
	require.Error(t, persist.verifyInertPrefixLocked(ChoreographyInertCancellation{
		Task: spec.Task, ExpectedOID: "receipt", Attempt: 1}, 2),
		"a later signed retry must not retroactively authorize an earlier inert cancellation")
	require.NoError(t, persist.verifyInertPrefixLocked(ChoreographyInertCancellation{
		Task: spec.Task, ExpectedOID: "retry", Attempt: 2}, 3),
		"the exact settled legacy retry has no active claim at its later prefix")
}

func TestChoreographyInertHistoryMatchesLocalAuthorityClaim(t *testing.T) {
	spec := ChoreographyTaskSpec{Task: "publish", LocalAuthority: true, Budget: "publish", Weight: 1, ReservationOID: "root"}
	history := newChoreographyInertHistory(spec)
	require.NoError(t, history.apply(spec, ChoreographyEvent{Kind: ChoreographyClaimAccepted,
		Task: spec.Task, TaskOID: "claim", Node: "node", Attempt: 1}))
	event := ChoreographyEvent{Kind: ChoreographyTerminal, Task: spec.Task, TaskOID: "terminal",
		PriorTaskOID: "other-claim", Node: "node", Attempt: 1, Budget: spec.Budget, Weight: spec.Weight}
	require.Error(t, history.apply(spec, event))
	require.Len(t, history.claims, 1)
	event.PriorTaskOID = "claim"
	require.NoError(t, history.apply(spec, event))
	require.Empty(t, history.claims)
}
