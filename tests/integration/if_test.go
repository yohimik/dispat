package integration_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// `dispat if` against the real binary: the condition grammar decides which
// shell string reaches /bin/sh, and the chosen script's exit status becomes
// the process's. Assertions go on the script's own stdout, because output
// passes straight through rather than being folded into the log, which is the
// property that makes the command usable in the middle of a pipeline.

func TestIfChoosesABranchFromTheEnvironment(t *testing.T) {
	// The whole point of the command in one scenario: the same invocation,
	// three environments, three answers. No config file and no packages are
	// involved, because a condition is about the environment rather than about
	// the repository the command is standing in.
	r := harness.New(t)

	chain := []string{
		"if", "ENV=prod", "--then", "echo took-prod",
		"--elif", "ENV=stage", "--then", "echo took-stage",
		"--else", "echo took-none",
	}

	res := r.CommandEnv([]string{"ENV=prod"}, chain...)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "took-prod")
	assert.NotContains(t, res.Stdout, "took-stage", "only the first matching branch runs")

	res = r.CommandEnv([]string{"ENV=stage"}, chain...)
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "took-stage")

	res = r.CommandEnv([]string{"ENV=other"}, chain...)
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "took-none", "no condition held, so the else is the default case")

	// And with the variable absent entirely, which is not the same input as a
	// value that simply does not match, but is the same answer.
	res = r.Command(chain...)
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "took-none")
}

func TestIfConditionGrammarEndToEnd(t *testing.T) {
	// Every spelling, through the process boundary, so the shell quoting a
	// user actually types is covered rather than only the parser.
	r := harness.New(t)
	env := []string{"CI=1", "EMPTY=", "ENV=prod", "BRANCH=release/1.2"}

	for name, tc := range map[string]struct {
		cond string
		want string
	}{
		"set":                {"CI", "yes"},
		"set but empty":      {"EMPTY", "no"},
		"absent":             {"NOPE", "no"},
		"negated and absent": {"!NOPE", "yes"},
		"negated and set":    {"!CI", "no"},
		"equal":              {"ENV=prod", "yes"},
		"not equal":          {"ENV=dev", "no"},
		"empty value":        {"EMPTY=", "yes"},
		"bang equal":         {"ENV!=dev", "yes"},
		"glob":               {"BRANCH~release/*", "yes"},
		"glob miss":          {"BRANCH~main*", "no"},
		"negated glob":       {"BRANCH!~main*", "yes"},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.CommandEnv(env, "if", tc.cond, "--then", "echo yes", "--else", "echo no")
			require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
			assert.Contains(t, res.Stdout, tc.want)
		})
	}
}

func TestIfPropagatesTheExitCode(t *testing.T) {
	// The helper is transparent: a pipeline gating on a specific code still
	// sees the code the script chose. --on-failure is the way to override it,
	// and it runs only when something actually failed.
	r := harness.New(t)

	res := r.CommandEnv([]string{"GO=1"}, "if", "GO", "--then", "exit 7")
	assert.Equal(t, 7, res.Code, "the script's own code becomes the command's")

	res = r.CommandEnv([]string{"GO=1"}, "if", "GO", "--then", "exit 7",
		"--on-failure", "echo cleaning up; exit 3")
	assert.Equal(t, 3, res.Code, "the failure script's code replaces the failed script's")
	assert.Contains(t, res.Stdout, "cleaning up")

	res = r.CommandEnv([]string{"GO=1"}, "if", "GO", "--then", "echo fine",
		"--on-failure", "echo should-not-run")
	assert.Equal(t, 0, res.Code)
	assert.NotContains(t, res.Stdout, "should-not-run", "nothing failed, so nothing reacts to a failure")

	// Nothing matched and no else: a guard that finds nothing to do has
	// succeeded, and it must run nothing at all.
	res = r.Command("if", "ABSENT", "--then", "exit 7")
	assert.Equal(t, 0, res.Code)
	assert.Empty(t, res.Stdout)
}

func TestIfRunsInTheInvocationFolder(t *testing.T) {
	// The chosen script runs where the command was invoked, so a relative path
	// in it means what the caller meant.
	r := harness.New(t)
	r.SeedPackage("packages", "core")

	// The condition needs no environment of its own here, so the invocation
	// folder is the only variable: CommandAt is what points --root inside.
	res := r.CommandAt("packages/core", "if", "!ABSENT", "--then", "cat main.txt")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "core", "the script read a file relative to --root")
}

// TestIfRunsWhereItIsTold: --in moves the chosen script, in the vocabulary
// exec uses for the same question. The half a command line settles on its own,
// a path and cwd, is resolved without reading anything, which is what keeps the
// command's promise that a condition costs no configuration.
func TestIfRunsWhereItIsTold(t *testing.T) {
	r := harness.New(t)
	require.NoError(t, os.MkdirAll(r.Path("build"), 0o755))
	require.NoError(t, os.WriteFile(r.Path("build", "marker.txt"), []byte("in-build"), 0o644))

	// No config file exists anywhere in this repository, which is the sharpest
	// witness available: anything that works here provably read none.
	require.NoFileExists(t, r.Path("dispat.json"))

	res := r.Command("if", "!ABSENT", "--then", "cat marker.txt", "--in", "build")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "in-build", "a path needs no configuration to resolve")

	res = r.CommandAt("build", "if", "!ABSENT", "--then", "cat marker.txt", "--in", "cwd")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "in-build", "nor does cwd")

	// A folder that is not there is refused by name rather than by whatever the
	// shell would have said about a directory it could not enter.
	res = r.Command("if", "!ABSENT", "--then", "true", "--in", "nowhere")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "nowhere")

	// A malformed value is decided by the flags alone, so it is the usage exit.
	assert.Equal(t, 2, r.Command("if", "!ABSENT", "--then", "true", "--in", "pkg:").Code)
}

func TestIfReadsTheConfigOnlyForAnInWithANameInIt(t *testing.T) {
	// The line the feature draws: asking where a package is costs finding out,
	// and asking anything else still costs nothing. A config file that cannot
	// be parsed is what makes the difference visible, since every command that
	// reads one fails on it and every command that does not is unaffected.
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	cfg := harness.BaseFile(2)
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: models.PathList{"packages"}}}
	r.WriteConfigModel(cfg)

	res := r.Command("if", "!ABSENT", "--then", "pwd", "--in", "pkg:core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assertSameFolder(t, r.Path("packages", "core"), res.Stdout)

	res = r.Command("if", "!ABSENT", "--then", "pwd", "--in", "space:libs")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assertSameFolder(t, r.Path("packages"), res.Stdout)

	res = r.CommandAt("packages/core", "if", "!ABSENT", "--then", "pwd", "--in", "root")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assertSameFolder(t, r.Root, res.Stdout)

	res = r.Command("if", "!ABSENT", "--then", "true", "--in", "pkg:ghost")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "ghost")

	// Now break the file. Only the invocations that have to look a name up
	// notice, which is the whole claim stated as one comparison.
	require.NoError(t, os.WriteFile(r.Path("dispat.json"), []byte("{ not json"), 0o644))

	res = r.Command("if", "!ABSENT", "--then", "echo ran-anyway")
	require.Equal(t, 0, res.Code, "a bare if reads no configuration, broken or not")
	assert.Contains(t, res.Stdout, "ran-anyway")

	res = r.Command("if", "!ABSENT", "--then", "echo ran-anyway", "--in", "build")
	assert.NotEqual(t, 0, res.Code, "the folder is missing, but the config was still never read")
	assert.NotContains(t, res.Stdout+res.Stderr, "configuration",
		"a path is resolved without one")

	res = r.Command("if", "!ABSENT", "--then", "true", "--in", "pkg:core")
	assert.Equal(t, 1, res.Code, "naming a package is what makes the broken file matter")
}

// TestIfFileConditions: -f and -d ask the filesystem, so like an environment
// condition they cost no config file and no git repository. A path that is
// absent or the wrong kind is false rather than an error, matching the shell's
// [ -f ] and [ -d ]: the question was "is it there", and it is not.
func TestIfFileConditions(t *testing.T) {
	r := harness.New(t)
	require.NoError(t, os.MkdirAll(r.Path("build"), 0o755))
	require.NoError(t, os.WriteFile(r.Path("build", "report.json"), []byte("{}"), 0o644))

	// No config file exists anywhere in this repository, which is the sharpest
	// witness available: anything that works here provably read none.
	require.NoFileExists(t, r.Path("dispat.json"))

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"-f on a file":        {[]string{"-f", "build/report.json"}, "yes"},
		"-f on a folder":      {[]string{"-f", "build"}, "no"},
		"-f on nothing":       {[]string{"-f", "ghost.json"}, "no"},
		"-d on a folder":      {[]string{"-d", "build"}, "yes"},
		"-d on a file":        {[]string{"-d", "build/report.json"}, "no"},
		"-d on nothing":       {[]string{"-d", "ghost"}, "no"},
		"--file, spelled out": {[]string{"--file", "build/report.json"}, "yes"},
		"--dir, spelled out":  {[]string{"--dir", "build"}, "yes"},
	} {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"if"}, tc.args...)
			res := r.Command(append(args, "--then", "echo yes", "--else", "echo no")...)
			require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
			assert.Contains(t, res.Stdout, tc.want)
		})
	}

	// Transparent like every other condition: the chosen script's code is the
	// command's.
	res := r.Command("if", "-f", "build/report.json", "--then", "exit 7")
	assert.Equal(t, 7, res.Code)
}

// TestIfFileConditionResolvesWhereTheScriptRuns: a relative -f path resolves
// against the folder the chosen script runs in — after --in — so the test and
// a path inside the script text mean the same file. An absolute path is used
// as it is, from anywhere.
func TestIfFileConditionResolvesWhereTheScriptRuns(t *testing.T) {
	r := harness.New(t)
	require.NoError(t, os.MkdirAll(r.Path("build"), 0o755))
	require.NoError(t, os.WriteFile(r.Path("build", "marker.txt"), []byte("m"), 0o644))

	res := r.Command("if", "-f", "marker.txt", "--then", "echo yes", "--else", "echo no")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "no", "the marker is not in the invocation folder")

	res = r.Command("if", "-f", "marker.txt", "--then", "cat marker.txt", "--else", "echo no",
		"--in", "build")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "m", "--in moves the test along with the script")

	res = r.Command("if", "-f", r.Path("build", "marker.txt"), "--then", "echo yes", "--else", "echo no")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "yes", "an absolute path ignores the folder")

	// An --in naming a package still costs exactly what it costs alone: the
	// config read pays for the name, not for the file test.
	cfg := harness.BaseFile()
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: models.PathList{"packages"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	res = r.Command("if", "-f", "main.txt", "--then", "cat main.txt", "--else", "echo no",
		"--in", "pkg:core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "core", "the relative path resolved inside the package")
}

func TestIfNests(t *testing.T) {
	// A branch is shell text, so another dispat command is an ordinary thing
	// to put in one. This is how a chain grows past what one condition can say.
	r := harness.New(t)
	// The inner scripts are quoted because the rendered string is read by a
	// shell: unquoted, "echo inner-gold" would reach the inner dispat as a
	// --then plus a stray positional argument.
	inner := r.DispatCommand("if", "TIER=gold",
		"--then", "'echo inner-gold'", "--else", "'echo inner-other'")

	res := r.CommandEnv([]string{"CI=1", "TIER=gold"}, "if", "CI", "--then", inner)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "inner-gold")

	res = r.CommandEnv([]string{"CI=1", "TIER=silver"}, "if", "CI", "--then", inner)
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "inner-other")
}

func TestIfIsReservedAndNeedsNoRepository(t *testing.T) {
	// `if` is a command word like every other, so it never parses as `dispat
	// run if`; the two-word spelling stays available for a script by that name.
	// A usage mistake exits 2 and reads no config, which is what lets the
	// command work in a folder that is not a monorepo at all.
	r := harness.New(t)

	res := r.Command("if")
	assert.Equal(t, 2, res.Code, "a missing condition is a usage error, not a run script")

	res = r.Command("if", "CI", "--then", "a", "--elif", "ENV")
	assert.Equal(t, 2, res.Code, "every condition needs its own --then")

	res = r.Command("if", "MY-VAR", "--then", "a")
	assert.Equal(t, 2, res.Code, "a name no environment could carry is a usage error")

	// One leading condition only: two answers to "what does the first --then
	// guard" would leave one silently ignored.
	res = r.Command("if", "CI", "--changed", "--then", "a")
	assert.Equal(t, 2, res.Code, "a condition and --changed together are a usage error")

	res = r.Command("if", "-f", "x", "-d", "y", "--then", "a")
	assert.Equal(t, 2, res.Code, "two file tests together are a usage error")

	// The window flags describe the --changed selection; beside any other
	// condition they would be silently ignored, which is refused instead.
	res = r.Command("if", "CI", "-p", "core", "--then", "a")
	assert.Equal(t, 2, res.Code, "--package without --changed is a usage error")
}
