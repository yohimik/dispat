package integration

// Goal 21, continued: the --changed condition of `dispat if`. The gate selects
// packages with the same window, filter and consumer expansion every sweeping
// command uses, and holds when the result is non-empty. Assertions go on the
// scripts' own stdout, like the rest of the if suite: the command stays a
// transparent pipeline guard whatever its condition asks about.

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// changedGate runs one gate invocation and reports which branch answered:
// gate-go, gate-idle, or the exit code of a run that refused.
func changedGate(r *harness.Repo, flags ...string) string {
	args := append([]string{"if", "--changed"}, flags...)
	args = append(args, "--then", "echo gate-go", "--else", "echo gate-idle")
	res := r.Command(args...)
	if res.Code != 0 {
		return "exit " + strconv.Itoa(res.Code)
	}
	return gateAnswer(res.Stdout)
}

// gateAnswer picks the gate's word out of the run's log lines.
func gateAnswer(stdout string) string {
	for _, word := range []string{"gate-go", "gate-idle"} {
		if strings.Contains(stdout, word) {
			return word
		}
	}
	return ""
}

func TestIfChangedGatesOnTheReleaseWindow(t *testing.T) {
	// Without --since, the window is the release window: the gate holds while
	// something is pending and stops holding once it is released, so the same
	// invocation converges with the repository's state.
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): begin")

	assert.Equal(t, "gate-go", changedGate(r), "a pending release is a changed selection")

	r.ReleaseOK()
	assert.Equal(t, "gate-idle", changedGate(r), "after the release the window is empty")

	r.WriteFile("packages/core/more.txt", "more")
	r.Commit("feat(core): more")
	assert.Equal(t, "gate-go", changedGate(r), "a new commit fills the window again")
}

func TestIfChangedWithSince(t *testing.T) {
	// --since replaces the release window with what the commits since the
	// revision address, and 'all' switches the window off entirely. A revision
	// git cannot resolve is a failure with the revision named, never a silent
	// false: a gate that answered "no" to a typo would never fire again.
	r := singlePackageRepo(t, echoBuild)
	r.Commit("chore: scaffolding")

	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core): add a feature")
	assert.Equal(t, "gate-go", changedGate(r, "--since", "HEAD~1"),
		"the last commit addressed core")

	r.WriteFile("README.md", "notes")
	r.Commit("docs: update the readme")
	assert.Equal(t, "gate-idle", changedGate(r, "--since", "HEAD~1"),
		"the last commit addressed no package")
	assert.Equal(t, "gate-go", changedGate(r, "--since", "HEAD~2"),
		"two commits back, the feature is inside the window")
	assert.Equal(t, "gate-go", changedGate(r, "--since", "all"),
		"'all' selects every package, changed or not")

	res := r.Command("if", "--changed", "--since", "nonsense-rev", "--then", "true")
	assert.Equal(t, 1, res.Code, "a revision git cannot resolve is a failure, not a false")
	assert.Contains(t, res.Stdout+res.Stderr, "nonsense-rev", "the failure names the revision")
	assert.Contains(t, res.Stdout+res.Stderr, "cannot evaluate --changed")
}

func TestIfChangedNarrowsToTheSelection(t *testing.T) {
	// The selection flags mean what they mean everywhere: -p, -s and -g narrow
	// the answer, a package outside the window is a false said out loud, and a
	// term matching nothing is an error rather than a gate that never fires.
	r := harness.New(t)
	cfg := libsConfig(echoBuild)
	cfg.VersionGroups = map[string]models.VersionGroupConfig{"gang": {Versioning: models.VersioningFixed}}
	cfg.Packages = map[string]models.PackageConfig{"core": {VersionGroup: "gang"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("chore: scaffolding")

	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core): add a feature")

	assert.Equal(t, "gate-go", changedGate(r, "--since", "HEAD~1", "-p", "core"))
	assert.Equal(t, "gate-go", changedGate(r, "--since", "HEAD~1", "-s", "libs"),
		"a space term keeps any of its packages the window holds")
	assert.Equal(t, "gate-go", changedGate(r, "--since", "HEAD~1", "-g", "gang"),
		"a group term reaches the packages that carry it")

	res := r.Command("if", "--changed", "--since", "HEAD~1", "-p", "web",
		"--then", "echo gate-go", "--else", "echo gate-idle")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "gate-idle", "web did not change")
	assert.Contains(t, res.Stdout, "outside the window",
		"an explicitly named package the window left out is said out loud")

	res = r.Command("if", "--changed", "-p", "ghost", "--then", "true")
	assert.Equal(t, 1, res.Code, "a term matching no package is an error, never a false")
	assert.Contains(t, res.Stdout+res.Stderr, "ghost")
}

func TestIfChangedConsumersReachDownstream(t *testing.T) {
	// The one place the gate composes the shared parts in its own order:
	// --consumers expands the window before the filter narrows it, so the
	// question becomes "is this selection among what the changes reach". In a
	// sweep the expansion runs after the filter, where it asks for dependents;
	// here that order could never flip the answer, so it would say nothing.
	r := linkedRepo(t, "core", "web", echoBuild)
	r.Commit("chore: scaffolding")

	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core): add a feature")

	assert.Equal(t, "gate-idle", changedGate(r, "--since", "HEAD~1", "-p", "web"),
		"web itself did not change")
	assert.Equal(t, "gate-go", changedGate(r, "--since", "HEAD~1", "-p", "web", "--consumers"),
		"web consumes core, and core changed")
}

func TestIfChangedInfersFromTheInvocationFolder(t *testing.T) {
	// Invoked inside a package folder with no terms, the gate narrows to that
	// package, exactly as run does: the same invocation means "did anything
	// change" at the root and "did this package change" inside one.
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("chore: scaffolding")
	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core): add a feature")

	gateAt := func(rel string) string {
		res := r.CommandAt(rel, "if", "--changed", "--since", "HEAD~1",
			"--then", "echo gate-go", "--else", "echo gate-idle")
		require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
		return gateAnswer(res.Stdout)
	}
	assert.Equal(t, "gate-go", gateAt("packages/core"))
	assert.Equal(t, "gate-idle", gateAt("packages/web"))
	assert.Equal(t, "gate-go", changedGate(r, "--since", "HEAD~1"),
		"from the root, no folder narrows the answer")
}

func TestIfChangedInRunsElsewhere(t *testing.T) {
	// --in picks where the chosen script runs; it never narrows the window.
	// The two compose: the gate answers for the repository (or the filtered
	// selection), and the branch runs in the named folder.
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("chore: scaffolding")
	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core): add a feature")

	res := r.Command("if", "--changed", "--since", "HEAD~1", "--in", "pkg:web",
		"--then", "pwd > here.txt", "--else", "echo gate-idle")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	here, err := os.ReadFile(r.Path("packages/web/here.txt"))
	require.NoError(t, err, "core changed, so the gate held even though --in names web")
	assert.Contains(t, string(here), "packages/web", "the branch ran in the folder --in named")
}

func TestIfChangedConsumersNeedsASelection(t *testing.T) {
	// From the root with no terms, everything is selected, and expanding a
	// selection of everything cannot change whether it is empty: --consumers
	// would be a silent no-op, so it is refused. From inside a package folder
	// the invocation narrows the selection and the same flags mean something,
	// so there they are answered.
	r := linkedRepo(t, "core", "web", echoBuild)
	r.Commit("chore: scaffolding")
	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core): add a feature")

	res := r.Command("if", "--changed", "--consumers", "--then", "true")
	assert.Equal(t, 2, res.Code, "an unselectable --consumers is a usage error, not a silent no-op")
	assert.Contains(t, res.Stdout+res.Stderr, "--consumers")

	res = r.CommandAt("packages/web", "if", "--changed", "--since", "HEAD~1", "--consumers",
		"--then", "echo gate-go", "--else", "echo gate-idle")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "gate-go",
		"invoked inside web, the folder is the selection and web's provider changed")
}

func TestIfChangedChainsWithElif(t *testing.T) {
	// --changed leads the chain like any other condition: true, it wins even
	// over a true --elif behind it; false, the elifs and the else get their
	// ordinary turn.
	r := singlePackageRepo(t, echoBuild)
	r.Commit("chore: scaffolding")
	r.WriteFile("README.md", "notes")
	r.Commit("docs: update the readme")

	res := r.CommandEnv([]string{"FALLBACK=1"},
		"if", "--changed", "--since", "HEAD~1", "--then", "echo took-changed",
		"--elif", "FALLBACK", "--then", "echo took-fallback")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "took-fallback", "nothing changed, so the elif answers")
	assert.NotContains(t, res.Stdout, "took-changed")

	r.WriteFile("packages/core/feature.txt", "f")
	r.Commit("feat(core): add a feature")
	res = r.CommandEnv([]string{"FALLBACK=1"},
		"if", "--changed", "--since", "HEAD~1", "--then", "echo took-changed",
		"--elif", "FALLBACK", "--then", "echo took-fallback")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "took-changed", "the leading condition wins first")
	assert.NotContains(t, res.Stdout, "took-fallback")
}

func TestIfChangedReadsTheConfigOnlyWhenAsked(t *testing.T) {
	// --changed is the one condition that costs a config file and a git
	// repository, and only it pays: with the file broken or the repository
	// gone, a bare condition still runs where --changed refuses.
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): begin")
	assert.Equal(t, "gate-go", changedGate(r), "a healthy repository answers")

	good, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(r.Path("dispat.json"), []byte("{ not json"), 0o644))

	res := r.Command("if", "!ABSENT", "--then", "echo ran-anyway")
	require.Equal(t, 0, res.Code, "a bare if reads no configuration, broken or not")
	assert.Contains(t, res.Stdout, "ran-anyway")

	res = r.Command("if", "--changed", "--then", "true")
	assert.Equal(t, 1, res.Code, "asking about the repository is what makes the broken file matter")

	// Now the reverse: a healthy file in a folder that is no repository.
	require.NoError(t, os.WriteFile(r.Path("dispat.json"), good, 0o644))
	require.NoError(t, os.RemoveAll(r.Path(".git")))

	res = r.Command("if", "!ABSENT", "--then", "echo ran-anyway")
	require.Equal(t, 0, res.Code, "a bare if needs no git repository either")

	res = r.Command("if", "--changed", "--then", "true")
	assert.Equal(t, 1, res.Code, "--changed needs the repository it asks about")
}

func TestIfChangedPropagatesTheExitCode(t *testing.T) {
	// The helper stays transparent whatever the condition asked: the chosen
	// script's code is the command's, and a false answer with no else runs
	// nothing and succeeds.
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): begin")

	res := r.Command("if", "--changed", "--then", "exit 7")
	assert.Equal(t, 7, res.Code, "the script's own code becomes the command's")

	res = r.Command("if", "--changed", "--since", "all", "-p", "core", "--then", "echo held",
		"--on-failure", "echo should-not-run")
	assert.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "held")
	assert.NotContains(t, res.Stdout, "should-not-run")

	r.ReleaseOK()
	res = r.Command("if", "--changed", "--then", "exit 7")
	assert.Equal(t, 0, res.Code, "false with no else runs nothing and succeeds")
	assert.NotContains(t, res.Stdout, "exit", "nothing ran")
}
