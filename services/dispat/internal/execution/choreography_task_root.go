// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const choreographyTaskRefUnavailableErrorMessage = "execution: canonical task ref is unavailable"
const choreographyTaskRefReadErrorFormat = "execution: canonical task ref is unavailable: %w"
const choreographyTaskRootQueryErrorMessage = "execution: invalid choreography task root query"
const choreographyTaskRootAncestryErrorMessage = "execution: task ref misses pinned bootstrap ancestry"
const choreographyAbsentTaskRootIdentity = ""
const choreographyEmptyTaskRootHistoryCount = 0
const choreographyTaskRootHistoryFirstIndex = 0

type ChoreographyTaskRootQuery struct {
	Binding      ChoreographyRunBinding
	Task         string
	Kind         string
	BootstrapOID string
}

// ReadChoreographyTaskRoot finds the one inert signed reservation below a
// task's retained canonical ref. A cold peer calls it after independently
// deriving Task/Kind and authenticating the run bootstrap and ledger roster.
// This is a one-time O(history) read, not a scan on each ready selection.
func (m *GitMailbox) ReadChoreographyTaskRoot(ctx context.Context,
	query ChoreographyTaskRootQuery) (string, error) {
	if m == nil || !query.Binding.valid() || query.Task == choreographyAbsentTaskRootIdentity ||
		query.Kind == choreographyAbsentTaskRootIdentity || query.BootstrapOID == choreographyAbsentTaskRootIdentity {
		return choreographyAbsentTaskRootIdentity, fmt.Errorf(choreographyTaskRootQueryErrorMessage)
	}
	branch := FormatChoreographyTaskBranch(query.Binding.Run, query.Task)
	tip, err := m.readRemoteTip(ctx, branch)
	if err != nil {
		return choreographyAbsentTaskRootIdentity, fmt.Errorf(choreographyTaskRefReadErrorFormat, err)
	}
	if tip == choreographyAbsentTaskRootIdentity {
		return choreographyAbsentTaskRootIdentity, fmt.Errorf(choreographyTaskRefUnavailableErrorMessage)
	}
	if err := m.fetchRefs(ctx, []string{branch}); err != nil {
		return choreographyAbsentTaskRootIdentity, err
	}
	history, err := m.plumbing.FirstParentOIDs(ctx, tip, query.BootstrapOID)
	if err != nil || len(history) == choreographyEmptyTaskRootHistoryCount {
		return choreographyAbsentTaskRootIdentity, fmt.Errorf(choreographyTaskRootAncestryErrorMessage)
	}
	rootOID := history[choreographyTaskRootHistoryFirstIndex]
	expected := Reservation{Header: Header{
		Protocol: ChoreographyProtocolVersion, Kind: query.Kind,
		Run: query.Binding.Run, Generation: query.Binding.Generation,
		PlanDigest: query.Binding.PlanDigest, Task: query.Task,
		Attempt: minimumCompiledAttempt, Branch: branch,
	}, BootstrapOID: query.BootstrapOID, MaxAttempts: maxPlacementAttempts}
	if err := m.VerifyChoreographyReservation(ctx, rootOID, expected, returnReplyLimit); err != nil {
		return choreographyAbsentTaskRootIdentity, err
	}
	return rootOID, nil
}
