# Configuration file reference

One file at the monorepo root describes everything dispat does; `dispat init` writes a starter one, and
[Getting started](../getting-started.md) walks through a first configuration.

Loaded with viper: the format is inferred from the file extension, so JSON, YAML or TOML all work. With no `--config`
flag the file is **discovered**: the first of `dispat.json`, `dispat.yaml`, `dispat.yml`, `dispat.toml` that exists in
the root is used (the names [`dispat init`](../cli.md) writes under its formats), and when none exists the run fails
with an error naming every candidate tried. An explicitly passed `--config` is used as-is, with no fallback, so a typo
there fails loudly instead of silently loading a different file. Unknown keys are rejected (typo protection). Viper
matches keys case-insensitively and lowercases map keys, so script and space names are effectively case-insensitive.

This page covers the top level; the larger objects have their own pages:

| Page                                  | Covers                                                                                         |
|---------------------------------------|------------------------------------------------------------------------------------------------|
| [Spaces](./spaces.md)                 | Space options, stages and hooks, login, announce, outcome scripts, `versioning`, `runScripts`. |
| [Tags and baselines](./versions.md)   | `tagFormat`, `initials`.                                                                       |
| [Release records](./records.md)       | `changelog`, `github`, `commit`, the shared entry format options.                              |
| [Commit parsing options](./parser.md) | `commitErrors`, `nonPackageScopes`, `parser`.                                                  |

Related references: the [CLI](../cli.md), the [commit message format](../commits.md) and the
[script environment variables](../environment.md). Annotated full examples:
[`dispat.example.json`](../../dispat.example.json), [`dispat.example.yaml`](../../dispat.example.yaml).

## Top level

| Key                | Type                           | Required  | Description                                                                                                                      |
|--------------------|--------------------------------|-----------|----------------------------------------------------------------------------------------------------------------------------------|
| `scripts`          | map name → shell command       | no        | Named shell commands, like package.json scripts. Referenced by spaces.                                                           |
| `spaces`           | map name → space               | yes (≥ 1) | Package groups sharing build/publish behaviour; see [Spaces](./spaces.md).                                                       |
| `dependencies`     | list of `{consumer, provider, kind, keep}` | no  | Package-level consumer → provider relations. Both must exist; self-dependencies and cycles are rejected; duplicates are ignored. The optional `kind` names the manifest field the edge stands for: `dependencies` (default), `devDependencies`, `peerDependencies` or `optionalDependencies`; propagation follows or ignores the edge according to `parser.propagation.kinds` (default: every kind except `devDependencies`). `keep: true` marks an edge [`dispat compute`](../cli.md#commands) must never suggest removing: a deliberate relation no manifest declares (a Docker chain); the planner treats kept edges like any other. |
| `concurrency`      | int or `[int, int]`            | no        | One value for both stages, or `[build, publish]`. `0` (or omitted) means number of CPUs. More than two values is an error.       |
| `logLevel`         | string                         | no        | Minimum log level: `trace`, `debug`, `info` (default), `warn` or `error`.                                                        |
| `logFormat`        | string                         | no        | Logger output: `pretty` (default; colored console output) or `json` (machine-readable lines for CI ingestion).                   |
| `tagFormat`        | string                         | no        | Release tag template, overridable per space. Default `{name}@{version}`; see [`tagFormat`](./versions.md#tagformat).             |
| `commitErrors`     | string                         | no        | What an error in a commit message does to the run: `warn` (default) or `error`; see [`commitErrors`](./parser.md#commiterrors).  |
| `nonPackageScopes` | array of strings               | no        | Scope names that are deliberately not packages. Default `["release"]`; see [`nonPackageScopes`](./parser.md#nonpackagescopes).   |
| `changelog`        | object                         | no        | Per-package changelog file options; see [`changelog`](./records.md#changelog).                                                   |
| `github`           | object                         | no        | GitHub release options; see [`github`](./records.md#github).                                                                     |
| `initials`         | map package → version          | no        | Baseline versions used when a package's latest tag is missing or unparseable; see [`initials`](./versions.md#initials).          |
| `commit`           | object                         | no        | End-of-run release commit, tagging and push; disabled by default. See [`commit`](./records.md#commit).                           |
| `shell`            | array of strings               | no        | Command prefix scripts are appended to, e.g. `["bash", "-c"]` or `["cmd", "/C"]`. Default `["/bin/sh", "-c"]`.                   |
| `run`              | object                         | no        | The run-level hooks (`beforeAll` ... `afterPush`), keyed by hook name; see [Run-level hooks](#run-level-hooks).                  |
| `parser`           | object                         | no        | Commit-message parser options; see [`parser`](./parser.md#parser). Everything unset keeps the specification default.             |

## Script sequences

`scripts` defines named commands; the top-level `run` object and each space's `flow` object say **what runs when**,
referencing those names. Every entry of either object accepts a single script name or an array of names executed
**sequentially, in order**; a scalar is simply a one-element sequence. How a failure inside a sequence behaves depends
on what the sequence gates:

- **Release-gating scripts** (the stage scripts, the login, every hook up to `beforePublish`, and the run-level
  `beforeAll`) are fail-fast: the first failing command stops the sequence and fails the package's release, or, for the
  run-level `beforeAll`, the whole run.
- **Warn-only scripts** (`postPublish`, the whole announce frame of `beforeAnnounce`, `announce` and `postAnnounce`, the
  outcome scripts `onFail` / `onSkip`, and every other run-level hook) never fail anything: a failing command is logged
  as a warning and **the remaining commands of the sequence still run**. These hooks observe work that has already
  happened, so stopping the sequence could not undo it.

## Run-level hooks

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
| `run.postAll`      | Once, after the whole task graph finishes, even when nothing released.            |
| `run.beforeCommit` | Before the release commit. `commit` mode only, and only when something published. |
| `run.afterCommit`  | After the release commit succeeded.                                               |
| `run.postCommit`   | After the release commit **and** the tags.                                        |
| `run.beforePush`   | Before the push. `commit.push` mode only.                                         |
| `run.afterPush`    | After the push succeeded.                                                         |

`run.beforeAll` is the one **gating** run hook: it fires before any release work, when failing it can still stop
everything, so it does, fail-fast, aborting the run with exit `1` before anything is built, published or tagged. Every
other run hook only **warns** on failure (a warn-only sequence: every command runs even when an earlier one failed),
because it runs after the work it observes. The "after" hooks additionally only run when the operation they bracket
succeeded: a hook observing a commit or push that never happened would be reporting a lie.

All seven receive `DISPAT_STAGE` naming the hook and the [workspace listing](../environment.md#workspace-data);
`run.postAll` and everything after it additionally receive the
[run outcome listing](../environment.md#run-outcome-data) reporting which packages published, failed, were skipped or
were never planned to release (`run.beforeAll` fires before any outcome exists).
