// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// fleetGroup holds a semantic declaration and its owner for conflict reporting.
// Repository paths, scripts and defaults never travel with the declaration.
type fleetGroup struct {
	rule  VersionGroupConfig
	owner string
	name  string
}

type fleetGroups map[string]fleetGroup

// foldGroupName uses the same Unicode equivalence as EqualFold references.
// Lowercasing alone leaves the two Greek lowercase sigmas in different groups.
func foldGroupName(name string) string {
	return strings.Map(func(r rune) rune {
		smallest := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < smallest {
				smallest = next
			}
		}
		return unicode.ToLower(smallest)
	}, name)
}

func normalizeGroupRule(rule VersionGroupConfig) VersionGroupConfig {
	rule.Counter = model.Sharing(rule.Counter).String()
	rule.Channels = model.Sharing(rule.Channels).String()
	return rule
}

func (groups fleetGroups) add(name string, declaration fleetGroup) error {
	key := foldGroupName(name)
	declaration.rule = normalizeGroupRule(declaration.rule)
	declaration.name = name
	if previous, ok := groups[key]; ok && previous.rule != declaration.rule {
		return WithDiagnostic(DiagnosticComposition, fmt.Errorf("linked fleet: version group %q has conflicting policies in %s and %s (semver/counter/channels: %s/%s/%s versus %s/%s/%s)", key,
			previous.owner, declaration.owner, previous.rule.Versioning, previous.rule.Counter, previous.rule.Channels,
			declaration.rule.Versioning, declaration.rule.Counter, declaration.rule.Channels))
	}
	if _, ok := groups[key]; !ok {
		groups[key] = declaration
	}
	return nil
}

// resolveLinkedGroups builds one namespace from participating peers. Resolve
// folder overrides before collecting implicit space groups, using the same
// ownership boundary as package discovery. Copies keep each owner's original
// configuration intact for subsequent commands and configuration writes.
func resolveLinkedGroups(workspace *Workspace, gitRoots *gitRootMemo) (map[string]*discovery, error) {
	discoveries := make(map[string]*discovery, len(workspace.Repositories))
	groups := fleetGroups{}
	repositories := slices.Clone(workspace.Repositories)
	slices.SortFunc(repositories, func(a, b Repository) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	for _, repo := range repositories {
		policy := newWorkspaceFolderPolicy(workspace, &repo, gitRoots)
		discovery, err := newDiscoveryMode(repo.Config, repo.Root, policy.allow)
		if err != nil {
			return nil, err
		}
		for _, name := range sortedSpaceNames(repo.Config) {
			if _, _, _, _, err := discovery.resolveSpaceConfig(name); err != nil {
				return nil, err
			}
		}
		discoveries[repo.Name] = discovery
		resolved := discovery.spaceConfigs
		for _, name := range slices.Sorted(maps.Keys(repo.Config.VersionGroups)) {
			if err := groups.add(name, fleetGroup{rule: repo.Config.VersionGroups[name], owner: fmt.Sprintf("repository %q", repo.Name)}); err != nil {
				return nil, err
			}
		}
		for _, name := range slices.Sorted(maps.Keys(resolved)) {
			space := resolved[name]
			if space.VersionGroup != "" || !model.Versioning(space.Versioning).IsShared() {
				continue
			}
			if err := groups.add(name, fleetGroup{rule: VersionGroupConfig{Versioning: space.Versioning}, owner: fmt.Sprintf("repository %q space %q", repo.Name, name)}); err != nil {
				return nil, err
			}
		}
	}
	for _, repo := range repositories {
		copied := *repo.Config
		copied.VersionGroups = make(map[string]VersionGroupConfig, len(groups))
		for _, group := range groups {
			copied.VersionGroups[group.name] = group.rule
		}
		discoveries[repo.Name].c = &copied
	}
	return discoveries, nil
}
