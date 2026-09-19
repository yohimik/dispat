// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// Settling a choreographed fleet's links before a package publishes.
//
// A release records what it incorporated. An orchestrated fleet writes that in
// the control repository, as a checkpoint commit whose gitlinks name the
// source revisions. A choreographed fleet has no such repository, so the same
// fact is written into the links themselves: before a cross-repository
// consumer publishes, every repository on the route to each of its providers
// records the revision of the next hop, and the consumer records the first
// hop. The release commit the tag then sits on carries that evidence in its
// tree, which is exactly what the planner reads back.
//
// Three properties are what make it safe to do this before publication rather
// than after. It is ordinary commits only — nothing is published, so an
// interrupted settlement leaves a repository that the next run converges from.
// It is all-or-nothing per package: a route that cannot be recorded refuses
// the package before anything external happens. And it never holds two
// repository mutexes at once, taking the publish lanes it needs in name order,
// so two consumers settling overlapping routes cannot wait on each other.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// setLinkPlan records which repositories each releasing package needs link
// evidence for: the repositories its plan read history from, other than its
// own. It is filled once, from the plan, beside the snapshot closure.
func (w *workspaceRecorder) setLinkPlan(pl *plan.Plan) {
	if pl == nil || !w.app.workspace.IsChoreographed() {
		return
	}
	w.routesMu.Lock()
	w.routes = nil
	w.routesMu.Unlock()
	w.linkPlan = make(map[string][]string, len(pl.Releases))
	for _, name := range pl.Order {
		rel := pl.Releases[name]
		if rel == nil || rel.Pkg == nil || !rel.IsReleasing() {
			continue
		}
		words := pl.RepositoryInputs[name]
		var foreign []string
		for i, repository := range pl.RepositoryInputOrder {
			if i/64 >= len(words) || words[i/64]&(uint64(1)<<uint(i%64)) == 0 {
				continue
			}
			if strings.EqualFold(repository, rel.Pkg.Repository) {
				continue
			}
			foreign = append(foreign, repository)
		}
		if len(foreign) > 0 {
			sort.Strings(foreign)
			w.linkPlan[name] = foreign
			w.app.log.Debug().Str("package", name).Strs("repositories", foreign).
				Msg("fleet links to settle before publication")
		}
	}
}

// settleNode is one repository of a settlement: the pins it has to record for
// the hops beyond it, and the children whose heads those pins are.
type settleNode struct {
	record   *repositoryRecord
	children []*settleNode
	// path is where this node's parent holds its link to it.
	path string
	// head is the revision the parent must pin once this node has settled.
	head string
}

// settlePlan is the route tree of one package's settlement, rooted at the
// repository the package belongs to.
type settlePlan struct {
	root  *settleNode
	nodes map[string]*settleNode
	// order lists every participating repository name, sorted, which is the
	// order their publish lanes are taken in.
	order []string
}

// planLinks builds the route tree from the consumer's repository to every
// repository its release needs evidence for. It answers nil when the package
// needs no settlement at all, which is every package of a single-repository
// release and every package whose providers live beside it.
func (w *workspaceRecorder) planLinks(rel *plan.Release) (*settlePlan, error) {
	if rel == nil || rel.Pkg == nil || len(w.linkPlan[rel.Pkg.Name]) == 0 {
		return nil, nil
	}
	// One route tree per release, computed once: acquirePublish asks for it,
	// and the pre-flight verification asks the same question again. The map
	// holds one entry per releasing package and goes with the recorder.
	w.routesMu.Lock()
	defer w.routesMu.Unlock()
	if route, ok := w.routes[rel]; ok {
		return route, nil
	}
	own := w.byName[rel.Pkg.Repository]
	if own == nil {
		return nil, fmt.Errorf("no repository owner for package %s", rel.Pkg.Name)
	}
	route := &settlePlan{nodes: map[string]*settleNode{}}
	route.root = &settleNode{record: own}
	route.nodes[strings.ToLower(own.repo.Name)] = route.root
	for _, target := range w.linkPlan[rel.Pkg.Name] {
		hops := w.app.workspace.LinkRoute(own.repo.Name, target)
		if len(hops) == 0 {
			return nil, config.WithDiagnostic(config.DiagnosticBoundary, fmt.Errorf(
				"E333: package %s reads repository %s, which no chain of fleet links joins to %s; link the fleet with `dispat compute` or add repositoryBaselines",
				rel.Pkg.Name, target, own.repo.Name))
		}
		parent := route.root
		for _, hop := range hops[1:] {
			node, seen := route.nodes[strings.ToLower(hop)]
			if !seen {
				record := w.byName[hop]
				if record == nil {
					return nil, fmt.Errorf("package %s route names repository %s, which this run did not compose", rel.Pkg.Name, hop)
				}
				path := w.app.workspace.LinkRoute(parent.record.repo.Name, hop)
				if len(path) != 2 {
					return nil, fmt.Errorf("repositories %s and %s are not linked directly", parent.record.repo.Name, hop)
				}
				node = &settleNode{record: record, path: linkPathBetween(parent.record.repo, record.repo)}
				if node.path == "" {
					return nil, fmt.Errorf("repository %s holds no fleet link to %s", parent.record.repo.Name, hop)
				}
				route.nodes[strings.ToLower(hop)] = node
				parent.children = append(parent.children, node)
			}
			parent = node
		}
	}
	for name := range route.nodes {
		route.order = append(route.order, route.nodes[name].record.repo.Name)
	}
	sort.Strings(route.order)
	for _, node := range route.nodes {
		sort.Slice(node.children, func(i, j int) bool {
			return node.children[i].record.repo.Name < node.children[j].record.repo.Name
		})
	}
	if w.routes == nil {
		w.routes = make(map[*plan.Release]*settlePlan)
	}
	w.routes[rel] = route
	return route, nil
}

// linkPathBetween is where one repository holds its link to another, read from
// whichever side declares it. A link only the far side declares still joins
// the two; composition reports the missing half as W332.
func linkPathBetween(from, to *config.Repository) string {
	for name, path := range from.Links {
		if strings.EqualFold(name, to.Name) {
			return path
		}
	}
	// The far side's own link path is relative to its own root and says
	// nothing about where this repository holds it, so a one-sided link is
	// only usable when this side is the one that declared it.
	return ""
}

// settleLinks records the fleet links one package's release depends on, in the
// repositories that have to carry them, before that package publishes.
//
// The lanes are taken in repository-name order and every one but the
// package's own is released again before returning: the consumer's lane stays
// held for the publish itself, exactly as it was before this existed.
func (w *workspaceRecorder) settleLinks(ctx context.Context, rel *plan.Release, held map[string]func()) error {
	return w.settleLinksWithHooks(ctx, recordHooks{ctx: ctx, observer: ctx}, rel, held)
}

// settleLinksWithHooks is settleLinks under an explicit hook bracket. A
// settlement is made before publication, so its hooks run on the live run's
// own context; the parameter exists because the recorder's other commit paths
// take theirs the same way.
func (w *workspaceRecorder) settleLinksWithHooks(ctx context.Context, hooks recordHooks,
	rel *plan.Release, held map[string]func()) error {
	route, err := w.planLinks(rel)
	if err != nil || route == nil {
		return err
	}
	own := route.root.record
	if !own.repo.Commit.IsEnabled() {
		// Nothing records anything here, so there is nothing to settle. The
		// next plan will need a repositoryBaselines tuple, which is what the
		// specification already says about a tag-only consumer.
		own.git.Log.Info().Str("package", rel.Pkg.Name).Strs("repositories", route.order).
			Msg("release commits are disabled; no fleet link evidence is recorded for this release")
		return nil
	}
	for _, name := range route.order {
		record := w.byName[name]
		if record == nil || record.repo.Commit.IsEnabled() {
			continue
		}
		// All or nothing: a route that cannot record its hop would leave the
		// consumer's tag with evidence that stops halfway, and the next plan
		// would refuse it with E333 after the package had already published.
		return config.WithDiagnostic(config.DiagnosticBoundary, fmt.Errorf(
			"E333: package %s needs repository %s to record a fleet link, but its release commits are disabled; enable commit for the whole route or add repositoryBaselines",
			rel.Pkg.Name, name))
	}
	for _, name := range route.order {
		if _, ok := held[name]; !ok {
			return fmt.Errorf("package %s settles repository %s without holding its publish lane", rel.Pkg.Name, name)
		}
	}
	if err := w.settleNode(ctx, hooks, rel, route.root); err != nil {
		return err
	}
	own.git.Log.Debug().Str("package", rel.Pkg.Name).Strs("repositories", route.order).
		Msg("fleet links settled")
	return nil
}

// settleNode records the pins of one node's children, after each of them has
// settled. The walk is depth first, so a pin is always written to a revision
// that is already final.
func (w *workspaceRecorder) settleNode(ctx context.Context, hooks recordHooks, rel *plan.Release, node *settleNode) error {
	for _, child := range node.children {
		if err := w.settleNode(ctx, hooks, rel, child); err != nil {
			return err
		}
	}
	record := node.record
	record.mu.Lock()
	defer record.mu.Unlock()
	if len(node.children) == 0 {
		// The far end of a route records nothing; what it contributes is the
		// revision its parent has to pin.
		head, err := record.git.HeadSHA(ctx)
		if err != nil {
			return fmt.Errorf("repository %s: reading the revision to settle: %w", record.repo.Name, err)
		}
		node.head = head
		return nil
	}
	pins := make(map[string]string, len(node.children))
	paths := make([]string, 0, len(node.children))
	for _, child := range node.children {
		pins[child.path] = child.head
		paths = append(paths, child.path)
	}
	sort.Strings(paths)
	head, err := record.git.HeadSHA(ctx)
	if err != nil {
		return fmt.Errorf("repository %s: reading the revision to settle: %w", record.repo.Name, err)
	}
	node.head = head
	// The fast path, and the ordinary one once a fleet has settled: one tree
	// read says whether this repository already records these revisions.
	recorded, err := record.git.GitlinksAtPaths(ctx, head, paths)
	if err != nil {
		return fmt.Errorf("repository %s: reading its fleet links: %w", record.repo.Name, err)
	}
	pending := false
	for path, pin := range pins {
		if recorded[path] != pin {
			pending = true
			break
		}
	}
	if pending {
		if record.repo.Commit.IsPushEnabled() {
			if record.branch == "" {
				return config.WithDiagnostic("E337", fmt.Errorf(
					"E337: repository %s is detached; set commit.branch before recording and pushing a fleet link", record.repo.Name))
			}
			// A pin must never outrun the thing it points at: the revision
			// has to be on the child's own remote before this repository
			// records a commit naming it.
			for _, child := range node.children {
				if err := w.verifySettledRemote(ctx, child.record, child.head); err != nil {
					return fmt.Errorf("repository %s cannot record a fleet link to %s: %w",
						record.repo.Name, child.record.repo.Name, err)
				}
			}
		}
		// The hooks bracket the commit exactly where a checkpoint's do: before
		// the advisory lock is taken and after it is released, so no user
		// script ever runs while this repository is held.
		hooks.run(record.hooks, "beforeCommit", record.repo.Config.Run.BeforeCommit)
		unlock, lockErr := gitx.AcquireMutations(ctx, record.git)
		if lockErr != nil {
			return lockErr
		}
		settled, err := w.commitLinks(ctx, record, rel, pins, paths)
		unlock()
		if err != nil {
			return fmt.Errorf("repository %s: recording fleet links: %w", record.repo.Name, err)
		}
		node.head = settled
		hooks.run(record.hooks, "afterCommit", record.repo.Config.Run.AfterCommit)
		hooks.run(record.hooks, "postCommit", record.repo.Config.Run.PostCommit)
	} else {
		record.git.Log.Debug().Str("package", rel.Pkg.Name).Strs("links", paths).
			Msg("fleet links already record these revisions")
	}
	return w.pushSettled(ctx, hooks, record, node.head, pending)
}

// verifySettledRemote proves a peer's remote already holds the revision this
// settlement is about to pin, and answers which branch to ask about.
//
// A linked checkout is detached — `submodule update` leaves it at the pinned
// revision, not on a branch — so the repository's own branch is usually empty
// at the far end of a route. What the fleet knows about it is in the roster,
// which is where the branch comes from when the repository states none. This
// is for the question alone: pushing a commit from a detached repository
// still needs its own commit.branch, and still stops with E337 without one.
func (w *workspaceRecorder) verifySettledRemote(ctx context.Context, record *repositoryRecord, revision string) error {
	branch, source := w.verificationBranch(record)
	if branch == "" {
		return fmt.Errorf(
			"repository %s has no branch to verify revision %s against: set its commit.branch, or state a branch for it in the fleet roster",
			record.repo.Name, revision)
	}
	record.git.Log.Debug().Str("branch", branch).Str("from", source).Str("revision", revision).
		Msg("verifying the revision a fleet link will pin")
	return record.git.VerifyRemoteBranch(ctx, record.remote(), branch, revision)
}

// verificationBranch is the branch a peer's remote is asked about, and where
// that answer came from. The repository's own policy wins; then the roster
// entry of whichever repository links it, which is the one that chose the
// branch the link follows; then any roster that states one at all.
func (w *workspaceRecorder) verificationBranch(record *repositoryRecord) (branch, source string) {
	if record.branch != "" {
		return record.branch, "commit.branch"
	}
	if record.repo.Commit != nil && record.repo.Commit.Branch != "" {
		return record.repo.Commit.Branch, "commit.branch"
	}
	if linker := w.byName[record.repo.Linker]; linker != nil {
		for _, entry := range linker.repo.Config.Repositories {
			if strings.EqualFold(entry.Name, record.repo.Name) && entry.Branch != "" {
				return entry.Branch, "repositories[" + linker.repo.Name + "]"
			}
		}
	}
	for _, other := range w.ordered {
		for _, entry := range other.repo.Config.Repositories {
			if strings.EqualFold(entry.Name, record.repo.Name) && entry.Branch != "" {
				return entry.Branch, "repositories[" + other.repo.Name + "]"
			}
		}
	}
	return "", ""
}

// commitLinks writes one node's pins and advances everything that watches its
// HEAD. The guard admits the new revision here rather than discovering it:
// the pre-publish verification reads expectedHead, and a settlement that did
// not announce itself would be refused as drift.
func (w *workspaceRecorder) commitLinks(ctx context.Context, record *repositoryRecord,
	rel *plan.Release, pins map[string]string, paths []string) (string, error) {
	if err := record.verifyExpectedHead(ctx); err != nil {
		return "", err
	}
	// The settlement carries the release's own message, exactly as a control
	// checkpoint does. That is what makes it readable as evidence: a release
	// whose packages write nothing leaves its tag on this commit, and a
	// commit that names the tag is what the next plan's first test asks for.
	message := renderCommitMessage(record.repo.Commit.MessageFormat, []string{rel.Pkg.Name}, []string{rel.TagName()})
	settled, created, err := record.git.CommitGitlinks(ctx, message, pins)
	if err != nil {
		return "", err
	}
	record.expectedHead = settled
	if created {
		record.git.Log.Info().Str("package", rel.Pkg.Name).Strs("links", paths).
			Str("revision", settled).Msg("recorded fleet links")
	}
	return settled, nil
}

// pushSettled makes a settlement durable. It pushes a commit it just created,
// and also a head the remote does not have yet, which is how a run that was
// interrupted between the commit and the push converges on a retry.
func (w *workspaceRecorder) pushSettled(ctx context.Context, hooks recordHooks,
	record *repositoryRecord, settled string, created bool) error {
	if !record.repo.Commit.IsPushEnabled() {
		return nil
	}
	if !created {
		if err := record.git.VerifyRemoteBranch(ctx, record.remote(), record.branch, settled); err == nil {
			return nil
		}
	}
	hooks.run(record.hooks, "beforePush", record.repo.Config.Run.BeforePush)
	err := func() error {
		unlock, lockErr := gitx.AcquireMutations(ctx, record.git)
		if lockErr != nil {
			return lockErr
		}
		defer unlock()
		if err := verifyRepositoryPin(ctx, record, settled); err != nil {
			return err
		}
		return record.git.PushRelease(ctx, record.remote(), record.branch, nil, nil)
	}()
	if err != nil {
		return fmt.Errorf("repository %s: pushing the settled fleet links: %w", record.repo.Name, err)
	}
	record.git.Log.Info().Str("revision", settled).Str("branch", record.branch).
		Msg("pushed settled fleet links")
	hooks.run(record.hooks, "afterPush", record.repo.Config.Run.AfterPush)
	return nil
}

// settleLanes are the publish lanes one settlement needs, in name order.
func (w *workspaceRecorder) settleLanes(rel *plan.Release) ([]string, error) {
	route, err := w.planLinks(rel)
	if err != nil {
		return nil, err
	}
	if route == nil {
		if rel == nil || rel.Pkg == nil {
			return nil, nil
		}
		return []string{rel.Pkg.Repository}, nil
	}
	return route.order, nil
}

// linkPathsOf lists a repository's fleet link paths, which is what its Git
// handle excludes from every pathspec the release builds.
func linkPathsOf(repository *config.Repository) []string {
	if repository == nil || len(repository.Links) == 0 {
		return nil
	}
	paths := make([]string, 0, len(repository.Links))
	for _, peer := range repository.LinkPeers() {
		paths = append(paths, repository.Links[peer])
	}
	sort.Strings(paths)
	return paths
}
