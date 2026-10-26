// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChoreographyReservationRetainsAndAuthenticatesExactBootstrap(t *testing.T) {
	fixture := newMailboxFixture(t)
	ctx := t.Context()
	bootstrapOID, err := fixture.mailbox.commitMessage(ctx, choreographyBootstrapKind,
		[]byte(`{"run":"run"}`), nil, nil)
	require.NoError(t, err)
	reservation := Reservation{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: "run", Generation: "generation", PlanDigest: "plan",
		Task: "core:build", Attempt: 1,
		Branch:   FormatChoreographyTaskBranch("run", "core:build"),
		IssuedAt: time.Now().UTC().Format(time.RFC3339)},
		BootstrapOID: bootstrapOID, MaxAttempts: maxPlacementAttempts}
	oid, err := fixture.mailbox.PrepareChoreographyReservation(ctx, &reservation)
	require.NoError(t, err)
	require.NoError(t, fixture.mailbox.VerifyChoreographyReservation(ctx, oid, reservation, 1<<20))
	restarted := reservation
	restarted.IssuedAt = ""
	require.NoError(t, fixture.mailbox.VerifyChoreographyReservation(ctx, oid, restarted, 1<<20))
	parents, err := fixture.git.CommitParents(ctx, oid)
	require.NoError(t, err)
	require.Equal(t, []string{bootstrapOID}, parents)

	wrong := reservation
	wrong.BootstrapOID = oid
	require.Error(t, fixture.mailbox.VerifyChoreographyReservation(ctx, oid, wrong, 1<<20))
	wrong = reservation
	wrong.MaxAttempts--
	require.Error(t, fixture.mailbox.VerifyChoreographyReservation(ctx, oid, wrong, 1<<20))

	invalid := reservation
	invalid.BootstrapOID = ""
	_, err = fixture.mailbox.PrepareChoreographyReservation(ctx, &invalid)
	require.Error(t, err)
	invalid = reservation
	invalid.MaxAttempts = 0
	_, err = fixture.mailbox.PrepareChoreographyReservation(ctx, &invalid)
	require.Error(t, err)

	// A signed document cannot invent the bootstrap relation; the exact
	// commit parent is independently checked.
	document, err := json.Marshal(reservation)
	require.NoError(t, err)
	detached, err := fixture.mailbox.commitMessage(ctx, MessageReservation, document, nil, nil)
	require.NoError(t, err)
	require.Error(t, fixture.mailbox.VerifyChoreographyReservation(ctx, detached, reservation, 1<<20))
}
