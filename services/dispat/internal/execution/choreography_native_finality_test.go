// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetainedNativeBlockedProofBindsCanonicalReservation(t *testing.T) {
	fixture, original, binding, spec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "build", Package: "api"})
	opts := original.opts
	persist, err := NewGitChoreographyPersist(opts)
	require.NoError(t, err)
	_, err = persist.LedgerHead(t.Context())
	require.NoError(t, err)
	blocked := ChoreographyBlockedOutcome{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, ExpectedOID: spec.ReservationOID,
		Package: "api", BlockedBy: "provider", PackageOutcome: "skipped"}
	oid, err := persist.PersistBlocked(t.Context(), blocked)
	require.NoError(t, err)
	page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 4)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, oid, page.Events[0].TaskOID)
	locator := NodeSlotNativeRecoveryLocator{Binding: binding,
		CanonicalEndpoint: fixture.endpoint, BootstrapOID: opts.BootstrapOID,
		LedgerRootOID: opts.Ledger.OID, TaskBranch: spec.Branch,
		ReservationOID: spec.ReservationOID, Kind: spec.Kind, Node: "node-a"}
	reader, err := NewGitChoreographyPersist(opts)
	require.NoError(t, err)
	feed := &RetainedChoreographyFeed{reader: reader, maxDocumentBytes: 1 << 20}
	require.NoError(t, feed.VerifyNativeTaskFinality(t.Context(), spec.Task,
		locator, page.Events[0]))
	wrong := locator
	wrong.ReservationOID = opts.BootstrapOID
	require.Error(t, feed.VerifyNativeTaskFinality(t.Context(), spec.Task,
		wrong, page.Events[0]))
}

func TestRetainedFirstOnlyBlockedProofBindsSignedLocalTaskRef(t *testing.T) {
	fixture, original, binding, spec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "api:publish", Package: "api", Kind: "publish",
			LocalAuthority: true, ExecutorOwned: true, Budget: "build", Weight: 1,
			EligibleNodes: []string{"node-a"}})
	opts := original.opts
	persist, err := NewGitChoreographyPersist(opts)
	require.NoError(t, err)
	_, err = persist.LedgerHead(t.Context())
	require.NoError(t, err)
	blocked := ChoreographyBlockedOutcome{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, ExpectedOID: spec.ReservationOID,
		Package: "api", BlockedBy: "provider",
		PackageOutcome: "skipped"}
	_, err = persist.PersistBlocked(t.Context(), blocked)
	require.NoError(t, err)
	page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 4)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	locator := NodeSlotNativeRecoveryLocator{Binding: binding,
		CanonicalEndpoint: fixture.endpoint, BootstrapOID: opts.BootstrapOID,
		LedgerRootOID: opts.Ledger.OID, TaskBranch: spec.Branch,
		ReservationOID: spec.ReservationOID, Kind: spec.Kind, Node: "node-a"}
	reader, err := NewGitChoreographyPersist(opts)
	require.NoError(t, err)
	reader.localTasks = nil
	feed := &RetainedChoreographyFeed{reader: reader, maxDocumentBytes: 1 << 20}
	require.NoError(t, feed.VerifyNativeTaskFinality(t.Context(), spec.Task,
		locator, page.Events[0]))
}
