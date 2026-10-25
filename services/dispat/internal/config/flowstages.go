// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package config

// The `flow` key beyond its script lists: the object form of a stage entry,
// the stages a flow defines, and the order every configured stage of a package
// runs in.
//
// The shape is read by pkg/models' own normaliser, so a config file and the
// public type cannot come to disagree about what a flow is; what this file
// adds is the CLI's weak list reading, the resolution of the entries onto the
// domain Space, and the refusal of an order the engine cannot run, decided
// here, as the configuration is read, so that `release`, `status` and `run`
// all stop before any command with the same code.

import (
	"fmt"
	"sort"
	"strings"

	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// flowObject fills a `flow` object at any of the four levels that carry one.
func flowObject(dst **SpaceFlowConfig) setter {
	return func(val any, at string) error {
		object, isObject := val.(map[string]any)
		if !isObject {
			return wants(at, "an object")
		}
		flow, err := public.NormalizeFlow(object, at, readFlowList)
		if err != nil {
			return err
		}
		*dst = flow
		return nil
	}
}

// readFlowList reads one entry's script list the way every list key of the
// config language is read: a scalar is the one-element list it stands for.
func readFlowList(val any, at string) ([]string, error) {
	var list []string
	if err := strs(&list)(val, at); err != nil {
		return nil, err
	}
	return list, nil
}

// flowStageRefs are the script references the stages a flow defines make,
// labelled for error messages the way scriptRefs labels the typed entries.
func flowStageRefs(flow *SpaceFlowConfig) map[string][]string {
	refs := make(map[string][]string)
	for name, stage := range flow.Stages {
		if stage.Script != nil {
			refs["flow."+name] = stage.Script
		}
	}
	for hook, list := range flow.StageHooks {
		refs["flow."+hook] = list
	}
	return refs
}

// hasStageEntry reports whether a flow gives a built-in stage a script or a
// hook. A separate placed object can retain a commandless stage's constraints.
func hasStageEntry(script, before, after []string) bool {
	return len(script)+len(before)+len(after) > 0
}

// userStageNames lists the stages a flow defines, sorted: every stage entry
// that is not a built-in one, and every stage a hook brackets.
func userStageNames(flow *SpaceFlowConfig) []string {
	seen := make(map[string]bool)
	for name := range flow.Stages {
		if !public.IsBuiltInFlowStage(name) {
			seen[name] = true
		}
	}
	for hook := range flow.StageHooks {
		if stage, _, isHook := public.FlowHookStage(hook); isHook {
			seen[stage] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// userStageHooks answers the two hook entries of one defined stage.
func userStageHooks(flow *SpaceFlowConfig, name string) (before, after []string) {
	title := public.FlowStageTitle(name)
	return flow.StageHooks["before"+title], flow.StageHooks["post"+title]
}

// resolveUserStages maps the stages a flow defines onto the domain, in name
// order, with every reference resolved through the package's scope.
func resolveUserStages(scope scriptScope, flow *SpaceFlowConfig) []model.UserStage {
	var stages []model.UserStage
	for _, name := range userStageNames(flow) {
		before, after := userStageHooks(flow, name)
		stages = append(stages, model.UserStage{
			Name:       name,
			Commands:   scope.commands(flow.Stages[name].Script),
			BeforeHook: scope.commands(before),
			PostHook:   scope.commands(after),
		})
	}
	return stages
}

// resolveStageLabels collects the labels the flow gives its stages, keyed by
// the name each stage runs under: the version stage's label sits under
// whichever of its two names configured it.
func resolveStageLabels(sc SpaceConfig) map[string]string {
	labels := make(map[string]string)
	for name, stage := range sc.Flow.Stages {
		if stage.Label == "" {
			continue
		}
		if name == public.FlowStageVersion || name == public.FlowStagePropagate {
			name = resolveVersionStage(sc).String()
		}
		labels[name] = stage.Label
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

// resolveStageGraph resolves the package graph of every stage the package
// has (§13.11): the version stage and the build, which every package has, the
// publish where the flow configures one (a package without one has the
// `none` target), the sign, syncLock, test and announce stages where the
// configuration gives it one, and the stages the flow defines. The version
// stage goes in under its canonical name whatever key configured it. The
// resolver reads either spelling of it in a constraint, and drops a built-in
// stage this package does not run, so one shared flow fits every target. The
// refusals come back as data, so the workspace is read whole before any
// package is refused.
func resolveStageGraph(sc SpaceConfig) (model.StageGraph, []model.StageDiagnostic) {
	flow := sc.Flow
	var specs []model.StageSpec
	add := func(name string, entry public.StageConfig) {
		specs = append(specs, model.StageSpec{Name: name, Before: entry.Before, After: entry.After, Parallel: entry.Parallel})
	}
	add(public.FlowStageVersion, versionStageEntry(flow))
	if hasSignEntry(sc) {
		add(public.FlowStageSign, flow.Stages[public.FlowStageSign])
	}
	if av, _ := resolveAutoVersionSetting(sc); av != nil && len(av.SyncLock) > 0 {
		add(public.FlowStageSyncLock, public.StageConfig{})
	}
	add(public.FlowStageBuild, flow.Stages[public.FlowStageBuild])
	for _, builtIn := range []struct {
		name                  string
		script, before, after []string
	}{
		{public.FlowStageTest, flow.Test, flow.BeforeTest, flow.PostTest},
		{public.FlowStagePublish, flow.Publish, flow.BeforePublish, flow.PostPublish},
		{public.FlowStageAnnounce, flow.Announce, flow.BeforeAnnounce, flow.PostAnnounce},
	} {
		if hasStageEntry(builtIn.script, builtIn.before, builtIn.after) ||
			flow.Stages[builtIn.name].IsPlaced() {
			add(builtIn.name, flow.Stages[builtIn.name])
		}
	}
	for _, name := range userStageNames(flow) {
		add(name, flow.Stages[name])
	}
	return model.ResolveStageGraph(specs)
}

// versionStageEntry is the version stage's object half under whichever of
// its two spellings the ladder left it.
func versionStageEntry(flow *SpaceFlowConfig) public.StageConfig {
	if entry, isStated := flow.Stages[public.FlowStagePropagate]; isStated {
		return entry
	}
	return flow.Stages[public.FlowStageVersion]
}

// hasSignEntry reports whether a space's packages run a sign stage: autoSign
// or any of the sign stage's three sequences.
func hasSignEntry(sc SpaceConfig) bool {
	flow := sc.Flow
	return sc.AutoSign.IsEnabled() || hasStageEntry(flow.Sign, flow.BeforeSign, flow.PostSign)
}

// StageGraphError is the refusal of a workspace whose configuration no order
// realises: every package's diagnostics, by package name byte-wise and then in
// the order §19.2a gives them. It carries the first diagnostic's code and the
// refusal's stable identifier, and prints every diagnostic.
type StageGraphError struct {
	Diagnostics []PackageStageDiagnostic
}

// PackageStageDiagnostic is one diagnostic with the package it belongs to.
type PackageStageDiagnostic struct {
	Package string
	model.StageDiagnostic
}

func (e *StageGraphError) Error() string {
	lines := make([]string, 0, len(e.Diagnostics))
	for _, d := range e.Diagnostics {
		lines = append(lines, fmt.Sprintf("package %q: %s", d.Package, d.Message))
	}
	return strings.Join(lines, "; ")
}

// DiagnosticCode is the first diagnostic's code.
func (e *StageGraphError) DiagnosticCode() string { return e.Diagnostics[0].Code }

// DiagnosticCategory is the refusal's stable identifier (§28.9).
func (e *StageGraphError) DiagnosticCategory() string { return e.Diagnostics[0].DiagnosticCategory() }

// CheckStageGraphs refuses a workspace in which some package's configuration
// no order realises (§13.11), whether or not the package is in the plan, so
// that a release, read-only planning and a command sweep stop before any
// effect alike. The diagnostics are ordered by package name byte-wise and
// then as each package's resolution ordered them.
func CheckStageGraphs(pkgs []*model.Package) error {
	var refused []PackageStageDiagnostic
	for _, pkg := range pkgs {
		for _, d := range pkg.Space.StageRefusals {
			refused = append(refused, PackageStageDiagnostic{Package: pkg.Name, StageDiagnostic: d})
		}
	}
	if len(refused) == 0 {
		return nil
	}
	sort.SliceStable(refused, func(i, j int) bool { return refused[i].Package < refused[j].Package })
	return &StageGraphError{Diagnostics: refused}
}

// replaceStages writes one layer's stage entries over what it inherited: a
// nearer level that states an entry replaces the whole inherited entry, its
// constraints included (§13.11), so an entry carries exactly what the nearest
// level that stated it wrote. The script half of a built-in stage lives in its
// typed field and is replaced beside the entry by mergeFlow.
func replaceStages(base, over map[string]public.StageConfig, restated map[string]bool) map[string]public.StageConfig {
	if len(over) == 0 && len(restated) == 0 {
		return base
	}
	out := make(map[string]public.StageConfig, len(base)+len(over))
	for name, stage := range base {
		if !restated[name] {
			out[name] = stage
		}
	}
	for name, stage := range over {
		out[name] = stage
	}
	return out
}

// overlayStageHooks folds one layer's user-stage hooks onto what it
// inherited, entry by entry like the built-in hooks.
func overlayStageHooks(base, over map[string][]string) map[string][]string {
	if len(over) == 0 {
		return base
	}
	out := make(map[string][]string, len(base)+len(over))
	for hook, list := range base {
		out[hook] = list
	}
	for hook, list := range over {
		out[hook] = list
	}
	return out
}
