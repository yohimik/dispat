// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"fmt"
	"math"
	"time"
)

const (
	choreographyPickupNoDeadlineSeconds = 0
	choreographyPickupLastUTCYear       = 9999
	choreographyPickupDeadlineAbsent    = ""
	choreographyPickupDeadlineError     = "execution: pickup contact deadline is outside the signed assignment time range"
)

// choreographyPickupDeadline derives one contact-loss fence from the signed
// Assignment issuance and its immutable compiled timeout, so a lost Claim
// reply cannot create a later deadline by retrying the same proposal.
func choreographyPickupDeadline(issuedAt string, seconds int) (string, error) {
	if seconds == choreographyPickupNoDeadlineSeconds {
		return choreographyPickupDeadlineAbsent, nil
	}
	if seconds < choreographyPickupNoDeadlineSeconds ||
		int64(seconds) > math.MaxInt64/int64(time.Second) {
		return choreographyPickupDeadlineAbsent, fmt.Errorf(choreographyPickupDeadlineError)
	}
	issued, err := time.Parse(time.RFC3339, issuedAt)
	if err != nil {
		return choreographyPickupDeadlineAbsent, fmt.Errorf(choreographyPickupDeadlineError)
	}
	deadline := issued.Add(time.Duration(seconds) * time.Second)
	if !deadline.After(issued) || deadline.Year() > choreographyPickupLastUTCYear {
		return choreographyPickupDeadlineAbsent, fmt.Errorf(choreographyPickupDeadlineError)
	}
	return deadline.UTC().Format(time.RFC3339), nil
}
