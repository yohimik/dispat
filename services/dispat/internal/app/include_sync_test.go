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
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// includeSyncClosing is a closing phase over two packages: web published,
// and core with the given outcome.
func includeSyncClosing(root string, core *release.Result) closingRecord {
	space := &model.Space{Name: "libs"}
	pl := &plan.Plan{Order: []string{"core", "web"}, Releases: map[string]*plan.Release{}}
	for _, name := range pl.Order {
		pl.Releases[name] = &plan.Release{Pkg: &model.Package{Name: name, Dir: filepath.Join(root, name), Space: space}}
	}
	pl.Releases["web"].Pkg.Changelog = model.ChangelogSpec{Enabled: true, File: "../docs/web.md"}
	return closingRecord{plan: pl, results: map[string]*release.Result{
		"core": core,
		"web":  {Name: "web", Status: release.StatusPublished, IsPrepared: true},
	}}
}

// TestIsIncludeSyncNeeded: the shared include paths are re-synchronized only
// for a single history making release commits with include paths, when a
// package prepared its release files and did not publish while another did.
// Every other run keeps the closing phase exactly as it was.
func TestIsIncludeSyncNeeded(t *testing.T) {
	root := filepath.FromSlash("/work/repo")
	withInclude := &config.File{Commit: &config.CommitConfig{Enabled: models.Bool(true), Include: []string{"lock.txt"}}}
	app := &App{root: root, cfg: withInclude, log: zerolog.Nop()}
	failedPrepared := &release.Result{Name: "core", Status: release.StatusFailed, IsPrepared: true}

	assert.True(t, app.isIncludeSyncNeeded(includeSyncClosing(root, failedPrepared)))
	assert.True(t, app.isIncludeSyncNeeded(includeSyncClosing(root,
		&release.Result{Name: "core", Status: release.StatusSkipped, IsPrepared: true})), "a skipped writer")
	assert.False(t, app.isIncludeSyncNeeded(includeSyncClosing(root,
		&release.Result{Name: "core", Status: release.StatusFailed})), "a failure before anything was prepared")
	assert.False(t, app.isIncludeSyncNeeded(includeSyncClosing(root,
		&release.Result{Name: "core", Status: release.StatusPublished, IsPrepared: true})), "everything published")

	nothingPublished := includeSyncClosing(root, failedPrepared)
	nothingPublished.results["web"].Status = release.StatusFailed
	assert.False(t, app.isIncludeSyncNeeded(nothingPublished), "no release commit to protect")

	fleet := includeSyncClosing(root, failedPrepared)
	fleet.fleet = &workspaceRecorder{}
	assert.False(t, app.isIncludeSyncNeeded(fleet), "a fleet commits its include paths during the run")

	noInclude := &App{root: root, log: zerolog.Nop(),
		cfg: &config.File{Commit: &config.CommitConfig{Enabled: models.Bool(true)}}}
	assert.False(t, noInclude.isIncludeSyncNeeded(includeSyncClosing(root, failedPrepared)))
	commitOff := &App{root: root, log: zerolog.Nop(),
		cfg: &config.File{Commit: &config.CommitConfig{Include: []string{"lock.txt"}}}}
	assert.False(t, commitOff.isIncludeSyncNeeded(includeSyncClosing(root, failedPrepared)))
}

// TestFormatPublishedExclusionsCoversChangelogsOutsideTheFolder: a restore
// of shared paths leaves every published folder alone, and the published
// changelog too where it resolves outside that folder.
func TestFormatPublishedExclusionsCoversChangelogsOutsideTheFolder(t *testing.T) {
	root := filepath.FromSlash("/work/repo")
	closing := includeSyncClosing(root, &release.Result{Name: "core", Status: release.StatusFailed, IsPrepared: true})

	assert.Equal(t, []string{filepath.Join(root, "web"), filepath.Join(root, "docs", "web.md")},
		formatPublishedExclusions(closing.plan, closing.results))
	assert.Equal(t, []string{filepath.Join(root, "core")}, resolveUnpublishedPrepared(closing))
}
