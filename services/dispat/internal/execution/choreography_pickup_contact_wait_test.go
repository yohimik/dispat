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
	pickupContactWaitSignedGrace   = 3 * time.Second
	pickupContactWaitObserverGrace = time.Millisecond
	pickupContactWaitEventBuffer   = 1
	pickupContactWaitNextAttempt   = 2
	pickupContactWaitBound         = 10 * time.Second
	pickupContactWaitInitialClaim  = "contact-wait-first-claim"
)

// Delay the signed Cancel feed until the observer's local fallback expires.
// The broker and persistence retain the original signed deadline throughout.
type pickupContactWaitProbe struct {
	*pickupBudgetWaitProbe
	cancelDeadline time.Time
	isExpired      bool
	isResumed      bool
	resumed        chan time.Time
	release        chan struct{}
}

func (p *pickupContactWaitProbe) PersistCancel(ctx context.Context,
	cancel ChoreographyCancellation) (ChoreographyCancellationReceipt, error) {
	receipt, err := p.GitChoreographyPersist.PersistCancel(ctx, cancel)
	if err != nil {
		return receipt, err
	}
	deadline, err := time.Parse(time.RFC3339Nano, receipt.CancelReplyDeadlineUTC)
	if err != nil {
		return receipt, err
	}
	p.mu.Lock()
	p.cancelDeadline = deadline
	p.mu.Unlock()
	return receipt, nil
}

func (p *pickupContactWaitProbe) WaitTaskEvent(ctx context.Context, task string,
	after ChoreographyEventCursor) (ChoreographyEvent, ChoreographyEventCursor, error) {
	p.mu.Lock()
	if !p.cancelDeadline.IsZero() && !p.isExpired {
		p.isExpired = true
		p.mu.Unlock()
		<-ctx.Done()
		return ChoreographyEvent{}, ChoreographyEventCursor{}, ctx.Err()
	}
	if p.isExpired && !p.isResumed {
		p.isResumed = true
		p.mu.Unlock()
		deadline, _ := ctx.Deadline()
		p.resumed <- deadline
		<-p.release
		return p.pickupBudgetWaitProbe.WaitTaskEvent(ctx, task, after)
	}
	p.mu.Unlock()
	return p.pickupBudgetWaitProbe.WaitTaskEvent(ctx, task, after)
}

func TestChoreographyPickupContactWaitResumesExactSignedDeadline(t *testing.T) {
	for _, mode := range []string{bootstrapCompilationModeName, bootstrapInterpretationModeName} {
		t.Run(mode, func(t *testing.T) {
			maxAttempts := choreographyAttemptPolicyLegacyUnspecified
			if mode == bootstrapInterpretationModeName {
				maxAttempts = interpretedContactMaxAttempts
			}
			fixture, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t,
				choreographyLedgerFixtureSettings{AuthorityID: interpretedAttemptTestAuthority,
					AdmissionSeconds: choreographyTestAdmissionSeconds, Mode: mode,
					MaxAttempts: maxAttempts, RetryAfterUncertainAttempt: true,
					Capacity:        choreographyContactTestCapacity,
					DeadlineSeconds: choreographyContactTestDeadlineSeconds})
			probe := &pickupContactWaitProbe{pickupBudgetWaitProbe: &pickupBudgetWaitProbe{
				GitChoreographyPersist: persist, parked: make(chan struct{}, pickupContactWaitEventBuffer)},
				resumed: make(chan time.Time, pickupContactWaitEventBuffer), release: make(chan struct{})}
			opts := choreographyContactBrokerOptions(persist, spec)
			opts.Persist = probe
			opts.CancelResponseDuration = pickupContactWaitSignedGrace
			broker, err := RecoverFirstNodeBroker(t.Context(), opts)
			require.NoError(t, err)
			pickupID := NewChoreographyPickupID()
			if mode == bootstrapInterpretationModeName {
				pickupID = NewInterpretedChoreographyPickupID()
			}
			claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
				Task: spec.Task, Attempt: choreographyGitFirstAttempt, Node: choreographyContactTestNode,
				Branch: spec.Branch, ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
				PickupID: pickupID, ProposalID: pickupContactWaitInitialClaim}
			completeChoreographyFixtureClaim(t, persist, &claim)
			accepted, err := broker.AcceptClaim(t.Context(), claim)
			require.NoError(t, err)
			require.True(t, accepted.Accepted)
			coordinator := &Coordinator{Run: binding.Run, GraphRootOID: persist.opts.GraphRootOID,
				choreographyBroker: broker, choreographyPersist: persist,
				canonicalMailbox: fixture.mailbox, choreographyInitialized: true,
				Timeouts: Timeouts{Cancel: pickupContactWaitObserverGrace}}
			ctx, cancel := context.WithCancel(t.Context())
			result := make(chan pickupBudgetWaitAnswer, pickupContactWaitEventBuffer)
			finished := make(chan struct{})
			var releaseOnce sync.Once
			resume := func() { releaseOnce.Do(func() { close(probe.release) }) }
			t.Cleanup(func() {
				resume()
				cancel()
				select {
				case <-finished:
				case <-time.After(pickupContactWaitBound):
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
			var resumedDeadline time.Time
			select {
			case resumedDeadline = <-probe.resumed:
			case got := <-result:
				t.Fatalf("early local expiry aborted the signed cancellation wait: %v", got.err)
			case <-time.After(pickupContactWaitBound):
				t.Fatal("task observer did not resume the signed cancellation deadline")
			}
			broker.mu.Lock()
			task := broker.tasks[spec.Task]
			deadlineText := task.pickups[pickupID].cancelReplyDeadlineUTC
			attempt, used := task.attempt, broker.used[spec.Budget]
			isLost, isSettled := task.pickups[pickupID].contactLost, task.pickups[pickupID].settled
			broker.mu.Unlock()
			deadline, err := time.Parse(time.RFC3339Nano, deadlineText)
			require.NoError(t, err)
			require.Equal(t, deadline, resumedDeadline, "the observer must not extend the signed fence")
			require.Equal(t, claim.Attempt, attempt, "no successor opens before signed contact loss")
			require.Equal(t, spec.Weight, used)
			require.False(t, isLost)
			require.False(t, isSettled)
			resume()
			select {
			case <-probe.parked:
			case got := <-result:
				t.Fatalf("signed cancellation wait aborted before contact loss: %v", got.err)
			case <-time.After(pickupContactWaitBound):
				t.Fatal("task observer did not reach signed contact loss")
			}
			require.False(t, time.Now().Before(deadline))
			broker.mu.Lock()
			attempt, used = task.attempt, broker.used[spec.Budget]
			isLost, isSettled = task.pickups[pickupID].contactLost, task.pickups[pickupID].settled
			retainedDeadline := task.pickups[pickupID].cancelReplyDeadlineUTC
			broker.mu.Unlock()
			require.Equal(t, pickupContactWaitNextAttempt, attempt)
			require.Equal(t, spec.Weight, used, "contact loss retains the old physical charge")
			require.True(t, isLost)
			require.False(t, isSettled)
			require.Equal(t, deadlineText, retainedDeadline)
			page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, choreographyLedgerPageMaxEvents)
			require.NoError(t, err)
			var loss ChoreographyEvent
			for _, event := range page.Events {
				if event.Kind == ChoreographyPickupContactLost {
					loss = event
				}
			}
			require.Equal(t, ChoreographyPickupContactLost, loss.Kind)
			require.Equal(t, pickupID, loss.PickupID)
			require.Equal(t, deadlineText, loss.CancelReplyDeadlineUTC)
			cancel()
			select {
			case got := <-result:
				require.Error(t, got.err)
				require.True(t, got.outcome.UnresolvedOffer)
				require.Nil(t, got.outcome.NoClaim)
			case <-time.After(pickupContactWaitBound):
				t.Fatal("explicit cancellation did not end the pending observer")
			}
		})
	}
}
