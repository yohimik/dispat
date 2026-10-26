// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type acceptedCallbackPersist struct {
	ChoreographyPersistable
	binding      ChoreographyRunBinding
	root         string
	head         string
	lookupSignal chan struct{}
	claimCalls   int
}

func (p *acceptedCallbackPersist) LookupReceipt(context.Context, ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
	if p.lookupSignal != nil {
		select {
		case p.lookupSignal <- struct{}{}:
		default:
		}
	}
	return ChoreographyReceipt{}, false, nil
}
func (p *acceptedCallbackPersist) LedgerHead(context.Context) (ChoreographyEventCursor, error) {
	return ChoreographyEventCursor{OID: p.head, Sequence: 1}, nil
}
func (p *acceptedCallbackPersist) PersistClaim(_ context.Context, proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
	p.claimCalls++
	digest, err := ChoreographyProposalDigest(proposal)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	return ChoreographyReceipt{ChoreographyRunBinding: p.binding, Kind: choreographyClaimKind,
		Task: proposal.Task, Attempt: proposal.Attempt, Node: proposal.Node, Branch: proposal.Branch,
		ProposalID: proposal.ProposalID, ProposalDigest: digest, GraphRootOID: p.root,
		OID: "accepted-claim", Accepted: true}, nil
}
func (p *acceptedCallbackPersist) PersistInputInvalidation(context.Context, ChoreographyInputInvalidationRequest) error {
	return nil
}
func (p *acceptedCallbackPersist) StopClaims(context.Context) (ChoreographyEventCursor, error) {
	return ChoreographyEventCursor{OID: "signed-stop", Sequence: 2}, nil
}
func (p *acceptedCallbackPersist) PersistRefusal(_ context.Context, proposal ChoreographyClaimProposal, reason string) (ChoreographyReceipt, error) {
	digest, err := ChoreographyProposalDigest(proposal)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	return ChoreographyReceipt{ChoreographyRunBinding: p.binding, Kind: choreographyClaimKind,
		Task: proposal.Task, Attempt: proposal.Attempt, Node: proposal.Node, Branch: proposal.Branch,
		ProposalID: proposal.ProposalID, ProposalDigest: digest, GraphRootOID: p.root,
		OID: proposal.ExpectedOID, Refused: true, FenceOID: "refused-epoch", RefusalReason: reason}, nil
}

func TestChoreographyAcceptedCallbackIgnoresBusyAndStaleClaims(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation", PlanDigest: "plan", GraphDigest: "graph"}
	template := ChoreographyCompiledTemplate{Task: "build", Kind: KindBuild, Weight: 1}
	templateDigest, err := DigestChoreographyCompiledTemplate(template)
	require.NoError(t, err)
	inputsDigest, err := ChoreographyReadyInputsDigest(binding, "build", nil)
	require.NoError(t, err)
	bindings := ChoreographyTaskBindings{}
	bindingsDigest, err := DigestChoreographyTaskBindings(bindings)
	require.NoError(t, err)
	spec := ChoreographyTaskSpec{Task: "build", Kind: string(KindBuild), Branch: FormatChoreographyTaskBranch(binding.Run, "build"),
		ReservationOID: "reservation", InputsDigest: inputsDigest, Budget: ChoreographyBudgetBuild, Weight: 1,
		Placement: PlacementWorker, TemplateDigest: templateDigest}
	persist := &acceptedCallbackPersist{binding: binding, root: "complete-root", head: "ledger-head"}
	count := 0
	var consumed ChoreographyTaskBindings
	b, err := NewFirstNodeBroker(FirstNodeBrokerOptions{Binding: binding, GraphRootOID: persist.root,
		InitialMembershipEpochOID: "initial-epoch", FirstNode: "first", Tasks: []ChoreographyTaskSpec{spec},
		Templates: []ChoreographyCompiledTemplate{template},
		Members: []ChoreographyRuntimeNode{{Name: "first", First: true, Capacity: 1, Platform: "linux/amd64", AuthorityID: "0123456789abcdef0123456789abcdef"},
			{Name: "peer", Capacity: 1, Platform: "linux/amd64", AuthorityID: "11111111111111111111111111111111"}},
		NodeCapacity: map[string]int{"first": 1, "peer": 1}, Budgets: map[string]int{ChoreographyBudgetBuild: 1},
		ResolveBindings: func(context.Context, string, string) (ChoreographyTaskBindings, error) { return bindings, nil },
		OnClaimAccepted: func(_ ChoreographyTaskSpec, value ChoreographyTaskBindings) { count++; consumed = value },
		Persist:         persist})
	require.NoError(t, err)
	b.executionStarted = true
	pickupID := NewChoreographyPickupID()
	proposal := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task, Attempt: 1,
		Node: "peer", Branch: spec.Branch, ExpectedOID: spec.ReservationOID, ExpectedLedgerOID: persist.head,
		GraphRootOID: persist.root, MembershipEpochOID: "initial-epoch", CapacityAuthorityID: "11111111111111111111111111111111",
		LeaseIntentID: nodeSlotLeaseID(NodeSlotIntent{Run: binding.Run, Task: spec.Task, Attempt: 1, PickupID: pickupID}), LeaseWeight: 1,
		PickupID:     pickupID,
		InputsDigest: inputsDigest, TemplateDigest: templateDigest, BindingsDigest: bindingsDigest,
		Bindings: bindings, ProposalID: "accepted"}
	staleProposal := proposal
	staleProposal.ProposalID = "stale"
	staleProposal.ExpectedLedgerOID = "older-ledger"
	stale, err := b.AcceptClaim(t.Context(), staleProposal)
	require.NoError(t, err)
	require.True(t, stale.Refused)
	require.Equal(t, ChoreographyRefusalStale, stale.RefusalReason)
	require.Zero(t, count)
	first, err := b.AcceptClaim(t.Context(), proposal)
	require.NoError(t, err)
	require.True(t, first.Accepted)
	require.Equal(t, 1, count)
	require.Equal(t, bindings, consumed)
	proposal.ProposalID = "busy"
	busy, err := b.AcceptClaim(t.Context(), proposal)
	require.NoError(t, err)
	require.True(t, busy.Refused)
	require.Equal(t, ChoreographyRefusalBusy, busy.RefusalReason)
	require.Equal(t, 1, count)
	require.Equal(t, bindings, consumed)
}

func TestChoreographyNativeWriteFencesClaimUntilInputInvalidation(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation", PlanDigest: "plan", GraphDigest: "graph"}
	inputsDigest, err := ChoreographyReadyInputsDigest(binding, "build", nil)
	require.NoError(t, err)
	spec := ChoreographyTaskSpec{Task: "build", Kind: string(KindBuild), Branch: FormatChoreographyTaskBranch(binding.Run, "build"),
		ReservationOID: "reservation", InputsDigest: inputsDigest, Budget: ChoreographyBudgetBuild, Weight: 1, Placement: PlacementWorker}
	persist := &acceptedCallbackPersist{binding: binding, root: "complete-root", head: "ledger-head", lookupSignal: make(chan struct{}, 1)}
	b, err := NewFirstNodeBroker(FirstNodeBrokerOptions{Binding: binding, GraphRootOID: persist.root,
		InitialMembershipEpochOID: "initial-epoch", FirstNode: "first", Tasks: []ChoreographyTaskSpec{spec},
		Members: []ChoreographyRuntimeNode{{Name: "first", First: true, Capacity: 1, Platform: "linux/amd64", AuthorityID: "0123456789abcdef0123456789abcdef"},
			{Name: "peer", Capacity: 1, Platform: "linux/amd64", AuthorityID: "11111111111111111111111111111111"}},
		NodeCapacity: map[string]int{"first": 1, "peer": 1}, Budgets: map[string]int{ChoreographyBudgetBuild: 1}, Persist: persist})
	require.NoError(t, err)
	b.executionStarted = true
	release, err := b.BeginNativeInputWrite(t.Context())
	require.NoError(t, err)
	proposal := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task, Attempt: 1,
		Node: "peer", Branch: spec.Branch, ExpectedOID: spec.ReservationOID, ExpectedLedgerOID: persist.head,
		GraphRootOID: persist.root, MembershipEpochOID: "initial-epoch", CapacityAuthorityID: "11111111111111111111111111111111",
		LeaseIntentID: nodeSlotLeaseID(NodeSlotIntent{Run: binding.Run, Task: spec.Task, Attempt: 1}), LeaseWeight: 1,
		InputsDigest: inputsDigest, ProposalID: "racing-claim"}
	type claimOutcome struct {
		receipt ChoreographyReceipt
		err     error
	}
	done := make(chan claimOutcome, 1)
	go func() { receipt, err := b.AcceptClaim(t.Context(), proposal); done <- claimOutcome{receipt, err} }()
	<-persist.lookupSignal // The claim has passed durable replay and is at the native lane.
	select {
	case <-done:
		t.Fatal("claim crossed active native source write")
	default:
	}
	request := ChoreographyInputInvalidationRequest{ChoreographyRunBinding: binding,
		GraphRootOID: persist.root, OldPreparedOID: strings.Repeat("a", 40), NewPreparedOID: strings.Repeat("b", 40),
		AffectedTasks: []string{spec.Task}}
	require.NoError(t, b.InvalidatePreparedInput(t.Context(), request))
	release()
	result := <-done
	require.NoError(t, result.err)
	require.True(t, result.receipt.Refused)
	require.Equal(t, ChoreographyRefusalStopping, result.receipt.RefusalReason)
	require.Zero(t, persist.claimCalls)
}
