// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final production-review cases for the remaining real-binary planning and
// command boundaries. These scenarios exercise composed configuration and
// release state that cannot be represented by planner test doubles.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFinalRemainingExecUsesTheControlSpaceAuthority gives an imported source
// and control the same local space name. An explicit space subject must resolve
// the control space's folder layer and working directory; the imported file is
// authoritative only for packages owned by its repository.
func TestFinalRemainingExecUsesTheControlSpaceAuthority(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	imported := polyrepoFile()
	delete(imported, "polyrepo")
	imported["spaces"] = map[string]any{"libs": map[string]any{"path": []string{"packages"}}}
	writePolyrepoJSON(t, source, "dispat.json", imported)
	writePolyrepoJSON(t, source, "packages/dispat.json", map[string]any{
		"scripts": map[string]any{"identify": []string{"printf 'source:%s\\n' \"$PWD\""}},
	})
	source.Commit("feat(lib): configure imported local space")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	require.NoError(t, os.MkdirAll(control.Path("owned"), 0o755))
	writePolyrepoJSON(t, control, "owned/dispat.json", map[string]any{
		"scripts": map[string]any{"identify": []string{"printf 'control:%s:%s\\n' \"$AUTHORITY\" \"$PWD\""}},
		"env":     map[string]any{"AUTHORITY": "control-folder"},
	})
	cfg := polyrepoFile()
	cfg["configs"] = []string{"sources/lib/dispat.json"}
	cfg["spaces"] = map[string]any{"libs": map[string]any{"path": []string{"owned"}}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose equal local space names")

	res := control.Command("exec", "identify", "--for", "space:libs", "--in", "space:libs")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "control:control-folder:")
	assert.Contains(t, res.Stdout, filepath.Join(control.Root, "owned"))
	assert.NotContains(t, res.Stdout, "source:")
	assert.Empty(t, polyrepoTags(control, "sources/lib"), "exec remains read-only")
}

// TestFinalRemainingImportedFolderConfigFailsClosed keeps the imported root
// config valid while corrupting the space-folder layer it delegates to. That
// nearer policy cannot be skipped to retain a partial source package graph.
func TestFinalRemainingImportedFolderConfigFailsClosed(t *testing.T) {
	control := finalImportedConfigFleet(t)
	beforeControl := control.Git("rev-parse", "HEAD")
	beforeSource := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	control.WriteFile("sources/lib/packages/dispat.json", `{"scripts":`)

	res := control.Status()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "sources/lib/packages/dispat.json")
	assert.Contains(t, combined, "package discovery failed")
	assert.NotContains(t, combined, "release plan ready")
	assert.Empty(t, plannedPackages(res), "no partial imported graph is reported")
	assert.Equal(t, beforeControl, control.Git("rev-parse", "HEAD"))
	assert.Equal(t, beforeSource, control.Git("-C", "sources/lib", "rev-parse", "HEAD"))
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
}

// TestFinalRemainingExecCwdDiscoveryFailuresNameTheUnreadableLayer reaches the
// two subject-resolution sites independently. A broken folder layer blocks an
// inferred --for subject and an inferred --script-from subject before either
// can fall back to a root script.
func TestFinalRemainingExecCwdDiscoveryFailuresNameTheUnreadableLayer(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "subject", args: []string{"exec", "which", "--for", "cwd", "--script-from", "root"}},
		{name: "script source", args: []string{"exec", "which", "--for", "root", "--script-from", "cwd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, marker := finalExecLayerRepo(t)
			r.WriteFile("packages/dispat.json", `{"scripts":`)
			r.WorkFrom("packages")

			res := r.Command(tc.args...)
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, "packages/dispat.json")
			assert.NoFileExists(t, marker)
		})
	}
}

// TestFinalRemainingPrivatePinStoreCreationFailureStopsBeforeScripts makes the
// run-scoped coordinator's parent unusable. A composed release cannot proceed
// without its private cross-process pins, and no source ref or script output
// may escape that setup failure.
func TestFinalRemainingPrivatePinStoreCreationFailureStopsBeforeScripts(t *testing.T) {
	f := finalPolyrepo(t)
	control := f.control
	control.WriteFile("tmp-is-a-file", "not a directory\n")
	beforeControl := control.Git("rev-parse", "HEAD")
	beforeSource := control.Git("-C", "sources/lib", "rev-parse", "HEAD")

	blocked := control.Path("tmp-is-a-file")
	res := control.CommandEnv([]string{"TMPDIR=" + blocked, "TMP=" + blocked, "TEMP=" + blocked}, "release")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "creating live pin directory")
	assert.NotContains(t, combined, "building")
	assert.NotContains(t, combined, "publishing")
	assert.Equal(t, beforeControl, control.Git("rev-parse", "HEAD"))
	assert.Equal(t, beforeSource, control.Git("-C", "sources/lib", "rev-parse", "HEAD"))
	assert.Empty(t, control.TagList())
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
}

// TestFinalRemainingRepositoryInputClosureCrossesGroupsAndDependencies builds
// an alternating input chain: provider p -> fixed-group member a, across the
// fixed group to b, then b -> consumer c. Mutating p immediately before c's
// publication must invalidate c's snapshot even though p is neither c's
// direct provider nor a member of c's group.
func TestFinalRemainingRepositoryInputClosureCrossesGroupsAndDependencies(t *testing.T) {
	control := harness.New(t)
	for _, name := range []string{"p", "a", "b", "c"} {
		source := harness.New(t)
		source.SeedPackage("packages", name)
		source.Commit("feat(" + name + "): bootstrap " + name)
		addPolyrepoSource(t, control, name+"-source", "sources/"+name, source)
	}
	cfg := polyrepoFile()
	cfg["concurrency"] = []int{1}
	cfg["versionGroups"] = map[string]any{"pair": map[string]any{"versioning": "fixed"}}
	cfg["spaces"] = map[string]any{
		"provider": map[string]any{"path": []string{"sources/p/packages"}},
		"first":    map[string]any{"path": []string{"sources/a/packages"}, "versionGroup": "pair"},
		"second":   map[string]any{"path": []string{"sources/b/packages"}, "versionGroup": "pair"},
		"consumer": map[string]any{"path": []string{"sources/c/packages"}},
	}
	cfg["dependencies"] = map[string]any{"a": []any{"p"}, "c": []any{"b"}}
	cfg["scripts"] = map[string]any{
		"build":   []string{"echo building $DISPAT_PACKAGE"},
		"guard":   []string{`if [ "$DISPAT_PACKAGE" = c ]; then git -C ../../../p commit --allow-empty -q -m 'chore: unplanned provider advance'; : > ../../../../p-mutated; fi`},
		"publish": []string{`printf '%s\n' "$DISPAT_PACKAGE" > published`},
	}
	cfg["flow"] = map[string]any{
		"build": []string{"build"}, "beforePublish": []string{"guard"}, "publish": []string{"publish"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose alternating repository inputs")

	status := control.StatusOK()
	for _, name := range []string{"p", "a", "b", "c"} {
		assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(status.Events, name).Str("version"))
	}
	res := control.Release()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, "E330"), "events: %#v", res.Events)
	assert.FileExists(t, control.Path("p-mutated"), "the indirect provider mutation occurred")
	assert.NoFileExists(t, control.Path("sources", "c", "packages", "c", "published"),
		"the transitive consumer is guarded before publication")
	assert.NotContains(t, polyrepoTags(control, "sources/c"), "c@0.1.0")
}
