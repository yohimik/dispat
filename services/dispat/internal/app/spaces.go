// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// A space keeps its owner's defaults even when another repository selects it.
// Unlike a version group, its name does not merge its scripts or directories.
type spaceOwner struct {
	name, root string
	file       *config.File
	space      config.SpaceConfig
	resolver   *spaceConfigResolver
}

// All declarations of one repository share a single resolution. A command
// naming another owner never reads this owner's folder files.
type spaceConfigResolver struct {
	root, owner string
	file        *config.File
	workspace   *config.Workspace
	once        sync.Once
	spaces      map[string]config.SpaceConfig
	err         error
}

func (r *spaceConfigResolver) resolve() (map[string]config.SpaceConfig, error) {
	r.once.Do(func() {
		r.spaces, r.err = config.ResolvedRepositorySpaceConfigs(r.file, r.root, r.workspace)
	})
	return r.spaces, r.err
}

func (s spaceOwner) resolveDirectory() string {
	return filepath.Join(s.root, filepath.FromSlash(s.space.Path.First()))
}

func (a *App) resolveSpaces() ([]spaceOwner, error) {
	a.spacesOnce.Do(func() {
		repositories := []config.Repository{{Root: a.root, Config: a.cfg, Control: true}}
		if a.workspace != nil {
			repositories = a.workspace.Repositories
		}
		for _, repo := range repositories {
			if !repo.Control && !repo.Imported {
				continue
			}
			if repo.Config == nil {
				a.spacesErr = fmt.Errorf("space configuration is missing for repository %q", repo.Name)
				return
			}
			resolver := &spaceConfigResolver{root: repo.Root, owner: repo.Name, file: repo.Config, workspace: a.workspace}
			for name, space := range repo.Config.Spaces {
				a.spaceCfgs = append(a.spaceCfgs, spaceOwner{
					name: name, root: repo.Root, file: repo.Config, space: space, resolver: resolver,
				})
			}
		}
		slices.SortFunc(a.spaceCfgs, func(x, y spaceOwner) int {
			if order := strings.Compare(x.name, y.name); order != 0 {
				return order
			}
			return strings.Compare(x.resolver.owner, y.resolver.owner)
		})
	})
	return a.spaceCfgs, a.spacesErr
}

// A named space retains entry precedence. A peer-only name resolves when it
// has one owner; an ambiguous peer name requires a package or current folder.
func (a *App) resolveSpace(loc Location) (spaceOwner, error) {
	spaces, err := a.resolveSpaces()
	if err != nil {
		return spaceOwner{}, err
	}
	var matches []spaceOwner
	for _, space := range spaces {
		if !strings.EqualFold(space.name, loc.name) {
			continue
		}
		if loc.owner != "" {
			if space.root == loc.owner {
				return space.resolveEffective()
			}
			continue
		}
		if space.file == a.cfg {
			return space.resolveEffective()
		}
		matches = append(matches, space)
	}
	if len(matches) == 1 {
		return matches[0].resolveEffective()
	}
	if len(matches) > 1 {
		return spaceOwner{}, fmt.Errorf("space %q belongs to multiple peer repositories; use a package subject or run from the space with --for cwd", loc.name)
	}
	return spaceOwner{}, fmt.Errorf("unknown space %q", loc.name)
}

func (s spaceOwner) resolveEffective() (spaceOwner, error) {
	spaces, err := s.resolver.resolve()
	if err != nil {
		return spaceOwner{}, err
	}
	space, ok := spaces[s.name]
	if !ok {
		return spaceOwner{}, fmt.Errorf("space %q is missing from repository %q", s.name, s.resolver.owner)
	}
	s.space = space
	return s, nil
}
