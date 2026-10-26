// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const choreographyClaimReplayIdentityError = "execution: accepted claim differs from signed node, lease or complete graph identity"

func (g *GitChoreographyPersist) verifyAcceptedClaimEvent(ctx context.Context,
	event ChoreographyEvent, priorLedgerOID string) error {
	m := g.opts.Mailbox
	tree, err := m.readTree(ctx, event.TaskOID)
	if err != nil {
		return err
	}
	if tree.kind != choreographyClaimKind {
		return fmt.Errorf(choreographyClaimReplayIdentityError)
	}
	document, err := m.Read(ctx, ChainTip{OID: event.TaskOID, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, returnReplyLimit)
	if err != nil {
		return err
	}
	var claim ChoreographyClaimProposal
	if err := strictJSON(document, &claim); err != nil {
		return err
	}
	digest, err := ChoreographyProposalDigest(claim)
	if err != nil {
		return err
	}
	if claim.validate() != nil || claim.ChoreographyRunBinding != g.opts.Binding ||
		claim.GraphRootOID != g.opts.GraphRootOID || claim.ExpectedLedgerOID != priorLedgerOID ||
		claim.Task != event.Task || claim.Attempt != event.Attempt || claim.Node != event.Node ||
		claim.Branch != g.tasks[event.Task].Branch || claim.ExpectedOID != event.PriorTaskOID ||
		claim.TemplateDigest != g.tasks[event.Task].TemplateDigest ||
		claim.ProposalID != event.ProposalID || digest != event.ProposalDigest ||
		claim.InputsDigest != event.InputsDigest ||
		claim.MembershipEpochOID != event.MembershipEpochOID ||
		claim.CapacityAuthorityID != event.CapacityAuthorityID ||
		claim.LeaseIntentID != event.LeaseIntentID || claim.LeaseWeight != event.LeaseWeight ||
		claim.LeaseWeight != g.tasks[event.Task].Weight ||
		claim.PickupID != event.PickupID ||
		claim.LeaseIntentID != nodeSlotLeaseID(NodeSlotIntent{Run: claim.Run, Task: claim.Task, Attempt: claim.Attempt, PickupID: claim.PickupID}) {
		return fmt.Errorf(choreographyClaimReplayIdentityError)
	}
	if err := VerifyChoreographyClaimIdentity(g.opts.JoinPolicy, claim); err != nil {
		return err
	}
	return nil
}
