// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 52: repository-aware histories. These tests keep the binary boundary
// honest: every source is a real Git repository, every control pointer is a
// real submodule gitlink, and no source configuration is needed in central
// mode. The fixtures use raw JSON while this new public schema is settling;
// the model package owns its wire-format round trips separately.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// polyrepoFile supplies inert release stages and turns off every external
// recorder. A test adds spaces, packages, imports and edges to the returned
// object explicitly, so the configuration under test remains visible.
func polyrepoFile() map[string]any {
	return map[string]any{
		"polyrepo":    true,
		"concurrency": []int{2},
		"logLevel":    "info",
		"logFormat":   "json",
		"updateCheck": false,
		"github":      map[string]any{"enabled": false},
		"changelog":   map[string]any{"enabled": false},
		"commit":      map[string]any{"enabled": false},
		"scripts": map[string]any{
			"build":   []string{"echo building"},
			"publish": []string{"echo publishing"},
		},
		"flow": map[string]any{
			"build":   []string{"build"},
			"publish": []string{"publish"},
		},
	}
}

func writePolyrepoJSON(t *testing.T, r *harness.Repo, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	r.WriteFile(path, string(data)+"\n")
}

// addPolyrepoSource checks out source as a submodule and gives the checkout a
// local identity. Later source commits happen in this checkout, which is the
// exact working tree the binary sees.
func addPolyrepoSource(t *testing.T, control *harness.Repo, name, path string, source *harness.Repo) {
	t.Helper()
	control.Git("-c", "protocol.file.allow=always", "submodule", "add", "-q", "--name", name, source.Root, path)
	control.Git("-C", path, "config", "user.email", "integration@dispat.test")
	control.Git("-C", path, "config", "user.name", "dispat integration")
}

func commitPolyrepoSource(t *testing.T, control *harness.Repo, path, message string) string {
	t.Helper()
	control.Git("-C", path, "add", "-A")
	control.Git("-C", path, "commit", "-q", "-m", message)
	return control.Git("-C", path, "rev-parse", "HEAD")
}

// checkpointPolyrepoSource records the source's current HEAD in the control
// repository. The ordinary chore message carries no release intent of its
// own; the source commit is the only direct unit in the composed history.
func checkpointPolyrepoSource(t *testing.T, control *harness.Repo, path string) {
	t.Helper()
	control.Git("add", path)
	control.Git("commit", "-q", "-m", "chore: update "+filepath.Base(path)+" pointer")
}

func polyrepoTags(control *harness.Repo, path string) []string {
	out := control.Git("-C", path, "tag", "--list")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func centralSpaces(paths map[string]string) map[string]any {
	spaces := make(map[string]any, len(paths))
	for name, path := range paths {
		spaces[name] = map[string]any{"path": []string{path}}
	}
	return spaces
}

// TestPolyrepoCentralOwnershipAndSourceScopedHistory proves the central mode's
// essential lifecycle: child configs are not loaded implicitly, tags and
// baselines belong to the source repositories, a source scope cannot directly
// address a package in another source, and an explicit propagation directive
// still crosses the repository edge exactly once.
func TestPolyrepoCentralOwnershipAndSourceScopedHistory(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	writePolyrepoJSON(t, libSource, "dispat.json", map[string]any{
		"unknownChildKey": "central mode must not load me",
	})
	libSource.Commit("feat(lib): bootstrap library")

	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	writePolyrepoJSON(t, appSource, "dispat.json", map[string]any{
		"unknownChildKey": "central mode must not load me either",
	})
	appSource.Commit("feat(app): bootstrap application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libraries":    "sources/lib/packages",
		"applications": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"lib"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble the control repository")

	initial := control.StatusOK()
	assert.Equal(t, "direct", harness.GraphLine(initial.Events, "lib").Str("reason"))
	assert.Equal(t, "direct", harness.GraphLine(initial.Events, "app").Str("reason"))
	controlBefore := control.Git("rev-parse", "HEAD")
	libBefore := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	appBefore := control.Git("-C", "sources/app", "rev-parse", "HEAD")
	control.ReleaseOK()
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0")
	assert.Contains(t, polyrepoTags(control, "sources/app"), "app@0.1.0")
	assert.Empty(t, control.TagList(), "source releases must not place package tags in the control repository")
	assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"), "disabled control commits stay disabled")
	assert.Equal(t, libBefore, control.Git("-C", "sources/lib", "rev-parse", "HEAD"))
	assert.Equal(t, appBefore, control.Git("-C", "sources/app", "rev-parse", "HEAD"),
		"tag-only source releases do not synthesize record commits")
	cfg["repositoryBaselines"] = []any{
		map[string]any{"consumer": "app", "releaseTag": "app@0.1.0", "repository": "lib-source", "revision": libBefore},
		map[string]any{"consumer": "app", "releaseTag": "app@0.1.0", "repository": "control", "revision": controlBefore},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)

	// The control repository is the fleet-wide intent stream. Its explicit
	// package scope may address app even though app's files live elsewhere.
	control.CommitEmpty("fix(app): fleet-wide release directive")
	fleet := control.StatusOK()
	assert.Equal(t, "direct", harness.GraphLine(fleet.Events, "app").Str("reason"))
	assert.Equal(t, "unchanged", harness.GraphLine(fleet.Events, "lib").Str("message"))
	control.ReleaseOK()
	assert.Contains(t, polyrepoTags(control, "sources/app"), "app@0.1.1")
	directive := control.Git("rev-parse", "HEAD")
	cfg["repositoryBaselines"] = append(cfg["repositoryBaselines"].([]any),
		map[string]any{"consumer": "app", "releaseTag": "app@0.1.1", "repository": "lib-source", "revision": libBefore},
		map[string]any{"consumer": "app", "releaseTag": "app@0.1.1", "repository": "control", "revision": directive},
	)
	writePolyrepoJSON(t, control, "dispat.json", cfg)

	control.WriteFile("sources/lib/packages/lib/api.txt", "two\n")
	commitPolyrepoSource(t, control, "sources/lib", "fix(lib)^: change the shared API")
	checkpointPolyrepoSource(t, control, "sources/lib")
	propagated := control.StatusOK()
	assert.Equal(t, "0.1.0 -> 0.1.1", harness.GraphLine(propagated.Events, "lib").Str("version"))
	assert.Equal(t, "propagated from lib", harness.GraphLine(propagated.Events, "app").Str("reason"))

	// app exists in the fleet, but it is not a package of lib-source. The
	// source commit therefore cannot reinterpret that scope as fleet-wide.
	control.WriteFile("sources/lib/packages/lib/foreign-scope.txt", "one\n")
	commitPolyrepoSource(t, control, "sources/lib", "fix(app): a foreign source scope")
	checkpointPolyrepoSource(t, control, "sources/lib")
	foreign := control.Status()
	assert.Equal(t, 0, foreign.Code, "an invalid authored unit is discarded under the existing commit-error policy")
	assert.True(t, harness.IsCodePresent(foreign.Events, "E130"), "stdout:\n%s\nstderr:\n%s", foreign.Stdout, foreign.Stderr)
	assert.Equal(t, harness.GraphLine(propagated.Events, "app").Str("version"),
		harness.GraphLine(foreign.Events, "app").Str("version"), "the foreign direct scope contributes no bump")
	assert.Equal(t, "propagated from lib", harness.GraphLine(foreign.Events, "app").Str("reason"))
}

// TestPolyrepoControlWildcardIsFleetWide proves that control-history scopes
// resolve against the combined package namespace. Neither source has pending
// work, so both direct patches can come only from the control `*` directive.
func TestPolyrepoControlWildcardIsFleetWide(t *testing.T) {
	first := harness.New(t)
	first.SeedPackage("packages", "a")
	first.Commit("feat(a): initial package")
	first.Git("tag", "-a", "a@1.0.0", "-m", "initial release")
	second := harness.New(t)
	second.SeedPackage("packages", "b")
	second.Commit("feat(b): initial package")
	second.Git("tag", "-a", "b@1.0.0", "-m", "initial release")

	control := harness.New(t)
	control.CommitEmpty("chore: establish control baseline")
	controlBaseline := control.Git("rev-parse", "HEAD")
	addPolyrepoSource(t, control, "first-source", "sources/first", first)
	addPolyrepoSource(t, control, "second-source", "sources/second", second)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"first":  "sources/first/packages",
		"second": "sources/second/packages",
	})
	cfg["repositoryBaselines"] = []any{
		map[string]any{"consumer": "a", "releaseTag": "a@1.0.0", "repository": "control", "revision": controlBaseline},
		map[string]any{"consumer": "b", "releaseTag": "b@1.0.0", "repository": "control", "revision": controlBaseline},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble fleet")
	control.CommitEmpty("fix(*): fleet-wide patch")

	status := control.StatusOK()
	assert.Equal(t, "1.0.0 -> 1.0.1", harness.GraphLine(status.Events, "a").Str("version"))
	assert.Equal(t, "direct", harness.GraphLine(status.Events, "a").Str("reason"))
	assert.Equal(t, "1.0.0 -> 1.0.1", harness.GraphLine(status.Events, "b").Str("version"))
	assert.Equal(t, "direct", harness.GraphLine(status.Events, "b").Str("reason"))
}

// TestPolyrepoOptOutKeepsLegacyPointerHistory leaves every activation input
// absent. The source's own feature commit remains invisible to planning; the
// ordinary control commit that moves the gitlink supplies the legacy patch,
// and the resulting release tag stays in the control repository.
func TestPolyrepoOptOutKeepsLegacyPointerHistory(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): source baseline")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	delete(cfg, "polyrepo")
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble legacy control repository")
	control.Git("tag", "-a", "lib@1.0.0", "-m", "legacy control baseline")

	control.WriteFile("sources/lib/packages/lib/feature.txt", "source feature\n")
	commitPolyrepoSource(t, control, "sources/lib", "feat(lib): source-owned feature")
	control.Git("add", "sources/lib")
	control.Git("commit", "-q", "-m", "fix(lib): advance the ordinary gitlink")

	status := control.StatusOK()
	assert.Equal(t, "1.0.0 -> 1.0.1", harness.GraphLine(status.Events, "lib").Str("version"),
		"legacy mode reads the control patch rather than the nested source feature")
	control.ReleaseOK()
	assert.Contains(t, control.TagList(), "lib@1.0.1")
	assert.Empty(t, polyrepoTags(control, "sources/lib"), "legacy package tags remain control-owned")
}

// TestPolyrepoImportedConfigsAndCLIImports proves both import spellings use
// repository-local paths, imply polyrepo mode, compose two identically named
// spaces without merging their ownership, and retain the explicit opt-out.
func TestPolyrepoImportedConfigsAndCLIImports(t *testing.T) {
	newImportedSource := func(t *testing.T, pkg string) *harness.Repo {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", pkg)
		cfg := polyrepoFile()
		delete(cfg, "polyrepo")
		cfg["spaces"] = map[string]any{
			"workspace": map[string]any{"path": []string{"packages"}},
		}
		writePolyrepoJSON(t, source, "dispat.json", cfg)
		source.Commit("feat(" + pkg + "): bootstrap imported package")
		return source
	}

	libSource := newImportedSource(t, "lib")
	appSource := newImportedSource(t, "app")
	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)

	central := polyrepoFile()
	delete(central, "polyrepo") // imports alone imply repository-aware mode
	central["configs"] = []string{"sources/lib/dispat.json", "sources/app/dispat.json"}
	writePolyrepoJSON(t, control, "central.json", central)
	control.WriteFile("dispat.json", "{\n  \"unknownDefault\": true\n}\n")
	control.Commit("chore: import the source configurations")

	fromFile := control.Command("status", "--config", "central.json")
	require.Equal(t, 0, fromFile.Code, "stdout:\n%s\nstderr:\n%s", fromFile.Stdout, fromFile.Stderr)
	assert.Equal(t, "direct", harness.GraphLine(fromFile.Events, "lib").Str("reason"))
	assert.Equal(t, "direct", harness.GraphLine(fromFile.Events, "app").Str("reason"))

	disabled := control.Command("status", "--config", "central.json", "--polyrepo=false")
	assert.Equal(t, 2, disabled.Code, "imports and an explicit single-repository mode are contradictory")
	assert.Contains(t, strings.ToLower(disabled.Stdout+disabled.Stderr), "polyrepo")

	// Keep --config as the control file while supplying the same source list
	// on the command line. Repeating --configs appends; it does not replace.
	noImports := polyrepoFile()
	delete(noImports, "polyrepo")
	writePolyrepoJSON(t, control, "cli.json", noImports)
	fromCLI := control.Command("status", "--config", "cli.json",
		"--configs", "sources/lib/dispat.json",
		"--configs", "sources/app/dispat.json")
	require.Equal(t, 0, fromCLI.Code, "stdout:\n%s\nstderr:\n%s", fromCLI.Stdout, fromCLI.Stderr)
	assert.Equal(t, "direct", harness.GraphLine(fromCLI.Events, "lib").Str("reason"))
	assert.Equal(t, "direct", harness.GraphLine(fromCLI.Events, "app").Str("reason"))
}

// TestPolyrepoConfigImportResolvesFromDeclaringFragment places `configs` in a
// root-level $ref. Its relative path starts at that fragment, while a CLI
// import starts at the control root; both spellings reach the same source.
func TestPolyrepoConfigImportResolvesFromDeclaringFragment(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	imported := polyrepoFile()
	delete(imported, "polyrepo")
	imported["spaces"] = centralSpaces(map[string]string{"workspace": "packages"})
	writePolyrepoJSON(t, source, "dispat.json", imported)
	source.Commit("feat(lib): bootstrap imported library")
	source.Git("tag", "-a", "lib@1.0.0", "-m", "known baseline")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	control.WriteFile("cfg/imports.json", "{\n  \"configs\": [\"../sources/lib/dispat.json\"]\n}\n")
	central := polyrepoFile()
	delete(central, "polyrepo")
	central["$ref"] = "./cfg/imports.json"
	writePolyrepoJSON(t, control, "dispat.json", central)
	control.Commit("chore: import from a fragment")

	fromFragment := control.StatusOK()
	assert.NotEmpty(t, harness.GraphLine(fromFragment.Events, "lib").Str("message"),
		"the fragment-relative import discovers lib")

	cliControl := polyrepoFile()
	delete(cliControl, "polyrepo")
	writePolyrepoJSON(t, control, "cli.json", cliControl)
	fromCLI := control.Command("status", "--config", "cli.json", "--configs", "sources/lib/dispat.json")
	require.Equal(t, 0, fromCLI.Code, "stdout:\n%s\nstderr:\n%s", fromCLI.Stdout, fromCLI.Stderr)
	assert.NotEmpty(t, harness.GraphLine(fromCLI.Events, "lib").Str("message"),
		"the CLI import resolves from the control root")

	// The import list may itself be assembled from `$ref` fragments, one of
	// them named by an absolute path, which a generated configuration is
	// entitled to produce. Each fragment's paths are read relative to the
	// fragment, which is why the list is resolved before it is decoded.
	t.Run("a list of fragments, one named by an absolute path", func(t *testing.T) {
		control := covPolyrepoImportFleet(t)
		// Each fragment lives beside the sources it names, and names them
		// relative to itself rather than to the control file.
		control.WriteFile("fragments/first.json", `["../sources/one/dispat.json"]`+"\n")
		absolute := filepath.Join(t.TempDir(), "second.json")
		// The control root is canonicalized before paths are held against it,
		// so the fragment names the same canonical spelling.
		canonicalRoot, err := filepath.EvalSymlinks(control.Root)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(absolute,
			[]byte(`["`+filepath.Join(canonicalRoot, "sources", "two", "dispat.json")+`"]`+"\n"), 0o644))
		control.WriteConfigRaw(map[string]any{
			"polyrepo":    true,
			"logFormat":   "json",
			"logLevel":    "info",
			"updateCheck": false,
			"github":      map[string]any{"enabled": false},
			"configs":     map[string]any{"$ref": []string{"fragments/first.json", absolute}},
		})
		control.Commit("chore: import both sources through fragments")

		found := covPolyrepoImported(control.StatusOK())
		assert.True(t, found["one"], "a fragment's relative path is read from the fragment")
		assert.True(t, found["two"], "and a fragment may be named by an absolute path")
	})

}

// TestPolyrepoCanonicalConfigImportsAreDeduplicated proves that two spellings
// of the same imported file compose one owner. It also keeps a missing explicit
// import fatal instead of silently falling back to an implicit child config.
func TestPolyrepoCanonicalConfigImportsAreDeduplicated(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	imported := polyrepoFile()
	delete(imported, "polyrepo")
	imported["spaces"] = centralSpaces(map[string]string{"workspace": "packages"})
	writePolyrepoJSON(t, source, "dispat.json", imported)
	source.Commit("feat(lib): bootstrap imported library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	central := polyrepoFile()
	delete(central, "polyrepo")
	central["configs"] = []string{
		"sources/lib/dispat.json",
		"sources/lib/./dispat.json",
	}
	writePolyrepoJSON(t, control, "dispat.json", central)
	control.Commit("chore: repeat the same canonical import")

	deduplicated := control.Status()
	require.Equal(t, 0, deduplicated.Code, "stdout:\n%s\nstderr:\n%s", deduplicated.Stdout, deduplicated.Stderr)
	assert.NotEmpty(t, harness.GraphLine(deduplicated.Events, "lib").Str("message"))

	missing := control.Command("status", "--configs", "sources/lib/missing.json")
	assert.NotZero(t, missing.Code)
	assert.Contains(t, strings.ToLower(missing.Stdout+missing.Stderr), "missing.json")
}

// TestPolyrepoMixedCentralAndImportedPackages declares one source package in
// the control config and imports another source's config. Their histories
// remain disjoint while the composed graph and selector see both.
func TestPolyrepoMixedCentralAndImportedPackages(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	imported := polyrepoFile()
	delete(imported, "polyrepo")
	imported["spaces"] = centralSpaces(map[string]string{"libraries": "packages"})
	writePolyrepoJSON(t, source, "dispat.json", imported)
	source.Commit("feat(lib): bootstrap imported library")
	hostedSource := harness.New(t)
	hostedSource.SeedPackage("packages", "hosted")
	hostedSource.Commit("feat(hosted): bootstrap centrally declared package")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	addPolyrepoSource(t, control, "hosted-source", "sources/hosted", hostedSource)
	central := polyrepoFile()
	delete(central, "polyrepo")
	central["configs"] = []string{"sources/lib/dispat.json"}
	central["spaces"] = centralSpaces(map[string]string{"hosted": "sources/hosted/packages"})
	writePolyrepoJSON(t, control, "dispat.json", central)
	control.Commit("chore: assemble the mixed workspace")

	all := control.StatusOK()
	assert.Equal(t, "direct", harness.GraphLine(all.Events, "lib").Str("reason"))
	assert.Equal(t, "direct", harness.GraphLine(all.Events, "hosted").Str("reason"))
	selected := control.StatusOK("--package", "lib")
	assert.Equal(t, "⊝ not selected", harness.GraphLine(selected.Events, "hosted").Str("message"))
	assert.Equal(t, "● changed", harness.GraphLine(selected.Events, "lib").Str("message"))
}

// TestPolyrepoSinceControlRevisionProjectsSourceGitlinks asks for one control
// boundary and expects repository-local ranges from its historical gitlinks.
// The control pointer commit is deliberately `fix`: counting it as package
// work would falsely release the source whose own range contains only chore.
func TestPolyrepoSinceControlRevisionProjectsSourceGitlinks(t *testing.T) {
	first := harness.New(t)
	first.SeedPackage("packages", "a")
	first.Commit("feat(a): initial package")
	first.Git("tag", "-a", "a@1.0.0", "-m", "initial release")
	second := harness.New(t)
	second.SeedPackage("packages", "b")
	second.Commit("feat(b): initial package")
	second.Git("tag", "-a", "b@1.0.0", "-m", "initial release")

	control := harness.New(t)
	addPolyrepoSource(t, control, "first-source", "sources/first", first)
	addPolyrepoSource(t, control, "second-source", "sources/second", second)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"first":  "sources/first/packages",
		"second": "sources/second/packages",
	})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: establish projected source boundaries")
	boundary := control.Git("rev-parse", "HEAD")

	control.WriteFile("sources/first/packages/a/feature.txt", "feature\n")
	commitPolyrepoSource(t, control, "sources/first", "feat(a): source feature")
	control.WriteFile("sources/first/packages/a/fix.txt", "fix\n")
	commitPolyrepoSource(t, control, "sources/first", "fix(a): source fix")
	control.WriteFile("sources/second/packages/b/maintenance.txt", "maintenance\n")
	commitPolyrepoSource(t, control, "sources/second", "chore(b): source maintenance")
	control.Git("add", "sources/first", "sources/second")
	control.Git("commit", "-q", "-m", "fix: advance both gitlinks")

	cfg["scripts"].(map[string]any)["inspect"] = []string{"echo inspect"}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	status := control.RunScriptOK("inspect", "--since", boundary)
	assert.Equal(t, "1.0.0 -> 1.1.0", harness.GraphLine(status.Events, "a").Str("version"))
	assert.Equal(t, "unchanged", harness.GraphLine(status.Events, "b").Str("message"),
		"the control gitlink fix is evidence, not a second direct package change")
}

// TestPolyrepoImportedDefaultsAndNestedCommandContext proves an imported
// repository keeps its own shell, environment and scripts. A nested dispat
// launched from lib's package directory also keeps the outer composed
// workspace, so it can select app from a different imported repository.
func TestPolyrepoImportedDefaultsAndNestedCommandContext(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libConfig := polyrepoFile()
	delete(libConfig, "polyrepo")
	libConfig["spaces"] = centralSpaces(map[string]string{"workspace": "packages"})
	writePolyrepoJSON(t, libSource, "dispat.json", libConfig)
	libSource.Commit("feat(lib): bootstrap library")

	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appConfig := polyrepoFile()
	delete(appConfig, "polyrepo")
	appConfig["spaces"] = centralSpaces(map[string]string{"workspace": "packages"})
	writePolyrepoJSON(t, appSource, "dispat.json", appConfig)
	appSource.Commit("feat(app): bootstrap application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	central := polyrepoFile()
	delete(central, "polyrepo")
	central["configs"] = []string{"sources/lib/dispat.json", "sources/app/dispat.json"}
	central["env"] = map[string]any{"OWNER": "control"}
	central["shell"] = []string{"sh", "-c"}
	writePolyrepoJSON(t, control, "dispat.json", central)
	control.Commit("chore: assemble imported repositories")

	// Edit the config in the checked-out source so the absolute nested binary
	// is the exact artifact this test invocation built.
	libConfig["env"] = map[string]any{"OWNER": "lib-source"}
	libConfig["shell"] = []string{"bash", "-c"}
	libConfig["scripts"] = map[string]any{
		"build":   []string{"echo building"},
		"publish": []string{"echo publishing"},
		"report": []string{
			`[[ "$OWNER" == lib-source ]] || exit 41; printf '%s:%s\n' "$OWNER" "$DISPAT_PACKAGE" > ../../owner.log`,
		},
		"nested": []string{
			control.DispatCommand("status", "--package", "app") + " > ../../../../nested.jsonl",
		},
	}
	writePolyrepoJSON(t, control, "sources/lib/dispat.json", libConfig)
	commitPolyrepoSource(t, control, "sources/lib", "chore: configure source-local scripts")
	checkpointPolyrepoSource(t, control, "sources/lib")

	control.RunScriptOK("report", "--since", "all", "--package", "lib")
	owner, err := os.ReadFile(control.Path("sources/lib/owner.log"))
	require.NoError(t, err)
	assert.Equal(t, "lib-source:lib\n", string(owner))

	control.RunScriptOK("nested", "--since", "all", "--package", "lib")
	nested, err := os.ReadFile(control.Path("nested.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(nested), `"package":"app"`,
		"the child process retains the outer control config and both imports")
}

// TestPolyrepoNestedStepsReadLiveCommitPin runs two standalone record steps
// and a nested planner in one source-owned shell. The commit step appends its
// new source HEAD to the live DISPAT_OUTPUT file; the following process must
// accept that exact pin even though the control gitlink has not moved yet.
func TestPolyrepoNestedStepsReadLiveCommitPin(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libConfig := polyrepoFile()
	delete(libConfig, "polyrepo")
	libConfig["spaces"] = centralSpaces(map[string]string{"workspace": "packages"})
	libConfig["changelog"] = map[string]any{"enabled": true}
	libConfig["commit"] = map[string]any{"enabled": true}
	writePolyrepoJSON(t, libSource, "dispat.json", libConfig)
	libSource.Commit("feat(lib): bootstrap library")

	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appConfig := polyrepoFile()
	delete(appConfig, "polyrepo")
	appConfig["spaces"] = centralSpaces(map[string]string{"workspace": "packages"})
	writePolyrepoJSON(t, appSource, "dispat.json", appConfig)
	appSource.Commit("feat(app): bootstrap application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	central := polyrepoFile()
	delete(central, "polyrepo")
	central["configs"] = []string{"sources/lib/dispat.json", "sources/app/dispat.json"}
	writePolyrepoJSON(t, control, "dispat.json", central)
	control.Commit("chore: assemble nested record workspace")
	controlPin := control.Git("rev-parse", "HEAD:sources/lib")

	libConfig["scripts"] = map[string]any{
		"build":   []string{"echo building"},
		"publish": []string{"echo publishing"},
		"record-and-inspect": []string{
			control.DispatCommand("changelog"),
			control.DispatCommand("commit", "--tag"),
			control.DispatCommand("status", "--package", "app") + " > ../../../../nested-after-live-pin.jsonl",
		},
	}
	writePolyrepoJSON(t, control, "sources/lib/dispat.json", libConfig)
	commitPolyrepoSource(t, control, "sources/lib", "chore: configure nested record steps")
	checkpointPolyrepoSource(t, control, "sources/lib")
	controlPin = control.Git("rev-parse", "HEAD:sources/lib")

	control.RunScriptOK("record-and-inspect", "--since", "all", "--package", "lib")
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0")
	assert.NotEqual(t, controlPin, control.Git("-C", "sources/lib", "rev-parse", "HEAD"),
		"the nested commit moves the source before any control checkpoint")
	nested, err := os.ReadFile(control.Path("nested-after-live-pin.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(nested), `"package":"app"`)
}

// TestPolyrepoNestedInterleavedOwnerPinsRetainEveryCandidate exercises the
// A/lib -> B/service -> A/tool order. Nested commits leave two valid candidate
// SHAs for source A with source B between them; the final nested plan may use
// A's newest exact candidate without losing or confusing either owner.
func TestPolyrepoNestedInterleavedOwnerPinsRetainEveryCandidate(t *testing.T) {
	aSource := harness.New(t)
	aSource.SeedPackage("packages", "lib")
	aSource.SeedPackage("packages", "tool")
	aSource.Commit("feat(lib,tool): bootstrap source A")
	aSource.Git("tag", "-a", "lib@1.0.0", "-m", "initial library")
	aSource.Git("tag", "-a", "tool@1.0.0", "-m", "initial tool")
	bSource := harness.New(t)
	bSource.SeedPackage("packages", "service")
	bSource.Commit("feat(service): bootstrap source B")
	bSource.Git("tag", "-a", "service@1.0.0", "-m", "initial service")

	control := harness.New(t)
	addPolyrepoSource(t, control, "a-source", "sources/a", aSource)
	addPolyrepoSource(t, control, "b-source", "sources/b", bSource)
	cfg := polyrepoFile()
	cfg["concurrency"] = []int{1}
	cfg["spaces"] = centralSpaces(map[string]string{
		"a": "sources/a/packages",
		"b": "sources/b/packages",
	})
	cfg["dependencies"] = map[string]any{
		"service": []any{"lib"},
		"tool":    []any{"service"},
	}
	cfg["repositoryBaselines"] = []any{
		map[string]any{"consumer": "service", "releaseTag": "service@1.0.0", "repository": "a-source", "revision": "lib@1.0.0"},
		map[string]any{"consumer": "tool", "releaseTag": "tool@1.0.0", "repository": "b-source", "revision": "service@1.0.0"},
	}
	cfg["scripts"] = map[string]any{
		"build":   []string{"echo building"},
		"publish": []string{"echo publishing"},
		"record-and-inspect": []string{
			`printf '%s\n' "$DISPAT_PACKAGE" > "nested-$DISPAT_PACKAGE.txt"`,
			control.DispatCommand("commit"),
			`if [ "$DISPAT_PACKAGE" = tool ]; then ` +
				control.DispatCommand("status", "--package", "lib") +
				` > ../../../../interleaved-nested.jsonl; fi`,
		},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble interleaved nested workspace")
	control.WriteFile("sources/a/packages/lib/next.txt", "next library\n")
	control.WriteFile("sources/a/packages/tool/next.txt", "next tool\n")
	commitPolyrepoSource(t, control, "sources/a", "feat(lib,tool): advance source A")
	checkpointPolyrepoSource(t, control, "sources/a")
	control.WriteFile("sources/b/packages/service/next.txt", "next service\n")
	commitPolyrepoSource(t, control, "sources/b", "feat(service): advance source B")
	checkpointPolyrepoSource(t, control, "sources/b")
	aPin := control.Git("rev-parse", "HEAD:sources/a")
	bPin := control.Git("rev-parse", "HEAD:sources/b")

	control.RunScriptOK("record-and-inspect", "--since", "all")
	assert.NotEqual(t, aPin, control.Git("-C", "sources/a", "rev-parse", "HEAD"))
	assert.NotEqual(t, bPin, control.Git("-C", "sources/b", "rev-parse", "HEAD"))
	assert.Equal(t, 2, strings.Count(control.Git("-C", "sources/a", "log", "--format=%s", aPin+"..HEAD"), "chore(release):"),
		"source A records lib, leaves for source B, then records tool")
	assert.Equal(t, 1, strings.Count(control.Git("-C", "sources/b", "log", "--format=%s", bPin+"..HEAD"), "chore(release):"))
	nested, err := os.ReadFile(control.Path("interleaved-nested.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(nested), `"package":"lib"`)
}

// TestPolyrepoNestedCommitTagsSerializePerOwnerAndRunOwnersInParallel gives
// one source two independent packages and another source one package. The
// source-A nested Git writes must serialize, while a bounded file handshake
// proves one of them can still publish beside source B under concurrency 2.
func TestPolyrepoNestedCommitTagsSerializePerOwnerAndRunOwnersInParallel(t *testing.T) {
	aSource := harness.New(t)
	aSource.SeedPackage("packages", "alpha")
	aSource.SeedPackage("packages", "beta")
	aSource.Commit("feat(alpha,beta): bootstrap source A")
	bSource := harness.New(t)
	bSource.SeedPackage("packages", "gamma")
	bSource.Commit("feat(gamma): bootstrap source B")

	control := harness.New(t)
	addPolyrepoSource(t, control, "a-source", "sources/a", aSource)
	addPolyrepoSource(t, control, "b-source", "sources/b", bSource)
	cfg := polyrepoFile()
	cfg["concurrency"] = []int{2}
	cfg["spaces"] = centralSpaces(map[string]string{
		"a": "sources/a/packages",
		"b": "sources/b/packages",
	})
	cfg["scripts"] = map[string]any{
		"build": []string{"echo building"},
		"publish": []string{
			`case "$DISPAT_PACKAGE" in gamma) owner=b; peer=a;; *) owner=a; peer=b;; esac; ` +
				`marker=../../../../owner-$owner.started; peer_marker=../../../../owner-$peer.started; ` +
				`: > "$marker"; attempts=0; ` +
				`while [ ! -f "$peer_marker" ] && [ "$attempts" -lt 300 ]; do attempts=$((attempts + 1)); sleep 0.01; done; ` +
				`test -f "$peer_marker"; printf '%s\n' "$DISPAT_PACKAGE" > nested-release.txt`,
			control.DispatCommand("commit", "--tag"),
		},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble owner-concurrent nested flow")

	res := control.Release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.FileExists(t, control.Path("owner-a.started"))
	assert.FileExists(t, control.Path("owner-b.started"))
	assert.ElementsMatch(t, []string{"alpha@0.1.0", "beta@0.1.0"}, polyrepoTags(control, "sources/a"))
	assert.Equal(t, []string{"gamma@0.1.0"}, polyrepoTags(control, "sources/b"))
	assert.Equal(t, 2, strings.Count(control.Git("-C", "sources/a", "log", "--format=%s"), "chore(release):"),
		"same-owner nested Git writes complete as two distinct release commits")
	assert.Equal(t, 1, strings.Count(control.Git("-C", "sources/b", "log", "--format=%s"), "chore(release):"))
}

// TestPolyrepoConcurrentNestedCommandReadsPinsPublishedAfterShellStart keeps
// both source publish shells alive while source A records its release. Source
// B then invokes nested fleet commands from the environment it received
// before A moved, so only the live verified-pin channel can authorize A's new
// HEAD. The two start markers also prove this is not satisfied by globally
// serializing package scripts.
func TestPolyrepoConcurrentNestedCommandReadsPinsPublishedAfterShellStart(t *testing.T) {
	aSource := harness.New(t)
	aSource.SeedPackage("packages", "alpha")
	aSource.Commit("feat(alpha): bootstrap source A")
	bSource := harness.New(t)
	bSource.SeedPackage("packages", "beta")
	bSource.Commit("feat(beta): bootstrap source B")

	control := harness.New(t)
	addPolyrepoSource(t, control, "a-source", "sources/a", aSource)
	addPolyrepoSource(t, control, "b-source", "sources/b", bSource)
	status := control.DispatCommand("status", "--package", "beta", "--log-format", "json")
	commit := control.DispatCommand("commit", "--tag")
	publish := `wait_for() { marker=$1; attempts=0; while [ ! -f "$marker" ] && [ "$attempts" -lt 300 ]; do attempts=$((attempts + 1)); sleep 0.01; done; test -f "$marker"; }; ` +
		`case "$DISPAT_PACKAGE" in ` +
		`alpha) : > ../../../../alpha.started; wait_for ../../../../beta.started; ` +
		`printf 'alpha\n' > nested-release.txt; ` + commit + `; : > ../../../../alpha.recorded;; ` +
		`beta) : > ../../../../beta.started; wait_for ../../../../alpha.started; wait_for ../../../../alpha.recorded; ` +
		status + ` > ../../../../beta-nested-status.jsonl; ` +
		`printf 'beta\n' > nested-release.txt; ` + commit + `;; ` +
		`esac`
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"a": "sources/a/packages",
		"b": "sources/b/packages",
	})
	cfg["scripts"] = map[string]any{
		"build":   []string{"echo building"},
		"publish": []string{publish},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble live-pin concurrency workspace")

	res := control.Release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.FileExists(t, control.Path("alpha.started"))
	assert.FileExists(t, control.Path("beta.started"))
	assert.FileExists(t, control.Path("alpha.recorded"))
	assert.Equal(t, []string{"alpha@0.1.0"}, polyrepoTags(control, "sources/a"))
	assert.Equal(t, []string{"beta@0.1.0"}, polyrepoTags(control, "sources/b"))
	nested, err := os.ReadFile(control.Path("beta-nested-status.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(nested), `"package":"beta"`)
}

// TestPolyrepoNestedForeignOwnerExportCannotAuthorizeSourceHead ensures the
// package-to-owner map, rather than the script's cwd, grants temporary pins.
// A lib export carrying app-source's new SHA therefore cannot authorize the
// app checkout after that source moves ahead of its control gitlink.
func TestPolyrepoNestedForeignOwnerExportCannotAuthorizeSourceHead(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.Commit("feat(lib): bootstrap library")
	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appSource.WriteFile("packages/app/foreign.txt", "before\n")
	appSource.Commit("feat(app): bootstrap application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages",
		"apps": "sources/app/packages",
	})
	cfg["scripts"] = map[string]any{
		"build":   []string{"echo building"},
		"publish": []string{"echo publishing"},
		"foreign-pin": []string{
			`printf 'after\n' > ../../../app/packages/app/foreign.txt`,
			`git -C ../../../app add packages/app/foreign.txt && git -C ../../../app commit -q -m 'fix(app): foreign advance'`,
			`printf 'PACKAGE_LIB=%s\n' "$(git -C ../../../app rev-parse HEAD)" >> "$DISPAT_OUTPUT"`,
			control.DispatCommand("status", "--log-format", "json") + " > ../../../../foreign-nested.jsonl 2>&1",
		},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble foreign pin workspace")

	res := control.RunScript("foreign-pin", "--since", "all", "--package", "lib")
	assert.NotZero(t, res.Code)
	nested, err := os.ReadFile(control.Path("foreign-nested.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(nested), `"code":"E330"`)
	assert.Contains(t, string(nested), "app-source")
}

// TestPolyrepoImportedSameNameSpacesHaveIndependentLogins gives two imported
// repositories the same local space name and distinct credentials markers.
// Each repository must pass through its own login gate under its own config.
func TestPolyrepoImportedSameNameSpacesHaveIndependentLogins(t *testing.T) {
	newSource := func(t *testing.T, pkg string) *harness.Repo {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", pkg)
		cfg := polyrepoFile()
		delete(cfg, "polyrepo")
		cfg["env"] = map[string]any{"OWNER": pkg}
		cfg["spaces"] = map[string]any{
			"workspace": map[string]any{
				"path": []string{"packages"},
				"flow": map[string]any{
					"build": []string{"build"}, "publish": []string{"publish"}, "login": []string{"login"},
				},
			},
		}
		cfg["scripts"] = map[string]any{
			"build": []string{"echo building"}, "publish": []string{"echo publishing"},
			"login": []string{`printf '%s\n' "$OWNER" > "login-$OWNER.txt"`},
		}
		writePolyrepoJSON(t, source, "dispat.json", cfg)
		source.Commit("feat(" + pkg + "): bootstrap imported package")
		return source
	}
	aSource := newSource(t, "a")
	bSource := newSource(t, "b")
	control := harness.New(t)
	addPolyrepoSource(t, control, "a-source", "sources/a", aSource)
	addPolyrepoSource(t, control, "b-source", "sources/b", bSource)
	central := polyrepoFile()
	delete(central, "polyrepo")
	central["configs"] = []string{"sources/a/dispat.json", "sources/b/dispat.json"}
	writePolyrepoJSON(t, control, "dispat.json", central)
	control.Commit("chore: import equal-name spaces")

	control.ReleaseOK()
	aLogin, err := os.ReadFile(control.Path("sources/a/packages/login-a.txt"))
	require.NoError(t, err)
	bLogin, err := os.ReadFile(control.Path("sources/b/packages/login-b.txt"))
	require.NoError(t, err)
	assert.Equal(t, "a\n", string(aLogin))
	assert.Equal(t, "b\n", string(bLogin))
	assert.NoFileExists(t, control.Path("sources/a/packages/login-b.txt"))
	assert.NoFileExists(t, control.Path("sources/b/packages/login-a.txt"))
}

// TestPolyrepoImportedGitHubPoliciesUseSourceOwners gives each imported
// repository its own fake GitHub endpoint and coordinates. A release from the
// composed fleet must route only that source's package to each endpoint.
func TestPolyrepoImportedGitHubPoliciesUseSourceOwners(t *testing.T) {
	libServer, libBodies := githubFake(t)
	appServer, appBodies := githubFake(t)
	newSource := func(t *testing.T, pkg, repo, endpoint string) *harness.Repo {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", pkg)
		cfg := polyrepoFile()
		delete(cfg, "polyrepo")
		cfg["spaces"] = centralSpaces(map[string]string{"workspace": "packages"})
		cfg["github"] = map[string]any{
			"enabled":     true,
			"allPackages": true,
			"owner":       "acme",
			"repo":        repo,
			"apiUrl":      endpoint,
			"tokenEnv":    "DISPAT_IT_TOKEN",
		}
		writePolyrepoJSON(t, source, "dispat.json", cfg)
		source.Commit("feat(" + pkg + "): bootstrap source")
		return source
	}

	libSource := newSource(t, "lib", "libraries", libServer.URL)
	appSource := newSource(t, "app", "applications", appServer.URL)
	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	central := polyrepoFile()
	delete(central, "polyrepo")
	central["configs"] = []string{"sources/lib/dispat.json", "sources/app/dispat.json"}
	writePolyrepoJSON(t, control, "dispat.json", central)
	control.Commit("chore: import source release policies")
	t.Setenv("DISPAT_IT_TOKEN", "token")

	res := control.Release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	libReleases := decodeAll[ghBody](t, libBodies())
	appReleases := decodeAll[ghBody](t, appBodies())
	require.Len(t, libReleases, 1)
	require.Len(t, appReleases, 1)
	assert.Equal(t, "lib@0.1.0", libReleases[0].TagName)
	assert.Equal(t, "app@0.1.0", appReleases[0].TagName)
}

// TestPolyrepoExternalDependencyIsActiveWhenPresent proves external is only
// an allowance for an absent provider. Once the provider joins the composed
// workspace, the same declaration becomes an ordinary active graph edge.
func TestPolyrepoExternalDependencyIsActiveWhenPresent(t *testing.T) {
	t.Run("missing provider is skipped", func(t *testing.T) {
		appSource := harness.New(t)
		appSource.SeedPackage("packages", "app")
		appSource.Commit("feat(app): bootstrap app")
		control := harness.New(t)
		addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
		cfg := polyrepoFile()
		cfg["spaces"] = centralSpaces(map[string]string{"apps": "sources/app/packages"})
		cfg["dependencies"] = map[string]any{
			"app": []any{map[string]any{"provider": "lib", "external": true}},
		}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: assemble app")

		status := control.StatusOK()
		assert.Empty(t, dependsOn(status, "app"))
		assert.True(t, harness.IsCodePresentForPackage(status.Events, "W330", "app"),
			"the skipped optional provider remains visible in structured diagnostics")
	})

	t.Run("missing provider does not excuse an invalid edge kind", func(t *testing.T) {
		appSource := harness.New(t)
		appSource.SeedPackage("packages", "app")
		appSource.Commit("feat(app): bootstrap app")
		control := harness.New(t)
		addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
		cfg := polyrepoFile()
		cfg["spaces"] = centralSpaces(map[string]string{"apps": "sources/app/packages"})
		cfg["dependencies"] = map[string]any{
			"app": []any{map[string]any{
				"provider": "lib",
				"external": true,
				"kind":     "not-a-manifest-kind",
			}},
		}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: configure invalid external edge")

		status := control.Status()
		assert.NotZero(t, status.Code)
		assert.Contains(t, strings.ToLower(status.Stdout+status.Stderr), "kind")
	})

	t.Run("included provider is an active edge", func(t *testing.T) {
		libSource := harness.New(t)
		libSource.SeedPackage("packages", "lib")
		libSource.Commit("feat(lib): bootstrap lib")
		appSource := harness.New(t)
		appSource.SeedPackage("packages", "app")
		appSource.Commit("feat(app): bootstrap app")
		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
		addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
		cfg := polyrepoFile()
		cfg["spaces"] = centralSpaces(map[string]string{
			"libs": "sources/lib/packages",
			"apps": "sources/app/packages",
		})
		cfg["dependencies"] = map[string]any{
			"app": []any{map[string]any{"provider": "lib", "external": true}},
		}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: assemble fleet")

		status := control.StatusOK()
		assert.Contains(t, dependsOn(status, "app"), "lib")
	})

	t.Run("included external edge participates in cycle detection", func(t *testing.T) {
		libSource := harness.New(t)
		libSource.SeedPackage("packages", "lib")
		libSource.Commit("feat(lib): bootstrap lib")
		appSource := harness.New(t)
		appSource.SeedPackage("packages", "app")
		appSource.Commit("feat(app): bootstrap app")
		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
		addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
		cfg := polyrepoFile()
		cfg["spaces"] = centralSpaces(map[string]string{
			"libs": "sources/lib/packages",
			"apps": "sources/app/packages",
		})
		cfg["dependencies"] = map[string]any{
			"app": []any{map[string]any{"provider": "lib", "external": true}},
			"lib": []any{"app"},
		}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: assemble cyclic fleet")

		status := control.Status()
		assert.NotZero(t, status.Code)
		assert.True(t, harness.IsCodePresent(status.Events, "E200"), "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	})

	t.Run("active edge preserves case selection propagation order and blocking", func(t *testing.T) {
		libSource := harness.New(t)
		libSource.SeedPackage("packages", "lib")
		libSource.Commit("feat(lib)^: bootstrap propagating provider")
		appSource := harness.New(t)
		appSource.SeedPackage("packages", "app")
		appSource.Commit("chore(app): bootstrap consumer")
		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
		addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
		cfg := polyrepoFile()
		cfg["concurrency"] = []int{1}
		cfg["spaces"] = map[string]any{
			"libs": map[string]any{
				"path":                  []string{"sources/lib/packages"},
				"isBuildWaitingPublish": true,
			},
			"apps": map[string]any{"path": []string{"sources/app/packages"}},
		}
		cfg["dependencies"] = map[string]any{
			"app": []any{map[string]any{"provider": "LIB", "external": true}},
		}
		cfg["scripts"] = map[string]any{
			"build": []string{`printf '%s build\n' "$DISPAT_PACKAGE" >> ../../../../order.log`},
			"publish": []string{
				`printf '%s publish\n' "$DISPAT_PACKAGE" >> ../../../../order.log; test "$DISPAT_PACKAGE" != lib`,
			},
			"selected": []string{`printf '%s\n' "$DISPAT_PACKAGE" >> ../../../../selected.log`},
		}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: assemble active external edge")

		status := control.StatusOK()
		assert.Equal(t, "propagated from lib", harness.GraphLine(status.Events, "app").Str("reason"))
		control.RunScriptOK("selected", "--package", "lib", "--consumers")
		selected, err := os.ReadFile(control.Path("selected.log"))
		require.NoError(t, err)
		assert.Equal(t, "lib\napp\n", string(selected),
			"case-insensitive external provider resolution participates in consumer selection")

		failed := control.Release()
		assert.Equal(t, 1, failed.Code)
		order, err := os.ReadFile(control.Path("order.log"))
		require.NoError(t, err)
		assert.Equal(t, "lib build\nlib publish\n", string(order),
			"the provider is attempted first and its failure blocks the waiting consumer")
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
		assert.Empty(t, polyrepoTags(control, "sources/app"))
	})
}

// TestPolyrepoComputePreservesExternalDependency writes a newly detected
// source-to-source manifest edge beside a kept external edge. The config
// rewrite must retain external, kind and keep on the original provider.
func TestPolyrepoComputePreservesExternalDependency(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.WriteFile("packages/lib/package.json", `{"name":"@acme/lib","version":"1.0.0"}`)
	libSource.Commit("feat(lib): bootstrap lib")
	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appSource.WriteFile("packages/app/package.json",
		`{"name":"@acme/app","version":"1.0.0","dependencies":{"@acme/lib":"^1.0.0"}}`)
	appSource.Commit("feat(app): bootstrap app")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages",
		"apps": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{
		"app": []any{map[string]any{
			"provider": "remote-sdk",
			"kind":     "optionalDependencies",
			"keep":     true,
			"external": true,
		}},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure external provider")

	res := control.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	data, err := os.ReadFile(control.Path("dispat.json"))
	require.NoError(t, err)
	var written map[string]any
	require.NoError(t, json.Unmarshal(data, &written))
	deps, ok := written["dependencies"].(map[string]any)
	require.True(t, ok, "dependencies stays in canonical consumer-keyed form: %s", data)
	providers, ok := deps["app"].([]any)
	require.True(t, ok, "app keeps both provider entries: %s", data)
	require.Len(t, providers, 2)
	assert.Contains(t, string(data), `"provider": "remote-sdk"`)
	assert.Contains(t, string(data), `"kind": "optionalDependencies"`)
	assert.Contains(t, string(data), `"keep": true`)
	assert.Contains(t, string(data), `"external": true`)
	assert.Contains(t, string(data), `"lib"`, "compute adds the detected in-workspace provider")
}

// TestPolyrepoComputeWritesImportedOwnerConfig derives an edge whose consumer
// is declared by an imported source config. The edit belongs in that source's
// file; the imports-only control file remains byte-for-byte unchanged.
func TestPolyrepoComputeWritesImportedOwnerConfig(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.WriteFile("packages/lib/package.json", `{"name":"@acme/lib","version":"1.0.0"}`)
	libConfig := polyrepoFile()
	delete(libConfig, "polyrepo")
	libConfig["spaces"] = centralSpaces(map[string]string{"workspace": "packages"})
	writePolyrepoJSON(t, libSource, "dispat.json", libConfig)
	libSource.Commit("feat(lib): bootstrap lib")

	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appSource.WriteFile("packages/app/package.json",
		`{"name":"@acme/app","version":"1.0.0","dependencies":{"@acme/lib":"^1.0.0"}}`)
	appConfig := polyrepoFile()
	delete(appConfig, "polyrepo")
	appConfig["spaces"] = centralSpaces(map[string]string{"workspace": "packages"})
	appConfig["dependencies"] = map[string]any{
		"app": []any{map[string]any{
			"provider": "remote-sdk",
			"keep":     true,
			"external": true,
		}},
	}
	writePolyrepoJSON(t, appSource, "dispat.json", appConfig)
	appSource.Commit("feat(app): bootstrap app")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	central := polyrepoFile()
	delete(central, "polyrepo")
	central["configs"] = []string{"sources/lib/dispat.json", "sources/app/dispat.json"}
	writePolyrepoJSON(t, control, "dispat.json", central)
	control.Commit("chore: import compute owners")
	controlBefore, err := os.ReadFile(control.Path("dispat.json"))
	require.NoError(t, err)

	res := control.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	controlAfter, err := os.ReadFile(control.Path("dispat.json"))
	require.NoError(t, err)
	assert.Equal(t, string(controlBefore), string(controlAfter), "the control import list is not the consumer's declaration")
	appAfter, err := os.ReadFile(control.Path("sources/app/dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(appAfter), `"provider": "remote-sdk"`)
	assert.Contains(t, string(appAfter), `"external": true`)
	assert.Contains(t, string(appAfter), `"lib"`, "the detected provider is written beside app's owned declarations")
	assert.FileExists(t, control.Path("sources/app/dispat.json.backup"))
}

// TestPolyrepoRejectsReservedControlRepositoryName ensures the synthetic
// control history cannot be shadowed by a .gitmodules logical name. The check
// is case-insensitive and precedes discovery or release work in both central
// and imported composition modes.
func TestPolyrepoRejectsReservedControlRepositoryName(t *testing.T) {
	tests := []struct {
		name       string
		moduleName string
		imported   bool
	}{
		{name: "central exact name", moduleName: "control"},
		{name: "imported mixed case", moduleName: "cOnTrOl", imported: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := harness.New(t)
			source.SeedPackage("packages", "lib")
			if tc.imported {
				owner := polyrepoFile()
				delete(owner, "polyrepo")
				owner["spaces"] = centralSpaces(map[string]string{"libs": "packages"})
				owner["scripts"] = map[string]any{
					"build":   []string{"echo building"},
					"publish": []string{"echo published > published.txt"},
				}
				writePolyrepoJSON(t, source, "dispat.json", owner)
			}
			source.Commit("feat(lib): bootstrap source package")

			control := harness.New(t)
			addPolyrepoSource(t, control, tc.moduleName, "sources/lib", source)
			cfg := polyrepoFile()
			if tc.imported {
				delete(cfg, "polyrepo")
				cfg["configs"] = []string{"sources/lib/dispat.json"}
			} else {
				cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
				cfg["scripts"] = map[string]any{
					"build":   []string{"echo building"},
					"publish": []string{"echo published > published.txt"},
				}
			}
			writePolyrepoJSON(t, control, "dispat.json", cfg)
			control.Commit("chore: configure reserved repository name")
			controlBefore := control.Git("rev-parse", "HEAD")
			sourceBefore := control.Git("-C", "sources/lib", "rev-parse", "HEAD")

			res := control.Release("--log-format", "json")
			assert.NotZero(t, res.Code)
			events := harness.ParseEvents(res.Stderr) // composition fails before configured stdout logging
			assert.True(t, harness.IsCodePresent(events, "E330"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			var diagnostic harness.Event
			for _, event := range events {
				if event.Code() == "E330" {
					diagnostic = event
					break
				}
			}
			assert.Contains(t, diagnostic.Str("error"), `reserved submodule name "`+tc.moduleName+`"`)
			assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))
			assert.Equal(t, sourceBefore, control.Git("-C", "sources/lib", "rev-parse", "HEAD"))
			assert.Empty(t, polyrepoTags(control, "sources/lib"))
			assert.NoFileExists(t, control.Path("sources/lib/packages/lib/published.txt"))
		})
	}
}

// TestPolyrepoOwnershipValidation rejects the configurations that would make
// one package's owner ambiguous or let an imported repository claim paths in
// its sibling or control repository.
func TestPolyrepoOwnershipValidation(t *testing.T) {
	importConfig := func(t *testing.T, source *harness.Repo, pkg string) {
		t.Helper()
		cfg := polyrepoFile()
		delete(cfg, "polyrepo")
		cfg["spaces"] = centralSpaces(map[string]string{"workspace": "packages"})
		writePolyrepoJSON(t, source, "dispat.json", cfg)
		source.Commit("feat(" + pkg + "): bootstrap package")
	}

	t.Run("duplicate package identity across sources", func(t *testing.T) {
		first := harness.New(t)
		first.SeedPackage("packages", "Core")
		importConfig(t, first, "Core")
		second := harness.New(t)
		second.SeedPackage("packages", "core")
		importConfig(t, second, "core")
		control := harness.New(t)
		addPolyrepoSource(t, control, "first-source", "sources/first", first)
		addPolyrepoSource(t, control, "second-source", "sources/second", second)
		cfg := polyrepoFile()
		delete(cfg, "polyrepo")
		cfg["configs"] = []string{"sources/first/dispat.json", "sources/second/dispat.json"}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: assemble duplicate identities")

		res := control.Status()
		assert.NotZero(t, res.Code)
		assert.Contains(t, strings.ToLower(res.Stdout+res.Stderr), "duplicate package")
		assert.Contains(t, strings.ToLower(res.Stdout+res.Stderr), "core")
	})

	t.Run("imported path escapes its owner", func(t *testing.T) {
		first := harness.New(t)
		first.SeedPackage("packages", "inside")
		bad := polyrepoFile()
		delete(bad, "polyrepo")
		bad["packages"] = map[string]any{
			"escape": map[string]any{"path": "../second/packages/escape"},
		}
		writePolyrepoJSON(t, first, "dispat.json", bad)
		first.Commit("chore: configure an escaping path")
		second := harness.New(t)
		second.SeedPackage("packages", "escape")
		second.Commit("feat(escape): sibling package")
		control := harness.New(t)
		addPolyrepoSource(t, control, "first-source", "sources/first", first)
		addPolyrepoSource(t, control, "second-source", "sources/second", second)
		cfg := polyrepoFile()
		delete(cfg, "polyrepo")
		cfg["configs"] = []string{"sources/first/dispat.json"}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: assemble owner escape")

		res := control.Status()
		assert.NotZero(t, res.Code)
		assert.Contains(t, strings.ToLower(res.Stdout+res.Stderr), "escapes")
	})

	t.Run("central control-owned package remains valid", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "legit")
		source.Commit("feat(legit): source package")
		control := harness.New(t)
		addPolyrepoSource(t, control, "legit-source", "sources/legit", source)
		control.SeedPackage("control-packages", "rogue")
		cfg := polyrepoFile()
		cfg["spaces"] = centralSpaces(map[string]string{
			"legit": "sources/legit/packages",
			"rogue": "control-packages",
		})
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("feat(rogue): configure a control-owned package")

		res := control.StatusOK()
		assert.Equal(t, "direct", harness.GraphLine(res.Events, "rogue").Str("reason"))
		assert.Equal(t, "direct", harness.GraphLine(res.Events, "legit").Str("reason"))
	})

	t.Run("control wrapper cannot cross into a source", func(t *testing.T) {
		source := harness.New(t)
		source.WriteFile("main.txt", "source\n")
		source.Commit("feat(api): source implementation")
		control := harness.New(t)
		control.WriteFile("services/api/package.json", `{"name":"api"}`)
		addPolyrepoSource(t, control, "api-source", "services/api/src", source)
		cfg := polyrepoFile()
		cfg["packages"] = map[string]any{
			"api": map[string]any{"path": "services/api", "src": "src"},
		}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: configure a control wrapper around a source")

		polyrepo := control.Status()
		assert.NotZero(t, polyrepo.Code)
		assert.True(t, harness.IsCodePresent(polyrepo.Events, "E331"), "stdout:\n%s\nstderr:\n%s", polyrepo.Stdout, polyrepo.Stderr)

		delete(cfg, "polyrepo")
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		legacy := control.Status()
		assert.Equal(t, 0, legacy.Code, "legacy wrapper behavior remains available: stdout:\n%s\nstderr:\n%s",
			legacy.Stdout, legacy.Stderr)
	})
}

// TestPolyrepoImportedGroupsAreLocalAndCentralGroupsMaySpanSources proves the
// ownership rule for shared version groups in both configuration modes.
func TestPolyrepoImportedGroupsAreLocalAndCentralGroupsMaySpanSources(t *testing.T) {
	groupConfig := func() map[string]any {
		cfg := polyrepoFile()
		delete(cfg, "polyrepo")
		cfg["versionGroups"] = map[string]any{
			"fleet": map[string]any{"versioning": "fixed"},
		}
		cfg["spaces"] = map[string]any{
			"workspace": map[string]any{
				"path":         []string{"packages"},
				"versionGroup": "fleet",
			},
		}
		return cfg
	}

	t.Run("imported group names are repository local", func(t *testing.T) {
		first := harness.New(t)
		first.SeedPackage("packages", "a1")
		first.SeedPackage("packages", "a2")
		writePolyrepoJSON(t, first, "dispat.json", groupConfig())
		first.Commit("feat(a1): change only the first imported group")
		second := harness.New(t)
		second.SeedPackage("packages", "b1")
		second.SeedPackage("packages", "b2")
		writePolyrepoJSON(t, second, "dispat.json", groupConfig())
		second.Commit("chore: seed the second imported group")

		control := harness.New(t)
		addPolyrepoSource(t, control, "first-source", "sources/first", first)
		addPolyrepoSource(t, control, "second-source", "sources/second", second)
		cfg := polyrepoFile()
		delete(cfg, "polyrepo")
		cfg["configs"] = []string{"sources/first/dispat.json", "sources/second/dispat.json"}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: assemble imported groups")

		status := control.StatusOK()
		assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(status.Events, "a1").Str("version"))
		assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(status.Events, "a2").Str("version"))
		assert.Equal(t, "unchanged", harness.GraphLine(status.Events, "b1").Str("message"))
		assert.Equal(t, "unchanged", harness.GraphLine(status.Events, "b2").Str("message"))
		byGroup := control.StatusOK("--group", "fleet")
		assert.Equal(t, "● changed", harness.GraphLine(byGroup.Events, "a1").Str("message"))
		assert.Equal(t, "unchanged", harness.GraphLine(byGroup.Events, "b1").Str("message"),
			"the unqualified imported group selector is the union of both local groups")
		bySpace := control.StatusOK("--space", "workspace")
		assert.Equal(t, "● changed", harness.GraphLine(bySpace.Events, "a1").Str("message"))
		assert.Equal(t, "unchanged", harness.GraphLine(bySpace.Events, "b1").Str("message"),
			"the unqualified imported space selector is the union of both local spaces")
	})

	t.Run("central explicit group spans sources", func(t *testing.T) {
		first := harness.New(t)
		first.SeedPackage("packages", "a")
		first.Commit("feat(a): change the first central member")
		second := harness.New(t)
		second.SeedPackage("packages", "b")
		second.Commit("chore: seed the second central member")
		control := harness.New(t)
		addPolyrepoSource(t, control, "first-source", "sources/first", first)
		addPolyrepoSource(t, control, "second-source", "sources/second", second)
		cfg := polyrepoFile()
		cfg["versionGroups"] = map[string]any{
			"fleet": map[string]any{"versioning": "fixed"},
		}
		cfg["spaces"] = map[string]any{
			"first": map[string]any{
				"path":         []string{"sources/first/packages"},
				"versionGroup": "fleet",
			},
			"second": map[string]any{
				"path":         []string{"sources/second/packages"},
				"versionGroup": "fleet",
			},
		}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: assemble central group")

		status := control.StatusOK()
		assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(status.Events, "a").Str("version"))
		assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(status.Events, "b").Str("version"))
		selected := control.StatusOK("--group", "fleet")
		assert.Equal(t, "● changed", harness.GraphLine(selected.Events, "a").Str("message"))
		assert.Equal(t, "● changed", harness.GraphLine(selected.Events, "b").Str("message"))
	})
}

// TestPolyrepoFixedRideGuardsSourceHistoryWithoutDependency gives source B no
// dependency on source A; their only relationship is a central fixed group.
// Once A's nested release has been durably pushed, B's beforePublish hook
// advances A again. B must guard the history that caused its fixed ride and
// refuse publication from the now-stale plan.
func TestPolyrepoFixedRideGuardsSourceHistoryWithoutDependency(t *testing.T) {
	aSource := harness.New(t)
	aSource.SeedPackage("packages", "a")
	aSource.Commit("feat(a): initial package A")
	aSource.Git("tag", "-a", "a@1.0.0", "-m", "initial release")
	aSource.WriteFile("packages/a/fix.txt", "one\n")
	aSource.Commit("fix(a): source-A-only patch")
	bSource := harness.New(t)
	bSource.SeedPackage("packages", "b")
	bSource.Commit("feat(b): initial package B")
	bSource.Git("tag", "-a", "b@1.0.0", "-m", "initial release")

	control := harness.New(t)
	addPolyrepoSource(t, control, "source-a", "sources/a", aSource)
	addPolyrepoSource(t, control, "source-b", "sources/b", bSource)
	aRemote := filepath.Join(t.TempDir(), "source-a.git")
	control.Git("init", "-q", "--bare", aRemote)
	control.Git("-C", "sources/a", "remote", "set-url", "origin", aRemote)
	control.Git("-C", "sources/a", "push", "-q", "origin", "HEAD:refs/heads/main")

	commitA := control.DispatCommand("commit", "--tag")
	waitForA := `attempts=0; while [ ! -f ../../../../a-recorded ] && [ "$attempts" -lt 300 ]; do attempts=$((attempts + 1)); sleep 0.01; done; test -f ../../../../a-recorded`
	cfg := polyrepoFile()
	cfg["versionGroups"] = map[string]any{
		"fleet": map[string]any{"versioning": "fixed"},
	}
	cfg["spaces"] = map[string]any{
		"first": map[string]any{
			"path":         []string{"sources/a/packages"},
			"versionGroup": "fleet",
		},
		"second": map[string]any{
			"path":         []string{"sources/b/packages"},
			"versionGroup": "fleet",
		},
	}
	cfg["repositoryOverrides"] = map[string]any{
		"source-a": map[string]any{"commit": map[string]any{
			"enabled": true,
			"push":    true,
			"remote":  "origin",
			"branch":  "main",
		}},
	}
	cfg["scripts"] = map[string]any{
		"build": []string{"echo building"},
		"mark-a-recorded": []string{
			`case "$PWD" in */sources/a) : > ../../a-recorded;; esac`,
		},
		"guard-b": []string{
			`if [ "$DISPAT_PACKAGE" = b ]; then ` + waitForA + `; ` +
				`git -C ../../../a commit --allow-empty -q -m 'chore: unplanned source A advance'; ` +
				`: > ../../../../a-mutated-by-b; fi`,
		},
		"publish": []string{
			`case "$DISPAT_PACKAGE" in ` +
				`a) printf 'a\n' > nested-release.txt; ` + commitA + `; : > ../../../../a-published;; ` +
				`b) : > ../../../../b-published;; esac`,
		},
	}
	cfg["flow"] = map[string]any{
		"build":         []string{"build"},
		"beforePublish": []string{"guard-b"},
		"publish":       []string{"publish"},
	}
	cfg["run"] = map[string]any{"afterPush": []string{"mark-a-recorded"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure cross-source fixed ride")

	status := control.StatusOK()
	assert.Equal(t, "1.0.0 -> 1.0.1", harness.GraphLine(status.Events, "a").Str("version"))
	assert.Equal(t, "1.0.0 -> 1.0.1", harness.GraphLine(status.Events, "b").Str("version"))
	assert.Equal(t, "fixed group versioning", harness.GraphLine(status.Events, "b").Str("reason"))

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	assert.True(t, harness.IsCodePresent(res.Events, "E330"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.FileExists(t, control.Path("a-recorded"), "source A completed its ordinary source record first")
	assert.FileExists(t, control.Path("a-mutated-by-b"), "B's hook performed the unplanned mutation")
	assert.FileExists(t, control.Path("a-published"))
	assert.NoFileExists(t, control.Path("b-published"), "B must fail its cross-group guard before publication")
	assert.Contains(t, polyrepoTags(control, "sources/a"), "a@1.0.1")
	assert.NotContains(t, polyrepoTags(control, "sources/b"), "b@1.0.1")
}

// TestPolyrepoIdenticalObjectIDsAndTagNamesStayIsolated adds the same origin
// twice, yielding identical commit OIDs and identical v1.0.0 tag names. A
// later commit in only one checkout must not contaminate the other package's
// cached history or baseline.
func TestPolyrepoIdenticalObjectIDsAndTagNamesStayIsolated(t *testing.T) {
	origin := harness.New(t)
	origin.SeedPackage("packages", "lib")
	origin.SeedPackage("packages", "tool")
	origin.Commit("feat(lib,tool): shared seed object")
	origin.Git("tag", "-a", "v1.0.0", "-m", "shared spelling")

	control := harness.New(t)
	addPolyrepoSource(t, control, "first-source", "sources/first", origin)
	addPolyrepoSource(t, control, "second-source", "sources/second", origin)
	cfg := polyrepoFile()
	cfg["packages"] = map[string]any{
		"lib": map[string]any{
			"path":      "sources/first/packages/lib",
			"tagFormat": "v{version}",
		},
		"tool": map[string]any{
			"path":      "sources/second/packages/tool",
			"tagFormat": "v{version}",
		},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble identical histories")

	baseline := control.StatusOK()
	assert.Equal(t, "unchanged", harness.GraphLine(baseline.Events, "lib").Str("message"))
	assert.Equal(t, "1.0.0", harness.GraphLine(baseline.Events, "lib").Str("version"))
	assert.Equal(t, "unchanged", harness.GraphLine(baseline.Events, "tool").Str("message"))
	assert.Equal(t, "1.0.0", harness.GraphLine(baseline.Events, "tool").Str("version"))

	control.WriteFile("sources/first/packages/lib/feature.txt", "only first\n")
	commitPolyrepoSource(t, control, "sources/first", "feat(lib): first repository moves")
	checkpointPolyrepoSource(t, control, "sources/first")
	moved := control.StatusOK()
	assert.Equal(t, "1.0.0 -> 1.1.0", harness.GraphLine(moved.Events, "lib").Str("version"))
	assert.Equal(t, "unchanged", harness.GraphLine(moved.Events, "tool").Str("message"))
	assert.Equal(t, "1.0.0", harness.GraphLine(moved.Events, "tool").Str("version"))
}

// TestPolyrepoSameTagSpellingKeepsCheckpointOwnersSeparate gives two
// consumers the valid repository-local tag v1.0.0 at different control
// checkpoints. Their provider snapshots differ, so checkpoint lookup must be
// qualified by consumer ownership rather than collapsing the raw tag text.
//
// The provider *releases* its breaking change before the second consumer's
// checkpoint, which is what makes that consumer square with it. A checkpoint
// observing an unreleased provider commit would leave the consumer owed the
// bump all the same (§13.4a): what discharges a propagated contribution is a
// release of the provider carrying it, not a snapshot of its working history.
func TestPolyrepoSameTagSpellingKeepsCheckpointOwnersSeparate(t *testing.T) {
	provider := harness.New(t)
	provider.SeedPackage("packages", "p")
	provider.Commit("feat(p): initial provider")
	provider.Git("tag", "-a", "p@1.0.0", "-m", "initial provider")
	first := harness.New(t)
	first.SeedPackage("packages", "a")
	first.Commit("chore(a): unreleased base")
	second := harness.New(t)
	second.SeedPackage("packages", "b")
	second.Commit("chore(b): unreleased base")

	control := harness.New(t)
	addPolyrepoSource(t, control, "provider-source", "sources/provider", provider)
	addPolyrepoSource(t, control, "first-source", "sources/first", first)
	addPolyrepoSource(t, control, "second-source", "sources/second", second)
	cfg := polyrepoFile()
	cfg["packages"] = map[string]any{
		"p": map[string]any{"path": "sources/provider/packages/p"},
		"a": map[string]any{"path": "sources/first/packages/a", "tagFormat": "v{version}"},
		"b": map[string]any{"path": "sources/second/packages/b", "tagFormat": "v{version}"},
	}
	cfg["dependencies"] = map[string]any{
		"a": []any{"p"},
		"b": []any{"p"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble same-tag consumers")

	control.WriteFile("sources/first/packages/a/release.txt", "first\n")
	commitPolyrepoSource(t, control, "sources/first", "feat(a): first consumer release")
	control.Git("-C", "sources/first", "tag", "-a", "v1.0.0", "-m", "first consumer release")
	control.Git("add", "sources/first")
	control.Git("commit", "-q", "-m", "chore(release): v1.0.0")

	control.WriteFile("sources/provider/packages/p/break.txt", "breaking\n")
	commitPolyrepoSource(t, control, "sources/provider", "feat(p)^major!: breaking provider release")
	control.Git("-C", "sources/provider", "tag", "-a", "p@2.0.0", "-m", "breaking provider release")
	checkpointPolyrepoSource(t, control, "sources/provider")

	control.WriteFile("sources/second/packages/b/release.txt", "second\n")
	commitPolyrepoSource(t, control, "sources/second", "feat(b): second consumer release")
	control.Git("-C", "sources/second", "tag", "-a", "v1.0.0", "-m", "second consumer release")
	control.Git("add", "sources/second")
	control.Git("commit", "-q", "-m", "chore(release): v1.0.0")

	control.WriteFile("sources/provider/packages/p/fix.txt", "fix\n")
	commitPolyrepoSource(t, control, "sources/provider", "fix(p)^patch: later provider fix")
	checkpointPolyrepoSource(t, control, "sources/provider")

	status := control.StatusOK()
	assert.Equal(t, "1.0.0 -> 2.0.0", harness.GraphLine(status.Events, "a").Str("version"),
		"the first consumer checkpoint predates the provider breaking change")
	assert.Equal(t, "1.0.0 -> 1.0.1", harness.GraphLine(status.Events, "b").Str("version"),
		"the second consumer checkpoint already includes the breaking provider change")
	assert.Equal(t, "propagated from p", harness.GraphLine(status.Events, "a").Str("reason"))
	assert.Equal(t, "propagated from p", harness.GraphLine(status.Events, "b").Str("reason"))
}

// TestPolyrepoIncomparableSourceDirectivesNeedCausalControlResolution gives
// one consumer competing propagated channels from two source DAGs. Neither
// source is newer, so E334 is required. A later control directive can resolve
// only the source revisions in its gitlink snapshot; still-later source work
// makes the old resolution insufficient again.
func TestPolyrepoIncomparableSourceDirectivesNeedCausalControlResolution(t *testing.T) {
	newSource := func(t *testing.T, pkg string) *harness.Repo {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", pkg)
		source.Commit("feat(" + pkg + "): initial package")
		source.Git("tag", "-a", pkg+"@1.0.0", "-m", "initial release")
		return source
	}
	aSource := newSource(t, "a")
	bSource := newSource(t, "b")
	appSource := newSource(t, "app")
	control := harness.New(t)
	addPolyrepoSource(t, control, "a-source", "sources/a", aSource)
	addPolyrepoSource(t, control, "b-source", "sources/b", bSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"a":   "sources/a/packages",
		"b":   "sources/b/packages",
		"app": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"a", "b"}}
	cfg["repositoryBaselines"] = []any{
		map[string]any{"consumer": "app", "releaseTag": "app@1.0.0", "repository": "a-source", "revision": "a@1.0.0"},
		map[string]any{"consumer": "app", "releaseTag": "app@1.0.0", "repository": "b-source", "revision": "b@1.0.0"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: establish source baselines")
	controlBaseline := control.Git("rev-parse", "HEAD")

	control.WriteFile("sources/a/packages/a/channel.txt", "beta\n")
	commitPolyrepoSource(t, control, "sources/a", "release(a)%beta%%beta++1: propose beta")
	control.WriteFile("sources/b/packages/b/channel.txt", "rc\n")
	commitPolyrepoSource(t, control, "sources/b", "release(b)%rc%%rc++1: propose rc")
	cfg["repositoryBaselines"] = append(cfg["repositoryBaselines"].([]any),
		map[string]any{
			"consumer": "app", "releaseTag": "app@1.0.0", "repository": "control", "revision": controlBaseline,
		},
		map[string]any{
			"consumer": "a", "releaseTag": "a@1.0.0", "repository": "control", "revision": controlBaseline,
		},
	)
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Git("add", "dispat.json", "sources/a", "sources/b")
	control.Git("commit", "-q", "-m", "chore: observe incomparable channel proposals")

	conflict := control.Status()
	assert.NotZero(t, conflict.Code)
	assert.True(t, harness.IsCodePresent(conflict.Events, "E334"), "stdout:\n%s\nstderr:\n%s", conflict.Stdout, conflict.Stderr)

	control.CommitEmpty("release(a)%%beta++1: resolve observed proposals")
	resolved := control.StatusOK()
	assert.Contains(t, harness.GraphLine(resolved.Events, "app").Str("version"), "beta",
		"a propagated control proposal resolves same-axis candidates in its causal snapshot")

	control.WriteFile("sources/b/packages/b/channel.txt", "canary\n")
	commitPolyrepoSource(t, control, "sources/b", "release(b)%canary%%canary++1: later proposal")
	checkpointPolyrepoSource(t, control, "sources/b")
	later := control.Status()
	assert.NotZero(t, later.Code)
	assert.True(t, harness.IsCodePresent(later.Events, "E334"),
		"the earlier control proposal cannot resolve source work outside its gitlink snapshot: stdout:\n%s\nstderr:\n%s",
		later.Stdout, later.Stderr)
}

// TestPolyrepoOwnerChannelBeatsIncomparablePropagation keeps all three
// histories real: two providers propose different propagated channels while
// the consumer's own source chooses its channel directly. The direct choice
// is authoritative even though neither provider history observes the other.
func TestPolyrepoOwnerChannelBeatsIncomparablePropagation(t *testing.T) {
	newSource := func(t *testing.T, pkg string) *harness.Repo {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", pkg)
		source.Commit("feat(" + pkg + "): initial package")
		source.Git("tag", "-a", pkg+"@1.0.0", "-m", "initial release")
		return source
	}
	aSource := newSource(t, "a")
	bSource := newSource(t, "b")
	appSource := newSource(t, "app")
	control := harness.New(t)
	addPolyrepoSource(t, control, "a-source", "sources/a", aSource)
	addPolyrepoSource(t, control, "b-source", "sources/b", bSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"a":   "sources/a/packages",
		"b":   "sources/b/packages",
		"app": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"a", "b"}}
	cfg["repositoryBaselines"] = []any{
		map[string]any{"consumer": "app", "releaseTag": "app@1.0.0", "repository": "a-source", "revision": "a@1.0.0"},
		map[string]any{"consumer": "app", "releaseTag": "app@1.0.0", "repository": "b-source", "revision": "b@1.0.0"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: establish source baselines")

	control.WriteFile("sources/a/packages/a/channel.txt", "beta\n")
	commitPolyrepoSource(t, control, "sources/a", "release(a)%beta%%beta++1: propose beta")
	control.WriteFile("sources/b/packages/b/channel.txt", "rc\n")
	commitPolyrepoSource(t, control, "sources/b", "release(b)%rc%%rc++1: propose rc")
	control.WriteFile("sources/app/packages/app/channel.txt", "canary\n")
	commitPolyrepoSource(t, control, "sources/app", "release(app)%canary: choose the app channel")
	control.Git("add", "sources/a", "sources/b", "sources/app")
	control.Git("commit", "-q", "-m", "chore: observe channel proposals")

	status := control.StatusOK()
	app := harness.GraphLine(status.Events, "app")
	assert.Equal(t, "stable -> canary", app.Str("channel"))
	assert.Contains(t, app.Str("version"), "-canary.")
}

// TestPolyrepoPrereleaseAndStableWindowsStayRepositoryLocal advances a beta
// train beside an ordinary stable line. Each source reads only its own tags
// and fresh commits, so counters and channels cannot bleed between histories.
func TestPolyrepoPrereleaseAndStableWindowsStayRepositoryLocal(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.Commit("feat(lib): initial library")
	libSource.Git("tag", "-a", "lib@1.0.0", "-m", "initial library")
	libSource.WriteFile("packages/lib/beta.txt", "beta zero\n")
	libSource.Commit("feat(lib)%beta: start beta train")

	toolSource := harness.New(t)
	toolSource.SeedPackage("packages", "tool")
	toolSource.Commit("feat(tool): initial tool")
	toolSource.Git("tag", "-a", "tool@1.0.0", "-m", "initial tool")
	toolSource.WriteFile("packages/tool/fix.txt", "stable patch\n")
	toolSource.Commit("fix(tool): stable patch")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "tool-source", "sources/tool", toolSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs":  "sources/lib/packages",
		"tools": "sources/tool/packages",
	})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble beta and stable histories")

	control.ReleaseOK()
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@1.1.0-beta.0")
	assert.Contains(t, polyrepoTags(control, "sources/tool"), "tool@1.0.1")

	control.WriteFile("sources/lib/packages/lib/beta.txt", "beta one\n")
	commitPolyrepoSource(t, control, "sources/lib", "fix(lib)%beta++1: continue beta train")
	checkpointPolyrepoSource(t, control, "sources/lib")
	control.WriteFile("sources/tool/packages/tool/feature.txt", "stable feature\n")
	commitPolyrepoSource(t, control, "sources/tool", "feat(tool): stable feature")
	checkpointPolyrepoSource(t, control, "sources/tool")
	second := control.StatusOK()
	assert.Equal(t, "1.1.0-beta.0 -> 1.1.0-beta.1", harness.GraphLine(second.Events, "lib").Str("version"))
	assert.Equal(t, "1.0.1 -> 1.1.0", harness.GraphLine(second.Events, "tool").Str("version"))
	control.ReleaseOK()

	control.WriteFile("sources/lib/packages/lib/beta.txt", "stable\n")
	commitPolyrepoSource(t, control, "sources/lib", "release(lib)%beta>stable: graduate library")
	checkpointPolyrepoSource(t, control, "sources/lib")
	control.ReleaseOK()
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@1.1.0")
	assert.Equal(t, 4, len(polyrepoTags(control, "sources/lib")))
	assert.Equal(t, 3, len(polyrepoTags(control, "sources/tool")),
		"the unchanged stable source gains no tag when the other source graduates")
	assert.Empty(t, control.TagList())
}

// TestPolyrepoStableAndPrereleaseBaselineTuplesStaySeparate gives one
// consumer both a stable and a prerelease release tag with deliberately
// different provider positions. Its active beta boundary must use the beta
// tuple; merging it with the stable tuple would hide the required catch-up.
func TestPolyrepoStableAndPrereleaseBaselineTuplesStaySeparate(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.Commit("feat(lib): initial library")
	libSource.Git("tag", "-a", "lib@1.0.0", "-m", "initial library")
	libSource.WriteFile("packages/lib/minor.txt", "minor\n")
	libSource.Commit("feat(lib): minor release")
	libSource.Git("tag", "-a", "lib@1.1.0", "-m", "minor release")
	libSource.WriteFile("packages/lib/fix.txt", "fix\n")
	libSource.Commit("fix(lib)^: patch release")
	libSource.Git("tag", "-a", "lib@1.1.1", "-m", "patch release")

	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appSource.Commit("feat(app): stable application")
	appSource.Git("tag", "-a", "app@1.0.0", "-m", "stable application")
	appSource.WriteFile("packages/app/beta.txt", "beta\n")
	appSource.Commit("feat(app)%beta: beta application")
	appSource.Git("tag", "-a", "app@1.1.0-beta.0", "-m", "beta application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages",
		"apps": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"lib"}}
	cfg["repositoryBaselines"] = []any{
		map[string]any{
			"consumer":   "app",
			"releaseTag": "app@1.0.0",
			"repository": "lib-source",
			"revision":   "lib@1.1.1",
		},
		map[string]any{
			"consumer":   "app",
			"releaseTag": "app@1.1.0-beta.0",
			"repository": "lib-source",
			"revision":   "lib@1.1.0",
		},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure stable and beta boundaries")

	status := control.StatusOK()
	assert.Equal(t, "unchanged", harness.GraphLine(status.Events, "lib").Str("message"))
	assert.Equal(t, "catch-up from lib", harness.GraphLine(status.Events, "app").Str("reason"),
		"the current beta tag uses its own older provider tuple")
	assert.True(t, harness.IsCodePresentForPackage(status.Events, "W193", "app"))
}

// TestPolyrepoInterleavedRepositoryGraphReleases releases A/lib -> B/service
// -> A/tool. Scheduling by repository would deadlock this shape; scheduling
// by the package DAG completes and writes each tag to its owning repository.
func TestPolyrepoInterleavedRepositoryGraphReleases(t *testing.T) {
	aSource := harness.New(t)
	aSource.SeedPackage("packages", "lib")
	aSource.SeedPackage("packages", "tool")
	aSource.Commit("feat(lib,tool): bootstrap A packages")
	bSource := harness.New(t)
	bSource.SeedPackage("packages", "service")
	bSource.Commit("feat(service): bootstrap B service")

	control := harness.New(t)
	addPolyrepoSource(t, control, "source-a", "sources/a", aSource)
	addPolyrepoSource(t, control, "source-b", "sources/b", bSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"a": "sources/a/packages",
		"b": "sources/b/packages",
	})
	cfg["dependencies"] = map[string]any{
		"service": []any{"lib"},
		"tool":    []any{"service"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble interleaved graph")

	res := control.Release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.ElementsMatch(t, []string{"lib@0.1.0", "tool@0.1.0"}, polyrepoTags(control, "sources/a"))
	assert.Equal(t, []string{"service@0.1.0"}, polyrepoTags(control, "sources/b"))
	assert.Empty(t, control.TagList())
}

// TestPolyrepoRepositoryOverrideCommitsAndPushesDetachedSource uses a complete
// source commit-policy override. The source is detached, so commit.branch is
// the only safe push target; the root commit policy separately creates the
// local control checkpoint that records the source release commit.
func TestPolyrepoRepositoryOverrideCommitsAndPushesDetachedSource(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	bare := filepath.Join(t.TempDir(), "source.git")
	control.Git("init", "-q", "--bare", bare)
	control.Git("-C", bare, "symbolic-ref", "HEAD", "refs/heads/release")
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", bare)
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/release")
	control.Git("-C", "sources/lib", "checkout", "-q", "--detach")

	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["changelog"] = map[string]any{"enabled": true}
	cfg["commit"] = map[string]any{
		"enabled":       true,
		"messageFormat": "chore(control): checkpoint {tags}",
	}
	cfg["repositoryOverrides"] = map[string]any{
		"lib-source": map[string]any{
			"commit": map[string]any{
				"enabled":       true,
				"messageFormat": "chore(source): record {tags}",
				"push":          true,
				"remote":        "origin",
				"branch":        "release",
				"name":          "source release bot",
				"email":         "source-release@dispat.test",
			},
		},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure detached source release")
	controlBefore := control.Git("rev-parse", "HEAD")
	sourceBefore := control.Git("-C", "sources/lib", "rev-parse", "HEAD")

	res := control.Release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	controlAfter := control.Git("rev-parse", "HEAD")
	assert.NotEqual(t, sourceBefore, sourceAfter, "the source writes its own release record commit")
	assert.NotEqual(t, controlBefore, controlAfter, "the control repository checkpoints the advanced gitlink")
	assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
	assert.Equal(t, sourceAfter, control.Git("-C", bare, "rev-parse", "refs/heads/release"))
	assert.Equal(t, sourceAfter, control.Git("-C", bare, "rev-parse", "lib@0.1.0^{commit}"))
	assert.FileExists(t, control.Path("sources/lib/packages/lib/CHANGELOG.md"),
		"the source-owned changelog is written inside the source repository")
	assert.NoFileExists(t, control.Path("packages/lib/CHANGELOG.md"))
	assert.Equal(t, "source release bot <source-release@dispat.test>",
		control.Git("-C", "sources/lib", "log", "-1", "--format=%an <%ae>"))
	assert.Contains(t, control.Git("-C", "sources/lib", "log", "-1", "--format=%s"), "chore(source): record")
	assert.Contains(t, control.Git("log", "-1", "--format=%s"), "chore(control): checkpoint")
}

// TestPolyrepoDetachedPushRequiresBranch rejects an ambiguous branch update
// before publication. The source release commit, tag and control checkpoint
// all remain absent when a detached source is configured to push without an
// explicit commit.branch.
func TestPolyrepoDetachedPushRequiresBranch(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	bare := filepath.Join(t.TempDir(), "source.git")
	control.Git("init", "-q", "--bare", bare)
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", bare)
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/release")
	control.Git("-C", "sources/lib", "checkout", "-q", "--detach")

	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["changelog"] = map[string]any{"enabled": true}
	cfg["commit"] = map[string]any{"enabled": true}
	cfg["scripts"] = map[string]any{
		"build": []string{"echo building"}, "publish": []string{"echo published > ../../published.txt"},
	}
	cfg["repositoryOverrides"] = map[string]any{
		"lib-source": map[string]any{
			"commit": map[string]any{
				"enabled": true,
				"push":    true,
				"remote":  "origin",
			},
		},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure ambiguous detached push")
	controlBefore := control.Git("rev-parse", "HEAD")
	sourceBefore := control.Git("-C", "sources/lib", "rev-parse", "HEAD")

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	assert.True(t, harness.IsCodePresent(res.Events, "E337"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, sourceBefore, control.Git("-C", "sources/lib", "rev-parse", "HEAD"))
	assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.NoFileExists(t, control.Path("sources/lib/published.txt"),
		"detached branch validation must finish before publication")
}

// TestPolyrepoControlBehindItsRemoteRefusesBeforePlanning: a fleet plans from
// every participant's tags, so a control checkout that has fallen behind its
// remote is refused before anything is planned, exactly as a single history's
// checkout is. The refusal names the repository and the branch, no planning
// event precedes it, no package script runs and nothing is tagged.
func TestPolyrepoControlBehindItsRemoteRefusesBeforePlanning(t *testing.T) {
	fleet := finalPolyrepo(t)
	control := fleet.control
	marker := control.Path("built.txt")
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["commit"] = map[string]any{"enabled": true, "push": true}
	cfg["scripts"] = map[string]any{
		"build": []string{"echo built >> " + harness.ShQuote(marker)}, "publish": []string{"echo publishing"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: push the control repository's records")
	control.AddBareRemote()
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	control.Git("commit", "--allow-empty", "-q", "-m", "chore: work another clone pushed first")
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	control.Git("reset", "-q", "--hard", "HEAD~1")

	res := control.Release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "repository control is behind remote branch "+harness.DefaultBranch)
	assert.Equal(t, -1, firstIndex(res.Events, planningStarted),
		"the stale checkout is refused before anything is planned\nstdout:\n%s", res.Stdout)
	assert.NoFileExists(t, marker, "no package script ran")
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
}

// TestPolyrepoDetachedSourcePinnedBehindItsBranchStillReleases: the check a
// fleet makes before it plans compares the branch each participant has
// checked out, and a detached source has none. A source pinned at a revision
// its remote's branch has since moved past is therefore not refused: with
// nothing to commit it pushes an immutable tag at the pinned revision, and the
// remote's branch stays where it is. Its remote is proved reachable once.
func TestPolyrepoDetachedSourcePinnedBehindItsBranchStillReleases(t *testing.T) {
	fleet := finalPolyrepo(t)
	control := fleet.control
	bare := filepath.Join(t.TempDir(), "source.git")
	control.Git("init", "-q", "--bare", bare)
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", bare)
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	pinned := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	control.Git("-C", "sources/lib", "commit", "--allow-empty", "-q", "-m", "chore: later work on the branch")
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	control.Git("-C", "sources/lib", "checkout", "-q", "--detach", pinned)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["repositoryOverrides"] = map[string]any{
		"lib-source": map[string]any{"commit": map[string]any{"enabled": true, "push": true, "remote": "origin"}},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: push the pinned source's records")
	branchTip := control.Git("-C", bare, "rev-parse", "refs/heads/"+harness.DefaultBranch)
	proofs := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C */sources/lib *ls-remote --heads origin*", Nth: 1 << 20})

	res := control.CommandEnv(proofs.Env())

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 1, proofs.Matches(), "the source remote is proved once, before the plan")
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "core@0.1.0")
	assert.Equal(t, pinned, control.Git("-C", bare, "rev-parse", "core@0.1.0^{commit}"),
		"the tag names the pinned revision")
	assert.Equal(t, branchTip, control.Git("-C", bare, "rev-parse", "refs/heads/"+harness.DefaultBranch),
		"and the remote's branch is where it was")
}

// TestPolyrepoCheckpointFailurePreservesSourceAndBlocksConsumer makes the
// control commit fail after the provider's source commit and tag succeeded.
// The result reports E335, does not run the dependent leg, and leaves enough
// durable evidence for an operator to repair the ordinary gitlink explicitly.
// Retrying after that repair keeps the provider tag and finishes the consumer.
func TestPolyrepoCheckpointFailurePreservesSourceAndBlocksConsumer(t *testing.T) {
	sink := newWebhookSink(t)
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.Commit("feat(lib): initial library")
	libSource.Git("tag", "-a", "lib@1.0.0", "-m", "initial library")
	libSource.WriteFile("packages/lib/feature.txt", "provider feature\n")
	libSource.Commit("feat(lib)^: provider feature")

	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appSource.Commit("feat(app): initial application")
	appSource.Git("tag", "-a", "app@1.0.0", "-m", "initial application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages",
		"apps": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"lib"}}
	cfg["changelog"] = map[string]any{"enabled": true}
	cfg["commit"] = map[string]any{"enabled": true}
	cfg["webhooks"] = []any{map[string]any{
		"url":    sink.srv.URL,
		"events": []string{"package.published", "release.finished"},
	}}
	cfg["repositoryBaselines"] = []any{map[string]any{
		"consumer":   "app",
		"releaseTag": "app@1.0.0",
		"repository": "lib-source",
		"revision":   "lib@1.0.0",
	}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble release graph")
	pinnedBefore := control.Git("rev-parse", "HEAD:sources/lib")

	hook := control.Path(".git", "hooks", "pre-commit")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 37\n"), 0o755))
	failed := control.Release()
	assert.Equal(t, 1, failed.Code, "the checkpoint failure fails the run")
	assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Contains(t, strings.ToLower(failed.Stdout+failed.Stderr), "checkpoint")
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@1.1.0",
		"the provider's successful publication remains truthful")
	assert.NotContains(t, polyrepoTags(control, "sources/app"), "app@1.0.1",
		"the unresolved checkpoint blocks dependent publication")
	published := sink.find(t, "package.published")
	assert.Equal(t, "lib", published["package"])
	assert.Equal(t, "published", published["status"], "the source package remains truthfully published")
	finished := sink.find(t, "release.finished")
	assert.Equal(t, "failed", finished["status"],
		"a critical checkpoint failure makes the run fail even after source publication")
	libReleased := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	assert.NotEqual(t, pinnedBefore, libReleased)
	assert.Equal(t, pinnedBefore, control.Git("rev-parse", "HEAD:sources/lib"),
		"a failed checkpoint does not claim the new source revision")

	require.NoError(t, os.Remove(hook))
	control.Git("add", "sources/lib")
	control.Git("commit", "-q", "-m", "chore: repair lib source checkpoint")
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, libReleased, control.Git("-C", "sources/lib", "rev-parse", "lib@1.1.0^{commit}"),
		"retry preserves the already published provider tag")
	assert.Equal(t, 2, len(polyrepoTags(control, "sources/lib")), "the provider is not published twice")
	assert.Contains(t, polyrepoTags(control, "sources/app"), "app@1.0.1")
	converged := control.StatusOK()
	assert.Equal(t, "unchanged", harness.GraphLine(converged.Events, "lib").Str("message"))
	assert.Equal(t, "unchanged", harness.GraphLine(converged.Events, "app").Str("message"))
}

// TestPolyrepoSourceRecordFailureBlocksConsumerAndRetries fails the source's
// own release commit before its tag exists. The control pointer stays put,
// dependent work is blocked, and removing the failure lets one clean retry
// record both repositories without carrying partial release state forward.
func TestPolyrepoSourceRecordFailureBlocksConsumerAndRetries(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.Commit("feat(lib): initial library")
	libSource.Git("tag", "-a", "lib@1.0.0", "-m", "initial library")
	libSource.WriteFile("packages/lib/feature.txt", "provider feature\n")
	libSource.Commit("feat(lib)^: provider feature")
	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appSource.Commit("feat(app): initial application")
	appSource.Git("tag", "-a", "app@1.0.0", "-m", "initial application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages",
		"apps": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"lib"}}
	cfg["changelog"] = map[string]any{"enabled": true}
	cfg["commit"] = map[string]any{"enabled": true}
	cfg["repositoryBaselines"] = []any{map[string]any{
		"consumer":   "app",
		"releaseTag": "app@1.0.0",
		"repository": "lib-source",
		"revision":   "lib@1.0.0",
	}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble release graph")
	controlBefore := control.Git("rev-parse", "HEAD")

	sourceGitDir := control.Git("-C", "sources/lib", "rev-parse", "--absolute-git-dir")
	sourceHook := filepath.Join(sourceGitDir, "hooks", "pre-commit")
	require.NoError(t, os.WriteFile(sourceHook, []byte("#!/bin/sh\nexit 38\n"), 0o755))
	failed := control.Release()
	assert.Equal(t, 1, failed.Code)
	assert.Contains(t, strings.ToLower(failed.Stdout+failed.Stderr), "source release commit")
	assert.NotContains(t, polyrepoTags(control, "sources/lib"), "lib@1.1.0")
	assert.NotContains(t, polyrepoTags(control, "sources/app"), "app@1.0.1")
	assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"),
		"a failed source record produces no control checkpoint")

	require.NoError(t, os.Remove(sourceHook))
	// A failed native commit intentionally leaves generated source-owned state
	// for operator inspection. Repair that disposable state explicitly before
	// retrying; the ordinary dirty-path guard must remain strict.
	control.Git("-C", "sources/lib", "reset", "--hard", "HEAD")
	_ = os.Remove(control.Path("sources/lib/packages/lib/CHANGELOG.md"))
	assert.NoFileExists(t, control.Path("sources/lib/packages/lib/CHANGELOG.md"))
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@1.1.0")
	assert.Contains(t, polyrepoTags(control, "sources/app"), "app@1.0.1")
}

// TestPolyrepoPartialReleasePreservesIndependentSuccessOnRetry lets one
// source publish while an unrelated source fails its build. The failed
// source's consumer is blocked, independent success remains tagged, and the
// retry resumes from that durable tag instead of publishing it twice.
func TestPolyrepoPartialReleasePreservesIndependentSuccessOnRetry(t *testing.T) {
	newSource := func(t *testing.T, pkg string) *harness.Repo {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", pkg)
		source.Commit("feat(" + pkg + "): bootstrap package")
		return source
	}
	good := newSource(t, "good")
	bad := newSource(t, "bad")
	dependent := newSource(t, "dependent")
	control := harness.New(t)
	addPolyrepoSource(t, control, "good-source", "sources/good", good)
	addPolyrepoSource(t, control, "bad-source", "sources/bad", bad)
	addPolyrepoSource(t, control, "dependent-source", "sources/dependent", dependent)
	cfg := polyrepoFile()
	cfg["concurrency"] = []int{1}
	spaces := centralSpaces(map[string]string{
		"good":      "sources/good/packages",
		"bad":       "sources/bad/packages",
		"dependent": "sources/dependent/packages",
	})
	spaces["bad"] = map[string]any{
		"path":                  []string{"sources/bad/packages"},
		"isBuildWaitingPublish": true,
	}
	cfg["spaces"] = spaces
	cfg["dependencies"] = map[string]any{"dependent": []any{"bad"}}
	cfg["scripts"] = map[string]any{
		"build":   []string{`test "$DISPAT_PACKAGE" != bad`},
		"publish": []string{"echo publishing"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure a partial fleet release")

	failed := control.Release()
	assert.Equal(t, 1, failed.Code)
	assert.Equal(t, []string{"good@0.1.0"}, polyrepoTags(control, "sources/good"),
		"independent work continues and is retained")
	assert.Empty(t, polyrepoTags(control, "sources/bad"))
	assert.Empty(t, polyrepoTags(control, "sources/dependent"), "the failed source blocks its consumer")

	cfg["scripts"] = map[string]any{
		"build":   []string{"echo building"},
		"publish": []string{"echo publishing"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, []string{"good@0.1.0"}, polyrepoTags(control, "sources/good"),
		"the retry reads and preserves the successful source tag")
	assert.Equal(t, []string{"bad@0.1.0"}, polyrepoTags(control, "sources/bad"))
	assert.Equal(t, []string{"dependent@0.1.0"}, polyrepoTags(control, "sources/dependent"))
}

// TestPolyrepoSourcePushFailureDoesNotAdvanceControl records locally, then
// rejects the configured source push. The resulting E335 may retain local
// source state, but the control repository must never checkpoint a source
// commit or tag that its remote cannot supply.
func TestPolyrepoSourcePushFailureDoesNotAdvanceControl(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")
	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	bare := filepath.Join(t.TempDir(), "source.git")
	control.Git("init", "-q", "--bare", bare)
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", bare)
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/main")

	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["changelog"] = map[string]any{"enabled": true}
	cfg["commit"] = map[string]any{"enabled": true}
	cfg["repositoryOverrides"] = map[string]any{
		"lib-source": map[string]any{"commit": map[string]any{
			"enabled": true,
			"push":    true,
			"remote":  "origin",
			"branch":  "main",
		}},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure rejected source push")
	controlBefore := control.Git("rev-parse", "HEAD")
	pinnedBefore := control.Git("rev-parse", "HEAD:sources/lib")
	require.NoError(t, os.WriteFile(filepath.Join(bare, "hooks", "pre-receive"), []byte("#!/bin/sh\nexit 39\n"), 0o755))

	failed := control.Release()
	assert.Equal(t, 1, failed.Code)
	assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))
	assert.Equal(t, pinnedBefore, control.Git("rev-parse", "HEAD:sources/lib"),
		"the control checkpoint never advances to an unreachable source commit")
	assert.Equal(t, pinnedBefore, control.Git("-C", bare, "rev-parse", "refs/heads/main"))
	assert.Empty(t, control.Git("-C", bare, "tag", "--list"), "the rejected remote received no source tag")
}

// TestPolyrepoCheckpointRefusesASourceRevisionItCannotProve: a control branch
// that names a source revision nobody else can fetch is a broken checkout for
// everyone who clones it, so the checkpoint push waits for proof that the
// source tag is on the source remote. A source that records without
// publishing, and a source remote that cannot be asked, both leave that proof
// impossible: the run refuses with E335 naming the source, the source record
// the run did write stays where it is, and no checkpoint is written or pushed.
func TestPolyrepoCheckpointRefusesASourceRevisionItCannotProve(t *testing.T) {
	for _, row := range []struct {
		name string
		// unreachable points the source at a remote that cannot be asked.
		unreachable bool
		want        []string
	}{
		{name: "the source remote lacks the revision", want: []string{"is not available from", "lib@0.1.0", "lib-source"}},
		{name: "the source remote cannot be asked", unreachable: true, want: []string{"lib-source"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			control, sourceBare, controlBare := covPolyrepoPushableFleet(t)
			if row.unreachable {
				control.Git("-C", "sources/lib", "remote", "set-url", "origin",
					filepath.Join(t.TempDir(), "not-a-repository"))
			}
			cfg := covPolyrepoFile()
			cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
			cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
			cfg.Commit = &models.CommitConfig{
				Enabled: models.Bool(true), Push: true, Remote: "origin",
				Branch: harness.DefaultBranch, Verify: models.Bool(false),
			}
			cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
				// The source records locally and publishes nothing.
				"lib-source": {Commit: &models.CommitConfig{Enabled: models.Bool(true)}},
			}
			control.WriteConfigModel(cfg)
			control.Commit("chore: configure a source that records without publishing")
			control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
			controlBefore := control.Git("rev-parse", "HEAD")

			res := control.Release()
			assert.Equal(t, 1, res.Code)
			assert.True(t, harness.IsCodePresent(res.Events, "E335"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			for _, want := range row.want {
				assert.Contains(t, res.Stdout+res.Stderr, want)
			}
			assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0",
				"the source record the run did write stays where it is")
			if !row.unreachable {
				assert.NotContains(t, control.Git("-C", sourceBare, "tag", "--list"), "lib@0.1.0")
			}
			assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"),
				"no checkpoint is written for a revision the source remote cannot be shown to carry")
			assert.Equal(t, controlBefore, control.Git("-C", controlBare, "rev-parse", "refs/heads/"+harness.DefaultBranch))
		})
	}
}

// TestRevertOnFailRestoresThroughTheOwningRepository: `revertOnFail` puts a
// failed package's folder back the way the run found it, and in a composed
// workspace that folder belongs to a repository other than the checkout that
// contains it. The rollback runs in the owning repository in both history
// modes that have more than one: in a control repository, a source package
// reverts inside its source and a control package inside the control, neither
// reaching into the other; in a choreographed fleet, a provider's package
// reverts through the provider's own repository.
func TestRevertOnFailRestoresThroughTheOwningRepository(t *testing.T) {
	t.Run("a control repository and its source", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		control.SeedPackage("packages", "tool")
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		cfg := covPolyrepoFile()
		cfg.Spaces = map[string]models.SpaceConfig{
			"libs":  {Path: models.PathList{"sources/lib/packages"}, RevertOnFail: models.Bool(true)},
			"tools": {Path: models.PathList{"packages"}, RevertOnFail: models.Bool(true)},
		}
		cfg.Scripts["publish"] = models.Script{"echo scribbled >> main.txt", "exit 3"}
		control.WriteConfigModel(cfg)
		control.Commit("chore: configure a fleet that reverts a failed publish")

		res := control.Release()
		assert.Equal(t, 1, res.Code)
		assert.Equal(t, "lib\n", readAbs(t, control.Path("sources", "lib", "packages", "lib", "main.txt")),
			"the source package folder is restored inside the source repository")
		assert.Equal(t, "tool\n", readAbs(t, control.Path("packages", "tool", "main.txt")),
			"and the control-owned package folder in the control repository")
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
		assert.Empty(t, control.TagList())
	})

	t.Run("a choreographed fleet", func(t *testing.T) {
		fleet := crossRepositoryFleet(t)
		fleet.writeConfig("sdk", func(cfg *models.File) {
			cfg.Scripts = releaseFlow("echo building", "printf 'half written\\n' > packages/sdk-pkg/main.txt; exit 1")
			cfg.Flow = &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}
			cfg.RevertOnFail = models.Bool(true)
		})
		fleet.peer("sdk").Commit("fix(sdk-pkg): fail while publishing")
		fleet.push("sdk")
		fleet.follow("api", "sdk")
		api := fleet.peer("api")

		res := api.Release("--package", "*")
		assert.Equal(t, 1, res.Code)
		assert.Equal(t, "sdk-pkg\n", readAbs(t, api.Path(".links", "sdk", "packages", "sdk-pkg", "main.txt")),
			"the provider's own repository restored its own file")
		assert.Empty(t, tagsIn(api.Repo, ".links/sdk"))
	})
}

// TestPolyrepoReleaseLockGuardsAndCleansSourceWork acquires every repository
// lock in stable name order. Contention stops later acquisitions and unwinds
// earlier ones, while interruption removes every admitted lock without
// leaving a source tag behind.
func TestPolyrepoReleaseLockGuardsAndCleansSourceWork(t *testing.T) {
	setup := func(t *testing.T, build string) (*harness.Repo, string) {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")
		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		cfg := polyrepoFile()
		cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg["scripts"] = map[string]any{
			"build":   []string{build},
			"publish": []string{"echo publishing"},
		}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: configure locked fleet")
		return control, control.AddBareRemote()
	}

	t.Run("held control lock blocks all sources", func(t *testing.T) {
		control, bare := setup(t, "echo building")
		held := holdLock(t, control, bare)

		res := releaseLocked(control)
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, strings.ToLower(res.Stdout+res.Stderr), "release lock")
		assert.Empty(t, polyrepoTags(control, "sources/lib"), "a refused fleet mutates no source")
		assert.Equal(t, held, lockObject(t, bare), "the other run's control lock remains untouched")
		assert.False(t, control.IsTagged(lockTag))
	})

	t.Run("uncoordinated source refuses the fleet", func(t *testing.T) {
		control, bare := setup(t, "echo building")
		control.Git("-C", "sources/lib", "remote", "set-url", "origin", control.Path("missing-source-remote.git"))

		res := releaseLocked(control)
		assert.Equal(t, 1, res.Code)
		assert.True(t, harness.IsCodePresent(res.Events, "E336"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Empty(t, polyrepoTags(control, "sources/lib"))
		assertLockCleared(t, control, bare)
	})

	t.Run("later source contention unwinds every earlier fleet lock", func(t *testing.T) {
		aSource := harness.New(t)
		aSource.SeedPackage("packages", "a")
		aSource.Commit("feat(a): bootstrap first source")
		zSource := harness.New(t)
		zSource.SeedPackage("packages", "z")
		zSource.Commit("feat(z): bootstrap later source")
		control := harness.New(t)
		addPolyrepoSource(t, control, "a-source", "sources/a", aSource)
		addPolyrepoSource(t, control, "z-source", "sources/z", zSource)
		cfg := polyrepoFile()
		cfg["spaces"] = centralSpaces(map[string]string{
			"a": "sources/a/packages",
			"z": "sources/z/packages",
		})
		cfg["scripts"] = map[string]any{
			"build":   []string{"echo ran > ../../../../build-ran"},
			"publish": []string{"echo publishing"},
		}
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: configure ordered fleet locks")
		controlRemote := control.AddBareRemote()
		addSourceRemote := func(path, name string) string {
			remote := filepath.Join(t.TempDir(), name+".git")
			control.Git("init", "-q", "--bare", remote)
			control.Git("-C", path, "remote", "set-url", "origin", remote)
			control.Git("-C", path, "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
			return remote
		}
		aRemote := addSourceRemote("sources/a", "a-source")
		zRemote := addSourceRemote("sources/z", "z-source")
		bareGit(t, zRemote, "-c", "user.email=other@dispat.test", "-c", "user.name=other clone",
			"tag", "-a", lockTag, "-m", "held by another release", harness.DefaultBranch)
		held := lockObject(t, zRemote)

		res := releaseLocked(control)
		assert.Equal(t, 1, res.Code)
		var acquired []string
		var contention harness.Event
		for _, event := range res.Events {
			if event.Str("message") == "release lock acquired" {
				acquired = append(acquired, event.Str("repository"))
			}
			if event.Code() == "E336" {
				contention = event
			}
		}
		assert.Equal(t, []string{"a-source", "control"}, acquired,
			"the fleet must acquire every earlier repository before reaching z-source")
		require.NotEmpty(t, contention, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, contention.Str("error"), "repository z-source")
		assert.Equal(t, held, lockObject(t, zRemote), "the later source's foreign lock remains untouched")
		assert.False(t, remoteHoldsLock(t, aRemote), "the earlier source lock must unwind")
		assert.Empty(t, control.Git("-C", "sources/a", "tag", "--list", lockTag))
		assert.Empty(t, control.Git("-C", "sources/z", "tag", "--list", lockTag))
		assertLockCleared(t, control, controlRemote)
		assert.NoFileExists(t, control.Path("build-ran"), "lock acquisition fails before package scripts")
		assert.Empty(t, polyrepoTags(control, "sources/a"))
		assert.Empty(t, polyrepoTags(control, "sources/z"))
	})

	t.Run("interruption clears the control lock", func(t *testing.T) {
		control, bare := setup(t, "echo started > ../../started; sleep 30")
		proc := control.StartReleaseEnv(harness.LockEnabled)
		require.Eventually(t, func() bool {
			_, err := os.Stat(control.Path("sources/lib/started"))
			return err == nil && remoteHoldsLock(t, bare)
		}, 20*time.Second, 20*time.Millisecond, "the source build never ran under the control lock")
		proc.Signal(os.Interrupt)
		res := proc.Wait()
		assert.NotEqual(t, 0, res.Code)
		assert.Empty(t, polyrepoTags(control, "sources/lib"), "an interrupted build records no source release")
		assertLockCleared(t, control, bare)
	})
}

// TestPolyrepoSourceCannotSwitchOffItsOwnLock: an orchestrated fleet releases
// under the control configuration's lock policy. A source whose imported
// configuration sets unsafeDisableLock is still locked, and says so on one
// warning line naming the ignored setting and the repository (not W331, which
// names repositories released without a lock), so the lock another
// run holds on that source refuses the fleet with E336 exactly as it would
// with no setting at all. The lock stays the other run's, and the control lock
// this run took first is given back.
func TestPolyrepoSourceCannotSwitchOffItsOwnLock(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	imported := polyrepoFile()
	delete(imported, "polyrepo")
	imported["spaces"] = centralSpaces(map[string]string{"workspace": "packages"})
	imported["unsafeDisableLock"] = true
	writePolyrepoJSON(t, source, "dispat.json", imported)
	source.Commit("feat(lib): a source that asks to release without its lock")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	central := polyrepoFile()
	central["configs"] = []string{"sources/lib/dispat.json"}
	writePolyrepoJSON(t, control, "dispat.json", central)
	control.Commit("chore: import the source configuration")
	controlRemote := control.AddBareRemote()
	sourceRemote := filepath.Join(t.TempDir(), "lib-source.git")
	control.Git("init", "-q", "--bare", sourceRemote)
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", sourceRemote)
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	bareGit(t, sourceRemote, "-c", "user.email=other@dispat.test", "-c", "user.name=other clone",
		"tag", "-a", lockTag, "-m", "held by another release", harness.DefaultBranch)
	held := lockObject(t, sourceRemote)

	res := releaseLocked(control)

	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireDiagnostic(t, res, "E336")
	var ignored []any
	for _, event := range res.Events {
		if event.Code() == "W331" {
			t.Errorf("a locked source is not a bypassed one: %v", event)
		}
		if strings.HasPrefix(event.Str("message"), "ignored: ") {
			ignored, _ = event["repositories"].([]any)
		}
	}
	assert.Equal(t, []any{"lib-source"}, ignored, "the ignored setting is named with its repository")
	assert.Equal(t, held, lockObject(t, sourceRemote), "the other run's lock is untouched")
	assert.Empty(t, polyrepoTags(control, "sources/lib"), "a refused fleet mutates no source")
	assertLockCleared(t, control, controlRemote)
}

// TestPolyrepoReleaseCleansLivePinContext captures the private coordinator
// path from a real package shell. Every return from the outer Release owns its
// removal, including a successful publication and a beforePublish refusal.
func TestPolyrepoReleaseCleansLivePinContext(t *testing.T) {
	tests := []struct {
		name        string
		failGating  bool
		wantCode    int
		wantPublish bool
	}{
		{name: "success", wantCode: 0, wantPublish: true},
		{name: "gating failure", failGating: true, wantCode: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := harness.New(t)
			source.SeedPackage("packages", "lib")
			source.Commit("feat(lib): bootstrap library")
			control := harness.New(t)
			addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
			capture := `printf '%s\n' "$DISPAT_INTERNAL_WORKSPACE_LIVE_PINS" > ../../../../live-pin-path`
			cfg := polyrepoFile()
			cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
			cfg["scripts"] = map[string]any{
				"build":   []string{"echo building"},
				"capture": []string{capture + `; exit 41`},
				"publish": []string{capture + `; : > ../../../../published`},
			}
			if tc.failGating {
				cfg["flow"] = map[string]any{
					"build":         []string{"build"},
					"beforePublish": []string{"capture"},
					"publish":       []string{"publish"},
				}
			}
			writePolyrepoJSON(t, control, "dispat.json", cfg)
			control.Commit("chore: configure live pin cleanup")

			res := control.Release()
			assert.Equal(t, tc.wantCode, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			pathBytes, err := os.ReadFile(control.Path("live-pin-path"))
			require.NoError(t, err, "the package shell did not receive a live coordinator")
			livePath := strings.TrimSpace(string(pathBytes))
			require.NotEmpty(t, livePath)
			_, err = os.Stat(livePath)
			assert.ErrorIs(t, err, os.ErrNotExist, "the outer Release must remove its transient coordinator")
			if tc.wantPublish {
				assert.FileExists(t, control.Path("published"))
				assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0")
			} else {
				assert.NoFileExists(t, control.Path("published"))
				assert.Empty(t, polyrepoTags(control, "sources/lib"))
			}
		})
	}
}

// TestPolyrepoRefusesUninitializedPinnedMismatchAndShallowSources exercises
// repository integrity through the binary. Every state would truncate or
// substitute a history, so each must fail before a plan is returned.
func TestPolyrepoRefusesUninitializedPinnedMismatchAndShallowSources(t *testing.T) {
	setup := func(t *testing.T) *harness.Repo {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): first")
		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		cfg := polyrepoFile()
		cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
		writePolyrepoJSON(t, control, "dispat.json", cfg)
		control.Commit("chore: assemble source")
		return control
	}

	// refusedBeforePlanning holds every refusal to the same shape: exit 1
	// naming the declared identity, and not one package planned.
	refusedBeforePlanning := func(t *testing.T, res harness.RunResult, want string) {
		t.Helper()
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "lib-source", "the refusal names the declared source")
		assert.Contains(t, strings.ToLower(res.Stdout+res.Stderr), want)
		assert.Empty(t, plannedPackages(res), "the source is refused before anything is planned")
	}

	t.Run("uninitialized", func(t *testing.T) {
		// A deinitialized submodule leaves an empty folder of the control
		// repository behind, which is the control repository, not a source.
		control := setup(t)
		control.Git("submodule", "deinit", "-q", "-f", "sources/lib")
		refusedBeforePlanning(t, control.Status(), "resolves to git root")
	})

	t.Run("nothing checked out", func(t *testing.T) {
		control := setup(t)
		control.Git("submodule", "deinit", "-q", "-f", "sources/lib")
		require.NoError(t, os.RemoveAll(control.Path("sources", "lib")))
		refusedBeforePlanning(t, control.Status(), "missing or uninitialized")
	})

	t.Run("working tree does not match pinned gitlink", func(t *testing.T) {
		control := setup(t)
		control.WriteFile("sources/lib/packages/lib/later.txt", "later\n")
		commitPolyrepoSource(t, control, "sources/lib", "fix(lib): not checkpointed")
		refusedBeforePlanning(t, control.Status(), "control head pins")
	})

	t.Run("shallow", func(t *testing.T) {
		control := setup(t)
		remote := control.Git("-C", "sources/lib", "remote", "get-url", "origin")
		control.Git("submodule", "deinit", "-q", "-f", "sources/lib")
		require.NoError(t, os.RemoveAll(control.Path("sources/lib")))
		out := control.Git("-c", "protocol.file.allow=always", "clone", "-q", "--depth", "1", "file://"+remote, "sources/lib")
		assert.Empty(t, out)
		require.Equal(t, "true", control.Git("-C", "sources/lib", "rev-parse", "--is-shallow-repository"),
			"the fixture itself must be shallow before it asks dispat to reject it")
		refusedBeforePlanning(t, control.Status(), "shallow")
	})
}

// TestPolyrepoBeforeAllTagDriftStopsBeforePublication changes a relevant
// source tag after planning but before package work. A fixed fleet snapshot
// cannot combine the old inventory with the new high-water mark.
func TestPolyrepoBeforeAllTagDriftStopsBeforePublication(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")
	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["scripts"] = map[string]any{
		"drift":   []string{"git -C sources/lib tag lib@9.0.0"},
		"build":   []string{"echo building"},
		"publish": []string{"echo published > published.txt"},
	}
	cfg["run"] = map[string]any{"beforeAll": []string{"drift"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure preflight tag drift")

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	assert.True(t, harness.IsCodePresent(res.Events, "E330"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@9.0.0",
		"the hook mutation proves the inventory really changed")
	assert.NotContains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0")
	assert.NoFileExists(t, control.Path("sources/lib/packages/lib/published.txt"),
		"publication never starts from the stale plan")
}

// TestPolyrepoBeforePublishBaselineTagDriftStopsPublication force-moves the
// source baseline after build, at the last gating hook before publish. The
// per-package snapshot check must reject the changed inventory before the
// publish script or planned release tag can run.
func TestPolyrepoBeforePublishBaselineTagDriftStopsPublication(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): initial library")
	source.Git("tag", "-a", "lib@1.0.0", "-m", "initial release")
	source.WriteFile("packages/lib/feature.txt", "next\n")
	source.Commit("feat(lib): next library feature")
	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["scripts"] = map[string]any{
		"build":      []string{"echo building"},
		"mutate-tag": []string{"git tag -f lib@1.0.0"},
		"publish":    []string{"echo published > ../../published.txt"},
	}
	cfg["flow"] = map[string]any{
		"build":         []string{"build"},
		"beforePublish": []string{"mutate-tag"},
		"publish":       []string{"publish"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure before-publish tag drift")
	baselineBefore := control.Git("-C", "sources/lib", "rev-parse", "lib@1.0.0^{commit}")

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	assert.True(t, harness.IsCodePresent(res.Events, "E330"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NotEqual(t, baselineBefore, control.Git("-C", "sources/lib", "rev-parse", "lib@1.0.0^{commit}"),
		"the hook mutation proves the baseline tag moved")
	assert.NotContains(t, polyrepoTags(control, "sources/lib"), "lib@1.1.0")
	assert.NoFileExists(t, control.Path("sources/lib/published.txt"),
		"the publish script must not run after baseline drift")
}

// TestPolyrepoBeforePublishControlDirectiveDriftStopsPublication changes the
// control history after planning when that history supplied the applicable
// release directive. Even with automatic control commits disabled, the plan
// must validate that provenance immediately before publication.
func TestPolyrepoBeforePublishControlDirectiveDriftStopsPublication(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): initial library")
	source.Git("tag", "-a", "lib@1.0.0", "-m", "initial release")
	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["scripts"] = map[string]any{
		"build":           []string{"echo building"},
		"advance-control": []string{"git -C ../../../.. commit --allow-empty -q -m 'chore: concurrent control advance'"},
		"publish":         []string{"echo published > ../../../../control-drift-published"},
	}
	cfg["flow"] = map[string]any{
		"build":         []string{"build"},
		"beforePublish": []string{"advance-control"},
		"publish":       []string{"publish"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure control provenance guard")
	baselineControl := control.Git("rev-parse", "HEAD")
	cfg["repositoryBaselines"] = []any{map[string]any{
		"consumer":   "lib",
		"releaseTag": "lib@1.0.0",
		"repository": "control",
		"revision":   baselineControl,
	}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: declare the control baseline")
	control.CommitEmpty("fix(lib): fleet release directive")
	plannedControl := control.Git("rev-parse", "HEAD")

	res := control.Release()
	assert.Equal(t, 1, res.Code)
	assert.True(t, harness.IsCodePresent(res.Events, "E330"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NotEqual(t, plannedControl, control.Git("rev-parse", "HEAD"),
		"the hook mutation proves applicable control history changed")
	assert.NotContains(t, polyrepoTags(control, "sources/lib"), "lib@1.0.1")
	assert.NoFileExists(t, control.Path("control-drift-published"),
		"publication never starts from stale control provenance")
}

// TestPolyrepoCrossRepositoryBaselineRequiresEvidence is the boundary where
// timestamps and a coincidentally matching current gitlink are unsafe. The
// consumer tag is added after the provider pointer advanced, while still
// targeting the consumer commit the control repository had pinned all along.
// Only an explicit tuple can say which provider revision that release used.
func TestPolyrepoCrossRepositoryBaselineRequiresEvidence(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.Commit("feat(lib): initial library")
	libSource.Git("tag", "-a", "lib@1.0.0", "-m", "initial library")
	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appSource.Commit("feat(app): initial application")
	appSource.Git("tag", "-a", "app@1.0.0", "-m", "initial application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages",
		"apps": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"lib"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble initial fleet")

	// Advance and tag lib, then record its pointer. app's later tag still
	// points at the old app SHA; adding that tag does not create a control
	// checkpoint and cannot prove app consumed lib@1.1.0.
	control.WriteFile("sources/lib/packages/lib/feature.txt", "feature\n")
	commitPolyrepoSource(t, control, "sources/lib", "feat(lib)^: provider feature")
	control.Git("-C", "sources/lib", "tag", "-a", "lib@1.1.0", "-m", "provider release")
	checkpointPolyrepoSource(t, control, "sources/lib")
	control.Git("-C", "sources/app", "tag", "-a", "app@1.0.1", "-m", "late tag on an old pinned commit")

	ambiguous := control.Status()
	assert.NotZero(t, ambiguous.Code, "a matching gitlink without a release checkpoint proves no consumption boundary")
	assert.Contains(t, strings.ToLower(ambiguous.Stdout+ambiguous.Stderr), "baseline")

	baseline := map[string]any{
		"consumer":   "app",
		"releaseTag": "app@1.0.1",
		"repository": "lib-source",
		"revision":   "lib@1.0.0",
	}
	cfg["repositoryBaselines"] = []any{baseline, baseline}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	duplicate := control.Status()
	assert.NotZero(t, duplicate.Code)
	assert.Contains(t, strings.ToLower(duplicate.Stdout+duplicate.Stderr), "baseline")

	cfg["repositoryBaselines"] = []any{baseline}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	recovered := control.StatusOK()
	assert.Equal(t, "unchanged", harness.GraphLine(recovered.Events, "lib").Str("message"))
	assert.Equal(t, "catch-up from lib", harness.GraphLine(recovered.Events, "app").Str("reason"))
	assert.True(t, harness.IsCodePresentForPackage(recovered.Events, "W193", "app"))
}

// TestPolyrepoCustomCheckpointMessageProvesNoBaseline moves consumer and
// provider gitlinks together but records them under an opaque control message.
// Even with matching tag targets, that commit is not an ordinary parseable
// release checkpoint and cannot establish the consumer's provider boundary.
func TestPolyrepoCustomCheckpointMessageProvesNoBaseline(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.Commit("feat(lib): initial library")
	libSource.Git("tag", "-a", "lib@1.0.0", "-m", "initial library")
	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appSource.Commit("feat(app): initial application")
	appSource.Git("tag", "-a", "app@1.0.0", "-m", "initial application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages",
		"apps": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"lib"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: establish old fleet snapshot")

	control.WriteFile("sources/lib/packages/lib/feature.txt", "provider release\n")
	commitPolyrepoSource(t, control, "sources/lib", "feat(lib): provider release")
	control.Git("-C", "sources/lib", "tag", "-a", "lib@1.1.0", "-m", "provider release")
	control.WriteFile("sources/app/packages/app/adopt.txt", "consumer release\n")
	commitPolyrepoSource(t, control, "sources/app", "fix(app): consumer release")
	control.Git("-C", "sources/app", "tag", "-a", "app@1.0.1", "-m", "consumer release")
	control.Git("add", "sources/lib", "sources/app")
	control.Git("commit", "-q", "-m", "chore: opaque custom checkpoint")

	control.WriteFile("sources/lib/packages/lib/fix.txt", "later provider fix\n")
	commitPolyrepoSource(t, control, "sources/lib", "fix(lib)^: later provider fix")
	checkpointPolyrepoSource(t, control, "sources/lib")

	status := control.Status()
	assert.NotZero(t, status.Code)
	assert.True(t, harness.IsCodePresent(status.Events, "E333"), "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	assert.Contains(t, strings.ToLower(status.Stdout+status.Stderr), "baseline")
}

// TestPolyrepoReleaseCheckpointProvidesNextConsumerBaseline is the positive
// automatic-association case. A normal control release checkpoint names the
// exact source tags and records their matching gitlink transitions; after the
// bootstrap tuple is removed, that durable commit is enough to place the
// consumer's next provider boundary without dates or guesswork.
func TestPolyrepoReleaseCheckpointProvidesNextConsumerBaseline(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.Commit("feat(lib): initial library")
	libSource.Git("tag", "-a", "lib@1.0.0", "-m", "initial library")
	libSource.WriteFile("packages/lib/feature.txt", "first feature\n")
	libSource.Commit("feat(lib)^: first provider feature")
	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appSource.Commit("feat(app): initial application")
	appSource.Git("tag", "-a", "app@1.0.0", "-m", "initial application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages",
		"apps": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"lib"}}
	cfg["changelog"] = map[string]any{"enabled": true}
	cfg["commit"] = map[string]any{"enabled": true}
	cfg["repositoryBaselines"] = []any{map[string]any{
		"consumer":   "app",
		"releaseTag": "app@1.0.0",
		"repository": "lib-source",
		"revision":   "lib@1.0.0",
	}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble checkpointed fleet")

	control.ReleaseOK()
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@1.1.0")
	assert.Contains(t, polyrepoTags(control, "sources/app"), "app@1.0.1")
	checkpoint := control.Git("log", "-1", "--format=%H", "--grep", "app@1.0.1")
	require.NotEmpty(t, checkpoint, "the consumer release writes an ordinary identifiable checkpoint")
	assert.Contains(t, control.Git("show", "-s", "--format=%s", checkpoint), "app@1.0.1")
	appPin := control.Git("-C", "sources/app", "rev-parse", "app@1.0.1^{commit}")
	libPin := control.Git("-C", "sources/lib", "rev-parse", "lib@1.1.0^{commit}")
	assert.Equal(t, appPin, control.Git("rev-parse", checkpoint+":sources/app"))
	assert.Equal(t, libPin, control.Git("rev-parse", checkpoint+":sources/lib"),
		"the consumer checkpoint's same tree pins its incorporated provider revision")
	assert.NotEqual(t, appPin, control.Git("rev-parse", checkpoint+"^:sources/app"),
		"the checkpoint itself carries the consumer gitlink transition")

	delete(cfg, "repositoryBaselines")
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: rely on the recorded release checkpoint")
	control.WriteFile("sources/lib/packages/lib/feature.txt", "second fix\n")
	commitPolyrepoSource(t, control, "sources/lib", "fix(lib)^: second provider change")
	checkpointPolyrepoSource(t, control, "sources/lib")

	status := control.StatusOK()
	assert.Equal(t, "1.1.0 -> 1.1.1", harness.GraphLine(status.Events, "lib").Str("version"))
	assert.Equal(t, "propagated from lib", harness.GraphLine(status.Events, "app").Str("reason"))
	assert.False(t, harness.IsCodePresentForPackage(status.Events, "W193", "app"),
		"the provider and consumer will move together; this is fresh propagation")
}

// TestPolyrepoImportedConsumerBaselineRequiresEvidence covers the other
// unsafe association shape: an already-released standalone consumer is added
// to the fleet after the provider pointer reached a newer release. The import
// commit proves when the checkout joined, not what provider revision the old
// consumer release used.
func TestPolyrepoImportedConsumerBaselineRequiresEvidence(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.Commit("feat(lib): initial library")
	libSource.Git("tag", "-a", "lib@1.0.0", "-m", "initial library")
	libSource.WriteFile("packages/lib/feature.txt", "new provider release\n")
	libSource.Commit("feat(lib)^: provider feature")
	libSource.Git("tag", "-a", "lib@1.1.0", "-m", "new provider release")

	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appConfig := polyrepoFile()
	delete(appConfig, "polyrepo")
	appConfig["spaces"] = centralSpaces(map[string]string{"apps": "packages"})
	writePolyrepoJSON(t, appSource, "dispat.json", appConfig)
	appSource.Commit("feat(app): independently released app")
	appSource.Git("tag", "-a", "app@1.0.0", "-m", "existing app release")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: pin provider at its newer release")

	// app joins only now. Its source tag predates the import, so this gitlink
	// addition cannot prove app consumed the provider revision currently pinned.
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg["configs"] = []string{"sources/app/dispat.json"}
	cfg["dependencies"] = map[string]any{"app": []any{"lib"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: import independently released consumer")

	ambiguous := control.Status()
	assert.NotZero(t, ambiguous.Code)
	assert.Contains(t, strings.ToLower(ambiguous.Stdout+ambiguous.Stderr), "baseline")

	cfg["repositoryBaselines"] = []any{map[string]any{
		"consumer":   "app",
		"releaseTag": "app@1.0.0",
		"repository": "lib-source",
		"revision":   "lib@1.0.0",
	}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	recovered := control.StatusOK()
	assert.Equal(t, "unchanged", harness.GraphLine(recovered.Events, "lib").Str("message"))
	assert.Equal(t, "catch-up from lib", harness.GraphLine(recovered.Events, "app").Str("reason"))
	assert.True(t, harness.IsCodePresentForPackage(recovered.Events, "W193", "app"))
}

// TestPolyrepoCommitModeSourceRestoresASkippedConsumer: the commit mode default
// that restores a skipped package's folder holds in a fleet too, decided by
// the policy of the repository that owns the folder. The source makes release
// commits and sets no revertOnFail, so the consumer its failed provider skips
// is restored inside the source repository, and the source checkout is left
// with nothing of the skipped release in it.
func TestPolyrepoCommitModeSourceRestoresASkippedConsumer(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "core")
	source.SeedPackage("packages", "app")
	source.Commit("feat(core)^: bootstrap the library, reaching its consumer")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	// core fails only once app's version stage has edited its folder, so the
	// skip always follows a stage that wrote.
	appMark := harness.ShQuote(filepath.Join(t.TempDir(), "app"))
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["dependencies"] = map[string]any{"app": []any{"core"}}
	cfg["scripts"] = map[string]any{
		"build":  []string{"echo building"},
		"mutate": []string{"echo dirty >> main.txt && echo extra > extra.txt && : > " + appMark},
		"publish": []string{`if [ "$DISPAT_PACKAGE" = core ]; then while [ ! -e ` + appMark +
			` ]; do sleep 0.05; done; exit 1; fi; echo publishing`},
	}
	cfg["flow"] = map[string]any{
		"version": []string{"mutate"},
		"build":   []string{"build"},
		"publish": []string{"publish"},
	}
	cfg["repositoryOverrides"] = map[string]any{
		"lib-source": map[string]any{"commit": map[string]any{"enabled": true}},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: release a source in commit mode")

	res := control.Release()
	require.Equal(t, 1, res.Code, "the provider's publish failure fails the run\nstdout:\n%s", res.Stdout)
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W194", "app"), "app is reported blocked")
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.Equal(t, "app\n", readFileString(t, control.Path("sources", "lib", "packages", "app", "main.txt")),
		"the tracked edit is restored in the source repository")
	assert.NoFileExists(t, control.Path("sources", "lib", "packages", "app", "extra.txt"),
		"the untracked file is removed")
	assert.Empty(t, control.Git("-C", "sources/lib", "status", "--porcelain"),
		"nothing of the skipped release is left in the source checkout")
}
