// Package plan reads git history and computes the release plan: which
// packages changed, what their next versions are, and in which order they must
// be processed.
package plan

import (
	"context"
	"fmt"

	"github.com/yohimik/monorel/internal/conventional"
	"github.com/yohimik/monorel/internal/gitx"
	"github.com/yohimik/monorel/internal/graph"
	"github.com/yohimik/monorel/internal/model"
	"github.com/yohimik/monorel/internal/semver"
)

// Release describes what (if anything) will happen to one package.
type Release struct {
	Pkg     *model.Package
	Current semver.Version // zero value when the package was never tagged
	Tagged  bool           // whether a previous release tag exists
	OwnBump semver.Bump    // bump demanded by the package's own commits
	Bump    semver.Bump    // final bump including provider propagation
	Next    semver.Version // version to release; equals Current when unchanged
	Commits []conventional.Commit
	DueTo   []string // changed providers that forced (at least) a patch bump
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
//   - a higher own bump always wins over the propagated patch.
//
// Graph work is O((V+E) log V); git is queried once per package.
func Compute(ctx context.Context, git gitx.Git, pkgs []*model.Package, deps []model.Dependency) (*Plan, error) {
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
		if found {
			rel.Current, rel.Tagged, since = tag.Version, true, tag.Name
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
			if releases[prov].Changed() {
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
