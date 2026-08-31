package integration

// Goal 21, continued: `dispat for`, the third shell helper. It runs one script
// per item of a list, which is the construct a script copied from a POSIX shell
// loses the moment `shell` names something else. Assertions go on the scripts'
// own stdout and on the files they write, like the rest of the shell-helper
// suite: output passes straight through rather than being folded into the log,
// which is what makes the command usable in the middle of a pipeline.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// forConfig is the workspace the domain scenarios share: two spaces, a package
// in a declared cross-space group, a package in no group at all, and a space
// that versions as one so the implicit group is covered too.
func forConfig() models.File {
	cfg := harness.BaseFile(2)
	cfg.VersionGroups = map[string]models.VersionGroupConfig{
		"gang": {Versioning: models.VersioningFixed},
	}
	cfg.Packages = map[string]models.PackageConfig{"core": {VersionGroup: "gang"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs":  {Path: models.PathList{"packages"}},
		"tools": {Path: models.PathList{"tools"}, Versioning: models.VersioningFixed},
	}
	return cfg
}

// forRepo seeds that workspace: core (in the declared group), web (in none) and
// cli (in its space's implicit one).
func forRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(forConfig())
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.SeedPackage("tools", "cli")
	r.Commit("chore: scaffolding")
	return r
}

// forLines runs one loop whose script appends the given text per iteration and
// returns the lines it wrote. The file is named by a *relative* path, so the
// helper doubles as the standing proof that every iteration runs in the
// invocation folder: one relative path means one file however long the list.
func forLines(t *testing.T, r *harness.Repo, text string, args ...string) []string {
	t.Helper()
	_ = os.Remove(r.Path("items.txt"))
	res := r.Command(append(append([]string{"for"}, args...),
		"--do", "echo "+text+" >> items.txt")...)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	data, err := os.ReadFile(r.Path("items.txt"))
	if err != nil {
		return nil // the loop ran zero times, which is a legal outcome
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestForIteratesALiteralList(t *testing.T) {
	// The whole point of the command in one scenario: a list of words, one
	// script per word, in the order they were typed. No config file exists
	// anywhere in this repository, which is the sharpest witness available —
	// anything that works here provably read none.
	r := harness.New(t)
	require.NoFileExists(t, r.Path("dispat.json"))

	res := r.Command("for", "alpha", "beta", "gamma",
		"--do", `echo "$DISPAT_INDEX/$DISPAT_TOTAL $DISPAT_ITEM"`)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assertOrderedIn(t, res.Stdout, "0/3 alpha", "1/3 beta", "2/3 gamma")

	// An item is one argument, whatever is inside it: the shell that typed the
	// command line already decided where the words end, and the loop must not
	// split them again.
	res = r.Command("for", "one two", "a'b", `c"d`, "--do", `echo "[$DISPAT_ITEM]"`)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assertOrderedIn(t, res.Stdout, "[one two]", "[a'b]", `[c"d]`)
}

func TestForPropagatesTheExitCode(t *testing.T) {
	// The helper is transparent: a pipeline gating on a specific code still
	// sees the code an item's script chose. The default stops at the first
	// failure; --keep-going finishes the list and still reports that first
	// code, because a later item succeeding says nothing about the one that
	// did not.
	r := harness.New(t)
	fail := `echo ran-$DISPAT_ITEM; [ "$DISPAT_ITEM" != b ] || exit 7; [ "$DISPAT_ITEM" != c ] || exit 9`

	res := r.Command("for", "a", "b", "c", "--do", fail)
	assert.Equal(t, 7, res.Code, "the failing item's own code becomes the command's")
	assert.Contains(t, res.Stdout, "ran-b")
	assert.NotContains(t, res.Stdout, "ran-c", "the items after the failure never start")

	res = r.Command("for", "a", "b", "c", "--do", fail, "--keep-going")
	assert.Equal(t, 7, res.Code, "the first failure still decides, though c failed worse")
	assert.Contains(t, res.Stdout, "ran-c", "every item runs")

	res = r.Command("for", "a", "b", "--do", fail, "--keep-going",
		"--on-failure", "echo cleaning up; exit 3")
	assert.Equal(t, 3, res.Code, "the failure script's code replaces the loop's")
	assert.Equal(t, 1, strings.Count(res.Stdout, "cleaning up"),
		"one cleanup for the loop, not one per failing item")

	res = r.Command("for", "a", "b", "--do", "echo fine", "--on-failure", "echo should-not-run")
	assert.Equal(t, 0, res.Code)
	assert.NotContains(t, res.Stdout, "should-not-run", "nothing failed, so nothing reacts to a failure")
}

func TestForRunsEveryDoScriptPerItem(t *testing.T) {
	// --do is repeatable, and the scripts of one item are that item's sequence:
	// they run in order and stop at the first one that fails, the same
	// fail-fast a release stage's sequence gets.
	r := harness.New(t)

	res := r.Command("for", "a", "b",
		"--do", "echo one-$DISPAT_ITEM", "--do", "echo two-$DISPAT_ITEM")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assertOrderedIn(t, res.Stdout, "one-a", "two-a", "one-b", "two-b")

	res = r.Command("for", "a", "b",
		"--do", "echo one-$DISPAT_ITEM; exit 4", "--do", "echo two-$DISPAT_ITEM")
	assert.Equal(t, 4, res.Code)
	assert.NotContains(t, res.Stdout, "two-a", "the item stopped inside its own sequence")
	assert.NotContains(t, res.Stdout, "one-b", "and the failing item stopped the loop")
}

func TestForOverNothingSucceedsUnlessItemsAreRequired(t *testing.T) {
	// Shell fidelity: `for x in $EMPTY` runs the body zero times and succeeds,
	// so an empty list is an outcome rather than a mistake. --require-items is
	// how a CI stage says the list mattering was the point, and it speaks for
	// every source rather than only the literal one.
	r := forRepo(t)

	res := r.Command("for", "--do", "echo should-not-run")
	assert.Equal(t, 0, res.Code, "an empty list is a loop that ran zero times")
	assert.NotContains(t, res.Stdout, "should-not-run")

	res = r.Command("for", "--do", "echo should-not-run", "--require-items")
	assert.Equal(t, 1, res.Code)
	assert.NotContains(t, res.Stdout, "should-not-run")
	assert.Contains(t, res.Stdout+res.Stderr, "--require-items")

	// The same rule over a domain whose window is legitimately empty: nothing
	// is pending in this repository, so --changed selects no package.
	assert.Equal(t, 0, r.Command("for", "--changed", "--do", "echo should-not-run").Code)
	assert.Equal(t, 1, r.Command("for", "--changed", "--do", "echo x", "--require-items").Code)

	// ...and a non-empty iteration is unaffected by the flag.
	res = r.Command("for", "-p", "core", "--do", "echo ran-$DISPAT_ITEM", "--require-items")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "ran-core")
}

func TestForIteratesPackages(t *testing.T) {
	// -p iterates over the packages the terms name, in discovery order, and
	// describes each one with the release environment's own variable names, so
	// a script written for a stage runs unchanged inside a loop.
	r := forRepo(t)

	assert.Equal(t, []string{"core", "web", "cli"}, forLines(t, r, "$DISPAT_ITEM", "-p", "*"),
		"a glob reaches every package, in discovery order")
	assert.Equal(t, []string{"core", "web"}, forLines(t, r, "$DISPAT_ITEM", "-p", "core,web"))

	res := r.Command("for", "-p", "*",
		"--do", `echo "$DISPAT_ITEM|$DISPAT_PACKAGE|$DISPAT_SPACE|${DISPAT_GROUP-unset}"`)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "core|core|libs|gang", "a declared group is named")
	assert.Contains(t, res.Stdout, "web|web|libs|unset",
		"an independent package is in no group, and the variable is unset rather than empty")
	assert.Contains(t, res.Stdout, "cli|cli|tools|tools",
		"a space that versions as a group lends its members its own name")

	// A term matching nothing is an error rather than a loop that ran zero
	// times, which is how a typo would otherwise hide.
	res = r.Command("for", "-p", "ghost", "--do", "true")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "ghost")
}

func TestForRunsEveryItemWhereTheCommandWasInvoked(t *testing.T) {
	// No item is entered: every iteration runs where the invocation stands, so
	// a relative path in the script means one thing throughout the list. The
	// item's own folder is exported as an absolute path instead, for a script
	// that wants it — which is the two halves of the same decision.
	r := forRepo(t)

	res := r.Command("for", "-p", "*", "--do",
		`echo "$DISPAT_ITEM" >> here.txt; echo "$DISPAT_ITEM" > "$DISPAT_DIR/stamp.txt"`)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	here, err := os.ReadFile(r.Path("here.txt"))
	require.NoError(t, err, "the relative path resolved in the invocation folder, not in a package")
	assert.Equal(t, "core\nweb\ncli\n", string(here), "one file for the whole list")

	for path, want := range map[string]string{
		"packages/core/stamp.txt": "core\n",
		"packages/web/stamp.txt":  "web\n",
		"tools/cli/stamp.txt":     "cli\n",
	} {
		data, err := os.ReadFile(r.Path(path))
		require.NoError(t, err, "DISPAT_DIR is absolute and names the item's own folder")
		assert.Equal(t, want, string(data))
	}
}

func TestForIteratesSpacesAndGroups(t *testing.T) {
	// -s and -g iterate over the spaces and the versioning groups themselves,
	// not over the packages inside them: a loop over the three packages of a
	// space is not the job a loop over the space is.
	r := forRepo(t)

	assert.Equal(t, []string{"libs", "tools"}, forLines(t, r, "$DISPAT_ITEM", "-s", "*"))
	assert.Equal(t, []string{"gang", "tools"}, forLines(t, r, "$DISPAT_ITEM", "-g", "*"),
		"a declared group and a space that versions as one are both groups")

	res := r.Command("for", "-s", "libs", "--do",
		`echo "$DISPAT_ITEM|$DISPAT_SPACE"; echo seen > "$DISPAT_DIR/space.txt"`)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "libs|libs")
	assert.FileExists(t, r.Path("packages", "space.txt"),
		"a space's folder is its primary path, the same one --in space: places a script in")

	res = r.Command("for", "-g", "gang", "--do", `echo "$DISPAT_ITEM|$DISPAT_GROUP|${DISPAT_DIR-none}"`)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "gang|gang|none",
		"a group is a versioning relationship rather than a folder, so it has none to name")

	// The unknown-term errors are the filter's own, hints included, so a name
	// filed under the wrong flag is told which flag would have reached it.
	res = r.Command("for", "-s", "gang", "--do", "true")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "matches no configured space")
	assert.Contains(t, res.Stdout+res.Stderr, "--group")

	res = r.Command("for", "-g", "libs", "--do", "true")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "matches no versioning group")
	assert.Contains(t, res.Stdout+res.Stderr, "--space")
}

func TestForReadsTheConfigOnlyWhenTheListNeedsIt(t *testing.T) {
	// The line the command draws, stated as one comparison: a literal list in a
	// folder the command line already spelled in full reads nothing, and
	// everything that asks about the monorepo pays for the answer. A config
	// file that cannot be parsed is what makes the difference visible.
	r := forRepo(t)
	require.NoError(t, os.MkdirAll(r.Path("build"), 0o755))
	require.NoError(t, os.WriteFile(r.Path("build", "marker.txt"), []byte("in-build"), 0o644))

	res := r.Command("for", "a", "--do", "cat marker.txt", "--in", "build")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "in-build", "a path needs no configuration to resolve")

	res = r.CommandAt("build", "for", "a", "--do", "cat marker.txt", "--in", "cwd")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "in-build", "nor does cwd")

	res = r.Command("for", "a", "b", "--do", "cat main.txt", "--in", "pkg:core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, 2, strings.Count(res.Stdout, "core"),
		"--in moves every iteration, not only the first")

	// Now break the file. Only the invocations that have to look something up
	// notice, which is the whole claim stated as one comparison.
	require.NoError(t, os.WriteFile(r.Path("dispat.json"), []byte("{ not json"), 0o644))

	res = r.Command("for", "a", "b", "--do", "echo ran-anyway")
	require.Equal(t, 0, res.Code, "a literal list reads no configuration, broken or not")
	assert.Equal(t, 2, strings.Count(res.Stdout, "ran-anyway"))

	assert.Equal(t, 1, r.Command("for", "a", "--do", "true", "--in", "pkg:core").Code,
		"naming a package is what makes the broken file matter")
	assert.Equal(t, 1, r.Command("for", "-p", "*", "--do", "true").Code,
		"and so is asking which packages there are")
}

func TestForCannotBeToldWhichItemItIsOn(t *testing.T) {
	// The iterator variables are appended after the item's own and after
	// everything the process environment carries, so a script may read
	// DISPAT_ITEM knowing what it holds. Without that, a stale variable left
	// over from an enclosing run would quietly answer for the loop — which is
	// the ordinary case, since a loop's natural home is inside a release stage.
	r := forRepo(t)

	res := r.CommandEnv([]string{"DISPAT_ITEM=impostor", "DISPAT_INDEX=99", "DISPAT_TOTAL=99"},
		"for", "a", "b", "--do", `echo "$DISPAT_ITEM/$DISPAT_INDEX/$DISPAT_TOTAL"`)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assertOrderedIn(t, res.Stdout, "a/0/2", "b/1/2")
	assert.NotContains(t, res.Stdout, "impostor")

	res = r.CommandEnv([]string{"DISPAT_PACKAGE=impostor"},
		"for", "-p", "core", "--do", `echo "pkg=$DISPAT_PACKAGE"`)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "pkg=core")
}

func TestForUsesTheConfiguredShell(t *testing.T) {
	// The reason the command exists: a loop written once runs its body through
	// the shell this repository configured, so the one construct that made a
	// script shell-specific no longer does. A bashism invalid under the default
	// /bin/sh -c is what proves the shell actually moved.
	const bash = "/bin/bash"
	if _, err := os.Stat(bash); err != nil {
		t.Skip("bash not available at /bin/bash")
	}
	r := harness.New(t)
	cfg := forConfig()
	cfg.Shell = []string{bash, "-c"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.SeedPackage("tools", "cli")
	r.Commit("chore: scaffolding")

	res := r.Command("for", "-p", "core,web", "--do",
		"arr=(a b c); echo $DISPAT_ITEM-${arr[1]}")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assertOrderedIn(t, res.Stdout, "core-b", "web-b")
}

func TestForIsReservedAndRefusesBadFlags(t *testing.T) {
	// `for` is a command word like every other, so it never parses as `dispat
	// run for`. Every malformed invocation below is decided by the flags alone
	// and exits 2 having read no config, which is what lets the command work in
	// a folder that is not a monorepo at all.
	r := harness.New(t)

	for name, args := range map[string][]string{
		"no --do at all":            {"for", "a"},
		"no --do beside a source":   {"for", "--changed"},
		"items beside -p":           {"for", "a", "-p", "core", "--do", "true"},
		"items beside --changed":    {"for", "a", "--changed", "--do", "true"},
		"two kinds of thing":        {"for", "-p", "core", "-s", "libs", "--do", "true"},
		"two kinds, the other pair": {"for", "-s", "libs", "-g", "gang", "--do", "true"},
		"both halves of the window": {"for", "--changed", "--unchanged", "--do", "true"},
		"consumers with no window":  {"for", "-p", "core", "--consumers", "--do", "true"},
		"a malformed --in":          {"for", "a", "--do", "true", "--in", "pkg:"},
		"arguments after a dash":    {"for", "a", "--do", "true", "--", "x"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, 2, r.Command(args...).Code)
		})
	}
}

func TestForShadowsARunScriptCalledFor(t *testing.T) {
	// The cost of the word, pinned deliberately: `for` is now a command, so a
	// script by that name is no longer reachable by the bare shorthand. The
	// two-word spelling still reaches it, which is the whole of the workaround.
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"for": {"echo ran-the-script"}}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: models.PathList{"packages"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")

	res := r.Command("for", "a", "--do", "echo ran-the-loop")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "ran-the-loop")
	assert.NotContains(t, res.Stdout, "ran-the-script", "the command word wins")

	res = r.Command("run", "for")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "ran-the-script", "the two-word spelling still reaches it")
}
