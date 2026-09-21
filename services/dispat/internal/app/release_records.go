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
	stores := comparingStores(a.releaseStores(fleet))
	if len(stores) == 0 {
		return nil
	}
	// Discovery comes after the stores are settled, so a run that records
	// nowhere pays nothing for a comparison it does not make. A workspace that
	// cannot be discovered has no records to compare and no plan either: the
	// planner reports that failure in its own words a moment later, and
	// reporting it here would rename it.
	packages, err := a.packages()
	if err != nil {
		a.log.Debug().Err(err).Msg("release records are not compared: the workspace could not be discovered")
		return nil
	}
	aliases := plan.NewAliasFilter(packages)
	byRepository := packagesByRepository(packages)
	for _, store := range stores {
		store.packages = byRepository[store.repository]
		if err := store.compare(ctx, aliases); err != nil {
			return err
		}
	}
	return nil
}

// releaseStores lists what this run records to: one store for a single
// repository, and one per participating repository of a composed workspace,
// because each of them records to a store of its own.
func (a *App) releaseStores(fleet *workspaceRecorder) []releaseStore {
	if fleet == nil {
		return []releaseStore{{
			git: a.git, remote: a.pushRemote(), commit: a.cfg.Commit, log: a.log,
		}}
	}
	stores := make([]releaseStore, 0, len(fleet.ordered))
	for _, record := range fleet.ordered {
		stores = append(stores, releaseStore{
			repository: record.repo.Name, git: record.git, remote: record.remote(),
			commit: record.repo.Commit, log: record.git.Log,
		})
	}
	return stores
}

// comparingStores keeps the stores this run has something to compare with, and
// says of each one it drops why.
//
// A run that pushes nothing records nowhere, so its own repository is its store
// and its planning input is already the whole of it. commit.verify=false is the
// setting for a remote that rejects ls-remote and accepts pushes, and the
// comparison is another ls-remote: the exemption the up-front checks give that
// remote is the same exemption here.
func comparingStores(stores []releaseStore) []releaseStore {
	comparing := make([]releaseStore, 0, len(stores))
	for _, store := range stores {
		if !store.commit.IsPushEnabled() {
			store.log.Debug().Msg("release records are not compared: this run pushes nothing, so its own repository is its store")
			continue
		}
		if !store.commit.IsVerifyEnabled() {
			// Said out loud rather than at debug, because this is the one
			// exemption a person chose: an engine that forgoes the read must
			// not let the run read as though the records had been compared.
			store.log.Warn().Str("remote", gitx.RedactURL(store.remote)).
				Msg("release records are not compared: commit.verify is off for this remote, " +
					"so a checkout missing a record this remote holds can publish that version a second time")
			continue
		}
		comparing = append(comparing, store)
	}
	return comparing
}

// packagesByRepository groups the workspace's packages by the repository whose
// records they belong to, which is the empty name in a single repository.
func packagesByRepository(packages []*model.Package) map[string][]*model.Package {
	byRepository := make(map[string][]*model.Package, len(packages))
	for _, pkg := range packages {
		if pkg != nil {
			byRepository[pkg.Repository] = append(byRepository[pkg.Repository], pkg)
		}
	}
	return byRepository
}

// compare reads this store's release records once and holds every one of them
// against the planning input. A repository owning no package has no record
// namespace and reads nothing.
func (s releaseStore) compare(ctx context.Context, aliases plan.AliasFilter) error {
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
// A name carrying no version is not a record of a version, whatever shape it
// has: `core@backup` matches the format and states nothing this run could
// plan again, and the planner reads no baseline and no duplicate out of it
// either. The comparison is about the versions a run would publish a second
// time, so it skips those exactly as the planner does.
//
// The ancestry question is asked only for a record this checkout lacks, which
// in the ordinary case is none: a record the checkout holds is answered by
// comparing the two commits, and a record on a commit the checkout does not
// hold cannot be reachable from its head, so it can affect no plan this run
// computes.
func (s releaseStore) checkRecord(ctx context.Context, record gitx.Tag, held gitx.TagSnapshot) error {
	if !record.Parsed {
		s.log.Debug().Str("tag", record.Name).
			Msg("the store holds a tag with no version in it; it records no release")
		return nil
	}
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
