// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// ---------------------------------------------------------------------------
// §13.1 workspace
// ---------------------------------------------------------------------------

func (cp *computation) loadWorkspace(deps []model.Dependency) error {
	g := graph.New()
	for _, p := range cp.pkgs {
		cp.byName[p.Name] = p
		cp.byFold[globx.Fold(p.Name)] = p.Name
		g.AddNode(p.Name)
	}
	cp.providers = make(map[string][]string)
	cp.edges = make(map[string][]edge)

	seen := make(map[model.Dependency]bool)
	for _, d := range deps {
		if seen[d] { // tolerate duplicate config entries
			continue
		}
		seen[d] = true
		if err := g.AddEdge(d.Provider, d.Consumer); err != nil {
			return err
		}
		cp.providers[d.Consumer] = append(cp.providers[d.Consumer], d.Provider)
		cp.edges[d.Provider] = append(cp.edges[d.Provider], edge{to: d.Consumer, kind: d.Kind})
	}
	// Deterministic traversal order (§17.2): the BFS of §9.2 marks each
	// package seen once, so the order it meets them must not depend on map
	// iteration or on the order of the config file.
	for _, es := range cp.edges {
		sort.Slice(es, func(i, j int) bool { return es[i].to < es[j].to })
	}

	order, err := g.TopoSort()
	if err != nil {
		// §16 E200: a cyclic graph has no publish order — the run cannot
		// produce a correct plan, and the code must surface as a diagnostic
		// (not a bare load failure) so operators and tooling can key off it.
		// §13.1 also requires the cycle report to name the manifest field
		// carrying each edge, so the message lists the edges among the
		// blocked nodes with their kinds.
		msg := err.Error()
		var cyc *graph.CycleError
		if errors.As(err, &cyc) {
			members := make(map[string]bool, len(cyc.Nodes))
			for _, n := range cyc.Nodes {
				members[n] = true
			}
			var edges []string
			for d := range seen {
				if members[d.Consumer] && members[d.Provider] {
					edges = append(edges, fmt.Sprintf("%s -> %s (%s)", d.Consumer, d.Provider, d.Kind))
				}
			}
			sort.Strings(edges)
			msg += "; edges: " + strings.Join(edges, ", ")
		}
		cp.err(CodeDependencyCycle, "", "", msg)
		return errFatalPlan
	}
	cp.order = order
	return nil
}

// errFatalPlan signals that a repository-scoped error was recorded in
// cp.diags: no correct plan exists, and Compute returns the diagnostics as a
// fatal plan instead of an ordinary error, so the §16 code reaches the
// caller's diagnostics stream.
var errFatalPlan = errors.New("plan: repository-scoped error")

// fatalPlan is the plan of a repository where no correct plan exists: the
// recorded diagnostics — at least one of them repository-scoped, making
// Fatal() true — and nothing releasable.
func (cp *computation) fatalPlan() *Plan {
	return &Plan{
		Releases:    map[string]*Release{},
		Providers:   map[string][]string{},
		Diagnostics: cp.diags,
	}
}
