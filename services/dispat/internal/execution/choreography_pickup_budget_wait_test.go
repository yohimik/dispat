// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	pickupBudgetWaitOtherTask           = "unrelated-build"
	pickupBudgetWaitOtherProposal       = "unrelated-claim"
	pickupBudgetWaitRefusedProposal     = "capacity-refused-successor"
	pickupBudgetWaitBound               = 10 * time.Second
	pickupBudgetWaitTaskDeadlineSeconds = 30
)

type pickupBudgetWaitProbe struct {
	*GitChoreographyPersist
	mu     sync.Mutex
	loss   ChoreographyEventCursor
	parked chan struct{}
}

func (p *pickupBudgetWaitProbe) PersistPickupContactLost(ctx context.Context, loss ChoreographyPickupContactLoss) error {
	if err := p.GitChoreographyPersist.PersistPickupContactLost(ctx, loss); err != nil {
		return err
	}
	cursor, err := p.LedgerHead(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.loss = cursor
	p.mu.Unlock()
	return nil
}

func (p *pickupBudgetWaitProbe) WaitTaskEvent(ctx context.Context, task string,
	after ChoreographyEventCursor) (ChoreographyEvent, ChoreographyEventCursor, error) {
	p.mu.Lock()
	isAfterLoss := p.loss.OID != "" && after == p.loss
	p.mu.Unlock()
	if isAfterLoss {
		select {
		case p.parked <- struct{}{}:
		default:
		}
	}
	return p.GitChoreographyPersist.WaitTaskEvent(ctx, task, after)
}

type pickupBudgetWaitAnswer struct {
	outcome release.StageOutcome
	err     error
}

func TestChoreographyPickupSuccessorWaitsForSignedCapacityOrStop(t *testing.T) {
	for _, hasUnrelatedPickup := range []bool{false, true} {
		name := "old pickup holds the only slot until independent cancellation"
		if hasUnrelatedPickup {
			name = "unrelated signed settlement admits a successor"
		}
		t.Run(name, func(t *testing.T) {
			capacity := pickupBudgetTestWeight
			tasks := []ChoreographyTaskSpec{{Task: pickupBudgetTestTask}}
			if hasUnrelatedPickup {
				capacity++
				tasks = append(tasks, ChoreographyTaskSpec{Task: pickupBudgetWaitOtherTask})
			}
			fixture, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t,
				choreographyLedgerFixtureSettings{AuthorityID: interpretedAttemptTestAuthority,
					AdmissionSeconds: choreographyTestAdmissionSeconds, Tasks: tasks,
					Mode: bootstrapInterpretationModeName, MaxAttempts: interpretedContactMaxAttempts,
					RetryAfterUncertainAttempt: true, Capacity: capacity,
					DeadlineSeconds: pickupBudgetWaitTaskDeadlineSeconds})
			probe := &pickupBudgetWaitProbe{GitChoreographyPersist: persist, parked: make(chan struct{}, 1)}
			opts := choreographyFixtureBrokerOptions(persist)
			opts.Persist = probe
			opts.Members[0].Capacity = capacity
			opts.NodeCapacity[interpretedContactNode] = capacity
			opts.Budgets[spec.Budget] = capacity
			opts.CancelResponseDuration = time.Duration(choreographyContactTestDeadlineSeconds) * time.Second
			broker, err := RecoverFirstNodeBroker(t.Context(), opts)
			require.NoError(t, err)
			claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
				Task: spec.Task, Attempt: choreographyGitFirstAttempt, Node: interpretedContactNode,
				Branch: spec.Branch, ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
				PickupID: NewInterpretedChoreographyPickupID(), ProposalID: interpretedContactFirstProposal}
			completeChoreographyFixtureClaim(t, persist, &claim)
			accepted, err := broker.AcceptClaim(t.Context(), claim)
			require.NoError(t, err)
			require.True(t, accepted.Accepted)
			var other ChoreographyClaimProposal
			var otherAccepted ChoreographyReceipt
			if hasUnrelatedPickup {
				otherSpec := persist.opts.Tasks[1]
				other = ChoreographyClaimProposal{ChoreographyRunBinding: binding,
					Task: otherSpec.Task, Attempt: choreographyGitFirstAttempt, Node: interpretedContactNode,
					Branch: otherSpec.Branch, ExpectedOID: otherSpec.ReservationOID, InputsDigest: otherSpec.InputsDigest,
					PickupID: NewInterpretedChoreographyPickupID(), ProposalID: pickupBudgetWaitOtherProposal}
				completeChoreographyFixtureClaim(t, persist, &other)
				otherAccepted, err = broker.AcceptClaim(t.Context(), other)
				require.NoError(t, err)
				require.True(t, otherAccepted.Accepted)
			}
			_, err = broker.CancelClaim(t.Context(), ChoreographyCancellation{
				ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
				Attempt: claim.Attempt, Node: claim.Node, ClaimOID: accepted.OID,
				AssignmentOID: accepted.AssignmentOID})
			require.NoError(t, err)
			coordinator := &Coordinator{Run: binding.Run, GraphRootOID: persist.opts.GraphRootOID,
				choreographyBroker: broker, choreographyPersist: persist,
				canonicalMailbox: fixture.mailbox, choreographyInitialized: true,
				Timeouts: Timeouts{Cancel: time.Duration(choreographyContactTestDeadlineSeconds) * time.Second}}
			ctx, cancel := context.WithCancel(t.Context())
			result := make(chan pickupBudgetWaitAnswer, 1)
			finished := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(pickupBudgetWaitBound):
					t.Error("task observer did not settle after explicit cancellation")
				}
			})
			go func() {
				defer close(finished)
				outcome, waitErr := coordinator.waitCompiledTask(ctx, compiledTaskWaitOptions{
					TaskID: spec.Task, Request: release.StageRequest{TaskID: spec.Task, Stage: string(KindBuild),
						Release: &plan.Release{Pkg: &model.Package{Name: spec.Task}}}})
				result <- pickupBudgetWaitAnswer{outcome: outcome, err: waitErr}
			}()
			select {
			case <-probe.parked:
			case got := <-result:
				t.Fatalf("full budget aborted the contact-lost task instead of parking: %v", got.err)
			case <-time.After(pickupBudgetWaitBound):
				t.Fatal("task observer did not reach the signed contact-loss wait")
			}
			broker.mu.Lock()
			used := broker.used[spec.Budget]
			tip, attempt := broker.tasks[spec.Task].tip, broker.tasks[spec.Task].attempt
			isOldSettled := broker.tasks[spec.Task].pickups[claim.PickupID].settled
			broker.mu.Unlock()
			require.Equal(t, capacity, used)
			require.False(t, isOldSettled)
			successor := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
				Task: spec.Task, Attempt: attempt, Node: claim.Node, Branch: spec.Branch,
				ExpectedOID: tip, InputsDigest: spec.InputsDigest,
				PickupID: NewInterpretedChoreographyPickupID(), ProposalID: pickupBudgetWaitRefusedProposal}
			completeChoreographyFixtureClaim(t, persist, &successor)
			refused, err := broker.AcceptClaim(t.Context(), successor)
			require.NoError(t, err)
			require.False(t, refused.Accepted)
			require.True(t, refused.Refused)
			require.Equal(t, ChoreographyRefusalCapacity, refused.RefusalReason)
			require.Equal(t, choreographyGitRefusalBudgetScope, refused.CapacityScope)
			require.NotEmpty(t, refused.FenceOID)
			if hasUnrelatedPickup {
				otherSpec := persist.opts.Tasks[1]
				otherResult := acceptPackageFixtureResult(t, fixture, broker, binding, otherSpec,
					otherAccepted, other, choreographyContactTestSecondResult, StatusSucceeded)
				require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
					ChoreographyRunBinding: binding, Task: other.Task, Branch: other.Branch,
					Attempt: other.Attempt, Node: other.Node, ResultOID: otherResult.OID, Succeeded: true}))
				successor.ProposalID = interpretedContactSecondProposal
				completeChoreographyFixtureClaim(t, persist, &successor)
				second, acceptErr := broker.AcceptClaim(t.Context(), successor)
				require.NoError(t, acceptErr)
				require.True(t, second.Accepted)
			}
			select {
			case got := <-result:
				t.Fatalf("observer returned without a terminal event or independent stop: %v", got.err)
			default:
			}
			cancel()
			select {
			case got := <-result:
				require.Error(t, got.err)
				require.NotErrorIs(t, got.err, ErrChoreographyPickupCapacityHeld)
				require.True(t, got.outcome.UnresolvedOffer, "contact loss retains an unsettled accepted claim after stop")
				require.Nil(t, got.outcome.NoClaim, "an accepted physical pickup cannot become a no-claim outcome")
			case <-time.After(pickupBudgetWaitBound):
				t.Fatal("explicit cancellation did not end the pending observer")
			}
			broker.mu.Lock()
			isTerminal := broker.tasks[spec.Task].terminal
			isPendingInert := broker.tasks[spec.Task].pendingInertCancel
			isOldSettled = broker.tasks[spec.Task].pickups[claim.PickupID].settled
			used = broker.used[spec.Budget]
			broker.mu.Unlock()
			require.Equal(t, capacity, used, "no unsettled physical charge was released")
			require.False(t, isOldSettled)
			require.False(t, isTerminal, "stop cannot fabricate inert finality for an unsettled pickup")
			require.False(t, isPendingInert)
			coldPersist, err := NewGitChoreographyPersist(persist.opts)
			require.NoError(t, err)
			opts.Persist = coldPersist
			cold, err := RecoverFirstNodeBroker(t.Context(), opts)
			require.NoError(t, err)
			require.Equal(t, capacity, cold.used[spec.Budget])
			require.False(t, cold.tasks[spec.Task].terminal)
			require.False(t, cold.tasks[spec.Task].pendingInertCancel)
			require.False(t, cold.tasks[spec.Task].pickups[claim.PickupID].settled)
			page, err := coldPersist.ReadEvents(t.Context(), ChoreographyEventCursor{}, choreographyLedgerPageMaxEvents)
			require.NoError(t, err)
			for _, event := range page.Events {
				require.NotEqual(t, ChoreographyInertCancelled, event.Kind, "stop must not publish fabricated no-claim finality")
			}
		})
	}
}
