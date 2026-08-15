package app

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// `dispat if`'s branch selection and exit-code handling, against a fake runner
// so no shell is spawned and the question "which script did it choose" can be
// asked directly. That the chosen string then reaches a real /bin/sh is the
// integration suite's claim, not this one's.

// fakeRunner records every command it is asked to run and answers with a
// scripted outcome per command.
type fakeRunner struct {
	ran      []string   // commands, in the order they were run
	dirs     []string   // the working directory each was run in
	envs     [][]string // the env each was handed
	outcomes map[string]error
}

var _ script.Runner = (*fakeRunner)(nil)

func (f *fakeRunner) Run(_ context.Context, dir, command string, env []string, _, _ io.Writer) error {
	f.ran = append(f.ran, command)
	f.dirs = append(f.dirs, dir)
	f.envs = append(f.envs, env)
	return f.outcomes[command]
}

// exitErr is a real *exec.ExitError carrying the given status. Producing one
// by actually failing a process is what proves the code travels the same path
// a shell's would, rather than a hand-built error that happens to satisfy
// errors.As.
func exitErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	require.Error(t, err, "the helper must actually fail")
	return err
}

// runIf drives RunIf over a fixed environment with a fake runner.
func runIf(t *testing.T, f *fakeRunner, env map[string]string, opts IfOptions) int {
	t.Helper()
	opts.Runner = f
	opts.Lookup = func(name string) string { return env[name] }
	opts.Log = zerolog.Nop()
	code, err := RunIf(context.Background(), opts)
	require.NoError(t, err)
	return code
}

func mustCond(t *testing.T, spec string) Condition {
	t.Helper()
	c, err := ParseCondition(spec)
	require.NoError(t, err)
	return c
}

func TestRunIfChoosesTheFirstMatchingBranch(t *testing.T) {
	// The chain is an if/elif/else: the first true condition wins and nothing
	// after it is even considered, which is what makes a chain a switch rather
	// than a list of independent guards.
	f := &fakeRunner{}
	code := runIf(t, f, map[string]string{"ENV": "stage"}, IfOptions{
		Branches: []Branch{
			{Cond: mustCond(t, "ENV=prod"), Script: "deploy prod"},
			{Cond: mustCond(t, "ENV=stage"), Script: "deploy stage"},
			{Cond: mustCond(t, "ENV"), Script: "deploy something"},
		},
		Else: "echo nothing",
	})
	assert.Equal(t, 0, code)
	assert.Equal(t, []string{"deploy stage"}, f.ran,
		"only the first matching branch runs, and the later true one must not")
}

func TestRunIfFallsBackToElse(t *testing.T) {
	// No condition true and an else present: the else is the default case.
	f := &fakeRunner{}
	code := runIf(t, f, map[string]string{}, IfOptions{
		Branches: []Branch{
			{Cond: mustCond(t, "ENV=prod"), Script: "deploy prod"},
			{Cond: mustCond(t, "CI"), Script: "ci"},
		},
		Else: "echo nothing",
	})
	assert.Equal(t, 0, code)
	assert.Equal(t, []string{"echo nothing"}, f.ran)
}

func TestRunIfWithNoMatchAndNoElseRunsNothing(t *testing.T) {
	// A chain whose conditions are all false has answered the question it was
	// asked. Failing here would make the command unusable as a guard.
	f := &fakeRunner{}
	code := runIf(t, f, map[string]string{}, IfOptions{
		Branches: []Branch{{Cond: mustCond(t, "CI"), Script: "make ci"}},
	})
	assert.Equal(t, 0, code)
	assert.Empty(t, f.ran, "nothing may run when nothing matched")
}

func TestRunIfEmptyBranchIsStillTheAnswer(t *testing.T) {
	// "--then ''" is a deliberate no-op for a case that should do nothing. It
	// must not fall through to the else, which would run the wrong script for
	// the matched case.
	f := &fakeRunner{}
	code := runIf(t, f, map[string]string{"CI": "1"}, IfOptions{
		Branches: []Branch{{Cond: mustCond(t, "CI"), Script: ""}},
		Else:     "echo not this",
	})
	assert.Equal(t, 0, code)
	assert.Empty(t, f.ran, "a matched empty branch runs nothing and does not reach the else")
}

func TestRunIfPropagatesTheScriptsExitCode(t *testing.T) {
	// The helper is transparent in a pipeline: the code is the script's own,
	// not a dispat verdict about it.
	f := &fakeRunner{outcomes: map[string]error{"boom": exitErr(t, 7)}}
	code := runIf(t, f, map[string]string{"CI": "1"}, IfOptions{
		Branches: []Branch{{Cond: mustCond(t, "CI"), Script: "boom"}},
	})
	assert.Equal(t, 7, code)
}

func TestRunIfOnFailureDecidesTheCode(t *testing.T) {
	// With --on-failure the failure script has the last word, both on the
	// outcome and on the exit code.
	f := &fakeRunner{outcomes: map[string]error{
		"boom":   exitErr(t, 7),
		"notify": exitErr(t, 3),
	}}
	code := runIf(t, f, map[string]string{"CI": "1"}, IfOptions{
		Branches:  []Branch{{Cond: mustCond(t, "CI"), Script: "boom"}},
		OnFailure: "notify",
	})
	assert.Equal(t, 3, code, "the failure script's code replaces the failed script's")
	assert.Equal(t, []string{"boom", "notify"}, f.ran)
}

func TestRunIfOnFailureIsSkippedOnSuccess(t *testing.T) {
	f := &fakeRunner{}
	code := runIf(t, f, map[string]string{"CI": "1"}, IfOptions{
		Branches:  []Branch{{Cond: mustCond(t, "CI"), Script: "fine"}},
		OnFailure: "notify",
	})
	assert.Equal(t, 0, code)
	assert.Equal(t, []string{"fine"}, f.ran, "nothing failed, so nothing reacts to a failure")
}

func TestRunIfOnFailureSurvivesACancelledContext(t *testing.T) {
	// A Ctrl-C that killed the script must not also kill the cleanup reacting
	// to it: the failure script runs detached from the cancelled context, the
	// same treatment the finalize step gets.
	f := &fakeRunner{outcomes: map[string]error{"boom": exitErr(t, 1)}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, err := RunIf(ctx, IfOptions{
		Branches:  []Branch{{Cond: mustCond(t, "CI"), Script: "boom"}},
		OnFailure: "cleanup",
		Runner:    f,
		Lookup:    func(string) string { return "1" },
		Log:       zerolog.Nop(),
	})
	require.NoError(t, err)
	assert.Equal(t, 0, code, "the cleanup succeeded, so its code is the command's")
	assert.Equal(t, []string{"boom", "cleanup"}, f.ran,
		"the failure script must run even though the context was already cancelled")
}

func TestRunIfReportsARunnerFailureAsDispatsOwn(t *testing.T) {
	// A missing shell is not the script saying something, so it becomes
	// dispat's own failure rather than an exit code passed along.
	f := &fakeRunner{outcomes: map[string]error{"boom": errors.New("no such shell")}}
	code, err := RunIf(context.Background(), IfOptions{
		Branches: []Branch{{Cond: mustCond(t, "CI"), Script: "boom"}},
		Runner:   f,
		Lookup:   func(string) string { return "1" },
		Log:      zerolog.Nop(),
	})
	require.Error(t, err)
	assert.Equal(t, 1, code)
}

func TestRunIfTakesAResolvedLeadingBranch(t *testing.T) {
	// A leading condition answered before the chain ran — --changed, a file
	// test — slots into the chain as any other branch: resolved true wins over
	// a true elif behind it, resolved false falls through to the elifs and the
	// else. This is what lets RunIf stay untouched by the non-env conditions.
	f := &fakeRunner{}
	env := map[string]string{"ENV": "prod"}

	code := runIf(t, f, env, IfOptions{
		Branches: []Branch{
			{Cond: ResolvedCondition("--changed", true), Script: "build"},
			{Cond: mustCond(t, "ENV=prod"), Script: "deploy"},
		},
		Else: "echo idle",
	})
	assert.Equal(t, 0, code)
	assert.Equal(t, []string{"build"}, f.ran,
		"a leading condition resolved true wins even over a true elif")

	f = &fakeRunner{}
	runIf(t, f, env, IfOptions{
		Branches: []Branch{
			{Cond: ResolvedCondition("--changed", false), Script: "build"},
			{Cond: mustCond(t, "ENV=prod"), Script: "deploy"},
		},
		Else: "echo idle",
	})
	assert.Equal(t, []string{"deploy"}, f.ran,
		"a leading condition resolved false falls through to the elif chain")

	f = &fakeRunner{}
	runIf(t, f, map[string]string{}, IfOptions{
		Branches: []Branch{{Cond: ResolvedCondition("--changed", false), Script: "build"}},
		Else:     "echo idle",
	})
	assert.Equal(t, []string{"echo idle"}, f.ran,
		"resolved false with nothing else true reaches the else")
}

func TestRunIfRunsInTheGivenDirectory(t *testing.T) {
	f := &fakeRunner{}
	runIf(t, f, map[string]string{"CI": "1"}, IfOptions{
		Branches: []Branch{{Cond: mustCond(t, "CI"), Script: "pwd"}},
		Dir:      "/tmp/somewhere",
	})
	assert.Equal(t, []string{"/tmp/somewhere"}, f.dirs)
}
