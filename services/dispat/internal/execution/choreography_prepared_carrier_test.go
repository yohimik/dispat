// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreparedCarrierColdReadRetainsExactSourceParents(t *testing.T) {
	fixture := newMailboxFixture(t)
	ctx := t.Context()
	graphRoot, err := fixture.mailbox.commitMessage(ctx, choreographyBootstrapKind,
		[]byte(`{"root":true}`), nil, nil)
	require.NoError(t, err)
	snapshot, err := fixture.mailbox.commitMessage(ctx, choreographyBootstrapKind,
		[]byte(`{"source":true}`), nil, nil)
	require.NoError(t, err)
	carrier := ChoreographyPreparedCarrier{
		ChoreographyRunBinding: ChoreographyRunBinding{Run: "run", Generation: "generation",
			PlanDigest: "plan", GraphDigest: "graph"},
		GraphRootOID: graphRoot, Task: "core:build", TemplateDigest: "template",
		PredecessorResultOIDs: []string{"prior-result"},
		Bindings: ChoreographyTaskBindings{
			Repositories:          []AssignmentRepository{{Path: ".", Snapshot: snapshot}},
			PredecessorResultOIDs: []string{"prior-result"},
		},
	}
	oid, err := fixture.mailbox.PrepareChoreographyPreparedCarrier(ctx, carrier, 1<<20)
	require.NoError(t, err)
	reloaded, err := fixture.mailbox.ReadChoreographyPreparedCarrier(ctx, oid, 1<<20)
	require.NoError(t, err)
	require.Equal(t, carrier, reloaded)
	require.Equal(t, snapshot, reloaded.SourceSnapshot(""))

	document, err := json.Marshal(carrier)
	require.NoError(t, err)
	forgedParent, err := fixture.mailbox.commitMessage(ctx, choreographyPreparedCarrierKind,
		document, []string{graphRoot}, nil)
	require.NoError(t, err)
	_, err = fixture.mailbox.ReadChoreographyPreparedCarrier(ctx, forgedParent, 1<<20)
	require.ErrorContains(t, err, "differs from its graph")
}
