// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Preflight's planning snapshots use the same baseline and capture machinery
// as task inputs. They are created before probes, so peers can independently
// interpret the exact configuration/source bytes the first node planned.

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	choreographySnapshotNoSources              = 0
	choreographySnapshotMissingSourceIdentity  = ""
	choreographySnapshotIncompleteSourcesError = "execution: preflight planning sources are incomplete"
	choreographySnapshotMissingDirectoryFormat = "execution: preflight source %q has no repository directory"
	choreographySnapshotResolveDirectoryFormat = "execution: resolving preflight source %q: %w"
	choreographySnapshotRepeatedSourceFormat   = "execution: repeated or incomplete preflight source %q"
	choreographySnapshotSourceSeen             = true
)

type PinnedSourceCaptureOptions struct {
	Sources        []Source
	OutputRoots    map[string][]string
	OpenRepository func(string) *gitx.LocalGitx
	Store          *gitx.LocalGitx
	Log            zerolog.Logger
}

// PinnedRunSources owns temporary baseline indexes until Coordinator.Close.
// Repositories are exact source commitments, imported into the first node's
// mailbox store without creating any Git ref.
type PinnedRunSources struct {
	Repositories []AssignmentRepository
	// Trees map source identity to immutable captured file-tree OIDs. Compare
	// these with PlannedHead to bracket planning; Snapshot commits can differ
	// only because their transport timestamp changed.
	Trees     map[string]string
	commits   []string
	snapshots *snapshots
}

func CapturePinnedRunSources(ctx context.Context, opts PinnedSourceCaptureOptions) (*PinnedRunSources, error) {
	if len(opts.Sources) == choreographySnapshotNoSources || opts.OpenRepository == nil || opts.Store == nil {
		return nil, fmt.Errorf(choreographySnapshotIncompleteSourcesError)
	}
	normalized := make([]Source, len(opts.Sources))
	normalizedRoots := make(map[string][]string, len(opts.OutputRoots))
	for index, source := range opts.Sources {
		if source.Dir == choreographySnapshotMissingSourceIdentity {
			return nil, fmt.Errorf(choreographySnapshotMissingDirectoryFormat, source.Name)
		}
		absoluteDir, err := filepath.Abs(source.Dir)
		if err != nil {
			return nil, fmt.Errorf(choreographySnapshotResolveDirectoryFormat, source.Name, err)
		}
		normalizedRoots[absoluteDir] = opts.OutputRoots[source.Dir]
		source.Dir = absoluteDir
		normalized[index] = source
	}
	s := newSnapshots()
	if err := s.captureBaselines(ctx, baselineCapture{sources: normalized, roots: normalizedRoots,
		open: opts.OpenRepository, log: opts.Log}); err != nil {
		s.closeBaselines()
		return nil, err
	}
	result := &PinnedRunSources{snapshots: s, Trees: make(map[string]string, len(normalized))}
	seen := map[string]bool{}
	for _, source := range normalized {
		if source.Dir == choreographySnapshotMissingSourceIdentity || source.Head == choreographySnapshotMissingSourceIdentity || seen[source.Name] {
			result.Close()
			return nil, fmt.Errorf(choreographySnapshotRepeatedSourceFormat, source.Name)
		}
		seen[source.Name] = choreographySnapshotSourceSeen
		commit, err := s.capture(ctx, opts.OpenRepository(source.Dir), source, opts.Log)
		if err != nil {
			result.Close()
			return nil, err
		}
		if err := opts.Store.ImportCommitFrom(ctx, source.Dir, commit); err != nil {
			result.Close()
			return nil, err
		}
		tree, err := opts.Store.CommitTreeOID(ctx, commit)
		if err != nil {
			result.Close()
			return nil, err
		}
		result.Trees[source.Name] = tree
		result.Repositories = append(result.Repositories, AssignmentRepository{Name: source.Name, Path: source.Path,
			Snapshot: commit, PlannedHead: source.Head})
		result.commits = append(result.commits, commit)
	}
	return result, nil
}

// CommitOIDs returns the probe's exact direct carrier parents.
func (p *PinnedRunSources) CommitOIDs() []string {
	if p == nil {
		return nil
	}
	return slices.Clone(p.commits)
}

// Close releases temporary index copies. It is safe both on preflight error
// and after Coordinator.Close; the snapshots object forgets closed indexes.
func (p *PinnedRunSources) Close() {
	if p != nil && p.snapshots != nil {
		p.snapshots.closeBaselines()
	}
}
