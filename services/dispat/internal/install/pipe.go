package install

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// AssetEnv names the staged file in the pipe command's environment. A command
// that cannot read a stream (unzip wants to seek, and an installer may want to
// keep the file) takes the path instead of the standard input.
const AssetEnv = "DISPAT_ASSET"

// AssetNameEnv is the file's name as the release published it, which is what a
// command needs to tell one archive layout from another.
const AssetNameEnv = "DISPAT_ASSET_NAME"

// pipeWaitDelay bounds how long a finished pipe is waited on for the output
// pipes its children may still hold, exactly as a package script is.
const pipeWaitDelay = 5 * time.Second

// Pipe hands a downloaded file to a command instead of installing it.
//
// It is the whole of dispat's answer to an asset that is not a binary: an
// archive is unpacked by the tool that unpacks archives, and a release that
// ships an install script is run by a shell, both of which the machine already
// has and neither of which belongs inside dispat. The verification is the same
// either way, so what reaches the command has still been checked against the
// size and the checksum the release published.
//
// The file arrives twice over, because the two shapes of command need
// different things: on the standard input, which is what "tar -xz" and "sh"
// read, and by path in DISPAT_ASSET, which is what a command that has to seek
// needs.
type Pipe struct {
	// Command is the shell text to run.
	Command string
	// Dir is its working directory, which is the install folder: a pipe that
	// unpacks an archive puts the result where a binary would have gone.
	Dir string
	// Shell is the command prefix, defaulting to script.DefaultShell so a pipe
	// is run exactly as a package script is.
	Shell          []string
	Stdout, Stderr io.Writer
	Log            zerolog.Logger
}

// Run feeds the staged file to the command and reports what it made of it.
//
// The command's own exit code is not propagated as an exit code of dispat's:
// a pipe is the last step of an install rather than a script under a helper,
// so a failure is a failed install and is reported as one.
func (p Pipe) Run(ctx context.Context, path, assetName string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("install: reading the downloaded file: %w", err)
	}
	defer f.Close()

	shell := p.Shell
	if len(shell) == 0 {
		shell = script.DefaultShell()
	}
	args := append(append([]string{}, shell[1:]...), p.Command)
	cmd := exec.CommandContext(ctx, shell[0], args...)
	cmd.Dir = p.Dir
	cmd.Env = append(os.Environ(), AssetEnv+"="+path, AssetNameEnv+"="+assetName)
	// Streamed rather than buffered: the file is a release asset, and holding
	// one in memory to hand it to a command that reads it once would be paying
	// for a copy nobody looks at.
	cmd.Stdin = f
	cmd.Stdout = p.Stdout
	cmd.Stderr = p.Stderr
	// The same treatment a package script gets: its own process group, so an
	// interrupt reaches an unpacker's children rather than only the shell, and
	// a wait delay so a survivor holding the output pipes cannot hang the
	// install forever.
	script.SetProcessGroup(cmd)
	cmd.WaitDelay = pipeWaitDelay

	started := time.Now()
	err = cmd.Run()
	p.Log.Debug().Str("command", p.Command).Str("dir", p.Dir).
		Dur("took", time.Since(started)).Err(err).Msg("install: the pipe finished")
	if err != nil {
		return fmt.Errorf("install: the pipe failed: %w", err)
	}
	return nil
}
