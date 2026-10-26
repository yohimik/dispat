// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const (
	choreographyPickupRandomBytes         = 16
	choreographyPickupHashPrefixBytes     = 16
	choreographyPickupIDHexChars          = choreographyPickupRandomBytes * 2
	choreographyPickupPendingBranchPrefix = "dispat-pending-"
	choreographyPickupOutputBranchPrefix  = "dispat-output-"
	choreographyPickupFailedBranchPrefix  = "dispat-failed-"
	choreographyPickupBranchSeparator     = "-"
	choreographyPickupMissingIdentity     = ""
	choreographyPickupIdentityError       = "execution: pickup branch lacks a valid run, task, node or unique pickup ID"
	choreographyPickupOutcomeStatusError  = "execution: pickup outcome branch has no executable success or failure status"
)

// ChoreographyPickupIdentity names one physical pickup independently of the
// logical task attempt. Its ID is generated and retained before reserving the
// node slot, then reused for exact retries and cold recovery.
type ChoreographyPickupIdentity struct {
	Run      string `json:"run"`
	Task     string `json:"task"`
	Node     string `json:"node"`
	PickupID string `json:"pickupId"`
}

// NewChoreographyPickupID returns a cryptographically random pickup ID. The
// caller must persist this value before making a claim; generating another ID
// after an unknown reply would describe a different physical attempt.
func NewChoreographyPickupID() string {
	return formatRandomHex(choreographyPickupRandomBytes)
}

func (identity ChoreographyPickupIdentity) validate() error {
	if identity.Run == choreographyPickupMissingIdentity ||
		identity.Task == choreographyPickupMissingIdentity ||
		identity.Node == choreographyPickupMissingIdentity ||
		len(identity.PickupID) != choreographyPickupIDHexChars {
		return fmt.Errorf(choreographyPickupIdentityError)
	}
	decoded, err := hex.DecodeString(identity.PickupID)
	if err != nil || hex.EncodeToString(decoded) != identity.PickupID {
		return fmt.Errorf(choreographyPickupIdentityError)
	}
	return nil
}

func choreographyPickupNamePart(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:choreographyPickupHashPrefixBytes])
}

func formatChoreographyPickupBranch(identity ChoreographyPickupIdentity, prefix string) (string, error) {
	if err := identity.validate(); err != nil {
		return choreographyPickupMissingIdentity, err
	}
	return prefix + choreographyPickupNamePart(identity.Run) + choreographyPickupBranchSeparator +
		choreographyPickupNamePart(identity.Task) + choreographyPickupBranchSeparator +
		choreographyPickupNamePart(identity.Node) + choreographyPickupBranchSeparator +
		identity.PickupID, nil
}

// FormatChoreographyPendingBranch names the immutable branch a worker creates
// from its exact accepted Claim tip before it begins command effects.
func FormatChoreographyPendingBranch(identity ChoreographyPickupIdentity) (string, error) {
	return formatChoreographyPickupBranch(identity, choreographyPickupPendingBranchPrefix)
}

// FormatChoreographyOutcomeBranch names the separate output or failed branch
// whose signed commit descends from this pickup's immutable pending commit.
func FormatChoreographyOutcomeBranch(identity ChoreographyPickupIdentity, status string) (string, error) {
	switch status {
	case StatusSucceeded:
		return formatChoreographyPickupBranch(identity, choreographyPickupOutputBranchPrefix)
	case StatusFailed, StatusCancelled:
		return formatChoreographyPickupBranch(identity, choreographyPickupFailedBranchPrefix)
	default:
		return choreographyPickupMissingIdentity, fmt.Errorf(choreographyPickupOutcomeStatusError)
	}
}
