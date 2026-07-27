// Package cli wires the tool together: configuration, planning, execution and
// reporting.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"

	"github.com/yohimik/monorel/internal/changelog"
	"github.com/yohimik/monorel/internal/config"
	"github.com/yohimik/monorel/internal/gitx"
	"github.com/yohimik/monorel/internal/plan"
	"github.com/yohimik/monorel/internal/release"
	"github.com/yohimik/monorel/internal/script"
)

// Commands accepted by Run.
const (
	cmdRelease = "release" // build and publish changed packages (default)
	cmdStatus  = "status"  // only print the graph and new versions
)

// Run is the program entry point; it returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := pflag.NewFlagSet("monorel", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "monorepo root folder")
	cfgName := fs.String("config", "monorel.yaml", "config file name, relative to --root")
	fs.IntSlice("concurrency", nil, "override the configured concurrency: one value for both stages, or build,publish (e.g. 4,2)")
	fs.String("log-level", "", "override the configured logLevel (pretty, trace, debug, info, warn, error)")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `usage: monorel [command] [flags]

commands:
  release  build and publish changed packages (default)
  status   print the project graph and new versions, without building

flags:
%s`, fs.FlagUsages())
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return 0
		}
		return 2
	}

	// Config errors are reported with a bootstrap logger since the configured
	// log level is not known yet.
	bootLog := zerolog.New(zerolog.ConsoleWriter{Out: stderr, TimeFormat: "15:04:05"}).
		With().Timestamp().Logger()

	cmd := cmdRelease
	if rest := fs.Args(); len(rest) > 0 {
		cmd = rest[0]
		if cmd != cmdRelease && cmd != cmdStatus {
			bootLog.Error().Str("command", cmd).Msg("unknown command (want release or status)")
			fs.Usage()
			return 2
		}
		if len(rest) > 1 {
			bootLog.Error().Strs("args", rest[1:]).Msg("unexpected arguments")
			return 2
		}
	}

	cfg, err := config.Load(filepath.Join(*root, *cfgName), fs)
	if err != nil {
		bootLog.Error().Err(err).Msg("invalid configuration")
		return 1
	}
	log := newLogger(cfg.LogLevel, stdout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pkgs, deps, err := cfg.Discover(*root)
	if err != nil {
		log.Error().Err(err).Msg("package discovery failed")
		return 1
	}

	git := &gitx.CLI{Dir: *root}
	pl, err := plan.Compute(ctx, git, pkgs, deps)
	if err != nil {
		log.Error().Err(err).Msg("planning failed")
		return 1
	}
	printGraph(log, pl)
	if cmd == cmdStatus {
		return 0
	}

	exec := &release.Executor{
		BuildConcurrency:   cfg.BuildConcurrency,
		PublishConcurrency: cfg.PublishConcurrency,
		Runner:             &script.ShellRunner{},
		Tagger:             git,
		Changelog:          &changelog.FileWriter{},
		Log:                log,
	}
	start := time.Now()
	results := exec.Run(ctx, pl)
	return summarize(log, pl, results, time.Since(start))
}

// newLogger builds the run logger. "pretty" renders human-friendly console
// output; any other level emits machine-readable JSON for CI pipelines.
func newLogger(level string, out io.Writer) zerolog.Logger {
	if level == "pretty" {
		w := zerolog.ConsoleWriter{Out: out, TimeFormat: "15:04:05"}
		return zerolog.New(w).Level(zerolog.InfoLevel).With().Timestamp().Logger()
	}
	lvl, err := zerolog.ParseLevel(level)
	if err != nil { // config validation makes this unreachable
		lvl = zerolog.InfoLevel
	}
	return zerolog.New(out).Level(lvl).With().Timestamp().Logger()
}

// printGraph prints the whole project graph in dependency order, highlighting
// changed packages with their version transition.
func printGraph(log zerolog.Logger, pl *plan.Plan) {
	changedCount := 0
	for _, name := range pl.Order {
		rel := pl.Releases[name]
		ev := log.Info().
			Str("package", name).
			Str("space", rel.Pkg.Space.Name)
		if provs := pl.Providers[name]; len(provs) > 0 {
			ev = ev.Strs("dependsOn", provs)
		}
		if rel.Changed() {
			changedCount++
			ev.Str("bump", rel.Bump.String()).
				Str("version", rel.Current.String()+" -> "+rel.Next.String()).
				Int("ownCommits", len(rel.Commits)).
				Strs("dueToProviders", rel.DueTo).
				Msg("● changed")
		} else {
			ev.Str("version", rel.Current.String()).Msg("unchanged")
		}
	}
	log.Info().Int("packages", len(pl.Order)).Int("changed", changedCount).Msg("release plan ready")
}

// summarize prints one line per processed package plus totals, and returns the
// process exit code (1 when anything failed).
func summarize(log zerolog.Logger, pl *plan.Plan, results map[string]*release.Result, took time.Duration) int {
	published, failed, skipped := 0, 0, 0
	for _, name := range pl.Order {
		res, ok := results[name]
		if !ok {
			continue
		}
		var ev *zerolog.Event
		switch res.Status {
		case release.StatusPublished:
			published++
			ev = log.Info().Str("tag", gitx.TagName(name, res.To))
		case release.StatusFailed:
			failed++
			ev = log.Error().Err(res.Err)
		default:
			skipped++
			ev = log.Warn()
		}
		ev.Str("package", name).
			Str("status", res.Status.String()).
			Str("version", res.From.String()+" -> "+res.To.String()).
			Dur("took", res.Duration).
			Msg("summary")
	}
	log.Info().
		Int("published", published).
		Int("failed", failed).
		Int("skipped", skipped).
		Int("unchanged", len(pl.Order)-len(results)).
		Dur("took", took).
		Msg("done")
	if failed > 0 {
		return 1
	}
	return 0
}
