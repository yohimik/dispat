// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Asking again whether this run still owns what it took (CCME §28.6).
//
// "After lock loss, no new effect may start" is a rule about moments rather
// than about a run: the lock was verified before the plan was fixed, and every
// effect started after that is started on the strength of a check that is
// minutes old. So the question is asked again before each one, of the remote
// rather than of the memory of having acquired anything, because that is the
// whole difference between the two: a lock somebody else took over is a lock
// this run still remembers taking.
//
// The question has one owner per run, the OwnershipGate, whichever machine
// the effect happens on. A release that runs everything here asks it before
// every publication; a release that delegates asks it before every
// assignment and every authorization as well. One gate is what makes the two
// agree: a loss the local publication path decides is a loss the dispatch
// path reads, and the other way round.
//
// A successful answer is not reused for a later effect: the remote lock may
// disappear between two of them. A loss, once seen, is remembered, because
// ownership is not something a run gets back.
//
// What a loss does is not only to refuse the next effect. Every attempt
// already in flight on another machine is ended too: a compute attempt is
// fenced by revoking its ref, and a publisher that was authorized before the
// loss is asked what it had got to, exactly as an interrupted run asks.
// Recording a publication that was authorized before the loss is a separate
// matter and still happens, since a create-only record of an effect that
// already occurred is not a new effect.

import (
	"context"
	"errors"
	"sync"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// OwnershipGate is one run's answer to "may a new effect start": the
// question, the memory of a loss once it was seen, and the cancellations a
// loss has to reach.
//
// The nil gate is a run that holds no lock, and asks nothing: every method is
// nil-safe, so a caller with no exclusion to verify carries no gate at all.
type OwnershipGate struct {
	run string
	// verify is the question itself, supplied by the caller that holds the
	// locks: this package does not know how a release lock is read, and knows
	// only the two answers a verification that failed can give.
	verify func(context.Context) error
	log    zerolog.Logger

	// verifyGate orders remote checks so a successful check cannot return
	// after another found a loss. A waiter can still leave on cancellation.
	verifyGate chan struct{}
	mu         sync.Mutex
	failure    error
	isLost     bool
	haltings   map[int]context.CancelFunc
	nextHalt   int
}

// NewOwnershipGate opens the gate over one run's verification.
//
// The verification is a function rather than an interface because it is one
// question with no vocabulary of its own: "do you still hold what you took",
// answered by the code that took it. A nil verification asks nothing, which
// is what a caller that holds no lock hands in.
func NewOwnershipGate(run string, verify func(context.Context) error, log zerolog.Logger) *OwnershipGate {
	return &OwnershipGate{run: run, verify: verify, log: log,
		verifyGate: make(chan struct{}, 1), haltings: map[int]context.CancelFunc{}}
}

// Check answers whether a new effect may start, and remembers a loss.
//
// A caller that cancels gets its context's error back and no loss is
// decided: an interrupted lookup establishes nothing about the remote lock,
// and the detached record and cleanup paths of a stopping run still need the
// ownership it has.
func (g *OwnershipGate) Check(ctx context.Context) error {
	if g == nil || g.verify == nil {
		return nil
	}
	select {
	case g.verifyGate <- struct{}{}:
		defer func() { <-g.verifyGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	g.mu.Lock()
	if g.isLost {
		failure := g.failure
		g.mu.Unlock()
		return failure
	}
	g.mu.Unlock()
	err := g.verify(ctx)
	g.mu.Lock()
	defer g.mu.Unlock()
	if err == nil {
		return nil
	}
	if g.isLost {
		return g.failure
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	g.isLost = true
	g.failure = NewIdentifiedDiagnostic(Identity{Run: g.run},
		CodeLockLost, CategoryNativeRecordingOrLock,
		"this run cannot show that it still owns the release lock it took, so no new effect of it may start: %w", err)
	event := g.log.Error().Err(err).Str("code", CodeLockLost).
		Str("category", CategoryNativeRecordingOrLock).Str("reason", resolveLossReason(err))
	if g.run != "" {
		event = event.Str("run", g.run)
	}
	event.Msg("the release lock was lost, so no new effect may start")
	g.haltAttempts()
	return g.failure
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
// gate's lock, which is also what keeps two losses from halting twice.
func (g *OwnershipGate) haltAttempts() {
	for key, halt := range g.haltings {
		halt()
		delete(g.haltings, key)
	}
}

// watch derives one attempt's context from the caller's so that a lock lost
// anywhere in the run ends this attempt too, and answers the removal of that
// registration.
//
// The cancellation is the same one an interrupt uses, deliberately: an attempt
// ended because the run lost its exclusion and an attempt ended because the
// run was interrupted are the same thing from the node's side, and both are
// settled by withdrawing the attempt and asking what it had got to.
func (g *OwnershipGate) watch(ctx context.Context) (context.Context, func()) {
	halted, halt := context.WithCancel(ctx)
	if g == nil || g.verify == nil {
		return halted, halt
	}
	g.mu.Lock()
	if g.isLost {
		g.mu.Unlock()
		halt()
		return halted, func() {}
	}
	key := g.nextHalt
	g.nextHalt++
	g.haltings[key] = halt
	g.mu.Unlock()
	return halted, func() {
		g.mu.Lock()
		delete(g.haltings, key)
		g.mu.Unlock()
		halt()
	}
}

// UseOwnership gives the coordinator the run's own gate, so that a loss this
// run's publications decide is the loss its dispatch reads, and the other way
// round. A nil gate is a run that holds no lock, as a sweep does: its
// coordinator then asks nothing before an assignment.
func (c *Coordinator) UseOwnership(gate *OwnershipGate) {
	c.ownership = gate
}

// VerifyOwnershipWith gives the coordinator a gate of its own over one
// verification, for a caller that has the question and no gate yet.
func (c *Coordinator) VerifyOwnershipWith(verify func(context.Context) error) {
	c.ownership = NewOwnershipGate(c.Run, verify, c.Log)
}

// checkOwnership answers whether a new assignment may start, and remembers a
// loss.
func (c *Coordinator) checkOwnership(ctx context.Context) error {
	return c.ownership.Check(ctx)
}

// watchOwnership derives one attempt's context so that a lock lost anywhere
// in the run ends this attempt too (see OwnershipGate.watch).
func (c *Coordinator) watchOwnership(ctx context.Context) (context.Context, func()) {
	return c.ownership.watch(ctx)
}
