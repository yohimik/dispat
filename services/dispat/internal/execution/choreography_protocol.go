// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The return channel carries proposals to the first node. A proposal is not
// an authorization to run: only a persisted, signed claim receipt is one.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	returnClaimPath                                          = "/dispat/choreography/v1/claim"
	returnResultPath                                         = "/dispat/choreography/v1/result"
	returnReceiptPath                                        = "/dispat/choreography/v1/receipt"
	returnProbePath                                          = "/dispat/choreography/v1/probe"
	returnAckPath                                            = "/dispat/choreography/v1/ack"
	returnEventsPath                                         = "/dispat/choreography/v1/events"
	returnProbeKind                              MessageKind = "choreography-probe-reply"
	returnAckKind                                MessageKind = "choreography-ack-proposal"
	returnClaimKind                              MessageKind = "choreography-claim-proposal"
	returnResultKind                             MessageKind = "choreography-result-proposal"
	returnQueryKind                              MessageKind = "choreography-receipt-query"
	returnReplyKind                              MessageKind = "choreography-receipt"
	returnEventsKind                             MessageKind = "choreography-event-query"
	returnPageKind                               MessageKind = "choreography-event-page"
	choreographyProtocolMissingField                         = ""
	choreographyProtocolFirstAttempt                         = 1
	choreographyProtocolMinimumLeaseWeight                   = 1
	choreographyProtocolNoPayloadBytes           int64       = 0
	choreographyProtocolPayloadDigestHexChars                = sha256.Size * 2
	choreographyProtocolClaimKeySizeError                    = "execution: claim identity private key has invalid size"
	choreographyProtocolClaimKeyEncodingError                = "execution: late member claim lacks valid node identity encoding"
	choreographyProtocolClaimSignatureError                  = "execution: late member claim signature differs from pinned node identity"
	choreographyProtocolClaimIncompleteError                 = "execution: incomplete choreography claim proposal"
	choreographyProtocolResultIncompleteError                = "execution: incomplete or oversized choreography result proposal"
	choreographyProtocolPayloadDigestError                   = "execution: invalid choreography payload digest"
	choreographyProtocolUnsupportedProposalError             = "execution: unsupported choreography proposal"
)

const (
	ChoreographyRefusalCapacity       = "capacity"
	ChoreographyRefusalNodeCapacity   = "capacity-node"
	ChoreographyRefusalBudgetCapacity = "capacity-budget"
	ChoreographyRefusalBusy           = "busy"
	ChoreographyRefusalNotReady       = "not-ready"
	ChoreographyRefusalStale          = "stale-epoch"
	ChoreographyRefusalTerminal       = "terminal"
	ChoreographyRefusalSealed         = "sealed"
	ChoreographyRefusalStopping       = "stopping"
)

// ChoreographyProbePath is the preflight route the first node serves before
// any task ref or broker exists. The app composes it with the task handler.
const ChoreographyProbePath = returnProbePath

const (
	ChoreographyClaimReceiptKind  = choreographyClaimKind
	ChoreographyResultReceiptKind = choreographyResultKind
)

// ChoreographyRunBinding is the pinned identity every node independently
// checks before proposing or persisting work. The graph digest names the
// locally reconstructed task graph; no task graph file is transported.
type ChoreographyRunBinding struct {
	Run         string `json:"run"`
	Generation  string `json:"generation"`
	PlanDigest  string `json:"planDigest"`
	GraphDigest string `json:"graphDigest"`
}

func (b ChoreographyRunBinding) valid() bool {
	return b.Run != choreographyProtocolMissingField && b.Generation != choreographyProtocolMissingField && b.PlanDigest != choreographyProtocolMissingField && b.GraphDigest != choreographyProtocolMissingField
}

// ChoreographyClaimProposal names one already selected ready task. Node is
// the selecting choreographer, which may be the first node itself. The first
// node checks the proposal against its indexed graph and exact task ref.
type ChoreographyClaimProposal struct {
	ChoreographyRunBinding
	Task                string                   `json:"task"`
	Attempt             int                      `json:"attempt"`
	Node                string                   `json:"node"`
	Branch              string                   `json:"branch"`
	ExpectedOID         string                   `json:"expectedOid"`
	ExpectedLedgerOID   string                   `json:"expectedLedgerOid"`
	GraphRootOID        string                   `json:"graphRootOid"`
	MembershipEpochOID  string                   `json:"membershipEpochOid"`
	CapacityAuthorityID string                   `json:"capacityAuthorityId"`
	LeaseIntentID       string                   `json:"leaseIntentId"`
	LeaseWeight         int                      `json:"leaseWeight"`
	IdentitySignature   string                   `json:"identitySignature,omitempty"`
	InputsDigest        string                   `json:"inputsDigest"`
	TemplateDigest      string                   `json:"templateDigest,omitempty"`
	BindingsDigest      string                   `json:"bindingsDigest,omitempty"`
	Bindings            ChoreographyTaskBindings `json:"bindings,omitempty"`
	ProposalID          string                   `json:"proposalId"`
	resolvedBindings    *ChoreographyTaskBindings
}

// ChoreographyClaimSignableBytes binds the node key to the entire immutable
// claim, including its durable local lease and membership epoch.
func ChoreographyClaimSignableBytes(p ChoreographyClaimProposal) ([]byte, error) {
	p.IdentitySignature = choreographyProtocolMissingField
	p.resolvedBindings = nil
	return json.Marshal(p)
}

func SignChoreographyClaimProposal(p *ChoreographyClaimProposal, key ed25519.PrivateKey) error {
	if p == nil || len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf(choreographyProtocolClaimKeySizeError)
	}
	document, err := ChoreographyClaimSignableBytes(*p)
	if err != nil {
		return err
	}
	p.IdentitySignature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, document))
	return nil
}

func VerifyChoreographyClaimIdentity(policy ChoreographyJoinPolicy, p ChoreographyClaimProposal) error {
	allowed, found := policy.AuthorizedNode(p.Node)
	if !found || !allowed.LateJoin {
		return nil
	}
	key, keyErr := base64.RawURLEncoding.DecodeString(allowed.IdentityPublicKey)
	signature, signatureErr := base64.RawURLEncoding.DecodeString(p.IdentitySignature)
	if keyErr != nil || signatureErr != nil || len(key) != ed25519.PublicKeySize ||
		len(signature) != ed25519.SignatureSize {
		return fmt.Errorf(choreographyProtocolClaimKeyEncodingError)
	}
	document, err := ChoreographyClaimSignableBytes(p)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(key), document, signature) {
		return fmt.Errorf(choreographyProtocolClaimSignatureError)
	}
	return nil
}

func (p ChoreographyClaimProposal) validate() error {
	if !p.ChoreographyRunBinding.valid() || p.Task == choreographyProtocolMissingField || p.Attempt < choreographyProtocolFirstAttempt ||
		p.Node == choreographyProtocolMissingField || p.Branch == choreographyProtocolMissingField || p.ExpectedOID == choreographyProtocolMissingField || p.ExpectedLedgerOID == choreographyProtocolMissingField ||
		p.InputsDigest == choreographyProtocolMissingField || p.GraphRootOID == choreographyProtocolMissingField || p.ProposalID == choreographyProtocolMissingField ||
		p.MembershipEpochOID == choreographyProtocolMissingField || p.CapacityAuthorityID == choreographyProtocolMissingField ||
		p.LeaseIntentID == choreographyProtocolMissingField || p.LeaseWeight < choreographyProtocolMinimumLeaseWeight {
		return fmt.Errorf(choreographyProtocolClaimIncompleteError)
	}
	return nil
}

// ChoreographyResultProposal binds streamed result objects to the claim the
// first node persisted. PayloadSHA256 and PayloadBytes are checked while the
// body is copied to bounded temporary storage, before it can be imported.
type ChoreographyResultProposal struct {
	ChoreographyRunBinding
	Task          string `json:"task"`
	Attempt       int    `json:"attempt"`
	Node          string `json:"node"`
	Branch        string `json:"branch"`
	ClaimOID      string `json:"claimOid"`
	InputsDigest  string `json:"inputsDigest"`
	ProposalID    string `json:"proposalId"`
	ResultDigest  string `json:"resultDigest"`
	PayloadBytes  int64  `json:"payloadBytes"`
	PayloadSHA256 string `json:"payloadSha256"`
}

func (p ChoreographyResultProposal) validate(maxBytes int64) error {
	if !p.ChoreographyRunBinding.valid() || p.Task == choreographyProtocolMissingField || p.Attempt < choreographyProtocolFirstAttempt ||
		p.Node == choreographyProtocolMissingField || p.Branch == choreographyProtocolMissingField || p.ClaimOID == choreographyProtocolMissingField ||
		p.InputsDigest == choreographyProtocolMissingField || p.ProposalID == choreographyProtocolMissingField || p.ResultDigest == choreographyProtocolMissingField ||
		p.PayloadBytes < choreographyProtocolNoPayloadBytes || p.PayloadBytes > maxBytes {
		return fmt.Errorf(choreographyProtocolResultIncompleteError)
	}
	if _, err := hex.DecodeString(p.PayloadSHA256); err != nil || len(p.PayloadSHA256) != choreographyProtocolPayloadDigestHexChars {
		return fmt.Errorf(choreographyProtocolPayloadDigestError)
	}
	return nil
}

// ChoreographyReceipt is signed by the first node after its Git CAS settles.
// Accepted means the exact claim or result was persisted. A failed attempt's
// result does not by itself declare the logical task terminal.
type ChoreographyReceipt struct {
	ChoreographyRunBinding
	Kind           MessageKind `json:"kind"`
	Task           string      `json:"task"`
	Attempt        int         `json:"attempt"`
	Node           string      `json:"node"`
	ProposalID     string      `json:"proposalId"`
	ProposalDigest string      `json:"proposalDigest"`
	Branch         string      `json:"branch"`
	OID            string      `json:"oid"`
	GraphRootOID   string      `json:"graphRootOid,omitempty"`
	AssignmentOID  string      `json:"assignmentOid,omitempty"`
	Accepted       bool        `json:"accepted"`
	// Cursor is the exact signed accepted-event position. A peer starts its
	// finality wait here, never at the run's root or a later ledger head.
	Cursor ChoreographyEventCursor `json:"cursor,omitempty"`
	// Refused is definitive only with a signed FenceOID naming a durable
	// ledger epoch after the proposal's ExpectedLedgerOID. Pending/unknown
	// responses never set it.
	Refused       bool   `json:"refused,omitempty"`
	FenceOID      string `json:"fenceOid,omitempty"`
	FenceSequence uint64 `json:"fenceSequence,omitempty"`
	RefusalReason string `json:"refusalReason,omitempty"`
	CapacityScope string `json:"capacityScope,omitempty"`
	Budget        string `json:"budget,omitempty"`
	Terminal      bool   `json:"terminal"`
}

// ChoreographyReceiptQuery settles an unanswered response without repeating
// the streamed payload or authorizing a different participant to adopt it.
type ChoreographyReceiptQuery struct {
	ChoreographyRunBinding
	GraphRootOID      string      `json:"graphRootOid,omitempty"`
	Kind              MessageKind `json:"kind"`
	Task              string      `json:"task"`
	Attempt           int         `json:"attempt"`
	Branch            string      `json:"branch"`
	Node              string      `json:"node"`
	ProposalID        string      `json:"proposalId"`
	ProposalDigest    string      `json:"proposalDigest"`
	ExpectedOID       string      `json:"expectedOid,omitempty"`
	ExpectedLedgerOID string      `json:"expectedLedgerOid,omitempty"`
}

// ChoreographyEventRequest asks for the earliest as-yet-unapplied signed
// frontier changes after one exact durable ledger cursor.
type ChoreographyEventRequest struct {
	ChoreographyRunBinding
	Node      string                  `json:"node"`
	Cursor    ChoreographyEventCursor `json:"cursor"`
	MaxEvents int                     `json:"maxEvents"`
}

// ChoreographyProbeReply answers a signed exact preflight assignment without
// giving the peer any Git push credential. The first node persists the probe
// claim and result before it accepts the node into this run's pool.
type ChoreographyProbeReply struct {
	ChoreographyRunBinding
	Node          string     `json:"node"`
	Branch        string     `json:"branch"`
	AssignmentOID string     `json:"assignmentOid"`
	Report        NodeReport `json:"report"`
}

// ChoreographyAckProposal acknowledges only the exact cancellation this
// choreographer observed. The first node CAS-pushes Ack after validation;
// a result/cancel race cannot be resolved by overwriting either state.
type ChoreographyAckProposal struct {
	ChoreographyRunBinding
	Task           string `json:"task"`
	Attempt        int    `json:"attempt"`
	Node           string `json:"node"`
	Branch         string `json:"branch"`
	CancelOID      string `json:"cancelOid"`
	AssignmentOID  string `json:"assignmentOid"`
	Phase          string `json:"phase,omitempty"`
	CommandStarted bool   `json:"commandStarted,omitempty"`
	ProposalID     string `json:"proposalId"`
}

// ChoreographyProposalDigest binds a proposal ID to every effective field.
// The JSON uses the protocol struct, so equivalent field order on the wire
// does not create a different logical proposal.
func ChoreographyProposalDigest(proposal any) (string, error) {
	switch proposal.(type) {
	case ChoreographyClaimProposal, ChoreographyResultProposal, ChoreographyAckProposal, ChoreographyDrainProposal:
	default:
		return choreographyProtocolMissingField, fmt.Errorf(choreographyProtocolUnsupportedProposalError)
	}
	document, err := json.Marshal(proposal)
	if err != nil {
		return choreographyProtocolMissingField, err
	}
	sum := sha256.Sum256(document)
	return hex.EncodeToString(sum[:]), nil
}

func FormatClaimReceiptQuery(p ChoreographyClaimProposal) (ChoreographyReceiptQuery, error) {
	digest, err := ChoreographyProposalDigest(p)
	return ChoreographyReceiptQuery{ChoreographyRunBinding: p.ChoreographyRunBinding,
		GraphRootOID: p.GraphRootOID,
		Kind:         choreographyClaimKind, Task: p.Task, Attempt: p.Attempt, Branch: p.Branch,
		Node: p.Node, ProposalID: p.ProposalID, ProposalDigest: digest,
		ExpectedOID: p.ExpectedOID, ExpectedLedgerOID: p.ExpectedLedgerOID}, err
}

func FormatResultReceiptQuery(p ChoreographyResultProposal) (ChoreographyReceiptQuery, error) {
	digest, err := ChoreographyProposalDigest(p)
	return ChoreographyReceiptQuery{ChoreographyRunBinding: p.ChoreographyRunBinding,
		Kind: choreographyResultKind, Task: p.Task, Attempt: p.Attempt, Branch: p.Branch,
		Node: p.Node, ProposalID: p.ProposalID, ProposalDigest: digest}, err
}

func FormatAckReceiptQuery(p ChoreographyAckProposal) (ChoreographyReceiptQuery, error) {
	digest, err := ChoreographyProposalDigest(p)
	return ChoreographyReceiptQuery{ChoreographyRunBinding: p.ChoreographyRunBinding,
		Kind: MessageAck, Task: p.Task, Attempt: p.Attempt, Branch: p.Branch,
		Node: p.Node, ProposalID: p.ProposalID, ProposalDigest: digest}, err
}
