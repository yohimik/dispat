// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import "fmt"

type compiledClaimSettlement uint8

const (
	compiledClaimUnsettled compiledClaimSettlement = iota
	compiledClaimSettled
	compiledClaimNeedsRefresh
)

const (
	compiledRefreshNoSequence       = 0
	compiledRefreshFenceErrorFormat = "execution: stale claim fence %s at sequence %d conflicts with the signed run feed"
)

type compiledClaimRefresh struct {
	fence ChoreographyEventCursor
}

func (run *workerCompiledRun) requeueStaleClaim(taskID string) error {
	task := run.frontier.tasks[taskID]
	if task == nil {
		return fmt.Errorf(frontierRefusedRequeueError)
	}
	// Exact reconciliation may lag an already-applied competing claim or
	// finality event. Its refusal cannot undo that authoritative task state.
	if task.state != frontierPending {
		return nil
	}
	return run.frontier.RequeueFenced(taskID)
}

func (run *workerCompiledRun) prepareClaimRefresh(proposal ChoreographyClaimProposal,
	receipt ChoreographyReceipt) (*compiledClaimRefresh, error) {
	if receipt.RefusalReason != ChoreographyRefusalStale {
		return run.claimRefresh, nil
	}
	fence := ChoreographyEventCursor{OID: receipt.FenceOID, Sequence: receipt.FenceSequence}
	cursor := run.frontier.Cursor()
	if fence.Sequence == compiledRefreshNoSequence ||
		proposal.ExpectedLedgerOID == cursor.OID && fence.Sequence <= cursor.Sequence ||
		fence.Sequence == cursor.Sequence && fence.OID != cursor.OID ||
		run.claimRefresh != nil && fence.Sequence == run.claimRefresh.fence.Sequence && fence.OID != run.claimRefresh.fence.OID {
		return nil, fmt.Errorf(compiledRefreshFenceErrorFormat, fence.OID, fence.Sequence)
	}
	if run.claimRefresh != nil && fence.Sequence < run.claimRefresh.fence.Sequence {
		return run.claimRefresh, nil
	}
	return &compiledClaimRefresh{fence: fence}, nil
}

func (run *workerCompiledRun) verifyClaimRefreshPage(page ChoreographyEventPage) error {
	if run.claimRefresh == nil {
		return nil
	}
	fence := run.claimRefresh.fence
	cursor := run.frontier.Cursor()
	if cursor.Sequence == fence.Sequence && cursor.OID != fence.OID ||
		page.Cursor.Sequence == fence.Sequence && page.Cursor.OID != fence.OID {
		return fmt.Errorf(compiledRefreshFenceErrorFormat, fence.OID, fence.Sequence)
	}
	for _, event := range page.Events {
		if event.Sequence == fence.Sequence && event.LedgerOID != fence.OID {
			return fmt.Errorf(compiledRefreshFenceErrorFormat, fence.OID, fence.Sequence)
		}
	}
	return nil
}

func (run *workerCompiledRun) completeClaimRefresh() {
	// Only the applied contiguous feed can advance this barrier. The receipt's
	// fence and an advisory page Head never replace the local frontier cursor.
	// A delayed pending receipt may name a fence the feed already passed.
	if run.claimRefresh != nil && run.frontier.Cursor().Sequence >= run.claimRefresh.fence.Sequence {
		run.claimRefresh = nil
	}
}
