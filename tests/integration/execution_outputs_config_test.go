// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57, second part: `buildOutputs` and `buildPlatforms`, the two keys that
// say what a package's build leaves behind and which machines may run it.
//
// Nothing carries the outputs anywhere yet. What is settled here is what a
// reader can check before anything does: the ladder each key resolves
// through, the shapes a level may write, and the refusal that catches two
// packages claiming one folder. The resolved values are read out of `dispat
// status --log-level debug`, which is the one place a run says what a
// package's configuration came to without releasing anything.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionResolvedPackage is the debug line discovery writes for one
// package: what the ladder settled for it, before the plan says anything
// about whether it is releasing.
func executionResolvedPackage(t *testing.T, res harness.RunResult, packageName string) harness.Event {
	t.Helper()
	for _, event := range res.Events {
		if event.Str("message") == "package resolved" && event.Package() == packageName {
			return event
		}
	}
	t.Fatalf("no resolved line for package %q\nstdout:\n%s\nstderr:\n%s", packageName, res.Stdout, res.Stderr)
	return nil
}

// executionResolvedList reads one of the two build lists out of a resolved
// line. A missing field is a list nobody stated, which is what an opted-out
// package looks like: the line a workspace without these keys writes is the
// line it always wrote.
func executionResolvedList(t *testing.T, event harness.Event, field string) []string {
	t.Helper()
	raw, isStated := event[field]
	if !isStated {
		return nil
	}
	values, isList := raw.([]any)
	require.True(t, isList, "%s is a list of strings, got %T", field, raw)
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, isText := value.(string)
		require.True(t, isText, "%s holds strings, got %T", field, value)
		out = append(out, text)
	}
	return out
}

// executionOutputsRepo is the ladder fixture: a repository default, a space
// entry over it, the space folder's own file over that, and one package
// folder file per package that has something of its own to say. `tool` is a
// standalone package, which is its own space and therefore the one package
// that reaches the repository default by the other route.
func executionOutputsRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.BuildOutputs = []string{"dist"}
	cfg.BuildPlatforms = []string{"linux/amd64"}
	libs := cfg.Spaces["libs"]
	libs.BuildOutputs = []string{"build"}
	cfg.Spaces["libs"] = libs
	cfg.Packages = map[string]models.PackageConfig{"tool": {Path: "tools/tool"}}
	r.WriteConfigModel(cfg)

	for _, name := range []string{"core", "utils", "plain"} {
		r.SeedPackage("packages", name)
	}
	r.WriteFile(filepath.Join("tools", "tool", "main.txt"), "tool\n")
	// The space folder's own file, the layer between the root file's entry
	// and anything said about one package.
	r.WriteFile(filepath.Join("packages", "dispat.json"),
		executionRawJSON(r, map[string]any{"buildOutputs": []any{"out"}}))
	r.WriteFile(filepath.Join("packages", "core", "dispat.json"),
		executionRawJSON(r, map[string]any{
			"buildOutputs":   []any{"lib/esm"},
			"buildPlatforms": []any{"darwin/arm64"},
		}))
	// The one shape the typed model cannot express: omitempty drops a present
	// empty list, and an empty list is how a package opts out.
	r.WriteFile(filepath.Join("packages", "utils", "dispat.json"),
		executionRawJSON(r, map[string]any{"buildOutputs": []any{}}))
	r.Commit("feat(core,utils,plain,tool): bootstrap")
	return r
}

// TestExecutionBuildOutputsLadder: both keys ride the ordinary configuration
// ladder and replace whole. The nearest level that states a list wins, a
// level that states nothing inherits, and an explicit empty list is how a
// package says its build leaves nothing for anyone else to install.
func TestExecutionBuildOutputsLadder(t *testing.T) {
	r := executionOutputsRepo(t)
	res := r.StatusOK("--log-level", "debug")

	core := executionResolvedPackage(t, res, "core")
	assert.Equal(t, []string{"lib/esm"}, executionResolvedList(t, core, "buildOutputs"),
		"the package folder's own file is the nearest statement about the package")
	assert.Equal(t, []string{"darwin/arm64"}, executionResolvedList(t, core, "buildPlatforms"),
		"the two keys travel the ladder independently")

	plain := executionResolvedPackage(t, res, "plain")
	assert.Equal(t, []string{"out"}, executionResolvedList(t, plain, "buildOutputs"),
		"the space folder's file outranks the root file's space entry")
	assert.Equal(t, []string{"linux/amd64"}, executionResolvedList(t, plain, "buildPlatforms"),
		"and says nothing about the key it did not state")

	utils := executionResolvedPackage(t, res, "utils")
	assert.Empty(t, executionResolvedList(t, utils, "buildOutputs"),
		"an explicit empty list opts the package out of what it would inherit")
	assert.Equal(t, []string{"linux/amd64"}, executionResolvedList(t, utils, "buildPlatforms"),
		"opting out of the outputs says nothing about where the build may run")

	tool := executionResolvedPackage(t, res, "tool")
	assert.Equal(t, []string{"dist"}, executionResolvedList(t, tool, "buildOutputs"),
		"a standalone package is its own space and inherits the repository default")

	assert.Empty(t, r.TagList(), "status releases nothing")
}

// executionNestedOutputsRepo is the fixture the ownership claims are made on:
// `plugin` is a standalone package inside `core`'s own folder, which is the
// only way two packages can come to name one folder at all.
func executionNestedOutputsRepo(t *testing.T, core, plugin []string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{
		"core":   {BuildOutputs: core},
		"plugin": {Path: "packages/core/plugin", BuildOutputs: plugin},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile(filepath.Join("packages", "core", "plugin", "main.txt"), "plugin\n")
	r.Commit("feat(core,plugin): bootstrap")
	return r
}

// TestExecutionOverlappingBuildOutputsFailPreflight: package folders may
// nest, so two packages can claim one folder, and the folder's contents would
// then be attributed to whichever build finished last. The pair is refused
// while nothing has happened but a configuration being read, and the refusal
// names both packages so the reader knows which declaration to change.
func TestExecutionOverlappingBuildOutputsFailPreflight(t *testing.T) {
	for name, row := range map[string]struct {
		core, plugin []string
		want         string
	}{
		"the same folder from two packages": {
			core: []string{"plugin/dist"}, plugin: []string{"dist"}, want: "they name the same folder"},
		"one package's folder inside another's root": {
			core: []string{"plugin"}, plugin: []string{"dist"}, want: "the second sits inside the first"},
		"two spellings of one folder": {
			core: []string{"plugin/DIST"}, plugin: []string{"dist"}, want: "they name the same folder"},
		"a root holding a nested package that declares nothing": {
			core: []string{"plugin"}, plugin: nil, want: `which holds the folder of package "plugin"`},
	} {
		t.Run(name, func(t *testing.T) {
			r := executionNestedOutputsRepo(t, row.core, row.plugin)
			res := r.Status("--log-format", "json")
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			text := diagnosticText(res)
			assert.Contains(t, text, row.want)
			assert.Contains(t, text, `package "core"`)
			assert.Contains(t, text, `package "plugin"`)
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
				"no %s diagnostic\nstdout:\n%s\nstderr:\n%s", executionRefusalCode, res.Stdout, res.Stderr)
			assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
		})
	}

	t.Run("sibling folders are the ordinary workspace", func(t *testing.T) {
		r := executionNestedOutputsRepo(t, []string{"dist"}, []string{"dist"})
		res := r.StatusOK("--log-level", "debug")
		assert.Equal(t, []string{"dist"},
			executionResolvedList(t, executionResolvedPackage(t, res, "core"), "buildOutputs"))
		assert.Equal(t, []string{"dist"},
			executionResolvedList(t, executionResolvedPackage(t, res, "plugin"), "buildOutputs"),
			"one name in two package folders is two folders")
	})
}

// executionBuildRefusal is one row of the shape table: the configuration a
// level writes, and the sentence the reader is owed for it.
type executionBuildRefusal struct {
	mutate func(*models.File)
	want   string
}

// executionRootOutputs states one list at the repository level, where the key
// path a refusal names is the key alone.
func executionRootOutputs(outputs ...string) func(*models.File) {
	return func(cfg *models.File) { cfg.BuildOutputs = outputs }
}

// executionRootPlatforms states one platform list at the repository level.
func executionRootPlatforms(platforms ...string) func(*models.File) {
	return func(cfg *models.File) { cfg.BuildPlatforms = platforms }
}

// TestExecutionBuildOutputRefusals: every rule a single level's lists are
// held to, through the binary. A refused configuration exits non-zero, names
// the key path, carries the execution diagnostic and releases nothing,
// because what is wrong is how the run would be executed and nothing about it
// has started.
func TestExecutionBuildOutputRefusals(t *testing.T) {
	for name, row := range map[string]executionBuildRefusal{
		"an empty path": {
			executionRootOutputs(""), "buildOutputs: an empty path names no build output"},
		"a path holding a NUL byte": {
			executionRootOutputs("dist\x00"), "NUL byte"},
		"a path written with backslashes": {
			executionRootOutputs(`dist\assets`), "slash-separated"},
		"a path naming a drive": {
			executionRootOutputs("C:/dist"), "colon"},
		"an absolute path": {
			executionRootOutputs("/srv/dist"), "absolute"},
		"a path leaving the package folder": {
			executionRootOutputs("../dist"), "leaves the package folder"},
		"a path climbing out through a folder": {
			executionRootOutputs("dist/../../elsewhere"), "leaves the package folder"},
		"the package folder itself": {
			executionRootOutputs("./"), "names the package folder itself"},
		"a path naming repository metadata": {
			executionRootOutputs(".git/hooks"), "repository metadata"},
		"a path reaching repository metadata deeper down": {
			executionRootOutputs("dist/.GIT"), "repository metadata"},
		"one root written twice": {
			executionRootOutputs("dist", "dist/"), "one build output root"},
		"a root inside another root": {
			executionRootOutputs("dist", "dist/assets"), "one build output root"},
		"two spellings of one root": {
			executionRootOutputs("dist", "DIST"), "one build output root"},
		"a platform with no architecture": {
			executionRootPlatforms("linux"), "is not a platform"},
		"a platform with a third half": {
			executionRootPlatforms("linux/amd64/v3"), "is not a platform"},
		"a platform spelled with capitals": {
			executionRootPlatforms("Linux/amd64"), "is not a platform"},
		"a platform stated twice": {
			executionRootPlatforms("linux/amd64", "linux/amd64"), "already stated at index 0"},
		"a space's own list": {
			func(cfg *models.File) {
				libs := cfg.Spaces["libs"]
				libs.BuildOutputs = []string{"../dist"}
				cfg.Spaces["libs"] = libs
			}, `space "libs": buildOutputs`},
		"a package entry's own list": {
			func(cfg *models.File) {
				cfg.Packages = map[string]models.PackageConfig{"core": {BuildPlatforms: []string{"linux"}}}
			}, "buildPlatforms"},
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			r.SeedPackage("packages", "core")
			cfg := libsConfig(echoBuild, 1)
			row.mutate(&cfg)
			r.WriteConfigModel(cfg)
			r.Commit("feat(core): bootstrap")
			res := r.Status("--log-format", "json")
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, diagnosticText(res), row.want)
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
				"no %s diagnostic\nstdout:\n%s\nstderr:\n%s", executionRefusalCode, res.Stdout, res.Stderr)
			assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
		})
	}
}

// TestExecutionBuildOutputsInAPackageFolderFileAreRefusedToo: the rules
// belong to the keys rather than to the root file, so the layer a checkout
// carries with it is held to them as well.
func TestExecutionBuildOutputsInAPackageFolderFileAreRefusedToo(t *testing.T) {
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.WriteFile(filepath.Join("packages", "core", "dispat.json"),
		executionRawJSON(r, map[string]any{"buildOutputs": []any{"dist/.git"}}))
	r.Commit("feat(core): bootstrap")
	res := r.Status("--log-format", "json")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, diagnosticText(res), "repository metadata")
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
		"no %s diagnostic\nstdout:\n%s\nstderr:\n%s", executionRefusalCode, res.Stdout, res.Stderr)
}

// TestExecutionBuildKeysAreAbsentFromAnOrdinaryRun: a workspace that states
// neither key writes the resolved line it always wrote, with no field for
// either of them, which is what keeps the debug output of every existing
// configuration unchanged.
func TestExecutionBuildKeysAreAbsentFromAnOrdinaryRun(t *testing.T) {
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.Commit("feat(core): bootstrap")
	res := r.StatusOK("--log-level", "debug")
	resolved := executionResolvedPackage(t, res, "core")
	for _, field := range []string{"buildOutputs", "buildPlatforms"} {
		_, isStated := resolved[field]
		assert.False(t, isStated, "a package nobody declared %s for names no %s", field, field)
	}
}
