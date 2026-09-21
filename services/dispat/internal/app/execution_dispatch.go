// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// What a distributed run hands its coordinator before it executes anything.
//
// Three questions belong to this package and to nowhere else: which
// repositories one package's build actually reads, where each of them lives on
// this machine, and which shell a folder's commands run through. They are the
// workspace's own answers, so they are answered here and passed as functions;
// handing the transport the workspace would be handing it a planner.
//
// With no worker links every function here returns before it does anything,
// which is what keeps a repository that never asked for distributed execution
// paying nothing for it.

import (
	"context"
	"path/filepath"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// openDispatch starts the coordinator's pollers and gives it the run's own
// answers about repositories, folders and shells.
func (a *App) openDispatch(ctx context.Context, coordinator *execution.Coordinator, pl *plan.Plan) {
	coordinator.Start(ctx, execution.Dispatch{
		Concurrency:    a.cfg.Execution.ResolveConcurrency(),
		Sources:        a.resolveInputSources(pl),
		Inputs:         a.resolveOutputProviders(pl),
		Shell:          a.resolveShell,
		OpenRepository: a.openRepository,
		Store:          a.git,
	})
}

// resolveOutputProviders answers, for one package, the providers whose build
// outputs its own build may read: the transitive provider closure over every
// dependency kind, narrowed to the packages this run releases and that declare
// outputs, in the plan's dependency order.
//
// Every kind, because a build-only edge supplies bytes exactly as a runtime
// edge does: a package that generates types for its consumer is a provider
// whether or not the release rules propagate a bump along that edge. The
// planner's provider map is already the whole declared graph, so the closure
// is read from it rather than from the propagation kinds.
func (a *App) resolveOutputProviders(pl *plan.Plan) func(string) []execution.InputPackage {
	return func(packageName string) []execution.InputPackage {
		closure := map[string]bool{}
		collectProviderClosure(pl, packageName, closure)
		providers := make([]execution.InputPackage, 0, len(closure))
		for _, name := range pl.Order {
			rel := pl.Releases[name]
			if !closure[name] || rel == nil || !rel.IsReleasing() || len(rel.Pkg.Space.BuildOutputs) == 0 {
				continue
			}
			providers = append(providers, execution.InputPackage{
				Package: name, Path: relativeRepositoryPath(a.runAnchor(), rel.Pkg.Dir),
			})
		}
		return providers
	}
}

// collectProviderClosure marks every package that is a provider of name,
// directly or through another provider. The plan's graph is acyclic before
// anything is dispatched (a cycle is refused at planning), and the seen set
// makes a diamond cost one visit rather than two.
func collectProviderClosure(pl *plan.Plan, name string, seen map[string]bool) {
	for _, provider := range pl.Providers[name] {
		if seen[provider] {
			continue
		}
		seen[provider] = true
		collectProviderClosure(pl, provider, seen)
	}
}

// runAnchor is the folder every path a node reproduces is relative to: the
// control root of a composed workspace, and the checkout itself otherwise.
func (a *App) runAnchor() string {
	if a.workspace == nil {
		return a.root
	}
	return a.workspace.ControlRoot
}

// resolveInputSources answers, for one package, the repositories its build
// reads and the planned head each of their prepared states descends from.
//
// A single history is one source with no name and no path: the checkout the
// run was started in, at the head the plan was computed against. A composed
// workspace is the package's own input closure (§28.3), so a node materializes
// what the package's history actually depends on rather than the whole fleet.
func (a *App) resolveInputSources(pl *plan.Plan) func(string) []execution.Source {
	if a.workspace == nil {
		root := []execution.Source{{Path: ".", Dir: a.root, Head: a.plannedHeads[""]}}
		return func(string) []execution.Source { return root }
	}
	return func(packageName string) []execution.Source {
		return a.composedInputSources(pl, packageName)
	}
}

// composedInputSources is the input closure of one package in a composed
// workspace, the repository that owns the package first so that every other
// checkout is created inside one that already exists.
func (a *App) composedInputSources(pl *plan.Plan, packageName string) []execution.Source {
	rel := pl.Releases[packageName]
	if rel == nil {
		return nil
	}
	words := pl.RepositoryInputs[packageName]
	sources := make([]execution.Source, 0, len(pl.RepositoryInputOrder))
	for index, name := range pl.RepositoryInputOrder {
		if index/64 >= len(words) || words[index/64]&(1<<uint(index%64)) == 0 {
			continue
		}
		repository := a.workspace.RepositoryByName(name)
		if repository == nil {
			continue
		}
		source := execution.Source{
			Name: name,
			Path: relativeRepositoryPath(a.workspace.ControlRoot, repository.Root),
			Dir:  repository.Root,
			Head: a.plannedHeads[name],
		}
		if name == rel.Pkg.Repository {
			sources = append([]execution.Source{source}, sources...)
			continue
		}
		sources = append(sources, source)
	}
	return sources
}

// relativeRepositoryPath is where one repository's checkout sits relative to
// the run's anchor, which is the layout a node reproduces.
func relativeRepositoryPath(anchor, root string) string {
	relative, err := filepath.Rel(anchor, root)
	if err != nil {
		return root
	}
	return filepath.ToSlash(relative)
}

// resolveShell is the interpreter a folder's commands run through: the
// configuration that owns the folder in a composed workspace, and this run's
// own everywhere else. It is the same question the package runner asks, so a
// command a node runs is started the way the orchestrator would have started
// it.
func (a *App) resolveShell(dir string) []string {
	shell := a.cfg.Shell
	if a.workspace != nil {
		if repository := a.workspace.RepositoryForDir(dir); repository != nil && repository.Config != nil {
			shell = repository.Config.Shell
		}
	}
	if len(shell) == 0 {
		return script.DefaultShell()
	}
	return shell
}

// openRepository opens the plumbing of one repository's working tree, so that
// a prepared input state is written into the object store it was captured
// from.
func (a *App) openRepository(dir string) *gitx.LocalGitx {
	if dir == a.root {
		return a.git
	}
	return &gitx.LocalGitx{Dir: dir, Log: a.log}
}
