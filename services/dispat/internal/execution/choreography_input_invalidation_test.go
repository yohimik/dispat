// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type invalidationFeedPersist struct {
	ChoreographyPersistable
	events   []ChoreographyEvent
	prepared map[string]string
	request  ChoreographyInputInvalidationRequest
}

func (p *invalidationFeedPersist) ReadEvents(_ context.Context, cursor ChoreographyEventCursor, _ int) (ChoreographyEventPage, error) {
	if cursor.Sequence != 0 {
		return ChoreographyEventPage{Cursor: cursor}, nil
	}
	return ChoreographyEventPage{Events: p.events, Cursor: ChoreographyEventCursor{OID: "signed-feed", Sequence: uint64(len(p.events))}}, nil
}
func (p *invalidationFeedPersist) ReadAcceptedClaimSourceSnapshot(_ context.Context, event ChoreographyEvent, _ string) (string, error) {
	return p.prepared[event.TaskOID], nil
}
func (p *invalidationFeedPersist) PersistInputInvalidation(_ context.Context, request ChoreographyInputInvalidationRequest) error {
	p.request = request
	return nil
}
func (p *invalidationFeedPersist) StopClaims(context.Context) (ChoreographyEventCursor, error) {
	return ChoreographyEventCursor{OID: "signed-stop", Sequence: uint64(len(p.events) + 2)}, nil
}

func TestChoreographyInvalidationTraversesVirtualAndTerminalRelaysAndMatchesOldInput(t *testing.T) {
	oldOID, newOID, otherOID := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation", PlanDigest: "plan", GraphDigest: "graph"}
	persist := &invalidationFeedPersist{prepared: map[string]string{"claim-match": oldOID, "claim-other": otherOID},
		events: []ChoreographyEvent{
			{Kind: ChoreographyClaimAccepted, Task: "build", TaskOID: "claim-match"},
			{Kind: ChoreographyResultAccepted, Task: "build", PriorTaskOID: "claim-match"},
			{Kind: ChoreographyClaimAccepted, Task: "unrelated", TaskOID: "claim-other"},
			{Kind: ChoreographyResultAccepted, Task: "unrelated", PriorTaskOID: "claim-other"},
		}}
	b := &FirstNodeBroker{binding: binding, graphRootOID: "complete-root", persist: persist,
		budgetWake: make(chan struct{}), tasks: map[string]*indexedChoreographyTask{
			"build":     {spec: ChoreographyTaskSpec{Task: "build", Package: "core"}, children: []string{"gate"}},
			"gate":      {spec: ChoreographyTaskSpec{Task: "gate", Virtual: true}, children: []string{"middle"}},
			"middle":    {spec: ChoreographyTaskSpec{Task: "middle", Package: "core"}, terminal: true, children: []string{"leaf"}},
			"leaf":      {spec: ChoreographyTaskSpec{Task: "leaf", Package: "consumer"}},
			"unrelated": {spec: ChoreographyTaskSpec{Task: "unrelated", Package: "core"}},
		}}
	require.NoError(t, b.InvalidatePackagePreparedInput(t.Context(),
		ChoreographyPreparedInputChange{Repository: "repo", Package: "core",
			OldPreparedOID: oldOID, NewPreparedOID: newOID}))
	require.Equal(t, []string{"build", "leaf"}, persist.request.AffectedTasks)
	require.Equal(t, oldOID, persist.request.OldPreparedOID)
	require.Equal(t, newOID, persist.request.NewPreparedOID)
	require.True(t, b.stopping)
	require.True(t, b.invalidated)
}

func TestChoreographyAcceptedSourceSnapshotRejectsConflictingClaims(t *testing.T) {
	firstOID, secondOID := strings.Repeat("a", 40), strings.Repeat("b", 40)
	persist := &invalidationFeedPersist{prepared: map[string]string{"claim-a": firstOID, "claim-b": secondOID},
		events: []ChoreographyEvent{{Kind: ChoreographyClaimAccepted, Task: "build-a", TaskOID: "claim-a"},
			{Kind: ChoreographyResultAccepted, Task: "build-a", PriorTaskOID: "claim-a"},
			{Kind: ChoreographyClaimAccepted, Task: "build-b", TaskOID: "claim-b"},
			{Kind: ChoreographyResultAccepted, Task: "build-b", PriorTaskOID: "claim-b"}}}
	b := &FirstNodeBroker{persist: persist, tasks: map[string]*indexedChoreographyTask{
		"build-a": {spec: ChoreographyTaskSpec{Task: "build-a", Package: "core", Kind: string(KindBuild)}},
		"build-b": {spec: ChoreographyTaskSpec{Task: "build-b", Package: "core", Kind: string(KindBuild)}},
	}}
	_, _, err := b.AcceptedSourceSnapshot(t.Context(), "repo", "core")
	require.ErrorContains(t, err, "disagree")
	persist.prepared["claim-b"] = firstOID
	oid, found, err := b.AcceptedSourceSnapshot(t.Context(), "repo", "core")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, firstOID, oid)
}
