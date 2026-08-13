package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"

	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/app"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

// runner carries what one invocation's phases share: the parsed command line,
// where output goes, the bootstrap logger every pre-config refusal uses, and
// the update check that outlives the command it ran beside.
//
// The phases below are ordered by how much they need to know, and each one
// runs only once everything before it has agreed there is a command to run.
// The flags decide first, because a usage mistake must never cost the reader
// a configuration error first; then the commands that read no config file;
// then the config; then everything that needs it. Splitting them apart is
// what makes that order visible instead of implied by a thousand lines of
// sequence.
type runner struct {
	fs     *pflag.FlagSet
	o      *options
	inv    invocation
	stdout io.Writer
	stderr io.Writer
	boot   zerolog.Logger

	// checkCtx bounds the background update check; update is Run's, so a
	// phase that starts the check writes through the pointer and Run's
	// deferred print still sees it.
	checkCtx context.Context
	update   *notice

	// What the flag-only phase parsed for the commands that asked for it.
	write    writeRequest
	subs     []writer.Substitution
	execOpts app.ExecOptions
}

// usage prints one command's help on the error stream.
func (r *runner) usage(cmd string) { printCommandUsage(r.stderr, r.fs, cmd) }

// signalCtx is the interruptible context every command that does real work
// runs under. The caller defers the returned stop.
func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// logger builds the run logger from the flags alone, for the commands that
// never reach a config file to take a level or a format from.
func (r *runner) logger() zerolog.Logger {
	return newLogger(orDefault(*r.o.logLevel, "info"), orDefault(*r.o.logFormat, "pretty"), r.stdout)
}

// versionOrHelp answers the two questions that must be answerable without a
// config file, a git repository or a valid command line.
func (r *runner) versionOrHelp() (int, bool) {
	if *r.o.showVersion {
		*r.update = startUpdateCheck(r.checkCtx, r.o, r.fs, orDefault(*r.o.logFormat, "pretty"), true)
		r.update.status = true
		fmt.Fprintln(r.stdout, logo)
		fmt.Fprintln(r.stdout, "\n"+versionLine())
		return 0, true
	}
	if *r.o.showHelp {
		// Before the arity checks: `dispat writer --help` should help rather
		// than complain that it was given no manifests. With no command word
		// at all, the program help.
		if word := commandWord(r.fs.Args()); word != "" {
			r.usage(word)
		} else {
			r.fs.Usage()
		}
		return 0, true
	}
	return 0, false
}

// validateFlags is everything the flags alone decide, before any config is
// loaded, so a usage mistake never first costs the user a config error. It
// fills in write and subs for the commands whose flags carry a request.
func (r *runner) validateFlags() (int, bool) {
	cmd := r.inv.cmd
	if sweepCommand(cmd) && !app.ValidOnError(*r.o.onError) {
		r.boot.Error().Str("on-error", *r.o.onError).Msgf("unknown --on-error value (want %q or %q)",
			app.OnErrorSkip, app.OnErrorContinue)
		return 2, true
	}
	if cmd == cmdAutoversion || cmd == cmdAutowriter {
		if !validManifestScope(*r.o.avManifests, cmd == cmdAutoversion) {
			r.boot.Error().Str("manifests", *r.o.avManifests).
				Msg(manifestScopeHint(cmd == cmdAutoversion))
			return 2, true
		}
	}
	if cmd == cmdAutowriter && *r.o.wrLinkLocal && *r.o.wrUnlinkLocal {
		// One walk cannot both write and remove the same directive, and
		// guessing which was meant is worse than saying so.
		r.boot.Error().Msg("--link-local and --unlink-local ask for opposite things; pick one")
		r.usage(cmd)
		return 2, true
	}
	if cmd == cmdSelfUpdate && *r.o.suRollback {
		// A rollback downloads nothing, so every flag that chooses something
		// to download contradicts it.
		for _, name := range []string{"release", "prerelease", "force"} {
			if r.fs.Changed(name) {
				r.boot.Error().Msgf("--rollback restores the kept binary and downloads nothing, so --%s means nothing beside it", name)
				r.usage(cmd)
				return 2, true
			}
		}
	}
	if cmd == cmdWriter || cmd == cmdAutowriter {
		var ok bool
		if r.write, ok = parseWriteRequest(cmd, r.o, r.usage, r.boot); !ok {
			return 2, true
		}
	}
	if cmd == cmdAutoreplacer {
		var err error
		if r.subs, err = parseSubSpecs(*r.o.rpSub); err != nil {
			r.boot.Error().Err(err).Msg("invalid substitution")
			return 2, true
		}
		if len(r.subs) == 0 {
			r.boot.Error().Msg("autoreplacer needs something to write: --sub 'find=>write'")
			r.usage(cmd)
			return 2, true
		}
		if len(*r.o.rpFiles) == 0 {
			// A rule with no globs selects nothing, and writing nothing
			// silently is how a typo hides.
			r.boot.Error().Msg("autoreplacer needs files to look in: --files '**/*.gradle'")
			r.usage(cmd)
			return 2, true
		}
	}
	return 0, false
}

// runIf performs the `if` command, which reads no config file and starts no
// update check: a condition is about the environment, not about the
// repository it is standing in, and this is glue that may run dozens of times
// in one script, where a GitHub request per call and a notice on stdout would
// both be wrong.
func (r *runner) runIf() (int, bool) {
	if r.inv.cmd != cmdIf {
		return 0, false
	}
	branches, ok := parseBranches(r.inv.cond, r.o, r.usage, r.boot)
	if !ok {
		return 2, true
	}
	ctx, stop := signalCtx()
	defer stop()
	code, err := app.RunIf(ctx, app.IfOptions{
		Branches: branches, Else: *r.o.ifElse, OnFailure: *r.o.onFailure,
		Lookup: os.Getenv, Dir: *r.o.root,
		Stdout: r.stdout, Stderr: r.stderr, Log: r.logger(),
	})
	if err != nil {
		return 1, true
	}
	return code, true
}

// prepareExec validates exec's flags and builds its options. The command
// itself needs the config, so it runs later; only its usage checks belong
// here, for the same reason every other command's do.
func (r *runner) prepareExec() (int, bool) {
	if r.inv.cmd != cmdExec {
		return 0, false
	}
	subj, ok := execSubject(r.o, r.usage, r.boot)
	if !ok {
		return 2, true
	}
	from, ok := execScriptFrom(r.o, r.boot)
	if !ok {
		return 2, true
	}
	if !checkExecEnv(r.o, subj, r.usage, r.boot) {
		return 2, true
	}
	r.execOpts = app.ExecOptions{
		Script: r.inv.script, Subject: subj, ScriptFrom: from,
		Fallback: *r.o.execFallback, Env: *r.o.execEnv, OnFailure: *r.o.onFailure,
		Args: r.inv.args, Dir: *r.o.root, Stdout: r.stdout, Stderr: r.stderr,
	}
	return 0, false
}

// runPreConfig performs the commands that read no config file: init, because
// it is what creates one; self-update, because it is about the binary rather
// than any repository it might be standing in; and the three manifest
// commands, which read nothing but the files named on the command line.
func (r *runner) runPreConfig() (int, bool) {
	cmd := r.inv.cmd
	if cmd == cmdInit || manifestCommand(cmd) {
		// These have no updateCheck option to consult, so the environment
		// variable is the whole of their opt-out. self-update is left out on
		// purpose: it reports the answer itself.
		*r.update = startUpdateCheck(r.checkCtx, r.o, r.fs, orDefault(*r.o.logFormat, "pretty"), true)
	}
	switch {
	case cmd == cmdInit:
		return r.runInit(), true
	case cmd == cmdSelfUpdate:
		return r.runSelfUpdate(), true
	case manifestCommand(cmd):
		return r.runManifests(), true
	}
	return 0, false
}

func (r *runner) runInit() int {
	name, err := app.InitConfig(*r.o.root, *r.o.initFormat)
	if err != nil {
		r.boot.Error().Err(err).Msg("init failed")
		return 1
	}
	fmt.Fprintf(r.stdout, "created %s\n", name)
	return 0
}

func (r *runner) runSelfUpdate() int {
	format := orDefault(*r.o.logFormat, "pretty")
	log := newLogger(orDefault(*r.o.logLevel, "info"), format, r.stdout)
	ctx, stop := signalCtx()
	defer stop()
	src := updateSource(r.o, r.fs)
	src.Prerelease = *r.o.suPrerelease
	src.Log = log
	pending, err := app.SelfUpdate(ctx, app.SelfUpdateOptions{
		Build: selfupdate.Describe(Version), Source: src, Release: *r.o.suRelease,
		Check: *r.o.check, Force: *r.o.suForce, Rollback: *r.o.suRollback,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		JSON: format == "json", Out: r.stdout, Log: log,
	})
	if err != nil {
		return 1
	}
	// --check exits 1 when the same invocation without it would change the
	// binary, which is the gate a CI job puts in front of an update.
	if *r.o.check && pending {
		return 1
	}
	return 0
}

// runManifests performs scanner, replacer and writer: the manifest libraries
// themselves, pointed at the files named on the command line. Their logger
// comes from the flags alone, since there is no config file behind them.
func (r *runner) runManifests() int {
	log := r.logger()
	ctx, stop := signalCtx()
	defer stop()
	switch r.inv.cmd {
	case cmdScanner:
		if app.ScanManifests(ctx, app.ScanOptions{
			Root: *r.o.root, Dir: r.inv.dir, RootOnly: *r.o.scanRootOnly, Strict: *r.o.strict,
			JSON: *r.o.logFormat == "json", Out: r.stdout, Log: log,
		}) != nil {
			return 1
		}
	case cmdReplacer:
		subs, err := parseSubSpecs(*r.o.rpSub)
		if err != nil {
			r.boot.Error().Err(err).Msg("invalid substitution")
			return 2
		}
		if len(subs) == 0 {
			r.boot.Error().Msg("replacer needs something to write: --sub 'find=>write'")
			r.usage(r.inv.cmd)
			return 2
		}
		if app.SubstituteFiles(ctx, app.SubstituteOptions{
			Root: *r.o.root, Paths: r.inv.paths, Subs: subs, Strict: *r.o.strict,
			JSON: *r.o.logFormat == "json", Out: r.stdout, Log: log,
		}) != nil {
			return 1
		}
	default:
		if app.WriteManifests(ctx, app.WriteOptions{
			Root: *r.o.root, Paths: r.inv.paths, Version: r.write.version,
			Edits: r.write.edits, Links: r.write.links, Strict: *r.o.strict,
			JSON: *r.o.logFormat == "json", Out: r.stdout, Log: log,
		}) != nil {
			return 1
		}
	}
	return 0
}

// runConfigured loads the configuration and performs everything that needs
// it: exec straight after the load, and every package-selecting command once
// the selection and the application are built.
func (r *runner) runConfigured() int {
	// An explicit --config is used as-is; the default falls back through the
	// known config file names: in --root first, then ascending its parents, so
	// the CLI works from inside a package folder with the config's own
	// directory as the effective monorepo root. `dispat init --format yaml`
	// and a plain `dispat status` compose without flags either way.
	cfgPath, resolvedRoot, err := config.ResolveFile(*r.o.root, *r.o.cfgName, r.fs.Changed("config"))
	if err != nil {
		r.boot.Error().Err(err).Msg("config file not found")
		return 1
	}
	cfg, err := config.Load(cfgPath, r.fs)
	if err != nil {
		r.boot.Error().Err(err).Msg("invalid configuration")
		return 1
	}
	if r.fs.Changed("quiet-parser") {
		// The config states the repository's habit; the flag states this
		// invocation's, in both directions: --quiet-parser=false brings the
		// parser's findings back for one run without editing the config.
		cfg.Parser.Quiet = *r.o.quietParser
	}
	log := newLogger(cfg.LogLevel, cfg.LogFormat, r.stdout)
	// The first thing worth knowing about any run is which file it read and
	// which folder it decided was the monorepo root, because both are inferred
	// when no flag names them and "it ran with the wrong config" looks exactly
	// like a configuration bug until you can see them. Logged here rather than
	// at resolution because this is the first moment the configured level is
	// known.
	log.Debug().
		Str("config", cfgPath).
		Str("root", resolvedRoot).
		Bool("explicitConfig", r.fs.Changed("config")).
		Int("spaces", len(cfg.Spaces)).
		Int("packages", len(cfg.Packages)).
		Msg("configuration loaded")

	if r.inv.cmd == cmdExec {
		// Straight after the config, which is all it needs: no plan unless
		// --env asked for one, and no update check, for the same reason as if.
		ctx, stop := signalCtx()
		defer stop()
		code, err := app.New(resolvedRoot, cfg, log).Exec(ctx, r.execOpts)
		if err != nil {
			return 1
		}
		return code
	}
	// Now that the configuration has spoken, the check can start: a run that
	// switched it off must make no request at all, which means not making one
	// before the option has been read.
	*r.update = startUpdateCheck(r.checkCtx, r.o, r.fs, cfg.LogFormat, cfg.UpdateCheckEnabled())

	ctx, stop := signalCtx()
	defer stop()
	return r.dispatch(ctx, cfg, resolvedRoot, cfgPath, log)
}

// dispatch runs the selected package-selecting command. The application does
// the work and logs its own findings; the controller only maps the outcome
// onto an exit code.
func (r *runner) dispatch(ctx context.Context, cfg *config.File, root, cfgPath string, log zerolog.Logger) int {
	o := r.o
	// The one selection every package-selecting command shares. Dir is --root
	// as the user spelled it, where they stood, and not the resolved monorepo
	// root the ascent just found: the difference between the two is exactly
	// what narrows a command to the folder it was invoked from.
	sel := filter.Filter{Packages: *o.pkgFilter, Spaces: *o.spaceFilter, Groups: *o.groupFilter, Dir: *o.root}
	// The one window every sweeping command shares: that selection, the
	// revision the run counts changes from, and the downstream expansion.
	window := app.WindowOptions{Filter: sel, Since: *o.since, Consumers: *o.consumers}

	a := app.New(root, cfg, log)
	// The release's own options, shared by the command that performs it and
	// the command that shows it in advance.
	relOpts := app.ReleaseOptions{Filter: sel, Strict: *o.strict, RequireRelease: *o.requireRelease}
	switch r.inv.cmd {
	case cmdStatus:
		if a.Status(ctx, relOpts) != nil {
			return 1
		}
	case cmdRun:
		if a.RunScript(ctx, r.inv.script, app.RunOptions{OnError: *o.onError, Window: window, Args: r.inv.args}) != nil {
			return 1
		}
	case cmdPreview:
		res, err := a.Preview(ctx, sel)
		if err != nil {
			return 1
		}
		switch {
		case res.Notes != "":
			fmt.Fprint(r.stdout, res.Notes)
		case res.Scope == "":
			fmt.Fprintln(r.stdout, "no pending changes")
		default:
			fmt.Fprintf(r.stdout, "no pending changes for %s\n", res.Scope)
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
		if r.fs.Changed("write-version") {
			opts.WriteVersion = o.avWriteVersion
		}
		if a.AutoVersion(ctx, opts) != nil {
			return 1
		}
	case cmdAutowriter:
		if a.AutoWriter(ctx, app.AutoWriterOptions{
			Window: window, OnError: *o.onError, Version: r.write.version,
			Edits: r.write.edits, Links: r.write.links, Manifests: *o.avManifests,
			SetLocal: *o.wrSetLocal, Range: *o.avRange,
			LinkLocal: *o.wrLinkLocal, UnlinkLocal: *o.wrUnlinkLocal,
			OnlyUpdated: *o.onlyUpdated, SyncLock: *o.avSyncLock, Strict: *o.strict,
			JSON: cfg.LogFormat == "json", Out: r.stdout,
		}) != nil {
			return 1
		}
	case cmdAutoreplacer:
		if a.AutoReplacer(ctx, app.AutoReplacerOptions{
			Window: window, OnError: *o.onError,
			Subs: r.subs, Files: *o.rpFiles,
			OnlyUpdated: *o.onlyUpdated, Strict: *o.strict,
			JSON: cfg.LogFormat == "json", Out: r.stdout,
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
			Out:         r.stdout,
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
