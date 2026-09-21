package plan

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// RepositoryHistory is one immutable repository snapshot participating in a
// composed plan. Path is the repository's gitlink path in the control
// repository; it is empty for the control history and for legacy plans.
type RepositoryHistory struct {
	Name             string
	Root             string
	Path             string
	Git              gitx.Gitx
	ParserConfig     ccme.Config
	NonPackageScopes []string
	Control          bool
	// Linker is the repository whose fleet link reached this one in a
	// choreographed fleet. Exactly one participant has none: the entry.
	Linker string
	// Links maps each linked peer to the path holding it in this repository.
	// It is empty for an orchestrated fleet, whose one inventory is the
	// control repository's `.gitmodules`.
	Links map[string]string
}

// RepositoryBaseline is an explicit cross-repository release boundary. Its
// Revision is already a full commit OID resolved and reachability-checked by
// workspace composition.
type RepositoryBaseline struct {
	Consumer, ReleaseTag, Repository, Revision string
}

// HistoryStats exposes operation counts for debug diagnostics, scale tests
// and benchmarks. Counters are safe under the bounded concurrent tag reads.
type HistoryStats struct {
	TagInventories         atomic.Int64
	CommitWindows          atomic.Int64
	UniqueCommits          atomic.Int64
	CanonicalBytes         atomic.Int64
	WindowCommitRefs       atomic.Int64
	AncestryLookups        atomic.Int64
	ControlSnapshotRuns    atomic.Int64
	PersistentLinkNodes    atomic.Int64
	ReachabilityEdges      atomic.Int64
	ChannelFrontierEntries atomic.Int64
	// LinkReads counts the Git reads a choreographed fleet's boundary
	// evidence makes: one per repository for the release subjects, and one
	// per (repository, revision) whose links a route passes through.
	LinkReads atomic.Int64
}

type historyCommit struct {
	repository string
	root       string
	commit     gitx.Commit
}

func cloneHistoryCommit(repository, root string, commit gitx.Commit) historyCommit {
	parents := make([]string, len(commit.Parents))
	for i, parent := range commit.Parents {
		parents[i] = strings.Clone(parent)
	}
	files := make([]string, len(commit.Files))
	for i, file := range commit.Files {
		files[i] = strings.Clone(file)
	}
	return historyCommit{
		repository: strings.Clone(repository),
		root:       strings.Clone(root),
		commit: gitx.Commit{
			SHA:         strings.Clone(commit.SHA),
			Parents:     parents,
			AuthorName:  strings.Clone(commit.AuthorName),
			AuthorEmail: strings.Clone(commit.AuthorEmail),
			Message:     strings.Clone(commit.Message),
			Files:       files,
		},
	}
}

type baselineKey struct {
	consumer, tag, repository string
}

const historyKeySeparator = "\x00"

func historyKey(repository, sha string) string {
	if repository == "" {
		return sha
	}
	return repository + historyKeySeparator + sha
}

func splitHistoryKey(key string) (repository, sha string) {
	if before, after, ok := strings.Cut(key, historyKeySeparator); ok {
		return before, after
	}
	return "", key
}

func rawHistoryKey(key string) string {
	_, sha := splitHistoryKey(key)
	return sha
}

func commitHistoryKey(c historyCommit) string {
	key := commitKey(c.commit)
	return historyKey(c.repository, key)
}

func qualifyParents(repository string, parents []string) []string {
	if repository == "" || len(parents) == 0 {
		return parents
	}
	out := make([]string, len(parents))
	for i, parent := range parents {
		out[i] = historyKey(repository, parent)
	}
	return out
}

type gitlinkSnapshotReader interface {
	GitlinksAt(ctx context.Context, commit string) (map[string]string, error)
}

type controlSnapshot struct {
	commit string
	state  *controlGitlinkState
}

type controlGitlinkState struct {
	links *persistentLinkNode
}

type persistentLinkNode struct {
	left, right *persistentLinkNode
	value       string
}

type controlHistoryReader interface {
	ControlGitlinkHistory(context.Context) ([]gitx.ControlHistoryCommit, error)
}

func checkpointTagKey(repository, tag string) string {
	return strings.ToLower(repository) + historyKeySeparator + tag
}

// loadRepositoryTagsAndWindows is the composed-history form of §13.2-13.3.
// It retains one graph and one unit stream while isolating tag namespaces,
// windows, parsers and ancestry by repository.
// loadRepositoryTagsAndWindows is §13.2 and §13.3 over a composed workspace.
// It runs as three phases, in the order each one's inputs become available:
// the repositories' tag inventories, the baselines those tags resolve to, and
// the pending windows those baselines bound.
func (cp *computation) loadRepositoryTagsAndWindows() error {
	if err := cp.loadRepositoryTags(); err != nil {
		return err
	}
	if err := cp.resolveTagBaselines(); err != nil {
		return err
	}
	return cp.loadRepositoryWindows()
}

// loadRepositoryTags is §13.2 phase one: every participating repository's tag
// inventory, partitioned onto its own packages.
//
// A real CLI inventories one repository's refs in a single git process.
// Lightweight Git implementations retain the bounded per-package fallback,
// which is also the path an interrupt has to be able to stop.
func (cp *computation) loadRepositoryTags() error {
	packagesByRepo := make(map[string][]*model.Package)
	for _, p := range cp.pkgs {
		key := strings.ToLower(p.Repository)
		packagesByRepo[key] = append(packagesByRepo[key], p)
	}
	// Sorted, so which repository a failure names and the order the trace
	// reads in are properties of the workspace rather than of a map walk.
	repositories := make([]string, 0, len(packagesByRepo))
	for repository := range packagesByRepo {
		repositories = append(repositories, repository)
	}
	slices.Sort(repositories)

	for _, repository := range repositories {
		packages := packagesByRepo[repository]
		history, ok := cp.histories[repository]
		if !ok {
			return fmt.Errorf("plan: repository history %q is missing", repository)
		}
		aliases := NewAliasFilter(packages)
		formats := make(map[string]gitx.TagFormat, len(packages))
		for _, p := range packages {
			if (&Release{Pkg: p}).IsReleasable() {
				formats[p.Name] = (&Release{Pkg: p}).TagFormat()
			}
		}
		if bulk, ok := history.Git.(interface {
			TagsForPackages(context.Context, map[string]gitx.TagFormat) (map[string]gitx.Tags, error)
		}); ok {
			all, err := bulk.TagsForPackages(cp.ctx, formats)
			if err != nil {
				return fmt.Errorf("plan: repository %s loading tags: %w", history.Name, err)
			}
			if cp.stats != nil {
				cp.stats.TagInventories.Add(1)
			}
			for _, p := range packages {
				cp.tags[p.Name] = cp.withoutIgnoredTags(history.Name, aliases.Without(all[p.Name], p.Name, cp.log))
			}
		} else if err := cp.loadRepositoryTagsPerPackage(history, packages, aliases); err != nil {
			return err
		}
		cp.log.Debug().Str("repository", history.Name).Int("packages", len(packages)).
			Int("tags", repositoryTagCount(cp.tags, packages)).Msg("plan: repository tag inventory loaded")
	}
	return nil
}

// loadRepositoryTagsPerPackage is the fallback for a repository whose Git
// implementation offers no bulk inventory: one query per releasable package,
// in order, stopping where the caller's cancellation found it.
func (cp *computation) loadRepositoryTagsPerPackage(
	history RepositoryHistory, packages []*model.Package, aliases AliasFilter) error {

	for _, p := range packages {
		if !(&Release{Pkg: p}).IsReleasable() {
			continue
		}
		// A Git implementation that ignores its context would otherwise keep
		// querying one package after another past an interrupt. The bulk
		// inventory is a single call and needs no such check.
		if err := cp.ctx.Err(); err != nil {
			return fmt.Errorf("plan: repository %s loading tags: %w", history.Name, err)
		}
		tags, err := history.Git.Tags(cp.ctx, p.Name, (&Release{Pkg: p}).TagFormat())
		if err != nil {
			return fmt.Errorf("plan: %s: %w", p.Name, err)
		}
		if cp.stats != nil {
			cp.stats.TagInventories.Add(1)
		}
		cp.tags[p.Name] = cp.withoutIgnoredTags(history.Name, aliases.Without(tags, p.Name, cp.log))
	}
	return nil
}

// resolveTagBaselines is §12.3 over the loaded inventories: each package's
// newest tag and newest stable tag become its release's baseline and current
// version. Commit fields stay raw for records and integrations; the qualified
// keys beside them remain internal.
func (cp *computation) resolveTagBaselines() error {
	stableTags := make(map[string]gitx.Tag, len(cp.pkgs))
	latestTags := make(map[string]gitx.Tag, len(cp.pkgs))
	for _, p := range cp.pkgs {
		tags := cp.tags[p.Name]
		if a, b, dup := duplicateVersionTags(tags); dup {
			cp.err(CodeDuplicateVersionTag, p.Name, "", fmt.Sprintf(
				"tags %s and %s parse to the same version %s but point at different commits",
				a.Name, b.Name, a.Version.String()))
			return errFatalPlan
		}
		rel := &Release{Pkg: p}
		newest, hasNewest := tags.Baseline()
		if hasNewest {
			latestTags[p.Name] = newest
		}
		if hasNewest && newest.Parsed {
			rel.Baseline, rel.HasBaseline = newest.Version, true
			rel.BaselineCommit = newest.Commit
			rel.baselineCommitKey = historyKey(p.Repository, newest.Commit)
		}
		rel.BaselineChannel = channelOf(rel.Baseline, rel.HasBaseline)
		rel.Channel = rel.BaselineChannel
		stable, hasStable := tags.StableBaseline()
		if hasStable {
			stableTags[p.Name] = stable
			rel.StableCommit = stable.Commit
			rel.stableCommitKey = historyKey(p.Repository, stable.Commit)
			if stable.Parsed {
				rel.Current, rel.Tagged = stable.Version, true
			} else if init, ok := cp.initials[p.Name]; ok {
				rel.Current, rel.FromInitials = init, true
			}
		} else if init, ok := cp.initials[p.Name]; ok && rel.IsReleasable() {
			rel.Current, rel.FromInitials = init, true
		}
		rel.Next = rel.Current
		if rel.HasBaseline {
			rel.Next = rel.Baseline
		}
		cp.rel[p.Name] = rel
	}
	cp.stableTags, cp.latestTags = stableTags, latestTags
	return cp.validateRepositoryBaselines()
}

// windowIndex is the shared state of one plan's window loading: the commit
// lists already read per boundary, the immutable membership sets built from
// them, the one canonical copy of each commit, and the interned keys that let
// overlapping windows of one repository share their entries.
//
// It exists as a type rather than a closure's captured variables because
// every window a package attaches is one of these entries, and the sharing —
// not the reading — is what keeps a fleet's memory proportional to its
// history rather than to its packages times its history.
type windowIndex struct {
	commitLists  map[string][]string
	windowSets   map[string]map[string]bool
	canonical    map[string]historyCommit
	internedKeys map[string]string
	lists        [][]string
	// unions holds, per cache key, a window already read as part of its
	// repository's single union walk; see readRepositoryUnions.
	unions map[string]unionWindow
}

// unionWindow is one boundary's window inside a union listing: the listing,
// and the commits of it the boundary is an ancestor-or-self of, which are the
// ones the window leaves out. The window is what remains, in listing order.
type unionWindow struct {
	all      []gitx.Commit
	excluded *commitSet
}

func newWindowIndex() *windowIndex {
	return &windowIndex{
		commitLists:  make(map[string][]string),
		windowSets:   make(map[string]map[string]bool),
		canonical:    make(map[string]historyCommit),
		internedKeys: make(map[string]string),
		unions:       make(map[string]unionWindow),
	}
}

// load returns the immutable membership set of one repository's history after
// boundary, together with the cache key naming that view. Packages released at
// the same boundary share both.
func (cp *computation) load(idx *windowIndex, history RepositoryHistory, boundary, pkg string) (
	map[string]bool, string, error) {

	_, rawBoundary := splitHistoryKey(boundary)
	cacheKey := historyKey(history.Name, rawBoundary)
	keys, ok := idx.commitLists[cacheKey]
	if !ok {
		var raw []gitx.Commit
		var err error
		if history.Control && cp.controlIndexed {
			raw = cp.controlCommitsAfter(rawBoundary)
		} else if window, read := idx.unions[cacheKey]; read {
			raw = make([]gitx.Commit, 0, len(window.all)-window.excluded.len())
			for pos, commit := range window.all {
				if !window.excluded.has(pos) {
					raw = append(raw, commit)
				}
			}
			delete(idx.unions, cacheKey)
		} else {
			// Stop reading one window after another once the caller has gone,
			// for the same reason the tag fallback does.
			if err := cp.ctx.Err(); err != nil {
				return nil, "", fmt.Errorf("plan: %s history for %s: %w", history.Name, pkg, err)
			}
			raw, err = history.Git.Commits(cp.ctx, rawBoundary)
			if err != nil {
				return nil, "", fmt.Errorf("plan: %s history for %s: %w", history.Name, pkg, err)
			}
		}
		counted := pkg != controlIntentLabel
		if cp.stats != nil && counted {
			cp.stats.CommitWindows.Add(1)
		}
		keys = make([]string, 0, len(raw))
		for _, commit := range raw {
			item := historyCommit{repository: history.Name, root: history.Root, commit: commit}
			key := commitHistoryKey(item)
			if interned, exists := idx.internedKeys[key]; exists {
				key = interned
			} else {
				idx.internedKeys[key] = key
			}
			keys = append(keys, key)
			if _, exists := idx.canonical[key]; !exists {
				cloned := cloneHistoryCommit(history.Name, history.Root, commit)
				idx.canonical[key] = cloned
				if cp.stats != nil {
					cp.stats.CanonicalBytes.Add(int64(historyCommitBytes(cloned)))
				}
			}
		}
		idx.commitLists[cacheKey] = keys
		idx.lists = append(idx.lists, keys)
		if cp.stats != nil && counted {
			cp.stats.WindowCommitRefs.Add(int64(len(keys)))
		}
		cp.log.Debug().Str("repository", history.Name).Str("boundary", rawBoundary).
			Int("commits", len(keys)).Msg("plan: repository history window indexed")
	}
	set, ok := idx.windowSets[cacheKey]
	if !ok {
		set = make(map[string]bool, len(keys))
		for _, key := range keys {
			set[key] = true
		}
		idx.windowSets[cacheKey] = set
	}
	return set, cacheKey, nil
}

// readRepositoryUnions reads, once, every repository whose windows start at
// more than one boundary, and leaves each of those windows in idx.unions for
// load to pick up. It is loadLegacyWindows' single read (plan.go) per
// repository: the union in one walk, the windows recovered from it by the
// marker pass. A repository it does not apply to is simply left to load: a Git
// implementation without gitx.UnionHistoryx, a single boundary, a boundary
// that is not a full commit id, or the control history where it is served
// from its own index.
func (cp *computation) readRepositoryUnions(idx *windowIndex) error {
	type repositoryBoundaries struct {
		history RepositoryHistory
		raw     []string
		seen    map[string]bool
		pkg     string
	}
	var order []string
	byRepository := make(map[string]*repositoryBoundaries)
	for _, p := range cp.pkgs {
		for _, repository := range cp.relevantRepositories(p.Name) {
			folded := strings.ToLower(repository)
			history := cp.histories[folded]
			if history.Control && cp.controlIndexed {
				continue
			}
			rb := byRepository[folded]
			if rb == nil {
				rb = &repositoryBoundaries{history: history, seen: make(map[string]bool), pkg: p.Name}
				byRepository[folded] = rb
				order = append(order, folded)
			}
			for _, boundary := range []string{cp.stableBoundaries[p.Name][folded], cp.publishedBoundaries[p.Name][folded]} {
				_, raw := splitHistoryKey(boundary)
				if !rb.seen[raw] {
					rb.seen[raw] = true
					rb.raw = append(rb.raw, raw)
				}
			}
		}
	}
	for _, folded := range order {
		rb := byRepository[folded]
		union, ok := rb.history.Git.(gitx.UnionHistoryx)
		if !ok || len(rb.raw) < 2 || !allCommitIDs(rb.raw) {
			continue
		}
		if err := cp.ctx.Err(); err != nil {
			return fmt.Errorf("plan: %s history for %s: %w", rb.history.Name, rb.pkg, err)
		}
		all, err := union.CommitsSinceAny(cp.ctx, rb.raw)
		if errors.Is(err, gitx.ErrBoundaryNotBehindHead) {
			continue // a pin off this head: no window is recoverable by ancestry
		}
		if err != nil {
			return fmt.Errorf("plan: %s history for %s: %w", rb.history.Name, rb.pkg, err)
		}
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
			continue
		}
		var markers []int32
		for _, raw := range rb.raw {
			if pos, ok := at[raw]; ok {
				markers = append(markers, pos)
			}
		}
		index.mark(markers)
		windows := make(map[string]unionWindow, len(rb.raw))
		for _, raw := range rb.raw {
			window := unionWindow{all: all}
			if pos, ok := at[raw]; ok {
				if window.excluded = index.ancestors(pos); window.excluded == nil {
					windows = nil // past the index's budget: read them one by one
					break
				}
			}
			// A boundary the union does not hold is behind every other one, or
			// is no boundary at all: its window is the whole union.
			windows[historyKey(rb.history.Name, raw)] = window
		}
		for key, window := range windows {
			idx.unions[key] = window
		}
	}
	return nil
}

// allCommitIDs reports whether every boundary is a full object id or empty,
// the empty one being the repository read from its first commit.
func allCommitIDs(boundaries []string) bool {
	for _, b := range boundaries {
		if b == "" {
			continue
		}
		if len(b) != 40 && len(b) != 64 {
			return false
		}
		for i := 0; i < len(b); i++ {
			if c := b[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}

// controlIntentLabel names the control history's own window in the trace. It
// is not a package, and it is deliberately left out of the per-package window
// counts the scale tests read.
const controlIntentLabel = "control intent"

// loadRepositoryWindows is §13.3 over a composed workspace: each package's
// pending window in every repository its plan reads, measured from the
// boundary that repository's release checkpoint resolves to.
func (cp *computation) loadRepositoryWindows() error {
	if err := cp.evidenceFor().index(); err != nil {
		return err
	}
	snapshots, ambiguous := cp.controlSnapshots, cp.controlAmbiguous

	idx := newWindowIndex()
	// Every boundary first, so that a repository read through more than one of
	// them is read once (readRepositoryUnions), then the windows themselves.
	for _, p := range cp.pkgs {
		stable, latest := cp.stableTags[p.Name], cp.latestTags[p.Name]
		cp.stableBoundaries[p.Name] = make(map[string]string)
		cp.publishedBoundaries[p.Name] = make(map[string]string)
		for _, repository := range cp.relevantRepositories(p.Name) {
			boundary, err := cp.repositoryBoundary(p, stable, repository, snapshots, ambiguous)
			if err != nil {
				return err
			}
			cp.stableBoundaries[p.Name][strings.ToLower(repository)] = boundary
			published, err := cp.repositoryBoundary(p, latest, repository, snapshots, ambiguous)
			if err != nil {
				return err
			}
			cp.publishedBoundaries[p.Name][strings.ToLower(repository)] = published
		}
	}
	if err := cp.readRepositoryUnions(idx); err != nil {
		return err
	}
	for _, p := range cp.pkgs {
		for _, repository := range cp.relevantRepositories(p.Name) {
			boundary := cp.stableBoundaries[p.Name][strings.ToLower(repository)]
			published := cp.publishedBoundaries[p.Name][strings.ToLower(repository)]

			history := cp.histories[strings.ToLower(repository)]
			stableWindow, stableKey, err := cp.load(idx, history, boundary, p.Name)
			if err != nil {
				return err
			}
			cp.windowRefs[p.Name] = append(cp.windowRefs[p.Name], stableWindow)
			cp.windowKeys[p.Name] = append(cp.windowKeys[p.Name], stableKey)

			// Ordinarily the latest prerelease boundary descends from the stable
			// boundary, making its window a subset. Explicit tuples may describe
			// a consumer whose prerelease shipped an older provider revision than
			// its stable tag. Retain that fresh window as a second shared view so
			// the intervening provider work is visible as catch-up work.
			if published != boundary {
				freshWindow, freshKey, err := cp.load(idx, history, published, p.Name)
				if err != nil {
					return err
				}
				cp.windowRefs[p.Name] = append(cp.windowRefs[p.Name], freshWindow)
				cp.windowKeys[p.Name] = append(cp.windowKeys[p.Name], freshKey)
			}
		}
	}
	if cp.controlIndexed {
		control := cp.histories[strings.ToLower(cp.controlRepo)]
		if _, _, err := cp.load(idx, control, "", controlIntentLabel); err != nil {
			return err
		}
	}
	cp.buildRepositoryUnion(idx.lists, idx.canonical)
	cp.indexRepositoryAncestry()
	cp.log.Debug().Int("packages", len(cp.pkgs)).Int("windows", len(idx.commitLists)).
		Int("commits", len(cp.commits)).Msg("plan: repository tags and windows loaded")
	// Every retained commit, parent and control-state scalar has been cloned or
	// interned by this point. Drop the bulk records so substring-backed fields
	// cannot keep several overlapping git-log buffers live for the whole plan.
	cp.controlHistory = nil
	return nil
}

func historyCommitBytes(commit historyCommit) int {
	n := len(commit.repository) + len(commit.root) + len(commit.commit.SHA) +
		len(commit.commit.AuthorName) + len(commit.commit.AuthorEmail) + len(commit.commit.Message)
	for _, parent := range commit.commit.Parents {
		n += len(parent)
	}
	for _, file := range commit.commit.Files {
		n += len(file)
	}
	return n
}

func repositoryTagCount(tags map[string]gitx.Tags, packages []*model.Package) int {
	total := 0
	for _, pkg := range packages {
		total += len(tags[pkg.Name])
	}
	return total
}

func (cp *computation) validateRepositoryBaselines() error {
	if len(cp.baselineSpecs) == 0 {
		return nil
	}
	tagsByConsumer := make(map[string]map[string]bool, len(cp.tags))
	for consumer, tags := range cp.tags {
		index := make(map[string]bool, len(tags))
		for _, tag := range tags {
			if tag.Parsed && tag.Name != "" {
				index[tag.Name] = true
			}
		}
		tagsByConsumer[strings.ToLower(consumer)] = index
	}
	for _, baseline := range cp.baselineSpecs {
		consumer := strings.ToLower(baseline.Consumer)
		if cp.byName[baseline.Consumer] == nil {
			// Package lookup is case-insensitive in configuration, but byName is
			// canonical. Avoid a per-tuple scan by using the prebuilt tag index.
			if _, exists := tagsByConsumer[consumer]; !exists {
				cp.err(CodeRepositoryBoundary, baseline.Consumer, "", fmt.Sprintf(
					"repository baseline names unknown consumer %q", baseline.Consumer))
				return errFatalPlan
			}
		}
		if !tagsByConsumer[consumer][baseline.ReleaseTag] {
			cp.err(CodeRepositoryBoundary, baseline.Consumer, "", fmt.Sprintf(
				"repository baseline releaseTag %q is not an exact reachable release tag for consumer %q",
				baseline.ReleaseTag, baseline.Consumer))
			return errFatalPlan
		}
	}
	return nil
}

func (cp *computation) relevantRepositories(packageName string) []string {
	return cp.repositoryReach[packageName]
}

// prepareRepositoryReach computes the transitive provider repository set once
// over the already-topologically-sorted graph. A compact bitset visits each
// dependency edge once and retains only the final O(package×relevant-repo)
// output rather than running a full graph search for every package.
func (cp *computation) prepareRepositoryReach() {
	repositories := make([]string, 0, len(cp.histories))
	index := make(map[string]int, len(cp.histories))
	for _, history := range cp.histories {
		repositories = append(repositories, history.Name)
	}
	slices.Sort(repositories)
	for i, repository := range repositories {
		index[strings.ToLower(repository)] = i
	}
	words := (len(repositories) + 63) / 64
	sets := make(map[string][]uint64, len(cp.order))
	interned := make(map[string][]string)
	for _, name := range cp.order {
		bits := make([]uint64, words)
		if p := cp.byName[name]; p != nil {
			i := index[strings.ToLower(p.Repository)]
			bits[i/64] |= uint64(1) << uint(i%64)
		}
		seenProvider := make(map[string]bool)
		for _, provider := range cp.providers[name] {
			if seenProvider[provider] {
				continue
			}
			seenProvider[provider] = true
			if cp.stats != nil {
				cp.stats.ReachabilityEdges.Add(1)
			}
			for i, word := range sets[provider] {
				bits[i] |= word
			}
		}
		sets[name] = bits
		key := repositoryWordsKey(bits)
		if shared, ok := interned[key]; ok {
			cp.repositoryReach[name] = shared
			continue
		}
		var out []string
		for i, repository := range repositories {
			if bits[i/64]&(uint64(1)<<uint(i%64)) != 0 {
				out = append(out, repository)
			}
		}
		interned[key] = out
		cp.repositoryReach[name] = out
	}
}

func (cp *computation) repositoryBoundary(pkg *model.Package, tag gitx.Tag, repository string,
	snapshots map[string]controlSnapshot, ambiguous map[string]bool) (string, error) {
	if tag.Name == "" {
		return "", nil
	}
	if strings.EqualFold(pkg.Repository, repository) {
		return historyKey(repository, tag.Commit), nil
	}
	key := baselineKey{consumer: strings.ToLower(pkg.Name), tag: tag.Name, repository: strings.ToLower(repository)}
	if revision := cp.baselines[key]; revision != "" {
		cp.log.Trace().Str("consumer", pkg.Name).Str("releaseTag", tag.Name).
			Str("repository", repository).Str("revision", rawHistoryKey(revision)).
			Msg("plan: explicit repository baseline resolved")
		return revision, nil
	}
	// What is left is the record layout's question: which revision of that
	// repository this release already carried. Each layout proves it from
	// what it records. The remedy is the same tuple either way, so the
	// diagnostic is raised here rather than in each of them.
	revision, remedy := cp.evidenceFor().resolve(boundaryQuery{
		pkg: pkg, tag: tag, repository: repository, snapshots: snapshots, ambiguous: ambiguous})
	if revision == "" {
		cp.err(CodeRepositoryBoundary, pkg.Name, "", remedy)
		return "", errFatalPlan
	}
	return revision, nil
}

// evidenceFor answers which boundary evidence this computation reads. The
// choice is made once, when the computation is set up; a computation a test
// assembled by hand falls back to the control checkpoints it always read.
func (cp *computation) evidenceFor() boundaryEvidence {
	if cp.evidence == nil {
		cp.evidence = checkpointEvidence{cp: cp}
	}
	return cp.evidence
}

func (cp *computation) controlCheckpoints() (map[string]controlSnapshot, map[string]bool, error) {
	out := make(map[string]controlSnapshot)
	ambiguous := make(map[string]bool)
	if cp.controlRepo == "" {
		return out, ambiguous, nil
	}
	control := cp.histories[strings.ToLower(cp.controlRepo)]
	reader, ok := control.Git.(controlHistoryReader)
	if !ok {
		return out, ambiguous, nil
	}
	history, err := reader.ControlGitlinkHistory(cp.ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("plan: indexing control checkpoints: %w", err)
	}
	cp.controlHistory = history
	cp.controlIndexed = true
	cp.controlStates = make(map[string]*controlGitlinkState, len(history))
	cp.controlPathIndex = make(map[string]int)
	for _, repository := range cp.histories {
		if repository.Path != "" {
			cp.controlPathIndex[repository.Path] = len(cp.controlPathIndex)
		}
	}
	cp.controlPathCount = len(cp.controlPathIndex)
	for i := len(history) - 1; i >= 0; i-- {
		commit := history[i]
		var links *persistentLinkNode
		if len(commit.Parents) > 0 {
			parent := cp.controlStates[commit.Parents[0]]
			if parent == nil {
				return nil, nil, fmt.Errorf("plan: control history %s is missing first parent %s", commit.SHA, commit.Parents[0])
			}
			links = parent.links
		}
		for path, transition := range commit.Gitlinks {
			pathIndex, participates := cp.controlPathIndex[path]
			if !participates {
				continue
			}
			value := transition.To
			if allZeroObjectID(value) {
				value = ""
			} else {
				value = strings.Clone(value)
			}
			links = updatePersistentLink(links, 0, cp.controlPathCount, pathIndex, value)
			if cp.stats != nil {
				cp.stats.PersistentLinkNodes.Add(int64(persistentUpdateNodes(0, cp.controlPathCount, pathIndex)))
			}
		}
		cp.controlStates[strings.Clone(commit.SHA)] = &controlGitlinkState{links: links}
	}
	if cp.stats != nil {
		cp.stats.ControlSnapshotRuns.Add(1)
	}
	cp.log.Debug().Str("repository", control.Name).Int("commits", len(history)).
		Int("sourcePaths", cp.controlPathCount).Msg("plan: control gitlink history indexed")

	type owner struct{ repository, commit, path string }
	tagOwners := make(map[string][]owner)
	controlTags := make(map[string][]string)
	for _, p := range cp.pkgs {
		history := cp.histories[strings.ToLower(p.Repository)]
		for _, tag := range cp.tags[p.Name] {
			if tag.Name != "" && tag.Commit != "" {
				if history.Control {
					controlTags[tag.Commit] = append(controlTags[tag.Commit], checkpointTagKey(p.Repository, tag.Name))
				} else {
					tagOwners[tag.Name] = append(tagOwners[tag.Name], owner{p.Repository, tag.Commit, history.Path})
				}
			}
		}
	}
	snapshotsByCommit := make(map[string]controlSnapshot)
	snapshotFor := func(commit string) controlSnapshot {
		if snapshot, ok := snapshotsByCommit[commit]; ok {
			return snapshot
		}
		snapshot := controlSnapshot{commit: strings.Clone(commit), state: cp.controlStates[commit]}
		snapshotsByCommit[commit] = snapshot
		return snapshot
	}
	assign := func(key string, snapshot controlSnapshot) {
		if previous, exists := out[key]; exists && previous.commit != snapshot.commit {
			ambiguous[key] = true
			delete(out, key)
		} else if !ambiguous[key] {
			out[key] = snapshot
		}
	}
	for _, commit := range history {
		for _, key := range controlTags[commit.SHA] {
			assign(key, snapshotFor(commit.SHA))
		}
		var named []struct {
			tag   string
			owner owner
		}
		for _, tag := range releaseSubjectTags(strings.SplitN(commit.Message, "\n", 2)[0]) {
			for _, candidate := range tagOwners[tag] {
				if candidate.path != "" {
					named = append(named, struct {
						tag   string
						owner owner
					}{tag, candidate})
				}
			}
		}
		if len(named) == 0 {
			continue
		}
		for _, candidate := range named {
			transition, ok := commit.Gitlinks[candidate.owner.path]
			if !ok || transition.From == transition.To || transition.To != candidate.owner.commit {
				continue
			}
			assign(checkpointTagKey(candidate.owner.repository, candidate.tag), snapshotFor(commit.SHA))
		}
	}
	return out, ambiguous, nil
}

func persistentUpdateNodes(low, high, index int) int {
	if high-low <= 1 {
		return 1
	}
	middle := low + (high-low)/2
	if index < middle {
		return 1 + persistentUpdateNodes(low, middle, index)
	}
	return 1 + persistentUpdateNodes(middle, high, index)
}

func allZeroObjectID(value string) bool {
	if value == "" {
		return true
	}
	for _, r := range value {
		if r != '0' {
			return false
		}
	}
	return true
}

func (cp *computation) controlLinkAt(commit, path string) string {
	index, ok := cp.controlPathIndex[path]
	if !ok || cp.controlPathCount == 0 {
		return ""
	}
	state := cp.controlStates[commit]
	if state == nil {
		return ""
	}
	return lookupPersistentLink(state.links, 0, cp.controlPathCount, index)
}

func (cp *computation) controlSnapshotLink(snapshot controlSnapshot, repository string) string {
	history, ok := cp.history(repository)
	if !ok || history.Path == "" || snapshot.state == nil {
		return ""
	}
	index, ok := cp.controlPathIndex[history.Path]
	if !ok {
		return ""
	}
	return lookupPersistentLink(snapshot.state.links, 0, cp.controlPathCount, index)
}

func updatePersistentLink(node *persistentLinkNode, low, high, index int, value string) *persistentLinkNode {
	if high-low <= 1 {
		return &persistentLinkNode{value: value}
	}
	copy := &persistentLinkNode{}
	if node != nil {
		*copy = *node
	}
	middle := low + (high-low)/2
	if index < middle {
		copy.left = updatePersistentLink(copy.left, low, middle, index, value)
	} else {
		copy.right = updatePersistentLink(copy.right, middle, high, index, value)
	}
	return copy
}

func lookupPersistentLink(node *persistentLinkNode, low, high, index int) string {
	if node == nil {
		return ""
	}
	if high-low <= 1 {
		return node.value
	}
	middle := low + (high-low)/2
	if index < middle {
		return lookupPersistentLink(node.left, low, middle, index)
	}
	return lookupPersistentLink(node.right, middle, high, index)
}

// controlObserves reports whether a control directive's causal snapshot
// contains the source revision. A control commit that predates the source
// change cannot resolve precedence merely because it happens to be traversed
// first in the merged stream.
func (cp *computation) controlObserves(controlKey, sourceKey string) bool {
	controlRepository, controlCommit := splitHistoryKey(controlKey)
	sourceRepository, _ := splitHistoryKey(sourceKey)
	if !strings.EqualFold(controlRepository, cp.controlRepo) || sourceRepository == "" ||
		strings.EqualFold(sourceRepository, cp.controlRepo) {
		return false
	}
	history, ok := cp.history(sourceRepository)
	if !ok || history.Path == "" {
		return false
	}
	pin := cp.controlLinkAt(controlCommit, history.Path)
	if pin == "" {
		return false
	}
	return cp.ancestorOrSelf(sourceKey, historyKey(history.Name, pin))
}

// commitPrecedence compares revisions only where repository history proves an
// order. The bool is false for incomparable source DAGs.
func (cp *computation) commitPrecedence(a, b string) (aNewer, comparable bool) {
	if a == b {
		return false, true
	}
	repoA, _ := splitHistoryKey(a)
	repoB, _ := splitHistoryKey(b)
	if strings.EqualFold(repoA, repoB) {
		return cp.newerCommit(a, b), true
	}
	if cp.controlObserves(a, b) {
		return true, true
	}
	if cp.controlObserves(b, a) {
		return false, true
	}
	return false, false
}

func (cp *computation) controlResolves(commit string, candidates []channelPick) bool {
	repository, _ := splitHistoryKey(commit)
	if !strings.EqualFold(repository, cp.controlRepo) {
		return false
	}
	for _, candidate := range candidates {
		repo, _ := splitHistoryKey(candidate.commit)
		if strings.EqualFold(repo, cp.controlRepo) {
			continue
		}
		if !cp.controlObserves(commit, candidate.commit) {
			return false
		}
	}
	return true
}

// validateControlProjectionHeads prevents fleet intent from being applied to
// source code that did not yet contain the control commit's gitlink snapshot.
// A sync:none workspace may deliberately keep control and source at different
// revisions, so the mismatch alone is valid. It becomes unsafe only when an
// actionable control unit actually resolves to a package in that source and
// the control snapshot's pin is ahead of, or incomparable with, the active
// source HEAD.
//
// "Actually resolves" is the same admission test every application pass uses,
// and it has to be, or the guard refuses plans it has no stake in. cp.commits
// is the *union* of every package's window (buildRepositoryUnion), so a
// control commit one package discharged long ago is still in the list while
// another package's window holds it. directBumps, sourcePackages,
// resolveHolds, resolveChannels and both propagation passes all admit a
// (commit, package) pair only while cp.inWindow(package, commit) holds and
// the commit is not already contained in that package's baseline; a pair
// failing either test contributes nothing to this run, so requiring its
// source to carry the pin would block a release the directive cannot touch.
// Propagation targets are admitted against the *target's* window in those
// passes, which is why the same pair of tests covers the walked packages
// controlProjectionPackages adds.
func (cp *computation) validateControlProjectionHeads() error {
	if cp.controlRepo == "" {
		return nil
	}
	invalid := false
	for _, rec := range cp.commits {
		if !strings.EqualFold(rec.repository, cp.controlRepo) {
			continue
		}
		for i, unit := range rec.units {
			if !IsControlUnitAffectingRelease(unit) || i >= len(rec.scope) {
				continue
			}
			checked := make(map[string]bool)
			packageNames := cp.controlProjectionPackages(rec, i)
			for _, packageName := range packageNames {
				pkg := cp.byName[packageName]
				if pkg == nil || pkg.Repository == "" || strings.EqualFold(pkg.Repository, cp.controlRepo) ||
					checked[strings.ToLower(pkg.Repository)] || !cp.inWindow(packageName, rec.key) ||
					cp.containedInBaseline(packageName, rec.key) ||
					cp.cancelledFor(rec.key, packageName) || cp.held[packageName] {
					continue
				}
				checked[strings.ToLower(pkg.Repository)] = true
				history, ok := cp.history(pkg.Repository)
				head := cp.repositoryHeads[history.Name]
				pin := cp.controlLinkAt(rec.commit.SHA, history.Path)
				if !ok || history.Path == "" || head == "" || pin == "" {
					continue
				}
				contains, err := cp.sourceContainsPin(history, pin, head)
				if err != nil {
					return err
				}
				if contains {
					continue
				}
				cp.err(CodeRepositoryBoundary, packageName, rec.key, fmt.Sprintf(
					"control directive at %s targets repository %s at active revision %s, but its control snapshot pins %s; synchronize the source to include that pin or choose an earlier control revision",
					rec.commit.SHA, history.Name, head, pin))
				invalid = true
			}
		}
	}
	if err := cp.ancestryFailed(); err != nil {
		return err
	}
	if invalid {
		return errFatalPlan
	}
	return nil
}

// sourceContainsPin answers the guard's one question: does the active source
// checkout already carry the revision this control snapshot pinned?
//
// The pin is read out of the control tree, so it is exactly the revision a
// source clone can be missing — the scenario the guard exists for often *is*
// a source that never fetched it. Ancestry alone cannot answer that: git
// treats an unknown commit on the left of `merge-base --is-ancestor` as a
// fatal error, cp.ancestorLookup records it in cp.ancErr, and Compute then
// aborts with a git exit status in place of the diagnostic that names the
// repository, the active revision, the pin and how to recover. Probing
// presence first turns "not here" back into the ordinary answer false while
// leaving an unreadable repository fatal, and a Git implementation without
// the capability keeps the plain ancestry path it had before.
func (cp *computation) sourceContainsPin(history RepositoryHistory, pin, head string) (bool, error) {
	key := historyKey(history.Name, pin)
	if probe, ok := history.Git.(gitx.CommitProbex); ok {
		present, cached := cp.pinPresent[key]
		if !cached {
			answer, err := probe.IsCommitPresent(cp.ctx, pin)
			if err != nil {
				return false, fmt.Errorf("plan: repository %s: %w", history.Name, err)
			}
			if cp.pinPresent == nil {
				cp.pinPresent = make(map[string]bool)
			}
			cp.pinPresent[key] = answer
			present = answer
		}
		if !present {
			return false, nil
		}
	}
	return cp.ancestorOrSelf(key, historyKey(history.Name, head)), nil
}

func (cp *computation) controlProjectionPackages(rec *commitRec, i int) []string {
	affected := make(map[string]bool, len(rec.scope[i]))
	for name := range rec.scope[i] {
		affected[name] = true
	}
	if i < len(rec.propagations) {
		propagation := rec.propagations[i]
		if !propagation.inert() {
			for _, target := range cp.walk(rec.scope[i], propagation.Depth, propagation.kinds) {
				if propagation.allowsTarget(target.name) {
					affected[target.name] = true
				}
			}
		}
	}
	if i < len(rec.channelPropagations) {
		channel := rec.channelPropagations[i]
		if !channel.inert() {
			for _, target := range cp.walk(rec.scope[i], channel.Depth, channel.kinds) {
				if channel.allowsTarget(target.name) {
					affected[target.name] = true
				}
			}
		}
	}
	packageNames := make([]string, 0, len(affected))
	for packageName := range affected {
		packageNames = append(packageNames, packageName)
	}
	slices.Sort(packageNames)
	return packageNames
}

// IsControlUnitAffectingRelease reports whether a control unit can change what
// a source repository publishes, which is what makes the projection guard's
// question worth asking about it at all.
//
// The one directive deliberately left out is a bumpless `Release-As: none`.
// It holds the package (§8.6.1): it can only remove a release, never create
// one or move a version, so projecting it onto a source older than the
// control snapshot's pin publishes nothing that source did not already carry.
// Refusing the plan for it would turn the safest possible fleet statement
// into a fatal error. `Release-As: auto` and an exact pin stay in, because
// both do produce a release, at a version the control author chose while
// looking at the pinned source.
//
// A bumpless `Channel:` stays in as well, and that is not symmetry for its
// own sake: resolveChannels pushes a candidate from every non-cancel unit
// whose ChannelSet is true regardless of its bump, and channel(P) decides
// whether a package releasing for some other reason publishes 1.2.0 or
// 1.2.0-beta.1. The directive therefore changes a real release and needs the
// source it was written against.
func IsControlUnitAffectingRelease(unit *ccme.Unit) bool {
	if unit == nil {
		return false
	}
	if unit.Bump != ccme.BumpNone || unit.IsCancel() || unit.Directives.ChannelSet ||
		len(unit.Directives.Edits) > 0 || len(unit.Directives.Deletes) > 0 {
		return true
	}
	return unit.Directives.ReleaseAs != nil && unit.Directives.ReleaseAs.Kind != ccme.ReleaseAsNone
}

// controlCommitsAfter reuses the single bulk control inventory. The excluded
// set is the boundary's native ancestor closure, so branches merged after the
// boundary remain in the window exactly as they do in boundary..HEAD.
func (cp *computation) controlCommitsAfter(boundary string) []gitx.Commit {
	excluded := make(map[string]bool)
	if boundary != "" {
		parents := make(map[string][]string, len(cp.controlHistory))
		for _, commit := range cp.controlHistory {
			parents[commit.SHA] = commit.Parents
		}
		queue := []string{boundary}
		for len(queue) > 0 {
			commit := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			if excluded[commit] {
				continue
			}
			excluded[commit] = true
			queue = append(queue, parents[commit]...)
		}
	}
	out := make([]gitx.Commit, 0, len(cp.controlHistory))
	for _, commit := range cp.controlHistory {
		if excluded[commit.SHA] {
			continue
		}
		out = append(out, gitx.Commit{
			SHA: commit.SHA, Parents: commit.Parents, AuthorName: commit.AuthorName,
			AuthorEmail: commit.AuthorEmail, Message: commit.Message, Files: commit.Files,
		})
	}
	return out
}

func (cp *computation) buildRepositoryUnion(lists [][]string, canonical map[string]historyCommit) {
	idx := make([]int, len(lists))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int { return len(lists[b]) - len(lists[a]) })
	for _, i := range idx {
		for _, key := range lists[i] {
			if _, exists := cp.byKey[key]; exists {
				continue
			}
			item := canonical[key]
			rec := &commitRec{commit: item.commit, key: key, rank: len(cp.commits), repository: item.repository, root: item.root}
			cp.byKey[key] = rec
			cp.commits = append(cp.commits, rec)
			if parents := qualifyParents(item.repository, item.commit.Parents); len(parents) > 0 {
				cp.parents[key] = parents
				cp.linked = true
			}
			if cp.stats != nil {
				cp.stats.UniqueCommits.Add(1)
			}
		}
	}
}

func (cp *computation) history(name string) (RepositoryHistory, bool) {
	if name == "" && len(cp.histories) == 0 {
		return RepositoryHistory{Git: cp.git, Root: cp.root}, true
	}
	h, ok := cp.histories[strings.ToLower(name)]
	return h, ok
}

// resolveApplicableControlBoundaries activates control history only for
// packages an explicit control unit can actually address. A local/tag-only
// source release therefore needs no artificial control tuple, while a tagged
// package affected by fleet intent must prove which control snapshot it had
// already consumed.
func (cp *computation) resolveApplicableControlBoundaries() error {
	if cp.controlRepo == "" {
		return nil
	}
	needed := make(map[string]bool)
	for _, rec := range cp.commits {
		if !strings.EqualFold(rec.repository, cp.controlRepo) {
			continue
		}
		for i, unit := range rec.units {
			for name := range rec.scope[i] {
				needed[name] = true
			}
			if unit.IsCancel() {
				continue
			}
			sources := rec.scope[i]
			propagation := rec.propagations[i]
			if !propagation.inert() {
				for _, target := range cp.walk(sources, propagation.Depth, propagation.kinds) {
					if propagation.allowsTarget(target.name) {
						needed[target.name] = true
					}
				}
			}
			channel := rec.channelPropagations[i]
			if !channel.inert() {
				for _, target := range cp.walk(sources, channel.Depth, channel.kinds) {
					if channel.allowsTarget(target.name) {
						needed[target.name] = true
					}
				}
			}
		}
	}
	for _, name := range cp.order {
		if !needed[name] {
			continue
		}
		cp.controlInputs[name] = true
		pkg := cp.byName[name]
		if pkg == nil || strings.EqualFold(pkg.Repository, cp.controlRepo) {
			continue
		}
		for _, candidate := range []struct {
			tag        gitx.Tag
			boundaries map[string]map[string]string
		}{{cp.stableTags[name], cp.stableBoundaries}, {cp.latestTags[name], cp.publishedBoundaries}} {
			if candidate.tag.Name == "" {
				continue
			}
			boundary, err := cp.repositoryBoundary(pkg, candidate.tag, cp.controlRepo,
				cp.controlSnapshots, cp.controlAmbiguous)
			if err != nil {
				return err
			}
			candidate.boundaries[name][strings.ToLower(cp.controlRepo)] = boundary
		}
	}
	return nil
}

// releaseRepositoryInputs closes the history inputs used by each package
// over dependency propagation and shared-version groups. Dependency closure
// is already available in repositoryReach.
//
// An input flows from a provider to its dependents and between the members of
// a version group in both directions, so dependency and group edges together
// can form a cycle and one pass in dependency order does not reach the fixed
// point. Each group is one node adjacent to its members, never a clique. The
// strongly connected components of that graph are condensed, every member of
// a component has the same closure, and the components are visited providers
// first with one bitset union per edge: O((P+E+V)·ceil(Q/64)), where a walk of
// the graph per repository is O(Q·(P+E+V)) (CCME §13.11).
func (cp *computation) releaseRepositoryInputs() ([]string, map[string][]uint64) {
	if len(cp.histories) == 0 {
		return nil, nil
	}
	repositories := make([]string, 0, len(cp.histories))
	index := make(map[string]int, len(cp.histories))
	for _, history := range cp.histories {
		repositories = append(repositories, history.Name)
	}
	slices.Sort(repositories)
	for i, repository := range repositories {
		index[strings.ToLower(repository)] = i
	}
	wordCount := (len(repositories) + 63) / 64

	// Nodes are the packages in plan order, then the groups as first met.
	node := make(map[string]int, len(cp.order))
	for i, name := range cp.order {
		node[name] = i
	}
	groupNode := make(map[string]int)
	out := make([][]int, len(cp.order)) // the nodes an input flows on to
	initial := make([][]uint64, len(cp.order))
	for i, name := range cp.order {
		bits := make([]uint64, wordCount)
		for _, repository := range cp.repositoryReach[name] {
			if r, ok := index[strings.ToLower(repository)]; ok {
				bits[r/64] |= uint64(1) << uint(r%64)
			}
		}
		if cp.controlInputs[name] {
			if r, ok := index[strings.ToLower(cp.controlRepo)]; ok {
				bits[r/64] |= uint64(1) << uint(r%64)
			}
		}
		initial[i] = bits
		if pkg := cp.byName[name]; pkg != nil {
			if group := pkg.VersionGroupIdentity(); group != "" {
				g, met := groupNode[group]
				if !met {
					g = len(out)
					groupNode[group] = g
					out = append(out, nil)
					initial = append(initial, make([]uint64, wordCount))
				}
				out[i] = append(out[i], g)
				out[g] = append(out[g], i)
			}
		}
		seen := make(map[string]bool)
		for _, provider := range cp.providers[name] {
			if p, known := node[provider]; known && !seen[provider] {
				out[p] = append(out[p], i)
				seen[provider] = true
			}
		}
	}

	component, emitted := stronglyConnected(out)
	closure := make([][]uint64, len(emitted))
	for c, members := range emitted {
		closure[c] = make([]uint64, wordCount)
		for _, n := range members {
			for w, word := range initial[n] {
				closure[c][w] |= word
			}
		}
	}
	// Tarjan emits a component after everything it flows on to, so the reverse
	// order meets every provider before its dependents.
	for c := len(emitted) - 1; c >= 0; c-- {
		for _, n := range emitted[c] {
			for _, to := range out[n] {
				if d := component[to]; d != c {
					for w, word := range closure[c] {
						closure[d][w] |= word
					}
				}
			}
		}
	}

	result := make(map[string][]uint64, len(cp.order))
	interned := make(map[string][]uint64)
	for i, name := range cp.order {
		values := closure[component[i]]
		key := repositoryWordsKey(values)
		if shared, ok := interned[key]; ok {
			values = shared
		} else {
			interned[key] = values
		}
		result[name] = values
	}
	return repositories, result
}

// stronglyConnected is Tarjan's algorithm, iterative so that a long dependency
// chain costs heap and not stack. It returns each node's component and the
// components in the order they complete, which is reverse topological: a
// component is emitted only after every component it has an edge into.
func stronglyConnected(out [][]int) (component []int, emitted [][]int) {
	const unvisited = -1
	n := len(out)
	component = make([]int, n)
	indexOf, low := make([]int, n), make([]int, n)
	onStack := make([]bool, n)
	for i := range indexOf {
		indexOf[i], component[i] = unvisited, unvisited
	}
	type frame struct{ node, edge int }
	var stack []int
	next := 0
	for root := 0; root < n; root++ {
		if indexOf[root] != unvisited {
			continue
		}
		work := []frame{{node: root}}
		indexOf[root], low[root] = next, next
		next++
		stack, onStack[root] = append(stack, root), true
		for len(work) > 0 {
			f := &work[len(work)-1]
			if f.edge < len(out[f.node]) {
				to := out[f.node][f.edge]
				f.edge++
				switch {
				case indexOf[to] == unvisited:
					indexOf[to], low[to] = next, next
					next++
					stack, onStack[to] = append(stack, to), true
					work = append(work, frame{node: to})
				case onStack[to]:
					low[f.node] = min(low[f.node], indexOf[to])
				}
				continue
			}
			done := f.node
			work = work[:len(work)-1]
			if len(work) > 0 {
				parent := work[len(work)-1].node
				low[parent] = min(low[parent], low[done])
			}
			if low[done] != indexOf[done] {
				continue
			}
			var members []int
			for {
				top := stack[len(stack)-1]
				stack, onStack[top] = stack[:len(stack)-1], false
				component[top] = len(emitted)
				members = append(members, top)
				if top == done {
					break
				}
			}
			emitted = append(emitted, members)
		}
	}
	return component, emitted
}

func repositoryWordsKey(words []uint64) string {
	key := make([]byte, len(words)*8)
	for i, word := range words {
		binary.LittleEndian.PutUint64(key[i*8:], word)
	}
	return string(key)
}

func (cp *computation) gitForKey(key string) (gitx.Gitx, string, bool) {
	repository, raw := splitHistoryKey(key)
	h, ok := cp.history(repository)
	if !ok || h.Git == nil {
		return nil, raw, false
	}
	return h.Git, raw, true
}
