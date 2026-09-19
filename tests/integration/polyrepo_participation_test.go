// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Repository participation. `repositoryOverrides.<source>.enabled: false`
// removes one linked repository from the run before anything touches it, and
// these tests hold that promise to what is externally observable: the excluded
// checkout need not exist, its remote is never written to, its packages are in
// no plan and reachable through no selector, and the paths it occupies stay
// reserved for it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// eventStrings reads a JSON log field written as a list of strings.
func eventStrings(event harness.Event, key string) []string {
	items, _ := event[key].([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// excludedFleet assembles one participating source (`lib-source`) and one the
// control file excludes (`app-source`), each with its own bare remote, and
// returns the control repository with the excluded source's remote.
func excludedFleet(t *testing.T, extra func(control *harness.Repo, cfg map[string]any)) (*harness.Repo, string, string) {
	t.Helper()
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.Commit("feat(lib): bootstrap library")

	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appSource.Commit("feat(app): bootstrap application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages",
		"apps": "sources/app/packages",
	})
	cfg["repositoryOverrides"] = map[string]any{"app-source": map[string]any{"enabled": false}}
	cfg["scripts"] = map[string]any{
		"build":    []string{"echo building"},
		"publish":  []string{"echo publishing"},
		"selected": []string{`printf '%s\n' "$DISPAT_PACKAGE" >> ../../../../selected.log`},
	}
	if extra != nil {
		extra(control, cfg)
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble fleet without the application")

	controlRemote := control.AddBareRemote()
	sourceRemote := func(path, name string) string {
		remote := filepath.Join(t.TempDir(), name+".git")
		control.Git("init", "-q", "--bare", remote)
		control.Git("-C", path, "remote", "set-url", "origin", remote)
		control.Git("-C", path, "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
		return remote
	}
	sourceRemote("sources/lib", "lib-source")
	appRemote := sourceRemote("sources/app", "app-source")
	return control, controlRemote, appRemote
}

// TestPolyrepoExcludedRepositoryIsAbsentFromEveryPhase proves that exclusion
// happens before source initialization, history checks, planning, snapshots,
// hooks and lock acquisition: an excluded repository may be uninitialized or
// shallow, contributes no package to any plan or selector, and its remote
// never receives the fleet release lock.
func TestPolyrepoExcludedRepositoryIsAbsentFromEveryPhase(t *testing.T) {
	t.Run("uninitialized excluded checkout still releases the fleet", func(t *testing.T) {
		control, controlRemote, appRemote := excludedFleet(t, nil)
		require.NoError(t, os.RemoveAll(control.Path("sources", "app")))

		status := control.StatusOK()
		assert.Equal(t, "direct", harness.GraphLine(status.Events, "lib").Str("reason"))
		assert.Empty(t, harness.GraphLine(status.Events, "app").Str("reason"),
			"an excluded repository contributes no package")

		res := releaseLocked(control)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0")
		assert.NotContains(t, bareGit(t, appRemote, "tag"), lockTag,
			"an excluded repository is never locked")
		assert.Empty(t, strings.TrimSpace(bareGit(t, appRemote, "tag")),
			"an excluded repository receives no tag of any kind")
		assertLockCleared(t, control, controlRemote)
	})

	t.Run("shallow excluded checkout is never inspected", func(t *testing.T) {
		control, _, appRemote := excludedFleet(t, nil)
		head := control.Git("-C", "sources/app", "rev-parse", "HEAD")
		shallow := control.Path(".git", "modules", "app-source", "shallow")
		require.NoError(t, os.WriteFile(shallow, []byte(head+"\n"), 0o644))
		require.Equal(t, "true", control.Git("-C", "sources/app", "rev-parse", "--is-shallow-repository"))

		res := releaseLocked(control)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.False(t, harness.IsCodePresent(res.Events, "E330"), "events: %#v", res.Events)
		assert.Empty(t, polyrepoTags(control, "sources/app"),
			"an excluded repository records nothing")
		assert.NotContains(t, bareGit(t, appRemote, "tag"), lockTag)
	})

	t.Run("composition reports the exclusion", func(t *testing.T) {
		control, _, _ := excludedFleet(t, func(_ *harness.Repo, cfg map[string]any) { cfg["logLevel"] = "debug" })
		status := control.StatusOK()
		var composed, excluded harness.Event
		for _, event := range status.Events {
			switch event.Str("message") {
			case "polyrepo workspace composed":
				composed = event
			case "repository excluded from the release":
				excluded = event
			}
		}
		require.NotEmpty(t, composed, "stdout:\n%s", status.Stdout)
		assert.Equal(t, []string{"control", "lib-source"}, eventStrings(composed, "repositories"))
		assert.Equal(t, []string{"app-source"}, eventStrings(composed, "excludedRepositories"))
		require.NotEmpty(t, excluded)
		assert.Equal(t, "app-source", excluded.Str("repository"))
		assert.Contains(t, excluded.Str("reason"), "enabled=false")
	})

	t.Run("no selector reintroduces an excluded package", func(t *testing.T) {
		control, _, _ := excludedFleet(t, nil)

		byPackage := control.Status("-p", "app")
		assert.NotZero(t, byPackage.Code, "stdout:\n%s\nstderr:\n%s", byPackage.Stdout, byPackage.Stderr)

		bySpace := control.Status("--space", "apps")
		assert.NotZero(t, bySpace.Code, "stdout:\n%s\nstderr:\n%s", bySpace.Stdout, bySpace.Stderr)

		byGroup := control.Status("-g", "apps")
		assert.NotZero(t, byGroup.Code, "stdout:\n%s\nstderr:\n%s", byGroup.Stdout, byGroup.Stderr)

		byScript := control.RunScript("selected", "--package", "app")
		assert.NotZero(t, byScript.Code, "stdout:\n%s\nstderr:\n%s", byScript.Stdout, byScript.Stderr)

		control.RunScriptOK("selected", "--package", "lib", "--consumers")
		control.RunScriptOK("selected", "--package", "*", "--since", "all")
		selected, err := os.ReadFile(control.Path("selected.log"))
		require.NoError(t, err)
		assert.Equal(t, "lib\nlib\n", string(selected),
			"--consumers and --since all reach every participating package and no excluded one")
	})

	t.Run("a nested command keeps the exclusion", func(t *testing.T) {
		control, _, _ := excludedFleet(t, func(control *harness.Repo, cfg map[string]any) {
			scripts := cfg["scripts"].(map[string]any)
			scripts["nested"] = []string{
				control.DispatCommand("status", "--package", "app") +
					" > ../../../../nested.log 2>&1; echo $? > ../../../../nested.code",
			}
		})

		control.RunScriptOK("nested", "--package", "lib", "--since", "all")
		code, err := os.ReadFile(control.Path("nested.code"))
		require.NoError(t, err)
		assert.NotEqual(t, "0\n", string(code), "a nested command must not reach an excluded package")
		nested, err := os.ReadFile(control.Path("nested.log"))
		require.NoError(t, err)
		assert.NotContains(t, string(nested), `"package":"app"`)
	})

	t.Run("a control directive cannot address an excluded package", func(t *testing.T) {
		control, _, appRemote := excludedFleet(t, nil)
		control.WriteFile("notes.txt", "fleet note\n")
		control.Commit("fix(app): control intent for an excluded package")

		res := releaseLocked(control)
		assert.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.True(t, harness.IsCodePresent(res.Events, "E130"), "events: %#v", res.Events)
		assert.Empty(t, strings.TrimSpace(bareGit(t, appRemote, "tag")))
	})

	t.Run("a wildcard directive stays inside the participating fleet", func(t *testing.T) {
		control, _, appRemote := excludedFleet(t, nil)
		control.WriteFile("notes.txt", "fleet note\n")
		control.Commit("fix(*): fleet wide patch")

		res := releaseLocked(control)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0")
		assert.Empty(t, polyrepoTags(control, "sources/app"))
		assert.Empty(t, strings.TrimSpace(bareGit(t, appRemote, "tag")))
	})
}

// TestPolyrepoExcludedRepositoryReservesItsBoundary proves that exclusion
// removes a repository from the run without releasing its filesystem
// boundary: a control-owned package reaching into the excluded checkout is an
// ownership error rather than an implicitly adopted control folder.
func TestPolyrepoExcludedRepositoryReservesItsBoundary(t *testing.T) {
	control, _, _ := excludedFleet(t, func(_ *harness.Repo, cfg map[string]any) {
		cfg["packages"] = map[string]any{
			"wrapper": map[string]any{"path": "sources/app/packages/app"},
		}
	})

	status := control.Status()
	assert.NotZero(t, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	assert.Contains(t, status.Stdout+status.Stderr, "nested Git repository",
		"the excluded checkout keeps its ownership boundary")
}

// TestPolyrepoExcludedProviderIsRefusedUnlessExternal proves the two
// dependency outcomes an exclusion produces: a required edge onto a package of
// an excluded repository is a hard error that names that repository, while an
// `external: true` edge keeps the ordinary absent-provider warning.
func TestPolyrepoExcludedProviderIsRefusedUnlessExternal(t *testing.T) {
	t.Run("required provider is refused and names the repository", func(t *testing.T) {
		control, _, _ := excludedFleet(t, func(_ *harness.Repo, cfg map[string]any) {
			cfg["dependencies"] = map[string]any{"lib": []any{"app"}}
		})

		status := control.Status()
		assert.NotZero(t, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
		combined := status.Stdout + status.Stderr
		assert.Contains(t, combined, "unknown provider package")
		assert.Contains(t, combined, "app-source")
		assert.Contains(t, combined, "excluded by repositoryOverrides")
	})

	t.Run("external provider keeps the inactive-edge warning", func(t *testing.T) {
		control, _, _ := excludedFleet(t, func(_ *harness.Repo, cfg map[string]any) {
			cfg["dependencies"] = map[string]any{
				"lib": []any{map[string]any{"provider": "app", "external": true}},
			}
		})

		status := control.StatusOK()
		assert.True(t, harness.IsCodePresent(status.Events, "W330"), "events: %#v", status.Events)
		assert.Equal(t, "direct", harness.GraphLine(status.Events, "lib").Str("reason"))
	})
}

// TestPolyrepoExcludedImportedSourceKeepsOwnedCommitPolicy proves that
// participation metadata is accepted for an imported source — the import is
// skipped with its repository — while a central `commit` override aimed at an
// imported source remains refused.
func TestPolyrepoExcludedImportedSourceKeepsOwnedCommitPolicy(t *testing.T) {
	newFleet := func(t *testing.T, override map[string]any) *harness.Repo {
		t.Helper()
		libSource := harness.New(t)
		libSource.SeedPackage("packages", "lib")
		writePolyrepoJSON(t, libSource, "dispat.json", map[string]any{
			"spaces": map[string]any{"libs": map[string]any{"path": []string{"packages"}}},
		})
		libSource.Commit("feat(lib): bootstrap imported library")

		appSource := harness.New(t)
		appSource.SeedPackage("packages", "app")
		writePolyrepoJSON(t, appSource, "dispat.json", map[string]any{
			"spaces": map[string]any{"apps": map[string]any{"path": []string{"packages"}}},
		})
		appSource.Commit("feat(app): bootstrap imported application")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
		addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
		cfg := polyrepoFile()
		cfg["configs"] = []string{"sources/lib/dispat.json", "sources/app/dispat.json"}
		cfg["repositoryOverrides"] = override
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: assemble imported fleet")
		return control
	}

	t.Run("participation metadata excludes an imported source", func(t *testing.T) {
		control := newFleet(t, map[string]any{"app-source": map[string]any{"enabled": false}})
		status := control.StatusOK()
		assert.Equal(t, "direct", harness.GraphLine(status.Events, "lib").Str("reason"))
		assert.Empty(t, harness.GraphLine(status.Events, "app").Str("reason"))
	})

	t.Run("a commit override for an imported source stays refused", func(t *testing.T) {
		control := newFleet(t, map[string]any{
			"app-source": map[string]any{"commit": map[string]any{"enabled": false}},
		})
		status := control.Status()
		assert.NotZero(t, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
		combined := status.Stdout + status.Stderr
		assert.Contains(t, combined, "E332")
		assert.Contains(t, combined, "cannot override imported repository config")
	})

	t.Run("an unknown override name is refused whether it enables or disables", func(t *testing.T) {
		for _, override := range []map[string]any{
			{"App-Source": map[string]any{"enabled": false}},
			{"App-Source": map[string]any{"commit": map[string]any{"enabled": false}}},
		} {
			control := newFleet(t, override)
			status := control.Status()
			assert.NotZero(t, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
			assert.Contains(t, status.Stdout+status.Stderr, "unknown source repository")
		}
	})
}
