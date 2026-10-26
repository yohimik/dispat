// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const (
	choreographyTaskLedgerAncestryError            = "execution: canonical task tip is outside exact signed ledger ancestry"
	choreographyTaskLedgerEventError               = "execution: signed ledger task transition differs from complete graph"
	choreographyTaskLedgerNoOID                    = ""
	choreographyTaskLedgerMinimumHistoryCommits    = 2
	choreographyTaskLedgerMinimumEventParents      = 2
	choreographyTaskLedgerMaximumEventParents      = 3
	choreographyTaskLedgerPriorParentIndex         = 0
	choreographyTaskLedgerTaskParentIndex          = 1
	choreographyTaskLedgerPreparedParentIndex      = 2
	choreographyTaskLedgerFirstEventIndex          = 1
	choreographyTaskLedgerInitializationEventIndex = 1
	choreographyTaskLedgerNextEventIndex           = 1
)

// readChoreographyTaskLedgerTips projects one signed ledger history onto the
// complete root's task refs. A cold reader compares these tips with remote
// refs, so a signed but unadmitted task commit cannot masquerade as progress.
type choreographyTaskLedgerProjection struct {
	Binding      ChoreographyRunBinding
	BootstrapOID string
	GraphRootOID string
	Tasks        []ChoreographyTaskSpec
}

func (m *GitMailbox) readChoreographyTaskLedgerTips(ctx context.Context,
	projection choreographyTaskLedgerProjection) (map[string]string, error) {
	binding, bootstrapOID, graphRootOID, tasks := projection.Binding,
		projection.BootstrapOID, projection.GraphRootOID, projection.Tasks
	branch := FormatChoreographyLedgerBranch(binding.Run)
	head, err := m.readRemoteTip(ctx, branch)
	if err != nil {
		return nil, err
	}
	if head == choreographyTaskLedgerNoOID {
		return nil, fmt.Errorf(choreographyTaskLedgerAncestryError)
	}
	if err := m.fetchRefs(ctx, []string{branch}); err != nil {
		return nil, err
	}
	history, err := m.plumbing.FirstParentOIDs(ctx, head, bootstrapOID)
	if err != nil {
		return nil, err
	}
	if len(history) < choreographyTaskLedgerMinimumHistoryCommits {
		return nil, fmt.Errorf(choreographyTaskLedgerAncestryError)
	}
	tips := make(map[string]string, len(tasks))
	for _, task := range tasks {
		if !task.Virtual {
			tips[task.Task] = task.ReservationOID
		}
	}
	for index := choreographyTaskLedgerFirstEventIndex; index < len(history); index++ {
		oid := history[index]
		parents, err := m.plumbing.CommitParents(ctx, oid)
		if err != nil {
			return nil, err
		}
		if len(parents) < choreographyTaskLedgerMinimumEventParents || len(parents) > choreographyTaskLedgerMaximumEventParents ||
			parents[choreographyTaskLedgerPriorParentIndex] != history[index-choreographyTaskLedgerNextEventIndex] {
			return nil, fmt.Errorf(choreographyTaskLedgerAncestryError)
		}
		tree, err := m.readTree(ctx, oid)
		if err != nil {
			return nil, err
		}
		if tree.kind != choreographyLedgerKind {
			return nil, fmt.Errorf(choreographyTaskLedgerAncestryError)
		}
		bytes, err := m.Read(ctx, ChainTip{OID: oid, Kind: tree.kind,
			document: tree.document, signature: tree.signature}, returnReplyLimit)
		if err != nil {
			return nil, err
		}
		var record choreographyLedgerRecord
		if err := strictJSON(bytes, &record); err != nil {
			return nil, err
		}
		if record.ChoreographyRunBinding != binding || record.Sequence != uint64(index) ||
			record.Event == nil || record.Event.Sequence != uint64(index) ||
			parents[choreographyTaskLedgerTaskParentIndex] != record.Event.TaskOID {
			return nil, fmt.Errorf(choreographyTaskLedgerEventError)
		}
		event := record.Event
		if event.Kind == ChoreographyFrameReady && event.PreparedInputOID != choreographyTaskLedgerNoOID &&
			(len(parents) != choreographyTaskLedgerMaximumEventParents ||
				parents[choreographyTaskLedgerPreparedParentIndex] != event.PreparedInputOID) {
			return nil, fmt.Errorf(choreographyTaskLedgerEventError)
		}
		if ((event.Kind == ChoreographyResultAccepted || event.Kind == ChoreographyAckAccepted) &&
			event.PickupID != choreographyTaskLedgerNoOID &&
			event.ResultReceiptOID != choreographyTaskLedgerNoOID ||
			event.Kind == ChoreographyPickupSettled) &&
			(len(parents) != choreographyTaskLedgerMaximumEventParents ||
				event.ResultReceiptOID == choreographyTaskLedgerNoOID ||
				parents[choreographyTaskLedgerPreparedParentIndex] != event.ResultReceiptOID) {
			return nil, fmt.Errorf(choreographyTaskLedgerEventError)
		}
		if index == choreographyTaskLedgerInitializationEventIndex {
			if event.Kind != ChoreographyInitializationComplete || event.TaskOID != graphRootOID ||
				event.GraphRootOID != graphRootOID {
				return nil, fmt.Errorf(choreographyTaskLedgerAncestryError)
			}
			continue
		}
		prior, taskKnown := tips[event.Task]
		if !taskKnown {
			continue
		}
		switch event.Kind {
		case ChoreographyAssignmentOffered, ChoreographyClaimAccepted,
			ChoreographyCancelIssued, ChoreographyQueueFenced,
			ChoreographyRetryReady, ChoreographyTerminal,
			ChoreographyBlocked, ChoreographyNoClaimExpired, ChoreographyInertCancelled:
			if event.PriorTaskOID != prior || event.TaskOID == prior {
				return nil, fmt.Errorf(choreographyTaskLedgerEventError)
			}
			tips[event.Task] = event.TaskOID
		case ChoreographyAckAccepted:
			if event.PickupID != choreographyTaskLedgerNoOID &&
				event.ResultReceiptOID != choreographyTaskLedgerNoOID {
				if event.PriorTaskOID != prior || event.TaskOID != prior {
					return nil, fmt.Errorf(choreographyTaskLedgerEventError)
				}
				continue
			}
			if event.PriorTaskOID != prior || event.TaskOID == prior {
				return nil, fmt.Errorf(choreographyTaskLedgerEventError)
			}
			tips[event.Task] = event.TaskOID
		case ChoreographyResultAccepted:
			if event.PickupID != choreographyTaskLedgerNoOID {
				if event.PriorTaskOID != prior || event.TaskOID != prior {
					return nil, fmt.Errorf(choreographyTaskLedgerEventError)
				}
				continue
			}
			if event.PriorTaskOID != prior || event.TaskOID == prior {
				return nil, fmt.Errorf(choreographyTaskLedgerEventError)
			}
			tips[event.Task] = event.TaskOID
		case ChoreographyClaimRefused, ChoreographyFrameReady, ChoreographyNoClaimPackageSettled,
			ChoreographyTaskPackageSettled,
			ChoreographyInertCancelSettled, ChoreographyPickupContactLost,
			ChoreographyPickupSettled:
			if event.TaskOID != prior {
				return nil, fmt.Errorf(choreographyTaskLedgerEventError)
			}
		default:
			return nil, fmt.Errorf(choreographyTaskLedgerEventError)
		}
	}
	return tips, nil
}
