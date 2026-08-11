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
	name  string
	args  string
	short string
	long  string
	flags []string
}

// selectionFlags are the two every package-selecting command shares.
var selectionFlags = []string{"package", "space"}

// globalFlags apply to every command, so they are rendered separately rather
// than repeated in each entry.
var globalFlags = []string{"root", "config", "concurrency", "log-level", "log-format",
	"quiet-parser", "version", "help"}

// commands is the table. Order is the order the command list prints in:
// the everyday commands, then the step commands, then the manifest tools.
var commands = []command{
	{
		name:  cmdRelease,
		short: "build and publish changed packages (default)",
		long: `Compute the release plan and execute it: the version, build and publish
stages of every changed package, in dependency order, then the release
commit, the tags and the GitHub releases the configuration asks for.

This is what a bare "dispat" does.`,
	},
	{
		name:  cmdStatus,
		short: "print the project graph and new versions, without building",
		long: `Compute the same plan a release would and print it — every package, its
new version, its channel transition and why it is releasing — without
building, tagging or writing anything.

Exits 0 even when a release would refuse, because showing the plan is the
job; only a repository that cannot produce a correct plan at all exits 1.`,
	},
	{
		name:  cmdRun,
		args:  "<script>",
		short: "run the named script inside each changed package that defines it",
		long: `Run the named script inside each changed package that defines it — its
own scripts, then its space's, then the top-level ones — honouring the
dependency graph, so a package waits for the providers it depends on.

--package and --space narrow that to part of the monorepo, as does the
package or space folder the command is invoked from. --since replaces the
release window with the commits since a git revision.

"dispat <script>" is a shorthand when <script> is not a command name.`,
		flags: append([]string{"on-error", "since", "consumers"}, selectionFlags...),
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
		long: `Print the pending release notes — breaking changes, features, fixes —
for every package with something pending, or for the selected ones.
Nothing is written and nothing is released.`,
		flags: selectionFlags,
	},
	{
		name:  cmdChangelog,
		short: "write the pending changelog entry now",
		long: `Write each covered package's pending changelog entry now, so a custom
flow can land it inside the release commit instead of after it. An entry
the file already carries is skipped (W222), which is also what makes the
release stage skip the entries written here.`,
		flags: append([]string{"file", "title", "date-format"}, selectionFlags...),
	},
	{
		name:  cmdAutoversion,
		short: "reconcile manifests to the planned versions",
		long: `Reconcile each covered package's manifests to the planned versions —
native auto-versioning, the same work the version stage does — and run the
space's syncLock scripts where the manifests changed.`,
		flags: append([]string{"range", "match", "manifests", "no-replace", "write-version", "sync-lock"},
			selectionFlags...),
	},
	{
		name:  cmdCommit,
		short: "create the per-package release commit",
		long: `Create each covered package's release commit: the package folder staged
plus the commit.include paths, the message rendered per commit.messageFormat.
--tag also creates the annotated release tag at the resulting commit, and
--push pushes the branch and, with --tag, the tags. A tag that already
exists at that commit is skipped (W223).`,
		flags: append([]string{"tag", "push", "name", "email", "remote", "message-format", "include"},
			selectionFlags...),
	},
	{
		name:  cmdGithub,
		short: "create the per-package GitHub release now",
		long: `Create each covered package's GitHub release now, so a flow can publish
it from its own stage — an announce script, say — instead of waiting for
the end of the run. A release the repository already carries is skipped
(W224), so a re-run after a later failure is a no-op.

Meant for a stage script: the opt-in and the files to attach are read from
DISPAT_EXPORT_GITHUB in the environment the stage handed the command, and
github.allPackages is the configuration-level opt-in for everything else.`,
		flags: append([]string{"owner", "repo", "api-url", "token-env", "target"}, selectionFlags...),
	},
	{
		name:  cmdCompute,
		short: "derive the dependency graph and the starting versions from the manifests",
		long: `Scan every package's manifests (package.json, go.mod, Cargo.toml,
pyproject.toml, composer.json, pom.xml, *.csproj, pubspec.yaml,
requirements*.txt, Dockerfile, compose.yaml) and suggest the config changes
they imply: the dependency edges between packages, and an initials entry for
every package already at a version no release tag carries yet.

--write applies all of them, --interactive confirms each, --check reports
only and exits 1 when suggestions exist, which is the CI gate. An edge
marked keep: true is never suggested for removal, an initials entry already
in the config is never rewritten, and --package/--space scope the suggestions
to those packages.`,
		flags: append([]string{"write", "interactive", "check"}, selectionFlags...),
	},
	{
		name:  cmdScanner,
		args:  "[folder]",
		short: "print what a folder's manifests declare",
		long: `Print what the folder's manifests declare: its identity, its ecosystem
and every dependency with its declared range. Without a folder, --root is
scanned. Needs no config file and no git repository.`,
		flags: []string{"root-only", "strict"},
	},
	{
		name:  cmdWriter,
		args:  "<manifest>...",
		short: "edit manifests in place, preserving their formatting",
		long: `Edit the named manifests in place, preserving their formatting:
--set-version rewrites the manifest's own version, --set sets a
dependency's declared range, --replace points one at a local folder.
Needs no config file and no git repository.`,
		flags: []string{"set-version", "set", "replace", "strict"},
	},
	{
		name:  cmdReplacer,
		args:  "<file>...",
		short: "replace literal text in any file, parsing nothing",
		long: `Replace literal text in the named files, parsing nothing: --sub
'find=>write', repeatable and applied in order, for the versions no
manifest writer reaches — a Gradle coordinate, a Helm chart, a README.
Needs no config file and no git repository.`,
		flags: []string{"sub", "strict"},
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
	b.WriteString("\n\nusage: dispat " + strings.TrimSpace(c.name+" "+c.args) + " [flags]\n\n")
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
