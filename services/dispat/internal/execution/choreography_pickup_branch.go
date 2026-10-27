// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

const (
	choreographyPickupRandomBytes           = 16
	choreographyPickupHashPrefixBytes       = 16
	choreographyPickupIDHexChars            = choreographyPickupRandomBytes * 2
	choreographyInterpretedTimeHexChars     = 16
	choreographyInterpretedIDHexChars       = choreographyInterpretedTimeHexChars + choreographyPickupIDHexChars
	choreographyPickupPendingBranchPrefix   = "dispat-pending-"
	choreographyPickupOutputBranchPrefix    = "dispat-output-"
	choreographyPickupCompletedBranchPrefix = "dispat-completed-"
	choreographyPickupFailedBranchPrefix    = "dispat-failed-"
	choreographyPickupBranchSeparator       = "-"
	choreographyPickupMissingIdentity       = ""
	choreographyPickupIdentityError         = "execution: pickup branch lacks a valid run, task, node or unique pickup ID"
	choreographyPickupOutcomeStatusError    = "execution: pickup outcome branch has no executable success or failure status"
	choreographyPickupTimeFormat            = "20060102T150405.000000000Z"
	choreographyPickupTimeError             = "execution: interpreted pickup ID has no valid UTC timestamp"
	choreographyInterpretedIDFormat         = "%016x%s"
	choreographyInterpretedTimeRadix        = 16
	choreographyInterpretedTimeBits         = 64
	choreographyInterpretedUnixSeconds      = 0
)

// ChoreographyPickupIdentity names one physical pickup independently of the
// logical task attempt. Its ID is generated and retained before reserving the
// node slot, then reused for exact retries and cold recovery.
type ChoreographyPickupIdentity struct {
	Run      string `json:"run"`
	Task     string `json:"task"`
	Node     string `json:"node"`
	PickupID string `json:"pickupId"`
	Mode     string `json:"-"`
}

// NewChoreographyPickupID returns a cryptographically random pickup ID. The
// caller must persist this value before making a claim; generating another ID
// after an unknown reply would describe a different physical attempt.
func NewChoreographyPickupID() string {
	return formatRandomHex(choreographyPickupRandomBytes)
}

// NewInterpretedChoreographyPickupID binds a creation time to a 128-bit
// random identity. Time describes the ref but grants no execution authority.
func NewInterpretedChoreographyPickupID() string {
	return fmt.Sprintf(choreographyInterpretedIDFormat, time.Now().UTC().UnixNano(),
		formatRandomHex(choreographyPickupRandomBytes))
}

func newChoreographyPickupIDForMode(mode string) string {
	if mode == bootstrapInterpretationModeName {
		return NewInterpretedChoreographyPickupID()
	}
	return NewChoreographyPickupID()
}

func interpretedPickupTime(pickupID string) (time.Time, error) {
	if len(pickupID) != choreographyInterpretedIDHexChars {
		return time.Time{}, fmt.Errorf(choreographyPickupTimeError)
	}
	nanos, err := strconv.ParseInt(pickupID[:choreographyInterpretedTimeHexChars],
		choreographyInterpretedTimeRadix, choreographyInterpretedTimeBits)
	if err != nil || nanos <= choreographyInterpretedUnixSeconds {
		return time.Time{}, fmt.Errorf(choreographyPickupTimeError)
	}
	return time.Unix(choreographyInterpretedUnixSeconds, nanos).UTC(), nil
}

// ValidateChoreographyPickupID checks the mode-specific physical identity.
// Its timestamp is never a freshness, ordering, or finality check.
func ValidateChoreographyPickupID(mode, pickupID string) error {
	want := choreographyPickupIDHexChars
	if mode == bootstrapInterpretationModeName {
		want = choreographyInterpretedIDHexChars
	} else if mode != bootstrapCompilationModeName {
		return fmt.Errorf(choreographyPickupIdentityError)
	}
	if len(pickupID) != want {
		return fmt.Errorf(choreographyPickupIdentityError)
	}
	decoded, err := hex.DecodeString(pickupID)
	if err != nil || hex.EncodeToString(decoded) != pickupID {
		return fmt.Errorf(choreographyPickupIdentityError)
	}
	if mode == bootstrapInterpretationModeName {
		_, err = interpretedPickupTime(pickupID)
	}
	return err
}

func (identity ChoreographyPickupIdentity) effectiveMode() string {
	if identity.Mode != choreographyPickupMissingIdentity {
		return identity.Mode
	}
	if len(identity.PickupID) == choreographyInterpretedIDHexChars {
		return bootstrapInterpretationModeName
	}
	return bootstrapCompilationModeName
}

func (identity ChoreographyPickupIdentity) validate() error {
	if identity.Run == choreographyPickupMissingIdentity ||
		identity.Task == choreographyPickupMissingIdentity ||
		identity.Node == choreographyPickupMissingIdentity {
		return fmt.Errorf(choreographyPickupIdentityError)
	}
	return ValidateChoreographyPickupID(identity.effectiveMode(), identity.PickupID)
}

func choreographyPickupNamePart(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:choreographyPickupHashPrefixBytes])
}

func formatChoreographyPickupBranch(identity ChoreographyPickupIdentity, prefix string) (string, error) {
	if err := identity.validate(); err != nil {
		return choreographyPickupMissingIdentity, err
	}
	if identity.effectiveMode() == bootstrapInterpretationModeName {
		issuedAt, err := interpretedPickupTime(identity.PickupID)
		if err != nil {
			return choreographyPickupMissingIdentity, err
		}
		prefix += issuedAt.Format(choreographyPickupTimeFormat) + choreographyPickupBranchSeparator
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
		if identity.effectiveMode() == bootstrapInterpretationModeName {
			return formatChoreographyPickupBranch(identity, choreographyPickupCompletedBranchPrefix)
		}
		return formatChoreographyPickupBranch(identity, choreographyPickupOutputBranchPrefix)
	case StatusFailed, StatusCancelled:
		return formatChoreographyPickupBranch(identity, choreographyPickupFailedBranchPrefix)
	default:
		return choreographyPickupMissingIdentity, fmt.Errorf(choreographyPickupOutcomeStatusError)
	}
}
