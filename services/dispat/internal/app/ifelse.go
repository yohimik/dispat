package app

import (
	"context"
	"fmt"
	"io"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// `dispat if`: the shell's own if/elif/else, spelled so it fits on one line
// inside a configured script. It is a package-level function rather than a
// method because it reads no configuration and needs no repository, like
// InitConfig and the manifest commands: a condition is about the environment,
// not about the monorepo it happens to be standing in.

// Branch is one condition and the script it guards.
type Branch struct {
	Cond   Condition
	Script string
}

// IfOptions is one whole if/elif/else chain.
type IfOptions struct {
	// Branches are tried in order and the first match wins.
	Branches []Branch
	// Else runs when no branch matched. Empty means there is no else, which
	// makes "nothing matched" a silent success.
	Else string
	// Lookup reads a variable. Nil means the process environment, which is
	// what the command uses; tests pass their own.
	Lookup func(string) string
	// OnFailure runs when the chosen script fails and decides the exit code.
	OnFailure string
	// Dir is the working directory: --root, as the user spelled it.
	Dir string
	// Runner executes the chosen script. Nil means a plain ShellRunner, which
	// is /bin/sh -c: there is no configuration here to take a shell from.
	Runner script.Runner
	Stdout io.Writer
	Stderr io.Writer
	Log    zerolog.Logger
}

// RunIf evaluates the chain and runs the one script it selects, returning the
// process exit code.
//
// Nothing matching with no else is a success that runs nothing: a chain whose
// conditions are all false has answered the question it was asked, and a
// command that failed there could not be used as a guard.
func RunIf(ctx context.Context, opts IfOptions) (int, error) {
	lookup := opts.Lookup
	if lookup == nil {
		return 0, fmt.Errorf("no environment lookup")
	}
	// matched is tracked apart from the script text because a branch may
	// legitimately guard an empty script ("--then ''", a deliberate no-op), and
	// that must still count as the chain's answer rather than falling through
	// to the else.
	chosen, matched := "", false
	for _, b := range opts.Branches {
		if b.Cond.Match(lookup) {
			chosen, matched = b.Script, true
			opts.Log.Debug().Str("condition", b.Cond.Spec).Msg("condition matched")
			break
		}
	}
	if !matched {
		if opts.Else == "" {
			opts.Log.Debug().Msg("no condition matched and there is no else, nothing to run")
			return 0, nil
		}
		chosen = opts.Else
		opts.Log.Debug().Msg("no condition matched, running the else script")
	}
	if chosen == "" {
		opts.Log.Debug().Msg("the selected branch is empty, nothing to run")
		return 0, nil
	}

	runner := opts.Runner
	if runner == nil {
		runner = &script.ShellRunner{}
	}
	return shellCall{
		Runner: runner, Dir: opts.Dir, Script: chosen, OnFailure: opts.OnFailure,
		Stdout: opts.Stdout, Stderr: opts.Stderr, Log: opts.Log,
	}.run(ctx)
}
