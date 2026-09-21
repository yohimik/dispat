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
//
// The runner is the release's own rather than one assembled here: a build of a
// provider this run does not release still runs in this workspace, so a
// composed run's nested dispat has to see the same context and the same pins
// it would see in any other script of the same repository.
func (a *App) openDispatch(ctx context.Context, coordinator *execution.Coordinator,
	pl *plan.Plan, runner script.Runnerx) {
	coordinator.Start(ctx, execution.Dispatch{
		Concurrency:    a.cfg.Execution.ResolveConcurrency(),
		Sources:        a.resolveInputSources(pl),
		Inputs:         a.resolveOutputProviders(pl),
		Prepare:        a.resolvePreparedProviders(pl, runner),
		Shell:          a.resolveShell,
		OpenRepository: a.openRepository,
		Store:          a.git,
	})
}

// resolveOutputProviders answers, for one package, the providers whose build
// outputs its own build may read: the transitive provider closure over every
// dependency kind, narrowed to the packages that declare outputs, in the
// plan's dependency order.
//
// Every kind, because a build-only edge supplies bytes exactly as a runtime
// edge does: a package that generates types for its consumer is a provider
// whether or not the release rules propagate a bump along that edge. The
// planner's provider map is already the whole declared graph, so the closure
// is read from it rather than from the propagation kinds.
//
// Whether this run releases the provider narrows nothing and is reported
// instead. §28.5 requires a task's inputs to include every build dependency
// it reads, "including dependencies whose packages need no new release": a
// consumer compiles against the same folder either way, so a provider the run
// leaves alone is a provider the run has to build anyway, and saying so here
// is what lets the coordinator build it exactly once.
func (a *App) resolveOutputProviders(pl *plan.Plan) func(string) []execution.InputPackage {
	return func(packageName string) []execution.InputPackage {
		closure := map[string]bool{}
		collectProviderClosure(pl, packageName, closure)
		providers := make([]execution.InputPackage, 0, len(closure))
		for _, name := range pl.Order {
			rel := pl.Releases[name]
			if !closure[name] || rel == nil || len(rel.Pkg.Space.BuildOutputs) == 0 {
				continue
			}
			providers = append(providers, execution.InputPackage{
				Package:    name,
				Path:       relativeRepositoryPath(a.runAnchor(), rel.Pkg.Dir),
				IsPrepared: !rel.IsReleasing(),
			})
		}
		return providers
	}
}

// collectProviderClosure marks every package whose build outputs the build of
// name may read: its providers, directly or through another provider. The
// plan's graph is acyclic before anything is dispatched (a cycle is refused at
// planning), and the seen set makes a diamond cost one visit rather than two.
//
// A provider whose relation is `none` ends the path it is reached by (CCME
// §19.2a): that relation declares that a consumer's build reads nothing the
// provider builds, and what a package does not read it cannot pass on. Such a
// provider is left unmarked rather than marked and skipped, because another
// path with no `none` hop on it may still reach it.
func collectProviderClosure(pl *plan.Plan, name string, seen map[string]bool) {
	for _, provider := range pl.Providers[name] {
		if seen[provider] || !isBuildReadingProvider(pl, provider) {
			continue
		}
		seen[provider] = true
		collectProviderClosure(pl, provider, seen)
	}
}

// isBuildReadingProvider reports whether a consumer's build reads what this
// provider builds, which is every relation but `none`. A name the plan holds
// no release for reads as the default relation, as it does in the task graph.
func isBuildReadingProvider(pl *plan.Plan, provider string) bool {
	rel := pl.Releases[provider]
	return rel == nil || rel.Pkg.Space.ProviderRelation.IsBuildWaitingBuild()
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
