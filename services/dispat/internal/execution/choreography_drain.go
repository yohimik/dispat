// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

const (
	choreographyDrainKind               MessageKind = "choreography-drain-proposal"
	choreographyDrainQueryKind          MessageKind = "choreography-drain-query"
	choreographyDrainReplyKind          MessageKind = "choreography-drain-receipt"
	choreographyMemberDrainKind         MessageKind = "choreography-member-drain"
	returnDrainPath                                 = "/dispat/choreography/v1/drain"
	returnDrainReceiptPath                          = "/dispat/choreography/v1/drain-receipt"
	choreographyDrainInvalidError                   = "execution: drain request lacks exact admitted node identity or ledger epoch"
	choreographyDrainIdentityError                  = "execution: drain identity signature differs from pinned late member"
	choreographyDrainRefusalStopping                = "run-stopping"
	choreographyDrainRefusalStale                   = "stale-membership-epoch"
	choreographyDrainRefusalAlready                 = "already-draining"
	choreographyDrainMembershipDraining             = true
	choreographyDrainSignedRefusal                  = true
	choreographyDrainLookupMissing                  = false
)

type ChoreographyDrainProposal struct {
	ChoreographyRunBinding
	BootstrapOID        string `json:"bootstrapOid"`
	GraphRootOID        string `json:"graphRootOid"`
	Node                string `json:"node"`
	CapacityAuthorityID string `json:"capacityAuthorityId"`
	MembershipEpochOID  string `json:"membershipEpochOid"`
	ExpectedLedgerOID   string `json:"expectedLedgerOid"`
	RequestID           string `json:"requestId"`
	IdentitySignature   string `json:"identitySignature,omitempty"`
}

func (p ChoreographyDrainProposal) validate() error {
	if !p.ChoreographyRunBinding.valid() || p.BootstrapOID == choreographyDrainMissingIdentity || p.GraphRootOID == choreographyDrainMissingIdentity ||
		p.Node == choreographyDrainMissingIdentity || p.CapacityAuthorityID == choreographyDrainMissingIdentity || p.MembershipEpochOID == choreographyDrainMissingIdentity ||
		p.ExpectedLedgerOID == choreographyDrainMissingIdentity || p.RequestID == choreographyDrainMissingIdentity {
		return fmt.Errorf(choreographyDrainInvalidError)
	}
	return nil
}

func ChoreographyDrainSignableBytes(p ChoreographyDrainProposal) ([]byte, error) {
	p.IdentitySignature = choreographyDrainMissingIdentity
	return json.Marshal(p)
}

func SignChoreographyDrainProposal(p *ChoreographyDrainProposal, key ed25519.PrivateKey) error {
	if p == nil || len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf(choreographyDrainIdentityError)
	}
	bytes, err := ChoreographyDrainSignableBytes(*p)
	if err != nil {
		return err
	}
	p.IdentitySignature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, bytes))
	return nil
}

func VerifyChoreographyDrainIdentity(policy ChoreographyJoinPolicy, p ChoreographyDrainProposal) error {
	allowed, found := policy.AuthorizedNode(p.Node)
	if !found {
		return fmt.Errorf(choreographyDrainIdentityError)
	}
	if !allowed.LateJoin {
		return nil
	}
	key, keyErr := base64.RawURLEncoding.DecodeString(allowed.IdentityPublicKey)
	signature, signatureErr := base64.RawURLEncoding.DecodeString(p.IdentitySignature)
	if keyErr != nil || signatureErr != nil || len(key) != ed25519.PublicKeySize ||
		len(signature) != ed25519.SignatureSize {
		return fmt.Errorf(choreographyDrainIdentityError)
	}
	bytes, err := ChoreographyDrainSignableBytes(p)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(key), bytes, signature) {
		return fmt.Errorf(choreographyDrainIdentityError)
	}
	return nil
}

type ChoreographyDrainReceipt struct {
	ChoreographyRunBinding
	BootstrapOID  string                  `json:"bootstrapOid"`
	GraphRootOID  string                  `json:"graphRootOid"`
	Node          string                  `json:"node"`
	RequestID     string                  `json:"requestId"`
	RequestDigest string                  `json:"requestDigest"`
	Accepted      bool                    `json:"accepted"`
	Refused       bool                    `json:"refused,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
	Cursor        ChoreographyEventCursor `json:"cursor"`
}

type ChoreographyDrainReceiptQuery struct {
	ChoreographyRunBinding
	BootstrapOID      string `json:"bootstrapOid"`
	GraphRootOID      string `json:"graphRootOid"`
	Node              string `json:"node"`
	RequestID         string `json:"requestId"`
	RequestDigest     string `json:"requestDigest"`
	ExpectedLedgerOID string `json:"expectedLedgerOid"`
}

func FormatChoreographyDrainReceiptQuery(p ChoreographyDrainProposal) (ChoreographyDrainReceiptQuery, error) {
	if err := p.validate(); err != nil {
		return ChoreographyDrainReceiptQuery{}, err
	}
	digest, err := ChoreographyProposalDigest(p)
	if err != nil {
		return ChoreographyDrainReceiptQuery{}, err
	}
	return ChoreographyDrainReceiptQuery{ChoreographyRunBinding: p.ChoreographyRunBinding,
		BootstrapOID: p.BootstrapOID, GraphRootOID: p.GraphRootOID, Node: p.Node,
		RequestID: p.RequestID, RequestDigest: digest, ExpectedLedgerOID: p.ExpectedLedgerOID}, nil
}

func (b *FirstNodeBroker) DrainNode(ctx context.Context, p ChoreographyDrainProposal) (ChoreographyDrainReceipt, error) {
	if err := p.validate(); err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	if p.ChoreographyRunBinding != b.binding || p.GraphRootOID != b.graphRootOID {
		return ChoreographyDrainReceipt{}, fmt.Errorf(choreographyDrainInvalidError)
	}
	if err := VerifyChoreographyDrainIdentity(b.joinPolicy, p); err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	query, err := FormatChoreographyDrainReceiptQuery(p)
	if err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	persist, ok := b.persist.(interface {
		PersistDrain(context.Context, ChoreographyDrainProposal, ChoreographyRuntimeNode) (ChoreographyDrainReceipt, error)
		LookupDrainReceipt(context.Context, ChoreographyDrainReceiptQuery) (ChoreographyDrainReceipt, bool, error)
	})
	if !ok {
		return ChoreographyDrainReceipt{}, fmt.Errorf(choreographyDrainInvalidError)
	}
	if old, found, err := persist.LookupDrainReceipt(ctx, query); err != nil {
		return ChoreographyDrainReceipt{}, err
	} else if found {
		return old, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	member := b.members[p.Node]
	if member.Name == choreographyDrainMissingIdentity || member.First || member.AuthorityID != p.CapacityAuthorityID ||
		b.memberEpoch[p.Node] != p.MembershipEpochOID {
		return ChoreographyDrainReceipt{}, fmt.Errorf(choreographyDrainInvalidError)
	}
	if b.stopping || b.sealed || b.draining[p.Node] {
		head, err := b.persist.LedgerHead(ctx)
		if err != nil {
			return ChoreographyDrainReceipt{}, err
		}
		reason := choreographyDrainRefusalAlready
		if b.stopping || b.sealed {
			reason = choreographyDrainRefusalStopping
		}
		return ChoreographyDrainReceipt{ChoreographyRunBinding: p.ChoreographyRunBinding,
			BootstrapOID: p.BootstrapOID, GraphRootOID: p.GraphRootOID, Node: p.Node,
			RequestID: p.RequestID, RequestDigest: query.RequestDigest, Refused: choreographyDrainSignedRefusal,
			Reason: reason, Cursor: head}, nil
	}
	receipt, err := persist.PersistDrain(ctx, p, member)
	if err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	if receipt.Accepted {
		b.draining[p.Node] = choreographyDrainMembershipDraining
		b.memberEpoch[p.Node] = receipt.Cursor.OID
		b.wakeBudgetWaiters()
	}
	return receipt, nil
}

func (b *FirstNodeBroker) LookupDrainReceipt(ctx context.Context, q ChoreographyDrainReceiptQuery) (ChoreographyDrainReceipt, bool, error) {
	persist, ok := b.persist.(interface {
		LookupDrainReceipt(context.Context, ChoreographyDrainReceiptQuery) (ChoreographyDrainReceipt, bool, error)
	})
	if !ok || q.ChoreographyRunBinding != b.binding || q.GraphRootOID != b.graphRootOID {
		return ChoreographyDrainReceipt{}, choreographyDrainLookupMissing, fmt.Errorf(choreographyDrainInvalidError)
	}
	return persist.LookupDrainReceipt(ctx, q)
}
