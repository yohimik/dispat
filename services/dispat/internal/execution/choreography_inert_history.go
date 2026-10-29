// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import "fmt"

const (
	choreographyInertHistoryNoClaims  = 0
	choreographyInertHistoryNoWeight  = 0
	choreographyInertHistoryUnsettled = true
	choreographyInertHistorySettled   = false
)

type choreographyInertHistory struct {
	tip      string
	attempt  int
	offered  ChoreographyEvent
	terminal bool
	claims   map[string]ChoreographyEvent
	receipts map[string]ChoreographyEvent
}

func newChoreographyInertHistory(spec ChoreographyTaskSpec) *choreographyInertHistory {
	return &choreographyInertHistory{tip: spec.ReservationOID, attempt: choreographyInertCancelMinimumAttempt,
		claims: make(map[string]ChoreographyEvent), receipts: make(map[string]ChoreographyEvent)}
}

func (h *choreographyInertHistory) apply(spec ChoreographyTaskSpec, event ChoreographyEvent) error {
	switch event.Kind {
	case ChoreographyAssignmentOffered:
		h.offered = event
	case ChoreographyClaimAccepted:
		if event.TaskOID == choreographyInertCancelEmptyIdentity ||
			h.claims[event.TaskOID].TaskOID != choreographyInertCancelEmptyIdentity {
			return fmt.Errorf(choreographyInertCancelStateError)
		}
		h.claims[event.TaskOID] = event
		h.offered = ChoreographyEvent{}
	case ChoreographyResultAccepted, ChoreographyAckAccepted:
		claimOID := event.ClaimOID
		if event.PickupID == choreographyInertCancelEmptyIdentity && event.Kind == ChoreographyResultAccepted {
			claimOID = event.PriorTaskOID
		}
		if !h.matchesClaim(event, claimOID) {
			return fmt.Errorf(choreographyInertCancelStateError)
		}
		receiptOID := event.ResultReceiptOID
		if receiptOID == choreographyInertCancelEmptyIdentity {
			receiptOID = event.TaskOID
		}
		event.ClaimOID = claimOID
		h.receipts[receiptOID] = event
	case ChoreographyPickupContactLost:
		if !h.matchesClaim(event, event.ClaimOID) {
			return fmt.Errorf(choreographyInertCancelStateError)
		}
		h.attempt = event.Attempt + choreographyInertCancelMinimumAttempt
	case ChoreographyTerminal, ChoreographyRetryReady, ChoreographyPickupSettled:
		if err := h.settleClaim(spec, event); err != nil {
			return err
		}
		if event.Kind == ChoreographyRetryReady {
			h.attempt = event.Attempt + choreographyInertCancelMinimumAttempt
		}
		if event.Kind == ChoreographyTerminal {
			h.terminal = choreographyInertCancelTerminalState
		}
	case ChoreographyBlocked, ChoreographyNoClaimExpired, ChoreographyInertCancelled:
		h.terminal = choreographyInertCancelTerminalState
	}
	h.tip = event.TaskOID
	return nil
}

func (h *choreographyInertHistory) matchesClaim(event ChoreographyEvent, claimOID string) bool {
	claim, found := h.claims[claimOID]
	return found && claimOID != choreographyInertCancelEmptyIdentity &&
		claim.Task == event.Task && claim.Node == event.Node && claim.Attempt == event.Attempt &&
		claim.PickupID == event.PickupID
}

func (h *choreographyInertHistory) settleClaim(spec ChoreographyTaskSpec, event ChoreographyEvent) error {
	if (event.PickupID != choreographyInertCancelEmptyIdentity ||
		event.ResultReceiptOID != choreographyInertCancelEmptyIdentity || spec.LocalAuthority) &&
		(event.Budget != spec.Budget || event.Weight != spec.Weight) {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	if spec.LocalAuthority && event.Kind == ChoreographyTerminal {
		if !h.matchesClaim(event, event.PriorTaskOID) || event.ResultReceiptOID != choreographyInertCancelEmptyIdentity {
			return fmt.Errorf(choreographyInertCancelStateError)
		}
		delete(h.claims, event.PriorTaskOID)
		return nil
	}
	if event.ResultReceiptOID == choreographyInertCancelEmptyIdentity {
		if event.PickupID != choreographyInertCancelEmptyIdentity ||
			event.Kind == ChoreographyPickupSettled || h.offered.TaskOID == choreographyInertCancelEmptyIdentity ||
			event.Budget != choreographyInertCancelEmptyIdentity || event.Weight != choreographyInertHistoryNoWeight ||
			event.Node != h.offered.Node || event.Attempt != h.offered.Attempt {
			return fmt.Errorf(choreographyInertCancelStateError)
		}
		h.offered = ChoreographyEvent{}
		return nil
	}
	receipt, found := h.receipts[event.ResultReceiptOID]
	if !found || !h.matchesClaim(event, receipt.ClaimOID) || event.OutputOID != receipt.OutputOID ||
		event.PickupID != choreographyInertCancelEmptyIdentity && event.ClaimOID != receipt.ClaimOID {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	delete(h.claims, receipt.ClaimOID)
	delete(h.receipts, event.ResultReceiptOID)
	return nil
}

// All facts come from the authenticated prefix already indexed by the caller.
// A candidate inert event and any later settlement must not authorize itself.
func (g *GitChoreographyPersist) verifyInertPrefixLocked(outcome ChoreographyInertCancellation, sequence uint64) error {
	spec := g.tasks[outcome.Task]
	history := newChoreographyInertHistory(spec)
	for _, position := range g.taskEventPositionsLocked(outcome.Task) {
		event := g.events[position]
		if event.Sequence > sequence {
			break
		}
		if err := history.apply(spec, event); err != nil {
			return err
		}
	}
	if history.terminal || len(history.claims) != choreographyInertHistoryNoClaims ||
		history.offered.TaskOID != choreographyInertCancelEmptyIdentity ||
		history.tip != outcome.ExpectedOID || history.attempt != outcome.Attempt {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	return nil
}

func hasUnsettledChoreographyPickups(task *indexedChoreographyTask) bool {
	for _, pickup := range task.pickups {
		if !pickup.settled {
			return choreographyInertHistoryUnsettled
		}
	}
	return choreographyInertHistorySettled
}

func hasUnsettledFrontierPickups(task *choreographyFrontierTask) bool {
	for _, pickup := range task.pickups {
		if !pickup.settled {
			return choreographyInertHistoryUnsettled
		}
	}
	return choreographyInertHistorySettled
}
