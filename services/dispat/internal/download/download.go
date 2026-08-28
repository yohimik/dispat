// Package download installs a tool published as a GitHub release asset.
//
// It is `dispat self-update` pointed at somebody else's repository: the same
// listing walk that picks a release, the same streamed download checked
// against the size and the checksum the release published, and the same two
// renames that put a file in place while keeping the one it replaced. What is
// new here is everything that could be assumed about dispat and cannot be
// assumed about a stranger's binary: which repository, which of its files,
// what to call the result, where it goes, and whether it is a binary at all.
//
// The package prints nothing and reads no configuration. The app layer turns
// what it returns into log events and text, exactly as it does for
// self-update.
package download

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

// Command is the command word this package's failures name.
const Command = "download"

// stagePattern names the folder a piped download is staged in. A folder of its
// own, removed whatever happens, because a pipe never renames the file into
// place and a half-read archive must not be left sitting on PATH.
const stagePattern = "dispat-download-"

// Fetcher stages a verified asset in a folder and answers where it put it.
//
// It is the one piece of the download this package borrows rather than owns,
// and it is an interface so the pipe path can be driven without a network:
// what happens to a staged file is this package's business, and where the
// staged file came from is not.
type Fetcher interface {
	Fetch(ctx context.Context, a selfupdate.Asset, dir, target string) (string, error)
}

// NewInstaller is the fetcher and installer a download uses: the self-update
// one, told which command it is working for and asked to validate nothing.
//
// Nothing, because there is nothing it could check. A foreign binary need not
// answer --version, and one downloaded for another platform cannot be run here
// at all, so insisting on either would refuse installs that are perfectly
// correct. The size and the checksum the release published stand in its place,
// and they check the transfer rather than the program.
func NewInstaller(exe string, client *http.Client, log zerolog.Logger) *selfupdate.Installer {
	return &selfupdate.Installer{Exe: exe, Client: client, Command: Command, Log: log}
}

// Stage runs fn with a verified copy of the asset in a folder of its own, and
// removes the folder afterwards whatever happened.
//
// It is what the pipe path installs through: the asset is checked exactly as
// an installed one is, but nothing is ever renamed onto PATH, so a pipe that
// fails leaves the machine as it found it.
func Stage(ctx context.Context, f Fetcher, a selfupdate.Asset, fn func(path string) error) error {
	dir, err := os.MkdirTemp("", stagePattern)
	if err != nil {
		return fmt.Errorf("download: cannot stage the download: %w", err)
	}
	defer os.RemoveAll(dir)
	path, err := f.Fetch(ctx, a, dir, filepath.Join(dir, a.Name))
	if err != nil {
		return err
	}
	return fn(named(path, a.Name))
}

// named gives the staged file the name the release published, so a command
// reading it by path sees "tool-1.2.3-linux.tar.gz" rather than the temporary
// name it was written under. A suffix is what an unpacker switches on, and
// keeping only the extension would turn .tar.gz into .gz.
//
// The name comes off the API, so only its last segment is used and anything
// that is not a name of its own leaves the staged file where it is: the
// folder is dispat's, and nothing may be written outside it.
func named(path, asset string) string {
	base := filepath.Base(asset)
	if base == "" || base == "." || base == ".." || base != asset {
		return path
	}
	renamed := filepath.Join(filepath.Dir(path), base)
	if renamed == path || os.Rename(path, renamed) != nil {
		// The staged file is still perfectly readable under its own name;
		// only the convenience of the published one is lost.
		return path
	}
	return renamed
}
