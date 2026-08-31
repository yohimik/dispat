# CLI reference

This page lists every dispat command with its flags and exit codes. Run `dispat` on its own to release. Use the other
commands to narrow that release or perform a single step.

```
dispat [command] [flags]
```

## Commands

| Command                   | Effect                                                                                                            |
|---------------------------|---------------------------------------------------------------------------------------------------------------------|
| `release` (default)       | Plan the release, print the graph, and run the version, build, and publish steps for every changed package before recording releases and tagging. dispat takes the [release lock](./release.md#the-release-lock) first to refuse concurrent releases instead of racing them. Pass `--package`, `--space`, or `--group` to release part of the graph; see [The release command](./release.md). |
| `status`                  | Plan the release, print the graph with computed version bumps, and exit. dispat executes, tags, and writes nothing. This accepts the same selection flags as a release; see [The status command](./status.md).          |
| `run <script>`            | Run a script in every changed package that declares it. dispat runs these in graph order; see [The run command](./run.md).        |
| `init`                    | Write a starter config file and exit; see [The init command](./init.md).                                  |
| `preview`                 | Print pending release notes and exit; see [The preview command](./preview.md).                            |
| `changelog`               | Write the pending changelog entry immediately; see [The changelog command](./changelog.md).                               |
| `autoversion`             | Reconcile manifests to the planned versions; see [The autoversion command](./autoversion.md).                         |
| `autowriter`             | Apply one set of manifest edits to every covered package; see [The autowriter command](./autowriter.md). |
| `autoreplacer`         | Replace literal text across every covered package; see [The autoreplacer command](./autoreplacer.md). |
| `commit`                  | Create the per-package release commit; see [The commit command](./commit.md).                               |
| `github`                  | Create the per-package GitHub release immediately; see [The github command](./github.md).                           |
| `trigger <event>`         | Deliver one script-raised `script.<event>` webhook event, from inside a stage script; see [The trigger command](./trigger.md). |
| `compute`                 | Derive the dependency graph and the starting versions from the packages' manifests; see [The compute command](./compute.md). |
| `if [cond]`               | Run one of several shell scripts. dispat chooses the script based on a condition matching the environment, the filesystem, or the changed packages; see [The if command](./if.md). |
| `exec <script>`           | Run one declared script here exactly once. You can run it for a named subject or the folder you are in; see [The exec command](./exec.md). |
| `self-update`             | Replace this binary with the latest release; see [The self-update command](./self-update.md).                             |
| `install <repo>`          | Install a tool from any GitHub repository's releases; see [The install command](./install.md).                            |
| `scanner [folder]`        | Print what a folder's manifests declare; see [The scanner command](./scanner.md).                     |
| `writer <manifest>...`    | Edit manifests in place while preserving their format; see [The writer command](./writer.md).                  |
| `replacer <file>...`      | Replace literal text in any file without parsing it; see [The replacer command](./replacer.md).                             |

## Global flags

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--root`              | `.`         | Set the starting directory for config resolution. This defaults to your current directory. The *effective* monorepo root is the directory containing the config file (see `--config`), so the CLI works from inside a package folder. |
| `--config`            | auto        | Set the config file name relative to `--root`. Leave this unset to let dispat discover the file using the [resolution rules](../configuration/README.md). Passing an explicit name uses that exact file with no fallback and no ascent.  |
| `--env-file`          | `./.env`    | Read environment variables from this file instead of `./.env`. You can repeat this flag, and later files win. A named file that does not exist stops the run, but the default file is allowed to be missing; see [The `.env` file](../configuration/dotenv.md). |
| `--concurrency`       | from config | Override the concurrency limit. Pass one value for both stages (`7`) or separate values for `build,publish` (`4,2`). `dispat run` uses the build value as its budget.                                                                                 |
| `--log-level`         | from config | Override the log level. Choose `trace`, `debug`, `info`, `warn`, or `error`.                                                                                                                                                   |
| `--log-format`        | from config | Override the log format. Choose `pretty` or `json`.                                                                                                                                                                          |
| `--quiet-parser`      | from config | Override `parser.quiet` to hide the commit-message parser's own diagnostics. Pass `--quiet-parser=false` to show them again when your config sets `quiet: true`; see [the parser options](../configuration/parser.md#quiet). |
| `--version`           |             | Print the dispat logo, version, and platform (`dispat 1.2.3 (darwin_arm64)`) and exit. This needs no config file. Release binaries carry the release tag's version, local builds report `dev`, and a binary installed with `go install` says so in the parenthesis because that decides how it is [updated](../reference/self-update.md#how-you-installed-it-matters). |
| `--help`, `-h`        |             | Print help and exit. Running this without a command word prints the command list and the global flags. Running it after a command word prints that command's synopsis and its own flags; see [Getting help](#getting-help).                            |

Every other flag belongs to a command and is listed on that command's page.

Flags follow a strict precedence. An explicitly set flag overrides the config file, which overrides the flag default,
which overrides the built-in default.

## Getting help

Run `dispat --help` to list every command with a one-line summary and the flags that apply everywhere. A command's own
flags are one step away. Run `dispat <command> --help` to print only that command's synopsis, description, and specific
flags.

```sh
dispat --help              # the command list and the global flags
dispat run --help          # run's synopsis and its own flags
dispat github --help       # the github step's, and so on
```

Help needs no config file and no git repository. It exits `0` because asking for help is not an error. A word that is
not a command name acts as the [run shorthand](./run.md), so `dispat lint --help` prints help for the run command.

## Exit codes

dispat uses four exit codes:

| Code | Meaning |
|------|---------|
| `0` | Success. This includes a run where nothing had changed. |
| `1` | A configuration or planning error, a refused release, or an interrupted run. This also covers runs where at least one package failed or a step failed after its release was already out. See [`commitErrors`](../configuration/parser.md#commiterrors) and the [release lock](./release.md#the-release-lock). |
| `2` | A bad command line, including a flag that belongs to another command. |
| `3` | `--require-release` was passed to `release` or `status` and the plan had nothing to release. |

dispat refuses a release only *before* any of it happens, because once the first build script runs, nothing aborts the
run. A package can fail and cause its consumers to be skipped, but every other package still releases and the finalize
phase still records what published. Once a package's publish succeeds, any subsequent failure is reported as a
[critical](../internals/architecture.md#after-the-point-of-no-return) error that makes the command exit `1` at the end
with all remaining work completed.

Both `release` and `status` print the plan's diagnostics before the graph. Both commands narrow the plan to their
[selection](./release.md) between the two steps.

The `status` command exits `1` only for a `--strict` selection the plan cannot release or a repository-scoped failure
(an unreadable tag, a version that would go backwards, a dependency cycle, a shallow clone). Passing
`--require-release` with nothing to release exits `3`. This unique code lets a pipeline tell "nothing to do" from
"something is wrong".

For any other scenario, the printed plan is exactly what a release would use, so there is nothing to fail over. When a
release *would* refuse under `commitErrors: error`, `status` prints a warning and still exits `0`. A withheld package
or a split versioning group produces a warning on both commands and exits `0` without `--strict`.

An empty plan is a success on both commands because releasing nothing is correct for a repository with nothing pending.
Pass `--require-release` to opt out of this behavior on `release` and `status` alike when your CI stage must publish
something; see [Gating a pipeline on the plan](../reference/ci.md#gating-a-pipeline-on-the-plan). dispat answers this
flag before taking the [release lock](./release.md#the-release-lock), so an empty run never queues real releases behind
it.

The two [shell helpers](./if.md#exit-codes) are the exception. The `if` and `exec` commands hand back the exit code of
the script they ran, so `dispat if CI --then 'exit 7'` exits `7`. This ensures a pipeline gating on a specific code
still works with a helper in the middle.

Pass `--on-failure` to replace that code with a custom one. An exit code of `2` still means a bad command line, which
is worth knowing if your script exits `2` itself. A false condition is not a failure, but an `if --changed` that cannot
be evaluated at all (like an unresolvable git revision or an unloaded configuration) exits `1`.

## Interruption

A Ctrl-C or a CI job kill stops a release cleanly rather than mid-write. dispat terminates in-flight scripts, reports
their packages as `cancelled`, and prevents unstarted packages from starting. A package whose publish had already
succeeded still gets its durable record.

The changelog entry, the annotated tag, and the release commit and push still happen for published packages, because
losing that record would re-release the same version on the next run. No more operator scripts run (no hooks, no
announce). The command exits `1`, and the next run releases exactly what the interrupted one still owed.
