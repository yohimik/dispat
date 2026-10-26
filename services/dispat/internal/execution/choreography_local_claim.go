// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const (
	localClaimMissingIdentity            = ""
	localClaimProposalFieldSeparator     = "\x00"
	localClaimMissingGraphAuthorityError = "execution: first-local claim has no complete graph authority"
	localClaimUnavailableTaskErrorFormat = "execution: first node cannot claim task %q from its current state"
	localClaimMissingLeaseError          = "execution: first-local claim lacks its exact held physical lease"
	localClaimMissingTemplateError       = "execution: first-local task has no signed compiled template or branch"
	localClaimMissingResolverError       = "execution: first-local task has no trusted effective-input resolver"
)

// ClaimChoreographyLocalTask takes the first node's own exact canonical task.
// It never chooses work for another node and starts no effect before the
// accepted signed task/ledger receipt is durable.
func (c *Coordinator) ClaimChoreographyLocalTask(ctx context.Context,
	taskID string, lease NodeSlotLease) (ChoreographyReceipt, error) {
	if c == nil || c.choreographyBroker == nil || c.GraphRootOID == localClaimMissingIdentity {
		return ChoreographyReceipt{}, fmt.Errorf(localClaimMissingGraphAuthorityError)
	}
	b := c.choreographyBroker
	inputsDigest, predecessorIDs, err := b.ExpectedInputIdentity(taskID)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	b.mu.Lock()
	task := b.tasks[taskID]
	if task == nil || !b.nodeEligible(task.spec, c.Local.Name) || task.claim != localClaimMissingIdentity {
		b.mu.Unlock()
		return ChoreographyReceipt{}, fmt.Errorf(localClaimUnavailableTaskErrorFormat, taskID)
	}
	branch, tip, attempt, templateDigest, weight, preparedInputOID := task.spec.Branch, task.tip,
		task.attempt, task.spec.TemplateDigest, task.spec.Weight, task.preparedInputOID
	b.mu.Unlock()
	if lease.ID == localClaimMissingIdentity || lease.ID != nodeSlotLeaseID(lease.Intent) ||
		lease.Intent.Run != c.Run || lease.Intent.Task != taskID ||
		lease.Intent.Attempt != attempt || lease.Intent.Weight != weight {
		return ChoreographyReceipt{}, fmt.Errorf(localClaimMissingLeaseError)
	}
	epoch, member, err := b.MembershipEpoch(c.Local.Name)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if templateDigest == localClaimMissingIdentity || branch == localClaimMissingIdentity || tip == localClaimMissingIdentity {
		return ChoreographyReceipt{}, fmt.Errorf(localClaimMissingTemplateError)
	}
	if b.resolveBindings == nil {
		return ChoreographyReceipt{}, fmt.Errorf(localClaimMissingResolverError)
	}
	identity := ChoreographyTaskBindings{PredecessorResultOIDs: predecessorIDs,
		PreparedInputOID: preparedInputOID}
	identityDigest, err := DigestChoreographyTaskBindings(identity)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	head, err := b.persist.LedgerHead(ctx)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	proposal := ChoreographyClaimProposal{ChoreographyRunBinding: b.binding,
		Task: taskID, Attempt: attempt, Node: c.Local.Name,
		Branch: branch, ExpectedOID: tip, ExpectedLedgerOID: head.OID,
		GraphRootOID: c.GraphRootOID, InputsDigest: inputsDigest,
		MembershipEpochOID: epoch, CapacityAuthorityID: member.AuthorityID,
		LeaseIntentID: lease.ID, LeaseWeight: weight,
		TemplateDigest: templateDigest, Bindings: identity,
		BindingsDigest: identityDigest,
		ProposalID: returnProposalID(returnProposalIdentity{Kind: choreographyClaimKind,
			Run: c.Run, Task: taskID, Attempt: attempt,
			OID: tip + localClaimProposalFieldSeparator + head.OID})}
	return acceptFirstLocalClaim(ctx, b, proposal)
}
