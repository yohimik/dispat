// Package script executes package scripts through the system shell.
package script

import (
	"context"
	"io"
	"os/exec"
)

// Runner executes a shell command inside a package folder.
type Runner interface {
	Run(ctx context.Context, dir, command string, stdout, stderr io.Writer) error
}

// ShellRunner runs commands through the system shell, like npm scripts do.
type ShellRunner struct {
	Shell string // defaults to /bin/sh
}

var _ Runner = (*ShellRunner)(nil)

func (r *ShellRunner) Run(ctx context.Context, dir, command string, stdout, stderr io.Writer) error {
	shell := r.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
