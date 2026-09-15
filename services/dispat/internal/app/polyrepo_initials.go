// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// configurationsByRepository indexes declaration ownership once per operation.
// Central source packages use control defaults; imports use their own defaults.
func (a *App) configurationsByRepository() map[string]*config.File {
	if a.workspace == nil {
		return nil
	}
	owners := make(map[string]*config.File, len(a.workspace.Repositories))
	for _, repo := range a.workspace.Repositories {
		cfg := a.cfg
		if repo.Imported && repo.Config != nil {
			cfg = repo.Config
		}
		owners[repo.Name] = cfg
	}
	return owners
}

// workspaceInitialVersions joins package names and initials per declaring
// config in linear space and time, without rescanning every initials map for
// every package. Missing entries are reported against their owning config.
func (a *App) workspaceInitialVersions(pkgs []*model.Package) map[string]ccme.Version {
	owners := a.configurationsByRepository()
	names := make(map[*config.File]map[string]string)
	for _, p := range pkgs {
		cfg := owners[p.Repository]
		if cfg == nil {
			continue
		}
		if names[cfg] == nil {
			names[cfg] = make(map[string]string)
		}
		names[cfg][strings.ToLower(p.Name)] = p.Name
	}
	out := make(map[string]ccme.Version)
	for _, repo := range a.workspace.Repositories {
		if (!repo.Control && !repo.Imported) || repo.Config == nil {
			continue
		}
		for key, version := range repo.Config.InitialVersions {
			if name, exists := names[repo.Config][strings.ToLower(key)]; exists {
				out[name] = version
			} else {
				a.log.Warn().Str("package", key).Str("repository", repo.Name).
					Msg("initials entry matches no discovered package owned by this config, ignoring")
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
