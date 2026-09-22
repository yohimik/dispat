// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The orchestrator's half of cancellation, and what capacity means once an
// attempt has stopped answering (CCME §28.2, §28.6).
//
// One sentence of the specification decides the shape of everything here: a
// timeout alone cannot free capacity for a possibly overlapping attempt. A
// wait that elapsed says what this run has stopped expecting and says nothing
// at all about the machine at the other end, so a slot comes back on evidence
// and never on a clock. There are exactly three pieces of evidence, and each of
// them is something a party wrote down: a result, an acknowledged withdrawal,
// and a coordination ref this run revoked before anybody had claimed it.
//
// The third is the cheapest and the most useful, and it is why an assignment
// that merely queued costs a run nothing. An attempt nobody claimed performed
// no effect and holds no authorization, so deleting its ref is a fence rather
// than a hope (§28.6): whatever the node does afterwards, its
// expected-previous-object-ID push can no longer succeed. The slot is free, the
// node is healthy, and the task is offered again under an attempt of its own.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// The reasons a node's slot is held and the node taken out of the pool. They
// are an enum because the line that reports one is read by an operator looking
// for which machine to go and look at, and by a CI filter that has to tell a
// killed worker from a run that was interrupted.
const (
	// LeakTaskDeadline is an attempt that did not report within the run's own
	// wait. The work may still be running there, so the slot is not free.
	LeakTaskDeadline = "task-deadline"
	// LeakUnacknowledgedCancel is an attempt this run withdrew and never heard
	// the end of, which is the same statement one step further on.
	LeakUnacknowledgedCancel = "cancel-unacknowledged"
	// LeakTransport is an attempt whose branch could not be written or read at
	// all, so nothing is known about what the node is doing.
	LeakTransport = "transport"
)

// errQueueExpired is the one failure of a dispatched attempt that is not a
// failure of its task: an assignment that waited out the run's own wait
// without anybody claiming it, and was then revoked. It is a sentinel because
// the caller's reaction is to place the task again rather than to report
// anything, and because that reaction must not be triggered by a failure that
// merely reads like this one.
var errQueueExpired = errors.New("execution: the assignment was revoked before any node claimed it")

// cancellation is what a withdrawn attempt's node said about itself: whether
// it answered at all, how far the frame had got, and whether the stage's own
// command sequence had begun.
//
// The last field is the whole reason the type exists. For a build it is
// information; for a publication it is the difference between an outcome this
// run knows and one nobody can establish from here (§28.6), and the only
// party that can state it is the one that started the process.
type cancellation struct {
	isAcknowledged   bool
	phase            string
	isCommandStarted bool
}

// withdrawAttempt writes the withdrawal on the attempt's tip and waits,
// bounded, to be told that nothing of it is running.
//
// The acknowledgement is what returns the node's capacity, so the wait for it
// is the run's `timeouts.cancel` and is taken on a context detached from the
// caller's: the commonest reason to withdraw an attempt is that the run itself
// was interrupted, and a wait the same signal cancelled would be a wait that
// never happened.
func (c *Coordinator) withdrawAttempt(ctx context.Context, node, task string, attempt int,
	kind string, offer taskOffer, tipOID string) cancellation {
	withdrawn, err := c.advance(ctx, node, offer.branch, tipOID, MessageCancel, Withdrawal{
		Header:     c.formatOrchestratorHeader(kind, task, attempt, node, offer.branch),
		Assignment: offer.offered, Tip: tipOID,
	})
	if err != nil {
		c.Log.Warn().Err(err).Str("run", c.Run).Str("task", task).Str("worker", node).
			Int("attempt", attempt).Str("code", CodeTransport).
			Str("category", CategoryTransportCleanup).Msg("the attempt could not be withdrawn")
		return cancellation{}
	}
	c.recordOwnedRef(node, offer.branch, withdrawn)
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("worker", node).Int("attempt", attempt).
		Str("commit", withdrawn).Msg("attempt withdrawn")
	acked, done := context.WithTimeout(context.WithoutCancel(ctx), c.Timeouts.Cancel)
	defer done()
	return c.awaitAcknowledgement(acked, node, task, attempt, offer, withdrawn)
}

// awaitAcknowledgement polls the attempt's branch until the node has answered
// the withdrawal or the cancel wait runs out.
//
// The branch is re-read rather than watched through the poller: the
// acknowledgement follows an object this party wrote, so the poller's memo
// would not report it as a movement worth anything, and the party that needs
// the answer is this one.
func (c *Coordinator) awaitAcknowledgement(ctx context.Context, node, task string, attempt int,
	offer taskOffer, withdrawn string) cancellation {
	mailbox := c.mailboxes[node]
	interval := minimumPollInterval
	next := time.NewTimer(interval)
	defer next.Stop()
	for {
		select {
		case <-ctx.Done():
			c.Log.Warn().Str("run", c.Run).Str("task", task).Str("worker", node).
				Int("attempt", attempt).Str("code", CodeTransport).
				Str("category", CategoryTransportCleanup).
				Msg("the withdrawal was not acknowledged within the cancel wait")
			return cancellation{}
		case <-next.C:
		}
		interval = min(interval*2, maximumPollInterval)
		next.Reset(interval)
		head, err := mailbox.Reread(ctx, offer.branch)
		if err != nil || head.OID == "" || head.OID == withdrawn {
			continue
		}
		tip, err := mailbox.Inspect(ctx, head)
		if err != nil || tip.Kind != MessageAck {
			continue
		}
		c.recordOwnedRef(node, offer.branch, head.OID)
		answered, reason := c.readAcknowledgement(ctx, node, task, attempt, offer, tip, withdrawn)
		if reason != "" {
			c.Log.Warn().Str("worker", node).Str("branch", tip.Branch).Str("commit", tip.OID).
				Str("reason", string(reason)).Str("code", CodeAuthority).
				Str("category", CategoryAuthority).Msg("stale or foreign receipt ignored")
			continue
		}
		c.Log.Debug().Str("run", c.Run).Str("task", task).Str("worker", node).
			Int("attempt", attempt).Str("phase", answered.phase).
			Bool("commandStarted", answered.isCommandStarted).
			Msg("the withdrawn attempt was acknowledged")
		return answered
	}
}

// readAcknowledgement verifies one acknowledgement and answers what it says,
// or the reason it is not this attempt's.
//
// It is held to everything a result is held to, plus the cancellation it
// answers: an acknowledgement of an earlier withdrawal of the same branch says
// nothing about the one this run is waiting on.
func (c *Coordinator) readAcknowledgement(ctx context.Context, node, task string, attempt int,
	offer taskOffer, tip ChainTip, withdrawn string) (cancellation, RejectReason) {
	document, err := c.mailboxes[node].Read(ctx, tip, c.Limits.MaxManifestBytes)
	if err != nil {
		if reason := RejectionReason(err); reason != "" {
			return cancellation{}, reason
		}
		return cancellation{}, ReasonUnreadable
	}
	var message Ack
	if err := json.Unmarshal(document, &message); err != nil {
		return cancellation{}, ReasonUnreadable
	}
	if reason := CheckHeader(message.Header,
		Binding{Node: node, Branch: offer.branch}, time.Now()); reason != "" {
		return cancellation{}, reason
	}
	if !IsTransitionLegal(tip.Previous, MessageAck, PartyWorker) ||
		tip.PreviousOID != withdrawn || message.Cancel != withdrawn ||
		message.Assignment != offer.offered || message.Run != c.Run || message.Task != task ||
		message.Attempt != attempt || message.Generation != c.Generation ||
		message.PlanDigest != c.PlanDigest {
		return cancellation{}, ReasonReplay
	}
	return cancellation{isAcknowledged: true, phase: message.Phase,
		isCommandStarted: message.CommandStarted}, ""
}

// revokeAttempt deletes one attempt's coordination ref, which is the fence
// §28.6 provides for an attempt that holds no authorization.
//
// It is the one deletion this run makes before the end, and it is safe for
// exactly the reason the specification states: an attempt that was never
// authorized to publish and may not write a native ref can do nothing once its
// expected-previous-object-ID updates stop succeeding. Nothing consumes such an
// attempt's branch either, since no result of it was ever admitted, so no
// consumer can be left without bytes it was promised.
//
// A ref that could not be deleted is left owned, so the run's own close tries
// again and reports it retained if it still cannot: a fence that did not take
// is the one cleanup failure that is an error rather than untidiness.
func (c *Coordinator) revokeAttempt(ctx context.Context, node, task string, attempt int,
	branch, expectedOld string) bool {
	isRevoked, err := c.mailboxes[node].Withdraw(ctx, branch, expectedOld)
	if err != nil || !isRevoked {
		c.Log.Error().Err(err).Str("run", c.Run).Str("task", task).Str("worker", node).
			Int("attempt", attempt).Str("branch", branch).Str("code", CodeTransport).
			Str("category", CategoryTransportCleanup).
			Msg("the attempt could not be fenced by revoking its coordination ref")
		return false
	}
	c.forgetOwnedRef(node, branch)
	c.Log.Debug().Str("run", c.Run).Str("task", task).Str("worker", node).Int("attempt", attempt).
		Str("branch", branch).Msg("the attempt was fenced by revoking its coordination ref")
	return true
}

// settleAbandonedAttempt is what a compute attempt that stopped answering
// costs: its slot, its node, and a fence.
//
// The slot stays where it is and the node leaves the pool because the work may
// still be running there, and §28.2 is explicit that a timeout may not free
// capacity for something that could overlap it. The ref is revoked because
// that is what makes the statement safe rather than merely cautious: the
// attempt holds no publication authorization and may write no native ref, so
// once its ref is gone it can have no effect at all.
func (c *Coordinator) settleAbandonedAttempt(ctx context.Context, lease *Lease, task string,
	attempt int, offer taskOffer, tipOID string) error {
	lease.Leak(LeakTaskDeadline)
	c.revokeAttempt(context.WithoutCancel(ctx), lease.Node, task, attempt, offer.branch, tipOID)
	return c.refuseTask(task, lease.Node, attempt, fmt.Errorf(
		"the node did not report within %s: the attempt is fenced by revoking its coordination ref, and the node is not used again by this run",
		c.Timeouts.Task))
}

// settleInterruptedAttempt withdraws one attempt because the run itself is
// ending, and gives its slot back when the node says the work has stopped.
//
// The error is the context's, wrapped, because that is what the executor reads
// to tell an interrupted package from a failed one: a package whose remote task
// was cancelled is `cancelled` exactly as a local one is, with no failure
// hooks and no revert.
func (c *Coordinator) settleInterruptedAttempt(ctx context.Context, lease *Lease, task string,
	attempt int, kind string, offer taskOffer, tipOID string) error {
	settled := c.withdrawAttempt(ctx, lease.Node, task, attempt, kind, offer, tipOID)
	if settled.isAcknowledged {
		lease.Release()
	} else {
		lease.Leak(LeakUnacknowledgedCancel)
	}
	return fmt.Errorf("waiting for %s on %s: %w", task, lease.Node, ctx.Err())
}

// formatOrchestratorHeader is the header of everything this party writes onto
// an attempt's branch after the assignment itself.
func (c *Coordinator) formatOrchestratorHeader(kind, task string, attempt int,
	node, branch string) Header {
	return Header{
		Protocol: ProtocolVersion, Kind: kind, Run: c.Run,
		PlanDigest: c.PlanDigest, Task: task, Attempt: attempt,
		Generation: c.Generation, Node: node, Branch: branch,
		IssuedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// forgetOwnedRef drops one ref from the set this run closes at the end,
// because it is already gone.
func (c *Coordinator) forgetOwnedRef(node, branch string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := make([]gitx.BranchLease, 0, len(c.owned[node]))
	for _, lease := range c.owned[node] {
		if lease.Branch == branch {
			continue
		}
		kept = append(kept, lease)
	}
	c.owned[node] = kept
}
