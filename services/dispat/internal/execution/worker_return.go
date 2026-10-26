// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// A worker returns exact proposals to the first node. An HTTP error
// does not answer whether a request was accepted: only a matching, signed
// durable receipt permits the task to start or its local slot to be reused.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

var errResultWithdrawn = errors.New(workerReturnWithdrawnError)

type pendingReturnClaim struct {
	tip        ChainTip
	assignment Assignment
	proposal   ChoreographyClaimProposal
	lease      NodeSlotLease
}

// The first-node direct adapter can wait on one indexed task; remote peers
// continue consuming the authenticated run feed for their whole frontier.
type workerTaskEventWaiter interface {
	WaitTaskEvent(context.Context, string, ChoreographyEventCursor) (ChoreographyEvent, ChoreographyEventCursor, error)
}

func (w *Worker) reserveNodeSlot(ctx context.Context, assignment Assignment,
	channel *workerReturnChannel, proposal ChoreographyClaimProposal) (NodeSlotLease, bool, error) {
	if assignment.Protocol != ChoreographyProtocolVersion {
		return NodeSlotLease{}, workerReturnLegacyNoSlot, nil
	}
	if w.NodeSlots == nil || w.Report.AuthorityID == workerReturnNoIdentity ||
		w.Report.AuthorityID != w.NodeSlots.AuthorityID() || assignment.Weight < workerReturnMinimumWeight {
		return NodeSlotLease{}, workerReturnSlotUnavailable, fmt.Errorf(workerReturnAuthorityError)
	}
	if channel == nil || channel.binding != proposal.ChoreographyRunBinding ||
		assignment.BootstrapOID == workerReturnNoIdentity || w.Endpoint == workerReturnNoIdentity ||
		proposal.ExpectedOID == workerReturnNoIdentity || proposal.ExpectedLedgerOID == workerReturnNoIdentity ||
		proposal.Branch != assignment.Branch || proposal.Task != assignment.Task ||
		proposal.Attempt != assignment.Attempt || proposal.Node != w.Node ||
		(channel.bootstrap != nil && (channel.bootstrap.CoordinationEndpoint != w.Endpoint ||
			channel.bootstrapOID != assignment.BootstrapOID)) {
		return NodeSlotLease{}, workerReturnSlotUnavailable, fmt.Errorf(workerReturnRecoveryIdentityError)
	}
	query, err := FormatClaimReceiptQuery(proposal)
	if err != nil {
		return NodeSlotLease{}, workerReturnSlotUnavailable, err
	}
	locator := &NodeSlotRecoveryLocator{Binding: channel.binding,
		CanonicalEndpoint: w.Endpoint, BootstrapOID: assignment.BootstrapOID,
		TaskBranch: assignment.Branch, Kind: assignment.Kind,
		ExpectedLedgerOID: proposal.ExpectedLedgerOID, ProposalDigest: query.ProposalDigest,
		Node: w.Node}
	if proposal.GraphRootOID == workerReturnNoIdentity {
		locator.AssignmentOID = proposal.ExpectedOID
	} else {
		locator.ExpectedTaskOID = proposal.ExpectedOID
		locator.GraphRootOID = proposal.GraphRootOID
		locator.TemplateDigest = proposal.TemplateDigest
	}
	return w.NodeSlots.TryReserve(ctx, NodeSlotIntent{Run: assignment.Run,
		Task: assignment.Task, Attempt: assignment.Attempt, Weight: assignment.Weight,
		PickupID: proposal.PickupID, Recovery: locator})
}

func (w *Worker) releaseNodeSlot(ctx context.Context, lease NodeSlotLease, evidenceOID string) error {
	if lease.ID == workerReturnNoIdentity {
		return nil
	}
	if w.NodeSlots == nil {
		return fmt.Errorf(workerReturnLostAuthorityError)
	}
	return w.NodeSlots.ReleaseSettled(ctx, lease.ID, evidenceOID)
}

type parkedReturnClaim struct {
	assignment Assignment
	reason     string
	scope      string
	budget     string
	fence      uint64
}

type parkedReturnRun struct {
	channel  *workerReturnChannel
	cursor   ChoreographyEventCursor
	entries  map[string]*parkedReturnClaim
	byBudget map[string]map[string]bool
	byNode   map[string]map[string]bool
	byTask   map[string]map[string]bool
	notReady map[string]bool
}

func addParked(index map[string]map[string]bool, key, branch string) {
	if index[key] == nil {
		index[key] = make(map[string]bool)
	}
	index[key][branch] = workerReturnParked
}

// parkRefusedClaim releases only the local slot. A capacity refusal is
// reconsidered after an exact signed release of that node or budget, never
// merely because the next mailbox polling tick arrived.
type parkedClaimRequest struct {
	Channel    *workerReturnChannel
	Branch     string
	Assignment Assignment
	Receipt    ChoreographyReceipt
}

func (w *Worker) parkRefusedClaim(request parkedClaimRequest) {
	channel, branch, assignment, receipt := request.Channel, request.Branch, request.Assignment, request.Receipt
	switch receipt.RefusalReason {
	case ChoreographyRefusalStale:
		// The signed newer epoch fences this proposal. One reread may form a
		// new exact proposal; another stale response needs another new epoch.
		w.reconsiderOfferedTask(branch)
		return
	case ChoreographyRefusalTerminal, ChoreographyRefusalStopping, ChoreographyRefusalSealed:
		return
	}
	if w.parkedReturn == nil {
		w.parkedReturn = make(map[string]*parkedReturnRun)
	}
	run := w.parkedReturn[assignment.Run]
	if run == nil {
		run = &parkedReturnRun{channel: channel,
			cursor:   ChoreographyEventCursor{OID: receipt.FenceOID, Sequence: receipt.FenceSequence},
			entries:  make(map[string]*parkedReturnClaim),
			byBudget: make(map[string]map[string]bool), byNode: make(map[string]map[string]bool),
			byTask: make(map[string]map[string]bool), notReady: make(map[string]bool)}
		w.parkedReturn[assignment.Run] = run
	}
	if run.cursor.Sequence > receipt.FenceSequence {
		// The already verified cursor is newer than the refusal fence. This
		// one reread is bounded by a real ledger transition, not by polling.
		w.reconsiderOfferedTask(branch)
		return
	}
	entry := &parkedReturnClaim{assignment: assignment, reason: receipt.RefusalReason,
		scope: receipt.CapacityScope, budget: receipt.Budget, fence: receipt.FenceSequence}
	run.entries[branch] = entry
	switch entry.reason {
	case ChoreographyRefusalCapacity:
		if entry.scope == compiledCapacityScopeNode {
			addParked(run.byNode, w.Node, branch)
		} else {
			addParked(run.byBudget, entry.budget, branch)
		}
	case ChoreographyRefusalBusy:
		addParked(run.byTask, assignment.Task, branch)
	case ChoreographyRefusalNotReady:
		run.notReady[branch] = workerReturnParked
	}
}

func (w *Worker) wakeParked(run *parkedReturnRun, branch string, sequence uint64) bool {
	entry := run.entries[branch]
	if entry == nil || sequence <= entry.fence {
		return workerReturnParkedNotWoken
	}
	delete(run.entries, branch)
	delete(run.notReady, branch)
	delete(run.byBudget[entry.budget], branch)
	delete(run.byNode[w.Node], branch)
	delete(run.byTask[entry.assignment.Task], branch)
	w.reconsiderOfferedTask(branch)
	delete(w.slotForTask, branch)
	return workerReturnParkedWoken
}

// pollParkedReturnClaims reads one signed event page per run. It indexes
// waiting branches by the resource or task whose durable transition can
// change their answer; idle ticks perform no Git writes or new proposals.
func (w *Worker) pollParkedReturnClaims(ctx context.Context) bool {
	progress := workerReturnNoProgress
	for name, run := range w.parkedReturn {
		if len(run.entries) == workerReturnNoEntries {
			delete(w.parkedReturn, name)
			continue
		}
		pollCtx, cancel := context.WithTimeout(ctx, taskReportTimeout)
		page, err := run.channel.client.PollEvents(pollCtx, ChoreographyEventRequest{
			ChoreographyRunBinding: run.channel.binding, Node: w.Node,
			Cursor: run.cursor, MaxEvents: workerReturnEventPageSize})
		cancel()
		if err != nil {
			w.Log.Debug().Err(err).Str(workerReturnLogRunField, name).Msg(workerReturnParkedAwaitMessage)
			continue
		}
		if page.Cursor.OID == workerReturnNoIdentity || page.Cursor.Sequence != run.cursor.Sequence+uint64(len(page.Events)) ||
			(len(page.Events) == workerReturnNoEntries && page.Cursor.OID != run.cursor.OID) {
			w.Log.Error().Str(workerReturnLogRunField, name).Msg(workerReturnParkedPageErrorMessage)
			continue
		}
		valid := workerReturnPageValid
		for index, event := range page.Events {
			if event.Sequence != run.cursor.Sequence+uint64(index)+workerReturnFirstEventSequence {
				valid = workerReturnPageInvalid
				break
			}
		}
		if !valid {
			w.Log.Error().Str(workerReturnLogRunField, name).Msg(workerReturnParkedSequenceErrorMessage)
			continue
		}
		for _, event := range page.Events {
			if event.Kind == ChoreographyRunStopping || event.Kind == ChoreographyRunSealed {
				for branch := range run.entries {
					delete(w.slotForTask, branch)
				}
				delete(w.parkedReturn, name)
				progress = workerReturnProgress
				break
			}
			if event.Kind != ChoreographyTerminal && event.Kind != ChoreographyRetryReady &&
				event.Kind != ChoreographyPickupSettled && event.Kind != ChoreographyBlocked {
				continue
			}
			if event.Kind != ChoreographyBlocked && event.Weight > workerReturnNoPackBudget {
				for branch := range run.byBudget[event.Budget] {
					progress = w.wakeParked(run, branch, event.Sequence) || progress
				}
				for branch := range run.byNode[event.Node] {
					progress = w.wakeParked(run, branch, event.Sequence) || progress
				}
			}
			for branch := range run.byTask[event.Task] {
				progress = w.wakeParked(run, branch, event.Sequence) || progress
			}
			for branch := range run.notReady {
				progress = w.wakeParked(run, branch, event.Sequence) || progress
			}
		}
		if w.parkedReturn[name] == run {
			run.cursor = page.Cursor
		}
	}
	return progress
}

type returnProposalIdentity struct {
	Kind    MessageKind
	Run     string
	Task    string
	Attempt int
	OID     string
}

func returnProposalID(identity returnProposalIdentity) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf(workerReturnProposalDigestFormat,
		identity.Kind, identity.Run, identity.Task, identity.Attempt, identity.OID)))
	return hex.EncodeToString(sum[:])
}

type returnClaimRequest struct {
	Binding    ChoreographyRunBinding
	LedgerOID  string
	Tip        ChainTip
	Assignment Assignment
}

func (w *Worker) formatReturnClaim(request returnClaimRequest) ChoreographyClaimProposal {
	binding, ledgerOID, tip, assignment := request.Binding, request.LedgerOID, request.Tip, request.Assignment
	return ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: assignment.Task, Attempt: assignment.Attempt, Node: w.Node,
		Branch: tip.Branch, ExpectedOID: tip.OID, ExpectedLedgerOID: ledgerOID,
		InputsDigest: CompiledClaimInputDigest(tip.OID),
		ProposalID: returnProposalID(returnProposalIdentity{Kind: choreographyClaimKind, Run: assignment.Run,
			Task: assignment.Task, Attempt: assignment.Attempt,
			OID: tip.OID + workerReturnProposalPartSeparator + ledgerOID})}
}

// submitReturnClaim first asks the first node to persist the exact claim.
// An unanswered request is queried by its full body digest. A missing receipt
// at one read is unsettled, because the original request may still arrive.
func (w *Worker) submitReturnClaim(ctx context.Context, channel *workerReturnChannel,
	proposal ChoreographyClaimProposal) (ChoreographyReceipt, bool, error) {
	receipt, err := channel.client.SubmitClaim(ctx, proposal)
	if err != nil {
		query, queryErr := FormatClaimReceiptQuery(proposal)
		if queryErr != nil {
			return ChoreographyReceipt{}, workerReturnReceiptUnverified, queryErr
		}
		settled, found, lookupErr := channel.client.LookupReceipt(ctx, query)
		if lookupErr == nil && found {
			receipt, err = settled, nil
		} else {
			if lookupErr != nil {
				return ChoreographyReceipt{}, workerReturnReceiptUnverified,
					fmt.Errorf(workerReturnClaimLookupErrorFormat, lookupErr)
			}
			return ChoreographyReceipt{}, workerReturnReceiptUnverified, fmt.Errorf(workerReturnClaimUnsettledErrorFormat, err)
		}
	}
	query, err := FormatClaimReceiptQuery(proposal)
	if err != nil {
		return ChoreographyReceipt{}, workerReturnReceiptUnverified, err
	}
	invalid := workerReturnNoReason
	switch {
	case receipt.OID == workerReturnNoIdentity:
		invalid = workerReturnClaimMissingOIDReason
	case receipt.ChoreographyRunBinding != proposal.ChoreographyRunBinding:
		invalid = workerReturnClaimWrongRunReason
	case receipt.Kind != choreographyClaimKind || receipt.Task != proposal.Task ||
		receipt.Attempt != proposal.Attempt || receipt.Node != proposal.Node ||
		receipt.Branch != proposal.Branch || receipt.ProposalID != proposal.ProposalID ||
		receipt.ProposalDigest != query.ProposalDigest:
		invalid = workerReturnClaimWrongIdentityReason
	case !receipt.Accepted && !receipt.Refused || receipt.Accepted && receipt.Refused:
		invalid = workerReturnClaimWrongStateReason
	case receipt.Accepted && receipt.OID == proposal.ExpectedOID:
		invalid = workerReturnClaimUnadvancedReason
	case receipt.Accepted && receipt.FenceOID != workerReturnNoIdentity:
		invalid = workerReturnClaimFenceReason
	case receipt.Refused && receipt.Terminal:
		invalid = workerReturnRefusalTerminalReason
	case receipt.Refused && receipt.OID != proposal.ExpectedOID:
		invalid = workerReturnRefusalWrongOIDReason
	case receipt.Refused && receipt.FenceOID == workerReturnNoIdentity:
		invalid = workerReturnRefusalNoFenceReason
	case receipt.Refused && receipt.FenceOID == proposal.ExpectedLedgerOID:
		invalid = workerReturnRefusalUnadvancedReason
	}
	if invalid != workerReturnNoIdentity {
		return ChoreographyReceipt{}, workerReturnReceiptUnverified,
			fmt.Errorf(workerReturnClaimIdentityErrorFormat, invalid)
	}
	if receipt.Refused {
		switch receipt.RefusalReason {
		case ChoreographyRefusalCapacity:
			if (receipt.CapacityScope != compiledCapacityScopeNode &&
				receipt.CapacityScope != compiledCapacityScopeBudget) || receipt.Budget == workerReturnNoIdentity {
				return ChoreographyReceipt{}, workerReturnReceiptUnverified,
					fmt.Errorf(workerReturnCapacityResourceError)
			}
		case ChoreographyRefusalBusy, ChoreographyRefusalNotReady, ChoreographyRefusalStale,
			ChoreographyRefusalTerminal, ChoreographyRefusalStopping, ChoreographyRefusalSealed:
			if receipt.CapacityScope != workerReturnNoCapacityScope {
				return ChoreographyReceipt{}, workerReturnReceiptUnverified,
					fmt.Errorf(workerReturnCapacityScopeError)
			}
		default:
			return ChoreographyReceipt{}, workerReturnReceiptUnverified, fmt.Errorf(workerReturnRefusalReasonError)
		}
	}
	return receipt, workerReturnReceiptVerified, nil
}

// settlePendingReturnClaims retries only the same proposal while its slot is
// fenced. A different candidate cannot consume that slot on a transient 404.
func (w *Worker) settlePendingReturnClaims(ctx context.Context) bool {
	progress := workerReturnNoProgress
	for branch, pending := range w.pendingReturn {
		channel, err := w.returnChannelFor(pending.assignment)
		if err != nil {
			w.Log.Error().Err(err).Str(workerReturnLogBranchField, branch).Msg(workerReturnPendingChannelErrorMessage)
			continue
		}
		claimCtx, stopClaim := context.WithTimeout(ctx, taskReportTimeout)
		receipt, settled, err := w.submitReturnClaim(claimCtx, channel, pending.proposal)
		if !settled {
			stopClaim()
			w.Log.Debug().Err(err).Str(workerReturnLogBranchField, branch).Msg(workerReturnPendingUnsettledMessage)
			continue
		}
		if receipt.Refused {
			stopClaim()
			settleCtx, done := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
			err := w.releaseNodeSlot(settleCtx, pending.lease, receipt.FenceOID)
			done()
			if err != nil {
				w.Log.Error().Err(err).Str(workerReturnLogBranchField, branch).Msg(workerReturnRefusalSlotErrorMessage)
				continue
			}
			delete(w.pendingReturn, branch)
			<-w.slots
			w.parkRefusedClaim(parkedClaimRequest{Channel: channel, Branch: branch,
				Assignment: pending.assignment, Receipt: receipt})
			if run := w.parkedReturn[pending.assignment.Run]; run == nil || run.entries[branch] == nil {
				delete(w.slotForTask, branch)
			}
			progress = workerReturnProgress
			continue
		}
		answered, done, err := w.startReturnedClaim(returnedClaimStart{
			LookupContext: claimCtx, TaskContext: ctx, Tip: pending.tip,
			Assignment: pending.assignment, ClaimOID: receipt.OID,
			Held: []NodeSlotLease{pending.lease},
		})
		stopClaim()
		if !done {
			w.Log.Debug().Err(err).Str(workerReturnLogBranchField, branch).Msg(workerReturnClaimAwaitTaskMessage)
			continue
		}
		delete(w.pendingReturn, branch)
		delete(w.slotForTask, branch)
		if err != nil {
			w.Log.Error().Err(err).Str(workerReturnLogBranchField, branch).Msg(workerReturnClaimStartErrorMessage)
		}
		progress = progress || answered
	}
	return progress
}

// startReturnedClaim checks the current exact task ref before any script. A
// Cancel observed before the accepted claim receipt must be acknowledged
// without starting work; a lagging or unrelated ref leaves the slot fenced.
type returnedClaimStart struct {
	LookupContext context.Context
	TaskContext   context.Context
	Tip           ChainTip
	Assignment    Assignment
	ClaimOID      string
	Held          []NodeSlotLease
}

func (w *Worker) startReturnedClaim(request returnedClaimStart) (bool, bool, error) {
	ctx, taskCtx := request.LookupContext, request.TaskContext
	tip, assignment, claim, held := request.Tip, request.Assignment, request.ClaimOID, request.Held
	head, err := w.Mailbox.Reread(ctx, tip.Branch)
	if err != nil || head.OID == workerReturnNoIdentity {
		return workerReturnClaimNotAnswered, workerReturnClaimStateUnknown, err
	}
	if head.OID == claim {
		if assignment.GraphRootOID != workerReturnNoIdentity {
			pickup := &claimedTask{assignment: assignment, tip: tip, claimed: claim}
			if len(held) != workerReturnNoEntries {
				pickup.slotLease = held[workerReturnFirstHeldLeaseIndex]
			}
			if _, err := w.ensurePickupPending(ctx, pickup); err != nil {
				return workerReturnClaimNotAnswered, workerReturnClaimStateUnknown, err
			}
			// Publishing the pending ref may outlast a concurrent Cancel. Check
			// authority again after that remote write and before starting work.
			head, err = w.Mailbox.Reread(ctx, tip.Branch)
			if err != nil || head.OID == workerReturnNoIdentity {
				return workerReturnClaimNotAnswered, workerReturnClaimStateUnknown, err
			}
		}
	}
	if head.OID == claim {
		// The bounded claim lookup ends when this function returns. The
		// command must inherit the serving run's context instead, or the
		// successful claim cancels its own materialization immediately.
		started, err := w.startClaimedTask(taskCtx, tip, assignment, claim, held...)
		if err != nil {
			return started, workerReturnClaimStateUnknown, err
		}
		return started, workerReturnClaimStateHandled, err
	}
	ackTask := &claimedTask{assignment: assignment, tip: tip, claimed: claim,
		finalityCtx: taskCtx}
	if len(held) != workerReturnNoEntries {
		ackTask.slotLease = held[workerReturnFirstHeldLeaseIndex]
	}
	answer, err := w.Mailbox.Inspect(ctx, head)
	if err != nil || answer.Kind != MessageCancel || answer.PreviousOID != claim {
		answer, err = w.readRetainedPickupCancellation(ctx, ackTask)
	}
	if err != nil || answer.Kind != MessageCancel || answer.PreviousOID != claim {
		return workerReturnClaimNotAnswered, workerReturnClaimStateUnknown, err
	}
	if reason := w.checkWithdrawal(ctx, answer, tip, claim, assignment); reason != workerReturnNoIdentity {
		w.reportRejection(answer, reason)
		return workerReturnClaimNotAnswered, workerReturnClaimStateUnknown, fmt.Errorf(workerReturnWithdrawalErrorFormat, reason)
	}
	if _, err := w.returnTaskAck(ctx, taskAckReturnRequest{Task: ackTask,
		CancelOID: answer.OID, Phase: release.PartInputs,
		CommandStarted: workerReturnUnconfirmedCommandStart}); err != nil {
		return workerReturnClaimNotAnswered, workerReturnClaimStateUnknown, err
	}
	if err := w.Seen.Record(assignment.Run, assignment.Task, assignment.Attempt, time.Now()); err != nil {
		return workerReturnClaimNotAnswered, workerReturnClaimStateUnknown, err
	}
	<-w.slots
	return workerReturnClaimAnswered, workerReturnClaimStateHandled, nil
}

type boundedResultPackWriter struct {
	to        io.Writer
	remaining int64
}

func (w *boundedResultPackWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return workerReturnNoBytes, gitx.ErrTransportLimit
	}
	n, err := w.to.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// returnTaskResult durably publishes the pickup outcome before submitting it
// for canonical admission. A lost reply retains the same signed result.
func (w *Worker) returnTaskResult(ctx context.Context, task *claimedTask, report Result,
	carried []gitx.TreeEntry) (string, error) {
	channel, err := w.returnChannelFor(task.assignment)
	if err != nil {
		return workerReturnNoIdentity, err
	}
	maxPack := min(w.Report.Limits.MaxPackBytes, task.assignment.Limits.MaxPackBytes)
	if w.ReturnSigner == nil || w.Cache == nil || maxPack <= workerReturnNoPackBudget {
		return workerReturnNoIdentity, fmt.Errorf(workerReturnPackUnavailableError)
	}
	report.Assignment = task.claimed
	report, resultOID, err := w.prepareTaskResultCommit(ctx,
		pickupOutcomeRequest{Task: task, Report: report, Carried: carried})
	if err != nil {
		return workerReturnNoIdentity, err
	}
	payload, err := os.CreateTemp(w.StateDir, workerReturnResultPackFilePattern)
	if err != nil {
		return workerReturnNoIdentity, fmt.Errorf(workerReturnOpenPackErrorFormat, err)
	}
	defer func() {
		_ = payload.Close()
		_ = os.Remove(payload.Name())
	}()
	writer := &boundedResultPackWriter{to: payload, remaining: maxPack}
	if report.PendingOID != workerReturnNoIdentity {
		err = w.Cache.ExportCommitPackSince(ctx, gitx.CommitPackDeltaOptions{
			CommitOID: resultOID, BaseOID: task.claimed, Writer: writer})
	} else {
		err = w.Cache.ExportCommitPack(ctx, resultOID, writer)
	}
	if err != nil {
		return workerReturnNoIdentity, err
	}
	inputsDigest := task.assignment.InputsDigest
	if inputsDigest == workerReturnNoIdentity {
		inputsDigest = CompiledClaimInputDigest(task.tip.OID)
	}
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: channel.binding,
		Task: task.assignment.Task, Attempt: task.assignment.Attempt, Node: w.Node,
		Branch: task.tip.Branch, ClaimOID: task.claimed,
		InputsDigest: inputsDigest, ResultDigest: resultOID,
		PickupID: report.PickupID, PendingBranch: report.PendingBranch,
		PendingOID: report.PendingOID, OutcomeBranch: report.OutcomeBranch,
		ProposalID: returnProposalID(returnProposalIdentity{Kind: choreographyResultKind,
			Run: task.assignment.Run, Task: task.assignment.Task,
			Attempt: task.assignment.Attempt, OID: resultOID})}
	prepared, err := PrepareChoreographyResult(ctx, proposal, payload, maxPack)
	if err != nil {
		return workerReturnNoIdentity, err
	}
	query, err := FormatResultReceiptQuery(prepared)
	if err != nil {
		return workerReturnNoIdentity, err
	}
	var receipt ChoreographyReceipt
	for {
		receipt, err = channel.client.SubmitResult(ctx, prepared, payload)
		if err == nil {
			break
		}
		settled, found, lookupErr := channel.client.LookupReceipt(ctx, query)
		if lookupErr == nil && found {
			receipt = settled
			break
		}
		// A rejected Result may have lost the exact task-ref race to Cancel.
		// The command has already stopped, so acknowledge that signed Cancel
		// while this report budget is still live. A bare HTTP error or 404 is
		// never itself a cancellation or a reason to release the slot.
		if ctx.Err() == nil && w.acknowledgeResultWithdrawal(ctx, task, task.claimed, w.Log) {
			return workerReturnNoIdentity, errResultWithdrawn
		}
		// Retain the same prepared proposal and pack. Replaying that exact
		// body is idempotent at the first node; changing it after one 404 is not.
		select {
		case <-ctx.Done():
			return workerReturnNoIdentity, fmt.Errorf(workerReturnResultUnsettledErrorFormat, ctx.Err())
		case <-time.After(minimumPollInterval):
		}
	}
	if !receipt.Accepted || receipt.Terminal || receipt.OID == workerReturnNoIdentity ||
		receipt.ChoreographyRunBinding != prepared.ChoreographyRunBinding ||
		receipt.Kind != choreographyResultKind || receipt.Task != prepared.Task ||
		receipt.Attempt != prepared.Attempt || receipt.Node != prepared.Node ||
		receipt.Branch != prepared.Branch || receipt.ProposalID != prepared.ProposalID ||
		receipt.ProposalDigest != query.ProposalDigest {
		return workerReturnNoIdentity, fmt.Errorf(workerReturnResultReceiptError)
	}
	if err := w.awaitNodeSlotDecision(ctx, task, channel, receipt); err != nil {
		return workerReturnNoIdentity, err
	}
	return receipt.OID, nil
}

func (w *Worker) prepareTaskResultCommit(ctx context.Context,
	request pickupOutcomeRequest) (Result, string, error) {
	if request.Task.assignment.GraphRootOID != workerReturnNoIdentity {
		return w.publishPickupOutcome(ctx, request)
	}
	document, err := json.Marshal(request.Report)
	if err != nil {
		return Result{}, workerReturnNoIdentity, fmt.Errorf(workerReturnEncodeResultErrorFormat, err)
	}
	oid, err := formatMessageCommit(ctx, w.Cache, w.ReturnSigner, MessageResult,
		document, nil, request.Carried)
	return request.Report, oid, err
}

// returnTaskAck is sent only after the command process group has stopped.
// The exact observed Cancel object is part of the signed proposal, so a
// different cancellation cannot be substituted after an unanswered reply.
type taskAckReturnRequest struct {
	Task           *claimedTask
	CancelOID      string
	Phase          string
	CommandStarted bool
}

func (w *Worker) returnTaskAck(ctx context.Context, request taskAckReturnRequest) (string, error) {
	task, cancel, phase, commandStarted := request.Task, request.CancelOID, request.Phase, request.CommandStarted
	channel, err := w.returnChannelFor(task.assignment)
	if err != nil {
		return workerReturnNoIdentity, err
	}
	if w.ReturnSigner == nil || cancel == workerReturnNoIdentity {
		return workerReturnNoIdentity, fmt.Errorf(workerReturnCancelIdentityError)
	}
	if err := w.recordCancelledPickup(ctx, task); err != nil {
		return workerReturnNoIdentity, err
	}
	proposal := ChoreographyAckProposal{ChoreographyRunBinding: channel.binding,
		Task: task.assignment.Task, Attempt: task.assignment.Attempt, Node: w.Node,
		Branch: task.tip.Branch, CancelOID: cancel, AssignmentOID: task.tip.OID,
		Phase: phase, CommandStarted: commandStarted,
		ProposalID: returnProposalID(returnProposalIdentity{Kind: MessageAck, Run: task.assignment.Run,
			Task: task.assignment.Task, Attempt: task.assignment.Attempt, OID: cancel})}
	query, err := FormatAckReceiptQuery(proposal)
	if err != nil {
		return workerReturnNoIdentity, err
	}
	var receipt ChoreographyReceipt
	for {
		receipt, err = channel.client.SubmitAck(ctx, proposal)
		if err == nil {
			break
		}
		settled, found, lookupErr := channel.client.LookupReceipt(ctx, query)
		if lookupErr == nil && found {
			receipt = settled
			break
		}
		select {
		case <-ctx.Done():
			return workerReturnNoIdentity, fmt.Errorf(workerReturnAckUnsettledErrorFormat, ctx.Err())
		case <-time.After(minimumPollInterval):
		}
	}
	if !receipt.Accepted || receipt.OID == workerReturnNoIdentity ||
		receipt.ChoreographyRunBinding != proposal.ChoreographyRunBinding ||
		receipt.Kind != MessageAck || receipt.Task != proposal.Task ||
		receipt.Attempt != proposal.Attempt || receipt.Node != proposal.Node ||
		receipt.Branch != proposal.Branch || receipt.ProposalID != proposal.ProposalID ||
		receipt.ProposalDigest != query.ProposalDigest {
		return workerReturnNoIdentity, fmt.Errorf(workerReturnAckReceiptError)
	}
	if err := w.awaitNodeSlotDecision(ctx, task, channel, receipt); err != nil {
		return workerReturnNoIdentity, err
	}
	return receipt.OID, nil
}

func (w *Worker) recordCancelledPickup(ctx context.Context, task *claimedTask) error {
	if task.assignment.GraphRootOID == workerReturnNoIdentity {
		return nil
	}
	state, err := w.ensurePickupPending(ctx, task)
	if err != nil {
		return err
	}
	if state.ResultOID != workerReturnNoIdentity {
		return w.pushPickupRef(ctx, workerPickupRef{Branch: state.OutcomeBranch, OID: state.ResultOID})
	}
	_, _, err = w.publishPickupOutcome(ctx, pickupOutcomeRequest{
		Task: task, Report: Result{
			Header: w.formatReplyHeader(task.assignment.Header), Assignment: task.claimed,
			Status:   StatusCancelled,
			Platform: Platform{OS: w.Report.OS, Arch: w.Report.Arch, Dispat: w.Report.Dispat},
		}})
	return err
}

// awaitNodeSlotDecision keeps physical capacity until the first node's
// signed logical finality releases its own capacity. ResultAccepted and
// AckAccepted alone are receipts for an effect, not a retry or terminal
// decision. The accepted receipt's exact cursor avoids replaying the whole
// run ledger once per completed task.
func (w *Worker) awaitNodeSlotDecision(ctx context.Context, task *claimedTask,
	channel *workerReturnChannel, receipt ChoreographyReceipt) error {
	if task.slotLease.ID == workerReturnNoIdentity {
		return nil
	}
	if receipt.Cursor.OID == workerReturnNoIdentity || receipt.Cursor.Sequence == workerReturnNoLedgerSequence {
		return fmt.Errorf(workerReturnCursorError)
	}
	// Submission and receipt lookup have a short bounded budget. Once the
	// receipt is accepted, finality belongs to the serving run: sibling work
	// and first-node cleanup may outlast that budget while this process stays
	// alive. On a serving-run stop the lease remains durable for cold replay.
	if task.finalityCtx != nil {
		ctx = task.finalityCtx
	}
	request := workerFinalityWaitRequest{Task: task, Channel: channel, Receipt: receipt}
	err := w.awaitNodeSlotDecisionUntil(ctx, request)
	if err == nil || (ctx.Err() == nil && !errors.Is(err, context.Canceled)) {
		return err
	}
	// A graceful stop may cancel the serving context just after exact signed
	// finality was persisted. Read the already available event once with an
	// independent bound before retaining the physical lease as unknown.
	probeCtx, done := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
	defer done()
	request.ProbeOnly = workerReturnFinalityProbeOnly
	probeErr := w.awaitNodeSlotDecisionUntil(probeCtx, request)
	if probeErr == nil {
		return nil
	}
	if !errors.Is(probeErr, context.Canceled) {
		return probeErr
	}
	return err
}

type workerFinalityWaitRequest struct {
	Task      *claimedTask
	Channel   *workerReturnChannel
	Receipt   ChoreographyReceipt
	ProbeOnly bool
}

func (w *Worker) awaitNodeSlotDecisionUntil(ctx context.Context, request workerFinalityWaitRequest) error {
	task, channel, receipt := request.Task, request.Channel, request.Receipt
	cursor := receipt.Cursor
	var retained *RetainedChoreographyFeed
	for {
		if waiter, ok := channel.client.(workerTaskEventWaiter); ok && !request.ProbeOnly {
			event, next, err := waiter.WaitTaskEvent(ctx, task.assignment.Task, cursor)
			if err != nil {
				return fmt.Errorf(workerReturnCapacityUnsettledErrorFormat, err)
			}
			if next.OID == workerReturnNoIdentity || next.Sequence <= cursor.Sequence || event.Sequence != next.Sequence ||
				event.Task != task.assignment.Task {
				return fmt.Errorf(workerReturnLocalEventError)
			}
			cursor = next
			if event.Kind != ChoreographyRetryReady && event.Kind != ChoreographyTerminal &&
				event.Kind != ChoreographyPickupSettled {
				continue
			}
			if event.Attempt != task.assignment.Attempt || event.Node != w.Node ||
				event.PickupID != task.slotLease.Intent.PickupID {
				continue
			}
			if event.ResultReceiptOID != receipt.OID {
				return fmt.Errorf(workerReturnLocalReleaseError)
			}
			if event.Kind == ChoreographyPickupSettled {
				return w.settleClaimedNodeSlot(ctx, task, next.OID)
			}
			return w.settleClaimedNodeSlot(ctx, task, event.TaskOID)
		}
		var page ChoreographyEventPage
		var err error
		if retained != nil {
			page, err = retained.ReadEvents(ctx, cursor, workerReturnEventPageSize)
		} else {
			page, err = channel.client.PollEvents(ctx, ChoreographyEventRequest{
				ChoreographyRunBinding: channel.binding, Node: w.Node,
				Cursor: cursor, MaxEvents: workerReturnEventPageSize})
		}
		if err != nil {
			if retained == nil && task.slotLease.Intent.Recovery != nil {
				if mailbox, ok := w.Mailbox.(*GitMailbox); ok {
					r := task.slotLease.Intent.Recovery
					retained, _ = w.openRetainedNodeSlotFeed(ctx, mailbox, nodeSlotRunKey{
						Binding: r.Binding, BootstrapOID: r.BootstrapOID,
						Endpoint: r.CanonicalEndpoint})
					if retained != nil {
						continue
					}
				}
			}
			if ctx.Err() != nil {
				return fmt.Errorf(workerReturnCapacityUnsettledErrorFormat, ctx.Err())
			}
			if request.ProbeOnly {
				return fmt.Errorf(workerReturnCapacityUnsettledErrorFormat, err)
			}
			w.Log.Debug().Err(err).Str(workerReturnLogTaskField, task.assignment.Task).
				Msg(workerReturnAwaitFinalityMessage)
		} else {
			if page.Cursor.OID == workerReturnNoIdentity || page.Cursor.Sequence != cursor.Sequence+uint64(len(page.Events)) ||
				(len(page.Events) == workerReturnNoEntries && page.Cursor.OID != cursor.OID) {
				return fmt.Errorf(workerReturnPageError)
			}
			for index, event := range page.Events {
				if event.Sequence != cursor.Sequence+uint64(index)+workerReturnFirstEventSequence || event.TaskOID == workerReturnNoIdentity {
					return fmt.Errorf(workerReturnSequenceError)
				}
				if event.Task != task.assignment.Task || event.Attempt != task.assignment.Attempt ||
					event.Node != w.Node || event.PickupID != task.slotLease.Intent.PickupID {
					continue
				}
				if event.Kind != ChoreographyRetryReady && event.Kind != ChoreographyTerminal &&
					event.Kind != ChoreographyPickupSettled {
					continue
				}
				if event.ResultReceiptOID != receipt.OID {
					return fmt.Errorf(workerReturnReleaseResultError)
				}
				if retained != nil {
					if err := retained.VerifyAttemptFinality(ctx, ChoreographyAttemptFinalityProof{
						Event: event, ReceiptOID: receipt.OID,
						ClaimOID: task.claimed, AssignmentOID: task.tip.OID}); err != nil {
						return fmt.Errorf(workerReturnRetainedDecisionErrorFormat, err)
					}
				}
				if event.Kind == ChoreographyPickupSettled {
					return w.settleClaimedNodeSlot(ctx, task, page.Cursor.OID)
				}
				return w.settleClaimedNodeSlot(ctx, task, event.TaskOID)
			}
			cursor = page.Cursor
			if request.ProbeOnly && len(page.Events) == workerReturnNoEntries {
				return fmt.Errorf(workerReturnCapacityUnsettledErrorFormat, context.Canceled)
			}
		}
		if request.ProbeOnly {
			continue
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf(workerReturnCapacityUnsettledErrorFormat, ctx.Err())
		case <-time.After(minimumPollInterval):
		}
	}
}

func (w *Worker) settleClaimedNodeSlot(ctx context.Context, task *claimedTask, evidenceOID string) error {
	task.slotMu.Lock()
	defer task.slotMu.Unlock()
	if task.slotSettled {
		return nil
	}
	settleCtx, done := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
	defer done()
	if err := w.releaseNodeSlot(settleCtx, task.slotLease, evidenceOID); err != nil {
		return err
	}
	task.slotSettled = workerReturnSlotSettled
	return nil
}
