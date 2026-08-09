# CLI reference

```
dispat [command] [flags]
```

## Commands

| Command                   | Effect                                                                                                            |
|---------------------------|-------------------------------------------------------------------------------------------------------------------|
| `release` (default)       | Plan, print the graph, then run version/build/publish for every changed package, record releases, tag.            |
| `status`                  | Plan and print the graph with computed version bumps, then exit. Nothing is executed, tagged or written.          |
| `run <script> [package]`  | Execute a space run script over the changed packages, graph-ordered; see [The run command](#the-run-command).     |
| `init`                    | Write a starter config file and exit; see [The init command](#the-init-command).                                  |
| `test <script> <package>` | Run one top-level script in one package under the release environment; see [The test command](#the-test-command). |
| `preview [package]`       | Print pending release notes and exit; see [The preview command](#the-preview-command).                            |
| `changelog [package]`     | Write the pending changelog entry now; see [The step commands](#the-step-commands).                               |
| `autoversion [package]`   | Reconcile manifests to the planned versions; see [The step commands](#the-step-commands).                         |
| `commit [package]`        | Create the per-package release commit; see [The step commands](#the-step-commands).                               |
| `compute`                 | Derive the dependency graph from the packages' manifests; see [The compute command](#the-compute-command).        |

## Flags

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--root`              | `.`         | Where to start config resolution, usually where you stand. The *effective* monorepo root is the directory the config file is found in (see `--config`), so the CLI works from inside a package folder. |
| `--config`            | auto        | Config file name, relative to `--root`. When not set, the file is discovered under the [resolution rules](./configuration/README.md); an explicit name is used as-is, with no fallback and no ascent.  |
| `--concurrency`       | from config | Override: one value for both stages (`7`) or `build,publish` (`4,2`). `dispat run` uses the build value as its budget.                                                                                 |
| `--on-error`          | `skip`      | `run` only: what a failing script does to the failed package's dependents, `skip` (transitive) or `continue`. Either way the command exits `1` on any failure.                                         |
| `--since`, `-s`       |             | `run` only: select the packages the commits since a git revision address, instead of the release window; see [the run command](#the-run-command).                                                      |
| `--consumers`         |             | `run` only: additionally run every package that transitively depends on a selected one; see [the run command](#the-run-command).                                                                       |
| `--log-level`         | from config | Override: `trace`, `debug`, `info`, `warn`, `error`.                                                                                                                                                   |
| `--log-format`        | from config | Override: `pretty` or `json`.                                                                                                                                                                          |
| `--format`            | `json`      | `init` only: the config file format to write (`json`, `yaml` or `toml`).                                                                                                                               |
| `--write`             |             | `compute` only: apply every suggestion to the config file (previous copy saved as `<name>.backup`).                                                                                                    |
| `--interactive`, `-i` |             | `compute` only: confirm each suggestion (`y`/`N` on stdin) before applying it; wins over `--write`.                                                                                                    |
| `--check`             |             | `compute` only: report only and exit `1` when suggestions exist: the CI gate for a config lagging the manifests. Overrides both apply modes.                                                           |
| `--tag`               |             | `commit` only: also create the annotated release tag at the resulting commit; an identical existing tag is skipped.        |
| `--push`              |             | `commit` only: push the branch, and with `--tag` the tags; tags already on the remote are skipped.                         |
| `--name`, `--email`   | from config | `commit` only: override the `commit.name` / `commit.email` committer identity.                                             |
| `--remote`            | from config | `commit` only: override the `commit.remote` push target.                                                                   |
| `--message-format`    | from config | `commit` only: override the `commit.messageFormat` template.                                                               |
| `--include`           | from config | `commit` only: override the `commit.include` extra staged paths.                                                           |
| `--file`, `--title`, `--date-format` | from config | `changelog` only: override the matching `changelog.*` values for every package of the invocation.          |
| `--range`, `--match`, `--manifests`, `--write-version` | from config | `autoversion` only: override the matching `autoVersion.*` policy for the invocation.     |
| `--sync-lock`         | `true`      | `autoversion` only: run the syncLock scripts for packages whose manifests changed; `--sync-lock=false` skips them.         |
| `--version`           |             | Print the dispat logo and version (`dispat 1.2.3`) and exit; needs no config file. Release binaries carry the release tag's version, local builds report `dev`.                                        |
| `--help`              |             | Print the logo and usage.                                                                                                                                                                              |

Flag precedence (via viper): explicitly set flag > config file > flag default > built-in default.

## The run command

`dispat run <script>` plans, then executes the named
[space run script](./configuration/spaces.md#runscripts-and-dispat-run) inside each changed package, honouring the
dependency graph. Nothing is released or tagged. A failing script's dependents are skipped or kept running per
`--on-error`.

Four ways to select what it covers:

- **Default**: the changed packages, the same set a release would process.
- **A target**: `dispat run <script> <package>` runs in exactly that package, changed or not, with no graph. Naming an
  unknown package, or one whose space does not define the script, is an error: a targeted run that runs nothing is how a
  typo hides.
- **A window**: `--since <rev>` (`-s`) selects the packages the commits in `rev..HEAD` address: `HEAD~1` for the last
  commit (per-commit CI), `origin/main` for this branch's own commits (PR pipelines), a release tag, or `all` for every
  package. Selection follows the planner's [scope semantics](./commits.md#scope-sets): a commit's written scopes are
  authoritative, and only scopeless units fall back to the files they changed.
- **Downstream expansion**: `--consumers` additionally selects every package that transitively depends on a selected one
  (a consumer pulled in brings its own consumers), so downstream packages re-run with a change the window alone would
  not reach. The added packages run whether or not they changed, after their selected providers, with the ordinary
  `--on-error` cascade.

`--since` and `--consumers` are each mutually exclusive with an explicit `[package]`.

`dispat <script>` is a shorthand whenever `<script>` is not a command name. It takes no package argument, but invoked
from inside a package folder (or any subdirectory of it) it narrows to that package; from the monorepo top it covers
every changed package. `--since` and `--consumers` override the folder narrowing: an explicit flag beats inference.

## The init command

`dispat init` writes a starter config file into `--root` (`dispat.json`, or `dispat.yaml` / `dispat.toml` with
`--format`) and exits. An existing file is never overwritten; that is an error. So is a `--root` that is not a git
repository root (no `.git`): the config establishes the effective monorepo root, so it belongs next to `.git`. Needs no
config file.

## The test command

`dispat test <script> <package>` plans, then runs the named top-level script (a key of `scripts`) once, inside the
package's folder, with the package's full [`DISPAT_*` environment](./environment.md) (`DISPAT_STAGE` is
`test:<script>`). Nothing is released, tagged or written; it is a way to try a script under exactly the input a stage
would hand it. The package does not have to be changed: an unchanged package's environment carries its baseline as both
the old and new version.

## The preview command

`dispat preview [package]` plans, then prints the pending release notes: the breaking-changes/features/fixes sections
plus provider updates that the next release's changelog entry and GitHub release body would carry. With a package name
the preview covers that package; without one it covers every package that has something pending, in publish order. It
follows the [release-notes windowing](./configuration/records.md#changelog), so a pending prerelease previews only its
own changeset. Prints `no pending changes` when nothing is.

## The step commands

`dispat changelog`, `dispat autoversion` and `dispat commit` expose the release pipeline's native steps to custom
flows: a stage script can run a step at the moment the flow needs it, and the release stage later finds the work done
and skips it. All three share the run command's selection: an explicit `[package]` targets one package (an unknown
name is an error; a package that is not releasing is a logged no-op, so a flow never fails over a converged or held
package); invoked from inside a package folder, the command narrows to that package; from the monorepo root it covers
every releasing package in dependency order. The three command words are reserved: like every command name, each
shadows a [run script](./configuration/spaces.md#runscripts-and-dispat-run) of the same name, so `dispat commit` is
never `dispat run commit`.

Every config value the commands consume is also a flag that overrides it for the invocation, listed in the
[flags table](#flags).

**`dispat changelog [package]`** writes each covered package's pending changelog entry, exactly what the release
stage's recorder would write. An entry that already exists in the file is a skip (`W222`), and the same check makes
the release stage skip entries this command already wrote. That is the point of running it early: a changelog written
in a `beforePublish` script, before `dispat commit`, lands inside the tagged commit.

**`dispat autoversion [package]`** runs the native manifest reconciliation of the version stage: declared workspace
ranges rewritten to the planned versions, own versions updated, and the space's `syncLock` scripts run for each
package whose manifests actually changed. Rewriting already-reconciled manifests changes nothing, so re-running is
safe. A space without an `autoVersion` block is skipped unless a policy flag forces one, which then starts from the
defaults.

**`dispat commit [package]`** creates each covered package's release commit: the package folder staged together with
the `commit.include` paths, the message rendered from `commit.messageFormat` with that one package's name and tag.
A package with nothing to stage is a clean no-op. With `--tag`, the annotated release tag is created at the resulting
commit; a tag that already exists there is a skip (`W223`), while a tag at any other commit is an error. With
`--push`, the branch is pushed once after all packages, and with `--tag` the tags too, skipping any already on the
remote. When the command runs inside a release stage script (the environment carries `DISPAT_OUTPUT`), each package's
commit is exported as `PACKAGE_<KEY>`, pinning the outer run's tag and GitHub release to it.

## The compute command

`dispat compute` reads what every package already declares about its dependencies and turns it into the config's
declared edges, so the graph does not have to be maintained by hand. It diffs the manifests against the **merged**
declaration list: the top-level `dependencies` key plus every
[package-declared list](./configuration/packages.md#package-dependencies) (a `packages` entry's or an in-folder config
file's). By default the suggestions are only printed; `--write` applies them, `--interactive` confirms each, `--check`
gates CI. Needs no git history.

**What it reads.** Every package folder is scanned for manifests: `package.json`, `go.mod`, `Cargo.toml`,
`pyproject.toml` (PEP 621 and Poetry), `composer.json`, `pom.xml`, `*.csproj`, `pubspec.yaml` and
`requirements*.txt`.

**How a dependency becomes an edge.** A declaration matches a workspace package by manifest name first (Python names are
PEP 503-normalised, Maven names are `groupId:artifactId`), then by a declared local path (`file:`, a relative
`replace`, `path =`, a `ProjectReference`). Two packages declaring the same manifest name is ambiguous: reported as
`W220`, and no edges are derived from that name.

**What it suggests.** Three kinds of change, each printed with the manifest line that motivates it:

- `+ add` for a detected pair no source declares;
- `~ kind` for a declared pair whose `kind` disagrees with the manifests;
- `- remove` for a declared pair no manifest supports. Removal is only suggested when the consumer actually has parsed
  manifests, plus, unconditionally, when an edge names a package that no longer exists on disk (the one drift every
  other command refuses to load). An edge marked `keep: true` is never suggested for removal: the escape hatch for
  deliberate relations no manifest declares, a Docker image chain being the usual one. A package-declared edge cannot
  carry `keep`; redeclaring it in the top-level list with `keep: true` silences the suggestion the same way.

A suggestion against a package-declared edge names its source (`[packages/core/dispat.json: dependencies[0]]`), so the
listing says which file an applied change would touch.

**How changes are applied.** Nothing is written by default. `--write` applies every suggestion, `--interactive` asks
`y`/`N` per suggestion on stdin. Each change is applied to the file that holds the declaration: additions append to the
root config's top-level `dependencies` list, a removal edits the declaring source (the root list, the
`packages.<name>.dependencies` entry, or the package's own in-folder config file), and a kind correction on a
package-declared edge moves it to the root list (the provider-string form cannot carry a kind). Every edited file is
first copied to `<name>.backup` (untracked files worth a `.gitignore` entry; overwritten on every applying run), and
each write is atomic. A TOML file is not rewritten in place: `--write` prints a paste-ready block for it and fails
instead. `--check` overrides both apply modes: it writes nothing and exits `1` when any suggestion exists across any
source, which is the CI gate for a config lagging the manifests.

## Exit codes

Exit codes: `0` success (including "nothing changed"), `1` configuration/planning error, a refused release (see
[`commitErrors`](./configuration/parser.md#commiterrors)), at least one package failed, or an interrupted run, `2` bad
command line.

Both `release` and `status` print the plan's diagnostics before the graph. `status` exits `1` only for a
repository-scoped failure (an unreadable tag, a version that would go backwards, a dependency cycle, a shallow clone),
because for anything else the plan it just printed is the plan a release would use; when a release *would* refuse (for
example under `commitErrors: error`) it says so in a warning and still exits `0`.

## Interruption

Ctrl-C (or a CI job kill) stops a release cleanly rather than mid-write. In-flight scripts are terminated and their
packages reported as `cancelled`; packages that had not started never start. A package whose publish had already
succeeded still gets its durable record: the changelog entry, the annotated tag and, in release-commit mode, the release
commit and push still happen for it, because losing the record of a completed publish would re-release the same version
on the next run. No more operator scripts run (no hooks, no announce). The command exits `1`, and the next run releases
exactly what the interrupted one still owed.
