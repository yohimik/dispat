// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Asking again whether this run still owns what it took (CCME §28.6).
//
// "After lock loss, no new effect may start" is a rule about moments rather
// than about a run: the lock was verified before the plan was fixed, and every
// assignment written after that is a new effect started on the strength of a
// check that is minutes old. So the question is asked again before each one,
// of the remote rather than of the memory of having acquired anything, because
// that is the whole difference between the two: a lock somebody else took over
// is a lock this run still remembers taking.
//
// A successful answer is not reused for a later effect: the remote lock may
// disappear between two assignments. A loss, once seen, is remembered,
// because ownership is not something a run gets back.
//
// What a loss does is not only to refuse the next assignment. Every attempt
// already in flight is ended too: a compute attempt is fenced by revoking its
// ref, and a publisher that was authorized before the loss is asked what it
// had got to, exactly as an interrupted run asks. Recording a publication that
// was authorized before the loss is a separate matter and still happens, since
// a create-only record of an effect that already occurred is not a new effect.

import (
	"context"
	"errors"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// ownership is the run's memory of the last answer, and of the cancellations
// that a loss has to reach.
type ownership struct {
	// verify is the question itself, supplied by the caller that holds the
	// locks: this package does not know how a release lock is read, and knows
	// only the two answers a verification that failed can give.
	verify func(context.Context) error

	// verifyGate orders remote checks so a successful check cannot return
	// after another found a loss. A waiter can still leave on cancellation.
	verifyGate chan struct{}
	mu         sync.Mutex
	failure    error
	isLost     bool
	haltings   map[int]context.CancelFunc
	nextHalt   int
}

// VerifyOwnershipWith gives the run the question it asks before every new
// assignment.
//
// It is a function rather than an interface because it is one question with no
// vocabulary of its own: "do you still hold what you took", answered by the
// code that took it. Leaving it unset is what a caller with no locks does, and
// nothing is then asked or cached.
func (c *Coordinator) VerifyOwnershipWith(verify func(context.Context) error) {
	c.ownership.verify = verify
	c.ownership.verifyGate = make(chan struct{}, 1)
	c.ownership.haltings = map[int]context.CancelFunc{}
}

// VerifyOwnership checks the complete lock set before a publication is
// authorized. It shares the dispatch check's remembered loss and cancellation
// of in-flight attempts.
func (c *Coordinator) VerifyOwnership(ctx context.Context) error {
	return c.checkOwnership(ctx)
}

// checkOwnership answers whether a new effect may start, and remembers a loss.
func (c *Coordinator) checkOwnership(ctx context.Context) error {
	if c.ownership.verify == nil {
		return nil
	}
	select {
	case c.ownership.verifyGate <- struct{}{}:
		defer func() { <-c.ownership.verifyGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.ownership.mu.Lock()
	if c.ownership.isLost {
		failure := c.ownership.failure
		c.ownership.mu.Unlock()
		return failure
	}
	c.ownership.mu.Unlock()
	err := c.ownership.verify(ctx)
	c.ownership.mu.Lock()
	defer c.ownership.mu.Unlock()
	if err == nil {
		return nil
	}
	if c.ownership.isLost {
		return c.ownership.failure
	}
	if ctx.Err() != nil {
		// An interrupted lookup establishes nothing about the remote lock.
		// The run is stopping already; leave ownership available to detached
		// record and cleanup paths rather than reporting a false lock loss.
		return ctx.Err()
	}
	c.ownership.isLost = true
	c.ownership.failure = NewIdentifiedDiagnostic(Identity{Run: c.Run},
		CodeLockLost, CategoryNativeRecordingOrLock,
		"this run cannot show that it still owns the release lock it took, so it starts no further assignment and issues no further authorization: %w", err)
	c.Log.Error().Err(err).Str("run", c.Run).Str("code", CodeLockLost).
		Str("category", CategoryNativeRecordingOrLock).Str("reason", resolveLossReason(err)).
		Msg("the release lock was lost, so no new effect may start")
	c.haltAttempts()
	return c.ownership.failure
}

// The two reasons a verification ends ownership, as the lost line names them.
const (
	// lossReasonLost is a remote that was read and carries another lock object
	// under the lock's name, or none.
	lossReasonLost = "lost"
	// lossReasonUnverified is a remote that could not be read within the
	// verification's bounded reads. The lock may still be there; this run just
	// cannot show that it owns it, which is what a new effect needs.
	lossReasonUnverified = "unverified"
)

// resolveLossReason tells the two apart for the lost line. The question and
// its reading belong to the caller that holds the locks; what this package
// knows is only the two answers a verification that did not succeed can give,
// and anything that is not a positive loss is an answer that was not read.
func resolveLossReason(err error) string {
	if errors.Is(err, release.ErrLockLost) {
		return lossReasonLost
	}
	return lossReasonUnverified
}

// haltAttempts ends every attempt this run has in flight. It runs under the
// ownership lock, which is also what keeps two losses from halting twice.
func (c *Coordinator) haltAttempts() {
	for key, halt := range c.ownership.haltings {
		halt()
		delete(c.ownership.haltings, key)
	}
}

// watchOwnership derives one attempt's context from the caller's so that a
// lock lost anywhere in the run ends this attempt too, and answers the removal
// of that registration.
//
// The cancellation is the same one an interrupt uses, deliberately: an attempt
// ended because the run lost its exclusion and an attempt ended because the
// run was interrupted are the same thing from the node's side, and both are
// settled by withdrawing the attempt and asking what it had got to.
func (c *Coordinator) watchOwnership(ctx context.Context) (context.Context, func()) {
	halted, halt := context.WithCancel(ctx)
	if c.ownership.verify == nil {
		return halted, halt
	}
	c.ownership.mu.Lock()
	if c.ownership.isLost {
		c.ownership.mu.Unlock()
		halt()
		return halted, func() {}
	}
	key := c.ownership.nextHalt
	c.ownership.nextHalt++
	c.ownership.haltings[key] = halt
	c.ownership.mu.Unlock()
	return halted, func() {
		c.ownership.mu.Lock()
		delete(c.ownership.haltings, key)
		c.ownership.mu.Unlock()
		halt()
	}
}
