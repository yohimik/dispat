// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	choreographyCloseAdmissionFenceErrorFormat = "execution: choreography admission could not be fenced: %w"
	choreographyClosePendingTaskErrorFormat    = "execution: choreography task %s still awaits task or physical-slot settlement"
	choreographyCloseUnresolvedErrorFormat     = "execution: unresolved choreography work retains coordination refs: %w"
	choreographyCloseProbeErrorFormat          = "execution: preflight probe cleanup: %w"
	choreographyCloseProbeRetainedErrorFormat  = "execution: preflight probe %s remains after choreography seal"
	choreographyCloseUnsealedRefsError         = "execution: canonical task refs cannot close before signed run seal"
	choreographyCloseUnknownTaskError          = "execution: ledger names unknown task ref"
	choreographyCloseUnknownTransitionError    = "execution: unknown task-ref transition"
	choreographyCloseTipMismatchErrorFormat    = "execution: canonical task ref %s differs from signed ledger tip"
	choreographyCloseNoTaskIdentity            = ""
	choreographyCloseStopPendingScan           = false
	choreographyCloseNormalSeal                = false
	choreographyCloseAbortedSeal               = true
	choreographyCloseNoCanonicalTasks          = 0
)

// closeChoreography first persists a stop-admission fence, then seals only a
// quiescent run. The ledger, bootstrap, task refs and peer index stay retained
// for exact receipt/feed reconciliation; probe refs can be removed because
// the retained bootstrap supplies cold-start identity. An active attempt
// makes Close fail closed with its refs intact.
func (c *Coordinator) closeChoreography(ctx context.Context) error {
	if c.stopWatching != nil {
		c.stopWatching()
		c.watching.Wait()
		c.stopWatching = nil
	}
	if _, err := c.choreographyBroker.StopClaims(ctx); err != nil {
		return fmt.Errorf(choreographyCloseAdmissionFenceErrorFormat, err)
	}
	var pendingTask string
	c.choreographyPending.Range(func(key, _ any) bool {
		pendingTask = key.(string)
		return choreographyCloseStopPendingScan
	})
	if pendingTask != choreographyCloseNoTaskIdentity {
		return fmt.Errorf(choreographyClosePendingTaskErrorFormat, pendingTask)
	}
	if _, err := c.choreographyPersist.SealRun(ctx, choreographyCloseNormalSeal); err != nil {
		if _, abortErr := c.choreographyPersist.SealRun(ctx, choreographyCloseAbortedSeal); abortErr != nil {
			return fmt.Errorf(choreographyCloseUnresolvedErrorFormat, abortErr)
		}
	}
	c.mu.Lock()
	probes := make(map[string][]gitx.BranchLease)
	for node, leases := range c.owned {
		for _, lease := range leases {
			if strings.HasPrefix(lease.Branch, branchPrefix) {
				probes[node] = append(probes[node], lease)
			}
		}
	}
	c.mu.Unlock()
	for _, node := range sortedNodes(probes) {
		outcomes, err := c.mailboxes[node].Close(ctx, probes[node])
		if err != nil {
			return fmt.Errorf(choreographyCloseProbeErrorFormat, err)
		}
		for _, outcome := range outcomes {
			if outcome.Result != gitx.BranchDeleted {
				return fmt.Errorf(choreographyCloseProbeRetainedErrorFormat, outcome.Branch)
			}
		}
	}
	return nil
}

// CanonicalTaskLeases returns exact task-ref tips only after a signed final
// seal. It excludes the ledger ref: the signed feed, receipts and bootstrap
// carrier must remain readable until every consumer reaches the separate
// settlement gate. No bounded v2 maxChainDepth proof is used for v3 cleanup.
func (g *GitChoreographyPersist) CanonicalTaskLeases(ctx context.Context) ([]gitx.BranchLease, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.syncLedgerLocked(ctx); err != nil {
		return nil, err
	}
	if !g.sealed || !g.stopping {
		return nil, fmt.Errorf(choreographyCloseUnsealedRefsError)
	}
	expected := make(map[string]string, len(g.tasks))
	for task, spec := range g.tasks {
		expected[task] = spec.ReservationOID
	}
	for _, event := range g.events {
		if _, exists := expected[event.Task]; !exists {
			if event.Task != choreographyRunTaskID && g.localTasks[event.Task].Task == choreographyCloseNoTaskIdentity {
				return nil, fmt.Errorf(choreographyCloseUnknownTaskError)
			}
			continue
		}
		switch event.Kind {
		case ChoreographyAssignmentOffered, ChoreographyClaimAccepted, ChoreographyResultAccepted,
			ChoreographyCancelIssued, ChoreographyQueueFenced, ChoreographyAckAccepted,
			ChoreographyRetryReady, ChoreographyTerminal, ChoreographyBlocked,
			ChoreographyNoClaimExpired, ChoreographyInertCancelled:
			expected[event.Task] = event.TaskOID
		case ChoreographyClaimRefused, ChoreographyFrameReady,
			ChoreographyNoClaimPackageSettled, ChoreographyInertCancelSettled,
			ChoreographyTaskPackageSettled:
			// These transitions change only the ledger epoch.
		default:
			return nil, fmt.Errorf(choreographyCloseUnknownTransitionError)
		}
	}
	names := make([]string, choreographyCloseNoCanonicalTasks, len(g.tasks))
	for task := range g.tasks {
		names = append(names, task)
	}
	sort.Strings(names)
	leases := make([]gitx.BranchLease, choreographyCloseNoCanonicalTasks, len(names))
	for _, task := range names {
		branch := g.tasks[task].Branch
		actual, err := g.opts.Mailbox.readRemoteTip(ctx, branch)
		if err != nil {
			return nil, err
		}
		if actual == choreographyCloseNoTaskIdentity {
			continue // a prior exact cleanup already removed this ref
		}
		if actual != expected[task] {
			return nil, fmt.Errorf(choreographyCloseTipMismatchErrorFormat, branch)
		}
		leases = append(leases, gitx.BranchLease{Branch: branch, ExpectedOld: actual})
	}
	return leases, nil
}
