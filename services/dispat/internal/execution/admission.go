// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// What the orchestrator does with the outputs a node reports (CCME §28.5).
//
// Three things, in this order and nowhere else. It admits: the manifest is
// held to the same rules the consuming node will hold it to, against the roots
// the PLAN declares rather than the ones the manifest claims, so a node cannot
// widen what it is allowed to produce. It installs: the bytes go into this
// machine's own checkout of the package, because the stages that follow a
// delegated build here (publication, recording, the hooks that upload files)
// read the working tree and know nothing about mailboxes. And it remembers:
// one admitted set per package, named by the exact object it was read at, so
// that every consumer of that package is told about exactly the set that was
// admitted rather than about whatever the branch holds later.
//
// A set that is refused is refused whole. The package fails at its build
// stage and nothing at all is put in its place: no registry contents, no
// second build under a different input, no leftover folder from an earlier
// run. Whether a consumer is then attempted is the task graph's ordinary
// decision (a package with changes of its own is), and a consumer that is
// attempted finds the bytes absent and fails on them, which is what §28.5
// means by failing the prerequisite.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// admittedOutputs retains one verified result commit and its manifest identity.
// A later assignment imports that commit into its own signed carrier.
type admittedOutputs struct {
	node     string
	commit   string
	manifest admittedManifest
	// Worker results live in the run store; local results can live in the
	// package repository of a composed workspace until imported.
	store *gitx.LocalGitx
}

// admittedManifest is what the run keeps of an admitted manifest. The entries
// are left behind on purpose: they are checked and installed inside the
// admission, every later reader only names the set or reports its totals, and
// a record that kept them would hold every file of every package for the
// whole run.
type admittedManifest struct {
	task        string
	packageName string
	digest      string
	outputTree  string
	files       int
	bytes       int64
}

// newAdmittedManifest keeps the naming fields and totals of one manifest.
func newAdmittedManifest(manifest *OutputManifest) admittedManifest {
	return admittedManifest{
		task: manifest.Task, packageName: manifest.Package, digest: manifest.Digest,
		outputTree: manifest.OutputTree, files: manifest.Files, bytes: manifest.Bytes,
	}
}

// outputRegistry is the run's record of admitted package and stage output sets.
//
// One owner: the coordinator. Builds run concurrently, so the map is written
// by whichever dispatch finished and read by whichever dispatch is starting,
// and the mutex is what makes "the outputs of this package" one answer.
type outputRegistry struct {
	mu       sync.Mutex
	admitted map[string]*admittedOutputs
	stages   map[string]*admittedOutputs
	latest   map[string]*admittedOutputs
}

// newOutputRegistry opens an empty record.
func newOutputRegistry() *outputRegistry {
	return &outputRegistry{admitted: map[string]*admittedOutputs{},
		stages: map[string]*admittedOutputs{}, latest: map[string]*admittedOutputs{}}
}

// remember records one package's admitted set, replacing nothing: a package is
// built once per run, and a second admission would be a second answer to a
// question that has one.
func (r *outputRegistry) remember(packageName string, admitted *admittedOutputs) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.admitted[packageName] = admitted
}

func stageOutputKey(packageName, stage string) string { return packageName + "\x00" + stage }

func (r *outputRegistry) rememberStage(packageName, stage string, admitted *admittedOutputs) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stages[stageOutputKey(packageName, stage)] = admitted
	r.latest[packageName] = admitted
}

// findLatest returns the last verified output set installed for a package.
// A later stage's complete root supersedes the build root at publication.
func (r *outputRegistry) findLatest(packageName string) *admittedOutputs {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.latest[packageName]
}

func (r *outputRegistry) findStage(packageName, stage string) *admittedOutputs {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stages[stageOutputKey(packageName, stage)]
}

// find answers what was admitted for one package, and nil for a package that
// produced nothing this run.
func (r *outputRegistry) find(packageName string) *admittedOutputs {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.admitted[packageName]
}

// producedOutputs is one output set on its way into the run's record: where
// its bytes are, how a consumer would be pointed at them, and whether this
// checkout still has to receive them.
//
// It exists so that admission is one function for both producers. A worker's
// set arrives through a mailbox and has to be installed here before anything
// that reads the working tree runs; a set this machine built is already in
// the working tree it was captured from, and installing it would be copying a
// folder over itself. Everything else about admitting the two is identical,
// and the point of one function is that it cannot become two rules.
type producedOutputs struct {
	node     string
	store    *gitx.LocalGitx
	commit   string
	manifest *OutputManifest
	// attempt is the attempt of the task that produced the set, which the
	// manifest is bound to: a task placed again after its assignment queued
	// unclaimed produces its outputs under its second or third attempt, and a
	// set held to the first would be refused for being exactly what it is.
	attempt int
	// isInstalledHere says the bytes are already where this checkout needs
	// them, which is true of exactly one producer: this machine.
	isInstalledHere bool
}

// admitOutputs holds one producer's outputs to the plan's own declaration and
// installs what survives into this machine's checkout.
//
// A package that declares no outputs admits none, whatever was reported: the
// declaration is the configuration's, so a set nobody asked for describes
// files no consumer was ever going to be told about.
func (c *Coordinator) admitOutputs(ctx context.Context, task string, produced producedOutputs,
	request release.StageRequest) error {
	roots := request.Release.Pkg.Space.BuildOutputs
	if len(roots) == 0 {
		return nil
	}
	if produced.manifest == nil {
		return c.refuseOutputSet(task, produced.node, ReasonBytesMissing,
			fmt.Errorf("no build outputs were reported for a package that declares %d", len(roots)))
	}
	validated, err := ValidateOutputs(ctx, produced.store, produced.manifest, OutputExpectation{
		Run: c.Run, PlanDigest: c.PlanDigest, Generation: c.Generation, Task: task, Attempt: produced.attempt,
		Roots: roots, Platforms: request.Release.Pkg.Space.BuildPlatforms, Limits: c.Limits,
	})
	if err != nil {
		return c.refuseOutputSet(task, produced.node, OutputFaultReason(err), err)
	}
	took := time.Now()
	if !produced.isInstalledHere {
		if err := c.installLocally(ctx, request, InstallRequest{
			Git: produced.store, Manifest: produced.manifest, Objects: validated.Objects,
		}); err != nil {
			return c.refuseOutputSet(task, produced.node, OutputFaultReason(err), err)
		}
	}
	accepted := &admittedOutputs{
		node:   produced.node,
		commit: produced.commit, manifest: newAdmittedManifest(produced.manifest), store: produced.store,
	}
	stage := request.Stage
	if stage == "" {
		_, stage, _ = strings.Cut(task, ":")
	}
	c.outputs.rememberStage(request.Release.Pkg.Name, stage, accepted)
	if stage == "build" {
		c.outputs.remember(request.Release.Pkg.Name, accepted)
	}
	verified := c.Log.Debug().Str("run", c.Run).Str("task", task).
		Int("files", validated.Files).Int64("bytes", validated.Bytes).Dur("took", time.Since(took))
	admitted := c.Log.Info().Str("run", c.Run).Str("task", task).
		Str("package", request.Release.Pkg.Name).Int("files", validated.Files).Int64("bytes", validated.Bytes)
	// A set this machine produced names no worker: the writer of the line is
	// the node it is about, and gate 7b's rule is that `worker` names another
	// node or nothing at all.
	if !produced.isInstalledHere {
		verified = verified.Str("worker", produced.node)
		admitted = admitted.Str("worker", produced.node)
	}
	verified.Msg("outputs verified")
	admitted.Msg("outputs admitted")
	return nil
}

// refuseOutputSet is the one refusal a rejected output set produces: the rule
// it broke and the work it was about, and never anything the set said.
//
// The node is named only when it is another machine: a set this run produced
// itself is refused on a line that already says who wrote it, and repeating
// that name in `worker` would read as a report about somebody else.
func (c *Coordinator) refuseOutputSet(task, node string, reason OutputReason, err error) error {
	event := c.Log.Warn().Str("run", c.Run).Str("task", task)
	if node != "" {
		event = event.Str("worker", node)
	}
	event.Str("reason", string(reason)).Str("code", CodeIntegrity).
		Str("category", CategoryIntegrity).Msg("outputs rejected")
	return NewIdentifiedDiagnostic(Identity{Run: c.Run, Worker: node, Task: task, Attempt: 1},
		CodeIntegrity, CategoryIntegrity,
		"the build outputs %s reported cannot be used (%s): %w", task, reason, err)
}

// installLocally puts an admitted set into this machine's own checkout of the
// package that produced it.
//
// The set is assembled in the owning repository's private Git directory, so
// Git cannot record the staged bytes, and installing each root is a rename
// rather than a copy. When that rename fails, as it does for a linked
// worktree whose private Git directory is on another filesystem, the set is
// assembled again in a private folder beside the outermost checkout (see
// installStaged). The shared side of the snapshot guard is held while it
// happens.
func (c *Coordinator) installLocally(ctx context.Context, request release.StageRequest,
	install InstallRequest) error {
	owner, err := c.resolveOwnerRepository(request)
	if err != nil {
		return err
	}
	index, err := owner.IndexPath(ctx)
	if err != nil {
		return fmt.Errorf("execution: locating the private folder of %s: %w", owner.Dir, err)
	}
	c.guard.RLock()
	defer c.guard.RUnlock()
	install.Dir, install.Log = request.Dir, c.Log
	return installStaged(ctx, outputStagingSpec{
		indexPath: index, ownerDir: owner.Dir, run: c.Run, packageName: install.Manifest.Package,
	}, install, InstallOutputs)
}

// resolveOwnerRepository opens the repository the package being built belongs
// to, which in a composed workspace is one of several and is never assumed to
// be the one the run was started in.
func (c *Coordinator) resolveOwnerRepository(request release.StageRequest) (*gitx.LocalGitx, error) {
	for _, source := range c.dispatch.Sources(request.Release.Pkg.Name) {
		if source.Name == request.Release.Pkg.Repository {
			return c.dispatch.OpenRepository(source.Dir), nil
		}
	}
	return nil, fmt.Errorf("execution: the input closure of %s names no repository %q to install its outputs into",
		request.Release.Pkg.Name, request.Release.Pkg.Repository)
}

// resolveTaskInputs is what one package's build consumes: the admitted output
// set of every package in its provider closure that declares outputs, each
// named on a branch the node this task is going to can fetch from.
func (c *Coordinator) resolveTaskInputs(ctx context.Context, node, packageName string) ([]AssignmentInput, error) {
	providers := c.dispatch.Inputs(packageName)
	inputs := make([]AssignmentInput, 0, len(providers))
	for _, provider := range providers {
		admitted := c.outputs.find(provider.Package)
		if admitted == nil {
			// A provider that declares outputs and admitted none produced
			// nothing this consumer can be given; whether that is allowed is
			// the task graph's decision, not this one's.
			c.Log.Trace().Str("run", c.Run).Str("task", packageName).
				Str("package", provider.Package).Msg("the provider admitted no outputs")
			continue
		}
		if err := c.carryAdmittedOutput(ctx, admitted); err != nil {
			return nil, err
		}
		inputs = append(inputs, AssignmentInput{
			Task: admitted.manifest.task, Package: provider.Package,
			Commit: admitted.commit, Digest: admitted.manifest.digest, Path: provider.Path,
		})
	}
	return inputs, nil
}

// carryAdmittedOutput makes one verified result reachable from the Assignment
// commit. Its direct secondary-parent proof replaces a runtime relay branch.
func (c *Coordinator) carryAdmittedOutput(ctx context.Context, admitted *admittedOutputs) error {
	if admitted.store == nil {
		return fmt.Errorf("execution: admitted output %s has no object store", admitted.manifest.task)
	}
	return c.dispatch.Store.ImportCommitFrom(ctx, admitted.store.Dir, admitted.commit)
}
