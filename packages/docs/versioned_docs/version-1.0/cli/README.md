# CLI reference

Every dispat command, with its flags and its exit codes. `dispat` on its own releases; every other command either
narrows that release or performs one of its steps by itself.

```
dispat [command] [flags]
```

## Commands

| Command                   | Effect                                                                                                            |
|---------------------------|---------------------------------------------------------------------------------------------------------------------|
| `release` (default)       | Plan, print the graph, then run version/build/publish for every changed package, record releases, tag. Takes the [release lock](./release.md#the-release-lock) first, so two releases at once are refused rather than raced. `--package` / `--space` / `--group` release part of the graph; see [The release command](./release.md). |
| `status`                  | Plan and print the graph with computed version bumps, then exit. Nothing is executed, tagged or written. Takes the release's own selection flags; see [The status command](./status.md).          |
| `run <script>`            | Run a script in every changed package that has it, graph-ordered; see [The run command](./run.md).        |
| `init`                    | Write a starter config file and exit; see [The init command](./init.md).                                  |
| `preview`                 | Print pending release notes and exit; see [The preview command](./preview.md).                            |
| `changelog`               | Write the pending changelog entry now; see [The changelog command](./changelog.md).                               |
| `autoversion`             | Reconcile manifests to the planned versions; see [The autoversion command](./autoversion.md).                         |
| `autowriter`             | Apply one set of manifest edits to every covered package; see [The autowriter command](./autowriter.md). |
| `autoreplacer`         | Replace literal text across every covered package; see [The autoreplacer command](./autoreplacer.md). |
| `commit`                  | Create the per-package release commit; see [The commit command](./commit.md).                               |
| `github`                  | Create the per-package GitHub release now; see [The github command](./github.md).                           |
| `compute`                 | Derive the dependency graph and the starting versions from the packages' manifests; see [The compute command](./compute.md). |
| `if <cond>`               | Run one of several shell scripts, chosen by a condition on the environment, the filesystem or the changed packages; see [The if command](./if.md). |
| `exec <script>`           | Run one declared script here, once, for a named subject or the folder you are in; see [The exec command](./exec.md). |
| `self-update`             | Replace this binary with the latest release; see [The self-update command](./self-update.md).                             |
| `scanner [folder]`        | Print what a folder's manifests declare; see [The scanner command](./scanner.md).                     |
| `writer <manifest>...`    | Edit manifests in place, format-preserving; see [The writer command](./writer.md).                  |
| `replacer <file>...`      | Replace literal text in any file, parsing nothing; see [The replacer command](./replacer.md).                             |

## Global flags

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--root`              | `.`         | Where to start config resolution, usually where you stand. The *effective* monorepo root is the directory the config file is found in (see `--config`), so the CLI works from inside a package folder. |
| `--config`            | auto        | Config file name, relative to `--root`. When not set, the file is discovered under the [resolution rules](../configuration/README.md); an explicit name is used as-is, with no fallback and no ascent.  |
| `--env-file`          | `./.env`    | Read environment variables from this file instead of `./.env`. Repeatable, later files winning; a named file that does not exist stops the run, while the default one simply may not exist. See [The `.env` file](../configuration/dotenv.md). |
| `--concurrency`       | from config | Override: one value for both stages (`7`) or `build,publish` (`4,2`). `dispat run` uses the build value as its budget.                                                                                 |
| `--log-level`         | from config | Override: `trace`, `debug`, `info`, `warn`, `error`.                                                                                                                                                   |
| `--log-format`        | from config | Override: `pretty` or `json`.                                                                                                                                                                          |
| `--quiet-parser`      | from config | Override `parser.quiet`: hide the commit-message parser's own diagnostics. `--quiet-parser=false` shows them again when the config sets `quiet: true`; see [the parser options](../configuration/parser.md#quiet). |
| `--version`           |             | Print the dispat logo, version and platform (`dispat 1.2.3 (darwin_arm64)`) and exit; needs no config file. Release binaries carry the release tag's version, local builds report `dev`, and a binary installed with `go install` says so in the same parenthesis, since that decides how it is [updated](../reference/self-update.md#how-you-installed-it-matters). |
| `--help`, `-h`        |             | Print help and exit. Without a command word, the command list and the global flags; after one, that command's synopsis and its own flags. See [Getting help](#getting-help).                            |

Every other flag belongs to a command and is listed on that command's page.

Flag precedence (via viper): explicitly set flag > config file > flag default > built-in default.

## Getting help

`dispat --help` lists every command with a one-line summary, plus the flags that apply everywhere. A command's own
flags are one step away: `dispat <command> --help` prints that command's synopsis, what it does, and the flags it
reads, and nothing else, so the page stays readable however many commands dispat grows.

```sh
dispat --help              # the command list and the global flags
dispat run --help          # run's synopsis and its own flags
dispat github --help       # the github step's, and so on
```

Help needs no config file and no git repository, and exits `0`: asking for help is not an error. A word that is not a
command name is the [run shorthand](./run.md), so `dispat lint --help` prints run's help.

## Exit codes

There are three:

| Code | Meaning |
|------|---------|
| `0` | Success, which includes a run where nothing had changed. |
| `1` | A configuration or planning error, a refused release (see [`commitErrors`](../configuration/parser.md#commiterrors) and the [release lock](./release.md#the-release-lock)), at least one package that failed, a step that failed after its release was already out, or an interrupted run. |
| `2` | A bad command line. |

A release is refused only *before* any of it happens. Once the first build script runs, nothing aborts the run: a
package can fail and its consumers can be skipped behind it, but every other package still releases and the finalize
phase still records what published. And once a package's publish succeeds, nothing can fail that package at all: a tag,
changelog entry, GitHub release, release commit or push that fails after that point is reported as a
[critical](../internals/architecture.md#after-the-point-of-no-return) and makes the command exit `1` at the end, with all the
remaining work already done.

Both `release` and `status` print the plan's diagnostics before the graph, and both narrow it to their
[selection](./release.md) between the two.

`status` exits `1` in only two cases: a repository-scoped failure (an unreadable tag, a version that would go
backwards, a dependency cycle, a shallow clone), or a `--strict` selection the plan cannot release.
`--require-release` with nothing to release exits `3`, a code of its own, so a pipeline gating on it can tell
"nothing to do" from "something is wrong". For anything else the plan it just printed is the plan a release would
use, so there is nothing to fail over. When a release *would* refuse, for example under `commitErrors: error`,
`status` says so in a warning and still exits `0`. A withheld package or a split versioning group is a warning on
both commands and exits `0` without `--strict`.

An empty plan is a success on both commands: releasing nothing is what a repository with nothing pending should do.
`--require-release` opts out of that, on `release` and `status` alike, for the CI stage whose point is that this run
publishes something; see [Gating a pipeline on the plan](../reference/ci.md#gating-a-pipeline-on-the-plan). On
`release` it is answered before the [release lock](./release.md#the-release-lock) is taken, so a run that would
publish nothing never makes a real release queue behind it.

The two [shell helpers](./if.md#exit-codes) are the exception: `if` and `exec` hand back the exit code of the
script they ran, so `dispat if CI --then 'exit 7'` exits `7` and a pipeline gating on a specific code still works with a
helper in the middle. `--on-failure` replaces that code with its own. `2` still means a bad command line, which is worth
knowing if a script exits `2` itself. A condition that is false is not a failure, but an `if --changed` that cannot be
evaluated at all, a revision git cannot resolve or a configuration that cannot be loaded, exits `1`.

## Interruption

Ctrl-C (or a CI job kill) stops a release cleanly rather than mid-write. In-flight scripts are terminated and their
packages reported as `cancelled`; packages that had not started never start. A package whose publish had already
succeeded still gets its durable record: the changelog entry, the annotated tag and, in release-commit mode, the release
commit and push still happen for it, because losing the record of a completed publish would re-release the same version
on the next run. No more operator scripts run (no hooks, no announce). The command exits `1`, and the next run releases
exactly what the interrupted one still owed.
