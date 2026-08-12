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
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"

	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/app"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

// Commands accepted by Run.
const (
	cmdRelease = "release" // build and publish changed packages (default)
	cmdStatus  = "status"  // only print the graph and new versions
	cmdRun     = "run"     // run a script inside each changed package that has it
	cmdInit    = "init"    // write a starter config file; needs no config or git
	cmdPreview = "preview" // print one package's pending release notes
	cmdCompute = "compute" // derive the graph and the baselines from manifests

	// The shell helpers, which run one script rather than sweeping a
	// selection: a condition picks the script for one, the configuration
	// names it for the other. Both propagate the script's own exit code.
	cmdIf   = "if"   // run one of several scripts, chosen by an env condition
	cmdExec = "exec" // run one declared script here, for a named subject

	// The standalone step commands, exposing the release pipeline's native
	// steps to custom flows. Like every command word (and unlike --version),
	// each permanently shadows a run script of the same name: `dispat
	// changelog` is never `dispat run changelog`.
	cmdChangelog      = "changelog"      // write pending changelog entries now
	cmdAutoversion    = "autoversion"    // native manifest reconciliation, plus syncLock
	cmdAutowriter     = "autowriter"     // the writer's edits, over the whole selection
	cmdAutosubstitute = "autosubstitute" // literal substitutions, over the whole selection
	cmdCommit         = "commit"         // per-package release commit (--tag, --push)
	cmdGithub         = "github"         // per-package GitHub release, published now

	// The manifest commands, exposing the pkg/scanner and pkg/writer
	// libraries directly. Like init, they need no config file and no git
	// repository: they only ever look at the files they are pointed at.
	cmdScanner  = "scanner"  // read what a folder's manifests declare
	cmdWriter   = "writer"   // edit manifests in place, format-preserving
	cmdReplacer = "replacer" // replace literal text in any file, no parsing

	// cmdSelfUpdate is about the binary rather than about any repository, so
	// like init and the manifest commands it needs no config and no git.
	cmdSelfUpdate = "self-update"
)

// manifestCommand reports the commands that need neither a config file nor a
// git repository: they read and write only the files named on the command
// line, which is what makes them usable on any checkout.
func manifestCommand(cmd string) bool {
	return cmd == cmdScanner || cmd == cmdWriter || cmd == cmdReplacer
}

// sweepCommand reports the commands that cover a set of packages and execute
// something in each of them, which is the group that reads --on-error, --since
// and --consumers.
func sweepCommand(cmd string) bool {
	switch cmd {
	case cmdRun, cmdAutowriter, cmdAutosubstitute, cmdChangelog, cmdAutoversion, cmdCommit, cmdGithub:
		return true
	}
	return false
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

// versionLine is what --version reports: the version, and the platform the
// binary was built for. The platform is there because "dispat 1.4.0" alone
// does not say which of the release's binaries is running, and that is the
// first thing a bug report needs. A binary the Go toolchain installed says so
// in the same parenthesis, because that is what decides how it is updated.
//
// The version itself is the ldflags-injected value when a release build set
// one, otherwise the module version the toolchain stamped in (`go install
// .../services/dispat@v1.2.3` records v1.2.3 with no ldflags involved),
// otherwise the "dev" of a plain local build; see selfupdate.Describe.
func versionLine() string {
	build := selfupdate.Describe(Version)
	return "dispat " + build.Version + " (" + build.Platform(runtime.GOOS, runtime.GOARCH) + ")"
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

	// Housekeeping, before anything else and on every invocation: the copy a
	// self-update kept is deleted once it is a week old. With no backup
	// present, which is every run outside that week, this is one stat.
	if exe, err := selfupdate.Executable(); err == nil {
		selfupdate.PruneBackup(exe, time.Now())
	}

	// The update check runs beside the command and is read on the way out.
	// Deferred in this order so the notice prints before the context that
	// carries it is cancelled.
	checkCtx, endCheck := context.WithCancel(context.Background())
	defer endCheck()
	var update notice
	defer func() { update.print(stdout) }()

	if *o.showVersion {
		// Before anything else: the version must print without a config file.
		update = startUpdateCheck(checkCtx, o, fs, orDefault(*o.logFormat, "pretty"), true)
		update.status = true
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

	inv, badArgs := parseInvocation(fs.Args(), fs.ArgsLenAtDash(), usageForCommand, bootLog)
	if badArgs {
		return 2
	}
	cmd, runScript := inv.cmd, inv.script
	if sweepCommand(cmd) && !app.ValidOnError(*o.onError) {
		bootLog.Error().Str("on-error", *o.onError).Msgf("unknown --on-error value (want %q or %q)",
			app.OnErrorSkip, app.OnErrorContinue)
		return 2
	}
	// The rest of what the flags alone decide, also before any config is
	// loaded: a usage mistake should not first cost the user a config error.
	if cmd == cmdAutoversion || cmd == cmdAutowriter {
		if !validManifestScope(*o.avManifests, cmd == cmdAutoversion) {
			bootLog.Error().Str("manifests", *o.avManifests).
				Msg(manifestScopeHint(cmd == cmdAutoversion))
			return 2
		}
	}
	if cmd == cmdAutowriter && *o.wrLinkLocal && *o.wrUnlinkLocal {
		// One walk cannot both write and remove the same directive, and
		// guessing which was meant is worse than saying so.
		bootLog.Error().Msg("--link-local and --unlink-local ask for opposite things; pick one")
		usageForCommand(cmd)
		return 2
	}
	if cmd == cmdSelfUpdate && *o.suRollback {
		// A rollback downloads nothing, so every flag that chooses something
		// to download contradicts it.
		for _, name := range []string{"release", "prerelease", "force"} {
			if fs.Changed(name) {
				bootLog.Error().Msgf("--rollback restores the kept binary and downloads nothing, so --%s means nothing beside it", name)
				usageForCommand(cmd)
				return 2
			}
		}
	}
	var write writeRequest
	if cmd == cmdWriter || cmd == cmdAutowriter {
		var ok bool
		if write, ok = parseWriteRequest(cmd, o, usageForCommand, bootLog); !ok {
			return 2
		}
	}
	var subs []writer.Substitution
	if cmd == cmdAutosubstitute {
		var err error
		if subs, err = parseSubSpecs(*o.rpSub); err != nil {
			bootLog.Error().Err(err).Msg("invalid substitution")
			return 2
		}
		if len(subs) == 0 {
			bootLog.Error().Msg("autosubstitute needs something to write: --sub 'find=>write'")
			usageForCommand(cmd)
			return 2
		}
		if len(*o.rpFiles) == 0 {
			// A rule with no globs selects nothing, and writing nothing
			// silently is how a typo hides.
			bootLog.Error().Msg("autosubstitute needs files to look in: --files '**/*.gradle'")
			usageForCommand(cmd)
			return 2
		}
	}
	if cmd == cmdIf {
		// Before config loading, and before the update check: a condition is
		// about the environment, not about the repository it is standing in,
		// and this is glue that may run dozens of times in one script — a
		// GitHub request per call and a notice on stdout would both be wrong.
		branches, ok := parseBranches(inv.cond, o, usageForCommand, bootLog)
		if !ok {
			return 2
		}
		log := newLogger(orDefault(*o.logLevel, "info"), orDefault(*o.logFormat, "pretty"), stdout)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		code, err := app.RunIf(ctx, app.IfOptions{
			Branches: branches, Else: *o.ifElse, OnFailure: *o.onFailure,
			Lookup: os.Getenv, Dir: *o.root,
			Stdout: stdout, Stderr: stderr, Log: log,
		})
		if err != nil {
			return 1
		}
		return code
	}
	var execOpts app.ExecOptions
	if cmd == cmdExec {
		// The flags alone, before any config is read, so a usage mistake never
		// first costs a config error.
		subj, ok := execSubject(o, usageForCommand, bootLog)
		if !ok {
			return 2
		}
		from, ok := execScriptFrom(o, bootLog)
		if !ok {
			return 2
		}
		if !checkExecEnv(o, subj, usageForCommand, bootLog) {
			return 2
		}
		execOpts = app.ExecOptions{
			Script: inv.script, Subject: subj, ScriptFrom: from,
			Fallback: *o.execFallback, Env: *o.execEnv, OnFailure: *o.onFailure,
			Args: inv.args, Dir: *o.root, Stdout: stdout, Stderr: stderr,
		}
	}
	if cmd == cmdInit || manifestCommand(cmd) {
		// The commands that read no config file have no updateCheck option to
		// consult, so the environment variable is the whole of their opt-out.
		// self-update is left out on purpose: it reports the answer itself.
		update = startUpdateCheck(checkCtx, o, fs, orDefault(*o.logFormat, "pretty"), true)
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
	if cmd == cmdSelfUpdate {
		// Before config loading too, and for the same reason as init: this
		// command is about the binary, not about any repository it might be
		// standing in.
		format := orDefault(*o.logFormat, "pretty")
		log := newLogger(orDefault(*o.logLevel, "info"), format, stdout)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		src := updateSource(o, fs)
		src.Prerelease = *o.suPrerelease
		src.Log = log
		pending, err := app.SelfUpdate(ctx, app.SelfUpdateOptions{
			Build: selfupdate.Describe(Version), Source: src, Release: *o.suRelease,
			Check: *o.check, Force: *o.suForce, Rollback: *o.suRollback,
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
			JSON: format == "json", Out: stdout, Log: log,
		})
		if err != nil {
			return 1
		}
		// --check exits 1 when the same invocation without it would change
		// the binary, which is the gate a CI job puts in front of an update.
		if *o.check && pending {
			return 1
		}
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
		if app.WriteManifests(ctx, app.WriteOptions{
			Root: *o.root, Paths: inv.paths, Version: write.version,
			Edits: write.edits, Links: write.links, Strict: *o.strict,
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
	// The first thing worth knowing about any run is which file it read and
	// which folder it decided was the monorepo root, because both are inferred
	// when no flag names them and "it ran with the wrong config" looks exactly
	// like a configuration bug until you can see them. Logged here rather than
	// at resolution because this is the first moment the configured level is
	// known.
	log.Debug().
		Str("config", cfgPath).
		Str("root", resolvedRoot).
		Bool("explicitConfig", fs.Changed("config")).
		Int("spaces", len(cfg.Spaces)).
		Int("packages", len(cfg.Packages)).
		Msg("configuration loaded")
	if cmd == cmdExec {
		// Straight after the config, which is all it needs: no plan unless
		// --env asked for one, and no update check, for the same reason as if.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		code, err := app.New(resolvedRoot, cfg, log).Exec(ctx, execOpts)
		if err != nil {
			return 1
		}
		return code
	}
	// Now that the configuration has spoken, the check can start: a run that
	// switched it off must make no request at all, which means not making one
	// before the option has been read.
	update = startUpdateCheck(checkCtx, o, fs, cfg.LogFormat, cfg.UpdateCheckEnabled())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The one selection every package-selecting command shares. Dir is --root
	// as the user spelled it — where they stood — and not the resolved
	// monorepo root the ascent just found: the difference between the two is
	// exactly what narrows a command to the folder it was invoked from.
	sel := filter.Filter{Packages: *o.pkgFilter, Spaces: *o.spaceFilter, Groups: *o.groupFilter, Dir: *o.root}
	// The one window every sweeping command shares: that selection, the
	// revision the run counts changes from, and the downstream expansion.
	window := app.WindowOptions{Filter: sel, Since: *o.since, Consumers: *o.consumers}

	// The application does the work and logs its own findings; the controller
	// only maps the outcome onto an exit code.
	a := app.New(resolvedRoot, cfg, log)
	// The release's own options, shared by the command that performs it and
	// the command that shows it in advance.
	relOpts := app.ReleaseOptions{Filter: sel, Strict: *o.strict}
	switch cmd {
	case cmdStatus:
		if a.Status(ctx, relOpts) != nil {
			return 1
		}
	case cmdRun:
		if a.RunScript(ctx, runScript, app.RunOptions{OnError: *o.onError, Window: window, Args: inv.args}) != nil {
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
		if a.Changelog(ctx, app.ChangelogOptions{Window: window, OnError: *o.onError,
			File: *o.clFile, FileTitle: *o.clFileTitle, DateFormat: *o.clDateFormat,
			ReleaseName: *o.releaseName}) != nil {
			return 1
		}
	case cmdAutoversion:
		opts := app.AutoVersionOptions{Window: window, OnError: *o.onError,
			Range: *o.avRange, Match: *o.avMatch, Manifests: *o.avManifests,
			OnlyUpdated: *o.onlyUpdated, SyncLock: *o.avSyncLock, NoReplace: *o.avNoReplace}
		if fs.Changed("write-version") {
			opts.WriteVersion = o.avWriteVersion
		}
		if a.AutoVersion(ctx, opts) != nil {
			return 1
		}
	case cmdAutowriter:
		if a.AutoWriter(ctx, app.AutoWriterOptions{
			Window: window, OnError: *o.onError, Version: write.version,
			Edits: write.edits, Links: write.links, Manifests: *o.avManifests,
			SetLocal: *o.wrSetLocal, Range: *o.avRange,
			LinkLocal: *o.wrLinkLocal, UnlinkLocal: *o.wrUnlinkLocal,
			OnlyUpdated: *o.onlyUpdated, SyncLock: *o.avSyncLock, Strict: *o.strict,
			JSON: cfg.LogFormat == "json", Out: stdout,
		}) != nil {
			return 1
		}
	case cmdAutosubstitute:
		if a.AutoSubstitute(ctx, app.AutoSubstituteOptions{
			Window: window, OnError: *o.onError,
			Subs: subs, Files: *o.rpFiles,
			OnlyUpdated: *o.onlyUpdated, Strict: *o.strict,
			JSON: cfg.LogFormat == "json", Out: stdout,
		}) != nil {
			return 1
		}
	case cmdCommit:
		if a.Commit(ctx, app.CommitOptions{Window: window, OnError: *o.onError,
			Tag: *o.commitTag, Push: *o.commitPush, Name: *o.commitName, Email: *o.commitEmail,
			Remote: *o.commitRemote, Message: *o.commitMessage, TagName: *o.commitTagName,
			NoForce: *o.commitNoForce,
			Include: *o.commitInclude}) != nil {
			return 1
		}
	case cmdGithub:
		if a.GitHub(ctx, app.GitHubOptions{Window: window, OnError: *o.onError,
			Owner: *o.ghOwner, Repo: *o.ghRepo,
			APIURL: *o.ghAPIURL, TokenEnv: *o.ghTokenEnv, Target: *o.ghTarget,
			ReleaseName: *o.releaseName}) != nil {
			return 1
		}
	case cmdCompute:
		open, err := a.Compute(ctx, cfgPath, app.ComputeOptions{
			Write:       *o.computeWrite,
			Interactive: *o.computeInteractive,
			Check:       *o.check,
			Filter:      sel,
			In:          os.Stdin,
			Out:         stdout,
		})
		if err != nil {
			return 1
		}
		if *o.check && open > 0 {
			return 1
		}
	default:
		if _, err := a.Release(ctx, relOpts); err != nil {
			return 1
		}
	}
	return 0
}

// invocation is the parsed command line: which command runs and its
// positional arguments.
type invocation struct {
	cmd    string
	script string   // run and exec: the script name
	cond   string   // if: the leading condition
	dir    string   // scanner: the optional folder to scan
	paths  []string // writer and replacer: the files to edit
	args   []string // run and exec: what followed `--`, for the script
}

// parseInvocation maps the positional arguments onto a command, validating
// each command's arity — all before any config is loaded, so a usage mistake
// costs nothing. bad reports an unusable command line (the usage exit, 2),
// already logged and, where it helps, followed by the usage text.
//
// dash is pflag's ArgsLenAtDash: -1 when no `--` was typed, otherwise the
// index in rest where the arguments after it begin. Only `run` and `exec`
// forward those to their script; for every other command a `--` is a mistake
// worth naming rather than a list to ignore.
//
// Splitting on the dash before the arity checks is what lets both rules hold
// at once. The checks below still see positional arguments alone, so
// `dispat run lint core` remains the usage error it has always been — the
// selection is a flag — while `dispat run lint -- core` hands `core` to the
// script, because the operator said which one they meant.
func parseInvocation(rest []string, dash int, usage func(string), log zerolog.Logger) (inv invocation, bad bool) {
	inv = invocation{cmd: cmdRelease}
	var forwarded []string
	if dash >= 0 && dash <= len(rest) {
		forwarded, rest = rest[dash:], rest[:dash]
	}
	if len(rest) == 0 {
		if len(forwarded) > 0 {
			// `dispat -- something`: no command to forward to.
			log.Error().Strs("args", forwarded).
				Msg("arguments after `--` need a command that forwards them: run or exec")
			return inv, true
		}
		return inv, false
	}
	inv.cmd = rest[0]
	if len(forwarded) > 0 && !forwardsArgs(inv.cmd) {
		log.Error().Strs("args", forwarded).
			Msgf("%s does not forward arguments; only run and exec pass what follows `--` to a script", inv.cmd)
		usage(commandWord(rest))
		return inv, true
	}
	inv.args = forwarded
	switch inv.cmd {
	case cmdRelease, cmdStatus, cmdInit, cmdCompute, cmdSelfUpdate:
		if len(rest) > 1 {
			log.Error().Strs("args", rest[1:]).Msg("unexpected arguments")
			return inv, true
		}
	case cmdRun:
		if len(rest) != 2 {
			log.Error().Msg("run requires exactly one argument: the script name (select packages with --package, --space or --group; pass arguments to the script after `--`)")
			usage(inv.cmd)
			return inv, true
		}
		inv.script = rest[1]
	case cmdIf:
		if len(rest) != 2 {
			log.Error().Msg("if requires exactly one argument: the condition (NAME, !NAME, NAME=value, NAME!=value, NAME~glob or NAME!~glob)")
			usage(inv.cmd)
			return inv, true
		}
		inv.cond = rest[1]
	case cmdExec:
		if len(rest) != 2 {
			log.Error().Msg("exec requires exactly one argument: the script name (choose the subject with --for-package or --for-space; pass arguments to the script after `--`)")
			usage(inv.cmd)
			return inv, true
		}
		inv.script = rest[1]
	case cmdPreview, cmdChangelog, cmdAutoversion, cmdAutowriter, cmdAutosubstitute, cmdCommit, cmdGithub:
		if len(rest) > 1 {
			log.Error().Strs("args", rest[1:]).
				Msgf("%s takes no arguments (select packages with --package, --space or --group)", inv.cmd)
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

// forwardsArgs reports whether a command word passes what follows `--` to the
// script it runs.
//
// Only the two commands whose whole job is running one named script do: `run`
// (in both spellings, so an unknown word — the run shorthand — counts) and
// `exec`. `if` is deliberately not among them: its branches are shell text the
// operator already writes in full, so there is nothing a forwarded argument
// would reach that the branch could not say itself. Neither are the release
// commands, where an argument typed at the terminal has no business entering a
// pipeline whose record is a tag.
func forwardsArgs(cmd string) bool {
	if _, known := lookupCommand(cmd); !known {
		return true // the run shorthand: `dispat lint -- --fix`
	}
	return cmd == cmdRun || cmd == cmdExec
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

// validManifestScope reports whether the --manifests value is one the command
// accepts. Both commands that read the flag take "root" and "all"; only
// auto-versioning takes "none", because switching the parsing strategy off is
// how a space asks for its replace rules alone, and a command whose whole job
// is writing manifests has no such reading.
func validManifestScope(value string, allowNone bool) bool {
	switch value {
	case "", "root", "all":
		return true
	case "none":
		return allowNone
	}
	return false
}

// manifestScopeHint is the refusal, spelling out the scopes that command has.
func manifestScopeHint(allowNone bool) string {
	if allowNone {
		return "unknown --manifests value (want root, all or none)"
	}
	return "unknown --manifests value (want root or all)"
}

// writeRequest is the parsed --set-version/--set/--link trio, which
// `dispat writer` and `dispat autowriter` spell the same way and differ over
// only in which files they apply it to.
type writeRequest struct {
	version string
	edits   []writer.Edit
	links   []writer.Link
}

// parseWriteRequest reads the three flags into one request. A malformed spec
// and a request with nothing in it are both usage mistakes, reported here and
// answered with false.
//
// `dispat autowriter` can also derive its edits from the manifests, and an
// invocation asking for that has plenty to write without naming a single
// dependency, so the empty check lets those through.
func parseWriteRequest(cmd string, o *options, usage func(string), log zerolog.Logger) (writeRequest, bool) {
	edits, links, err := parseEditSpecs(*o.wrSet, *o.wrLink)
	if err != nil {
		log.Error().Err(err).Msg("invalid edit")
		return writeRequest{}, false
	}
	derived := cmd == cmdAutowriter && (*o.wrSetLocal || *o.wrLinkLocal || *o.wrUnlinkLocal)
	if *o.wrSetVersion == "" && len(edits) == 0 && len(links) == 0 && !derived {
		log.Error().Msgf("%s needs something to write: --set-version, --set or --link", cmd)
		usage(cmd)
		return writeRequest{}, false
	}
	return writeRequest{version: *o.wrSetVersion, edits: edits, links: links}, true
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
