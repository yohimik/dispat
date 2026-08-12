package script

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendArgsWithNoneIsTheIdentity(t *testing.T) {
	// The load-bearing case: every invocation that forwards nothing must
	// produce exactly the text it produced before forwarding existed.
	assert.Equal(t, "vitest run", AppendArgs("vitest run", nil))
	assert.Equal(t, "vitest run", AppendArgs("vitest run", []string{}))
}

func TestAppendArgsLeavesOrdinaryFlagsAlone(t *testing.T) {
	// What almost every invocation looks like: nothing gains a quote, so the
	// assembled command reads in a log exactly as it was typed, and it means
	// the same thing under a shell that is not POSIX.
	assert.Equal(t, "vitest run --watch --reporter=dot",
		AppendArgs("vitest run", []string{"--watch", "--reporter=dot"}))
	assert.Equal(t, "go build ./cmd/... -o bin/app",
		AppendArgs("go build", []string{"./cmd/...", "-o", "bin/app"}))
}

func TestQuoteArgOnlyWhatNeedsIt(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain flag":      {"--watch", "--watch"},
		"flag with value": {"--reporter=dot", "--reporter=dot"},
		"path":            {"src/main.go", "src/main.go"},
		"percent":         {"50%", "50%"},
		"space":           {"my suite", "'my suite'"},
		"tab":             {"a\tb", "'a\tb'"},
		"glob":            {"*.go", "'*.go'"},
		"dollar":          {"$HOME", "'$HOME'"},
		"semicolon":       {"a;rm -rf /", "'a;rm -rf /'"},
		"pipe":            {"a|b", "'a|b'"},
		"backtick":        {"`id`", "'`id`'"},
		"double quote":    {`say "hi"`, `'say "hi"'`},
		"single quote":    {"it's", `'it'\''s'`},
		"only quotes":     {"''", `''\'''\'''`},
		"leading hash":    {"#comment", "'#comment'"},
		"tilde":           {"~/x", "'~/x'"},
		"empty":           {"", "''"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, QuoteArg(tc.in))
		})
	}
}

// TestAppendArgsSurvivesTheShell is the claim the quoting exists for, made
// against a real /bin/sh rather than against the string: whatever was typed
// arrives as one argument, byte for byte, however many metacharacters it
// carries. A rule that quoted too little would split these; one that quoted
// too much would deliver the quotes themselves.
func TestAppendArgsSurvivesTheShell(t *testing.T) {
	args := []string{
		"--watch",
		"--reporter=dot",
		"my suite",
		"it's",
		"$HOME",
		"a;b",
		"*.go",
		"",
	}
	// Print each argument on its own line, so the round trip is observable.
	command := AppendArgs(`printf '%s\n'`, args)

	var out strings.Builder
	r := &ShellRunner{}
	require.NoError(t, r.Run(context.Background(), t.TempDir(), command, nil, &out, &out))

	got := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	assert.Equal(t, args, got, "command was: %s", command)
}

// TestAppendArgsDoesNotLetAnArgumentRunACommand: the quoting is also what
// stands between a forwarded argument and the shell it lands in. An argument
// is data, and no spelling of it may become a second command.
func TestAppendArgsDoesNotLetAnArgumentRunACommand(t *testing.T) {
	marker := t.TempDir() + "/pwned"
	command := AppendArgs("printf '%s'", []string{"; touch " + marker})

	var out strings.Builder
	r := &ShellRunner{}
	require.NoError(t, r.Run(context.Background(), t.TempDir(), command, nil, &out, &out))

	assert.Equal(t, "; touch "+marker, out.String(), "the argument arrived as text")
	assert.NoFileExists(t, marker, "and was never run")
}
