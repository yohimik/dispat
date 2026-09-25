// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// runOwnedPlan is a workspace of five packages: the repository root as a
// package, a parent folder holding a nested package, and two plain packages
// whose folder names share a prefix.
func runOwnedPlan(root string) *plan.Plan {
	space := &model.Space{Name: "libs"}
	folders := map[string]string{
		"site":  root,
		"tools": filepath.Join(root, "tools"),
		"cli":   filepath.Join(root, "tools", "cli"),
		"core":  filepath.Join(root, "packages", "core"),
		"corex": filepath.Join(root, "packages", "corex"),
	}
	pl := &plan.Plan{Releases: map[string]*plan.Release{}}
	for name, dir := range folders {
		pl.Releases[name] = &plan.Release{Pkg: &model.Package{Name: name, Dir: dir, Space: space}}
	}
	return pl
}

// TestResolveRunOwnedFolder: in commit mode a package's folder is the run's
// own, and a skipped package there is restored, unless the folder is the
// repository root or holds another package's folder. A folder sharing only a
// name prefix with another is not a parent of it. Without release commits no
// folder is the run's own, because nothing proved it clean at the start.
func TestResolveRunOwnedFolder(t *testing.T) {
	root := filepath.FromSlash("/work/repo")
	pl := runOwnedPlan(root)
	commitOn := &config.File{Commit: &config.CommitConfig{Enabled: models.Bool(true)}}
	owned := (&App{root: root, cfg: commitOn, log: zerolog.Nop()}).resolveRunOwnedFolder(pl, nil)

	assert.True(t, owned(pl.Releases["core"]), "a plain package folder in commit mode")
	assert.True(t, owned(pl.Releases["cli"]), "a nested package holding nothing of its own")
	assert.False(t, owned(pl.Releases["site"]), "the repository root")
	assert.False(t, owned(pl.Releases["tools"]), "a folder containing another package's folder")

	commitOff := &config.File{Commit: &config.CommitConfig{Enabled: models.Bool(false)}}
	notOwned := (&App{root: root, cfg: commitOff, log: zerolog.Nop()}).resolveRunOwnedFolder(pl, nil)
	for name, rel := range pl.Releases {
		assert.False(t, notOwned(rel), "%s: without release commits nothing proved the folder clean", name)
	}
}
