// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The orchestrator's half of a delegated publication (CCME §28.6).
//
// Publication is the one stage this profile treats asymmetrically, and the
// asymmetry is the policy rather than an omission. A build is the work a pool
// exists for, so the default offers it to the pool; a publication is
// serialized per repository whatever happens, so delegating one buys the run
// no parallelism at all and spreads the registry credentials over one more
// machine. It therefore travels only where the operator asked for it in so
// many words, and a space that authenticates never travels at all: a login is
// what pins a space's publishes to the machine the release was started on.
//
// What the orchestrator keeps is everything that decides: the owner lane, the
// revalidation, the single-use authorization, the records, the tag and every
// hook that follows. What a node gets is the beforePublish hook and the
// publish commands, and it gets them one message at a time, because the point
// of the handshake is that the decision to publish is made after the hook has
// run and immediately before the command, on the machine that owns the locks.

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// authorizationLifetime is how long an authorization means anything.
//
// It exists because the checks behind an authorization are statements about
// an instant: the lock was held, the snapshot matched, the inputs had not
// moved. A publisher that acted on them minutes later would be publishing
// under an ownership nobody re-verified, so the message carries the instant it
// expires and the node that would act on it enforces that itself. Two minutes
// is generous for two machines on one network and short beside anything an
// operator would do by hand.
const authorizationLifetime = 2 * time.Minute

// Publish runs one package's publish frame where this run places it.
//
// The placement is the same decision the build stage makes, asked of the
// publish stage: one function, one vocabulary, and the asymmetry between the
// two stages stated there rather than here. A publication this run keeps takes
// the local path unchanged, which is the path every release has always taken,
// hook and revalidation and commands in one sequence in this checkout.
func (c *Coordinator) Publish(ctx context.Context, request release.StageRequest,
	here release.LocalFrame, authorize func(context.Context) error) (release.StageOutcome, error) {
	space := request.Release.Pkg.Space
	placement := ResolveStagePlacement(request.Stage,
		space.RunOnly.ResolvePublish(), len(space.LoginScript) > 0)
	if placement != PlacementWorker {
		return c.publishHere(ctx, here)
	}
	task := request.Release.Pkg.Name + ":" + request.Stage
	// The node that built the package is asked for first: its cache already
	// holds the objects this publication is about to install. It is a
	// preference and the pool treats it as one.
	return c.placeTask(ctx, task, placement, space.BuildPlatforms, c.producerOf(request),
		func(ctx context.Context, lease *Lease, attempt int) (release.StageOutcome, error) {
			return c.dispatchPublish(ctx, lease, task, attempt, request, authorize)
		})
}

// publishHere runs the publish frame in this process, as a release with no
// worker link runs it.
//
// It takes no node slot, deliberately. The publication already holds the
// package's run-wide publish budget and its owner's publication lane, and
// adding a third bound would change what a repository's `execution.concurrency`
// means for the one stage that is serialized anyway.
func (c *Coordinator) publishHere(ctx context.Context, here release.LocalFrame) (release.StageOutcome, error) {
	what, err := here(ctx)
	if err != nil {
		// The executor's own sentence, unchanged: a publication that failed on
		// this machine fails its package exactly as it did before any of this
		// existed, hook, revalidation, revert and onFail included.
		return release.StageOutcome{LocalFailure: what}, err
	}
	return release.StageOutcome{}, nil
}

// producerOf is the node that built this package, and the empty string for a
// package whose outputs this run has not admitted or built somewhere else.
func (c *Coordinator) producerOf(request release.StageRequest) string {
	admitted := c.outputs.find(request.Release.Pkg.Name)
	if admitted == nil {
		return ""
	}
	return admitted.node
}

// dispatchPublish offers one package's publish frame to the node the pool
// chose and carries it through the handshake.
//
// The input state is prepared exactly as a build's is, and by publish time the
// working tree also holds the version edits of packages that came earlier.
// That is not a problem the snapshot has to solve: what decides whether this
// publication may happen is the revalidation behind the authorization, and it
// compares the package's own inputs rather than the whole tree.
func (c *Coordinator) dispatchPublish(ctx context.Context, lease *Lease, task string, attempt int,
	request release.StageRequest, authorize func(context.Context) error) (release.StageOutcome, error) {
	outcome := release.StageOutcome{Node: lease.Node}
	sources := c.dispatch.Sources(request.Release.Pkg.Name)
	dir, err := resolvePackageDir(sources, request)
	if err != nil {
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, attempt, err)
	}
	commits, err := c.captureInputs(ctx, sources)
	if err != nil {
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, attempt, err)
	}
	repositories, err := c.offerInputs(ctx, lease.Node, sources, commits)
	if err != nil {
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, attempt, err)
	}
	inputs, err := c.resolvePublishInputs(ctx, lease.Node, request, sources, dir)
	if err != nil {
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, attempt, err)
	}
	offer, err := c.offerAssignment(ctx, lease, task,
		c.formatAssignment(lease.Node, KindPublish, task, attempt, dir, repositories, inputs, request))
	if err != nil {
		return outcome, err
	}
	defer offer.observer.forget(offer.branch)
	return c.awaitPublication(ctx, lease, task, attempt, request.Release.Pkg.Repository,
		outcome, offer, authorize)
}

// resolvePublishInputs is what a publishing node has to have in place before
// its beforePublish hook runs: the package's OWN admitted output set, and the
// provider sets its build was given.
//
// Its own set is the point. A publish script uploads what the build produced,
// and on another machine those bytes exist only because this run admitted them
// and can name them; the providers' sets come too because a publish hook reads
// the same folder a build hook did. Which providers those are is the same
// question the build asked, so it is asked the same way.
func (c *Coordinator) resolvePublishInputs(ctx context.Context, node string,
	request release.StageRequest, sources []Source, dir string) ([]AssignmentInput, error) {
	packageName := request.Release.Pkg.Name
	inputs, err := c.resolveTaskInputs(ctx, node, packageName)
	if err != nil {
		return nil, err
	}
	own := c.outputs.find(packageName)
	if own == nil {
		// A package that declares no build outputs publishes from its sources,
		// which the prepared input state already carries.
		return inputs, nil
	}
	branch, err := c.reachOutputs(ctx, node, own)
	if err != nil {
		return nil, err
	}
	return append(inputs, AssignmentInput{
		Task: own.manifest.Task, Package: packageName, Branch: branch,
		Commit: own.commit, Digest: own.manifest.Digest,
		Path: resolveAnchoredPath(sources, request.Release.Pkg.Repository, dir),
	}), nil
}

// resolveAnchoredPath is where one package's folder sits in the checkout a node
// materializes: its repository's place under the run's anchor, and the
// package's place inside that repository.
func resolveAnchoredPath(sources []Source, repository, dir string) string {
	for _, source := range sources {
		if source.Name == repository {
			return path.Join(source.Path, dir)
		}
	}
	return dir
}

// awaitPublication carries one delegated publication from the offer to its
// terminal result, authorizing it in between.
//
// Two waits with the same length and different meanings. Before the
// authorization, a node that stops answering has done nothing: the attempt is
// abandoned exactly as an abandoned build is. After it, the run has told a
// machine to publish and does not know whether it did, which is a different
// outcome with a code of its own.
func (c *Coordinator) awaitPublication(ctx context.Context, lease *Lease, task string,
	attempt int, repository string, outcome release.StageOutcome, offer taskOffer,
	authorize func(context.Context) error) (release.StageOutcome, error) {
	deadline := time.NewTimer(c.Timeouts.Task)
	defer deadline.Stop()
	state := publicationState{tip: offer.offered}
	offeredAt, claimedAt := time.Now(), time.Time{}
	for {
		select {
		case reply := <-offer.replies:
			if reply.kind == MessageClaim {
				claimedAt = time.Now()
				// The publication has started being prepared, so the run-time
				// clock starts with it: the queue this assignment waited in is
				// not this attempt's own time.
				state.isClaimed, state.tip = true, reply.commit
				deadline.Reset(c.Timeouts.Task)
				continue
			}
			if reply.kind == MessageResult {
				c.rememberTiming(task, resolveQueueTime(offeredAt, claimedAt),
					resolveRunTime(offeredAt, claimedAt))
				lease.Release()
				return c.readPublicationOutcome(task, attempt, outcome, reply.result)
			}
			outcome.Exports = formatOutputs(reply.ready.Exports)
			state.tip = reply.commit
			if err := c.authorizePublication(ctx, lease, task, attempt, repository,
				offer, reply, authorize); err != nil {
				outcome.FailedPart = release.PartAuthorization
				return outcome, err
			}
			// The wait starts again, now for the only message that can say
			// what became of an effect this run authorized.
			state.isAuthorized, state.tip = true, offer.observer.readAuthorizedTip(offer.branch)
			deadline.Reset(c.Timeouts.Task)
		case <-deadline.C:
			if !state.isClaimed && !state.isAuthorized {
				expired, err := c.settleQueuedAttempt(ctx, lease, task, attempt, offer)
				if err != nil {
					return outcome, err
				}
				state.isClaimed, state.tip = true, expired
				deadline.Reset(c.Timeouts.Task)
				continue
			}
			if !state.isAuthorized {
				return outcome, c.settleAbandonedAttempt(ctx, lease, task, attempt, offer, state.tip)
			}
			return outcome, c.resolveUnansweredPublication(ctx, lease, task, attempt,
				repository, offer, state.tip)
		case <-ctx.Done():
			if !state.isAuthorized {
				return outcome, c.settleInterruptedAttempt(ctx, lease, task, attempt,
					KindPublish, offer, state.tip)
			}
			return outcome, c.resolveUnansweredPublication(ctx, lease, task, attempt,
				repository, offer, state.tip)
		}
	}
}

// publicationState is how far one delegated publication has got: the object
// its branch now carries, whether a node has taken the work on, and whether
// this run has authorized the effect.
//
// The last field is the one that changes what every other outcome means. An
// attempt that was never authorized is abandoned for free; one that was is an
// effect this run asked for and may not be able to account for, which is a
// different class of outcome with a code of its own (§28.6).
type publicationState struct {
	tip          string
	isClaimed    bool
	isAuthorized bool
}

// readPublicationOutcome turns one publisher's terminal result into what the
// executor does with it.
//
// It admits nothing, and that is the difference between this and a build. A
// publication produces an effect on a registry rather than an output set: the
// bytes it uploaded are the ones this run already admitted from the build, and
// what comes back is what its scripts exported and whether they succeeded.
// The exports on the outcome so far are the beforePublish hook's, carried by
// the ready message for the one case in which no result ever follows it. A
// result accumulates the whole frame's exports, hook included, so it replaces
// them rather than being added to them.
func (c *Coordinator) readPublicationOutcome(task string, attempt int,
	outcome release.StageOutcome, result Result) (release.StageOutcome, error) {
	outcome, err := c.readReportedOutcome(task, attempt, outcome, result)
	if err != nil {
		return outcome, err
	}
	c.reportTaskFinished(task, result, outcome)
	return outcome, nil
}

// authorizePublication revalidates everything the publication rests on and
// writes the single-use authorization, or withdraws the attempt.
//
// The order is the whole of §28.6. The hook has run on the executing node and
// said so; the run then checks what only it can check; and only if that passes
// does the authorization exist at all. The attempt is marked authorized before
// the message is written rather than after, so a push whose response is lost
// cannot become a second authorization: the mark is what makes it single use,
// and the compare-and-swap is what makes it one message.
func (c *Coordinator) authorizePublication(ctx context.Context, lease *Lease, task string,
	attempt int, repository string, offer taskOffer, reply taskReply,
	authorize func(context.Context) error) error {
	waiting := offer.observer.find(offer.branch)
	if waiting == nil || waiting.isAuthorized {
		// No second withdrawal and no second authorization: an attempt this
		// run has already answered is an attempt whose effect may already have
		// happened, and the one thing that must not follow it is another
		// message telling a node to start.
		return NewIdentifiedDiagnostic(Identity{Run: c.Run, Worker: lease.Node, Task: task, Attempt: attempt},
			CodeAuthority, CategoryAuthority,
			"%s asked to be authorized twice and an authorization is single use: no second effect may start under it",
			task)
	}
	if authorize != nil {
		if err := authorize(ctx); err != nil {
			return c.withdrawPublication(ctx, lease, task, attempt, offer, reply, err)
		}
	}
	waiting.isAuthorized = true
	authorized, err := c.advance(ctx, lease.Node, offer.branch, reply.commit, MessageGo,
		c.formatGo(reply, lease.Node, offer.branch))
	if err != nil {
		return c.reportLostAuthorization(ctx, lease, task, attempt, repository, offer, reply, err)
	}
	c.recordOwnedRef(lease.Node, offer.branch, authorized)
	// The authorization is what a withdrawal of a running publisher is leased
	// against, so the object is remembered where the waiting task can read it.
	waiting.authorizedTip = authorized
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
		Str("package", reply.ready.Task).Str("commit", authorized).
		Int("attempt", reply.ready.Attempt).Msg("publication authorized")
	return nil
}

// reportLostAuthorization decides what an authorization this run could not
// write means, and it is the one place the mark set before the push earns its
// keep.
//
// The question is not whether the push returned an error. It is whether the
// authorization could have reached the node, and the branch is what answers
// that: a branch still carrying the ready commit carries no authorization, so
// no publisher can have acted on one and the attempt is an ordinary abandoned
// one. A branch that has moved to something else, or that cannot be read at
// all, is the case the mark exists for: a push whose answer never came back
// may well have applied, §28.6 allows nothing to be inferred from the
// silence, and the outcome is unknown with everything that follows from it.
func (c *Coordinator) reportLostAuthorization(ctx context.Context, lease *Lease, task string,
	attempt int, repository string, offer taskOffer, reply taskReply, err error) error {
	lease.Leak(LeakTransport)
	if !c.isAuthorizationReachable(ctx, lease.Node, offer.branch, reply.commit) {
		return c.refuseTask(task, lease.Node, attempt, err)
	}
	c.Log.Error().Err(err).Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
		Int("attempt", attempt).Str("code", CodePublicationUnknown).
		Str("category", CategoryPublicationUnknown).
		Msg("the publication authorization could not be written and may still have reached the node")
	return c.reportUnknownPublication(task, attempt, lease.Node, repository, cancellation{})
}

// isAuthorizationReachable reports whether an authorization this run failed to
// write could nonetheless be on the branch.
//
// A lease the remote refused and a push that never left this machine both
// leave the branch where it was, and the ready commit still being the tip is
// the proof of that. Anything else, including a read that fails, is answered
// yes: the safe answer to "might a node have been told to publish" is the one
// that withholds a second attempt rather than the one that assumes nothing
// happened.
func (c *Coordinator) isAuthorizationReachable(ctx context.Context, node, branch, ready string) bool {
	head, err := c.mailboxes[node].Reread(ctx, branch)
	if err != nil {
		return true
	}
	if head.OID == "" {
		// The branch is gone, so nothing can be read from it and nothing can
		// be pushed onto it: no publisher was authorized on this attempt.
		return false
	}
	return head.OID != ready
}

// formatGo is the authorization document: the work it belongs to, the exact
// ready commit it answers, and the instant it stops meaning anything.
func (c *Coordinator) formatGo(reply taskReply, node, branch string) Go {
	return Go{
		Header:     c.formatPublishHeader(reply, node, branch),
		Assignment: reply.ready.Assignment,
		Ready:      reply.commit,
		NotAfter:   time.Now().UTC().Add(authorizationLifetime).Format(time.RFC3339),
	}
}

// formatPublishHeader is the header both messages this party writes to a
// publisher carry: the work as the run knows it, addressed to the node it is
// for, on the branch of this attempt and at this moment.
func (c *Coordinator) formatPublishHeader(reply taskReply, node, branch string) Header {
	return Header{
		Protocol: ProtocolVersion, Kind: KindPublish, Run: c.Run,
		PlanDigest: c.PlanDigest, Task: reply.ready.Task, Attempt: reply.ready.Attempt,
		Generation: c.Generation, Node: node, Branch: branch,
		IssuedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// advance writes one of this party's messages onto a branch, under a lease
// over the object it answers.
func (c *Coordinator) advance(ctx context.Context, node, branch, expectedOld string,
	kind MessageKind, message any) (string, error) {
	document, err := json.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("execution: writing the %s document: %w", kind, err)
	}
	return c.mailboxes[node].Advance(ctx, branch, expectedOld, kind, document, nil)
}

// withdrawPublication tells a waiting publisher that it is not authorized, and
// waits briefly to be told that nothing happened.
//
// The acknowledgement is what returns the node's capacity: a publisher that
// confirmed it stopped is a machine with nothing of this run running on it,
// and one that never answered is a machine this run will not use again. Either
// way the package fails with the error that refused the authorization, because
// that is what an operator has to read first.
func (c *Coordinator) withdrawPublication(ctx context.Context, lease *Lease, task string,
	attempt int, offer taskOffer, reply taskReply, refused error) error {
	settled := c.withdrawAttempt(ctx, lease.Node, task, attempt, KindPublish, offer, reply.commit)
	if !settled.isAcknowledged {
		lease.Leak(LeakUnacknowledgedCancel)
		return refused
	}
	lease.Release()
	c.Log.Warn().Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
		Int("attempt", attempt).Str("code", CodeAuthority).Str("category", CategoryAuthority).
		Msg("publication withheld")
	return refused
}

// resolveUnansweredPublication is what this run says about a publication it
// authorized and never heard the end of.
//
// It is the single place the "outcome unknown" class is decided, and what it
// decides is an ordering rather than a verdict. The run first asks the one
// question that can still be answered: it withdraws the attempt and waits, for
// the cancel wait and no longer, for the node to say what the publisher had
// got to. That answer, and nothing else, tells the two cases apart. A node
// that stopped before its publish command began published nothing, so the
// package simply failed. A node that stopped in the middle of it, or never
// answered at all, leaves an outcome nobody here can establish, and §28.6 is
// explicit that neither a missing reply nor a missing tag proves failure.
//
// Whichever of the two it is, no second attempt is authorized under this
// authorization in this run. The difference is what happens to the exclusion:
// a publisher that acknowledged is quiesced, so the locks go back as usual,
// and one that did not is still possibly running, so the repository it was
// publishing into stays locked for an operator.
func (c *Coordinator) resolveUnansweredPublication(ctx context.Context, lease *Lease, task string,
	attempt int, repository string, offer taskOffer, tipOID string) error {
	settled := c.withdrawAttempt(ctx, lease.Node, task, attempt, KindPublish, offer, tipOID)
	if settled.isAcknowledged {
		lease.Release()
	} else {
		lease.Leak(LeakUnacknowledgedCancel)
	}
	if isPublicationOutcomeKnown(settled) {
		// A known outcome after all: the node was still before its own
		// command, so the package failed at the publish stage exactly as a
		// publisher that reported its own failure would have.
		c.Log.Warn().Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
			Int("attempt", attempt).Str("phase", settled.phase).
			Str("code", CodeIntegrity).Str("category", CategoryIntegrity).
			Msg("the authorized publication was withdrawn before its command started")
		return c.refuseTask(task, lease.Node, attempt, fmt.Errorf(
			"the node was authorized to publish and stopped in the %s phase without starting the publish command, so nothing was published",
			settled.phase))
	}
	return c.reportUnknownPublication(task, attempt, lease.Node, repository, settled)
}

// isPublicationOutcomeKnown is the decision of §28.6, as one sentence.
//
// An authorized publisher leaves a knowable outcome under exactly one
// condition: it answered, and it answered that its own command had not begun.
// Everything else is unknown, and the two ways of being unknown are worth
// naming because they look nothing alike and mean the same thing. A node that
// acknowledged after its command started knows only that it was killed
// somewhere inside a registry upload. A node that never answered says nothing
// at all. Neither a missing reply nor a missing tag proves failure, so nothing
// is inferred from either.
func isPublicationOutcomeKnown(settled cancellation) bool {
	return settled.isAcknowledged && !settled.isCommandStarted
}

// reportUnknownPublication records one publication whose outcome cannot be
// established, and answers the failure the package carries.
//
// The remedy is the order §28.6 requires a lock diagnostic to name, and the
// run id is what makes it followable: the evidence for the one question an
// operator has to answer lives in this run's own coordination refs, as an
// authorization with no result beside it.
func (c *Coordinator) reportUnknownPublication(task string, attempt int, node, repository string,
	settled cancellation) error {
	c.rememberUnknownPublication(unknownPublication{Task: task, Attempt: attempt, Node: node,
		Repository: repository, IsQuiesced: settled.isAcknowledged})
	event := c.Log.Error().Str("run", c.Run).Str("task", task).Str("worker", node).
		Int("attempt", attempt).Bool("quiesced", settled.isAcknowledged).
		Str("code", CodePublicationUnknown).Str("category", CategoryPublicationUnknown)
	if settled.phase != "" {
		event = event.Str("phase", settled.phase)
	}
	event.Msg("the outcome of an authorized publication cannot be established")
	return NewIdentifiedDiagnostic(Identity{Run: c.Run, Worker: node, Task: task, Attempt: attempt},
		CodePublicationUnknown, CategoryPublicationUnknown,
		"%s was authorized to publish on %s and this run cannot establish whether the publication happened, so it makes no second attempt under that authorization: list the coordination refs of run %s (dispat-worker-*), find the authorization with no result beside it and confirm on %s that the publisher has stopped, check the registry for the version, delete the run's refs, and only then clear any lock this run retained",
		task, node, c.Run, node)
}
