// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	compiledNoticeIdentityError    = "execution: compiled participation notice differs from this node's pinned run"
	compiledNoticeMailboxError     = "execution: compiled participation requires a signed canonical Git mailbox"
	compiledDrainFeedErrorFormat   = "execution: worker drain cannot read its signed run state: %w"
	compiledDrainReplyErrorFormat  = "execution: worker drain has no exact signed receipt: %w"
	compiledJoinKeyError           = "execution: late choreography participation has no pinned node identity key"
	compiledJoinRefusedErrorFormat = "execution: choreography join for node %q was refused: %s"
	compiledEventPageSize          = 32
	compiledBootstrapMode          = "compilation"
	compiledCapacityScopeBudget    = "budget"
	compiledCapacityScopeNode      = "node"
	compiledMissingIdentity        = ""
	compiledNoSignedEvents         = 0
	compiledFirstSelectionIndex    = 0
	compiledSingleHeadEvent        = 1
	compiledFirstRosterIndex       = 0
	compiledFirstParentIndex       = 0
	compiledExactClaimParentCount  = 1
	compiledNoJoinKeyBytes         = 0
	compiledNoPackagePolicies      = 0
	compiledNoLeaseWeight          = 0
	compiledNoSelectionProgress    = false
	compiledSelectionProgress      = true
	compiledJoinPending            = true
	compiledJoinSettled            = false
	compiledDrainAccepted          = true
)

type workerCompiledRun struct {
	binding         ChoreographyRunBinding
	bootstrapOID    string
	rootOID         string
	bootstrap       RunBootstrap
	carrier         ChoreographyGraphCarrier
	channel         *workerReturnChannel
	member          ChoreographyRuntimeNode
	membershipEpoch string
	frontier        *ChoreographyFrontier
	templates       map[string]ChoreographyCompiledTemplate
	pending         map[string]*workerCompiledClaim
	parked          map[string]compiledParkedCapacity
	joinProposal    *ChoreographyJoinProposal
	joining         bool
	retainedFeed    *RetainedChoreographyFeed
	interpreted     *InterpretedRun
	claimRefresh    *compiledClaimRefresh
}

type workerCompiledClaim struct {
	proposal ChoreographyClaimProposal
	lease    NodeSlotLease
	frame    *InterpretedReadyFrame
}

type compiledParkedCapacity struct {
	scope    string
	resource string
}

type workerJoinReturnx interface {
	SubmitJoin(context.Context, ChoreographyJoinProposal) (ChoreographyJoinReceipt, error)
	LookupJoin(context.Context, ChoreographyJoinReceiptQuery) (ChoreographyJoinReceipt, bool, error)
}

type workerDrainReturnx interface {
	SubmitDrain(context.Context, ChoreographyDrainProposal) (ChoreographyDrainReceipt, error)
	LookupDrain(context.Context, ChoreographyDrainReceiptQuery) (ChoreographyDrainReceipt, bool, error)
}

// drainCompiledRuns stops new selection through a signed membership event.
// Accepted physical leases remain held until each Result or Ack settles.
func (w *Worker) drainCompiledRuns(ctx context.Context) error {
	for _, run := range w.compiledRuns {
		if run.joining || run.frontier == nil || run.frontier.stopped || run.frontier.sealed ||
			run.membershipEpoch == compiledMissingIdentity {
			continue
		}
		client, ok := run.channel.client.(workerDrainReturnx)
		if !ok {
			return fmt.Errorf(compiledNoticeIdentityError)
		}
		for {
			page, err := run.channel.client.PollEvents(ctx, ChoreographyEventRequest{
				ChoreographyRunBinding: run.binding, Node: w.Node, MaxEvents: compiledSingleHeadEvent})
			if err != nil {
				retired, retireErr := w.retireSealedCompiledRun(ctx, run)
				if retired || retireErr == nil && (run.frontier.stopped || run.frontier.sealed) {
					break
				}
				return fmt.Errorf(compiledDrainFeedErrorFormat, errors.Join(err, retireErr))
			}
			if page.Head.OID == compiledMissingIdentity {
				return fmt.Errorf(compiledNoticeIdentityError)
			}
			proposal := ChoreographyDrainProposal{ChoreographyRunBinding: run.binding,
				BootstrapOID: run.bootstrapOID, GraphRootOID: run.rootOID, Node: w.Node,
				CapacityAuthorityID: w.Report.AuthorityID,
				MembershipEpochOID:  run.membershipEpoch, ExpectedLedgerOID: page.Head.OID,
				RequestID: returnProposalID(returnProposalIdentity{Kind: choreographyDrainKind,
					Run: run.binding.Run, Task: w.Node, Attempt: minimumCompiledAttempt,
					OID: run.membershipEpoch + page.Head.OID})}
			if len(w.JoinIdentityKey) != compiledNoJoinKeyBytes {
				if err := SignChoreographyDrainProposal(&proposal, w.JoinIdentityKey); err != nil {
					return err
				}
			}
			query, err := FormatChoreographyDrainReceiptQuery(proposal)
			if err != nil {
				return err
			}
			receipt, err := client.SubmitDrain(ctx, proposal)
			if err != nil {
				settled, found, lookupErr := client.LookupDrain(ctx, query)
				if lookupErr != nil || !found {
					retired, retireErr := w.retireSealedCompiledRun(ctx, run)
					if retired || retireErr == nil && (run.frontier.stopped || run.frontier.sealed) {
						break
					}
					return fmt.Errorf(compiledDrainReplyErrorFormat, errors.Join(err, lookupErr, retireErr))
				}
				receipt = settled
			}
			if receipt.RequestDigest != query.RequestDigest ||
				receipt.GraphRootOID != run.rootOID || receipt.Node != w.Node ||
				receipt.ChoreographyRunBinding != run.binding ||
				(!receipt.Accepted && !receipt.Refused) {
				return fmt.Errorf(compiledNoticeIdentityError)
			}
			if receipt.Refused && receipt.Reason == choreographyDrainRefusalStale &&
				receipt.Cursor.OID != compiledMissingIdentity && receipt.Cursor.OID != proposal.ExpectedLedgerOID {
				continue
			}
			if !receipt.Accepted {
				if receipt.Refused && receipt.Reason == choreographyDrainRefusalStopping {
					run.frontier.draining = compiledDrainAccepted
					break
				}
				return fmt.Errorf(compiledNoticeIdentityError)
			}
			run.frontier.draining = compiledDrainAccepted
			break
		}
	}
	return nil
}

func (w *Worker) handleCompiledNotice(ctx context.Context,
	head gitx.RemoteHead, notice SlotNotice) (progress bool, err error) {
	if notice.Protocol != ChoreographyProtocolVersion || notice.Node != w.Node ||
		notice.Branch != head.Name || notice.Kind != KindSlot ||
		notice.Sequence != choreographyActivatedSlotSequence ||
		notice.BootstrapOID == compiledMissingIdentity || notice.GraphRootOID == compiledMissingIdentity || notice.TaskBranch != compiledMissingIdentity ||
		notice.AssignmentOID != compiledMissingIdentity || notice.Run == compiledMissingIdentity {
		return compiledNoSelectionProgress, fmt.Errorf(compiledNoticeIdentityError)
	}
	if prior := w.compiledRuns[notice.Run]; prior != nil {
		if prior.bootstrapOID != notice.BootstrapOID || prior.rootOID != notice.GraphRootOID ||
			prior.binding.Generation != notice.Generation || prior.binding.PlanDigest != notice.PlanDigest {
			return compiledNoSelectionProgress, fmt.Errorf(compiledNoticeIdentityError)
		}
		return compiledNoSelectionProgress, nil
	}
	mailbox, ok := w.Mailbox.(*GitMailbox)
	if !ok {
		return compiledNoSelectionProgress, fmt.Errorf(compiledNoticeMailboxError)
	}
	bootstrap, err := mailbox.ReadChoreographyBootstrap(ctx, notice.BootstrapOID,
		w.Report.Limits.MaxManifestBytes, w.Report.Limits.MaxPackBytes)
	if err != nil {
		return compiledNoSelectionProgress, err
	}
	binding := bootstrap.ChoreographyRunBinding
	if bootstrap.Protocol != ChoreographyProtocolVersion ||
		!validChoreographyMode(bootstrap.Mode) ||
		binding.Run != notice.Run || binding.Generation != notice.Generation ||
		binding.PlanDigest != notice.PlanDigest || bootstrap.CoordinationEndpoint != w.Endpoint ||
		bootstrap.AlgorithmVersion != ChoreographyGraphAlgorithmVersion {
		return compiledNoSelectionProgress, fmt.Errorf(compiledNoticeIdentityError)
	}
	if unavailable := w.compiledUnavailable[notice.Run]; unavailable != nil {
		if unavailable.bootstrapOID != notice.BootstrapOID || unavailable.rootOID != notice.GraphRootOID {
			return compiledNoSelectionProgress, fmt.Errorf(compiledGraphDrainIdentityError)
		}
		if unavailable.drained {
			return compiledNoSelectionProgress, nil
		}
		if unavailable.proposal != nil {
			return w.settleCompiledUnavailableDrain(ctx, unavailable)
		}
	}
	carrier, rootOID, err := mailbox.ReadChoreographyGraphCarrier(ctx, binding,
		notice.BootstrapOID, w.Report.Limits.MaxManifestBytes)
	if err != nil {
		return w.recordCompiledGraphReadFailure(ctx, compiledGraphReadFailure{
			Notice: notice, Bootstrap: bootstrap, ReadErr: err})
	}
	if unavailable := w.compiledUnavailable[notice.Run]; unavailable != nil {
		if unavailable.channel != nil {
			unavailable.channel.client.Close()
		}
		delete(w.compiledUnavailable, notice.Run)
	}
	if rootOID != notice.GraphRootOID || carrier.AlgorithmVersion != bootstrap.AlgorithmVersion ||
		carrier.LogicalDescriptorDigest != binding.GraphDigest {
		return compiledNoSelectionProgress, fmt.Errorf(compiledNoticeIdentityError)
	}
	if err := validateChoreographyHybridPolicy(bootstrap.Mode, &bootstrap.PinnedInputs, carrier); err != nil {
		return compiledNoSelectionProgress, err
	}
	if err := validateChoreographyRetryPolicy(bootstrap.JoinPolicy, carrier.Tasks); err != nil {
		return compiledNoSelectionProgress, err
	}
	_, roster, err := ReadChoreographyLedgerRoot(ctx, mailbox, binding, notice.BootstrapOID)
	if err != nil {
		return compiledNoSelectionProgress, err
	}
	var interpreted *InterpretedRun
	isInstalled := false
	defer func() {
		if !isInstalled && interpreted != nil && interpreted.Close != nil {
			err = errors.Join(err, interpreted.Close())
		}
	}()
	if bootstrap.Mode == bootstrapInterpretationModeName {
		if w.PrepareInterpretedRun == nil {
			return compiledNoSelectionProgress, fmt.Errorf(interpretedRunMissingCallbackError)
		}
		interpreted, err = w.PrepareInterpretedRun(ctx, InterpretedRunOptions{
			Bootstrap: bootstrap, BootstrapOID: notice.BootstrapOID,
			GraphRootOID: rootOID, Roster: roster,
			ObjectStoreDir: mailbox.ObjectStoreDir(), Carrier: carrier})
		if err != nil {
			return compiledNoSelectionProgress, err
		}
		if err := verifyInterpretedRun(bootstrap, carrier, interpreted); err != nil {
			return compiledNoSelectionProgress, err
		}
	}
	allowed, authorized := bootstrap.JoinPolicy.AuthorizedNode(w.Node)
	if !authorized || allowed.CoordinationEndpoint != w.Endpoint ||
		w.NodeSlots == nil || w.Report.AuthorityID == compiledMissingIdentity ||
		w.Report.AuthorityID != w.NodeSlots.AuthorityID() {
		return compiledNoSelectionProgress, fmt.Errorf(compiledNoticeIdentityError)
	}
	memberIndex := slices.IndexFunc(roster.Nodes, func(node ChoreographyRuntimeNode) bool {
		return node.Name == w.Node
	})
	if memberIndex < compiledFirstRosterIndex && (!allowed.LateJoin || len(w.JoinIdentityKey) == compiledNoJoinKeyBytes) {
		return compiledNoSelectionProgress, fmt.Errorf(compiledJoinKeyError)
	}
	channel, err := w.prepareReturnChannel(binding,
		bootstrap.ReturnEndpoint, bootstrap.ReturnCertSHA256)
	if err != nil {
		return compiledNoSelectionProgress, err
	}
	defer func() {
		if !isInstalled {
			w.closeUnrememberedReturnChannel(channel)
		}
	}()
	run := &workerCompiledRun{binding: binding, bootstrapOID: notice.BootstrapOID,
		rootOID: rootOID, bootstrap: bootstrap, carrier: carrier, channel: channel,
		interpreted: interpreted,
		templates:   make(map[string]ChoreographyCompiledTemplate, len(carrier.Templates)),
		pending:     make(map[string]*workerCompiledClaim), parked: make(map[string]compiledParkedCapacity)}
	for _, template := range carrier.Templates {
		run.templates[template.Task] = template
	}
	if memberIndex >= compiledFirstRosterIndex {
		run.member = roster.Nodes[memberIndex]
		if run.member.AuthorityID != w.Report.AuthorityID ||
			run.member.Capacity != w.Report.Capacity ||
			run.member.Platform != w.Report.OS+nodePlatformSeparator+w.Report.Arch {
			return compiledNoSelectionProgress, fmt.Errorf(compiledNoticeIdentityError)
		}
		if err := run.openFrontier(w.Node); err != nil {
			return compiledNoSelectionProgress, err
		}
	} else {
		run.joining = compiledJoinPending
	}
	if w.compiledRuns == nil {
		w.compiledRuns = make(map[string]*workerCompiledRun)
	}
	channel.bootstrapOID, channel.bootstrap = notice.BootstrapOID, &bootstrap
	w.rememberReturnChannel(channel)
	w.compiledRuns[notice.Run] = run
	isInstalled = true
	return compiledSelectionProgress, nil
}

func (r *workerCompiledRun) openFrontier(node string) error {
	specs := r.carrier.Tasks
	if r.interpreted != nil {
		specs = slices.Clone(r.interpreted.Tasks)
		signedByTask := make(map[string]ChoreographyTaskSpec, len(r.carrier.Tasks))
		for _, signed := range r.carrier.Tasks {
			signedByTask[signed.Task] = signed
		}
		for index := range specs {
			signed := signedByTask[specs[index].Task]
			specs[index].Branch = signed.Branch
			specs[index].ReservationOID = signed.ReservationOID
			specs[index].TemplateDigest = signed.TemplateDigest
			specs[index].EligibleNodes = signed.EligibleNodes
		}
	}
	frontier, err := NewCompiledChoreographyFrontier(CompiledChoreographyFrontierOptions{
		Binding:      r.binding,
		GraphRootOID: r.rootOID,
		Node:         r.member,
		Tasks:        specs,
	})
	if err != nil {
		return err
	}
	if r.interpreted != nil && r.interpreted.OutcomePolicy != nil {
		if err := frontier.AttachOutcomePolicy(r.interpreted.OutcomePolicy); err != nil {
			return err
		}
	} else if len(r.carrier.PackagePolicies) != compiledNoPackagePolicies {
		policy, policyErr := NewChoreographyPackageOutcomeEvaluator(r.carrier.PackagePolicies)
		if policyErr != nil {
			return policyErr
		}
		if err := frontier.AttachOutcomePolicy(policy); err != nil {
			return err
		}
	}
	r.frontier = frontier
	return nil
}

func (w *Worker) pollCompiledRuns(ctx context.Context) (bool, error) {
	progress := compiledNoSelectionProgress
	for _, run := range w.compiledRuns {
		if run.joining {
			joined, err := w.joinCompiledRun(ctx, run)
			if err != nil {
				return progress, err
			}
			progress = progress || joined
			if run.joining {
				continue
			}
		}
		changed, err := w.pollCompiledEvents(ctx, run)
		if err != nil {
			return progress, err
		}
		progress = progress || changed
		if w.compiledRuns[run.binding.Run] != run {
			continue
		}
		if run.frontier != nil && run.frontier.sealed {
			retired, retireErr := w.retireSealedCompiledRun(ctx, run)
			if retireErr != nil {
				return progress, retireErr
			}
			progress = progress || retired
			if retired {
				continue
			}
		}
		if run.frontier != nil && run.membershipEpoch != compiledMissingIdentity {
			claimed, claimErr := w.pickCompiledTasksWithRefresh(ctx, run)
			if claimErr != nil {
				return progress, claimErr
			}
			progress = progress || claimed
		}
	}
	return progress, nil
}

func (w *Worker) joinCompiledRun(ctx context.Context, run *workerCompiledRun) (bool, error) {
	client, ok := run.channel.client.(workerJoinReturnx)
	if !ok {
		return compiledNoSelectionProgress, fmt.Errorf(compiledNoticeIdentityError)
	}
	if run.joinProposal == nil {
		page, err := run.channel.client.PollEvents(ctx, ChoreographyEventRequest{
			ChoreographyRunBinding: run.binding, Node: w.Node, MaxEvents: compiledSingleHeadEvent})
		if err != nil {
			return compiledNoSelectionProgress, err
		}
		if page.Head.OID == compiledMissingIdentity {
			return compiledNoSelectionProgress, fmt.Errorf(compiledNoticeIdentityError)
		}
		report := w.Report
		report.Protocol = ChoreographyProtocolVersion
		proposal := ChoreographyJoinProposal{ChoreographyRunBinding: run.binding,
			BootstrapOID: run.bootstrapOID, GraphRootOID: run.rootOID,
			AlgorithmVersion: run.bootstrap.AlgorithmVersion, Node: w.Node,
			Endpoint: w.Endpoint, Report: report, ExpectedLedgerOID: page.Head.OID,
			RequestID: returnProposalID(returnProposalIdentity{Kind: choreographyJoinKind,
				Run: run.binding.Run, Task: w.Node, Attempt: minimumCompiledAttempt,
				OID: run.rootOID + page.Head.OID})}
		if err := SignChoreographyJoinProposal(&proposal, w.JoinIdentityKey); err != nil {
			return compiledNoSelectionProgress, err
		}
		run.joinProposal = &proposal
	}
	query, err := FormatChoreographyJoinReceiptQuery(*run.joinProposal)
	if err != nil {
		return compiledNoSelectionProgress, err
	}
	receipt, err := client.SubmitJoin(ctx, *run.joinProposal)
	if err != nil {
		settled, found, lookupErr := client.LookupJoin(ctx, query)
		if lookupErr != nil {
			return compiledNoSelectionProgress, lookupErr
		}
		if !found {
			return compiledNoSelectionProgress, nil
		}
		receipt = settled
	}
	if receipt.RequestDigest != query.RequestDigest || receipt.GraphRootOID != run.rootOID ||
		receipt.ChoreographyRunBinding != run.binding || receipt.Node != w.Node {
		return compiledNoSelectionProgress, fmt.Errorf(compiledNoticeIdentityError)
	}
	if receipt.Refused {
		if receipt.Reason == choreographyJoinRefusalStale && receipt.Cursor.OID != compiledMissingIdentity &&
			receipt.Cursor.OID != run.joinProposal.ExpectedLedgerOID {
			// The old exact request is definitively fenced. Re-read a fresh
			// signed head on the next bounded poll before signing a new one.
			run.joinProposal = nil
			return compiledSelectionProgress, nil
		}
		return compiledNoSelectionProgress, fmt.Errorf(compiledJoinRefusedErrorFormat, w.Node, receipt.Reason)
	}
	if !receipt.Accepted || receipt.Member.AuthorityID != w.Report.AuthorityID ||
		receipt.Cursor.OID == compiledMissingIdentity {
		return compiledNoSelectionProgress, fmt.Errorf(compiledNoticeIdentityError)
	}
	run.member, run.membershipEpoch = receipt.Member, receipt.Cursor.OID
	if err := run.openFrontier(w.Node); err != nil {
		return compiledNoSelectionProgress, err
	}
	run.joining, run.joinProposal = compiledJoinSettled, nil
	return compiledSelectionProgress, nil
}

func (w *Worker) pollCompiledEvents(ctx context.Context, run *workerCompiledRun) (bool, error) {
	progress := compiledNoSelectionProgress
	for {
		page, err := run.channel.client.PollEvents(ctx, ChoreographyEventRequest{
			ChoreographyRunBinding: run.binding, Node: w.Node,
			Cursor: run.frontier.Cursor(), MaxEvents: compiledEventPageSize})
		if err != nil {
			retired, _ := w.retireSealedCompiledRun(ctx, run)
			if retired {
				return compiledSelectionProgress, nil
			}
			return progress, err
		}
		changed, err := w.applyCompiledEventPage(run, page)
		if err != nil {
			return progress, err
		}
		progress = progress || changed
		if !page.More {
			run.completeClaimRefresh()
			return progress, nil
		}
	}
}

func (w *Worker) applyCompiledEventPage(run *workerCompiledRun, page ChoreographyEventPage) (bool, error) {
	if err := run.verifyClaimRefreshPage(page); err != nil {
		return compiledNoSelectionProgress, err
	}
	for _, event := range page.Events {
		if event.Kind == ChoreographyInitializationComplete && run.membershipEpoch == compiledMissingIdentity {
			run.membershipEpoch = event.LedgerOID
		}
		if event.Node == w.Node &&
			(event.Kind == ChoreographyMemberJoined || event.Kind == ChoreographyMemberCapacityChanged) {
			run.membershipEpoch = event.LedgerOID
		}
		if (event.Kind == ChoreographyTerminal || event.Kind == ChoreographyRetryReady ||
			event.Kind == ChoreographyPickupSettled) && event.Weight > compiledNoLeaseWeight && event.Budget != compiledMissingIdentity {
			for taskID, parked := range run.parked {
				if parked.scope == compiledCapacityScopeBudget && parked.resource == event.Budget ||
					parked.scope == compiledCapacityScopeNode && parked.resource == event.Node {
					if err := run.frontier.RequeueFenced(taskID); err != nil {
						return compiledNoSelectionProgress, err
					}
					delete(run.parked, taskID)
				}
			}
		}
	}
	if err := run.frontier.ApplyEventPage(page); err != nil {
		return compiledNoSelectionProgress, err
	}
	return len(page.Events) != compiledNoSignedEvents, nil
}

func (w *Worker) pickCompiledTasks(ctx context.Context, run *workerCompiledRun) (bool, error) {
	progress := compiledNoSelectionProgress
	for _, pending := range run.pending {
		settled, err := w.settleCompiledClaim(ctx, run, pending)
		if err != nil {
			return progress, err
		}
		progress = progress || settled != compiledClaimUnsettled
	}
	if run.claimRefresh != nil || !w.isStateOwned() || len(w.slots) == cap(w.slots) || run.frontier.stopped || run.frontier.sealed ||
		run.frontier.head == len(run.frontier.ready) {
		return progress, nil
	}
	if err := w.Seen.Persist(); err != nil {
		return progress, err
	}
	limit := len(run.frontier.ready) - run.frontier.head
	for tried := compiledFirstSelectionIndex; tried < limit && len(w.slots) < cap(w.slots); tried++ {
		selected, found, err := run.frontier.SelectReady()
		if err != nil {
			return progress, err
		}
		if !found {
			break
		}
		if run.pending[selected.Spec.Task] != nil || run.parked[selected.Spec.Task].scope != compiledMissingIdentity {
			continue
		}
		inputsDigest, predecessors, tipOID, err := run.frontier.ReadyClaimInputs(selected.Spec.Task)
		if err != nil {
			return progress, err
		}
		identity := ChoreographyTaskBindings{PredecessorResultOIDs: predecessors,
			PreparedInputOID: run.frontier.PreparedInputOID(selected.Spec.Task)}
		frame, err := prepareInterpretedReadyFrame(ctx, interpretedReadyFrameOptions{
			run: run, selected: selected, inputsDigest: inputsDigest, identity: identity})
		if err != nil {
			return progress, err
		}
		bindingsDigest, err := DigestChoreographyTaskBindings(identity)
		if err != nil {
			return progress, err
		}
		intent := NodeSlotIntent{Run: run.binding.Run, Task: selected.Spec.Task,
			Attempt: selected.Attempt, Weight: selected.Spec.Weight,
			PickupID: newChoreographyPickupIDForMode(run.bootstrap.Mode)}
		leaseID := nodeSlotLeaseID(intent)
		proposal := ChoreographyClaimProposal{ChoreographyRunBinding: run.binding,
			Task: selected.Spec.Task, Attempt: selected.Attempt, Node: w.Node,
			PickupID: intent.PickupID,
			Branch:   selected.Spec.Branch, ExpectedOID: tipOID,
			ExpectedLedgerOID: run.frontier.Cursor().OID,
			GraphRootOID:      run.rootOID, MembershipEpochOID: run.membershipEpoch,
			CapacityAuthorityID: w.Report.AuthorityID,
			LeaseIntentID:       leaseID, LeaseWeight: selected.Spec.Weight,
			InputsDigest: inputsDigest, TemplateDigest: selected.Spec.TemplateDigest,
			Bindings: identity, BindingsDigest: bindingsDigest,
			ProposalID: returnProposalID(returnProposalIdentity{Kind: choreographyClaimKind,
				Run: run.binding.Run, Task: selected.Spec.Task, Attempt: selected.Attempt,
				OID: tipOID + run.frontier.Cursor().OID + leaseID})}
		if len(w.JoinIdentityKey) != compiledNoJoinKeyBytes {
			if err := SignChoreographyClaimProposal(&proposal, w.JoinIdentityKey); err != nil {
				return progress, err
			}
		}
		select {
		case w.slots <- struct{}{}:
		default:
			return progress, run.frontier.ReturnUnproposed(selected.Spec.Task)
		}
		placeholder := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
			Kind: selected.Spec.Kind, Run: run.binding.Run,
			Generation: run.binding.Generation, PlanDigest: run.binding.PlanDigest,
			Task: selected.Spec.Task, Attempt: selected.Attempt,
			Node: w.Node, Branch: selected.Spec.Branch},
			Weight: selected.Spec.Weight, BootstrapOID: run.bootstrapOID,
			GraphDigest: run.binding.GraphDigest, GraphRootOID: run.rootOID,
			TemplateDigest: selected.Spec.TemplateDigest}
		lease, available, err := w.reserveNodeSlot(ctx, placeholder, run.channel, proposal)
		if err != nil {
			<-w.slots
			return progress, err
		}
		if !available {
			<-w.slots
			if err := run.frontier.ReturnUnproposed(selected.Spec.Task); err != nil {
				return progress, err
			}
			continue
		}
		if lease.ID != proposal.LeaseIntentID {
			return progress, fmt.Errorf(compiledNoticeIdentityError)
		}
		pending := &workerCompiledClaim{proposal: proposal, lease: lease, frame: frame}
		run.pending[selected.Spec.Task] = pending
		settled, err := w.settleCompiledClaim(ctx, run, pending)
		if err != nil {
			return progress, err
		}
		progress = progress || settled != compiledClaimUnsettled
		if settled == compiledClaimNeedsRefresh {
			return progress, nil
		}
	}
	return progress, nil
}

func (w *Worker) settleCompiledClaim(ctx context.Context, run *workerCompiledRun,
	pending *workerCompiledClaim) (compiledClaimSettlement, error) {
	proposal := pending.proposal
	receipt, settled, err := w.submitReturnClaim(ctx, run.channel, proposal)
	if !settled {
		// An unknown outcome retains the exact proposal and physical lease.
		return compiledClaimUnsettled, err
	}
	if receipt.GraphRootOID != run.rootOID || receipt.Task != proposal.Task ||
		receipt.Node != w.Node || receipt.Branch != proposal.Branch {
		return compiledClaimUnsettled, fmt.Errorf(compiledNoticeIdentityError)
	}
	if receipt.Refused {
		refresh, err := run.prepareClaimRefresh(proposal, receipt)
		if err != nil {
			return compiledClaimUnsettled, err
		}
		if err := w.releaseNodeSlot(ctx, pending.lease, receipt.FenceOID); err != nil {
			return compiledClaimUnsettled, err
		}
		<-w.slots
		delete(run.pending, proposal.Task)
		if receipt.RefusalReason == ChoreographyRefusalStale {
			if err := run.requeueStaleClaim(proposal.Task); err != nil {
				return compiledClaimUnsettled, err
			}
			run.claimRefresh = refresh
			return compiledClaimNeedsRefresh, nil
		} else if receipt.RefusalReason == ChoreographyRefusalCapacity {
			resource := receipt.Budget
			if receipt.CapacityScope == compiledCapacityScopeNode {
				resource = w.Node
			}
			run.parked[proposal.Task] = compiledParkedCapacity{
				scope: receipt.CapacityScope, resource: resource}
		}
		return compiledClaimSettled, nil
	}
	if !receipt.Accepted || receipt.AssignmentOID == compiledMissingIdentity {
		return compiledClaimUnsettled, fmt.Errorf(compiledNoticeIdentityError)
	}
	if run.interpreted != nil && pending.frame == nil {
		return compiledClaimUnsettled, fmt.Errorf(interpretedReadyFrameMissingError)
	}
	mailbox, ok := w.Mailbox.(*GitMailbox)
	if !ok {
		return compiledClaimUnsettled, fmt.Errorf(compiledNoticeMailboxError)
	}
	if err := verifyFetchedCompiledClaim(ctx, mailbox, proposal, receipt); err != nil {
		return compiledClaimUnsettled, err
	}
	assignment, err := mailbox.VerifyAssignmentCarriers(ctx, receipt.AssignmentOID,
		w.Report.Limits.MaxManifestBytes)
	if err != nil {
		return compiledClaimUnsettled, err
	}
	if assignment == nil || assignment.GraphRootOID != run.rootOID ||
		assignment.InputsDigest != proposal.InputsDigest ||
		assignment.BootstrapOID != run.bootstrapOID ||
		assignment.GraphDigest != run.binding.GraphDigest ||
		assignment.Node != w.Node || assignment.Task != proposal.Task ||
		assignment.Attempt != proposal.Attempt || assignment.Branch != proposal.Branch {
		return compiledClaimUnsettled, fmt.Errorf(compiledNoticeIdentityError)
	}
	if err := VerifyChoreographyAssignmentTemplate(run.templates[proposal.Task], *assignment); err != nil {
		return compiledClaimUnsettled, err
	}
	fullBindings := ChoreographyTaskBindings{Repositories: assignment.Repositories,
		Inputs: assignment.Inputs, Exports: assignment.Exports, Env: assignment.Env,
		PredecessorResultOIDs: proposal.Bindings.PredecessorResultOIDs,
		PreparedInputOID:      proposal.Bindings.PreparedInputOID}
	digest, err := DigestChoreographyTaskBindings(fullBindings)
	if err != nil {
		return compiledClaimUnsettled, err
	}
	if digest != assignment.BindingsDigest {
		return compiledClaimUnsettled, fmt.Errorf(compiledNoticeIdentityError)
	}
	if pending.frame != nil {
		interpretedDigest, digestErr := DigestChoreographyTaskBindings(pending.frame.Bindings)
		if digestErr != nil {
			return compiledClaimUnsettled, digestErr
		}
		if interpretedDigest != assignment.BindingsDigest {
			return compiledClaimUnsettled, fmt.Errorf(interpretedReadyFrameMismatchError)
		}
	}
	assignmentTip := ChainTip{OID: receipt.AssignmentOID, Branch: proposal.Branch,
		Kind: MessageAssignment}
	started, done, err := w.startReturnedClaim(returnedClaimStart{
		LookupContext: ctx, TaskContext: ctx, Tip: assignmentTip,
		Assignment: *assignment, ClaimOID: receipt.OID, Held: []NodeSlotLease{pending.lease},
	})
	if done {
		delete(run.pending, proposal.Task)
	}
	if started {
		return compiledClaimSettled, err
	}
	return compiledClaimUnsettled, err
}

// verifyFetchedCompiledClaim imports only the advertised canonical task ref.
// The accepted HTTP receipt names an object that a different Git store does
// not yet have; its signed exact claim must also be on that ref's first-parent
// history before the assignment can be used.
func verifyFetchedCompiledClaim(ctx context.Context, mailbox *GitMailbox,
	proposal ChoreographyClaimProposal, receipt ChoreographyReceipt) error {
	head, err := mailbox.Reread(ctx, proposal.Branch)
	if err != nil {
		return err
	}
	if head.OID == compiledMissingIdentity {
		return &Rejection{Reason: ReasonChain}
	}
	if err := mailbox.remote.ResolveFetchedCommit(ctx,
		gitx.TransportRefPrefix+proposal.Branch, receipt.OID, mailbox.maxDepth); err != nil {
		return &Rejection{Reason: ReasonChain}
	}
	parents, err := mailbox.plumbing.CommitParents(ctx, receipt.OID)
	if err != nil || len(parents) != compiledExactClaimParentCount || parents[compiledFirstParentIndex] != receipt.AssignmentOID {
		return &Rejection{Reason: ReasonChain}
	}
	data, err := readChoreographyFinalityDocument(ctx, choreographyFinalityDocumentQuery{
		Mailbox: mailbox, OID: receipt.OID, Kind: choreographyClaimKind,
		MaxBytes: returnReplyLimit})
	if err != nil {
		return err
	}
	var signed ChoreographyClaimProposal
	if strictJSON(data, &signed) != nil {
		return &Rejection{Reason: ReasonChain}
	}
	digest, err := ChoreographyProposalDigest(signed)
	if err != nil || digest != receipt.ProposalDigest ||
		signed.ChoreographyRunBinding != proposal.ChoreographyRunBinding ||
		signed.GraphRootOID != proposal.GraphRootOID || signed.Task != proposal.Task ||
		signed.Attempt != proposal.Attempt || signed.Node != proposal.Node ||
		signed.Branch != proposal.Branch || signed.ProposalID != proposal.ProposalID ||
		signed.ExpectedOID != proposal.ExpectedOID {
		return &Rejection{Reason: ReasonChain}
	}
	return nil
}
