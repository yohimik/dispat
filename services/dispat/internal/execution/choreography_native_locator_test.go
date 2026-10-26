// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeNodeSlotLocatorBindsExactPreparedTaskAndLedgerRoots(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "build", EligibleNodes: []string{"node-a"}})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	c := &Coordinator{Local: LocalNode{Name: "node-a"},
		BootstrapOID:            persist.opts.BootstrapOID,
		GraphRootOID:            persist.opts.GraphRootOID,
		Links:                   []Link{{Name: "node-a", Endpoint: fixture.endpoint}},
		mailboxes:               map[string]*GitMailbox{"node-a": fixture.mailbox},
		canonicalMailbox:        fixture.mailbox,
		choreographyInitialized: true, choreographyPersist: persist,
		choreographyBroker: broker}
	locator, err := c.NativeNodeSlotLocator(t.Context(), spec.Task)
	require.NoError(t, err)
	require.Equal(t, binding, locator.Binding)
	require.Equal(t, spec.Branch, locator.TaskBranch)
	require.Equal(t, spec.ReservationOID, locator.ReservationOID)
	require.Equal(t, persist.opts.Ledger.OID, locator.LedgerRootOID)
	require.Equal(t, persist.opts.BootstrapOID, locator.BootstrapOID)
	require.Equal(t, fixture.endpoint, locator.CanonicalEndpoint)

	broker.tasks[spec.Task].tip = "other"
	_, err = c.NativeNodeSlotLocator(t.Context(), spec.Task)
	require.Error(t, err, "a moved task root cannot be captured as a pre-effect intent")
}
