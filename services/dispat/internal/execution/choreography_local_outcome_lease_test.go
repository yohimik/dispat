// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutcomeHookReusesOnlyExactStoppedFirstLocalStage(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation",
		PlanDigest: "plan", GraphDigest: "graph"}
	assignment := strings.Repeat("a", 40)
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	slots, err := OpenNodeSlotAuthority(root, "first", 1)
	require.NoError(t, err)
	intent := NodeSlotIntent{Run: binding.Run, Task: "api:build", Attempt: 1, Weight: 1,
		Recovery: &NodeSlotRecoveryLocator{Binding: binding, Node: "first",
			CanonicalEndpoint: "file:///canonical", BootstrapOID: strings.Repeat("b", 40),
			TaskBranch: FormatChoreographyTaskBranch(binding.Run, "api:build"),
			Kind:       KindBuild, AssignmentOID: assignment,
			ExpectedLedgerOID: strings.Repeat("c", 40),
			ProposalDigest:    strings.Repeat("d", 64)}}
	_, acquired, err := slots.TryReserve(t.Context(), intent)
	require.NoError(t, err)
	require.True(t, acquired)
	c := &Coordinator{Local: LocalNode{Name: "first"},
		choreographyBroker: &FirstNodeBroker{binding: binding}}
	pending := &choreographyPendingDecision{packageName: "api", assignmentOID: assignment,
		decision: ChoreographyDecision{Task: intent.Task, Attempt: 1},
		lease:    &Lease{IsLocal: true}}
	c.choreographyPending.Store(intent.Task, pending)
	reusable, err := c.HasStoppedFirstLocalStage(t.Context(), "api", 1, slots)
	require.NoError(t, err)
	require.False(t, reusable, "a running Claim has no accepted stopped command")
	pending.decision.ResultOID = strings.Repeat("e", 40)
	pending.resultAccepted = true
	reusable, err = c.HasStoppedFirstLocalStage(t.Context(), "api", 1, slots)
	require.NoError(t, err)
	require.True(t, reusable)
	pending.resultAccepted, pending.ackAccepted = false, true
	reusable, err = c.HasStoppedFirstLocalStage(t.Context(), "api", 1, slots)
	require.NoError(t, err)
	require.True(t, reusable, "an exact signed Ack proves the cancelled command stopped")
	pending.decision.Unclaimed = true
	reusable, err = c.HasStoppedFirstLocalStage(t.Context(), "api", 1, slots)
	require.NoError(t, err)
	require.False(t, reusable, "an unclaimed queue fence proves no local command stopped")
	pending.decision.Unclaimed = false
	reusable, err = c.HasStoppedFirstLocalStage(t.Context(), "api", 2, slots)
	require.NoError(t, err)
	require.False(t, reusable, "a lighter stage lease cannot authorize a heavier outcome hook")
	pending.assignmentOID = strings.Repeat("f", 40)
	reusable, err = c.HasStoppedFirstLocalStage(t.Context(), "api", 1, slots)
	require.NoError(t, err)
	require.False(t, reusable, "another assignment's physical slot is not reusable")
}
