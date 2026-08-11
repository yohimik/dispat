// Package cli is the command-line controller: it parses flags and commands,
// loads the configuration, builds the logger, and delegates the actual work
// to the app package, mapping its results onto process exit codes.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"

	"github.com/yohimik/dispat/services/dispat/internal/app"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
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
	cmdGithub      = "github"      // per-package GitHub release, published now

	// The manifest commands, exposing the pkg/scanner and pkg/writer
	// libraries directly. Like init, they need no config file and no git
	// repository: they only ever look at the files they are pointed at.
	cmdScanner  = "scanner"  // read what a folder's manifests declare
	cmdWriter   = "writer"   // edit manifests in place, format-preserving
	cmdReplacer = "replacer" // replace literal text in any file, no parsing
)

// manifestCommand reports the commands that need neither a config file nor a
// git repository: they read and write only the files named on the command
// line, which is what makes them usable on any checkout.
func manifestCommand(cmd string) bool {
	return cmd == cmdScanner || cmd == cmdWriter || cmd == cmdReplacer
}

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

// versionLine is the whole of what --version reports: the version, and the
// platform the binary was built for. The platform is there because "dispat
// 1.4.0" alone does not say which of the release's three binaries is
// running, and that is the first thing a bug report needs.
func versionLine() string {
	return "dispat " + resolvedVersion() + " (" + runtime.GOOS + "_" + runtime.GOARCH + ")"
}

// Run is the program entry point; it returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	o := declareFlags(fs)
	fs.Usage = func() { printUsage(stderr, fs) }
	usageForCommand := func(cmd string) { printCommandUsage(stderr, fs, cmd) }
	if err := fs.Parse(args); err != nil {
		// pflag's ContinueOnError mode returns the error without printing it,
		// so an unrecognized flag would otherwise exit 2 in total silence.
		fmt.Fprintf(stderr, "dispat: %v\n\n", err)
		fs.Usage()
		return 2
	}
	if *o.showVersion {
		// Before anything else: the version must print without a config file.
		fmt.Fprintln(stdout, logo)
		fmt.Fprintln(stdout, "\n"+versionLine())
		return 0
	}
	if *o.showHelp {
		// Also before anything else, and before the arity checks: `dispat
		// writer --help` should help rather than complain that it was given
		// no manifests. With no command word at all, the program help.
		if word := commandWord(fs.Args()); word != "" {
			usageForCommand(word)
		} else {
			fs.Usage()
		}
		return 0
	}

	// Config errors are reported with a bootstrap logger since the configured
	// log level is not known yet.
	bootLog := zerolog.New(zerolog.ConsoleWriter{Out: stderr, TimeFormat: "15:04:05"}).
		With().Timestamp().Logger()

	inv, badArgs := parseInvocation(fs.Args(), usageForCommand, bootLog)
	if badArgs {
		return 2
	}
	cmd, runScript := inv.cmd, inv.script
	if cmd == cmdRun && !app.ValidOnError(*o.onError) {
		bootLog.Error().Str("on-error", *o.onError).Msgf("unknown --on-error value (want %q or %q)",
			app.OnErrorSkip, app.OnErrorContinue)
		return 2
	}
	if cmd == cmdInit {
		// Before config loading: init is what creates the config, so there is
		// nothing to load yet (and no git repository is needed either).
		name, err := app.InitConfig(*o.root, *o.initFormat)
		if err != nil {
			bootLog.Error().Err(err).Msg("init failed")
			return 1
		}
		fmt.Fprintf(stdout, "created %s\n", name)
		return 0
	}
	if manifestCommand(cmd) {
		// Also before config loading: these three are the manifest libraries
		// themselves, and they read nothing but the files named on the
		// command line. Their logger comes from the flags alone, since there
		// is no config file behind them to take a level or a format from.
		log := newLogger(orDefault(*o.logLevel, "info"), orDefault(*o.logFormat, "pretty"), stdout)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if cmd == cmdScanner {
			if app.ScanManifests(ctx, app.ScanOptions{
				Root: *o.root, Dir: inv.dir, RootOnly: *o.scanRootOnly, Strict: *o.strict,
				JSON: *o.logFormat == "json", Out: stdout, Log: log,
			}) != nil {
				return 1
			}
			return 0
		}
		if cmd == cmdReplacer {
			subs, err := parseSubSpecs(*o.rpSub)
			if err != nil {
				bootLog.Error().Err(err).Msg("invalid substitution")
				return 2
			}
			if len(subs) == 0 {
				bootLog.Error().Msg("replacer needs something to write: --sub 'find=>write'")
				usageForCommand(cmd)
				return 2
			}
			if app.SubstituteFiles(ctx, app.SubstituteOptions{
				Root: *o.root, Paths: inv.paths, Subs: subs, Strict: *o.strict,
				JSON: *o.logFormat == "json", Out: stdout, Log: log,
			}) != nil {
				return 1
			}
			return 0
		}
		edits, repls, err := parseEditSpecs(*o.wrSet, *o.wrReplace)
		if err != nil {
			bootLog.Error().Err(err).Msg("invalid edit")
			return 2
		}
		if *o.wrSetVersion == "" && len(edits) == 0 && len(repls) == 0 {
			bootLog.Error().Msg("writer needs something to write: --set-version, --set or --replace")
			usageForCommand(cmd)
			return 2
		}
		if app.WriteManifests(ctx, app.WriteOptions{
			Root: *o.root, Paths: inv.paths, Version: *o.wrSetVersion,
			Edits: edits, Replacements: repls, Strict: *o.strict,
			JSON: *o.logFormat == "json", Out: stdout, Log: log,
		}) != nil {
			return 1
		}
		return 0
	}

	// An explicit --config is used as-is; the default falls back through the
	// known config file names — in --root first, then ascending its parents,
	// so the CLI works from inside a package folder with the config's own
	// directory as the effective monorepo root. `dispat init --format yaml`
	// and a plain `dispat status` compose without flags either way.
	cfgPath, resolvedRoot, err := config.ResolveFile(*o.root, *o.cfgName, fs.Changed("config"))
	if err != nil {
		bootLog.Error().Err(err).Msg("config file not found")
		return 1
	}
	cfg, err := config.Load(cfgPath, fs)
	if err != nil {
		bootLog.Error().Err(err).Msg("invalid configuration")
		return 1
	}
	if fs.Changed("quiet-parser") {
		// The config states the repository's habit; the flag states this
		// invocation's, in both directions — --quiet-parser=false brings the
		// parser's findings back for one run without editing the config.
		cfg.Parser.Quiet = *o.quietParser
	}
	log := newLogger(cfg.LogLevel, cfg.LogFormat, stdout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The one selection every package-selecting command shares. Dir is --root
	// as the user spelled it — where they stood — and not the resolved
	// monorepo root the ascent just found: the difference between the two is
	// exactly what narrows a command to the folder it was invoked from.
	sel := filter.Filter{Packages: *o.pkgFilter, Spaces: *o.spaceFilter, Dir: *o.root}

	// The application does the work and logs its own findings; the controller
	// only maps the outcome onto an exit code.
	a := app.New(resolvedRoot, cfg, log)
	switch cmd {
	case cmdStatus:
		if a.Status(ctx) != nil {
			return 1
		}
	case cmdRun:
		if a.RunScript(ctx, runScript, app.RunOptions{OnError: *o.onError,
			Filter: sel, Since: *o.since, Consumers: *o.consumers}) != nil {
			return 1
		}
	case cmdPreview:
		res, err := a.Preview(ctx, sel)
		if err != nil {
			return 1
		}
		switch {
		case res.Notes != "":
			fmt.Fprint(stdout, res.Notes)
		case res.Scope == "":
			fmt.Fprintln(stdout, "no pending changes")
		default:
			fmt.Fprintf(stdout, "no pending changes for %s\n", res.Scope)
		}
	case cmdChangelog:
		if a.Changelog(ctx, app.ChangelogOptions{Filter: sel,
			File: *o.clFile, Title: *o.clTitle, DateFormat: *o.clDateFormat}) != nil {
			return 1
		}
	case cmdAutoversion:
		opts := app.AutoVersionOptions{Filter: sel,
			Range: *o.avRange, Match: *o.avMatch, SyncLock: *o.avSyncLock, NoReplace: *o.avNoReplace}
		switch *o.avManifests {
		case "", "root", "all", "none":
			opts.Manifests = *o.avManifests
		default:
			bootLog.Error().Str("manifests", *o.avManifests).
				Msg("unknown --manifests value (want root, all or none)")
			return 2
		}
		if fs.Changed("write-version") {
			opts.WriteVersion = o.avWriteVersion
		}
		if a.AutoVersion(ctx, opts) != nil {
			return 1
		}
	case cmdCommit:
		if a.Commit(ctx, app.CommitOptions{Filter: sel,
			Tag: *o.commitTag, Push: *o.commitPush, Name: *o.commitName, Email: *o.commitEmail,
			Remote: *o.commitRemote, Message: *o.commitMessage, Include: *o.commitInclude}) != nil {
			return 1
		}
	case cmdGithub:
		if a.GitHub(ctx, app.GitHubOptions{Filter: sel, Owner: *o.ghOwner, Repo: *o.ghRepo,
			APIURL: *o.ghAPIURL, TokenEnv: *o.ghTokenEnv, Target: *o.ghTarget}) != nil {
			return 1
		}
	case cmdCompute:
		open, err := a.Compute(ctx, cfgPath, app.ComputeOptions{
			Write:       *o.computeWrite,
			Interactive: *o.computeInteractive,
			Check:       *o.computeCheck,
			Filter:      sel,
			In:          os.Stdin,
			Out:         stdout,
		})
		if err != nil {
			return 1
		}
		if *o.computeCheck && open > 0 {
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
	script string   // run: the script name
	dir    string   // scanner: the optional folder to scan
	paths  []string // writer and replacer: the files to edit
}

// parseInvocation maps the positional arguments onto a command, validating
// each command's arity — all before any config is loaded, so a usage mistake
// costs nothing. bad reports an unusable command line (the usage exit, 2),
// already logged and, where it helps, followed by the usage text.
func parseInvocation(rest []string, usage func(string), log zerolog.Logger) (inv invocation, bad bool) {
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
		if len(rest) != 2 {
			log.Error().Msg("run requires exactly one argument: the script name (select packages with --package or --space)")
			usage(inv.cmd)
			return inv, true
		}
		inv.script = rest[1]
	case cmdPreview, cmdChangelog, cmdAutoversion, cmdCommit, cmdGithub:
		if len(rest) > 1 {
			log.Error().Strs("args", rest[1:]).
				Msgf("%s takes no arguments (select packages with --package or --space)", inv.cmd)
			usage(inv.cmd)
			return inv, true
		}
	case cmdScanner:
		if len(rest) > 2 {
			log.Error().Msg("scanner takes at most one argument: the folder to scan")
			usage(inv.cmd)
			return inv, true
		}
		if len(rest) == 2 {
			inv.dir = rest[1] // no argument: scan --root itself
		}
	case cmdWriter:
		if len(rest) < 2 {
			log.Error().Msg("writer requires at least one manifest file to edit")
			usage(inv.cmd)
			return inv, true
		}
		inv.paths = rest[1:]
	case cmdReplacer:
		if len(rest) < 2 {
			log.Error().Msg("replacer requires at least one file to edit")
			usage(inv.cmd)
			return inv, true
		}
		inv.paths = rest[1:]
	default:
		// Not a command name: treat the word as a script, so `dispat lint` is
		// `dispat run lint`. A name nothing defines still fails cleanly
		// later, which also catches command typos.
		if len(rest) > 1 {
			log.Error().Strs("args", rest[1:]).Msg("unexpected arguments")
			usage(cmdRun) // the shorthand's own help, not the program's
			return inv, true
		}
		inv.script, inv.cmd = inv.cmd, cmdRun
	}
	return inv, false
}

// commandWord answers which command's help an invocation is asking for,
// without validating anything: the command word when the first argument is
// one, cmdRun when it is the run shorthand, and "" when there is no
// positional argument at all, which is a request for the program help.
func commandWord(rest []string) string {
	if len(rest) == 0 {
		return ""
	}
	if _, ok := lookupCommand(rest[0]); ok {
		return rest[0]
	}
	return cmdRun
}

// orDefault answers with fallback when the flag was left at its empty
// "take it from the config" default and there is no config to take it from.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
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
