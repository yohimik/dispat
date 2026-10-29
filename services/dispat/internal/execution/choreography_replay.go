// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"slices"
	"time"
)

const (
	choreographyReplayMissingIdentity             = ""
	choreographyReplayOutcomeFailed               = "failed"
	choreographyReplayOutcomeCancelled            = "cancelled"
	choreographyReplayNoEvents                    = 0
	choreographyReplayNoEdges                     = 0
	choreographyReplayNoWeight                    = 0
	choreographyReplayFirstAttempt                = 1
	choreographyReplayEventPageSize               = 32
	choreographyReplayStarted                     = true
	choreographyReplayStopping                    = true
	choreographyReplayInvalidated                 = true
	choreographyReplayDraining                    = true
	choreographyReplaySignedStop                  = true
	choreographyReplaySealed                      = true
	choreographyReplayReadyFrame                  = true
	choreographyReplayTerminal                    = true
	choreographyReplayPickupSettled               = true
	choreographyReplayPickupContactLost           = true
	choreographyReplayFailed                      = false
	choreographyReplayPending                     = true
	choreographyReplayNotPending                  = false
	choreographyReplayFrameNotReady               = false
	choreographyReplayPageAdvanceError            = "execution: signed ledger replay did not advance"
	choreographyReplayCursorAdvanceError          = "execution: signed ledger moved outside replay cursor"
	choreographyReplayPostSealError               = "execution: signed event follows run seal"
	choreographyReplayInitRootError               = "execution: initialization-complete event names another graph root"
	choreographyReplayInvalidationError           = "execution: invalid signed native input invalidation"
	choreographyReplayInvalidationTaskError       = "execution: invalidation names unknown compiled task"
	choreographyReplayMemberRootError             = "execution: membership event differs from signed complete graph"
	choreographyReplayMemberReplacementError      = "execution: membership join replaces an unresolved authority"
	choreographyReplayMemberAuthorityError        = "execution: membership update names another capacity authority"
	choreographyReplayCapacityError               = "execution: capacity update falls below accepted claims"
	choreographyReplayStopError                   = "execution: malformed run stopping event"
	choreographyReplaySealError                   = "execution: malformed run seal event"
	choreographyReplaySealClaimError              = "execution: sealed run retains an active claim"
	choreographyReplaySealTaskError               = "execution: normal run seal leaves an unfinished task"
	choreographyReplayUnknownTaskError            = "execution: signed event names no indexed task"
	choreographyReplayFrameError                  = "execution: signed ready-frame event differs from replayed task"
	choreographyReplayOfferError                  = "execution: signed assignment violates replayed graph"
	choreographyReplayRefusalError                = "execution: invalid refused claim event"
	choreographyReplayClaimError                  = "execution: signed claim violates replayed graph or capacity"
	choreographyReplayReplyError                  = "execution: signed attempt reply has no active claim"
	choreographyReplayCancelError                 = "execution: signed cancellation differs from active claim"
	choreographyReplayQueueFenceError             = "execution: signed queue fence differs from offered task"
	choreographyReplayLocalOutcomeError           = "execution: invalid signed local authority outcome"
	choreographyReplayDecisionError               = "execution: signed decision differs from active attempt"
	choreographyReplayRetryPackageError           = "execution: retry cannot settle a package"
	choreographyReplayNoClaimDeadlineError        = "execution: no-claim expiry differs from inert deadline task"
	choreographyReplayNoClaimOutcomeError         = "execution: no-claim expiry has another package outcome"
	choreographyReplayNoClaimSettlementError      = "execution: no-claim package settlement lacks pending task failure"
	choreographyReplayTaskPackageSettlementError  = "execution: task package settlement lacks pending exact result finality"
	choreographyReplayBlockedTaskError            = "execution: signed blocked event has no ready task"
	choreographyReplayBlockedOutcomeError         = "execution: no-claim settlement has another causal package outcome"
	choreographyReplayUnsupportedEventErrorFormat = "execution: unsupported signed ledger event %q"
)

// RecoverFirstNodeBroker replays one retained signed ledger before the return
// handler is made available. A restarted first node must verify its run lock
// and retained TLS identity separately before calling this constructor.
func RecoverFirstNodeBroker(ctx context.Context, opts FirstNodeBrokerOptions) (*FirstNodeBroker, error) {
	broker, err := NewFirstNodeBroker(opts)
	if err != nil {
		return nil, err
	}
	cursor := ChoreographyEventCursor{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := opts.Persist.ReadEvents(ctx, cursor, choreographyReplayEventPageSize)
		if err != nil {
			return nil, err
		}
		if page.Head.Sequence < cursor.Sequence ||
			(len(page.Events) == choreographyReplayNoEvents && page.More) ||
			(len(page.Events) > choreographyReplayNoEvents && page.Cursor.Sequence != cursor.Sequence+uint64(len(page.Events))) {
			return nil, fmt.Errorf(choreographyReplayPageAdvanceError)
		}
		for _, event := range page.Events {
			if err := broker.replayEvent(event); err != nil {
				return nil, err
			}
		}
		cursor = page.Cursor
		if page.More {
			continue
		}
		// A late prior transaction may settle while the new first node is
		// replaying. Keep reading until the exact signed head is stable.
		head, err := opts.Persist.LedgerHead(ctx)
		if err != nil {
			return nil, err
		}
		if head == cursor {
			if broker.invalidated && !broker.signedStop {
				if _, err := opts.Persist.StopClaims(ctx); err != nil {
					return nil, err
				}
				continue
			}
			return broker, nil
		}
		if head.Sequence <= cursor.Sequence {
			return nil, fmt.Errorf(choreographyReplayCursorAdvanceError)
		}
	}
}

func (b *FirstNodeBroker) replayEvent(event ChoreographyEvent) error {
	if b.sealed {
		return fmt.Errorf(choreographyReplayPostSealError)
	}
	if event.Kind == ChoreographyInitializationComplete {
		if event.Task != choreographyRunTaskID || event.TaskOID != b.graphRootOID ||
			event.GraphRootOID != b.graphRootOID || event.LedgerOID == choreographyReplayMissingIdentity {
			return fmt.Errorf(choreographyReplayInitRootError)
		}
		return nil
	}
	if event.Kind == ChoreographyExecutionStarted {
		deadline, err := choreographyExecutionDeadline(b.joinPolicy, event.ExecutionStartedUTC)
		if err != nil || b.executionStarted || b.stopping || event.Task != choreographyRunTaskID ||
			event.TaskOID != b.graphRootOID || event.GraphRootOID != b.graphRootOID ||
			event.AdmissionDeadlineUTC != deadline || event.LedgerOID == choreographyReplayMissingIdentity {
			return fmt.Errorf(choreographyInvalidExecutionStartError)
		}
		b.executionStarted, b.admissionDeadlineUTC = choreographyReplayStarted, deadline
		return nil
	}
	if event.Kind == ChoreographyInputInvalidation {
		request := ChoreographyInputInvalidationRequest{ChoreographyRunBinding: b.binding,
			GraphRootOID: event.GraphRootOID, OldPreparedOID: event.OldPreparedOID,
			NewPreparedOID: event.NewPreparedOID, AffectedTasks: event.AffectedTasks}
		if b.stopping || event.Task != choreographyRunTaskID ||
			event.TaskOID != b.graphRootOID || event.LedgerOID == choreographyReplayMissingIdentity ||
			!validChoreographyInputInvalidation(request) ||
			event.GraphRootOID != b.graphRootOID {
			return fmt.Errorf(choreographyReplayInvalidationError)
		}
		for _, task := range event.AffectedTasks {
			if b.tasks[task] == nil {
				return fmt.Errorf(choreographyReplayInvalidationTaskError)
			}
		}
		b.stopping, b.invalidated = choreographyReplayStopping, choreographyReplayInvalidated
		return nil
	}
	if event.Kind == ChoreographyMemberJoined || event.Kind == ChoreographyMemberDraining ||
		event.Kind == ChoreographyMemberCapacityChanged {
		if event.Task != choreographyMembershipTaskID || event.Node == choreographyReplayMissingIdentity ||
			event.Member == nil || event.Member.Name != event.Node ||
			event.GraphRootOID != b.graphRootOID || event.LedgerOID == choreographyReplayMissingIdentity {
			return fmt.Errorf(choreographyReplayMemberRootError)
		}
		if event.Kind == ChoreographyMemberJoined {
			if b.members[event.Node].Name != choreographyReplayMissingIdentity || b.memberEpoch[event.Node] != choreographyReplayMissingIdentity {
				return fmt.Errorf(choreographyReplayMemberReplacementError)
			}
			b.members[event.Node] = *event.Member
			b.nodeCaps[event.Node] = event.Member.Capacity
			b.memberEpoch[event.Node] = event.LedgerOID
		} else if b.members[event.Node].AuthorityID != event.Member.AuthorityID {
			return fmt.Errorf(choreographyReplayMemberAuthorityError)
		} else if event.Kind == ChoreographyMemberDraining {
			b.draining[event.Node] = choreographyReplayDraining
			b.memberEpoch[event.Node] = event.LedgerOID
		} else {
			if event.Member.Capacity < b.nodeUsed[event.Node] {
				return fmt.Errorf(choreographyReplayCapacityError)
			}
			b.members[event.Node] = *event.Member
			b.nodeCaps[event.Node] = event.Member.Capacity
			b.memberEpoch[event.Node] = event.LedgerOID
		}
		return nil
	}
	if event.Kind == ChoreographyRunStopping {
		if b.signedStop || event.Task != choreographyRunTaskID || event.TaskOID == choreographyReplayMissingIdentity {
			return fmt.Errorf(choreographyReplayStopError)
		}
		b.stopping = choreographyReplayStopping
		b.signedStop = choreographyReplaySignedStop
		return nil
	}
	if event.Kind == ChoreographyRunSealed {
		if !b.stopping || event.Task != choreographyRunTaskID ||
			event.TaskOID == choreographyReplayMissingIdentity {
			return fmt.Errorf(choreographyReplaySealError)
		}
		for _, task := range b.tasks {
			if task.claim != choreographyReplayMissingIdentity {
				return fmt.Errorf(choreographyReplaySealClaimError)
			}
			for _, pickup := range task.pickups {
				if !pickup.settled {
					return fmt.Errorf(choreographyReplaySealClaimError)
				}
			}
			if !event.Aborted && !task.terminal {
				return fmt.Errorf(choreographyReplaySealTaskError)
			}
		}
		b.sealed = choreographyReplaySealed
		return nil
	}
	task := b.tasks[event.Task]
	if task == nil || event.TaskOID == choreographyReplayMissingIdentity {
		return fmt.Errorf(choreographyReplayUnknownTaskError)
	}
	if choreographyPickupAdmissionEvent(event.Kind) {
		if err := validateChoreographySignedPickupID(b.joinPolicy, task.spec, event.PickupID); err != nil {
			return err
		}
	}
	switch event.Kind {
	case ChoreographyPickupSettled:
		pickup := task.pickups[event.PickupID]
		if pickup == nil || pickup.settled || pickup.claimOID != event.ClaimOID ||
			pickup.resultReceiptOID == choreographyReplayMissingIdentity ||
			pickup.resultReceiptOID != event.ResultReceiptOID ||
			pickup.resultOutputOID != event.OutputOID ||
			pickup.attempt != event.Attempt || pickup.node != event.Node ||
			event.TaskOID != task.tip || event.PriorTaskOID != task.tip ||
			event.Budget != task.spec.Budget || event.Weight != task.spec.Weight ||
			event.GraphRootOID != b.graphRootOID ||
			!task.terminal && task.claim == pickup.claimOID {
			return fmt.Errorf(choreographyReplayDecisionError)
		}
		b.used[task.spec.Budget] -= task.spec.Weight
		b.nodeUsed[pickup.node] -= task.spec.Weight
		pickup.settled = choreographyReplayPickupSettled
		pickup.settlementDigest = event.ProposalDigest
		if task.claim == pickup.claimOID {
			task.claim, task.cancel, task.result, task.owner =
				choreographyReplayMissingIdentity, choreographyReplayMissingIdentity,
				choreographyReplayMissingIdentity, choreographyReplayMissingIdentity
		}
		return nil
	case ChoreographyPickupContactLost:
		pickup := task.pickups[event.PickupID]
		if pickup == nil || pickup.settled || pickup.contactLost || task.terminal ||
			task.attempt >= task.spec.EffectiveMaxAttempts() ||
			task.spec.ExecutorOwned || !task.spec.RetryAfterUncertainAttempt || task.claim != event.ClaimOID ||
			pickup.claimOID != event.ClaimOID || pickup.attempt != event.Attempt ||
			pickup.node != event.Node || pickup.cancelOID == choreographyReplayMissingIdentity ||
			pickup.cancelOID != task.cancel || pickup.resultReceiptOID != choreographyReplayMissingIdentity ||
			pickup.cancelReplyDeadlineUTC != event.CancelReplyDeadlineUTC ||
			event.CancelReplyDeadlineUTC == choreographyReplayMissingIdentity ||
			event.TaskOID != task.tip || event.PriorTaskOID != task.tip ||
			event.GraphRootOID != b.graphRootOID {
			return fmt.Errorf(choreographyReplayClaimError)
		}
		pickup.contactLost = choreographyReplayPickupContactLost
		task.claim, task.cancel, task.result, task.owner =
			choreographyReplayMissingIdentity, choreographyReplayMissingIdentity,
			choreographyReplayMissingIdentity, choreographyReplayMissingIdentity
		task.attempt++
		return nil
	case ChoreographyFrameReady:
		expectedInputs, expectedErr := b.readyInputsDigest(task)
		if task.spec.Virtual || task.remaining != choreographyReplayNoEdges || task.terminal ||
			event.Attempt != task.attempt ||
			event.TaskOID != task.tip || event.PriorTaskOID != task.tip ||
			event.GraphRootOID != b.graphRootOID || event.InputsDigest == choreographyReplayMissingIdentity ||
			expectedErr != nil || event.InputsDigest != expectedInputs ||
			task.spec.NeedsPreparedInput && event.PreparedInputOID == choreographyReplayMissingIdentity ||
			task.frameReady && task.preparedInputOID != event.PreparedInputOID {
			return fmt.Errorf(choreographyReplayFrameError)
		}
		task.frameReady, task.preparedInputOID = choreographyReplayReadyFrame, event.PreparedInputOID
		return nil
	case ChoreographyAssignmentOffered:
		if task.spec.Virtual || task.spec.LocalAuthority || task.remaining != choreographyReplayNoEdges || task.terminal ||
			task.claim != choreographyReplayMissingIdentity || task.offeredNode != choreographyReplayMissingIdentity || task.attempt != event.Attempt ||
			event.PriorTaskOID != task.tip || event.InputsDigest != CompiledClaimInputDigest(event.TaskOID) ||
			!slices.Contains(task.spec.EligibleNodes, event.Node) ||
			(b.firstNode != choreographyReplayMissingIdentity && (event.Node == b.firstNode) != (event.SlotOID == choreographyReplayMissingIdentity)) ||
			(event.SlotOID == choreographyReplayMissingIdentity) != (event.SlotBranch == choreographyReplayMissingIdentity) {
			return fmt.Errorf(choreographyReplayOfferError)
		}
		task.tip, task.offeredNode, task.inputsDigest = event.TaskOID, event.Node, event.InputsDigest
		return nil
	case ChoreographyClaimRefused:
		if task.spec.Virtual || task.spec.LocalAuthority || event.Attempt < choreographyReplayFirstAttempt ||
			event.ProposalDigest == choreographyReplayMissingIdentity || event.ProposalID == choreographyReplayMissingIdentity || event.RefusalReason == choreographyReplayMissingIdentity ||
			event.GraphRootOID != b.graphRootOID {
			return fmt.Errorf(choreographyReplayRefusalError)
		}
		return nil
	case ChoreographyClaimAccepted:
		if !b.executionStarted || task.spec.Virtual || task.remaining != choreographyReplayNoEdges || task.terminal ||
			task.spec.NeedsPreparedInput && (!task.frameReady || task.preparedInputOID == choreographyReplayMissingIdentity) ||
			task.claim != choreographyReplayMissingIdentity || task.attempt != event.Attempt || event.InputsDigest == choreographyReplayMissingIdentity ||
			(event.PriorTaskOID != choreographyReplayMissingIdentity && event.PriorTaskOID != task.tip) ||
			event.ProposalDigest == choreographyReplayMissingIdentity || event.ProposalID == choreographyReplayMissingIdentity ||
			!b.nodeEligible(task.spec, event.Node) ||
			event.MembershipEpochOID != b.memberEpoch[event.Node] ||
			event.CapacityAuthorityID != b.members[event.Node].AuthorityID ||
			event.LeaseIntentID != nodeSlotLeaseID(NodeSlotIntent{Run: b.binding.Run, Task: event.Task, Attempt: event.Attempt, PickupID: event.PickupID}) ||
			event.LeaseWeight != task.spec.Weight ||
			(event.GraphRootOID != choreographyReplayMissingIdentity && event.GraphRootOID != b.graphRootOID) ||
			(task.offeredNode != choreographyReplayMissingIdentity && task.offeredNode != event.Node) ||
			(task.inputsDigest != choreographyReplayMissingIdentity && task.inputsDigest != event.InputsDigest) ||
			b.used[task.spec.Budget]+task.spec.Weight > b.budgets[task.spec.Budget] ||
			b.nodeUsed[event.Node]+task.spec.Weight > b.nodeCaps[event.Node] {
			return fmt.Errorf(choreographyReplayClaimError)
		}
		task.claim, task.tip, task.owner = event.TaskOID, event.TaskOID, event.Node
		if event.PickupID != choreographyReplayMissingIdentity {
			if task.pickups == nil {
				task.pickups = make(map[string]*indexedChoreographyPickup)
			}
			if task.pickups[event.PickupID] != nil {
				return fmt.Errorf(choreographyReplayClaimError)
			}
			task.pickups[event.PickupID] = &indexedChoreographyPickup{
				id: event.PickupID, attempt: event.Attempt, node: event.Node,
				claimOID: event.TaskOID, assignmentOID: event.AssignmentOID,
				leaseIntentID: event.LeaseIntentID, deadlineUTC: event.PickupDeadlineUTC}
		}
		task.inputsDigest = event.InputsDigest
		b.used[task.spec.Budget] += task.spec.Weight
		b.nodeUsed[event.Node] += task.spec.Weight
		return nil
	case ChoreographyResultAccepted, ChoreographyAckAccepted:
		if event.Kind == ChoreographyAckAccepted && event.PickupID != choreographyReplayMissingIdentity &&
			event.ResultReceiptOID != choreographyReplayMissingIdentity {
			pickup := task.pickups[event.PickupID]
			if pickup == nil || !pickup.contactLost || pickup.cancelOID == choreographyReplayMissingIdentity ||
				pickup.claimOID != event.ClaimOID || pickup.attempt != event.Attempt ||
				pickup.node != event.Node || pickup.resultReceiptOID != choreographyReplayMissingIdentity ||
				event.TaskOID != task.tip || event.PriorTaskOID != task.tip ||
				event.GraphRootOID != b.graphRootOID ||
				event.ProposalDigest == choreographyReplayMissingIdentity ||
				event.ProposalID == choreographyReplayMissingIdentity {
				return fmt.Errorf(choreographyReplayReplyError)
			}
			pickup.resultReceiptOID = event.ResultReceiptOID
			return nil
		}
		if event.Kind == ChoreographyAckAccepted && event.PickupID != choreographyReplayMissingIdentity {
			pickup := task.pickups[event.PickupID]
			if pickup == nil || pickup.claimOID != event.ClaimOID ||
				pickup.attempt != event.Attempt || pickup.node != event.Node ||
				pickup.cancelOID == choreographyReplayMissingIdentity ||
				pickup.cancelOID != event.PriorTaskOID ||
				pickup.resultReceiptOID != choreographyReplayMissingIdentity ||
				task.claim != pickup.claimOID || task.cancel != pickup.cancelOID ||
				event.ProposalDigest == choreographyReplayMissingIdentity ||
				event.ProposalID == choreographyReplayMissingIdentity {
				return fmt.Errorf(choreographyReplayReplyError)
			}
			pickup.resultReceiptOID = event.TaskOID
			task.result, task.tip = event.TaskOID, event.TaskOID
			return nil
		}
		if event.Kind == ChoreographyResultAccepted && event.PickupID != choreographyReplayMissingIdentity {
			pickup := task.pickups[event.PickupID]
			if pickup == nil || pickup.claimOID != event.ClaimOID ||
				pickup.cancelOID != choreographyReplayMissingIdentity ||
				pickup.attempt != event.Attempt || pickup.node != event.Node ||
				pickup.resultReceiptOID != choreographyReplayMissingIdentity ||
				event.TaskOID != task.tip ||
				event.ResultReceiptOID == choreographyReplayMissingIdentity ||
				event.ProposalDigest == choreographyReplayMissingIdentity ||
				event.ProposalID == choreographyReplayMissingIdentity {
				return fmt.Errorf(choreographyReplayReplyError)
			}
			pickup.resultReceiptOID, pickup.resultOutputOID = event.ResultReceiptOID, event.OutputOID
			if task.claim == event.ClaimOID {
				task.result, task.resultOutputOID = event.ResultReceiptOID, event.OutputOID
			}
			return nil
		}
		if task.claim == choreographyReplayMissingIdentity || task.result != choreographyReplayMissingIdentity || task.terminal ||
			task.attempt != event.Attempt || task.owner != event.Node ||
			event.ProposalDigest == choreographyReplayMissingIdentity || event.ProposalID == choreographyReplayMissingIdentity ||
			(event.Kind == ChoreographyResultAccepted && task.cancel != choreographyReplayMissingIdentity) ||
			(event.Kind == ChoreographyAckAccepted && task.cancel == choreographyReplayMissingIdentity) {
			return fmt.Errorf(choreographyReplayReplyError)
		}
		task.result, task.tip, task.resultOutputOID = event.TaskOID, event.TaskOID, event.OutputOID
		return nil
	case ChoreographyCancelIssued:
		if task.claim == choreographyReplayMissingIdentity || task.result != choreographyReplayMissingIdentity || task.cancel != choreographyReplayMissingIdentity ||
			task.terminal || task.attempt != event.Attempt || task.owner != event.Node ||
			task.claim != event.PriorTaskOID || event.ClaimOID != choreographyReplayMissingIdentity &&
			event.ClaimOID != event.PriorTaskOID {
			return fmt.Errorf(choreographyReplayCancelError)
		}
		if event.PickupID != choreographyReplayMissingIdentity {
			if _, err := time.Parse(time.RFC3339, event.CancelReplyDeadlineUTC); err != nil {
				return fmt.Errorf(choreographyReplayCancelError)
			}
		}
		task.cancel, task.tip = event.TaskOID, event.TaskOID
		for _, pickup := range task.pickups {
			if pickup.claimOID == event.PriorTaskOID {
				if event.PickupID != pickup.id || event.ClaimOID != pickup.claimOID {
					return fmt.Errorf(choreographyReplayCancelError)
				}
				pickup.cancelOID = event.TaskOID
				pickup.cancelReplyDeadlineUTC = event.CancelReplyDeadlineUTC
				break
			}
		}
		return nil
	case ChoreographyQueueFenced:
		if task.claim != choreographyReplayMissingIdentity || task.queueFence != choreographyReplayMissingIdentity || task.terminal ||
			task.offeredNode != event.Node || task.attempt != event.Attempt ||
			task.tip != event.PriorTaskOID || event.PriorTaskOID == choreographyReplayMissingIdentity {
			return fmt.Errorf(choreographyReplayQueueFenceError)
		}
		task.queueFence, task.queueOffered, task.tip = event.TaskOID, event.PriorTaskOID, event.TaskOID
		return nil
	case ChoreographyRetryReady, ChoreographyTerminal:
		if event.PickupID != choreographyReplayMissingIdentity {
			pickup := task.pickups[event.PickupID]
			if pickup == nil || pickup.settled || pickup.claimOID != event.ClaimOID ||
				pickup.resultReceiptOID != event.ResultReceiptOID ||
				pickup.resultOutputOID != event.OutputOID ||
				pickup.attempt != event.Attempt || pickup.node != event.Node ||
				event.PriorTaskOID != task.tip || event.Package != task.spec.Package ||
				event.Budget != task.spec.Budget || event.Weight != task.spec.Weight ||
				task.terminal || event.Kind == ChoreographyRetryReady &&
				(event.Succeeded || !task.spec.IsPickupRetryAllowed(pickup.resultOutputOID != choreographyNoAcceptedResultOutputOID) ||
					pickup.attempt >= task.spec.EffectiveMaxAttempts() ||
					event.PackageOutcome != choreographyReplayMissingIdentity) {
				return fmt.Errorf(choreographyReplayDecisionError)
			}
			if err := b.validatePackageOutcome(event.Package, event.PackageOutcome); err != nil {
				return err
			}
			b.used[task.spec.Budget] -= task.spec.Weight
			b.nodeUsed[pickup.node] -= task.spec.Weight
			pickup.settled = choreographyReplayPickupSettled
			task.tip = event.TaskOID
			if task.claim == pickup.claimOID {
				task.claim, task.cancel, task.result, task.owner =
					choreographyReplayMissingIdentity, choreographyReplayMissingIdentity,
					choreographyReplayMissingIdentity, choreographyReplayMissingIdentity
			}
			if event.Kind == ChoreographyRetryReady {
				task.attempt++
				task.resultOutputOID = choreographyNoAcceptedResultOutputOID
				task.frameReady, task.preparedInputOID = choreographyReplayFrameNotReady,
					choreographyReplayMissingIdentity
				return nil
			}
			task.terminal, task.succeeded = choreographyReplayTerminal, event.Succeeded
			task.resultOutputOID = pickup.resultOutputOID
			if err := b.applyPackageOutcome(event.Package, event.PackageOutcome); err != nil {
				return err
			}
			if task.spec.Package != choreographyReplayMissingIdentity &&
				event.PackageOutcome == choreographyReplayMissingIdentity &&
				!task.spec.ExecutorOwned {
				task.pendingPackageSettlement = choreographyReplayPending
			} else {
				b.discharge(task)
			}
			return nil
		}
		if task.spec.LocalAuthority {
			if event.Kind != ChoreographyTerminal || task.remaining != choreographyReplayNoEdges || task.terminal ||
				event.ResultReceiptOID != choreographyReplayMissingIdentity || event.Package != task.spec.Package ||
				task.claim == choreographyReplayMissingIdentity || task.owner != event.Node ||
				event.PriorTaskOID != task.claim || event.GraphRootOID != b.graphRootOID ||
				event.Budget != task.spec.Budget || event.Weight != task.spec.Weight {
				return fmt.Errorf(choreographyReplayLocalOutcomeError)
			}
			task.terminal, task.succeeded, task.tip = choreographyReplayTerminal, event.Succeeded, event.TaskOID
			b.used[task.spec.Budget] -= task.spec.Weight
			b.nodeUsed[task.owner] -= task.spec.Weight
			task.claim, task.owner = choreographyReplayMissingIdentity, choreographyReplayMissingIdentity
			if err := b.validatePackageOutcome(event.Package, event.PackageOutcome); err != nil {
				return err
			}
			if err := b.applyPackageOutcome(event.Package, event.PackageOutcome); err != nil {
				return err
			}
			b.discharge(task)
			return nil
		}
		if task.claim == choreographyReplayMissingIdentity && task.offeredNode != choreographyReplayMissingIdentity && task.result == choreographyReplayMissingIdentity &&
			event.PriorTaskOID == task.tip && event.ResultReceiptOID == choreographyReplayMissingIdentity &&
			event.Attempt == task.attempt && event.Node == task.offeredNode &&
			event.Budget == choreographyReplayMissingIdentity && event.Weight == choreographyReplayNoWeight && !event.Succeeded &&
			event.Package == task.spec.Package &&
			(event.Kind == ChoreographyRetryReady && task.queueFence == choreographyReplayMissingIdentity &&
				event.PackageOutcome == choreographyReplayMissingIdentity ||
				event.Kind == ChoreographyTerminal && task.queueFence == task.tip) {
			if err := b.validatePackageOutcome(event.Package, event.PackageOutcome); err != nil {
				return err
			}
			task.tip, task.offeredNode, task.queueFence, task.queueOffered = event.TaskOID, choreographyReplayMissingIdentity, choreographyReplayMissingIdentity, choreographyReplayMissingIdentity
			if event.Kind == ChoreographyRetryReady {
				task.attempt++
				task.frameReady, task.preparedInputOID = choreographyReplayFrameNotReady, choreographyReplayMissingIdentity
				return nil
			}
			task.terminal = choreographyReplayTerminal
			if err := b.applyPackageOutcome(event.Package, event.PackageOutcome); err != nil {
				return err
			}
			b.discharge(task)
			return nil
		}
		if task.claim == choreographyReplayMissingIdentity || task.result == choreographyReplayMissingIdentity || task.terminal ||
			task.attempt != event.Attempt || task.owner != event.Node ||
			task.result != event.ResultReceiptOID || event.Package != task.spec.Package ||
			event.Budget != task.spec.Budget || event.Weight != task.spec.Weight ||
			event.OutputOID != task.resultOutputOID {
			return fmt.Errorf(choreographyReplayDecisionError)
		}
		if event.Kind == ChoreographyRetryReady && (event.Succeeded || event.PackageOutcome != choreographyReplayMissingIdentity) {
			return fmt.Errorf(choreographyReplayRetryPackageError)
		}
		if err := b.validatePackageOutcome(event.Package, event.PackageOutcome); err != nil {
			return err
		}
		b.used[task.spec.Budget] -= task.spec.Weight
		b.nodeUsed[task.owner] -= task.spec.Weight
		task.claim, task.cancel, task.result, task.owner, task.offeredNode = choreographyReplayMissingIdentity, choreographyReplayMissingIdentity, choreographyReplayMissingIdentity, choreographyReplayMissingIdentity, choreographyReplayMissingIdentity
		task.tip = event.TaskOID
		if event.Kind == ChoreographyRetryReady {
			task.attempt++
			task.resultOutputOID = choreographyNoAcceptedResultOutputOID
			task.frameReady, task.preparedInputOID = choreographyReplayFrameNotReady, choreographyReplayMissingIdentity
			return nil
		}
		task.terminal, task.succeeded = choreographyReplayTerminal, event.Succeeded
		if err := b.applyPackageOutcome(event.Package, event.PackageOutcome); err != nil {
			return err
		}
		if task.spec.Package != choreographyReplayMissingIdentity && event.PackageOutcome == choreographyReplayMissingIdentity &&
			!task.spec.ExecutorOwned &&
			event.ResultReceiptOID != choreographyReplayMissingIdentity {
			task.pendingPackageSettlement = choreographyReplayPending
		} else {
			b.discharge(task)
		}
		return nil
	case ChoreographyTaskPackageSettled:
		if !task.pendingPackageSettlement || !task.terminal || task.spec.Package == choreographyReplayMissingIdentity ||
			event.TaskOID != task.tip || event.PriorTaskOID != task.tip ||
			event.GraphRootOID != b.graphRootOID || event.Package != task.spec.Package ||
			event.Attempt != task.attempt || event.Node != choreographyReplayMissingIdentity ||
			event.Budget != choreographyReplayMissingIdentity || event.Weight != choreographyReplayNoWeight ||
			event.OutputOID != choreographyReplayMissingIdentity || event.ResultReceiptOID != choreographyReplayMissingIdentity {
			return fmt.Errorf(choreographyReplayTaskPackageSettlementError)
		}
		if err := b.validatePackageOutcome(event.Package, event.PackageOutcome); err != nil {
			return err
		}
		if err := b.applyPackageOutcome(event.Package, event.PackageOutcome); err != nil {
			return err
		}
		if event.OutputAdmissionRejected && task.spec.Kind == string(KindPrepare) {
			task.preparedBlockedBy = task.spec.Package
		}
		task.pendingPackageSettlement = choreographyReplayNotPending
		b.discharge(task)
		return nil
	case ChoreographyNoClaimExpired:
		policyDigest, err := digestChoreographyAdmissionPolicy(b.joinPolicy)
		if err != nil {
			return err
		}
		if task.spec.Virtual || !isChoreographyNoClaimPlacement(task.spec.Placement) ||
			task.spec.LocalAuthority ||
			task.remaining != choreographyReplayNoEdges || task.terminal || task.claim != choreographyReplayMissingIdentity ||
			task.offeredNode != choreographyReplayMissingIdentity || task.result != choreographyReplayMissingIdentity ||
			task.attempt != choreographyReplayFirstAttempt ||
			task.tip != task.spec.ReservationOID || event.PriorTaskOID != task.tip ||
			event.GraphRootOID != b.graphRootOID || event.Node != choreographyReplayMissingIdentity ||
			event.Package != task.spec.Package || event.Succeeded ||
			event.BlockedBy != choreographyNoClaimCause(task.spec.Placement) ||
			event.MissingPlacement != string(task.spec.Placement) ||
			event.AdmissionDeadlineUTC != b.admissionDeadlineUTC ||
			event.AdmissionPolicyDigest != policyDigest {
			return fmt.Errorf(choreographyReplayNoClaimDeadlineError)
		}
		if event.PackageOutcome != choreographyReplayMissingIdentity {
			return fmt.Errorf(choreographyReplayNoClaimOutcomeError)
		}
		task.tip, task.terminal, task.succeeded = event.TaskOID, choreographyReplayTerminal, choreographyReplayFailed
		if task.spec.Package != choreographyReplayMissingIdentity && task.spec.Kind != string(KindPrepare) {
			task.pendingNoClaim = choreographyReplayPending
		} else {
			b.discharge(task)
		}
		return nil
	case ChoreographyInertCancelled:
		if !b.stopping || task.spec.Virtual || task.terminal ||
			hasUnsettledChoreographyPickups(task) ||
			task.claim != choreographyReplayMissingIdentity || task.result != choreographyReplayMissingIdentity || task.offeredNode != choreographyReplayMissingIdentity ||
			task.queueFence != choreographyReplayMissingIdentity || event.PriorTaskOID != task.tip ||
			event.GraphRootOID != b.graphRootOID || event.Package != task.spec.Package ||
			event.BlockedBy != choreographyInertCancelCause || event.PackageOutcome != choreographyReplayMissingIdentity ||
			event.Attempt != task.attempt {
			return fmt.Errorf(choreographyInertCancelStateError)
		}
		task.tip, task.terminal, task.succeeded = event.TaskOID, choreographyReplayTerminal, choreographyReplayFailed
		if task.spec.Package != choreographyReplayMissingIdentity && task.spec.Kind != string(KindPrepare) {
			task.pendingInertCancel = choreographyReplayPending
		} else {
			b.discharge(task)
		}
		return nil
	case ChoreographyInertCancelSettled:
		if !task.pendingInertCancel || !task.terminal || task.spec.Package == choreographyReplayMissingIdentity ||
			event.TaskOID != task.tip || event.PriorTaskOID != task.tip ||
			event.GraphRootOID != b.graphRootOID || event.Package != task.spec.Package ||
			event.BlockedBy != choreographyInertCancelCause || event.Attempt != task.attempt ||
			!isValidChoreographyInertCancelOutcome(event.PackageOutcome, b.packageState[event.Package]) {
			return fmt.Errorf(choreographyInertCancelStateError)
		}
		if err := b.validatePackageOutcome(event.Package, event.PackageOutcome); err != nil {
			return err
		}
		if err := b.applyPackageOutcome(event.Package, event.PackageOutcome); err != nil {
			return err
		}
		task.pendingInertCancel = choreographyReplayNotPending
		b.discharge(task)
		return nil
	case ChoreographyNoClaimPackageSettled:
		if !task.pendingNoClaim || !task.terminal || task.spec.Package == choreographyReplayMissingIdentity ||
			event.Attempt != choreographyReplayFirstAttempt ||
			event.TaskOID != task.tip || event.PriorTaskOID != task.tip ||
			event.GraphRootOID != b.graphRootOID || event.Package != task.spec.Package ||
			(event.PackageOutcome != choreographyReplayOutcomeFailed && event.PackageOutcome != choreographyReplayMissingIdentity) ||
			event.BlockedBy != choreographyNoClaimCause(task.spec.Placement) {
			return fmt.Errorf(choreographyReplayNoClaimSettlementError)
		}
		if err := b.validatePackageOutcome(event.Package, event.PackageOutcome); err != nil {
			return err
		}
		if err := b.applyPackageOutcome(event.Package, event.PackageOutcome); err != nil {
			return err
		}
		task.pendingNoClaim = choreographyReplayNotPending
		b.discharge(task)
		return nil
	case ChoreographyBlocked:
		if task.spec.Virtual || task.remaining != choreographyReplayNoEdges || task.terminal ||
			task.claim != choreographyReplayMissingIdentity ||
			event.Package != task.spec.Package || event.BlockedBy == choreographyReplayMissingIdentity ||
			event.PriorTaskOID != task.tip {
			return fmt.Errorf(choreographyReplayBlockedTaskError)
		}
		if event.PackageOutcome == choreographyReplayOutcomeFailed && b.packageState[event.Package] != choreographyReplayMissingIdentity ||
			event.PackageOutcome == choreographyReplayMissingIdentity && b.packageState[event.Package] != choreographyReplayOutcomeFailed {
			return fmt.Errorf(choreographyReplayBlockedOutcomeError)
		}
		if err := b.validatePackageOutcome(event.Package, event.PackageOutcome); err != nil {
			return err
		}
		task.terminal = choreographyReplayTerminal
		task.tip = event.TaskOID
		if err := b.applyPackageOutcome(event.Package, event.PackageOutcome); err != nil {
			return err
		}
		b.discharge(task)
		return nil
	default:
		return fmt.Errorf(choreographyReplayUnsupportedEventErrorFormat, event.Kind)
	}
}
