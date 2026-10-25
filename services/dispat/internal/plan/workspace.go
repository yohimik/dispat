// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// ---------------------------------------------------------------------------
// §13.1 workspace
// ---------------------------------------------------------------------------

func (cp *computation) loadWorkspace(opts Options) error {
	orderGraph := graph.New()
	configuredGraph := graph.New()
	for _, p := range cp.pkgs {
		cp.byName[p.Name] = p
		cp.byFold[globx.Fold(p.Name)] = p.Name
		orderGraph.AddNode(p.Name)
		configuredGraph.AddNode(p.Name)
	}
	cp.providers = make(map[string][]string)
	cp.publishProviders = make(map[string][]string)
	cp.blockingProviders = make(map[string][]string)
	cp.edges = make(map[string][]edge)
	propagateKinds := opts.ParserConfig.Propagation.Kinds
	if propagateKinds == nil {
		propagateKinds = ccme.DefaultPropagateKinds()
	}
	publishKinds := opts.PublishOrderKinds
	if publishKinds == nil {
		publishKinds = []model.DepKind{
			model.KindDependencies, model.KindPeerDependencies, model.KindOptionalDependencies,
		}
	}
	blockingKinds := opts.PublishBlockingKinds
	if blockingKinds == nil {
		blockingKinds = []model.DepKind{model.KindDependencies, model.KindPeerDependencies}
	}
	wantsPropagation := func(kind model.DepKind) bool {
		for _, selected := range propagateKinds {
			if selected == ccme.KindAll || string(selected) == kind.String() {
				return true
			}
		}
		return false
	}
	wantsPublication := func(kind model.DepKind, selectedKinds []model.DepKind) bool {
		for _, selected := range selectedKinds {
			if selected == kind {
				return true
			}
		}
		return false
	}
	wantsBlocking := func(kind model.DepKind, selectedKinds []model.DepKind) bool {
		for _, selected := range selectedKinds {
			if selected == kind {
				return true
			}
		}
		return false
	}

	seen := make(map[model.Dependency]bool)
	var orderEdges, configuredEdges []model.Dependency
	for _, d := range opts.Dependencies {
		if seen[d] {
			continue
		}
		seen[d] = true
		cp.providers[d.Consumer] = append(cp.providers[d.Consumer], d.Provider)
		cp.edges[d.Provider] = append(cp.edges[d.Provider], edge{to: d.Consumer, kind: d.Kind})
		consumer := cp.byName[d.Consumer]
		if consumer == nil {
			return fmt.Errorf("dependency consumer %q is absent from the workspace", d.Consumer)
		}
		consumerPublishKinds := publishKinds
		consumerBlockingKinds := blockingKinds
		if consumer.Space != nil {
			if consumer.Space.PublishOrderKindsSet {
				consumerPublishKinds = consumer.Space.PublishOrderKinds
			}
			if consumer.Space.PublishBlockingKindsSet {
				consumerBlockingKinds = consumer.Space.PublishBlockingKinds
			}
		}
		publish := wantsPublication(d.Kind, consumerPublishKinds)
		if publish {
			cp.publishProviders[d.Consumer] = append(cp.publishProviders[d.Consumer], d.Provider)
		}
		if wantsBlocking(d.Kind, consumerBlockingKinds) {
			cp.blockingProviders[d.Consumer] = append(cp.blockingProviders[d.Consumer], d.Provider)
		}
		propagate := wantsPropagation(d.Kind)
		if publish || propagate {
			if err := orderGraph.AddEdge(d.Provider, d.Consumer); err != nil {
				return err
			}
			orderEdges = append(orderEdges, d)
		}
		provider := cp.byName[d.Provider]
		if provider == nil {
			return fmt.Errorf("dependency provider %q is absent from the workspace", d.Provider)
		}
		buildWaiting := provider.Space == nil || provider.Space.ProviderRelation.IsBuildWaitingBuild()
		if publish || propagate || buildWaiting {
			if err := configuredGraph.AddEdge(d.Provider, d.Consumer); err != nil {
				return err
			}
			configuredEdges = append(configuredEdges, d)
		}
	}
	for _, es := range cp.edges {
		sort.Slice(es, func(i, j int) bool { return es[i].to < es[j].to })
	}
	for _, names := range cp.publishProviders {
		sort.Strings(names)
	}
	for _, names := range cp.blockingProviders {
		sort.Strings(names)
	}

	order, err := orderGraph.TopoSort()
	if err != nil {
		cp.err(CodeDependencyCycle, "", "", cycleMessage("dependency cycle", cp.pkgs, orderEdges))
		return errFatalPlan
	}
	cp.order = order
	if _, err := configuredGraph.TopoSort(); err != nil {
		cp.err(CodeStageWorkspaceCycle, "", "", cycleMessage(
			"unrealizable execution configuration", cp.pkgs, configuredEdges))
		return errFatalPlan
	}
	return nil
}

// cycleMessage uses strongly connected components: a failed topological sort's
// blocked tail also contains descendants that are not on a cycle.
func cycleMessage(prefix string, pkgs []*model.Package, edges []model.Dependency) string {
	adj := make(map[string][]string, len(pkgs))
	for _, d := range edges {
		adj[d.Provider] = append(adj[d.Provider], d.Consumer)
	}
	index := 0
	indices := make(map[string]int, len(pkgs))
	low := make(map[string]int, len(pkgs))
	onStack := make(map[string]bool, len(pkgs))
	var stack []string
	cycle := make(map[string]bool)
	cyclicComponent := make(map[string]int)
	componentID := 0
	var visit func(string)
	visit = func(name string) {
		index++
		indices[name], low[name] = index, index
		stack = append(stack, name)
		onStack[name] = true
		for _, next := range adj[name] {
			if indices[next] == 0 {
				visit(next)
				if low[next] < low[name] {
					low[name] = low[next]
				}
			} else if onStack[next] && indices[next] < low[name] {
				low[name] = indices[next]
			}
		}
		if low[name] != indices[name] {
			return
		}
		var component []string
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			component = append(component, member)
			if member == name {
				break
			}
		}
		if len(component) > 1 {
			componentID++
			for _, member := range component {
				cycle[member] = true
				cyclicComponent[member] = componentID
			}
		} else {
			for _, next := range adj[name] {
				if next == name {
					componentID++
					cycle[name] = true
					cyclicComponent[name] = componentID
				}
			}
		}
	}
	for _, p := range pkgs {
		if indices[p.Name] == 0 {
			visit(p.Name)
		}
	}
	var names, fields []string
	for name := range cycle {
		names = append(names, name)
	}
	for _, d := range edges {
		if cyclicComponent[d.Provider] != 0 && cyclicComponent[d.Provider] == cyclicComponent[d.Consumer] {
			fields = append(fields, fmt.Sprintf("%s -> %s (%s)", d.Consumer, d.Provider, d.Kind))
		}
	}
	sort.Strings(names)
	sort.Strings(fields)
	return fmt.Sprintf("%s: packages: %s; manifest edges: %s", prefix,
		strings.Join(names, ", "), strings.Join(fields, ", "))
}

// errFatalPlan signals that a repository-scoped error was recorded in
// cp.diags: no correct plan exists, and Compute returns the diagnostics as a
// fatal plan instead of an ordinary error, so the §16 code reaches the
// caller's diagnostics stream.
var errFatalPlan = errors.New("plan: repository-scoped error")

// fatalPlan is the plan of a repository where no correct plan exists: the
// recorded diagnostics and nothing releasable.
func (cp *computation) fatalPlan() *Plan {
	return &Plan{
		Releases:    map[string]*Release{},
		Providers:   map[string][]string{},
		Diagnostics: cp.diags,
	}
}
