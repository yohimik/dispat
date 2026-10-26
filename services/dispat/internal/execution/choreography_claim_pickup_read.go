// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const (
	choreographyClaimPickupReadError       = "execution: accepted claim has no exact signed pickup identity"
	choreographyClaimPickupMissingIdentity = ""
)

func (m *GitMailbox) readChoreographyClaimPickupID(ctx context.Context,
	claimOID string) (string, error) {
	data, err := readChoreographyFinalityDocument(ctx, choreographyFinalityDocumentQuery{
		Mailbox: m, OID: claimOID, Kind: choreographyClaimKind,
		MaxBytes: returnReplyLimit})
	if err != nil {
		return choreographyClaimPickupMissingIdentity, err
	}
	var claim ChoreographyClaimProposal
	if strictJSON(data, &claim) != nil || claim.PickupID != choreographyClaimPickupMissingIdentity &&
		(ChoreographyPickupIdentity{Run: claim.Run, Task: claim.Task,
			Node: claim.Node, PickupID: claim.PickupID}).validate() != nil {
		return choreographyClaimPickupMissingIdentity, fmt.Errorf(choreographyClaimPickupReadError)
	}
	return claim.PickupID, nil
}
