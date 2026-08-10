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
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"

	"github.com/yohimik/dispat/services/dispat/internal/app"
	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// Commands accepted by Run.
const (
	cmdRelease = "release" // build and publish changed packages (default)
	cmdStatus  = "status"  // only print the graph and new versions
	cmdRun     = "run"     // run a script inside each changed package that has it
	cmdInit    = "init"    // write a starter config file; needs no config or git
	cmdPreview = "preview" // print one package's pending release notes
	cmdCompute = "compute" // derive the dependency graph from manifests

	// The standalone step commands, exposing the release pipeline's native
	// steps to custom flows. Like every command word (and unlike --version),
	// each permanently shadows a run script of the same name: `dispat
	// changelog` is never `dispat run changelog`.
	cmdChangelog   = "changelog"   // write pending changelog entries now
	cmdAutoversion = "autoversion" // native manifest reconciliation, plus syncLock
	cmdCommit      = "commit"      // per-package release commit (--tag, --push)
)

// Version is the dispat version `--version` reports. The default marks a
// local build; releases override it at build time from the release tag:
//
//	go build -ldflags "-X github.com/yohimik/dispat/services/dispat/internal/cli.Version=$DISPAT_VERSION"
//
// It is a flag, not a command, because a bare word after `dispat` is the run
// shorthand — a `version` command would shadow a run script named after the
// version stage.
var Version = "dev"

// resolvedVersion is what --version prints: the ldflags-injected value when a
// release build set one; otherwise the module version the Go toolchain
// stamped into the binary (`go install .../services/dispat@v1.2.3` records
// v1.2.3 there, with no ldflags involved); otherwise the "dev" default of a
// plain local build, whose stamp is "(devel)".
func resolvedVersion() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return Version
}

// logo is the dispat mark rendered for terminals — the block twin of
// imgs/logo.png: two same-size 6×6 squares,
// frame 1 thick, the filled square overlapping a quarter of the frame's
// inner area. Each logical pixel is a double `█`, which is what keeps the
// squares square in a terminal's ~1:2 character cells.
const logo = `
████████████
██        ██
██        ██
██    ████████████
██    ████████████
██████████████████
      ████████████
      ████████████
      ████████████`

// Run is the program entry point; it returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "monorepo root folder")
	cfgName := fs.String("config", "dispat.json",
		"config file name, relative to --root; when not set, the first of dispat.json, dispat.yaml, dispat.yml, dispat.toml that exists")
	fs.IntSlice("concurrency", nil, "override the configured concurrency: one value for both stages, or build,publish (e.g. 4,2); dispat run uses the build value")
	fs.String("log-level", "", "override the configured logLevel (trace, debug, info, warn, error)")
	fs.String("log-format", "", "override the configured logFormat (pretty, json)")
	onError := fs.String("on-error", app.OnErrorSkip,
		"run command: what a failing script does to the failed package's dependents (skip or continue)")
	since := fs.StringP("since", "s", "",
		"run command: select the packages the commits since the git revision address (scopes first, changed files for scopeless commits; e.g. HEAD~1, origin/main, a tag; 'all' selects every package) instead of the release window")
	consumers := fs.Bool("consumers", false,
		"run command: additionally run every package that transitively depends on a selected one, so downstream consumers are re-run with the change")
	initFormat := fs.String("format", "json",
		"init command: config file format (json, yaml or toml)")
	computeWrite := fs.Bool("write", false,
		"compute command: apply every suggestion to the config file")
	computeInteractive := fs.BoolP("interactive", "i", false,
		"compute command: confirm each suggestion before applying it")
	computeCheck := fs.Bool("check", false,
		"compute command: report only and exit 1 when suggestions exist (CI gate)")
	commitTag := fs.Bool("tag", false,
		"commit command: also create the annotated release tag at the resulting commit; an identical existing tag is skipped")
	commitPush := fs.Bool("push", false,
		"commit command: push the branch, and with --tag the tag(s); tags already on the remote are skipped")
	commitName := fs.String("name", "",
		"commit command: override the commit.name committer identity")
	commitEmail := fs.String("email", "",
		"commit command: override the commit.email committer identity")
	commitRemote := fs.String("remote", "",
		"commit command: override the commit.remote push target")
	commitMessage := fs.String("message-format", "",
		"commit command: override the commit.messageFormat template ({tags}, {packages})")
	commitInclude := fs.StringSlice("include", nil,
		"commit command: override the commit.include extra staged paths")
	clFile := fs.String("file", "",
		"changelog command: override the changelog.file name")
	clTitle := fs.String("title", "",
		"changelog command: override the changelog.title first line")
	clDateFormat := fs.String("date-format", "",
		"changelog command: override the changelog.dateFormat entry date layout")
	avRange := fs.String("range", "",
		"autoversion command: override the autoVersion.range write policy")
	avMatch := fs.StringSlice("match", nil,
		"autoversion command: override the autoVersion.match range globs")
	avManifests := fs.String("manifests", "",
		"autoversion command: override autoVersion.manifests (root or all)")
	avWriteVersion := fs.Bool("write-version", true,
		"autoversion command: override autoVersion.writeVersion")
	avSyncLock := fs.Bool("sync-lock", true,
		"autoversion command: run the space's syncLock scripts for changed packages")
	showVersion := fs.Bool("version", false, "print the dispat version and exit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `%s

usage: dispat [command] [flags]

commands:
  release                  build and publish changed packages (default)
  status                   print the project graph and new versions, without building
  run <script> [package]   run the named script inside each changed package that
                           defines it — its own scripts, then its space's, then the
                           top-level ones — honouring the dependency graph; or inside
                           the one named package only, changed or not. "dispat
                           <script>" is a shorthand when <script> is not a command
                           name, narrowing to the package it is invoked from inside a
                           package folder
  init                     write a starter config file (--format json, yaml or toml)
                           at the git repository root, unless one already exists
  preview [package]        print the pending release notes (breaking changes,
                           features, fixes) for one package, or for every
                           package with something pending when none is named
  changelog [package]      write the pending changelog entry now, so a custom
                           flow can land it inside the release commit;
                           already-written entries are skipped
  autoversion [package]    reconcile manifests to the planned versions (native
                           auto-versioning) and run syncLock where they changed
  commit [package]         create the per-package release commit; --tag tags
                           it, --push pushes the branch and tags
  compute                  scan every package's manifests (package.json, go.mod,
                           Cargo.toml, pyproject.toml, composer.json, pom.xml,
                           *.csproj, pubspec.yaml, requirements*.txt), derive
                           the dependency graph and suggest config changes;
                           --write applies all, --interactive confirms each,
                           --check gates CI; an edge marked keep: true is
                           never suggested for removal

flags:
%s`, logo, fs.FlagUsages())
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
	if *showVersion {
		// Before anything else: the version must print without a config file.
		fmt.Fprintln(stdout, logo)
		fmt.Fprintln(stdout, "\ndispat "+resolvedVersion())
		return 0
	}

	// Config errors are reported with a bootstrap logger since the configured
	// log level is not known yet.
	bootLog := zerolog.New(zerolog.ConsoleWriter{Out: stderr, TimeFormat: "15:04:05"}).
		With().Timestamp().Logger()

	inv, badArgs := parseInvocation(fs.Args(), fs.Usage, bootLog)
	if badArgs {
		return 2
	}
	cmd, runScript, argPkg := inv.cmd, inv.script, inv.pkg
	if cmd == cmdRun && !app.ValidOnError(*onError) {
		bootLog.Error().Str("on-error", *onError).Msgf("unknown --on-error value (want %q or %q)",
			app.OnErrorSkip, app.OnErrorContinue)
		return 2
	}
	if cmd == cmdRun && inv.pkg != "" && *since != "" {
		bootLog.Error().Msg("--since and an explicit package are mutually exclusive: the package already names the whole selection")
		return 2
	}
	if cmd == cmdRun && inv.pkg != "" && *consumers {
		bootLog.Error().Msg("--consumers and an explicit package are mutually exclusive: a targeted run is exactly one package")
		return 2
	}
	if cmd == cmdInit {
		// Before config loading: init is what creates the config, so there is
		// nothing to load yet (and no git repository is needed either).
		name, err := app.InitConfig(*root, *initFormat)
		if err != nil {
			bootLog.Error().Err(err).Msg("init failed")
			return 1
		}
		fmt.Fprintf(stdout, "created %s\n", name)
		return 0
	}

	// An explicit --config is used as-is; the default falls back through the
	// known config file names — in --root first, then ascending its parents,
	// so the CLI works from inside a package folder with the config's own
	// directory as the effective monorepo root. `dispat init --format yaml`
	// and a plain `dispat status` compose without flags either way.
	cfgPath, resolvedRoot, err := config.ResolveFile(*root, *cfgName, fs.Changed("config"))
	if err != nil {
		bootLog.Error().Err(err).Msg("config file not found")
		return 1
	}
	cfg, err := config.Load(cfgPath, fs)
	if err != nil {
		bootLog.Error().Err(err).Msg("invalid configuration")
		return 1
	}
	log := newLogger(cfg.LogLevel, cfg.LogFormat, stdout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The application does the work and logs its own findings; the controller
	// only maps the outcome onto an exit code.
	a := app.New(resolvedRoot, cfg, log)
	switch cmd {
	case cmdStatus:
		if a.Status(ctx) != nil {
			return 1
		}
	case cmdRun:
		opts := app.RunOptions{OnError: *onError, Package: inv.pkg, Since: *since, Consumers: *consumers}
		if inv.shorthand {
			// The shorthand narrows to the package the command was invoked
			// from: --root (default ".") is where the user stood, and the
			// ascent just told us where the monorepo root actually is.
			opts.Dir = *root
		}
		if a.RunScript(ctx, runScript, opts) != nil {
			return 1
		}
	case cmdPreview:
		notes, err := a.Preview(ctx, argPkg)
		if err != nil {
			return 1
		}
		switch {
		case notes != "":
			fmt.Fprint(stdout, notes)
		case argPkg == "":
			fmt.Fprintln(stdout, "no pending changes")
		default:
			fmt.Fprintf(stdout, "no pending changes for %s\n", argPkg)
		}
	case cmdChangelog:
		if a.Changelog(ctx, app.ChangelogOptions{Package: argPkg, Dir: *root,
			File: *clFile, Title: *clTitle, DateFormat: *clDateFormat}) != nil {
			return 1
		}
	case cmdAutoversion:
		opts := app.AutoVersionOptions{Package: argPkg, Dir: *root,
			Range: *avRange, Match: *avMatch, SyncLock: *avSyncLock}
		switch *avManifests {
		case "":
		case "all":
			v := true
			opts.AllManifests = &v
		case "root":
			v := false
			opts.AllManifests = &v
		default:
			bootLog.Error().Str("manifests", *avManifests).Msg("unknown --manifests value (want root or all)")
			return 2
		}
		if fs.Changed("write-version") {
			opts.WriteVersion = avWriteVersion
		}
		if a.AutoVersion(ctx, opts) != nil {
			return 1
		}
	case cmdCommit:
		if a.Commit(ctx, app.CommitOptions{Package: argPkg, Dir: *root,
			Tag: *commitTag, Push: *commitPush, Name: *commitName, Email: *commitEmail,
			Remote: *commitRemote, Message: *commitMessage, Include: *commitInclude}) != nil {
			return 1
		}
	case cmdCompute:
		open, err := a.Compute(ctx, cfgPath, app.ComputeOptions{
			Write:       *computeWrite,
			Interactive: *computeInteractive,
			Check:       *computeCheck,
			In:          os.Stdin,
			Out:         stdout,
		})
		if err != nil {
			return 1
		}
		if *computeCheck && open > 0 {
			return 1
		}
	default:
		if _, err := a.Release(ctx); err != nil {
			return 1
		}
	}
	return 0
}

// invocation is the parsed command line: which command runs and its
// positional arguments.
type invocation struct {
	cmd    string
	script string // run: the script name
	pkg    string // run: the optional target package; preview and the step commands: the package
	// shorthand marks the `dispat <script>` spelling of the run command; only
	// it narrows to the package the command was invoked from.
	shorthand bool
}

// parseInvocation maps the positional arguments onto a command, validating
// each command's arity — all before any config is loaded, so a usage mistake
// costs nothing. bad reports an unusable command line (the usage exit, 2),
// already logged and, where it helps, followed by the usage text.
func parseInvocation(rest []string, usage func(), log zerolog.Logger) (inv invocation, bad bool) {
	inv = invocation{cmd: cmdRelease}
	if len(rest) == 0 {
		return inv, false
	}
	inv.cmd = rest[0]
	switch inv.cmd {
	case cmdRelease, cmdStatus, cmdInit, cmdCompute:
		if len(rest) > 1 {
			log.Error().Strs("args", rest[1:]).Msg("unexpected arguments")
			return inv, true
		}
	case cmdRun:
		if len(rest) < 2 || len(rest) > 3 {
			log.Error().Msg("run requires the script name, optionally followed by one package")
			usage()
			return inv, true
		}
		inv.script = rest[1]
		if len(rest) == 3 {
			inv.pkg = rest[2]
		}
	case cmdPreview, cmdChangelog, cmdAutoversion, cmdCommit:
		if len(rest) > 2 {
			log.Error().Msgf("%s takes at most one argument: the package name", inv.cmd)
			usage()
			return inv, true
		}
		if len(rest) == 2 {
			inv.pkg = rest[1] // no argument: cover every releasing package
		}
	default:
		// Not a command name: treat the word as a script, so `dispat lint` is
		// `dispat run lint`. A name nothing defines still fails cleanly
		// later, which also catches command typos.
		if len(rest) > 1 {
			log.Error().Strs("args", rest[1:]).Msg("unexpected arguments")
			usage()
			return inv, true
		}
		inv.script, inv.cmd, inv.shorthand = inv.cmd, cmdRun, true
	}
	return inv, false
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
