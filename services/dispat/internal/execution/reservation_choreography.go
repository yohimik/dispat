// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	choreographyReservationMissingOID           = ""
	choreographyReservationMinimumDocumentBytes = 0
	choreographyReservationFirstAttempt         = 1
	choreographyReservationBootstrapParentCount = 1
	choreographyReservationBootstrapParentIndex = 0
	choreographyReservationWriteErrorFormat     = "execution: writing choreography reservation: %w"
	choreographyReservationIncompleteReadError  = "execution: incomplete choreography reservation read"
	choreographyReservationInvalidError         = "execution: invalid choreography reservation"
	choreographyReservationInvalidTimeError     = "execution: invalid choreography reservation time"
)

// PrepareChoreographyReservation retains the signed run bootstrap through
// the one authoritative branch of a logical task. The root is inert; a later
// claim still needs an exact first-node CAS and receipt.
func (m *GitMailbox) PrepareChoreographyReservation(ctx context.Context,
	reservation *Reservation) (string, error) {
	if err := validateChoreographyReservation(reservation); err != nil {
		return choreographyReservationMissingOID, err
	}
	document, err := json.Marshal(reservation)
	if err != nil {
		return choreographyReservationMissingOID, fmt.Errorf(choreographyReservationWriteErrorFormat, err)
	}
	return m.commitMessage(ctx, MessageReservation, document,
		[]string{reservation.BootstrapOID}, nil)
}

// VerifyChoreographyReservation authenticates the root and its exact
// bootstrap parent before the first node accepts any attempt on this task.
func (m *GitMailbox) VerifyChoreographyReservation(ctx context.Context, oid string,
	expected Reservation, maxBytes int64) error {
	if oid == choreographyReservationMissingOID || maxBytes <= choreographyReservationMinimumDocumentBytes || expected.BootstrapOID == choreographyReservationMissingOID {
		return fmt.Errorf(choreographyReservationIncompleteReadError)
	}
	parents, err := m.plumbing.CommitParents(ctx, oid)
	if err != nil {
		return err
	}
	if len(parents) != choreographyReservationBootstrapParentCount || parents[choreographyReservationBootstrapParentIndex] != expected.BootstrapOID {
		return &Rejection{Reason: ReasonChain}
	}
	bootstrap, err := m.readTree(ctx, parents[choreographyReservationBootstrapParentIndex])
	if err != nil {
		return err
	}
	if bootstrap.kind != choreographyBootstrapKind {
		return &Rejection{Reason: ReasonChain}
	}
	tree, err := m.readTree(ctx, oid)
	if err != nil {
		return err
	}
	if tree.kind != MessageReservation {
		return &Rejection{Reason: ReasonChain}
	}
	document, err := m.Read(ctx, ChainTip{OID: oid, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, maxBytes)
	if err != nil {
		return err
	}
	var actual Reservation
	if err := json.Unmarshal(document, &actual); err != nil {
		return &Rejection{Reason: ReasonUnreadable}
	}
	// IssuedAt is signed but not a stable planner fact. A restarted first node
	// may reconstruct the expected reservation from the run and task spec.
	if expected.IssuedAt == choreographyReservationMissingOID {
		expected.IssuedAt = actual.IssuedAt
	}
	if validateChoreographyReservation(&actual) != nil ||
		validateChoreographyReservation(&expected) != nil ||
		actual.Header != expected.Header || actual.BootstrapOID != expected.BootstrapOID ||
		actual.MaxAttempts != expected.MaxAttempts {
		return &Rejection{Reason: ReasonChain}
	}
	return nil
}

func validateChoreographyReservation(reservation *Reservation) error {
	if reservation == nil || reservation.Protocol != ChoreographyProtocolVersion ||
		reservation.Run == choreographyReservationMissingOID || reservation.Generation == choreographyReservationMissingOID ||
		reservation.PlanDigest == choreographyReservationMissingOID || reservation.Kind == choreographyReservationMissingOID ||
		reservation.Task == choreographyReservationMissingOID || reservation.Attempt != choreographyReservationFirstAttempt || reservation.Node != choreographyReservationMissingOID ||
		reservation.Branch != FormatChoreographyTaskBranch(reservation.Run, reservation.Task) ||
		reservation.BootstrapOID == choreographyReservationMissingOID || !validChoreographyAttemptLimit(reservation.MaxAttempts) {
		return fmt.Errorf(choreographyReservationInvalidError)
	}
	if _, err := time.Parse(time.RFC3339, reservation.IssuedAt); err != nil {
		return fmt.Errorf(choreographyReservationInvalidTimeError)
	}
	return nil
}
