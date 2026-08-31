package integration

// Goal 21, continued: the window sources of `dispat for`. --changed iterates
// over the packages every sweeping command would cover, --unchanged over the
// ones it leaves out, and a bare --since is the first of the two spelled the
// way `dispat run --since` spells it. The pair's load-bearing claim is that
// they partition the repository: every package is in exactly one of them,
// whatever the narrowing flags say.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// forWindow runs one window loop and returns the packages it iterated over, in
// the order it reached them.
func forWindow(t *testing.T, r *harness.Repo, flags ...string) []string {
	t.Helper()
	return forLines(t, r, "$DISPAT_ITEM", flags...)
}

func TestForChangedIteratesTheReleaseWindow(t *testing.T) {
	// Without --since the window is the release window: the loop covers what a
	// release would, stops covering it once the release has happened, and picks
	// it up again on the next commit — the same convergence `if --changed`
	// gates on, here as a list rather than as a yes or no.
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("chore: scaffolding")

	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core): add a feature")
	assert.Equal(t, []string{"core"}, forWindow(t, r, "--changed"),
		"exactly the window, not every package")
	assert.Equal(t, []string{"web"}, forWindow(t, r, "--unchanged"),
		"and exactly its complement")

	r.ReleaseOK()
	assert.Empty(t, forWindow(t, r, "--changed"),
		"after the release the window is empty, and an empty loop runs nothing")
	assert.Equal(t, []string{"core", "web"}, forWindow(t, r, "--unchanged"),
		"which puts every package in the other half")
}

func TestForChangedWithSince(t *testing.T) {
	// --since replaces the release window with what the commits since the
	// revision address, and 'all' switches the window off entirely. A bare
	// --since is --changed --since, which is how `dispat run` already spells
	// it; a revision git cannot resolve is a failure naming it rather than an
	// empty list, because a loop that silently ran zero times is how a typo
	// hides.
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("chore: scaffolding")

	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core): add a feature")

	assert.Equal(t, []string{"core"}, forWindow(t, r, "--since", "HEAD~1"),
		"a bare --since is the changed window, spelled as run spells it")
	assert.Equal(t, []string{"core"}, forWindow(t, r, "--changed", "--since", "HEAD~1"),
		"and the two spellings are one source, not two")
	assert.Equal(t, []string{"web"}, forWindow(t, r, "--unchanged", "--since", "HEAD~1"),
		"--since moves the complement with it")
	assert.Equal(t, []string{"core", "web"}, forWindow(t, r, "--since", "all"),
		"'all' selects every package, changed or not")
	assert.Empty(t, forWindow(t, r, "--unchanged", "--since", "all"),
		"which leaves the complement empty")

	res := r.Command("for", "--since", "nonsense-rev", "--do", "true")
	assert.Equal(t, 1, res.Code, "a revision git cannot resolve is a failure, not an empty list")
	assert.Contains(t, res.Stdout+res.Stderr, "nonsense-rev", "the failure names the revision")
}

func TestForChangedNarrowsToTheSelection(t *testing.T) {
	// Under a window source, -p, -s and -g stop being the list and become the
	// narrowing they are everywhere else. That overload is the one thing about
	// the grammar worth pinning: the same three flags mean two things, and
	// which one they mean is decided by whether a window flag is present.
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.VersionGroups = map[string]models.VersionGroupConfig{"gang": {Versioning: models.VersioningFixed}}
	cfg.Packages = map[string]models.PackageConfig{"core": {VersionGroup: "gang"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("chore: scaffolding")

	r.WriteFile("packages/core/feature.txt", "f")
	r.WriteFile("packages/web/feature.txt", "f")
	r.Commit("feat(core,web): a feature each")

	assert.Equal(t, []string{"core", "web"}, forWindow(t, r, "--changed"))
	assert.Equal(t, []string{"core"}, forWindow(t, r, "--changed", "-p", "core"),
		"a package term narrows the window rather than naming the list")
	assert.Equal(t, []string{"core", "web"}, forWindow(t, r, "--changed", "-s", "libs"))
	assert.Equal(t, []string{"core"}, forWindow(t, r, "--changed", "-g", "gang"))

	// The same three flags, with no window flag beside them, are the source
	// again — and there naming two of them is refused rather than composed.
	assert.Equal(t, []string{"libs"}, forWindow(t, r, "-s", "libs"),
		"without a window, -s iterates over the space itself")
	assert.Equal(t, 2, r.Command("for", "-p", "core", "-s", "libs", "--do", "true").Code)

	// A term matching no package is an error on either half.
	assert.Equal(t, 1, r.Command("for", "--changed", "-p", "ghost", "--do", "true").Code)
	assert.Equal(t, 1, r.Command("for", "--unchanged", "-p", "ghost", "--do", "true").Code)
}

func TestForChangedConsumersReachDownstream(t *testing.T) {
	// --consumers expands the window downstream, which changes the list a loop
	// visits — so unlike `dispat if --changed`, where expanding a selection of
	// everything could never flip the answer, there is nothing to refuse here.
	// The complement shrinks by exactly what the window gained.
	r := linkedRepo(t, "core", "web", echoBuild)
	r.Commit("chore: scaffolding")
	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core): add a feature")

	assert.Equal(t, []string{"core"}, forWindow(t, r, "--since", "HEAD~1"))
	assert.Equal(t, []string{"core", "web"}, forWindow(t, r, "--since", "HEAD~1", "--consumers"),
		"web consumes core, and core changed")
	assert.Equal(t, []string{"web"}, forWindow(t, r, "--unchanged", "--since", "HEAD~1"))
	assert.Empty(t, forWindow(t, r, "--unchanged", "--since", "HEAD~1", "--consumers"),
		"the same expansion moves web out of the complement")

	// From the root with no terms, which is exactly the shape `dispat if`
	// refuses: for a loop it is an ordinary and useful invocation.
	res := r.Command("for", "--changed", "--consumers", "--do", "echo ran-$DISPAT_ITEM")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "ran-core")
}

func TestForChangedIteratesInDependencyOrder(t *testing.T) {
	// The window comes out in the plan's dependency order, which is the order
	// every sweep schedules in: a provider is visited before the consumer that
	// depends on it, so a loop doing per-package work in sequence sees the same
	// order a release would.
	r := linkedRepo(t, "core", "web", echoBuild)
	r.Commit("chore: scaffolding")
	r.WriteFile("packages/web/feature.txt", "f")
	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core,web): a feature each")

	assert.Equal(t, []string{"core", "web"}, forWindow(t, r, "--changed"),
		"the provider comes first, whatever order the folders or the commit named")
}

func TestForChangedInfersFromTheInvocationFolder(t *testing.T) {
	// Invoked inside a package folder with no terms, the window narrows to that
	// package exactly as it does for every other command, so the same
	// invocation means "every changed package" at the root and "this one, if it
	// changed" inside one.
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("chore: scaffolding")
	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core): add a feature")

	at := func(rel string, flags ...string) string {
		args := append(append([]string{"for"}, flags...), "--do", "echo ran-$DISPAT_ITEM")
		res := r.CommandAt(rel, args...)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		return res.Stdout
	}
	assert.Contains(t, at("packages/core", "--changed"), "ran-core")
	assert.NotContains(t, at("packages/web", "--changed"), "ran-",
		"web did not change, so the loop inside it runs nothing")
	assert.Contains(t, at("packages/web", "--unchanged"), "ran-web",
		"and the complement is where it is instead")
}

func TestForChangedNeedsTheRepositoryItAsksAbout(t *testing.T) {
	// The window sources are the ones that cost a config file and a git
	// repository, and only they pay: with the file broken or the repository
	// gone, a literal list still runs in the same folder where --changed
	// refuses. That comparison is the whole of the command's cost rule.
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): begin")
	assert.Equal(t, []string{"core"}, forWindow(t, r, "--changed"), "a healthy repository answers")

	good, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(r.Path("dispat.json"), []byte("{ not json"), 0o644))

	res := r.Command("for", "a", "b", "--do", "echo ran-anyway")
	require.Equal(t, 0, res.Code, "a literal list reads no configuration, broken or not")
	assert.Equal(t, 2, strings.Count(res.Stdout, "ran-anyway"))

	assert.Equal(t, 1, r.Command("for", "--changed", "--do", "true").Code,
		"asking about the repository is what makes the broken file matter")
	assert.Equal(t, 1, r.Command("for", "--unchanged", "--do", "true").Code)

	// Now the reverse: a healthy file in a folder that is no repository.
	require.NoError(t, os.WriteFile(r.Path("dispat.json"), good, 0o644))
	require.NoError(t, os.RemoveAll(r.Path(".git")))

	res = r.Command("for", "a", "--do", "echo ran-anyway")
	require.Equal(t, 0, res.Code, "a literal list needs no git repository either")
	assert.Equal(t, 1, r.Command("for", "--changed", "--do", "true").Code,
		"--changed needs the repository it asks about")
}

func TestForChangedPropagatesTheExitCode(t *testing.T) {
	// The helper stays transparent whatever the list came from: a failing item
	// ends the loop with its own code, and --keep-going still finishes the
	// window while reporting that first failure.
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("feat(core,web): begin")

	fail := `echo ran-$DISPAT_ITEM; [ "$DISPAT_ITEM" != core ] || exit 7`

	res := r.Command("for", "--changed", "--do", fail)
	assert.Equal(t, 7, res.Code)
	assert.NotContains(t, res.Stdout, "ran-web", "the loop stopped at core")

	res = r.Command("for", "--changed", "--do", fail, "--keep-going")
	assert.Equal(t, 7, res.Code)
	assert.Contains(t, res.Stdout, "ran-web", "--keep-going finishes the window")
}
