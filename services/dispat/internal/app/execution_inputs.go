// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// What a release checks in the moment between a package's beforePublish hook
// and its publish command (CCME §27.2, §28.6).
//
// The check exists because that moment is the last one in which a publication
// can still be withheld for free, and because everything it rests on was
// established earlier. The lock was taken before the plan was fixed; the
// artefact was built from a working tree that has since been written to by
// every version stage of the run and by every record of the packages that
// published before this one. So three questions are asked again here, in the
// order of what they cost and of what they mean.
//
// Does this run still own the repository it is about to publish into? A run
// that lost its lock may start no new effect, whatever it was allowed to do a
// minute ago, and that holds for every release that took a lock, local or
// distributed. Is the fleet still the fleet the plan was computed over, which
// is the check a composed workspace has always made. And, for a run that
// delegates work, has anything this package's artefact was built from changed
// since the build consumed it?
//
// The last one is the one worth stating precisely, because it has to be
// narrow. A release moves the repository constantly and almost none of it is
// relevant: an earlier package's changelog, an unrelated folder's version
// edit, a tag. What withholds a publication is a change under this package's
// own folder or under the folders of the packages its artefact was built
// against, and the changelog files of exactly those packages are excluded
// because writing them is what a release does on its way past.
//
// With no worker links the relevant-input check is not composed, and a run
// that also holds no lock and composes no fleet gets no callback at all.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// codeLockLost is the code dispat already reports a lost or unusable release
// lock under. It is the execution profile's own spelling of it, so that the
// code a publication is withheld with and the code a run refuses a new
// assignment with cannot drift apart.
const codeLockLost = execution.CodeLockLost

// defaultChangelogFile is the changelog a package writes when it enables the
// record without naming a file, which is the name the recorder itself falls
// back to.
const defaultChangelogFile = "CHANGELOG.md"

// resolvePrePublishCheck is the callback the executor runs after a package's
// beforePublish hook and immediately before its publish command.
//
// Every run that holds a lock asks first whether it still does, through the
// run's one ownership gate, local or distributed: the lock was taken before
// the plan was fixed, and a publication is a new effect started on the
// strength of that check. A composed workspace then makes the revalidation it
// has always made, and a run with worker links adds the relevant-input check a
// distributed publication needs. The checks run on both paths of a distributed
// run: a publication this run kept and one it delegated are authorized by the
// same sentence, so the two cannot come to different answers about the same
// release. A run that holds no lock, composes nothing and delegates nothing
// gets no callback at all, which is what it always got.
func (a *App) resolvePrePublishCheck(pl *plan.Plan, fleet *workspaceRecorder,
	coordinator *execution.Coordinator) func(context.Context, *plan.Release) error {
	verifyFleet := resolveFleetPublishCheck(fleet)
	var inputs *relevantInputs
	if coordinator != nil {
		inputs = a.newRelevantInputs(pl, fleet, coordinator)
	}
	if a.ownership == nil && verifyFleet == nil && inputs == nil {
		return nil
	}
	return func(ctx context.Context, rel *plan.Release) error {
		if err := a.checkLockOwnership(ctx, rel); err != nil {
			return err
		}
		if verifyFleet != nil {
			if err := verifyFleet(ctx, rel); err != nil {
				return err
			}
		}
		if inputs == nil {
			return nil
		}
		return inputs.checkRelevantInputs(ctx, rel)
	}
}

// resolveFleetPublishCheck is the revalidation a composed workspace has always
// made here: the publish branch is where the plan expects it, and the fleet
// snapshot is still the one the plan was computed over.
func resolveFleetPublishCheck(fleet *workspaceRecorder) func(context.Context, *plan.Release) error {
	if fleet == nil {
		return nil
	}
	return func(ctx context.Context, rel *plan.Release) error {
		if err := fleet.verifyPublishBranch(ctx, rel); err != nil {
			return err
		}
		return fleet.verifySnapshot(ctx, rel)
	}
}

// checkLockOwnership refuses publication if any repository in this run's
// complete lock set is no longer owned.
//
// It asks the run's own gate, which a distributed run's coordinator borrows
// for its assignments and authorizations too. A loss here therefore halts
// every other attempt in flight, including work in another repository of the
// same fleet. A run that holds no lock has no gate and is not asked. A caller
// that was interrupted gets its context's error rather than a refusal: an
// interrupted lookup is not a lost lock.
func (a *App) checkLockOwnership(ctx context.Context, rel *plan.Release) error {
	if a.ownership == nil {
		return nil
	}
	if err := a.ownership.Check(ctx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return a.refusePublication(rel, err)
	}
	a.log.Trace().Str("package", rel.Pkg.Name).Msg("the release lock is still held")
	return nil
}

// refusePublication writes the refusal where it is decided and hands it back
// with the code and the §28.9 class on it.
func (a *App) refusePublication(rel *plan.Release, err error) error {
	refusal := execution.NewIdentifiedDiagnostic(
		execution.Identity{Run: a.runID, Task: rel.Pkg.Name + ":publish"},
		codeLockLost, execution.CategoryNativeRecordingOrLock, "%w", err)
	event := a.logError(refusal).Str("package", rel.Pkg.Name)
	if errors.Is(err, release.ErrLockLost) {
		// The lock on the remote is another run's now, or nobody's: the one
		// remedy that must not be given is the one for a stranded lock.
		event = event.Str("remedy", release.LockLostRemedy)
	}
	event.Msg("publication not authorized")
	return refusal
}

// relevantInputs answers, for one package, whether anything its artefact was
// built from has changed since the build consumed it.
//
// It is a small type rather than a closure over six values because the
// question has two halves that are computed at different times: which packages
// are relevant to one package is a property of the plan and is the same every
// time it is asked, while what the repository holds is a property of this
// moment. The first half is memoised, the second never is.
type relevantInputs struct {
	app         *App
	plan        *plan.Plan
	fleet       *workspaceRecorder
	coordinator *execution.Coordinator
}

// newRelevantInputs opens the check over one run's plan.
func (a *App) newRelevantInputs(pl *plan.Plan, fleet *workspaceRecorder,
	coordinator *execution.Coordinator) *relevantInputs {
	return &relevantInputs{app: a, plan: pl, fleet: fleet, coordinator: coordinator}
}

// checkRelevantInputs withholds a publication whose relevant inputs moved
// after the build that produced the artefact.
//
// The cheap answer comes first and is the ordinary one: a repository still at
// the head the plan was computed against has had nothing committed to it at
// all, so there is nothing to compare. Only a repository that has moved is
// compared, and then only over the folders that matter.
func (r *relevantInputs) checkRelevantInputs(ctx context.Context, rel *plan.Release) error {
	git, root := r.resolveOwner(rel)
	if git == nil {
		return nil
	}
	head, err := git.HeadSHA(ctx)
	if err != nil {
		return r.refuse(rel, fmt.Errorf("reading the head of %s: %w", rel.Pkg.Repository, err))
	}
	planned := r.app.plannedHeads[rel.Pkg.Repository]
	if head == planned {
		r.app.log.Trace().Str("package", rel.Pkg.Name).
			Msg("the repository is still at the planned head, so no input can have changed")
		return nil
	}
	changed, err := r.resolveChangedInputs(ctx, git, root, rel, head)
	if err != nil {
		return r.refuse(rel, err)
	}
	if len(changed) == 0 {
		r.app.log.Debug().Str("package", rel.Pkg.Name).Str("head", head).
			Msg("the repository moved, and nothing this package was built from moved with it")
		return nil
	}
	return r.refuse(rel, fmt.Errorf(
		"%d file(s) this package's build read changed after it was built (%s): the artefact does not describe the repository any more, so the publication is withheld and a new plan is required",
		len(changed), strings.Join(changed, ", ")))
}

// resolveChangedInputs is the comparison itself: what differs, between the
// state the build consumed and the repository as it is now, under the folders
// of the packages this one was built against.
//
// The baseline is the exact state the build was dispatched from where there is
// one. A build the run placed on this machine read the working tree itself and
// left no such object, and a package with no build task consumed nothing, so
// both fall back to the head the plan was computed against: comparing against
// the plan is stricter than comparing against the build and can only withhold
// a publication that the narrower question would also have looked at.
func (r *relevantInputs) resolveChangedInputs(ctx context.Context, git *gitx.LocalGitx,
	root string, rel *plan.Release, head string) ([]string, error) {
	baseline := r.coordinator.PreparedSnapshot(rel.Pkg.Name)
	if baseline == "" {
		baseline = r.app.plannedHeads[rel.Pkg.Repository]
	}
	if baseline == "" {
		// A repository whose history is unborn has nothing to compare and
		// nothing that could have changed under it.
		return nil, nil
	}
	pathspecs := r.formatRelevantPathspecs(root, rel)
	if len(pathspecs) == 0 {
		return nil, nil
	}
	changed, err := git.ChangedPathsBetween(ctx, baseline, head, pathspecs)
	if err != nil {
		return nil, err
	}
	return changed, nil
}

// formatRelevantPathspecs is what the comparison looks at: the folders of this
// package and of every package its build could have read, minus the changelog
// file each of them writes.
//
// The exclusions are the point of the whole check being narrow. A release
// writes a changelog into the folder of every package it publishes, so a
// second package of the same repository would find its provider's folder
// changed by the act of releasing it, and every run of more than one package
// would withhold its own publications.
func (r *relevantInputs) formatRelevantPathspecs(root string, rel *plan.Release) []string {
	var folders, exclusions []string
	for _, name := range r.resolveRelevantPackages(rel) {
		relevant := r.plan.Releases[name]
		if relevant == nil || relevant.Pkg.Repository != rel.Pkg.Repository {
			// A provider in another repository is guarded by that
			// repository's own lock and its own planned head, and a diff of
			// this repository could say nothing about it.
			continue
		}
		folder := relativeRepositoryPath(root, relevant.Pkg.Dir)
		folders = append(folders, folder)
		if !relevant.Pkg.Changelog.Enabled {
			continue
		}
		exclusions = append(exclusions, ":(exclude)"+path.Join(folder, resolveChangelogFile(relevant)))
	}
	if len(folders) == 0 {
		return nil
	}
	sort.Strings(folders)
	sort.Strings(exclusions)
	return append(folders, exclusions...)
}

// resolveChangelogFile is the file one package's changelog record writes, in
// the spelling the recorder itself resolves it to.
func resolveChangelogFile(rel *plan.Release) string {
	if rel.Pkg.Changelog.File == "" {
		return defaultChangelogFile
	}
	return filepath.ToSlash(rel.Pkg.Changelog.File)
}

// resolveRelevantPackages is this package and every package its build could
// have read: its transitive providers, and the members of its version group.
//
// The version group is in because a group releases as one version: a change
// inside a sibling's folder is a change to what this package's own manifest
// says about the release, whether or not the graph makes the sibling a
// provider.
func (r *relevantInputs) resolveRelevantPackages(rel *plan.Release) []string {
	relevant := map[string]bool{rel.Pkg.Name: true}
	collectProviderClosure(r.plan, rel.Pkg.Name, relevant)
	group := rel.Pkg.VersionGroupIdentity()
	for name, other := range r.plan.Releases {
		if group != "" && other.Pkg.VersionGroupIdentity() == group {
			relevant[name] = true
		}
	}
	names := make([]string, 0, len(relevant))
	for name := range relevant {
		names = append(names, name)
	}
	return names
}

// resolveOwner opens the repository one package publishes into, and the folder
// its paths are relative to.
func (r *relevantInputs) resolveOwner(rel *plan.Release) (*gitx.LocalGitx, string) {
	if r.fleet == nil {
		return r.app.git, r.app.root
	}
	owner := r.fleet.byName[rel.Pkg.Repository]
	if owner == nil {
		return nil, ""
	}
	return owner.git, owner.repo.Root
}

// refuse is the one diagnostic a withheld publication produces: the integrity
// code, the §28.9 class, and the package it is about.
func (r *relevantInputs) refuse(rel *plan.Release, err error) error {
	refusal := execution.NewIdentifiedDiagnostic(
		execution.Identity{Run: r.app.runID, Task: rel.Pkg.Name + ":publish"},
		execution.CodeIntegrity, execution.CategoryIntegrity, "%w", err)
	r.app.logError(refusal).Str("package", rel.Pkg.Name).Msg("publication withheld")
	return refusal
}
