// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	pickupBudgetTestTask           = "build"
	pickupBudgetTestBudget         = "run"
	pickupBudgetTestOldClaim       = "old-claim"
	pickupBudgetTestOldPickup      = "old-pickup"
	pickupBudgetTestSuccessorClaim = "successor-claim"
	pickupBudgetTestReceipt        = "old-pickup-receipt"
	pickupBudgetTestFault          = "signed broker replay failed"
	pickupBudgetTestOtherIdentity  = "different-identity"
	pickupBudgetTestWeight         = 1
	pickupBudgetTestCapacity       = 2
)

func pickupBudgetObservationFixture() (*FirstNodeBroker, ChoreographyPickupSuccessor) {
	binding := ChoreographyRunBinding{Run: "budget-run", Generation: "budget-generation"}
	return &FirstNodeBroker{binding: binding,
			budgets: map[string]int{pickupBudgetTestBudget: pickupBudgetTestCapacity},
			used:    map[string]int{pickupBudgetTestBudget: pickupBudgetTestCapacity},
			tasks: map[string]*indexedChoreographyTask{pickupBudgetTestTask: {
				spec: ChoreographyTaskSpec{Task: pickupBudgetTestTask, Budget: pickupBudgetTestBudget,
					Weight: pickupBudgetTestWeight, MaxAttempts: interpretedContactMaxAttempts},
				attempt: interpretedContactMaxAttempts,
				pickups: map[string]*indexedChoreographyPickup{pickupBudgetTestOldPickup: {
					id: pickupBudgetTestOldPickup, claimOID: pickupBudgetTestOldClaim,
					attempt: choreographyGitFirstAttempt, contactLost: true}},
			}}}, ChoreographyPickupSuccessor{ChoreographyRunBinding: binding,
			Task: pickupBudgetTestTask, ClaimOID: pickupBudgetTestOldClaim, PickupID: pickupBudgetTestOldPickup}
}

func TestChoreographyPickupSuccessorBudgetRequiresExactRetainedIdentity(t *testing.T) {
	cases := []struct {
		name   string
		change func(*FirstNodeBroker, *ChoreographyPickupSuccessor)
		want   string
	}{
		{name: "no successor retains exhausted budget", want: choreographyPickupBudgetHeldErrorText},
		{name: "contact loss attempt increment alone is not a claim", change: func(b *FirstNodeBroker, _ *ChoreographyPickupSuccessor) {
			b.tasks[pickupBudgetTestTask].attempt++
		}, want: choreographyPickupBudgetHeldErrorText},
		{name: "available budget permits event draining", change: func(b *FirstNodeBroker, _ *ChoreographyPickupSuccessor) {
			b.used[pickupBudgetTestBudget] = pickupBudgetTestWeight
		}},
		{name: "accepted successor already owns its charge", change: func(b *FirstNodeBroker, _ *ChoreographyPickupSuccessor) {
			b.tasks[pickupBudgetTestTask].claim = pickupBudgetTestSuccessorClaim
		}},
		{name: "terminal state permits event draining", change: func(b *FirstNodeBroker, _ *ChoreographyPickupSuccessor) {
			b.tasks[pickupBudgetTestTask].terminal = true
		}},
		{name: "exact old receipt permits event draining", change: func(b *FirstNodeBroker, _ *ChoreographyPickupSuccessor) {
			b.tasks[pickupBudgetTestTask].pickups[pickupBudgetTestOldPickup].resultReceiptOID = pickupBudgetTestReceipt
		}},
		{name: "exact old settlement permits event draining", change: func(b *FirstNodeBroker, _ *ChoreographyPickupSuccessor) {
			b.tasks[pickupBudgetTestTask].pickups[pickupBudgetTestOldPickup].settled = true
		}},
		{name: "another run cannot use terminal shortcut", change: func(b *FirstNodeBroker, q *ChoreographyPickupSuccessor) {
			b.tasks[pickupBudgetTestTask].terminal = true
			q.Run = pickupBudgetTestOtherIdentity
		}, want: choreographyPickupBudgetIdentityErrorText},
		{name: "another claim cannot use successor shortcut", change: func(b *FirstNodeBroker, q *ChoreographyPickupSuccessor) {
			b.tasks[pickupBudgetTestTask].claim = pickupBudgetTestSuccessorClaim
			q.ClaimOID = pickupBudgetTestOtherIdentity
		}, want: choreographyPickupBudgetIdentityErrorText},
		{name: "absent claim cannot use successor shortcut", change: func(b *FirstNodeBroker, q *ChoreographyPickupSuccessor) {
			b.tasks[pickupBudgetTestTask].claim = pickupBudgetTestSuccessorClaim
			q.ClaimOID = ""
		}, want: choreographyPickupBudgetIdentityErrorText},
		{name: "missing pickup cannot use terminal shortcut", change: func(b *FirstNodeBroker, q *ChoreographyPickupSuccessor) {
			b.tasks[pickupBudgetTestTask].terminal = true
			q.PickupID = pickupBudgetTestOtherIdentity
		}, want: choreographyPickupBudgetIdentityErrorText},
		{name: "mismatched retained pickup cannot use receipt shortcut", change: func(b *FirstNodeBroker, _ *ChoreographyPickupSuccessor) {
			pickup := b.tasks[pickupBudgetTestTask].pickups[pickupBudgetTestOldPickup]
			pickup.id, pickup.resultReceiptOID = pickupBudgetTestOtherIdentity, pickupBudgetTestReceipt
		}, want: choreographyPickupBudgetIdentityErrorText},
		{name: "contact loss must already be signed", change: func(b *FirstNodeBroker, _ *ChoreographyPickupSuccessor) {
			b.tasks[pickupBudgetTestTask].pickups[pickupBudgetTestOldPickup].contactLost = false
		}, want: choreographyPickupBudgetIdentityErrorText},
		{name: "missing task fails closed", change: func(_ *FirstNodeBroker, q *ChoreographyPickupSuccessor) {
			q.Task = pickupBudgetTestOtherIdentity
		}, want: choreographyPickupBudgetTaskErrorText},
		{name: "broker fault cannot use terminal shortcut", change: func(b *FirstNodeBroker, _ *ChoreographyPickupSuccessor) {
			b.fault = errors.New(pickupBudgetTestFault)
			b.tasks[pickupBudgetTestTask].terminal = true
		}, want: pickupBudgetTestFault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			broker, query := pickupBudgetObservationFixture()
			if tc.change != nil {
				tc.change(broker, &query)
			}
			used := broker.used[pickupBudgetTestBudget]
			attempt := broker.tasks[pickupBudgetTestTask].attempt
			err := broker.CheckPickupSuccessorBudget(query)
			if tc.want != "" {
				require.EqualError(t, err, tc.want)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, used, broker.used[pickupBudgetTestBudget])
			require.Equal(t, attempt, broker.tasks[pickupBudgetTestTask].attempt)
			require.Equal(t, interpretedContactMaxAttempts,
				broker.tasks[pickupBudgetTestTask].spec.EffectiveMaxAttempts())
		})
	}
	var absent *FirstNodeBroker
	_, query := pickupBudgetObservationFixture()
	require.EqualError(t, absent.CheckPickupSuccessorBudget(query), choreographyPickupBudgetTaskErrorText)
}

func TestChoreographyPickupSuccessorBudgetObservesAcceptedProgress(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		name := "successor claims between observation and budget check"
		if terminal {
			name = "successor reaches finality before budget check"
		}
		t.Run(name, func(t *testing.T) {
			fixture, persist, binding, spec, broker, first, accepted := interpretedContactFixture(t, true)
			deadline, err := time.Parse(time.RFC3339,
				broker.tasks[spec.Task].pickups[first.PickupID].cancelReplyDeadlineUTC)
			require.NoError(t, err)
			if remaining := time.Until(deadline.Add(choreographyContactTestClockMargin)); remaining > 0 {
				timer := time.NewTimer(remaining)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-t.Context().Done():
					t.Fatal(t.Context().Err())
				}
			}
			require.NoError(t, broker.MarkPickupContactLost(t.Context(), spec.Task, first.PickupID))
			broker.mu.Lock()
			isSuccessorClaimed := broker.tasks[spec.Task].claim != "" &&
				broker.tasks[spec.Task].claim != accepted.OID
			broker.mu.Unlock()
			require.False(t, isSuccessorClaimed)
			second := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
				Task: spec.Task, Attempt: broker.tasks[spec.Task].attempt,
				Node: first.Node, Branch: spec.Branch, ExpectedOID: broker.tasks[spec.Task].tip,
				InputsDigest: broker.tasks[spec.Task].inputsDigest,
				PickupID:     NewInterpretedChoreographyPickupID(), ProposalID: interpretedContactSecondProposal}
			completeChoreographyFixtureClaim(t, persist, &second)
			secondClaim, err := broker.AcceptClaim(t.Context(), second)
			require.NoError(t, err)
			require.Equal(t, choreographyContactTestCapacity*spec.Weight, broker.used[spec.Budget])
			if terminal {
				resultDocument := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
					Kind: spec.Kind, Run: binding.Run, Generation: binding.Generation,
					PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: second.Attempt,
					Node: second.Node, Branch: spec.Branch}, Status: StatusSucceeded}
				proposal, pack := prepareChoreographyFixturePickupResult(t, fixture,
					choreographyFixturePickupResult{Claim: second, Accepted: secondClaim,
						Result: resultDocument, ProposalID: interpretedContactSecondResult})
				result, err := broker.AcceptResult(t.Context(), proposal, pack)
				require.NoError(t, err)
				require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
					ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
					Attempt: second.Attempt, Node: second.Node, ResultOID: result.OID,
					Succeeded: true}))
				require.Equal(t, spec.Weight, broker.used[spec.Budget])
			}
			query := ChoreographyPickupSuccessor{ChoreographyRunBinding: binding,
				Task: spec.Task, ClaimOID: accepted.OID, PickupID: first.PickupID}
			used := broker.used[spec.Budget]
			require.NoError(t, broker.CheckPickupSuccessorBudget(query))
			require.Equal(t, used, broker.used[spec.Budget])
			require.Equal(t, interpretedContactMaxAttempts, broker.tasks[spec.Task].attempt)
			require.Equal(t, interpretedContactMaxAttempts, broker.tasks[spec.Task].spec.EffectiveMaxAttempts())
			require.False(t, broker.tasks[spec.Task].pickups[first.PickupID].settled,
				"observing progress cannot release the old physical pickup")
			if !terminal {
				_, err := broker.CancelClaim(t.Context(), ChoreographyCancellation{
					ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
					Attempt: second.Attempt, Node: second.Node, ClaimOID: secondClaim.OID,
					AssignmentOID: secondClaim.AssignmentOID})
				require.NoError(t, err)
				require.ErrorContains(t, broker.MarkPickupContactLost(t.Context(), spec.Task, second.PickupID),
					choreographyPickupContactAttemptsExhaustedError)
				require.Equal(t, used, broker.used[spec.Budget])
			}
		})
	}
}
