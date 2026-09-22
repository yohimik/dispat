// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The orchestrator executing one of its own run's build frames (CCME §28.1:
// the orchestrator "MAY execute tasks locally under the same rules").
//
// Under the same rules is the whole of the design here. A frame placed on
// this machine runs the executor's own gating sequence, in the checkout the
// run was started in, holding one of this node's capacity slots and the
// shared side of the snapshot guard; and what it produces is then captured,
// described and admitted through exactly the code a worker's result goes
// through. Everything downstream therefore cannot tell the two apart: a
// consumer placed on a node is handed the same {branch, commit, digest}
// triple whichever machine produced the bytes, and it verifies them the same
// way.
//
// Two things are deliberately not done. Nothing is installed: the bytes were
// written into this checkout by the build itself, and installing them would
// be replacing a folder with itself. And nothing is pushed until a consumer
// needs it: the result lives in this repository's object store, and the relay
// that carries it to a node's mailbox is made lazily, once per endpoint, by
// the same routine that relays a worker's result across two mailboxes.

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// buildHere runs one build frame in this process and admits what it produced.
//
// The slot is given back on every path, and it is given back rather than
// leaked whatever the frame did: a frame that ran here ended here, so there
// is no machine left running something nobody can account for, which is the
// only thing a leaked slot is for.
func (c *Coordinator) buildHere(ctx context.Context, lease *Lease, task string,
	request release.StageRequest, here release.LocalFrame) (release.StageOutcome, error) {
	defer lease.Release()
	c.Log.Info().Str("run", c.Run).Str("task", task).
		Msg("task placed on the node that started the run")
	// The provider outputs this frame reads are already in this checkout: a
	// releasing provider's were installed by its own admission, and a prepared
	// one's by the admission of the preparation the caller waited for before
	// it asked for a node at all.
	//
	// The frame holds this node's pool slot and nothing else. It is not run
	// under the snapshot guard a version or syncLock frame holds, on purpose:
	// those frames write the tracked files other tasks are snapshotted from
	// and last moments, while a build lasts as long as the build does and
	// writes outputs no snapshot carries. Holding the guard across it would
	// make every dispatch that needs a fresh snapshot wait for the longest
	// build placed here, and this node takes a build exactly when the workers
	// are busy, which is when the next dispatch is about to be needed.
	what, err := here(ctx)
	if err != nil {
		// The executor's own sentence, unchanged: a build that failed on this
		// machine fails its package exactly as it did before any of this
		// existed, hooks, revert and onFail included.
		return release.StageOutcome{LocalFailure: what}, err
	}
	if err := c.admitLocalOutputs(ctx, task, request); err != nil {
		return release.StageOutcome{FailedPart: release.PartOutputs}, err
	}
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("status", StatusSucceeded).
		Msg("task finished")
	return release.StageOutcome{}, nil
}

// admitLocalOutputs describes what a frame that ran here produced and admits
// it through the path a worker's result takes.
//
// The capture reads the working tree, so it holds the same shared guard the
// frame did; the description is bound to this run, this plan, this task and
// this machine, exactly as a worker binds its own; and the result it travels
// in is signed with this run's secret, because the node that will consume it
// verifies a signature and knows nothing about who produced the bytes.
func (c *Coordinator) admitLocalOutputs(ctx context.Context, task string,
	request release.StageRequest) error {
	if len(request.Release.Pkg.Space.BuildOutputs) == 0 {
		return nil
	}
	manifest, err := c.captureLocalOutputs(ctx, task, request)
	if err != nil {
		return c.refuseOutputSet(task, "", OutputFaultReason(err), err)
	}
	owner, err := c.resolveOwnerRepository(request)
	if err != nil {
		return c.refuseOutputSet(task, "", "", err)
	}
	commit, err := c.reportLocalResult(ctx, owner, manifest)
	if err != nil {
		return c.refuseOutputSet(task, "", "", err)
	}
	return c.admitOutputs(ctx, task, producedOutputs{
		node: c.Local.Name, store: owner, commit: commit,
		manifest: manifest, attempt: manifest.Attempt, isInstalledHere: true,
	}, request)
}

// captureLocalOutputs turns this checkout's declared output folders into a
// tree and the manifest that describes it, under the same rules and the same
// refusals a worker capturing its own is held to.
func (c *Coordinator) captureLocalOutputs(ctx context.Context, task string,
	request release.StageRequest) (*OutputManifest, error) {
	sources := c.dispatch.Sources(request.Release.Pkg.Name)
	dir, err := resolvePackageDir(sources, request)
	if err != nil {
		return nil, err
	}
	owner, err := c.resolveOwnerRepository(request)
	if err != nil {
		return nil, err
	}
	c.guard.RLock()
	defer c.guard.RUnlock()
	manifest, err := CaptureOutputs(ctx, CaptureRequest{
		Git:         owner,
		Dir:         request.Dir,
		PackagePath: dir,
		Roots:       request.Release.Pkg.Space.BuildOutputs,
		Limits:      c.Limits,
		Manifest: OutputManifest{
			Run: c.Run, PlanDigest: c.PlanDigest, Task: task, Attempt: 1,
			Generation: c.Generation, Node: c.Local.Name,
			Package: request.Release.Pkg.Name, Version: request.Release.Next.String(),
			Platform: Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
			Inputs:   c.formatLocalInputs(request.Release.Pkg.Name),
		},
	})
	if err != nil {
		return nil, err
	}
	if err := checkManifestSize(manifest, c.Limits); err != nil {
		return nil, err
	}
	return manifest, nil
}

// formatLocalInputs is what this build consumed, as a manifest names it: the
// admitted output set of every provider whose bytes were in this checkout
// when the frame ran.
func (c *Coordinator) formatLocalInputs(packageName string) []ManifestInput {
	providers := c.dispatch.Inputs(packageName)
	inputs := make([]ManifestInput, 0, len(providers))
	for _, provider := range providers {
		admitted := c.outputs.find(provider.Package)
		if admitted == nil {
			continue
		}
		inputs = append(inputs, ManifestInput{
			Package: provider.Package, OutputTree: admitted.manifest.OutputTree,
			Digest: admitted.manifest.Digest,
		})
	}
	return inputs
}

// reportLocalResult writes the result a consumer reads, into the store the
// outputs were captured in, and answers the commit it sits at.
//
// It is a root commit and it is pushed nowhere: what makes it reachable to a
// consuming node is the relay branch the input resolution creates when a
// consumer is actually placed on a mailbox, which is the same lazy copy two
// workers with separate mailboxes already go through.
func (c *Coordinator) reportLocalResult(ctx context.Context, store *gitx.LocalGitx,
	manifest *OutputManifest) (string, error) {
	document, err := json.Marshal(Result{
		Header: Header{
			Protocol: ProtocolVersion, Kind: KindBuild, Run: c.Run,
			PlanDigest: c.PlanDigest, Task: manifest.Task, Attempt: manifest.Attempt,
			Generation: c.Generation, Node: c.Local.Name,
			IssuedAt: time.Now().UTC().Format(time.RFC3339),
		},
		Status:   StatusSucceeded,
		Platform: manifest.Platform,
		Outputs:  manifest,
	})
	if err != nil {
		return "", fmt.Errorf("execution: writing the result of a build run here: %w", err)
	}
	commit, err := formatMessageCommit(ctx, store, c.Signer, MessageResult, document, nil,
		carriedOutputs(manifest))
	if err != nil {
		return "", err
	}
	c.Log.Debug().Str("run", c.Run).Str("task", manifest.Task).Str("commit", commit).
		Int("files", manifest.Files).Int64("bytes", manifest.Bytes).
		Msg("outputs captured here")
	return commit, nil
}
