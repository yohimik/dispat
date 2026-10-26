// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"fmt"
)

const choreographyDrainPersistIdentityError = "execution: drain persistence differs from pinned admitted node"
const choreographyDrainMissingIdentity = ""
const choreographyDrainNoPriorEvent = false
const choreographyDrainPriorEventFound = true
const choreographyDrainReceiptRefused = true
const choreographyDrainReceiptAccepted = true
const choreographyDrainSequenceIndexOffset = 1

type choreographyDrainRefusal struct {
	Proposal ChoreographyDrainProposal
	Digest   string
	Cursor   ChoreographyEventCursor
	Reason   string
}

func (g *GitChoreographyPersist) PersistDrain(ctx context.Context,
	proposal ChoreographyDrainProposal, member ChoreographyRuntimeNode) (ChoreographyDrainReceipt, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	if err := proposal.validate(); err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	if proposal.ChoreographyRunBinding != g.opts.Binding || proposal.BootstrapOID != g.opts.BootstrapOID ||
		proposal.GraphRootOID != g.opts.GraphRootOID || proposal.Node != member.Name ||
		proposal.CapacityAuthorityID != member.AuthorityID || member.First {
		return ChoreographyDrainReceipt{}, fmt.Errorf(choreographyDrainPersistIdentityError)
	}
	if err := VerifyChoreographyDrainIdentity(g.opts.JoinPolicy, proposal); err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	query, err := FormatChoreographyDrainReceiptQuery(proposal)
	if err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	if old, found, err := g.LookupDrainReceipt(ctx, query); err != nil {
		return ChoreographyDrainReceipt{}, err
	} else if found {
		return old, nil
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	g.mu.Lock()
	stopping := g.stopping || g.sealed
	already := choreographyDrainNoPriorEvent
	for _, event := range g.events {
		if event.Kind == ChoreographyMemberDraining && event.Node == proposal.Node {
			already = choreographyDrainPriorEventFound
		}
	}
	g.mu.Unlock()
	if stopping {
		return g.refuseDrain(choreographyDrainRefusal{Proposal: proposal,
			Digest: query.RequestDigest, Cursor: head, Reason: choreographyDrainRefusalStopping}), nil
	}
	if already {
		return g.refuseDrain(choreographyDrainRefusal{Proposal: proposal,
			Digest: query.RequestDigest, Cursor: head, Reason: choreographyDrainRefusalAlready}), nil
	}
	if head.OID != proposal.ExpectedLedgerOID {
		return g.refuseDrain(choreographyDrainRefusal{Proposal: proposal,
			Digest: query.RequestDigest, Cursor: head, Reason: choreographyDrainRefusalStale}), nil
	}
	document, err := json.Marshal(proposal)
	if err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	requestOID, err := g.opts.Mailbox.commitMessage(ctx, choreographyMemberDrainKind,
		document, []string{g.opts.GraphRootOID}, nil)
	if err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{Identity: query.RequestDigest,
		ExpectedLedgerOID: proposal.ExpectedLedgerOID, NewTaskOID: requestOID,
		Event: ChoreographyEvent{Kind: ChoreographyMemberDraining,
			Task: choreographyMembershipTaskID, TaskOID: requestOID, Node: proposal.Node,
			Member: &member, GraphRootOID: proposal.GraphRootOID,
			ProposalID: proposal.RequestID, ProposalDigest: query.RequestDigest}}); err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	receipt, found, err := g.LookupDrainReceipt(ctx, query)
	if err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	if !found {
		return ChoreographyDrainReceipt{}, fmt.Errorf(choreographyDrainPersistIdentityError)
	}
	return receipt, nil
}

func (g *GitChoreographyPersist) refuseDrain(refusal choreographyDrainRefusal) ChoreographyDrainReceipt {
	proposal := refusal.Proposal
	return ChoreographyDrainReceipt{ChoreographyRunBinding: proposal.ChoreographyRunBinding,
		BootstrapOID: proposal.BootstrapOID, GraphRootOID: proposal.GraphRootOID,
		Node: proposal.Node, RequestID: proposal.RequestID, RequestDigest: refusal.Digest,
		Refused: choreographyDrainReceiptRefused, Reason: refusal.Reason, Cursor: refusal.Cursor}
}

func (g *GitChoreographyPersist) LookupDrainReceipt(ctx context.Context,
	query ChoreographyDrainReceiptQuery) (ChoreographyDrainReceipt, bool, error) {
	if query.ChoreographyRunBinding != g.opts.Binding || query.BootstrapOID != g.opts.BootstrapOID ||
		query.GraphRootOID != g.opts.GraphRootOID || query.Node == choreographyDrainMissingIdentity ||
		query.RequestID == choreographyDrainMissingIdentity || query.RequestDigest == choreographyDrainMissingIdentity ||
		query.ExpectedLedgerOID == choreographyDrainMissingIdentity {
		return ChoreographyDrainReceipt{}, choreographyDrainNoPriorEvent, fmt.Errorf(choreographyDrainPersistIdentityError)
	}
	if _, err := g.LedgerHead(ctx); err != nil {
		return ChoreographyDrainReceipt{}, choreographyDrainNoPriorEvent, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cursor, found := g.drained[query.RequestDigest]
	if !found {
		return ChoreographyDrainReceipt{}, choreographyDrainNoPriorEvent, nil
	}
	event := g.events[cursor.Sequence-choreographyDrainSequenceIndexOffset]
	if event.Kind != ChoreographyMemberDraining || event.Node != query.Node ||
		event.ProposalID != query.RequestID || event.GraphRootOID != query.GraphRootOID ||
		event.Member == nil {
		return ChoreographyDrainReceipt{}, choreographyDrainNoPriorEvent, fmt.Errorf(choreographyDrainPersistIdentityError)
	}
	return ChoreographyDrainReceipt{ChoreographyRunBinding: query.ChoreographyRunBinding,
		BootstrapOID: query.BootstrapOID, GraphRootOID: query.GraphRootOID, Node: query.Node,
		RequestID: query.RequestID, RequestDigest: query.RequestDigest,
		Accepted: choreographyDrainReceiptAccepted, Cursor: cursor}, choreographyDrainPriorEventFound, nil
}
