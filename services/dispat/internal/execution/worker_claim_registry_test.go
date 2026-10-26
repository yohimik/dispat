// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkerClaimRegistrySeparatesSuccessorFromRetainedPickup(t *testing.T) {
	worker := &Worker{}
	old := &claimedTask{tip: ChainTip{Branch: "task"}, claimed: "old-claim", expectedTip: "old-claim"}
	successor := &claimedTask{tip: ChainTip{Branch: "task"}, claimed: "new-claim", expectedTip: "new-claim"}
	forgetOld := worker.registerClaim(old)
	forgetSuccessor := worker.registerClaim(successor)
	oldCancel := ChainTip{Branch: "task", PreviousOID: "old-claim"}
	newCancel := ChainTip{Branch: "task", PreviousOID: "new-claim"}
	require.Same(t, old, worker.findWithdrawalClaim(oldCancel))
	require.Same(t, successor, worker.findWithdrawalClaim(newCancel))
	require.Nil(t, worker.findWithdrawalClaim(ChainTip{Branch: "other-task", PreviousOID: "old-claim"}))
	forgetOld()
	require.Nil(t, worker.findWithdrawalClaim(oldCancel))
	require.Same(t, successor, worker.findWithdrawalClaim(newCancel),
		"settling the old pickup must preserve the successor's cancellation registration")
	forgetOld()
	require.Same(t, successor, worker.findWithdrawalClaim(newCancel))
	successor.advanceTip("authorization")
	require.Nil(t, worker.findWithdrawalClaim(newCancel))
	require.Same(t, successor, worker.findWithdrawalClaim(ChainTip{Branch: "task", PreviousOID: "authorization"}))
	forgetSuccessor()
	require.Empty(t, worker.claimed)
}
