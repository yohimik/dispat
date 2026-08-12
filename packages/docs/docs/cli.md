# CLI reference

```
dispat [command] [flags]
```

## Commands

| Command                   | Effect                                                                                                            |
|---------------------------|-------------------------------------------------------------------------------------------------------------------|
| `release` (default)       | Plan, print the graph, then run version/build/publish for every changed package, record releases, tag. Takes the [release lock](#the-release-lock) first, so two releases at once are refused rather than raced. `--package` / `--space` / `--group` release part of the graph; see [Releasing part of the graph](#releasing-part-of-the-graph). |
| `status`                  | Plan and print the graph with computed version bumps, then exit. Nothing is executed, tagged or written. Takes the release's own selection flags.          |
| `run <script>`            | Run a script in every changed package that has it, graph-ordered; see [The run command](#the-run-command).        |
| `init`                    | Write a starter config file and exit; see [The init command](#the-init-command).                                  |
| `preview`                 | Print pending release notes and exit; see [The preview command](#the-preview-command).                            |
| `changelog`               | Write the pending changelog entry now; see [The step commands](#the-step-commands).                               |
| `autoversion`             | Reconcile manifests to the planned versions; see [The step commands](#the-step-commands).                         |
| `autoreplace`             | Apply one set of manifest edits to every covered package; see [The autoreplace command](#the-autoreplace-command). |
| `commit`                  | Create the per-package release commit; see [The step commands](#the-step-commands).                               |
| `github`                  | Create the per-package GitHub release now; see [The step commands](#the-step-commands).                           |
| `if <cond>`               | Run one of several shell scripts, chosen by a condition on the environment; see [Shell helpers](./shell-helpers.md). |
| `exec <script>`           | Run one declared script here, once, for a named subject; see [Shell helpers](./shell-helpers.md). |
| `compute`                 | Derive the dependency graph and the starting versions from the packages' manifests; see [The compute command](#the-compute-command). |
| `self-update`             | Replace this binary with the latest release; see [Updating dispat](./self-update.md).                             |
| `scanner [folder]`        | Print what a folder's manifests declare; see [The manifest commands](#the-manifest-commands).                     |
| `writer <manifest>...`    | Edit manifests in place, format-preserving; see [The manifest commands](#the-manifest-commands).                  |
| `replacer <file>...`      | Replace literal text in any file, parsing nothing; see [The replacer](./replacer.md).                             |

## Flags

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--root`              | `.`         | Where to start config resolution, usually where you stand. The *effective* monorepo root is the directory the config file is found in (see `--config`), so the CLI works from inside a package folder. |
| `--config`            | auto        | Config file name, relative to `--root`. When not set, the file is discovered under the [resolution rules](./configuration/README.md); an explicit name is used as-is, with no fallback and no ascent.  |
| `--concurrency`       | from config | Override: one value for both stages (`7`) or `build,publish` (`4,2`). `dispat run` uses the build value as its budget.                                                                                 |
| `--on-error`          | `skip`      | Every sweeping command (`run`, `autoreplace`, `changelog`, `autoversion`, `commit`, `github`): what a failed package does to its dependents, `skip` (transitive) or `continue`. Either way the command exits `1` on any failure.                                         |
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same nine commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](#choosing-the-packages).            |
| `--group`, `-g`       |             | The same nine commands: narrow to every package of the named [versioning groups](./versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](#choosing-the-packages).            |
| `--since`             |             | The same six commands: cover the packages the commits since a git revision address, instead of the release window. `all` covers every package; see [the run command](#the-run-command).                |
| `--consumers`         |             | The same six commands: additionally cover every package that transitively depends on a selected one; see [the run command](#the-run-command).                                                          |
| `--log-level`         | from config | Override: `trace`, `debug`, `info`, `warn`, `error`.                                                                                                                                                   |
| `--log-format`        | from config | Override: `pretty` or `json`.                                                                                                                                                                          |
| `--quiet-parser`      | from config | Override `parser.quiet`: hide the commit-message parser's own diagnostics. `--quiet-parser=false` shows them again when the config sets `quiet: true`; see [the parser options](./configuration/parser.md#quiet). |
| `--format`            | `json`      | `init` only: the config file format to write (`json`, `yaml` or `toml`).                                                                                                                               |
| `--write`             |             | `compute` only: apply every suggestion to the config file (previous copy saved as `<name>.backup`).                                                                                                    |
| `--interactive`, `-i` |             | `compute` only: confirm each suggestion (`y`/`N` on stdin) before applying it; wins over `--write`.                                                                                                    |
| `--check`             |             | `compute` and `self-update`: report only, change nothing, and exit `1` when there is something to do. For `compute`, any suggestion at all, edges and baselines alike, which is the CI gate for a config lagging the manifests, and it overrides both apply modes. For `self-update`, a release it would install.  |
| `--tag`               |             | `commit` only: also create the annotated release tag at the resulting commit; an identical existing tag is skipped, and one at a different commit is left alone and reported (`E211`).        |
| `--push`              |             | `commit` only: push the branch, and with `--tag` the tags.                         |
| `--no-force`          |             | `commit` only: turn [`commit.force`](./configuration/records.md#force) off for this invocation, leaving a tag the repository or the remote already carries as it is.                         |
| `--name`, `--email`   | from config | `commit` only: override the `commit.name` / `commit.email` committer identity.                                             |
| `--remote`            | from config | `commit` only: override the `commit.remote` push target.                                                                   |
| `--tag-name`          | computed    | `commit` only: name the annotated tag instead of computing it; pass `$DISPAT_TAG` from a release stage. One package only.   |
| `--message-format`    | from config | `commit` only: override the `commit.messageFormat` template.                                                               |
| `--include`           | from config | `commit` only: override the `commit.include` extra staged paths.                                                           |
| `--owner`, `--repo`, `--api-url`, `--token-env` | from config | `github`: override the matching `github.*` values for every package of the invocation. `self-update`: point it at another repository or a GitHub Enterprise endpoint instead of dispat's own releases.  |
| `--force`             |             | `self-update` only: install the selected release even when it is not newer, which repairs a damaged binary and leaves a prerelease line.                                    |
| `--prerelease`        |             | `self-update` only: consider prereleases too. Ordering still decides, so a released `1.2.0` still wins over `1.2.0-rc.1`.                                                   |
| `--release`           |             | `self-update` only: install exactly this version, downgrades included. A leading `v` is fine.                                                                              |
| `--rollback`          |             | `self-update` only: restore the binary the last update replaced, downloading nothing. Refuses beside the flags that select a release; combines with `--check`.              |
| `--target`            |             | `github` only: create the tag at this commit or branch (`target_commitish`). Only safe once the commit is on the remote.   |
| `--file`, `--file-title`, `--date-format` | from config | `changelog` only: override the matching `changelog.*` values for every package of the invocation. `--file-title` states the whole title as one line. |
| `--release-name`      | from config | `changelog` and `github`: override [`releaseName`](./configuration/records.md#your-own-words-around-an-entry) for the invocation. `$VAR` and `${VAR}` expand as they do in the config. |
| `--range`, `--match`, `--write-version` | from config | `autoversion` only: override the matching `autoVersion.*` policy for the invocation.                             |
| `--manifests`         | from config | `autoversion` and `autoreplace`: which of a package's manifests are rewritten, `root` (the ones in the package folder) or `all` (every manifest under it). `autoversion` also takes `none`, which turns its parsing strategy off. |
| `--only-updated`      |             | `autoversion` and `autoreplace`: rewrite only the declarations naming a package this run updates, leaving a range that had fallen behind a provider released earlier as it is. |
| `--no-replace`        |             | `autoversion` only: skip the `autoVersion.replace` rules for this invocation.                                     |
| `--sync-lock`         | `true`      | `autoversion` and `autoreplace`: run the syncLock scripts for packages whose manifests changed; `--sync-lock=false` skips them. |
| `--root-only`         |             | `scanner` only: read the folder's own manifests without descending into sub-folders.                                       |
| `--set-version`       |             | `writer` and `autoreplace`: rewrite the manifest's own version field. For `autoreplace`, `{version}` writes the covered package's planned version, and only its root manifests are touched. |
| `--set`               |             | `writer` and `autoreplace`: set one dependency's declared range, `[kind:]name=range`; repeatable. For `autoreplace`, `{version}` in the range is the planned version of the package the edit names. |
| `--replace`           |             | `writer` and `autoreplace`: point a dependency at a local folder, `name=path`; an empty path removes the redirect. Repeatable. |
| `--sub`               |             | `replacer` only: replace literal text, `find=>write`; repeatable and applied in order. See [The replacer](./replacer.md). |
| `--then`, `--elif`, `--else` |      | `if` only: the script a condition runs, another condition, and the script for when none held. `--then` and `--elif` are repeatable and pair in order. See [Shell helpers](./shell-helpers.md#dispat-if). |
| `--for-package`, `--for-space` |    | `exec` only: the subject of the invocation, which decides both the level the script name is looked up in and the environment the script gets. One exact name, no globs. See [Shell helpers](./shell-helpers.md#one-subject-decides-everything). |
| `--fallback`          |             | `exec` only: resolve the script name the way `dispat run` does, walking from the package to its space to the top level, instead of reading the named level alone. |
| `--script-from`       |             | `exec` only: take the script text from `pkg:<name>`, `space:<name>` or `root`, leaving the environment with the subject. See [Taking the script from somewhere else](./shell-helpers.md#taking-the-script-from-somewhere-else). |
| `--env`               | `static`    | `exec` only: what the subject adds to the environment. `static` is its declared `env`, `dispat` the `DISPAT_*` release variables, `both` what a stage script sees. The last two compute a plan, and nothing else in the command does. |
| `--on-failure`        |             | `if` and `exec`: run this script when the chosen script fails, and exit with the failure script's code instead of the failed script's. |
| `--strict`            |             | Turns a tolerated finding into a failure. `release` and `status`: a selection the plan cannot release as it stands (a package waiting for its providers, a split versioning group), refused before anything is published; see [Releasing part of the graph](#releasing-part-of-the-graph). `scanner`, `writer` and `replacer`: a manifest that failed to parse, an edit the manifest does not declare, or a `--sub` that matched nothing. `autoreplace`: an edit that matched no manifest anywhere; see [Editing across the monorepo](./autoreplace.md#applied-skipped-and-missing-across-many-packages). |
| `--version`           |             | Print the dispat logo, version and platform (`dispat 1.2.3 (darwin_arm64)`) and exit; needs no config file. Release binaries carry the release tag's version, local builds report `dev`, and a binary installed with `go install` says so in the same parenthesis, since that decides how it is [updated](./self-update.md#how-you-installed-it-matters). |
| `--help`, `-h`        |             | Print help and exit. Without a command word, the command list and the global flags; after one, that command's synopsis and its own flags. See [Getting help](#getting-help).                            |

Flag precedence (via viper): explicitly set flag > config file > flag default > built-in default.

## Getting help

`dispat --help` lists every command with a one-line summary, plus the flags that apply everywhere. A command's own
flags are one step away: `dispat <command> --help` prints that command's synopsis, what it does, and the flags it
reads — and nothing else, so the page stays readable however many commands dispat grows.

```sh
dispat --help              # the command list and the global flags
dispat run --help          # run's synopsis and its own flags
dispat github --help       # the github step's, and so on
```

Help needs no config file and no git repository, and exits `0`: asking for help is not an error. A word that is not a
command name is the [run shorthand](#the-run-command), so `dispat lint --help` prints run's help.

## Releasing part of the graph

`dispat release` and `dispat status` read the same [selection](#choosing-the-packages) every other command does:
`--package`, `--space`, `--group`, or the package or space folder the command was invoked from. The plan is computed for the
whole repository and narrowed afterwards, so a selection decides *what* is released and never *at which version* —
`dispat release -p core` releases core at exactly the version a full release would have given it.

One rule is the selection's own, and it comes from publish order. A selected package whose provider is releasing in
the same plan and is *not* selected is **withheld**: releasing it first would ship release notes crediting a provider
version that does not exist yet (§19.2). It is reported as `W230` with the providers it waits for, the rest of the
selection still releases, and the next run releases it. The rule is transitive, and a provider that is unchanged or
held is nothing to wait for.

A [versioning group](./versioning.md) is the softer case: a selection that takes only part of one releases and warns
(`W231`). Nothing goes out of order, and the members left behind are ridden up to the group's version by the next run
(`W210`), so the split is temporary and needs no operator. Naming the group itself, `dispat release -g platform`,
takes every member at once and so can never split it.

`--strict` refuses both, before anything is built, published or tagged: either the selection goes out as written or
nothing does. On `status` it exits `1` for the same selections, which makes it a gate to put in front of a release
job. The graph is printed either way, so a refusal always comes with the plan that explains it.

The full guide, with worked output, is [Partial releases](./partial-releases.md).

## The release lock

Before it plans anything, `dispat release` claims the repository: it pushes a `dispat-release-lock` tag to the remote
(`commit.remote`, by default `origin`) and deletes it when the run ends, however the run ends. A second release started
while the first is running cannot push that tag, so it is refused with exit `1` before it builds, publishes or tags
anything.

The lock is taken on every release, whether or not `commit.push` is enabled, so the release job needs write access to
the remote either way. [`unsafeDisableLock: true`](./configuration/README.md) in the config, or
`DISPAT_UNSAFE_DISABLE_LOCK=true` in the environment, switches it off, which is what a repository with no remote to
coordinate through needs. No other command takes it.

The full guide, including how to clear a lock a killed run left behind, is [The release lock](./release-lock.md).

## The run command

`dispat run <script>` plans, then runs the named
[script](./configuration/spaces.md#scripts-and-dispat-run) inside each changed package that has one, honouring the
dependency graph. Nothing is released or tagged. A failing script's dependents are skipped or kept running per
`--on-error`.

Each package looks the name up in its own `scripts`, then its space's, then the file's. The level you define a name at
is therefore what decides the reach: a file-level script runs in every changed package, a space's in that space's
packages, a package's in that package alone. A selected package with no command for the name does nothing. Exit `1`
means either that no level defines the name, or that none of the selected packages have it.

Selection happens in three steps, in this order:

1. **A window** decides which packages are on the table. By default that is the changed packages, the same set a
   release would process. `--since <rev>` instead selects the packages the commits in `rev..HEAD` address: `HEAD~1`
   for the last commit (per-commit CI), `origin/main` for this branch's own commits (PR pipelines), a release tag, or
   the reserved `all` for every package, changed or not. Selection follows the planner's
   [scope semantics](./commits.md#scope-sets): a commit's written scopes are authoritative, and only scopeless units
   fall back to the files they changed.
2. **The filter** picks from that window: `--package` / `--space` / `--group`, or the folder you are standing in, as described in
   [Choosing the packages](#choosing-the-packages). It only ever narrows, so `dispat run build -p core` runs core when
   core changed and nothing at all when it did not. `--since all -p core` is how you run a script in a package
   regardless — the way to try one script under the exact input its stage would give it, without releasing anything.
   An unchanged package carries its baseline as both the old and the new version.
3. **`--consumers`** then expands the result with every package that transitively depends on a selected one (a
   consumer pulled in brings its own consumers), so downstream packages re-run with a change the window alone would
   not reach. The expansion is deliberately not filtered back out: asking for a package's consumers is asking for
   packages you did not name. The added packages run whether or not they changed, after their selected providers, with
   the ordinary `--on-error` cascade.

`dispat <script>` is a shorthand whenever `<script>` is not a command name. Both spellings take the same flags and
narrow to the same folders.

### Choosing the packages

Three flags name the same thing three ways. `--package` (`-p`) names packages. `--space` (`-s`) names spaces, and
selects every package of one. `--group` (`-g`) names [versioning groups](./versioning.md), and selects every package
that versions with the rest of the group. All three are repeatable and comma-separated (`-p core,web`,
`-p core -p web`), matched case-insensitively, and all three accept `*` globs: `-p '@acme/*'` for a prefix, `-p '*'`
for every package, `-s '*'` for every space, `-g '*'` for every group. Quote a glob, or the shell expands it first. No
word is reserved: a package named `all` is selected by `all` and by nothing else. Terms combine by union, so a package
named twice over is still selected once.

A space is a folder and a group is a versioning relationship, which is why they are separate flags. A group may hold
packages from several spaces, or a single package out of one, and a
[standalone package](./configuration/packages.md#standalone-packages-path) that joined a group is reachable by
`--group` although it belongs to no space at all. A package that versions on its own belongs to no group, so `-g '*'`
never reaches it.

A term that matches nothing is an error, never an empty selection, because a command that quietly acts on nothing is
how a typo hides. The error names what was discovered, and looks across the other two flags: naming a space in
`--package`, a group in `--space`, or a package in `--group` says so and points at the flag that reaches it. A
standalone package belongs to no space, so `--package` (or `-p '*'`) is the only way to name one unless it joined a
group; `-s '*'` means every configured space and leaves it out.

With no terms at all, the folder the command was invoked from is the selection: inside a package folder (or any
subdirectory of it) that package, inside a space folder that space, anywhere else — the monorepo root included —
nothing, so the command covers its usual set. The deepest match wins, so a standalone package nested inside another
package's folder still selects itself. A term on the command line always beats the folder it was typed in. A group is
never inferred this way, because no folder is a group; `--group` is the only way to name one.

Nine commands read the same selection: `release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `commit`,
`github` and `compute`. What each of them *does* with it differs — a release additionally has to respect publish
order, described in [Releasing part of the graph](#releasing-part-of-the-graph) — but which packages a term picks out
never does.

## The init command

`dispat init` writes a starter config file into `--root` (`dispat.json`, or `dispat.yaml` / `dispat.toml` with
`--format`) and exits. An existing file is never overwritten; that is an error. So is a `--root` that is not a git
repository root (no `.git`): the config establishes the effective monorepo root, so it belongs next to `.git`. Needs no
config file.

## The preview command

`dispat preview` plans, then prints the pending release notes: the breaking-changes/features/fixes sections plus
provider updates that the next release's changelog entry and GitHub release body would carry. It covers every package
that has something pending, in publish order, narrowed by
[`--package` / `--space` / `--group` or the invocation folder](#choosing-the-packages). It follows the
[release-notes windowing](./configuration/records.md#changelog), so a pending prerelease previews only its own
changeset. Prints `no pending changes` when nothing is — naming the selection when there was one.

## The step commands

`dispat changelog`, `dispat autoversion`, `dispat commit` and `dispat github` expose the release pipeline's native
steps to custom flows: a stage script can run a step at the moment the flow needs it, and the release stage later
finds the work done and skips it. All four share the run command's [selection](#choosing-the-packages) *and* its
window: with no terms they cover every releasing package in dependency order, `--package`, `--space`, `--group` or the
invocation folder narrows that, `--since` replaces the window, `--consumers` expands it downstream, and `--on-error`
decides what a failed package does to its dependents. A term matching no package is an error; a *selected* package
that is not releasing is a logged no-op, so a flow never fails over a converged or held package — which also means a
step run after `dispat commit --tag` covers nothing until `--since all` puts the tagged package back on the table.
The four command words are reserved: like every command name, each wins
the `dispat <script>` shorthand over a [script](./configuration/spaces.md#scripts-and-dispat-run) of the same name, so
`dispat commit` is always the command. Spelling it out as `dispat run commit` still reaches the script.

`dispat commit` and `dispat github` cover their packages one at a time — a repository has one index and one HEAD, and
a release order is worth reading — while `dispat changelog` and `dispat autoversion` write inside each package's own
folder and ride the build concurrency budget.

Every config value the commands consume is also a flag that overrides it for the invocation, listed in the
[flags table](#flags).

**`dispat changelog`** writes each covered package's pending changelog entry, exactly what the release
stage's recorder would write. An entry that already exists in the file is a skip (`W222`), and the same check makes
the release stage skip entries this command already wrote. That is the point of running it early: a changelog written
in a `beforePublish` script, before `dispat commit`, lands inside the tagged commit.

**`dispat autoversion`** runs the native manifest reconciliation of the version stage: declared workspace
ranges rewritten to the planned versions, own versions updated, and the space's `syncLock` scripts run for each
package whose manifests actually changed. Rewriting already-reconciled manifests changes nothing, so re-running is
safe. A space without an `autoVersion` block is skipped unless a policy flag forces one, which then starts from the
defaults. `--only-updated` narrows the rewrites to declarations naming a package this run releases, so a range that
had fallen behind a provider released earlier is left as it is instead of caught up (`W197`).

**`dispat commit`** creates each covered package's release commit: the package folder staged together with
the `commit.include` paths, the message rendered from `commit.messageFormat` with that one package's name and tag.
A package with nothing to stage is a clean no-op. With `--tag`, the annotated release tag is created at the resulting
commit; a tag that already exists there is a skip (`W223`), while a tag at any other commit is an error. With
`--push`, the branch is pushed once after all packages, and with `--tag` the tags too, skipping any already on the
remote. When the command runs inside a release stage script (the environment carries `DISPAT_OUTPUT`), each package's
commit is exported as `PACKAGE_<KEY>`, pinning the outer run's tag and GitHub release to it.

**`dispat github`** creates each covered package's GitHub release, exactly what the release pipeline's own recorder
would create: the release named after the package tag (or after
[`releaseName`](./configuration/records.md#your-own-words-around-an-entry)), its body the rendered changelog
sections. A release the
repository already carries is a skip (`W224`), so a repeated invocation — and the release that follows one — converge
instead of failing on the API's duplicate-tag rejection.

The opt-in is the one the recorder uses: a package is released when its scripts exported
[`DISPAT_EXPORT_GITHUB`](./environment.md#script-outputs), or when
[`github.allPackages`](./configuration/records.md#github) covers it. Run inside a stage script, the command reads that
export out of its own environment — the stage handed it over, along with `DISPAT_PACKAGE` naming whose it is — and
attaches the files it lists. Run by hand with the variable exported, it covers every package the invocation selects.
Without either opt-in the command publishes nothing, and says so with exit `0`.

## The autoreplace command

`dispat autoreplace` is `dispat writer` pointed at a selection instead of a list of files: `--set-version`, `--set` and
`--replace` mean exactly what they mean there, but the manifests are found by scanning each covered package and the
packages are the ones the plan and the window pick. It takes the same selection and window flags as the step commands,
so `--package`, `--space`, `--group`, `--since` and `--consumers` all read the same.

`--manifests root` (the default) edits the manifests sitting in each package folder, `--manifests all` every manifest
under it, leaving any that belongs to another package to that package. A range may be written as `{version}`, which
resolves to the planned version of the package the edit names, and `--set-version {version}` to the covered package's
own — written to its root manifests alone. `--only-updated` drops every edit naming a package this run does not
update, and `--strict` fails on an edit that matched no manifest anywhere, which is the cross-package reading of
missing: an edit absent from one manifest of twenty is the ordinary case.

A covered package with no manifest anything can write is a no-op; a selection in which none of them has one is an
error. The whole command, with worked examples, is in
[Editing across the monorepo](./autoreplace.md).

## The compute command

`dispat compute` reads what every package already declares about itself and turns it into configuration, so neither the
dependency graph nor the starting versions have to be transcribed by hand. It derives two things:

- the **declared edges**, diffed against the **merged** declaration list: the top-level `dependencies` key plus every
  [package-declared list](./configuration/packages.md#package-dependencies) (a `packages` entry's or an in-folder
  config file's);
- the **baselines** a first release starts from, as [`initials`](./configuration/versions.md#initials) entries taken
  from the version the manifests declare.

By default the suggestions are only printed; `--write` applies them, `--interactive` confirms each, `--check` gates CI.
The edges need no git history. The baselines read each package's release tags, and only those.

[`--package` / `--space` / `--group`](#choosing-the-packages) scope the report to the selected packages' own declarations.
Detection still reads every package's manifests whichever way you narrow: the workspace name index is what resolves a
declared dependency onto a provider, so an edge onto a package outside the selection stays recognised rather than
being proposed for removal.

**What it reads.** Every package folder is scanned for manifests: `package.json`, `go.mod`, `Cargo.toml`,
`pyproject.toml` (PEP 621 and Poetry), `composer.json`, `pom.xml`, `*.csproj`, `pubspec.yaml`, `requirements*.txt`,
`Dockerfile` and `compose.yaml`.

**How a dependency becomes an edge.** A declaration matches a workspace package by manifest name first (Python names are
PEP 503-normalised, Maven names are `groupId:artifactId`, Docker names are image repositories such as
`ghcr.io/acme/api`), then by a declared local path (`file:`, a relative `replace`, `path =`, a `ProjectReference`). Two packages declaring the same manifest name is ambiguous: reported as
`W220`, and no edges are derived from that name.

**What it suggests.** Four kinds of change, each printed with the manifest line that motivates it:

- `+ add` for a detected pair no source declares;
- `~ kind` for a declared pair whose `kind` disagrees with the manifests;
- `- remove` for a declared pair no manifest supports. Removal is only suggested when the consumer actually has parsed
  manifests, plus, unconditionally, when an edge names a package that no longer exists on disk (the one drift every
  other command refuses to load). An edge marked `keep: true` is never suggested for removal: the escape hatch for
  deliberate relations no manifest declares, a Docker image chain being the usual one. `keep` works wherever the edge
  is declared, a package's own list included.
- `+ initial` for a package whose starting version only its manifests know, described below.

A suggestion against a package-declared edge names its source (`[packages/core/dispat.json: dependencies[0]]`), so the
listing says which file an applied change would touch.

**Baselines from manifest versions.** A repository adopting dispat already carries its versions somewhere, and that
somewhere is the manifests. Without an entry for them dispat would start every package at `0.0.0` and release `0.0.1`,
throwing away the history the files know about, so compute offers the missing entries:

```console
+ initial core 1.4.2  packages/core/package.json declares 1.4.2; no release tag yet
```

An entry is proposed for a package only when all of this holds, and the last point is the one that keeps an established
repository quiet:

- Its manifests declare a version. Root manifests are asked first and nested ones only when no root manifest has an
  answer, the same rank that decides manifest names.
- They agree on it. Two root manifests declaring different versions is `W225`, and no baseline comes from them.
- The version is a plain semver release. Something that is not semver at all, and a prerelease such as
  `1.0.0-SNAPSHOT` (a version being worked toward rather than one released), are both passed over, as is `0.0.0`,
  which is already where a package with no entry starts.
- The config has no `initials` entry for it yet. An entry already there is your decision, and compute never rewrites
  one, whatever the manifests say.
- Its release tags cannot answer. The planner only ever reads `initials` when a package has no parseable stable tag,
  so that is the only case an entry is worth writing. A package with a readable release tag is silent, and a package
  whose newest tag matches the format but is not a version gets the suggestion with that tag named in the evidence.

The entries land in the top-level `initials` map with every entry already there left exactly as it is, spelling
included. To silence a suggestion for good, write the entry yourself: `"core": "0.0.0"` is a decision like any other
and compute will leave it alone. Without a git repository the baselines are skipped altogether, with one warning, and
the edges are computed as usual.

**How changes are applied.** Nothing is written by default. The listing puts the edges first and the baselines after
them, by package name. `--write` applies every suggestion, `--interactive` asks `y`/`N` per suggestion on stdin. Each
change is applied to the file that holds the declaration: additions go into the root config's top-level
`dependencies` object under their consumer, unless that consumer already declares its providers in a
`packages.<name>.dependencies` entry or its own in-folder file, in which case the addition joins them there. A removal
and a kind correction edit the declaring source in place, and a baseline goes into the root config's `initials` map. Everything one
file receives is written in a single pass, so a run that changes two of its keys still leaves one backup. Every edited
file is first copied to `<name>.backup` (untracked files worth a `.gitignore` entry; overwritten on every applying
run), and each write is atomic. A TOML file is not rewritten in place: `--write` prints a paste-ready block for it and
fails instead. `--check` overrides both apply modes: it writes nothing and exits `1` when any suggestion exists across
any source, which is the CI gate for a config lagging the manifests.

## The manifest commands

`dispat scanner`, `dispat writer` and `dispat replacer` expose the manifest libraries directly: the first prints what a
folder's manifests declare, the second edits a declaration in place while preserving the file's formatting, and the
third replaces literal text in any file at all. All three need no config file, no git repository and no release plan,
so they work on any checkout. Positional paths resolve against `--root`, and `--log-format json` swaps each command's
listing for one event per file.

`dispat scanner [folder]` walks the folder (`--root-only` stays out of sub-folders) and prints each manifest's
identity, ecosystem and dependency declarations. A manifest that fails to parse is reported while the rest are still
listed, and `--strict` turns that into exit `1`.

`dispat writer <manifest>...` applies `--set-version`, `--set` and `--replace` to each named manifest. Every edit ends
as applied, skipped (a version deferring to something outside the file, which is normal and never fails the command)
or missing (a dependency the manifest does not declare, which fails only under `--strict`). A path no writer covers
always exits `1`.

`dispat replacer <file>...` applies each `--sub 'find=>write'` to each named file, in the order given and to every
occurrence, parsing nothing. It is the tool for the versions no manifest holds: a Gradle coordinate, a Helm chart,
a README example. A pattern that matched nothing anywhere fails only under `--strict`; a file that cannot be read, or
that looks binary, exits `1`.

The full guide, with worked examples and the format list, is [Manifest tools](./manifests.md), and the replacer has
[a page of its own](./replacer.md).

## The self-update command

`dispat self-update` replaces the running binary with the latest stable release of dispat itself. It needs no config
file and no git repository: it is about the binary, not about the repository it is standing in.

The binary for the running platform is downloaded from the GitHub release, checked against the size and checksum the
release published, run once to prove it works, and only then moved into place. The binary it replaces is kept beside it
as `<name>.backup` and removed by a later run a week on. Nothing moves until every check has passed, so a failed update
leaves the working binary exactly where it was.

```console
$ dispat self-update --check
current   dispat 1.0.0 (darwin_arm64)
available dispat 1.1.0 (services/dispat/v1.1.0)

install it with: dispat self-update
```

`--check` changes nothing and exits `1` when there is something to install, which makes it a gate. `--prerelease`
considers the release candidates too, `--release <version>` installs one named version including a downgrade, and
`--force` installs the selection even when it is not newer. Nothing downgrades on its own: a release older than the
running binary is reported as "already the latest" unless one of those two flags says otherwise.

`--rollback` restores the kept binary and downloads nothing. It rotates rather than moves, so the binary it replaces
becomes the new backup and a second `--rollback` returns.

A binary installed with `go install` is not replaced — the next `go install` would undo it — and `--check` prints the
`go install` command that does update it. A local build (`dev`) is refused for the same reason.

Every other command reports a newer stable release on its way out, without ever waiting for the answer. Set
[`updateCheck`](./configuration/README.md) to `false`, or `DISPAT_UPDATE_CHECK=0` in the environment, to switch that
off. The full guide is [Updating dispat](./self-update.md).

## Exit codes

Exit codes: `0` success (including "nothing changed"), `1` configuration/planning error, a refused release (see
[`commitErrors`](./configuration/parser.md#commiterrors) and the [release lock](#the-release-lock)), at least one
package failed, a step that failed after its release was already out, or an interrupted run, `2` bad command line.

A release is refused only *before* any of it happens. Once the first build script runs, nothing aborts the run: a
package can fail and its consumers can be skipped behind it, but every other package still releases and the finalize
phase still records what published. And once a package's publish succeeds, nothing can fail that package at all: a tag,
changelog entry, GitHub release, release commit or push that fails after that point is reported as a
[critical](./architecture.md#after-the-point-of-no-return) and makes the command exit `1` at the end, with all the
remaining work already done.

Both `release` and `status` print the plan's diagnostics before the graph, and both narrow it to their
[selection](#releasing-part-of-the-graph) between the two. `status` exits `1` only for a repository-scoped failure (an
unreadable tag, a version that would go backwards, a dependency cycle, a shallow clone) or for a `--strict` selection
the plan cannot release, because for anything else the plan it just printed is the plan a release would use; when a
release *would* refuse (for example under `commitErrors: error`) it says so in a warning and still exits `0`. A
withheld package or a split versioning group is a warning on both commands and exits `0` without `--strict`.

The two [shell helpers](./shell-helpers.md#exit-codes) are the exception: `if` and `exec` hand back the exit code of the
script they ran, so `dispat if CI --then 'exit 7'` exits `7` and a pipeline gating on a specific code still works with a
helper in the middle. `--on-failure` replaces that code with its own. `2` still means a bad command line, which is worth
knowing if a script exits `2` itself.

## Interruption

Ctrl-C (or a CI job kill) stops a release cleanly rather than mid-write. In-flight scripts are terminated and their
packages reported as `cancelled`; packages that had not started never start. A package whose publish had already
succeeded still gets its durable record: the changelog entry, the annotated tag and, in release-commit mode, the release
commit and push still happen for it, because losing the record of a completed publish would re-release the same version
on the next run. No more operator scripts run (no hooks, no announce). The command exits `1`, and the next run releases
exactly what the interrupted one still owed.
