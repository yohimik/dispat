// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Local publication and output routing for computation stages. Release
// effects and native records remain on the orchestrator.

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

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
	for i := len(request.Descriptor.OwnOutputStages) - 1; i >= 0; i-- {
		if admitted := c.outputs.findStage(request.Release.Pkg.Name,
			request.Descriptor.OwnOutputStages[i]); admitted != nil {
			return admitted.node
		}
	}
	admitted := c.outputs.find(request.Release.Pkg.Name)
	if admitted == nil {
		return ""
	}
	return admitted.node
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
	stages := request.Descriptor.OwnOutputStages
	if len(stages) == 0 {
		stages = []string{"build"} // hand-built descriptors retain the original contract
	}
	for _, stage := range stages {
		own := c.outputs.findStage(packageName, stage)
		if own == nil && stage == "build" {
			own = c.outputs.find(packageName)
		}
		if own == nil {
			continue
		}
		branch, err := c.reachOutputs(ctx, node, own)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, AssignmentInput{
			Task: own.manifest.task, Package: packageName, Branch: branch,
			Commit: own.commit, Digest: own.manifest.digest,
			Path: resolveAnchoredPath(sources, request.Release.Pkg.Repository, dir),
		})
	}
	return inputs, nil
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
