// Package script executes package scripts through a configurable shell.
package script

import (
	"context"
	"io"
	"os"
	"os/exec"
)

// Runner executes a shell command inside a package folder. env entries
// ("KEY=value") are added on top of the parent process environment.
type Runner interface {
	Run(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) error
}

// ShellRunner runs commands through a shell, like npm scripts do.
type ShellRunner struct {
	// Shell is the command prefix the script is appended to, e.g.
	// ["bash", "-c"] or ["cmd", "/C"]. Defaults to ["/bin/sh", "-c"].
	Shell []string
}

var _ Runner = (*ShellRunner)(nil)

func (r *ShellRunner) Run(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) error {
	shell := r.Shell
	if len(shell) == 0 {
		shell = []string{"/bin/sh", "-c"}
	}
	args := append(append([]string{}, shell[1:]...), command)
	cmd := exec.CommandContext(ctx, shell[0], args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
