// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompiledClaimRejectsSwappedGenericStageForExactTaskID(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "build", Kind: KindStage, Stage: "test"})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
		ProposalID: "swapped-stage-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	swapped := persist.opts.Templates[0]
	swapped.Stage = "lint"
	claim.TemplateDigest, err = DigestChoreographyCompiledTemplate(swapped)
	require.NoError(t, err)
	_, err = broker.AcceptClaim(t.Context(), claim)
	require.ErrorContains(t, err, "template")
	require.Equal(t, spec.ReservationOID, broker.tasks[spec.Task].tip,
		"a swapped stage may not advance the canonical task ref")
	require.Equal(t, spec.ReservationOID, remoteChoreographyOID(t, fixture, spec.Branch))
}
