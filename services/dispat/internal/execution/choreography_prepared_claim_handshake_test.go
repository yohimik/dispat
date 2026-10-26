// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestChoreographyFirstPreparedClaimSignsFrameBeforeExactRetry(t *testing.T) {
	f := newGraphCarrierFixtureWithPrepared(t, true, true)
	policy := ChoreographyJoinPolicy{AdmissionDurationSeconds: choreographyTestAdmissionSeconds,
		Authorized: []ChoreographyAuthorizedNode{{Name: "peer", CoordinationEndpoint: f.endpoint.endpoint}}}
	persist, err := NewGitChoreographyPersist(GitChoreographyOptions{Binding: f.binding,
		Mailbox: f.endpoint.mailbox, Ledger: ChoreographyLedgerReservation{Branch: FormatChoreographyLedgerBranch(f.binding.Run), OID: f.ledgerRoot},
		BootstrapOID: f.bootstrap, GraphRootOID: f.root, InitializationCompleteOID: f.complete,
		Tasks: f.carrier.Tasks, Templates: f.carrier.Templates, JoinPolicy: policy,
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion, MaxPackBytes: 1 << 20,
		VerifyFirst:  func(context.Context) error { return nil },
		VerifyResult: func(context.Context, ChoreographyResultProposal, Result, *gitx.LocalGitx) error { return nil }})
	require.NoError(t, err)
	full := ChoreographyTaskBindings{Repositories: []AssignmentRepository{{Name: "repo", Path: ".", Snapshot: f.bootstrap}}}
	carrier := ChoreographyPreparedCarrier{ChoreographyRunBinding: f.binding, GraphRootOID: f.root,
		Task: f.spec.Task, TemplateDigest: f.spec.TemplateDigest,
		PredecessorResultOIDs: []string{}, Bindings: full}
	carrierOID, err := f.endpoint.mailbox.PrepareChoreographyPreparedCarrier(t.Context(), carrier, 1<<20)
	require.NoError(t, err)
	full.PreparedInputOID = carrierOID
	opts := FirstNodeBrokerOptions{Binding: f.binding, GraphRootOID: f.root, InitialMembershipEpochOID: f.complete,
		FirstNode: "first", Tasks: f.carrier.Tasks, Templates: f.carrier.Templates,
		Members: []ChoreographyRuntimeNode{{Name: "first", First: true, Platform: "linux/amd64", Capacity: 1, AuthorityID: "0123456789abcdef0123456789abcdef"},
			{Name: "peer", Platform: "linux/amd64", Capacity: 1, AuthorityID: "11111111111111111111111111111111"}},
		NodeCapacity: map[string]int{"first": 1, "peer": 1}, Budgets: map[string]int{ChoreographyBudgetBuild: 1},
		JoinPolicy: policy, ResolveBindings: func(context.Context, string, string) (ChoreographyTaskBindings, error) { return full, nil },
		Persist: persist}
	broker, err := NewFirstNodeBroker(opts)
	require.NoError(t, err)
	start, err := broker.StartExecution(t.Context())
	require.NoError(t, err)
	inputsDigest, err := ChoreographyReadyInputsDigest(f.binding, f.spec.Task, nil)
	require.NoError(t, err)
	for _, mutation := range []struct {
		name   string
		change func(*ChoreographyPreparedCarrier)
	}{
		{name: "another-graph-root", change: func(c *ChoreographyPreparedCarrier) { c.GraphRootOID = f.bootstrap }},
		{name: "another-template", change: func(c *ChoreographyPreparedCarrier) { c.TemplateDigest = "forged-template" }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			forged := carrier
			mutation.change(&forged)
			forgedOID, err := f.endpoint.mailbox.PrepareChoreographyPreparedCarrier(t.Context(), forged, 1<<20)
			require.NoError(t, err)
			require.Error(t, persist.PersistFrameReady(t.Context(), ChoreographyFrameReadyRequest{
				TaskID: f.spec.Task, Attempt: 1, TaskOID: f.taskRoot,
				PreparedInputOID: forgedOID, InputsDigest: inputsDigest}))
			require.Equal(t, start.OID, remoteChoreographyOID(t, f.endpoint,
				FormatChoreographyLedgerBranch(f.binding.Run)))
		})
	}
	identity := ChoreographyTaskBindings{}
	identityDigest, err := DigestChoreographyTaskBindings(identity)
	require.NoError(t, err)
	proposal := ChoreographyClaimProposal{ChoreographyRunBinding: f.binding, Task: f.spec.Task, Attempt: 1,
		Node: "peer", Branch: f.spec.Branch, ExpectedOID: f.taskRoot, ExpectedLedgerOID: start.OID,
		GraphRootOID: f.root, MembershipEpochOID: f.complete, CapacityAuthorityID: "11111111111111111111111111111111",
		LeaseIntentID: nodeSlotLeaseID(NodeSlotIntent{Run: f.binding.Run, Task: f.spec.Task, Attempt: 1}), LeaseWeight: 1,
		InputsDigest: inputsDigest, TemplateDigest: f.spec.TemplateDigest, Bindings: identity,
		BindingsDigest: identityDigest, ProposalID: "first-prepared-attempt"}
	wrongTemplate := proposal
	wrongTemplate.TemplateDigest = "another-template"
	wrongTemplate.ProposalID = "invalid-template"
	_, err = broker.AcceptClaim(t.Context(), wrongTemplate)
	require.Error(t, err)
	require.Equal(t, start.OID, remoteChoreographyOID(t, f.endpoint, FormatChoreographyLedgerBranch(f.binding.Run)))
	stale := proposal
	stale.ExpectedLedgerOID = f.complete
	stale.ProposalID = "stale-prepared"
	staleReceipt, err := broker.AcceptClaim(t.Context(), stale)
	require.NoError(t, err)
	require.True(t, staleReceipt.Refused)
	require.Equal(t, start.OID, remoteChoreographyOID(t, f.endpoint, FormatChoreographyLedgerBranch(f.binding.Run)))
	first, err := broker.AcceptClaim(t.Context(), proposal)
	require.NoError(t, err)
	require.True(t, first.Refused)
	require.Equal(t, ChoreographyRefusalStale, first.RefusalReason)
	require.Equal(t, f.taskRoot, remoteChoreographyOID(t, f.endpoint, f.spec.Branch))
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 4)
	require.NoError(t, err)
	require.Len(t, page.Events, 3)
	require.Equal(t, ChoreographyFrameReady, page.Events[2].Kind)
	require.Equal(t, proposal.Attempt, page.Events[2].Attempt)
	require.Equal(t, carrierOID, page.Events[2].PreparedInputOID)
	frontier, err := NewCompiledChoreographyFrontier(f.binding, f.root,
		ChoreographyRuntimeNode{Name: "peer", Platform: "linux/amd64", Capacity: 1,
			AuthorityID: "11111111111111111111111111111111"}, f.carrier.Tasks)
	require.NoError(t, err)
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Events: page.Events[:2], Cursor: ChoreographyEventCursor{
			OID: page.Events[1].LedgerOID, Sequence: page.Events[1].Sequence}}))
	_, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Events: page.Events[2:], Cursor: page.Cursor}))
	require.NoError(t, frontier.RequeueFenced(proposal.Task))
	selected, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, proposal.Task, selected.Spec.Task)
	require.Equal(t, carrierOID, frontier.PreparedInputOID(selected.Spec.Task))
	proposal.ExpectedLedgerOID = first.FenceOID
	proposal.Bindings = ChoreographyTaskBindings{PreparedInputOID: carrierOID}
	proposal.BindingsDigest, err = DigestChoreographyTaskBindings(proposal.Bindings)
	require.NoError(t, err)
	proposal.ProposalID = "exact-prepared-retry"
	accepted, err := broker.AcceptClaim(t.Context(), proposal)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)
	require.Equal(t, f.root, accepted.GraphRootOID)
	require.NotEmpty(t, accepted.AssignmentOID)
	view, err := f.endpoint.mailbox.OpenCarriedAssignment(t.Context(), accepted.AssignmentOID, 1<<20)
	require.NoError(t, err)
	fullDigest, err := DigestChoreographyTaskBindings(full)
	require.NoError(t, err)
	require.Equal(t, fullDigest, view.Assignment.BindingsDigest)
	require.Equal(t, f.bootstrap, view.Assignment.Repositories[0].Snapshot)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = restarted
	recovered, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, accepted.OID, recovered.tasks[f.spec.Task].claim)
}

func TestChoreographyFatalBindingStopsRunAndSettlesExactRefusal(t *testing.T) {
	f, persist, opts := newExecutionStartFixture(t)
	bindErr := errors.New("pinned source unavailable")
	opts.ResolveBindings = func(context.Context, string, string) (ChoreographyTaskBindings, error) {
		return ChoreographyTaskBindings{}, bindErr
	}
	var reported error
	opts.OnFatalBinding = func(err error) { reported = err }
	broker, err := NewFirstNodeBroker(opts)
	require.NoError(t, err)
	start, err := broker.StartExecution(t.Context())
	require.NoError(t, err)
	inputsDigest, err := ChoreographyReadyInputsDigest(f.binding, f.spec.Task, nil)
	require.NoError(t, err)
	identity := ChoreographyTaskBindings{}
	identityDigest, err := DigestChoreographyTaskBindings(identity)
	require.NoError(t, err)
	proposal := ChoreographyClaimProposal{ChoreographyRunBinding: f.binding,
		Task: f.spec.Task, Attempt: 1, Node: "peer", Branch: f.spec.Branch,
		ExpectedOID: f.taskRoot, ExpectedLedgerOID: start.OID, GraphRootOID: f.root,
		MembershipEpochOID: f.complete, CapacityAuthorityID: "11111111111111111111111111111111",
		LeaseIntentID: nodeSlotLeaseID(NodeSlotIntent{Run: f.binding.Run, Task: f.spec.Task, Attempt: 1}),
		LeaseWeight:   1, InputsDigest: inputsDigest, TemplateDigest: f.spec.TemplateDigest,
		Bindings: identity, BindingsDigest: identityDigest, ProposalID: "fatal-binding"}
	refused, err := broker.AcceptClaim(t.Context(), proposal)
	require.ErrorIs(t, err, ErrChoreographyBindingFatal)
	require.ErrorContains(t, err, bindErr.Error())
	require.ErrorIs(t, reported, ErrChoreographyBindingFatal)
	require.True(t, refused.Refused)
	require.Equal(t, ChoreographyRefusalStopping, refused.RefusalReason)
	require.Equal(t, f.root, refused.GraphRootOID)
	require.Equal(t, f.taskRoot, remoteChoreographyOID(t, f.endpoint, f.spec.Branch))
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 4)
	require.NoError(t, err)
	require.Len(t, page.Events, 4)
	require.Equal(t, ChoreographyRunStopping, page.Events[2].Kind)
	require.Equal(t, ChoreographyClaimRefused, page.Events[3].Kind)
	require.Equal(t, ChoreographyRefusalStopping, page.Events[3].RefusalReason)
	require.Equal(t, page.Cursor.OID, refused.FenceOID)
	query, err := FormatClaimReceiptQuery(proposal)
	require.NoError(t, err)
	lookedUp, found, err := persist.LookupReceipt(t.Context(), query)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, refused, lookedUp)
}
