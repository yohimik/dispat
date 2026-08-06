// Package cli is the command-line controller: it parses flags and commands,
// loads the configuration, builds the logger, and delegates the actual work
// to the app package, mapping its results onto process exit codes.
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

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"

	"github.com/yohimik/dispat/services/cli/internal/app"
	"github.com/yohimik/dispat/services/cli/internal/config"
)

// Commands accepted by Run.
const (
	cmdRelease = "release" // build and publish changed packages (default)
	cmdStatus  = "status"  // only print the graph and new versions
	cmdRun     = "run"     // run a space run script inside each changed package
)

// Run is the program entry point; it returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "monorepo root folder")
	cfgName := fs.String("config", "dispat.json", "config file name, relative to --root")
	fs.IntSlice("concurrency", nil, "override the configured concurrency: one value for both stages, or build,publish (e.g. 4,2); dispat run uses the build value")
	fs.String("log-level", "", "override the configured logLevel (trace, debug, info, warn, error)")
	fs.String("log-format", "", "override the configured logFormat (pretty, json)")
	onError := fs.String("on-error", app.OnErrorSkip,
		"run command: what a failing script does to the failed package's dependents (skip or continue)")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `usage: dispat [command] [flags]

commands:
  release       build and publish changed packages (default)
  status        print the project graph and new versions, without building
  run <script>  run the named space run script inside each changed package,
                honouring the dependency graph; "dispat <script>" is a
                shorthand when <script> is not a command name

flags:
%s`, fs.FlagUsages())
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return 0
		}
		// pflag's ContinueOnError mode returns the error without printing it,
		// so an unrecognized flag would otherwise exit 2 in total silence.
		fmt.Fprintf(stderr, "dispat: %v\n\n", err)
		fs.Usage()
		return 2
	}

	// Config errors are reported with a bootstrap logger since the configured
	// log level is not known yet.
	bootLog := zerolog.New(zerolog.ConsoleWriter{Out: stderr, TimeFormat: "15:04:05"}).
		With().Timestamp().Logger()

	cmd := cmdRelease
	runScript := ""
	if rest := fs.Args(); len(rest) > 0 {
		cmd = rest[0]
		switch cmd {
		case cmdRelease, cmdStatus:
			if len(rest) > 1 {
				bootLog.Error().Strs("args", rest[1:]).Msg("unexpected arguments")
				return 2
			}
		case cmdRun:
			if len(rest) != 2 {
				bootLog.Error().Msg("run requires exactly one argument: the run script name")
				fs.Usage()
				return 2
			}
			runScript = rest[1]
		default:
			// Not a command name: treat the word as a run script, so
			// `dispat lint` is `dispat run lint`. A name no space defines
			// still fails cleanly below, which also catches command typos.
			if len(rest) > 1 {
				bootLog.Error().Strs("args", rest[1:]).Msg("unexpected arguments")
				fs.Usage()
				return 2
			}
			runScript = cmd
			cmd = cmdRun
		}
	}
	if cmd == cmdRun && !app.ValidOnError(*onError) {
		bootLog.Error().Str("on-error", *onError).Msgf("unknown --on-error value (want %q or %q)",
			app.OnErrorSkip, app.OnErrorContinue)
		return 2
	}

	cfg, err := config.Load(filepath.Join(*root, *cfgName), fs)
	if err != nil {
		bootLog.Error().Err(err).Msg("invalid configuration")
		return 1
	}
	log := newLogger(cfg.LogLevel, cfg.LogFormat, stdout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The application does the work and logs its own findings; the controller
	// only maps the outcome onto an exit code.
	a := app.New(*root, cfg, log)
	switch cmd {
	case cmdStatus:
		if a.Status(ctx) != nil {
			return 1
		}
	case cmdRun:
		if a.RunScript(ctx, runScript, *onError) != nil {
			return 1
		}
	default:
		if a.Release(ctx) != nil {
			return 1
		}
	}
	return 0
}

// newLogger builds the run logger at the configured level. Format "pretty"
// renders human-friendly console output; "json" emits machine-readable lines
// for CI pipelines.
func newLogger(level, format string, out io.Writer) zerolog.Logger {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil { // config validation makes this unreachable
		lvl = zerolog.InfoLevel
	}
	if format == "pretty" {
		out = zerolog.ConsoleWriter{Out: out, TimeFormat: "15:04:05"}
	}
	return zerolog.New(out).Level(lvl).With().Timestamp().Logger()
}
