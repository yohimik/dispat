// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// The indexed wait may choose its deadline or cancellation branch while a
// ClaimAccepted event is already durable. This wrapper controls that choice;
// every other broker operation uses the real Git persistence implementation.
type staleExpiryWaitPersist struct {
	*GitChoreographyPersist
	entered           chan struct{}
	claimSeen         chan struct{}
	admissionDeadline time.Time
}

func (p *staleExpiryWaitPersist) WaitTaskEvent(ctx context.Context, task string,
	after ChoreographyEventCursor) (ChoreographyEvent, ChoreographyEventCursor, error) {
	if deadline, ok := ctx.Deadline(); !ok || deadline.After(p.admissionDeadline.Add(time.Second)) {
		event, next, err := p.GitChoreographyPersist.WaitTaskEvent(ctx, task, after)
		if err == nil && event.Kind == ChoreographyClaimAccepted {
			select {
			case p.claimSeen <- struct{}{}:
			default:
			}
		}
		return event, next, err
	}
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ChoreographyEvent{}, ChoreographyEventCursor{}, ctx.Err()
}

func newTimedWorkerExpiryFixture(t *testing.T) (graphCarrierFixture,
	*GitChoreographyPersist, FirstNodeBrokerOptions) {
	t.Helper()
	f := newGraphCarrierFixtureWithPrepared(t, false, false)
	template := f.carrier.Templates[0]
	template.DeadlineSeconds = 30
	spec := f.spec
	var err error
	spec.TemplateDigest, err = DigestChoreographyCompiledTemplate(template)
	require.NoError(t, err)
	binding := f.binding
	binding.GraphDigest = ""
	binding.GraphDigest, err = DigestChoreographyLogicalGraph(ChoreographyLogicalGraphInventory{
		Binding: binding, AlgorithmVersion: ChoreographyGraphAlgorithmVersion,
		SemanticGraphDigest: f.carrier.SemanticGraphDigest,
		Tasks:               []ChoreographyTaskSpec{spec}, Templates: []ChoreographyCompiledTemplate{template}})
	require.NoError(t, err)
	carrier := f.carrier
	carrier.ChoreographyRunBinding = binding
	carrier.Tasks = []ChoreographyTaskSpec{spec}
	carrier.Templates = []ChoreographyCompiledTemplate{template}
	carrier.LogicalDescriptorDigest, carrier.ManifestDigest, err = digestChoreographyGraphCarrier(carrier)
	require.NoError(t, err)
	document, err := json.Marshal(carrier)
	require.NoError(t, err)
	root, err := f.endpoint.mailbox.commitMessage(t.Context(), choreographyGraphRootKind,
		document, []string{f.bootstrap}, nil)
	require.NoError(t, err)
	require.NoError(t, f.endpoint.git.PushAtomicAdvances(t.Context(), f.endpoint.endpoint,
		[]gitx.BranchAdvance{{Branch: FormatChoreographyGraphRootBranch(binding.Run),
			ExpectedOld: f.root, OID: root}}))
	ledger, err := PrepareChoreographyLedgerRoot(t.Context(), ChoreographyLedgerRootOptions{
		Mailbox: f.endpoint.mailbox, Binding: binding, BootstrapOID: f.bootstrap,
		Roster: ChoreographyRuntimeRoster{Nodes: []ChoreographyRuntimeNode{
			{Name: "first", First: true, Platform: "linux/amd64", Capacity: 1,
				AuthorityID: "0123456789abcdef0123456789abcdef"},
			{Name: "peer", Platform: "linux/amd64", Capacity: 1,
				AuthorityID: "11111111111111111111111111111111"}},
			Budgets: []ChoreographyRuntimeBudget{{Name: ChoreographyBudgetBuild, Capacity: 1}}}})
	require.NoError(t, err)
	require.NoError(t, f.endpoint.git.PushAtomicAdvances(t.Context(), f.endpoint.endpoint,
		[]gitx.BranchAdvance{{Branch: ledger.Branch, ExpectedOld: f.ledgerRoot, OID: ledger.OID}}))
	f.ledgerRoot = ledger.OID
	initEvent := ChoreographyEvent{Sequence: 1, Kind: ChoreographyInitializationComplete,
		Task: choreographyRunTaskID, TaskOID: root, GraphRootOID: root}
	initRecord := choreographyLedgerRecord{ChoreographyRunBinding: binding,
		Sequence: 1, Event: &initEvent}
	document, err = json.Marshal(initRecord)
	require.NoError(t, err)
	complete, err := f.endpoint.mailbox.commitMessage(t.Context(), choreographyLedgerKind,
		document, []string{f.ledgerRoot, root}, nil)
	require.NoError(t, err)
	require.NoError(t, f.endpoint.git.PushAtomicAdvances(t.Context(), f.endpoint.endpoint,
		[]gitx.BranchAdvance{{Branch: FormatChoreographyLedgerBranch(binding.Run),
			ExpectedOld: f.ledgerRoot, OID: complete}}))
	f.binding, f.spec, f.carrier, f.root, f.complete = binding, spec, carrier, root, complete
	policy := ChoreographyJoinPolicy{AdmissionDurationSeconds: 2,
		Authorized: []ChoreographyAuthorizedNode{{Name: "peer",
			CoordinationEndpoint: f.endpoint.endpoint}}}
	persist, err := NewGitChoreographyPersist(GitChoreographyOptions{
		Binding: binding, Mailbox: f.endpoint.mailbox,
		Ledger: ChoreographyLedgerReservation{Branch: FormatChoreographyLedgerBranch(binding.Run),
			OID: f.ledgerRoot},
		BootstrapOID: f.bootstrap, GraphRootOID: root, InitializationCompleteOID: complete,
		Tasks: carrier.Tasks, Templates: carrier.Templates, JoinPolicy: policy,
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion, MaxPackBytes: 1 << 20,
		VerifyFirst: func(context.Context) error { return nil },
		VerifyResult: func(context.Context, ChoreographyResultProposal, Result, *gitx.LocalGitx) error {
			return nil
		},
	})
	require.NoError(t, err)
	first := ChoreographyRuntimeNode{Name: "first", First: true, Platform: "linux/amd64",
		Capacity: 1, AuthorityID: "0123456789abcdef0123456789abcdef"}
	peer := ChoreographyRuntimeNode{Name: "peer", Platform: "linux/amd64",
		Capacity: 1, AuthorityID: "11111111111111111111111111111111"}
	opts := FirstNodeBrokerOptions{Binding: binding, GraphRootOID: root,
		InitialMembershipEpochOID: complete, FirstNode: first.Name,
		Tasks: carrier.Tasks, Templates: carrier.Templates, Members: []ChoreographyRuntimeNode{first, peer},
		NodeCapacity: map[string]int{first.Name: 1, peer.Name: 1},
		Budgets:      map[string]int{ChoreographyBudgetBuild: 1}, JoinPolicy: policy, Persist: persist}
	return f, persist, opts
}

func TestCompiledAdmissionReconcilesSignedClaimAfterDeadlineRace(t *testing.T) {
	fixture, persist, opts := newTimedWorkerExpiryFixture(t)
	store := gitx.NewPlumbing(fixture.endpoint.git)
	tree := store.MakeTree(t.Context(), nil)
	sourceOID := store.CommitTree(t.Context(), tree, nil, "stale-observer-source")
	require.NoError(t, store.Err())
	opts.ResolveBindings = func(context.Context, string, string) (ChoreographyTaskBindings, error) {
		return ChoreographyTaskBindings{Repositories: []AssignmentRepository{{Path: ".", Snapshot: sourceOID}}}, nil
	}
	probe := &staleExpiryWaitPersist{GitChoreographyPersist: persist,
		entered: make(chan struct{}, 1), claimSeen: make(chan struct{}, 1)}
	opts.Persist = probe
	broker, err := NewFirstNodeBroker(opts)
	require.NoError(t, err)
	_, err = broker.StartExecution(t.Context())
	require.NoError(t, err)
	probe.admissionDeadline, err = time.Parse(choreographyAdmissionTimeFormat,
		broker.admissionDeadlineUTC)
	require.NoError(t, err)
	coordinator := &Coordinator{Run: fixture.binding.Run, GraphRootOID: fixture.root,
		choreographyBroker: broker, choreographyPersist: persist,
		canonicalMailbox: fixture.endpoint.mailbox, choreographyInitialized: true,
		outputs: newOutputRegistry()}
	coordinator.Limits.MaxManifestBytes = 1 << 20
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	type answer struct {
		outcome release.StageOutcome
		err     error
	}
	result := make(chan answer, 1)
	go func() {
		outcome, waitErr := coordinator.waitCompiledTask(ctx, compiledTaskWaitOptions{
			TaskID: fixture.spec.Task, Request: release.StageRequest{TaskID: fixture.spec.Task,
				Stage: "build", Release: &plan.Release{Pkg: &model.Package{Name: "core"}}},
			Admit: func(_ context.Context, event ChoreographyEvent, _ Result) (release.StageOutcome, error) {
				return release.StageOutcome{Node: event.Node}, nil
			}})
		result <- answer{outcome: outcome, err: waitErr}
	}()
	select {
	case <-probe.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("observer did not enter indexed task wait before the admission deadline")
	}
	inputsDigest, _, err := broker.ExpectedInputIdentity(fixture.spec.Task)
	require.NoError(t, err)
	head, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	bindingsDigest, err := DigestChoreographyTaskBindings(ChoreographyTaskBindings{})
	require.NoError(t, err)
	pickupID := NewChoreographyPickupID()
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: fixture.binding,
		GraphRootOID: fixture.root, Task: fixture.spec.Task, Attempt: 1, Node: "peer",
		Branch: fixture.spec.Branch, ExpectedOID: fixture.spec.ReservationOID,
		ExpectedLedgerOID: head.OID, InputsDigest: inputsDigest,
		PickupID: pickupID, ProposalID: "stale-observer-claim",
		MembershipEpochOID: fixture.complete, CapacityAuthorityID: "11111111111111111111111111111111",
		LeaseIntentID: nodeSlotLeaseID(NodeSlotIntent{Run: fixture.binding.Run,
			Task: fixture.spec.Task, Attempt: 1, PickupID: pickupID}),
		LeaseWeight: fixture.spec.Weight, TemplateDigest: fixture.spec.TemplateDigest,
		Bindings: ChoreographyTaskBindings{}, BindingsDigest: bindingsDigest}
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)
	head, err = persist.LedgerHead(t.Context())
	require.NoError(t, err)
	drain := ChoreographyDrainProposal{ChoreographyRunBinding: fixture.binding,
		BootstrapOID: fixture.bootstrap, GraphRootOID: fixture.root, Node: "peer",
		CapacityAuthorityID: claim.CapacityAuthorityID, MembershipEpochOID: fixture.complete,
		ExpectedLedgerOID: head.OID, RequestID: "stale-observer-drain"}
	drained, err := broker.DrainNode(t.Context(), drain)
	require.NoError(t, err)
	require.True(t, drained.Accepted)
	require.False(t, broker.IsEligibleNodeAdmitted(fixture.spec.Task))
	select {
	case <-probe.claimSeen:
	case <-ctx.Done():
		t.Fatal("observer did not consume the signed Claim after the deadline race")
	}
	workerResult := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: fixture.spec.Kind, Run: fixture.binding.Run,
		Generation: fixture.binding.Generation, PlanDigest: fixture.binding.PlanDigest,
		Task: fixture.spec.Task, Attempt: claim.Attempt, Node: claim.Node,
		Branch: claim.Branch}, Status: StatusSucceeded}
	resultProposal, pack := prepareChoreographyFixturePickupResult(t, fixture.endpoint,
		choreographyFixturePickupResult{Claim: claim, Accepted: accepted,
			Result: workerResult, ProposalID: "stale-observer-result"})
	acceptedResult, err := broker.AcceptResult(t.Context(), resultProposal, pack)
	require.NoError(t, err)
	select {
	case got := <-result:
		require.NoError(t, got.err)
		require.Equal(t, "peer", got.outcome.Node)
	case <-ctx.Done():
		t.Fatal("observer did not consume the signed Claim and Result after the deadline race")
	}
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 8)
	require.NoError(t, err)
	var claimed, drainedEvent, terminal, expired bool
	for _, event := range page.Events {
		switch event.Kind {
		case ChoreographyClaimAccepted:
			claimed = event.Task == fixture.spec.Task && event.TaskOID == accepted.OID
		case ChoreographyMemberDraining:
			drainedEvent = event.Node == "peer"
		case ChoreographyTerminal:
			terminal = event.Task == fixture.spec.Task &&
				event.ResultReceiptOID == acceptedResult.OID
		case ChoreographyNoClaimExpired:
			expired = true
		}
	}
	require.True(t, claimed)
	require.True(t, drainedEvent)
	require.True(t, terminal)
	require.False(t, expired, "broker must not overwrite an accepted Claim with no-claim finality")
}

func requireNoClaimDisposition(t *testing.T, err error,
	want choreographyNoClaimDisposition) {
	t.Helper()
	var refused *choreographyNoClaimBrokerRefusal
	require.ErrorAs(t, err, &refused)
	require.Equal(t, want, refused.disposition)
}

func TestNoClaimExpiryClassifiesOnlyBrokerLockedProgress(t *testing.T) {
	t.Run("signed deadline remains in the future", func(t *testing.T) {
		_, persist, opts := newExecutionStartFixture(t)
		broker, err := NewFirstNodeBroker(opts)
		require.NoError(t, err)
		_, err = broker.StartExecution(t.Context())
		require.NoError(t, err)
		requireNoClaimDisposition(t, broker.ExpireUnclaimed(t.Context(), opts.Tasks[0].Task),
			choreographyNoClaimDispositionDeadlineFuture)
		page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 4)
		require.NoError(t, err)
		require.Len(t, page.Events, 2, "an early timer must not sign no-claim")
	})
	t.Run("unexplained state and persistence faults stay strict", func(t *testing.T) {
		_, _, opts := newExecutionStartFixture(t)
		broker, err := NewFirstNodeBroker(opts)
		require.NoError(t, err)
		_, err = broker.StartExecution(t.Context())
		require.NoError(t, err)
		task := broker.tasks[opts.Tasks[0].Task]
		assertStrict := func(want string) {
			t.Helper()
			got := broker.ExpireUnclaimed(t.Context(), task.spec.Task)
			require.ErrorContains(t, got, want)
			var refused *choreographyNoClaimBrokerRefusal
			require.False(t, errors.As(got, &refused))
		}
		task.offeredNode = "peer"
		assertStrict(choreographyNoClaimOfferedReason)
		task.offeredNode = ""
		task.result = "unsettled-result"
		assertStrict(choreographyNoClaimResultReason)
		task.result = ""
		task.queueFence = "queued"
		assertStrict(choreographyNoClaimQueueReason)
		task.queueFence = ""
		task.attempt++
		assertStrict(choreographyNoClaimAttemptReason)
		task.attempt--
		task.tip = "unexpected-tip"
		assertStrict(choreographyNoClaimTipReason)
		task.tip = task.spec.ReservationOID
		unknown := errors.New("unknown signed claim CAS outcome")
		broker.fault = unknown
		require.ErrorIs(t, broker.ExpireUnclaimed(t.Context(), task.spec.Task), unknown)
	})
	t.Run("signed stop uses the normal inert settlement path", func(t *testing.T) {
		_, _, opts := newExecutionStartFixture(t)
		broker, err := NewFirstNodeBroker(opts)
		require.NoError(t, err)
		_, err = broker.StartExecution(t.Context())
		require.NoError(t, err)
		_, err = broker.StopClaims(t.Context())
		require.NoError(t, err)
		requireNoClaimDisposition(t, broker.ExpireUnclaimed(t.Context(), opts.Tasks[0].Task),
			choreographyNoClaimDispositionStopping)
	})
	t.Run("compatible signed join wins the deadline race", func(t *testing.T) {
		fixture, persist, opts := newExecutionStartFixture(t)
		public, private, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		policy := ChoreographyJoinPolicy{AdmissionDurationSeconds: 2,
			Authorized: []ChoreographyAuthorizedNode{
				{Name: "peer", CoordinationEndpoint: fixture.endpoint.endpoint},
				{Name: "late", CoordinationEndpoint: fixture.endpoint.endpoint,
					IdentityPublicKey: base64.RawURLEncoding.EncodeToString(public), LateJoin: true}}}
		opts.JoinPolicy = policy
		persist.opts.JoinPolicy = policy
		broker, err := NewFirstNodeBroker(opts)
		require.NoError(t, err)
		_, err = broker.StartExecution(t.Context())
		require.NoError(t, err)
		head, err := persist.LedgerHead(t.Context())
		require.NoError(t, err)
		drained, err := broker.DrainNode(t.Context(), ChoreographyDrainProposal{
			ChoreographyRunBinding: fixture.binding, BootstrapOID: fixture.bootstrap,
			GraphRootOID: fixture.root, Node: "peer",
			CapacityAuthorityID: "11111111111111111111111111111111",
			MembershipEpochOID:  fixture.complete, ExpectedLedgerOID: head.OID,
			RequestID: "join-expiry-drain"})
		require.NoError(t, err)
		require.True(t, drained.Accepted)
		head, err = persist.LedgerHead(t.Context())
		require.NoError(t, err)
		join := ChoreographyJoinProposal{ChoreographyRunBinding: fixture.binding,
			BootstrapOID: fixture.bootstrap, GraphRootOID: fixture.root,
			AlgorithmVersion: ChoreographyGraphAlgorithmVersion,
			Node:             "late", Endpoint: fixture.endpoint.endpoint, RequestID: "join-expiry-late",
			ExpectedLedgerOID: head.OID,
			Report: NodeReport{Protocol: ChoreographyProtocolVersion, Dispat: "same-build",
				OS: "linux", Arch: "amd64", Capacity: 1,
				AuthorityID: "22222222222222222222222222222222", GitVersion: "git"}}
		require.NoError(t, SignChoreographyJoinProposal(&join, private))
		joined, err := broker.AdmitNode(t.Context(), join)
		require.NoError(t, err)
		require.True(t, joined.Accepted)
		waitForFixtureAdmissionDeadline(t, broker.admissionDeadlineUTC)
		requireNoClaimDisposition(t, broker.ExpireUnclaimed(t.Context(), fixture.spec.Task),
			choreographyNoClaimDispositionEligible)
		require.False(t, broker.tasks[fixture.spec.Task].terminal)
	})
	t.Run("signed inert terminal already won", func(t *testing.T) {
		_, persist, _, spec := newChoreographyLedgerFixtureWithSettings(t,
			choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
				AdmissionSeconds: 1, Tasks: []ChoreographyTaskSpec{{Task: "core:build",
					Package: "core", Kind: string(KindBuild), Placement: PlacementWorker,
					Platforms: []string{"plan9/mips"}}}})
		opts := choreographyFixtureBrokerOptions(persist)
		opts.OutcomePolicy = preparedBrokerPolicy{}
		broker, err := RecoverFirstNodeBroker(t.Context(), opts)
		require.NoError(t, err)
		waitForFixtureAdmissionDeadline(t, broker.admissionDeadlineUTC)
		require.NoError(t, broker.ExpireUnclaimed(t.Context(), spec.Task))
		terminal := broker.tasks[spec.Task].tip
		requireNoClaimDisposition(t, broker.ExpireUnclaimed(t.Context(), spec.Task),
			choreographyNoClaimDispositionAdvanced)
		require.Equal(t, terminal, broker.tasks[spec.Task].tip)
	})
	t.Run("signed prepared blocker takes causal proof path", func(t *testing.T) {
		_, persist, _, provider := newChoreographyLedgerFixtureWithSettings(t,
			choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
				AdmissionSeconds: 1, Tasks: []ChoreographyTaskSpec{
					{Task: "assets:prepare", Package: "assets", Kind: string(KindPrepare),
						Placement: PlacementWorker, Platforms: []string{"plan9/mips"}},
					{Task: "ui:build", Package: "ui", Kind: string(KindBuild),
						Placement: PlacementWorker, PreparedProviders: []string{"assets"},
						Predecessors: []string{"assets:prepare"}},
				}})
		opts := choreographyFixtureBrokerOptions(persist)
		opts.OutcomePolicy = preparedBrokerPolicy{}
		broker, err := RecoverFirstNodeBroker(t.Context(), opts)
		require.NoError(t, err)
		waitForFixtureAdmissionDeadline(t, broker.admissionDeadlineUTC)
		require.NoError(t, broker.ExpireUnclaimed(t.Context(), provider.Task))
		require.Equal(t, "assets", broker.tasks["ui:build"].preparedBlockedBy)
		broker.tasks["ui:build"].queueFence = "unsettled-queue"
		var strict *choreographyNoClaimBrokerRefusal
		queueErr := broker.ExpireUnclaimed(t.Context(), "ui:build")
		require.ErrorContains(t, queueErr, choreographyNoClaimQueueReason)
		require.False(t, errors.As(queueErr, &strict))
		broker.tasks["ui:build"].queueFence = ""
		requireNoClaimDisposition(t, broker.ExpireUnclaimed(t.Context(), "ui:build"),
			choreographyNoClaimDispositionPreparedBlocked)
		require.False(t, broker.tasks["ui:build"].terminal)
		page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 4)
		require.NoError(t, err)
		require.Len(t, page.Events, 1)
		require.Equal(t, provider.Task, page.Events[0].Task)
	})
}
