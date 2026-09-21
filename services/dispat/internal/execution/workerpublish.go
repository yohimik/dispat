// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The publishing node's half of the ready/go handshake (CCME §28.6).
//
// A publication is the one task whose commands cannot be taken back, so the
// node does not decide to run them. It installs the package's own verified
// build outputs, runs the beforePublish hook, says on the branch that it is
// ready, and then stops: the run that owns the locks is what decides whether
// the publication happens, and it says so with one single-use message written
// after it has revalidated everything the decision rests on.
//
// Three things make that decision hold at the moment it matters rather than at
// the moment it was taken. The authorization names the exact ready commit it
// answers, so it is an authorization for one state of one branch and not for
// the next one. It carries the instant it stops meaning anything, so an
// authorization that arrived late is refused by the party that would act on
// it. And the node re-reads the remote tip once more immediately before the
// command, which is the fence at the effect site: a withdrawal pushed after
// the authorization and before the command still stops it.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// defaultAuthorizationWait bounds the wait for an authorization when the
// assignment states no deadline of its own. It is the same order as the
// ordinary task wait, because what it bounds is the same thing: how long a
// node holds a slot, a checkout and an installed output set for a run that may
// never answer.
const defaultAuthorizationWait = 30 * time.Minute

// resolvePublicationGate is the step a publish frame waits at, and nothing at
// all for every other kind of task.
//
// It is resolved where the frame is assembled rather than asked for inside it,
// so that a build's frame carries no publication machinery it would have to
// skip: what makes a publication different is one call, and the difference is
// visible in one place.
func (w *Worker) resolvePublicationGate(tip ChainTip, claimed string, assignment Assignment,
	log zerolog.Logger) framePermitx {
	if assignment.Kind != KindPublish {
		return nil
	}
	return func(ctx context.Context, exports []plan.Output) taskOutcome {
		return w.awaitAuthorization(ctx, tip, claimed, assignment, exports, log)
	}
}

// awaitAuthorization reports this node ready and waits for the run to
// authorize the publication, answering the outcome that withholds it and the
// zero outcome that lets the command start.
//
// The hook's exports go out with the ready message rather than waiting for the
// result: they are what a beforePublish hook is for, and the orchestrator
// merges them onto the release at the same point the local path does, so a
// publication that is refused afterwards still carried them home.
func (w *Worker) awaitAuthorization(ctx context.Context, tip ChainTip, claimed string,
	assignment Assignment, exports []plan.Output, log zerolog.Logger) taskOutcome {
	ready, err := w.advance(ctx, tip, claimed, MessageReady, Ready{
		Header: w.formatReplyHeader(assignment.Header), Assignment: tip.OID,
		Claim: claimed, Exports: formatExports(exports),
	}, nil)
	if err != nil {
		log.Error().Err(err).Str("code", CodeTransport).Str("category", CategoryTransportCleanup).
			Msg("this node could not report itself ready to publish")
		return withheldPublication(claimed)
	}
	log.Info().Str("commit", ready).Str("package", assignment.Package.Name).
		Msg("ready to publish, awaiting authorization")
	return w.watchAuthorization(ctx, tip, ready, assignment, log)
}

// watchAuthorization polls the attempt's own branch until it carries the
// run's answer, or until the wait this assignment states runs out.
//
// The branch is re-read rather than observed through the node's own poll: the
// loop that watches the whole namespace would consume the movement this wait
// is about, and a publisher that missed its authorization would wait for a
// push that had already happened.
func (w *Worker) watchAuthorization(ctx context.Context, tip ChainTip, ready string,
	assignment Assignment, log zerolog.Logger) taskOutcome {
	deadline := time.NewTimer(resolveAuthorizationWait(assignment.DeadlineSeconds))
	defer deadline.Stop()
	interval := minimumPollInterval
	next := time.NewTimer(interval)
	defer next.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Warn().Err(ctx.Err()).Msg("the wait for a publication authorization was interrupted")
			return withheldPublication(ready)
		case <-deadline.C:
			log.Warn().Str("code", CodeAuthority).Str("category", CategoryAuthority).
				Msg("no publication authorization arrived within the run's own wait")
			return withheldPublication(ready)
		case <-next.C:
		}
		interval = min(interval*2, maximumPollInterval)
		next.Reset(interval)
		answer, isMoved := w.readAuthorizationTip(ctx, tip.Branch, ready, log)
		if !isMoved {
			continue
		}
		return w.resolveAuthorization(ctx, answer, tip, ready, assignment, log)
	}
}

// resolveAuthorization decides what one moved tip means for a publication that
// is waiting at the gate.
func (w *Worker) resolveAuthorization(ctx context.Context, answer ChainTip, tip ChainTip,
	ready string, assignment Assignment, log zerolog.Logger) taskOutcome {
	if answer.Kind == MessageCancel {
		return w.acknowledgeWithdrawal(ctx, answer, tip, assignment, log)
	}
	if answer.Kind != MessageGo {
		log.Warn().Str("message", string(answer.Kind)).Str("code", CodeAuthority).
			Str("category", CategoryAuthority).
			Msg("the publication branch moved to something that is not an authorization")
		return withheldPublication(ready)
	}
	if reason := w.checkAuthorization(ctx, answer, tip, ready, assignment); reason != "" {
		log.Warn().Str("reason", string(reason)).Str("commit", answer.OID).
			Str("code", CodeAuthority).Str("category", CategoryAuthority).
			Msg("the publication authorization was refused")
		return withheldPublication(ready)
	}
	if !w.isAuthorizationCurrent(ctx, tip.Branch, answer.OID, log) {
		return withheldPublication(answer.OID)
	}
	log.Info().Str("commit", answer.OID).Str("package", assignment.Package.Name).
		Msg("publication authorized, starting the publish command")
	return taskOutcome{expectedTip: answer.OID}
}

// acknowledgeWithdrawal answers a cancellation that arrived before anything
// irreversible started.
//
// The acknowledgement is the attempt's terminal message, and no result follows
// it. That is the point: the run withdrew the attempt and is waiting to hear
// that nothing happened, so a result pushed afterwards would be a second
// answer on a chain that carries one, and the branch would have to be read
// twice to find out which of them is true.
func (w *Worker) acknowledgeWithdrawal(ctx context.Context, answer ChainTip, tip ChainTip,
	assignment Assignment, log zerolog.Logger) taskOutcome {
	// Nothing of the publication has started, so there is no process group to
	// wait for: the acknowledgement is owed immediately and is written on a
	// context of its own, because the withdrawal may have come with a stop.
	ackCtx, done := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
	defer done()
	acked, err := w.advance(ackCtx, tip, answer.OID, MessageAck, Ack{
		Header: w.formatReplyHeader(assignment.Header), Assignment: tip.OID, Cancel: answer.OID,
	}, nil)
	if err != nil {
		log.Error().Err(err).Str("code", CodeTransport).Str("category", CategoryTransportCleanup).
			Msg("the withdrawal could not be acknowledged")
		return taskOutcome{status: StatusCancelled, failedPart: release.PartAuthorization,
			reason: ReasonAuthorization, expectedTip: answer.OID, isAnswered: true}
	}
	log.Info().Str("commit", acked).Msg("publication withdrawn before it started")
	return taskOutcome{status: StatusCancelled, expectedTip: acked, isAnswered: true}
}

// readAuthorizationTip answers what the attempt's branch now carries, and
// reports nothing at all for everything that is not yet an answer.
//
// A mailbox briefly out of reach, a branch that has not moved since this node
// wrote its ready, and a branch the run has already closed are one situation
// from here: the publication is not authorized, and the deadline is what
// decides how long that is waited out.
func (w *Worker) readAuthorizationTip(ctx context.Context, branch, ready string,
	log zerolog.Logger) (ChainTip, bool) {
	head, err := w.Mailbox.Reread(ctx, branch)
	if err == nil && head.OID != "" && head.OID != ready {
		answer, inspectErr := w.Mailbox.Inspect(ctx, head)
		if inspectErr == nil {
			return answer, true
		}
		err = inspectErr
	}
	log.Trace().Err(err).Str("commit", head.OID).Msg("no publication authorization yet")
	return ChainTip{}, false
}

// checkAuthorization holds one authorization to everything that makes it this
// attempt's: the rules every message is held to, the exact objects it answers,
// the step of the chain it was written on, and the instant it stops meaning
// anything.
//
// The conditions are one refusal rather than one each because they are one
// statement: this is an authorization for this ready, of this attempt, of this
// run, and it is still current. A message failing any of them is a message
// this node must not publish on, and which of them it failed tells an attacker
// more than it tells an operator.
func (w *Worker) checkAuthorization(ctx context.Context, answer ChainTip, tip ChainTip,
	ready string, assignment Assignment) RejectReason {
	document, err := w.Mailbox.Read(ctx, answer, assignment.Limits.MaxManifestBytes)
	if err != nil {
		if reason := RejectionReason(err); reason != "" {
			return reason
		}
		return ReasonUnreadable
	}
	var message Go
	if err := json.Unmarshal(document, &message); err != nil {
		return ReasonUnreadable
	}
	if reason := CheckHeader(message.Header, Binding{Node: w.Node, Branch: tip.Branch}, time.Now()); reason != "" {
		return reason
	}
	if !IsTransitionLegal(answer.Previous, MessageGo, PartyOrchestrator) ||
		answer.PreviousOID != ready || message.Ready != ready || message.Assignment != tip.OID ||
		message.Run != assignment.Run || message.Task != assignment.Task ||
		message.Attempt != assignment.Attempt || message.Generation != assignment.Generation ||
		message.PlanDigest != assignment.PlanDigest {
		return ReasonReplay
	}
	if !isAuthorizationUnexpired(message.NotAfter, time.Now()) {
		return ReasonIssuedAt
	}
	return ""
}

// isAuthorizationUnexpired reports whether an authorization may still be acted
// on. An authorization that states no expiry is refused rather than trusted:
// the expiry is what bounds the window between the checks the run made and the
// effect this node produces, and a publication with no such bound is one
// nobody can say anything about afterwards.
func isAuthorizationUnexpired(notAfter string, now time.Time) bool {
	limit, err := time.Parse(time.RFC3339, notAfter)
	return err == nil && now.Before(limit)
}

// isAuthorizationCurrent is the fence at the effect site: one last read of the
// remote tip, immediately before the command that cannot be taken back.
//
// Everything checked before this was checked about objects this node had
// already fetched, which says what the branch held at some earlier instant. A
// withdrawal pushed after the authorization and before the command would pass
// every one of those checks and still mean the publication must not happen, so
// the last thing the node does is ask the remote where the branch is now.
func (w *Worker) isAuthorizationCurrent(ctx context.Context, branch, authorized string,
	log zerolog.Logger) bool {
	head, err := w.Mailbox.Reread(ctx, branch)
	if err == nil && head.OID == authorized {
		return true
	}
	log.Warn().Err(err).Str("commit", head.OID).Str("code", CodeAuthority).
		Str("category", CategoryAuthority).
		Msg("the publication branch no longer carries the authorization, so the command is not started")
	return false
}

// withheldPublication is the outcome of an attempt whose command never
// started, whatever stopped it: the node reports a failed task naming the
// authorization, because a publication that was not authorized is a
// publication that did not happen and a run must never read it as one that
// might have.
func withheldPublication(expectedTip string) taskOutcome {
	return taskOutcome{status: StatusFailed, failedPart: release.PartAuthorization,
		reason: ReasonAuthorization, expectedTip: expectedTip}
}

// resolveAuthorizationWait is how long this node waits to be authorized: the
// run's own wait, as the assignment states it, and a bound of this node's own
// for an assignment that states none.
func resolveAuthorizationWait(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultAuthorizationWait
	}
	return time.Duration(seconds) * time.Second
}
