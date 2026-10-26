// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// PrepareChoreographyReservation retains the signed run bootstrap through
// the one authoritative branch of a logical task. The root is inert; a later
// claim still needs an exact first-node CAS and receipt.
func (m *GitMailbox) PrepareChoreographyReservation(ctx context.Context,
	reservation *Reservation) (string, error) {
	if err := validateChoreographyReservation(reservation); err != nil {
		return "", err
	}
	document, err := json.Marshal(reservation)
	if err != nil {
		return "", fmt.Errorf("execution: writing choreography reservation: %w", err)
	}
	return m.commitMessage(ctx, MessageReservation, document,
		[]string{reservation.BootstrapOID}, nil)
}

// VerifyChoreographyReservation authenticates the root and its exact
// bootstrap parent before the first node accepts any attempt on this task.
func (m *GitMailbox) VerifyChoreographyReservation(ctx context.Context, oid string,
	expected Reservation, maxBytes int64) error {
	if oid == "" || maxBytes <= 0 || expected.BootstrapOID == "" {
		return fmt.Errorf("execution: incomplete choreography reservation read")
	}
	parents, err := m.plumbing.CommitParents(ctx, oid)
	if err != nil {
		return err
	}
	if len(parents) != 1 || parents[0] != expected.BootstrapOID {
		return &Rejection{Reason: ReasonChain}
	}
	bootstrap, err := m.readTree(ctx, parents[0])
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
	if expected.IssuedAt == "" {
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
		reservation.Run == "" || reservation.Generation == "" ||
		reservation.PlanDigest == "" || reservation.Kind == "" ||
		reservation.Task == "" || reservation.Attempt != 1 || reservation.Node != "" ||
		reservation.Branch != FormatChoreographyTaskBranch(reservation.Run, reservation.Task) ||
		reservation.BootstrapOID == "" || reservation.MaxAttempts != maxPlacementAttempts {
		return fmt.Errorf("execution: invalid choreography reservation")
	}
	if _, err := time.Parse(time.RFC3339, reservation.IssuedAt); err != nil {
		return fmt.Errorf("execution: invalid choreography reservation time")
	}
	return nil
}
