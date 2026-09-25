// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
)

// ---------------------------------------------------------------------------
// §13.2 tags and §13.3 pending windows
// ---------------------------------------------------------------------------

func (cp *computation) loadTagsAndWindows() error {
	if len(cp.histories) > 0 {
		return cp.loadRepositoryTagsAndWindows()
	}
	return cp.loadLegacyTagsAndWindows()
}

func (cp *computation) loadLegacyTagsAndWindows() error {
	// Inventory all reachable tags in one Git operation, then partition them
	// with each package's own format. A plan must see one ref snapshot.
	tagsFor := make([]gitx.Tags, len(cp.pkgs))
	formats := make(map[string]gitx.TagFormat, len(cp.pkgs))
	for _, p := range cp.pkgs {
		if (&Release{Pkg: p}).IsReleasable() {
			formats[p.Name] = (&Release{Pkg: p}).TagFormat()
		}
	}
	all, err := cp.git.TagsForPackages(cp.ctx, formats)
	if err != nil {
		return fmt.Errorf("plan: loading tags: %w", err)
	}
	if err := cp.ctx.Err(); err != nil {
		return fmt.Errorf("plan: loading tags: %w", err)
	}
	if cp.stats != nil {
		cp.stats.TagInventories.Add(1)
	}
	for i, p := range cp.pkgs {
		tagsFor[i] = all[p.Name]
	}

	// Windows are keyed by DISTINCT starting commit: packages whose differently
	// named stable tags point at the same commit share the listing and the
	// immutable membership set. The tag name remains the query argument for
	// Git implementations whose Commits API accepts a ref rather than an OID.
	var boundaries []windowBoundary
	seenBoundary := make(map[string]bool)

	// Every package's aliases, compiled once: an alias of one package can land
	// in another's listing, so the filter is the workspace's rather than the
	// listing owner's. See AliasFilter.
	aliases := NewAliasFilter(cp.pkgs)

	for i, p := range cp.pkgs {
		rel := &Release{Pkg: p}

		// Both baselines of §12.3 are selections over the same per-package
		// list returned by the one bulk inventory.
		tags := tagsFor[i]
		tags = cp.withoutIgnoredTags("", aliases.Without(tags, p.Name, cp.log))
		// Kept for the graduation's dependencies record: reconstructing what a
		// consumer's last stable release shipped against is a question about
		// the provider's tags, and the planner holds no other state between
		// runs (versionAt).
		cp.tags[p.Name] = tags

		// §16 E191: two reachable tags parsing to the same version of this
		// package (build metadata carries no precedence, so "1.2.3" and
		// "1.2.3+b" collide) on different commits make the baseline selection
		// ambiguous — no correct plan exists.
		if a, b, dup := duplicateVersionTags(tags); dup {
			cp.err(CodeDuplicateVersionTag, p.Name, "", fmt.Sprintf(
				"tags %s and %s parse to the same version %s but point at different commits",
				a.Name, b.Name, a.Version.String()))
			return errFatalPlan
		}

		newest, hasNewest := tags.Baseline()
		if hasNewest && newest.Parsed {
			rel.Baseline, rel.HasBaseline = newest.Version, true
			rel.BaselineCommit = newest.Commit
		}
		// §11.1: a package with no baseline is on the stable channel, so a
		// never-released package is graduated by nothing and entered onto a
		// train by any directive naming it.
		rel.BaselineChannel = channelOf(rel.Baseline, rel.HasBaseline)
		rel.Channel = rel.BaselineChannel

		stable, hasStable := tags.StableBaseline()

		// Baseline resolution. The latest parseable *stable* tag wins. When
		// the newest tag exists but cannot be parsed, the pre-last tag is NOT
		// used: the baseline comes from initials (default 0.0.0) while the
		// window is still measured from the unparseable tag, so that already
		// released commits are not counted twice.
		since := ""
		switch {
		case hasStable && stable.Parsed:
			rel.Current, rel.Tagged, since = stable.Version, true, stable.Name
			rel.StableCommit = stable.Commit
		case hasStable:
			since = stable.Name
			rel.StableCommit = stable.Commit
			if init, ok := cp.initials[p.Name]; ok {
				rel.Current, rel.FromInitials = init, true
			}
		default: // never stably released: the window is the whole history (§13.3)
			// Initials seed a first release; a versioning-none package never
			// has one, so a fabricated Current must not appear for it.
			if init, ok := cp.initials[p.Name]; ok && rel.IsReleasable() {
				rel.Current, rel.FromInitials = init, true
			}
		}
		rel.Next = rel.Current
		if rel.HasBaseline {
			rel.Next = rel.Baseline
		}
		cp.rel[p.Name] = rel

		cacheKey := commitWindowCacheKey(rel.StableCommit, since)
		if !seenBoundary[cacheKey] {
			seenBoundary[cacheKey] = true
			boundaries = append(boundaries, windowBoundary{
				key: cacheKey, since: since, commit: rel.StableCommit, pkg: p.Name})
		}
		cp.windowKey[p.Name] = cacheKey
	}

	windows, err := cp.loadLegacyWindows(boundaries)
	if err != nil {
		return err
	}
	// The owed windows extend the union and nothing else: they are nobody's
	// pending window, so the package windows below are the ordinary ones.
	if err := cp.loadOwedWindows(boundaries); err != nil {
		return err
	}
	for _, p := range cp.pkgs {
		cp.window[p.Name] = windows[cp.windowKey[p.Name]]
	}
	cp.log.Debug().Int("packages", len(cp.pkgs)).Int("windows", len(boundaries)).
		Int("commits", len(cp.commits)).Msg("plan: tags and windows loaded")
	return nil
}

// windowBoundary is one distinct stable baseline a window is measured from:
// its cache key, the tag naming it, its peeled commit where the Git
// implementation provides one, and the first package that needed it, which
// is the name a failure to read it is reported against.
type windowBoundary struct {
	key, since, commit, pkg string
}

// loadLegacyWindows is §13.3 over one history: the union of the pending
// windows, ranked, and the membership set of every distinct boundary.
//
// A Git implementation with gitx.UnionHistoryx reads the union in one walk.
// The windows are then recovered from it by the marker pass (ancestry.go): a
// commit is in the window after b exactly when it is not an ancestor-or-self
// of b. Messages and changed paths are read and parsed once, where a read
// per boundary repeats every commit two windows share.
func (cp *computation) loadLegacyWindows(boundaries []windowBoundary) (map[string]*commitSet, error) {
	windows := make(map[string]*commitSet, len(boundaries))
	sizes, err := cp.readUnionWindow(boundaries, windows)
	if err != nil {
		return nil, err
	}
	if sizes == nil {
		if sizes, err = cp.readWindowPerBoundary(boundaries, windows); err != nil {
			return nil, err
		}
	}
	for i, b := range boundaries {
		if cp.stats != nil {
			cp.stats.CommitWindows.Add(1)
			cp.stats.WindowCommitRefs.Add(int64(sizes[i]))
		}
		cp.log.Debug().Str("boundary", b.key).Int("commits", sizes[i]).
			Msg("plan: history window indexed")
	}
	cp.indexAncestry()
	return windows, nil
}

// readWindowPerBoundary reads one listing per distinct boundary. It is every
// Git implementation's path but the real one's, and the real one's when there
// is a single boundary and therefore nothing to share.
func (cp *computation) readWindowPerBoundary(boundaries []windowBoundary, windows map[string]*commitSet) ([]int, error) {
	// Per-boundary commit lists, kept so the union can be ranked afterwards.
	lists := make([][]gitx.Commit, 0, len(boundaries))
	sizes := make([]int, 0, len(boundaries))
	for _, b := range boundaries {
		// A Git implementation that ignores its context would otherwise keep
		// reading one window per boundary after an interrupt. The check costs
		// one atomic load per distinct boundary.
		if err := cp.ctx.Err(); err != nil {
			return nil, fmt.Errorf("plan: loading windows: %w", err)
		}
		commits, err := cp.git.Commits(cp.ctx, b.since)
		if err != nil {
			return nil, fmt.Errorf("plan: %s: %w", b.pkg, err)
		}
		lists = append(lists, commits)
		sizes = append(sizes, len(commits))
	}
	cp.buildUnion(lists)
	for i, b := range boundaries {
		windows[b.key] = commitSetOf(len(cp.commits), func(yield func(int)) {
			for _, c := range lists[i] {
				yield(cp.byKey[commitKey(c)].rank)
			}
		})
	}
	return sizes, nil
}

// readUnionWindow reads every window in one walk. It answers nil sizes when
// the single read does not apply: the Git implementation lacks the capability,
// there is one boundary and so nothing shared, or a boundary has no peeled
// commit id to name it by.
func (cp *computation) readUnionWindow(boundaries []windowBoundary, windows map[string]*commitSet) ([]int, error) {
	union, ok := cp.git.(gitx.UnionHistoryx)
	if !ok || len(boundaries) < 2 {
		return nil, nil
	}
	ids := make([]string, len(boundaries))
	for i, b := range boundaries {
		if b.commit == "" && b.since != "" {
			return nil, nil
		}
		ids[i] = b.commit // "" is the package never stably released: the whole history
	}
	if err := cp.ctx.Err(); err != nil {
		return nil, fmt.Errorf("plan: loading windows: %w", err)
	}
	all, err := union.CommitsSinceAny(cp.ctx, ids)
	if errors.Is(err, gitx.ErrBoundaryNotBehindHead) {
		return nil, nil // a window cannot be recovered by ancestry then; read them one by one
	}
	if err != nil {
		return nil, fmt.Errorf("plan: %s: %w", boundaries[0].pkg, err)
	}

	// The marker pass over the listing as read: position is rank for now.
	at := make(map[string]int32, len(all))
	for i, c := range all {
		at[c.SHA] = int32(i)
	}
	index := newAncestryIndex(len(all), func(pos int) []int32 {
		var parents []int32
		for _, p := range all[pos].Parents {
			if i, ok := at[p]; ok {
				parents = append(parents, i)
			}
		}
		return parents
	})
	if index == nil {
		return nil, nil // not a DAG; let the per-boundary reads say what is wrong
	}
	var markers []int32
	for _, id := range ids {
		if pos, ok := at[id]; ok {
			markers = append(markers, pos)
		}
	}
	index.mark(markers)
	// behind is what boundary i excludes. A boundary the union does not hold is
	// behind every other one, so it excludes nothing: its window is the union.
	behind := func(i int) *commitSet {
		if pos, ok := at[ids[i]]; ok {
			return index.ancestors(pos)
		}
		return nil
	}
	sizes := make([]int, len(boundaries))
	for i := range boundaries {
		if _, held := at[ids[i]]; held && behind(i) == nil {
			return nil, nil // past the index's budget; read the windows one by one
		}
		sizes[i] = len(all) - behind(i).len()
	}

	// The union is ranked exactly as buildUnion ranks per-boundary listings:
	// the longest window first, then whatever each shorter one adds, in its own
	// order. A window's order is its order in the union (gitx.UnionHistoryx),
	// so nested boundaries, which is nearly every history, rank as read.
	byLength := make([]int, len(boundaries))
	for i := range byLength {
		byLength[i] = i
	}
	sort.SliceStable(byLength, func(a, b int) bool { return sizes[byLength[a]] > sizes[byLength[b]] })
	ranked, placed := all, make([]bool, len(all))
	if sizes[byLength[0]] != len(all) {
		ranked = make([]gitx.Commit, 0, len(all))
		for _, i := range byLength {
			excluded := behind(i)
			for pos, c := range all {
				if !placed[pos] && !excluded.has(pos) {
					placed[pos] = true
					ranked = append(ranked, c)
				}
			}
			if len(ranked) == len(all) {
				break
			}
		}
	}
	cp.buildUnion([][]gitx.Commit{ranked})

	// In rank space. Positions are ranks when the union ranked as read, and
	// the pass is cheap enough to repeat when it did not.
	if sizes[byLength[0]] != len(all) {
		if index = cp.newUnionAncestry(); index == nil {
			return nil, fmt.Errorf("plan: %s: the union history is not a DAG", boundaries[0].pkg)
		}
		markers = markers[:0]
		for _, id := range ids {
			if rec := cp.byKey[id]; rec != nil {
				markers = append(markers, int32(rec.rank))
			}
		}
		index.mark(markers)
	}
	cp.anc = index
	for i, b := range boundaries {
		var excluded *commitSet
		if rec := cp.byKey[ids[i]]; rec != nil {
			if excluded = index.ancestors(int32(rec.rank)); excluded == nil {
				return nil, fmt.Errorf("plan: %s: window boundary %s was not indexed", b.pkg, b.key)
			}
		}
		windows[b.key] = commitSetOf(len(cp.commits), func(yield func(int)) {
			for rank := range cp.commits {
				if !excluded.has(rank) {
					yield(rank)
				}
			}
		})
	}
	return sizes, nil
}

// commitWindowCacheKey identifies the history boundary independently of the
// spelling of the tag that names it. Some lightweight Git implementations do
// not provide peeled commit IDs; retaining the tag in that case is safe and
// avoids conflating two unknown boundaries. Prefixes keep an OID, a tag name,
// and the root history in separate namespaces.
func commitWindowCacheKey(commit, tag string) string {
	if commit != "" {
		return "commit:" + commit
	}
	if tag != "" {
		return "tag:" + tag
	}
	return "root:"
}

// duplicateVersionTags finds two parsed tags carrying the same version on
// different commits. Version identity ignores build metadata, exactly as
// precedence does.
func duplicateVersionTags(tags gitx.Tags) (a, b gitx.Tag, dup bool) {
	byVersion := make(map[string]gitx.Tag, len(tags))
	for _, t := range tags {
		if !t.Parsed {
			continue
		}
		key := t.Version.String()
		prev, seen := byVersion[key]
		if seen && prev.Commit != t.Commit {
			return prev, t, true
		}
		if !seen {
			byVersion[key] = t
		}
	}
	return gitx.Tag{}, gitx.Tag{}, false
}

// baselineChannel is channelOf(baseline(P)) (§11.1). It reads the package's
// own tag and nothing computed in this run, which is what makes a transition's
// <from> stable across phases and gives the channel axis its convergence
// (§13.7c G7).
func (cp *computation) baselineChannel(pkg string) string {
	if rel := cp.rel[pkg]; rel != nil {
		return rel.BaselineChannel
	}
	return ccme.ChannelStable
}

// containedInBaseline reports whether the commit has already been published by
// the package's baseline tag. It can only be true on a prerelease train: the
// window is measured from the *stable* tag (§13.3), so a train's window spans
// commits its prerelease releases already shipped, and those commits are ahead
// of StableCommit but at-or-behind BaselineCommit. For a stable package the
// two tags coincide and the window already excludes released commits.
//
// Contained work still counts toward the train's bump — §11.4 recomputes the
// target over the whole window — but it is discharged for every purpose that
// asks "is this still pending?": it does not re-release the train (NewWork),
// a Release-As it carries is no longer in force, and a cancel cannot discard
// it (§10.3: cancellation never reaches a published tag, and a prerelease tag
// is a published tag).
func (cp *computation) containedInBaseline(pkg, key string) bool {
	rel := cp.rel[pkg]
	if len(cp.histories) > 0 {
		repository, _ := splitHistoryKey(key)
		stable := cp.stableBoundaries[pkg][globx.Fold(repository)]
		published := cp.publishedBoundaries[pkg][globx.Fold(repository)]
		if published == "" || published == stable {
			return false
		}
		return cp.ancestorOrSelf(key, published)
	}
	if rel == nil || rel.BaselineCommit == "" || rel.BaselineCommit == rel.StableCommit {
		return false
	}
	return cp.ancestorOrSelf(key, rel.BaselineCommit)
}

func (cp *computation) inWindow(pkg, key string) bool {
	if len(cp.histories) == 0 {
		rec := cp.byKey[key]
		return rec != nil && cp.window[pkg].has(rec.rank)
	}
	repository, _ := splitHistoryKey(key)
	if p := cp.byName[pkg]; p != nil && strings.EqualFold(repository, cp.controlRepo) &&
		!strings.EqualFold(p.Repository, cp.controlRepo) {
		boundary := cp.stableBoundaries[pkg][globx.Fold(cp.controlRepo)]
		return boundary == "" || !cp.ancestorOrSelf(key, boundary)
	}
	for _, window := range cp.windowRefs[pkg] {
		if window[key] {
			return true
		}
	}
	return false
}

func (cp *computation) windowSize(pkg string) int {
	if len(cp.histories) == 0 {
		return cp.window[pkg].len()
	}
	n := 0
	for _, window := range cp.windowRefs[pkg] {
		n += len(window)
	}
	return n
}

// buildUnion merges the per-package windows into one history-ordered,
// deduplicated list. A commit reachable through several windows contributes
// its units exactly once (§13.3).
func (cp *computation) buildUnion(lists [][]gitx.Commit) {
	// Rank against the longest window first, so the widest available view of
	// history defines the ordering and shorter windows (suffixes of it) reuse
	// the ranks they already have.
	idx := make([]int, len(lists))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return len(lists[idx[a]]) > len(lists[idx[b]]) })

	for _, i := range idx {
		for _, c := range lists[i] {
			key := commitKey(c)
			if _, ok := cp.byKey[key]; ok {
				continue
			}
			rec := &commitRec{commit: c, key: key, rank: len(cp.commits)}
			cp.byKey[key] = rec
			cp.commits = append(cp.commits, rec)
			if cp.stats != nil {
				cp.stats.UniqueCommits.Add(1)
			}
			if ps := c.Parents; len(ps) > 0 {
				cp.parents[key] = ps
				cp.linked = true
			}
		}
	}
}
