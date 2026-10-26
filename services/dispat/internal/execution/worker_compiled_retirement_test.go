// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSealedCompiledRunRetiresOnlyAfterLocalClaimAndProposalSettle(t *testing.T) {
	binding := testReturnBinding()
	channel := &workerReturnChannel{binding: binding, client: &fakeWorkerReturn{}}
	run := &workerCompiledRun{binding: binding, channel: channel,
		frontier: &ChoreographyFrontier{sealed: true},
		pending:  map[string]*workerCompiledClaim{"task": {}}}
	w := &Worker{compiledRuns: map[string]*workerCompiledRun{binding.Run: run},
		returnChannels: map[string]*workerReturnChannel{binding.Run: channel},
		claimed: map[string]*claimedTask{"branch": {assignment: Assignment{
			Header: Header{Run: binding.Run}}}}}
	retired, err := w.retireSealedCompiledRun(t.Context(), run)
	require.NoError(t, err)
	require.False(t, retired)
	delete(run.pending, "task")
	retired, err = w.retireSealedCompiledRun(t.Context(), run)
	require.NoError(t, err)
	require.False(t, retired)
	delete(w.claimed, "branch")
	retired, err = w.retireSealedCompiledRun(t.Context(), run)
	require.NoError(t, err)
	require.True(t, retired)
	require.Empty(t, w.compiledRuns)
	require.Empty(t, w.returnChannels)
}
