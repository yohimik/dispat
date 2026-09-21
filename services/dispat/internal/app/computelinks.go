// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// The fleet half of compute: turning a roster into the links that join it.
//
// A choreographed fleet is declared twice over. Every peer names the others in
// its `repositories` roster, which is the membership list, and the peers are
// actually joined by submodule links, which are what a run walks. compute is
// what turns the first into the second: it proposes the minimum set of links
// that connects everything the roster names, the half of a link only one of
// its two repositories declares, the checkouts a declared link is missing, and
// the roster entries a peer has not heard about.
//
// The link set is a spanning tree over a union-find seeded with the links that
// already exist. That gives three properties worth having: exactly one link
// per group beyond the first, never a second path between two repositories
// (which composition would refuse as E338), and the same answer whatever order
// the fleet was assembled in. Which tree it is is chosen in computecentres.go,
// by joining the groups at their centres, and Kruskal over the fleet's pairs
// in folded-identity order is what is left for a group no centre of which can
// hold a link.
//
// Nothing here commits and nothing here deletes. A link is created and staged;
// the operator reads the diff and commits it. A link the fleet no longer needs
// stays where it is and is reported by composition instead, because removing a
// link is removing history's only record of what a release incorporated.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// The kinds of fleet change compute proposes.
const (
	linkChangeLink       = "link"
	linkChangeInit       = "init"
	linkChangeRepository = "repository"
)

// linkSuggestion is one proposed fleet change: a link to create, a link
// checkout to materialize, or a roster entry to add.
type linkSuggestion struct {
	kind string
	// repository is the peer whose configuration or checkout changes.
	repository string
	// peer is the other end: the repository being linked, initialized or
	// named.
	peer string
	// path is where the link lives inside repository.
	path string
	url  string
	// branch is the peer's release branch, which a created link follows.
	branch string
	// linker is set only for the missing half of a one-sided link: the
	// repository that already declares this link and already holds the
	// checkout. The change is then the back half alone, written inside that
	// checkout, and path is where the linker keeps it.
	linker string
	// detail is the evidence, in the listing's right-hand column.
	detail string
}

func (s linkSuggestion) render() string {
	switch s.kind {
	case linkChangeInit:
		return fmt.Sprintf("+ init %s %s  %s", s.repository, s.path, s.detail)
	case linkChangeRepository:
		return fmt.Sprintf("+ repository %s %s  %s", s.repository, s.peer, s.detail)
	default:
		return fmt.Sprintf("+ link %s %s  %s", s.repository, s.peer, s.detail)
	}
}

// suggestLinks proposes what the fleet is missing: the links that would
// connect its roster, the halves of the links only one end declares, the
// checkouts its declared links lack, and the roster entries that would let
// every peer compose the same fleet.
func (a *App) suggestLinks(topology string) ([]linkSuggestion, error) {
	topology, err := normalizeComputeTopology(topology)
	if err != nil {
		return nil, err
	}
	if !a.workspace.IsLinked() {
		if topology == "star" {
			return nil, errors.New("star topology requires a linked fleet with a named entry repository")
		}
		return nil, nil
	}
	for _, finding := range a.workspace.LinkFindings() {
		switch finding.Code {
		case config.DiagnosticLinkGraph:
			return nil, fmt.Errorf("existing fleet link topology is cyclic: %s; compute never removes links, so the existing links require operator resolution", finding.Message)
		case config.DiagnosticOwnershipInvalid, config.DiagnosticComposition, config.DiagnosticBoundary:
			return nil, fmt.Errorf("cannot compute repository topology from invalid configuration: %s: %s", finding.Code, finding.Message)
		case config.DiagnosticIdentity:
			return nil, fmt.Errorf("existing fleet repository identity is inconsistent: %s; compute never rewrites repository identities, so the existing configuration requires operator resolution", finding.Message)
		}
	}
	fleet := a.fleetRoster()
	var out []linkSuggestion
	links, err := a.missingLinks(fleet, topology)
	if err != nil {
		return nil, err
	}
	out = append(out, links...)
	out = append(out, a.missingBackLinks()...)
	out = append(out, a.missingCheckouts()...)
	out = append(out, a.missingRosterEntries(fleet)...)
	return out, nil
}

func normalizeComputeTopology(topology string) (string, error) {
	switch topology {
	case "", "minimal":
		return "minimal", nil
	case "star":
		return topology, nil
	default:
		return "", fmt.Errorf("invalid compute topology %q: expected minimal or star", topology)
	}
}

// rosterEntry is one fleet member as the rosters describe it, folded together
// from every peer that names it.
type rosterEntry struct {
	name   string
	url    string
	branch string
	// composed is true when this run actually walked into the repository.
	composed bool
}

// fleetRoster is every identity this fleet holds, as the composed
// repositories and their rosters name it, in folded-identity order.
func (a *App) fleetRoster() []rosterEntry {
	byFold := map[string]*rosterEntry{}
	disabled := make(map[string]bool)
	for _, repository := range a.workspace.DisabledRepositories() {
		disabled[strings.ToLower(repository.Name)] = true
	}
	remember := func(name string) *rosterEntry {
		key := strings.ToLower(name)
		if entry, ok := byFold[key]; ok {
			return entry
		}
		entry := &rosterEntry{name: name}
		byFold[key] = entry
		return entry
	}
	for i := range a.workspace.Repositories {
		repository := &a.workspace.Repositories[i]
		remember(repository.Name).composed = true
		for _, declared := range repository.Config.Repositories {
			if disabled[strings.ToLower(declared.Name)] {
				continue
			}
			entry := remember(declared.Name)
			if entry.url == "" {
				entry.url = declared.URL
			}
			if entry.branch == "" {
				entry.branch = declared.Branch
			}
		}
	}
	out := make([]rosterEntry, 0, len(byFold))
	for _, entry := range byFold {
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].name) < strings.ToLower(out[j].name) })
	return out
}

// missingLinks is the spanning tree the fleet still needs, seeded with the
// links that exist: the group centres joined to the hub's centre first,
// because that is the proposal with the shortest longest route, and then
// whatever pair Kruskal can still reach for a group the centres could not join.
func (a *App) missingLinks(fleet []rosterEntry, topology string) ([]linkSuggestion, error) {
	if len(fleet) < 2 {
		return nil, nil
	}
	if topology == "star" {
		return a.suggestStarLinks(fleet)
	}
	// The groups and the union-find are read from one set of pairs, so a group
	// the centres join is always a set the union-find has apart, and neither
	// pass can propose the second route between two repositories that E338
	// refuses.
	adjacency := fleetLinkAdjacency(fleet, a.workspace.Repositories)
	joined := newFleetGroups(fleet)
	for linker, peers := range adjacency {
		for _, peer := range peers {
			joined.join(linker, peer)
		}
	}
	out := a.suggestCentreLinks(fleet, adjacency, joined)
	return append(out, a.suggestReachableLinks(fleet, joined)...), nil
}

// suggestCentreLinks joins every group of the fleet to the hub group at their
// centres, which is the proposal of fewest links whose longest route between
// two repositories is shortest. Section 27.9 of the specification charges link
// evidence and settlement by that route, so the shape is worth choosing rather
// than taking whichever pair a pass over the roster reaches first.
func (a *App) suggestCentreLinks(fleet []rosterEntry, adjacency map[string][]string, joined *fleetGroups) []linkSuggestion {
	groups := splitFleetIntoGroups(fleet, adjacency)
	if len(groups) < 2 {
		return nil
	}
	hub := chooseFleetHub(groups)
	a.log.Debug().Str("hub", hub.centre().name).Int("radius", hub.radius).Int("groups", len(groups)).
		Msg("joining the fleet's groups at their centres, which keeps the longest route between two repositories short")
	var out []linkSuggestion
	for _, group := range groups {
		if strings.EqualFold(group.centre().name, hub.centre().name) {
			continue
		}
		from, to, isWritable := chooseGroupJoinEnds(group, hub)
		if !isWritable {
			a.log.Debug().Str("centre", group.centre().name).
				Msg("no end of this group's link to the fleet is a checkout this run holds, so the pair is left to what compute can reach")
			continue
		}
		owner, peer := chooseLinkOwner(from, to)
		joined.join(owner.name, peer.name)
		a.log.Trace().Str("repository", owner.name).Str("peer", peer.name).Int("radius", group.radius).
			Msg("proposing the link that joins this group to the fleet at its centre")
		out = append(out, a.proposeLink(owner, peer))
	}
	return out
}

// suggestReachableLinks is what is left of Kruskal: the fleet's pairs in
// folded order, joining whatever the centres could not, which is a group no
// member of which this run has a checkout of. It proposes what can be written
// and leaves the rest to be reported.
func (a *App) suggestReachableLinks(fleet []rosterEntry, joined *fleetGroups) []linkSuggestion {
	var out []linkSuggestion
	for i := 0; i < len(fleet); i++ {
		for j := i + 1; j < len(fleet); j++ {
			// The link is created in whichever end this run can reach; the
			// other half is written inside the checkout the creation makes.
			owner, peer := chooseLinkOwner(fleet[i], fleet[j])
			if !owner.composed {
				continue
			}
			if !joined.join(owner.name, peer.name) {
				continue
			}
			out = append(out, a.proposeLink(owner, peer))
		}
	}
	return out
}

// proposeLink is one fleet link as compute offers it: created in the end that
// can write it, at the path that end's own roster asks for.
func (a *App) proposeLink(owner, peer rosterEntry) linkSuggestion {
	return linkSuggestion{
		kind: linkChangeLink, repository: owner.name, peer: peer.name,
		path:   resolveRepositoryLinkPath(a.workspace.RepositoryByName(owner.name), peer.name),
		url:    peer.url,
		branch: peer.branch,
		detail: fmt.Sprintf("connects %s to the fleet", peer.name),
	}
}

// suggestStarLinks proposes one direct link from the entry repository to
// every other member. Existing links between two non-entry peers cannot be
// reconciled without deleting history, so star refuses them before compute
// selects or writes any suggestion.
func (a *App) suggestStarLinks(fleet []rosterEntry) ([]linkSuggestion, error) {
	entry := a.workspace.EntryRepository()
	if entry == nil {
		return nil, errors.New("star topology needs an entry repository")
	}
	for i := range a.workspace.Repositories {
		repository := &a.workspace.Repositories[i]
		for _, peer := range repository.LinkPeers() {
			if !strings.EqualFold(repository.Name, entry.Name) && !strings.EqualFold(peer, entry.Name) {
				return nil, fmt.Errorf("star topology is incompatible with existing link %s <-> %s; existing links are retained, so use minimal topology", repository.Name, peer)
			}
		}
	}
	var out []linkSuggestion
	for _, peer := range fleet {
		composedPeer := a.workspace.RepositoryByName(peer.name)
		if strings.EqualFold(peer.name, entry.Name) || isRepositoryLinkedToPeer(entry, peer.name) || isRepositoryLinkedToPeer(composedPeer, entry.Name) {
			continue
		}
		// A roster-only peer is unavailable to inspect, but the entry is always
		// the writable owner of a star edge.
		out = append(out, linkSuggestion{
			kind: linkChangeLink, repository: entry.Name, peer: peer.name,
			path: resolveRepositoryLinkPath(entry, peer.name), url: peer.url, branch: peer.branch,
			detail: fmt.Sprintf("connects %s directly to %s", peer.name, entry.Name),
		})
	}
	return out, nil
}

func isRepositoryLinkedToPeer(repository *config.Repository, peer string) bool {
	if repository == nil {
		return false
	}
	for name := range repository.Links {
		if strings.EqualFold(name, peer) {
			return true
		}
	}
	return false
}

// missingBackLinks proposes the half of every link only one of its two
// repositories declares, which is the state composition reports as W332.
//
// It reads the composed repositories rather than the findings: a link is
// one-sided exactly when one side's Links names the other and the other side's
// does not, and that is the same question W332 is derived from. The pair is
// already joined, so nothing here adds an edge to the spanning tree; what it
// adds is the declaration that lets a release starting at the far end compose
// the same fleet. A peer this run never walked into is skipped, because there
// is no checkout to write the declaration in.
func (a *App) missingBackLinks() []linkSuggestion {
	var out []linkSuggestion
	for i := range a.workspace.Repositories {
		linker := &a.workspace.Repositories[i]
		for _, name := range linker.LinkPeers() {
			peer := a.workspace.RepositoryByName(name)
			if peer == nil || linkPathBetween(peer, linker) != "" {
				continue
			}
			a.log.Debug().Str("repository", peer.Name).Str("peer", linker.Name).
				Str("path", linker.Links[name]).
				Msg("this fleet link is declared at one end only; proposing the other half")
			out = append(out, linkSuggestion{
				kind: linkChangeLink, repository: peer.Name, peer: linker.Name,
				linker: linker.Name, path: linker.Links[name],
				detail: fmt.Sprintf("declares the other half of the link %s already holds", linker.Name),
			})
		}
	}
	return out
}

// missingCheckouts proposes initializing a link the fleet declares but this
// checkout never materialized, which composition reported rather than walked.
func (a *App) missingCheckouts() []linkSuggestion {
	var out []linkSuggestion
	for _, finding := range a.workspace.LinkFindings() {
		if finding.Code != config.DiagnosticRepositoryInvalid || finding.Repository == "" || finding.Peer == "" {
			continue
		}
		owner := a.workspace.RepositoryByName(finding.Repository)
		if owner == nil {
			continue
		}
		path := owner.Links[finding.Peer]
		if path == "" {
			path = resolveRepositoryLinkPath(owner, finding.Peer)
		}
		out = append(out, linkSuggestion{
			kind: linkChangeInit, repository: owner.Name, peer: finding.Peer, path: path,
			detail: "the fleet declares this link and the checkout is missing",
		})
	}
	return out
}

// missingRosterEntries proposes the fleet members a composed peer has not
// heard of. A peer that does not know a repository cannot plan a boundary
// across it, which is what composition reports as W333.
func (a *App) missingRosterEntries(fleet []rosterEntry) []linkSuggestion {
	var out []linkSuggestion
	for i := range a.workspace.Repositories {
		repository := &a.workspace.Repositories[i]
		stated := map[string]bool{strings.ToLower(repository.Name): true}
		for _, declared := range repository.Config.Repositories {
			stated[strings.ToLower(declared.Name)] = true
		}
		for _, member := range fleet {
			if stated[strings.ToLower(member.name)] {
				continue
			}
			if member.url == "" {
				// A roster entry with no url is an entry nothing can be
				// fetched from: the next repository to need a link to this
				// member would find nothing to clone. Writing it would look
				// like a repair and be one only halfway, so it is reported
				// and the operator supplies the url.
				a.log.Warn().Str("code", config.DiagnosticRosterDisagreement).
					Str("repository", repository.Name).Str("peer", member.name).
					Msg("no roster states a url for this repository, so its roster entry cannot be proposed; add the url where the fleet declares it")
				continue
			}
			out = append(out, linkSuggestion{
				kind: linkChangeRepository, repository: repository.Name, peer: member.name,
				url: member.url, branch: member.branch,
				detail: "the fleet holds this repository and this roster does not name it",
			})
		}
	}
	return out
}

// resolveRepositoryLinkPath resolves a roster path only in the repository that will
// own the proposed link. Roster paths are relative to their declaring
// repository, so a path learned from another peer cannot be reused here.
func resolveRepositoryLinkPath(owner *config.Repository, peer string) string {
	if owner != nil && owner.Config != nil {
		for _, declared := range owner.Config.Repositories {
			if strings.EqualFold(declared.Name, peer) && declared.Path != "" {
				return filepath.ToSlash(filepath.Clean(declared.Path))
			}
		}
	}
	return config.DefaultLinkPath(peer)
}

// fleetGroups is the union-find behind the spanning tree.
type fleetGroups struct{ parent map[string]string }

func newFleetGroups(fleet []rosterEntry) *fleetGroups {
	groups := &fleetGroups{parent: make(map[string]string, len(fleet))}
	for _, entry := range fleet {
		groups.parent[strings.ToLower(entry.name)] = strings.ToLower(entry.name)
	}
	return groups
}

func (g *fleetGroups) find(name string) string {
	key := strings.ToLower(name)
	for {
		parent, ok := g.parent[key]
		if !ok {
			g.parent[key] = key
			return key
		}
		if parent == key {
			return key
		}
		g.parent[key] = g.parent[parent]
		key = g.parent[key]
	}
}

// join unites two identities and reports whether they were apart, which is
// what makes an edge part of the spanning tree rather than a second path.
func (g *fleetGroups) join(a, b string) bool {
	rootA, rootB := g.find(a), g.find(b)
	if rootA == rootB {
		return false
	}
	g.parent[rootB] = rootA
	return true
}

// applyLinkChanges performs the accepted fleet changes, after the
// configuration edits have been written: a link is a checkout and a staged
// pin, and it is only ever created for a fleet the file already describes.
//
// The loop is the fixed point: creating a link makes another peer's roster
// readable, which can name members this run had never heard of. It is bounded
// by the fleet's size, so a fleet that keeps describing new members stops
// rather than spinning.
func (a *App) applyLinkChanges(ctx context.Context, cfgPath string, apply []linkSuggestion, topology string, out io.Writer) error {
	if len(apply) == 0 {
		return nil
	}
	applied := 0
	done := make(map[string]bool, len(apply))
	pending := apply
	for round := 0; round <= len(a.fleetRoster()) && len(pending) > 0; round++ {
		for _, change := range pending {
			key := change.kind + "\x00" + strings.ToLower(change.repository) + "\x00" + strings.ToLower(change.peer)
			if done[key] {
				continue
			}
			done[key] = true
			created, err := a.applyLinkChange(ctx, change, out)
			if err != nil {
				// Every other thing this command refuses for reports itself
				// before returning, and the caller only turns an error into an
				// exit status: without this, a fleet change that could not be
				// made left the operator with a failed run and no sentence
				// saying which change, in which repository, or why.
				a.log.Error().Err(err).Str("repository", change.repository).
					Str("peer", change.peer).Msg("the fleet change could not be applied")
				return err
			}
			if created {
				applied++
			}
		}
		var err error
		pending, err = a.linksAfterRepair(ctx, cfgPath, topology)
		if err != nil {
			wrapped := fmt.Errorf("re-reading the fleet after %d staged link operation(s): %w; review the staged changes before retrying", applied, err)
			a.log.Error().Err(wrapped).Str("topology", topology).
				Msg("the fleet could not be verified after applying link changes")
			return wrapped
		}
	}
	if applied > 0 {
		fmt.Fprintf(out, "created %d fleet link operation(s); review and commit them in each repository\n", applied)
	}
	return nil
}

// linksAfterRepair reads the fleet again and answers what is still missing.
//
// This is the fixed point: a link that was just created makes another peer's
// roster readable, and that roster can name repositories this run had never
// heard of. The workspace a command composed is immutable, so the fleet is
// composed again — leniently, as compute always does — and the suggestions
// are derived from what is there now. A fleet that cannot be recomposed
// stops the loop rather than repeating what it already tried.
func (a *App) linksAfterRepair(ctx context.Context, cfgPath, topology string) ([]linkSuggestion, error) {
	fresh, err := config.ComposeWorkspaceForRepair(ctx, a.cfg, cfgPath, a.root, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("the repaired fleet cannot be read again: %w", err)
	}
	if fresh == nil {
		a.log.Debug().Msg("the repaired fleet produced no workspace; run compute once more to finish it")
		return nil, nil
	}
	a.workspace = fresh
	var pending []linkSuggestion
	changes, err := a.suggestLinks(topology)
	if err != nil {
		return nil, fmt.Errorf("the repaired fleet is incompatible with %s topology: %w", topology, err)
	}
	for _, change := range changes {
		if change.kind == linkChangeLink || change.kind == linkChangeInit {
			pending = append(pending, change)
		}
	}
	return pending, nil
}

// applyLinkChange performs one fleet change and reports whether it did
// anything. A roster entry is a configuration edit and has already been
// written by the time this runs.
func (a *App) applyLinkChange(ctx context.Context, change linkSuggestion, out io.Writer) (bool, error) {
	owner := a.workspace.RepositoryByName(change.repository)
	if owner == nil {
		return false, nil
	}
	git := &gitx.LocalGitx{Dir: owner.Root, Log: a.log, LinkPaths: linkPathsOf(owner)}
	switch change.kind {
	case linkChangeInit:
		if err := git.InitSubmodule(ctx, change.path); err != nil {
			return false, fmt.Errorf("repository %s: initializing the fleet link at %s: %w", owner.Name, change.path, err)
		}
		fmt.Fprintf(out, "initialized %s in %s\n", change.path, owner.Name)
		return true, nil
	case linkChangeLink:
		if linker := a.workspace.RepositoryByName(change.linker); linker != nil {
			// The far end declares this link already and its checkout is
			// there, so the back half is the whole change: a declaration and
			// a pin written inside that checkout, and nothing fetched.
			return a.writeBackLink(ctx, linker,
				linkSuggestion{peer: change.repository, path: change.path}, out)
		}
		if change.url == "" {
			a.log.Warn().Str("repository", owner.Name).Str("peer", change.peer).
				Msg("the roster states no url for this peer, so the fleet link cannot be created")
			return false, nil
		}
		if err := git.AddSubmodule(ctx, gitx.Submodule{
			Name: change.peer, URL: change.url, Path: change.path, Branch: change.branch}); err != nil {
			return false, fmt.Errorf("repository %s: creating the fleet link to %s: %w", owner.Name, change.peer, err)
		}
		a.log.Info().Str("repository", owner.Name).Str("peer", change.peer).Str("path", change.path).
			Msg("created fleet link")
		fmt.Fprintf(out, "linked %s from %s at %s\n", change.peer, owner.Name, change.path)
		if _, err := a.writeBackLink(ctx, owner, change, out); err != nil {
			return true, err
		}
		return true, nil
	}
	return false, nil
}

// writeBackLink declares this repository inside the peer that links it,
// without cloning anything: the peer's checkout is the one the forward link
// created, and what it needs is the declaration and a pin its remote can
// serve. It reports whether the declaration was written.
func (a *App) writeBackLink(ctx context.Context, owner *config.Repository, change linkSuggestion, out io.Writer) (bool, error) {
	git := &gitx.LocalGitx{Dir: owner.Root, Log: a.log}
	remote := "origin"
	if owner.Commit != nil && owner.Commit.Remote != "" {
		remote = owner.Commit.Remote
	}
	url, err := git.RemoteURL(ctx, remote)
	if err != nil || strings.TrimSpace(url) == "" {
		a.log.Warn().Str("repository", owner.Name).Str("peer", change.peer).
			Msg("this repository has no remote to be fetched from, so the other half of the link is left to the operator")
		return false, nil
	}
	branch := ""
	if owner.Commit != nil {
		branch = owner.Commit.Branch
	}
	if branch == "" {
		if current, branchErr := git.CurrentBranch(ctx); branchErr == nil {
			branch = current
		}
	}
	revision := ""
	if branch != "" {
		if tip, tipErr := remoteBranchTip(ctx, git, remote, branch); tipErr == nil {
			revision = tip
		}
	}
	if revision == "" {
		head, headErr := git.HeadSHA(ctx)
		if headErr != nil {
			return false, fmt.Errorf("repository %s: reading the revision to pin the back link at: %w", owner.Name, headErr)
		}
		revision = head
		a.log.Warn().Str("repository", owner.Name).Str("peer", change.peer).Str("revision", revision).
			Msg("pinning the other half of the link at a revision the remote does not have yet; push this repository before releasing the fleet")
	}
	peerGit := &gitx.LocalGitx{Dir: filepath.Join(owner.Root, filepath.FromSlash(change.path)), Log: a.log}
	back := gitx.Submodule{Name: owner.Name, URL: url, Path: config.DefaultLinkPath(owner.Name), Branch: branch}
	if err := peerGit.AddGitlink(ctx, back, revision); err != nil {
		return false, fmt.Errorf("repository %s: declaring the other half of the link to %s: %w", change.peer, owner.Name, err)
	}
	a.log.Info().Str("repository", change.peer).Str("peer", owner.Name).Str("revision", revision).
		Msg("declared the other half of the fleet link")
	fmt.Fprintf(out, "declared %s inside %s at %s\n", owner.Name, change.peer, back.Path)
	return true, nil
}

// remoteBranchTip is the revision a peer can actually fetch: this
// repository's branch as its remote holds it.
func remoteBranchTip(ctx context.Context, git *gitx.LocalGitx, remote, branch string) (string, error) {
	head, err := git.HeadSHA(ctx)
	if err != nil {
		return "", err
	}
	if err := git.VerifyRemoteBranch(ctx, remote, branch, head); err != nil {
		return "", err
	}
	return head, nil
}

// collectLinkEdits routes each accepted roster entry into the configuration
// of the repository that has to state it, the way every other imported-owner
// edit is routed.
func (a *App) collectLinkEdits(edits *fileEdits, cfgPath string, apply []linkSuggestion) error {
	byPath := map[string][]linkSuggestion{}
	var order []string
	for _, change := range apply {
		if change.kind != linkChangeRepository {
			continue
		}
		owner := a.workspace.RepositoryByName(change.repository)
		if owner == nil {
			continue
		}
		path := cfgPath
		if owner.ConfigPath != "" {
			path = owner.ConfigPath
		}
		if _, seen := byPath[path]; !seen {
			order = append(order, path)
		}
		byPath[path] = append(byPath[path], change)
	}
	for _, path := range order {
		owner := a.workspace.RepositoryByName(byPath[path][0].repository)
		next := append([]config.RepositoryLinkConfig(nil), owner.Config.Repositories...)
		for _, change := range byPath[path] {
			next = append(next, config.RepositoryLinkConfig{
				Name: change.peer, URL: change.url, Path: change.path, Branch: change.branch})
		}
		sort.Slice(next, func(i, j int) bool {
			return strings.ToLower(next[i].Name) < strings.ToLower(next[j].Name)
		})
		owner.Config.Repositories = next
		if err := edits.add(path, config.Edit{KeyPath: []string{"repositories"}, Value: next}); err != nil {
			return err
		}
	}
	return nil
}
