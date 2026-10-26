// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestChoreographySignedLocalFailureFencesOnlyInertReservation(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	spec.Package = "pkg"
	persist.tasks[spec.Task] = spec
	persist.opts.Tasks = []ChoreographyTaskSpec{spec}
	digest := sha256.Sum256([]byte("frame input could not be read"))
	blocked := ChoreographyBlockedOutcome{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, ExpectedOID: spec.ReservationOID,
		Package: spec.Package, BlockedBy: spec.Package, PackageOutcome: "failed",
		LocalFailure: &ChoreographyLocalFailureEvidence{Part: choreographyFailurePartInputs,
			ErrorSHA256: hex.EncodeToString(digest[:]), ReservationOID: spec.ReservationOID}}
	withoutEvidence := blocked
	withoutEvidence.LocalFailure = nil
	_, err := persist.PersistBlocked(t.Context(), withoutEvidence)
	require.ErrorContains(t, err, "planned package outcome")
	wrongRoot := blocked
	wrongRoot.LocalFailure = &ChoreographyLocalFailureEvidence{Part: choreographyFailurePartInputs,
		ErrorSHA256: blocked.LocalFailure.ErrorSHA256, ReservationOID: persist.opts.Ledger.OID}
	_, err = persist.PersistBlocked(t.Context(), wrongRoot)
	require.ErrorContains(t, err, "planned package outcome")
	blockedOID, err := persist.PersistBlocked(t.Context(), blocked)
	require.NoError(t, err)
	require.Equal(t, blockedOID, remoteChoreographyOID(t, fixture, spec.Branch))
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	page, err := restarted.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, restarted), 2)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, ChoreographyBlocked, page.Events[0].Kind)
	require.Equal(t, spec.ReservationOID, page.Events[0].PriorTaskOID)
}

func TestChoreographyLocalPreclaimFailureLeavesUnrelatedTaskClaimable(t *testing.T) {
	fixture, persist, binding, failedSpec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "build", Package: "failed-package"},
		ChoreographyTaskSpec{Task: "unrelated-build", Package: "unrelated-package"})
	other := persist.opts.Tasks[1]
	policy := &testedOutcomePolicy{status: map[string]string{}}
	broker, err := RecoverFirstNodeBroker(t.Context(), FirstNodeBrokerOptions{Binding: binding,
		GraphRootOID:              persist.opts.GraphRootOID,
		InitialMembershipEpochOID: persist.opts.InitializationCompleteOID,
		JoinPolicy:                persist.opts.JoinPolicy, FirstNode: "node-a",
		Tasks: persist.opts.Tasks, Templates: persist.opts.Templates,
		Members: []ChoreographyRuntimeNode{{Name: "node-a", First: true,
			Platform: "linux/amd64", Capacity: 1,
			AuthorityID: "0123456789abcdef0123456789abcdef"}},
		NodeCapacity: map[string]int{"node-a": 1}, Budgets: map[string]int{"build": 1},
		OutcomePolicy: policy, Persist: persist,
		ResolveBindings: func(context.Context, string, string) (ChoreographyTaskBindings, error) {
			return ChoreographyTaskBindings{}, nil
		}})
	require.NoError(t, err)
	digest := sha256.Sum256([]byte("real preclaim input error"))
	require.NoError(t, broker.AcceptBlocked(t.Context(), ChoreographyBlockedOutcome{
		ChoreographyRunBinding: binding, Task: failedSpec.Task,
		Package: failedSpec.Package, BlockedBy: failedSpec.Package, PackageOutcome: "failed",
		LocalFailure: &ChoreographyLocalFailureEvidence{Part: choreographyFailurePartInputs,
			ErrorSHA256: hex.EncodeToString(digest[:])},
	}))
	require.Equal(t, "failed", policy.status[failedSpec.Package])
	require.False(t, broker.tasks[other.Task].terminal)
	ledger, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: other.Task,
		Attempt: 1, Node: "node-a", Branch: other.Branch,
		ExpectedOID: other.ReservationOID, ExpectedLedgerOID: ledger.OID,
		InputsDigest: broker.tasks[other.Task].inputsDigest, ProposalID: "independent-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	require.True(t, accepted.Accepted, "%+v", accepted)
	require.Equal(t, accepted.OID, remoteChoreographyOID(t, fixture, other.Branch))
}

func TestChoreographyNoncausalLocalFailureRequiresPriorSignedPackageFailure(t *testing.T) {
	_, persist, binding, causal := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "build", Package: "shared-package"},
		ChoreographyTaskSpec{Task: "sibling-build", Package: "shared-package"})
	sibling := persist.opts.Tasks[1]
	digest := sha256.Sum256([]byte("preframe failure"))
	blocked := func(spec ChoreographyTaskSpec, status string) ChoreographyBlockedOutcome {
		return ChoreographyBlockedOutcome{ChoreographyRunBinding: binding,
			Task: spec.Task, Attempt: 1, ExpectedOID: spec.ReservationOID,
			Package: spec.Package, BlockedBy: spec.Package, PackageOutcome: status,
			LocalFailure: &ChoreographyLocalFailureEvidence{Part: choreographyFailurePartFrame,
				ErrorSHA256: hex.EncodeToString(digest[:]), ReservationOID: spec.ReservationOID}}
	}
	_, err := persist.PersistBlocked(t.Context(), blocked(sibling, ""))
	require.ErrorContains(t, err, "prior signed outcome")
	_, err = persist.PersistBlocked(t.Context(), blocked(causal, "failed"))
	require.NoError(t, err)
	_, err = persist.PersistBlocked(t.Context(), blocked(sibling, ""))
	require.NoError(t, err)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	page, err := restarted.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, restarted), 3)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	require.Equal(t, "failed", restarted.packageState[causal.Package])
}

func newChoreographyLedgerFixture(t *testing.T, requested ...ChoreographyTaskSpec) (*mailboxFixture, *GitChoreographyPersist,
	ChoreographyRunBinding, ChoreographyTaskSpec) {
	return newChoreographyLedgerFixtureWithAuthority(t,
		"0123456789abcdef0123456789abcdef", requested...)
}

func newChoreographyLedgerFixtureWithAuthority(t *testing.T, authorityID string,
	requested ...ChoreographyTaskSpec) (*mailboxFixture, *GitChoreographyPersist,
	ChoreographyRunBinding, ChoreographyTaskSpec) {
	return newChoreographyLedgerFixtureWithSettings(t, choreographyLedgerFixtureSettings{
		AuthorityID: authorityID, AdmissionSeconds: choreographyTestAdmissionSeconds,
		Tasks: requested})
}

type choreographyLedgerFixtureSettings struct {
	AuthorityID      string
	AdmissionSeconds int
	Tasks            []ChoreographyTaskSpec
	OutputRoots      []string
}

func newChoreographyLedgerFixtureWithSettings(t *testing.T, settings choreographyLedgerFixtureSettings) (*mailboxFixture, *GitChoreographyPersist,
	ChoreographyRunBinding, ChoreographyTaskSpec) {
	t.Helper()
	fixture := newMailboxFixture(t)
	binding := ChoreographyRunBinding{Run: FormatRunID(), Generation: "generation",
		PlanDigest: "plan"}
	requested := settings.Tasks
	if len(requested) == 0 {
		requested = []ChoreographyTaskSpec{{Task: "build"}}
	}
	specs := make([]ChoreographyTaskSpec, len(requested))
	templates := make([]ChoreographyCompiledTemplate, 0, len(requested))
	for index, requestedSpec := range requested {
		spec := requestedSpec
		if spec.Virtual {
			specs[index] = spec
			continue
		}
		if spec.Kind == "" {
			spec.Kind = string(KindBuild)
		}
		if spec.Budget == "" {
			spec.Budget = "build"
		}
		if spec.Weight == 0 {
			spec.Weight = 1
		}
		if spec.InputsDigest == "" && !spec.ExecutorOwned {
			spec.InputsDigest = "inputs"
		}
		if spec.Placement == "" {
			spec.Placement = PlacementOrchestrator
		}
		spec.Branch = FormatChoreographyTaskBranch(binding.Run, spec.Task)
		template := ChoreographyCompiledTemplate{Task: spec.Task, Kind: spec.Kind,
			Stage: spec.Stage, Weight: spec.Weight, Outputs: settings.OutputRoots}
		var err error
		spec.TemplateDigest, err = DigestChoreographyCompiledTemplate(template)
		require.NoError(t, err)
		specs[index] = spec
		templates = append(templates, template)
	}
	var err error
	binding.GraphDigest, err = DigestChoreographyLogicalGraph(ChoreographyLogicalGraphInventory{
		Binding: binding, AlgorithmVersion: ChoreographyGraphAlgorithmVersion,
		SemanticGraphDigest: "fixture-semantic-graph", Tasks: specs, Templates: templates})
	require.NoError(t, err)
	fixturePlumbing := gitx.NewPlumbing(fixture.git)
	sourceTree := fixturePlumbing.MakeTree(t.Context(), nil)
	plannedOID := fixturePlumbing.CommitTree(t.Context(), sourceTree, nil, "fixture-planned-source")
	sourceOID := fixturePlumbing.CommitTree(t.Context(), sourceTree, []string{plannedOID}, "fixture-source")
	require.NoError(t, fixturePlumbing.Err())
	policy := ChoreographyJoinPolicy{AdmissionDurationSeconds: settings.AdmissionSeconds,
		Authorized: []ChoreographyAuthorizedNode{{Name: "node-a", CoordinationEndpoint: fixture.endpoint}}}
	bootstrapOID, err := fixture.mailbox.PrepareChoreographyBootstrap(t.Context(), ChoreographyBootstrapOptions{
		Bootstrap: &RunBootstrap{Protocol: ChoreographyProtocolVersion, ChoreographyRunBinding: binding,
			Mode: "compilation", AlgorithmVersion: ChoreographyGraphAlgorithmVersion,
			JoinPolicy: policy, CoordinationEndpoint: fixture.endpoint,
			ReturnEndpoint: "https://localhost:8383", ReturnCertSHA256: strings.Repeat("0", 64),
			PinnedInputs: PinnedRunInputs{Repositories: []AssignmentRepository{{Snapshot: sourceOID, PlannedHead: plannedOID}},
				Selection: []byte(`{"command":"release"}`), Options: []byte(`{"strict":false}`)}},
		MaxDocumentBytes: 1 << 20, MaxTagBytes: 1 << 20})
	require.NoError(t, err)
	root, err := PrepareChoreographyLedgerRoot(t.Context(), ChoreographyLedgerRootOptions{
		Mailbox: fixture.mailbox, Binding: binding, BootstrapOID: bootstrapOID,
		Roster: ChoreographyRuntimeRoster{
			Nodes:   []ChoreographyRuntimeNode{{Name: "node-a", Platform: "linux/amd64", Capacity: 1, First: true, AuthorityID: settings.AuthorityID}},
			Budgets: []ChoreographyRuntimeBudget{{Name: "build", Capacity: 1}},
		}})
	require.NoError(t, err)
	refs := []gitx.CreateRef{{Branch: root.Branch, OID: root.OID}}
	for index := range specs {
		spec := &specs[index]
		if spec.Virtual {
			continue
		}
		reservation := &Reservation{Header: Header{Protocol: ChoreographyProtocolVersion,
			Kind: spec.Kind, Run: binding.Run, Generation: binding.Generation,
			PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
			Branch: spec.Branch, IssuedAt: time.Now().UTC().Format(time.RFC3339)},
			BootstrapOID: bootstrapOID, MaxAttempts: maxPlacementAttempts}
		spec.ReservationOID, err = fixture.mailbox.PrepareChoreographyReservation(t.Context(), reservation)
		require.NoError(t, err)
		refs = append(refs, gitx.CreateRef{Branch: spec.Branch, OID: spec.ReservationOID})
	}
	require.NoError(t, fixture.git.PushCreateAtomic(t.Context(), fixture.endpoint, refs))
	carrier := ChoreographyGraphCarrier{ChoreographyRunBinding: binding, BootstrapOID: bootstrapOID,
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion, SemanticGraphDigest: "fixture-semantic-graph",
		Tasks: specs, Templates: templates}
	carrier.LogicalDescriptorDigest, carrier.ManifestDigest, err = digestChoreographyGraphCarrier(carrier)
	require.NoError(t, err)
	carrierBytes, err := json.Marshal(carrier)
	require.NoError(t, err)
	graphRootOID, err := fixture.mailbox.commitMessage(t.Context(), choreographyGraphRootKind,
		carrierBytes, []string{bootstrapOID}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushCreateAtomic(t.Context(), fixture.endpoint,
		[]gitx.CreateRef{{Branch: FormatChoreographyGraphRootBranch(binding.Run), OID: graphRootOID}}))
	initEvent := ChoreographyEvent{Sequence: 1, Kind: ChoreographyInitializationComplete,
		Task: choreographyRunTaskID, TaskOID: graphRootOID, GraphRootOID: graphRootOID}
	initRecord := choreographyLedgerRecord{ChoreographyRunBinding: binding, Sequence: 1, Event: &initEvent}
	initBytes, err := json.Marshal(initRecord)
	require.NoError(t, err)
	initOID, err := fixture.mailbox.commitMessage(t.Context(), choreographyLedgerKind,
		initBytes, []string{root.OID, graphRootOID}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushAtomicAdvances(t.Context(), fixture.endpoint,
		[]gitx.BranchAdvance{{Branch: root.Branch, ExpectedOld: root.OID, OID: initOID}}))
	persist, err := NewGitChoreographyPersist(GitChoreographyOptions{Binding: binding,
		Mailbox: fixture.mailbox, VerifyFirst: func(context.Context) error { return nil },
		VerifyResult: func(context.Context, ChoreographyResultProposal, Result, *gitx.LocalGitx) error { return nil },
		MaxPackBytes: 1 << 20, Ledger: root, BootstrapOID: bootstrapOID,
		GraphRootOID: graphRootOID, InitializationCompleteOID: initOID,
		JoinPolicy: policy, AlgorithmVersion: ChoreographyGraphAlgorithmVersion,
		Tasks: specs, Templates: templates})
	require.NoError(t, err)
	_, _, err = persist.PersistExecutionStart(t.Context())
	require.NoError(t, err)
	return fixture, persist, binding, specs[0]
}

func choreographyFixtureStartCursor(t *testing.T, persist *GitChoreographyPersist) ChoreographyEventCursor {
	t.Helper()
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 2)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	require.Equal(t, ChoreographyInitializationComplete, page.Events[0].Kind)
	require.Equal(t, ChoreographyExecutionStarted, page.Events[1].Kind)
	return page.Cursor
}

func completeChoreographyFixtureClaim(t *testing.T, persist *GitChoreographyPersist,
	proposal *ChoreographyClaimProposal) {
	t.Helper()
	head, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	proposal.ExpectedLedgerOID = head.OID
	proposal.GraphRootOID = persist.opts.GraphRootOID
	proposal.MembershipEpochOID = persist.opts.InitializationCompleteOID
	_, roster, err := ReadChoreographyLedgerRoot(t.Context(), persist.opts.Mailbox,
		persist.opts.Binding, persist.opts.BootstrapOID)
	require.NoError(t, err)
	require.Len(t, roster.Nodes, 1)
	proposal.CapacityAuthorityID = roster.Nodes[0].AuthorityID
	proposal.LeaseIntentID = nodeSlotLeaseID(NodeSlotIntent{Run: proposal.Run,
		Task: proposal.Task, Attempt: proposal.Attempt})
	proposal.LeaseWeight = persist.tasks[proposal.Task].Weight
	proposal.TemplateDigest = persist.tasks[proposal.Task].TemplateDigest
	proposal.Bindings = ChoreographyTaskBindings{}
	proposal.BindingsDigest, err = DigestChoreographyTaskBindings(proposal.Bindings)
	require.NoError(t, err)
	proposal.resolvedBindings = &ChoreographyTaskBindings{}
}

func choreographyFixtureBrokerOptions(persist *GitChoreographyPersist) FirstNodeBrokerOptions {
	return FirstNodeBrokerOptions{Binding: persist.opts.Binding,
		GraphRootOID:              persist.opts.GraphRootOID,
		InitialMembershipEpochOID: persist.opts.InitializationCompleteOID,
		JoinPolicy:                persist.opts.JoinPolicy, FirstNode: "node-a",
		Tasks: persist.opts.Tasks, Templates: persist.opts.Templates,
		Members: []ChoreographyRuntimeNode{{Name: "node-a", First: true,
			Platform: "linux/amd64", Capacity: 1,
			AuthorityID: "0123456789abcdef0123456789abcdef"}},
		NodeCapacity: map[string]int{"node-a": 1}, Budgets: map[string]int{"build": 1},
		ResolveBindings: func(context.Context, string, string) (ChoreographyTaskBindings, error) {
			return ChoreographyTaskBindings{}, nil
		}, Persist: persist}
}

func TestChoreographyClaimAtomicallyMovesTaskAndLedgerAndFeedsExactEvent(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	root, roster, err := ReadChoreographyLedgerRoot(t.Context(), fixture.mailbox, binding, persist.opts.BootstrapOID)
	require.NoError(t, err)
	require.Equal(t, persist.opts.Ledger, root)
	require.Equal(t, []ChoreographyRuntimeNode{{Name: "node-a", Platform: "linux/amd64", Capacity: 1, First: true, AuthorityID: "0123456789abcdef0123456789abcdef"}}, roster.Nodes)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		ExpectedLedgerOID: persist.opts.Ledger.OID,
		InputsDigest:      spec.InputsDigest, ProposalID: "proposal-1"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	receipt, err := persist.PersistClaim(t.Context(), claim)
	require.NoError(t, err)
	require.Equal(t, receipt.OID, remoteChoreographyOID(t, fixture, spec.Branch))
	ledgerTip := remoteChoreographyOID(t, fixture, persist.opts.Ledger.Branch)
	require.NotEqual(t, persist.opts.Ledger.OID, ledgerTip)
	page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 1)
	require.NoError(t, err)
	require.False(t, page.More)
	claimDigest, err := ChoreographyProposalDigest(claim)
	require.NoError(t, err)
	require.Equal(t, uint64(3), page.Events[0].Sequence)
	require.Equal(t, ChoreographyClaimAccepted, page.Events[0].Kind)
	require.Equal(t, spec.Task, page.Events[0].Task)
	require.Equal(t, receipt.OID, page.Events[0].TaskOID)
	require.Equal(t, spec.ReservationOID, page.Events[0].PriorTaskOID)
	require.Equal(t, receipt.AssignmentOID, page.Events[0].AssignmentOID)
	require.Equal(t, persist.opts.GraphRootOID, page.Events[0].GraphRootOID)
	require.Equal(t, claimDigest, page.Events[0].ProposalDigest)
	require.Equal(t, claim.ProposalID, page.Events[0].ProposalID)
	require.Equal(t, claim.InputsDigest, page.Events[0].InputsDigest)
	require.Equal(t, ledgerTip, page.Cursor.OID)
	second, err := persist.ReadEvents(t.Context(), page.Cursor, 1)
	require.NoError(t, err)
	require.Empty(t, second.Events)
	query, err := FormatClaimReceiptQuery(claim)
	require.NoError(t, err)
	found, ok, err := persist.LookupReceipt(t.Context(), query)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, receipt, found)
}

func TestChoreographyIndexedTaskWaitWakesOnExactDurableEvent(t *testing.T) {
	_, persist, binding, spec := newChoreographyLedgerFixture(t)
	root := choreographyFixtureStartCursor(t, persist)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type answer struct {
		event  ChoreographyEvent
		cursor ChoreographyEventCursor
		err    error
	}
	ready := make(chan answer, 1)
	go func() {
		event, cursor, err := persist.WaitTaskEvent(ctx, spec.Task, root)
		ready <- answer{event, cursor, err}
	}()
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		ExpectedLedgerOID: root.OID, InputsDigest: spec.InputsDigest, ProposalID: "indexed-wait"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	receipt, err := persist.PersistClaim(ctx, claim)
	require.NoError(t, err)
	got := <-ready
	require.NoError(t, got.err)
	require.Equal(t, ChoreographyClaimAccepted, got.event.Kind)
	require.Equal(t, receipt.OID, got.event.TaskOID)
	require.Equal(t, uint64(3), got.cursor.Sequence)
	_, _, err = persist.WaitTaskEvent(ctx, spec.Task,
		ChoreographyEventCursor{OID: "outside-ledger", Sequence: 1})
	require.ErrorContains(t, err, "outside the signed ledger")
}

func TestChoreographyAdmissionStopChecksSignedOfferInterval(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unclaimed", true: "claim receipt lost"}[claimed], func(t *testing.T) {
			_, persist, binding, spec := newChoreographyLedgerFixture(t)
			offer := choreographyFixtureStartCursor(t, persist)
			claimOID := ""
			if claimed {
				claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
					Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
					ExpectedOID: spec.ReservationOID, ExpectedLedgerOID: offer.OID,
					InputsDigest: spec.InputsDigest, ProposalID: "lost-reply"}
				completeChoreographyFixtureClaim(t, persist, &claim)
				receipt, err := persist.PersistClaim(t.Context(), claim)
				require.NoError(t, err)
				claimOID = receipt.OID
			}
			stopped, err := persist.StopClaims(t.Context())
			require.NoError(t, err)
			events, err := persist.TaskEventsThrough(t.Context(), spec.Task, offer, stopped)
			require.NoError(t, err)
			if claimed {
				require.Len(t, events, 1)
				require.Equal(t, ChoreographyClaimAccepted, events[0].Kind)
				require.Equal(t, claimOID, events[0].TaskOID)
				require.Equal(t, spec.ReservationOID, events[0].PriorTaskOID)
			} else {
				require.Empty(t, events, "the durable stop fences all delayed claims")
			}
			_, err = persist.TaskEventsThrough(t.Context(), spec.Task,
				ChoreographyEventCursor{OID: "not-in-ledger"}, stopped)
			require.ErrorContains(t, err, "outside the signed ledger")
		})
	}
}

func TestChoreographyCompiledClaimAtomicallyMovesTaskAndLedgerWithoutOffer(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	slot, err := PrepareChoreographySlotRoot(t.Context(), ChoreographySlotRootOptions{
		Mailbox: fixture.mailbox, Binding: binding, BootstrapOID: persist.opts.BootstrapOID,
		Node: "node-a", Index: 0})
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushCreateAtomic(t.Context(), fixture.endpoint,
		[]gitx.CreateRef{{Branch: slot.branch, OID: slot.oid}}))
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		InputsDigest: spec.InputsDigest,
		ProposalID:   "compiled-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	receipt, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	require.True(t, receipt.Accepted)
	require.NotEmpty(t, receipt.AssignmentOID)
	require.Equal(t, receipt.OID, remoteChoreographyOID(t, fixture, spec.Branch))
	require.Equal(t, slot.oid, remoteChoreographyOID(t, fixture, slot.branch))
	claimParents, err := fixture.mailbox.plumbing.CommitParents(t.Context(), receipt.OID)
	require.NoError(t, err)
	require.Equal(t, receipt.AssignmentOID, claimParents[0])
	assignmentParents, err := fixture.mailbox.plumbing.CommitParents(t.Context(), receipt.AssignmentOID)
	require.NoError(t, err)
	require.Equal(t, spec.ReservationOID, assignmentParents[0])
	verified, err := fixture.mailbox.OpenCarriedAssignment(t.Context(), receipt.AssignmentOID, 1<<20)
	require.NoError(t, err)
	require.Equal(t, persist.opts.GraphRootOID, verified.Assignment.GraphRootOID)
	page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 2)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, ChoreographyClaimAccepted, page.Events[0].Kind)
	require.Equal(t, receipt.AssignmentOID, page.Events[0].AssignmentOID)
	recovered, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	require.Equal(t, receipt.OID, recovered.tasks[spec.Task].claim)
	require.Equal(t, 1, recovered.nodeUsed["node-a"])
}

func TestChoreographyStaleDirectClaimLookupNamesExactInertReservation(t *testing.T) {
	_, persist, binding, spec := newChoreographyLedgerFixture(t)
	stale := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		InputsDigest: spec.InputsDigest,
		ProposalID:   "delayed-compiled-claim"}
	completeChoreographyFixtureClaim(t, persist, &stale)
	advance := stale
	advance.ProposalID = "unrelated-ledger-advance"
	_, err := persist.PersistRefusal(t.Context(), advance, ChoreographyRefusalNotReady)
	require.NoError(t, err)
	query, err := FormatClaimReceiptQuery(stale)
	require.NoError(t, err)
	refused, found, err := persist.LookupReceipt(t.Context(), query)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, refused.Refused)
	require.Equal(t, ChoreographyRefusalStale, refused.RefusalReason)
	require.Equal(t, spec.ReservationOID, refused.OID)
	require.NotEqual(t, query.ExpectedLedgerOID, refused.FenceOID)

	query.ExpectedOID = "0000000000000000000000000000000000000000"
	_, found, err = persist.LookupReceipt(t.Context(), query)
	require.ErrorContains(t, err, "no authenticated expected task state")
	require.False(t, found)
}

func TestChoreographyFirstLocalClaimReproposesAfterAnotherLedgerEvent(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		InputsDigest: spec.InputsDigest}
	completeChoreographyFixtureClaim(t, persist, &claim)
	claim.ProposalID = returnProposalID(choreographyClaimKind, binding.Run, spec.Task,
		claim.Attempt, claim.ExpectedOID+"\x00"+claim.ExpectedLedgerOID)
	advance := claim
	advance.ProposalID = "another-ledger-event"
	_, err = persist.PersistRefusal(t.Context(), advance, ChoreographyRefusalNotReady)
	require.NoError(t, err)
	accepted, err := acceptFirstLocalClaim(t.Context(), broker, claim)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)
	require.Equal(t, accepted.OID, remoteChoreographyOID(t, fixture, spec.Branch))
	parents, err := fixture.mailbox.plumbing.CommitParents(t.Context(), accepted.OID)
	require.NoError(t, err)
	require.Equal(t, accepted.AssignmentOID, parents[0])
	assignmentParents, err := fixture.mailbox.plumbing.CommitParents(t.Context(), accepted.AssignmentOID)
	require.NoError(t, err)
	require.Equal(t, spec.ReservationOID, assignmentParents[0])
	page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 8)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	require.Equal(t, ChoreographyClaimRefused, page.Events[0].Kind)
	require.Equal(t, ChoreographyClaimAccepted, page.Events[1].Kind)
}

func TestChoreographyStaleClaimRejectsExpectedAssignmentWithAnotherRunCarrier(t *testing.T) {
	for name, mutate := range map[string]func(*Assignment){
		"graph":     func(a *Assignment) { a.GraphDigest = "another-graph" },
		"bootstrap": func(a *Assignment) { a.BootstrapOID = "0000000000000000000000000000000000000000" },
	} {
		t.Run(name, func(t *testing.T) {
			fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
			assignment := &Assignment{Weight: spec.Weight, GraphDigest: binding.GraphDigest,
				BootstrapOID: persist.opts.BootstrapOID, Header: Header{Protocol: ChoreographyProtocolVersion,
					Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
					PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
					Node: "node-a", Branch: spec.Branch, IssuedAt: time.Now().UTC().Format(time.RFC3339)}}
			mutate(assignment)
			document, err := json.Marshal(assignment)
			require.NoError(t, err)
			assignmentOID, err := fixture.mailbox.commitMessage(t.Context(), MessageAssignment,
				document, []string{spec.ReservationOID}, nil)
			require.NoError(t, err)
			require.NoError(t, fixture.git.PushAtomicAdvances(t.Context(), fixture.endpoint,
				[]gitx.BranchAdvance{{Branch: spec.Branch, ExpectedOld: spec.ReservationOID, OID: assignmentOID}}))
			_, err = persist.StopClaims(t.Context())
			require.NoError(t, err)
			claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
				Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: assignmentOID,
				ExpectedLedgerOID: persist.opts.Ledger.OID,
				InputsDigest:      CompiledClaimInputDigest(assignmentOID), ProposalID: "stale-wrong-carrier"}
			query, err := FormatClaimReceiptQuery(claim)
			require.NoError(t, err)
			_, found, err := persist.LookupReceipt(t.Context(), query)
			require.Error(t, err)
			require.False(t, found)
		})
	}
}

func TestChoreographyLookupDoesNotAcceptTaskCommitWithoutLedgerEvent(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, ExpectedLedgerOID: persist.opts.Ledger.OID,
		InputsDigest: spec.InputsDigest, ProposalID: "unaccepted-task-commit"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	document, err := json.Marshal(claim)
	require.NoError(t, err)
	claimOID, err := fixture.mailbox.commitMessage(t.Context(), choreographyClaimKind,
		document, []string{spec.ReservationOID}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushAtomicAdvances(t.Context(), fixture.endpoint,
		[]gitx.BranchAdvance{{Branch: spec.Branch, ExpectedOld: spec.ReservationOID, OID: claimOID}}))
	query, err := FormatClaimReceiptQuery(claim)
	require.NoError(t, err)
	_, accepted, err := persist.LookupReceipt(t.Context(), query)
	require.ErrorContains(t, err, "no signed ledger acceptance")
	require.False(t, accepted)
}

func TestChoreographyRejectedClaimCannotConsumeCapacityLedger(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	document, err := json.Marshal(ChoreographyDecision{ChoreographyRunBinding: binding,
		Task: spec.Task, Branch: spec.Branch, Attempt: 1, ResultOID: spec.ReservationOID})
	require.NoError(t, err)
	foreign, err := fixture.mailbox.commitMessage(t.Context(), choreographyDecisionKind,
		document, []string{spec.ReservationOID}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushAdvance(t.Context(), fixture.endpoint, foreign,
		spec.Branch, spec.ReservationOID))
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		ExpectedLedgerOID: persist.opts.Ledger.OID,
		InputsDigest:      spec.InputsDigest, ProposalID: "proposal-1"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	_, err = persist.PersistClaim(t.Context(), claim)
	require.Error(t, err)
	require.Equal(t, foreign, remoteChoreographyOID(t, fixture, spec.Branch))
	require.Equal(t, claim.ExpectedLedgerOID,
		remoteChoreographyOID(t, fixture, persist.opts.Ledger.Branch),
		"an atomic stale claim cannot consume capacity")
}

func TestChoreographyRefusalFencesDelayedClaimAndSurvivesBrokerRestart(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		ExpectedLedgerOID: persist.opts.Ledger.OID,
		InputsDigest:      spec.InputsDigest, ProposalID: "capacity-refused"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	query, err := FormatClaimReceiptQuery(claim)
	require.NoError(t, err)
	before, found, err := persist.LookupReceipt(t.Context(), query)
	require.NoError(t, err, "a v3 reservation ends receipt search before its bootstrap parent")
	require.False(t, found)
	require.Empty(t, before.OID)
	refused, err := persist.PersistRefusal(t.Context(), claim, ChoreographyRefusalCapacity)
	require.NoError(t, err)
	require.True(t, refused.Refused)
	require.False(t, refused.Accepted)
	require.NotEqual(t, claim.ExpectedLedgerOID, refused.FenceOID)
	require.Equal(t, spec.ReservationOID, remoteChoreographyOID(t, fixture, spec.Branch))
	_, err = persist.PersistClaim(t.Context(), claim)
	require.ErrorContains(t, err, "stale capacity epoch")
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	recovered, found, err := restarted.LookupReceipt(t.Context(), query)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, refused, recovered)
	page, err := restarted.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, restarted), 1)
	require.NoError(t, err)
	require.Equal(t, ChoreographyClaimRefused, page.Events[0].Kind)
	require.Equal(t, query.ProposalDigest, page.Events[0].ProposalDigest)
	// A distinct proposal that was in flight against the old epoch but
	// received no response is also permanently fenced. Its exact query is
	// enough to derive that answer after the first node restarts.
	delayed := claim
	delayed.ProposalID = "delayed-capacity-proposal"
	delayedQuery, err := FormatClaimReceiptQuery(delayed)
	require.NoError(t, err)
	stale, found, err := restarted.LookupReceipt(t.Context(), delayedQuery)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, stale.Refused)
	require.Equal(t, refused.FenceOID, stale.FenceOID)
	_, err = restarted.PersistClaim(t.Context(), delayed)
	require.ErrorContains(t, err, "stale capacity epoch")
}

func TestChoreographyAcceptedClaimLookupOutlivesTaskRefCleanup(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		ExpectedLedgerOID: persist.opts.Ledger.OID,
		InputsDigest:      spec.InputsDigest, ProposalID: "accepted-before-cleanup"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	accepted, err := persist.PersistClaim(t.Context(), claim)
	require.NoError(t, err)
	deleted, err := fixture.mailbox.Withdraw(t.Context(), spec.Branch, accepted.OID)
	require.NoError(t, err)
	require.True(t, deleted)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	query, err := FormatClaimReceiptQuery(claim)
	require.NoError(t, err)
	recovered, found, err := restarted.LookupReceipt(t.Context(), query)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, accepted, recovered, "an accepted claim is never misclassified as refused")
}

func TestChoreographySealRetainsFenceForColdPendingClaim(t *testing.T) {
	_, persist, binding, spec := newChoreographyLedgerFixture(t)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		ExpectedLedgerOID: persist.opts.Ledger.OID,
		InputsDigest:      spec.InputsDigest, ProposalID: "lost-claim-before-close"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	stopping, err := persist.StopClaims(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, claim.ExpectedLedgerOID, stopping.OID)
	_, err = persist.SealRun(t.Context(), false)
	require.ErrorContains(t, err, "unfinished task")
	sealed, err := persist.SealRun(t.Context(), true)
	require.NoError(t, err)
	require.NotEqual(t, claim.ExpectedLedgerOID, sealed.OID)
	leaves, err := persist.CanonicalTaskLeases(t.Context())
	require.NoError(t, err)
	require.Equal(t, []gitx.BranchLease{{Branch: spec.Branch, ExpectedOld: spec.ReservationOID}}, leaves,
		"a sealed run exposes only exact task leases, never its required feed ref")
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	query, err := FormatClaimReceiptQuery(claim)
	require.NoError(t, err)
	refused, found, err := restarted.LookupReceipt(t.Context(), query)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, ChoreographyRefusalSealed, refused.RefusalReason)
	require.Equal(t, sealed.OID, refused.FenceOID)
	_, err = restarted.PersistClaim(t.Context(), claim)
	require.ErrorContains(t, err, "sealed")
	page, err := restarted.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, restarted), 2)
	require.NoError(t, err)
	require.Equal(t, ChoreographyRunStopping, page.Events[0].Kind)
	require.Equal(t, ChoreographyRunSealed, page.Events[1].Kind)
	require.True(t, page.Events[1].Aborted)
}

func TestChoreographyCannotSealWhileAcceptedClaimRemainsActive(t *testing.T) {
	_, persist, binding, spec := newChoreographyLedgerFixture(t)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		ExpectedLedgerOID: persist.opts.Ledger.OID,
		InputsDigest:      spec.InputsDigest, ProposalID: "active-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	_, err := persist.PersistClaim(t.Context(), claim)
	require.NoError(t, err)
	_, err = persist.StopClaims(t.Context())
	require.NoError(t, err)
	_, err = persist.SealRun(t.Context(), true)
	require.ErrorContains(t, err, "active claim")
}

func TestChoreographyBrokerReplaysHeldCapacityBeforeTakingNewClaims(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	rootOID, err := fixture.mailbox.ReadChoreographyTaskRoot(t.Context(), ChoreographyTaskRootQuery{
		Binding: binding, Task: spec.Task, Kind: spec.Kind,
		BootstrapOID: persist.opts.BootstrapOID,
	})
	require.NoError(t, err)
	require.Equal(t, spec.ReservationOID, rootOID)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		ExpectedLedgerOID: persist.opts.Ledger.OID,
		InputsDigest:      spec.InputsDigest, ProposalID: "claim-before-restart"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	accepted, err := persist.PersistClaim(t.Context(), claim)
	require.NoError(t, err)
	rootOID, err = fixture.mailbox.ReadChoreographyTaskRoot(t.Context(), ChoreographyTaskRootQuery{
		Binding: binding, Task: spec.Task, Kind: spec.Kind,
		BootstrapOID: persist.opts.BootstrapOID,
	})
	require.NoError(t, err)
	require.Equal(t, spec.ReservationOID, rootOID)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	broker, err := RecoverFirstNodeBroker(t.Context(), FirstNodeBrokerOptions{
		Binding: binding, GraphRootOID: persist.opts.GraphRootOID,
		InitialMembershipEpochOID: persist.opts.InitializationCompleteOID,
		FirstNode:                 "node-a", Tasks: []ChoreographyTaskSpec{spec},
		Templates: persist.opts.Templates,
		Members: []ChoreographyRuntimeNode{{Name: "node-a", Platform: "linux/amd64", Capacity: 1,
			First: true, AuthorityID: "0123456789abcdef0123456789abcdef"}},
		NodeCapacity: map[string]int{"node-a": 1}, Budgets: map[string]int{"build": 1},
		JoinPolicy: persist.opts.JoinPolicy, Persist: restarted,
	})
	require.NoError(t, err)
	require.Equal(t, accepted.OID, broker.tasks[spec.Task].claim)
	require.Equal(t, accepted.OID, broker.tasks[spec.Task].tip)
	require.Equal(t, 1, broker.nodeUsed["node-a"])
	require.Equal(t, 1, broker.used["build"])
	query, err := FormatClaimReceiptQuery(claim)
	require.NoError(t, err)
	result, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	require.Equal(t, accepted, result)
	page, err := restarted.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, restarted), 32)
	require.NoError(t, err)
	require.Len(t, page.Events, 1, "duplicate claim must not consume another epoch")
	found, ok, err := restarted.LookupReceipt(t.Context(), query)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, accepted, found)
}

func TestChoreographyBlockedAtomicallyFencesTaskRefAndLedger(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	spec.Package = "app"
	persist.tasks[spec.Task] = spec
	persist.opts.Tasks = []ChoreographyTaskSpec{spec}
	blocked := ChoreographyBlockedOutcome{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, ExpectedOID: spec.ReservationOID,
		Package: spec.Package, BlockedBy: "provider", PackageOutcome: "skipped"}
	oid, err := persist.PersistBlocked(t.Context(), blocked)
	require.NoError(t, err)
	require.Equal(t, oid, remoteChoreographyOID(t, fixture, spec.Branch))
	require.NotEqual(t, persist.opts.Ledger.OID,
		remoteChoreographyOID(t, fixture, persist.opts.Ledger.Branch))
	page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 1)
	require.NoError(t, err)
	require.Equal(t, ChoreographyBlocked, page.Events[0].Kind)
	require.Equal(t, spec.ReservationOID, page.Events[0].PriorTaskOID)
	require.Equal(t, oid, page.Events[0].TaskOID)
	_, err = persist.StopClaims(t.Context())
	require.NoError(t, err)
	_, err = persist.SealRun(t.Context(), false)
	require.NoError(t, err)
	leases, err := persist.CanonicalTaskLeases(t.Context())
	require.NoError(t, err)
	require.Equal(t, []gitx.BranchLease{{Branch: spec.Branch, ExpectedOld: oid}}, leases)
}

func remoteChoreographyOID(t *testing.T, fixture *mailboxFixture, branch string) string {
	t.Helper()
	oid, err := fixture.mailbox.readRemoteTip(t.Context(), branch)
	require.NoError(t, err)
	return oid
}
