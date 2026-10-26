// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBrokerPreservesRepeatedSharedGraphEdges(t *testing.T) {
	_, persist, _, build := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "build", Predecessors: []string{"gate"}},
		ChoreographyTaskSpec{Task: "root", Virtual: true},
		ChoreographyTaskSpec{Task: "gate", Virtual: true, Predecessors: []string{"root", "root"}})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	require.True(t, broker.tasks["root"].terminal)
	require.True(t, broker.tasks["gate"].terminal)
	require.Zero(t, broker.tasks["gate"].remaining)
	require.Zero(t, broker.tasks[build.Task].remaining)
}

func TestReadyInputIdentityPreservesRepeatedEdgesButRejectsConflict(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation",
		PlanDigest: "plan", GraphDigest: "graph"}
	one := ChoreographyInputFinality{Task: "root", OID: "terminal", Succeeded: true}
	single, err := ChoreographyReadyInputsDigest(binding, "consumer", []ChoreographyInputFinality{one})
	require.NoError(t, err)
	repeated, err := ChoreographyReadyInputsDigest(binding, "consumer", []ChoreographyInputFinality{one, one})
	require.NoError(t, err)
	require.NotEqual(t, single, repeated, "parallel edges remain part of the pinned input identity")
	_, err = ChoreographyReadyInputsDigest(binding, "consumer", []ChoreographyInputFinality{
		one, {Task: "root", OID: "foreign-terminal", Succeeded: true},
	})
	require.ErrorContains(t, err, "conflicting finality")
}
