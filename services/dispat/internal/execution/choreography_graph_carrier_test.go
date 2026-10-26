// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

type graphCarrierFixture struct {
	endpoint                                        *mailboxFixture
	binding                                         ChoreographyRunBinding
	bootstrap, root, ledgerRoot, complete, taskRoot string
	spec                                            ChoreographyTaskSpec
	carrier                                         ChoreographyGraphCarrier
}

type graphAdvanceAfterHeadsTransport struct {
	transportx
	advance func() error
}

func (t *graphAdvanceAfterHeadsTransport) ListRemoteHeads(ctx context.Context,
	endpoint, pattern string) ([]gitx.RemoteHead, error) {
	heads, err := t.transportx.ListRemoteHeads(ctx, endpoint, pattern)
	if err == nil && t.advance != nil && strings.Contains(pattern, reservationBranchPrefix) {
		advance := t.advance
		t.advance = nil
		if err := advance(); err != nil {
			return nil, err
		}
	}
	return heads, err
}

func newCompleteGraphCarrierFixture(t *testing.T) graphCarrierFixture {
	return newGraphCarrierFixture(t, true)
}

func newGraphCarrierFixture(t *testing.T, finish bool) graphCarrierFixture {
	return newGraphCarrierFixtureWithPrepared(t, finish, false)
}

func newGraphCarrierFixtureWithPrepared(t *testing.T, finish, needsPrepared bool) graphCarrierFixture {
	t.Helper()
	f := newMailboxFixture(t)
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation", PlanDigest: "plan"}
	template := ChoreographyCompiledTemplate{Task: "core:build", Kind: KindBuild, Weight: 1,
		Frame: &AssignmentFrame{Commands: []string{"build"}}}
	templateDigest, err := DigestChoreographyCompiledTemplate(template)
	require.NoError(t, err)
	spec := ChoreographyTaskSpec{Task: template.Task, Kind: template.Kind, Weight: 1,
		Budget: ChoreographyBudgetBuild, Placement: PlacementWorker,
		Branch: FormatChoreographyTaskBranch(binding.Run, template.Task), TemplateDigest: templateDigest,
		NeedsPreparedInput: needsPrepared}
	binding.GraphDigest, err = DigestChoreographyLogicalGraph(ChoreographyLogicalGraphInventory{
		Binding: binding, AlgorithmVersion: ChoreographyGraphAlgorithmVersion,
		SemanticGraphDigest: "semantic-typed-graph", Tasks: []ChoreographyTaskSpec{spec},
		Templates: []ChoreographyCompiledTemplate{template}})
	require.NoError(t, err)
	bootstrap, err := f.mailbox.commitMessage(t.Context(), choreographyBootstrapKind,
		[]byte(`{"run":"run"}`), nil, nil)
	require.NoError(t, err)
	reservation := &Reservation{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: spec.Kind, Run: binding.Run, Generation: binding.Generation, PlanDigest: binding.PlanDigest,
		Task: spec.Task, Attempt: 1, Branch: spec.Branch, IssuedAt: time.Now().UTC().Format(time.RFC3339)},
		BootstrapOID: bootstrap, MaxAttempts: maxPlacementAttempts}
	spec.ReservationOID, err = f.mailbox.PrepareChoreographyReservation(t.Context(), reservation)
	require.NoError(t, err)
	ledger, err := PrepareChoreographyLedgerRoot(t.Context(), ChoreographyLedgerRootOptions{
		Mailbox: f.mailbox, Binding: binding, BootstrapOID: bootstrap,
		Roster: ChoreographyRuntimeRoster{Nodes: []ChoreographyRuntimeNode{{Name: "first", Platform: "linux/amd64",
			Capacity: 1, First: true, AuthorityID: "0123456789abcdef0123456789abcdef"},
			{Name: "peer", Platform: "linux/amd64", Capacity: 1, AuthorityID: "11111111111111111111111111111111"}},
			Budgets: []ChoreographyRuntimeBudget{{Name: ChoreographyBudgetBuild, Capacity: 1}}}})
	require.NoError(t, err)
	require.NoError(t, f.git.PushCreateAtomic(t.Context(), f.endpoint, []gitx.CreateRef{
		{Branch: spec.Branch, OID: spec.ReservationOID}, {Branch: ledger.Branch, OID: ledger.OID}}))
	carrier := ChoreographyGraphCarrier{ChoreographyRunBinding: binding, BootstrapOID: bootstrap,
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion, SemanticGraphDigest: "semantic-typed-graph",
		Tasks: []ChoreographyTaskSpec{spec}, Templates: []ChoreographyCompiledTemplate{template}}
	logical, manifest, err := digestChoreographyGraphCarrier(carrier)
	require.NoError(t, err)
	carrier.LogicalDescriptorDigest, carrier.ManifestDigest = logical, manifest
	require.NoError(t, validateChoreographyGraphCarrier(carrier))
	document, err := json.Marshal(carrier)
	require.NoError(t, err)
	root, err := f.mailbox.commitMessage(t.Context(), choreographyGraphRootKind, document, []string{bootstrap}, nil)
	require.NoError(t, err)
	require.NoError(t, f.git.PushCreateAtomic(t.Context(), f.endpoint, []gitx.CreateRef{
		{Branch: FormatChoreographyGraphRootBranch(binding.Run), OID: root}}))
	if !finish {
		return graphCarrierFixture{endpoint: f, binding: binding, bootstrap: bootstrap, root: root,
			ledgerRoot: ledger.OID, taskRoot: spec.ReservationOID, spec: spec, carrier: carrier}
	}
	event := ChoreographyEvent{Sequence: 1, Kind: ChoreographyInitializationComplete,
		Task: choreographyRunTaskID, TaskOID: root, GraphRootOID: root}
	record := choreographyLedgerRecord{ChoreographyRunBinding: binding, Sequence: 1, Event: &event}
	document, err = json.Marshal(record)
	require.NoError(t, err)
	complete, err := f.mailbox.commitMessage(t.Context(), choreographyLedgerKind,
		document, []string{ledger.OID, root}, nil)
	require.NoError(t, err)
	require.NoError(t, f.git.PushAtomicAdvances(t.Context(), f.endpoint, []gitx.BranchAdvance{
		{Branch: ledger.Branch, ExpectedOld: ledger.OID, OID: complete}}))
	return graphCarrierFixture{endpoint: f, binding: binding, bootstrap: bootstrap, root: root,
		ledgerRoot: ledger.OID, complete: complete, taskRoot: spec.ReservationOID, spec: spec, carrier: carrier}
}

func TestCompleteGraphCarrierRejectsAlteredAndMissingTemplates(t *testing.T) {
	f := newCompleteGraphCarrierFixture(t)
	peer := f.endpoint.second(t)
	_, root, err := peer.mailbox.ReadChoreographyGraphCarrier(t.Context(), f.binding, f.bootstrap, 1<<20)
	require.NoError(t, err)
	require.Equal(t, f.root, root)
	altered := f.carrier
	altered.Templates = []ChoreographyCompiledTemplate{{Task: f.spec.Task, Kind: KindBuild, Weight: 1,
		Frame: &AssignmentFrame{Commands: []string{"different"}}}}
	require.Error(t, validateChoreographyGraphCarrier(altered))
	omitted := f.carrier
	omitted.Templates = nil
	require.Error(t, validateChoreographyGraphCarrier(omitted))
	additional := f.carrier
	additional.Templates = append(additional.Templates, ChoreographyCompiledTemplate{Task: "extra", Kind: KindBuild, Weight: 1})
	require.Error(t, validateChoreographyGraphCarrier(additional))
	wrong := f.binding
	wrong.GraphDigest = "different"
	_, _, err = peer.mailbox.ReadChoreographyGraphCarrier(t.Context(), wrong, f.bootstrap, 1<<20)
	require.Error(t, err)
}

func TestCompleteGraphCarrierAcceptsAdvancedSignedTaskAndRejectsUnledgeredTip(t *testing.T) {
	f := newCompleteGraphCarrierFixture(t)
	blocked := ChoreographyBlockedOutcome{ChoreographyRunBinding: f.binding, Task: f.spec.Task,
		Attempt: 1, ExpectedOID: f.taskRoot, Package: "core", BlockedBy: "core"}
	document, err := json.Marshal(blocked)
	require.NoError(t, err)
	blockedOID, err := f.endpoint.mailbox.commitMessage(t.Context(), choreographyBlockedKind,
		document, []string{f.taskRoot}, nil)
	require.NoError(t, err)
	event := ChoreographyEvent{Sequence: 2, Kind: ChoreographyBlocked, Task: f.spec.Task,
		TaskOID: blockedOID, PriorTaskOID: f.taskRoot, BlockedBy: "core"}
	record := choreographyLedgerRecord{ChoreographyRunBinding: f.binding, Sequence: 2, Event: &event}
	document, err = json.Marshal(record)
	require.NoError(t, err)
	ledgerOID, err := f.endpoint.mailbox.commitMessage(t.Context(), choreographyLedgerKind,
		document, []string{f.complete, blockedOID}, nil)
	require.NoError(t, err)
	require.NoError(t, f.endpoint.git.PushAtomicAdvances(t.Context(), f.endpoint.endpoint, []gitx.BranchAdvance{
		{Branch: f.spec.Branch, ExpectedOld: f.taskRoot, OID: blockedOID},
		{Branch: FormatChoreographyLedgerBranch(f.binding.Run), ExpectedOld: f.complete, OID: ledgerOID}}))
	peer := f.endpoint.second(t)
	_, root, err := peer.mailbox.ReadChoreographyGraphCarrier(t.Context(), f.binding, f.bootstrap, 1<<20)
	require.NoError(t, err)
	require.Equal(t, f.root, root)
	unledgered, err := f.endpoint.mailbox.commitMessage(t.Context(), choreographyDecisionKind,
		[]byte(`{"unexpected":true}`), []string{blockedOID}, nil)
	require.NoError(t, err)
	require.NoError(t, f.endpoint.git.PushAtomicAdvances(t.Context(), f.endpoint.endpoint, []gitx.BranchAdvance{
		{Branch: f.spec.Branch, ExpectedOld: blockedOID, OID: unledgered}}))
	_, _, err = peer.mailbox.ReadChoreographyGraphCarrier(t.Context(), f.binding, f.bootstrap, 1<<20)
	require.ErrorContains(t, err, "ledger ancestry")
}

func TestCompleteGraphCarrierRetriesCoherentTaskAndLedgerReadDuringClaimCAS(t *testing.T) {
	f := newCompleteGraphCarrierFixture(t)
	plumbing := gitx.NewPlumbing(f.endpoint.git)
	sourceTree := plumbing.MakeTree(t.Context(), nil)
	sourceOID := plumbing.CommitTree(t.Context(), sourceTree, nil, "signed-claim-source")
	require.NoError(t, plumbing.Err())
	bindings := ChoreographyTaskBindings{Repositories: []AssignmentRepository{{Snapshot: sourceOID}}}
	assignment, err := BindChoreographyCompiledTemplate(ChoreographyTemplateBindingOptions{
		Template: f.carrier.Templates[0],
		Bindings: bindings,
		Header: Header{Protocol: ChoreographyProtocolVersion, Kind: KindBuild, Run: f.binding.Run,
			Generation: f.binding.Generation, PlanDigest: f.binding.PlanDigest,
			Task: f.spec.Task, Attempt: 1, Node: "peer", Branch: f.spec.Branch,
			IssuedAt: time.Now().UTC().Format(time.RFC3339)},
		BootstrapOID: f.bootstrap,
		GraphDigest:  f.binding.GraphDigest,
	})
	require.NoError(t, err)
	assignment.GraphRootOID, assignment.InputsDigest = f.root, "signed-inputs"
	assignmentOID, err := f.endpoint.mailbox.PrepareAssignmentOnReservation(t.Context(),
		&assignment, f.taskRoot)
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: f.binding,
		Task: f.spec.Task, Attempt: 1, Node: "peer", Branch: f.spec.Branch,
		ExpectedOID: f.taskRoot, ExpectedLedgerOID: f.complete, GraphRootOID: f.root,
		MembershipEpochOID: f.complete, CapacityAuthorityID: "11111111111111111111111111111111",
		LeaseIntentID: "held-peer-lease", LeaseWeight: 1,
		InputsDigest: assignment.InputsDigest, TemplateDigest: assignment.TemplateDigest,
		ProposalID: "claim-during-cold-read"}
	document, err := json.Marshal(claim)
	require.NoError(t, err)
	claimOID, err := f.endpoint.mailbox.commitMessage(t.Context(), choreographyClaimKind,
		document, []string{assignmentOID}, nil)
	require.NoError(t, err)
	event := ChoreographyEvent{Sequence: 2, Kind: ChoreographyClaimAccepted, Task: f.spec.Task,
		Attempt: 1, Node: "peer", TaskOID: claimOID, PriorTaskOID: f.taskRoot,
		AssignmentOID: assignmentOID, GraphRootOID: f.root, InputsDigest: claim.InputsDigest}
	record := choreographyLedgerRecord{ChoreographyRunBinding: f.binding, Sequence: 2, Event: &event}
	document, err = json.Marshal(record)
	require.NoError(t, err)
	ledgerOID, err := f.endpoint.mailbox.commitMessage(t.Context(), choreographyLedgerKind,
		document, []string{f.complete, claimOID}, nil)
	require.NoError(t, err)
	peer := f.endpoint.second(t)
	peer.mailbox.remote = &graphAdvanceAfterHeadsTransport{transportx: peer.mailbox.remote,
		advance: func() error {
			return f.endpoint.git.PushAtomicAdvances(t.Context(), f.endpoint.endpoint, []gitx.BranchAdvance{
				{Branch: f.spec.Branch, ExpectedOld: f.taskRoot, OID: claimOID},
				{Branch: FormatChoreographyLedgerBranch(f.binding.Run), ExpectedOld: f.complete, OID: ledgerOID}})
		}}
	_, root, err := peer.mailbox.ReadChoreographyGraphCarrier(t.Context(), f.binding, f.bootstrap, 1<<20)
	require.NoError(t, err)
	require.Equal(t, f.root, root)
}

func TestCompleteGraphCarrierRejectsPartialInitializationAndWrongRefInventory(t *testing.T) {
	partial := newGraphCarrierFixture(t, false)
	peer := partial.endpoint.second(t)
	_, _, err := peer.mailbox.ReadChoreographyGraphCarrier(t.Context(), partial.binding, partial.bootstrap, 1<<20)
	require.ErrorContains(t, err, "initialization-complete")

	f := newCompleteGraphCarrierFixture(t)
	peer = f.endpoint.second(t)
	extraBranch := FormatChoreographyTaskBranch(f.binding.Run, "extra")
	extraOID, err := f.endpoint.mailbox.commitMessage(t.Context(), choreographyBlockedKind,
		[]byte(`{"extra":true}`), []string{f.bootstrap}, nil)
	require.NoError(t, err)
	require.NoError(t, f.endpoint.git.PushCreateAtomic(t.Context(), f.endpoint.endpoint,
		[]gitx.CreateRef{{Branch: extraBranch, OID: extraOID}}))
	_, _, err = peer.mailbox.ReadChoreographyGraphCarrier(t.Context(), f.binding, f.bootstrap, 1<<20)
	require.ErrorContains(t, err, "task ref count")
	runGitIn(t, f.endpoint.endpoint, "update-ref", "-d", "refs/heads/"+extraBranch)
	runGitIn(t, f.endpoint.endpoint, "update-ref", "-d", "refs/heads/"+f.spec.Branch)
	_, _, err = peer.mailbox.ReadChoreographyGraphCarrier(t.Context(), f.binding, f.bootstrap, 1<<20)
	require.ErrorContains(t, err, "task ref count")
}
