package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"

	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/app"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/install"
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
	repository install.Repository
	write      writeRequest
	reps       []writer.Replacement
	execOpts   app.ExecOptions
	ifBranches []app.Branch
	ifIn       *app.Location
	ifFile     *fileTest
}

// fileTest is an if invocation whose leading condition is --file or --dir. The
// path is kept rather than answered in the flag phase because a relative path
// resolves where the chosen script runs, and that folder may take an --in, or
// even a configuration, to know.
type fileTest struct {
	path    string
	wantDir bool
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
// fills in write and reps for the commands whose flags carry a request.
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
	if cmd == cmdScanner {
		if *r.o.scVerifyUnlinked && *r.o.scVerifyLinked {
			// The gates assert opposite states of the same tree; both at once
			// can never pass.
			r.boot.Error().Msg("--verify-unlinked and --verify-linked ask for opposite things; pick one")
			r.usage(cmd)
			return 2, true
		}
		for _, forbid := range *r.o.scForbidRange {
			for _, require := range *r.o.scRequireRange {
				if forbid == require {
					r.boot.Error().Str("pattern", forbid).
						Msg("the same pattern cannot be forbidden and required at once")
					r.usage(cmd)
					return 2, true
				}
			}
		}
	}
	if cmd == cmdWriter && *r.o.wrDropLinks && len(*r.o.wrLink) > 0 {
		// One invocation cannot both place redirects and sweep them all away.
		r.boot.Error().Msg("--drop-links and --link ask for opposite things; pick one")
		r.usage(cmd)
		return 2, true
	}
	if cmd == cmdInstall {
		if code, done := r.validateInstall(); done {
			return code, true
		}
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
		if r.reps, err = parseReplaceSpecs(*r.o.rpReplace); err != nil {
			r.boot.Error().Err(err).Msg("invalid replacement")
			return 2, true
		}
		if len(r.reps) == 0 {
			r.boot.Error().Msg("autoreplacer needs something to write: --replace 'find=>write'")
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
// update check: a condition is about the environment or the filesystem, not
// about the repository it is standing in, and this is glue that may run dozens
// of times in one script, where a GitHub request per call and a notice on
// stdout would both be wrong.
//
// Two things can change that, and both are opt-in. An --in naming something a
// configuration defines (pkg:, space:, root) has to be looked up, and
// --changed asks about the repository itself, so either makes the phase defer
// and runConfigured performs the command once the file is loaded. A path or
// cwd is a folder the command line already spelled in full, and a file test is
// answered by the filesystem alone, so everything else still runs with nothing
// read. Asking about the repository costs finding out, and asking nothing
// still costs nothing.
func (r *runner) runIf() (int, bool) {
	if r.inv.cmd != cmdIf {
		return 0, false
	}
	lead, code := r.ifLeadCondition()
	if code != 0 {
		return code, true
	}
	branches, ok := parseBranches(lead, r.o, r.usage, r.boot)
	if !ok {
		return 2, true
	}
	in, ok := helperIn(r.o, r.boot)
	if !ok {
		return 2, true
	}
	r.ifBranches = branches
	r.ifIn = in
	if *r.o.ifChanged || (in != nil && in.Deferred()) {
		return 0, false
	}
	dir := *r.o.root
	if in != nil {
		// Neither kind left needs the configuration, so no App is built to
		// resolve them: a path and cwd are answered by the command line alone.
		var err error
		if dir, err = app.PlainDir(*in, dir); err != nil {
			r.boot.Error().Err(err).Msg("invalid --in")
			return 1, true
		}
	}
	return r.runIfIn(dir), true
}

// ifLeadCondition validates the flags that can carry if's leading condition
// and builds it. Exactly one source may speak — the positional condition,
// --changed, --file or --dir — because two answers to "what does the first
// --then guard" would leave one of them silently ignored. The non-zero exit
// code reports a refusal, already logged.
//
// --changed and the file tests return a placeholder resolved false: the real
// answer takes a folder, or a whole configuration, that this phase does not
// have yet. The placeholder keeps the chain's shape, so the pairing checks
// still run before anything is read.
func (r *runner) ifLeadCondition() (app.Condition, int) {
	sources := 0
	for _, set := range []bool{r.inv.condSet, *r.o.ifChanged, r.fs.Changed("file"), r.fs.Changed("dir")} {
		if set {
			sources++
		}
	}
	if sources == 0 {
		r.boot.Error().Msg("if requires a condition: NAME, !NAME, NAME=value, NAME!=value, NAME~glob, NAME!~glob, --changed, or a file test (--file/-f, --dir/-d)")
		r.usage(cmdIf)
		return app.Condition{}, 2
	}
	if sources > 1 {
		r.boot.Error().Msg("if takes one leading condition; pick one of the positional condition, --changed, --file and --dir")
		r.usage(cmdIf)
		return app.Condition{}, 2
	}
	if !*r.o.ifChanged {
		// The window flags describe the --changed selection; beside any other
		// condition they would be silently ignored, which is how a gate that
		// never fires gets written.
		for _, name := range []string{"package", "space", "group", "since", "consumers"} {
			if r.fs.Changed(name) {
				r.boot.Error().Msgf("--%s narrows the --changed selection and means nothing without --changed", name)
				r.usage(cmdIf)
				return app.Condition{}, 2
			}
		}
	}
	switch {
	case r.inv.condSet:
		c, err := app.ParseCondition(r.inv.cond)
		if err != nil {
			r.boot.Error().Err(err).Msg("invalid condition")
			return app.Condition{}, 2
		}
		return c, 0
	case *r.o.ifChanged:
		return app.ResolvedCondition("--changed", false), 0
	}
	path, wantDir, flag := *r.o.clFile, false, "--file"
	if r.fs.Changed("dir") {
		path, wantDir, flag = *r.o.ifDir, true, "--dir"
	}
	if path == "" {
		r.boot.Error().Msgf("%s names no path", flag)
		r.usage(cmdIf)
		return app.Condition{}, 2
	}
	r.ifFile = &fileTest{path: path, wantDir: wantDir}
	return app.ResolvedCondition(flag+" "+path, false), 0
}

// runIfIn performs `if` in the resolved folder. Both callers reach the command
// through here, so the options it runs with are written once — including the
// file tests, which wait for this moment because dir is what a relative path
// resolves against.
func (r *runner) runIfIn(dir string) int {
	log := r.logger()
	if r.ifFile != nil {
		cond := app.FileCondition(dir, r.ifFile.path, r.ifFile.wantDir)
		r.ifBranches[0].Cond = cond
		log.Debug().Str("condition", cond.Spec).Str("dir", dir).
			Bool("held", cond.Match(os.Getenv)).Msg("file condition evaluated")
	}
	ctx, stop := signalCtx()
	defer stop()
	code, err := app.RunIf(ctx, app.IfOptions{
		Branches: r.ifBranches, Else: *r.o.ifElse, OnFailure: *r.o.onFailure,
		Lookup: os.Getenv, Dir: dir,
		Stdout: r.stdout, Stderr: r.stderr, Log: log,
	})
	if err != nil {
		return 1
	}
	return code
}

// prepareExec validates exec's flags and builds its options. The command
// itself needs the config, so it runs later; only its usage checks belong
// here, for the same reason every other command's do.
func (r *runner) prepareExec() (int, bool) {
	if r.inv.cmd != cmdExec {
		return 0, false
	}
	subj, ok := execSubject(r.o, r.boot)
	if !ok {
		return 2, true
	}
	from, ok := execScriptFrom(r.o, r.boot)
	if !ok {
		return 2, true
	}
	in, ok := helperIn(r.o, r.boot)
	if !ok {
		return 2, true
	}
	if !checkExecEnv(r.o, subj, r.usage, r.boot) {
		return 2, true
	}
	r.execOpts = app.ExecOptions{
		Script: r.inv.script, Subject: subj, ScriptFrom: from, In: in,
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
	if cmd == cmdInit || cmd == cmdInstall || manifestCommand(cmd) {
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
	case cmd == cmdInstall:
		return r.runInstall(), true
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

// validateInstall is everything `dispat install`'s flags decide on their
// own, and where the repository is parsed: a mistyped URL is a usage mistake
// and belongs beside the others, before a single request is made.
func (r *runner) validateInstall() (int, bool) {
	if *r.o.suRollback {
		// A rollback restores what is already installed, so every flag that
		// chooses something to download contradicts it. --as and --bin-dir do
		// not: they are what says which tool is being restored.
		for _, name := range []string{"release", "prerelease", "force", "asset", "pipe", "tag-prefix"} {
			if r.fs.Changed(name) {
				r.boot.Error().Msgf(
					"--rollback restores the kept binary and downloads nothing, so --%s means nothing beside it", name)
				r.usage(cmdInstall)
				return 2, true
			}
		}
		if r.inv.repository == "" && *r.o.instName == "" {
			// Neither says which tool to restore, and guessing is worse than
			// asking.
			r.boot.Error().Msg("--rollback needs to know which tool: name the repository, or name the file with --as")
			r.usage(cmdInstall)
			return 2, true
		}
	}
	for _, name := range []string{"owner", "repo"} {
		// The repository is the argument, so these two would be read and
		// then silently overwritten, which is how a flag that never fires
		// gets written into a script.
		if r.fs.Changed(name) {
			r.boot.Error().Msgf("install takes its repository as an argument, so --%s means nothing beside it", name)
			r.usage(cmdInstall)
			return 2, true
		}
	}
	if err := install.ValidName(*r.o.instName); *r.o.instName != "" && err != nil {
		r.boot.Error().Err(err).Msg("invalid --as")
		r.usage(cmdInstall)
		return 2, true
	}
	if r.inv.repository == "" {
		if !*r.o.suRollback {
			r.boot.Error().Msg("install requires a repository: dispat install https://github.com/owner/repo")
			r.usage(cmdInstall)
			return 2, true
		}
		// A rollback with only --as: there is no repository to parse, and the
		// zero value carries no owner into anything that would use one.
		return 0, false
	}
	repo, err := install.ParseRepository(r.inv.repository)
	if err != nil {
		r.boot.Error().Err(err).Msg("invalid repository")
		r.usage(cmdInstall)
		return 2, true
	}
	r.repository = repo
	return 0, false
}

// runInstall performs `dispat install`. Its logger comes from the flags
// alone, like every other command that reaches no config file.
func (r *runner) runInstall() int {
	format := orDefault(*r.o.logFormat, "pretty")
	log := newLogger(orDefault(*r.o.logLevel, "info"), format, r.stdout)
	ctx, stop := signalCtx()
	defer stop()

	src := updateSource(r.o, r.fs)
	src.Owner, src.Repo = r.repository.Owner, r.repository.Repo
	// The repository's own host decides the endpoint unless --api-url named
	// one, which is what makes a GitHub Enterprise URL work with nothing else
	// typed.
	if !r.fs.Changed("api-url") {
		src.APIURL = r.repository.APIURL()
	}
	// The conventional GITHUB_TOKEN is only ever sent to github.com. Here the
	// host comes from an argument rather than from a flag the operator set on
	// purpose, so a repository URL naming any other host would otherwise be
	// enough to make dispat hand somebody's github.com credentials to it.
	// --token-env is how a token is sent to an endpoint deliberately.
	if src.APIURL != "" && !r.fs.Changed("token-env") {
		if src.Token != "" {
			log.Debug().Str("api", src.APIURL).
				Msg("install: the endpoint is not github.com, so GITHUB_TOKEN is not sent; name one with --token-env")
		}
		src.Token = ""
	}
	src.Command = install.Command
	src.TagPrefix = *r.o.instTagPrefix
	src.AnyTag = *r.o.instTagPrefix == ""
	src.Prerelease = *r.o.suPrerelease
	src.Log = log

	pending, err := app.Install(ctx, app.InstallOptions{
		Repository: r.repository, Source: src, Release: *r.o.suRelease,
		Asset: *r.o.instAsset, BinDir: *r.o.instBinDir, Name: *r.o.instName, Pipe: *r.o.instPipe,
		Check: *r.o.check, Force: *r.o.suForce, Rollback: *r.o.suRollback,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		JSON: format == "json", Out: r.stdout, Err: r.stderr, Log: log,
	})
	if err != nil {
		return 1
	}
	// --check exits 1 when the same invocation without it would install
	// something, which is the gate a provisioning script puts in front of an
	// install it does not want to repeat.
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
			VerifyUnlinked: *r.o.scVerifyUnlinked, VerifyLinked: *r.o.scVerifyLinked,
			ForbidRanges: *r.o.scForbidRange, RequireRanges: *r.o.scRequireRange,
			JSON: *r.o.logFormat == "json", Out: r.stdout, Log: log,
		}) != nil {
			return 1
		}
	case cmdReplacer:
		reps, err := parseReplaceSpecs(*r.o.rpReplace)
		if err != nil {
			r.boot.Error().Err(err).Msg("invalid replacement")
			return 2
		}
		if len(reps) == 0 {
			r.boot.Error().Msg("replacer needs something to write: --replace 'find=>write'")
			r.usage(r.inv.cmd)
			return 2
		}
		if app.ReplaceFiles(ctx, app.ReplaceOptions{
			Root: *r.o.root, Paths: r.inv.paths, Replacements: reps, Strict: *r.o.strict,
			JSON: *r.o.logFormat == "json", Out: r.stdout, Log: log,
		}) != nil {
			return 1
		}
	default:
		if app.WriteManifests(ctx, app.WriteOptions{
			Root: *r.o.root, Paths: r.inv.paths, Version: r.write.version,
			Build: r.write.build,
			Edits: r.write.edits, Links: r.write.links, DropLinks: r.write.dropLinks,
			Strict: *r.o.strict,
			JSON:   *r.o.logFormat == "json", Out: r.stdout, Log: log,
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
		Int("configFiles", len(cfg.SourceFiles)).
		Int("spaces", len(cfg.Spaces)).
		Int("packages", len(cfg.Packages)).
		Msg("configuration loaded")
	// Which files a configuration was actually made of is only interesting
	// once it is made of more than one, and then it is the first question:
	// a `$ref` naming the wrong fragment looks exactly like a key nobody
	// wrote.
	for _, file := range cfg.SourceFiles {
		log.Trace().Str("file", file).Msg("configuration file read")
	}

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
	if r.inv.cmd == cmdIf {
		// Only --changed, or an --in naming a package, a space or the root,
		// gets this far; every other `if` already ran without reading any of
		// this. The block must stay above the update check below: no `if` path
		// may cost a GitHub request, however much else it asked for.
		a := app.New(resolvedRoot, cfg, log)
		dir := *r.o.root
		if r.ifIn != nil {
			var err error
			if dir, err = a.ResolveDir(*r.ifIn, *r.o.root); err != nil {
				log.Error().Err(err).Msg("invalid --in")
				return 1
			}
		}
		if *r.o.ifChanged {
			// The gate expands the window with consumers and then asks "is the
			// selection among it" — so with everything selected, --consumers
			// cannot change the answer: expanding a set never empties it and
			// never fills an empty one. Refused only here, because whether the
			// invocation folder narrows the selection needs the resolved root.
			if *r.o.consumers && len(*r.o.pkgFilter)+len(*r.o.spaceFilter)+len(*r.o.groupFilter) == 0 &&
				sameDir(*r.o.root, resolvedRoot) {
				log.Error().Msg("--consumers expands what the changes reach and cannot change the answer when everything is selected; add --package, --space or --group, or run from inside a package folder")
				r.usage(cmdIf)
				return 2
			}
			ctx, stop := signalCtx()
			defer stop()
			sel := filter.Filter{Packages: *r.o.pkgFilter, Spaces: *r.o.spaceFilter,
				Groups: *r.o.groupFilter, Dir: *r.o.root}
			names, err := a.ChangedSelection(ctx, app.WindowOptions{
				Filter: sel, Since: *r.o.since, Consumers: *r.o.consumers})
			if err != nil {
				log.Error().Err(err).Msg("cannot evaluate --changed")
				return 1
			}
			r.ifBranches[0].Cond = app.ResolvedCondition("--changed", len(names) > 0)
			log.Debug().Strs("packages", names).Bool("held", len(names) > 0).
				Msg("changed selection resolved")
		}
		return r.runIfIn(dir)
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
		// A clean plan that releases nothing is exit 3, apart from exit 1's
		// real failures, so a pipeline can gate on --require-release without
		// reading a broken configuration as "nothing to release".
		if err := a.Status(ctx, relOpts); err != nil {
			if errors.Is(err, app.ErrNothingToRelease) {
				return 3
			}
			return 1
		}
	case cmdRun:
		if a.RunScript(ctx, r.inv.script, app.RunOptions{OnError: *o.onError, Window: window, Args: r.inv.args}) != nil {
			return 1
		}
	case cmdPreview:
		res, err := a.Preview(ctx, app.PreviewOptions{
			Filter: sel, Changelog: *o.pvChangelog, GitHub: *o.pvGithub})
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
			ReleaseName: *o.releaseName, Authors: o.authorOptions()}) != nil {
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
			Replacements: r.reps, Files: *o.rpFiles,
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
			ReleaseName: *o.releaseName, Authors: o.authorOptions()}) != nil {
			return 1
		}
	case cmdTrigger:
		if a.Trigger(ctx, r.inv.event, r.inv.progress, r.inv.message) != nil {
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
		// The same mapping as status, so the two commands sharing
		// --require-release cannot disagree about what its refusal means.
		if _, err := a.Release(ctx, relOpts); err != nil {
			if errors.Is(err, app.ErrNothingToRelease) {
				return 3
			}
			return 1
		}
	}
	return 0
}

// sameDir reports whether two paths name the same folder, resolved through
// symlinks so a macOS /tmp and its /private twin compare equal.
func sameDir(a, b string) bool {
	canon := func(p string) string {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		return p
	}
	return canon(a) == canon(b)
}
