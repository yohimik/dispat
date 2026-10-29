// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func installCompiledRefreshRun(fixture *compiledRefreshFixture) {
	fixture.worker.compiledRuns = map[string]*workerCompiledRun{fixture.run.binding.Run: fixture.run}
}

func TestCompiledStaleClaimImmediatelyRefreshesOnceBeforeOrdinaryBackoff(t *testing.T) {
	for _, mode := range []string{bootstrapCompilationModeName, bootstrapInterpretationModeName, bootstrapHybridModeName} {
		t.Run(mode, func(t *testing.T) {
			fixture := newCompiledRefreshFixture(t)
			fixture.run.bootstrap.Mode = mode
			installCompiledRefreshRun(fixture)
			polls := 0
			fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
				polls++
				if polls == 2 {
					return fixture.page(fixture.fenceEvent()), nil
				}
				cursor := fixture.run.frontier.Cursor()
				return ChoreographyEventPage{Cursor: cursor, Head: fixture.fence}, nil
			}
			fixture.client.claim = func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
				fixture.proposals = append(fixture.proposals, proposal)
				if len(fixture.proposals) == 2 {
					fixture.fence = ChoreographyEventCursor{OID: strings.Repeat("9", 40), Sequence: 3}
				}
				return fixture.refusal(t, proposal, ChoreographyRefusalStale), nil
			}
			progress, err := fixture.worker.pollCompiledRuns(t.Context())
			require.NoError(t, err)
			require.True(t, progress)
			require.Equal(t, 2, polls, "one ordinary read and one immediate refresh, even if a second newer fence exists")
			require.Len(t, fixture.proposals, 2)
			require.Equal(t, strings.Repeat("e", 40), fixture.proposals[1].ExpectedLedgerOID)
			require.NotEqual(t, fixture.proposals[0].PickupID, fixture.proposals[1].PickupID)
			require.Equal(t, fixture.fence, fixture.run.claimRefresh.fence)
			fixture.requireNoHeldSlots(t)

			_, err = fixture.worker.pollCompiledRuns(t.Context())
			require.NoError(t, err)
			require.Equal(t, 3, polls, "an unchanged page must not trigger another immediate read")
			require.Len(t, fixture.proposals, 2)
		})
	}
}

func TestCompiledImmediateRefreshDoesNotInferAdvanceFromHeadOrUnavailableFeed(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "advisory newer head", true: "unavailable feed"}[unavailable], func(t *testing.T) {
			fixture := newCompiledRefreshFixture(t)
			installCompiledRefreshRun(fixture)
			polls := 0
			fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
				polls++
				if polls == 2 && unavailable {
					return ChoreographyEventPage{}, errors.New("feed unavailable")
				}
				cursor := fixture.run.frontier.Cursor()
				return ChoreographyEventPage{Cursor: cursor, Head: fixture.fence}, nil
			}
			_, err := fixture.worker.pollCompiledRuns(t.Context())
			if unavailable {
				require.ErrorContains(t, err, "feed unavailable")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 2, polls)
			require.Len(t, fixture.proposals, 1)
			require.NotNil(t, fixture.run.claimRefresh)
			fixture.requireNoHeldSlots(t)
			_, err = fixture.worker.pollCompiledRuns(t.Context())
			require.NoError(t, err)
			require.Equal(t, 3, polls)
			require.Len(t, fixture.proposals, 1)
		})
	}
}

func TestCompiledImmediateRefreshRechecksSignedStoppingMembershipAndCompetingClaim(t *testing.T) {
	for _, kind := range []ChoreographyEventKind{ChoreographyRunStopping, ChoreographyMemberDraining, ChoreographyClaimAccepted} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newCompiledRefreshFixture(t)
			installCompiledRefreshRun(fixture)
			polls := 0
			fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
				polls++
				if polls == 1 {
					cursor := fixture.run.frontier.Cursor()
					return ChoreographyEventPage{Cursor: cursor, Head: cursor}, nil
				}
				event := ChoreographyEvent{Kind: kind, Task: choreographyRunTaskID, TaskOID: fixture.run.rootOID, GraphRootOID: fixture.run.rootOID}
				switch kind {
				case ChoreographyMemberDraining:
					event.Task, event.Node = choreographyMembershipTaskID, fixture.worker.Node
					event.Member = &ChoreographyRuntimeNode{Name: fixture.worker.Node, Capacity: 2}
				case ChoreographyClaimAccepted:
					task := fixture.run.frontier.tasks[fixture.proposals[0].Task].spec
					event.Task, event.TaskOID, event.PriorTaskOID = task.Task, "competing-claim", task.ReservationOID
					event.Node, event.Attempt, event.AssignmentOID = "another-node", 1, "assignment"
					fixture.run.frontier.tasks["cli:build"].state = frontierBusy
				}
				return fixture.page(event), nil
			}
			_, err := fixture.worker.pollCompiledRuns(t.Context())
			require.NoError(t, err)
			require.Equal(t, 2, polls)
			require.Nil(t, fixture.run.claimRefresh)
			require.Len(t, fixture.proposals, 1)
			fixture.requireNoHeldSlots(t)
		})
	}
}

func TestCompiledImmediateRefreshFollowsExactPendingReconciliationOnly(t *testing.T) {
	fixture := newCompiledRefreshFixture(t)
	installCompiledRefreshRun(fixture)
	polls := 0
	fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
		polls++
		if polls == 3 {
			return fixture.page(fixture.fenceEvent()), nil
		}
		cursor := fixture.run.frontier.Cursor()
		return ChoreographyEventPage{Cursor: cursor, Head: cursor}, nil
	}
	fixture.client.claim = func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		fixture.proposals = append(fixture.proposals, proposal)
		if len(fixture.proposals) == 2 {
			return fixture.refusal(t, proposal, ChoreographyRefusalStale), nil
		}
		return ChoreographyReceipt{}, errors.New("reply unavailable")
	}
	_, err := fixture.worker.pollCompiledRuns(t.Context())
	require.ErrorContains(t, err, "reply unavailable")
	require.Equal(t, 1, polls, "an ambiguous reply is not an immediate-refresh hint")
	require.Len(t, fixture.run.pending, 1)
	held, err := fixture.worker.NodeSlots.Held(t.Context())
	require.NoError(t, err)
	require.Len(t, held, 1)
	_, err = fixture.worker.pollCompiledRuns(t.Context())
	require.ErrorContains(t, err, "reply unavailable")
	require.Equal(t, 3, polls)
	require.Len(t, fixture.proposals, 3)
	require.Equal(t, fixture.proposals[0], fixture.proposals[1])
	require.NotEqual(t, fixture.proposals[1].PickupID, fixture.proposals[2].PickupID)
	require.Equal(t, fixture.fence.OID, fixture.proposals[2].ExpectedLedgerOID)
	held, err = fixture.worker.NodeSlots.Held(t.Context())
	require.NoError(t, err)
	require.Len(t, held, 1, "only the new ambiguous proposal retains capacity")
}
