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
// stage, its consumers are blocked by the ordinary rules, and nothing is
// substituted: no registry contents, no second build under a different input.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// admittedOutputs is one package's admitted output set as every consumer of it
// has to be told about it: which node produced it, where that node's answer
// can be fetched from, the exact object it is read at, and the manifest that
// was admitted.
//
// The endpoint is carried rather than looked up again because it is what
// decides whether a consumer can reach the bytes at all: two nodes sharing one
// mailbox need no copy, and two nodes with mailboxes of their own need the
// object relayed onto the consumer's.
type admittedOutputs struct {
	node     string
	endpoint string
	branch   string
	commit   string
	manifest *OutputManifest
}

// outputRegistry is the run's record of what has been admitted and of what has
// already been relayed where.
//
// One owner: the coordinator. Builds run concurrently, so the map is written
// by whichever dispatch finished and read by whichever dispatch is starting,
// and the mutex is what makes "the outputs of this package" one answer.
type outputRegistry struct {
	mu       sync.Mutex
	admitted map[string]*admittedOutputs
	relayed  map[string]string
}

// newOutputRegistry opens an empty record.
func newOutputRegistry() *outputRegistry {
	return &outputRegistry{admitted: map[string]*admittedOutputs{}, relayed: map[string]string{}}
}

// remember records one package's admitted set, replacing nothing: a package is
// built once per run, and a second admission would be a second answer to a
// question that has one.
func (r *outputRegistry) remember(packageName string, admitted *admittedOutputs) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.admitted[packageName] = admitted
}

// find answers what was admitted for one package, and nil for a package that
// produced nothing this run.
func (r *outputRegistry) find(packageName string) *admittedOutputs {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.admitted[packageName]
}

// reuseRelay answers the branch one object was already copied onto an endpoint
// under, so that three consumers on one mailbox cost one relay push.
func (r *outputRegistry) reuseRelay(endpoint, commit string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	branch, isRelayed := r.relayed[endpoint+"\x00"+commit]
	return branch, isRelayed
}

// rememberRelay records one copy.
func (r *outputRegistry) rememberRelay(endpoint, commit, branch string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.relayed[endpoint+"\x00"+commit] = branch
}

// admitOutputs holds one node's reported outputs to the plan's own declaration
// and installs what survives into this machine's checkout.
//
// A package that declares no outputs admits none, whatever a node reported:
// the declaration is the configuration's, so a result carrying a set nobody
// asked for describes files no consumer was ever going to be told about.
func (c *Coordinator) admitOutputs(ctx context.Context, task, branch, commit string,
	result Result, request release.StageRequest) error {
	roots := request.Release.Pkg.Space.BuildOutputs
	if len(roots) == 0 {
		return nil
	}
	if result.Outputs == nil {
		return c.refuseOutputSet(task, result.Node, ReasonBytesMissing,
			fmt.Errorf("the node reported no build outputs for a package that declares %d", len(roots)))
	}
	totals, err := ValidateOutputs(ctx, c.dispatch.Store, result.Outputs, OutputExpectation{
		Run: c.Run, PlanDigest: c.PlanDigest, Generation: c.Generation, Task: task, Attempt: 1,
		Roots: roots, Platforms: request.Release.Pkg.Space.BuildPlatforms, Limits: c.Limits,
	})
	if err != nil {
		return c.refuseOutputSet(task, result.Node, OutputFaultReason(err), err)
	}
	took := time.Now()
	if err := c.installLocally(ctx, request, result.Outputs); err != nil {
		return c.refuseOutputSet(task, result.Node, OutputFaultReason(err), err)
	}
	c.outputs.remember(request.Release.Pkg.Name, &admittedOutputs{
		node: result.Node, endpoint: c.endpointOf(result.Node), branch: branch,
		commit: commit, manifest: result.Outputs,
	})
	c.Log.Debug().Str("run", c.Run).Str("task", task).Str("worker", result.Node).
		Int("files", totals.Files).Int64("bytes", totals.Bytes).
		Dur("took", time.Since(took)).Msg("outputs verified")
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("worker", result.Node).
		Str("package", request.Release.Pkg.Name).Int("files", totals.Files).
		Int64("bytes", totals.Bytes).Msg("outputs admitted")
	return nil
}

// refuseOutputSet is the one refusal a rejected output set produces: the rule
// it broke and the work it was about, and never anything the set said.
func (c *Coordinator) refuseOutputSet(task, node string, reason OutputReason, err error) error {
	c.Log.Warn().Str("run", c.Run).Str("task", task).Str("worker", node).
		Str("reason", string(reason)).Str("code", CodeIntegrity).
		Str("category", CategoryIntegrity).Msg("outputs rejected")
	return NewIdentifiedDiagnostic(Identity{Run: c.Run, Worker: node, Task: task, Attempt: 1},
		CodeIntegrity, CategoryIntegrity,
		"the build outputs %s reported cannot be used (%s): %w", task, reason, err)
}

// installLocally puts an admitted set into this machine's own checkout of the
// package that produced it.
//
// The staging folder is inside the owning repository's git directory, which is
// two things at once: it is never a tracked path, so nothing a release stages
// or reverts can see it, and it is on the same file system as the destination,
// which is what makes the rename that installs a root a rename rather than a
// copy. The shared side of the snapshot guard is held while it happens, for
// the same reason a version frame holds it: a working tree being written is a
// working tree nobody may be hashing at that moment.
func (c *Coordinator) installLocally(ctx context.Context, request release.StageRequest,
	manifest *OutputManifest) error {
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
	return InstallOutputs(ctx, InstallRequest{
		Git: c.dispatch.Store, Manifest: manifest, Dir: request.Dir,
		Staging: filepath.Join(filepath.Dir(index),
			"dispat-outputs-"+formatPathWord(c.Run)+"-"+formatPathWord(manifest.Package)),
		Log: c.Log,
	})
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
		branch, err := c.reachOutputs(ctx, node, admitted)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, AssignmentInput{
			Task: admitted.manifest.Task, Package: provider.Package, Branch: branch,
			Commit: admitted.commit, Digest: admitted.manifest.Digest, Path: provider.Path,
		})
	}
	return inputs, nil
}

// reachOutputs answers the branch of one node's own endpoint that an admitted
// set is fetchable from, copying the object there when it is somewhere else.
//
// A node talks to one mailbox and to nothing else, so a consumer whose
// endpoint is not the producer's cannot be told about the producer's branch.
// The copy is create-only and immutable, out of the store the objects are
// already in after admission, and it is made once per endpoint however many
// consumers of that provider are placed there.
func (c *Coordinator) reachOutputs(ctx context.Context, node string, admitted *admittedOutputs) (string, error) {
	endpoint := c.endpointOf(node)
	if endpoint == admitted.endpoint {
		return admitted.branch, nil
	}
	if branch, isRelayed := c.outputs.reuseRelay(endpoint, admitted.commit); isRelayed {
		return branch, nil
	}
	branch := FormatBranch(node, KindRelay, time.Now())
	if err := c.dispatch.Store.PushCreate(ctx, endpoint, admitted.commit, branch); err != nil {
		return "", fmt.Errorf("execution: relaying the outputs of %s to %s: %w",
			admitted.manifest.Package, node, err)
	}
	c.recordOwnedRef(node, branch, admitted.commit)
	c.outputs.rememberRelay(endpoint, admitted.commit, branch)
	c.Log.Debug().Str("run", c.Run).Str("worker", node).Str("branch", branch).
		Str("commit", admitted.commit).Str("package", admitted.manifest.Package).
		Msg("relay pushed")
	return branch, nil
}
