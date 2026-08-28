package app

// The `dispat download` command: dispat installing somebody else's release
// binary the way it installs its own.
//
// Like self-update and the manifest commands it reads no config file and needs
// no git repository, since it is about a folder on PATH rather than about the
// repository the binary is pointed at, so it is package-level rather than a
// method on App. It renders the same two ways they do: a report on Out for a
// terminal, structured events through Log when the run asked for JSON.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/download"
	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

// DownloadOptions is one `dispat download` invocation.
type DownloadOptions struct {
	// Repository is whose releases are read. It is the command's one
	// positional argument, already parsed.
	Repository download.Repository
	// Source is the release listing, pointed at that repository.
	Source selfupdate.Source
	// Release names an exact version to install instead of the latest one.
	Release string
	// Asset is which of the release's files to download, as a name or a glob,
	// with {os}, {arch}, {version}, {tag} and {name} expanded. Empty is only
	// unambiguous for a release carrying exactly one file.
	Asset string
	// BinDir and Name are where the tool is installed and what it is called
	// there. Both may be empty, and both then resolve themselves.
	BinDir string
	Name   string
	// Pipe hands the verified file to a command instead of installing it,
	// which is how an archive is unpacked and how a release's own install
	// script is run.
	Pipe string
	// Check reports what the same invocation would do and changes nothing.
	Check bool
	// Force installs the file even when the destination already carries it.
	Force bool
	// Rollback puts the binary the last download replaced back, without
	// downloading anything.
	Rollback bool
	// GOOS and GOARCH are the platform an asset pattern renders for. Fields
	// rather than runtime constants so a test can ask for a platform it is
	// not running.
	GOOS, GOARCH string
	// Env answers the three questions the install folder depends on. Nil is
	// the real machine.
	Env download.Environment
	// JSON renders events through Log instead of the report.
	JSON bool
	// Out receives the report.
	Out io.Writer
	// Err receives what a piped command writes when Out has to stay a clean
	// event stream. Nil is Out, which is right for a terminal: the command's
	// output belongs beside the report there.
	Err io.Writer
	// Log carries the events in JSON mode and the failures in both.
	Log zerolog.Logger
}

// env is the machine the install folder is resolved against.
func (o DownloadOptions) env() download.Environment {
	if o.Env != nil {
		return o.Env
	}
	return download.OSEnvironment{OS: o.GOOS}
}

// pipeOut is where a piped command's own output goes. In JSON mode it must not
// reach Out, whose every line is an event a machine parses; anywhere else it
// belongs exactly where the report does.
func (o DownloadOptions) pipeOut() io.Writer {
	if o.JSON && o.Err != nil {
		return o.Err
	}
	return o.Out
}

// piping reports whether this invocation hands the file to a command rather
// than installing it. The two paths differ in what they can know, not only in
// what they do: a pipe has no destination file to compare against.
func (o DownloadOptions) piping() bool { return o.Pipe != "" }

// Download performs, or merely reports, one download.
//
// pending is whether the same invocation without Check would change anything,
// which is the one question Check answers and the only thing that decides its
// exit code. It is false whenever the command actually did the work, so an
// ordinary run never looks like a failed gate.
func Download(ctx context.Context, opts DownloadOptions) (pending bool, err error) {
	target, err := download.ResolveTarget(opts.BinDir, opts.Name, opts.Repository, opts.env())
	if err != nil {
		opts.Log.Error().Err(err).Msg("download failed")
		return false, err
	}
	opts.Log.Debug().Str("repository", opts.Repository.String()).Str("dir", target.Dir).
		Str("name", target.Name).Bool("pipe", opts.piping()).Msg("download: destination resolved")
	// Housekeeping, on every invocation and before anything else: the copy an
	// earlier download of this tool kept is deleted once it is a week old,
	// exactly as dispat's own is at the top of every run.
	if selfupdate.PruneBackup(target.Path(), time.Now()) {
		opts.Log.Debug().Str("backup", selfupdate.BackupPath(target.Path())).
			Msg("download: the previous copy of this tool has expired and was removed")
	}
	// Whatever stands at the destination has to be a file a download may take
	// the place of, and it is asked before anything else so a refusal costs no
	// request. A pipe never touches it, so a pipe never asks.
	if !opts.piping() {
		if err := download.Replaceable(target.Path()); err != nil {
			opts.Log.Error().Err(err).Msg("download failed")
			return false, err
		}
	}
	if opts.Rollback {
		return downloadRollback(opts, target)
	}

	rel, err := resolveDownload(ctx, opts)
	if err != nil {
		opts.Log.Error().Err(err).Msg("download failed")
		return false, err
	}
	opts.Log.Debug().Str("tag", rel.Tag).Str("version", rel.Version.String()).
		Int("assets", len(rel.Assets)).Msg("download: release selected")

	asset, err := download.SelectAsset(rel, opts.Asset, download.Fields{
		OS: opts.GOOS, Arch: opts.GOARCH, Version: rel.Version.String(),
		Tag: rel.Tag, Name: opts.Repository.Repo,
	})
	if err != nil {
		opts.Log.Error().Err(err).Msg("no file to download")
		return false, err
	}
	opts.Log.Debug().Str("asset", asset.Name).Int64("bytes", asset.Size).
		Bool("digest", asset.Digest != "").Msg("download: asset selected")

	var change bool
	switch {
	case opts.Force, opts.piping():
		change = true
	default:
		installed, err := alreadyInstalled(opts, target, rel, asset)
		if err != nil {
			opts.Log.Error().Err(err).Msg("download failed")
			return false, err
		}
		change = !installed
	}
	if opts.Check {
		reportDownload(opts, target, rel, asset, change)
		return change, nil
	}
	if !change {
		if opts.JSON {
			opts.Log.Info().Str("tag", rel.Tag).Str("asset", asset.Name).Str("path", target.Path()).
				Msg("already installed")
		} else {
			fmt.Fprintf(opts.Out, "%s at %s is already %s\n", target.Name, target.Path(), rel.Tag)
			fmt.Fprintln(opts.Out, "install it again anyway with --force")
		}
		return false, nil
	}

	if err := download.EnsureDir(target.Dir); err != nil {
		opts.Log.Error().Err(err).Msg("download failed")
		return false, err
	}
	if !opts.JSON {
		fmt.Fprintf(opts.Out, "downloading %s (%s) from %s\n",
			asset.Name, humanBytes(asset.Size), opts.Repository.String())
	}
	if opts.piping() {
		return false, runDownloadPipe(ctx, opts, target, rel, asset)
	}
	return false, installDownload(ctx, opts, target, rel, asset)
}

// alreadyInstalled answers whether the destination already holds this exact
// file, which is what makes a download idempotent: a provisioning script may
// run the same command on every boot and pay for the transfer once.
//
// A release that publishes no checksum cannot be compared, and the honest
// answer is then "install it": saying it is already there would be a guess,
// and the guess that skips the install is the one that leaves a machine on an
// old binary forever. A destination dispat cannot read at all is a different
// thing entirely and is reported rather than guessed past.
func alreadyInstalled(opts DownloadOptions, target download.Target,
	rel selfupdate.Release, asset selfupdate.Asset) (bool, error) {
	installed, err := download.Installed(target.Path(), asset.Digest)
	switch {
	case errors.Is(err, download.ErrNoDigest):
		opts.Log.Warn().Str("asset", asset.Name).Str("tag", rel.Tag).
			Msg("the release publishes no checksum, so dispat cannot tell whether this tool is already installed")
		return false, nil
	case err != nil:
		// A destination that cannot be read is a destination that must not be
		// silently renamed aside, and a folder is the case that matters: the
		// install would otherwise move somebody's directory out of the way to
		// put a binary where it stood.
		return false, err
	}
	opts.Log.Debug().Str("path", target.Path()).Str("tag", rel.Tag).Bool("installed", installed).
		Msg("download: the destination was compared against the release digest")
	return installed, nil
}

// installDownload puts the verified file where the target says, keeping
// whatever was there as its backup.
func installDownload(ctx context.Context, opts DownloadOptions, target download.Target,
	rel selfupdate.Release, asset selfupdate.Asset) error {
	installer := download.NewInstaller(target.Path(), opts.Source.Client, opts.Log)
	backup, err := installer.Install(ctx, asset)
	if err != nil {
		opts.Log.Error().Err(err).Msg("download failed")
		return err
	}
	if opts.JSON {
		ev := opts.Log.Info().Str("repository", opts.Repository.String()).
			Str("tag", rel.Tag).Str("version", rel.Version.String()).
			Str("asset", asset.Name).Str("path", target.Path())
		if backup != "" {
			ev = ev.Str("backup", backup)
		}
		ev.Msg("tool installed")
		return nil
	}
	fmt.Fprintf(opts.Out, "installed %s %s at %s\n", target.Name, rel.Version.String(), target.Path())
	if backup != "" {
		fmt.Fprintf(opts.Out, "the previous binary is at %s, removed on its own after a week\n", backup)
		fmt.Fprintf(opts.Out, "put it back with \"dispat download %s --rollback\"\n", opts.Repository)
	}
	writeDownloadPathNote(opts, target)
	return nil
}

// runDownloadPipe hands the verified file to the command that was named for
// it, in the install folder, and reports what the command made of it.
func runDownloadPipe(ctx context.Context, opts DownloadOptions, target download.Target,
	rel selfupdate.Release, asset selfupdate.Asset) error {
	installer := download.NewInstaller(target.Path(), opts.Source.Client, opts.Log)
	pipe := download.Pipe{
		Command: opts.Pipe, Dir: target.Dir,
		Stdout: opts.pipeOut(), Stderr: opts.pipeOut(), Log: opts.Log,
	}
	err := download.Stage(ctx, installer, asset, func(path string) error {
		opts.Log.Debug().Str("command", opts.Pipe).Str("dir", target.Dir).
			Msg("download: handing the asset to the pipe")
		return pipe.Run(ctx, path, asset.Name)
	})
	if err != nil {
		opts.Log.Error().Err(err).Msg("download failed")
		return err
	}
	if opts.JSON {
		opts.Log.Info().Str("repository", opts.Repository.String()).Str("tag", rel.Tag).
			Str("asset", asset.Name).Str("dir", target.Dir).Str("pipe", opts.Pipe).
			Msg("asset piped")
		return nil
	}
	fmt.Fprintf(opts.Out, "piped %s (%s) through: %s\n", asset.Name, rel.Tag, opts.Pipe)
	writeDownloadPathNote(opts, target)
	return nil
}

// reportDownload is what --check prints: what would be installed, from where,
// and whether it would change anything.
func reportDownload(opts DownloadOptions, target download.Target, rel selfupdate.Release,
	asset selfupdate.Asset, change bool) {
	if opts.JSON {
		ev := opts.Log.Info().Str("repository", opts.Repository.String()).
			Str("tag", rel.Tag).Str("version", rel.Version.String()).
			Str("asset", asset.Name).Int64("bytes", asset.Size).Bool("pending", change)
		if opts.piping() {
			ev = ev.Str("pipe", opts.Pipe).Str("dir", target.Dir)
		} else {
			ev = ev.Str("path", target.Path())
		}
		ev.Msg("download check")
		return
	}
	fmt.Fprintf(opts.Out, "repository %s\n", opts.Repository)
	fmt.Fprintf(opts.Out, "release    %s (%s)\n", rel.Version.String(), rel.Tag)
	fmt.Fprintf(opts.Out, "asset      %s (%s)\n", asset.Name, humanBytes(asset.Size))
	if opts.piping() {
		fmt.Fprintf(opts.Out, "pipe       %s\n", opts.Pipe)
		fmt.Fprintf(opts.Out, "in         %s\n", target.Dir)
		fmt.Fprintln(opts.Out, "\nrun it with: dispat download")
		return
	}
	fmt.Fprintf(opts.Out, "install to %s\n", target.Path())
	if !change {
		fmt.Fprintln(opts.Out, "\nnothing to install: that file is already there")
		return
	}
	fmt.Fprintln(opts.Out, "\ninstall it with: dispat download")
}

// writeDownloadPathNote says so when the install folder is not on PATH, which
// is the one thing that makes a successful install look like a failed one: the
// tool is there and the shell cannot find it.
func writeDownloadPathNote(opts DownloadOptions, target download.Target) {
	if onPath(opts.env().Getenv("PATH"), target.Dir) {
		return
	}
	fmt.Fprintf(opts.Out, "note: %s is not on PATH, so the shell will not find %s until it is\n",
		target.Dir, target.Name)
}

// onPath reports whether dir is one of the folders the shell searches.
func onPath(pathVar, dir string) bool {
	if pathVar == "" {
		return false
	}
	for _, entry := range filepath.SplitList(pathVar) {
		if entry == "" {
			continue
		}
		if sameDirPath(entry, dir) {
			return true
		}
	}
	return false
}

// sameDirPath compares two folders the way a shell would: as written, and as
// the filesystem resolves them, so a symlinked /usr/local/bin still counts.
func sameDirPath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}

// downloadRollback restores the copy the last download of this tool kept, or
// reports that there is one to restore.
func downloadRollback(opts DownloadOptions, target download.Target) (bool, error) {
	backup := selfupdate.BackupPath(target.Path())
	if opts.Check {
		if _, err := os.Stat(backup); err != nil {
			if opts.JSON {
				opts.Log.Info().Str("path", target.Path()).Bool("pending", false).
					Msg("no backup to roll back to")
			} else {
				fmt.Fprintf(opts.Out, "there is no backup of %s to roll back to\n", target.Path())
			}
			return false, nil
		}
		if opts.JSON {
			opts.Log.Info().Str("backup", backup).Bool("pending", true).Msg("a backup is available")
		} else {
			fmt.Fprintf(opts.Out, "the backup of %s is at %s\n", target.Name, backup)
			fmt.Fprintln(opts.Out, "\nrestore it with: dispat download --rollback")
		}
		return true, nil
	}
	if err := selfupdate.Restore(target.Path()); err != nil {
		if errors.Is(err, selfupdate.ErrNoBackup) {
			err = fmt.Errorf("%w; a copy is kept for a week after a download, "+
				"and any version can be installed with --release <version>", err)
		}
		opts.Log.Error().Err(err).Msg("rollback failed")
		return false, err
	}
	if opts.JSON {
		opts.Log.Info().Str("path", target.Path()).Str("backup", backup).Msg("rolled back")
		return false, nil
	}
	fmt.Fprintf(opts.Out, "rolled back %s at %s\n", target.Name, target.Path())
	fmt.Fprintf(opts.Out, "the binary it replaced is now the backup, so another --rollback returns to it\n")
	return false, nil
}

// resolveDownload picks the release to install: the one named, or the highest
// the repository offers.
func resolveDownload(ctx context.Context, opts DownloadOptions) (selfupdate.Release, error) {
	if opts.Release != "" {
		return opts.Source.At(ctx, opts.Release)
	}
	rel, err := opts.Source.Latest(ctx)
	if errors.Is(err, selfupdate.ErrNoRelease) {
		// The two things a repository with releases nobody can see has wrong
		// are the prefix and the prereleases, and neither is guessable from
		// the outside, so both are named rather than either assumed.
		where := fmt.Sprintf("under %q", opts.Source.TagPrefix)
		if opts.Source.AnyTag {
			where = "with no tag prefix"
		}
		hint := "--tag-prefix says what a tag carries before its version"
		if !opts.Source.Prerelease {
			hint = "--prerelease considers the prereleases too, and " + hint
		}
		return selfupdate.Release{}, fmt.Errorf("%w %s (%s)", err, where, hint)
	}
	return rel, err
}
