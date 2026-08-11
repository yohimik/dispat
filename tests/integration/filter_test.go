package integration

// Area 17: the --package and --space selection every package command shares.
// Both flags take names or globs, repeated or comma-separated, matched
// case-insensitively; a term matching no package is an error, never an empty
// selection. Without terms, the folder the command was invoked from is the
// selection: inside a package folder that package, inside a space folder that
// space, anywhere else nothing at all — and an explicit term always wins over
// the folder it was typed in.
//
// The filter narrows, it never widens. `dispat run` picks its window first
// (the release window, or what --since addresses, or every package for
// --since all) and the terms pick from it; the step commands and preview
// narrow the releasing set the same way; compute scopes its suggestions to
// the selected consumers while still detecting against the whole workspace.
//
// A standalone package — a `packages` entry with a path — belongs to no
// space, so only --package reaches it.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// filterRepo is this file's fixture:
//
//	packages/core  packages/web   space "libs"  (web consumes core)
//	apps/site                     space "apps"
//	apps/group/deep               standalone, nested inside the apps folder
//	tools/tool                    standalone, outside every space
//
// "libs" defines the "lint" script and the file level defines "stamp", so a
// space term and a file-level script can be told apart. Every package is
// changed by the bootstrap commit, so the release window covers them all.
func filterRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{
		"build":   echoBuild,
		"publish": "echo publishing",
		"stamp":   `echo "$DISPAT_PACKAGE" >> "$(git rev-parse --show-toplevel)/stamp.log"`,
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(), Scripts: map[string]string{
			"lint": `echo "$DISPAT_PACKAGE" >> "$(git rev-parse --show-toplevel)/lint.log"`,
		}},
		"apps": {Path: "apps", Flow: buildPublish()},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"deep": {Path: "apps/group/deep", Flow: buildPublish()},
		"tool": {Path: "tools/tool", Flow: buildPublish()},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.SeedPackage("apps", "site")
	r.WriteFile("apps/.dispatignore", "group\n")
	r.WriteFile("apps/group/deep/main.txt", "x")
	r.WriteFile("tools/tool/main.txt", "x")
	r.Commit("feat(core,web,site,deep,tool): bootstrap every package")
	return r
}

// reset deletes a log file if it exists, so a scenario can tell "nothing ran"
// apart from "the previous step's output".
func reset(r *harness.Repo, name string) {
	_ = os.Remove(r.Path(name))
}

// logged returns the packages a script recorded, in execution order; nil when
// the log does not exist because nothing ran.
func logged(r *harness.Repo, name string) []string {
	data, err := os.ReadFile(r.Path(name))
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

// TestFilterSelectsNamedPackages: the --package spellings — one name, several,
// comma-separated, repeated, glob, case-insensitive — each narrow the window
// to exactly the packages they name, in graph order.
func TestFilterSelectsNamedPackages(t *testing.T) {
	r := filterRepo(t)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"-p", "web"}, []string{"web"}},
		{[]string{"-p", "core,web"}, []string{"core", "web"}},
		{[]string{"-p", "web", "-p", "core"}, []string{"core", "web"}},
		{[]string{"--package", "CORE"}, []string{"core"}},
		{[]string{"-p", "*e*"}, []string{"core", "web"}},
		{[]string{"-p", "*"}, []string{"core", "web"}},
	} {
		reset(r, "lint.log")
		res := r.RunScript("lint", tc.args...)
		require.Equal(t, 0, res.Code, "args %v — stderr:\n%s", tc.args, res.Stderr)
		assert.Equal(t, tc.want, logged(r, "lint.log"), "args: %v", tc.args)
	}
}

// TestFilterNarrowsTheWindowNeverWidensIt: a filter picks from the packages
// the window already covers, so an unchanged package needs a window that
// reaches it — which is what --since all is for.
func TestFilterNarrowsTheWindowNeverWidensIt(t *testing.T) {
	r := filterRepo(t)
	r.ReleaseOK() // every package converges

	r.RunScriptOK("lint", "-p", "core")
	assert.Empty(t, logged(r, "lint.log"), "an unchanged package is out of the release window")

	r.RunScriptOK("lint", "--since", "all", "-p", "core")
	assert.Equal(t, []string{"core"}, logged(r, "lint.log"), "--since all puts every package on the table")
}

// TestFilterUnmatchedTermsAreErrors: a term matching nothing is a typo, and
// the error looks across the other flag — the two spellings tell each other
// apart so a standalone package's name explains itself.
func TestFilterUnmatchedTermsAreErrors(t *testing.T) {
	r := filterRepo(t)

	res := r.RunScript("lint", "-p", "ghost")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "matches no package")
	assert.Contains(t, res.Stdout, "ghost")
	assert.Contains(t, res.Stdout, "core", "the error lists what was discovered")

	res = r.RunScript("lint", "-p", "no-such-*")
	assert.Equal(t, 1, res.Code, "a glob matching nothing is a typo too")

	res = r.RunScript("lint", "-s", "ghosts")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "matches no configured space")
	assert.Contains(t, res.Stdout, "apps, libs")

	res = r.RunScript("lint", "-p", "libs")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "libs is a space")

	res = r.RunScript("lint", "-s", "core")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "core is a package")
}

// TestFilterSpaceTermStaysInItsSpace: a --space term selects that space's
// packages and no others, and its glob and "every space" spellings do the
// same. A standalone package belongs to no space, whichever folder it sits in.
func TestFilterSpaceTermStaysInItsSpace(t *testing.T) {
	r := filterRepo(t)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"-s", "libs"}, []string{"core", "web"}},
		{[]string{"--space", "apps"}, []string{"site"}},
		{[]string{"-s", "libs,apps"}, []string{"core", "web", "site"}},
		{[]string{"-s", "*"}, []string{"core", "web", "site"}},
		{[]string{"-s", "libs", "-p", "tool"}, []string{"core", "web", "tool"}},
	} {
		reset(r, "stamp.log")
		res := r.RunScript("stamp", tc.args...)
		require.Equal(t, 0, res.Code, "args %v — stderr:\n%s", tc.args, res.Stderr)
		assert.ElementsMatch(t, tc.want, logged(r, "stamp.log"), "args: %v", tc.args)
	}
}

// TestFilterStandalonePackageBelongsToNoSpace: a `packages` entry with a path
// is reachable through --package and through the every-package glob, and
// never through --space — not even the one whose folder it happens to sit
// under.
func TestFilterStandalonePackageBelongsToNoSpace(t *testing.T) {
	r := filterRepo(t)

	r.RunScriptOK("stamp", "-p", "tool")
	assert.Equal(t, []string{"tool"}, logged(r, "stamp.log"))

	reset(r, "stamp.log")
	r.RunScriptOK("stamp", "-p", "*")
	assert.ElementsMatch(t, []string{"core", "web", "site", "deep", "tool"}, logged(r, "stamp.log"),
		"--package '*' is every package, standalone ones included")

	reset(r, "stamp.log")
	r.RunScriptOK("stamp", "-s", "apps")
	assert.Equal(t, []string{"site"}, logged(r, "stamp.log"),
		"deep sits under the apps folder but is not the space's")

	assert.Equal(t, 1, r.RunScript("stamp", "-s", "tool").Code)
}

// TestFilterInfersFromTheInvocationFolder: with no terms, the folder is the
// selection — a package folder or any subfolder of it selects that package, a
// space folder that space, the monorepo root and anywhere outside select
// nothing. The deepest match wins, so a standalone package nested inside
// another folder still selects itself.
func TestFilterInfersFromTheInvocationFolder(t *testing.T) {
	r := filterRepo(t)
	r.WriteFile("packages/core/internal/keep.txt", "x")
	r.WriteFile("apps/group/deep/src/keep.txt", "x")
	r.Commit("chore(core,deep): nested folders to stand in")

	for _, tc := range []struct {
		at   string
		want []string
	}{
		{"packages/core", []string{"core"}},
		{"packages/core/internal", []string{"core"}},
		{"packages", []string{"core", "web"}},
		{"apps", []string{"site"}},
		{"apps/group/deep", []string{"deep"}},
		{"apps/group/deep/src", []string{"deep"}},
		{"tools", []string{"core", "web", "site", "deep", "tool"}},
		{".", []string{"core", "web", "site", "deep", "tool"}},
	} {
		reset(r, "stamp.log")
		res := r.CommandAt(tc.at, "stamp")
		require.Equal(t, 0, res.Code, "at %s — stderr:\n%s", tc.at, res.Stderr)
		assert.ElementsMatch(t, tc.want, logged(r, "stamp.log"), "at: %s", tc.at)
	}
}

// TestFilterExplicitTermsBeatTheFolder: a term typed on the command line is
// the whole answer, whichever folder it was typed in.
func TestFilterExplicitTermsBeatTheFolder(t *testing.T) {
	r := filterRepo(t)

	res := r.CommandAt("packages/core", "stamp", "-p", "site")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, []string{"site"}, logged(r, "stamp.log"))

	reset(r, "stamp.log")
	res = r.CommandAt("packages/core", "stamp", "-s", "apps")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, []string{"site"}, logged(r, "stamp.log"))
}

// TestFilterRefusesASelectionWithoutTheScript: a filter reaching only packages
// that resolve no command for the name is an error, not a silent no-op — the
// same guard a whole-monorepo run applies.
func TestFilterRefusesASelectionWithoutTheScript(t *testing.T) {
	r := filterRepo(t)
	res := r.RunScript("lint", "-p", "site")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "no selected package defines script")
}

// TestFilterStepCommandsSelect: the step commands take the same terms and the
// same folder inference, and a selected package the plan is not releasing is a
// logged no-op rather than a failure.
func TestFilterStepCommandsSelect(t *testing.T) {
	r := filterRepo(t)

	res := r.Command("commit", "--package", "web", "--tag")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.True(t, r.HasTag("web@0.1.0"), "tags: %v", r.TagList())
	assert.False(t, r.HasTag("core@0.1.0"), "only the selected package is committed and tagged")

	res = r.Command("changelog", "--space", "libs")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.FileExists(t, r.Path("packages", "core", "CHANGELOG.md"))
	assert.NoFileExists(t, r.Path("apps", "site", "CHANGELOG.md"), "the other space stays untouched")

	res = r.CommandAt("apps/site", "changelog")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.FileExists(t, r.Path("apps", "site", "CHANGELOG.md"), "the folder is the selection")

	// web is committed and tagged, so it is no longer releasing: selecting it
	// is a logged no-op, never a failure.
	res = r.Command("changelog", "--package", "web")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stderr+res.Stdout, "not releasing")

	assert.Equal(t, 1, r.Command("autoversion", "-p", "ghost").Code, "an unmatched term is an error")
}

// TestFilterPreviewSelects: preview takes the same terms and the same folder
// inference, and names what it found nothing pending for.
func TestFilterPreviewSelects(t *testing.T) {
	r := filterRepo(t)

	res := r.Command("preview", "--package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "## core@0.1.0")
	assert.NotContains(t, res.Stdout, "## site@", "only the selected package")

	res = r.Command("preview", "--space", "apps")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "## site@0.1.0")
	assert.NotContains(t, res.Stdout, "## core@")

	res = r.CommandAt("packages/core", "preview")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "## core@0.1.0", "the folder is the selection")

	r.ReleaseOK()
	res = r.Command("preview", "--package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "no pending changes for core")
}

// TestFilterComputeScopesSuggestions: compute reports only the selected
// consumers' edges, while still detecting against every package's manifests —
// scanning only the selection would turn a supported edge into a removal.
func TestFilterComputeScopesSuggestions(t *testing.T) {
	r := filterRepo(t)
	// web declares core (already in the config) and tool (drift); site
	// declares core (drift too, in another space).
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("tools/tool/package.json", `{"name": "@acme/tool", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web", "version": "0.0.0",
		"dependencies": {"@acme/core": "workspace:*", "@acme/tool": "workspace:*"}}`)
	r.WriteFile("apps/site/package.json", `{"name": "@acme/site", "version": "0.0.0",
		"dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("chore(core,web,site,tool): manifests")

	res := r.Command("compute", "--package", "web")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "web -> tool")
	assert.NotContains(t, res.Stdout, "site -> core", "another consumer's drift is out of scope")
	assert.NotContains(t, res.Stdout, "- remove",
		"the declared web -> core edge is supported by a manifest, and detection reads "+
			"every package's manifests however the filter narrows the report")

	res = r.Command("compute", "--package", "web", "--write")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	data, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"provider": "tool"`)
	assert.NotContains(t, string(data), `"consumer": "site"`)

	res = r.Command("compute", "--package", "web")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "dependencies and baselines are in sync for web")

	res = r.Command("compute", "--space", "apps")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "site -> core")
}

// TestFilterPositionalPackagesAreAUsageError: the selection is a flag, so a
// bare package name after any of these commands is a usage mistake caught
// before anything is read.
func TestFilterPositionalPackagesAreAUsageError(t *testing.T) {
	r := filterRepo(t)
	for _, args := range [][]string{
		{"run", "lint", "core"},
		{"preview", "core"},
		{"changelog", "core"},
		{"autoversion", "core"},
		{"commit", "core"},
		{"compute", "core"},
	} {
		assert.Equal(t, 2, r.Command(args...).Code, "args: %v", args)
	}
}
