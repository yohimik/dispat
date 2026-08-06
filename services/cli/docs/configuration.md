# Configuration & CLI reference

## CLI

```
dispat [command] [flags]
```

| Command             | Effect                                                                                                                                                                                                                                          |
|---------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `release` (default) | Plan, print the graph, then run version/build/publish for every changed package, record releases, tag.                                                                                                                                          |
| `status`            | Plan and print the graph with computed version bumps, then exit. Nothing is executed, tagged or written.                                                                                                                                        |
| `run <script>`      | Plan, then execute the named [space run script](#runscripts-and-dispat-run) inside each changed package, honouring the dependency graph. Nothing is released or tagged. `dispat <script>` is a shorthand when `<script>` is not a command name. |

| Flag            | Default       | Effect                                                                                                                                                                              |
|-----------------|---------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--root`        | `.`           | Monorepo root folder (git repo root).                                                                                                                                               |
| `--config`      | `dispat.json` | Config file name, relative to `--root`.                                                                                                                                             |
| `--concurrency` | from config   | Override: one value for both stages (`7`) or `build,publish` (`4,2`). `dispat run` uses the build value as its budget.                                                              |
| `--on-error`    | `skip`        | `run` only: what a failing script does to the failed package's dependents — `skip` them (transitively) or `continue` running them. Either way the command exits `1` on any failure. |
| `--log-level`   | from config   | Override: `trace`, `debug`, `info`, `warn`, `error`.                                                                                                                                |
| `--log-format`  | from config   | Override: `pretty` or `json`.                                                                                                                                                       |
| `--version`     |               | Print the dispat version (`dispat 1.2.3`) and exit; needs no config file. Release binaries carry the release tag's version, local builds report `dev`.                              |
| `--help`        |               | Print usage.                                                                                                                                                                        |

Flag precedence (via viper): explicitly set flag > config file > flag default > built-in default.

Exit codes: `0` success (including "nothing changed"), `1` configuration/planning error, a refused release (see
[`commitErrors`](#commiterrors)) or at least one package failed, `2` bad command line.

Both commands print the plan's diagnostics before the graph. `status` exits `1` only for a repository-scoped failure —
an unreadable tag, a version that would go backwards, a dependency cycle — because for anything else the plan it just
printed is the plan a release would use.

## Configuration file

Loaded with viper: the format is inferred from the file extension, so JSON (default `dispat.json`), YAML or TOML all
work. Unknown keys are rejected (typo protection). Viper matches keys case-insensitively and lowercases map keys, so
script and space names are effectively case-insensitive.

### Top level

| Key                | Type                           | Required  | Description                                                                                                                      |
|--------------------|--------------------------------|-----------|----------------------------------------------------------------------------------------------------------------------------------|
| `scripts`          | map name → shell command       | no        | Named shell commands, like package.json scripts. Referenced by spaces.                                                           |
| `spaces`           | map name → space               | yes (≥ 1) | Package groups sharing build/publish behaviour.                                                                                  |
| `dependencies`     | list of `{consumer, provider}` | no        | Package-level consumer → provider relations. Both must exist; self-dependencies and cycles are rejected; duplicates are ignored. |
| `concurrency`      | int or `[int, int]`            | no        | One value for both stages, or `[build, publish]`. `0` (or omitted) means number of CPUs. More than two values is an error.       |
| `logLevel`         | string                         | no        | Minimum log level: `trace`, `debug`, `info` (default), `warn` or `error`.                                                        |
| `logFormat`        | string                         | no        | Logger output: `pretty` (default; colored console output) or `json` (machine-readable lines for CI ingestion).                   |
| `tagFormat`        | string                         | no        | Release tag template, overridable per space. Default `{name}@{version}`; see below.                                              |
| `commitErrors`     | string                         | no        | What an error in a commit message does to the run: `warn` (default) or `error`; see below.                                       |
| `nonPackageScopes` | array of strings               | no        | Scope names that are deliberately not packages. Default `["release"]`; see below.                                                |
| `changelog`        | object                         | no        | Per-package changelog file options; see below.                                                                                   |
| `github`           | object                         | no        | GitHub release options; see below.                                                                                               |
| `initials`         | map package → version          | no        | Baseline versions used when a package's latest tag is missing or unparseable; see below.                                         |
| `commit`           | object                         | no        | End-of-run release commit, tagging and push; see below. Disabled by default.                                                     |
| `shell`            | array of strings               | no        | Command prefix scripts are appended to, e.g. `["bash", "-c"]` or `["cmd", "/C"]`. Default `["/bin/sh", "-c"]`.                   |
| `run`              | object                         | no        | The run-level hooks (`beforeAll` … `afterPush`), keyed by hook name; see [Run-level hooks](#run-level-hooks).                    |
| `parser`           | object                         | no        | Commit-message parser options; see [`parser`](#parser). Everything unset keeps the specification default.                        |

`scripts` defines named commands; the `run` objects — this one and each space's — say **what runs when**, referencing
those names. Every entry of a `run` object accepts either a single script name or an array of names executed
**sequentially, in order**; a scalar is simply a one-element sequence. How a failure inside a sequence behaves depends
on what the sequence gates:

- **Release-gating scripts** (the stage scripts, the login, every hook up to `beforePublish`, and the run-level
  `beforeAll`) are fail-fast: the first failing command stops the sequence and fails the package's release — or, for the
  run-level `beforeAll`, the whole run.
- **Warn-only scripts** (`postPublish`, the whole announce frame — `beforeAnnounce`, `announce`, `postAnnounce` — the
  outcome scripts `onFail` / `onSkip`, and every other run-level hook) never fail anything: a failing command is logged
  as a warning and **the remaining commands of the sequence still run** — these hooks observe work that has already
  happened, so stopping the sequence could not undo it.

### Space options

| Key                     | Type                     | Required   | Description                                                                                                                                                                                                                                                                                                                                                                             |
|-------------------------|--------------------------|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `path`                  | string                   | yes        | Folder relative to the root. Every direct sub-folder is a package named after the folder (hidden folders are skipped). Package names must be unique across all spaces.                                                                                                                                                                                                                  |
| `isBuildWaitingPublish` | bool                     | no (false) | When `true`, consumers of packages from this space may only start their version/build stages after the provider is *published*, not merely built. When `false`, consumers may build as soon as the provider is built. In both modes a consumer's own publish always waits for the provider's publish and is skipped if it failed (unless the consumer has a release reason of its own). |
| `revertOnFail`          | bool                     | no (false) | When `true`, all local changes inside the package folder are rolled back (tracked files restored from HEAD, untracked files removed) if the package fails at any stage — or is skipped after its version stage already modified files.                                                                                                                                                  |
| `run`                   | object                   | no         | What the space runs at which stage; see the table below.                                                                                                                                                                                                                                                                                                                                |
| `tagFormat`             | string                   | no         | Overrides the repository-wide `tagFormat` for this space; see below.                                                                                                                                                                                                                                                                                                                    |
| `versioning`            | string                   | no         | How versions relate across the space's packages: `independent` (default), `fixed` or `fixedSparse`; see [`versioning`](#versioning).                                                                                                                                                                                                                                                    |
| `runScripts`            | map name → shell command | no         | Named commands for `dispat run <name>`. Values are shell commands themselves, **not** references into `scripts`; see [`runScripts` and `dispat run`](#runscripts-and-dispat-run).                                                                                                                                                                                                       |

The space's `run` object, keyed by stage or hook name (every entry a script name or an array of names):

| Key              | Kind    | Description                                                                                                                                                            |
|------------------|---------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `build`          | stage   | Build stage command(s).                                                                                                                                                |
| `publish`        | stage   | Publish stage command(s).                                                                                                                                              |
| `version`        | stage   | Manifest-sync stage command(s); runs exactly before the build, only for packages bumped due to provider updates.                                                       |
| `login`          | stage   | Authentication command(s), run **once per space** before the space's first publish; see below.                                                                         |
| `announce`       | stage   | Fourth stage, run after a successful publish: pushing the release out to update channels, with the release-notes variables. The whole frame only **warns**; see below. |
| `beforeAll`      | hook    | Before the package's first stage (its version stage when it has one, its build otherwise). Failure fails the package's release.                                        |
| `beforeVersion`  | hook    | Before the version stage. Failure fails the release.                                                                                                                   |
| `postVersion`    | hook    | After the version stage. Failure fails the release.                                                                                                                    |
| `beforeBuild`    | hook    | Before the build stage. Failure fails the release.                                                                                                                     |
| `postBuild`      | hook    | After the build stage. Failure fails the release.                                                                                                                      |
| `beforePublish`  | hook    | Before the publish stage (after the login). Failure fails the release.                                                                                                 |
| `postPublish`    | hook    | After a successful publish. Failure only **warns** — the release is already out.                                                                                       |
| `beforeAnnounce` | hook    | Before the announce stage. Failure only **warns** and does not stop the announce.                                                                                      |
| `postAnnounce`   | hook    | After the announce stage. Failure only **warns**.                                                                                                                      |
| `onFail`         | outcome | Runs once when the package **fails** at any stage. Warn-only; see below.                                                                                               |
| `onSkip`         | outcome | Runs once when the package is **skipped** because a provider failed. Warn-only; see below.                                                                             |

All script references are optional. A stage without a script still runs — ordering, skip semantics, statuses, tags and
release records are fully preserved — it just executes no shell command; an unconfigured hook is a no-op. Scripts run
through the configured `shell` (default `/bin/sh -c`) with the package folder as the working directory.

The hooks bracket the stages of every package of the space, each with the full stage environment (`DISPAT_STAGE`
carries the hook's name). Everything up to `run.beforePublish` exists to *gate* the release, so a failure there fails
the package exactly like a failing stage script — the pipeline stops, nothing is published or tagged, `revertOnFail`
applies. `run.postPublish` and the announce hooks run after the package's status has settled and only warn: failing the
package then would report an unpublished release for a published one. The version hooks share the version stage's skip
rule — when every provider a package was bumped for failed, neither the version script nor its hooks run.

#### `run.login`

Authentication (`npm login`, `docker login`, …) is a property of the space, not of any one package, so the login runs
**once per space and run**: the space's first publish triggers it, every other publish of the space **waits** until it
finishes, and it is never re-run within the run. Two spaces referencing the *same* script still log in once each — n
spaces, n logins — because credentials and registries belong to the space. A failing login fails the publish of
**every**
package in the space (none of them could have succeeded without it); other spaces are unaffected. The login runs in the
folder of the package whose publish happened to trigger it and gets the space-scoped environment: `DISPAT_SPACE`,
`DISPAT_STAGE=login`, the [workspace listing](#workspace-data) and `DISPAT_OUTPUT` — no package variables, since which
package triggered it is a scheduling accident. What it [exports](#script-outputs) is space-scoped too: every package
of the space receives the login's exports from its publish stage onward, sourced `<space>:login`.

#### `run.announce`

A fourth per-package stage, run after the publish frame completes (publish script, release records, tag,
`run.postPublish`). Its job is pushing the release out to update channels — a Slack or Discord message, a webhook, a
docs feed — so alongside the full stage environment it is the natural consumer of the
[release-notes variables](#release-notes-data) (`DISPAT_BREAKING_CHANGES`, `DISPAT_FEATURES`, `DISPAT_FIXES`) and the
channel variables (`DISPAT_CHANNEL`, `DISPAT_OLD_CHANNEL`, `DISPAT_IS_PRERELEASE`) for choosing where and how to
announce. It has the same hook structure as the other stages (`run.beforeAnnounce` / `run.postAnnounce`) but none of
their authority: the release is already out, so an error in the stage **or either hook** only warns, the package stays
published, and no failure among the three sequences stops the others from running. The frame is skipped entirely when
the publish failed — there is nothing to announce.

#### `run.onFail` and `run.onSkip`

Two outcome scripts, the failure-side counterparts of the announce stage. `run.onFail` runs once when a package of the
space **fails** — at any stage, a failing gating hook, release recorder or tag included — after its status has settled
and after `revertOnFail`'s rollback, so the script sees the folder's final state. `run.onSkip` runs once when the
package is **skipped** because a provider failed or was skipped. Both observe an outcome that has already happened, so
an error in either only warns; both receive the full package environment (`DISPAT_STAGE` is `onFail` / `onSkip`)
plus the specifics:

| Variable              | Set for  | Meaning                                                 |
|-----------------------|----------|---------------------------------------------------------|
| `DISPAT_FAILED_STAGE` | `onFail` | The stage that failed: `version`, `build` or `publish`. |
| `DISPAT_ERROR`        | `onFail` | The error message of the failing command or operation.  |
| `DISPAT_BLOCKED_BY`   | `onSkip` | The provider whose failure caused the skip.             |

Neither runs for a package that published — that is `run.postPublish` and the announce frame — and the run-level
[run outcome listing](#run-outcome-data) carries the same information for every package at once.

### `versioning`

How the versions of a space's packages relate to each other.

| Value                     | Effect                                                                                                                                                                                                                             |
|---------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `independent` *(default)* | Every package's version is computed from its own history alone — the behaviour described everywhere else in this document.                                                                                                         |
| `fixed`                   | One shared version for the whole space: a change to **any** member (by commit scope or changed files) releases **every** member at the same next version.                                                                          |
| `fixedSparse`             | The shared version is computed exactly like `fixed`, but a member with no changes of its own keeps its previous version and is not released; changed members release at the shared version, aligning to it the moment they change. |

Under `fixed` and `fixedSparse` the space versions **as if it were one package**: the shared next version is computed
over the space's highest baseline with the max bump across all members, the space runs a **single prerelease train**
(a channel directive on one member moves the whole space; a graduation ends the train for all of it), and an exact
`Release-As` naming one member pins the space's single version — with the usual pin guards applied to it. A
`Release-As: none` hold still applies to the member it names: the held member stays behind and catches up when resumed.

Scopes — commit scope-sets and changed files — keep exactly one job in a fixed space: deciding **which changelog entries
a package receives** (and what its GitHub release announces). A member released only because of the shared version gets
a single "no changes — version bump" entry instead of borrowing its neighbour's notes, and its presence in the plan is
reported as `W210` (non-suppressible, like the catch-up codes: nothing in the commit log alone explains it). Such a ride
is a full release at the execution level — its version/build/publish scripts, hooks, tag and records all run.

Two convergence properties are worth knowing. A fixed space whose members all carry the shared version releases nothing
on a quiet run, exactly like independent packages. And a `fixed` member left *behind* the space's published baseline —
its ride failed in an earlier run, or the space adopted `fixed` with unequal versions — is caught up at exactly that
baseline on the next run (also `W210`), restoring the one-version invariant; `fixedSparse` deliberately never does this,
since staying behind is its point.

Dependency edges stay package-scoped either way: a provider propagating into one member bumps that member (which then
carries its space along under `fixed`), and only the member with provider updates runs a version task.

### `runScripts` and `dispat run`

Each space may define `runScripts`: named shell commands for ad-hoc work over the packages a release *would* touch —
linting what is about to ship, printing diffs, smoke-checking artefacts.

```yaml
spaces:
  libs:
    path: packages
    run: { build: build, publish: publish }
    runScripts:
      lint: "npm run lint"
      preview: "echo \"$DISPAT_PACKAGE -> $DISPAT_NEW_VERSION\""
```

Unlike the stage entries, the values are **shell commands themselves**, not references into `scripts`. `dispat run
<name>` — or the shorthand `dispat <name>`, whenever `<name>` is not a command name — computes the plan and executes the
named script inside each **changed** package (the packages a release would process), **honouring the dependency graph**:
a package's script starts only after every changed provider's finished, and independent packages run concurrently within
the build concurrency budget (`--concurrency`'s first value). Each script gets the package's full
[DISPAT_* environment](#script-environment-variables) (`DISPAT_STAGE` is `run:<name>`), so a script moves freely between
a stage and a run script. A changed package whose space does not define the name completes as a no-op; a name **no**
space defines is an error (running nothing silently is how a typo hides).

What a failure does is the `--on-error` flag: under `skip` (the default) the failed package's changed dependents are
skipped, transitively — the same shape a release gives a failed provider — while independent packages keep running;
under `continue` the dependents run anyway. Any failure makes the command exit `1` either way. Nothing is released,
tagged or written. Names are matched case-insensitively (viper lowercases map keys).

Run scripts take part in [script outputs](#script-outputs) too, with one extra rule: outputs carry **across packages,
down the dependency graph**. Each run script gets `$DISPAT_OUTPUT` to export through, and a package's script receives
the exports of its changed providers' scripts (transitively — a script-less package in the middle still carries them
through) as `DISPAT_OUTPUT_<NAME>`, with `DISPAT_OUTPUT_SOURCE_<NAME>` still naming the original exporter
(`base:run:lint`). Providers merge in name order, the package's own re-export overrides, and under
`--on-error continue` a failed provider's exports still reach its dependents — mirroring what the pipeline's `onFail`
hooks receive.

### `tagFormat`

The template release tags are built from and read back with. Four placeholders are substituted; every other byte is
literal:

| Placeholder | Meaning                                            |
|-------------|----------------------------------------------------|
| `{name}`    | The package name.                                  |
| `{version}` | The semver version, with no `v` prefix of its own. |
| `{channel}` | The prerelease channel, e.g. `beta`.               |
| `{counter}` | The prerelease counter, e.g. `4`.                  |

Exactly one `{version}` is required — none leaves every version indistinguishable, more than one makes parsing
ambiguous — and every format is validated at load time, including a render-and-read-back round trip. `{name}` may appear
any number of times, including none.

| Format                         | Example tag            |
|--------------------------------|------------------------|
| `{name}@{version}` *(default)* | `core@1.2.3`           |
| `{name}@v{version}`            | `core@v1.2.3`          |
| `services/{name}@v{version}`   | `services/core@v1.2.3` |

`{channel}` and `{counter}` spell the prerelease out instead of leaving it inside `{version}`, for the conventions that
do not write it the way semver does. They are used together — a counter with no channel cannot tell two trains apart, a
channel with no counter gives every prerelease of a train the same tag — must follow `{version}` in that order, and
their presence narrows `{version}` to the `MAJOR.MINOR.PATCH` core. On a stable version there is no channel to write, so
the placeholders *and the literal text glued to them* are dropped:

| Format                                 | Prerelease tag      | Stable tag    |
|----------------------------------------|---------------------|---------------|
| `{name}@{version}` *(default)*         | `core@1.2.3-beta.4` | `core@1.2.3`  |
| `{name}@v{version}-{channel}{counter}` | `core@v1.2.3-beta4` | `core@v1.2.3` |
| `{name}@{version}.{channel}.{counter}` | `core@1.2.3.beta.4` | `core@1.2.3`  |

Only the tag's shape changes: the version is semver throughout, `beta.10` still sorts above `beta.9`, and a tag read
back yields the semver spelling whatever the tag wrote. The counter is not limited to the bare number the automatic
train produces — an exact `Release-As: 2.0.0-rc.1.hotfix` renders and reads back too, the counter being everything after
the channel. With no literal between `{channel}` and `{counter}`, a fused `beta10` splits at the letter–digit boundary
(`beta`/`10`); a channel name that itself ends in a digit (`rc2`) would be misread under such a format, so give the two
placeholders a separator literal if you use channels like that.

Set it at the top level for the repository and override it per space, so a monorepo whose Go modules want the path form
and whose npm packages want the plain one can have both. The format is what a *later* run reads a package's baseline
from, so changing it retroactively hides the existing history: either re-tag, or seed the new line with `initials`.

Tags that match a package's glob but not the format's literal text belong to someone else's convention and are ignored.
A tag that matches the shape but whose version cannot be parsed is the case `initials` exists for — see below.

### `commitErrors`

What an error in a commit message does to the run.

| Value              | Effect                                                                                                    |
|--------------------|-----------------------------------------------------------------------------------------------------------|
| `warn` *(default)* | The offending unit contributes nothing and the run continues. Other units in the same commit still apply. |
| `error`            | Any commit error stops the run before anything is built, published or tagged.                             |

`warn` is the blast radius the spec assigns to unit- and message-scoped errors: a malformed header or a scope naming an
unknown package is an authoring mistake in *one unit*, and the rest of the history is unaffected. `error` is the
stricter reading, and the one to choose when a mistyped scope silently dropping a package from a release is the worse
failure of the two.

Neither value affects **repository-scoped** failures — a prerelease tag with no numeric counter, a computed version that
would not exceed the baseline, a graduation that would go backwards, a dependency cycle. Those mean no correct plan
exists, so the run always aborts before releasing anything. They are fixed by correcting the repository (usually a tag)
and re-running, not by editing a commit.

Diagnostics are printed either way, with their code (`E130`, `W193`, …), the package and the commit.

### `nonPackageScopes`

Scope names that are deliberately not packages, so naming one is not the typo the unknown-package error exists to catch.
A unit scoping only these resolves to nothing, silently and with no diagnostic.

The default is `["release"]`, and it is load-bearing rather than cosmetic: dispat's own release commit is
`chore(release): {tags}`, so without the exemption every run in `commit` mode would leave an error behind for the next
run to trip over — under `commitErrors: "error"`, a tool that breaks its own repository on the second release. Add your
own conventions (`deps`, `ci`, …) as needed; setting it to `[]` disables the exemption entirely.

### Entry format options (shared by `changelog` and `github`)

| Key                 | Default            | Description                         |
|---------------------|--------------------|-------------------------------------|
| `dateFormat`        | `2006-01-02`       | Go time layout for the entry date.  |
| `breakingTitle`     | `Breaking Changes` | Section title for breaking changes. |
| `featuresTitle`     | `Features`         | Section title for features.         |
| `fixesTitle`        | `Fixes`            | Section title for fixes.            |
| `dependenciesTitle` | `Dependencies`     | Section title for provider updates. |

### `changelog`

| Key       | Default        | Description                                   |
|-----------|----------------|-----------------------------------------------|
| `enabled` | `true`         | Write a changelog file per published package. |
| `file`    | `CHANGELOG.md` | File name inside the package folder.          |
| `title`   | `# Changelog`  | First line of the file.                       |
| *format*  |                | All entry format options above.               |

New entries are prepended below the title, newest first.

### `github`

| Key        | Default                   | Description                                                                                                                                                                                                                                                                    |
|------------|---------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`  | `true`                    | Create a GitHub release per published package that exported [`DISPAT_EXPORT_GITHUB`](#script-outputs).                                                                                                                                                                                                                                 |
| `owner`    | from `$GITHUB_REPOSITORY` | Repository owner.                                                                                                                                                                                                                                                              |
| `repo`     | from `$GITHUB_REPOSITORY` | Repository name.                                                                                                                                                                                                                                                               |
| `apiUrl`   | `https://api.github.com`  | REST endpoint; set for GitHub Enterprise.                                                                                                                                                                                                                                      |
| `tokenEnv` | `GITHUB_TOKEN`            | Name of the environment variable holding the API token.                                                                                                                                                                                                                        |
| *format*   |                           | All entry format options above. The release body contains only the sections — the `## pkg@version (date)` header line used in changelog files is omitted, since the release title is already the tag and GitHub shows its own date; `dateFormat` therefore has no effect here. |

The release is **opt-in per package and per run**: it is created exactly when one of the package's scripts exported
[`DISPAT_EXPORT_GITHUB`](#script-outputs); a published package without the export is skipped (with an info-level
notice), so a script decides at run time which packages get a GitHub release. The release is named after the tag
(`pkg@1.3.0`); its body is the rendered changelog sections. When `enabled` but no repository or token can be resolved
at runtime, GitHub releases are skipped with a warning instead of failing the run. If the tag has not been pushed
yet, GitHub creates it at the default branch head.

The export's value names the **release assets**: a whitespace-separated list of absolute paths to existing files,
each uploaded (named after the file, `application/octet-stream`) right after the release is created — in `commit`
mode too, where the release itself moves to the finalize phase. An invalid entry — a relative path, a missing file, a
directory — is skipped with a warning while the release and the remaining files go through; a failed upload of a
valid file still fails the package like any other recording failure.

### `initials`

A map of package name → `MAJOR.MINOR.PATCH` (validated at load time). The value is the *baseline* the next release bumps
from — it never becomes a release by itself. It applies in exactly two situations:

- the package has no matching tag at all (a first release), or
- the newest matching tag (by creation date) exists but its version cannot be parsed as semver — e.g. a stray
  `core@0.0.1.0`. In that case older parseable tags are deliberately *not* used, and commits are still scanned from the
  unparseable tag (not the whole history).

Example: `"initials": {"core": "1.0.0"}` with an unparseable newest tag and one `fix(core)` commit since it releases
`core@1.0.1`. Packages without an entry fall back to `0.0.0` as usual. A parseable latest tag always beats initials.
Keys are matched case-insensitively against discovered packages (viper lowercases map keys); entries matching no package
are warned about and ignored.

### `commit`

| Key             | Default                  | Description                                                                                                     |
|-----------------|--------------------------|-----------------------------------------------------------------------------------------------------------------|
| `enabled`       | `false`                  | Create one release commit at the end of a successful run.                                                       |
| `messageFormat` | `chore(release): {tags}` | Template; `{tags}` and `{packages}` become comma-separated lists.                                               |
| `push`          | `false`                  | Push the release commit and tags (`git push --follow-tags <remote> HEAD`). Only applies when `enabled` is true. |
| `remote`        | `origin`                 | Remote to push to.                                                                                              |

When enabled, the run finishes with a *finalize phase*: all published packages' folders are staged and committed in a
single commit (changelog files, version-script manifest changes — add build outputs to `../../../.gitignore` or they get
committed too), release tags are created **on that commit** instead of during each publish, and GitHub releases move to
the end of the run. Every GitHub release body then documents the release commit SHA and the tag in a `### Release`
section — whether or not they were pushed. With `push` on, releases are created after the push and the tag is
additionally pinned to the release commit via `target_commitish`; without `push`, the SHA cannot be sent to GitHub (it
does not exist on the remote yet), so GitHub creates the tag ref at the default branch head until you push — the true
commit and tag remain recorded in the release body. If nothing changed on disk (e.g. changelogs disabled), no empty
commit is created but tags are still placed.

Pushing requires a checked-out branch (not a detached HEAD — use `actions/checkout` with a `ref`). When `push` is
enabled, remote access is **verified before any release work starts** (`git ls-remote`), and likewise an enabled GitHub
configuration is verified against the API (`GET /repos/{owner}/{repo}`) — misconfigured credentials fail the run
immediately, before anything is built. A failure during the finalize phase itself (commit, tag, push, GitHub release)
exits 1, but already-published registry artifacts stay published.

### `parser`

The commit-message parser options, previously fixed at the specification defaults. Everything is optional: an absent
`parser` object (or any unset field) keeps the default, so existing configurations parse exactly as before. An invalid
value fails the config load, before any planning.

| Key                        | Default                          | Description                                                                                                                                                                                                                                                         |
|----------------------------|----------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `separator`                | `---`                            | The unit separator line. At least three ASCII-printable characters, no whitespace, must not begin like a type. Repositories exchanging patches by mail often use `%%%`.                                                                                             |
| `types`                    | the standard table               | Map of commit type → bump (`none`, `patch`, `minor`, `major`). A non-empty map **replaces** the standard table (`feat`=minor, `fix`/`perf`/`revert`=patch, the rest none) wholesale — list every type you keep. Names are a–z only (viper lowercases keys anyway).  |
| `strictTypes`              | `false`                          | Turn an unknown commit type into an error (E140) instead of a warning; the [`commitErrors`](#commiterrors) policy decides whether that stops the run.                                                                                                               |
| `lenient`                  | `false`                          | Downgrade selected authoring errors to warnings: an uppercase type is lowercased, a missing space after `:` is accepted, a footer contradicting an inline directive wins.                                                                                           |
| `maxDescriptionLength`     | `100`                            | The long-description warning threshold, in Unicode scalar values; negative disables it.                                                                                                                                                                             |
| `propagation.bump`         | `patch`                          | The bump consumers take when a unit propagates without saying which: `none`, `patch`, `minor`, `major` or `inherit` (copy the unit's own bump).                                                                                                                     |
| `propagation.depth`        | `0`                              | The default propagation depth: a number of edges or `all`. **This is the knob that changes propagation from opt-in to on-by-default** — with `1`, a plain `feat(core):` reaches core's direct consumers with no caret written. A directive on the unit always wins. |
| `propagation.channelDepth` | `0`                              | The channel-axis counterpart: how far a channel travels by default.                                                                                                                                                                                                 |
| `propagation.kinds`        | all but `devDependencies`        | The dependency edges propagation follows: `dependencies`, `peerDependencies`, `optionalDependencies`, `devDependencies` or `all`.                                                                                                                                   |
| `propagation.channel`      | `inherit`                        | The default propagated channel value.                                                                                                                                                                                                                               |
| `limits.unitsPerMessage`   | `64`                             | Always-enforced parser bounds; exceeding one voids the whole message (E158). Negative disables a bound — trusted input only.                                                                                                                                        |
| `limits.scopeTermsPerUnit` | `256`                            |                                                                                                                                                                                                                                                                     |
| `limits.messageBytes`      | `1048576`                        |                                                                                                                                                                                                                                                                     |
| `allowedChannels`          | unrestricted                     | Restrict prerelease channel names (E181 outside the list); `stable` is always accepted.                                                                                                                                                                             |
| `messageLevelTrailers`     | Signed-off-by, Co-authored-by, … | Authorship/review trailers ignored wherever they appear. Setting the key replaces the list.                                                                                                                                                                         |
| `issueTrailers`            | Closes, Fixes, Refs, Resolves    | Issue-reference trailers, ignored for versioning but surfaced for changelogs. Setting the key replaces the list.                                                                                                                                                    |

```yaml
parser:
  types: { feat: minor, fix: patch, perf: patch, revert: patch, docs: patch }
  strictTypes: true
  propagation:
    depth: 1        # bundled dependencies: a bump reaches direct consumers by default
```

### Run-level hooks

Seven hooks observe the run as a whole rather than any one package. They live in the **top-level `run` object**, run in
the **monorepo root**, and each is a no-op when not configured:

```yaml
run:
  beforeAll: check-preconditions
  postAll: notify
```

| Hook               | Runs                                                                              |
|--------------------|-----------------------------------------------------------------------------------|
| `run.beforeAll`    | Once, after planning and verification, before the task graph starts.              |
| `run.postAll`      | Once, after the whole task graph finishes — even when nothing released.           |
| `run.beforeCommit` | Before the release commit. `commit` mode only, and only when something published. |
| `run.afterCommit`  | After the release commit succeeded.                                               |
| `run.postCommit`   | After the release commit **and** the tags.                                        |
| `run.beforePush`   | Before the push. `commit.push` mode only.                                         |
| `run.afterPush`    | After the push succeeded.                                                         |

`run.beforeAll` is the one **gating** run hook: it fires before any release work, when failing it can still stop
everything — so it does, fail-fast, aborting the run with exit `1` before anything is built, published or tagged. Every
other run hook only **warns** on failure (a warn-only sequence: every command runs even when an earlier one failed),
because it runs after the work it observes; the "after" hooks additionally only run when the operation they bracket
succeeded — a hook observing a commit or push that never happened would be reporting a lie.

All seven receive `DISPAT_STAGE` naming the hook and the [workspace listing](#workspace-data); `run.postAll` and
everything after it additionally receive the [run outcome listing](#run-outcome-data) reporting which packages
published, failed, were skipped or were never planned to release (`run.beforeAll` fires before any outcome exists).

## Commit message reference

Messages are parsed by [`pkg/ccme`](../../../pkg/ccme). A message holds one or more **units** separated by a line of
`---`; each has its own header, body and footers.

```
<type>[(<scope-set>)][<directives>][!]: <description>

[body]

[footers]
```

### Scope sets

| Term      | Resolves to                                       | Unknown name                  |
|-----------|---------------------------------------------------|-------------------------------|
| `core`    | that package                                      | error                         |
| `core,ui` | both                                              | error                         |
| `*`       | every package in the workspace                    | —                             |
| `@acme/*` | every package matching the glob                   | warning if it matches nothing |
| `.`       | the packages owning the commit's changed files    | —                             |
| `-app`    | removes `app` from the set; exclusions always win | warning                       |

With no parentheses at all the set is file-derived, by longest matching path prefix — a file under a package nested
inside another belongs to the inner one only. A unit resolving to no package is inert and reported as such.

### Inline directives

Written between the scope-set and the `:`. Every one has an equivalent footer; stating both is redundant and
contradicting is an error.

| Written  | Footer equivalent            | Meaning                                                     |
|----------|------------------------------|-------------------------------------------------------------|
| `!`      | `BREAKING CHANGE: <text>`    | Raises the unit's own bump to major.                        |
| `^`      | `Propagate-Depth: 1`         | Reach direct consumers.                                     |
| `^^`     | `Propagate-Depth: all`       | Reach the whole transitive closure.                         |
| `+N`     | `Propagate-Depth: N`         | Reach consumers up to N edges away. `+0` is no propagation. |
| `^minor` | `Propagate: minor`           | The bump consumers take. Default `patch`.                   |
| `^none`  | `Propagate: none`            | Propagate nothing, whatever the depth.                      |
| `@beta`  | `Channel: beta`              | The unit's own packages move to that channel.               |
| `@@beta` | `Propagate-Channel: beta`    | The channel handed to the consumers reached below.          |
| `++N`    | `Propagate-Channel-Depth: N` | How far the channel travels. Default `0`.                   |

Both depths default to `0`, and neither bounds the other: a unit reaches nobody on either axis until it says so.

### Footers

| Footer                                 | Effect                                                                                        |
|----------------------------------------|-----------------------------------------------------------------------------------------------|
| `BREAKING CHANGE: <text>`              | Major bump plus the text in the changelog. Case is significant; near-misses are warned about. |
| `Propagate: <bump>`                    | `none`, `patch`, `minor`, `major` or `inherit` (copy the unit's own bump).                    |
| `Propagate-Depth: <N\|all>`            | Bump-axis reach.                                                                              |
| `Propagate-Scope: <scope-set>`         | Intersects the reached set. If nothing survives, a warning and no propagation.                |
| `Propagate-Channel: <value>`           | `inherit` (default), `none`, `stable`, a channel name, or a `<from>><to>` transition.         |
| `Propagate-Channel-Depth: <N\|all>`    | Channel-axis reach.                                                                           |
| `Propagate-Channel-Scope: <scope-set>` | Restricts the channel axis. Defaults to `Propagate-Scope`.                                    |
| `Channel: <value>`                     | The unit's own channel, same grammar.                                                         |
| `Release-As: <none\|auto\|version>`    | Release control; see below.                                                                   |
| `Reverts: <sha>`                       | Informational.                                                                                |

### Channels and prereleases

A package's channel comes from its baseline tag: `1.5.0-beta.3` is on `beta`, `1.4.2` is on `stable`, and an untagged
package is on `stable`. Prerelease versions are `<major>.<minor>.<patch>-<channel>.<counter>` with a separate numeric
counter, so `beta.10` sorts above `beta.9`.

```
feat(core)@beta:            core enters the beta line; nothing else moves
feat(core)^@beta:           the same — the caret reaches the consumers and every
                            one is suppressed, because a stable consumer cannot
                            resolve a beta release
feat(core)^@beta++1:        core and its direct consumers enter beta together
feat(core)^:                an established train stays on beta with no directive
release(core)@stable:       graduate core
release(core)@beta>stable@@beta>stable++*:
                            graduate core and everything still on beta behind it
```

A `<from>><to>` transition matches against the *baseline* channel, which makes it idempotent: packages that already
graduated do not match, so the same directive is correct on the first run and the fifth. A bare `stable` arriving by
propagation never graduates a dependant — only a direct directive or a transition naming the train does, so graduation
cannot happen by accident.

A train converges the same way stable releases do: work a prerelease has already published cannot release again. The
version of the *next* prerelease (and of the graduation) is still computed over the whole train — a breaking change
shipped in `beta.0` keeps the target at the next major — but with no new commits since the last prerelease tag a re-run
finds nothing to do, a `Release-As` consumed by a prerelease is no longer in force, and a `cancel` cannot retract
train-published work (a *live* cancel aimed at published work warns `W170`). Convergence is quiet: the directive that
started the train is contained in the tag it produced, so it is not re-reported as "already on beta" (`W199`) on later
runs — that warning is reserved for a *fresh* directive pointing where the package already is — and a spent cancel (one
every package it names has released past) is not re-reported either.

### Release control

| Written                                    | Effect                                                                                   |
|--------------------------------------------|------------------------------------------------------------------------------------------|
| `release(<pkg>)` + `Release-As: none`      | Hold: the bump is retained and reported, not released, until a later directive lifts it. |
| `release(<pkg>)` + `Release-As: auto`      | Resume: release at the `max()` of everything accumulated, catch-up included.             |
| `release(<pkg>)` + `Release-As: <version>` | Pin an exact version.                                                                    |
| `cancel(<pkg>)`                            | Discard the package's unreleased metadata. Irreversible; never reaches a published tag.  |

A pin is rejected if it does not move the package forward, if it is below what the pending commits require, or if it
raises the major version more than one above the computed version. A rejected pin has the unit-scoped blast radius of
any other commit error: the error is reported (and, under `commitErrors: "error"`, stops the run), the bad directive
contributes nothing, and the package falls back to its ordinarily computed version — a `feat` sharing the commit with a
bad pin still releases at its computed bump, and a lone rejected pin releases nothing. A pin sets the version, never the
bump — how large a change is, is declared by the type. The version also decides the channel, so
`Release-As: 2.0.0-rc.0` enters the rc line and `Release-As: 2.0.0` graduates.

`cancel` only reaches backwards: it discards contributions from commits that are ancestors of the cancel, and work
landing afterwards accumulates normally. A `cancel` on a provider that has already published is a no-op and says so —
the version is public, and the right target for stopping a pending catch-up is the **consumer**.

## Script environment variables

Every script receives, on top of the parent environment:

| Variable                 | Example              | Meaning                                                                                                                                                                                                                                                                                      |
|--------------------------|----------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `DISPAT_PACKAGE`         | `core`               | Package name.                                                                                                                                                                                                                                                                                |
| `DISPAT_SPACE`           | `libs`               | Space name.                                                                                                                                                                                                                                                                                  |
| `DISPAT_OLD_VERSION`     | `1.2.3`              | The version the package last published (`0.0.0` for a first release).                                                                                                                                                                                                                        |
| `DISPAT_NEW_VERSION`     | `1.3.0-beta.4`       | Version being released: version + channel + counter, SemVer spelling.                                                                                                                                                                                                                        |
| `DISPAT_VERSION`         | `1.3.0`              | The core version alone — `MAJOR.MINOR.PATCH`, channel and counter stripped.                                                                                                                                                                                                                  |
| `DISPAT_TAG_VERSION`     | `1.3.0-beta4`        | Version + channel + counter as the space's `tagFormat` spells them: the version section of `DISPAT_TAG` without the name and its decoration (no `v` prefix, no path). Equals `DISPAT_NEW_VERSION` under formats that leave the prerelease inside `{version}`.                                |
| `DISPAT_STABLE_BASELINE` | `1.2.3`              | The last release with no prerelease component — what versions are computed from.                                                                                                                                                                                                             |
| `DISPAT_BASELINE`        | `1.3.0-beta.3`       | The latest baseline: the newest tag of any kind, prereleases included — what the computed version must exceed and where the channel is read from. **Unset** when the package has never released, so `${DISPAT_BASELINE+x}` detects a first release; when set it equals `DISPAT_OLD_VERSION`. |
| `DISPAT_BUMP`            | `minor`              | `none`, `patch`, `minor` or `major`. `none` on a channel-only release.                                                                                                                                                                                                                       |
| `DISPAT_CHANNEL`         | `beta`               | Channel being released on: `stable` or a prerelease identifier.                                                                                                                                                                                                                              |
| `DISPAT_OLD_CHANNEL`     | `stable`             | Channel of the previous release, so a graduation is distinguishable from an ordinary release.                                                                                                                                                                                                |
| `DISPAT_COUNTER`         | `4`                  | Prerelease counter of the version being released. **Unset** on a stable release.                                                                                                                                                                                                             |
| `DISPAT_OLD_COUNTER`     | `3`                  | Prerelease counter of the previous release. **Unset** when the previous release was stable.                                                                                                                                                                                                  |
| `DISPAT_IS_PRERELEASE`   | `true`               | `true` when `DISPAT_NEW_VERSION` carries a prerelease component. Handy for choosing a dist-tag.                                                                                                                                                                                              |
| `DISPAT_TAG`             | `core@v1.3.0-beta4`  | Tag that will be created on success: name + version + channel + counter, rendered with the space's `tagFormat`.                                                                                                                                                                              |
| `DISPAT_SEMVER_TAG`      | `core@1.3.0-beta.4`  | The same name + version + channel + counter under the normative `{name}@{version}` SemVer format, whatever `tagFormat` encodes — the spelling a script can rely on across spaces.                                                                                                            |
| `DISPAT_STAGE`           | `build`              | `version`, `build`, `publish` or `announce` for a stage script; the hook's name (`beforeBuild`, `postPublish`, `postAll`, …) for a hook; `login` for the login; `run:<name>` for a [run script](#runscripts-and-dispat-run).                                                                 |
| `DISPAT_OUTPUT`          | *(a temp file path)* | Where the script appends `NAME=value` (or `DISPAT_OUTPUT_NAME=value` — the same output) lines to [export outputs](#script-outputs) for everything that runs after it.                                                                                                                        |
| `DISPAT_OUTPUT_<NAME>`   | *(exported value)*   | One variable per accumulated [script output](#script-outputs); `DISPAT_OUTPUTS` lists the exported names (set even when empty).                                                                                                                                                              |
| `DISPAT_OUTPUT_SOURCE_<NAME>` | `core:build`    | The script that exported (or last re-exported) `<NAME>`: `<package>:<stage>`, or `<space>:login` for a login export.                                                                                                                                                                        |
| `DISPAT_EXPORT_GITHUB`   | `/pkg/dist/app.tgz`  | Set once a script [exported it](#script-outputs): the opt-in for the package's GitHub release, its value the asset list. Travels under its full name and stays out of `DISPAT_OUTPUTS`.                                                                                                      |

`DISPAT_OLD_VERSION` and `DISPAT_STABLE_BASELINE` differ only on a prerelease train: a package on `1.3.0-beta.1` whose
last stable release was `1.2.3` reports both, because the first is what it shipped and the second is what the next
version is computed from.

The counters are left **unset** — not empty — when there is nothing to report, so a shell's `${DISPAT_COUNTER+x}`
distinguishes "a stable release" from "a prerelease whose counter happens to be empty text", which an empty string
cannot. An exact `Release-As` may carry more than the bare number — `2.0.0-rc.1.hotfix` reports a counter of
`1.hotfix` — the counter is everything after the channel.

### Workspace data

Every stage additionally receives two per-package listings, readable from any shell without a parser. The version stage
is where manifests are reconciled, but a build baking versions into artefacts and a publish choosing dist-tags read the
same state, and identical environments keep a script movable between stages.

The **workspace listing** covers **every** workspace package with the version it will carry at the end of the run: its
planned version where it is releasing, its baseline otherwise.

```sh
DISPAT_WORKSPACE_PACKAGES="CORE UTILS"        # keys in plan order — for k in $DISPAT_WORKSPACE_PACKAGES
DISPAT_WORKSPACE_CORE_NAME="core"             # the raw package name
DISPAT_WORKSPACE_CORE_VERSION="1.3.0"
DISPAT_WORKSPACE_CORE_CHANNEL="stable"
DISPAT_WORKSPACE_CORE_RELEASING="true"
```

The breadth matters. dispat has no manifest model — reconciling declared dependency ranges is the version script's job —
and a correct reconciliation cannot be restricted to "released in the same run": a dependency may have been published by
an *earlier* run whose dependent leg failed, which is exactly the catch-up case. `_RELEASING=false` with a version newer
than the range you declared is that situation. Reconciling against every workspace dependency closes it, and is a no-op
whenever the narrow rule would already have been right.

The **updated-provider listing** covers the providers this package was bumped for:

```sh
DISPAT_UPDATED_PACKAGES="CORE"                # empty (not unset) when nothing was updated
DISPAT_UPDATED_CORE_NAME="core"
DISPAT_UPDATED_CORE_SPACE="libs"
DISPAT_UPDATED_CORE_OLD_VERSION="1.2.3"
DISPAT_UPDATED_CORE_NEW_VERSION="1.3.0"
DISPAT_UPDATED_CORE_CHANNEL="stable"
```

Providers that failed or were skipped are filtered out (their versions were never released), and the listing is resolved
per stage — a provider can fail between this package's build and its publish, and each stage sees the truth of its own
moment. If no successfully updated provider remains for a package bumped only by providers — it proceeds on its own
commits — the *version* script specifically is not executed at all: there is nothing to sync manifests to.

The `<KEY>` is the package name uppercased with everything outside `[A-Z0-9]` replaced by `_` — `@acme/ui` becomes
`_ACME_UI` — because a package name may contain bytes a variable name cannot. The raw name always travels in the
`_NAME` field; a lookup by name is `for k in $DISPAT_WORKSPACE_PACKAGES`, compare `_NAME`, read the fields. Should two
names sanitise to the same key (`core-utils` / `core.utils`), the first in plan order keeps it and the loser is omitted
from the listings with a warning — rename one of the pair if you hit this.

A package released on `stable` whose dependency currently carries a prerelease version is the one case no range can make
honest; the remedy is to graduate the provider too, or not to graduate the consumer yet.

### Release notes data

Every stage and hook of a package also receives its release notes, grouped exactly as the changelog file and the GitHub
release group their sections — units bumping major are breaking changes, minor are features, patch are fixes:

```sh
DISPAT_BREAKING_CHANGES="drop the old API"    # one headline per line
DISPAT_FEATURES="add streaming
add retries"
DISPAT_FIXES="close a leak"
```

Entries are the unit descriptions, newline-separated, in history order; a group with no entries is empty text (set, not
unset), so a line-wise loop iterates zero times. Bodies are omitted — they are multiline prose that would destroy the
line-per-entry contract — and stay in the changelog and the GitHub release. The dependencies section travels the same
way:

```sh
DISPAT_DEPENDENCIES="core: 1.2.3 -> 1.3.0"    # one "name: old -> new" line per live provider update
```

matching the changelog's rendering (`From` equals `To` on a catch-up, whose provider version is already out); the
`DISPAT_UPDATED_*` listing carries the same data field by field for scripts that want it addressable. The
[`run.announce`](#runannounce) stage is the natural consumer, but like every listing the variables reach every stage,
keeping scripts movable.

### Script outputs

Every per-package script and hook — the stages, their hooks, the announce frame, `onFail`/`onSkip`, the space's
`login` — receives `DISPAT_OUTPUT`: the path of a file it may append `NAME=value` lines to, `GITHUB_OUTPUT`-style, to
export values for everything that runs after it. The name may be written bare or already carrying the
`DISPAT_OUTPUT_` prefix — both spellings address the same output:

```sh
echo "DISPAT_OUTPUT_IMAGE_DIGEST=$(docker inspect --format='{{index .RepoDigests 0}}' img)" >> "$DISPAT_OUTPUT"
echo "IMAGE_DIGEST=..." >> "$DISPAT_OUTPUT"     # the same output, bare spelling
echo "DISPAT_EXPORT_GITHUB=$PWD/dist/app.tgz $PWD/dist/SHA256SUMS" >> "$DISPAT_OUTPUT"
```

Outputs accumulate across the package's pipeline into one store: every later script and hook of the package — the
outcome scripts `onFail`/`onSkip` included, so a notifier can report with them — receives each export as
`DISPAT_OUTPUT_<NAME>`, plus `DISPAT_OUTPUTS` listing the exported names (space-separated; set but empty when nothing
was exported) and `DISPAT_OUTPUT_SOURCE_<NAME>` naming the script each export came from, as `<package>:<stage>`
(`core:build`, `base:run:lint`) or `<space>:login` for the login. Hooks export exactly like stage scripts: a
`beforeBuild` export reaches the build, the publish and everything after. The **login script's** exports are
space-scoped: they reach every package of the space from its publish stage (the stage that waits for the login)
onward. In [`dispat run`](#runscripts-and-dispat-run) outputs additionally carry across packages, from a provider's
run script to its consumers'. Re-exporting a name overrides its earlier value and source, like a shell re-assignment.

The name must be a valid environment variable name; other `DISPAT_`-prefixed names are reserved (an export cannot
shadow the `DISPAT_*` environment), and a malformed line fails a release-gating sequence (and only warns in a
warn-only one). A sequence that fails still surrenders whatever it exported before failing, which is how `onFail`
gets to see it.

One export is a directive to the [GitHub recorder](#github): **`DISPAT_EXPORT_GITHUB`**. A package whose scripts
exported it gets a GitHub release; a package that never exported it is skipped by the recorder. Its value is a
whitespace-separated list of absolute paths to existing files (`$PWD` inside a script resolves to the package folder,
which makes absolute paths easy), each uploaded as an asset of the release, named after the file; an empty value
creates the release with no assets. An invalid entry — a relative path, a missing file, a directory — is skipped with
a warning while the release and the sound entries go through. Unlike ordinary outputs the export travels to later
scripts under its full name, so appending is
`echo "DISPAT_EXPORT_GITHUB=$DISPAT_EXPORT_GITHUB $PWD/more.tgz" >> "$DISPAT_OUTPUT"`, and it does not appear in
`DISPAT_OUTPUTS`.

### Run outcome data

The [run-level hooks](#run-level-hooks) additionally receive the run's outcome, rendered with the same `<KEY>` scheme:

```sh
DISPAT_PUBLISHED_PACKAGES="CORE"              # keys of published packages
DISPAT_FAILED_PACKAGES="UI"                   # keys of failed packages
DISPAT_SKIPPED_PACKAGES="APP"                 # keys of packages skipped because a provider failed
DISPAT_UNPLANNED_PACKAGES="UTILS"             # keys of packages the plan did not release
                                              # (unchanged, or held by Release-As: none)
DISPAT_RESULT_CORE_NAME="core"                # one block per planned package
DISPAT_RESULT_CORE_STATUS="published"         # published / failed / skipped
DISPAT_RESULT_CORE_OLD_VERSION="1.2.3"
DISPAT_RESULT_CORE_NEW_VERSION="1.3.0"
DISPAT_RESULT_CORE_CHANNEL="stable"
DISPAT_RESULT_UI_FAILED_STAGE="build"         # failed packages only
DISPAT_RESULT_APP_BLOCKED_BY="ui"             # skipped packages only: the provider to blame
```

The four list variables are set even when empty, so a shell for-loop iterates zero times instead of reading an unset
variable; `_FAILED_STAGE` and `_BLOCKED_BY`, conversely, are **unset** when there is nothing to report. Unplanned
packages carry no `DISPAT_RESULT_*` block — their state is the workspace listing's baseline entry.

### Size

One package costs ~250 bytes across its listing variables, so a 500-package monorepo puts roughly 125 KB into each
script's environment. Each individual variable is tiny, so the ceiling is total environment size — ~2 MiB on Linux, 1
MiB on macOS — good for a few thousand packages, far beyond the size at which one dispat workspace has usually become
several.
