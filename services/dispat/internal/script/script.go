// Package script executes package scripts through a configurable shell.
package script

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/rs/zerolog"
)

// Runner executes a shell command inside a package folder. env entries
// ("KEY=value") are added on top of the parent process environment.
type Runner interface {
	Run(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) error
}

// DefaultShell is the shell a runner falls back to when none is configured.
// It is a function rather than a slice so no caller can edit the default out
// from under the others.
func DefaultShell() []string { return []string{"/bin/sh", "-c"} }

// SetProcessGroup puts a command in a process group of its own, where the
// platform has them, so cancelling it signals the children it left behind
// rather than only the shell holding their output pipes.
//
// Exported for the one command outside this package that runs a shell string
// of its own: `dispat install --pipe` feeds the command it is given on the
// standard input, which no Runner does, and an interrupt reaching an unpacker
// halfway through a folder on PATH has to reach all of it.
func SetProcessGroup(cmd *exec.Cmd) { setSysProcAttr(cmd) }

// ShellRunner runs commands through a shell, like npm scripts do.
type ShellRunner struct {
	// Shell is the command prefix the script is appended to, e.g.
	// ["bash", "-c"] or ["cmd", "/C"]. Defaults to ["/bin/sh", "-c"].
	Shell []string
	// WaitDelay bounds how long Run keeps waiting for the script's output
	// pipes after its process exited or its context was cancelled (see
	// exec.Cmd.WaitDelay). Zero means the 5s default.
	WaitDelay time.Duration
	// Log records execution metadata at trace level. The zero
	// Logger writes nowhere, which is what the callers that have none get.
	Log zerolog.Logger
}

var _ Runner = (*ShellRunner)(nil)

func (r *ShellRunner) Run(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) error {
	shell := r.Shell
	if len(shell) == 0 {
		shell = DefaultShell()
	}
	args := append(append([]string{}, shell[1:]...), command)
	cmd := exec.CommandContext(ctx, shell[0], args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// A script may leave children behind (a daemon, a background upload), and
	// they inherit the output pipes. Killing only the shell on cancellation
	// would leave those children holding the pipes and Wait blocked on them
	// indefinitely, so on unix the script runs in its own process group and
	// cancellation signals the whole group (script_unix.go). WaitDelay is the
	// backstop for anything that escapes the group: after it, Wait stops
	// waiting for the pipes. A script whose own process succeeded but whose
	// surviving children forced that timeout has still succeeded —
	// backgrounding a daemon is legitimate — so ErrWaitDelay maps to nil.
	setSysProcAttr(cmd)
	cmd.WaitDelay = r.WaitDelay
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 5 * time.Second
	}
	// Record execution context and timing without copying a shell command
	// that may contain literal credentials into the log.
	started := time.Now()
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	if ev := r.Log.Trace(); ev.Enabled() {
		ev.Strs("shell", shell).
			Str("dir", dir).
			Int("commandBytes", len(command)).
			Int("env", len(env)).
			Dur("took", time.Since(started)).
			Err(err).
			Msg("script finished")
	}
	return err
}
