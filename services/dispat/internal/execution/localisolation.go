// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// A frame placed on the orchestrator uses a private source checkout when its
// package declares output roots. This gives it the same graph-selected input
// sets as a worker: another ready stage's files never become an accidental
// input merely because both frames happened to run on this machine.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

type localWorktree struct {
	git *gitx.LocalGitx
	dir string
}

type localIsolatedFrame struct {
	lease   *Lease
	task    string
	attempt int
	request release.StageRequest
	here    release.LocalFrame
}

func (c *Coordinator) runIsolatedHere(ctx context.Context, frame localIsolatedFrame) (release.StageOutcome, error) {
	lease, task, attempt, request, here := frame.lease, frame.task, frame.attempt, frame.request, frame.here
	if lease != nil {
		defer lease.Release()
	}
	root, err := os.MkdirTemp("", "dispat-local-frame-")
	if err != nil {
		return release.StageOutcome{FailedPart: release.PartInputs}, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	sources := c.dispatch.Sources(request.Release.Pkg.Name)
	commits, err := c.captureInputs(ctx, sources)
	if err != nil {
		return release.StageOutcome{FailedPart: release.PartInputs}, err
	}
	checkoutRoot := filepath.Join(root, "checkout")
	var added []localWorktree
	defer func() {
		for i := len(added) - 1; i >= 0; i-- {
			plumbing := gitx.NewPlumbing(added[i].git)
			plumbing.WorktreeRemove(context.WithoutCancel(ctx), added[i].dir)
			plumbing.WorktreePrune(context.WithoutCancel(ctx))
			if err := plumbing.Err(); err != nil {
				c.Log.Warn().Err(err).Str("task", task).Msg("isolated local checkout was not removed")
			}
		}
	}()
	ownerPath := ""
	var owner *gitx.LocalGitx
	var outputPaths []release.OutputPathMapping
	for i, source := range sources {
		worktree := filepath.Join(checkoutRoot, filepath.FromSlash(source.Path))
		if err := os.MkdirAll(filepath.Dir(worktree), 0o700); err != nil {
			return release.StageOutcome{FailedPart: release.PartInputs}, err
		}
		store := c.dispatch.OpenRepository(source.Dir)
		plumbing := gitx.NewPlumbing(store)
		plumbing.WorktreeAdd(ctx, worktree, commits[i])
		if err := plumbing.Err(); err != nil {
			return release.StageOutcome{FailedPart: release.PartInputs}, err
		}
		added = append(added, localWorktree{git: store, dir: worktree})
		if roots := c.dispatch.OutputRoots[source.Dir]; len(roots) > 0 {
			sourceRoot, err := filepath.EvalSymlinks(source.Dir)
			if err != nil {
				return release.StageOutcome{FailedPart: release.PartInputs}, err
			}
			taskRoot, err := filepath.EvalSymlinks(worktree)
			if err != nil {
				return release.StageOutcome{FailedPart: release.PartInputs}, err
			}
			outputPaths = append(outputPaths, release.OutputPathMapping{
				SourceRoot: sourceRoot, TaskRoot: taskRoot, Roots: roots})
		}
		if source.Name == request.Release.Pkg.Repository {
			owner, ownerPath = store, source.Path
		}
	}
	if owner == nil {
		return release.StageOutcome{FailedPart: release.PartInputs},
			fmt.Errorf("execution: no source repository owns %s", request.Release.Pkg.Name)
	}
	packagePath, err := resolvePackageDir(sources, request)
	if err != nil {
		return release.StageOutcome{FailedPart: release.PartInputs}, err
	}
	packageDir := filepath.Join(checkoutRoot, filepath.FromSlash(ownerPath), filepath.FromSlash(packagePath))
	// The prepared source snapshot retains initial root contents and excludes
	// this run's later output mutations; graph ancestors are installed below.
	inputs, err := c.installIsolatedInputs(ctx, isolatedInputSpec{
		root: root, checkoutRoot: checkoutRoot, packageDir: packageDir, request: request})
	if err != nil {
		return release.StageOutcome{FailedPart: release.PartInputs}, err
	}
	runner := &script.ShellRunner{Shell: c.dispatch.Shell(request.Dir), Log: c.Log}
	what, err := here(release.WithLocalStageRuntime(ctx, release.LocalStageRuntime{
		Dir: packageDir, Runner: runner, Env: formatInheritedBlanks(os.Environ()), OutputPaths: outputPaths}))
	if err != nil {
		return release.StageOutcome{LocalFailure: what}, err
	}
	if !request.Descriptor.IsProducingOutputs {
		return release.StageOutcome{}, nil
	}
	repositories := make([]ManifestRepository, 0, len(sources))
	for i, source := range sources {
		repositories = append(repositories, ManifestRepository{Name: source.Name, Snapshot: commits[i]})
	}
	manifest, err := CaptureOutputs(ctx, CaptureRequest{
		Git: owner, Dir: packageDir, PackagePath: packagePath,
		Roots: request.Release.Pkg.Space.BuildOutputs, Limits: c.Limits,
		IsAbsentRootEmpty: request.Stage != StageBuild,
		Manifest: OutputManifest{Run: c.Run, PlanDigest: c.PlanDigest, Task: task,
			Attempt: attempt, Generation: c.Generation, Node: c.Local.Name,
			Package: request.Release.Pkg.Name, Version: request.Release.Next.String(),
			Repositories: repositories, Platform: Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
			Inputs: inputs},
	})
	if err != nil {
		return release.StageOutcome{FailedPart: release.PartOutputs},
			c.refuseOutputSet(task, "", OutputFaultReason(err), err)
	}
	if err := checkManifestSize(manifest, c.Limits); err != nil {
		return release.StageOutcome{FailedPart: release.PartOutputs},
			c.refuseOutputSet(task, "", OutputFaultReason(err), err)
	}
	commit, err := c.reportLocalResult(ctx, owner, manifest)
	if err != nil {
		return release.StageOutcome{FailedPart: release.PartOutputs}, c.refuseOutputSet(task, "", "", err)
	}
	if err := c.admitOutputs(ctx, task, producedOutputs{node: c.Local.Name,
		store: owner, commit: commit, manifest: manifest, attempt: manifest.Attempt}, request); err != nil {
		return release.StageOutcome{FailedPart: release.PartOutputs}, err
	}
	return release.StageOutcome{}, nil
}

type isolatedInputSpec struct {
	root, checkoutRoot, packageDir string
	request                        release.StageRequest
}

// installIsolatedInputs uses admitted, signed results rather than the shared
// checkout's current root contents. Several own-stage sets are installed as
// one graph-aware replacement, including deletions made by descendants.
func (c *Coordinator) installIsolatedInputs(ctx context.Context, spec isolatedInputSpec) ([]ManifestInput, error) {
	root, checkoutRoot, packageDir, request := spec.root, spec.checkoutRoot, spec.packageDir, spec.request
	if request.Descriptor.Inputs == release.InputsNone {
		return nil, nil
	}
	var installed []ManifestInput
	for _, provider := range c.dispatch.Inputs(request.Release.Pkg.Name) {
		admitted := c.outputs.find(provider.Package)
		if admitted == nil {
			continue
		}
		manifest, objects, err := c.readAdmittedLocalInput(ctx, admitted, request)
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(checkoutRoot, filepath.FromSlash(provider.Path))
		if err := InstallOutputSets(ctx, []InstallRequest{{Git: admitted.store, Manifest: manifest,
			Objects: objects, Dir: dir, Staging: filepath.Join(root, "provider-"+formatIdentityHash(provider.Package)),
			Log: c.Log}}); err != nil {
			return nil, err
		}
		installed = append(installed, ManifestInput{Package: provider.Package,
			OutputTree: manifest.OutputTree, Digest: manifest.Digest})
	}
	if request.Descriptor.Inputs != release.InputsOwnOutputs {
		return installed, nil
	}
	stages := request.Descriptor.OwnOutputStages
	if len(stages) == 0 {
		stages = []string{StageBuild}
	}
	var sets []InstallRequest
	for _, stage := range stages {
		admitted := c.outputs.findStage(request.Release.Pkg.Name, stage)
		if admitted == nil && stage == StageBuild {
			admitted = c.outputs.find(request.Release.Pkg.Name)
		}
		if admitted == nil {
			continue
		}
		manifest, objects, err := c.readAdmittedLocalInput(ctx, admitted, request)
		if err != nil {
			return nil, err
		}
		sets = append(sets, InstallRequest{Git: admitted.store, Manifest: manifest,
			Objects: objects, Dir: packageDir, Staging: filepath.Join(root, "own"), Log: c.Log})
		installed = append(installed, ManifestInput{Package: request.Release.Pkg.Name,
			OutputTree: manifest.OutputTree, Digest: manifest.Digest})
	}
	if err := InstallOutputSets(ctx, sets); err != nil {
		return nil, err
	}
	return installed, nil
}

// prepareLocalPublishInputs retains the authoritative checkout for a local
// publisher, including scripts that call dispat commit. Every gating producer
// has finished before publish, so its graph-selected output sets can replace
// the shared root as one verified view immediately before the frame starts.
func (c *Coordinator) prepareLocalPublishInputs(ctx context.Context, request release.StageRequest) error {
	stages := request.Descriptor.OwnOutputStages
	if len(stages) == 0 {
		stages = []string{StageBuild}
	}
	var sets []InstallRequest
	for _, stage := range stages {
		admitted := c.outputs.findStage(request.Release.Pkg.Name, stage)
		if admitted == nil && stage == StageBuild {
			admitted = c.outputs.find(request.Release.Pkg.Name)
		}
		if admitted == nil {
			continue
		}
		manifest, objects, err := c.readAdmittedLocalInput(ctx, admitted, request)
		if err != nil {
			return err
		}
		sets = append(sets, InstallRequest{Git: admitted.store, Manifest: manifest,
			Objects: objects, Dir: request.Dir, Log: c.Log})
	}
	if len(sets) == 0 {
		return nil
	}
	staging, err := os.MkdirTemp(filepath.Dir(request.Dir), ".dispat-publish-inputs-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	for i := range sets {
		sets[i].Staging = staging
	}
	c.guard.RLock()
	defer c.guard.RUnlock()
	return InstallOutputSets(ctx, sets)
}

func (c *Coordinator) readAdmittedLocalInput(ctx context.Context, admitted *admittedOutputs,
	request release.StageRequest) (*OutputManifest, []string, error) {
	mailbox := &GitMailbox{plumbing: admitted.store, signer: c.Signer}
	tree, err := mailbox.readTree(ctx, admitted.commit)
	if err != nil {
		return nil, nil, fmt.Errorf("execution: admitted output result cannot be read: %w", err)
	}
	if tree.kind != MessageResult {
		return nil, nil, fmt.Errorf("execution: admitted output result has kind %s", tree.kind)
	}
	document, err := mailbox.Read(ctx, ChainTip{Kind: MessageResult,
		document: tree.document, signature: tree.signature}, c.Limits.MaxManifestBytes)
	if err != nil {
		return nil, nil, err
	}
	var result Result
	if err := json.Unmarshal(document, &result); err != nil {
		return nil, nil, fmt.Errorf("execution: admitted output result cannot be decoded: %w", err)
	}
	if result.Outputs == nil {
		return nil, nil, fmt.Errorf("execution: admitted output result has no manifest")
	}
	manifest := result.Outputs
	validated, err := ValidateOutputs(ctx, admitted.store, manifest, OutputExpectation{
		Run: c.Run, PlanDigest: c.PlanDigest, Generation: c.Generation,
		Task: admitted.manifest.task, Roots: manifest.Roots,
		Platforms: request.Release.Pkg.Space.BuildPlatforms,
		Digest:    admitted.manifest.digest, Limits: c.Limits,
	})
	if err != nil {
		return nil, nil, err
	}
	return manifest, validated.Objects, nil
}
