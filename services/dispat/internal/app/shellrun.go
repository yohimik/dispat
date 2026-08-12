package app

import (
	"context"
	"errors"
	"io"
	"os/exec"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// The one execution path the two shell helpers share. Both pick a shell string
// some other way — `dispat if` from a condition, `dispat exec` from the
// config — and from there the work is identical: run it, let its output
// through untouched, turn its exit status into the command's, and give
// --on-failure the last word. Keeping that here is what stops the two
// commands' notions of "failed" from drifting apart.

// shellCall is one script and everything needed to run it.
type shellCall struct {
	Runner script.Runner
	Dir    string   // the working directory: --root, as the user spelled it
	Script string   // the shell string itself
	Env    []string // KEY=value pairs added on top of the process environment
	// OnFailure runs when Script fails and decides the exit code in its place.
	// Empty means the failure propagates as it stands.
	OnFailure string
	Stdout    io.Writer
	Stderr    io.Writer
	Log       zerolog.Logger
}

// run executes the script and returns the process exit code.
//
// Output is passed straight through rather than folded into the log the way a
// release stage's is: glue must not reformat the output of the thing it wraps,
// or a helper in the middle of a pipeline stops being transparent.
//
// The code is the script's own, so `dispat if X --then 'exit 7'` exits 7. Only
// a failure that is not the script's — no shell, a cancelled context — becomes
// dispat's own 1, because those are dispat failing rather than the script
// saying something.
func (c shellCall) run(ctx context.Context) (int, error) {
	code, err := c.exec(ctx, c.Script)
	if err != nil {
		return 1, err
	}
	if code == 0 || c.OnFailure == "" {
		return code, nil
	}
	// The failure script decides the outcome, so it needs to survive whatever
	// ended the first one: a Ctrl-C that killed the script would otherwise kill
	// the cleanup reacting to it before it ran. Same treatment the finalize
	// step gets.
	c.Log.Debug().Int("code", code).Msg("script failed, running the failure script")
	failCode, err := c.exec(context.WithoutCancel(ctx), c.OnFailure)
	if err != nil {
		return 1, err
	}
	return failCode, nil
}

// exec runs one shell string and separates "the script said no" from "dispat
// could not run it". An *exec.ExitError carries the former; anything else is
// the latter, and is logged here because only this layer knows it was not the
// script's own answer.
func (c shellCall) exec(ctx context.Context, command string) (int, error) {
	err := c.Runner.Run(ctx, c.Dir, command, c.Env, c.Stdout, c.Stderr)
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code, nil
		}
		// Terminated by a signal, which ExitCode reports as -1: there is no
		// code of the script's own to propagate, and -1 is not a status a
		// process can exit with. A Ctrl-C reaching the whole group lands here.
		c.Log.Debug().Str("script", command).Msg("script was terminated by a signal")
		return 1, nil
	}
	c.Log.Error().Err(err).Str("script", command).Msg("could not run the script")
	return 0, err
}
