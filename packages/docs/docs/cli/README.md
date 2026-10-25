# CLI reference

Start with `dispat status` to inspect the release plan and `dispat preview` to read the pending notes. When you are
ready to release, run `dispat` or `dispat release`.

The commands below are grouped by task. Each command's page lists its flags and examples; global flags and exit codes
are covered on this page.

```
dispat [command] [flags]
```

## Commands

### Inspect and set up

| Command | What it does |
| --- | --- |
| `init` | Write a starter config. [Options](./init.md). |
| `status` | Show the release plan and version bumps without changing files. [Options](./status.md). |
| `preview` | Read pending release notes. [Options](./preview.md). |
| `diagnostics <message>` | Check a commit message against parser rules. [Options](./diagnostics.md). |
| `semver parse|build` | Parse or construct one semantic version as JSON. [Options](./semver.md). |
| `compute` | Suggest dependencies and starting versions from manifests. [Options](./compute.md). |

### Release and record

| Command | What it does |
| --- | --- |
| `release` (default) | Run version, build, and publish steps, then record releases and tags. Takes the [release lock](./release.md#the-release-lock) first. [Options and partial releases](./release.md). |
| `changelog` | Write pending changelog entries. [Options](./changelog.md). |
| `autoversion` | Update manifests to the planned versions. [Options](./autoversion.md). |
| `commit` | Create a checked source commit with `-m`, or per-package release commits. [Options](./commit.md). |
| `github` | Create GitHub releases for packages that opted in. [Options](./github.md). |

### Run scripts

| Command | What it does |
| --- | --- |
| `run <script>` | Run a declared script in changed packages, in dependency order. [Options](./run.md). |
| `exec <script>` | Run one declared script once. [Options](./exec.md). |
| `if [cond]` | Choose a shell command based on a variable, file, or changed packages. [Options](./if.md). |
| `for [item]...` | Run a shell command for each item, package, space, or group. [Options](./for.md). |
| `trigger <event>` | Send a `script.<event>` webhook event. [Options](./trigger.md). |
| `worker` | Accept assigned build and validation tasks. [Options](./worker.md) and [distributed execution](../distributed-execution.md). |

### Read and edit files

| Command | What it does |
| --- | --- |
| `scanner [folder]` | Read package names, versions, and dependencies from manifests. [Options](./scanner.md). |
| `writer <manifest>...` | Edit version and dependency fields in named manifests. [Options](./writer.md). |
| `replacer <file>...` | Replace literal text in named files. [Options](./replacer.md). |
| `autowriter` | Apply manifest edits across selected packages. [Options](./autowriter.md). |
| `autoreplacer` | Replace literal text across selected packages. [Options](./autoreplacer.md). |

### Install and update tools

| Command | What it does |
| --- | --- |
| `install <repo>` | Install a tool from GitHub release assets. [Options](./install.md). |
| `self-update` | Update the installed dispat binary. [Options](./self-update.md). |

## Global flags

### `--root`

Start configuration lookup in this directory. The default, `.`, is your current directory. The directory containing
the resolved config becomes the effective monorepo root (see `--config`), so commands can be run from a package folder.

### `--config`

The default is auto. Set the config file as a path relative to `--root` or as an absolute path. Leave this unset to let
dispat discover the file using the [resolution rules](../configuration/README.md). Passing an explicit path uses that
exact file with no fallback and no ascent.

### `--configs`

Import one additional configuration into a combined polyrepository workspace. Repeat the flag for more files. Paths
start at the effective control root, canonical duplicates load once, and the presence of this flag enables
[polyrepository mode](../control-repository.md#source-history-mode). `--config` still selects the one control file;
`--configs` adds source-owned declarations. The named file establishes that source's ordinary local root, space, and
package configuration layers. A centrally declared path entering another linked repository does not trigger discovery
there.

### `--polyrepo`

Read the independent Git histories of linked repositories. Identity-linked fleets enable this automatically when the
entry configuration states a non-empty `repository` identity; its optional `repositories` roster names other peers. A
control repository can enable it with `polyrepo: true` or imported `configs`. It requires each declared source to be
initialized at its pinned gitlink commit with complete history. See
[A control repository](../control-repository.md#source-history-mode).

Passing `--polyrepo=false` to an identity-linked repository is the standalone escape hatch: the
fleet links are not walked and that repository releases alone. See
[Releasing one peer alone](../choreographed-repositories.md#releasing-one-peer-alone).

### `--env-file`

The default is `./.env`. Read environment variables from this file instead of `./.env`. You can repeat this flag, and
later files win. A named file that does not exist stops the run, but the default file is allowed to be missing; see
[The `.env` file](../configuration/dotenv.md).

### `--concurrency`

The default comes from config. Override the concurrency limit. Pass one value for both stages (`7`) or separate
values for `build,publish` (`4,2`). `dispat run` uses the build value as its budget.

### `--log-level`

The default comes from config. Override the log level. Choose `trace`, `debug`, `info`, `warn`, or `error`.

### `--log-format`

The default comes from config. Override the log format. Choose `pretty` or `json`.

### `--quiet-parser`

The default comes from config. Override `parser.quiet` to hide the commit-message parser's own diagnostics. Pass
`--quiet-parser=false` to show them again when your config sets `quiet: true`; see
[the parser options](../configuration/parser.md#quiet). Standalone diagnostics and source-commit authoring always show
its validation diagnostics so the author can correct the proposed message.

### `--version`

Print the dispat logo, version, and platform (`dispat 1.2.3 (darwin_arm64)`) and exit. This needs no config file.
Release binaries carry the release tag's version, local builds report `dev`, and a binary installed with `go install`
says so in the parenthesis because that decides how it is
[updated](../reference/self-update.md#how-you-installed-it-matters).

On `preview` the flag names a past release instead, and the preview renders that release's notes; see
[Previewing a past release](./preview.md#previewing-a-past-release).

### `--help`, `-h`

Print help and exit. Running this without a command word prints the command list and the global flags. Running it after
a command word prints that command's synopsis and its own flags; see [Getting help](#getting-help).

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

From dispat 1.8.2, `dispat --help` links to the AI agent skill (agent work guide), configuration reference, and API
reference captured by that binary's release tag. Development builds explicitly link to upcoming documentation. Agents
should check for newer agent skill patches on the installed CLI's major/minor line, while keeping the configuration and
API references pinned to the installed binary.

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
flag after taking the [release lock](./release.md#the-release-lock) and planning. Use `dispat status --require-release`
for a CI check that does not take the lock or contact the remote.

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
