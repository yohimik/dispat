// Package cli is the command-line controller: it parses flags and commands,
// loads the configuration, builds the logger, and delegates the actual work
// to the app package, mapping its results onto process exit codes.
package cli

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"

	public "github.com/yohimik/dispat/pkg/models/v2"
	"github.com/yohimik/dispat/pkg/writer"

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
	// names it for the other, and the third runs one script per item of a
	// list. All three propagate the script's own exit code.
	cmdIf   = "if"   // run one of several scripts, chosen by an env condition
	cmdFor  = "for"  // run a script once per item of a list
	cmdExec = "exec" // run one declared script here, for a named subject

	// The standalone step commands, exposing the release pipeline's native
	// steps to custom flows. Like every command word (and unlike --version),
	// each permanently shadows a run script of the same name: `dispat
	// changelog` is never `dispat run changelog`.
	cmdChangelog    = "changelog"    // write pending changelog entries now
	cmdAutoversion  = "autoversion"  // native manifest reconciliation, plus syncLock
	cmdAutowriter   = "autowriter"   // the writer's edits, over the whole selection
	cmdAutoreplacer = "autoreplacer" // literal replacements, over the whole selection
	cmdCommit       = "commit"       // per-package release commit (--tag, --push)
	cmdGithub       = "github"       // per-package GitHub release, published now
	cmdTrigger      = "trigger"      // raise a webhook event from inside a script

	// The manifest commands, exposing the pkg/scanner and pkg/writer
	// libraries directly. Like init, they need no config file and no git
	// repository: they only ever look at the files they are pointed at.
	cmdScanner  = "scanner"  // read what a folder's manifests declare
	cmdWriter   = "writer"   // edit manifests in place, format-preserving
	cmdReplacer = "replacer" // replace literal text in any file, no parsing

	// cmdSelfUpdate is about the binary rather than about any repository, so
	// like init and the manifest commands it needs no config and no git.
	cmdSelfUpdate = "self-update"
	// cmdInstall is self-update pointed at somebody else's releases: it
	// installs a tool from any GitHub repository, and needs no config and no
	// git for the same reason.
	cmdInstall = "install"
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
	case cmdRun, cmdAutowriter, cmdAutoreplacer, cmdChangelog, cmdAutoversion, cmdCommit, cmdGithub:
		return true
	}
	return false
}

// Version is the dispat version `--version` reports. Releases stamp it at
// build time from the release tag:
//
//	go build -ldflags "-X github.com/yohimik/dispat/services/dispat/internal/cli.Version=$DISPAT_VERSION"
//
// It is a flag, not a command, because a bare word after `dispat` is the run
// shorthand — a `version` command would shadow a run script named after the
// version stage.
//
// It is declared without an initialiser, and that is load-bearing rather than
// a style choice: TinyGo's -X applies only to a string variable declared with
// no value, and silently ignores the flag for one declared with a value, so a
// `var Version = "dev"` would leave every TinyGo build reporting a local build
// however it was stamped. Under gc the two spellings are the same binary:
// selfupdate.Describe reads the empty string and "dev" alike as the local
// build, and every reader goes through it.
var Version string

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
//
// What it does itself is the housekeeping that belongs to the process rather
// than to any command: parse the flags, prune a stale self-update backup,
// bracket the background update check so its notice prints last. The command
// line's phases are runner's, in dispatch.go, each one running only once
// everything before it has agreed there is a command to run.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	o := declareFlags(fs)
	fs.Usage = func() { printUsage(stderr, fs) }
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

	r := &runner{
		fs: fs, o: o, stdout: stdout, stderr: stderr,
		checkCtx: checkCtx, update: &update,
		// Pre-config refusals are reported with a bootstrap logger. The config
		// file's own log settings are not known yet, but the flags are parsed,
		// so --log-format and --log-level already speak: a CI pipeline reading
		// JSON must see the config-not-found error as JSON too. The stream
		// stays stderr — these are diagnostics, not command output.
		boot: newLogger(orDefault(*o.logLevel, "info"), orDefault(*o.logFormat, "pretty"), stderr),
	}

	// The environment files come before every phase below, because dispat's
	// own variables are in them too: the update check reads DISPAT_UPDATE_CHECK
	// while printing the version, and the release lock reads its switch long
	// before any script runs.
	// What it read is reported through the flag-built logger, since no config
	// file has been found yet to state a level: this is a startup event, and
	// --log-level is the only thing that can have an opinion about it. A
	// failure goes to the bootstrap logger, like every other pre-config
	// refusal.
	if err := loadEnvFiles(*o.envFiles, r.logger()); err != nil {
		r.boot.Error().Err(err).Msg("cannot read an environment file")
		return 1
	}

	// Before anything else: the version and the help must both answer without
	// a config file, and the help before the arity checks.
	if code, done := r.versionOrHelp(); done {
		return code
	}

	// Then, before the arity checks: a flag belonging to another command. Its
	// errors are the ones a mis-parsed flag hides behind, so naming the flag has
	// to come first.
	if code, done := r.refuseForeignFlags(); done {
		return code
	}

	inv, badArgs := parseInvocation(fs.Args(), fs.ArgsLenAtDash(), r.usage, r.boot)
	if badArgs {
		return 2
	}
	r.inv = inv

	for _, phase := range []func() (int, bool){
		r.validateFlags, // what the flags alone decide
		r.runIf,         // the environment, without a repository
		r.runFor,        // a literal list, without a repository
		r.prepareExec,   // exec's usage checks, before its config
		r.runPreConfig,  // init, self-update and the manifest commands
	} {
		if code, done := phase(); done {
			return code
		}
	}
	return r.runConfigured()
}

// invocation is the parsed command line: which command runs and its
// positional arguments.
type invocation struct {
	cmd     string
	script  string // run and exec: the script name
	cond    string // if: the leading condition
	condSet bool   // if: a positional condition was given, even an empty one
	dir     string // scanner: the optional folder to scan
	// for: the literal items to iterate over, as typed. Empty is legal — an
	// empty list is an empty loop — so there is no "was one given" flag beside
	// it: whether a flag source spoke instead is the for phase's own check.
	items []string
	// install: the repository to install from, as it was typed. Parsed in
	// the flag phase, where every other usage mistake is caught.
	repository string
	paths      []string // writer and replacer: the files to edit
	args       []string // run and exec: what followed `--`, for the script
	// trigger: the raised event name, the progress value when the event is
	// progress, and the optional free-text message.
	event    string
	progress *int
	message  string
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
	case cmdInstall:
		// One repository, or none: a rollback restores what is already
		// installed and has no releases to read, so it takes --as instead.
		if len(rest) > 2 {
			log.Error().Strs("args", rest[2:]).
				Msg("install takes one repository: dispat install https://github.com/owner/repo")
			usage(inv.cmd)
			return inv, true
		}
		if len(rest) == 2 {
			inv.repository = rest[1]
		}
	case cmdRun:
		if len(rest) != 2 {
			log.Error().Msg("run requires exactly one argument: the script name (select packages with --package, --space or --group; pass arguments to the script after `--`)")
			usage(inv.cmd)
			return inv, true
		}
		inv.script = rest[1]
	case cmdIf:
		// Zero arguments is legal here because the leading condition may come
		// from a flag instead (--changed, --file, --dir); whether one source
		// actually spoke is the if phase's own check.
		if len(rest) > 2 {
			log.Error().Strs("args", rest[2:]).Msg("if takes at most one argument: the condition (or none, with --changed, --file or --dir)")
			usage(inv.cmd)
			return inv, true
		}
		if len(rest) == 2 {
			inv.cond, inv.condSet = rest[1], true
		}
	case cmdFor:
		// Any arity: the items are the list, and a list may be empty, because
		// the source may be a flag instead — or because an empty loop is what
		// the caller asked for. Which of the two it was is the for phase's check.
		inv.items = rest[1:]
	case cmdExec:
		if len(rest) != 2 {
			log.Error().Msg("exec requires exactly one argument: the script name (choose the subject with --for pkg:<name>, space:<name>, root or cwd; pass arguments to the script after `--`)")
			usage(inv.cmd)
			return inv, true
		}
		inv.script = rest[1]
	case cmdPreview, cmdChangelog, cmdAutoversion, cmdAutowriter, cmdAutoreplacer, cmdCommit, cmdGithub:
		if len(rest) > 1 {
			log.Error().Strs("args", rest[1:]).
				Msgf("%s takes no arguments (select packages with --package, --space or --group)", inv.cmd)
			usage(inv.cmd)
			return inv, true
		}
	case cmdTrigger:
		// `trigger <event> [message...]` raises script.<event>; `progress`
		// is the one typed kind, whose first argument is its 0-100 value.
		if len(rest) < 2 {
			log.Error().Msg("trigger requires an event: trigger <event> [message], or trigger progress <0-100> [message]")
			usage(inv.cmd)
			return inv, true
		}
		word := rest[1]
		if !public.IsWebhookScriptWord(word) {
			log.Error().Str("event", word).
				Msg("a triggered event is one word: a letter, then letters, digits, dashes or underscores")
			usage(inv.cmd)
			return inv, true
		}
		inv.event = public.WebhookScriptEvent(word)
		args := rest[2:]
		if word == "progress" {
			if len(rest) < 3 {
				log.Error().Msg("trigger progress requires its value: trigger progress <0-100> [message]")
				usage(inv.cmd)
				return inv, true
			}
			pct, err := strconv.Atoi(rest[2])
			if err != nil || pct < 0 || pct > 100 {
				log.Error().Str("value", rest[2]).Msg("trigger progress wants a whole number between 0 and 100")
				usage(inv.cmd)
				return inv, true
			}
			inv.progress = &pct
			args = rest[3:]
		}
		inv.message = strings.Join(args, " ")
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
	version   string
	build     string
	edits     []writer.Edit
	links     []writer.Link
	dropLinks bool
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
	drop := cmd == cmdWriter && *o.wrDropLinks
	build := ""
	if cmd == cmdWriter {
		build = *o.wrSetBuild
	}
	if *o.wrSetVersion == "" && build == "" && len(edits) == 0 && len(links) == 0 && !derived && !drop {
		log.Error().Msgf("%s needs something to write: --set-version, --set or --link", cmd)
		usage(cmd)
		return writeRequest{}, false
	}
	return writeRequest{version: *o.wrSetVersion, build: build, edits: edits, links: links, dropLinks: drop}, true
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
