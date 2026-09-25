// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// The shared files of a release commit.
//
// `commit.include` names files the release commit stages beside the published
// packages' folders, a workspace lock file first among them. Those files are
// written during the run by whichever package's version or syncLock stage
// regenerates them, and a whole-workspace regenerator records every package's
// manifest as it finds it: a package that later failed or was skipped leaves
// its planned version in the shared file. Committed as they stand, the files
// would record a version that never published.
//
// So when a package prepared its release files and did not publish, the
// closing phase re-synchronizes the shared files before the release commit:
// the unpublished packages' tracked files and the include paths go back to
// HEAD, leaving the published folders and changelogs alone, and the published
// packages' syncLock scripts run again with an environment naming only what
// published. A re-synchronization that cannot finish restores the include
// paths again and keeps them out of the release commit, with E223 naming the
// remedy, while the published folders are still committed.
//
// This covers a single history. A fleet commits each package's include paths
// into that package's own source commit while the run is still going, so a
// later re-synchronization could not take a sibling's write back out of it.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// syncSharedIncludes re-synchronizes the commit.include paths of a single
// history when a package prepared its release files and did not publish, and
// reports whether the release commit must leave the include paths out because
// the re-synchronization did not finish. With every prepared package
// published it does nothing at all.
//
// The Git restores run on recordCtx, the scripts on the live run: an
// interrupt stops a script and starts no other, and the include paths are
// restored again after it.
func (a *App) syncSharedIncludes(ctx, recordCtx context.Context, closing closingRecord, crit *criticals) bool {
	if !a.isIncludeSyncNeeded(closing) {
		return false
	}
	includes := a.resolveIncludePaths()
	exclusions := formatPublishedExclusions(closing.plan, closing.results)
	a.log.Info().Strs("paths", a.cfg.Commit.Include).
		Msg("re-synchronizing shared include paths without the versions that did not publish")
	err := a.resyncIncludes(ctx, recordCtx, closing, includes, exclusions)
	if err == nil {
		return false
	}
	// A regenerator stopped halfway can leave a file half written, so the
	// paths go back to HEAD before the commit is made without them.
	if restoreErr := a.git.RestoreToHead(recordCtx, includes, exclusions); restoreErr != nil {
		err = errors.Join(err, fmt.Errorf("restoring the include paths again: %w", restoreErr))
	}
	crit.record(a.log, plan.CodeCommitFailed, err,
		"shared include paths could not be re-synchronized without the versions that did not publish, so the "+
			"release commit leaves them out; run the syncLock scripts of the published packages, for example "+
			"`dispat run <script> --package <package> --since all`, then commit the paths",
		func(e *zerolog.Event) *zerolog.Event {
			return e.Strs("paths", a.cfg.Commit.Include).Strs("packages", resolveSyncLockRuns(closing))
		})
	return true
}

// isIncludeSyncNeeded reports whether the release commit's include paths may
// hold a version that did not publish: a single history making release
// commits with include paths, at least one published package for the commit
// to record, and a package that prepared its release files and did not
// publish.
func (a *App) isIncludeSyncNeeded(closing closingRecord) bool {
	if closing.fleet != nil || !a.cfg.Commit.IsEnabled() || len(a.cfg.Commit.Include) == 0 {
		return false
	}
	var published, unpublishedPrepared int
	for _, res := range closing.results {
		switch {
		case res.Status == release.StatusPublished:
			published++
		case res.IsPrepared:
			unpublishedPrepared++
		}
	}
	return published > 0 && unpublishedPrepared > 0
}

// resyncIncludes restores the tracked files of every prepared package that did
// not publish, then the include paths, and runs the published packages'
// syncLock scripts again. It stops at the first step that fails.
func (a *App) resyncIncludes(ctx, recordCtx context.Context, closing closingRecord, includes, exclusions []string) error {
	// First, so that a regenerator reading the workspace's manifests finds
	// each of them at the version that exists. Untracked files stay for
	// inspection.
	if err := a.git.RestoreToHead(recordCtx, resolveUnpublishedPrepared(closing), exclusions); err != nil {
		return fmt.Errorf("restoring the packages that did not publish: %w", err)
	}
	if err := a.git.RestoreToHead(recordCtx, includes, exclusions); err != nil {
		return fmt.Errorf("restoring the include paths: %w", err)
	}
	return a.rerunSyncLocks(ctx, closing)
}

// rerunSyncLocks runs the syncLock scripts of every published package whose
// syncLock stage ran commands during the run, one package at a time in plan
// order, which keeps within any syncLock budget. Each runs with the settled
// environment (release.SettledEnv). It answers the first failure, and the
// run's cancellation before it starts a package.
func (a *App) rerunSyncLocks(ctx context.Context, closing closingRecord) error {
	names := resolveSyncLockRuns(closing)
	if len(names) == 0 {
		a.log.Warn().Strs("paths", a.cfg.Commit.Include).
			Msg("no published package ran syncLock scripts to regenerate the shared include paths, so they stay as HEAD has them")
		return nil
	}
	runner := a.packageRunner()
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("re-running syncLock scripts: %w", err)
		}
		rel := closing.plan.Releases[name]
		log := a.log.With().Str("package", name).Str("stage", "syncLock").Logger()
		env := release.SettledEnv(release.SettledEnvRequest{Plan: closing.plan, Results: closing.results,
			Package: name, Stage: "syncLock"})
		seq := release.Sequence{Runner: runner, Dir: rel.Pkg.Dir, Stage: "syncLock",
			Commands: rel.Pkg.Space.AutoVersion.SyncLock, Env: env, Log: log, FailFast: true}
		if err := seq.Run(ctx); err != nil {
			return fmt.Errorf("re-running the syncLock scripts of %s: %w", name, err)
		}
		log.Info().Msg("syncLock re-run for the shared include paths")
	}
	return nil
}

// resolveSyncLockRuns lists, in plan order, the published packages whose
// syncLock stage ran its commands during the run.
func resolveSyncLockRuns(closing closingRecord) []string {
	var names []string
	for _, name := range closing.plan.Order {
		res, ok := closing.results[name]
		if !ok || res.Status != release.StatusPublished || !res.IsSyncLockRun {
			continue
		}
		if av := closing.plan.Releases[name].Pkg.Space.AutoVersion; av == nil || len(av.SyncLock) == 0 {
			continue
		}
		names = append(names, name)
	}
	return names
}

// resolveUnpublishedPrepared lists the folders of the packages that prepared
// their release files and did not publish.
func resolveUnpublishedPrepared(closing closingRecord) []string {
	var dirs []string
	for _, name := range closing.plan.Order {
		res, ok := closing.results[name]
		if !ok || !res.IsPrepared || res.Status == release.StatusPublished {
			continue
		}
		dirs = append(dirs, closing.plan.Releases[name].Pkg.Dir)
	}
	return dirs
}

// resolveIncludePaths is the commit.include list as absolute paths, whether
// or not each exists: the restore skips what Git does not know, and the
// missing ones are warned about once, when the release commit stages them.
func (a *App) resolveIncludePaths() []string {
	paths := make([]string, 0, len(a.cfg.Commit.Include))
	for _, include := range a.cfg.Commit.Include {
		paths = append(paths, filepath.Join(a.root, filepath.FromSlash(include)))
	}
	return paths
}

// formatPublishedExclusions lists what a restore of shared paths must never
// reach: every published package's folder and changelog file. A changelog may
// resolve outside its package's folder, and an include of `.` would otherwise
// take the release back out of the working tree.
func formatPublishedExclusions(pl *plan.Plan, results map[string]*release.Result) []string {
	var exclusions []string
	for _, name := range pl.Order {
		res, ok := results[name]
		if !ok || res.Status != release.StatusPublished {
			continue
		}
		rel := pl.Releases[name]
		exclusions = append(exclusions, rel.Pkg.Dir)
		if rel.Pkg.Changelog.Enabled {
			exclusions = append(exclusions, filepath.Join(rel.Pkg.Dir, filepath.FromSlash(resolveChangelogFile(rel))))
		}
	}
	return exclusions
}
