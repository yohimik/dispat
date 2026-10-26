// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	frameReadyTestPackLimit = 1 << 20
	frameReadyReadbackError = "the accepted ready-frame push cannot be read back"
)

type frameReadyCASTestFixture struct {
	graph      graphCarrierFixture
	persist    *GitChoreographyPersist
	broker     *FirstNodeBroker
	proposal   ChoreographyClaimProposal
	startOID   string
	carrierOID string
}

func newFrameReadyCASTestFixture(t *testing.T) frameReadyCASTestFixture {
	t.Helper()
	f := newGraphCarrierFixtureWithPrepared(t, true, true)
	policy := ChoreographyJoinPolicy{AdmissionDurationSeconds: choreographyTestAdmissionSeconds,
		Authorized: []ChoreographyAuthorizedNode{{Name: "peer", CoordinationEndpoint: f.endpoint.endpoint}}}
	persist, err := NewGitChoreographyPersist(GitChoreographyOptions{Binding: f.binding,
		Mailbox: f.endpoint.mailbox, Ledger: ChoreographyLedgerReservation{
			Branch: FormatChoreographyLedgerBranch(f.binding.Run), OID: f.ledgerRoot},
		BootstrapOID: f.bootstrap, GraphRootOID: f.root, InitializationCompleteOID: f.complete,
		Tasks: f.carrier.Tasks, Templates: f.carrier.Templates, JoinPolicy: policy,
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion, MaxPackBytes: frameReadyTestPackLimit,
		VerifyFirst:  func(context.Context) error { return nil },
		VerifyResult: func(context.Context, ChoreographyResultProposal, Result, *gitx.LocalGitx) error { return nil }})
	require.NoError(t, err)
	full := ChoreographyTaskBindings{Repositories: []AssignmentRepository{{
		Name: "repo", Path: ".", Snapshot: f.bootstrap}}}
	carrier := ChoreographyPreparedCarrier{ChoreographyRunBinding: f.binding,
		GraphRootOID: f.root, Task: f.spec.Task, TemplateDigest: f.spec.TemplateDigest,
		PredecessorResultOIDs: []string{}, Bindings: full}
	carrierOID, err := f.endpoint.mailbox.PrepareChoreographyPreparedCarrier(
		t.Context(), carrier, frameReadyTestPackLimit)
	require.NoError(t, err)
	full.PreparedInputOID = carrierOID
	broker, err := NewFirstNodeBroker(FirstNodeBrokerOptions{Binding: f.binding,
		GraphRootOID: f.root, InitialMembershipEpochOID: f.complete,
		FirstNode: "first", Tasks: f.carrier.Tasks, Templates: f.carrier.Templates,
		Members: []ChoreographyRuntimeNode{
			{Name: "first", First: true, Platform: "linux/amd64", Capacity: 1,
				AuthorityID: "0123456789abcdef0123456789abcdef"},
			{Name: "peer", Platform: "linux/amd64", Capacity: 1,
				AuthorityID: "11111111111111111111111111111111"}},
		NodeCapacity: map[string]int{"first": 1, "peer": 1},
		Budgets:      map[string]int{ChoreographyBudgetBuild: 1}, JoinPolicy: policy,
		ResolveBindings: func(context.Context, string, string) (ChoreographyTaskBindings, error) {
			return full, nil
		}, Persist: persist})
	require.NoError(t, err)
	start, err := broker.StartExecution(t.Context())
	require.NoError(t, err)
	inputsDigest, err := ChoreographyReadyInputsDigest(f.binding, f.spec.Task, nil)
	require.NoError(t, err)
	identity := ChoreographyTaskBindings{}
	identityDigest, err := DigestChoreographyTaskBindings(identity)
	require.NoError(t, err)
	pickupID := NewChoreographyPickupID()
	proposal := ChoreographyClaimProposal{ChoreographyRunBinding: f.binding,
		Task: f.spec.Task, Attempt: 1, Node: "peer", Branch: f.spec.Branch,
		ExpectedOID: f.taskRoot, ExpectedLedgerOID: start.OID, GraphRootOID: f.root,
		MembershipEpochOID: f.complete, CapacityAuthorityID: "11111111111111111111111111111111",
		PickupID: pickupID,
		LeaseIntentID: nodeSlotLeaseID(NodeSlotIntent{
			Run: f.binding.Run, Task: f.spec.Task, Attempt: 1, PickupID: pickupID}),
		LeaseWeight: 1, InputsDigest: inputsDigest, TemplateDigest: f.spec.TemplateDigest,
		Bindings: identity, BindingsDigest: identityDigest, ProposalID: "ready-frame-cas"}
	return frameReadyCASTestFixture{graph: f, persist: persist, broker: broker,
		proposal: proposal, startOID: start.OID, carrierOID: carrierOID}
}

type frameReadyReadbackTransport struct {
	transportx
	pushed bool
}

func (t *frameReadyReadbackTransport) PushAtomicAdvances(ctx context.Context, endpoint string,
	refs []gitx.BranchAdvance) error {
	if err := t.transportx.PushAtomicAdvances(ctx, endpoint, refs); err != nil {
		return err
	}
	t.pushed = true
	return nil
}

func (t *frameReadyReadbackTransport) ListRemoteHeads(ctx context.Context,
	endpoint, pattern string) ([]gitx.RemoteHead, error) {
	if t.pushed {
		return nil, errors.New(frameReadyReadbackError)
	}
	return t.transportx.ListRemoteHeads(ctx, endpoint, pattern)
}

type frameReadyRefusedTransport struct{ transportx }

func (*frameReadyRefusedTransport) PushAtomicAdvances(context.Context, string,
	[]gitx.BranchAdvance) error {
	return gitx.ErrRemoteRefused
}

type frameReadyTransientHeadTransport struct {
	transportx
	ledgerBranch string
	ledgerReads  int
}

func (t *frameReadyTransientHeadTransport) ListRemoteHeads(ctx context.Context,
	endpoint, pattern string) ([]gitx.RemoteHead, error) {
	if strings.HasSuffix(pattern, t.ledgerBranch) {
		t.ledgerReads++
		if t.ledgerReads == 2 {
			return nil, errors.New(frameReadyReadbackError)
		}
	}
	return t.transportx.ListRemoteHeads(ctx, endpoint, pattern)
}

func requireFrameReadyFailureWakesObserver(t *testing.T, fixture frameReadyCASTestFixture,
	wake <-chan struct{}, err error) {
	t.Helper()
	require.Error(t, err)
	require.ErrorIs(t, fixture.broker.fault, errChoreographyAtomicUnresolved)
	require.False(t, fixture.broker.tasks[fixture.graph.spec.Task].frameReady)
	require.Empty(t, fixture.broker.tasks[fixture.graph.spec.Task].claim)
	select {
	case <-wake:
	default:
		t.Fatal("failed ready-frame CAS left the compiled task observer asleep")
	}
}

func TestChoreographyFrameReadyUnknownCASWakesObserverAndRetainsExactPending(t *testing.T) {
	fixture := newFrameReadyCASTestFixture(t)
	transport := &choreographyAtomicUnknownTransport{
		transportx: fixture.graph.endpoint.mailbox.remote, always: true}
	fixture.graph.endpoint.mailbox.remote = transport
	wake := fixture.broker.EligibilityWake()
	_, err := fixture.broker.AcceptClaim(t.Context(), fixture.proposal)
	require.ErrorIs(t, err, gitx.ErrPushUnknown)
	requireFrameReadyFailureWakesObserver(t, fixture, wake, err)
	require.NotNil(t, fixture.persist.pending)
	require.Equal(t, fixture.startOID, remoteChoreographyOID(t, fixture.graph.endpoint,
		fixture.persist.opts.Ledger.Branch))
	require.Equal(t, fixture.graph.taskRoot, remoteChoreographyOID(t, fixture.graph.endpoint,
		fixture.graph.spec.Branch))
}

func TestChoreographyFrameReadyAppliedLostReplyReconcilesWithoutFault(t *testing.T) {
	fixture := newFrameReadyCASTestFixture(t)
	transport := &choreographyAtomicUnknownTransport{
		transportx: fixture.graph.endpoint.mailbox.remote, apply: true, always: true}
	fixture.graph.endpoint.mailbox.remote = transport
	receipt, err := fixture.broker.AcceptClaim(t.Context(), fixture.proposal)
	require.NoError(t, err)
	require.True(t, receipt.Refused)
	require.True(t, fixture.broker.tasks[fixture.graph.spec.Task].frameReady)
	require.Equal(t, fixture.carrierOID, fixture.broker.tasks[fixture.graph.spec.Task].preparedInputOID)
	require.Nil(t, fixture.broker.fault)
	require.Nil(t, fixture.persist.pending)
	require.Equal(t, fixture.graph.taskRoot, remoteChoreographyOID(t, fixture.graph.endpoint,
		fixture.graph.spec.Branch))
	page, err := fixture.persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 5)
	require.NoError(t, err)
	require.Len(t, page.Events, 3)
	require.Equal(t, ChoreographyFrameReady, page.Events[2].Kind)
	require.Equal(t, fixture.carrierOID, page.Events[2].PreparedInputOID)
}

func TestChoreographyFrameReadyUnreadableAppliedCASRetainsPending(t *testing.T) {
	fixture := newFrameReadyCASTestFixture(t)
	transport := &frameReadyReadbackTransport{transportx: fixture.graph.endpoint.mailbox.remote}
	fixture.graph.endpoint.mailbox.remote = transport
	wake := fixture.broker.EligibilityWake()
	_, err := fixture.broker.AcceptClaim(t.Context(), fixture.proposal)
	require.ErrorContains(t, err, frameReadyReadbackError)
	requireFrameReadyFailureWakesObserver(t, fixture, wake, err)
	require.True(t, transport.pushed)
	require.NotNil(t, fixture.persist.pending)
	fixture.graph.endpoint.mailbox.remote = transport.transportx
	require.Equal(t, fixture.graph.taskRoot, remoteChoreographyOID(t, fixture.graph.endpoint,
		fixture.graph.spec.Branch))
	require.NotEqual(t, fixture.startOID, remoteChoreographyOID(t, fixture.graph.endpoint,
		fixture.persist.opts.Ledger.Branch))
}

func TestChoreographyFrameReadyDefiniteRefusalWakesObserver(t *testing.T) {
	fixture := newFrameReadyCASTestFixture(t)
	fixture.graph.endpoint.mailbox.remote = &frameReadyRefusedTransport{
		transportx: fixture.graph.endpoint.mailbox.remote}
	wake := fixture.broker.EligibilityWake()
	_, err := fixture.broker.AcceptClaim(t.Context(), fixture.proposal)
	require.ErrorIs(t, err, errChoreographyAtomicRefused)
	require.ErrorIs(t, fixture.broker.fault, errChoreographyAtomicRefused)
	select {
	case <-wake:
	default:
		t.Fatal("refused ready-frame CAS left the compiled task observer asleep")
	}
	require.Nil(t, fixture.persist.pending)
	require.False(t, fixture.broker.tasks[fixture.graph.spec.Task].frameReady)
	require.Equal(t, fixture.startOID, remoteChoreographyOID(t, fixture.graph.endpoint,
		fixture.persist.opts.Ledger.Branch))
	require.Equal(t, fixture.graph.taskRoot, remoteChoreographyOID(t, fixture.graph.endpoint,
		fixture.graph.spec.Branch))
}

func TestChoreographyFrameReadyForeignTipCannotBecomeSignedHistory(t *testing.T) {
	fixture := newFrameReadyCASTestFixture(t)
	foreign, err := fixture.graph.endpoint.mailbox.commitMessage(
		t.Context(), MessageCancel, []byte("{}"), []string{fixture.graph.taskRoot}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.graph.endpoint.git.PushAdvance(t.Context(),
		fixture.graph.endpoint.endpoint, foreign, fixture.graph.spec.Branch, fixture.graph.taskRoot))
	wake := fixture.broker.EligibilityWake()
	_, err = fixture.broker.AcceptClaim(t.Context(), fixture.proposal)
	require.ErrorIs(t, err, errChoreographyAtomicUnresolved)
	requireFrameReadyFailureWakesObserver(t, fixture, wake, err)
	require.NotNil(t, fixture.persist.pending)
	require.Equal(t, foreign, remoteChoreographyOID(t, fixture.graph.endpoint,
		fixture.graph.spec.Branch))
	require.Equal(t, fixture.startOID, remoteChoreographyOID(t, fixture.graph.endpoint,
		fixture.persist.opts.Ledger.Branch))
}

func TestChoreographyFrameReadyPreCASReadFailureCanRetry(t *testing.T) {
	fixture := newFrameReadyCASTestFixture(t)
	transport := &frameReadyTransientHeadTransport{
		transportx:   fixture.graph.endpoint.mailbox.remote,
		ledgerBranch: fixture.persist.opts.Ledger.Branch}
	fixture.graph.endpoint.mailbox.remote = transport
	_, err := fixture.broker.AcceptClaim(t.Context(), fixture.proposal)
	require.ErrorContains(t, err, frameReadyReadbackError)
	require.Nil(t, fixture.broker.fault)
	require.Nil(t, fixture.persist.pending)
	require.False(t, fixture.broker.tasks[fixture.graph.spec.Task].frameReady)
	require.Equal(t, fixture.startOID, remoteChoreographyOID(t, fixture.graph.endpoint,
		fixture.persist.opts.Ledger.Branch))
	receipt, err := fixture.broker.AcceptClaim(t.Context(), fixture.proposal)
	require.NoError(t, err)
	require.True(t, receipt.Refused)
	require.True(t, fixture.broker.tasks[fixture.graph.spec.Task].frameReady)
	require.Nil(t, fixture.broker.fault)
}

func TestChoreographyMarkFrameReadyUnknownCASWakesObserver(t *testing.T) {
	fixture := newFrameReadyCASTestFixture(t)
	transport := &choreographyAtomicUnknownTransport{
		transportx: fixture.graph.endpoint.mailbox.remote, always: true}
	fixture.graph.endpoint.mailbox.remote = transport
	wake := fixture.broker.EligibilityWake()
	err := fixture.broker.MarkFrameReady(t.Context(), fixture.graph.spec.Task,
		fixture.carrierOID, fixture.proposal.InputsDigest)
	require.ErrorIs(t, err, gitx.ErrPushUnknown)
	requireFrameReadyFailureWakesObserver(t, fixture, wake, err)
	require.NotNil(t, fixture.persist.pending)
}

func TestChoreographyClaimUnreadableAppliedCASWakesObserver(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
		PickupID: NewChoreographyPickupID(), ProposalID: "unreadable-applied-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	transport := &frameReadyReadbackTransport{transportx: fixture.mailbox.remote}
	fixture.mailbox.remote = transport
	wake := broker.EligibilityWake()
	_, err = broker.AcceptClaim(t.Context(), claim)
	require.ErrorIs(t, err, errChoreographyAtomicUnresolved)
	require.ErrorIs(t, broker.fault, errChoreographyAtomicUnresolved)
	require.True(t, transport.pushed)
	require.NotNil(t, persist.pending)
	require.Empty(t, broker.tasks[spec.Task].claim)
	select {
	case <-wake:
	default:
		t.Fatal("unreadable accepted Claim left the compiled task observer asleep")
	}
}
