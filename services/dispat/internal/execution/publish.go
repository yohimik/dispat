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
	lease, err := c.Pool.AcquireNear(ctx, space.BuildPlatforms, placement, c.producerOf(request))
	if err != nil {
		return release.StageOutcome{}, c.refuseTask(task, "", err)
	}
	return c.dispatchPublish(ctx, lease, task, request, authorize)
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
func (c *Coordinator) dispatchPublish(ctx context.Context, lease *Lease, task string,
	request release.StageRequest, authorize func(context.Context) error) (release.StageOutcome, error) {
	outcome := release.StageOutcome{Node: lease.Node}
	sources := c.dispatch.Sources(request.Release.Pkg.Name)
	dir, err := resolvePackageDir(sources, request)
	if err != nil {
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, err)
	}
	commits, err := c.captureInputs(ctx, sources)
	if err != nil {
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, err)
	}
	repositories, err := c.offerInputs(ctx, lease.Node, sources, commits)
	if err != nil {
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, err)
	}
	inputs, err := c.resolvePublishInputs(ctx, lease.Node, request, sources, dir)
	if err != nil {
		lease.Release()
		return outcome, c.refuseTask(task, lease.Node, err)
	}
	offer, err := c.offerTask(ctx, lease, KindPublish, task, dir, repositories, inputs, request)
	if err != nil {
		return outcome, err
	}
	defer offer.observer.forget(offer.branch)
	return c.awaitPublication(ctx, lease, task, outcome, offer, request, authorize)
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
	outcome release.StageOutcome, offer taskOffer, request release.StageRequest,
	authorize func(context.Context) error) (release.StageOutcome, error) {
	deadline := time.NewTimer(c.Timeouts.Task)
	defer deadline.Stop()
	isAuthorized := false
	for {
		select {
		case reply := <-offer.replies:
			if reply.kind == MessageResult {
				lease.Release()
				return c.readPublicationOutcome(task, outcome, reply.result)
			}
			outcome.Exports = formatOutputs(reply.ready.Exports)
			if err := c.authorizePublication(ctx, lease, task, offer, reply, authorize); err != nil {
				outcome.FailedPart = release.PartAuthorization
				return outcome, err
			}
			// The wait starts again, now for the only message that can say
			// what became of an effect this run authorized.
			isAuthorized = true
			deadline.Reset(c.Timeouts.Task)
		case <-deadline.C:
			if !isAuthorized {
				lease.Leak()
				return outcome, c.refuseTask(task, lease.Node, fmt.Errorf(
					"the node did not report within %s: the attempt is abandoned and the node is not used again by this run",
					c.Timeouts.Task))
			}
			lease.Leak()
			return outcome, c.resolveUnansweredPublication(task, lease.Node)
		case <-ctx.Done():
			lease.Leak()
			return outcome, fmt.Errorf("waiting for %s on %s: %w", task, lease.Node, ctx.Err())
		}
	}
}

// readPublicationOutcome turns one publisher's terminal result into what the
// executor does with it.
//
// It admits nothing, and that is the difference between this and a build. A
// publication produces an effect on a registry rather than an output set: the
// bytes it uploaded are the ones this run already admitted from the build, and
// what comes back is what its scripts exported and whether they succeeded.
func (c *Coordinator) readPublicationOutcome(task string, outcome release.StageOutcome,
	result Result) (release.StageOutcome, error) {
	// The exports of the beforePublish hook arrived with the ready message and
	// are already on the outcome; the result carries what the publish command
	// exported, and both belong to the release.
	hookExports := outcome.Exports
	outcome, err := c.readReportedOutcome(task, outcome, result)
	outcome.Exports = append(hookExports, outcome.Exports...)
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
	offer taskOffer, reply taskReply, authorize func(context.Context) error) error {
	waiting := offer.observer.find(offer.branch)
	if waiting == nil || waiting.isAuthorized {
		// No second withdrawal and no second authorization: an attempt this
		// run has already answered is an attempt whose effect may already have
		// happened, and the one thing that must not follow it is another
		// message telling a node to start.
		return NewIdentifiedDiagnostic(Identity{Run: c.Run, Worker: lease.Node, Task: task, Attempt: 1},
			CodeAuthority, CategoryAuthority,
			"%s asked to be authorized twice and an authorization is single use: no second effect may start under it",
			task)
	}
	if authorize != nil {
		if err := authorize(ctx); err != nil {
			return c.withdrawPublication(ctx, lease, task, offer, reply, err)
		}
	}
	waiting.isAuthorized = true
	authorized, err := c.advance(ctx, lease.Node, offer.branch, reply.commit, MessageGo,
		c.formatGo(reply, lease.Node, offer.branch))
	if err != nil {
		lease.Leak()
		return c.refuseTask(task, lease.Node, err)
	}
	c.recordOwnedRef(lease.Node, offer.branch, authorized)
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
		Str("package", reply.ready.Task).Str("commit", authorized).
		Int("attempt", reply.ready.Attempt).Msg("publication authorized")
	return nil
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
	offer taskOffer, reply taskReply, refused error) error {
	withdrawn, err := c.advance(ctx, lease.Node, offer.branch, reply.commit, MessageCancel,
		Withdrawal{
			Header:     c.formatPublishHeader(reply, lease.Node, offer.branch),
			Assignment: reply.ready.Assignment, Tip: reply.commit,
		})
	if err != nil {
		lease.Leak()
		c.Log.Warn().Err(err).Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
			Str("code", CodeTransport).Str("category", CategoryTransportCleanup).
			Msg("the publication could not be withdrawn")
		return refused
	}
	c.recordOwnedRef(lease.Node, offer.branch, withdrawn)
	c.Log.Warn().Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
		Str("commit", withdrawn).Str("code", CodeAuthority).Str("category", CategoryAuthority).
		Msg("publication withheld")
	c.settleWithdrawal(ctx, lease, task, offer.branch, withdrawn)
	return refused
}

// settleWithdrawal waits for the node to acknowledge a withdrawal, and gives
// its slot back when it does.
func (c *Coordinator) settleWithdrawal(ctx context.Context, lease *Lease, task, branch, withdrawn string) {
	acked, done := context.WithTimeout(context.WithoutCancel(ctx), c.Timeouts.Cancel)
	defer done()
	if !c.isWithdrawalAcknowledged(acked, lease.Node, branch, withdrawn) {
		lease.Leak()
		c.Log.Warn().Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
			Str("code", CodeTransport).Str("category", CategoryTransportCleanup).
			Msg("the withdrawal was not acknowledged and the node's capacity is held")
		return
	}
	lease.Release()
	c.Log.Debug().Str("run", c.Run).Str("task", task).Str("worker", lease.Node).
		Msg("the withdrawn attempt was acknowledged")
}

// isWithdrawalAcknowledged polls the attempt's branch until the node has
// acknowledged the withdrawal or the cancel wait runs out.
//
// The branch is re-read rather than watched through the poller: the
// acknowledgement follows an object this party wrote, so the poller's memo
// would not report it as a movement worth anything, and the party that needs
// the answer is this one.
func (c *Coordinator) isWithdrawalAcknowledged(ctx context.Context, node, branch, withdrawn string) bool {
	mailbox := c.mailboxes[node]
	interval := minimumPollInterval
	next := time.NewTimer(interval)
	defer next.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-next.C:
		}
		interval = min(interval*2, maximumPollInterval)
		next.Reset(interval)
		head, err := mailbox.Reread(ctx, branch)
		if err != nil || head.OID == "" || head.OID == withdrawn {
			continue
		}
		tip, err := mailbox.Inspect(ctx, head)
		if err == nil && tip.Kind == MessageAck {
			c.recordOwnedRef(node, branch, head.OID)
			return true
		}
	}
}

// resolveUnansweredPublication is what this run says about a publication it
// authorized and never heard the end of.
//
// It is one function on purpose: this is the single place the "outcome
// unknown" class is decided, and gate 12 extends exactly this with the
// quiescence evidence, the lock retention and the operator's remedy. What this
// build does is the safe half of it. No second attempt is made under this
// authorization, the package fails, its dependents are blocked and the run
// exits non-zero, because an effect that may or may not have happened is the
// one thing a release must never report as either.
func (c *Coordinator) resolveUnansweredPublication(task, node string) error {
	c.Log.Error().Str("run", c.Run).Str("task", task).Str("worker", node).
		Str("code", CodePublicationUnknown).Str("category", CategoryPublicationUnknown).
		Msg("the authorized publication was never reported")
	return NewIdentifiedDiagnostic(Identity{Run: c.Run, Worker: node, Task: task, Attempt: 1},
		CodePublicationUnknown, CategoryPublicationUnknown,
		"%s was authorized to publish on %s and reported nothing within %s: whether the publication happened cannot be established from here, so this run makes no second attempt under that authorization",
		task, node, c.Timeouts.Task)
}
