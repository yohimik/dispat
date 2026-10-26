// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const choreographyMemberKind MessageKind = "choreography-member"
const choreographyMembershipTaskID = "__membership__"
const choreographyJoinRefusalStale = "stale-membership-epoch"
const choreographyJoinRefusalDeadline = "admission-deadline-expired"
const choreographyJoinRefusalAlreadyAdmitted = "identity-already-admitted"
const choreographyJoinRefusalStopping = "run-stopping"
const choreographyJoinRefusalIdentity = "invalid-node-identity"
const choreographyJoinRefusalReport = "incompatible-node-report"
const choreographyJoinRequestMismatchErrorMessage = "execution: join request differs from pinned run, graph or protocol"
const choreographyJoinPolicyMismatchErrorMessage = "execution: join identity or canonical endpoint is outside signed admission policy"
const choreographyJoinGraphUnsettledErrorMessage = "execution: complete graph root is not settled at its signed object"
const choreographyJoinReceiptAbsentErrorMessage = "execution: accepted join has no exact durable membership receipt"
const choreographyJoinQueryMismatchErrorMessage = "execution: join receipt query differs from pinned admission identity"
const choreographyJoinRetainedReceiptMismatchErrorMessage = "execution: retained join receipt names another membership transition"
const choreographyJoinMissingIdentity = ""
const choreographyJoinReceiptIndexOffset = 1
const choreographyJoinMemberNotFound = false
const choreographyJoinMemberAdmitted = true
const choreographyJoinRequestRefused = true

func (g *GitChoreographyPersist) PersistJoin(ctx context.Context,
	proposal ChoreographyJoinProposal) (ChoreographyJoinReceipt, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	if err := proposal.validate(); err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	if proposal.ChoreographyRunBinding != g.opts.Binding ||
		proposal.BootstrapOID != g.opts.BootstrapOID ||
		proposal.GraphRootOID != g.opts.GraphRootOID ||
		proposal.AlgorithmVersion != g.opts.AlgorithmVersion ||
		proposal.Report.Protocol != ChoreographyProtocolVersion {
		return ChoreographyJoinReceipt{}, fmt.Errorf(choreographyJoinRequestMismatchErrorMessage)
	}
	allowed, found := g.opts.JoinPolicy.AuthorizedNode(proposal.Node)
	if !found || !allowed.LateJoin || allowed.CoordinationEndpoint != proposal.Endpoint {
		return ChoreographyJoinReceipt{}, fmt.Errorf(choreographyJoinPolicyMismatchErrorMessage)
	}
	if err := VerifyChoreographyJoinIdentity(g.opts.JoinPolicy, proposal); err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	query, err := FormatChoreographyJoinReceiptQuery(proposal)
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	if old, known, err := g.LookupJoinReceipt(ctx, query); err != nil {
		return ChoreographyJoinReceipt{}, err
	} else if known {
		return old, nil
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	g.mu.Lock()
	stopping := g.stopping || g.sealed
	deadlineUTC := g.admissionDeadlineUTC
	started := g.executionStarted
	alreadyAdmitted := choreographyJoinMemberNotFound
	for _, event := range g.events {
		if event.Kind == ChoreographyMemberJoined && event.Node == proposal.Node {
			alreadyAdmitted = choreographyJoinMemberAdmitted
		}
	}
	g.mu.Unlock()
	var deadline time.Time
	if started {
		deadline, err = time.Parse(choreographyAdmissionTimeFormat, deadlineUTC)
		if err != nil {
			return ChoreographyJoinReceipt{}, fmt.Errorf(choreographyInvalidExecutionStartError)
		}
	}
	if stopping {
		return g.refuseJoin(choreographyJoinRefusal{Proposal: proposal,
			Digest: query.RequestDigest, Cursor: head, Reason: choreographyJoinRefusalStopping}), nil
	}
	if alreadyAdmitted {
		return g.refuseJoin(choreographyJoinRefusal{Proposal: proposal,
			Digest: query.RequestDigest, Cursor: head, Reason: choreographyJoinRefusalAlreadyAdmitted}), nil
	}
	if head.OID != proposal.ExpectedLedgerOID {
		return g.refuseJoin(choreographyJoinRefusal{Proposal: proposal,
			Digest: query.RequestDigest, Cursor: head, Reason: choreographyJoinRefusalStale}), nil
	}
	if started && time.Now().After(deadline) {
		return g.refuseJoin(choreographyJoinRefusal{Proposal: proposal,
			Digest: query.RequestDigest, Cursor: head, Reason: choreographyJoinRefusalDeadline}), nil
	}
	root, err := g.opts.Mailbox.readRemoteTip(ctx, FormatChoreographyGraphRootBranch(proposal.Run))
	if err != nil || root != g.opts.GraphRootOID {
		if err != nil {
			return ChoreographyJoinReceipt{}, err
		}
		return ChoreographyJoinReceipt{}, fmt.Errorf(choreographyJoinGraphUnsettledErrorMessage)
	}
	member := ChoreographyRuntimeNode{Name: proposal.Node,
		Platform: proposal.Report.OS + nodePlatformSeparator + proposal.Report.Arch,
		Capacity: proposal.Report.Capacity, AuthorityID: proposal.Report.AuthorityID}
	document, err := json.Marshal(proposal)
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	memberOID, err := g.opts.Mailbox.commitMessage(ctx, choreographyMemberKind,
		document, []string{g.opts.GraphRootOID}, nil)
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: query.RequestDigest, ExpectedLedgerOID: proposal.ExpectedLedgerOID,
		NewTaskOID: memberOID,
		Event: ChoreographyEvent{Kind: ChoreographyMemberJoined,
			Task: choreographyMembershipTaskID, TaskOID: memberOID,
			Node: proposal.Node, Member: &member, GraphRootOID: proposal.GraphRootOID,
			ProposalID: proposal.RequestID, ProposalDigest: query.RequestDigest},
	}); err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	receipt, found, err := g.LookupJoinReceipt(ctx, query)
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	if !found {
		return ChoreographyJoinReceipt{}, fmt.Errorf(choreographyJoinReceiptAbsentErrorMessage)
	}
	return receipt, nil
}

type choreographyJoinRefusal struct {
	Proposal ChoreographyJoinProposal
	Digest   string
	Cursor   ChoreographyEventCursor
	Reason   string
}

func (g *GitChoreographyPersist) refuseJoin(refusal choreographyJoinRefusal) ChoreographyJoinReceipt {
	proposal, digest, cursor, reason := refusal.Proposal, refusal.Digest, refusal.Cursor, refusal.Reason
	return ChoreographyJoinReceipt{ChoreographyRunBinding: proposal.ChoreographyRunBinding,
		BootstrapOID: proposal.BootstrapOID, GraphRootOID: proposal.GraphRootOID,
		Node: proposal.Node, RequestID: proposal.RequestID, RequestDigest: digest,
		Refused: choreographyJoinRequestRefused, Reason: reason, Cursor: cursor}
}

func (g *GitChoreographyPersist) LookupJoinReceipt(ctx context.Context,
	query ChoreographyJoinReceiptQuery) (ChoreographyJoinReceipt, bool, error) {
	if query.ChoreographyRunBinding != g.opts.Binding ||
		query.BootstrapOID != g.opts.BootstrapOID ||
		query.GraphRootOID != g.opts.GraphRootOID ||
		query.Node == choreographyJoinMissingIdentity || query.RequestID == choreographyJoinMissingIdentity ||
		query.RequestDigest == choreographyJoinMissingIdentity {
		return ChoreographyJoinReceipt{}, choreographyJoinMemberNotFound, fmt.Errorf(choreographyJoinQueryMismatchErrorMessage)
	}
	if _, err := g.LedgerHead(ctx); err != nil {
		return ChoreographyJoinReceipt{}, choreographyJoinMemberNotFound, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cursor, found := g.joined[query.RequestDigest]
	if !found {
		return ChoreographyJoinReceipt{}, choreographyJoinMemberNotFound, nil
	}
	event := g.events[cursor.Sequence-choreographyJoinReceiptIndexOffset]
	if event.Kind != ChoreographyMemberJoined || event.Node != query.Node ||
		event.ProposalID != query.RequestID || event.GraphRootOID != query.GraphRootOID ||
		event.Member == nil {
		return ChoreographyJoinReceipt{}, choreographyJoinMemberNotFound, fmt.Errorf(choreographyJoinRetainedReceiptMismatchErrorMessage)
	}
	return ChoreographyJoinReceipt{ChoreographyRunBinding: query.ChoreographyRunBinding,
		BootstrapOID: query.BootstrapOID, GraphRootOID: query.GraphRootOID,
		Node: query.Node, RequestID: query.RequestID, RequestDigest: query.RequestDigest,
		Member: *event.Member, Accepted: choreographyJoinMemberAdmitted,
		Cursor: cursor}, choreographyJoinMemberAdmitted, nil
}
