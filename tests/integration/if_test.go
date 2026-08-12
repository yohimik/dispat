package integration_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
}
