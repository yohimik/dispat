package plan

import (
	"context"
	"encoding/binary"
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
	Git              gitx.Git
	ParserConfig     ccme.Config
	NonPackageScopes []string
	Control          bool
}

// RepositoryBaseline is an explicit cross-repository release boundary. Its
// Revision is already a full commit OID resolved and reachability-checked by
// workspace composition.
type RepositoryBaseline struct {
	Consumer, ReleaseTag, Repository, Revision string
}

// HistoryStats exposes operation counts for planner scale tests and
// benchmarks. Counters are safe under the bounded concurrent tag reads.
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
func (cp *computation) loadRepositoryTagsAndWindows() error {
	packagesByRepo := make(map[string][]*model.Package)
	for _, p := range cp.pkgs {
		packagesByRepo[strings.ToLower(p.Repository)] = append(packagesByRepo[strings.ToLower(p.Repository)], p)
	}

	aliases := make(map[string]AliasFilter, len(packagesByRepo))
	for repository, packages := range packagesByRepo {
		aliases[repository] = NewAliasFilter(packages)
	}

	// A real CLI inventories one repository's refs once. Lightweight Git
	// implementations retain the bounded per-package fallback.
	for repository, packages := range packagesByRepo {
		history, ok := cp.histories[repository]
		if !ok {
			return fmt.Errorf("plan: repository history %q is missing", repository)
		}
		formats := make(map[string]gitx.TagFormat, len(packages))
		for _, p := range packages {
			if (&Release{Pkg: p}).Releasable() {
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
				cp.tags[p.Name] = cp.withoutIgnoredTags(history.Name, aliases[repository].Without(all[p.Name], p.Name, cp.log))
			}
			cp.log.Debug().Str("repository", history.Name).Int("packages", len(packages)).
				Int("tags", repositoryTagCount(cp.tags, packages)).Msg("plan: repository tag inventory loaded")
			continue
		}
		for _, p := range packages {
			if !(&Release{Pkg: p}).Releasable() {
				continue
			}
			tags, err := history.Git.Tags(cp.ctx, p.Name, (&Release{Pkg: p}).TagFormat())
			if err != nil {
				return fmt.Errorf("plan: %s: %w", p.Name, err)
			}
			if cp.stats != nil {
				cp.stats.TagInventories.Add(1)
			}
			cp.tags[p.Name] = cp.withoutIgnoredTags(history.Name, aliases[repository].Without(tags, p.Name, cp.log))
		}
		cp.log.Debug().Str("repository", history.Name).Int("packages", len(packages)).
			Int("tags", repositoryTagCount(cp.tags, packages)).Msg("plan: repository tag inventory loaded")
	}

	// Resolve the public package baselines first. Commit fields stay raw for
	// records and integrations; qualified keys remain internal.
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
		} else if init, ok := cp.initials[p.Name]; ok && rel.Releasable() {
			rel.Current, rel.FromInitials = init, true
		}
		rel.Next = rel.Current
		if rel.HasBaseline {
			rel.Next = rel.Baseline
		}
		cp.rel[p.Name] = rel
	}
	if err := cp.validateRepositoryBaselines(); err != nil {
		return err
	}

	snapshots, ambiguous, err := cp.controlCheckpoints()
	if err != nil {
		return err
	}
	cp.stableTags, cp.latestTags = stableTags, latestTags
	cp.controlSnapshots, cp.controlAmbiguous = snapshots, ambiguous
	commitLists := make(map[string][]string)
	windowSets := make(map[string]map[string]bool)
	canonical := make(map[string]historyCommit)
	internedKeys := make(map[string]string)
	var lists [][]string
	loadWindow := func(history RepositoryHistory, boundary string, pkg string) (map[string]bool, error) {
		_, rawBoundary := splitHistoryKey(boundary)
		cacheKey := historyKey(history.Name, rawBoundary)
		keys, ok := commitLists[cacheKey]
		if !ok {
			var raw []gitx.Commit
			var err error
			if history.Control && cp.controlIndexed {
				raw = cp.controlCommitsAfter(rawBoundary)
			} else {
				raw, err = history.Git.Commits(cp.ctx, rawBoundary)
				if err != nil {
					return nil, fmt.Errorf("plan: %s history for %s: %w", history.Name, pkg, err)
				}
			}
			if cp.stats != nil && pkg != "control intent" {
				cp.stats.CommitWindows.Add(1)
			}
			keys = make([]string, 0, len(raw))
			for _, commit := range raw {
				item := historyCommit{repository: history.Name, root: history.Root, commit: commit}
				key := commitHistoryKey(item)
				if interned, exists := internedKeys[key]; exists {
					key = interned
				} else {
					internedKeys[key] = key
				}
				keys = append(keys, key)
				if _, exists := canonical[key]; !exists {
					cloned := cloneHistoryCommit(history.Name, history.Root, commit)
					canonical[key] = cloned
					if cp.stats != nil {
						cp.stats.CanonicalBytes.Add(int64(historyCommitBytes(cloned)))
					}
				}
			}
			commitLists[cacheKey] = keys
			lists = append(lists, keys)
			if cp.stats != nil && pkg != "control intent" {
				cp.stats.WindowCommitRefs.Add(int64(len(keys)))
			}
			cp.log.Debug().Str("repository", history.Name).Str("boundary", rawBoundary).
				Int("commits", len(keys)).Msg("plan: repository history window indexed")
		}
		set, ok := windowSets[cacheKey]
		if !ok {
			set = make(map[string]bool, len(keys))
			for _, key := range keys {
				set[key] = true
			}
			windowSets[cacheKey] = set
		}
		return set, nil
	}
	for _, p := range cp.pkgs {
		repositories := cp.relevantRepositories(p.Name)
		stable := stableTags[p.Name]
		latest := latestTags[p.Name]
		cp.stableBoundaries[p.Name] = make(map[string]string)
		cp.publishedBoundaries[p.Name] = make(map[string]string)
		for _, repository := range repositories {
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

			history := cp.histories[strings.ToLower(repository)]
			stableWindow, err := loadWindow(history, boundary, p.Name)
			if err != nil {
				return err
			}
			cp.windowRefs[p.Name] = append(cp.windowRefs[p.Name], stableWindow)

			// Ordinarily the latest prerelease boundary descends from the stable
			// boundary, making its window a subset. Explicit tuples may describe
			// a consumer whose prerelease shipped an older provider revision than
			// its stable tag. Retain that fresh window as a second shared view so
			// the intervening provider work is visible as catch-up work.
			if published != boundary {
				freshWindow, err := loadWindow(history, published, p.Name)
				if err != nil {
					return err
				}
				cp.windowRefs[p.Name] = append(cp.windowRefs[p.Name], freshWindow)
			}
		}
	}
	if cp.controlIndexed {
		control := cp.histories[strings.ToLower(cp.controlRepo)]
		if _, err := loadWindow(control, "", "control intent"); err != nil {
			return err
		}
	}
	cp.buildRepositoryUnion(lists, canonical)
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
	checkpointKey := checkpointTagKey(pkg.Repository, tag.Name)
	if ambiguous[checkpointKey] {
		cp.err(CodeRepositoryBoundary, pkg.Name, "", fmt.Sprintf("release %s has ambiguous control checkpoint association; add repositoryBaselines for repository %s", tag.Name, repository))
		return "", errFatalPlan
	}
	snapshot, ok := snapshots[checkpointKey]
	if !ok {
		cp.err(CodeRepositoryBoundary, pkg.Name, "", fmt.Sprintf("release %s has no verifiable control checkpoint association; add repositoryBaselines for repository %s", tag.Name, repository))
		return "", errFatalPlan
	}
	if strings.EqualFold(repository, cp.controlRepo) {
		cp.log.Trace().Str("consumer", pkg.Name).Str("releaseTag", tag.Name).
			Str("repository", repository).Str("revision", snapshot.commit).
			Msg("plan: control checkpoint boundary resolved")
		return historyKey(repository, snapshot.commit), nil
	}
	revision := cp.controlSnapshotLink(snapshot, repository)
	if revision == "" {
		cp.err(CodeRepositoryBoundary, pkg.Name, "", fmt.Sprintf("release %s control checkpoint has no gitlink for repository %s; add repositoryBaselines", tag.Name, repository))
		return "", errFatalPlan
	}
	cp.log.Trace().Str("consumer", pkg.Name).Str("releaseTag", tag.Name).
		Str("repository", repository).Str("revision", revision).
		Msg("plan: control checkpoint source boundary resolved")
	return historyKey(repository, revision), nil
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
		subject := strings.SplitN(commit.Message, "\n", 2)[0]
		const prefix = "chore(release): "
		if !strings.HasPrefix(subject, prefix) {
			continue
		}
		var named []struct {
			tag   string
			owner owner
		}
		for _, token := range strings.Split(strings.TrimPrefix(subject, prefix), ",") {
			tag := strings.TrimSpace(token)
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
// is already available in repositoryReach. Each repository then traverses
// the package graph once, which avoids a separate graph search per release.
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
	sets := make(map[string][]uint64, len(cp.order))
	groups := make(map[string][]string)
	groupSets := make(map[string][]uint64)
	dependents := make(map[string][]string, len(cp.order))
	for _, name := range cp.order {
		bits := make([]uint64, wordCount)
		for _, repository := range cp.repositoryReach[name] {
			if i, ok := index[strings.ToLower(repository)]; ok {
				bits[i/64] |= uint64(1) << uint(i%64)
			}
		}
		if cp.controlInputs[name] {
			if i, ok := index[strings.ToLower(cp.controlRepo)]; ok {
				bits[i/64] |= uint64(1) << uint(i%64)
			}
		}
		sets[name] = bits
		if pkg := cp.byName[name]; pkg != nil {
			if group := pkg.VersionGroupIdentity(); group != "" {
				groups[group] = append(groups[group], name)
				if groupSets[group] == nil {
					groupSets[group] = make([]uint64, wordCount)
				}
				for i, word := range bits {
					groupSets[group][i] |= word
				}
			}
		}
		seen := make(map[string]bool)
		for _, provider := range cp.providers[name] {
			if !seen[provider] {
				dependents[provider] = append(dependents[provider], name)
				seen[provider] = true
			}
		}
	}

	for group, members := range groups {
		for _, name := range members {
			for i, word := range groupSets[group] {
				sets[name][i] |= word
			}
		}
	}

	// Dependency and group edges can alternate (a group member can introduce
	// a provider input that changes a downstream group). Propagating one
	// repository bit at a time reaches the exact fixed point in
	// O(Q*(P+E+V)), where Q is repositories and V is shared-group membership,
	// without repeatedly scanning whole bitsets as individual inputs arrive.
	for repositoryIndex := range repositories {
		word := repositoryIndex / 64
		mask := uint64(1) << uint(repositoryIndex%64)
		queue := make([]string, 0, len(cp.order))
		seen := make(map[string]bool, len(cp.order))
		seenGroups := make(map[string]bool, len(groups))
		for _, name := range cp.order {
			if sets[name][word]&mask != 0 {
				seen[name] = true
				queue = append(queue, name)
			}
		}
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			sets[name][word] |= mask
			for _, dependent := range dependents[name] {
				if !seen[dependent] {
					seen[dependent] = true
					queue = append(queue, dependent)
				}
			}
			pkg := cp.byName[name]
			if pkg == nil || pkg.VersionGroupIdentity() == "" {
				continue
			}
			group := pkg.VersionGroupIdentity()
			if seenGroups[group] {
				continue
			}
			seenGroups[group] = true
			for _, member := range groups[group] {
				if !seen[member] {
					seen[member] = true
					queue = append(queue, member)
				}
			}
		}
	}

	result := make(map[string][]uint64, len(cp.order))
	interned := make(map[string][]uint64)
	for _, name := range cp.order {
		values := sets[name]
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

func repositoryWordsKey(words []uint64) string {
	key := make([]byte, len(words)*8)
	for i, word := range words {
		binary.LittleEndian.PutUint64(key[i*8:], word)
	}
	return string(key)
}

func (cp *computation) gitForKey(key string) (gitx.Git, string, bool) {
	repository, raw := splitHistoryKey(key)
	h, ok := cp.history(repository)
	if !ok || h.Git == nil {
		return nil, raw, false
	}
	return h.Git, raw, true
}
