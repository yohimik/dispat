package app

// The `dispat self-update` command: dispat replacing its own binary with one
// downloaded from its GitHub releases, and the notice every other command can
// print on its way out when a newer one is available.
//
// Like the manifest commands, this reads no config file and needs no git
// repository — it is about the binary, not about the repository the binary is
// pointed at — so it is package-level rather than a method on App. It renders
// the same two ways they do: a report on Out for a terminal, structured events
// through Log when the run asked for JSON.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

// SelfUpdateOptions is one `dispat self-update` invocation.
type SelfUpdateOptions struct {
	// Build is what the running binary knows about itself.
	Build selfupdate.Build
	// Source is where the replacement comes from.
	Source selfupdate.Source
	// Release names an exact version to install instead of the latest one.
	Release string
	// Check reports what the same invocation would do and changes nothing.
	Check bool
	// Force installs the selected release even when it is not newer, which is
	// how a damaged binary is repaired and how a prerelease line is left.
	Force bool
	// Rollback restores the backup instead of downloading anything.
	Rollback bool
	// GOOS and GOARCH are the platform the binary is for. Fields rather than
	// runtime constants so a test can ask for a platform it is not running.
	GOOS, GOARCH string
	// JSON renders events through Log instead of the report.
	JSON bool
	// Out receives the report.
	Out io.Writer
	// Log carries the events in JSON mode and the failures in both.
	Log zerolog.Logger
}

// SelfUpdate performs, or merely reports, one self-update.
//
// pending is whether the same invocation without Check would change the
// binary, which is the one question Check answers and the only thing that
// decides its exit code. It is false whenever the command actually did the
// work, so an ordinary run never looks like a failed gate.
func SelfUpdate(ctx context.Context, opts SelfUpdateOptions) (pending bool, err error) {
	if opts.Rollback {
		return rollback(ctx, opts)
	}
	if opts.Build.Origin == selfupdate.OriginDev {
		err := errors.New("this is a local build, with no released version to compare against")
		opts.Log.Error().Err(err).Msg("self-update is for release binaries")
		return false, err
	}

	rel, err := resolve(ctx, opts)
	if err != nil {
		opts.Log.Error().Err(err).Msg("self-update failed")
		return false, err
	}
	current := opts.Build.Version
	// Whether this invocation would change the binary, which is the whole of
	// what --check answers. Nothing downgrades on its own: only naming a
	// version, or forcing the install, reaches a release that is not newer.
	cur, _ := ccme.ParseVersion(current) // Describe only reports parseable versions
	var change bool
	switch {
	case opts.Force:
		change = true
	case opts.Release != "":
		change = rel.Version.Compare(cur) != 0
	default:
		change = rel.Version.Compare(cur) > 0
	}

	if opts.Check {
		report(opts, rel.Tag, rel.Version.String(), change)
		return change, nil
	}
	if !change {
		if opts.JSON {
			opts.Log.Info().Str("version", current).Str("latest", rel.Version.String()).
				Msg("already on the latest release")
			return false, nil
		}
		fmt.Fprintf(opts.Out, "dispat %s is already the latest release (%s)\n", current, rel.Tag)
		fmt.Fprintln(opts.Out, "install it again anyway with --force")
		return false, nil
	}
	// A go install build is replaced by another go install: rewriting the
	// file the Go toolchain owns would work exactly until the next one.
	if opts.Build.Origin == selfupdate.OriginGoInstall {
		err := fmt.Errorf("this dispat was installed with go install; update it with: %s",
			selfupdate.GoInstallCommand)
		opts.Log.Error().Err(err).Msg("self-update cannot replace a go install build")
		return false, err
	}

	asset, ok := rel.Asset(opts.GOOS, opts.GOARCH)
	if !ok {
		err := fmt.Errorf("%s carries no %s: it has %s", rel.Tag,
			selfupdate.AssetName(opts.GOOS, opts.GOARCH), strings.Join(rel.AssetNames(), ", "))
		opts.Log.Error().Err(err).Msg("no binary for this platform")
		return false, err
	}

	if !opts.JSON {
		fmt.Fprintf(opts.Out, "downloading %s (%s)\n", asset.Name, humanBytes(asset.Size))
	}
	installer := &selfupdate.Installer{Client: opts.Source.Client, Log: opts.Log}
	backup, err := installer.Install(ctx, asset, rel.Version.String())
	if err != nil {
		opts.Log.Error().Err(err).Msg("self-update failed")
		return false, err
	}
	exe, _ := selfupdate.Executable()

	if opts.JSON {
		opts.Log.Info().Str("version", rel.Version.String()).Str("tag", rel.Tag).
			Str("path", exe).Str("backup", backup).Msg("update installed")
		return false, nil
	}
	fmt.Fprintf(opts.Out, "installed dispat %s at %s\n", rel.Version.String(), exe)
	fmt.Fprintf(opts.Out, "the previous binary is at %s, removed on its own after a week\n", backup)
	fmt.Fprintf(opts.Out, "put it back with \"dispat self-update --rollback\"\n")
	if note := selfupdate.MacNote(opts.GOOS, exe); note != "" {
		fmt.Fprint(opts.Out, note)
	}
	return false, nil
}

// resolve picks the release to install: the one named, or the highest the
// source offers.
func resolve(ctx context.Context, opts SelfUpdateOptions) (selfupdate.Release, error) {
	if opts.Release != "" {
		return opts.Source.At(ctx, opts.Release)
	}
	rel, err := opts.Source.Latest(ctx)
	if errors.Is(err, selfupdate.ErrNoRelease) && !opts.Source.Prerelease {
		return selfupdate.Release{}, fmt.Errorf("%w (--prerelease considers the prereleases too)", err)
	}
	return rel, err
}

// report is what --check prints, for every selection it can be given.
func report(opts SelfUpdateOptions, tag, latest string, change bool) {
	if opts.JSON {
		opts.Log.Info().Str("version", opts.Build.Version).Str("latest", latest).
			Str("tag", tag).Bool("pending", change).Msg("update check")
		return
	}
	fmt.Fprintf(opts.Out, "current   dispat %s (%s)\n", opts.Build.Version,
		opts.Build.Platform(opts.GOOS, opts.GOARCH))
	fmt.Fprintf(opts.Out, "available dispat %s (%s)\n", latest, tag)
	if !change {
		fmt.Fprintln(opts.Out, "\nnothing to install")
		return
	}
	switch opts.Build.Origin {
	case selfupdate.OriginGoInstall:
		fmt.Fprintf(opts.Out, "\nupdate it with: %s\n", selfupdate.GoInstallCommand)
	default:
		fmt.Fprintln(opts.Out, "\ninstall it with: dispat self-update")
		if note := selfupdate.MacNote(opts.GOOS, ""); note != "" {
			fmt.Fprint(opts.Out, note)
		}
	}
}

// rollback restores the kept binary, or reports that there is one to restore.
func rollback(ctx context.Context, opts SelfUpdateOptions) (bool, error) {
	exe, err := selfupdate.Executable()
	if err != nil {
		opts.Log.Error().Err(err).Msg("self-update failed")
		return false, err
	}
	if opts.Check {
		version, err := selfupdate.BackupVersion(ctx, exe)
		if errors.Is(err, selfupdate.ErrNoBackup) {
			if opts.JSON {
				opts.Log.Info().Bool("pending", false).Msg("no backup to roll back to")
			} else {
				fmt.Fprintln(opts.Out, "there is no backup to roll back to")
			}
			return false, nil
		}
		if err != nil {
			opts.Log.Error().Err(err).Msg("the backup cannot be used")
			return false, err
		}
		if opts.JSON {
			opts.Log.Info().Str("backup", version).Bool("pending", true).Msg("a backup is available")
		} else {
			fmt.Fprintf(opts.Out, "the backup at %s is dispat %s\n", selfupdate.BackupPath(exe), version)
			fmt.Fprintln(opts.Out, "\nrestore it with: dispat self-update --rollback")
		}
		return true, nil
	}

	from, to, err := selfupdate.Rollback(ctx, exe)
	if err != nil {
		if errors.Is(err, selfupdate.ErrNoBackup) {
			err = fmt.Errorf("%w; a backup is kept for a week after an update, "+
				"and any version can be installed with --release <version>", err)
		}
		opts.Log.Error().Err(err).Msg("rollback failed")
		return false, err
	}
	if opts.JSON {
		opts.Log.Info().Str("from", from).Str("version", to).Str("path", exe).Msg("rolled back")
		return false, nil
	}
	fmt.Fprintf(opts.Out, "rolled back to dispat %s at %s\n", to, exe)
	fmt.Fprintf(opts.Out, "dispat %s is now the backup, so another --rollback returns to it\n", from)
	return false, nil
}

// humanBytes renders a download size the way a person reads one.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
