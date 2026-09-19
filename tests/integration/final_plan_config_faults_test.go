// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final production-review refusals at the workspace configuration boundary.
// An imported file does not participate until its canonical path, Git root,
// repository identity, and own schema all agree.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func finalImportedConfigFleet(t *testing.T) *harness.Repo {
	t.Helper()
	source := harness.New(t)
	source.SeedPackage("packages", "core")
	sourceCfg := polyrepoFile()
	sourceCfg["polyrepo"] = false
	sourceCfg["spaces"] = centralSpaces(map[string]string{"libs": "packages"})
	writePolyrepoJSON(t, source, "dispat.json", sourceCfg)
	source.Commit("feat(core): bootstrap imported source")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	controlCfg := polyrepoFile()
	controlCfg["configs"] = []string{"sources/lib/dispat.json"}
	writePolyrepoJSON(t, control, "dispat.json", controlCfg)
	control.Commit("chore: import source release config")
	return control
}

// TestFinalImportedConfigRefusesACanonicalPathItCannotResolve: an import path
// can be lexically inside the workspace while its final symlink target is not
// resolvable. Composition names that config and stops before package discovery
// or planning rather than silently dropping it.
func TestFinalImportedConfigRefusesACanonicalPathItCannotResolve(t *testing.T) {
	control := finalImportedConfigFleet(t)
	path := control.Path("sources", "lib", "dispat.json")
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink("dispat.json", path))

	res := control.Status()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "sources/lib/dispat.json")
	assert.Contains(t, combined, "too many links")
	assert.NotContains(t, combined, "release plan ready")
	assert.Empty(t, polyrepoTags(control, "sources/lib"), "an unresolved import records no release")
}

// TestFinalImportedConfigRefusesAGitRootThatDisappeared: Git's root reply is
// canonicalized before it can identify a source. A path that no longer exists
// is an invalid imported repository, even though Git itself exited zero.
func TestFinalImportedConfigRefusesAGitRootThatDisappeared(t *testing.T) {
	control := finalImportedConfigFleet(t)
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C */sources/lib rev-parse --show-toplevel*",
		Nth:     2,
		Output:  control.Path("vanished-source-root"),
	})

	res := control.CommandEnv(fault.Env(), "status")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "imported repository root")
	assert.Contains(t, combined, "E330")
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, 2, fault.Matches(), "checkout validation precedes import ownership")
	assert.Empty(t, polyrepoTags(control, "sources/lib"), "an unresolved root records no release")
}

// TestFinalImportedConfigRefusesAGitRootFromAnotherDeclaredSource: a Git root
// must contain the canonical imported file before its .gitmodules identity can
// own that file. Otherwise a corrupt successful Git reply could silently apply
// one source's policy to another source's packages.
func TestFinalImportedConfigRefusesAGitRootFromAnotherDeclaredSource(t *testing.T) {
	control := finalImportedConfigFleet(t)
	other := harness.New(t)
	other.SeedPackage("packages", "other")
	other.Commit("feat(other): bootstrap second source")
	addPolyrepoSource(t, control, "other-source", "sources/other", other)
	control.Commit("chore: add second declared source")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C */sources/lib rev-parse --show-toplevel*",
		Nth:     2,
		Output:  control.Path("sources", "other"),
	})

	res := control.CommandEnv(fault.Env(), "status")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "does not contain imported config")
	assert.Contains(t, combined, "E330")
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, 2, fault.Matches(), "the import ownership query consumed the corrupt root once")
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.Empty(t, polyrepoTags(control, "sources/other"))
}

// TestFinalImportedConfigAllowsASymlinkWithinItsOwningRepository: canonical
// containment follows ordinary symlinks. An alias to a config in the same
// source keeps that source's identity and composes the expected package.
func TestFinalImportedConfigAllowsASymlinkWithinItsOwningRepository(t *testing.T) {
	control := finalImportedConfigFleet(t)
	require.NoError(t, os.Symlink("dispat.json", control.Path("sources", "lib", "release.json")))
	commitPolyrepoSource(t, control, "sources/lib", "chore: add local config alias")
	checkpointPolyrepoSource(t, control, "sources/lib")
	cfg := polyrepoFile()
	cfg["configs"] = []string{"sources/lib/release.json"}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: import source config through local alias")

	res := control.StatusOK()
	assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(res.Events, "core").Str("version"))
	assert.Empty(t, polyrepoTags(control, "sources/lib"), "status remains read-only")
}

// TestFinalImportedConfigSchemaFailureCannotBecomeAnEmptySource: the imported
// file owns release policy for its repository. If that file is malformed, the
// source is refused rather than retained with the control defaults.
func TestFinalImportedConfigSchemaFailureCannotBecomeAnEmptySource(t *testing.T) {
	control := finalImportedConfigFleet(t)
	control.WriteFile("sources/lib/dispat.json", "{\"unknownImportedPolicy\":true}\n")

	res := control.Status()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "imported config")
	assert.Contains(t, combined, "unknownImportedPolicy")
	assert.NotContains(t, combined, "release plan ready")
	assert.Empty(t, polyrepoTags(control, "sources/lib"), "a broken owner config records no release")
}

// TestFinalChoreographyRefusesControlStyleConfigImports: each choreographed
// peer carries its own repository-local config, so a CLI import would create a
// second control authority. It is refused before the fleet walk and planning.
func TestFinalChoreographyRefusesControlStyleConfigImports(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	api := fleet.peer("api")
	api.WriteFile("extra.json", "{}\n")

	res := api.CommandEnv(fileProtocolEnv(), "status", "--configs", "extra.json", "--package", "*")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "--configs imports a repository-local configuration")
	assert.Contains(t, combined, "E332")
	assert.NotContains(t, combined, "release plan ready")
	assert.Empty(t, api.TagList(), "a rejected composition records no release")
}
