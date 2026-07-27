# monorel

**monorel** releases monorepos: it detects which packages changed from conventional commits, computes their next
semantic versions (propagating bumps to dependants), and builds + publishes them in the right order — in parallel, with
changelogs, git tags and GitHub releases on the way out.

## Key features

- **Graph release orchestration** — packages declare consumer → provider relations; monorel topologically orders the
  whole pipeline, bumps dependants of changed packages automatically, skips consumers of failed packages (unless they
  have changes of their own), and keeps the rest of the graph releasing.
- **Parallel execution** — independent packages build and publish concurrently, with separate configurable concurrency
  budgets for the build and publish stages and deterministic ordering guarantees.
- **Single-file configuration** — one `monorel.json` (YAML/TOML work too) at the repo root describes everything:
  scripts, package spaces, dependencies, concurrency, changelog/GitHub/commit behavior.
- **Tool-, infra- and language-agnostic** — packages are just folders; build/publish/version steps are any shell
  commands (npm, go, cargo, docker, …) run through a configurable shell and fed context via `MONOREL_*` env vars.
  Versions live purely in git tags (`package@1.2.3`) — no version files, no lockstep, no framework buy-in.
- **Status tracking** — `monorel status` prints the full project graph with computed bumps and next versions without
  touching anything; releases end with a per-package summary (published / failed / skipped, durations, failed stage)
  and exit non-zero on failure. Logs are human-pretty locally and JSON in CI.
- **Release records built in** — per-package `CHANGELOG.md` entries and GitHub releases from the same commit data,
  optional single release commit + push, all customisable or disableable.
- **Safe by design** — upfront git/GitHub credential verification, optional per-space rollback of half-finished packages
  (`revertOnFail`), and no publishing against unpublished dependency versions, ever.
- **Built from scratch in Go for scale** — a single static binary with no runtime dependencies, written for giant
  project graphs and large commit histories: O ((V+E) log V) topological planning, a lock-free dependency-counting
  scheduler, regex-free single-pass commit and semver parsers, and one bounded `git tag`/`git log` query pair per
  package instead of full-history walks.

Documentation:

| Document                                     | Contents                                                                |
|----------------------------------------------|-------------------------------------------------------------------------|
| [Getting started](docs/getting-started.md)   | Install, first config, commands, CI setup.                              |
| [Configuration & CLI](docs/configuration.md) | Every config option, CLI flag, script environment variable, exit codes. |
| [Architecture](docs/architecture.md)         | Modules, algorithms, execution model, design decisions, testing.        |

## Versioning flow

Versions live exclusively in annotated git tags of the form `package@MAJOR.MINOR.PATCH`. For each package the planner
finds the highest such tag (compared as semver, not lexically) and scans commit subjects from that tag to `HEAD` — or
the whole history when the package was never tagged (a first release starts from `0.0.0`). When the newest tag exists
but cannot be parsed (e.g. a stray `core@0.0.1-0.0.0`), older parseable tags are not trusted either: the baseline comes
from the optional top-level `initials` config map (package → version), defaulting to `0.0.0`, while commits are still
scanned from the unparseable tag.

Commit subjects are classified by a minimal, regex-free conventional-commits parser; the scope must equal the package
name:

| Subject                                                                     | Own bump |
|-----------------------------------------------------------------------------|----------|
| `fix(pkg): …`                                                               | patch    |
| `feat(pkg): …`                                                              | minor    |
| `BREAKING CHANGE(pkg): …` / `BREAKING-CHANGE(pkg): …` / any `type(pkg)!: …` | major    |
| anything else                                                               | none     |

**Propagation.** A consumer of one or more changed providers gets a single patch bump, regardless of how many providers
changed. If the consumer's own commits demand more (minor/major), the higher bump wins. Propagation runs in topological
order, so transitive chains settle in one pass. A provider's breaking change still propagates as a patch by default —
the consumer's own "support the new major" commits raise its bump on their own.

**When a provider fails or is skipped.** Failures never abort the run. A provider that failed at *any* stage (version,
build or publish) or was skipped taints its consumers: they are skipped unless they have a release reason of their own —
their own conventional commits, or another changed provider that did publish successfully. This holds in both
`isBuildWaitingPublish` modes; a consumer's publish always waits for its providers' publishes, so even a consumer that
already built (allowed by `isBuildWaitingPublish: false` while the provider was still publishing) is skipped at its
publish once the provider's publish failure is known — nothing is ever published against an unpublished provider
version. Skips cascade down the dependency chain by the same rule. A consumer that proceeds on its own reason runs its
pipeline normally, except that failed and skipped providers are filtered from `MONOREL_UPDATED_PROVIDERS`, and if none
remain the version script is not executed at all. Spaces with `revertOnFail: true` additionally roll back all local
changes inside a failing package's folder (tracked files restored, untracked files removed), so a half-finished release
leaves no residue in the worktree.

**Pipeline per changed package.** Up to three stages, each optional to script:

1. **version** — only when the package is bumped due to provider updates; runs exactly before the build. With
   `isBuildWaitingPublish: true` on the provider's space it waits for that provider's build *and publish*; with `false`
   it waits for the provider's *build* only.
2. **build** — the package's build command.
3. **publish** — waits for the package's own build and always for its providers' publishes. On success: release
   recorders run (changelog file, GitHub release), then the annotated tag `package@version` is created (pushing is left
   to CI by default).

Optionally the run can end with a *finalize phase* (disabled by default): the `commit` option creates one release commit
capturing all published packages' changelog and manifest changes — tags then point at that commit, GitHub releases move
to the end of the run, and `commit.push` pushes the commit and tags (`git`/GitHub access is verified up front, before
any work starts).

Build and publish have independent concurrency budgets (`concurrency: [build, publish]`); version tasks share the build
budget. A stage without a configured script still runs — ordering, statuses, tags and release records are preserved — it
just executes no shell command.

## Quick start

```sh
go build -o monorel .
./monorel status   # print the graph and planned versions, change nothing
./monorel          # release: run the full pipeline
```

See [docs/getting-started.md](docs/getting-started.md) for the full walkthrough, and `monorel.example.json` /
`monorel.example.yaml` for annotated configs.
