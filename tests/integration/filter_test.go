package integration

// Area 17: the --package, --space and --group selection every package command
// shares. All three flags take names or globs, repeated or comma-separated,
// matched case-insensitively, and they union; a term matching no package is an
// error, never an empty selection, and it names the flag that would have
// reached it. Without terms, the folder the command was invoked from is the
// selection: inside a package folder that package, inside a space folder that
// space, anywhere else nothing at all — and an explicit term always wins over
// the folder it was typed in. A versioning group is never inferred from a
// folder, because it is a versioning relationship rather than a place.
//
// The filter narrows, it never widens. `dispat run` picks its window first
// (the release window, or what --since addresses, or every package for
// --since all) and the terms pick from it; the step commands and preview
// narrow the releasing set the same way; compute scopes its suggestions to
// the selected consumers while still detecting against the whole workspace.
//
// `release` and `status` read the same selection, with one rule of their own:
// publishing happens in dependency order, so a selected package whose provider
// is releasing and unselected is withheld for the next run (W230) rather than
// published ahead of it, while a selection that releases part of a versioning
// group publishes and warns (W231). --strict refuses either outright, before
// anything is built, and naming the group instead of its members is the
// selection that cannot split one.
//
// A standalone package — a `packages` entry with a path — belongs to no
// space, so only --package and the group it joined reach it.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

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
	cfg.Scripts = map[string]models.Script{
		"build":   {echoBuild},
		"publish": {"echo publishing"},
		"stamp":   {`echo "$DISPAT_PACKAGE" >> "$(git rev-parse --show-toplevel)/stamp.log"`},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), Scripts: map[string]models.Script{
			"lint": {`echo "$DISPAT_PACKAGE" >> "$(git rev-parse --show-toplevel)/lint.log"`},
		}},
		"apps": {Path: models.PathList{"apps"}, Flow: buildPublish()},
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
	r.WriteFile("apps/.dispatexclude", "group\n")
	r.WriteFile("apps/group/deep/main.txt", "x")
	r.WriteFile("tools/tool/main.txt", "x")
	r.Commit("feat(core,web,site,deep,tool): bootstrap every package")
	return r
}

// groupRepo is the fixture for the --group term, laid out so a group is
// provably not a folder:
//
//	packages/core  packages/web   space "libs", joined to the group "shared"
//	tools/tool                    standalone, joined to "shared" as well
//	apps/site                     space "apps", versioning as its own group
//	extras/solo                   space "extras", versioning independently
//
// So "shared" spans a space and a standalone package, "apps" is a group
// nothing declares (a space that versions as one carries its own name), and
// solo belongs to no group at all. The packages have no dependencies on each
// other, which keeps the publish order out of these scenarios.
func groupRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":   {echoBuild},
		"publish": {"echo publishing"},
		"stamp":   {`echo "$DISPAT_PACKAGE" >> "$(git rev-parse --show-toplevel)/stamp.log"`},
	}
	cfg.VersionGroups = map[string]models.VersionGroupConfig{
		"shared": {Versioning: "fixed"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs":   {Path: models.PathList{"packages"}, VersionGroup: "shared", Flow: buildPublish()},
		"apps":   {Path: models.PathList{"apps"}, Versioning: "fixed", Flow: buildPublish()},
		"extras": {Path: models.PathList{"extras"}, Flow: buildPublish()},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"tool": {Path: "tools/tool", VersionGroup: "shared", Flow: buildPublish()},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.SeedPackage("apps", "site")
	r.SeedPackage("extras", "solo")
	r.WriteFile("tools/tool/main.txt", "x")
	r.Commit("feat(core,web,site,solo,tool): bootstrap every package")
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

// TestFilterRefusesASelectionWithoutTheScript: a selection the user spelled
// out reaching only packages that resolve no command for the name is an
// error, not a silent no-op — in every explicit spelling: a package term, a
// space term, the invocation folder, and a term composed with --since. The
// window-only counterpart is a reported no-op, pinned in run_test.go.
func TestFilterRefusesASelectionWithoutTheScript(t *testing.T) {
	r := filterRepo(t)
	res := r.RunScript("lint", "-p", "site")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "no selected package defines script")

	res = r.RunScript("lint", "-s", "apps")
	assert.Equal(t, 1, res.Code, "a space term is the same claim about its packages")
	assert.Contains(t, res.Stdout, "no selected package defines script")

	res = r.CommandAt("apps/site", "lint")
	assert.Equal(t, 1, res.Code, "the invocation folder is an explicit selection too")
	assert.Contains(t, res.Stdout, "no selected package defines script")

	res = r.RunScript("lint", "--since", "all", "-p", "site")
	assert.Equal(t, 1, res.Code, "--since widens the window without excusing the term")
	assert.Contains(t, res.Stdout, "no selected package defines script")
}

// TestRunMixedSelectionRunsTheResolvers: an explicit selection refuses only
// when nothing resolves the name — one resolver among the named packages
// runs, the rest complete as no-ops, and the command succeeds.
func TestRunMixedSelectionRunsTheResolvers(t *testing.T) {
	r := filterRepo(t)
	res := r.RunScript("lint", "-p", "core,site")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"core"}, logged(r, "lint.log"), "core runs; site is a no-op, not a refusal")
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

	// web is committed and tagged, so the recomputed plan no longer releases
	// it: selecting it is a logged no-op, never a failure. --since all is what
	// puts it back on the table.
	res = r.Command("changelog", "--package", "web")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stderr+res.Stdout, "outside the window, nothing to do")

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
// scanning only the selection would turn a supported edge into a removal. The
// baselines are scoped the same way, by the package the entry would be about.
func TestFilterComputeScopesSuggestions(t *testing.T) {
	r := filterRepo(t)
	// web declares core (already in the config) and tool (drift); site
	// declares core (drift too, in another space). Only tool carries a
	// version worth a baseline, and it is outside every selection below.
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("tools/tool/package.json", `{"name": "@acme/tool", "version": "4.2.0"}`)
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web", "version": "0.0.0",
		"dependencies": {"@acme/core": "workspace:*", "@acme/tool": "workspace:*"}}`)
	r.WriteFile("apps/site/package.json", `{"name": "@acme/site", "version": "0.0.0",
		"dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("chore(core,web,site,tool): manifests")

	res := r.Command("compute", "--package", "web")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "web -> tool")
	assert.NotContains(t, res.Stdout, "site -> core", "another consumer's drift is out of scope")
	assert.NotContains(t, res.Stdout, "+ initial", "and so is another package's missing baseline")
	assert.NotContains(t, res.Stdout, "- remove",
		"the declared web -> core edge is supported by a manifest, and detection reads "+
			"every package's manifests however the filter narrows the report")

	res = r.Command("compute", "--package", "web", "--write")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	data, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"tool"`)
	assert.NotContains(t, string(data), `"site": [`, "the deselected consumer got no key")

	res = r.Command("compute", "--package", "web")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "dependencies and baselines are in sync for web")

	res = r.Command("compute", "--space", "apps")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "site -> core")

	// Naming the package it is about brings the baseline into scope.
	res = r.Command("compute", "--package", "tool")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "+ initial tool 4.2.0")
}

// TestFilterReleaseSelectsPartOfTheGraph: a release takes the same terms as
// every other command. What it selects is released for real — versioned,
// tagged, published — and everything else is left exactly where it was, for a
// later run to pick up.
func TestFilterReleaseSelectsPartOfTheGraph(t *testing.T) {
	r := filterRepo(t)

	res := r.ReleaseOK("-p", "core")
	assert.Equal(t, []string{"core@0.1.0"}, r.TagList(), "only the selected package is released")
	assert.Equal(t, "⊝ not selected", harness.GraphLine(res.Events, "site").Str("message"),
		"the graph says which packages the selection left out")

	// A space term releases that space's packages, and the standalone package
	// belonging to no space stays behind.
	r.ReleaseOK("-s", "apps")
	assert.ElementsMatch(t, []string{"core@0.1.0", "site@0.1.0"}, r.TagList())

	// The unfiltered run finishes the job: web, deep and tool release at the
	// versions they were always owed, and the packages already out converge.
	r.ReleaseOK()
	assert.ElementsMatch(t,
		[]string{"core@0.1.0", "site@0.1.0", "web@0.1.0", "deep@0.1.0", "tool@0.1.0"}, r.TagList())
	assert.Equal(t, 1, r.TagCount("core@"), "a package already released is not re-released")
}

// TestFilterReleaseWithholdsWhatTheOrderCannotReach: publishing a consumer
// before a provider the same plan releases is the one staleness case the
// publish order exists to prevent, so a selection that asks for it gets the
// consumer withheld (W230) instead — named, with what it waits for — and the
// run still exits 0. Naming the provider too releases both, in order.
func TestFilterReleaseWithholdsWhatTheOrderCannotReach(t *testing.T) {
	r := filterRepo(t)

	res := r.ReleaseOK("-p", "web")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W230", "web"),
		"web must be reported as withheld, not silently skipped")
	assert.Empty(t, r.TagList(), "web depends on a releasing core, so nothing may go out")

	// The finding names the provider to add, and adding it releases both.
	r.ReleaseOK("-p", "core,web")
	assert.ElementsMatch(t, []string{"core@0.1.0", "web@0.1.0"}, r.TagList())

	// An unchanged provider is nothing to wait for: once core is out, web
	// alone is a perfectly good selection.
	r.WriteFile("packages/web/feature.txt", "x")
	r.Commit("feat(web): more web")
	r.ReleaseOK("-p", "web")
	assert.True(t, r.HasTag("web@0.2.0"), "tags: %v", r.TagList())
}

// TestFilterReleaseStrictRefusesBeforeAnythingRuns: --strict turns the
// withholding into a refusal, and the refusal comes before any release work —
// the packages the selection *could* have released stay untouched too, so a
// filtered release in CI is all-or-nothing.
func TestFilterReleaseStrictRefusesBeforeAnythingRuns(t *testing.T) {
	r := filterRepo(t)

	res := r.Release("-p", "web,site", "--strict")
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.True(t, harness.HasCodeForPackage(res.Events, "W230", "web"))
	assert.Empty(t, r.TagList(), "site was releasable and must not have been released either")
	assert.NotContains(t, res.Stdout, "building", "the refusal comes before any stage script")

	// Without --strict the same selection releases what it can.
	r.ReleaseOK("-p", "web,site")
	assert.Equal(t, []string{"site@0.1.0"}, r.TagList())

	// A selection the plan can release cleanly is unaffected by --strict.
	r.ReleaseOK("-p", "core", "--strict")
	assert.ElementsMatch(t, []string{"site@0.1.0", "core@0.1.0"}, r.TagList())
}

// TestFilterReleaseSplitsAVersioningGroup: a versioning group is not an
// ordering constraint, so a selection that takes part of one releases and says
// so (W231). The group's shared version is untrue until the next run, which
// makes it whole with nothing for an operator to do: the members left behind
// release the split-off work at the version that already carries it — their
// own release, not a ride, since re-counting work the group has published
// would burn the next prefix on it a second time. --strict is how a
// repository opts out of ever being in that state.
func TestFilterReleaseSplitsAVersioningGroup(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Versioning: "fixed", Flow: buildPublish()},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "one")
	r.SeedPackage("packages", "two")
	r.Commit("feat(one,two): bootstrap the group")

	res := r.Release("-p", "one", "--strict")
	require.Equal(t, 1, res.Code, "--strict refuses to split a group\nstdout:\n%s", res.Stdout)
	assert.True(t, harness.HasCode(res.Events, "W231"))
	assert.Empty(t, r.TagList())

	res = r.ReleaseOK("-p", "one")
	assert.True(t, harness.HasCode(res.Events, "W231"), "the split is reported, not refused")
	assert.Equal(t, []string{"one@0.1.0"}, r.TagList())

	// The next run puts the group back on one version: two catches up at the
	// 0.1.0 that already carries the shared feat — its own release, with the
	// feat in its own changeset — and one, which published that work, is not
	// dragged into an empty re-release at the next minor.
	res = r.ReleaseOK()
	assert.False(t, harness.HasCode(res.Events, "W234"),
		"nobody rides: two's own commits are the whole cause")
	assert.ElementsMatch(t, []string{"one@0.1.0", "two@0.1.0"}, r.TagList())

	r.ReleaseOK()
	assert.ElementsMatch(t, []string{"one@0.1.0", "two@0.1.0"}, r.TagList(), "converged")
}

// TestFilterReleaseInfersFromTheInvocationFolder: with no terms the folder is
// the selection here too, so a release run from inside a package folder is
// that package's release.
func TestFilterReleaseInfersFromTheInvocationFolder(t *testing.T) {
	r := filterRepo(t)

	res := r.CommandAt("packages/core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, []string{"core@0.1.0"}, r.TagList())

	// And from the root it is the whole monorepo, as it always was.
	r.ReleaseOK()
	assert.Len(t, r.TagList(), 5)
}

// TestFilterReleaseRecordsOnlyWhatReleased: the durable records follow the
// narrowed run and nothing else — the release commit names the tags that were
// actually created, the changelog of an unreleased package is never written,
// and no tag exists for a package the selection left out.
func TestFilterReleaseRecordsOnlyWhatReleased(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.SeedPackage("packages", "other")
	r.Commit("feat(core,web,other): bootstrap")

	r.ReleaseOK("-p", "other")
	subject := r.Git("log", "-1", "--format=%s")
	assert.Contains(t, subject, "other@0.1.0")
	assert.NotContains(t, subject, "core@", "the release commit names only what was released")
	assert.Equal(t, []string{"other@0.1.0"}, r.TagList())
	assert.FileExists(t, r.Path("packages", "other", "CHANGELOG.md"))
	assert.NoFileExists(t, r.Path("packages", "core", "CHANGELOG.md"))
	assert.NoFileExists(t, r.Path("packages", "web", "CHANGELOG.md"))
}

// TestFilterStatusSelects: status is the release seen in advance, so it takes
// the same flags, narrows the same plan and reports the same findings — while
// still printing every package of the graph, which is its job. It keeps its
// exit-0 contract, and --strict is the one thing that changes it.
func TestFilterStatusSelects(t *testing.T) {
	r := filterRepo(t)

	res := r.StatusOK("-p", "core")
	assert.Equal(t, "● changed", harness.GraphLine(res.Events, "core").Str("message"))
	assert.Equal(t, "⊝ not selected", harness.GraphLine(res.Events, "web").Str("message"),
		"every package is still printed, with the selection visible in the graph")

	res = r.StatusOK("-p", "web")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W230", "web"))
	assert.Equal(t, "⊘ withheld until its providers release",
		harness.GraphLine(res.Events, "web").Str("message"))
	assert.Empty(t, r.TagList(), "status writes nothing, whatever it selects")

	res = r.Status("-p", "web", "--strict")
	assert.Equal(t, 1, res.Code, "--strict gates a selection before a release is attempted")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W230", "web"),
		"the refusal still explains itself")

	// A space term that brings the provider along with the consumer is a
	// selection the plan can release as it stands.
	res = r.StatusOK("-s", "libs", "--strict")
	assert.False(t, harness.HasCode(res.Events, "W230"))
	assert.Equal(t, "● changed", harness.GraphLine(res.Events, "web").Str("message"))

	assert.Equal(t, 1, r.Status("-p", "ghost").Code, "an unmatched term is an error here too")
}

// TestFilterRequireReleaseCountsOnlyWhatShips: --require-release asks whether
// this run publishes anything, so it reads the plan *after* the selection has
// narrowed it. A package the dependency order withheld has a version waiting
// and is on nobody's release list, which is exactly the case a filtered CI
// stage must not mistake for a release.
func TestFilterRequireReleaseCountsOnlyWhatShips(t *testing.T) {
	r := filterRepo(t)

	assert.Equal(t, 0, r.Status("-p", "core", "--require-release").Code,
		"the selection releases core, so the gate is open")

	res := r.Status("-p", "web", "--require-release")
	assert.Equal(t, 3, res.Code, "web waits for core, so this run would ship nothing")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W230", "web"),
		"the refusal still explains itself")
	assert.Equal(t, "⊘ withheld until its providers release",
		harness.GraphLine(res.Events, "web").Str("message"))

	assert.Equal(t, 0, r.Status("-p", "web").Code,
		"and without the flag the same selection is a warning, as before")
}

// TestFilterSelectsAVersioningGroup: --group names the packages that version
// together. It is a relationship and not a folder, so one term reaches a whole
// space, a standalone package outside every space, or both at once, and the
// spellings are the ones the other two flags take.
func TestFilterSelectsAVersioningGroup(t *testing.T) {
	r := groupRepo(t)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"-g", "shared"}, []string{"core", "web", "tool"}},
		{[]string{"--group", "SHARED"}, []string{"core", "web", "tool"}},
		{[]string{"-g", "apps"}, []string{"site"}},
		{[]string{"-g", "shared,apps"}, []string{"core", "web", "tool", "site"}},
		{[]string{"-g", "shared", "-g", "apps"}, []string{"core", "web", "tool", "site"}},
		{[]string{"-g", "*d"}, []string{"core", "web", "tool"}},
		{[]string{"-g", "*"}, []string{"core", "web", "tool", "site"}},
		{[]string{"-g", "apps", "-p", "solo"}, []string{"site", "solo"}},
		{[]string{"-g", "shared", "-s", "extras"}, []string{"core", "web", "tool", "solo"}},
		{[]string{"-g", "shared", "-p", "core"}, []string{"core", "web", "tool"}},
	} {
		reset(r, "stamp.log")
		res := r.RunScript("stamp", tc.args...)
		require.Equal(t, 0, res.Code, "args %v — stderr:\n%s", tc.args, res.Stderr)
		assert.ElementsMatch(t, tc.want, logged(r, "stamp.log"), "args: %v", tc.args)
	}

	reset(r, "stamp.log")
	r.RunScriptOK("stamp", "-g", "*")
	assert.NotContains(t, logged(r, "stamp.log"), "solo",
		"an independently versioned package belongs to no group, so no group term reaches it")
}

// TestFilterUnknownGroupTermsAreErrors: the three flags name three different
// things, so a term in the wrong one is answered with the flag that would have
// reached it rather than with an empty run.
func TestFilterUnknownGroupTermsAreErrors(t *testing.T) {
	r := groupRepo(t)

	res := r.RunScript("stamp", "-g", "ghost")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "matches no versioning group")
	assert.Contains(t, res.Stdout, "apps, shared", "the error lists the groups there are")

	res = r.RunScript("stamp", "-g", "no-such-*")
	assert.Equal(t, 1, res.Code, "a glob matching nothing is a typo too")

	res = r.RunScript("stamp", "-g", "extras")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "extras is a space",
		"a space whose packages version on their own has no group to name")

	res = r.RunScript("stamp", "-g", "core")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "core is a package")

	res = r.RunScript("stamp", "-s", "shared")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "shared is a versioning group")

	res = r.RunScript("stamp", "-p", "shared")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "shared is a versioning group")

	// A repository where every package versions on its own says so, rather
	// than listing an empty set of groups.
	plain := filterRepo(t)
	res = plain.RunScript("lint", "-g", "libs")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "this repository configures none")
}

// TestFilterGroupSelectsForEveryCommand: the group term is part of the one
// selection, so the commands that read a plan and the command that reads
// manifests all narrow by it identically.
func TestFilterGroupSelectsForEveryCommand(t *testing.T) {
	r := groupRepo(t)

	res := r.Command("preview", "-g", "apps")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "## site@0.1.0")
	assert.NotContains(t, res.Stdout, "## core@", "only the group's packages")

	res = r.StatusOK("-g", "shared")
	assert.Equal(t, "● changed", harness.GraphLine(res.Events, "core").Str("message"))
	assert.Equal(t, "⊝ not selected", harness.GraphLine(res.Events, "solo").Str("message"))

	res = r.Command("changelog", "-g", "apps")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.FileExists(t, r.Path("apps", "site", "CHANGELOG.md"))
	assert.NoFileExists(t, r.Path("packages", "core", "CHANGELOG.md"), "the other group stays untouched")

	res = r.Command("commit", "-g", "apps", "--tag")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, []string{"site@0.1.0"}, r.TagList())

	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web", "version": "0.0.0",
		"dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("chore(core,web): manifests")
	res = r.Command("compute", "-g", "shared")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "web -> core")
}

// TestFilterReleaseByGroupNeverSplitsIt: a group term takes every member of
// the group, which is exactly the selection a shared version can be released
// under — so it releases clean where naming one member warns (W231), and it
// passes --strict for the same reason.
func TestFilterReleaseByGroupNeverSplitsIt(t *testing.T) {
	r := groupRepo(t)

	res := r.Release("-p", "core", "--strict")
	require.Equal(t, 1, res.Code, "naming one member splits the group\nstdout:\n%s", res.Stdout)
	assert.True(t, harness.HasCode(res.Events, "W231"))
	assert.Empty(t, r.TagList())

	res = r.ReleaseOK("-g", "shared", "--strict")
	assert.False(t, harness.HasCode(res.Events, "W231"), "the whole group goes out at once")
	assert.ElementsMatch(t, []string{"core@0.1.0", "web@0.1.0", "tool@0.1.0"}, r.TagList(),
		"the group's members share one version, across the space and the standalone package")
	assert.Equal(t, "⊝ not selected", harness.GraphLine(res.Events, "solo").Str("message"))

	// The rest of the monorepo is still waiting, and the next run finishes it.
	r.ReleaseOK()
	assert.ElementsMatch(t,
		[]string{"core@0.1.0", "web@0.1.0", "tool@0.1.0", "site@0.1.0", "solo@0.1.0"}, r.TagList())
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
