package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/pflag"
)

// The help system. Every command word appears exactly once, in the table
// below, carrying the text that describes it and the names of the flags it
// reads. Both renderings — the command list and one command's own help —
// are built from that table plus the single master flag set, so a flag can
// never be documented in one place and forgotten in the other.

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

// command is one entry of the command table: the word, the positional
// arguments it takes, the one line the command list shows, the paragraphs
// its own help adds, and the flags it reads.
type command struct {
	name string
	args string
	// argsLong is the invocation line for the command's own help, where there
	// is room to spell an optional part out. The command list is a fixed-width
	// column, so anything longer than a short "<script>" belongs here rather
	// than in args. Empty means args serves both.
	argsLong string
	short    string
	long     string
	flags    []string
}

// usageArgs is what a command's own help shows after its name.
func (c command) usageArgs() string {
	if c.argsLong != "" {
		return c.argsLong
	}
	return c.args
}

// selectionFlags are the three every package-selecting command shares.
var selectionFlags = []string{"package", "space", "group"}

// windowFlags are what a sweeping command adds to the selection: which
// packages are on the table, how far downstream the answer reaches, and what a
// failure does to the dependents.
var windowFlags = append([]string{"since", "consumers", "on-error"}, selectionFlags...)

// ifFlags and execFlags are the two shell helpers' own, --on-failure and --in
// aside: both take those two, so they are added to each entry rather than
// living here. if also takes the window flags --since and --consumers and the
// selection, which all describe its --changed condition.
var ifFlags = []string{"then", "elif", "else", "changed", "file", "dir"}

// forFlags are the loop's own. It shares --changed, --since, --consumers and
// the selection with if, which is why only --unchanged is here beside the three
// flags nothing else reads.
var forFlags = []string{"do", "keep-going", "unchanged", "require-items", "changed"}

var execFlags = []string{"for", "fallback", "script-from", "env"}

// helperFlags are what the two shell helpers share.
var helperFlags = []string{"on-failure", "in"}

// globalFlags apply to every command, so they are rendered separately rather
// than repeated in each entry.
var globalFlags = []string{"root", "config", "env-file", "concurrency", "log-level", "log-format",
	"quiet-parser", "version", "help"}

// updateCheckFlags are read on every command without being any command's own:
// the background update check asks dispat's repository about dispat, and these
// four say where to ask (see updateSource). They are not rendered anywhere,
// because --api-url on `dispat status` redirects that check rather than the
// command, which is a detail of the check and not a flag of the command. They
// are named here so the check's own reading is not refused as a foreign flag,
// and here rather than in a file of its own because this is where "what may a
// command be given" is written down.
var updateCheckFlags = []string{"owner", "repo", "api-url", "token-env"}

// commands is the table. Order is the order the command list prints in:
// the everyday commands, then the step commands, then the manifest tools.
var commands = []command{
	{
		name:  cmdRelease,
		short: "build and publish changed packages (default)",
		long: `Compute the release plan and execute it: the version, build and publish
stages of every changed package, in dependency order, then the release
commit, the tags and the GitHub releases the configuration asks for.

--package, --space and --group release part of the graph instead of all of
it, as does the package or space folder the command is invoked from. A
selected package whose provider is releasing and unselected stays behind
for the next run (W230), and a selection that releases part of a versioning
group says so (W231); --strict refuses either before anything is published,
and --group takes a whole group at once so it never splits one.

Before anything else the run takes the release lock: a "dispat-release-lock"
tag pushed to the remote, removed when the run ends. A second release while
one is running is refused rather than raced. unsafeDisableLock in the config,
or DISPAT_UNSAFE_DISABLE_LOCK=true in the environment, switches it off for
repositories with no remote to coordinate through.

--require-release refuses a run that would publish nothing, before the lock is
taken, for the CI stage whose point is that this run releases something. The
refusal exits 3, apart from exit 1's failures, so a pipeline can tell "nothing
to do" from "something is wrong".

This is what a bare "dispat" does.`,
		flags: append([]string{"strict", "require-release"}, selectionFlags...),
	},
	{
		name:  cmdStatus,
		short: "print the project graph and new versions, without building",
		long: `Compute the same plan a release would and print it: every package, its
new version, its channel transition and why it is releasing, without
building, tagging or writing anything.

It takes the release's own selection flags and narrows the plan exactly as
a release would, so the graph shows what "dispat release" with the same
flags is about to do.

Exits 0 even when a release would refuse, because showing the plan is the
job. A repository that cannot produce a correct plan at all, or a --strict
selection the plan cannot release, exits 1; --require-release with a correct
plan that releases nothing exits 3, so a pipeline gating on it can tell
"nothing to do" from "something is wrong".`,
		flags: append([]string{"strict", "require-release"}, selectionFlags...),
	},
	{
		name:     cmdRun,
		args:     "<script>",
		argsLong: "<script> [-- args...]",
		short:    "run the named script inside each changed package that defines it",
		long: `Run the named script inside each changed package that defines it (its
own scripts, then its space's, then the top-level ones), honouring the
dependency graph, so a package waits for the providers it depends on.

--package, --space and --group narrow that to part of the monorepo, as does
the package or space folder the command is invoked from. --since replaces
the release window with the commits since a git revision.

Anything after "--" is appended to each package's command, so
"dispat run test -- --watch" runs the test script with --watch in every
package the run covers. A bare word without the "--" is still a usage error:
packages are selected with flags.

"dispat <script>" is a shorthand when <script> is not a command name.`,
		flags: windowFlags,
	},
	{
		name:  cmdInit,
		short: "write a starter config file",
		long: `Write a starter config file at the git repository root, unless one
already exists. Needs no configuration and no git repository of its own.`,
		flags: []string{"format"},
	},
	{
		name:  cmdPreview,
		short: "print the pending release notes",
		long: `Print the pending release notes (breaking changes, features, fixes)
for every package with something pending, or for the selected ones.
Nothing is written and nothing is released.

--changelog prints the changelog entry body and --github the GitHub
release body, each under its own entry format. Together they print both,
labelled. Naming neither prints the changelog entry.`,
		flags: append([]string{"changelog", "github"}, selectionFlags...),
	},
	{
		name:  cmdChangelog,
		short: "write the pending changelog entry now",
		long: `Write each covered package's pending changelog entry now, so a custom
flow can land it inside the release commit instead of after it. An entry
the file already carries is skipped (W226), which is also what makes the
release stage skip the entries written here.`,
		flags: append([]string{"file", "file-title", "date-format", "release-name",
			"authors", "authors-format", "authors-commits", "authors-include",
			"authors-exclude", "authors-title"}, windowFlags...),
	},
	{
		name:  cmdAutoversion,
		short: "reconcile manifests to the planned versions",
		long: `Reconcile each covered package's manifests to the planned versions:
native auto-versioning, the same work the version stage does, plus the
space's syncLock scripts where the manifests changed.

--only-updated leaves a range that had fallen behind a provider released
outside this run alone, so only the run's own updates are written.`,
		flags: append([]string{"range", "match", "manifests", "only-updated",
			"no-replace", "write-version", "sync-lock"}, windowFlags...),
	},
	{
		name:  cmdAutowriter,
		short: "apply the writer's edits to every covered package",
		long: `Apply one set of manifest edits to every covered package: --set-version,
--set and --link mean exactly what they mean for "dispat writer", but the
manifests are found by scanning each package folder instead of being named on
the command line, and the packages are the ones the plan selects.

--set-local, --link-local and --unlink-local derive the edits instead of
taking them: every dependency a manifest declares that names another package
here gets its range reconciled to that package's version (spelled by --range),
its folder redirect written, or that redirect removed. Nothing has to be typed
out, and a dependency named by --set or --link keeps what the command line
said. Local links are never removed by a release, so run --unlink-local before
publishing.

--manifests root edits the manifests in each package folder, all every
manifest under it. A range may be written as {version}, which resolves to the
planned version of the package the edit names, and --set-version {version} to
the covered package's own. --only-updated drops every edit naming a package
this run does not update, and --strict fails when an edit matched no manifest
anywhere.`,
		flags: append([]string{"set-version", "set", "link", "set-local", "link-local",
			"unlink-local", "range", "manifests", "only-updated",
			"sync-lock", "strict"}, windowFlags...),
	},
	{
		name:  cmdAutoreplacer,
		short: "replace literal text across every covered package",
		long: `Replace literal text in every covered package, the way "dispat replacer"
does it in named files: --replace takes find=>write, --files says which of each
package's files to look in, as globs relative to that package's folder.

A --replace mentioning {provider}, {providerVersion} or {providerPrevious} is
rendered once per workspace package the covered package depends on, so one
pattern reaches every hand-written coordinate without naming a dependency.
{name}, {version} and {previous} render the covered package itself.

The packages that carry these coordinates are usually the consumers of what
just changed, and the window covers only what the commits touched, so
--consumers is what reaches them. --only-updated narrows the fan-out to the
providers this run releases, and --strict fails when a replacement matched
nothing in any covered package.`,
		flags: append([]string{"replace", "files", "only-updated", "strict"}, windowFlags...),
	},
	{
		name:  cmdCommit,
		short: "create the per-package release commit",
		long: `Create each covered package's release commit: the package folder staged
plus the commit.include paths, the message rendered per commit.messageFormat.
--tag also creates the annotated release tag at the resulting commit, and
--push pushes the branch and, with --tag, the tags. A tag that already
exists at that commit is skipped (W223); one at a different commit is left
alone and reported (E221). Tags are written and pushed with force by default,
so a ref the remote already carries is replaced rather than skipped forever;
--no-force turns that off for this invocation. The branch is never force
pushed. --tag-name names the tag instead of
computing it, which a command running inside a release stage needs when a
shared version has moved under it; it covers one package only.`,
		flags: append([]string{"tag", "push", "no-force", "name", "email", "remote", "tag-name",
			"message-format", "include"}, windowFlags...),
	},
	{
		name:  cmdGithub,
		short: "create the per-package GitHub release now",
		long: `Create each covered package's GitHub release now, so a flow can publish
it from its own stage (an announce script, say) instead of waiting for
the end of the run. A release the repository already carries is skipped
(W224), so a re-run after a later failure is a no-op.

Meant for a stage script: the opt-in and the files to attach are read from
DISPAT_EXPORT_GITHUB in the environment the stage handed the command, and
github.allPackages is the configuration-level opt-in for everything else.

--draft overrides github.draft: the release is created for a human to
publish, and carries no tag ref until they do, so nothing that resolves a
release by its tag sees it meanwhile. --draft=false publishes straight
away over a configured draft.`,
		flags: append([]string{"owner", "repo", "api-url", "token-env", "target", "draft", "release-name",
			"authors", "authors-format", "authors-commits", "authors-include",
			"authors-exclude", "authors-title"}, windowFlags...),
	},
	{
		name:  cmdTrigger,
		short: "raise a webhook event from inside a script",
		long: `Deliver one script-raised event to the configured webhooks:

    dispat trigger progress 40 compiling assets
    dispat trigger deployed version is live

The event is one word — a letter, then letters, digits, dashes or
underscores — delivered as script.<word>, so a subscription tells dispat's
own events apart from what a script said by the prefix alone. progress is
the one typed event: its first argument is a whole number from 0 to 100.
Everything after the event (and the value) is the event's free-text message.

Meant for a stage script saying something between the stage.started and
stage.succeeded events the release emits on its own. The package, stage and
version are read from the DISPAT_* environment the enclosing run exported,
so the event attributes itself to the script that raised it and routes to
that package's effective webhook list; invoked outside a run, those fields
are simply absent and the top-level list alone hears it.

Like every webhook outcome, an endpoint that cannot be reached is a W239
warning, never an exit code: a script cannot fail its stage by reporting.
With no webhooks configured the command does nothing and exits 0.`,
		flags: nil,
	},
	{
		name:  cmdCompute,
		short: "derive the dependency graph and the starting versions from the manifests",
		long: `Scan every package's manifests, the same twenty families the
scanner command reads (npm, Go, Cargo, Python, Composer, Maven, NuGet, pub,
Ruby, CocoaPods, Xcode, Apple bundles, Android, Gradle, Docker, Unity, Godot,
Unreal, Defold, O3DE), and suggest
the config changes they imply: the dependency edges between packages, and an
initials entry for every package already at a version no release tag carries
yet.

--write applies all of them, --interactive confirms each, --check reports
only and exits 1 when suggestions exist, which is the CI gate. An edge
marked keep: true is never suggested for removal, an initials entry already
in the config is never rewritten, and --package/--space/--group scope the
suggestions to those packages.`,
		flags: append([]string{"write", "interactive", "check"}, selectionFlags...),
	},
	{
		name:     cmdIf,
		args:     "[cond]",
		argsLong: "<cond> | --changed | -f <path> | -d <path>",
		short:    "run one of several scripts, chosen by a condition",
		long: `Run one of several shell scripts, chosen by a condition. The leading
condition takes the first --then, each --elif takes the next, and --else runs
when none of them held. The first condition that holds wins and the rest are
skipped, so a chain of --elif is a switch and --else is its default case.

The leading condition is one of three kinds. A positional condition asks the
environment: NAME (set and non-empty), !NAME (unset or empty), NAME=value,
NAME!=value, NAME~glob or NAME!~glob. --file/-f <path> and --dir/-d <path> ask
the filesystem: the path exists and is a regular file, or a folder; a path
that is absent or the wrong kind is false, never an error, and a relative
path resolves where the chosen script runs. --changed asks the repository:
it holds when changed packages are selected. --since picks the window (the
release window without it, 'all' for every package), --consumers expands it
downstream, and --package/--space/--group then narrow it, so the gate asks
whether the selection is among what the changes reach: --changed -p web
--consumers holds when web, or anything web transitively consumes, changed.
Every --elif is an environment condition.
The scripts are shell text, not script names: this is the shell's own
if/elif/else, spelled to fit on one line inside a configured script.

The chosen script's exit code becomes the command's, so it stays transparent
in a pipeline, and --on-failure replaces that code with its own. Nothing
matching with no --else runs nothing and exits 0.

--in runs the chosen script somewhere else: a folder path, or pkg:<name>,
space:<name>, root or cwd. Needs no config file and no git repository, unless
--in names a package, a space or the root, which only a configuration can
point at, or --changed asks about the repository itself.`,
		flags: append(append(append([]string{}, ifFlags...),
			"since", "consumers"), append(append([]string{}, selectionFlags...), helperFlags...)...),
	},
	{
		name:     cmdFor,
		args:     "[item]...",
		argsLong: "[item]... | -p|-s|-g <globs> | --changed | --unchanged | --since <rev>",
		short:    "run a script once per item of a list",
		long: `Run a script once for each item of a list: the shell's own
"for x in ...; do ...; done", spelled so it means the same thing under every
shell a configuration may name. A loop copied from a POSIX script is the one
construct that breaks the moment "shell" is something else.

The list comes from exactly one source. Positional items are the list as
typed, and need no configuration at all. -p, -s and -g iterate over the
packages the terms name, over the spaces themselves, or over the versioning
groups themselves. --changed iterates over the changed packages, --unchanged
over the ones it leaves out, and --since <rev> alone is --changed --since
<rev>, spelled as "dispat run" spells it. Under any of those three, -p, -s
and -g narrow the window instead of being the source, exactly as they do for
"dispat if --changed", and --consumers expands it downstream; without one,
naming two of them at once has no meaning and is refused.

--do is the script, repeatable: several run in order for each item and stop
at the first one of them that fails. Each iteration exports DISPAT_ITEM, the
item, plus DISPAT_INDEX (0-based) and DISPAT_TOTAL. A package item also
exports DISPAT_PACKAGE, DISPAT_SPACE, DISPAT_DIR and DISPAT_GROUP (unset when
the package versions on its own); a space exports DISPAT_SPACE and
DISPAT_DIR, a group DISPAT_GROUP. The names are the release environment's, so
a script moves between a stage and a loop unchanged.

Every iteration runs where the invocation stands, or where --in points; no
item is cd'ed into, so a relative path means one thing throughout, and
DISPAT_DIR is what a script that wants the item's folder reads. The loop is
sequential: a shell's for runs one body at a time, and concurrency over a
selection is what "dispat run" already is.

The first failing item stops the loop and its exit code becomes the command's;
--keep-going runs the rest and still reports that first code, and --on-failure
replaces it once for the whole loop. An empty list runs nothing and exits 0,
which is what "for x in $EMPTY" does; --require-items makes it exit 1 instead.

Needs no config file and no git repository for a literal list, unless --in
names a package, a space or the root. Every other source is a question about
the monorepo and reads the configuration; --changed, --unchanged and --since
read the repository too. Note that the word now shadows a run script called
"for": spell that one "dispat run for".`,
		flags: append(append(append([]string{}, forFlags...),
			"since", "consumers"), append(append([]string{}, selectionFlags...), helperFlags...)...),
	},
	{
		name:     cmdExec,
		args:     "<script>",
		argsLong: "<script> [-- args...]",
		short:    "run one declared script here, once",
		long: `Run one script the configuration declares, in the current folder, once.
Unlike "dispat run" it computes no plan, sweeps nothing and consults no
dependency graph, which is what makes it usable as a step inside another
script.

One subject decides where the script is looked up and whose environment it
gets: --for pkg:<name>, --for space:<name>, or neither for the top level. The
folder the command was invoked from is consulted only when --for cwd asks it
to, so every other invocation resolves the same way from anywhere in the
repository.

Without --fallback only the named level is read, so a script defined a level
away fails loudly instead of running text nobody asked for; with it the name
resolves the way "dispat run" resolves it, the package over its space over the
top level. --script-from takes the same values as --for and moves the lookup
alone, leaving the environment with the subject.

--env says what the subject adds: static, its declared env, which is the
default; dispat, the DISPAT_* release variables; or both. The last two compute
a plan, and nothing else here does.

--in runs the script somewhere other than where the invocation stands: a
folder path, or the same pkg:<name>, space:<name>, root or cwd.

Anything after "--" is appended to the declared command, so a script in the
configuration takes a value from the terminal without being edited. The
--on-failure script never receives them.`,
		flags: append(append([]string{}, execFlags...), helperFlags...),
	},
	{
		name:  cmdSelfUpdate,
		short: "replace this binary with the latest release",
		long: `Replace the running dispat with the latest stable release of it: the
binary for this platform is downloaded from the GitHub release, checked
against the size and checksum the release published, run once to prove it
works, and only then moved into place. The binary it replaces is kept beside
it as <name>.backup and removed on its own after a week.

--check reports what the same invocation would do and exits 1 when there is
something to install, which is the CI gate. --prerelease considers the
prereleases too, --release installs one named version, downgrades included,
and --force installs the selection even when it is not newer.

--rollback puts the kept binary back and downloads nothing. It rotates, so
the binary it replaces becomes the new backup and a second --rollback
returns. Needs no config file and no git repository.`,
		flags: []string{"check", "force", "prerelease", "release", "rollback",
			"owner", "repo", "api-url", "token-env"},
	},
	{
		name:  cmdInstall,
		args:  "<repo>",
		short: "install a tool from any GitHub repository's releases",
		long: `Install a tool published as a GitHub release asset, the way self-update
installs dispat: the release is picked off the repository's listing, the file
is downloaded, checked against the size and the checksum the release
published, and only then moved into place. Anything already at the
destination is kept beside it as <name>.backup and removed on its own after a
week.

The repository is named however it is at hand: https://github.com/owner/repo,
the clone or SSH URL, a page inside it, or the owner/repo shorthand. A host
that is not github.com is taken for a GitHub Enterprise install and its API
derived from it, which --api-url overrides.

--asset says which of the release's files to install, by name or by glob, with
{os}, {arch}, {version}, {tag} and {name} expanded, so one invocation keeps
working across releases. A release carrying exactly one file needs none; every
other release is refused with its files listed, because guessing which of them
is the binary is how the wrong thing gets installed.

--bin-dir says where it goes and --as what it is called there. Without them
the folder is $DISPAT_BIN_DIR, then /usr/local/bin when it is writable, then
~/.local/bin, and the name is the repository's own.

--pipe hands the verified file to a command's standard input instead of
installing it, run in --bin-dir, which is how an asset that is not a bare
binary is dealt with: --pipe 'tar -xz' unpacks an archive there, --pipe sh
runs a release's own install script. $DISPAT_ASSET names the same file by
path, for a command that has to seek.

--release installs one named version, --prerelease considers the prereleases
too, and --tag-prefix says what the tags carry before their version ("v" by
default, empty for a repository tagging 1.2.3).

--check reports what would be installed and exits 1 when the destination does
not already hold that exact file, which is the gate a provisioning script
puts in front of an install. --force installs it even when it does.
--rollback puts the kept binary back and downloads nothing.

Needs no config file and no git repository.`,
		flags: []string{"asset", "bin-dir", "as", "pipe", "tag-prefix",
			"check", "force", "prerelease", "release", "rollback", "api-url", "token-env"},
	},
	{
		name:  cmdScanner,
		args:  "[folder]",
		short: "print what a folder's manifests declare",
		long: `Print what the folder's manifests declare: its identity, its ecosystem
and every dependency with its declared range. Without a folder, --root is
scanned. Needs no config file and no git repository.

Four gates turn the scan into a CI check, each failing with exit 1 and one
error event per finding. --verify-unlinked fails when any manifest still
carries a local-link directive, exactly what --link-local can inject;
--verify-linked is its inverse, failing when no manifest carries one.
--forbid-range fails for every declared range matching its pattern
(--forbid-range 'workspace:*' before publishing); --require-range fails
when nothing matches. The link gates and the range gates are unrelated
checks and combine freely, except that one flag and its inverse cannot be
asked together.`,
		flags: []string{"root-only", "verify-unlinked", "verify-linked",
			"forbid-range", "require-range", "strict"},
	},
	{
		name:  cmdWriter,
		args:  "<manifest>...",
		short: "edit manifests in place, preserving their formatting",
		long: `Edit the named manifests in place, preserving their formatting:
--set-version rewrites the manifest's own version, --set sets a
dependency's declared range, --link points one at a local folder,
--drop-links removes every local-link directive a manifest carries
without being told the names, and --set-build writes the build counter
where the format keeps one. Needs no config file and no git repository.`,
		flags: []string{"manifest-format", "set-version", "set-build", "set", "link", "drop-links", "strict"},
	},
	{
		name:  cmdReplacer,
		args:  "<file>...",
		short: "replace literal text in any file, parsing nothing",
		long: `Replace literal text in the named files, parsing nothing: --replace
'find=>write', repeatable and applied in order, for the versions no
manifest writer reaches: a Gradle coordinate, a Helm chart, a README.
Needs no config file and no git repository.`,
		flags: []string{"replace", "strict"},
	},
}

// lookupCommand finds a table entry by command word.
func lookupCommand(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// flagOwners names the commands whose help lists a flag, in table order.
//
// It is the command table read backwards, and deliberately so: the lists that
// render the help are the same lists that decide which command a flag belongs
// to, so a flag can never be documented for one command and accepted by
// another. A flag no entry claims has no owner to name, which the drift guard
// in cli_test.go makes impossible.
func flagOwners(name string) []string {
	var owners []string
	for _, c := range commands {
		for _, n := range c.flags {
			if n == name {
				owners = append(owners, c.name)
				break
			}
		}
	}
	return owners
}

// flagBlock renders the named flags of the master set. Building a scratch
// set out of the master's own *Flag values is what keeps one usage string
// per flag: the per-command help and the flag reference read the same text.
func flagBlock(master *pflag.FlagSet, names []string) string {
	sub := pflag.NewFlagSet("", pflag.ContinueOnError)
	for _, n := range names {
		if f := master.Lookup(n); f != nil {
			sub.AddFlag(f)
		}
	}
	return sub.FlagUsages()
}

// printUsage writes the program help: what dispat is invoked as, every
// command with its one-line summary, and the global flags. A command's own
// flags are one "dispat <command> --help" away, which is what keeps this
// page readable as the flag set grows.
func printUsage(out io.Writer, master *pflag.FlagSet) {
	var b strings.Builder
	b.WriteString(logo)
	b.WriteString("\n\nusage: dispat [command] [flags]\n\ncommands:\n")
	for _, c := range commands {
		b.WriteString(fmt.Sprintf("  %-24s %s\n", strings.TrimSpace(c.name+" "+c.args), c.short))
	}
	b.WriteString("\nglobal flags:\n")
	b.WriteString(flagBlock(master, globalFlags))
	b.WriteString("\nrun \"dispat <command> --help\" for a command's own flags.\n")
	fmt.Fprint(out, b.String())
}

// printCommandUsage writes one command's help: how it is invoked, what it
// does, its own flags and the global ones. An unknown word falls back to the
// program help rather than printing nothing.
func printCommandUsage(out io.Writer, master *pflag.FlagSet, name string) {
	c, ok := lookupCommand(name)
	if !ok {
		printUsage(out, master)
		return
	}
	var b strings.Builder
	b.WriteString(logo)
	b.WriteString("\n\nusage: dispat " + strings.TrimSpace(c.name+" "+c.usageArgs()) + " [flags]\n\n")
	b.WriteString(c.long)
	b.WriteString("\n")
	if own := flagBlock(master, c.flags); own != "" {
		b.WriteString("\nflags:\n")
		b.WriteString(own)
	}
	b.WriteString("\nglobal flags:\n")
	b.WriteString(flagBlock(master, globalFlags))
	fmt.Fprint(out, b.String())
}
