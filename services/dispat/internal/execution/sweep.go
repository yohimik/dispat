// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The orchestrator's side of a command sweep executed on worker nodes (CCME
// §28.10).
//
// A sweep is `dispat run`: one declared script, run in every package the
// selection covers, providers first. It is not a release. It records nothing,
// publishes nothing and holds no release lock, so its tasks are the simplest
// work this profile places: one package's commands for the swept script,
// with no hook around them, no provider output installed before them, and a
// placement decided by the package's own `runOnly` exactly as its build's is.
//
// What a sweep does carry back is what its script declared it writes. Those
// roots are relative to the repository rather than to the package, because
// the folder is shared: ten packages' `tests` tasks write ten profiles into
// one `coverage/`. So the orchestrator merges what comes back instead of
// replacing a folder, and it merges only once every task has answered: two
// tasks writing one path with different bytes is a conflict the root must not
// settle by arrival order, and the only way the root can hold neither file is
// that neither was installed before the conflict could be seen.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	lib "github.com/yohimik/dispat/pkg/config"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// SweepTask is one package's task of a command sweep, as the run hands it to
// its coordinator.
type SweepTask struct {
	// Request is the task as a node receives it: the release it belongs to,
	// the swept script's commands as the frame's commands and nothing before
	// or after them, the computed environment, the unresolved static pairs
	// and the package folder.
	Request release.StageRequest
	// Script is the declared script the sweep runs, which the task's commands
	// read as DISPAT_STAGE `run:<script>`.
	Script string
	// Roots are the folders the script's tasks write, relative to the root of
	// the package's repository, as the entry configuration declared them.
	Roots []string
	// Here is the task as this process runs it, in this checkout, answering
	// what its commands exported.
	Here func(ctx context.Context) ([]plan.Output, error)
}

// Sweep places one package's sweep task and runs it where it was placed.
//
// The placement is the build stage's, asked of the package's own `runOnly`
// and `buildPlatforms`: a sweep is the work a pool exists for, so `both` is
// offered to the workers first and to this machine when none has room, and a
// package that may only run here or only elsewhere is held to that.
func (c *Coordinator) Sweep(ctx context.Context, sweep SweepTask) (release.StageOutcome, error) {
	task := formatSweepTaskName(sweep.Request.Release.Pkg.Name)
	space := sweep.Request.Release.Pkg.Space
	placement := ResolveStagePlacement(StageBuild, space.RunOnly.ResolveBuild(), len(space.LoginScript) > 0)
	return c.placeTask(ctx, task, placement, space.BuildPlatforms, "",
		func(ctx context.Context, lease *Lease, attempt int) (release.StageOutcome, error) {
			if lease.IsLocal {
				return c.sweepHere(ctx, lease, task, sweep)
			}
			return c.dispatchSweep(ctx, lease, task, attempt, sweep)
		})
}

// formatSweepTaskName names one package's sweep task: the package under the
// sweep's own kind, so that a log, a branch name and the summary all say what
// kind of work they are looking at.
func formatSweepTaskName(packageName string) string { return packageName + ":" + KindRun }

// sweepHere runs one sweep task in this checkout, as a sweep with no workers
// runs every one of them.
//
// Nothing is captured. The task writes into the checkout the run was started
// in, which is where every root it declares is going to be merged anyway, so
// a capture would be a description of files that are already where they
// belong.
func (c *Coordinator) sweepHere(ctx context.Context, lease *Lease, task string,
	sweep SweepTask) (release.StageOutcome, error) {
	defer lease.Release()
	c.Log.Info().Str("run", c.Run).Str("task", task).
		Msg("task placed on the node that started the run")
	exports, err := sweep.Here(ctx)
	if err != nil {
		return release.StageOutcome{Exports: exports, LocalFailure: "run script failed"}, err
	}
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("status", StatusSucceeded).
		Int("exports", len(exports)).Msg("task finished")
	return release.StageOutcome{Exports: exports}, nil
}

// dispatchSweep offers one sweep task to the node the pool chose and waits
// for that node to report.
//
// It is a build's dispatch with its inputs left out: the package's input
// closure is captured and offered exactly as a build's is, and nothing any
// other task produced is installed before the commands, because a sweep
// installs nothing (§28.10).
func (c *Coordinator) dispatchSweep(ctx context.Context, lease *Lease, task string, attempt int,
	sweep SweepTask) (release.StageOutcome, error) {
	request := sweep.Request
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
	offer, err := c.offerAssignment(ctx, lease, task,
		c.formatSweepAssignment(lease.Node, task, attempt, dir, repositories, sweep))
	if err != nil {
		return outcome, err
	}
	defer offer.observer.forget(offer.branch)
	return c.awaitResult(ctx, lease, task, attempt, KindRun, outcome, offer,
		func(ctx context.Context, outcome release.StageOutcome, reply taskReply, branch string) (release.StageOutcome, error) {
			return c.readSweepOutcome(ctx, task, attempt, outcome, reply, sweep)
		})
}

// formatSweepAssignment is the document one sweep task travels as: a build's
// assignment with the swept script's commands as its whole frame, the script
// named, no inputs, and the sweep's own roots as the outputs it captures.
func (c *Coordinator) formatSweepAssignment(node, task string, attempt int, dir string,
	repositories []AssignmentRepository, sweep SweepTask) *Assignment {
	request := sweep.Request
	branch := FormatBranch(node, KindRun, time.Now())
	return &Assignment{
		Header:       c.formatOrchestratorHeader(KindRun, task, attempt, node, branch),
		Repositories: repositories,
		Package: &AssignmentPackage{
			Name:       request.Release.Pkg.Name,
			Version:    request.Release.Next.String(),
			Repository: request.Release.Pkg.Repository,
			Dir:        dir,
		},
		Frame:           &AssignmentFrame{Commands: request.Frame.Commands},
		Script:          sweep.Script,
		Env:             request.Env,
		StaticEnv:       request.StaticEnv,
		Exports:         formatExports(request.Release.Outputs),
		Shell:           c.dispatch.Shell(request.Dir),
		Platforms:       request.Release.Pkg.Space.BuildPlatforms,
		Outputs:         cleanSweepRoots(sweep.Roots),
		DeadlineSeconds: resolveTaskDeadlineSeconds(c.Timeouts.Task),
		Limits:          c.Limits,
	}
}

// cleanSweepRoots is the declared roots in the one spelling a manifest is
// compared against: slash-separated, with no trailing slash.
func cleanSweepRoots(roots []string) []string {
	cleaned := make([]string, 0, len(roots))
	for _, root := range roots {
		cleaned = append(cleaned, path.Clean(strings.TrimSuffix(filepath.ToSlash(root), "/")))
	}
	return cleaned
}

// readSweepOutcome turns one accepted sweep result into what the task comes
// to: the exports it produced, the failure it reports, and the outputs it
// captured, which are admitted here and merged only when the sweep ends.
func (c *Coordinator) readSweepOutcome(ctx context.Context, task string, attempt int,
	outcome release.StageOutcome, reply taskReply, sweep SweepTask) (release.StageOutcome, error) {
	result := reply.result
	outcome, err := c.readReportedOutcome(task, attempt, outcome, result)
	if err != nil {
		return outcome, err
	}
	if err := c.admitSweepOutputs(ctx, task, attempt, result, sweep); err != nil {
		outcome.FailedPart = release.PartOutputs
		return outcome, err
	}
	c.reportTaskFinished(task, result, outcome)
	return outcome, nil
}

// admitSweepOutputs holds one task's reported set to the sweep's own roots
// and to the one validator every output set goes through, then records it
// beside the sets the sweep has already admitted.
//
// A script that declares no roots admits nothing, whatever was reported: the
// declaration is the entry configuration's, so a set nobody asked for
// describes files nobody is going to merge.
func (c *Coordinator) admitSweepOutputs(ctx context.Context, task string, attempt int, result Result,
	sweep SweepTask) error {
	roots := cleanSweepRoots(sweep.Roots)
	if len(roots) == 0 {
		return nil
	}
	if result.Outputs == nil {
		return c.refuseSweepOutputs(task, result.Node, ReasonBytesMissing,
			fmt.Errorf("no outputs were reported for a script that declares %d roots", len(roots)))
	}
	totals, err := ValidateOutputs(ctx, c.dispatch.Store, result.Outputs, OutputExpectation{
		Run: c.Run, PlanDigest: c.PlanDigest, Generation: c.Generation, Task: task, Attempt: attempt,
		Roots: roots, Limits: c.Limits,
	})
	if err != nil {
		return c.refuseSweepOutputs(task, result.Node, OutputFaultReason(err), err)
	}
	owner, err := c.resolveOwnerRepository(sweep.Request)
	if err != nil {
		return c.refuseSweepOutputs(task, result.Node, "", err)
	}
	conflict, isConflicting := c.sweepOutputs.admit(&sweepSet{
		task: task, packageName: sweep.Request.Release.Pkg.Name, node: result.Node,
		repository: owner, manifest: result.Outputs,
	})
	if isConflicting {
		return c.refuseSweepOutputs(task, result.Node, ReasonPathConflict, fmt.Errorf(
			"%s and %s both wrote %s with different bytes: neither file is merged, and neither task's outputs are",
			conflict.first, task, conflict.path))
	}
	c.Log.Info().Str("run", c.Run).Str("task", task).Str("worker", result.Node).
		Str("package", sweep.Request.Release.Pkg.Name).Int("files", totals.Files).
		Int64("bytes", totals.Bytes).Msg("outputs admitted")
	return nil
}

// refuseSweepOutputs is the one refusal a rejected sweep set produces, on the
// line every refused output set is reported on: the rule it broke and the
// work it was about.
func (c *Coordinator) refuseSweepOutputs(task, node string, reason OutputReason, err error) error {
	c.sweepOutputs.reject(task)
	event := c.Log.Warn().Str("run", c.Run).Str("task", task)
	if node != "" {
		event = event.Str("worker", node)
	}
	event.Str("reason", string(reason)).Str("code", CodeIntegrity).
		Str("category", CategoryIntegrity).Msg("outputs rejected")
	return NewIdentifiedDiagnostic(Identity{Run: c.Run, Worker: node, Task: task},
		CodeIntegrity, CategoryIntegrity, "the outputs %s reported cannot be merged (%s): %w", task, reason, err)
}

// MergeSweepOutputs merges every admitted set of this sweep into the
// checkout, once every task has answered, and answers what could not be
// merged.
//
// A set that shares a conflicting path with another is left out whole, and so
// is the other: each task's set is installed all or nothing, and the root has
// to hold neither task's file at the path they disagree about. Every other set
// is merged, in task order so that two runs of one sweep write in one order,
// and a set that fails to merge fails its own task and no other.
func (c *Coordinator) MergeSweepOutputs(ctx context.Context) error {
	var failures []error
	for _, set := range c.sweepOutputs.collectMergeable() {
		if err := c.mergeSweepSet(ctx, set); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// mergeSweepSet merges one admitted set into the repository it belongs to.
//
// Staging follows the same private choice as build outputs, including the
// second assembly beside the checkout when a linked worktree's Git directory
// cannot rename into it.
func (c *Coordinator) mergeSweepSet(ctx context.Context, set *sweepSet) error {
	index, err := set.repository.IndexPath(ctx)
	if err != nil {
		return c.refuseSweepOutputs(set.task, set.node, "",
			fmt.Errorf("execution: locating the private folder of %s: %w", set.repository.Dir, err))
	}
	c.guard.RLock()
	err = installStaged(ctx, outputStagingSpec{
		indexPath: index, ownerDir: set.repository.Dir, run: c.Run, packageName: set.task,
	}, InstallRequest{
		Git: c.dispatch.Store, Manifest: set.manifest, Dir: set.repository.Dir, Log: c.Log,
	}, MergeInstallOutputs)
	c.guard.RUnlock()
	if err != nil {
		return c.refuseSweepOutputs(set.task, set.node, OutputFaultReason(err), err)
	}
	c.Log.Info().Str("run", c.Run).Str("task", set.task).Str("worker", set.node).
		Str("package", set.packageName).Int("files", set.manifest.Files).
		Int64("bytes", set.manifest.Bytes).Msg("outputs merged")
	return nil
}

// sweepSet is one task's admitted set, as the merge at the end of the sweep
// reads it.
type sweepSet struct {
	task        string
	packageName string
	node        string
	// repository is the checkout the set merges into: the repository that owns
	// the package, whose root the sweep's roots are relative to.
	repository *gitx.LocalGitx
	manifest   *OutputManifest
}

// sweepConflict is the first disagreement one set had with the sets before
// it: the path, and the task that wrote it first.
type sweepConflict struct {
	path  string
	first string
}

// sweepPathClaim is the first set that named one path, and what it said the
// path holds.
type sweepPathClaim struct {
	task    string
	path    string
	content string
}

// sweepOutputs is the sweep's record of what its tasks reported: the sets it
// admitted, the paths they claimed, the paths two of them disagree about, and
// what became of each task's set.
//
// One owner: the coordinator. Tasks finish concurrently, so admission is one
// decision under one mutex, which is what makes "has another task already
// written this path" one answer.
type sweepOutputs struct {
	mu         sync.Mutex
	sets       []*sweepSet
	claims     map[string]sweepPathClaim
	conflicted map[string]bool
	outcomes   map[string]string
}

// newSweepOutputs opens an empty record.
func newSweepOutputs() *sweepOutputs {
	return &sweepOutputs{claims: map[string]sweepPathClaim{}, conflicted: map[string]bool{},
		outcomes: map[string]string{}}
}

// admit records one set and answers the first path it disagrees about with a
// set admitted before it.
//
// Two tasks naming one path with the same content agree, and the path is
// merged once. Two naming it with different content, or two spellings of one
// path that a case-insensitive checkout would merge into one file, disagree,
// and every set naming that path is then left out of the merge. The set is
// recorded either way, so that one admitted later still finds the claim.
func (r *sweepOutputs) admit(set *sweepSet) (sweepConflict, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sets = append(r.sets, set)
	r.outcomes[set.task] = OutputsAdmitted
	var conflict sweepConflict
	isConflicting := false
	for _, entry := range set.manifest.Entries {
		key := formatSweepPathKey(set.repository.Dir, entry.Path)
		content := formatEntryContent(entry)
		claim, isClaimed := r.claims[key]
		if !isClaimed {
			r.claims[key] = sweepPathClaim{task: set.task, path: entry.Path, content: content}
			continue
		}
		if claim.path == entry.Path && claim.content == content {
			continue
		}
		r.conflicted[key] = true
		if !isConflicting {
			conflict, isConflicting = sweepConflict{path: entry.Path, first: claim.task}, true
		}
	}
	return conflict, isConflicting
}

// formatSweepPathKey is what two paths are compared as: the repository they
// merge into and the path inside it, both folded, because a checkout on a
// case-insensitive file system holds one file for two spellings.
func formatSweepPathKey(repository, carried string) string {
	return lib.Fold(repository) + "\x00" + lib.Fold(carried)
}

// formatEntryContent is everything one entry says it holds: what kind of
// entry it is, its mode, and the digest of its content or link target.
func formatEntryContent(entry ManifestEntry) string {
	return entry.Type + "\x00" + entry.Mode + "\x00" + entry.SHA256 + "\x00" + entry.Target
}

// reject records a task whose set was refused.
func (r *sweepOutputs) reject(task string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes[task] = OutputsRejected
}

// collectMergeable is every admitted set that names no path two sets disagree
// about, in task order, with every set that does recorded as rejected.
func (r *sweepOutputs) collectMergeable() []*sweepSet {
	r.mu.Lock()
	defer r.mu.Unlock()
	mergeable := make([]*sweepSet, 0, len(r.sets))
	for _, set := range r.sets {
		if r.isNamingConflictedPath(set) {
			r.outcomes[set.task] = OutputsRejected
			continue
		}
		mergeable = append(mergeable, set)
	}
	sort.SliceStable(mergeable, func(i, j int) bool { return mergeable[i].task < mergeable[j].task })
	return mergeable
}

// isNamingConflictedPath reports whether one set names a path two sets of the
// sweep disagree about. It is asked with the mutex held.
func (r *sweepOutputs) isNamingConflictedPath(set *sweepSet) bool {
	for _, entry := range set.manifest.Entries {
		if r.conflicted[formatSweepPathKey(set.repository.Dir, entry.Path)] {
			return true
		}
	}
	return false
}

// resolveOutcome is what became of one task's set, in the summary's words,
// and the size of the set when there was one.
func (r *sweepOutputs) resolveOutcome(task string) (string, *OutputManifest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	outcome, isKnown := r.outcomes[task]
	if !isKnown {
		return OutputsNone, nil
	}
	for _, set := range r.sets {
		if set.task == task {
			return outcome, set.manifest
		}
	}
	return outcome, nil
}
