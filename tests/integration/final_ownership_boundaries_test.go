// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final production-review cases for package ownership in a composed
// workspace. Every fixture uses real repositories and asks status to compose
// the complete package graph, so a refusal also proves ownership discovery is
// fail-closed before build, tag, or repository mutation can begin.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

type finalOwnershipSnapshot struct {
	controlHead string
	sourceHeads map[string]string
}

func snapshotFinalOwnership(control *harness.Repo, sources ...string) finalOwnershipSnapshot {
	snapshot := finalOwnershipSnapshot{
		controlHead: control.Git("rev-parse", "HEAD"),
		sourceHeads: make(map[string]string, len(sources)),
	}
	for _, source := range sources {
		snapshot.sourceHeads[source] = control.Git("-C", source, "rev-parse", "HEAD")
	}
	return snapshot
}

func assertFinalOwnershipRefusal(t *testing.T, control *harness.Repo, before finalOwnershipSnapshot, want string) {
	t.Helper()
	res := control.Status()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.True(t, harness.IsCodePresent(res.Events, "E331"), "events: %#v\n%s", res.Events, combined)
	var diagnostic string
	for _, event := range res.Events {
		diagnostic += event.Str("error")
	}
	assert.Contains(t, diagnostic, want, "events: %#v", res.Events)
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, before.controlHead, control.Git("rev-parse", "HEAD"), "ownership refusal preserves control HEAD")
	assert.Empty(t, control.TagList(), "ownership refusal writes no control tags")
	for source, head := range before.sourceHeads {
		assert.Equal(t, head, control.Git("-C", source, "rev-parse", "HEAD"),
			"ownership refusal preserves %s HEAD", source)
		assert.Empty(t, polyrepoTags(control, source), "ownership refusal writes no tags in %s", source)
	}
}

func finalOwnershipSource(t *testing.T, pkg string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	if pkg == "" {
		r.WriteFile("README.md", "source boundary\n")
		r.Commit("chore: initialize source boundary")
		return r
	}
	r.SeedPackage("packages", pkg)
	r.Commit("feat(" + pkg + "): initialize source package")
	return r
}

// TestFinalOwnershipRefusesAControlUmbrellaContainingASource covers the case
// where the package's own scope remains in control, but its directory wraps a
// source checkout. Without the containment guard, ordinary control commits
// could claim files whose objects belong to another repository.
func TestFinalOwnershipRefusesAControlUmbrellaContainingASource(t *testing.T) {
	source := finalOwnershipSource(t, "api")
	control := harness.New(t)
	control.WriteFile("services/package.json", `{"name":"umbrella"}`)
	addPolyrepoSource(t, control, "api-source", "services/api", source)
	cfg := polyrepoFile()
	cfg["packages"] = map[string]any{
		"umbrella": map[string]any{"path": "services"},
	}
	cfg["scripts"] = map[string]any{
		"build": []string{"printf built > ownership-write"}, "publish": []string{"printf published > ownership-publish"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure an umbrella around a source")
	before := snapshotFinalOwnership(control, "services/api")

	assertFinalOwnershipRefusal(t, control, before, `package "umbrella" path`)
	assert.NoFileExists(t, control.Path("services", "ownership-write"))
	assert.NoFileExists(t, control.Path("services", "ownership-publish"))
	assert.NoFileExists(t, control.Path("services", "api", "ownership-write"))
}

// TestFinalOwnershipCanonicalizesImportedPackageBoundaries proves an imported
// repository cannot claim a sibling through a symlink spelling. Both a whole
// package path and only its source scope are checked after canonicalization.
func TestFinalOwnershipCanonicalizesImportedPackageBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config map[string]any
		link   string
		target string
	}{
		{
			name: "package path escapes through a symlink",
			config: map[string]any{
				"escape": map[string]any{"path": "escape-link"},
			},
			link: "escape-link", target: "../second/packages/escape",
		},
		{
			name: "source scope escapes through a symlink",
			config: map[string]any{
				"app": map[string]any{"path": "packages/app", "src": "src"},
			},
			link: "packages/app/src", target: "../../../second/packages/escape",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := harness.New(t)
			if strings.Contains(tc.link, "packages/app") {
				first.SeedPackage("packages", "app")
			}
			cfg := polyrepoFile()
			delete(cfg, "polyrepo")
			cfg["packages"] = tc.config
			cfg["scripts"] = map[string]any{
				"build": []string{"printf built > ownership-write"}, "publish": []string{"printf published > ownership-publish"},
			}
			writePolyrepoJSON(t, first, "dispat.json", cfg)
			require.NoError(t, os.MkdirAll(filepath.Dir(first.Path(tc.link)), 0o755))
			require.NoError(t, os.Symlink(tc.target, first.Path(tc.link)))
			first.Commit("chore: configure a canonical ownership escape")

			second := finalOwnershipSource(t, "escape")
			control := harness.New(t)
			addPolyrepoSource(t, control, "first-source", "sources/first", first)
			addPolyrepoSource(t, control, "second-source", "sources/second", second)
			root := polyrepoFile()
			delete(root, "polyrepo")
			root["configs"] = []string{"sources/first/dispat.json"}
			writePolyrepoJSON(t, control, "dispat.json", root)
			control.Commit("chore: import the escaping package owner")
			before := snapshotFinalOwnership(control, "sources/first", "sources/second")

			assertFinalOwnershipRefusal(t, control, before,
				`imported repository "first-source" package`)
			assert.NoFileExists(t, control.Path("sources", "second", "packages", "escape", "ownership-write"))
			assert.NoFileExists(t, control.Path("sources", "second", "packages", "escape", "ownership-publish"))
		})
	}
}

// TestFinalOwnershipRefusesADanglingImportedSourceScope keeps a broken
// imported symlink from being normalized into an absent or ownerless scope.
// The configuration error is fatal before any package work starts.
func TestFinalOwnershipRefusesADanglingImportedSourceScope(t *testing.T) {
	first := harness.New(t)
	first.SeedPackage("packages", "app")
	cfg := polyrepoFile()
	delete(cfg, "polyrepo")
	cfg["packages"] = map[string]any{
		"app": map[string]any{"path": "packages/app", "src": "src"},
	}
	writePolyrepoJSON(t, first, "dispat.json", cfg)
	require.NoError(t, os.Symlink("missing-source", first.Path("packages", "app", "src")))
	first.Commit("chore: configure a dangling source scope")

	control := harness.New(t)
	addPolyrepoSource(t, control, "first-source", "sources/first", first)
	root := polyrepoFile()
	delete(root, "polyrepo")
	root["configs"] = []string{"sources/first/dispat.json"}
	writePolyrepoJSON(t, control, "dispat.json", root)
	control.Commit("chore: import the dangling package owner")
	before := snapshotFinalOwnership(control, "sources/first")

	res := control.Status()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	var diagnostic string
	for _, event := range res.Events {
		diagnostic += event.Str("error")
	}
	assert.Equal(t, `config: package "app": src "src" names no folder inside the package`, diagnostic)
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, before.controlHead, control.Git("rev-parse", "HEAD"))
	assert.Equal(t, before.sourceHeads["sources/first"], control.Git("-C", "sources/first", "rev-parse", "HEAD"))
	assert.Empty(t, polyrepoTags(control, "sources/first"))
}

// TestFinalOwnershipRefusesNestedConfiguredScopes makes the ambiguity
// explicit: two separately named packages cannot own a directory and one of
// its descendants in the same composed history.
func TestFinalOwnershipRefusesNestedConfiguredScopes(t *testing.T) {
	source := finalOwnershipSource(t, "")
	control := harness.New(t)
	addPolyrepoSource(t, control, "source", "sources/source", source)
	control.WriteFile("owned/outer/package.json", `{"name":"outer"}`)
	control.WriteFile("owned/outer/inner/package.json", `{"name":"inner"}`)
	cfg := polyrepoFile()
	cfg["packages"] = map[string]any{
		"outer": map[string]any{"path": "owned/outer"},
		"inner": map[string]any{"path": "owned/outer/inner"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure overlapping package scopes")
	before := snapshotFinalOwnership(control, "sources/source")

	assertFinalOwnershipRefusal(t, control, before, "package ownership overlaps")
	assert.NoFileExists(t, control.Path("owned", "outer", "ownership-write"))
	assert.NoFileExists(t, control.Path("owned", "outer", "inner", "ownership-write"))
}

// TestFinalOwnershipRefusesASymlinkedNestedGitMarker proves ownership cannot
// climb through a nested repository marker whose identity is itself a
// symlink. Only regular .git files and directories establish a boundary.
func TestFinalOwnershipRefusesASymlinkedNestedGitMarker(t *testing.T) {
	source := finalOwnershipSource(t, "")
	control := harness.New(t)
	addPolyrepoSource(t, control, "source", "sources/source", source)
	control.WriteFile("owned/pkg/package.json", `{"name":"pkg"}`)
	control.WriteFile("owned/fake-git/HEAD", "ref: refs/heads/main\n")
	require.NoError(t, os.Symlink("../fake-git", control.Path("owned", "pkg", ".git")))
	cfg := polyrepoFile()
	cfg["packages"] = map[string]any{
		"pkg": map[string]any{"path": "owned/pkg"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure a package behind a symlinked Git marker")
	before := snapshotFinalOwnership(control, "sources/source")

	assertFinalOwnershipRefusal(t, control, before, "unsupported .git marker")
	assert.NoFileExists(t, control.Path("owned", "pkg", "ownership-write"))
}
