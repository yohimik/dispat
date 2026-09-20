// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package config

// Choreographed composition: the fleet a run sees when no repository is the
// control repository.
//
// Central composition reads one inventory — the control checkout's
// `.gitmodules` — and every participant is one entry of it. A linked
// fleet has no such place to read: each peer knows its own identity, the
// roster of the fleet it belongs to, and the handful of submodule links that
// reach its neighbours. So the fleet is walked rather than listed, breadth
// first from the repository the command was invoked in, and the walk is what
// this file does.
//
// Two rules keep that walk finite and unambiguous. An identity is entered once,
// which is what stops the walk turning round at the back-link every two-sided
// pair carries; and reaching an identity a second time from anywhere other than
// the link it was entered through is a second path through the fleet, which
// E338 refuses, because a second path is a second answer to "which hops lie
// between these two repositories" and cross-repository evidence would then
// depend on which one a reader happened to follow.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultLinkPath is where a link to a peer lives when the roster states no
// path of its own. It is short, so a nested checkout on Windows keeps room for
// the paths inside it, and it starts with a dot, which is what keeps package
// discovery from ever descending into another repository's checkout.
func DefaultLinkPath(name string) string { return ".links/" + name }

// LinkedOptions steers one linked composition.
type LinkedOptions struct {
	// Lenient turns a fleet that is not yet linked correctly into findings
	// instead of a refusal. It belongs to `dispat compute`, whose whole job is
	// to repair those links; every other command needs the fleet it plans
	// against to be the fleet that exists.
	Lenient bool
	// InheritedPins records that this invocation accepted a validated live
	// workspace context, exactly as central composition does.
	InheritedPins bool
}

// LinkFinding is one recoverable problem the link walk observed. Findings are
// reported and do not stop a run: each names the repository that stated the
// link, the peer at its other end, and its diagnostic code.
type LinkFinding struct {
	Code       string
	Repository string
	Peer       string
	Message    string
}

// ComposeLinked composes the fleet reachable from one entry repository
// through its fleet links. cfg is the entry's own configuration, configPath
// the file it was read from, and entryRoot the repository that file belongs to.
func ComposeLinked(ctx context.Context, cfg *File, configPath, entryRoot string, opts LinkedOptions) (*Workspace, error) {
	if cfg == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := canonicalRepositoryRoot(entryRoot)
	if err != nil {
		return nil, WithDiagnostic(DiagnosticRepositoryInvalid,
			fmt.Errorf("linked fleet: resolve entry root %s: %w", entryRoot, err))
	}
	if err := requireCompleteRepository(root, cfg.Repository); err != nil {
		return nil, err
	}
	walk := &linkWalk{ctx: ctx, lenient: opts.Lenient, byFold: map[string]*linkNode{}}
	if err := walk.disable(cfg); err != nil {
		return nil, err
	}
	if err := walk.compose(&linkNode{
		identity: cfg.Repository, root: root, configPath: configPath, config: cfg,
	}); err != nil {
		return nil, err
	}
	walk.reportOneSidedLinks()
	walk.reportRosterDisagreement()

	repos := walk.repositories()
	mergePeerBaselines(cfg, repos)
	if err := resolveRepositoryBaselines(cfg, repos, &participation{disabled: walk.disabled}); err != nil {
		return nil, err
	}
	workspace := newWorkspace(root, repos, nil)
	workspace.inheritedPins = opts.InheritedPins
	workspace.disabled = walk.disabled
	workspace.Findings = walk.findings
	return workspace, nil
}

// IsLinked reports whether this workspace was composed from repository-owned
// configurations. Linked workspaces have a named entry and no control owner.
func (w *Workspace) IsLinked() bool {
	if w == nil {
		return false
	}
	for i := range w.Repositories {
		if w.Repositories[i].Control || strings.EqualFold(w.Repositories[i].Name, ControlRepository) {
			return false
		}
	}
	for i := range w.Repositories {
		if w.Repositories[i].Entry && w.Repositories[i].Name != "" {
			return true
		}
	}
	return false
}

// EntryRepository returns the repository the run is anchored in: the control
// repository of a central fleet, or the peer a linked run started
// from. Every caller that used to reach for the control identity asks this
// instead, which keeps callers independent of composition details.
func (w *Workspace) EntryRepository() *Repository {
	if w == nil {
		return nil
	}
	for i := range w.Repositories {
		if w.Repositories[i].Entry {
			return &w.Repositories[i]
		}
	}
	return w.RepositoryByName(ControlRepository)
}

// LinkPeers lists the fleet peers this repository links, in one stable order,
// so a log and a diagnostic name them the same way twice running.
func (r *Repository) LinkPeers() []string {
	if r == nil {
		return nil
	}
	return foldedOrder(r.Links)
}

// LinkFindings returns the recoverable link problems composition observed, in
// the order the walk found them.
func (w *Workspace) LinkFindings() []LinkFinding {
	if w == nil {
		return nil
	}
	return w.Findings
}

// LinkRoute returns the repositories a reader passes through to get from one
// participant to another, both ends included, or nil when either identity is
// absent from this workspace or no chain of links joins them.
//
// The route is unique because composition refused any fleet whose links do not
// form a tree, which is what lets cross-repository evidence be read hop by hop
// instead of guessed.
func (w *Workspace) LinkRoute(from, to string) []string {
	if w == nil {
		return nil
	}
	start, end := w.RepositoryByName(from), w.RepositoryByName(to)
	if start == nil || end == nil {
		return nil
	}
	if strings.EqualFold(start.Name, end.Name) {
		return []string{start.Name}
	}
	previous := map[string]string{strings.ToLower(start.Name): ""}
	for queue := []string{start.Name}; len(queue) > 0; queue = queue[1:] {
		current := queue[0]
		for _, peer := range w.linkNeighbours(current) {
			if _, seen := previous[strings.ToLower(peer)]; seen {
				continue
			}
			previous[strings.ToLower(peer)] = current
			if strings.EqualFold(peer, end.Name) {
				return linkRouteBack(previous, start.Name, peer)
			}
			queue = append(queue, peer)
		}
	}
	return nil
}

// linkNeighbours answers both ends of every link touching one repository. A
// link declared by only one of its two repositories still joins them for the
// purpose of reading a route; W332 is how the missing half is reported.
func (w *Workspace) linkNeighbours(name string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(peer string) {
		if peer == "" || seen[strings.ToLower(peer)] {
			return
		}
		seen[strings.ToLower(peer)] = true
		out = append(out, peer)
	}
	for i := range w.Repositories {
		repository := &w.Repositories[i]
		if strings.EqualFold(repository.Name, name) {
			for _, peer := range foldedOrder(repository.Links) {
				add(peer)
			}
			continue
		}
		for peer := range repository.Links {
			if strings.EqualFold(peer, name) {
				add(repository.Name)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// linkRouteBack walks the recorded predecessors from the far end back to the
// start and returns the chain the other way round.
func linkRouteBack(previous map[string]string, start, end string) []string {
	route := []string{end}
	for current := end; !strings.EqualFold(current, start); {
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

// linkNode is one repository while the walk is still running: what it says
// about itself, and how the walk reached it.
type linkNode struct {
	identity   string
	root       string
	configPath string
	config     *File
	// linker is the identity whose fleet link reached this repository. The
	// entry has none, which is what makes it the entry.
	linker string
	// gitlinkPath is where this repository sits inside its linker.
	gitlinkPath string
	// links maps each linked peer identity to the gitlink path holding it in
	// this repository.
	links map[string]string
	head  string
}

// linkWalk is one breadth-first pass over a fleet's links.
type linkWalk struct {
	ctx      context.Context
	lenient  bool
	order    []*linkNode
	byFold   map[string]*linkNode
	disabled []DisabledRepository
	findings []LinkFinding
}

func (w *linkWalk) compose(entry *linkNode) error {
	queue := []*linkNode{entry}
	w.admit(entry)
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		head, err := gitOutputContext(w.ctx, node.root, "rev-parse", "HEAD")
		if err != nil {
			return WithDiagnostic(DiagnosticRepositoryInvalid,
				fmt.Errorf("E330: linked fleet: repository %q has no HEAD: %w", node.identity, err))
		}
		node.head = head
		links, err := w.linksOf(node)
		if err != nil {
			return err
		}
		node.links = links
		for _, peer := range foldedOrder(links) {
			next, err := w.enter(node, peer, links[peer])
			if err != nil {
				return err
			}
			if next != nil {
				queue = append(queue, next)
			}
		}
	}
	return nil
}

// enter follows one fleet link. It answers the repository the walk must still
// visit, or nil when the link ends somewhere the walk has already been.
func (w *linkWalk) enter(node *linkNode, peer, path string) (*linkNode, error) {
	if visited, ok := w.byFold[strings.ToLower(peer)]; ok {
		// The identity cut. Every two-sided link is reached twice, once
		// forwards and once as the back-link of the repository it came from,
		// and the second sighting is the same edge rather than another route.
		if strings.EqualFold(peer, node.linker) {
			return nil, nil
		}
		return nil, w.refuse(DiagnosticLinkGraph, node.identity, peer, fmt.Errorf(
			"E338: linked fleet: repository %q is reached through %q and again through %q; fleet links must form one tree",
			visited.identity, visited.linker, node.identity))
	}
	root, err := containedPath(node.root, path)
	if err != nil {
		return nil, w.refuse(DiagnosticRepositoryInvalid, node.identity, peer, fmt.Errorf(
			"E330: linked fleet: repository %q link path %q: %w", node.identity, path, err))
	}
	uninitialized := fmt.Errorf(
		"E330: linked fleet: repository %q is not initialized at %s; run `git submodule update --init -- %s` in %s",
		peer, root, path, node.root)
	resolved, err := canonicalRepositoryRoot(root)
	if err != nil {
		return nil, w.refuse(DiagnosticRepositoryInvalid, node.identity, peer, uninitialized)
	}
	// A link whose folder exists but holds no repository of its own is the
	// state every unpopulated back-link is in, and Git answers questions asked
	// inside it from the repository that contains it. Only a folder that is
	// its own top level is a peer; anything else is the linker answering for
	// a checkout that was never made.
	top, err := gitOutputContext(w.ctx, resolved, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, w.refuse(DiagnosticRepositoryInvalid, node.identity, peer, uninitialized)
	}
	if top, err = canonicalRepositoryRoot(top); err != nil || top != resolved {
		return nil, w.refuse(DiagnosticRepositoryInvalid, node.identity, peer, uninitialized)
	}
	root = resolved
	if err := requireCompleteRepository(root, peer); err != nil {
		return nil, w.refuse(DiagnosticRepositoryInvalid, node.identity, peer, err)
	}
	configPath, err := peerConfigPath(root)
	if err != nil {
		return nil, w.refuse(DiagnosticRepositoryInvalid, node.identity, peer, fmt.Errorf(
			"E330: linked fleet: repository %q: %w", peer, err))
	}
	config, err := Load(configPath, nil)
	if err != nil {
		return nil, w.refuse(DiagnosticRepositoryInvalid, node.identity, peer, fmt.Errorf(
			"linked fleet: repository %q config %s: %w", peer, configPath, err))
	}
	if !config.IsLinked() {
		return nil, w.refuse(DiagnosticIdentity, node.identity, peer, fmt.Errorf(
			"E339: linked fleet: repository %q at %s has no repository identity; each linked peer must declare its own repository identity",
			peer, configPath))
	}
	if config.Repository != peer {
		return nil, w.refuse(DiagnosticIdentity, node.identity, peer, fmt.Errorf(
			"E339: linked fleet: %s calls itself %q but repository %q links it as %q; an identity is spelled the same on both sides of a link",
			configPath, config.Repository, node.identity, peer))
	}
	next := &linkNode{
		identity: config.Repository, root: root, configPath: configPath, config: config,
		linker: node.identity, gitlinkPath: path,
	}
	w.admit(next)
	return next, nil
}

func (w *linkWalk) admit(node *linkNode) {
	w.order = append(w.order, node)
	w.byFold[strings.ToLower(node.identity)] = node
}

// refuse turns one fleet problem into an error, or into a finding when the
// caller is repairing the fleet rather than releasing it.
func (w *linkWalk) refuse(code, repository, peer string, err error) error {
	if !w.lenient {
		return WithDiagnostic(code, err)
	}
	// Preserve the same diagnostic that strict composition would return.
	// Otherwise an invalid peer identity looks like a missing checkout.
	if reportedCode := DiagnosticCode(err); reportedCode != "" {
		code = reportedCode
	}
	w.findings = append(w.findings, LinkFinding{
		Code: code, Repository: repository, Peer: peer, Message: err.Error()})
	return nil
}

// linksOf answers which of a repository's submodules are fleet links: the ones
// its roster names. Every other submodule is an ordinary vendored checkout and
// takes no part in the fleet.
func (w *linkWalk) linksOf(node *linkNode) (map[string]string, error) {
	inventory, err := readLinkInventory(w.ctx, node.root)
	if err != nil {
		return nil, err
	}
	links := make(map[string]string, len(node.config.Repositories))
	for _, entry := range node.config.Repositories {
		if w.isDisabled(entry.Name) {
			continue
		}
		path, ok := inventory[entry.Name]
		if !ok {
			// Not every peer is a neighbour: the links form a tree over the
			// roster, so a repository two hops away is named here and linked
			// elsewhere. A near-miss is different, and is reported as one.
			for name := range inventory {
				if strings.EqualFold(name, entry.Name) {
					if err := w.refuse(DiagnosticIdentity, node.identity, entry.Name, fmt.Errorf(
						"E339: linked fleet: repository %q links submodule %q for roster entry %q; a fleet link carries the peer's identity exactly",
						node.identity, name, entry.Name)); err != nil {
						return nil, err
					}
				}
			}
			continue
		}
		links[entry.Name] = path
	}
	return links, nil
}

func (w *linkWalk) isDisabled(name string) bool {
	for i := range w.disabled {
		if strings.EqualFold(w.disabled[i].Name, name) {
			return true
		}
	}
	return false
}

// disable resolves participation from the entry configuration alone. A peer
// owns its own policy in a linked fleet, but whether it takes part in
// this run is the invocation's question, and the invocation is the entry.
func (w *linkWalk) disable(cfg *File) error {
	roster := make(map[string]bool, len(cfg.Repositories)+1)
	roster[strings.ToLower(cfg.Repository)] = true
	for _, entry := range cfg.Repositories {
		roster[strings.ToLower(entry.Name)] = true
	}
	for _, name := range sortedKeys(cfg.RepositoryOverrides) {
		if !roster[strings.ToLower(name)] {
			return WithDiagnostic(DiagnosticComposition,
				fmt.Errorf("linked fleet: repositoryOverrides names %q, which no roster entry declares", name))
		}
		if cfg.RepositoryOverrides[name].IsEnabled() {
			continue
		}
		if strings.EqualFold(name, cfg.Repository) {
			return WithDiagnostic(DiagnosticComposition,
				fmt.Errorf("linked fleet: repositoryOverrides[%q] excludes the repository the run started in", name))
		}
		w.disabled = append(w.disabled, DisabledRepository{Name: name})
	}
	sort.Slice(w.disabled, func(i, j int) bool { return w.disabled[i].Name < w.disabled[j].Name })
	return nil
}

// reportOneSidedLinks names every link only one end declares. The fleet still
// composes through it; what the finding says is that the other repository
// cannot compose the same fleet from where it stands.
func (w *linkWalk) reportOneSidedLinks() {
	for _, node := range w.order {
		for _, peer := range foldedOrder(node.links) {
			other, ok := w.byFold[strings.ToLower(peer)]
			if !ok {
				continue
			}
			if _, mutual := other.links[node.identity]; mutual {
				continue
			}
			w.findings = append(w.findings, LinkFinding{
				Code: DiagnosticLinkOneSided, Repository: node.identity, Peer: peer,
				Message: fmt.Sprintf(
					"W332: repository %q links %q, which does not link it back; a release starting in %q would compose a smaller fleet",
					node.identity, peer, peer),
			})
		}
	}
}

// reportRosterDisagreement names every peer whose roster is not the fleet this
// composition found. A repository added to one roster and not the others is
// the ordinary cause, and it matters because the repository that has not heard
// of a peer cannot plan a boundary across it.
func (w *linkWalk) reportRosterDisagreement() {
	fleet := make([]string, 0, len(w.order))
	for _, node := range w.order {
		fleet = append(fleet, node.identity)
	}
	sort.Slice(fleet, func(i, j int) bool { return strings.ToLower(fleet[i]) < strings.ToLower(fleet[j]) })
	for _, node := range w.order {
		stated := map[string]bool{strings.ToLower(node.identity): true}
		for _, entry := range node.config.Repositories {
			stated[strings.ToLower(entry.Name)] = true
		}
		var missing []string
		for _, name := range fleet {
			if !stated[strings.ToLower(name)] {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 {
			continue
		}
		w.findings = append(w.findings, LinkFinding{
			Code: DiagnosticRosterDisagreement, Repository: node.identity, Peer: missing[0],
			Message: fmt.Sprintf(
				"W333: repository %q does not name %s in its roster; a release starting there plans without them",
				node.identity, quotedNames(missing)),
		})
	}
}

// repositories renders the walk as the workspace's participants: the entry
// first, then every peer by identity, which is the order a reader of the
// composition log and of a lock sequence expects.
func (w *linkWalk) repositories() []Repository {
	nodes := append([]*linkNode(nil), w.order...)
	sort.Slice(nodes, func(i, j int) bool {
		if (nodes[i].linker == "") != (nodes[j].linker == "") {
			return nodes[i].linker == ""
		}
		return strings.ToLower(nodes[i].identity) < strings.ToLower(nodes[j].identity)
	})
	repos := make([]Repository, 0, len(nodes))
	for _, node := range nodes {
		repos = append(repos, Repository{
			Name:            node.identity,
			Root:            node.root,
			GitlinkPath:     node.gitlinkPath,
			ConfigPath:      node.configPath,
			Config:          node.config,
			Imported:        true,
			Entry:           node.linker == "",
			Linker:          node.linker,
			Links:           node.links,
			Commit:          node.config.Commit,
			CompositionHead: node.head,
		})
	}
	return repos
}

// mergePeerBaselines gathers the explicit cross-repository boundaries every
// peer declares into the entry's list. A boundary is a statement about two
// repositories, and the repository that owns the consumer is the one that
// knows it; with no control file to write it in, the run has to read them all.
func mergePeerBaselines(cfg *File, repos []Repository) {
	seen := make(map[string]bool, len(cfg.RepositoryBaselines))
	key := func(b RepositoryBaselineConfig) string {
		return strings.ToLower(b.Consumer) + "\x00" + b.ReleaseTag + "\x00" + strings.ToLower(b.Repository)
	}
	for _, b := range cfg.RepositoryBaselines {
		seen[key(b)] = true
	}
	for i := range repos {
		if repos[i].Config == cfg {
			continue
		}
		for _, b := range repos[i].Config.RepositoryBaselines {
			if seen[key(b)] {
				continue
			}
			seen[key(b)] = true
			cfg.RepositoryBaselines = append(cfg.RepositoryBaselines, b)
		}
	}
}

// readLinkInventory reads the submodule names and paths a repository declares,
// without requiring any of them to be initialized.
//
// loadSubmodules answers a stricter question and cannot serve here: it refuses
// a repository with no `.gitmodules` at all and requires every entry to be an
// initialized checkout. A linked peer may legitimately be neither —
// a repository that has not been linked yet has no `.gitmodules`, and the
// back-link of every two-sided pair is deliberately left unpopulated so the
// walk never descends into a second copy of its own linker.
func readLinkInventory(ctx context.Context, root string) (map[string]string, error) {
	file := filepath.Join(root, ".gitmodules")
	if _, err := os.Stat(file); err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, WithDiagnostic(DiagnosticRepositoryInvalid,
			fmt.Errorf("linked fleet: read %s: %w", file, err))
	}
	out, err := gitOutputContext(ctx, root, "config", "--file", file, "--null", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		// git config answers "no key matched" with exit status 1 and says
		// nothing, which is what an empty or link-less `.gitmodules` is. Any
		// other status is a file that cannot be read and is reported as one.
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return map[string]string{}, nil
		}
		return nil, WithDiagnostic(DiagnosticRepositoryInvalid,
			fmt.Errorf("linked fleet: read %s: %w", file, err))
	}
	inventory := map[string]string{}
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		key, path, ok := strings.Cut(record, "\n")
		if !ok {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid,
				fmt.Errorf("linked fleet: malformed git config output for %s", file))
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		if name == "" || path == "" {
			continue
		}
		inventory[name] = filepath.ToSlash(filepath.Clean(path))
	}
	return inventory, nil
}

// peerConfigPath finds a linked repository's own configuration file without
// ascending out of it. The ordinary resolution climbs parents, and every peer
// of a linked fleet sits inside the checkout of the repository that
// links it: an ascent would find the linker's file and compose a repository
// against a configuration it does not own.
func peerConfigPath(root string) (string, error) {
	for _, name := range defaultFileNames {
		candidate := filepath.Join(root, name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no dispat config file at %s (tried %s)", root, strings.Join(defaultFileNames, ", "))
}

// canonicalRepositoryRoot resolves a repository root the way every ownership
// comparison in a composed workspace spells it: absolute, with symlinks
// resolved, because a temporary directory on macOS is reached through one.
func canonicalRepositoryRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, err
	}
	return resolved, nil
}

// foldedOrder lists a link map's peers in one stable order, so two runs of the
// same fleet walk it the same way and report the same first problem.
func foldedOrder(links map[string]string) []string {
	names := make([]string, 0, len(links))
	for name := range links {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return names
}
