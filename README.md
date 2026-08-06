# dispat

**dispat** releases monorepos: it detects which packages changed from conventional commits, computes their next semantic
versions (propagating bumps to dependants), and builds + publishes them in the right order — in parallel, with
changelogs, git tags and GitHub releases on the way out.

## Why one more monorepo tool?

Every major monorepo tool can topologically sort a dependency graph. Build everything in order, then publish
everything — or publish only what changed, sequentially. That model works right up until reality asks two questions:

1. **What happens when an error hits in the middle?** Half the packages are published, half are not. Most tools either
   abort the whole run or plough on and leave you to reconstruct what shipped and what didn't. Re-running tends to
   re-release things that are already out — or you end up writing recovery scripts by hand.
2. **What if a consumer can only be *built* after its provider is *published*?** A Node package can be built before its
   consumers publish, but a Docker image is often buildable only by pulling its base image from a registry — the
   provider must already be published. "Build all, then publish all" quietly assumes every ecosystem behaves like npm,
   and mixed graphs break it entirely.

Modern projects are exactly that mix: many packages, built on different infrastructure — npm next to Docker next to Go —
wired into one dependency graph. No mainstream release tool handles a complex project graph *and* its error cases
smoothly. That gap is what dispat is built for:

- **Polyglot by construction.** Packages are just folders; build/publish/version steps are any shell commands. Per-space
  `isBuildWaitingPublish` states whether a consumer's build needs the provider merely *built* (node) or already
  *published* (docker), so a four-level npm→docker chain schedules correctly out of the box.
- **Built around an error model, not a happy path.** A failure never aborts the run: the broken package's consumers are
  skipped (unless they have changes of their own) and every unaffected subgraph keeps releasing. Failed or skipped
  consumers are never lost — the next run **catches them up** automatically, at the exact version they were originally
  owed, with no state file and no double release. Runs are **self-healing**: recovery *is* re-running, not a separate
  repair procedure.

Could you wire the same thing up in a general-purpose task scheduler? With enough YAML and glue, probably. dispat
deliberately does less: it handles **release logic only** — build + publish to a registry, with versioning, tagging and
changelogs around them. That focus is what keeps it easy to configure: one file, a predefined set of script slots
(`version`, `build`, `publish`, `announce`, `login`, per-stage hooks, `onFail`/`onSkip`) that between them cover every
project release need — you fill in shell commands, dispat supplies the orchestration, ordering and failure semantics.

## Key features

- **Releases the graph, not a list** — consumer → provider relations, topological ordering, parallel execution of
  independent packages with separate build/publish concurrency budgets, deterministic everywhere.
- **Blast radius written in the commit** — `feat(core):` releases `core` alone; `feat(core)^:` reaches direct consumers,
  `^^` the transitive closure, `+N` exactly N edges. Nothing is released because a tool guessed.
- **Failures don't stop the world** — a broken package skips only the consumers that truly depend on it; the rest of the
  graph keeps releasing, and CI still gets a non-zero exit.
- **Self-healing runs** — consumers that missed a run are caught up on the next one, at the version they were planned at
  originally. No orphans, no double releases, no state files: re-running is always safe.
- **Prerelease trains** — `@beta` starts a line, `^@beta++1` takes consumers along, `@beta>stable` graduates the train.
  Channels are derived from tags, so trains survive a fresh clone with zero state.
- **Per-space versioning modes** — `independent` (the default), `fixed` (one shared version for the whole space: any
  change releases every member, one prerelease train, riders get a "no changes" changelog entry) or `fixedSparse`
  (the shared version, but unchanged packages stay at their previous versions until they change).
- **Run scripts over the plan** — `dispat run lint` (or just `dispat lint`) executes a space-defined shell command
  inside each *changed* package, honouring the dependency graph with the build concurrency budget, with the same
  `DISPAT_*` environment the release stages get, releasing nothing; `--on-error` picks whether a failure skips the
  failed package's dependents or lets them run.
- **Script outputs & release assets** — every script and hook can export values `GITHUB_OUTPUT`-style through
  `$DISPAT_OUTPUT`; they reach all later scripts of the package (onFail/onSkip included) as `DISPAT_OUTPUT_*` — and
  in `dispat run` they carry across packages, provider to consumer. The special `GITHUB_ATTACHMENTS` output
  (`echo "GITHUB_ATTACHMENTS=$PWD/dist/app.tgz" >> "$DISPAT_OUTPUT"`) uploads files as GitHub release assets.
- **Polyglot & infra-agnostic** — any language, any registry: scripts are shell commands fed context via `DISPAT_*`
  env vars, and versions live purely in git tags. No version files, no lockstep, no framework buy-in.
- **One config file** — spaces, dependencies, scripts, hooks, tag formats, concurrency, changelog/GitHub behavior in a
  single `dispat.json` (YAML/TOML work too).
- **Dry-run first** — `dispat status` prints the whole plan — versions, channels, diagnostics — without touching
  anything. Run it on every pull request.
- **Release records built in** — per-package changelogs, annotated tags, GitHub releases, optional single release
  commit + push; all customisable or disableable.
- **Safe by design** — upfront git/GitHub credential verification, optional rollback of half-finished packages
  (`revertOnFail`), and never a publish against an unpublished dependency version.
- **A single static Go binary built for scale** — only runtime dependency is `git`; O ((V+E) log V) planning, a
  lock-free scheduling hot path, a regex-free single-pass commit parser, and exactly one bounded `git tag`/`git log`
  query pair per package instead of full-history walks.

## Inspiration

dispat stands on the shoulders of two things:

- **[Lerna](https://lerna.js.org/)** — the original monorepo release manager, which proved that versioning and
  publishing many packages from one repository can be a single command. dispat takes that idea beyond JavaScript and
  rebuilds it around an explicit dependency graph and an explicit error model.
- **[Conventional Commits](https://www.conventionalcommits.org/)** — commit messages as machine-readable release intent.
  dispat's parser, [`pkg/ccme`](./pkg/ccme), implements a strict superset of Conventional Commits 1.0.0 that adds the
  monorepo dimension: scopes as packages, propagation depth, prerelease channels.

## Documentation

| Document                                                    | Contents                                                                |
|-------------------------------------------------------------|-------------------------------------------------------------------------|
| [Getting started](./services/cli/docs/getting-started.md)   | Install, first config, commands, CI setup.                              |
| [Configuration & CLI](./services/cli/docs/configuration.md) | Every config option, CLI flag, script environment variable, exit codes. |
| [Architecture](./services/cli/docs/architecture.md)         | Modules, algorithms, execution model, design decisions, testing.        |
| [Integration tests](./tests/integration/README.md)          | The black-box suite: setup, running, coverage; links the test plan.     |

## Versioning flow

Versions live exclusively in annotated git tags — `package@MAJOR.MINOR.PATCH` by default, or whatever the space's
[`tagFormat`](./services/cli/docs/configuration.md#tagformat) says. For each package the planner resolves two tags:

- **baseline** — the highest tag by semver precedence, prereleases included. This is what the package last published,
  and the channel it is on is read from it (`1.5.0-beta.3` → `beta`, `1.4.2` → `stable`).
- **stable baseline** — the highest tag with *no* prerelease component. This is where versions are computed from, and it
  is what the *pending window* is measured from: every commit from that tag to `HEAD`.

The two coincide for a package on the stable channel. They differ on a prerelease train, and that is the point: the
window then spans the whole train, so a breaking change arriving mid-train moves the whole train rather than continuing
under a version that no longer describes the content.

When the newest tag exists but cannot be parsed (e.g. a stray `core@0.0.1.0`), older parseable tags are not trusted
either: the baseline comes from the optional top-level `initials` config map (package → version), defaulting to
`0.0.0`, while commits are still scanned from the unparseable tag.

Commit messages are parsed by [`pkg/ccme`](./pkg/ccme) — Conventional Commits with a monorepo extension, regex-free and
single-pass. A commit may hold several `---`-separated units, each with its own scope, directives and footers:

| Header                  | Own bump | Reaches                                     |
|-------------------------|----------|---------------------------------------------|
| `fix(core): …`          | patch    | `core` only                                 |
| `feat(core): …`         | minor    | `core` only                                 |
| `feat(core)!: …`        | major    | `core` only                                 |
| `feat(core)^: …`        | minor    | `core` + its direct consumers               |
| `feat(core)^^: …`       | minor    | `core` + every transitive consumer          |
| `feat(core)+2: …`       | minor    | `core` + consumers up to two edges away     |
| `feat(core)^minor: …`   | minor    | consumers take `minor` instead of `patch`   |
| `feat(core)^none: …`    | minor    | `core` only — the caret is explicitly inert |
| `fix(core,utils): …`    | patch    | both packages                               |
| `fix(*,-app): …`        | patch    | every package except `app`                  |
| `fix: …` (no scope)     | patch    | the packages owning the commit's files      |
| `cancel(core): …`       | —        | discards `core`'s unreleased metadata       |
| `release(core)@beta: …` | —        | moves `core` onto the beta line             |

Scopes must name discovered packages; `*` and `global` mean the whole workspace, a term with `*` is a glob, `.` is the
file-derived set, and a leading `-` excludes. A typo in an *include* is an error, because it would otherwise silently
drop a release; excluding a package that no longer exists is only a warning.

**Propagation is opt-in.** A plain `feat(core): …` releases `core` and nothing else. Reach is stated per commit with
`^` (one edge), `^^` (all edges) or `+N`, and the bump dependants take defaults to `patch` — `^minor` or a
`Propagate:` footer changes it, `^none` disables it. `Propagate-Scope` intersects the reached set with a scope-set, so
an internal app can be kept out of a workspace-wide change. Bumps merge by `max()`: two changed providers still produce
one patch, and a consumer's own `feat` beats an incoming `patch`. A package never propagates onward from a bump it
received — the originating commit's own depth is the only control, so blast radius is readable from the message.

**Prereleases and channels.** A package's channel is derived from its baseline tag alone — no side file, no config.
`@beta` on a unit puts its packages on the beta line; `@@`/`++N` propagate a channel to dependants; `@beta>stable` is a
transition that graduates whatever still matches. A stable consumer is *not* dragged into a release by a provider's
prerelease (it could not resolve it anyway) — `feat(core)^@beta` releases `core` alone and reports why; to take the
consumers along, put them on the line too: `feat(core)^@beta++1`. Trains converge on their own: once a package is on
`beta`, a directive saying `beta` proposes nothing.

**Space versioning modes.** A space may declare how its packages' versions relate:
[`versioning`](./services/cli/docs/configuration.md#versioning) is `independent` (default — everything above),
`fixed` or `fixedSparse`. Under `fixed` the space versions as one package: a change to any member releases every
member at one shared next version (computed over the space's highest baseline with the max bump), the space runs a
single prerelease train, an exact `Release-As` on one member pins the space, and a member released with nothing of its
own gets one "no changes — version bump" changelog entry, labelled `W210` in the plan. A member left behind (a failed
ride) is re-aligned to the space's published version on the next run. `fixedSparse` computes the same shared version
but releases only changed members — the rest keep their previous versions until they change, at which point they jump
to the shared version. Commit and file scopes keep exactly one job in these modes: deciding which changelog entries
(and GitHub release notes) each package receives.

**Release control.** `Release-As: none` holds a package — its bump is retained and reported, not released, until a later
`Release-As: auto` resumes it at the `max()` of everything accumulated. `Release-As: <version>` pins an exact version,
guarded against going backwards, against undershooting what the commits require, and against a major jump of more than
one. `cancel(pkg)` discards unreleased metadata for a package; it never reaches an already-published tag.

**When a provider fails or is skipped.** Failures never abort the run. A provider that failed at *any* stage (version,
build or publish) or was skipped taints its consumers: they are skipped unless they have a release reason of their own —
their own conventional commits, or another changed provider that did publish successfully. This holds in both
`isBuildWaitingPublish` modes; a consumer's publish always waits for its providers' publishes, so even a consumer that
already built (allowed by `isBuildWaitingPublish: false` while the provider was still publishing) is skipped at its
publish once the provider's publish failure is known — nothing is ever published against an unpublished provider
version. Skips cascade down the dependency chain by the same rule. A consumer that proceeds on its own reason runs its
pipeline normally, except that failed and skipped providers are filtered from the `DISPAT_UPDATED_*` variables, and if
none remain the version script is not executed at all. Spaces with `revertOnFail: true` additionally roll back all local
changes inside a failing package's folder (tracked files restored, untracked files removed), so a half-finished release
leaves no residue in the worktree.

**Catch-up: failed consumers are never lost.** Publishing is not atomic, so a run can end with some packages published
and others not. Catch-up is not a repair pass bolted on for that case — it is what the ordinary rule does when asked
against the right window. A commit propagates to a dependant exactly while *the dependant's own* window still contains
it, which does not change when the provider releases. So a consumer that missed a run is still owed its release on the
next one, with no state file, no timestamp comparison and no second traversal.

Four properties follow, and they are what make re-running safe:

- **No orphans.** A contribution survives every run until the dependant releases at a commit containing it.
- **Exactly once.** Once it does, the commit leaves its window and the contribution is gone — no double release.
- **Same version.** A package caught up on run 5 gets the version it was planned at on run 1, since its baseline never
  moved and the bump is a `max()` over the same commits.
- **No widening.** A later run's targets are always a subset of the first run's, so a failed publish can never enlarge
  what a commit releases.

Such a release is labelled a **catch-up** in the plan and reported with the origin's *published* version, because a
package appearing with no commits of its own and no releasing dependency is otherwise baffling to review. Its version
stage receives the provider's already-released version in the `DISPAT_UPDATED_*` variables, so manifests still sync. To
stop a catch-up you no longer want, act on the **consumer** — `cancel(<consumer>)` to drop it, or a hold to defer it.
Acting on the provider does nothing: its version is already public, and cancellation never reaches a published release.

**Pipeline per released package.** Up to four stages, each optional to script:

1. **version** — only when the package is bumped due to provider updates; runs exactly before the build. With
   `isBuildWaitingPublish: true` on the provider's space it waits for that provider's build *and publish*; with `false`
   it waits for the provider's *build* only.
2. **build** — the package's build command. Like every script it may export outputs by appending `NAME=value` lines
   to the file `$DISPAT_OUTPUT` points at: each value travels to every later script of the package as
   `DISPAT_OUTPUT_<NAME>`, and the `GITHUB_ATTACHMENTS` output (absolute file paths) becomes the GitHub release's
   assets.
3. **publish** — waits for the package's own build and always for its providers' publishes. A space with a
   `run.login` authenticates **once per space** before its first publish (every other publish of the space waits for it;
   a login failure fails them all). On success: release recorders run (changelog file, GitHub release), then the
   annotated tag is created (pushing is left to CI by default).
4. **announce** — after the publish frame, for pushing the release out to update channels (a Slack message, a webhook, a
   docs feed). It gets the release notes as `DISPAT_BREAKING_CHANGES` / `DISPAT_FEATURES` / `DISPAT_FIXES` — the same
   grouped data the changelog and GitHub release render — and the whole frame (its `run.beforeAnnounce` /
   `run.postAnnounce` hooks included) only warns on failure: the release is already out.

Every script option accepts a single script name or an array of names run sequentially; a failing command in a
release-gating sequence stops it and fails the package, while warn-only sequences keep running and only log. The stages
can also be bracketed with per-space hooks — `run.beforeAll`, `run.beforeVersion`/`run.postVersion`,
`run.beforeBuild`/`run.postBuild`, `run.beforePublish` all *gate* the release (their failure fails the package), while
`run.postPublish` only warns since the release is already out. Run-level hooks observe the whole run: a gating
`run.beforeAll` runs once before the task graph (its failure aborts the run), and the warn-only `run.postAll` plus the
commit/push hooks below run after, with the outcome exported as
`DISPAT_PUBLISHED/FAILED/SKIPPED/UNPLANNED_PACKAGES` and per-package `DISPAT_RESULT_*` variables.

Everything released is versioned and tagged, whatever its build produces — an exception there would cost convergence,
because a package whose window never advances reappears in every plan for ever. Packages held by `Release-As: none` are
the one thing that legitimately persists across runs, and they are excluded from the pipeline entirely.

Optionally the run can end with a *finalize phase* (disabled by default): the `commit` option creates one release commit
capturing all published packages' changelog and manifest changes — tags then point at that commit, GitHub releases move
to the end of the run, and `commit.push` pushes the commit and tags (`git`/GitHub access is verified up front, before
any work starts). The phase is bracketed by the warn-only run hooks `run.beforeCommit`/`run.afterCommit`,
`run.postCommit` (after commit and tags) and `run.beforePush`/`run.afterPush`.

Build and publish have independent concurrency budgets (`concurrency: [build, publish]`); version tasks share the build
budget. A stage without a configured script still runs — ordering, statuses, tags and release records are preserved — it
just executes no shell command.

Outside the release pipeline, `dispat run <name>` — or just `dispat <name>` — executes a space-defined
[`runScripts`](./services/cli/docs/configuration.md#runscripts-and-dispat-run) command inside each changed package,
honouring the dependency graph within the build concurrency budget (`--on-error` decides whether a failure skips the
dependents) — same `DISPAT_*` environment, nothing released or tagged.

## Quick start

```sh
go install github.com/yohimik/dispat@latest
dispat status   # print the graph and planned versions, change nothing
dispat          # release: run the full pipeline
```

See [./services/cli/docs/getting-started.md](./services/cli/docs/getting-started.md) for the full walkthrough, and
`dispat.example.json` /
`dispat.example.yaml` for annotated configs.

## Testing

dispat's failure semantics are its main promise, so they are tested at two independent layers — over 450 test functions
plus fuzzing across the workspace, all runnable with `go test ./...` in each module.

**Unit tests** (testify, in-memory fakes; `gitx`, `app` and `cli` against real temporary git repositories):

| Module / package                 | Statement coverage                                                                        |
|----------------------------------|-------------------------------------------------------------------------------------------|
| `pkg/ccme` (commit parser)       | **96.9%** — plus fuzz tests, allocation tests and the specification's conformance vectors |
| `pkg/models` (public config)     | 100%                                                                                      |
| `services/cli` (all packages)    | **93.4%** aggregate                                                                       |
| — `script`, `model`              | 100%                                                                                      |
| — `graph` (scheduler)            | 98.5%                                                                                     |
| — `config`                       | 97.7%                                                                                     |
| — `changelog`                    | 96.7%                                                                                     |
| — `release` (executor)           | 96.7%                                                                                     |
| — `plan` (planner)               | 94.3%                                                                                     |
| — `gitx`                         | 91.0%                                                                                     |
| — `github`                       | 90.6%                                                                                     |
| — `cli` (end-to-end, in-process) | 88.5%                                                                                     |
| — `app` (end-to-end, in-process) | 81.6% — plus everything the `cli` layer drives through it                                 |

The planner's two formulations of staleness — walking down from a provider and up from a consumer — are asserted to
agree, a cheap conformance check on the propagation rules. The full per-package test inventory is in
[Architecture → Testing](./services/cli/docs/architecture.md#testing).

**Integration tests** ([`tests/integration`](./tests/integration)) are a separate Go module that structurally *cannot*
import dispat's internals: it compiles the real binary and drives it against disposable git repositories exactly as a
user's shell would, asserting on git state, JSON log events and nanosecond-resolution execution timelines. Setup and
how to run are in the [integration tests README](./tests/integration/README.md); the current status and coverage are
in the [test results](./tests/integration/docs/test-results.md), with the claim-by-claim matrix in the
[integration test plan](./tests/integration/docs/test-plan.md).

## Planned features

- **Per-package overrides within a space** — a package will be able to override its enclosing space's configuration
  (scripts, concurrency, `revertOnFail`, changelog/GitHub behavior, …) for itself alone, so one-off exceptions no longer
  require carving a package out into its own space.
- **Computed dependency graph** — a command that analyzes packages' project files directly and derives the consumer →
  provider graph from them, so relations no longer have to be declared by hand in config; explicit overrides will be
  supported for cases the analysis can't or shouldn't infer.
- **Extendable config** — configuration will be splittable across multiple files, so large monorepos don't have to keep
  every space and package declaration in one flat file.
- **Auto versioning for a broad range of languages** — version bumps will be applied natively across package managers,
  so packages get automatic bump treatment without hand-rolled scripts.

## Projects using dispat (Real-world examples)

- [webxash3d-fwgs](https://github.com/yohimik/webxash3d-fwgs) — WebAssembly port of the Xash3D-FWGS game engine
  "real work docker depending on docker depending on npm" provider chain, four levels deep parallel builds from engine
  package to modded server image.

## License

This project is licensed under the MIT License. See the [LICENSE](./LICENSE.md) file for more information.
