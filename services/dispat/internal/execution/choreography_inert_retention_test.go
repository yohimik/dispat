// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	inertRetentionEventLimit      = choreographyLedgerPageMaxEvents
	inertRetentionForgeryIdentity = "invalid-inert-after-contact-loss"
	inertRetentionNativeProposal  = "native-claim-before-inert-stop"
)

func TestChoreographyInertCancellationRejectsRetainedPickupAcrossReaders(t *testing.T) {
	fixture, persist, binding, spec, broker, claim, _ := interpretedContactFixture(t, true)
	deadline, err := time.Parse(time.RFC3339, broker.tasks[spec.Task].pickups[claim.PickupID].cancelReplyDeadlineUTC)
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
	require.NoError(t, broker.MarkPickupContactLost(t.Context(), spec.Task, claim.PickupID))
	_, err = broker.StopClaims(t.Context())
	require.NoError(t, err)
	task := broker.tasks[spec.Task]
	outcome := ChoreographyInertCancellation{ChoreographyRunBinding: binding,
		GraphRootOID: persist.opts.GraphRootOID, Task: spec.Task, Attempt: task.attempt,
		Branch: spec.Branch, ExpectedOID: task.tip, Package: spec.Package, Cause: choreographyInertCancelCause}
	tip, used, nodeUsed := task.tip, broker.used[spec.Budget], broker.nodeUsed[claim.Node]
	require.ErrorContains(t, broker.CancelInertTask(t.Context(), spec.Task), choreographyInertCancelStateError)
	_, err = persist.PersistInertCancellation(t.Context(), outcome)
	require.ErrorContains(t, err, choreographyInertCancelStateError)
	require.Equal(t, tip, remoteChoreographyOID(t, fixture, spec.Branch))
	require.False(t, task.terminal)
	require.False(t, task.pendingInertCancel)
	require.Equal(t, used, broker.used[spec.Budget])
	require.Equal(t, nodeUsed, broker.nodeUsed[claim.Node])

	coldPersist, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	cold, err := RecoverFirstNodeBroker(t.Context(), choreographyContactBrokerOptions(coldPersist, spec))
	require.NoError(t, err)
	require.Equal(t, used, cold.used[spec.Budget])
	require.Equal(t, nodeUsed, cold.nodeUsed[claim.Node])
	require.False(t, cold.tasks[spec.Task].terminal)
	require.False(t, cold.tasks[spec.Task].pickups[claim.PickupID].settled)

	document, err := json.Marshal(outcome)
	require.NoError(t, err)
	invalidOID, err := fixture.mailbox.commitMessage(t.Context(), choreographyInertCancelKind,
		document, []string{tip}, nil)
	require.NoError(t, err)
	head, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	event := ChoreographyEvent{Kind: ChoreographyInertCancelled, Task: spec.Task,
		Attempt: outcome.Attempt, TaskOID: invalidOID, PriorTaskOID: tip,
		GraphRootOID: persist.opts.GraphRootOID, Package: spec.Package, BlockedBy: choreographyInertCancelCause}
	require.ErrorContains(t, broker.replayEvent(event), choreographyInertCancelStateError)
	require.Equal(t, tip, task.tip)
	require.False(t, task.terminal)
	require.Equal(t, used, broker.used[spec.Budget])
	// Bypass the producer guard to exercise a correctly signed, invalid history.
	require.NoError(t, persist.persistAtomic(t.Context(), choreographyLedgerMutation{
		Identity: inertRetentionForgeryIdentity, ExpectedLedgerOID: head.OID,
		TaskBranch: spec.Branch, OldTaskOID: tip, NewTaskOID: invalidOID, Event: event}))
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, inertRetentionEventLimit)
	require.NoError(t, err)
	frontier, err := NewCompiledChoreographyFrontier(CompiledChoreographyFrontierOptions{
		Binding: binding, GraphRootOID: persist.opts.GraphRootOID, Tasks: persist.opts.Tasks,
		Node: ChoreographyRuntimeNode{Name: claim.Node, First: true, Platform: "linux/amd64",
			Capacity: choreographyContactTestCapacity, AuthorityID: interpretedAttemptTestAuthority}})
	require.NoError(t, err)
	require.ErrorContains(t, frontier.ApplyEventPage(page), frontierInertCancelStateError)
	require.NotEqual(t, frontierTerminal, frontier.tasks[spec.Task].state)
	require.False(t, frontier.tasks[spec.Task].pickups[claim.PickupID].settled)
	coldPersist, err = NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	_, err = coldPersist.ReadEvents(t.Context(), ChoreographyEventCursor{}, inertRetentionEventLimit)
	require.ErrorContains(t, err, choreographyInertCancelStateError)
	_, err = RecoverFirstNodeBroker(t.Context(), choreographyContactBrokerOptions(coldPersist, spec))
	require.ErrorContains(t, err, choreographyInertCancelStateError)
}

func TestChoreographyInertPersistenceRejectsAnActiveNativeClaimWithoutPickup(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "native", ExecutorOwned: true, EligibleNodes: []string{"node-a"}})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: choreographyGitFirstAttempt, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest, ProposalID: inertRetentionNativeProposal}
	completeChoreographyFixtureClaim(t, persist, &claim)
	claim.InputsDigest, _, err = broker.ExpectedInputIdentity(spec.Task)
	require.NoError(t, err)
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)
	require.Empty(t, claim.PickupID)
	_, err = broker.StopClaims(t.Context())
	require.NoError(t, err)
	_, err = persist.PersistInertCancellation(t.Context(), ChoreographyInertCancellation{
		ChoreographyRunBinding: binding, GraphRootOID: persist.opts.GraphRootOID,
		Task: spec.Task, Attempt: claim.Attempt, Branch: spec.Branch,
		ExpectedOID: accepted.OID, Package: spec.Package, Cause: choreographyInertCancelCause})
	require.ErrorContains(t, err, choreographyInertCancelStateError)
	require.Equal(t, accepted.OID, remoteChoreographyOID(t, fixture, spec.Branch))
	require.Equal(t, spec.Weight, broker.used[spec.Budget])
	require.False(t, broker.tasks[spec.Task].terminal)
}
