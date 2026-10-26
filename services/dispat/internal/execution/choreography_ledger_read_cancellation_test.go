// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChoreographyLedgerReadPreservesCancellationAndRejectsUnsignedContent(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	root, err := readChoreographyLedgerRecord(t.Context(), fixture.mailbox, persist.opts.Ledger.OID, binding)
	require.NoError(t, err)
	require.Equal(t, binding, root.ChoreographyRunBinding)

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = readChoreographyLedgerRecord(cancelled, fixture.mailbox, persist.opts.Ledger.OID, binding)
	require.ErrorIs(t, err, context.Canceled)

	expired, cancelDeadline := context.WithDeadline(t.Context(), time.Time{})
	defer cancelDeadline()
	_, err = readChoreographyLedgerRecord(expired, fixture.mailbox, persist.opts.Ledger.OID, binding)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	_, err = readChoreographyLedgerRecord(t.Context(), fixture.mailbox, spec.ReservationOID, binding)
	require.ErrorContains(t, err, choreographyLedgerUnsignedObjectErr)
}
