// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The state machine of one coordination branch, and the rules a message has
// to satisfy before any party acts on it.
//
// Every transition is exactly one compare-and-swap push by exactly one party
// (§28.4), so the branch itself is the shared state and neither side has to
// trust a check it made a moment earlier. What is written here is only the
// decision: which step may follow which, who writes it, and whether a message
// found on a branch is one this node may act on at all. Nothing in this file
// touches git, which is what makes the protocol testable without a mailbox.

import (
	"errors"
	"fmt"
	"time"
)

// Party is one side of the protocol. It is the writer of a transition rather
// than a role of a node: an orchestrator-role node serving a task is the
// worker of that attempt and nothing else (§28.1).
type Party string

const (
	// PartyOrchestrator is the side that owns the run: it creates the branch,
	// authorizes a publication and withdraws an attempt.
	PartyOrchestrator Party = "orchestrator"
	// PartyWorker is the side that executes one assignment and reports.
	PartyWorker Party = "worker"
)

// Transition is one legal step of a coordination branch: the message its tip
// carries, the message that may follow, and the party that writes it.
type Transition struct {
	From MessageKind
	To   MessageKind
	By   Party
}

// transitions is the whole protocol, as data.
//
// The table is complete rather than limited to the steps this build performs,
// because it is the description both parties read: a step that is legal in
// the protocol and not yet implemented is a branch a node leaves alone, and
// that is a very different thing from a step nobody agreed on. The empty
// From is the branch not existing yet, which only a create-only push may
// follow.
var transitions = []Transition{
	{From: "", To: MessageAssignment, By: PartyOrchestrator},
	{From: MessageAssignment, To: MessageClaim, By: PartyWorker},
	{From: MessageClaim, To: MessageReady, By: PartyWorker},
	{From: MessageReady, To: MessageGo, By: PartyOrchestrator},
	{From: MessageGo, To: MessageResult, By: PartyWorker},
	{From: MessageClaim, To: MessageResult, By: PartyWorker},
	{From: MessageAssignment, To: MessageCancel, By: PartyOrchestrator},
	{From: MessageClaim, To: MessageCancel, By: PartyOrchestrator},
	{From: MessageReady, To: MessageCancel, By: PartyOrchestrator},
	{From: MessageCancel, To: MessageAck, By: PartyWorker},
}

// IsTransitionLegal reports whether one party may write `to` onto a branch
// whose tip carries `from`, an empty `from` being a branch that does not
// exist yet.
func IsTransitionLegal(from, to MessageKind, by Party) bool {
	for _, transition := range transitions {
		if transition.From == from && transition.To == to && transition.By == by {
			return true
		}
	}
	return false
}

// ChainTip is one observed coordination branch: where it is, what its tip
// carries, and what came immediately before it.
//
// The previous kind is what makes the shape checkable without walking the
// whole chain: each commit's tree holds its own message alone, so the tip and
// its first parent are one transition, and a chain built entirely of legal
// transitions is legal by induction. A parent carrying no message at all is
// the empty kind, which is both a root commit and the source snapshot a real
// assignment is written on top of.
type ChainTip struct {
	Branch   string
	OID      string
	Kind     MessageKind
	Previous MessageKind
	// PreviousOID is the first parent's object id, empty for a root commit.
	// A message names the exact object it answers, and this is what that name
	// is compared against: an authorization whose parent is not the ready
	// commit it claims to answer is an authorization for a different state of
	// the branch, whoever wrote it.
	PreviousOID string

	// isProtocol reports whether the commit's tree holds anything this
	// protocol wrote at all. A commit with no message and nothing under the
	// message folder is a prepared input state, which is a perfectly ordinary
	// thing to find on a mailbox; one that has the folder and not a readable
	// message in it is somebody writing into this node's mailbox, and the two
	// are told apart here so that only the second is a warning.
	isProtocol bool

	// The blobs the tip's tree holds for its message, resolved while the tree
	// was read so that reading the message itself costs no second listing.
	// They are this package's own plumbing and never leave it.
	document  string
	signature string
}

// Action is what a party does next with an observed branch.
type Action string

const (
	// ActionNone is a branch this node has nothing to do with right now: work
	// it already answered, work addressed to a state it does not write, or a
	// chain whose shape nobody in the protocol could have produced.
	ActionNone Action = "none"
	// ActionClaim is an assignment this node may take.
	ActionClaim Action = "claim"
)

// ResolveWorkerAction decides what a serving node does with a branch it
// found, from the observed chain alone.
//
// It is pure and deliberately narrow: an assignment written directly on top
// of anything but a source commit is not an assignment this protocol
// produced, and everything else on a branch is somebody else's step. What the
// node then does with the claim it is allowed to make depends on the kind of
// work, on its free capacity and on the acceptance rules, none of which are
// questions about the chain.
func ResolveWorkerAction(tip ChainTip) Action {
	if tip.Kind != MessageAssignment {
		return ActionNone
	}
	if !IsTransitionLegal(tip.Previous, tip.Kind, PartyOrchestrator) {
		return ActionNone
	}
	return ActionClaim
}

// RejectReason is why a node did not act on a message, as a stable word.
//
// The reason is logged and the message never is: a rejected assignment is
// unauthenticated input, so echoing what it said would put whatever an
// attacker wrote into somebody's log. The words are an enum so that an
// operator, and a CI filter, can tell "somebody is signing with the wrong
// secret" from "somebody is replaying yesterday's work" without reading
// prose.
type RejectReason string

const (
	// ReasonSignature is a message this node's secret did not sign, which
	// includes a signature that is not hex and a missing one.
	ReasonSignature RejectReason = "signature"
	// ReasonNode is a message addressed to a different node, whatever the
	// branch it was found on suggests.
	ReasonNode RejectReason = "node"
	// ReasonBranch is a message whose own branch binding disagrees with the
	// ref it was found on, which is how a message copied onto another
	// attempt's branch is recognised (§28.4).
	ReasonBranch RejectReason = "branch"
	// ReasonProtocol is a message of a version this node does not implement.
	ReasonProtocol RejectReason = "protocol"
	// ReasonIssuedAt is a message outside the replay window, or one whose
	// timestamp is not a timestamp.
	ReasonIssuedAt RejectReason = "issued-at"
	// ReasonReplay is a (run, task, attempt) triple this node has already
	// answered.
	ReasonReplay RejectReason = "replay"
	// ReasonChain is a branch whose shape no legal sequence of pushes could
	// have produced.
	ReasonChain RejectReason = "chain"
	// ReasonOversize is a document larger than the transfer ceiling, refused
	// before it is read rather than after.
	ReasonOversize RejectReason = "oversize"
	// ReasonUnreadable is a tip carrying no message this protocol knows, or a
	// document that is not the JSON it claims to be.
	ReasonUnreadable RejectReason = "unreadable"
	// ReasonPermit is work an assignment describes without authorizing: a
	// publish task whose permits do not include publication. The permits are
	// the assignment's own statement of what the node may do beyond running
	// commands (§28.4), so a task that would have to exceed them is refused
	// before it is claimed rather than halfway through.
	ReasonPermit RejectReason = "permit"
)

// Rejection is one message a node will not act on.
//
// It is an error so that the transport failures and the refusals travel the
// same return value, and it carries the reason as a value so that the caller
// logs the enum rather than parsing a sentence.
type Rejection struct {
	Reason RejectReason
}

// Error is the sentence a reader is owed, which says what was refused and
// never what it contained.
func (r *Rejection) Error() string {
	return fmt.Sprintf("execution: the message was rejected (%s)", r.Reason)
}

// RejectionReason is the reason err names, and the empty string for an error
// that is not a rejection at all. Read by type because this package owns both
// halves; a transport failure is deliberately not a rejection, because
// "nothing could be read" and "what was read is not authentic" call for
// opposite reactions.
func RejectionReason(err error) RejectReason {
	var rejection *Rejection
	if !errors.As(err, &rejection) {
		return ""
	}
	return rejection.Reason
}

// replayWindow is how far either side of now an issuedAt may sit before the
// message is refused. It is generous because it bounds replay rather than
// scheduling: the clocks of two CI machines differ by seconds, a queued
// assignment may wait, and the branch's own date label is never parsed at all
// (§28.4).
const replayWindow = 24 * time.Hour

// Binding is what a reader requires of a message before acting on it: the
// node it must be addressed to, and the branch it must have been found on.
type Binding struct {
	Node   string
	Branch string
}

// CheckHeader applies the acceptance rules a message must satisfy whichever
// side reads it, in the order that refuses the cheapest mistakes first, and
// answers the empty reason for a message that satisfies all of them.
//
// The rules are one function rather than one per caller because both parties
// authenticate the same things about each other: the orchestrator reading a
// result has exactly this node's problem in reverse. What is not here is what
// differs: the freshness of a claim against a seen-set, and the orchestrator's
// own binding of a result to the assignment it answers.
func CheckHeader(header Header, binding Binding, now time.Time) RejectReason {
	if header.Protocol != ProtocolVersion {
		return ReasonProtocol
	}
	if header.Node != binding.Node {
		return ReasonNode
	}
	if header.Branch != binding.Branch {
		return ReasonBranch
	}
	issued, err := time.Parse(time.RFC3339, header.IssuedAt)
	if err != nil {
		return ReasonIssuedAt
	}
	if issued.Before(now.Add(-replayWindow)) || issued.After(now.Add(replayWindow)) {
		return ReasonIssuedAt
	}
	return ""
}
