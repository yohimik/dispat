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
	logLevel := fs.String("log-level", "", "override the configured logLevel (trace, debug, info, warn, error)")
	logFormat := fs.String("log-format", "", "override the configured logFormat (pretty, json)")
	onError := fs.String("on-error", app.OnErrorSkip,
		"run command: what a failing script does to the failed package's dependents (skip or continue)")
	since := fs.String("since", "",
		"run command: select the packages the commits since the git revision address (scopes first, changed files for scopeless commits; e.g. HEAD~1, origin/main, a tag; 'all' selects every package) instead of the release window")
	consumers := fs.Bool("consumers", false,
		"run command: additionally run every package that transitively depends on a selected one, so downstream consumers are re-run with the change")
	pkgFilter := fs.StringSliceP("package", "p", nil,
		"run, preview, changelog, autoversion, commit and compute commands: narrow to the named packages (repeatable and comma-separated; '*' globs, so -p '*' is every package and -p '@acme/*' a prefix); without it, the package folder the command was invoked from")
	spaceFilter := fs.StringSliceP("space", "s", nil,
		"run, preview, changelog, autoversion, commit and compute commands: narrow to every package of the named spaces (repeatable, comma-separated, '*' globs); a standalone package belongs to no space, so --package is the only way to name one")
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
	ghOwner := fs.String("owner", "",
		"github command: override the github.owner repository owner")
	ghRepo := fs.String("repo", "",
		"github command: override the github.repo repository name")
	ghAPIURL := fs.String("api-url", "",
		"github command: override the github.apiUrl API endpoint (for GitHub Enterprise)")
	ghTokenEnv := fs.String("token-env", "",
		"github command: override the github.tokenEnv variable the token is read from")
	ghTarget := fs.String("target", "",
		"github command: create the tag at this commit or branch (target_commitish); only safe once the commit is on the remote")
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
		"autoversion command: override autoVersion.manifests (root, all or none)")
	avNoReplace := fs.Bool("no-replace", false,
		"autoversion command: skip the autoVersion.replace rules for this invocation")
	avWriteVersion := fs.Bool("write-version", true,
		"autoversion command: override autoVersion.writeVersion")
	avSyncLock := fs.Bool("sync-lock", true,
		"autoversion command: run the space's syncLock scripts for changed packages")
	scanRootOnly := fs.Bool("root-only", false,
		"scanner command: read only the manifests sitting directly in the folder, without descending")
	wrSetVersion := fs.String("set-version", "",
		"writer command: rewrite each manifest's own version field to this version")
	wrSet := fs.StringArray("set", nil,
		"writer command: set one dependency's declared range, [kind:]name=range (repeatable)")
	wrReplace := fs.StringArray("replace", nil,
		"writer command: point a dependency at a local folder, name=path; an empty path removes the redirect (repeatable)")
	rpSub := fs.StringArray("sub", nil,
		"replacer command: replace literal text in the named files, find=>write (repeatable, applied in order)")
	strict := fs.Bool("strict", false,
		"scanner, writer and replacer commands: exit 1 on a manifest that failed to parse, an edit the manifest does not declare, or a --sub that matched nothing")
	showVersion := fs.Bool("version", false, "print the dispat version and exit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `%s

usage: dispat [command] [flags]

commands:
  release                  build and publish changed packages (default)
  status                   print the project graph and new versions, without building
  run <script>             run the named script inside each changed package that
                           defines it — its own scripts, then its space's, then the
                           top-level ones — honouring the dependency graph.
                           --package and --space narrow that to part of the
                           monorepo, as does the package or space folder the
                           command is invoked from. "dispat <script>" is a
                           shorthand when <script> is not a command name
  init                     write a starter config file (--format json, yaml or toml)
                           at the git repository root, unless one already exists
  preview                  print the pending release notes (breaking changes,
                           features, fixes) for every package with something
                           pending, or for the selected ones
  changelog                write the pending changelog entry now, so a custom
                           flow can land it inside the release commit;
                           already-written entries are skipped
  autoversion              reconcile manifests to the planned versions (native
                           auto-versioning) and run syncLock where they changed
  commit                   create the per-package release commit; --tag tags
                           it, --push pushes the branch and tags
  github                   create the per-package GitHub release now, so a
                           flow can publish it from its own stage;
                           already-published releases are skipped
  compute                  scan every package's manifests (package.json, go.mod,
                           Cargo.toml, pyproject.toml, composer.json, pom.xml,
                           *.csproj, pubspec.yaml, requirements*.txt), derive
                           the dependency graph and suggest config changes;
                           --write applies all, --interactive confirms each,
                           --check gates CI; an edge marked keep: true is
                           never suggested for removal, and --package/--space
                           scope the suggestions to those packages' edges
  scanner [folder]         print what the folder's manifests declare: identity,
                           ecosystem and every dependency with its range;
                           --root-only stays out of sub-folders. Needs no
                           config file and no git repository
  writer <manifest>...     edit manifests in place, preserving their formatting:
                           --set-version rewrites the own version, --set sets a
                           dependency's range, --replace points one at a local
                           folder. Needs no config file and no git repository
  replacer <file>...       replace literal text in any file, parsing nothing:
                           --sub 'find=>write', repeatable and applied in
                           order, for the versions no manifest writer reaches
                           (a Gradle coordinate, a Helm chart, a README).
                           Needs no config file and no git repository

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
	cmd, runScript := inv.cmd, inv.script
	if cmd == cmdRun && !app.ValidOnError(*onError) {
		bootLog.Error().Str("on-error", *onError).Msgf("unknown --on-error value (want %q or %q)",
			app.OnErrorSkip, app.OnErrorContinue)
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
	if manifestCommand(cmd) {
		// Also before config loading: these three are the manifest libraries
		// themselves, and they read nothing but the files named on the
		// command line. Their logger comes from the flags alone, since there
		// is no config file behind them to take a level or a format from.
		log := newLogger(orDefault(*logLevel, "info"), orDefault(*logFormat, "pretty"), stdout)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if cmd == cmdScanner {
			if app.ScanManifests(ctx, app.ScanOptions{
				Root: *root, Dir: inv.dir, RootOnly: *scanRootOnly, Strict: *strict,
				JSON: *logFormat == "json", Out: stdout, Log: log,
			}) != nil {
				return 1
			}
			return 0
		}
		if cmd == cmdReplacer {
			subs, err := parseSubSpecs(*rpSub)
			if err != nil {
				bootLog.Error().Err(err).Msg("invalid substitution")
				return 2
			}
			if len(subs) == 0 {
				bootLog.Error().Msg("replacer needs something to write: --sub 'find=>write'")
				fs.Usage()
				return 2
			}
			if app.SubstituteFiles(ctx, app.SubstituteOptions{
				Root: *root, Paths: inv.paths, Subs: subs, Strict: *strict,
				JSON: *logFormat == "json", Out: stdout, Log: log,
			}) != nil {
				return 1
			}
			return 0
		}
		edits, repls, err := parseEditSpecs(*wrSet, *wrReplace)
		if err != nil {
			bootLog.Error().Err(err).Msg("invalid edit")
			return 2
		}
		if *wrSetVersion == "" && len(edits) == 0 && len(repls) == 0 {
			bootLog.Error().Msg("writer needs something to write: --set-version, --set or --replace")
			fs.Usage()
			return 2
		}
		if app.WriteManifests(ctx, app.WriteOptions{
			Root: *root, Paths: inv.paths, Version: *wrSetVersion,
			Edits: edits, Replacements: repls, Strict: *strict,
			JSON: *logFormat == "json", Out: stdout, Log: log,
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

	// The one selection every package-selecting command shares. Dir is --root
	// as the user spelled it — where they stood — and not the resolved
	// monorepo root the ascent just found: the difference between the two is
	// exactly what narrows a command to the folder it was invoked from.
	sel := filter.Filter{Packages: *pkgFilter, Spaces: *spaceFilter, Dir: *root}

	// The application does the work and logs its own findings; the controller
	// only maps the outcome onto an exit code.
	a := app.New(resolvedRoot, cfg, log)
	switch cmd {
	case cmdStatus:
		if a.Status(ctx) != nil {
			return 1
		}
	case cmdRun:
		if a.RunScript(ctx, runScript, app.RunOptions{OnError: *onError,
			Filter: sel, Since: *since, Consumers: *consumers}) != nil {
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
			File: *clFile, Title: *clTitle, DateFormat: *clDateFormat}) != nil {
			return 1
		}
	case cmdAutoversion:
		opts := app.AutoVersionOptions{Filter: sel,
			Range: *avRange, Match: *avMatch, SyncLock: *avSyncLock, NoReplace: *avNoReplace}
		switch *avManifests {
		case "", "root", "all", "none":
			opts.Manifests = *avManifests
		default:
			bootLog.Error().Str("manifests", *avManifests).
				Msg("unknown --manifests value (want root, all or none)")
			return 2
		}
		if fs.Changed("write-version") {
			opts.WriteVersion = avWriteVersion
		}
		if a.AutoVersion(ctx, opts) != nil {
			return 1
		}
	case cmdCommit:
		if a.Commit(ctx, app.CommitOptions{Filter: sel,
			Tag: *commitTag, Push: *commitPush, Name: *commitName, Email: *commitEmail,
			Remote: *commitRemote, Message: *commitMessage, Include: *commitInclude}) != nil {
			return 1
		}
	case cmdGithub:
		if a.GitHub(ctx, app.GitHubOptions{Filter: sel, Owner: *ghOwner, Repo: *ghRepo,
			APIURL: *ghAPIURL, TokenEnv: *ghTokenEnv, Target: *ghTarget}) != nil {
			return 1
		}
	case cmdCompute:
		open, err := a.Compute(ctx, cfgPath, app.ComputeOptions{
			Write:       *computeWrite,
			Interactive: *computeInteractive,
			Check:       *computeCheck,
			Filter:      sel,
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
	script string   // run: the script name
	dir    string   // scanner: the optional folder to scan
	paths  []string // writer and replacer: the files to edit
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
		if len(rest) != 2 {
			log.Error().Msg("run requires exactly one argument: the script name (select packages with --package or --space)")
			usage()
			return inv, true
		}
		inv.script = rest[1]
	case cmdPreview, cmdChangelog, cmdAutoversion, cmdCommit, cmdGithub:
		if len(rest) > 1 {
			log.Error().Strs("args", rest[1:]).
				Msgf("%s takes no arguments (select packages with --package or --space)", inv.cmd)
			usage()
			return inv, true
		}
	case cmdScanner:
		if len(rest) > 2 {
			log.Error().Msg("scanner takes at most one argument: the folder to scan")
			usage()
			return inv, true
		}
		if len(rest) == 2 {
			inv.dir = rest[1] // no argument: scan --root itself
		}
	case cmdWriter:
		if len(rest) < 2 {
			log.Error().Msg("writer requires at least one manifest file to edit")
			usage()
			return inv, true
		}
		inv.paths = rest[1:]
	case cmdReplacer:
		if len(rest) < 2 {
			log.Error().Msg("replacer requires at least one file to edit")
			usage()
			return inv, true
		}
		inv.paths = rest[1:]
	default:
		// Not a command name: treat the word as a script, so `dispat lint` is
		// `dispat run lint`. A name nothing defines still fails cleanly
		// later, which also catches command typos.
		if len(rest) > 1 {
			log.Error().Strs("args", rest[1:]).Msg("unexpected arguments")
			usage()
			return inv, true
		}
		inv.script, inv.cmd = inv.cmd, cmdRun
	}
	return inv, false
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
