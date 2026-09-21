// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Building a provider this run does not release (CCME §28.5).
//
// A consumer's task inputs have to include every build dependency it reads,
// and whether the provider happens to be releasing in the same run is not
// something the consumer's build can be made to care about. A package that
// was not touched, that is held, that was deselected or that versions to
// nothing still produces the bytes its consumers compile against, so this run
// builds it: once, under a non-release environment, with no version, no tag,
// no changelog, no record, no event claiming a release and no entry in the
// plan. Plan(I) is what it was before, and so is its digest.
//
// The once-ness is the whole design here. Two consumers of one provider are
// two tasks that may ask at the same moment, so the first caller does the work
// and everybody else waits on the same answer; a preparation that failed is
// shared exactly as one that succeeded, because a second attempt at a build
// that just failed would be a second answer to a question this run already
// has one for. A waiter that is cancelled stops waiting and nothing else: the
// work belongs to the run, not to whichever consumer happened to ask first.
//
// Nothing here consults the graph. Which providers need preparing is the
// workspace's answer, handed in as the input closure; cycles are refused as
// E200 before a plan exists, so the recursion below walks something acyclic
// and no second check is added that nothing could ever take.

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// PreparedProvider is one provider's build frame as this run would execute it
// without releasing it.
//
// Both halves are needed because the placement is decided after the frame is
// described: `runOnly` and `buildPlatforms` are the provider's own, so a
// prepared build obeys them exactly as its release build would, and the frame
// therefore has to be ready to travel or to be run here before anybody knows
// which it will be.
type PreparedProvider struct {
	// Request is the frame as a node receives it, in the same shape a release
	// build of the same package travels in.
	Request release.StageRequest
	// Here is the frame as this process runs it, which is what a placement on
	// the orchestrator executes. It is the caller's own sequence rather than
	// this package's, so a prepared build that stayed at home runs the
	// commands the same runner would have run for any other script of the
	// repository.
	Here release.LocalFrame
}

// PreparedRecord is what one prepared provider contributes to the run's
// report: a package that computed something and published nothing.
//
// The three words are the summary's own vocabulary (§28.9): what became of
// the computation, what became of the outputs, and what became of the
// publication. A prepared provider always answers the last one with "none",
// and that is the point of printing it at all: a reader seeing a package in
// the summary has to be able to tell at a glance that this run released
// nothing of it.
type PreparedRecord struct {
	Package     string
	Task        string
	Node        string
	Computation string
	Outputs     string
	Publication string
}

// The outcomes a prepared provider's record states. They are words rather than
// booleans because the summary prints them, and because "completed" and
// "admitted" are answers to two different questions that a single flag would
// run together.
const (
	// PreparationCompleted is a build frame that ran to the end.
	PreparationCompleted = "completed"
	// PreparationAdmitted is an output set this run verified and kept.
	PreparationAdmitted = "admitted"
	// PreparationFailed is either half that did not happen.
	PreparationFailed = "failed"
	// PreparationNone is the answer a prepared provider always gives about
	// publication, and the one an output set gives when the build never
	// produced it.
	PreparationNone = "none"
)

// preparation is one provider's preparation as every consumer of it sees it:
// a result that is not there yet, and the channel that says when it is.
//
// The fields are written by the caller that owns the preparation and read by
// the others only after the channel is closed, which is what makes the
// closing the whole of the synchronisation.
type preparation struct {
	done chan struct{}
	node string
	err  error
}

// prepareProviderOutputs makes sure every provider output this build reads is
// present before a command of it starts.
//
// For a provider this run releases there is nothing to do: its own admission
// installed its outputs into this checkout and named them to every node that
// needs them, whichever machine built it. What is left is the provider this
// run does NOT release and whose outputs a consumer still needs, and that is
// what is built here.
//
// A provider that could not be prepared fails its consumer at the build
// stage, naming the provider: the bytes the consumer was going to read do not
// exist, so there is nothing for it to build against and no reason to start a
// command that would fail on a missing folder.
func (c *Coordinator) prepareProviderOutputs(ctx context.Context, task string,
	request release.StageRequest) error {
	consumer := request.Release.Pkg.Name
	for _, provider := range c.dispatch.Inputs(consumer) {
		if !provider.IsPrepared {
			continue
		}
		if err := c.ensurePrepared(ctx, provider.Package); err != nil {
			return NewIdentifiedDiagnostic(Identity{Run: c.Run, Task: task},
				CodeIntegrity, CategoryIntegrity,
				"%s reads the build outputs of %s, which this run does not release and could not build: %w",
				consumer, provider.Package, err)
		}
	}
	return nil
}

// ensurePrepared builds one provider this run does not release, once, however
// many consumers ask for it and however many ask at the same moment.
func (c *Coordinator) ensurePrepared(ctx context.Context, packageName string) error {
	pending, isOwned := c.claimPreparation(packageName)
	if !isOwned {
		return pending.await(ctx, packageName)
	}
	pending.node, pending.err = c.prepareProvider(ctx, packageName)
	c.rememberPreparation(packageName, pending)
	close(pending.done)
	return pending.err
}

// claimPreparation answers the future for one provider, and whether this
// caller is the one that has to fill it.
func (c *Coordinator) claimPreparation(packageName string) (*preparation, bool) {
	c.preparing.Lock()
	defer c.preparing.Unlock()
	if pending, isStarted := c.preparations[packageName]; isStarted {
		return pending, false
	}
	pending := &preparation{done: make(chan struct{})}
	c.preparations[packageName] = pending
	return pending, true
}

// await waits for the caller that owns the preparation to finish it, or for
// this consumer's own context to end.
//
// A waiter leaving takes nothing with it. The preparation belongs to the run
// and the other consumers of the same provider are still waiting on it, so a
// cancelled consumer cancels a consumer and never a build.
func (p *preparation) await(ctx context.Context, packageName string) error {
	select {
	case <-p.done:
		return p.err
	case <-ctx.Done():
		return fmt.Errorf("waiting for the build outputs of %s: %w", packageName, ctx.Err())
	}
}

// rememberPreparation records what became of one preparation, in the order
// this run prepared them, so that the report can print them beside the
// packages the run released.
func (c *Coordinator) rememberPreparation(packageName string, pending *preparation) {
	c.preparing.Lock()
	defer c.preparing.Unlock()
	c.preparedRecords = append(c.preparedRecords, formatPreparedRecord(packageName, pending))
}

// formatPreparedRecord is what one finished preparation says about itself: a
// computation that ran or did not, the output set that came of it, and the
// publication that never happens either way.
func formatPreparedRecord(packageName string, pending *preparation) PreparedRecord {
	record := PreparedRecord{Package: packageName, Task: formatPrepareTask(packageName),
		Node: pending.node, Publication: PreparationNone}
	if pending.err != nil {
		record.Computation, record.Outputs = PreparationFailed, PreparationNone
		return record
	}
	record.Computation, record.Outputs = PreparationCompleted, PreparationAdmitted
	return record
}

// PreparedRecords is what this run built without releasing it, in the order
// the preparations finished.
//
// It is exported because the run's report is assembled outside this package
// and a prepared provider is the one piece of work a summary could not
// otherwise know about: it is in no plan, it has no result and it produced no
// event. The copy is deliberate: the slice keeps growing while the run does,
// and a reader iterating the coordinator's own would be iterating something
// another task is appending to.
func (c *Coordinator) PreparedRecords() []PreparedRecord {
	c.preparing.Lock()
	defer c.preparing.Unlock()
	return append([]PreparedRecord(nil), c.preparedRecords...)
}

// prepareProvider runs one provider's build frame where this run places it and
// admits what it produced, answering the node it ran on.
//
// The order is the consumer's own, one level down: what this provider reads is
// prepared before it runs, then a node slot is taken, then the frame is
// executed. Its own `runOnly` and `buildPlatforms` decide the placement,
// because a package that may only build on linux may only build on linux
// whether or not this run is releasing it.
func (c *Coordinator) prepareProvider(ctx context.Context, packageName string) (string, error) {
	task := formatPrepareTask(packageName)
	prepared, err := c.dispatch.Prepare(packageName)
	if err != nil {
		return "", c.refuseTask(task, "", err)
	}
	if err := c.prepareProviderOutputs(ctx, task, prepared.Request); err != nil {
		return "", err
	}
	space := prepared.Request.Release.Pkg.Space
	lease, err := c.Pool.Acquire(ctx, space.BuildPlatforms,
		ResolveStagePlacement(StageBuild, space.RunOnly.ResolveBuild(), len(space.LoginScript) > 0))
	if err != nil {
		return "", c.refuseTask(task, "", err)
	}
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("package", packageName).
		Str("version", prepared.Request.Release.Next.String()).
		Msg("building a provider this run does not release")
	if lease.IsLocal {
		_, err := c.buildHere(ctx, lease, task, prepared.Request, prepared.Here)
		return c.Local.Name, err
	}
	_, err = c.dispatchBuild(ctx, lease, KindPrepare, task, prepared.Request)
	return lease.Node, err
}

// formatPrepareTask names one preparation. It is the package's name under the
// preparation's own kind rather than under "build", so that a log, a branch
// name and a summary all say which of the two a reader is looking at without
// having to know which packages this run is releasing.
func formatPrepareTask(packageName string) string { return packageName + ":" + KindPrepare }
