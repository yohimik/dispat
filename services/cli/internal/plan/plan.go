// Package plan reads git history and computes the release plan: which
// packages changed, what their next versions are, and in which order they must
// be processed.
package plan

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/internal/conventional"
	"github.com/yohimik/dispat/internal/gitx"
	"github.com/yohimik/dispat/internal/graph"
	"github.com/yohimik/dispat/internal/model"
	"github.com/yohimik/dispat/internal/semver"
)

// Release describes what (if anything) will happen to one package.
type Release struct {
	Pkg *model.Package
	// Current is the baseline the next version is bumped from: the latest
	// parseable tag; otherwise the configured initial version; otherwise
	// 0.0.0.
	Current      semver.Version
	Tagged       bool  // whether a previous parseable release tag exists
	TagCreated   int64 // creation time (unix) of that tag; 0 when untagged
	FromInitials bool  // Current came from the config initials
	OwnBump      semver.Bump    // bump demanded by the package's own commits
	Bump         semver.Bump    // final bump including provider propagation
	Next         semver.Version // version to release; equals Current when unchanged
	Commits      []conventional.Commit
	DueTo        []string // changed providers that forced (at least) a patch bump
}

// Changed reports whether the package needs a release.
func (r *Release) Changed() bool { return r.Bump != semver.BumpNone }

// Plan is the full release plan for the repository.
type Plan struct {
	Order     []string            // topological order, providers before consumers
	Releases  map[string]*Release // one entry per package
	Providers map[string][]string // consumer -> its providers
	Consumers map[string][]string // provider -> its consumers
}

// Compute builds the dependency graph, inspects git history and decides every
// package's next version.
//
// Versioning rules:
//   - the package's own conventional commits since its last "pkg@version" tag
//     (or the whole history when never tagged) set OwnBump (fix=patch,
//     feat=minor, breaking=major; the highest wins);
//   - a consumer of one or more changed providers gets at least one patch
//     bump (a single patch, regardless of how many providers changed);
//   - a higher own bump always wins over the propagated patch;
//   - catch-up: a consumer whose provider's latest release tag is newer than
//     the consumer's own latest tag — or whose provider has a release while
//     the consumer was never released at all — also gets the patch bump.
//     This heals runs where the provider published but the consumer failed:
//     the next run schedules the missed consumer release automatically.
//
// Baseline resolution: the latest parseable tag wins. When the newest tag
// exists but its version cannot be parsed, or no tag exists at all, the
// baseline comes from initials (keyed by package name) and defaults to 0.0.0.
// With an unparseable newest tag, commits are still scanned from that tag.
//
// Graph work is O((V+E) log V); git is queried once per package.
func Compute(ctx context.Context, git gitx.Git, pkgs []*model.Package, deps []model.Dependency, initials map[string]semver.Version) (*Plan, error) {
	g := graph.New()
	for _, p := range pkgs {
		g.AddNode(p.Name)
	}
	providers := make(map[string][]string)
	consumers := make(map[string][]string)
	seen := make(map[model.Dependency]bool)
	for _, d := range deps {
		if seen[d] { // tolerate duplicate config entries
			continue
		}
		seen[d] = true
		if err := g.AddEdge(d.Provider, d.Consumer); err != nil {
			return nil, err
		}
		providers[d.Consumer] = append(providers[d.Consumer], d.Provider)
		consumers[d.Provider] = append(consumers[d.Provider], d.Consumer)
	}
	order, err := g.TopoSort()
	if err != nil {
		return nil, err
	}

	releases := make(map[string]*Release, len(pkgs))
	for _, p := range pkgs {
		rel := &Release{Pkg: p}
		tag, found, err := git.LatestTag(ctx, p.Name)
		if err != nil {
			return nil, fmt.Errorf("plan: %s: %w", p.Name, err)
		}
		since := ""
		switch {
		case found && tag.Parsed:
			rel.Current, rel.Tagged, since = tag.Version, true, tag.Name
			rel.TagCreated = tag.Created
		case found: // newest tag exists but is unparseable: scan from it,
			// take the baseline from initials (default 0.0.0).
			since = tag.Name
			if init, ok := initials[p.Name]; ok {
				rel.Current, rel.FromInitials = init, true
			}
		default: // never tagged
			if init, ok := initials[p.Name]; ok {
				rel.Current, rel.FromInitials = init, true
			}
		}
		subjects, err := git.Subjects(ctx, since)
		if err != nil {
			return nil, fmt.Errorf("plan: %s: %w", p.Name, err)
		}
		for _, s := range subjects {
			c := conventional.Parse(s)
			if c.Kind == conventional.KindOther || c.Scope != p.Name {
				continue
			}
			rel.Commits = append(rel.Commits, c)
			rel.OwnBump = semver.Max(rel.OwnBump, c.Kind.Bump())
		}
		rel.Bump = rel.OwnBump
		releases[p.Name] = rel
	}

	// Propagate provider changes in topological order: providers are final
	// before any of their consumers is visited.
	for _, name := range order {
		rel := releases[name]
		for _, prov := range providers[name] {
			pr := releases[prov]
			switch {
			case pr.Changed():
				rel.Bump = semver.Max(rel.Bump, semver.BumpPatch)
				rel.DueTo = append(rel.DueTo, prov)
			case pr.Tagged && (!rel.Tagged || pr.TagCreated > rel.TagCreated):
				// Catch-up: the provider's latest release is newer than this
				// package's own latest release (or this package was never
				// released while the provider has been) — the consumer missed
				// a provider update, e.g. it failed in the run that published
				// the provider. Schedule the pending patch release now.
				rel.Bump = semver.Max(rel.Bump, semver.BumpPatch)
				rel.DueTo = append(rel.DueTo, prov)
			}
		}
		if rel.Changed() {
			rel.Next = rel.Current.Bumped(rel.Bump)
		} else {
			rel.Next = rel.Current
		}
	}

	return &Plan{Order: order, Releases: releases, Providers: providers, Consumers: consumers}, nil
}
