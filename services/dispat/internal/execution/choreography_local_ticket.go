// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	firstLocalClaimLookupFailedErrorFormat = "execution: first-local claim for task %q has no verified receipt because lookup failed: %w"
	firstLocalClaimNotFoundErrorFormat     = "execution: first-local claim for task %q has no durable receipt after persistence error: %w"
	firstLocalClaimIdentityErrorFormat     = "execution: first-local claim for task %q has another proposal identity"
	firstLocalClaimRefusedErrorFormat      = "execution: first-local claim for task %q was refused as %q"
)

// ChoreographyClaimRefusedError carries a definitive, exact signed fence.
// A caller may release only the unaccepted lease for this proposal against
// Receipt.FenceOID; an unknown result never has this type.
type ChoreographyClaimRefusedError struct{ Receipt ChoreographyReceipt }

func (e *ChoreographyClaimRefusedError) Error() string {
	return fmt.Sprintf(firstLocalClaimRefusedErrorFormat,
		e.Receipt.Task, e.Receipt.RefusalReason)
}

// ChoreographyLocalTicket is the first node's branch-bearing native task.
// The Executor already holds its physical NodeSlot before admission; this
// ticket adds only the same canonical Assignment/Claim/Result/Terminal CAS
// used by a command frame. It never acquires a second physical slot.
type ChoreographyLocalTicket struct {
	mu           sync.Mutex
	coordinator  *Coordinator
	binding      ChoreographyRunBinding
	task         string
	kind         string
	branch       string
	attempt      int
	assignment   string
	claim        string
	inputsDigest string
	settlement   bool
	succeeded    bool
	outcome      release.PackageOutcome
	resultOID    string
	prepared     ChoreographyResultProposal
	receiptOID   string
	finished     bool
}

// BeginChoreographyLocalTask runs after the release evaluator admitted this
// task and before its first hook, native write, or command. A durable Claim
// receipt is the only authority to begin the effect.
func (c *Coordinator) BeginChoreographyLocalTask(ctx context.Context,
	taskID string, lease NodeSlotLease) (*ChoreographyLocalTicket, error) {
	if c == nil || c.choreographyBroker == nil || taskID == choreographyLocalAbsentIdentityText || c.Local.Name == choreographyLocalAbsentIdentityText {
		return nil, fmt.Errorf(choreographyLocalBrokerMissingErrorText)
	}
	b := c.choreographyBroker
	b.mu.Lock()
	task := b.tasks[taskID]
	if task == nil || task.spec.Virtual || task.spec.LocalAuthority || task.spec.Branch == choreographyLocalAbsentIdentityText ||
		task.remaining != choreographyLocalNoPredecessorsCount || task.terminal || task.claim != choreographyLocalAbsentIdentityText {
		b.mu.Unlock()
		return nil, fmt.Errorf(choreographyLocalTaskNotReadyErrorText)
	}
	inputsDigest, digestErr := b.readyInputsDigest(task)
	if digestErr != nil {
		b.mu.Unlock()
		return nil, digestErr
	}
	spec, attempt := task.spec, task.attempt
	binding := b.binding
	b.mu.Unlock()
	if spec.Placement != PlacementOrchestrator && spec.Placement != PlacementAnyNode {
		return nil, fmt.Errorf(choreographyLocalFirstNodeIneligibleErrorText)
	}
	c.registerChoreographyResult(choreographyResultRegistration{Task: taskID,
		Produces: choreographyLocalOutputCaptureDisabledState})
	receipt, err := c.ClaimChoreographyLocalTask(ctx, taskID, lease)
	if err != nil {
		return nil, c.refuseTask(taskID, c.Local.Name, attempt, err)
	}
	if !receipt.Accepted || receipt.Refused || receipt.OID == choreographyLocalAbsentIdentityText ||
		receipt.AssignmentOID == choreographyLocalAbsentIdentityText || receipt.GraphRootOID != c.GraphRootOID ||
		receipt.Task != taskID || receipt.Attempt != attempt ||
		receipt.Node != c.Local.Name || receipt.Branch != spec.Branch ||
		receipt.ChoreographyRunBinding != binding {
		return nil, fmt.Errorf(choreographyLocalClaimReceiptInvalidErrorText)
	}
	return &ChoreographyLocalTicket{coordinator: c, binding: binding,
		task: taskID, kind: spec.Kind, branch: spec.Branch, attempt: attempt,
		assignment: receipt.AssignmentOID, claim: receipt.OID,
		inputsDigest: inputsDigest}, nil
}

// acceptFirstLocalClaim keeps the same offered Assignment while reconciling
// each signed ledger epoch. An old proposal can only be replaced after its
// newer signed fence proves that its CAS can no longer land.
func acceptFirstLocalClaim(ctx context.Context, b *FirstNodeBroker,
	claim ChoreographyClaimProposal) (ChoreographyReceipt, error) {
	var receipt ChoreographyReceipt
	for {
		if err := ctx.Err(); err != nil {
			return ChoreographyReceipt{}, err
		}
		query, err := FormatClaimReceiptQuery(claim)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		receipt, err = b.AcceptClaim(ctx, claim)
		if err != nil {
			settled, found, lookupErr := b.LookupReceipt(ctx, query)
			if lookupErr != nil {
				return ChoreographyReceipt{}, fmt.Errorf(firstLocalClaimLookupFailedErrorFormat,
					claim.Task, errors.Join(err, lookupErr))
			}
			if !found {
				return ChoreographyReceipt{}, fmt.Errorf(firstLocalClaimNotFoundErrorFormat,
					claim.Task, err)
			}
			receipt = settled
		}
		if receipt.ChoreographyRunBinding != claim.ChoreographyRunBinding ||
			receipt.ProposalID != claim.ProposalID ||
			receipt.ProposalDigest != query.ProposalDigest ||
			receipt.GraphRootOID != claim.GraphRootOID {
			return ChoreographyReceipt{}, fmt.Errorf(firstLocalClaimIdentityErrorFormat, claim.Task)
		}
		if receipt.Accepted && !receipt.Refused && receipt.Kind == choreographyClaimKind &&
			receipt.OID != choreographyLocalAbsentIdentityText && receipt.Task == claim.Task && receipt.Attempt == claim.Attempt &&
			receipt.Node == claim.Node && receipt.Branch == claim.Branch {
			return receipt, nil
		}
		if !receipt.Refused || receipt.Accepted || receipt.Kind != choreographyClaimKind ||
			receipt.OID != claim.ExpectedOID || receipt.Task != claim.Task ||
			receipt.Attempt != claim.Attempt || receipt.Node != claim.Node ||
			receipt.Branch != claim.Branch ||
			receipt.FenceOID == choreographyLocalAbsentIdentityText || receipt.FenceOID == claim.ExpectedLedgerOID ||
			receipt.FenceSequence == choreographyLocalNoLedgerSequence {
			return ChoreographyReceipt{}, fmt.Errorf(firstLocalClaimIdentityErrorFormat, claim.Task)
		}
		if receipt.RefusalReason != ChoreographyRefusalStale {
			return receipt, &ChoreographyClaimRefusedError{Receipt: receipt}
		}
		// The signed newer ledger head fences the previous exact proposal.
		// Reoffer only the claim for this same immutable Assignment.
		claim.ExpectedLedgerOID = receipt.FenceOID
		claim.ProposalID = returnProposalID(returnProposalIdentity{Kind: choreographyClaimKind,
			Run: claim.Run, Task: claim.Task, Attempt: claim.Attempt,
			OID: claim.ExpectedOID + choreographyLocalProposalSeparatorText + claim.ExpectedLedgerOID})
	}
}

func containsNode(nodes []string, name string) bool {
	for _, node := range nodes {
		if node == name {
			return choreographyLocalNodePresentState
		}
	}
	return choreographyLocalNodeAbsentState
}

// ReserveFirstLocalPool accounts for a first-only Executor vertex before its
// physical NodeSlot is acquired. The caller must use this only for vertices
// that do not already use ReserveStage, preserving Pool→NodeSlot lock order.
// The returned release is idempotent and runs after native task finality.
func (c *Coordinator) ReserveFirstLocalPool(ctx context.Context,
	taskID string) (func(), error) {
	if c == nil || c.Pool == nil || c.choreographyBroker == nil || taskID == choreographyLocalAbsentIdentityText {
		return nil, fmt.Errorf(choreographyLocalPlacementUnavailableErrorText)
	}
	b := c.choreographyBroker
	b.mu.Lock()
	task := b.tasks[taskID]
	var spec ChoreographyTaskSpec
	if task != nil {
		spec = task.spec
	}
	b.mu.Unlock()
	if spec.Task == choreographyLocalAbsentIdentityText || spec.Virtual || spec.Weight < choreographyLocalMinimumWeight ||
		(spec.Placement != PlacementOrchestrator && spec.Placement != PlacementAnyNode) {
		return nil, fmt.Errorf(choreographyLocalVertexIneligibleErrorText)
	}
	if c.GraphRootOID != choreographyLocalAbsentIdentityText {
		// The signed broker wakes this exact native task when both its run
		// budget and first-node capacity are available. Waiting owns no
		// physical Pool or NodeSlot lease; the later Claim CAS is definitive.
		if err := b.WaitClaimBudget(ctx, taskID, c.Local.Name); err != nil {
			return nil, err
		}
		return func() {}, nil
	}
	lease, err := c.Pool.AcquireNearWeight(ctx, WeightedPlacementRequest{
		Placement: PlacementOrchestrator, Preferred: choreographyLocalAbsentIdentityText, Weight: spec.Weight,
	})
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(lease.Release) }, nil
}

// Finish persists the task's own status and its causally settled package
// outcome after finishTask has completed sibling and hook settlement. A
// failed persistence leaves the canonical claim unresolved for recovery.
func (t *ChoreographyLocalTicket) Finish(ctx context.Context, taskSucceeded bool,
	packageOutcome release.PackageOutcome) error {
	if t == nil || t.coordinator == nil {
		return fmt.Errorf(choreographyLocalTicketAbsentErrorText)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return nil
	}
	if t.settlement && (t.succeeded != taskSucceeded || t.outcome != packageOutcome) {
		return fmt.Errorf(choreographyLocalSettlementChangedErrorText)
	}
	t.settlement, t.succeeded, t.outcome = choreographyLocalSettlementStartedState, taskSucceeded, packageOutcome
	c := t.coordinator
	m, err := c.ChoreographyMailbox()
	if err != nil {
		return err
	}
	status := StatusFailed
	if taskSucceeded {
		status = StatusSucceeded
	}
	if t.receiptOID == choreographyLocalAbsentIdentityText {
		if t.resultOID == choreographyLocalAbsentIdentityText {
			header := c.formatOrchestratorHeader(t.kind, t.task, t.attempt, c.Local.Name, t.branch)
			header.Protocol = ChoreographyProtocolVersion
			result := Result{Header: header, Assignment: t.claim, Status: status}
			document, err := json.Marshal(result)
			if err != nil {
				return err
			}
			t.resultOID, err = m.commitMessage(ctx, MessageResult, document, nil, nil)
			if err != nil {
				return err
			}
		}
		maxPack := c.Limits.MaxPackBytes
		if maxPack <= choreographyLocalNoObjectBytes {
			return fmt.Errorf(choreographyLocalPackBoundMissingErrorText)
		}
		pack, err := os.CreateTemp(m.plumbing.Dir, choreographyLocalResultPackFilePattern)
		if err != nil {
			return err
		}
		defer func() { _ = pack.Close(); _ = os.Remove(pack.Name()) }()
		if err := m.plumbing.ExportCommitPack(ctx, t.resultOID,
			&boundedResultPackWriter{to: pack, remaining: maxPack}); err != nil {
			return err
		}
		proposal := ChoreographyResultProposal{ChoreographyRunBinding: t.binding,
			Task: t.task, Attempt: t.attempt, Node: c.Local.Name, Branch: t.branch,
			ClaimOID: t.claim, InputsDigest: t.inputsDigest,
			ResultDigest: t.resultOID, ProposalID: returnProposalID(returnProposalIdentity{
				Kind: choreographyResultKind, Run: t.binding.Run, Task: t.task,
				Attempt: t.attempt, OID: t.resultOID})}
		prepared, err := PrepareChoreographyResult(ctx, proposal, pack, maxPack)
		if err != nil {
			return err
		}
		if t.prepared.ResultDigest != choreographyLocalAbsentIdentityText && t.prepared != prepared {
			return fmt.Errorf(choreographyLocalUncertainPackChangedErrorText)
		}
		t.prepared = prepared
		receipt, err := c.choreographyBroker.AcceptResult(ctx, prepared, pack)
		if err != nil {
			query, queryErr := FormatResultReceiptQuery(prepared)
			if queryErr != nil {
				return err
			}
			settled, found, lookupErr := c.choreographyBroker.LookupReceipt(ctx, query)
			if lookupErr != nil || !found {
				return err
			}
			// Lookup proves the CAS landed, then the identical accepted body
			// restores the broker's indexed attempt after a lost reply.
			receipt, err = c.choreographyBroker.AcceptResult(ctx, prepared, pack)
			if err != nil {
				return fmt.Errorf(choreographyLocalReceiptReconciliationErrorFormat, err)
			}
			if receipt.OID != settled.OID {
				return fmt.Errorf(choreographyLocalReceiptChangedErrorText)
			}
		}
		if !receipt.Accepted || receipt.OID == choreographyLocalAbsentIdentityText || receipt.Kind != choreographyResultKind ||
			receipt.Task != t.task || receipt.Attempt != t.attempt || receipt.Node != c.Local.Name ||
			receipt.ProposalID != prepared.ProposalID {
			return fmt.Errorf(choreographyLocalReceiptNotDurableErrorText)
		}
		t.receiptOID = receipt.OID
	}
	if err := c.choreographyBroker.DecideAttempt(ctx, ChoreographyDecision{
		ChoreographyRunBinding: t.binding, Task: t.task, Branch: t.branch,
		Attempt: t.attempt, Node: c.Local.Name, ResultOID: t.receiptOID,
		Succeeded: taskSucceeded, PackageOutcome: string(packageOutcome),
	}); err != nil {
		return err
	}
	t.finished = choreographyLocalTicketFinishedState
	return nil
}

// BlockChoreographyLocalTask signs a no-claim decision after denied admission
// and its onSkip/revert hooks have settled. The shared outcome evaluator
// checks the exact blocker before the task branch and ledger advance.
func (c *Coordinator) BlockChoreographyLocalTask(ctx context.Context, taskID,
	blocker string, outcome release.PackageOutcome) error {
	if c == nil || c.choreographyBroker == nil || taskID == choreographyLocalAbsentIdentityText || blocker == choreographyLocalAbsentIdentityText {
		return fmt.Errorf(choreographyLocalBlockedTaskIncompleteErrorText)
	}
	b := c.choreographyBroker
	b.mu.Lock()
	task := b.tasks[taskID]
	var pkg string
	if task != nil {
		pkg = task.spec.Package
	}
	b.mu.Unlock()
	if pkg == choreographyLocalAbsentIdentityText {
		return fmt.Errorf(choreographyLocalBlockedTaskPackageMissingErrorText)
	}
	return b.AcceptBlocked(ctx, ChoreographyBlockedOutcome{
		ChoreographyRunBinding: b.binding, Task: taskID,
		Package: pkg, BlockedBy: blocker, PackageOutcome: string(outcome),
	})
}
