package script

import "strings"

// AppendArgs appends the arguments an operator typed after `--` to a script's
// command text, the way `npm run test -- --watch` appends to an npm script.
//
// Appending to the text, rather than passing the arguments as the shell's
// positional parameters, is what makes this work at all here. A dispat script
// is an opaque string from the configuration ("vitest run"), almost never
// written to read `"$@"`, and the shell it runs under is configurable: under
// `cmd /C` or `node -e` there are no positional parameters to pass. Appending
// is the one mechanism every shell shares, and it needs nothing added to a
// script that already exists.
//
// With no arguments the command is returned untouched, so the ordinary
// invocation produces byte-identical text to the one before this existed.
func AppendArgs(command string, args []string) string {
	if len(args) == 0 {
		return command
	}
	var b strings.Builder
	b.WriteString(command)
	for _, arg := range args {
		b.WriteByte(' ')
		b.WriteString(QuoteArg(arg))
	}
	return b.String()
}

// AppendArgsToLast appends the arguments to the last command of a script's
// sequence, which is where a script bound to several commands wants them.
//
// The last command is the script's work; the ones before it are what had to
// happen first. `["npm ci", "npm run test"] -- --watch` means watching the
// tests, not installing in watch mode — putting the arguments on every command
// would break the setup steps, and putting them on the first would put them on
// the one command that never wants them.
//
// With no arguments, or no commands, the sequence is returned as it stands, so
// the ordinary invocation neither copies nor changes anything.
func AppendArgsToLast(commands []string, args []string) []string {
	if len(args) == 0 || len(commands) == 0 {
		return commands
	}
	out := make([]string, len(commands))
	copy(out, commands)
	out[len(out)-1] = AppendArgs(out[len(out)-1], args)
	return out
}

// QuoteArg renders one argument for a shell command line, quoting it only when
// leaving it alone would change what the shell reads.
//
// Quoting only what needs it is deliberate. The overwhelmingly common
// arguments — `--watch`, `--reporter=dot`, a file path — go through verbatim,
// which keeps the assembled command readable in a log and, more usefully,
// keeps it correct under a non-POSIX shell: nothing is added that `cmd /C`
// would have to understand. An argument carrying whitespace or a shell
// metacharacter is the case where the two of them genuinely disagree, and
// there POSIX single-quoting wins, because it is the shell dispat defaults to
// and the only one where the question has a portable answer.
//
// An empty argument is always quoted: it has to survive as an argument rather
// than vanish into the gap between two others.
func QuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if !strings.ContainsAny(arg, shellSpecials) {
		return arg
	}
	// Single quotes take everything literally, so the only character needing
	// care is the single quote itself: close the run, emit an escaped quote,
	// open a new run.
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// shellSpecials is every character that means something to a POSIX shell
// outside single quotes, plus the whitespace that would split one argument
// into two.
//
// What is absent matters as much as what is here. `-`, `.`, `/`, `:`, `,`,
// `+`, `@`, `=` and `%` are what ordinary flags and paths are made of:
// `--reporter=dot` and `src/main.go` have to survive verbatim or the rule
// stops being "quote what needs it" and becomes "quote everything". None of
// them can change how a shell reads a word in argument position — `=` is only
// an assignment in the *first* word of a command, and we are always appending
// after one.
const shellSpecials = " \t\n\r\v\f|&;<>()$`\\\"'*?[]#~!{}"
