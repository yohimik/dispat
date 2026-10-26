// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const choreographyJoinReplayIdentityError = "execution: signed member join differs from pinned node identity or ledger epoch"
const choreographyJoinReplayCarrierParents = 1
const choreographyJoinReplayRootParentIndex = 0

func (g *GitChoreographyPersist) verifyJoinedMemberEvent(ctx context.Context,
	event ChoreographyEvent, priorLedgerOID string) error {
	if event.Kind != ChoreographyMemberJoined || event.Member == nil ||
		event.GraphRootOID != g.opts.GraphRootOID {
		return fmt.Errorf(choreographyJoinReplayIdentityError)
	}
	m := g.opts.Mailbox
	parents, err := m.plumbing.CommitParents(ctx, event.TaskOID)
	if err != nil {
		return err
	}
	if len(parents) != choreographyJoinReplayCarrierParents || parents[choreographyJoinReplayRootParentIndex] != g.opts.GraphRootOID {
		return fmt.Errorf(choreographyJoinReplayIdentityError)
	}
	tree, err := m.readTree(ctx, event.TaskOID)
	if err != nil {
		return err
	}
	if tree.kind != choreographyMemberKind {
		return fmt.Errorf(choreographyJoinReplayIdentityError)
	}
	document, err := m.Read(ctx, ChainTip{OID: event.TaskOID, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, returnReplyLimit)
	if err != nil {
		return err
	}
	var request ChoreographyJoinProposal
	if err := strictJSON(document, &request); err != nil {
		return err
	}
	digest, err := DigestChoreographyJoinProposal(request)
	if err != nil {
		return err
	}
	allowed, found := g.opts.JoinPolicy.AuthorizedNode(request.Node)
	if !found || !allowed.LateJoin || request.ChoreographyRunBinding != g.opts.Binding ||
		request.BootstrapOID != g.opts.BootstrapOID || request.GraphRootOID != g.opts.GraphRootOID ||
		request.AlgorithmVersion != g.opts.AlgorithmVersion ||
		request.ExpectedLedgerOID != priorLedgerOID ||
		request.Node != event.Node || request.RequestID != event.ProposalID ||
		digest != event.ProposalDigest || request.Report.AuthorityID != event.Member.AuthorityID ||
		request.Report.Capacity != event.Member.Capacity ||
		request.Report.OS+nodePlatformSeparator+request.Report.Arch != event.Member.Platform {
		return fmt.Errorf(choreographyJoinReplayIdentityError)
	}
	if err := VerifyChoreographyJoinIdentity(g.opts.JoinPolicy, request); err != nil {
		return err
	}
	return nil
}

func (g *GitChoreographyPersist) verifyDrainingMemberEvent(ctx context.Context,
	event ChoreographyEvent, priorLedgerOID string) error {
	if event.Kind != ChoreographyMemberDraining || event.Member == nil ||
		event.Member.Name != event.Node || event.Member.First ||
		event.GraphRootOID != g.opts.GraphRootOID {
		return fmt.Errorf(choreographyJoinReplayIdentityError)
	}
	m := g.opts.Mailbox
	parents, err := m.plumbing.CommitParents(ctx, event.TaskOID)
	if err != nil {
		return err
	}
	if len(parents) != choreographyJoinReplayCarrierParents || parents[choreographyJoinReplayRootParentIndex] != g.opts.GraphRootOID {
		return fmt.Errorf(choreographyJoinReplayIdentityError)
	}
	tree, err := m.readTree(ctx, event.TaskOID)
	if err != nil {
		return err
	}
	if tree.kind != choreographyMemberDrainKind {
		return fmt.Errorf(choreographyJoinReplayIdentityError)
	}
	document, err := m.Read(ctx, ChainTip{OID: event.TaskOID, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, returnReplyLimit)
	if err != nil {
		return err
	}
	var request ChoreographyDrainProposal
	if err := strictJSON(document, &request); err != nil {
		return err
	}
	query, err := FormatChoreographyDrainReceiptQuery(request)
	if err != nil {
		return err
	}
	if request.ChoreographyRunBinding != g.opts.Binding || request.BootstrapOID != g.opts.BootstrapOID ||
		request.GraphRootOID != g.opts.GraphRootOID || request.ExpectedLedgerOID != priorLedgerOID ||
		request.Node != event.Node || request.CapacityAuthorityID != event.Member.AuthorityID ||
		request.RequestID != event.ProposalID || query.RequestDigest != event.ProposalDigest {
		return fmt.Errorf(choreographyJoinReplayIdentityError)
	}
	return VerifyChoreographyDrainIdentity(g.opts.JoinPolicy, request)
}
