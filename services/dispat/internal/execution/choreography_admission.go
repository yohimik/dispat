// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const choreographyAdmissionTimeFormat = time.RFC3339Nano
const ChoreographyGraphAlgorithmVersion = "indexed-graph-v4"
const nodePlatformSeparator = "/"
const choreographyJoinKind MessageKind = "choreography-join-proposal"
const choreographyJoinReplyKind MessageKind = "choreography-join-receipt"
const choreographyJoinQueryKind MessageKind = "choreography-join-query"
const choreographyJoinReplyMessageKind MessageKind = "choreography-join-receipt"
const returnJoinPath = "/dispat/choreography/v1/join"
const returnJoinReceiptPath = "/dispat/choreography/v1/join-receipt"

const (
	joinPolicyDeadlineErrorFormat         = "execution: choreography admission deadline is invalid: %w"
	joinPolicyZeroDeadlineErrorMessage    = "execution: choreography admission deadline is zero"
	joinPolicyEmptyAuthorizedErrorMessage = "execution: choreography admission policy has no authorized nodes"
	joinPolicyEndpointErrorFormat         = "execution: choreography canonical endpoint is invalid: %w"
	joinPolicyEntryErrorFormat            = "execution: choreography admission entry %q is duplicate or names another canonical endpoint"
	joinProposalIncompleteErrorMessage    = "execution: choreography join request lacks pinned identity, capacity or epoch"
	joinIdentityKeySizeErrorMessage       = "execution: join identity private key has invalid size"
	joinIdentityPolicyErrorMessage        = "execution: join identity is outside signed admission policy"
	joinIdentityEncodingErrorMessage      = "execution: join identity key or signature has invalid encoding"
	joinIdentitySignatureErrorMessage     = "execution: join identity signature does not authorize the node"
	maxChoreographyAdmissionSeconds       = models.MaxExecutionAdmissionTimeoutSeconds
	joinPolicyDurationErrorMessage        = "execution: choreography admission duration must be finite and at most seven days"
	joinPolicyMinimumDurationSeconds      = 1
	joinPolicyMinimumCapacitySlots        = 1
	joinPolicyNoAuthorizedNodes           = 0
	joinPolicyMissingIdentity             = ""
	joinPolicyNodeSeen                    = true
	joinPolicyNodeUnseen                  = false
	joinPolicyMissingIndex                = -1
)

// ChoreographyAuthorizedNode is a pinned allowlist entry, not an admitted
// member. A late node gains claim authority only through a signed ledger
// transition after its exact capabilities have been checked.
type ChoreographyAuthorizedNode struct {
	Name                 string `json:"name"`
	CoordinationEndpoint string `json:"coordinationEndpoint"`
	IdentityPublicKey    string `json:"identityPublicKey"`
	LateJoin             bool   `json:"lateJoin,omitempty"`
}

// ChoreographyJoinPolicy is fixed by the bootstrap before any task effect.
// AdmissionDurationSeconds is signed before global hooks. The execution-start
// ledger event fixes its absolute deadline once command execution is legal.
type ChoreographyJoinPolicy struct {
	AdmissionDurationSeconds   int                          `json:"admissionDurationSeconds"`
	RetryAfterUncertainAttempt bool                         `json:"retryAfterUncertainAttempt,omitempty"`
	Authorized                 []ChoreographyAuthorizedNode `json:"authorized"`
}

func (p ChoreographyJoinPolicy) Validate(canonicalEndpoint string) error {
	if p.AdmissionDurationSeconds < joinPolicyMinimumDurationSeconds || p.AdmissionDurationSeconds > maxChoreographyAdmissionSeconds {
		return fmt.Errorf(joinPolicyDurationErrorMessage)
	}
	if len(p.Authorized) == joinPolicyNoAuthorizedNodes {
		return fmt.Errorf(joinPolicyEmptyAuthorizedErrorMessage)
	}
	if err := gitx.RequireTransportEndpoint(canonicalEndpoint); err != nil {
		return fmt.Errorf(joinPolicyEndpointErrorFormat, err)
	}
	seen := make(map[string]bool, len(p.Authorized))
	for _, node := range p.Authorized {
		key, keyErr := base64.RawURLEncoding.DecodeString(node.IdentityPublicKey)
		if node.Name == joinPolicyMissingIdentity || seen[node.Name] || node.CoordinationEndpoint != canonicalEndpoint ||
			(node.LateJoin && (keyErr != nil || len(key) != ed25519.PublicKeySize)) ||
			(node.IdentityPublicKey != joinPolicyMissingIdentity && (keyErr != nil || len(key) != ed25519.PublicKeySize)) {
			return fmt.Errorf(joinPolicyEntryErrorFormat, node.Name)
		}
		seen[node.Name] = joinPolicyNodeSeen
	}
	return nil
}

func (p ChoreographyJoinPolicy) AuthorizedNode(name string) (ChoreographyAuthorizedNode, bool) {
	index := slices.IndexFunc(p.Authorized, func(node ChoreographyAuthorizedNode) bool { return node.Name == name })
	if index == joinPolicyMissingIndex {
		return ChoreographyAuthorizedNode{}, joinPolicyNodeUnseen
	}
	return p.Authorized[index], joinPolicyNodeSeen
}

// ChoreographyJoinProposal is a request to enter a fixed run graph. Its HMAC
// authenticates the node, while the first node's ledger CAS grants authority.
type ChoreographyJoinProposal struct {
	ChoreographyRunBinding
	BootstrapOID      string     `json:"bootstrapOid"`
	GraphRootOID      string     `json:"graphRootOid"`
	AlgorithmVersion  string     `json:"algorithmVersion"`
	Node              string     `json:"node"`
	Endpoint          string     `json:"endpoint"`
	Report            NodeReport `json:"report"`
	IdentitySignature string     `json:"identitySignature"`
	RequestID         string     `json:"requestId"`
	ExpectedLedgerOID string     `json:"expectedLedgerOid"`
}

func (p ChoreographyJoinProposal) validate() error {
	if !p.ChoreographyRunBinding.valid() || p.BootstrapOID == joinPolicyMissingIdentity ||
		p.GraphRootOID == joinPolicyMissingIdentity || p.AlgorithmVersion == joinPolicyMissingIdentity || p.Node == joinPolicyMissingIdentity ||
		p.Endpoint == joinPolicyMissingIdentity || p.RequestID == joinPolicyMissingIdentity || p.ExpectedLedgerOID == joinPolicyMissingIdentity ||
		p.Report.Capacity < joinPolicyMinimumCapacitySlots || p.Report.AuthorityID == joinPolicyMissingIdentity ||
		p.Report.OS == joinPolicyMissingIdentity || p.Report.Arch == joinPolicyMissingIdentity || p.IdentitySignature == joinPolicyMissingIdentity {
		return fmt.Errorf(joinProposalIncompleteErrorMessage)
	}
	return nil
}

func ChoreographyJoinSignableBytes(p ChoreographyJoinProposal) ([]byte, error) {
	p.IdentitySignature = joinPolicyMissingIdentity
	return json.Marshal(p)
}

func SignChoreographyJoinProposal(p *ChoreographyJoinProposal, key ed25519.PrivateKey) error {
	if p == nil || len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf(joinIdentityKeySizeErrorMessage)
	}
	document, err := ChoreographyJoinSignableBytes(*p)
	if err != nil {
		return err
	}
	p.IdentitySignature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, document))
	return nil
}

func VerifyChoreographyJoinIdentity(policy ChoreographyJoinPolicy,
	p ChoreographyJoinProposal) error {
	allowed, found := policy.AuthorizedNode(p.Node)
	if !found || allowed.CoordinationEndpoint != p.Endpoint {
		return fmt.Errorf(joinIdentityPolicyErrorMessage)
	}
	key, keyErr := base64.RawURLEncoding.DecodeString(allowed.IdentityPublicKey)
	signature, signatureErr := base64.RawURLEncoding.DecodeString(p.IdentitySignature)
	if keyErr != nil || signatureErr != nil || len(key) != ed25519.PublicKeySize ||
		len(signature) != ed25519.SignatureSize {
		return fmt.Errorf(joinIdentityEncodingErrorMessage)
	}
	document, err := ChoreographyJoinSignableBytes(p)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(key), document, signature) {
		return fmt.Errorf(joinIdentitySignatureErrorMessage)
	}
	return nil
}

func DigestChoreographyJoinProposal(p ChoreographyJoinProposal) (string, error) {
	if err := p.validate(); err != nil {
		return joinPolicyMissingIdentity, err
	}
	document, err := json.Marshal(p)
	if err != nil {
		return joinPolicyMissingIdentity, err
	}
	sum := sha256.Sum256(document)
	return hex.EncodeToString(sum[:]), nil
}

type ChoreographyJoinReceipt struct {
	ChoreographyRunBinding
	BootstrapOID  string                  `json:"bootstrapOid"`
	GraphRootOID  string                  `json:"graphRootOid"`
	Node          string                  `json:"node"`
	RequestID     string                  `json:"requestId"`
	RequestDigest string                  `json:"requestDigest"`
	Member        ChoreographyRuntimeNode `json:"member"`
	Accepted      bool                    `json:"accepted"`
	Refused       bool                    `json:"refused,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
	Cursor        ChoreographyEventCursor `json:"cursor"`
}

type ChoreographyJoinReceiptQuery struct {
	ChoreographyRunBinding
	BootstrapOID      string `json:"bootstrapOid"`
	GraphRootOID      string `json:"graphRootOid"`
	Node              string `json:"node"`
	RequestID         string `json:"requestId"`
	RequestDigest     string `json:"requestDigest"`
	ExpectedLedgerOID string `json:"expectedLedgerOid"`
}

func FormatChoreographyJoinReceiptQuery(p ChoreographyJoinProposal) (ChoreographyJoinReceiptQuery, error) {
	digest, err := DigestChoreographyJoinProposal(p)
	return ChoreographyJoinReceiptQuery{ChoreographyRunBinding: p.ChoreographyRunBinding,
		BootstrapOID: p.BootstrapOID, GraphRootOID: p.GraphRootOID,
		Node: p.Node, RequestID: p.RequestID, RequestDigest: digest,
		ExpectedLedgerOID: p.ExpectedLedgerOID}, err
}
