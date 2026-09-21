// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// Planning from the store's release records, as read under the lock
// (CCME SPEC.md §13.2 "Records under the lock").
//
// The lock comes before the plan, but a plan is built from the tags the
// checkout happens to hold, and nothing about holding the lock makes those the
// tags the remote holds. A clone made before another run recorded, or made
// without tags at all, therefore plans a version that is already published,
// publishes it a second time and pushes its tag over the record of the first.
// The lock serialises such runs and isolates nothing.
//
// What closes it is one comparison, here, after the locks are held and before
// the plan is computed: every release record the store holds on a commit this
// checkout's head reaches has to be a record this checkout holds too, at the
// same commit. The lock is what makes one comparison enough, since no other
// coordinated run can add a record between it and this run's own records.
//
// Nothing is repaired and nothing is fetched. A run that fetches does so
// before it starts; from here a difference is a refusal, because both ways of
// "fixing" it silently (planning the package as unreleased, or refreshing the
// records after reading them) publish a version twice.

import (
	"context"
	"fmt"
	"sort"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// releaseStore is one participating repository as the comparison reads it: the
// checkout that plans, and the remote its records are written to.
//
// A composed workspace has one per repository, because each of them records to
// a store of its own and a stale clone of any one of them plans the same wrong
// version. Repository is empty for a single repository, which names itself in
// no message because there is nothing to tell it apart from.
type releaseStore struct {
	repository string
	git        *gitx.LocalGitx
	remote     string
	commit     *config.CommitConfig
	packages   []*model.Package
	log        zerolog.Logger
}

// compareReleaseRecords refuses a run whose planning input disagrees with the
// records of the store it would write to, for the single repository and for
// every participating repository of a composed workspace.
//
// It runs under the locks and before the plan, so a refusal costs one remote
// read and leaves the repository exactly as it found it: no hook has run, no
// package has built, nothing has published, and the deferred cleanup gives the
// locks back on the way out.
func (a *App) compareReleaseRecords(ctx context.Context, fleet *workspaceRecorder) error {
	packages, err := a.packages()
	if err != nil {
		return err
	}
	aliases := plan.NewAliasFilter(packages)
	for _, store := range a.releaseStores(fleet, packages) {
		if err := store.compare(ctx, aliases); err != nil {
			return err
		}
	}
	return nil
}

// releaseStores lists what this run records to, with each store's packages
// attached: the packages decide which tag names are records at all, and a
// repository owning none has no records to compare.
func (a *App) releaseStores(fleet *workspaceRecorder, packages []*model.Package) []releaseStore {
	byRepository := make(map[string][]*model.Package, len(packages))
	for _, pkg := range packages {
		if pkg != nil {
			byRepository[pkg.Repository] = append(byRepository[pkg.Repository], pkg)
		}
	}
	if fleet == nil {
		return []releaseStore{{
			git: a.git, remote: a.pushRemote(), commit: a.cfg.Commit,
			packages: byRepository[""], log: a.log,
		}}
	}
	stores := make([]releaseStore, 0, len(fleet.ordered))
	for _, record := range fleet.ordered {
		stores = append(stores, releaseStore{
			repository: record.repo.Name, git: record.git, remote: record.remote(),
			commit: record.repo.Commit, packages: byRepository[record.repo.Name],
			log: record.git.Log,
		})
	}
	return stores
}

// compare reads this store's release records once and holds every one of them
// against the planning input.
//
// Three runs have nothing to compare and say so rather than reading a remote.
// A run that pushes nothing records nowhere, so its own repository is its
// store. A repository owning no package has no record namespace. And
// commit.verify=false is the setting for a remote that rejects ls-remote and
// accepts pushes, which is the same read this would make: the run keeps the
// exemption the existing up-front checks give it.
func (s releaseStore) compare(ctx context.Context, aliases plan.AliasFilter) error {
	if !s.commit.IsPushEnabled() {
		s.log.Debug().Msg("release records are not compared: this run pushes nothing, so its own repository is its store")
		return nil
	}
	if !s.commit.IsVerifyEnabled() {
		s.log.Debug().Str("remote", gitx.RedactURL(s.remote)).
			Msg("release records are not compared: commit.verify is off for this remote")
		return nil
	}
	formats := releaseTagFormats(s.packages)
	if len(formats) == 0 {
		s.log.Debug().Msg("release records are not compared: this repository owns no package")
		return nil
	}
	stored, err := s.git.RemoteReleaseTags(ctx, s.remote, formats)
	if err != nil {
		return s.storeError(err)
	}
	held, err := s.git.RelevantTagSnapshot(ctx, releaseTagMatcher(s.packages))
	if err != nil {
		return s.storeError(fmt.Errorf("reading this checkout's release tags: %w", err))
	}
	compared := 0
	seen := make(map[string]bool, len(stored))
	for _, pkg := range sortedPackageNames(formats) {
		for _, record := range aliases.Without(stored[pkg], pkg, s.log) {
			if seen[record.Name] {
				continue
			}
			seen[record.Name] = true
			compared++
			if err := s.checkRecord(ctx, record, held); err != nil {
				return err
			}
		}
	}
	s.log.Debug().Str("remote", gitx.RedactURL(s.remote)).Int("records", compared).
		Msg("compared the store's release records with the planning input")
	return nil
}

// checkRecord holds one stored release record against the planning input.
//
// The ancestry question is asked only for a record this checkout lacks, which
// in the ordinary case is none: a record the checkout holds is answered by
// comparing the two commits, and a record on a commit the checkout does not
// hold cannot be reachable from its head, so it can affect no plan this run
// computes.
func (s releaseStore) checkRecord(ctx context.Context, record gitx.Tag, held gitx.TagSnapshot) error {
	if local, isHeld := held[record.Name]; isHeld {
		if local.Commit() == record.Commit {
			return nil
		}
		return s.recordAtOtherCommitError(record, local.Commit())
	}
	isOnHead, err := s.git.IsReachableFromHead(ctx, record.Commit)
	if err != nil {
		return s.storeError(fmt.Errorf("deciding whether %s is in this checkout's history: %w", record.Name, err))
	}
	if !isOnHead {
		s.log.Debug().Str("tag", record.Name).Str("commit", record.Commit).
			Msg("the store holds a release record off this checkout's history; it cannot change this plan")
		return nil
	}
	return s.missingRecordError(record)
}

// missingRecordError is §13.2's incomplete history: the store recorded a
// release of a commit this run plans from, and the run cannot see it.
//
// Planning the package as unreleased would publish that version a second time,
// so the run is refused rather than repaired, and the remedy is the fetch this
// engine deliberately does not make on the operator's behalf.
func (s releaseStore) missingRecordError(record gitx.Tag) error {
	return config.WithDiagnostic(plan.CodeShallowRepository, fmt.Errorf(
		"E196: %s%s records the release %s at %s, which this checkout does not hold although that commit is in its history; "+
			"fetch the remote's tags (git fetch --tags) and run again",
		s.prefix(), gitx.RedactURL(s.remote), record.Name, record.Commit))
}

// recordAtOtherCommitError is §13.2's disagreeing record: one release named at
// two commits, which no tie-break may resolve. Neither side is repaired or
// moved; a person decides which commit the version really is.
func (s releaseStore) recordAtOtherCommitError(record gitx.Tag, local string) error {
	return config.WithDiagnostic(plan.CodeDuplicateVersionTag, fmt.Errorf(
		"E191: %sthe release %s is recorded at %s on %s and at %s in this checkout; "+
			"a published record is never moved, so correct the tag that is wrong before releasing again",
		s.prefix(), record.Name, record.Commit, gitx.RedactURL(s.remote), local))
}

// storeError refuses a run whose store could not be read. It is the same
// answer a failed VerifyRemote gives, for the same reason: a plan built
// without the records is a plan nothing checked.
func (s releaseStore) storeError(err error) error {
	return config.WithDiagnostic(plan.CodeShallowRepository, fmt.Errorf(
		"E196: %scannot read the release records this run would write to: %w", s.prefix(), err))
}

// prefix names the repository in a composed workspace, and nothing at all in a
// single one, where there is no second repository to tell it from.
func (s releaseStore) prefix() string {
	if s.repository == "" {
		return ""
	}
	return "repository " + s.repository + ": "
}

// releaseTagFormats is each package's release-tag format, which is what makes
// a name on the remote a record of this workspace rather than somebody's tag.
func releaseTagFormats(packages []*model.Package) map[string]gitx.TagFormat {
	formats := make(map[string]gitx.TagFormat, len(packages))
	for _, pkg := range packages {
		if pkg != nil {
			formats[pkg.Name] = plan.TagFormatFor(pkg)
		}
	}
	return formats
}

// releaseTagMatcher compiles the same namespaces without their aliases: the
// local side of the comparison is asked only about names the remote already
// answered for, so an alias in the listing would be read and never used.
func releaseTagMatcher(packages []*model.Package) *gitx.TagSnapshotMatcher {
	namespaces := make([]gitx.TagNamespace, 0, len(packages))
	for _, pkg := range packages {
		if pkg != nil {
			namespaces = append(namespaces, gitx.TagNamespace{Package: pkg.Name, Release: plan.TagFormatFor(pkg)})
		}
	}
	return gitx.NewTagSnapshotMatcher(namespaces)
}

// sortedPackageNames fixes the order records are compared in, so two runs of
// one stale checkout refuse on the same record and report the same name.
func sortedPackageNames(formats map[string]gitx.TagFormat) []string {
	names := make([]string, 0, len(formats))
	for name := range formats {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
