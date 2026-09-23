// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

// Cross-repository boundary evidence, and the two ways a fleet proves it.
//
// A consumer that released at tag T carried some revision of every other
// repository it depends on, and the plan has to know which one: the window it
// measures from is exactly "what has happened in that repository since". A
// fleet with a control repository reads that from the control checkpoint the
// release wrote. A choreographed fleet has no such commit, so it reads the
// same fact out of the links themselves: the release commit the tag sits on
// carries the pins of this repository's neighbours, that neighbour's tree
// carries the pins of its own, and following the one route between two
// repositories arrives at the revision the consumer had.
//
// Both answers are proofs rather than guesses. Neither accepts a matching
// object id on its own: the checkpoint must be the commit that moved the
// consumer's gitlink to that tag, and the link chain must start at a release
// commit that names the tag, so a pointer that happens to match says nothing.
// Where the proof is missing the plan stops with E333 and the operator writes
// the `repositoryBaselines` tuple, which supplies explicit evidence for either record layout.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// gitlinkPathReader reads the recorded pins of named paths in one tree. It is
// a capability rather than part of Gitx so an in-memory history keeps working:
// a fake without it simply offers no link evidence.
type gitlinkPathReader interface {
	GitlinksAtPaths(ctx context.Context, revision string, paths []string) (map[string]string, error)
}

// commitSubjectReader reads the subject line of several revisions at once.
type commitSubjectReader interface {
	CommitSubjects(ctx context.Context, revisions []string) (map[string]string, error)
}

// releaseSubjectTags returns the release tags an ordinary release commit's
// subject names, or nil when the subject is not one of dispat's own release
// commits. It is the one place that literal prefix is read, so the control
// checkpoint and the link chain recognise exactly the same commits.
func releaseSubjectTags(subject string) []string {
	const prefix = "chore(release): "
	if !strings.HasPrefix(subject, prefix) {
		return nil
	}
	tokens := strings.Split(strings.TrimPrefix(subject, prefix), ",")
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if tag := strings.TrimSpace(token); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

// boundaryQuery is one question: which revision of repository had the release
// pkg made under tag already incorporated?
type boundaryQuery struct {
	pkg        *model.Package
	tag        gitx.Tag
	repository string
	// snapshots and ambiguous are the control checkpoint index. They are the
	// central checkpoint evidence and are read by nothing else.
	snapshots map[string]controlSnapshot
	ambiguous map[string]bool
}

// boundaryEvidence is how a record layout answers that question. There are two, and
// which one a computation uses is decided once, when it is set up.
type boundaryEvidence interface {
	// index prepares the evidence for this record layout, once per plan.
	index() error
	// resolve answers a qualified revision, or an empty one together with the
	// text the caller reports as E333.
	resolve(q boundaryQuery) (revision string, remedy string)
	// drop releases the indexes, with the planner's other ones.
	drop()
}

// checkpointEvidence reads the control repository's release checkpoints. It is
// the central checkpoint evidence and behaves exactly as it always has.
type checkpointEvidence struct{ cp *computation }

func (e checkpointEvidence) index() error {
	snapshots, ambiguous, err := e.cp.controlCheckpoints()
	if err != nil {
		return err
	}
	e.cp.controlSnapshots, e.cp.controlAmbiguous = snapshots, ambiguous
	return nil
}

func (e checkpointEvidence) resolve(q boundaryQuery) (string, string) {
	cp := e.cp
	checkpointKey := checkpointTagKey(q.pkg.Repository, q.tag.Name)
	if q.ambiguous[checkpointKey] {
		return "", fmt.Sprintf("release %s has ambiguous control checkpoint association; add repositoryBaselines for repository %s",
			q.tag.Name, q.repository)
	}
	snapshot, ok := q.snapshots[checkpointKey]
	if !ok {
		return "", fmt.Sprintf("release %s has no verifiable control checkpoint association; add repositoryBaselines for repository %s",
			q.tag.Name, q.repository)
	}
	if strings.EqualFold(q.repository, cp.controlRepo) {
		cp.log.Trace().Str("consumer", q.pkg.Name).Str("releaseTag", q.tag.Name).
			Str("repository", q.repository).Str("revision", snapshot.commit).
			Msg("plan: control checkpoint boundary resolved")
		return historyKey(q.repository, snapshot.commit), ""
	}
	revision := cp.controlSnapshotLink(snapshot, q.repository)
	if revision == "" {
		return "", fmt.Sprintf("release %s control checkpoint has no gitlink for repository %s; add repositoryBaselines",
			q.tag.Name, q.repository)
	}
	cp.log.Trace().Str("consumer", q.pkg.Name).Str("releaseTag", q.tag.Name).
		Str("repository", q.repository).Str("revision", revision).
		Msg("plan: control checkpoint source boundary resolved")
	return historyKey(q.repository, revision), ""
}

func (e checkpointEvidence) drop() {}

// linkEvidence reads the fleet links themselves, which is the only record a
// choreographed fleet keeps of what a release incorporated.
type linkEvidence struct {
	cp *computation
	// subjects is the subject line of every release-tag commit, read one
	// repository at a time before any boundary is asked for.
	subjects map[string]string
	// trees caches the pins one repository's tree records, keyed by
	// repository and revision. A fleet's consumers ask about the same few
	// revisions, and each answer is one Git process.
	trees map[string]map[string]string
	// routes caches the hops between two repositories, which the link graph
	// fixes for the whole plan.
	routes map[string][]string
}

func newLinkEvidence(cp *computation) *linkEvidence {
	return &linkEvidence{cp: cp, subjects: map[string]string{},
		trees: map[string]map[string]string{}, routes: map[string][]string{}}
}

// index reads the subject of every release tag's own commit, one call per
// repository. The first thing every boundary asks is whether the tag sits on
// an ordinary release commit, and asking that per consumer per provider would
// be one Git process each.
func (e *linkEvidence) index() error {
	cp := e.cp
	byRepository := map[string]map[string]bool{}
	for _, p := range cp.pkgs {
		if p.Repository == "" {
			continue
		}
		for _, tag := range cp.tags[p.Name] {
			if tag.Name == "" || tag.Commit == "" {
				continue
			}
			key := strings.ToLower(p.Repository)
			if byRepository[key] == nil {
				byRepository[key] = map[string]bool{}
			}
			byRepository[key][tag.Commit] = true
		}
	}
	for _, key := range sortedNames(byRepository) {
		history, ok := cp.histories[key]
		if !ok {
			continue
		}
		reader, ok := history.Git.(commitSubjectReader)
		if !ok {
			return fmt.Errorf("plan: repository %s cannot read release commit subjects", history.Name)
		}
		revisions := make([]string, 0, len(byRepository[key]))
		for revision := range byRepository[key] {
			revisions = append(revisions, revision)
		}
		sort.Strings(revisions)
		subjects, err := reader.CommitSubjects(cp.ctx, revisions)
		if err != nil {
			return fmt.Errorf("plan: reading repository %s release subjects: %w", history.Name, err)
		}
		if cp.stats != nil {
			cp.stats.LinkReads.Add(1)
		}
		for revision, subject := range subjects {
			e.subjects[historyKey(history.Name, revision)] = strings.Clone(subject)
		}
	}
	cp.log.Debug().Int("repositories", len(byRepository)).Int("revisions", len(e.subjects)).
		Msg("plan: fleet release subjects indexed")
	return nil
}

func (e *linkEvidence) resolve(q boundaryQuery) (string, string) {
	cp := e.cp
	// E1. The tag has to sit on an ordinary release commit that names it.
	// Without that the commit is some other commit the tag was attached to
	// later, and the pins in its tree are not the ones the release shipped.
	subject := e.subjects[historyKey(q.pkg.Repository, q.tag.Commit)]
	if !releaseSubjectNames(subject, q.tag.Name) {
		return "", fmt.Sprintf(
			"release %s is not recorded by a release commit in repository %s, so its fleet links prove nothing; add repositoryBaselines for repository %s",
			q.tag.Name, q.pkg.Repository, q.repository)
	}
	// E2. Follow the one route between the two repositories, reading each
	// hop's pin out of the previous hop's tree.
	revision, stopped := e.project(q.pkg.Repository, q.tag.Commit, q.repository)
	if stopped != "" {
		return "", fmt.Sprintf("release %s: %s; add repositoryBaselines for repository %s",
			q.tag.Name, stopped, q.repository)
	}
	// E3. The revision has to be one this repository actually holds, and one
	// the planned head descends from. Presence is asked first: a revision the
	// checkout never fetched is an answer, not a Git failure.
	history, ok := cp.history(q.repository)
	if !ok {
		return "", fmt.Sprintf("release %s names repository %s, which this run did not compose", q.tag.Name, q.repository)
	}
	head := cp.repositoryHeads[history.Name]
	if head != "" {
		contains, err := cp.sourceContainsPin(history, revision, head)
		if err != nil || !contains {
			return "", fmt.Sprintf(
				"release %s pins repository %s at %s, which is not reachable from its active revision %s; add repositoryBaselines for repository %s",
				q.tag.Name, history.Name, revision, head, q.repository)
		}
	}
	cp.log.Trace().Str("consumer", q.pkg.Name).Str("releaseTag", q.tag.Name).
		Str("repository", history.Name).Str("revision", revision).
		Msg("plan: fleet link boundary resolved")
	return historyKey(history.Name, revision), ""
}

func (e *linkEvidence) drop() {
	e.subjects = nil
	e.trees = nil
	e.routes = nil
}

// releaseSubjectNames reports whether a commit subject is a release commit
// recording exactly this tag.
func releaseSubjectNames(subject, tag string) bool {
	for _, named := range releaseSubjectTags(subject) {
		if named == tag {
			return true
		}
	}
	return false
}

// project follows the fleet route from one repository's revision to the
// revision of another repository that revision's tree pins, hop by hop. The
// second answer is empty on success and otherwise says where the walk stopped.
func (e *linkEvidence) project(from, revision, to string) (string, string) {
	route := e.route(from, to)
	if len(route) == 0 {
		return "", fmt.Sprintf("no chain of fleet links joins repositories %s and %s", from, to)
	}
	for i := 0; i+1 < len(route); i++ {
		hop, next := route[i], route[i+1]
		history, ok := e.cp.history(hop)
		if !ok {
			return "", fmt.Sprintf("repository %s is not part of this run", hop)
		}
		path := linkPathTo(history, next)
		if path == "" {
			return "", fmt.Sprintf("repository %s does not link %s", hop, next)
		}
		pins, err := e.pins(history, revision)
		if err != nil {
			return "", fmt.Sprintf("repository %s revision %s cannot be read (%v)", hop, revision, err)
		}
		pinned := pins[path]
		if pinned == "" {
			return "", fmt.Sprintf("repository %s revision %s records no link to %s at %s", hop, revision, next, path)
		}
		revision = pinned
	}
	return revision, ""
}

// pins reads every fleet link one revision of a repository records, and
// remembers it: the consumers of a hub all ask about the same few revisions,
// and each answer costs a Git process.
func (e *linkEvidence) pins(history RepositoryHistory, revision string) (map[string]string, error) {
	key := strings.ToLower(history.Name) + historyKeySeparator + revision
	if pins, ok := e.trees[key]; ok {
		return pins, nil
	}
	reader, ok := history.Git.(gitlinkPathReader)
	if !ok {
		return nil, fmt.Errorf("plan: repository %s cannot read fleet links at %s", history.Name, revision)
	}
	paths := linkPathsOf(history)
	pins, err := reader.GitlinksAtPaths(e.cp.ctx, revision, paths)
	if err != nil {
		return nil, err
	}
	if e.cp.stats != nil {
		e.cp.stats.LinkReads.Add(1)
	}
	retained := make(map[string]string, len(pins))
	for path, pin := range pins {
		// The pins outlive the command output they were parsed from.
		retained[strings.Clone(path)] = strings.Clone(pin)
	}
	e.trees[key] = retained
	e.cp.log.Trace().Str("repository", history.Name).Str("revision", revision).
		Int("links", len(retained)).Msg("plan: fleet links read")
	return retained, nil
}

// route answers the repositories a reader passes through to get from one to
// another, both ends included. The links form a tree, so the route is unique
// and is remembered for the rest of the plan.
func (e *linkEvidence) route(from, to string) []string {
	key := strings.ToLower(from) + historyKeySeparator + strings.ToLower(to)
	if route, ok := e.routes[key]; ok {
		return route
	}
	route := e.cp.linkRoute(from, to)
	e.routes[key] = route
	return route
}

// linkRoute is the breadth-first walk behind route, over the link graph the
// composed histories carry. A link declared by only one of its two ends still
// joins them here; composition reports the missing half as W332.
func (cp *computation) linkRoute(from, to string) []string {
	start, startOK := cp.history(from)
	end, endOK := cp.history(to)
	if !startOK || !endOK {
		return nil
	}
	if strings.EqualFold(start.Name, end.Name) {
		return []string{start.Name}
	}
	previous := map[string]string{strings.ToLower(start.Name): ""}
	for queue := []string{start.Name}; len(queue) > 0; queue = queue[1:] {
		for _, peer := range cp.linkNeighbours(queue[0]) {
			if _, seen := previous[strings.ToLower(peer)]; seen {
				continue
			}
			previous[strings.ToLower(peer)] = queue[0]
			if strings.EqualFold(peer, end.Name) {
				route := []string{peer}
				for current := peer; !strings.EqualFold(current, start.Name); {
					current = previous[strings.ToLower(current)]
					if current == "" {
						return nil
					}
					route = append(route, current)
				}
				for i, j := 0, len(route)-1; i < j; i, j = i+1, j-1 {
					route[i], route[j] = route[j], route[i]
				}
				return route
			}
			queue = append(queue, peer)
		}
	}
	return nil
}

// linkNeighbours answers both ends of every link touching one repository, in
// one stable order.
func (cp *computation) linkNeighbours(name string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(peer string) {
		if peer == "" || seen[strings.ToLower(peer)] {
			return
		}
		seen[strings.ToLower(peer)] = true
		out = append(out, peer)
	}
	for _, key := range sortedNames(cp.histories) {
		history := cp.histories[key]
		if strings.EqualFold(history.Name, name) {
			for _, peer := range sortedNames(history.Links) {
				add(peer)
			}
			continue
		}
		for peer := range history.Links {
			if strings.EqualFold(peer, name) {
				add(history.Name)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// sortedNames lists a map's keys in one stable order, whatever it maps to.
// sortedKeys answers the same question for the sets this package is mostly
// made of; the link indexes are maps of other things.
func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// linkPathTo is where one repository holds its link to a peer, whichever case
// either side spelled the identity in.
func linkPathTo(history RepositoryHistory, peer string) string {
	if path, ok := history.Links[peer]; ok {
		return path
	}
	for name, path := range history.Links {
		if strings.EqualFold(name, peer) {
			return path
		}
	}
	return ""
}

// linkPathsOf lists a repository's link paths in one stable order, which is
// what a tree read asks for.
func linkPathsOf(history RepositoryHistory) []string {
	paths := make([]string, 0, len(history.Links))
	for _, peer := range sortedNames(history.Links) {
		paths = append(paths, history.Links[peer])
	}
	sort.Strings(paths)
	return paths
}

// precedenceRemedy names what resolves two incomparable revisions, which is
// different for checkpoint records and linked records.
//
// An orchestrated fleet has a repository above the others, and a directive
// written there is evaluated against its own gitlinks, so it can order what
// the sources cannot. A choreographed fleet has no such repository: no unit
// reaches another repository's packages, so the only resolutions are to
// withdraw the conflicting intent or to restate it where those packages live.
func (cp *computation) precedenceRemedy() string {
	if cp.linkedFleet {
		return "; no unit reaches another repository's packages in a choreographed fleet, " +
			"so withdraw the conflicting intent or restate it in the repositories that carry those packages"
	}
	return "; add a causally applicable control directive"
}

// entryHistory is the repository a run is anchored in: the control repository
// of an orchestrated fleet, and the one repository no link reached of a
// choreographed one.
func (cp *computation) entryHistory() (RepositoryHistory, bool) {
	if cp.controlRepo != "" {
		return cp.history(cp.controlRepo)
	}
	var entry RepositoryHistory
	found := 0
	for _, key := range sortedNames(cp.histories) {
		if cp.histories[key].Linker == "" {
			entry = cp.histories[key]
			found++
		}
	}
	return entry, found == 1
}

// projectSince answers, for each repository of a composed workspace, the
// revision `--since <entry-revision>` means there.
//
// The entry repository's answer is the revision itself. Every other
// repository's is what that revision pinned: a fleet-wide selection is one
// range per repository, never the entry's history scanned once per consumer.
// An orchestrated workspace reads all of them out of the control tree in one
// call; a choreographed one follows the same routes its boundary evidence
// does. A repository the revision pins nothing for projects to the empty
// revision, exactly as an absent gitlink path always has.
func (cp *computation) projectSince(rev string, linked bool) (func(RepositoryHistory) string, error) {
	if linked {
		entry, ok := cp.entryHistory()
		if !ok {
			return nil, fmt.Errorf("plan: composed fleet has no entry repository to project %q from", rev)
		}
		evidence := newLinkEvidence(cp)
		return func(history RepositoryHistory) string {
			if strings.EqualFold(history.Name, entry.Name) {
				return rev
			}
			revision, stopped := evidence.project(entry.Name, rev, history.Name)
			if stopped != "" {
				cp.log.Debug().Str("repository", history.Name).Str("revision", rev).
					Str("reason", stopped).Msg("plan: fleet revision projects to no boundary")
				return ""
			}
			return revision
		}, nil
	}
	control, ok := cp.histories[strings.ToLower(cp.controlRepo)]
	if !ok {
		return nil, fmt.Errorf("plan: composed history has no control repository")
	}
	reader, ok := control.Git.(gitlinkSnapshotReader)
	if !ok {
		return nil, fmt.Errorf("plan: control repository cannot project gitlinks at %q", rev)
	}
	links, err := reader.GitlinksAt(cp.ctx, rev)
	if err != nil {
		return nil, fmt.Errorf("plan: resolving control gitlinks at %q: %w", rev, err)
	}
	return func(history RepositoryHistory) string {
		if history.Control {
			return rev
		}
		return links[history.Path]
	}, nil
}

// isLinkPath reports whether a changed file is where a repository holds one of
// its fleet links. A link move is a pointer the settlement wrote, never a
// change to a package, and §27 says a fleet link move is not a second change.
func (cp *computation) isLinkPath(repository, file string) bool {
	if len(cp.linkPaths) == 0 || repository == "" {
		return false
	}
	paths, ok := cp.linkPaths[strings.ToLower(repository)]
	if !ok {
		return false
	}
	return paths[strings.TrimSuffix(file, "/")]
}

// prepareLinkPaths indexes every repository's link paths once, for the changed
// files of every commit in every window to be checked against.
func (cp *computation) prepareLinkPaths() {
	for _, key := range sortedNames(cp.histories) {
		history := cp.histories[key]
		if len(history.Links) == 0 {
			continue
		}
		paths := make(map[string]bool, len(history.Links))
		for _, path := range linkPathsOf(history) {
			paths[path] = true
		}
		if cp.linkPaths == nil {
			cp.linkPaths = make(map[string]map[string]bool, len(cp.histories))
		}
		cp.linkPaths[key] = paths
	}
}
