// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestCompiledRelevantPathsIncludeSharedLockfile(t *testing.T) {
	root := t.TempDir()
	rel := &plan.Release{Pkg: &model.Package{Name: "app", Dir: filepath.Join(root, "packages", "app")}}
	pl := &plan.Plan{Releases: map[string]*plan.Release{"app": rel}}
	a := &App{cfg: &config.File{Commit: &config.CommitConfig{Include: []string{"shared.lock"}}}}
	inputs := a.newRelevantInputs(pl, nil, &execution.Coordinator{GraphRootOID: "signed-root"})
	pathspecs := inputs.formatRelevantPathspecs(root, rel)
	require.Contains(t, pathspecs, "packages/app")
	require.Contains(t, pathspecs, "shared.lock")
	require.NotContains(t, pathspecs, "unrelated.txt")
}

func TestCompiledOutputExceptionKeepsOverlappingSharedIncludesRelevant(t *testing.T) {
	a := &App{cfg: &config.File{Commit: &config.CommitConfig{
		Include: []string{"packages/docs/dist/shared.lock"}}}}
	inputs := &relevantInputs{app: a}
	require.True(t, inputs.sharedIncludeOverlapsOutputRoot("packages/docs/dist"))
	require.False(t, inputs.sharedIncludeOverlapsOutputRoot("packages/ui/dist"))
	a.cfg.Commit.Include = []string{"shared.lock"}
	require.False(t, inputs.sharedIncludeOverlapsOutputRoot("packages/docs/dist"))
	a.cfg.Commit.Include = []string{"."}
	require.True(t, inputs.sharedIncludeOverlapsOutputRoot("packages/docs/dist"),
		"a repository-wide include must keep generated paths under the native source fence")
	a.cfg.Commit.Include = []string{"./"}
	require.True(t, inputs.sharedIncludeOverlapsOutputRoot("packages/docs/dist"))
}
